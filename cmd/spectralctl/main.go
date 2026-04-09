package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

//go:embed schemas/*.json
var schemaFS embed.FS

type blockchainFile struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updated_at"`
	Blocks    []blockchain.Block `json:"blocks"`
}

type routesFile struct {
	Version   int             `json:"version"`
	UpdatedAt string          `json:"updated_at"`
	Routes    []routing.Route `json:"routes"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "validate":
		validateCmd(os.Args[2:])
	case "repair":
		repairCmd(os.Args[2:])
	case "backup":
		backupCmd(os.Args[2:])
	case "restore":
		restoreCmd(os.Args[2:])
	case "compact":
		compactCmd(os.Args[2:])
	case "keygen":
		keygenCmd(os.Args[2:])
	case "rekey":
		rekeyCmd(os.Args[2:])
	case "mesh-send":
		meshSendCmd(os.Args[2:])
	case "mesh-watch":
		meshWatchCmd(os.Args[2:])
	case "mesh-load":
		meshLoadCmd(os.Args[2:])
	case "route-best":
		routeBestCmd(os.Args[2:])
	case "api-health":
		apiHealthCmd(os.Args[2:])
	case "api-routes":
		apiRoutesCmd(os.Args[2:])
	case "api-agents":
		apiAgentsCmd(os.Args[2:])
	case "blockchain-inspect":
		blockchainInspectCmd(os.Args[2:])
	case "agent-register":
		agentRegisterCmd(os.Args[2:])
	case "chain-verify":
		chainVerifyCmd(os.Args[2:])
	case "blockchain-export":
		blockchainExportCmd(os.Args[2:])
	case "routes-export":
		routesExportCmd(os.Args[2:])
	case "tenant-list":
		tenantListCmd(os.Args[2:])
	case "tenant-create":
		tenantCreateCmd(os.Args[2:])
	case "tenant-delete":
		tenantDeleteCmd(os.Args[2:])
	case "chain-search":
		chainSearchCmd(os.Args[2:])
	case "agent-status":
		agentStatusCmd(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  spectralctl validate --data-dir <dir>")
	fmt.Fprintln(os.Stderr, "  spectralctl repair --data-dir <dir>")
	fmt.Fprintln(os.Stderr, "  spectralctl backup --db-path <path> --out <path> [--key <base64>]")
	fmt.Fprintln(os.Stderr, "  spectralctl restore --db-path <path> --in <path> [--key <base64>]")
	fmt.Fprintln(os.Stderr, "  spectralctl compact --db-path <path> --out <path>")
	fmt.Fprintln(os.Stderr, "  spectralctl compact --db-path <path> --in-place")
	fmt.Fprintln(os.Stderr, "  spectralctl keygen")
	fmt.Fprintln(os.Stderr, "  spectralctl rekey --in <path> --out <path> --old-key <base64> --new-key <base64>")
	fmt.Fprintln(os.Stderr, "  spectralctl mesh-send --addr <host:port> --api-key <key> --tenant <tenant> --kind data|control [options]")
	fmt.Fprintln(os.Stderr, "  spectralctl mesh-watch --addr <host:port> --api-key <key> --tenant <tenant> [--interval 5s]")
	fmt.Fprintln(os.Stderr, "  spectralctl mesh-load --addr <host:port> --api-key <key> --tenant <tenant> --kind data|control [--count N | --duration 10s] [--concurrency 4] [--ramp-start 2 --ramp-step 2 --ramp-interval 5s --ramp-max 8] [--rate 100] [--window 1s --window-live --window-live-json] [--hist ...] [--csv path] [--json]")
	fmt.Fprintln(os.Stderr, "  spectralctl route-best --db-path <path> [--tenant <tenant>]")
	fmt.Fprintln(os.Stderr, "  spectralctl api-health --url <http://host:port>")
	fmt.Fprintln(os.Stderr, "  spectralctl api-routes --url <http://host:port> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl api-agents --url <http://host:port> [--api-key <key>] [--tenant <tenant>]")
	fmt.Fprintln(os.Stderr, "  spectralctl blockchain-inspect --url <http://host:port> --index <N> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl agent-register --url <http://host:port> --id <id> [--addr <addr>] [--capability <c1,c2>] [--ttl 300] [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl chain-verify --url <http://host:port> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl blockchain-export --url <http://host:port> --out <path> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl routes-export --url <http://host:port> --out <path> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl tenant-list --url <http://host:port> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl tenant-create --url <http://host:port> --name <name> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl tenant-delete --url <http://host:port> --name <name> [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl chain-search --url <http://host:port> [--sender <s>] [--recipient <r>] [--api-key <key>]")
	fmt.Fprintln(os.Stderr, "  spectralctl agent-status --url <http://host:port> --id <id> --status <healthy|degraded|unknown> [--api-key <key>]")
}

func validateCmd(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "data directory")
	dbPath := fs.String("db-path", "", "db path (optional)")
	_ = fs.Parse(args)

	chainPath := *dataDir + "/blockchain.json"
	routesPath := *dataDir + "/routes.json"
	if *dbPath == "" {
		*dbPath = store.DBPath(*dataDir)
	}

	chainIssues, err := validateBlockchain(chainPath, *dbPath)
	if err != nil {
		exitErr(err)
	}
	routesIssues, err := validateRoutes(routesPath, *dbPath)
	if err != nil {
		exitErr(err)
	}
	if err := validateSchemaFile(chainPath, "schemas/blockchain.schema.json"); err != nil {
		chainIssues = append(chainIssues, "schema: "+err.Error())
	}
	if err := validateSchemaFile(routesPath, "schemas/routes.schema.json"); err != nil {
		routesIssues = append(routesIssues, "schema: "+err.Error())
	}
	printIssues("blockchain", chainIssues)
	printIssues("routes", routesIssues)
}

func repairCmd(args []string) {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "data directory")
	dbPath := fs.String("db-path", "", "db path (optional)")
	_ = fs.Parse(args)

	chainPath := *dataDir + "/blockchain.json"
	routesPath := *dataDir + "/routes.json"
	if *dbPath == "" {
		*dbPath = store.DBPath(*dataDir)
	}

	if err := repairBlockchain(chainPath, *dbPath); err != nil {
		exitErr(err)
	}
	if err := repairRoutes(routesPath, *dbPath); err != nil {
		exitErr(err)
	}
	fmt.Println("repair completed")
}

func backupCmd(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "db path")
	outPath := fs.String("out", "", "output path")
	key := fs.String("key", "", "base64-encoded 32-byte key (optional)")
	_ = fs.Parse(args)
	if *dbPath == "" || *outPath == "" {
		exitErr(errors.New("db-path and out are required"))
	}
	if strings.TrimSpace(*key) == "" {
		if err := store.Backup(*dbPath, *outPath); err != nil {
			exitErr(err)
		}
		if err := store.Verify(*outPath); err != nil {
			exitErr(err)
		}
		fmt.Println("backup completed")
		return
	}
	if err := store.BackupEncrypted(*dbPath, *outPath, *key); err != nil {
		exitErr(err)
	}
	if err := store.VerifyEncrypted(*outPath, *key); err != nil {
		exitErr(err)
	}
	fmt.Println("backup completed (encrypted)")
}

func restoreCmd(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "db path")
	inPath := fs.String("in", "", "input backup path")
	key := fs.String("key", "", "base64-encoded 32-byte key (optional)")
	_ = fs.Parse(args)
	if *dbPath == "" || *inPath == "" {
		exitErr(errors.New("db-path and in are required"))
	}
	if strings.TrimSpace(*key) == "" {
		if err := store.Restore(*dbPath, *inPath); err != nil {
			exitErr(err)
		}
		fmt.Println("restore completed")
		return
	}
	if err := store.RestoreEncrypted(*dbPath, *inPath, *key); err != nil {
		exitErr(err)
	}
	fmt.Println("restore completed (encrypted)")
}

func compactCmd(args []string) {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "db path")
	outPath := fs.String("out", "", "output path")
	inPlace := fs.Bool("in-place", false, "compact in place")
	_ = fs.Parse(args)
	if *dbPath == "" {
		exitErr(errors.New("db-path is required"))
	}
	if !*inPlace && *outPath == "" {
		exitErr(errors.New("out is required unless --in-place is set"))
	}
	if *inPlace {
		if err := store.CompactInPlace(*dbPath); err != nil {
			exitErr(err)
		}
		fmt.Println("compact completed (in-place)")
		return
	}
	if err := store.Compact(*dbPath, *outPath); err != nil {
		exitErr(err)
	}
	if err := store.Verify(*outPath); err != nil {
		exitErr(err)
	}
	fmt.Println("compact completed")
}

func keygenCmd(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	_ = fs.Parse(args)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		exitErr(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(key))
}

func rekeyCmd(args []string) {
	fs := flag.NewFlagSet("rekey", flag.ExitOnError)
	inPath := fs.String("in", "", "input encrypted backup")
	outPath := fs.String("out", "", "output encrypted backup")
	oldKey := fs.String("old-key", "", "old base64 key")
	newKey := fs.String("new-key", "", "new base64 key")
	_ = fs.Parse(args)
	if *inPath == "" || *outPath == "" || *oldKey == "" || *newKey == "" {
		exitErr(errors.New("in, out, old-key, and new-key are required"))
	}
	if err := store.RotateEncryptedBackup(*inPath, *outPath, *oldKey, *newKey); err != nil {
		exitErr(err)
	}
	fmt.Println("rekey completed")
}

func meshSendCmd(args []string) {
	fs := flag.NewFlagSet("mesh-send", flag.ExitOnError)
	addr := fs.String("addr", "", "gRPC address (host:port)")
	apiKey := fs.String("api-key", "", "API key")
	tenant := fs.String("tenant", "default", "tenant id")
	tlsEnabled := fs.Bool("tls", false, "enable TLS")
	tlsCA := fs.String("tls-ca", "", "CA bundle path (optional)")
	tlsServerName := fs.String("tls-server-name", "", "TLS server name override")
	tlsInsecure := fs.Bool("tls-insecure-skip-verify", false, "skip TLS verification (not recommended)")
	kind := fs.String("kind", "data", "data or control")
	sourceID := fs.Uint("source-id", 1, "source id (data)")
	destID := fs.Uint("destination-id", 2, "destination id (data)")
	nodeID := fs.Uint("node-id", 1, "node id (control)")
	payload := fs.String("payload", "hello", "payload string (data)")
	controlType := fs.String("control-type", "heartbeat", "heartbeat or handshake (control)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	count := fs.Int("count", 1, "number of messages")
	rate := fs.Float64("rate", 0, "messages per second (0 = as fast as possible)")
	_ = fs.Parse(args)

	if *addr == "" || *apiKey == "" {
		exitErr(errors.New("addr and api-key are required"))
	}

	if *count <= 0 {
		exitErr(errors.New("count must be positive"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	creds := insecure.NewCredentials()
	if *tlsEnabled {
		tlsCfg := &tls.Config{InsecureSkipVerify: *tlsInsecure, MinVersion: tls.VersionTLS12}
		if strings.TrimSpace(*tlsServerName) != "" {
			tlsCfg.ServerName = *tlsServerName
		}
		if strings.TrimSpace(*tlsCA) != "" {
			caBytes, err := os.ReadFile(*tlsCA)
			if err != nil {
				exitErr(err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caBytes) {
				exitErr(errors.New("invalid CA bundle"))
			}
			tlsCfg.RootCAs = pool
		}
		creds = credentials.NewTLS(tlsCfg)
	}
	conn, err := grpc.DialContext(ctx, *addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		exitErr(err)
	}
	defer func() { _ = conn.Close() }()

	client := meshpb.NewMeshServiceClient(conn)
	send := buildMeshSender(client, *apiKey, *tenant, strings.ToLower(strings.TrimSpace(*kind)), *controlType, *sourceID, *destID, *nodeID, *payload)
	start := time.Now().UTC()
	var sent int
	var failed int

	var ticker *time.Ticker
	if *rate > 0 {
		interval := time.Duration(float64(time.Second) / *rate)
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
	}
	for i := 0; i < *count; i++ {
		if ticker != nil {
			<-ticker.C
		}
		ack, err := send(ctx)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
			continue
		}
		sent++
		fmt.Printf("ack: source=%d dest=%d tenant=%s msg=%s\n", ack.SourceId, ack.DestinationId, ack.TenantId, ack.Message)
	}
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		fmt.Printf("summary: sent=%d failed=%d rate=%.2f msg/s\n", sent, failed, float64(sent)/elapsed)
	}
}

func meshWatchCmd(args []string) {
	fs := flag.NewFlagSet("mesh-watch", flag.ExitOnError)
	addr := fs.String("addr", "", "gRPC address (host:port)")
	apiKey := fs.String("api-key", "", "API key")
	tenant := fs.String("tenant", "default", "tenant id")
	tlsEnabled := fs.Bool("tls", false, "enable TLS")
	tlsCA := fs.String("tls-ca", "", "CA bundle path (optional)")
	tlsServerName := fs.String("tls-server-name", "", "TLS server name override")
	tlsInsecure := fs.Bool("tls-insecure-skip-verify", false, "skip TLS verification (not recommended)")
	interval := fs.Duration("interval", 5*time.Second, "interval between heartbeats")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)

	if *addr == "" || *apiKey == "" {
		exitErr(errors.New("addr and api-key are required"))
	}
	if *interval <= 0 {
		exitErr(errors.New("interval must be positive"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creds := insecure.NewCredentials()
	if *tlsEnabled {
		tlsCfg := &tls.Config{InsecureSkipVerify: *tlsInsecure, MinVersion: tls.VersionTLS12}
		if strings.TrimSpace(*tlsServerName) != "" {
			tlsCfg.ServerName = *tlsServerName
		}
		if strings.TrimSpace(*tlsCA) != "" {
			caBytes, err := os.ReadFile(*tlsCA)
			if err != nil {
				exitErr(err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caBytes) {
				exitErr(errors.New("invalid CA bundle"))
			}
			tlsCfg.RootCAs = pool
		}
		creds = credentials.NewTLS(tlsCfg)
	}
	conn, err := grpc.DialContext(ctx, *addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		exitErr(err)
	}
	defer func() { _ = conn.Close() }()

	client := meshpb.NewMeshServiceClient(conn)
	send := buildMeshSender(client, *apiKey, *tenant, "control", "heartbeat", 1, 1, 1, "watch")

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for range ticker.C {
		reqCtx, cancelReq := context.WithTimeout(context.Background(), *timeout)
		ack, err := send(reqCtx)
		cancelReq()
		if err != nil {
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
			continue
		}
		fmt.Printf("ack: source=%d dest=%d tenant=%s msg=%s\n", ack.SourceId, ack.DestinationId, ack.TenantId, ack.Message)
	}
}

func buildMeshSender(client meshpb.MeshServiceClient, apiKey, tenant, kind, controlType string, sourceID, destID, nodeID uint, payload string) func(context.Context) (*meshpb.Ack, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	controlType = strings.ToLower(strings.TrimSpace(controlType))
	return func(ctx context.Context) (*meshpb.Ack, error) {
		md := metadata.New(map[string]string{
			"x-api-key": apiKey,
		})
		if strings.TrimSpace(tenant) != "" {
			md.Set("x-tenant-id", tenant)
		}
		ctx = metadata.NewOutgoingContext(ctx, md)
		switch kind {
		case "data":
			msg := &meshpb.DataMessage{
				MsgType:       meshpb.DataMessage_DATA,
				SourceId:      uint32(sourceID),
				DestinationId: uint32(destID),
				Timestamp:     time.Now().UTC().Unix(),
				Payload:       []byte(payload),
				TenantId:      tenant,
			}
			return client.SendData(ctx, msg)
		case "control":
			ct := meshpb.ControlMessage_HEARTBEAT
			switch controlType {
			case "handshake":
				ct = meshpb.ControlMessage_HANDSHAKE
			case "heartbeat":
				ct = meshpb.ControlMessage_HEARTBEAT
			default:
				return nil, errors.New("invalid control-type")
			}
			msg := &meshpb.ControlMessage{
				MsgType:     meshpb.DataMessage_DATA,
				ControlType: ct,
				NodeId:      uint32(nodeID),
				Payload:     []byte(payload),
				TenantId:    tenant,
			}
			return client.SendControl(ctx, msg)
		default:
			return nil, errors.New("kind must be data or control")
		}
	}
}

func meshLoadCmd(args []string) {
	fs := flag.NewFlagSet("mesh-load", flag.ExitOnError)
	addr := fs.String("addr", "", "gRPC address (host:port)")
	apiKey := fs.String("api-key", "", "API key")
	tenant := fs.String("tenant", "default", "tenant id")
	tlsEnabled := fs.Bool("tls", false, "enable TLS")
	tlsCA := fs.String("tls-ca", "", "CA bundle path (optional)")
	tlsServerName := fs.String("tls-server-name", "", "TLS server name override")
	tlsInsecure := fs.Bool("tls-insecure-skip-verify", false, "skip TLS verification (not recommended)")
	kind := fs.String("kind", "data", "data or control")
	sourceID := fs.Uint("source-id", 1, "source id (data)")
	destID := fs.Uint("destination-id", 2, "destination id (data)")
	nodeID := fs.Uint("node-id", 1, "node id (control)")
	payload := fs.String("payload", "hello", "payload string (data)")
	controlType := fs.String("control-type", "heartbeat", "heartbeat or handshake (control)")
	count := fs.Int("count", 0, "total messages to send")
	duration := fs.Duration("duration", 0, "how long to run (e.g. 10s)")
	concurrency := fs.Int("concurrency", 4, "number of concurrent workers")
	rampStart := fs.Int("ramp-start", 0, "starting concurrency (default = --concurrency)")
	rampStep := fs.Int("ramp-step", 0, "concurrency increment per interval")
	rampInterval := fs.Duration("ramp-interval", 0, "interval to ramp concurrency (e.g. 5s)")
	rampMax := fs.Int("ramp-max", 0, "max concurrency during ramp (default = --concurrency)")
	rate := fs.Float64("rate", 0, "max messages per second (0 = unlimited)")
	jsonOut := fs.Bool("json", false, "emit JSON summary")
	csvOut := fs.String("csv", "", "write per-request CSV to path")
	bucketsRaw := fs.String("hist", "1,5,10,25,50,100,250,500,1000,2000", "histogram bucket bounds in ms (comma-separated)")
	window := fs.Duration("window", 0, "emit per-window latency stats (e.g. 1s)")
	windowLive := fs.Bool("window-live", false, "print window stats as they are collected")
	windowLiveJSON := fs.Bool("window-live-json", false, "emit live window stats as JSON lines")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)

	if *addr == "" || *apiKey == "" {
		exitErr(errors.New("addr and api-key are required"))
	}
	if *count <= 0 && *duration <= 0 {
		exitErr(errors.New("count or duration is required"))
	}
	if *concurrency <= 0 {
		exitErr(errors.New("concurrency must be positive"))
	}
	if *rampStart <= 0 {
		*rampStart = *concurrency
	}
	if *rampMax <= 0 {
		*rampMax = *concurrency
	}
	if *rampStart <= 0 || *rampMax <= 0 {
		exitErr(errors.New("ramp-start and ramp-max must be positive"))
	}
	if *rampInterval > 0 && *rampStep <= 0 {
		exitErr(errors.New("ramp-step must be positive when ramp-interval is set"))
	}
	if *rampMax < *concurrency {
		*rampMax = *concurrency
	}
	if *rampStart > *rampMax {
		*rampMax = *rampStart
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creds := insecure.NewCredentials()
	if *tlsEnabled {
		tlsCfg := &tls.Config{InsecureSkipVerify: *tlsInsecure, MinVersion: tls.VersionTLS12}
		if strings.TrimSpace(*tlsServerName) != "" {
			tlsCfg.ServerName = *tlsServerName
		}
		if strings.TrimSpace(*tlsCA) != "" {
			caBytes, err := os.ReadFile(*tlsCA)
			if err != nil {
				exitErr(err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caBytes) {
				exitErr(errors.New("invalid CA bundle"))
			}
			tlsCfg.RootCAs = pool
		}
		creds = credentials.NewTLS(tlsCfg)
	}
	conn, err := grpc.DialContext(ctx, *addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		exitErr(err)
	}
	defer func() { _ = conn.Close() }()

	client := meshpb.NewMeshServiceClient(conn)
	send := buildMeshSender(client, *apiKey, *tenant, strings.ToLower(strings.TrimSpace(*kind)), *controlType, *sourceID, *destID, *nodeID, *payload)

	jobCh := make(chan struct{})
	var wg sync.WaitGroup
	buckets, err := parseBuckets(*bucketsRaw)
	if err != nil {
		exitErr(err)
	}
	stats := newLoadStats(buckets)
	windowStats := newWindowStats(*window, *windowLive, *windowLiveJSON)

	var csvFile *os.File
	var csvWriter *csv.Writer
	if strings.TrimSpace(*csvOut) != "" {
		f, err := os.Create(*csvOut)
		if err != nil {
			exitErr(err)
		}
		csvFile = f
		csvWriter = csv.NewWriter(f)
		if err := csvWriter.Write([]string{"ts", "ok", "latency_ms", "error"}); err != nil {
			exitErr(err)
		}
	}
	defer func() {
		if csvWriter != nil {
			csvWriter.Flush()
		}
		if csvFile != nil {
			_ = csvFile.Close()
		}
	}()

	start := time.Now()
	worker := func() {
		defer wg.Done()
		for range jobCh {
			reqCtx, cancelReq := context.WithTimeout(context.Background(), *timeout)
			before := time.Now()
			_, err := send(reqCtx)
			cancelReq()
			lat := time.Since(before)
			if err != nil {
				stats.record(false, lat)
				windowStats.record(false, lat, err.Error())
				if csvWriter != nil {
					_ = csvWriter.Write([]string{time.Now().UTC().Format(time.RFC3339Nano), "false", fmt.Sprintf("%.3f", float64(lat.Microseconds())/1000.0), err.Error()})
				}
			} else {
				stats.record(true, lat)
				windowStats.record(true, lat, "")
				if csvWriter != nil {
					_ = csvWriter.Write([]string{time.Now().UTC().Format(time.RFC3339Nano), "true", fmt.Sprintf("%.3f", float64(lat.Microseconds())/1000.0), ""})
				}
			}
		}
	}
	launchWorkers := func(n int) {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go worker()
		}
	}
	launchWorkers(*rampStart)

	if *rampInterval > 0 && *rampStep > 0 && *rampStart < *rampMax {
		go func() {
			current := *rampStart
			ticker := time.NewTicker(*rampInterval)
			defer ticker.Stop()
			for range ticker.C {
				next := current + *rampStep
				if next > *rampMax {
					next = *rampMax
				}
				launchWorkers(next - current)
				current = next
				if current >= *rampMax {
					return
				}
			}
		}()
	}

	windowStats.start()
	defer windowStats.stop()

	if *rampStart < *concurrency {
		launchWorkers(*concurrency - *rampStart)
	}
	if *rampStart < *rampMax && *rampInterval == 0 {
		launchWorkers(*rampMax - *rampStart)
	}

	var ticker *time.Ticker
	if *rate > 0 {
		interval := time.Duration(float64(time.Second) / *rate)
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
	}

	sendJob := func() bool {
		if ticker != nil {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return false
			}
		}
		select {
		case jobCh <- struct{}{}:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if *duration > 0 {
		stop := time.NewTimer(*duration)
	loopDuration:
		for {
			select {
			case <-stop.C:
				break loopDuration
			default:
				if !sendJob() {
					break loopDuration
				}
			}
		}
	} else {
		for i := 0; i < *count; i++ {
			if !sendJob() {
				break
			}
		}
	}
	close(jobCh)
	wg.Wait()
	elapsed := time.Since(start)
	stats.finish(elapsed)
	windowStats.finish()
	if *jsonOut {
		summary := stats.summary()
		summary.Windows = windowStats.summaries()
		out, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			exitErr(err)
		}
		fmt.Println(string(out))
		return
	}
	printSummary(stats.summary(), windowStats.summaries())
}

type loadStats struct {
	mu        sync.Mutex
	successes int
	failures  int
	latencies []time.Duration
	elapsed   time.Duration
	buckets   []int64
	hist      []int
}

func newLoadStats(buckets []int64) *loadStats {
	return &loadStats{
		latencies: make([]time.Duration, 0, 1024),
		buckets:   buckets,
		hist:      make([]int, len(buckets)+1),
	}
}

func (s *loadStats) record(ok bool, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.successes++
	} else {
		s.failures++
	}
	s.latencies = append(s.latencies, latency)
	if len(s.buckets) > 0 {
		ms := latency.Milliseconds()
		idx := len(s.buckets)
		for i, b := range s.buckets {
			if ms <= b {
				idx = i
				break
			}
		}
		s.hist[idx]++
	}
}

func (s *loadStats) finish(elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.elapsed = elapsed
}

type loadSummary struct {
	ElapsedSeconds float64         `json:"elapsed_seconds"`
	Successes      int             `json:"successes"`
	Failures       int             `json:"failures"`
	Total          int             `json:"total"`
	RPS            float64         `json:"rps"`
	AvgMs          float64         `json:"avg_ms"`
	P50Ms          float64         `json:"p50_ms"`
	P95Ms          float64         `json:"p95_ms"`
	P99Ms          float64         `json:"p99_ms"`
	MaxMs          float64         `json:"max_ms"`
	Histogram      []histBucket    `json:"histogram,omitempty"`
	Windows        []windowSummary `json:"windows,omitempty"`
}

func (s *loadStats) summary() loadSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.successes + s.failures
	avg, p50, p95, p99, max := summarizeLatencies(s.latencies)
	elapsedSec := s.elapsed.Seconds()
	rps := 0.0
	if elapsedSec > 0 {
		rps = float64(total) / elapsedSec
	}
	summary := loadSummary{
		ElapsedSeconds: elapsedSec,
		Successes:      s.successes,
		Failures:       s.failures,
		Total:          total,
		RPS:            rps,
		AvgMs:          avg,
		P50Ms:          p50,
		P95Ms:          p95,
		P99Ms:          p99,
		MaxMs:          max,
	}
	if len(s.buckets) > 0 {
		out := make([]histBucket, 0, len(s.hist))
		for i := 0; i < len(s.hist); i++ {
			var upper *int64
			if i < len(s.buckets) {
				val := s.buckets[i]
				upper = &val
			}
			out = append(out, histBucket{
				UpperMs: upper,
				Count:   s.hist[i],
			})
		}
		summary.Histogram = out
	}
	return summary
}

type histBucket struct {
	UpperMs *int64 `json:"upper_ms"`
	Count   int    `json:"count"`
}

type errorCount struct {
	Error string `json:"error"`
	Count int    `json:"count"`
}

func summarizeLatencies(lats []time.Duration) (avg, p50, p95, p99, max float64) {
	if len(lats) == 0 {
		return 0, 0, 0, 0, 0
	}
	values := make([]int64, len(lats))
	var sum int64
	for i, v := range lats {
		ms := v.Milliseconds()
		values[i] = ms
		sum += ms
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	max = float64(values[len(values)-1])
	avg = float64(sum) / float64(len(values))
	p50 = percentile(values, 0.50)
	p95 = percentile(values, 0.95)
	p99 = percentile(values, 0.99)
	return avg, p50, p95, p99, max
}

func percentile(values []int64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return float64(values[0])
	}
	if p >= 1 {
		return float64(values[len(values)-1])
	}
	pos := int(float64(len(values)-1) * p)
	return float64(values[pos])
}

func printSummary(s loadSummary, windows []windowSummary) {
	fmt.Printf("elapsed: %.2fs\n", s.ElapsedSeconds)
	fmt.Printf("total: %d (ok=%d fail=%d)\n", s.Total, s.Successes, s.Failures)
	fmt.Printf("rate: %.2f msg/s\n", s.RPS)
	fmt.Printf("latency_ms: avg=%.2f p50=%.2f p95=%.2f p99=%.2f max=%.2f\n", s.AvgMs, s.P50Ms, s.P95Ms, s.P99Ms, s.MaxMs)
	if len(s.Histogram) > 0 {
		fmt.Println("histogram_ms:")
		var lastUpper int64
		for _, b := range s.Histogram {
			if b.UpperMs == nil {
				fmt.Printf("  >%d: %d\n", lastUpper, b.Count)
				continue
			}
			fmt.Printf("  <=%d: %d\n", *b.UpperMs, b.Count)
			lastUpper = *b.UpperMs
		}
	}
	if len(windows) > 0 {
		fmt.Println("window_stats:")
		for _, w := range windows {
			errInfo := ""
			if len(w.ErrorTop) > 0 {
				errInfo = fmt.Sprintf(" top_err=\"%s\" err_count=%d", w.ErrorTop[0].Error, w.ErrorTop[0].Count)
			}
			fmt.Printf("  %s ok=%d fail=%d rps=%.2f p50=%.2f p95=%.2f p99=%.2f max=%.2f%s\n", w.EndTime, w.Successes, w.Failures, w.RPS, w.P50Ms, w.P95Ms, w.P99Ms, w.MaxMs, errInfo)
		}
	}
}

func parseBuckets(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		val, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid bucket: %s", part)
		}
		if val <= 0 {
			return nil, errors.New("bucket values must be positive")
		}
		out = append(out, val)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func topErrorCounts(counts map[string]int, limit int) []errorCount {
	if len(counts) == 0 || limit <= 0 {
		return nil
	}
	out := make([]errorCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, errorCount{Error: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Error < out[j].Error
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

type windowSummary struct {
	EndTime   string       `json:"end_time"`
	Successes int          `json:"successes"`
	Failures  int          `json:"failures"`
	RPS       float64      `json:"rps"`
	P50Ms     float64      `json:"p50_ms"`
	P95Ms     float64      `json:"p95_ms"`
	P99Ms     float64      `json:"p99_ms"`
	MaxMs     float64      `json:"max_ms"`
	ErrorTop  []errorCount `json:"error_top,omitempty"`
}

type windowStats struct {
	enabled   bool
	window    time.Duration
	live      bool
	liveJSON  bool
	mu        sync.Mutex
	ok        int
	fail      int
	latencies []time.Duration
	windows   []windowSummary
	errCounts map[string]int
	ticker    *time.Ticker
	done      chan struct{}
	closed    bool
}

func newWindowStats(window time.Duration, live bool, liveJSON bool) *windowStats {
	return &windowStats{
		enabled:   window > 0,
		window:    window,
		live:      live,
		liveJSON:  liveJSON,
		latencies: make([]time.Duration, 0, 512),
		windows:   make([]windowSummary, 0, 16),
		errCounts: map[string]int{},
	}
}

func (w *windowStats) start() {
	if !w.enabled || w.window <= 0 {
		return
	}
	w.ticker = time.NewTicker(w.window)
	w.done = make(chan struct{})
	go func() {
		for {
			select {
			case <-w.ticker.C:
				w.flush(time.Now().UTC())
			case <-w.done:
				w.flush(time.Now().UTC())
				return
			}
		}
	}()
}

func (w *windowStats) stop() {
	if !w.enabled {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.mu.Unlock()
	if w.ticker != nil {
		w.ticker.Stop()
	}
	if w.done != nil {
		close(w.done)
	}
}

func (w *windowStats) record(ok bool, latency time.Duration, errStr string) {
	if !w.enabled {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if ok {
		w.ok++
	} else {
		w.fail++
		if strings.TrimSpace(errStr) != "" {
			w.errCounts[errStr]++
		}
	}
	w.latencies = append(w.latencies, latency)
}

func (w *windowStats) flush(now time.Time) {
	w.mu.Lock()
	ok := w.ok
	fail := w.fail
	lats := make([]time.Duration, len(w.latencies))
	copy(lats, w.latencies)
	errCounts := make(map[string]int, len(w.errCounts))
	for k, v := range w.errCounts {
		errCounts[k] = v
	}
	w.ok = 0
	w.fail = 0
	w.latencies = w.latencies[:0]
	for k := range w.errCounts {
		delete(w.errCounts, k)
	}
	w.mu.Unlock()
	if ok == 0 && fail == 0 {
		return
	}
	_, p50, p95, p99, max := summarizeLatencies(lats)
	elapsedSec := w.window.Seconds()
	rps := 0.0
	if elapsedSec > 0 {
		rps = float64(ok+fail) / elapsedSec
	}
	topErrors := topErrorCounts(errCounts, 3)
	summary := windowSummary{
		EndTime:   now.Format(time.RFC3339),
		Successes: ok,
		Failures:  fail,
		RPS:       rps,
		P50Ms:     p50,
		P95Ms:     p95,
		P99Ms:     p99,
		MaxMs:     max,
		ErrorTop:  topErrors,
	}
	w.mu.Lock()
	w.windows = append(w.windows, summary)
	w.mu.Unlock()
	if w.liveJSON {
		line, err := json.Marshal(summary)
		if err == nil {
			fmt.Println(string(line))
		}
	}
	if w.live {
		errInfo := ""
		if len(topErrors) > 0 {
			errInfo = fmt.Sprintf(" top_err=\"%s\" err_count=%d", topErrors[0].Error, topErrors[0].Count)
		}
		fmt.Printf("window %s ok=%d fail=%d rps=%.2f p50=%.2f p95=%.2f p99=%.2f max=%.2f%s\n", summary.EndTime, summary.Successes, summary.Failures, summary.RPS, summary.P50Ms, summary.P95Ms, summary.P99Ms, summary.MaxMs, errInfo)
	}
}

func (w *windowStats) finish() {
	if !w.enabled {
		return
	}
	w.stop()
}

func (w *windowStats) summaries() []windowSummary {
	if !w.enabled {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]windowSummary, len(w.windows))
	copy(out, w.windows)
	return out
}

func printIssues(name string, issues []string) {
	if len(issues) == 0 {
		fmt.Printf("%s: ok\n", name)
		return
	}
	fmt.Printf("%s: %d issue(s)\n", name, len(issues))
	for _, issue := range issues {
		fmt.Printf("- %s\n", issue)
	}
}

func validateBlockchain(path, dbPath string) ([]string, error) {
	if blocks, ok, err := readBlocksFromDB(dbPath); err != nil {
		return nil, err
	} else if ok {
		issues := validateBlocks(blocks)
		if err := validateSchemaDoc(blocks, "schemas/blockchain.schema.json"); err != nil {
			issues = append(issues, "schema: "+err.Error())
		}
		return issues, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file blockchainFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Blocks) > 0 {
		return validateBlocks(file.Blocks), nil
	}
	var blocks []blockchain.Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, err
	}
	return validateBlocks(blocks), nil
}

func validateBlocks(blocks []blockchain.Block) []string {
	var issues []string
	var prevHash string
	for i, block := range blocks {
		if !blockchain.Verify(&block) {
			issues = append(issues, fmt.Sprintf("block %d has invalid hash", i))
			break
		}
		if block.Index > 0 && block.PreviousHash != prevHash {
			issues = append(issues, fmt.Sprintf("block %d previous hash mismatch", i))
			break
		}
		prevHash = block.Hash
	}
	return issues
}

func validateRoutes(path, dbPath string) ([]string, error) {
	if routes, ok, err := readRoutesFromDB(dbPath); err != nil {
		return nil, err
	} else if ok {
		issues := validateRouteList(routes)
		if err := validateSchemaDoc(routes, "schemas/routes.schema.json"); err != nil {
			issues = append(issues, "schema: "+err.Error())
		}
		return issues, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file routesFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Routes) > 0 {
		return validateRouteList(file.Routes), nil
	}
	var routes []routing.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, err
	}
	return validateRouteList(routes), nil
}

func validateRouteList(routes []routing.Route) []string {
	var issues []string
	now := time.Now().UTC()
	for _, route := range routes {
		if route.Destination == "" {
			issues = append(issues, "route with empty destination")
			break
		}
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			issues = append(issues, fmt.Sprintf("route %s expired", route.Destination))
		}
	}
	return issues
}

func validateSchemaFile(path, schemaPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	return validateSchemaDoc(doc, schemaPath)
}

func validateSchemaDoc(doc any, schemaPath string) error {
	schemaBytes, err := schemaFS.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	if err := compiler.AddResource(schemaPath, strings.NewReader(string(schemaBytes))); err != nil {
		return err
	}
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		return err
	}
	if err := schema.Validate(doc); err != nil {
		return err
	}
	return nil
}

func repairBlockchain(path, dbPath string) error {
	if blocks, ok, err := readBlocksFromDB(dbPath); err != nil {
		return err
	} else if ok {
		valid := filterValidBlocks(blocks)
		return writeBlocksToDB(dbPath, valid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file blockchainFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Blocks) > 0 {
		valid := filterValidBlocks(file.Blocks)
		file.Blocks = valid
		file.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return writeJSON(path, file)
	}
	var blocks []blockchain.Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	valid := filterValidBlocks(blocks)
	return writeJSON(path, blockchainFile{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Blocks:    valid,
	})
}

func repairRoutes(path, dbPath string) error {
	if routes, ok, err := readRoutesFromDB(dbPath); err != nil {
		return err
	} else if ok {
		return writeRoutesToDB(dbPath, pruneExpired(routes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file routesFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Routes) > 0 {
		file.Routes = pruneExpired(file.Routes)
		file.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return writeJSON(path, file)
	}
	var routes []routing.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return err
	}
	return writeJSON(path, routesFile{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Routes:    pruneExpired(routes),
	})
}

func pruneExpired(routes []routing.Route) []routing.Route {
	now := time.Now().UTC()
	out := routes[:0]
	for _, route := range routes {
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			continue
		}
		out = append(out, route)
	}
	return out
}

func filterValidBlocks(blocks []blockchain.Block) []blockchain.Block {
	out := make([]blockchain.Block, 0, len(blocks))
	var prevHash string
	for _, block := range blocks {
		if !blockchain.Verify(&block) {
			break
		}
		if block.Index > 0 && block.PreviousHash != prevHash {
			break
		}
		prevHash = block.Hash
		out = append(out, block)
	}
	return out
}

func readBlocksFromDB(path string) ([]blockchain.Block, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = db.Close() }()
	blocks, err := db.ReadBlocks()
	if err != nil {
		return nil, false, err
	}
	if len(blocks) == 0 {
		return nil, false, nil
	}
	return blocks, true, nil
}

func readRoutesFromDB(path string) ([]routing.Route, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = db.Close() }()
	routes, err := db.ReadRoutes()
	if err != nil {
		return nil, false, err
	}
	if len(routes) == 0 {
		return nil, false, nil
	}
	return routes, true, nil
}

func writeBlocksToDB(path string, blocks []blockchain.Block) error {
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.WriteBlocks(blocks)
}

func writeRoutesToDB(path string, routes []routing.Route) error {
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.WriteRoutes(routes)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// route-best: find the best next hop from the local BoltDB
// ---------------------------------------------------------------------------

func routeBestCmd(args []string) {
	fs := flag.NewFlagSet("route-best", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "path to BoltDB file")
	tenant := fs.String("tenant", "default", "tenant name")
	_ = fs.Parse(args)
	if *dbPath == "" {
		exitErr(errors.New("--db-path is required"))
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		exitErr(fmt.Errorf("open db: %w", err))
	}
	defer func() { _ = db.Close() }()

	routes, err := db.ReadRoutesTenant(*tenant)
	if err != nil {
		exitErr(fmt.Errorf("read routes: %w", err))
	}
	engine := routing.NewRoutingEngineFromRoutes(routes)
	best, err := engine.SelectBestNextHop()
	if err != nil {
		exitErr(fmt.Errorf("no routes available: %w", err))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(best)
}

// ---------------------------------------------------------------------------
// api-health: HTTP health check against a running spectral-cloud server
// ---------------------------------------------------------------------------

func apiHealthCmd(args []string) {
	fs := flag.NewFlagSet("api-health", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server (e.g. http://localhost:8080)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}

	body, status, err := apiGET(strings.TrimRight(*serverURL, "/")+"/health", "", *timeout)
	if err != nil {
		exitErr(fmt.Errorf("health check failed: %w", err))
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "unhealthy (HTTP %d): %s\n", status, string(body))
		os.Exit(1)
	}
	fmt.Printf("healthy (HTTP %d): %s\n", status, string(body))
}

// ---------------------------------------------------------------------------
// api-routes: list routes from a running spectral-cloud server
// ---------------------------------------------------------------------------

func apiRoutesCmd(args []string) {
	fs := flag.NewFlagSet("api-routes", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key (optional)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	jsonOut := fs.Bool("json", false, "output raw JSON")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}

	body, status, err := apiGET(strings.TrimRight(*serverURL, "/")+"/routes", *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("api-routes failed: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	if *jsonOut {
		fmt.Println(string(body))
		return
	}
	var routes []map[string]any
	if err := json.Unmarshal(body, &routes); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("%-40s %10s %12s\n", "DESTINATION", "LATENCY(ms)", "THROUGHPUT(Mbps)")
	for _, r := range routes {
		dst := fmt.Sprint(r["destination"])
		lat := ""
		thr := ""
		if m, ok := r["metric"].(map[string]any); ok {
			lat = fmt.Sprint(m["latency"])
			thr = fmt.Sprint(m["throughput"])
		}
		fmt.Printf("%-40s %10s %12s\n", dst, lat, thr)
	}
	fmt.Printf("\n%d route(s)\n", len(routes))
}

// ---------------------------------------------------------------------------
// api-agents: list agents from a running spectral-cloud server
// ---------------------------------------------------------------------------

func apiAgentsCmd(args []string) {
	fs := flag.NewFlagSet("api-agents", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key (optional)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	jsonOut := fs.Bool("json", false, "output raw JSON")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}

	body, status, err := apiGET(strings.TrimRight(*serverURL, "/")+"/agents", *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("api-agents failed: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	if *jsonOut {
		fmt.Println(string(body))
		return
	}
	var agents []map[string]any
	if err := json.Unmarshal(body, &agents); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("%-20s %-30s %-10s %s\n", "ID", "ADDR", "STATUS", "LAST_SEEN")
	for _, a := range agents {
		id := fmt.Sprint(a["id"])
		addr := fmt.Sprint(a["addr"])
		status := fmt.Sprint(a["status"])
		lastSeen := fmt.Sprint(a["last_seen"])
		fmt.Printf("%-20s %-30s %-10s %s\n", id, addr, status, lastSeen)
	}
	fmt.Printf("\n%d agent(s)\n", len(agents))
}

// ---------------------------------------------------------------------------
// blockchain-inspect: fetch a specific block by index from a live server
// ---------------------------------------------------------------------------

func blockchainInspectCmd(args []string) {
	fs := flag.NewFlagSet("blockchain-inspect", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key (optional)")
	index := fs.Int("index", -1, "block index to fetch")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *index < 0 {
		exitErr(errors.New("--index must be >= 0"))
	}

	path := fmt.Sprintf("%s/blockchain/%d", strings.TrimRight(*serverURL, "/"), *index)
	body, status, err := apiGET(path, *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("blockchain-inspect failed: %w", err))
	}
	if status == 404 {
		exitErr(fmt.Errorf("block %d not found", *index))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	var block map[string]any
	if err := json.Unmarshal(body, &block); err != nil {
		fmt.Println(string(body))
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(block)
}

// ---------------------------------------------------------------------------
// agent-register: register an agent via the HTTP API
// ---------------------------------------------------------------------------

func agentRegisterCmd(args []string) {
	fs := flag.NewFlagSet("agent-register", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	id := fs.String("id", "", "agent ID (required)")
	addr := fs.String("addr", "", "agent address (e.g. 10.0.0.1:9000)")
	capsRaw := fs.String("capability", "", "comma-separated list of capabilities")
	ttl := fs.Int("ttl", 300, "TTL in seconds")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *id == "" {
		exitErr(errors.New("--id is required"))
	}

	var caps []string
	if *capsRaw != "" {
		for _, c := range strings.Split(*capsRaw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				caps = append(caps, c)
			}
		}
	}

	payload := map[string]any{
		"id":           *id,
		"addr":         *addr,
		"status":       "healthy",
		"ttl_seconds":  *ttl,
		"capabilities": caps,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		exitErr(fmt.Errorf("marshal payload: %w", err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	url := strings.TrimRight(*serverURL, "/") + "/agents/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		exitErr(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(*apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+*apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		exitErr(fmt.Errorf("agent-register failed: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(respBody)))
	}
	var agent map[string]any
	if err := json.Unmarshal(respBody, &agent); err != nil {
		fmt.Println(string(respBody))
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(agent)
	fmt.Printf("agent %q registered successfully\n", *id)
}

// ---------------------------------------------------------------------------
// chain-verify: verify chain integrity via the HTTP API
// ---------------------------------------------------------------------------

func chainVerifyCmd(args []string) {
	fs := flag.NewFlagSet("chain-verify", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	url := strings.TrimRight(*serverURL, "/") + "/blockchain/verify"
	body, status, err := apiGET(url, *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("chain-verify: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println(string(body))
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if valid, _ := result["valid"].(bool); !valid {
		fmt.Fprintf(os.Stderr, "chain integrity check FAILED (first bad block at index %v)\n", result["bad_index"])
		os.Exit(2)
	}
	fmt.Println("chain integrity OK")
}

// ---------------------------------------------------------------------------
// blockchain-export: download chain JSON from the HTTP API
// ---------------------------------------------------------------------------

func blockchainExportCmd(args []string) {
	fs := flag.NewFlagSet("blockchain-export", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	outPath := fs.String("out", "", "output file path (stdout if empty)")
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	url := strings.TrimRight(*serverURL, "/") + "/blockchain/export"
	body, status, err := apiGET(url, *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("blockchain-export: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	if *outPath == "" {
		fmt.Println(string(body))
		return
	}
	if err := os.WriteFile(*outPath, body, 0o600); err != nil {
		exitErr(fmt.Errorf("write %s: %w", *outPath, err))
	}
	fmt.Printf("blockchain exported to %s\n", *outPath)
}

// ---------------------------------------------------------------------------
// routes-export: download routes JSON from the HTTP API
// ---------------------------------------------------------------------------

func routesExportCmd(args []string) {
	fs := flag.NewFlagSet("routes-export", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	outPath := fs.String("out", "", "output file path (stdout if empty)")
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	url := strings.TrimRight(*serverURL, "/") + "/routes/export"
	body, status, err := apiGET(url, *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("routes-export: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	if *outPath == "" {
		fmt.Println(string(body))
		return
	}
	if err := os.WriteFile(*outPath, body, 0o600); err != nil {
		exitErr(fmt.Errorf("write %s: %w", *outPath, err))
	}
	fmt.Printf("routes exported to %s\n", *outPath)
}

// ---------------------------------------------------------------------------
// tenant-list: list tenants via the HTTP API
// ---------------------------------------------------------------------------

func tenantListCmd(args []string) {
	fs := flag.NewFlagSet("tenant-list", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	body, status, err := apiGET(strings.TrimRight(*serverURL, "/")+"/admin/tenants", *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("tenant-list: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	var out any
	_ = json.Unmarshal(body, &out)
	_ = enc.Encode(out)
}

// ---------------------------------------------------------------------------
// tenant-create: create a new tenant via the HTTP API
// ---------------------------------------------------------------------------

func tenantCreateCmd(args []string) {
	fs := flag.NewFlagSet("tenant-create", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	name := fs.String("name", "", "tenant name (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *name == "" {
		exitErr(errors.New("--name is required"))
	}
	payload, _ := json.Marshal(map[string]string{"name": *name})
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(*serverURL, "/")+"/admin/tenants", bytes.NewReader(payload))
	if err != nil {
		exitErr(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(*apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+*apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		exitErr(fmt.Errorf("tenant-create: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(respBody)))
	}
	var out any
	_ = json.Unmarshal(respBody, &out)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	fmt.Printf("tenant %q created\n", *name)
}

// ---------------------------------------------------------------------------
// tenant-delete: delete a tenant via the HTTP API
// ---------------------------------------------------------------------------

func tenantDeleteCmd(args []string) {
	fs := flag.NewFlagSet("tenant-delete", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	name := fs.String("name", "", "tenant name (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *name == "" {
		exitErr(errors.New("--name is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	u := strings.TrimRight(*serverURL, "/") + "/admin/tenants?name=" + *name
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		exitErr(fmt.Errorf("build request: %w", err))
	}
	if strings.TrimSpace(*apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+*apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		exitErr(fmt.Errorf("tenant-delete: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		exitErr(fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(body)))
	}
	fmt.Printf("tenant %q deleted\n", *name)
}

// ---------------------------------------------------------------------------
// chain-search: search blockchain transactions via the HTTP API
// ---------------------------------------------------------------------------

func chainSearchCmd(args []string) {
	fs := flag.NewFlagSet("chain-search", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	sender := fs.String("sender", "", "sender address filter")
	recipient := fs.String("recipient", "", "recipient address filter")
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *sender == "" && *recipient == "" {
		exitErr(errors.New("--sender or --recipient is required"))
	}
	u := strings.TrimRight(*serverURL, "/") + "/blockchain/search?"
	params := []string{}
	if *sender != "" {
		params = append(params, "sender="+*sender)
	}
	if *recipient != "" {
		params = append(params, "recipient="+*recipient)
	}
	u += strings.Join(params, "&")
	body, status, err := apiGET(u, *apiKey, *timeout)
	if err != nil {
		exitErr(fmt.Errorf("chain-search: %w", err))
	}
	if status != 200 {
		exitErr(fmt.Errorf("server returned HTTP %d: %s", status, string(body)))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	var out any
	_ = json.Unmarshal(body, &out)
	_ = enc.Encode(out)
}

// ---------------------------------------------------------------------------
// agent-status: update an agent's status via the HTTP API
// ---------------------------------------------------------------------------

func agentStatusCmd(args []string) {
	fs := flag.NewFlagSet("agent-status", flag.ExitOnError)
	serverURL := fs.String("url", "", "base URL of the spectral-cloud server")
	apiKey := fs.String("api-key", "", "API key")
	id := fs.String("id", "", "agent ID (required)")
	statusStr := fs.String("status", "", "new status: healthy|degraded|unknown (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(args)
	if *serverURL == "" {
		exitErr(errors.New("--url is required"))
	}
	if *id == "" {
		exitErr(errors.New("--id is required"))
	}
	if *statusStr == "" {
		exitErr(errors.New("--status is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	u := strings.TrimRight(*serverURL, "/") + "/agents/status?id=" + *id + "&status=" + *statusStr
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		exitErr(fmt.Errorf("build request: %w", err))
	}
	if strings.TrimSpace(*apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+*apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		exitErr(fmt.Errorf("agent-status: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		exitErr(fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(body)))
	}
	fmt.Printf("agent %q status updated to %q\n", *id, *statusStr)
}

// ---------------------------------------------------------------------------
// HTTP helpers for CLI commands
// ---------------------------------------------------------------------------

func apiGET(url, apiKey string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
