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

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

func makeHandler(chain *blockchain.Blockchain, router *routing.RoutingEngine, db *store.Store, counter *prometheus.CounterVec, auth authConfig, status *statusTracker, meshNode *mesh.Node) http.Handler {
	meshCounter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mesh_packets_total", Help: "test"}, []string{"outcome"})
	meshReject := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_mesh_reject_rate", Help: "test"})
	meshAnom := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_mesh_anomaly", Help: "test"}, []string{"type"})
	anomalyState := &meshAnomalyState{}
	return newHandler(chain, router, db, 1<<20, counter, meshCounter, meshReject, meshAnom, auth, 100, 200, status, meshNode, anomalyState)
}

func TestHealthEndpoint(t *testing.T) {
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

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

func TestBlockchainAddEndpoint(t *testing.T) {
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_2", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_3", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_4", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{
		apiKey:      "secret",
		publicRules: []pathRule{{value: "/health"}},
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
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
	handler := makeHandler(chain, router, db, counter, authConfig{
		apiKey:      "secret",
		publicRules: []pathRule{{value: "/health"}},
		adminRules:  []pathRule{{value: "/admin/status"}},
		adminCIDRs:  cidrs,
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_9", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

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
		var ack meshpb.DataMessage
		if err := proto.Unmarshal(out, &ack); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ack.MsgType != meshpb.DataMessage_ACK {
			t.Fatalf("expected ACK, got %v", ack.MsgType)
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_10", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(chain, router, db, counter, authConfig{}, nil, nil)

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
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
	if body["control_type"] != "HEARTBEAT" {
		t.Fatalf("expected control_type HEARTBEAT, got %v", body["control_type"])
	}
	if body["node_id"] != float64(42) {
		t.Fatalf("expected node_id 42, got %v", body["node_id"])
	}
}

func TestPublicPathsAllowUnauth(t *testing.T) {
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_6", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth := authConfig{
		apiKey: "secret",
		publicRules: []pathRule{
			{value: "/metrics"},
			{value: "/health"},
			{value: "/routes", method: http.MethodGet},
		},
	}
	handler := makeHandler(chain, router, db, counter, auth, nil, nil)
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_8", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auth := authConfig{
		apiKey:   "readkey",
		writeKey: "writekey",
		publicRules: []pathRule{
			{value: "/health"},
		},
	}
	handler := makeHandler(chain, router, db, counter, auth, nil, nil)
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
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
		adminRules: []pathRule{
			{value: "/routes", method: http.MethodPost},
		},
	}
	handler := makeHandler(chain, router, db, counter, auth, nil, nil)
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

	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrateLegacyJSON(tmp, db, chain, router, 2); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	if chain.Height() != 1 {
		t.Fatalf("expected height 1, got %d", chain.Height())
	}
	if router.RouteCount() != 1 {
		t.Fatalf("expected 1 route, got %d", router.RouteCount())
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
	chain := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrateLegacyJSON(tmp, db, chain, router, 2); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	if chain.Height() != 1 {
		t.Fatalf("expected height 1 (genesis), got %d", chain.Height())
	}
}
