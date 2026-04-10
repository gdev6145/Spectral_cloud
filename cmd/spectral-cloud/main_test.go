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
	"github.com/gdev6145/Spectral_cloud/pkg/agentgroup"
	"github.com/gdev6145/Spectral_cloud/pkg/circuit"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
	"github.com/gdev6145/Spectral_cloud/pkg/kv"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	"github.com/gdev6145/Spectral_cloud/pkg/mq"
	"github.com/gdev6145/Spectral_cloud/pkg/notify"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/scheduler"
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
	broker := events.NewBroker()
	kvStore := kv.New()
	notifyMgr := notify.New()
	jq := jobs.NewQueue()
	circuitMgr := circuit.New(5, 30*time.Second)
	groupMgr := agentgroup.New()
	sched := scheduler.New(jq)
	mqQueue := mq.New()
	return newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist, auth, 100, 200, 0, 0, tenantLimits{}, status, meshNode, anomalyState, agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kvStore, notifyMgr, circuitMgr, groupMgr, sched, mqQueue)
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
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_ready", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)

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

func TestEventHistoryEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_evhist", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add a block to generate an event.
	_, _ = http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[{"sender":"a","recipient":"b","amount":1}]`))

	resp, err := http.Get(srv.URL + "/events/history?limit=10")
	if err != nil {
		t.Fatalf("events history: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if _, ok := body["count"]; !ok {
		t.Fatal("expected count field in history response")
	}
	if _, ok := body["events"]; !ok {
		t.Fatal("expected events field in history response")
	}
}

func TestRoutesBatchDeleteEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_rdel", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add three routes.
	for _, dest := range []string{"batch-del-1", "batch-del-2", "batch-del-3"} {
		resp, err := http.Post(srv.URL+"/routes?destination="+dest+"&latency=5&throughput=50", "text/plain", nil)
		if err != nil {
			t.Fatalf("add route: %v", err)
		}
		resp.Body.Close()
	}

	// Batch delete two of them.
	body := `["batch-del-1","batch-del-2","does-not-exist"]`
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["deleted"].(float64) != 2 {
		t.Fatalf("expected deleted=2, got %v", result["deleted"])
	}
	if result["not_found"].(float64) != 1 {
		t.Fatalf("expected not_found=1, got %v", result["not_found"])
	}
}

func TestAgentGetByID(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_aggetid", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register an agent.
	_, _ = http.Post(srv.URL+"/agents/register", "application/json",
		bytes.NewBufferString(`{"id":"single-agent","addr":"10.0.0.1:9000","status":"healthy","ttl_seconds":300}`))

	// Fetch by ID.
	resp, err := http.Get(srv.URL + "/agents?id=single-agent")
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var a map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&a)
	resp.Body.Close()
	if a["id"] != "single-agent" {
		t.Fatalf("expected id=single-agent, got %v", a["id"])
	}

	// Non-existent → 404.
	resp, err = http.Get(srv.URL + "/agents?id=no-such-agent")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentStatusEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_agstat", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register an agent.
	_, _ = http.Post(srv.URL+"/agents/register", "application/json",
		bytes.NewBufferString(`{"id":"status-agent","addr":"10.0.0.1:9001","status":"healthy","ttl_seconds":300}`))

	// Update its status.
	resp, err := http.Post(srv.URL+"/agents/status?id=status-agent&status=degraded", "text/plain", nil)
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Confirm the status changed.
	resp, err = http.Get(srv.URL + "/agents?id=status-agent")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	var a map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&a)
	resp.Body.Close()
	if a["status"] != "degraded" {
		t.Fatalf("expected status=degraded, got %v", a["status"])
	}

	// Missing id → 400.
	resp, err = http.Post(srv.URL+"/agents/status?status=healthy", "text/plain", nil)
	if err != nil {
		t.Fatalf("status no id: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestBlockchainVerifyEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_verify", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add a block then verify.
	_, _ = http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[{"sender":"x","recipient":"y","amount":1}]`))

	resp, err := http.Get(srv.URL + "/blockchain/verify")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if valid, _ := body["valid"].(bool); !valid {
		t.Fatalf("expected chain to be valid, got %v", body)
	}
}

func TestBlockchainSearchEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_search", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[{"sender":"alice","recipient":"bob","amount":10}]`))
	_, _ = http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[{"sender":"carol","recipient":"alice","amount":5}]`))

	// Missing both params → 400.
	resp, err := http.Get(srv.URL + "/blockchain/search")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Filter by sender.
	resp, err = http.Get(srv.URL + "/blockchain/search?sender=alice")
	if err != nil {
		t.Fatalf("search sender: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["count"].(float64) != 1 {
		t.Fatalf("expected count=1, got %v", result["count"])
	}
}

func TestBlockchainExportEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_bcexp", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/blockchain/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["version"].(float64) != 1 {
		t.Fatalf("expected version=1, got %v", body["version"])
	}
	if body["blocks"] == nil {
		t.Fatal("expected blocks field in export")
	}
	if resp.Header.Get("Content-Disposition") == "" {
		t.Fatal("expected Content-Disposition header for export")
	}
}

func TestRoutesExportEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_rexp", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=export-node&latency=5&throughput=50", "text/plain", nil)

	resp, err := http.Get(srv.URL + "/routes/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["version"].(float64) != 1 {
		t.Fatalf("expected version=1, got %v", body["version"])
	}
}

func TestRoutesBatchEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_rbatch", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	batch := `[{"destination":"n1","latency":10,"throughput":100},{"destination":"n2","latency":20,"throughput":200}]`
	resp, err := http.Post(srv.URL+"/routes/batch", "application/json", bytes.NewBufferString(batch))
	if err != nil {
		t.Fatalf("batch post: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["added"].(float64) != 2 {
		t.Fatalf("expected added=2, got %v", result["added"])
	}

	// Empty batch → 400.
	resp, err = http.Post(srv.URL+"/routes/batch", "application/json", bytes.NewBufferString("[]"))
	if err != nil {
		t.Fatalf("batch empty: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty batch, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMeshPeersEndpointDisabled(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_peers", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/mesh/peers")
	if err != nil {
		t.Fatalf("mesh peers: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["status"] != "disabled" {
		t.Fatalf("expected status=disabled when no mesh node, got %v", body)
	}
}

func TestAdminTenantsCreateDelete(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_tenants", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Create a tenant.
	body := `{"name":"new-tenant"}`
	resp, err := http.Post(srv.URL+"/admin/tenants", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created["tenant"] != "new-tenant" {
		t.Fatalf("expected tenant=new-tenant, got %v", created["tenant"])
	}

	// Delete it.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/tenants?name=new-tenant", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Delete non-existent → 404.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/tenants?name=new-tenant", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete missing tenant: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
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

func TestAuditEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_audit_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Trigger a write to populate the audit log.
	resp, err := http.Post(srv.URL+"/blockchain/add", "application/json", bytes.NewBufferString(`[]`))
	if err != nil {
		t.Fatalf("add block: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatalf("GET /audit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Count   int `json:"count"`
		Entries []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count == 0 {
		t.Fatal("expected at least 1 audit entry")
	}
}

func TestAgentRouteEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_agent_route_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register an agent with a capability.
	body := `{"id":"cap-agent","capabilities":["inference"],"status":"healthy","ttl_seconds":300}`
	resp, err := http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/agents/route?capability=inference")
	if err != nil {
		t.Fatalf("GET /agents/route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var res struct {
		Count  int `json:"count"`
		Agents []struct{ ID string `json:"id"` } `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Count != 1 || res.Agents[0].ID != "cap-agent" {
		t.Errorf("unexpected route result: %+v", res)
	}
}

func TestJobsEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_jobs_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// makeHandler passes nil jobQueue by default; create handler with job queue.
	tenantMgr := newTenantManager(db)
	_, _ = tenantMgr.getTenant("default")
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_jobs_mesh_total", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_jobs_mesh_reject", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_jobs_mesh_anom", Help: "test"}, []string{"type"})
	durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_jobs_dur", Help: "test"}, []string{"path", "method"})
	agentReg := agent.NewRegistry()
	broker := events.NewBroker()
	jq := jobs.NewQueue()
	handler := newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist,
		authConfig{defaultTenant: "default"}, 100, 200, 0, 0, tenantLimits{}, nil, nil, &meshAnomalyState{},
		agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), mq.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Pre-register an agent with the "inference" capability so auto-dispatch works.
	regBody := `{"id":"agent-1","addr":"127.0.0.1:9000","status":"healthy","capabilities":["inference"]}`
	regResp, err := http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(regBody))
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	_ = regResp.Body.Close()
	if regResp.StatusCode != 201 {
		t.Fatalf("expected 201 from register, got %d", regResp.StatusCode)
	}

	// Submit a job.
	jobBody := `{"capability":"inference","payload":{"prompt":"test"}}`
	resp, err := http.Post(srv.URL+"/agents/jobs", "application/json", bytes.NewBufferString(jobBody))
	if err != nil {
		t.Fatalf("POST /agents/jobs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var submitted struct{ ID string `json:"id"` }
	if err := json.NewDecoder(resp.Body).Decode(&submitted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if submitted.ID == "" {
		t.Fatal("expected job ID")
	}

	// List jobs.
	resp2, err := http.Get(srv.URL + "/agents/jobs")
	if err != nil {
		t.Fatalf("GET /agents/jobs: %v", err)
	}
	defer resp2.Body.Close()
	var listed struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&listed)
	if listed.Count != 1 {
		t.Errorf("expected 1 job, got %d", listed.Count)
	}

	// Update job status.
	patchBody := `{"status":"done","result":"42"}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/agents/jobs/"+submitted.ID,
		bytes.NewBufferString(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}
	var updated struct {
		Status string `json:"status"`
		Result string `json:"result"`
	}
	_ = json.NewDecoder(resp3.Body).Decode(&updated)
	if updated.Status != "done" || updated.Result != "42" {
		t.Errorf("unexpected update: %+v", updated)
	}
}

func TestJobsClaimAndCancel(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_claim_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantMgr := newTenantManager(db)
	_, _ = tenantMgr.getTenant("default")
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_claim_mesh_total", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_claim_mesh_reject", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_claim_mesh_anom", Help: "test"}, []string{"type"})
	durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_claim_dur", Help: "test"}, []string{"path", "method"})
	agentReg := agent.NewRegistry()
	broker := events.NewBroker()
	jq := jobs.NewQueue()
	handler := newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist,
		authConfig{defaultTenant: "default"}, 100, 200, 0, 0, tenantLimits{}, nil, nil, &meshAnomalyState{},
		agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), mq.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register an agent.
	regBody := `{"id":"worker-1","addr":"10.0.0.1:9000","status":"healthy","capabilities":["ocr"]}`
	rr, _ := http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(regBody))
	_ = rr.Body.Close()

	// Submit a job — auto-dispatched to worker-1.
	jResp, _ := http.Post(srv.URL+"/agents/jobs", "application/json",
		bytes.NewBufferString(`{"capability":"ocr","payload":{"file":"doc.pdf"}}`))
	var submitted struct{ ID string `json:"id"` }
	_ = json.NewDecoder(jResp.Body).Decode(&submitted)
	_ = jResp.Body.Close()
	if submitted.ID == "" {
		t.Fatal("expected job ID from auto-dispatch")
	}

	// Submit a second job for cancel test.
	jResp2, _ := http.Post(srv.URL+"/agents/jobs", "application/json",
		bytes.NewBufferString(`{"capability":"ocr"}`))
	var submitted2 struct{ ID string `json:"id"` }
	_ = json.NewDecoder(jResp2.Body).Decode(&submitted2)
	_ = jResp2.Body.Close()

	// Agent claims the oldest pending job.
	claimResp, err := http.Get(srv.URL + "/agents/jobs/claim?agent_id=worker-1")
	if err != nil {
		t.Fatalf("claim GET: %v", err)
	}
	defer claimResp.Body.Close()
	if claimResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from claim, got %d", claimResp.StatusCode)
	}
	var claimed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(claimResp.Body).Decode(&claimed)
	if claimed.Status != "running" {
		t.Fatalf("expected running after claim, got %s", claimed.Status)
	}

	// No more matching pending jobs for worker-1 (second job not yet submitted to same agent).
	// Cancel the second job.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/agents/jobs/"+submitted2.ID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /agents/jobs: %v", err)
	}
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from cancel, got %d", delResp.StatusCode)
	}

	// Cancelling again (already cancelled) should return 404.
	req2, _ := http.NewRequest(http.MethodDelete, srv.URL+"/agents/jobs/"+submitted2.ID, nil)
	del2, _ := http.DefaultClient.Do(req2)
	_ = del2.Body.Close()
	if del2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on double-cancel, got %d", del2.StatusCode)
	}
}

func TestJobsClaim_NoPendingJobs(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_noclaim_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	tenantMgr := newTenantManager(db)
	_, _ = tenantMgr.getTenant("default")
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_noclaim_mesh_total", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_noclaim_mesh_reject", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_noclaim_mesh_anom", Help: "test"}, []string{"type"})
	durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_noclaim_dur", Help: "test"}, []string{"path", "method"})
	jq := jobs.NewQueue()
	handler := newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist,
		authConfig{defaultTenant: "default"}, 100, 200, 0, 0, tenantLimits{}, nil, nil, &meshAnomalyState{},
		agent.NewRegistry(), corsConfig{}, false, events.NewBroker(), nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), mq.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/agents/jobs/claim?agent_id=nobody")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 when no jobs, got %d", resp.StatusCode)
	}
}

func TestJobsAutoDispatch_NoAgent(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_nodispatch_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	tenantMgr := newTenantManager(db)
	_, _ = tenantMgr.getTenant("default")
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_nodispatch_mesh_total", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_nodispatch_mesh_reject", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_nodispatch_mesh_anom", Help: "test"}, []string{"type"})
	durHist := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "test_nodispatch_dur", Help: "test"}, []string{"path", "method"})
	jq := jobs.NewQueue()
	handler := newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist,
		authConfig{defaultTenant: "default"}, 100, 200, 0, 0, tenantLimits{}, nil, nil, &meshAnomalyState{},
		agent.NewRegistry(), corsConfig{}, false, events.NewBroker(), nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), mq.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// No agents registered — capability-only submit should fail with 503.
	resp, _ := http.Post(srv.URL+"/agents/jobs", "application/json",
		bytes.NewBufferString(`{"capability":"gpu-inference"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no agent available, got %d", resp.StatusCode)
	}
}

func TestMetricsJSONEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_metrics_json_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/metrics/json")
	if err != nil {
		t.Fatalf("GET /metrics/json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var snap struct {
		Timestamp string `json:"timestamp"`
		Jobs      int    `json:"jobs"`
		Tenants   []struct {
			Tenant string `json:"tenant"`
		} `json:"tenants"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Timestamp == "" {
		t.Error("expected timestamp")
	}
	if len(snap.Tenants) == 0 {
		t.Error("expected at least one tenant")
	}
}

func TestBlockchainImportEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bc_import_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Export current chain (genesis only).
	exportResp, err := http.Get(srv.URL + "/blockchain/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	exportBody, _ := io.ReadAll(exportResp.Body)
	exportResp.Body.Close()

	// Import it back — should be a no-op (all blocks already exist).
	importResp, err := http.Post(srv.URL+"/blockchain/import", "application/json", bytes.NewReader(exportBody))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer importResp.Body.Close()
	if importResp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", importResp.StatusCode)
	}
	var result struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := json.NewDecoder(importResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// All blocks already exist — should skip all.
	if result.Imported != 0 {
		t.Errorf("expected 0 imported, got %d", result.Imported)
	}
}

func TestRoutesImportEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_rt_import_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Add a route then export it.
	http.Post(srv.URL+"/routes?destination=import-test&latency=10&throughput=500", "text/plain", nil)
	exportResp, _ := http.Get(srv.URL + "/routes/export")
	exportBody, _ := io.ReadAll(exportResp.Body)
	exportResp.Body.Close()

	// Delete original route.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes?destination=import-test", nil)
	http.DefaultClient.Do(req)

	// Import it back.
	importResp, err := http.Post(srv.URL+"/routes/import", "application/json", bytes.NewReader(exportBody))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer importResp.Body.Close()
	if importResp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", importResp.StatusCode)
	}
	var result struct {
		Imported int `json:"imported"`
		Total    int `json:"total"`
	}
	if err := json.NewDecoder(importResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", result.Imported)
	}
}

func TestKVEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_kv_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/kv/mykey", bytes.NewBufferString(`{"value":"hello","ttl":0}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT expected 200, got %d", resp.StatusCode)
	}

	gr, _ := http.Get(srv.URL + "/kv/mykey")
	defer gr.Body.Close()
	var entry struct{ Value string `json:"value"` }
	json.NewDecoder(gr.Body).Decode(&entry)
	if entry.Value != "hello" {
		t.Errorf("expected hello, got %s", entry.Value)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/kv/mykey", nil)
	dr, _ := http.DefaultClient.Do(delReq)
	defer dr.Body.Close()
	if dr.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", dr.StatusCode)
	}
}

func TestSearchEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_search_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	http.Post(srv.URL+"/routes?destination=alpha-node&latency=10&throughput=100", "text/plain", nil)

	resp, _ := http.Get(srv.URL + "/search?q=alpha")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Routes []struct{ Destination string `json:"destination"` } `json:"routes"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Routes) != 1 || result.Routes[0].Destination != "alpha-node" {
		t.Errorf("expected alpha-node, got %+v", result.Routes)
	}
}

func TestNotificationRuleEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_notif_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	body := `{"name":"r1","webhook_url":"http://example.com/hook","event_types":["agent.registered"]}`
	cr, _ := http.Post(srv.URL+"/admin/notifications", "application/json", bytes.NewBufferString(body))
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("POST expected 201, got %d", cr.StatusCode)
	}
	var rule struct{ ID string `json:"id"` }
	json.NewDecoder(cr.Body).Decode(&rule)

	dr, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/notifications/"+rule.ID, nil)
	delResp, _ := http.DefaultClient.Do(dr)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", delResp.StatusCode)
	}
}

func TestCircuitBreakerEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_circuit_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Record a failure.
	fr, _ := http.Post(srv.URL+"/circuit/record", "application/json",
		bytes.NewBufferString(`{"agent_id":"agent-x","success":false}`))
	defer fr.Body.Close()
	if fr.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", fr.StatusCode)
	}
	var b struct {
		State    string `json:"state"`
		Failures int    `json:"failures"`
	}
	json.NewDecoder(fr.Body).Decode(&b)
	if b.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", b.Failures)
	}

	// List breakers.
	lr, _ := http.Get(srv.URL + "/circuit")
	defer lr.Body.Close()
	var list struct{ Count int `json:"count"` }
	json.NewDecoder(lr.Body).Decode(&list)
	if list.Count != 1 {
		t.Errorf("expected 1 breaker, got %d", list.Count)
	}

	// Reset.
	rr, _ := http.Post(srv.URL+"/circuit/reset?agent_id=agent-x", "application/json", nil)
	defer rr.Body.Close()
	var reset struct{ Failures int `json:"failures"` }
	json.NewDecoder(rr.Body).Decode(&reset)
	if reset.Failures != 0 {
		t.Errorf("expected 0 after reset, got %d", reset.Failures)
	}
}

func TestAgentGroupEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_groups_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Create group.
	cr, _ := http.Post(srv.URL+"/agent-groups", "application/json", bytes.NewBufferString(`{"name":"workers"}`))
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("POST expected 201, got %d", cr.StatusCode)
	}
	var g struct{ ID string `json:"id"` }
	json.NewDecoder(cr.Body).Decode(&g)

	// Add member.
	mr, _ := http.Post(srv.URL+"/agent-groups/"+g.ID+"/members", "application/json",
		bytes.NewBufferString(`{"agent_id":"agent-1"}`))
	defer mr.Body.Close()
	if mr.StatusCode != 200 {
		t.Fatalf("add member expected 200, got %d", mr.StatusCode)
	}

	// Get next (round-robin).
	nr, _ := http.Get(srv.URL + "/agent-groups/" + g.ID + "/next")
	defer nr.Body.Close()
	if nr.StatusCode != 200 {
		t.Fatalf("next expected 200, got %d", nr.StatusCode)
	}
	var next struct{ AgentID string `json:"agent_id"` }
	json.NewDecoder(nr.Body).Decode(&next)
	if next.AgentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", next.AgentID)
	}

	// Delete group.
	delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+g.ID, nil)
	dr, _ := http.DefaultClient.Do(delReq)
	defer dr.Body.Close()
	if dr.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", dr.StatusCode)
	}
}

func TestSchedulerEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_sched_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Create schedule.
	body := `{"name":"hourly-sync","capability":"sync","interval_seconds":3600}`
	cr, _ := http.Post(srv.URL+"/schedules", "application/json", bytes.NewBufferString(body))
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("POST expected 201, got %d", cr.StatusCode)
	}
	var s struct{ ID string `json:"id"`; Name string `json:"name"` }
	json.NewDecoder(cr.Body).Decode(&s)
	if s.Name != "hourly-sync" || s.ID == "" {
		t.Errorf("unexpected schedule: %+v", s)
	}

	// List.
	lr, _ := http.Get(srv.URL + "/schedules")
	defer lr.Body.Close()
	var list struct{ Count int `json:"count"` }
	json.NewDecoder(lr.Body).Decode(&list)
	if list.Count != 1 {
		t.Errorf("expected 1, got %d", list.Count)
	}

	// Delete.
	delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/schedules/"+s.ID, nil)
	dr, _ := http.DefaultClient.Do(delReq)
	defer dr.Body.Close()
	if dr.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", dr.StatusCode)
	}
}

// ── Message Queue endpoint tests ─────────────────────────────────────────────

func TestMQPublishAndConsume(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mq_pub_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Publish a message.
	body := `{"payload":{"task":"summarise","doc":"file.txt"}}`
	resp, err := http.Post(srv.URL+"/mq/jobs", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var msg struct {
		ID    string `json:"id"`
		Topic string `json:"topic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		t.Fatalf("decode msg: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("expected non-empty id")
	}
	if msg.Topic != "jobs" {
		t.Fatalf("expected topic 'jobs', got %q", msg.Topic)
	}

	// List topics — should show 1 pending.
	lr, err := http.Get(srv.URL + "/mq")
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	defer lr.Body.Close()
	if lr.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", lr.StatusCode)
	}
	var topicList struct {
		Count  int `json:"count"`
		Topics []struct {
			Topic   string `json:"topic"`
			Pending int    `json:"pending"`
		} `json:"topics"`
	}
	if err := json.NewDecoder(lr.Body).Decode(&topicList); err != nil {
		t.Fatalf("decode topics: %v", err)
	}
	if topicList.Count != 1 || topicList.Topics[0].Pending != 1 {
		t.Fatalf("expected 1 topic with 1 pending, got count=%d", topicList.Count)
	}

	// Consume the message.
	cr, err := http.Get(srv.URL + "/mq/jobs?count=1")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", cr.StatusCode)
	}
	var consumed struct {
		Count    int              `json:"count"`
		Messages []struct{ ID string `json:"id"` } `json:"messages"`
	}
	if err := json.NewDecoder(cr.Body).Decode(&consumed); err != nil {
		t.Fatalf("decode consumed: %v", err)
	}
	if consumed.Count != 1 || consumed.Messages[0].ID != msg.ID {
		t.Fatalf("expected 1 message with id=%s, got count=%d", msg.ID, consumed.Count)
	}
}

