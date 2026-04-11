// spectral-agent is a standalone agent process that registers with a Spectral
// Cloud server, sends periodic heartbeats, and polls for jobs to execute.
//
// Usage:
//
//	spectral-agent -id my-agent -capabilities echo,hash,http-fetch \
//	               -server http://localhost:8080 -api-key tenant-key
//
// Environment variables override flags:
//
//	SPECTRAL_SERVER, SPECTRAL_TENANT, SPECTRAL_AGENT_ID, SPECTRAL_API_KEY,
//	SPECTRAL_CAPABILITIES, SPECTRAL_TAGS (key=value,key=value)
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"
	"unicode"
)

// ── config ────────────────────────────────────────────────────────────────────

type config struct {
	serverURL    string
	tenant       string
	agentID      string
	apiKey       string
	capabilities []string
	tags         map[string]string
	addr         string
	pollInterval time.Duration
	hbInterval   time.Duration
	ttl          int
	concurrency  int
}

func loadConfig() config {
	srv := flag.String("server", "http://localhost:8080", "Spectral Cloud server URL")
	tenant := flag.String("tenant", "default", "Tenant ID")
	id := flag.String("id", "", "Agent ID (required)")
	apiKey := flag.String("api-key", "", "API key or tenant-scoped key for Spectral Cloud")
	caps := flag.String("capabilities", "echo", "Comma-separated capability list")
	addr := flag.String("addr", "", "Agent's reachable address (host:port)")
	tags := flag.String("tags", "", "Comma-separated key=value tags")
	poll := flag.Duration("poll", 2*time.Second, "Job poll interval")
	hb := flag.Duration("heartbeat", 15*time.Second, "Heartbeat interval")
	ttl := flag.Int("ttl", 60, "Agent TTL seconds (refreshed by heartbeat)")
	conc := flag.Int("concurrency", 4, "Max concurrent jobs")
	flag.Parse()

	cfg := config{
		serverURL:    envOr("SPECTRAL_SERVER", *srv),
		tenant:       envOr("SPECTRAL_TENANT", *tenant),
		agentID:      envOr("SPECTRAL_AGENT_ID", *id),
		apiKey:       envOr("SPECTRAL_API_KEY", *apiKey),
		addr:         *addr,
		pollInterval: *poll,
		hbInterval:   *hb,
		ttl:          *ttl,
		concurrency:  *conc,
	}

	rawCaps := envOr("SPECTRAL_CAPABILITIES", *caps)
	for _, c := range strings.Split(rawCaps, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			cfg.capabilities = append(cfg.capabilities, c)
		}
	}

	cfg.tags = make(map[string]string)
	rawTags := envOr("SPECTRAL_TAGS", *tags)
	for _, kv := range strings.Split(rawTags, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			cfg.tags[parts[0]] = parts[1]
		}
	}

	if cfg.agentID == "" {
		hostname, _ := os.Hostname()
		cfg.agentID = hostname + "-agent"
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

type apiClient struct {
	base   string
	tenant string
	apiKey string
	http   *http.Client
}

func newClient(base, tenant, apiKey string) *apiClient {
	return &apiClient{
		base:   strings.TrimRight(base, "/"),
		tenant: tenant,
		apiKey: strings.TrimSpace(apiKey),
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *apiClient) do(method, path string, body any, out any) (int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, bodyReader)
	if err != nil {
		return 0, err
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.ContentLength != 0 {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

func (c *apiClient) post(path string, body, out any) (int, error) {
	return c.do("POST", path, body, out)
}
func (c *apiClient) get(path string, out any) (int, error) {
	return c.do("GET", path, nil, out)
}
func (c *apiClient) patch(path string, body any) (int, error) {
	code, err := c.do("PATCH", path, body, nil)
	return code, err
}

// ── registration & heartbeat ──────────────────────────────────────────────────

type registerReq struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	Addr         string            `json:"addr,omitempty"`
	Status       string            `json:"status"`
	Tags         map[string]string `json:"tags,omitempty"`
	Capabilities []string          `json:"capabilities"`
	TTLSeconds   int               `json:"ttl_seconds"`
}

func register(ctx context.Context, api *apiClient, cfg config) error {
	req := registerReq{
		ID:           cfg.agentID,
		TenantID:     cfg.tenant,
		Addr:         cfg.addr,
		Status:       "healthy",
		Tags:         cfg.tags,
		Capabilities: cfg.capabilities,
		TTLSeconds:   cfg.ttl,
	}
	var resp map[string]any
	code, err := api.post("/agents/register", req, &resp)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if code != 201 {
		return fmt.Errorf("register: unexpected status %d", code)
	}
	return nil
}

func heartbeatLoop(ctx context.Context, api *apiClient, cfg config) {
	tick := time.NewTicker(cfg.hbInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			path := fmt.Sprintf("/agents/heartbeat?id=%s&ttl_seconds=%d", cfg.agentID, cfg.ttl)
			code, err := api.post(path, nil, nil)
			if err != nil || (code != 204 && code != 200) {
				log.Printf("[heartbeat] failed (code=%d err=%v) — re-registering", code, err)
				if rerr := register(ctx, api, cfg); rerr != nil {
					log.Printf("[heartbeat] re-register failed: %v", rerr)
				}
			} else {
				log.Printf("[heartbeat] ok")
			}
		}
	}
}

// ── job types ─────────────────────────────────────────────────────────────────

type job struct {
	ID         string         `json:"id"`
	Tenant     string         `json:"tenant"`
	AgentID    string         `json:"agent_id"`
	Capability string         `json:"capability"`
	Payload    map[string]any `json:"payload"`
	Priority   int            `json:"priority"`
	Status     string         `json:"status"`
}

type jobUpdate struct {
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ── capability handlers ───────────────────────────────────────────────────────

// handler executes a job and returns (result, errMsg).
type handler func(payload map[string]any) (string, string)

// capabilities is populated by init() so that the pipeline handler can
// reference the map without a circular initialisation cycle.
var capabilities map[string]handler

func init() {
	capabilities = map[string]handler{
	// echo — reflect payload back as JSON
	"echo": func(p map[string]any) (string, string) {
		b, _ := json.Marshal(p)
		return string(b), ""
	},

	// hash — SHA-256 of payload["data"] string
	"hash": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		sum := sha256.Sum256([]byte(data))
		return "sha256:" + hex.EncodeToString(sum[:]), ""
	},

	// reverse — reverse payload["text"]
	"reverse": func(p map[string]any) (string, string) {
		text, _ := p["text"].(string)
		if text == "" {
			return "", "payload.text is required"
		}
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes), ""
	},

	// count-words — count words in payload["text"]
	"count-words": func(p map[string]any) (string, string) {
		text, _ := p["text"].(string)
		if text == "" {
			return "0", ""
		}
		words := strings.FieldsFunc(text, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsPunct(r)
		})
		chars := len([]rune(text))
		lines := strings.Count(text, "\n") + 1
		return fmt.Sprintf(`{"words":%d,"chars":%d,"lines":%d}`, len(words), chars, lines), ""
	},

	// http-fetch — fetch payload["url"] and return first 512 bytes of body
	"http-fetch": func(p map[string]any) (string, string) {
		rawURL, _ := p["url"].(string)
		if rawURL == "" {
			return "", "payload.url is required"
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return "", fmt.Sprintf("invalid url: %v", err)
		}
		if err := validateFetchURL(context.Background(), parsed); err != nil {
			return "", err.Error()
		}
		client := newFetchHTTPClient()
		resp, err := client.Get(parsed.String())
		if err != nil {
			return "", fmt.Sprintf("fetch error: %v", err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 512)
		n, _ := io.ReadAtLeast(resp.Body, buf, 1)
		snippet := strings.TrimSpace(string(buf[:n]))
		return fmt.Sprintf(`{"status":%d,"body_preview":%q}`, resp.StatusCode, snippet), ""
	},

	// ping — TCP dial to payload["host"] (host:port), report latency
	"ping": func(p map[string]any) (string, string) {
		host, _ := p["host"].(string)
		if host == "" {
			return "", "payload.host (host:port) is required"
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", host, 4*time.Second)
		if err != nil {
			return fmt.Sprintf(`{"reachable":false,"error":%q}`, err.Error()), ""
		}
		conn.Close()
		ms := time.Since(start).Milliseconds()
		return fmt.Sprintf(`{"reachable":true,"latency_ms":%d}`, ms), ""
	},

	// math — evaluate simple arithmetic from payload["expr"]
	// Supports +, -, *, /, ^ on numbers using a recursive descent parser.
	"math": func(p map[string]any) (string, string) {
		expr, _ := p["expr"].(string)
		if expr == "" {
			return "", "payload.expr is required"
		}
		val, err := evalMath(strings.TrimSpace(expr))
		if err != nil {
			return "", err.Error()
		}
		return strconv.FormatFloat(val, 'f', -1, 64), ""
	},

	// summarize — extract top N sentences by word frequency from payload["text"]
	"summarize": func(p map[string]any) (string, string) {
		text, _ := p["text"].(string)
		if text == "" {
			return "", "payload.text is required"
		}
		nRaw, _ := p["sentences"].(float64)
		n := int(nRaw)
		if n <= 0 {
			n = 3
		}
		summary := extractiveSummarize(text, n)
		return summary, ""
	},

	// timestamp — return current time in various formats
	"timestamp": func(p map[string]any) (string, string) {
		now := time.Now().UTC()
		tz, _ := p["tz"].(string)
		if tz != "" {
			loc, err := time.LoadLocation(tz)
			if err == nil {
				now = now.In(loc)
			}
		}
		return fmt.Sprintf(`{"unix":%d,"rfc3339":%q,"human":%q}`,
			now.Unix(), now.Format(time.RFC3339), now.Format("2006-01-02 15:04:05 MST")), ""
	},

	// dns-lookup — resolve payload["host"] to IP addresses
	"dns-lookup": func(p map[string]any) (string, string) {
		host, _ := p["host"].(string)
		if host == "" {
			return "", "payload.host is required"
		}
		addrs, err := net.LookupHost(host)
		if err != nil {
			return fmt.Sprintf(`{"error":%q}`, err.Error()), ""
		}
		b, _ := json.Marshal(map[string]any{"host": host, "addrs": addrs})
		return string(b), ""
	},

	// sleep — simulate long-running work; payload["seconds"] (max 30)
	"sleep": func(p map[string]any) (string, string) {
		secs, _ := p["seconds"].(float64)
		if secs <= 0 {
			secs = 1
		}
		if secs > 30 {
			secs = 30
		}
		time.Sleep(time.Duration(secs * float64(time.Second)))
		return fmt.Sprintf("slept %.0f seconds", secs), ""
	},

	// ── data capabilities ─────────────��────────────────────────────────────

	// base64-encode / base64-decode
	"base64-encode": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		return base64.StdEncoding.EncodeToString([]byte(data)), ""
	},
	"base64-decode": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return "", fmt.Sprintf("invalid base64: %v", err)
		}
		return string(b), ""
	},

	// multi-hash — hash payload["data"] with algo md5|sha1|sha256|sha512
	"multi-hash": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		b := []byte(data)
		md5sum := md5.Sum(b)
		sha1sum := sha1.Sum(b)
		sha256sum := sha256.Sum256(b)
		sha512sum := sha512.Sum512(b)
		out, _ := json.Marshal(map[string]string{
			"md5":    hex.EncodeToString(md5sum[:]),
			"sha1":   hex.EncodeToString(sha1sum[:]),
			"sha256": hex.EncodeToString(sha256sum[:]),
			"sha512": hex.EncodeToString(sha512sum[:]),
		})
		return string(out), ""
	},

	// regex-match — match payload["pattern"] against payload["text"]
	"regex-match": func(p map[string]any) (string, string) {
		pattern, _ := p["pattern"].(string)
		text, _ := p["text"].(string)
		if pattern == "" {
			return "", "payload.pattern is required"
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Sprintf("invalid regex: %v", err)
		}
		matches := re.FindAllString(text, -1)
		groups := re.FindAllStringSubmatch(text, -1)
		b, _ := json.Marshal(map[string]any{
			"matched": len(matches) > 0,
			"count":   len(matches),
			"matches": matches,
			"groups":  groups,
		})
		return string(b), ""
	},

	// generate — produce random data; type: uuid|token|password|bytes
	"generate": func(p map[string]any) (string, string) {
		genType, _ := p["type"].(string)
		if genType == "" {
			genType = "uuid"
		}
		countF, _ := p["count"].(float64)
		count := int(countF)
		if count <= 0 {
			count = 1
		}
		if count > 100 {
			count = 100
		}
		results := make([]string, 0, count)
		for i := 0; i < count; i++ {
			buf := make([]byte, 16)
			rand.Read(buf)
			switch genType {
			case "uuid":
				buf[6] = (buf[6] & 0x0f) | 0x40
				buf[8] = (buf[8] & 0x3f) | 0x80
				results = append(results, fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
					buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]))
			case "token":
				results = append(results, hex.EncodeToString(buf))
			case "password":
				const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
				pw := make([]byte, 16)
				for j := range pw {
					pw[j] = charset[int(buf[j])%len(charset)]
				}
				results = append(results, string(pw))
			case "bytes":
				results = append(results, base64.StdEncoding.EncodeToString(buf))
			default:
				return "", fmt.Sprintf("unknown type %q (uuid|token|password|bytes)", genType)
			}
		}
		if count == 1 {
			return results[0], ""
		}
		b, _ := json.Marshal(results)
		return string(b), ""
	},

	// csv-parse — parse CSV from payload["data"], return stats + rows
	"csv-parse": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		r := csv.NewReader(strings.NewReader(data))
		records, err := r.ReadAll()
		if err != nil {
			return "", fmt.Sprintf("csv parse error: %v", err)
		}
		if len(records) == 0 {
			return `{"rows":0,"cols":0,"headers":[],"preview":[]}`, ""
		}
		headers := records[0]
		preview := records[1:]
		if len(preview) > 5 {
			preview = preview[:5]
		}
		b, _ := json.Marshal(map[string]any{
			"rows":    len(records) - 1,
			"cols":    len(headers),
			"headers": headers,
			"preview": preview,
		})
		return string(b), ""
	},

	// json-transform — extract a nested key from payload["data"] (dot notation)
	"json-query": func(p map[string]any) (string, string) {
		raw, _ := p["data"].(string)
		query, _ := p["query"].(string)
		if raw == "" {
			return "", "payload.data is required"
		}
		var doc any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Sprintf("invalid JSON: %v", err)
		}
		if query == "" {
			b, _ := json.MarshalIndent(doc, "", "  ")
			return string(b), ""
		}
		parts := strings.Split(query, ".")
		cur := doc
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			switch v := cur.(type) {
			case map[string]any:
				val, ok := v[part]
				if !ok {
					return "", fmt.Sprintf("key %q not found", part)
				}
				cur = val
			default:
				return "", fmt.Sprintf("cannot index into %T with key %q", cur, part)
			}
		}
		b, _ := json.Marshal(cur)
		return string(b), ""
	},

	// compress — gzip payload["data"] and return base64; or decompress
	"compress": func(p map[string]any) (string, string) {
		data, _ := p["data"].(string)
		op, _ := p["op"].(string)
		if data == "" {
			return "", "payload.data is required"
		}
		if op == "decompress" {
			raw, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return "", "invalid base64"
			}
			gr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				return "", fmt.Sprintf("gzip error: %v", err)
			}
			out, _ := io.ReadAll(gr)
			return string(out), ""
		}
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write([]byte(data))
		gw.Close()
		ratio := float64(len(data)) / float64(buf.Len())
		b, _ := json.Marshal(map[string]any{
			"compressed":    base64.StdEncoding.EncodeToString(buf.Bytes()),
			"original_size": len(data),
			"gzip_size":     buf.Len(),
			"ratio":         fmt.Sprintf("%.2fx", ratio),
		})
		return string(b), ""
	},

	// template — render a Go text/template; payload["tmpl"] + payload["data"] (JSON object)
	"template": func(p map[string]any) (string, string) {
		tmplStr, _ := p["tmpl"].(string)
		if tmplStr == "" {
			return "", "payload.tmpl is required"
		}
		t, err := template.New("t").Parse(tmplStr)
		if err != nil {
			return "", fmt.Sprintf("template parse error: %v", err)
		}
		var data any = p["data"]
		if raw, ok := p["data"].(string); ok {
			var d any
			if err := json.Unmarshal([]byte(raw), &d); err == nil {
				data = d
			}
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return "", fmt.Sprintf("template execute error: %v", err)
		}
		return buf.String(), ""
	},

	// sort-data — sort payload["items"] (array of strings or numbers)
	"sort-data": func(p map[string]any) (string, string) {
		items, ok := p["items"].([]any)
		if !ok || len(items) == 0 {
			return "", "payload.items (array) is required"
		}
		order, _ := p["order"].(string)
		// Try numeric sort first
		nums := make([]float64, len(items))
		allNum := true
		for i, v := range items {
			switch n := v.(type) {
			case float64:
				nums[i] = n
			case string:
				f, err := strconv.ParseFloat(n, 64)
				if err != nil {
					allNum = false
					break
				}
				nums[i] = f
			default:
				allNum = false
			}
		}
		if allNum {
			sort.Float64s(nums)
			if order == "desc" {
				for i, j := 0, len(nums)-1; i < j; i, j = i+1, j-1 {
					nums[i], nums[j] = nums[j], nums[i]
				}
			}
			b, _ := json.Marshal(nums)
			return string(b), ""
		}
		strs := make([]string, len(items))
		for i, v := range items {
			strs[i] = fmt.Sprint(v)
		}
		sort.Strings(strs)
		if order == "desc" {
			for i, j := 0, len(strs)-1; i < j; i, j = i+1, j-1 {
				strs[i], strs[j] = strs[j], strs[i]
			}
		}
		b, _ := json.Marshal(strs)
		return string(b), ""
	},

	// ── system capabilities ────────────────────────────────────────────────

	// sys-info — return runtime + OS info
	"sys-info": func(p map[string]any) (string, string) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		hostname, _ := os.Hostname()
		b, _ := json.Marshal(map[string]any{
			"hostname":      hostname,
			"os":            runtime.GOOS,
			"arch":          runtime.GOARCH,
			"cpus":          runtime.NumCPU(),
			"goroutines":    runtime.NumGoroutine(),
			"heap_alloc_mb": fmt.Sprintf("%.2f", float64(ms.HeapAlloc)/1024/1024),
			"heap_sys_mb":   fmt.Sprintf("%.2f", float64(ms.HeapSys)/1024/1024),
			"gc_runs":       ms.NumGC,
			"uptime_agent":  time.Now().UTC().Format(time.RFC3339),
		})
		return string(b), ""
	},

	// disk-usage — report disk usage for a path (defaults to /)
	"disk-usage": func(p map[string]any) (string, string) {
		path, _ := p["path"].(string)
		if path == "" {
			path = "/"
		}
		out, err := exec.Command("df", "-h", path).Output()
		if err != nil {
			return "", fmt.Sprintf("df error: %v", err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if len(lines) < 2 {
			return string(out), ""
		}
		// Parse df output: Filesystem Size Used Avail Use% Mounted
		fields := strings.Fields(lines[1])
		if len(fields) < 6 {
			return string(out), ""
		}
		b, _ := json.Marshal(map[string]string{
			"path":       path,
			"filesystem": fields[0],
			"size":       fields[1],
			"used":       fields[2],
			"avail":      fields[3],
			"use_pct":    fields[4],
			"mounted":    fields[5],
		})
		return string(b), ""
	},

	// process-count — count running processes
	"process-count": func(p map[string]any) (string, string) {
		out, err := exec.Command("sh", "-c", "ps -e --no-headers | wc -l").Output()
		if err != nil {
			return "", fmt.Sprintf("ps error: %v", err)
		}
		count := strings.TrimSpace(string(out))
		loadOut, _ := exec.Command("cat", "/proc/loadavg").Output()
		load := strings.TrimSpace(string(loadOut))
		b, _ := json.Marshal(map[string]string{
			"process_count": count,
			"load_avg":      load,
		})
		return string(b), ""
	},

	// file-stat — stat a path (restricted to /tmp and /home)
	"file-stat": func(p map[string]any) (string, string) {
		path, _ := p["path"].(string)
		if path == "" {
			return "", "payload.path is required"
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", "invalid path"
		}
		allowed := false
		for _, prefix := range []string{"/tmp/", "/home/", "/var/log/"} {
			if strings.HasPrefix(abs, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", "path not in allowed directories (/tmp, /home, /var/log)"
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Sprintf("stat error: %v", err)
		}
		b, _ := json.Marshal(map[string]any{
			"path":     abs,
			"name":     info.Name(),
			"size":     info.Size(),
			"mode":     info.Mode().String(),
			"modified": info.ModTime().UTC().Format(time.RFC3339),
			"is_dir":   info.IsDir(),
		})
		return string(b), ""
	},

	// ── pipeline capability ───────────────────────────────────────────���────

	// pipeline — chain multiple capability calls sequentially
	// payload: {"steps": [{"cap":"hash","payload":{"data":"..."}}, ...]}
	// output of each step is merged into next step's payload as "prev_result"
	"pipeline": func(p map[string]any) (string, string) {
		stepsRaw, ok := p["steps"].([]any)
		if !ok || len(stepsRaw) == 0 {
			return "", "payload.steps (array of {cap, payload}) is required"
		}
		prevResult := ""
		var allResults []map[string]any
		for i, stepRaw := range stepsRaw {
			step, ok := stepRaw.(map[string]any)
			if !ok {
				return "", fmt.Sprintf("step %d is not an object", i)
			}
			cap, _ := step["cap"].(string)
			if cap == "" {
				return "", fmt.Sprintf("step %d missing cap", i)
			}
			payload, _ := step["payload"].(map[string]any)
			if payload == nil {
				payload = map[string]any{}
			}
			if prevResult != "" {
				payload["prev_result"] = prevResult
			}
			h, ok := capabilities[cap]
			if !ok {
				return "", fmt.Sprintf("step %d: unknown capability %q", i, cap)
			}
			result, errMsg := h(payload)
			if errMsg != "" {
				return "", fmt.Sprintf("step %d (%s) failed: %s", i, cap, errMsg)
			}
			prevResult = result
			allResults = append(allResults, map[string]any{
				"step": i, "cap": cap, "result": result,
			})
		}
		b, _ := json.Marshal(map[string]any{
			"steps":        len(stepsRaw),
			"final_result": prevResult,
			"trace":        allResults,
		})
		return string(b), ""
	},
	// ── inference capability ──────────────────────────────────────────────────
	// inference — send payload["prompt"] to Claude and return the response text.
	// Optional payload fields: model, system, max_tokens.
	// Requires ANTHROPIC_API_KEY to be set in the agent's environment.
	"inference": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		prompt, _ := p["prompt"].(string)
		if prompt == "" {
			return "", "payload.prompt is required"
		}
		model, _ := p["model"].(string)
		system, _ := p["system"].(string)
		maxTokens := 0
		if v, ok := p["max_tokens"].(float64); ok {
			maxTokens = int(v)
		}

		// Build and send the Anthropic Messages API request inline so the
		// agent binary has no additional dependencies.
		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type reqBody struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system,omitempty"`
			Messages  []msg  `json:"messages"`
		}
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		if maxTokens <= 0 {
			maxTokens = 1024
		}
		body, _ := json.Marshal(reqBody{
			Model:     model,
			MaxTokens: maxTokens,
			System:    system,
			Messages:  []msg{{Role: "user", Content: prompt}},
		})

		req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", fmt.Sprintf("inference: build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference: http: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		var ar struct {
			Model   string `json:"model"`
			Usage   struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference: decode response: %v", err)
		}
		if ar.Error != nil {
			return "", fmt.Sprintf("inference: anthropic error (%s): %s", ar.Error.Type, ar.Error.Message)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Sprintf("inference: unexpected status %d", resp.StatusCode)
		}

		var text string
		for _, block := range ar.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		out, _ := json.Marshal(map[string]any{
			"model":         ar.Model,
			"content":       text,
			"input_tokens":  ar.Usage.InputTokens,
			"output_tokens": ar.Usage.OutputTokens,
		})
		return string(out), ""
	},

	// ── inference-batch capability ────────────────────────────────────────────
	// inference-batch — run multiple prompts concurrently against Claude.
	// Payload: {"prompts": ["p1","p2",...], "model": "...", "system": "...", "max_tokens": N}
	// Result:  {"results":[{"index":0,"content":"...","error":"..."},...]}
	"inference-batch": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		rawPrompts, _ := p["prompts"].([]any)
		if len(rawPrompts) == 0 {
			return "", "payload.prompts (array) is required"
		}
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		system, _ := p["system"].(string)
		maxTokens := 1024
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}

		type result struct {
			Index   int    `json:"index"`
			Content string `json:"content,omitempty"`
			Error   string `json:"error,omitempty"`
		}

		type inferResult struct {
			idx     int
			content string
			errMsg  string
		}

		resultCh := make(chan inferResult, len(rawPrompts))
		var wg sync.WaitGroup

		callClaude := func(idx int, prompt string) {
			defer wg.Done()
			type msg struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			type reqBody struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
				System    string `json:"system,omitempty"`
				Messages  []msg  `json:"messages"`
			}
			body, _ := json.Marshal(reqBody{
				Model: model, MaxTokens: maxTokens, System: system,
				Messages: []msg{{Role: "user", Content: prompt}},
			})
			req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
			if err != nil {
				resultCh <- inferResult{idx: idx, errMsg: err.Error()}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")

			client := &http.Client{Timeout: 120 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				resultCh <- inferResult{idx: idx, errMsg: err.Error()}
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			var ar struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Error *struct{ Message string `json:"message"` } `json:"error"`
			}
			if err := json.Unmarshal(raw, &ar); err != nil {
				resultCh <- inferResult{idx: idx, errMsg: fmt.Sprintf("decode: %v", err)}
				return
			}
			if ar.Error != nil {
				resultCh <- inferResult{idx: idx, errMsg: ar.Error.Message}
				return
			}
			var text string
			for _, block := range ar.Content {
				if block.Type == "text" {
					text += block.Text
				}
			}
			resultCh <- inferResult{idx: idx, content: text}
		}

		for i, raw := range rawPrompts {
			prompt, _ := raw.(string)
			if prompt == "" {
				continue
			}
			wg.Add(1)
			go callClaude(i, prompt)
		}
		wg.Wait()
		close(resultCh)

		results := make([]result, len(rawPrompts))
		for r := range resultCh {
			results[r.idx] = result{Index: r.idx, Content: r.content, Error: r.errMsg}
		}
		out, _ := json.Marshal(map[string]any{"results": results})
		return string(out), ""
	},

	// ── inference-code capability ─────────────────────────────────────────────
	// inference-code — code-specialised inference. Behaves exactly like
	// "inference" but ships a hardened coding system prompt by default so
	// callers don't need to craft one.
	// Payload: {"prompt":"...", "language":"go", "model":"...", "max_tokens":N}
	"inference-code": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		prompt, _ := p["prompt"].(string)
		if prompt == "" {
			return "", "payload.prompt is required"
		}
		lang, _ := p["language"].(string)
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		maxTokens := 4096
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}
		systemLines := []string{
			"You are an expert software engineer. Produce correct, idiomatic, production-ready code.",
			"Return only code and inline comments — no prose unless the user specifically asks for explanation.",
		}
		if lang != "" {
			systemLines = append(systemLines, "Preferred language: "+lang+".")
		}
		systemPrompt := strings.Join(systemLines, " ")

		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type reqBody struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system,omitempty"`
			Messages  []msg  `json:"messages"`
		}
		body, _ := json.Marshal(reqBody{
			Model: model, MaxTokens: maxTokens, System: systemPrompt,
			Messages: []msg{{Role: "user", Content: prompt}},
		})
		req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", fmt.Sprintf("inference-code: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference-code: http: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		var ar struct {
			Model   string `json:"model"`
			Usage   struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct{ Message string `json:"message"` } `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference-code: decode: %v", err)
		}
		if ar.Error != nil {
			return "", "inference-code: " + ar.Error.Message
		}
		var text string
		for _, block := range ar.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		out, _ := json.Marshal(map[string]any{
			"model":         ar.Model,
			"content":       text,
			"language":      lang,
			"input_tokens":  ar.Usage.InputTokens,
			"output_tokens": ar.Usage.OutputTokens,
		})
		return string(out), ""
	},

	// ── inference-vision capability ───────────────────────────────────────────
	// inference-vision — multimodal inference with image understanding.
	// Payload: {"prompt":"...", "image_base64":"<base64>", "media_type":"image/png",
	//           "model":"...", "max_tokens":N}
	// media_type defaults to "image/jpeg".
	"inference-vision": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		prompt, _ := p["prompt"].(string)
		if prompt == "" {
			return "", "payload.prompt is required"
		}
		imageB64, _ := p["image_base64"].(string)
		if imageB64 == "" {
			return "", "payload.image_base64 is required"
		}
		mediaType, _ := p["media_type"].(string)
		if mediaType == "" {
			mediaType = "image/jpeg"
		}
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		maxTokens := 1024
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}

		// Build a multimodal message with image + text content blocks.
		type imageSource struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		}
		type contentBlock struct {
			Type   string       `json:"type"`
			Source *imageSource `json:"source,omitempty"`
			Text   string       `json:"text,omitempty"`
		}
		type visionMsg struct {
			Role    string         `json:"role"`
			Content []contentBlock `json:"content"`
		}
		type reqBody struct {
			Model     string      `json:"model"`
			MaxTokens int         `json:"max_tokens"`
			Messages  []visionMsg `json:"messages"`
		}
		body, _ := json.Marshal(reqBody{
			Model:     model,
			MaxTokens: maxTokens,
			Messages: []visionMsg{{
				Role: "user",
				Content: []contentBlock{
					{Type: "image", Source: &imageSource{Type: "base64", MediaType: mediaType, Data: imageB64}},
					{Type: "text", Text: prompt},
				},
			}},
		})
		req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", fmt.Sprintf("inference-vision: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference-vision: http: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		var ar struct {
			Model   string `json:"model"`
			Usage   struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct{ Message string `json:"message"` } `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference-vision: decode: %v", err)
		}
		if ar.Error != nil {
			return "", "inference-vision: " + ar.Error.Message
		}
		var text string
		for _, block := range ar.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		out, _ := json.Marshal(map[string]any{
			"model":         ar.Model,
			"content":       text,
			"input_tokens":  ar.Usage.InputTokens,
			"output_tokens": ar.Usage.OutputTokens,
		})
		return string(out), ""
	},

	// ── inference-rag capability ──────────────────────────────────────────────
	// inference-rag — retrieval-augmented generation.
	// Fetches context documents from the Spectral Cloud KV store (or an inline
	// list) and prepends them to the prompt before calling Claude.
	//
	// Payload:
	//   prompt        string   — the user question (required)
	//   server        string   — control-plane base URL (default: SPECTRAL_SERVER env)
	//   api_key       string   — API key (default: SPECTRAL_API_KEY env)
	//   tenant        string   — tenant ID (default: SPECTRAL_TENANT env or "default")
	//   kv_prefix     string   — KV key prefix to fetch context from (e.g. "docs/")
	//   context_docs  []string — inline context strings (used if kv_prefix empty)
	//   model         string   — Claude model override
	//   max_tokens    int      — max tokens override
	"inference-rag": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		prompt, _ := p["prompt"].(string)
		if prompt == "" {
			return "", "payload.prompt is required"
		}
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		maxTokens := 2048
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}

		// Collect context documents.
		var contextDocs []string

		// 1. Inline docs from payload.
		if raw, ok := p["context_docs"].([]any); ok {
			for _, d := range raw {
				if s, ok := d.(string); ok && s != "" {
					contextDocs = append(contextDocs, s)
				}
			}
		}

		// 2. Fetch from KV store via control-plane API.
		kvPrefix, _ := p["kv_prefix"].(string)
		if kvPrefix != "" {
			serverURL := os.Getenv("SPECTRAL_SERVER")
			if s, ok := p["server"].(string); ok && s != "" {
				serverURL = s
			}
			if serverURL == "" {
				serverURL = "http://localhost:8080"
			}
			tenant := os.Getenv("SPECTRAL_TENANT")
			if t, ok := p["tenant"].(string); ok && t != "" {
				tenant = t
			}
			if tenant == "" {
				tenant = "default"
			}
			spectralKey := os.Getenv("SPECTRAL_API_KEY")
			if k, ok := p["api_key"].(string); ok && k != "" {
				spectralKey = k
			}

			kvURL := fmt.Sprintf("%s/kv?prefix=%s", strings.TrimRight(serverURL, "/"), url.QueryEscape(kvPrefix))
			req, _ := http.NewRequest(http.MethodGet, kvURL, nil)
			if spectralKey != "" {
				req.Header.Set("X-API-Key", spectralKey)
			}
			if tenant != "" {
				req.Header.Set("X-Tenant-ID", tenant)
			}
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				var kvResp struct {
					Entries []struct {
						Key   string `json:"key"`
						Value string `json:"value"`
					} `json:"entries"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&kvResp); err == nil {
					for _, e := range kvResp.Entries {
						contextDocs = append(contextDocs, fmt.Sprintf("[%s]\n%s", e.Key, e.Value))
					}
				}
			}
		}

		// Build the RAG prompt.
		var ragPrompt strings.Builder
		if len(contextDocs) > 0 {
			ragPrompt.WriteString("Use the following context documents to answer the question. If the answer is not in the documents, say so.\n\n")
			for i, doc := range contextDocs {
				fmt.Fprintf(&ragPrompt, "--- Document %d ---\n%s\n\n", i+1, doc)
			}
			ragPrompt.WriteString("--- Question ---\n")
		}
		ragPrompt.WriteString(prompt)

		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type reqBody struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system,omitempty"`
			Messages  []msg  `json:"messages"`
		}
		body, _ := json.Marshal(reqBody{
			Model:     model,
			MaxTokens: maxTokens,
			System:    "You are a helpful assistant that answers questions based on provided context documents.",
			Messages:  []msg{{Role: "user", Content: ragPrompt.String()}},
		})
		req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", fmt.Sprintf("inference-rag: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		httpClient := &http.Client{Timeout: 120 * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference-rag: http: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		var ar struct {
			Model   string `json:"model"`
			Usage   struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct{ Message string `json:"message"` } `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference-rag: decode: %v", err)
		}
		if ar.Error != nil {
			return "", "inference-rag: " + ar.Error.Message
		}
		var text string
		for _, block := range ar.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		out, _ := json.Marshal(map[string]any{
			"model":          ar.Model,
			"content":        text,
			"context_count":  len(contextDocs),
			"input_tokens":   ar.Usage.InputTokens,
			"output_tokens":  ar.Usage.OutputTokens,
		})
		return string(out), ""
	},

	// ── inference-classify capability ─────────────────────────────────────────
	// inference-classify — assign labels to text using Claude.
	// Payload: {"text":"...", "labels":["a","b","c"], "multi_label":false,
	//           "model":"...", "max_tokens":N}
	"inference-classify": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		text, _ := p["text"].(string)
		if text == "" {
			return "", "payload.text is required"
		}
		rawLabels, _ := p["labels"].([]any)
		if len(rawLabels) == 0 {
			return "", "payload.labels is required"
		}
		labels := make([]string, 0, len(rawLabels))
		for _, l := range rawLabels {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			}
		}
		multiLabel, _ := p["multi_label"].(bool)
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		maxTokens := 256
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}
		mode := "exactly one label"
		if multiLabel {
			mode = "one or more labels as a JSON array"
		}
		prompt := fmt.Sprintf(
			"Classify the text. Labels: %s\nReturn ONLY JSON: single-label: {\"label\":\"...\",\"confidence\":0-1}; multi-label: {\"labels\":[...],\"confidence\":{}}.\nSelect %s.\n\nText:\n%s",
			strings.Join(labels, ", "), mode, text,
		)
		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type reqBody struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system"`
			Messages  []msg  `json:"messages"`
		}
		body, _ := json.Marshal(reqBody{
			Model: model, MaxTokens: maxTokens,
			System:   "You are a text classification engine. Return only valid JSON.",
			Messages: []msg{{Role: "user", Content: prompt}},
		})
		req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference-classify: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var ar struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct{ Message string `json:"message"` } `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference-classify: decode: %v", err)
		}
		if ar.Error != nil {
			return "", "inference-classify: " + ar.Error.Message
		}
		var text2 string
		for _, b := range ar.Content {
			if b.Type == "text" {
				text2 += b.Text
			}
		}
		return text2, ""
	},

	// ── inference-translate capability ────────────────────────────────────────
	// inference-translate — translate text to a target language.
	// Payload: {"text":"...", "target_language":"French",
	//           "source_language":"auto", "model":"...", "max_tokens":N}
	"inference-translate": func(p map[string]any) (string, string) {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", "ANTHROPIC_API_KEY is not set"
		}
		text, _ := p["text"].(string)
		if text == "" {
			return "", "payload.text is required"
		}
		target, _ := p["target_language"].(string)
		if target == "" {
			return "", "payload.target_language is required"
		}
		source, _ := p["source_language"].(string)
		model, _ := p["model"].(string)
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		maxTokens := 2048
		if v, ok := p["max_tokens"].(float64); ok && int(v) > 0 {
			maxTokens = int(v)
		}
		sourceLine := ""
		if source != "" && source != "auto" {
			sourceLine = fmt.Sprintf(" from %s", source)
		}
		prompt := fmt.Sprintf("Translate the following text%s to %s. Return only the translated text.\n\n%s", sourceLine, target, text)
		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type reqBody struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system"`
			Messages  []msg  `json:"messages"`
		}
		body, _ := json.Marshal(reqBody{
			Model: model, MaxTokens: maxTokens,
			System:   "You are a professional translator. Return only the translated text.",
			Messages: []msg{{Role: "user", Content: prompt}},
		})
		req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Sprintf("inference-translate: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var ar struct {
			Model   string `json:"model"`
			Usage   struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct{ Message string `json:"message"` } `json:"error"`
		}
		if err := json.Unmarshal(raw, &ar); err != nil {
			return "", fmt.Sprintf("inference-translate: decode: %v", err)
		}
		if ar.Error != nil {
			return "", "inference-translate: " + ar.Error.Message
		}
		var translated string
		for _, b := range ar.Content {
			if b.Type == "text" {
				translated += b.Text
			}
		}
		out, _ := json.Marshal(map[string]any{
			"translation":     translated,
			"target_language": target,
			"model":           ar.Model,
			"input_tokens":    ar.Usage.InputTokens,
			"output_tokens":   ar.Usage.OutputTokens,
		})
		return string(out), ""
	},

	} // end map literal
} // end init()

