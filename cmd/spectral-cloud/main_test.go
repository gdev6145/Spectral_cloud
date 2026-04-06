package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

func makeHandler(db *store.Store, counter *prometheus.CounterVec, auth authConfig, status *statusTracker, meshNode *mesh.Node) http.Handler {
	if auth.defaultTenant == "" {
		auth.defaultTenant = "default"
	}
	tenantMgr := newTenantManager(db)
	_, _ = tenantMgr.getTenant(auth.defaultTenant)
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mesh_packets_total_h", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_mesh_reject_rate_h", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_mesh_anomaly_h", Help: "test"}, []string{"type"})
	durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_request_duration_h", Help: "test"}, []string{"path", "method"})
	anomalyState := &meshAnomalyState{}
	agentReg := agent.NewRegistry()
	return newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist, auth, 100, 200, 0, 0, tenantLimits{}, status, meshNode, anomalyState, agentReg, corsConfig{}, false, nil, nil, "")
}

func TestHealthEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

func TestReadyEndpoint(t *testing.T) {
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_ready", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/ready")
	if err != nil {
		t.Fatalf("ready request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("expected status ready, got %v", body["status"])
	}
}

func TestBlockchainAddEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_2", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString("not-json"))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	valid := `[{"sender":"a","recipient":"b","amount":1}]`
	resp, err = http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(valid))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestRoutesEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_3", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/routes?destination=node-1&latency=10&throughput=100&ttlSeconds=bad", "text/plain", nil)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/routes?destination=node-1&latency=10&throughput=100", "text/plain", nil)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/routes")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_4", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{
		apiKey:        "secret",
		defaultTenant: "default",
		publicRules:   []pathRule{{value: "/health"}},
	}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/routes")
	if err != nil {
		t.Fatalf("routes request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/routes", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("auth request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestStatusEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_5", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	status := &statusTracker{
		startedAt:           time.Now().UTC(),
		backupInterval:      "1h",
		compactInterval:     "6h",
		backupDir:           "/tmp/backups",
		compactionDir:       "/tmp/compactions",
		backupRetention:     2,
		compactionRetention: 2,
	}

	cidrs, err := parseCIDRList("127.0.0.1/32")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{
		apiKey:        "secret",
		defaultTenant: "default",
		publicRules:   []pathRule{{value: "/health"}},
		adminRules:    []pathRule{{value: "/admin/status"}},
		adminCIDRs:    cidrs,
	}, status, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/status", nil)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestProtoDataEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_9", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Run("valid", func(t *testing.T) {
		msg := &meshpb.DataMessage{
			MsgType:       meshpb.DataMessage_DATA,
			SourceId:      1,
			DestinationId: 2,
			Timestamp:     time.Now().UTC().Unix(),
			Payload:       []byte("hello"),
		}
		raw, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		resp, err := http.Post(srv.URL+"/proto/data", "application/x-protobuf", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var ack meshpb.Ack
		if err := proto.Unmarshal(out, &ack); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ack.SourceId != 2 || ack.DestinationId != 1 {
			t.Fatalf("expected source/dest swapped, got %d/%d", ack.SourceId, ack.DestinationId)
		}
	})

	t.Run("invalid-msg-type", func(t *testing.T) {
		msg := &meshpb.DataMessage{
			MsgType:       meshpb.DataMessage_ACK,
			SourceId:      1,
			DestinationId: 2,
			Timestamp:     time.Now().UTC().Unix(),
		}
		raw, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		resp, err := http.Post(srv.URL+"/proto/data", "application/x-protobuf", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestProtoControlEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_10", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	msg := &meshpb.ControlMessage{
		MsgType:     meshpb.DataMessage_DATA,
		ControlType: meshpb.ControlMessage_HEARTBEAT,
		NodeId:      42,
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+"/proto/control", "application/x-protobuf", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ack meshpb.Ack
	if err := proto.Unmarshal(out, &ack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ack.SourceId != 42 || ack.DestinationId != 42 {
		t.Fatalf("expected ack for node 42, got %d/%d", ack.SourceId, ack.DestinationId)
	}
}

func TestPublicPathsAllowUnauth(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_6", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth := authConfig{
		apiKey:        "secret",
		defaultTenant: "default",
		publicRules: []pathRule{
			{value: "/metrics"},
			{value: "/health"},
			{value: "/routes", method: http.MethodGet},
		},
	}
	handler := makeHandler(db, counter, auth, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/routes")
	if err != nil {
		t.Fatalf("routes request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/routes?destination=node-1&latency=1&throughput=2", "text/plain", nil)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWriteKeyEnforced(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_8", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth := authConfig{
		apiKey:        "readkey",
		writeKey:      "writekey",
		defaultTenant: "default",
		publicRules: []pathRule{
			{value: "/health"},
		},
	}
	handler := makeHandler(db, counter, auth, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-1&latency=1&throughput=2", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer readkey")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-2&latency=1&throughput=2", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer writekey")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestAdminWriteKey(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_7", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth := authConfig{
		apiKey:        "secret",
		adminWriteKey: "adminwrite",
		defaultTenant: "default",
		adminRules: []pathRule{
			{value: "/routes", method: http.MethodPost},
		},
	}
	handler := makeHandler(db, counter, auth, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/routes?destination=node-1&latency=1&throughput=2", "text/plain", nil)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-2&latency=1&throughput=2", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer adminwrite")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestPersistenceBackwardCompat(t *testing.T) {
	tmp := t.TempDir()
	chainPath := tmp + "/blockchain.json"
	routesPath := tmp + "/routes.json"

	legacyBlocks := `[{"index":0,"timestamp":"2026-04-03 17:40:29","transactions":null,"previousHash":"","hash":"abc"}]`
	if err := os.WriteFile(chainPath, []byte(legacyBlocks), 0o644); err != nil {
		t.Fatalf("write legacy blocks: %v", err)
	}
	legacyRoutes := `[{"destination":"node-1","metric":{"latency":1,"throughput":2},"addedAt":"2026-04-03T00:00:00Z"}]`
	if err := os.WriteFile(routesPath, []byte(legacyRoutes), 0o644); err != nil {
		t.Fatalf("write legacy routes: %v", err)
	}

	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrateLegacyJSON(tmp, db, "default", 2); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	blocks, err := db.ReadBlocksTenant("default")
	if err != nil {
		t.Fatalf("read blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	routes, err := db.ReadRoutesTenant("default")
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
}

func TestDeleteRouteEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_del", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add a route first.
	resp, err := http.Post(srv.URL+"/routes?destination=node-del&latency=5&throughput=50", "text/plain", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Delete it.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes?destination=node-del", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Confirm it's gone.
	resp, err = http.Get(srv.URL + "/routes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var routes []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&routes)
	resp.Body.Close()
	for _, r := range routes {
		if r["destination"] == "node-del" {
			t.Fatal("deleted route still present in list")
		}
	}
}

func TestRoutesBestEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_best", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// No routes yet → 404.
	resp, err := http.Get(srv.URL + "/routes/best")
	if err != nil {
		t.Fatalf("get best: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when no routes, got %d", resp.StatusCode)
	}

	// Add two routes; the one with lower latency should win.
	_, _ = http.Post(srv.URL+"/routes?destination=slow&latency=100&throughput=10", "text/plain", nil)
	_, _ = http.Post(srv.URL+"/routes?destination=fast&latency=10&throughput=10", "text/plain", nil)

	resp, err = http.Get(srv.URL + "/routes/best")
	if err != nil {
		t.Fatalf("get best: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var best map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&best)
	resp.Body.Close()
	if best["destination"] != "fast" {
		t.Fatalf("expected fast route, got %v", best["destination"])
	}
}

func TestBlockchainListEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_bclist", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add a block.
	resp, err := http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[{"sender":"a","recipient":"b","amount":1}]`))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/blockchain?limit=10&offset=0")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["height"].(float64) < 2 {
		t.Fatalf("expected height >= 2, got %v", body["height"])
	}

	resp, err = http.Get(srv.URL + "/blockchain/height")
	if err != nil {
		t.Fatalf("height: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var h map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if h["height"].(float64) < 2 {
		t.Fatalf("expected height >= 2 from /blockchain/height, got %v", h["height"])
	}
}

func TestAgentEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_agents", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// List should be empty.
	resp, err := http.Get(srv.URL + "/agents")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var list []any
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d agents", len(list))
	}

	// Register an agent.
	body := `{"id":"agent-1","addr":"10.0.0.1:9000","status":"healthy","ttl_seconds":300}`
	resp, err = http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var registered map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&registered)
	resp.Body.Close()
	if registered["id"] != "agent-1" {
		t.Fatalf("expected id agent-1, got %v", registered["id"])
	}

	// List should now have one agent.
	resp, err = http.Get(srv.URL + "/agents")
	if err != nil {
		t.Fatalf("list after register: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(list))
	}

	// Heartbeat.
	resp, err = http.Post(srv.URL+"/agents/heartbeat?id=agent-1&ttl_seconds=300", "text/plain", nil)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Deregister.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/agents?id=agent-1", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// List should be empty again.
	resp, err = http.Get(srv.URL + "/agents")
	if err != nil {
		t.Fatalf("list after deregister: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 0 {
		t.Fatalf("expected empty list after deregister, got %d agents", len(list))
	}
}

func TestRequestIDHeader(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_reqid", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Server should generate a request ID when none is provided.
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID header in response")
	}

	// Client-supplied ID should be echoed back.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	req.Header.Set("X-Request-ID", "my-test-id-123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get with id: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Request-ID") != "my-test-id-123" {
		t.Fatalf("expected echoed request ID, got %q", resp.Header.Get("X-Request-ID"))
	}
}

func TestPersistenceRejectsInvalidBlocks(t *testing.T) {
	tmp := t.TempDir()
	chainPath := tmp + "/blockchain.json"
	payload := `{
  "version": 1,
  "updated_at": "2026-04-03T00:00:00Z",
  "blocks": [
    {"index":0,"timestamp":"bad","transactions":null,"previousHash":"","hash":"bad"},
    {"index":1,"timestamp":"bad","transactions":null,"previousHash":"bad","hash":"bad"}
  ]
}`
	if err := os.WriteFile(chainPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write bad blocks: %v", err)
	}
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrateLegacyJSON(tmp, db, "default", 2); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	blocks, err := db.ReadBlocksTenant("default")
	if err != nil {
		t.Fatalf("read blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 valid block (genesis), got %d", len(blocks))
	}
}