func TestMQPurge(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mq_purge_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Publish two messages.
	for i := 0; i < 2; i++ {
		r, _ := http.Post(srv.URL+"/mq/work", "application/json", bytes.NewBufferString(`{"payload":{}}`))
		_ = r.Body.Close()
	}

	// Purge.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/mq/work", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on purge, got %d", resp.StatusCode)
	}
	var result struct{ Purged int `json:"purged"` }
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Purged != 2 {
		t.Fatalf("expected 2 purged, got %d", result.Purged)
	}
}

func TestMQConsumeEmpty(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mq_empty_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/mq/empty-topic")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct{ Count int `json:"count"` }
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Count != 0 {
		t.Fatalf("expected 0 messages, got %d", result.Count)
	}
}

func TestNotificationToggle(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_notif_toggle_req_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Create a notification rule.
	body := `{"name":"test-rule","webhook_url":"http://example.com/hook","event_types":["block_added"]}`
	resp, _ := http.Post(srv.URL+"/admin/notifications", "application/json", bytes.NewBufferString(body))
	var rule struct{ ID string `json:"id"` }
	json.NewDecoder(resp.Body).Decode(&rule)
	_ = resp.Body.Close()
	if rule.ID == "" {
		t.Fatal("expected non-empty rule id")
	}

	// Disable the rule via PATCH.
	patchBody := `{"active":false}`
	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/notifications/"+rule.ID, bytes.NewBufferString(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	pr, _ := http.DefaultClient.Do(patchReq)
	_ = pr.Body.Close()
	if pr.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 on PATCH, got %d", pr.StatusCode)
	}

	// Re-enable the rule.
	patchBody = `{"active":true}`
	patchReq2, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/notifications/"+rule.ID, bytes.NewBufferString(patchBody))
	patchReq2.Header.Set("Content-Type", "application/json")
	pr2, _ := http.DefaultClient.Do(patchReq2)
	_ = pr2.Body.Close()
	if pr2.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 on re-enable PATCH, got %d", pr2.StatusCode)
	}

	// PATCH with unknown id → 404.
	patchReq3, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/notifications/rule-9999", bytes.NewBufferString(`{"active":false}`))
	patchReq3.Header.Set("Content-Type", "application/json")
	pr3, _ := http.DefaultClient.Do(patchReq3)
	_ = pr3.Body.Close()
	if pr3.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown rule, got %d", pr3.StatusCode)
	}
}