// ── simple extractive summarizer ──────────────────────────────────────────────

func extractiveSummarize(text string, n int) string {
	sentences := splitSentences(text)
	if len(sentences) <= n {
		return text
	}
	// Word frequency
	freq := map[string]int{}
	for _, s := range sentences {
		for _, w := range strings.Fields(s) {
			w = strings.ToLower(strings.Trim(w, ".,!?;:\"'()[]"))
			if len(w) > 3 {
				freq[w]++
			}
		}
	}
	// Score each sentence
	type scored struct {
		text  string
		score int
		idx   int
	}
	var scores []scored
	for i, s := range sentences {
		score := 0
		for _, w := range strings.Fields(s) {
			w = strings.ToLower(strings.Trim(w, ".,!?;:\"'()[]"))
			score += freq[w]
		}
		scores = append(scores, scored{s, score, i})
	}
	// Sort by score descending, pick top n
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	top := scores[:n]
	// Re-sort by original index to preserve order
	for i := 0; i < len(top)-1; i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].idx < top[i].idx {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	var parts []string
	for _, s := range top {
		parts = append(parts, strings.TrimSpace(s.text))
	}
	return strings.Join(parts, " ")
}

func splitSentences(text string) []string {
	var out []string
	var buf strings.Builder
	for _, r := range text {
		buf.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			s := strings.TrimSpace(buf.String())
			if s != "" {
				out = append(out, s)
			}
			buf.Reset()
		}
	}
	if rem := strings.TrimSpace(buf.String()); rem != "" {
		out = append(out, rem)
	}
	return out
}

// ── minimal math expression evaluator ────────────────────────────────────────

type mathParser struct {
	input string
	pos   int
}

func evalMath(expr string) (float64, error) {
	p := &mathParser{input: expr}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	// skip trailing spaces
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("unexpected characters at position %d", p.pos)
	}
	return v, nil
}

func (p *mathParser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func (p *mathParser) parseExpr() (float64, error) {
	return p.parseAddSub()
}

func (p *mathParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *mathParser) parseMulDiv() (float64, error) {
	left, err := p.parsePow()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		right, err := p.parsePow()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		case '%':
			left = math.Mod(left, right)
		}
	}
	return left, nil
}

func (p *mathParser) parsePow() (float64, error) {
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '^' {
		p.pos++
		exp, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
		v, err := p.parseAtom()
		return -v, err
	}
	if p.pos < len(p.input) && p.input[p.pos] == '+' {
		p.pos++
	}
	return p.parseAtom()
}

// mathFuncs maps function names to their implementations.
var mathFuncs = map[string]func(float64) float64{
	"sqrt": math.Sqrt, "abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor,
	"round": math.Round, "log": math.Log, "log2": math.Log2, "log10": math.Log10,
	"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
	"exp": math.Exp,
}

func (p *mathParser) parseAtom() (float64, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	// Check for named function call: name(expr)
	if p.pos < len(p.input) && (p.input[p.pos] >= 'a' && p.input[p.pos] <= 'z') {
		start := p.pos
		for p.pos < len(p.input) && p.input[p.pos] >= 'a' && p.input[p.pos] <= 'z' {
			p.pos++
		}
		name := p.input[start:p.pos]
		p.skipSpaces()
		if p.pos < len(p.input) && p.input[p.pos] == '(' {
			p.pos++
			arg, err := p.parseExpr()
			if err != nil {
				return 0, err
			}
			p.skipSpaces()
			if p.pos >= len(p.input) || p.input[p.pos] != ')' {
				return 0, fmt.Errorf("missing ) after %s(", name)
			}
			p.pos++
			fn, ok := mathFuncs[name]
			if !ok {
				return 0, fmt.Errorf("unknown function %q", name)
			}
			return fn(arg), nil
		}
		// Constants
		switch name {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		}
		return 0, fmt.Errorf("unknown identifier %q", name)
	}
	if p.input[p.pos] == '(' {
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return v, nil
	}
	// parse number
	start := p.pos
	if p.pos < len(p.input) && (p.input[p.pos] == '-' || p.input[p.pos] == '+') {
		p.pos++
	}
	for p.pos < len(p.input) && (p.input[p.pos] >= '0' && p.input[p.pos] <= '9' || p.input[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("expected number at position %d", p.pos)
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}

// ── job worker ────────────────────────────────────────────────────────────────

func claimAndRun(ctx context.Context, api *apiClient, cfg config, sem chan struct{}) {
	for _, cap := range cfg.capabilities {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Try to acquire semaphore slot (non-blocking)
		select {
		case sem <- struct{}{}:
		default:
			continue // all slots busy
		}

		var j job
		path := fmt.Sprintf("/agents/jobs/claim?capability=%s&agent_id=%s", url.QueryEscape(cap), url.QueryEscape(cfg.agentID))
		code, err := api.get(path, &j)
		if err != nil || code != 200 || j.ID == "" {
			<-sem // release
			continue
		}

		log.Printf("[job] claimed %s capability=%s", j.ID, j.Capability)

		go func(j job) {
			defer func() { <-sem }()

			// Dispatch on the job's actual capability, not the claim cap.
			h, ok := capabilities[j.Capability]
			if !ok {
				finishJob(api, j.ID, "failed", "", fmt.Sprintf("no handler for capability %q", j.Capability))
				return
			}

			result, errMsg := h(j.Payload)
			if errMsg != "" {
				log.Printf("[job] %s failed: %s", j.ID, errMsg)
				finishJob(api, j.ID, "failed", "", errMsg)
			} else {
				log.Printf("[job] %s done: %s", j.ID, truncate(result, 80))
				finishJob(api, j.ID, "done", result, "")
			}
		}(j)
	}
}

func finishJob(api *apiClient, id, status, result, errMsg string) {
	upd := jobUpdate{Status: status, Result: result, Error: errMsg}
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		code, err := api.patch("/agents/jobs/"+id, upd)
		if err == nil && (code == 200 || code == 204) {
			return
		}
		if code == 429 {
			// Rate limited — wait and retry
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			continue
		}
		log.Printf("[job] failed to update %s: code=%d err=%v", id, code, err)
		return
	}
	log.Printf("[job] gave up updating %s after retries", id)
}

func newFetchHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if err := validateFetchHost(ctx, host); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	return &http.Client{
		Timeout:   8 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			return validateFetchURL(req.Context(), req.URL)
		},
	}
}

func validateFetchURL(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("url is required")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https URLs allowed")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("url host is required")
	}
	return validateFetchHost(ctx, u.Hostname())
}

func validateFetchHost(ctx context.Context, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("refusing to fetch from local or private network address")
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedFetchIP(ip) {
			return fmt.Errorf("refusing to fetch from local or private network address")
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host %q did not resolve", host)
	}
	for _, addr := range addrs {
		if blockedFetchIP(addr.IP) {
			return fmt.Errorf("refusing to fetch from local or private network address")
		}
	}
	return nil
}

func blockedFetchIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix(fmt.Sprintf("[%s] ", cfg.agentID))

	api := newClient(cfg.serverURL, cfg.tenant, cfg.apiKey)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Register with retry
	log.Printf("registering with %s (capabilities: %s)", cfg.serverURL, strings.Join(cfg.capabilities, ", "))
	for {
		if err := register(ctx, api, cfg); err != nil {
			log.Printf("registration failed: %v — retrying in 3s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		break
	}
	log.Printf("registered successfully")

	// Heartbeat goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		heartbeatLoop(ctx, api, cfg)
	}()

	// Job poll loop
	sem := make(chan struct{}, cfg.concurrency)
	tick := time.NewTicker(cfg.pollInterval)
	defer tick.Stop()

	log.Printf("polling for jobs every %s", cfg.pollInterval)
	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down")
			wg.Wait()
			return
		case <-tick.C:
			claimAndRun(ctx, api, cfg, sem)
		}
	}
}
