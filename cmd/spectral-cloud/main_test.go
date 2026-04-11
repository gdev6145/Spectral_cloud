package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/agentgroup"
	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"github.com/gdev6145/Spectral_cloud/pkg/billing"
	"github.com/gdev6145/Spectral_cloud/pkg/pipeline"
	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/circuit"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
	"github.com/gdev6145/Spectral_cloud/pkg/kv"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	"github.com/gdev6145/Spectral_cloud/pkg/notify"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
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
	notifyMgr := notify.NewWithStore(db)
	groupMgr := agentgroup.NewWithStore(db)
	jq := jobs.NewQueue()
	sched := scheduler.NewWithStore(jq, db)
	if tenantNames, err := db.TenantNames(); err == nil {
		for _, tn := range tenantNames {
			_, _ = notifyMgr.LoadFromStore(tn)
			_, _ = groupMgr.LoadFromStore(tn)
			_, _ = sched.LoadFromStore(context.Background(), tn)
		}
	}
	circuitMgr := circuit.New(5, 30*time.Second)
	return newHandler(tenantMgr, db, 1<<20, counter, meshCounter, meshReject, meshAnom, durHist, auth, 100, 200, 0, 0, tenantLimits{}, status, meshNode, anomalyState, agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kvStore, notifyMgr, circuitMgr, groupMgr, sched, pipeline.NewWithStore(db), billing.New(db))
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

func TestRouteItemEndpoints(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_route_item_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/routes?destination=node-item&latency=10&throughput=100", "text/plain", nil)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/routes/node-item")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/node-item", bytes.NewBufferString(`{"latency":5,"throughput":250,"ttl_seconds":60,"satellite":true,"tags":{"region":"us"}}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("patch route: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", patchResp.StatusCode)
	}
	var route struct {
		Destination string `json:"destination"`
		Metric      struct {
			Latency    int `json:"latency"`
			Throughput int `json:"throughput"`
		} `json:"metric"`
		Satellite bool              `json:"satellite"`
		Tags      map[string]string `json:"tags"`
		ExpiresAt *time.Time        `json:"expires_at"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&route); err != nil {
		t.Fatalf("decode route: %v", err)
	}
	if route.Destination != "node-item" || route.Metric.Latency != 5 || route.Metric.Throughput != 250 || !route.Satellite {
		t.Fatalf("unexpected updated route: %+v", route)
	}
	if route.Tags["region"] != "us" || route.ExpiresAt == nil {
		t.Fatalf("expected tags and ttl on updated route: %+v", route)
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes/node-item", nil)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("delete route: %v", err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", deleteResp.StatusCode)
	}
}

func TestRouteItemEndpointsAreTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_route_item_tenant_scope_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	createReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-a&latency=1&throughput=2", nil)
	createReq.Header.Set("X-API-Key", "key-a")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}

	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/routes/node-a", nil)
	getReq.Header.Set("X-API-Key", "key-b")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("cross-tenant get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getResp.StatusCode)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/node-a", bytes.NewBufferString(`{"latency":9}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-API-Key", "key-b")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("cross-tenant patch: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", patchResp.StatusCode)
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes/node-a", nil)
	deleteReq.Header.Set("X-API-Key", "key-b")
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", deleteResp.StatusCode)
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

func TestAdminWriteKeyStandalone(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_admin_write_only", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler := makeHandler(db, counter, authConfig{
		adminWriteKey: "adminwrite",
		defaultTenant: "default",
		adminRules: []pathRule{
			{value: "/routes", method: http.MethodPost},
		},
	}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-admin&latency=1&throughput=2", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer adminwrite")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestTenantWriteKeysStandaloneEnforced(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_tenant_write", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantWrite, err := auth.NewManagerFromEnv("tenant-a:tenant-write-key")
	if err != nil {
		t.Fatalf("tenant write manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{
		tenantWrite:   tenantWrite,
		defaultTenant: "default",
		publicRules:   []pathRule{{value: "/health"}},
	}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/routes?destination=node-a&latency=1&throughput=2", "text/plain", nil)
	if err != nil {
		t.Fatalf("routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/routes?destination=node-a&latency=1&throughput=2", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tenant-write-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized routes post failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	routes, err := db.ReadRoutesTenant("tenant-a")
	if err != nil {
		t.Fatalf("read tenant routes: %v", err)
	}
	if len(routes) != 1 || routes[0].Destination != "node-a" {
		t.Fatalf("expected route to be stored for tenant-a, got %+v", routes)
	}
}

func TestAuthWhoAmIEndpoint(t *testing.T) {
	t.Run("requires auth when configured", func(t *testing.T) {
		counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_whoami_auth", Help: "test"}, []string{"path", "method", "code"})
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

		resp, err := http.Get(srv.URL + "/auth/whoami")
		if err != nil {
			t.Fatalf("whoami request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/auth/whoami", nil)
		if err != nil {
			t.Fatalf("new request failed: %v", err)
		}
		req.Header.Set("Authorization", "Bearer secret")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("authorized whoami failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body authWhoAmIResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if !body.Authenticated || body.Access != "api" || body.Tenant != "default" {
			t.Fatalf("unexpected body: %+v", body)
		}
	})

	t.Run("resolves tenant write credentials on get", func(t *testing.T) {
		counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_whoami_tenant_write", Help: "test"}, []string{"path", "method", "code"})
		tmp := t.TempDir()
		db, err := store.Open(store.DBPath(tmp))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		tenantWrite, err := auth.NewManagerFromEnv("tenant-b:tenant-write-key")
		if err != nil {
			t.Fatalf("tenant write manager: %v", err)
		}
		handler := makeHandler(db, counter, authConfig{
			tenantWrite: tenantWrite,
		}, nil, nil)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/auth/whoami", nil)
		if err != nil {
			t.Fatalf("new request failed: %v", err)
		}
		req.Header.Set("X-API-Key", "tenant-write-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("whoami request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body authWhoAmIResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if !body.Authenticated || body.Access != "tenant-write" || body.Tenant != "tenant-b" {
			t.Fatalf("unexpected body: %+v", body)
		}
	})

	t.Run("returns anonymous when auth is disabled", func(t *testing.T) {
		counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_whoami_anon", Help: "test"}, []string{"path", "method", "code"})
		tmp := t.TempDir()
		db, err := store.Open(store.DBPath(tmp))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		handler := makeHandler(db, counter, authConfig{
			defaultTenant: "default",
		}, nil, nil)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/auth/whoami")
		if err != nil {
			t.Fatalf("whoami request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body authWhoAmIResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if body.Authenticated || body.Access != "anonymous" || body.Tenant != "default" {
			t.Fatalf("unexpected body: %+v", body)
		}
	})
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

func TestRoutesBestEndpointWithEdgeFilters(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_best_edge", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=edge-west&latency=15&throughput=80", "text/plain", nil)
	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-west", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-a"}}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("patch edge-west: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", patchResp.StatusCode)
	}

	_, _ = http.Post(srv.URL+"/routes?destination=edge-west-sat&latency=5&throughput=90", "text/plain", nil)
	patchReq, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-west-sat", bytes.NewBufferString(`{"satellite":true,"tags":{"region":"us-west","site":"edge-b"}}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err = http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("patch edge-west-sat: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", patchResp.StatusCode)
	}

	_, _ = http.Post(srv.URL+"/routes?destination=edge-east&latency=2&throughput=120", "text/plain", nil)
	patchReq, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-east", bytes.NewBufferString(`{"tags":{"region":"us-east","site":"edge-c"}}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err = http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("patch edge-east: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", patchResp.StatusCode)
	}

	resp, err := http.Get(srv.URL + "/routes/best?tag=region:us-west")
	if err != nil {
		t.Fatalf("get filtered best: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var best map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&best); err != nil {
		t.Fatalf("decode filtered best: %v", err)
	}
	resp.Body.Close()
	if best["destination"] != "edge-west" {
		t.Fatalf("expected terrestrial west edge route, got %v", best["destination"])
	}

	resp, err = http.Get(srv.URL + "/routes/best?tag=region:us-west&satellite_penalty=0")
	if err != nil {
		t.Fatalf("get zero-penalty best: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&best); err != nil {
		t.Fatalf("decode zero-penalty best: %v", err)
	}
	resp.Body.Close()
	if best["destination"] != "edge-west-sat" {
		t.Fatalf("expected satellite west edge route with zero penalty, got %v", best["destination"])
	}

	resp, err = http.Get(srv.URL + "/routes/best?tag=region:us-west&max_latency=10")
	if err != nil {
		t.Fatalf("get constrained best: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&best); err != nil {
		t.Fatalf("decode constrained best: %v", err)
	}
	resp.Body.Close()
	if best["destination"] != "edge-west-sat" {
		t.Fatalf("expected low-latency west edge route, got %v", best["destination"])
	}

	resp, err = http.Get(srv.URL + "/routes/best?tag=region:eu-central")
	if err != nil {
		t.Fatalf("get missing best: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/routes/best?tag=region:us-west&tag=site:edge-b")
	if err != nil {
		t.Fatalf("get multi-tag best: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&best); err != nil {
		t.Fatalf("decode multi-tag best: %v", err)
	}
	resp.Body.Close()
	if best["destination"] != "edge-west-sat" {
		t.Fatalf("expected exact site route from multi-tag filter, got %v", best["destination"])
	}

	resp, err = http.Get(srv.URL + "/routes/best?tag=invalid")
	if err != nil {
		t.Fatalf("get invalid tag best: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid tag filter, got %d", resp.StatusCode)
	}
}

func TestRoutesResolveEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_resolve_edge", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=west-a&latency=20&throughput=80", "text/plain", nil)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-a", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-a","tier":"standard"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=west-b&latency=10&throughput=90", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-b", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-b","tier":"premium"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=west-c&latency=14&throughput=85", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-c", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-c","tier":"premium"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=global-fast&latency=5&throughput=50", "text/plain", nil)

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&site=edge-a")
	if err != nil {
		t.Fatalf("resolve site: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var resolved struct {
		Scope string         `json:"scope"`
		Route map[string]any `json:"route"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode site resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "site" || resolved.Route["destination"] != "west-a" {
		t.Fatalf("unexpected site resolution: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&site=missing")
	if err != nil {
		t.Fatalf("resolve region fallback: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode region resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "region" || resolved.Route["destination"] != "west-b" {
		t.Fatalf("unexpected region fallback: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=eu-central")
	if err != nil {
		t.Fatalf("resolve global fallback: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode global resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "global" || resolved.Route["destination"] != "global-fast" {
		t.Fatalf("unexpected global fallback: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&tag=tier:premium")
	if err != nil {
		t.Fatalf("resolve filtered region fallback: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode filtered resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "region" || resolved.Route["destination"] != "west-b" {
		t.Fatalf("unexpected filtered resolve: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&tag=tier:premium&tag=site:edge-b")
	if err != nil {
		t.Fatalf("resolve multi-tag region fallback: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode multi-tag resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "region" || resolved.Route["destination"] != "west-b" {
		t.Fatalf("unexpected multi-tag resolve: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&site=missing&tag=tier:premium&explain=true")
	if err != nil {
		t.Fatalf("resolve explained fallback: %v", err)
	}
	var explained struct {
		Scope       string         `json:"scope"`
		Route       map[string]any `json:"route"`
		Explanation struct {
			Requested map[string]any   `json:"requested"`
			Attempts  []map[string]any `json:"attempts"`
		} `json:"explanation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&explained); err != nil {
		t.Fatalf("decode explained resolve: %v", err)
	}
	resp.Body.Close()
	if explained.Scope != "region" || explained.Route["destination"] != "west-b" {
		t.Fatalf("unexpected explained resolve: %+v", explained)
	}
	if len(explained.Explanation.Attempts) != 2 {
		t.Fatalf("expected 2 fallback attempts before selection, got %+v", explained.Explanation.Attempts)
	}
	if explained.Explanation.Attempts[0]["scope"] != "site" || explained.Explanation.Attempts[0]["selected"] != nil {
		t.Fatalf("expected unselected site attempt, got %+v", explained.Explanation.Attempts[0])
	}
	if explained.Explanation.Attempts[1]["scope"] != "region" || explained.Explanation.Attempts[1]["selected"] != true {
		t.Fatalf("expected selected region attempt, got %+v", explained.Explanation.Attempts[1])
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=ap-south&tag=tier:premium&explain=true")
	if err != nil {
		t.Fatalf("resolve explained miss: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	var miss struct {
		Error       string `json:"error"`
		Explanation struct {
			Attempts []map[string]any `json:"attempts"`
		} `json:"explanation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&miss); err != nil {
		t.Fatalf("decode explained miss: %v", err)
	}
	resp.Body.Close()
	if miss.Error != "no routes available" {
		t.Fatalf("unexpected explained miss error: %+v", miss)
	}
	if len(miss.Explanation.Attempts) != 2 {
		t.Fatalf("expected region and global attempts on miss, got %+v", miss.Explanation.Attempts)
	}
	if miss.Explanation.Attempts[0]["scope"] != "region" || miss.Explanation.Attempts[1]["scope"] != "global" {
		t.Fatalf("unexpected attempt order on miss: %+v", miss.Explanation.Attempts)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&tag=tier:premium&alternatives=2")
	if err != nil {
		t.Fatalf("resolve alternatives: %v", err)
	}
	var ranked struct {
		Scope        string           `json:"scope"`
		Route        map[string]any   `json:"route"`
		Alternatives []map[string]any `json:"alternatives"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ranked); err != nil {
		t.Fatalf("decode resolve alternatives: %v", err)
	}
	resp.Body.Close()
	if ranked.Scope != "region" || ranked.Route["destination"] != "west-b" {
		t.Fatalf("unexpected ranked resolve winner: %+v", ranked)
	}
	if len(ranked.Alternatives) != 1 || ranked.Alternatives[0]["destination"] != "west-c" {
		t.Fatalf("unexpected ranked resolve alternatives: %+v", ranked.Alternatives)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&site=missing&max_scope=region")
	if err != nil {
		t.Fatalf("resolve region-limited fallback: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode region-limited resolve: %v", err)
	}
	resp.Body.Close()
	if resolved.Scope != "region" || resolved.Route["destination"] != "west-b" {
		t.Fatalf("unexpected region-limited resolve: %+v", resolved)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&site=missing&max_scope=site&explain=true")
	if err != nil {
		t.Fatalf("resolve site-only miss: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for site-only miss, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&miss); err != nil {
		t.Fatalf("decode site-only miss: %v", err)
	}
	resp.Body.Close()
	if len(miss.Explanation.Attempts) != 1 || miss.Explanation.Attempts[0]["scope"] != "site" {
		t.Fatalf("expected only site attempt for site-only miss, got %+v", miss.Explanation.Attempts)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?alternatives=bad")
	if err != nil {
		t.Fatalf("resolve invalid alternatives: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid alternatives, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/routes/resolve?region=us-west&max_scope=site")
	if err != nil {
		t.Fatalf("resolve invalid max_scope: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid max_scope combination, got %d", resp.StatusCode)
	}
}

func TestRoutesResolveBatchEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_resolve_batch", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=west-b&latency=10&throughput=90", "text/plain", nil)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-b", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-b","tier":"premium"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=west-c&latency=14&throughput=85", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-c", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-c","tier":"premium"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=global-fast&latency=5&throughput=50", "text/plain", nil)

	body := `{"requests":[{"id":"premium-west","region":"us-west","tags":{"tier":"premium"},"alternatives":2},{"id":"site-only-miss","region":"us-west","site":"missing","max_scope":"site","explain":true},{"id":"bad-scope","region":"us-west","max_scope":"bogus"}]}`
	resp, err = http.Post(srv.URL+"/routes/resolve/batch", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("batch resolve: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Count   int              `json:"count"`
		Summary map[string]any   `json:"summary"`
		Results []map[string]any `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode batch resolve: %v", err)
	}
	if result.Count != 3 || len(result.Results) != 3 {
		t.Fatalf("unexpected batch result count: %+v", result)
	}
	if result.Summary["ok"].(float64) != 1 || result.Summary["not_found"].(float64) != 1 || result.Summary["invalid"].(float64) != 1 {
		t.Fatalf("unexpected batch summary: %+v", result.Summary)
	}
	if result.Results[0]["id"] != "premium-west" || result.Results[0]["status"].(float64) != 200 || result.Results[0]["scope"] != "region" {
		t.Fatalf("unexpected first batch result: %+v", result.Results[0])
	}
	firstRoute, ok := result.Results[0]["route"].(map[string]any)
	if !ok || firstRoute["destination"] != "west-b" {
		t.Fatalf("unexpected first batch route: %+v", result.Results[0]["route"])
	}
	firstAlternatives, ok := result.Results[0]["alternatives"].([]any)
	if !ok || len(firstAlternatives) != 1 {
		t.Fatalf("unexpected first batch alternatives: %+v", result.Results[0]["alternatives"])
	}
	altRoute, ok := firstAlternatives[0].(map[string]any)
	if !ok || altRoute["destination"] != "west-c" {
		t.Fatalf("unexpected first batch alternative route: %+v", firstAlternatives[0])
	}

	if result.Results[1]["id"] != "site-only-miss" || result.Results[1]["status"].(float64) != 404 || result.Results[1]["error"] != "no routes available" {
		t.Fatalf("unexpected second batch result: %+v", result.Results[1])
	}
	secondExplanation, ok := result.Results[1]["explanation"].(map[string]any)
	if !ok {
		t.Fatalf("expected explanation on second batch result, got %+v", result.Results[1]["explanation"])
	}
	secondAttempts, ok := secondExplanation["attempts"].([]any)
	if !ok || len(secondAttempts) != 1 {
		t.Fatalf("unexpected second batch attempts: %+v", secondExplanation["attempts"])
	}

	if result.Results[2]["id"] != "bad-scope" || result.Results[2]["status"].(float64) != 400 {
		t.Fatalf("unexpected third batch result: %+v", result.Results[2])
	}
	if result.Results[2]["error"] != "max_scope must be one of: site, region, global" {
		t.Fatalf("unexpected third batch error: %+v", result.Results[2])
	}

	defaultsBody := `{"defaults":{"region":"us-west","tags":{"tier":"premium"},"alternatives":2,"explain":true},"requests":[{"id":"defaults-hit"},{"id":"defaults-miss","site":"missing","max_scope":"site"}]}`
	resp, err = http.Post(srv.URL+"/routes/resolve/batch", "application/json", bytes.NewBufferString(defaultsBody))
	if err != nil {
		t.Fatalf("batch resolve with defaults: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var defaultsResult struct {
		Count   int              `json:"count"`
		Summary map[string]any   `json:"summary"`
		Results []map[string]any `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&defaultsResult); err != nil {
		t.Fatalf("decode batch resolve defaults: %v", err)
	}
	if defaultsResult.Count != 2 || len(defaultsResult.Results) != 2 {
		t.Fatalf("unexpected defaults batch count: %+v", defaultsResult)
	}
	if defaultsResult.Summary["ok"].(float64) != 1 || defaultsResult.Summary["not_found"].(float64) != 1 {
		t.Fatalf("unexpected defaults batch summary: %+v", defaultsResult.Summary)
	}
	if defaultsResult.Results[0]["id"] != "defaults-hit" || defaultsResult.Results[0]["scope"] != "region" {
		t.Fatalf("unexpected defaults batch hit: %+v", defaultsResult.Results[0])
	}
	defaultsAlternatives, ok := defaultsResult.Results[0]["alternatives"].([]any)
	if !ok || len(defaultsAlternatives) != 1 {
		t.Fatalf("expected inherited alternatives on defaults batch hit, got %+v", defaultsResult.Results[0]["alternatives"])
	}
	if _, ok := defaultsResult.Results[0]["explanation"].(map[string]any); !ok {
		t.Fatalf("expected inherited explanation on defaults batch hit, got %+v", defaultsResult.Results[0]["explanation"])
	}
	if defaultsResult.Results[1]["id"] != "defaults-miss" || defaultsResult.Results[1]["status"].(float64) != 404 {
		t.Fatalf("unexpected defaults batch miss: %+v", defaultsResult.Results[1])
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

func TestJobAndGroupEventsHistoryTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_job_group_events", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/agents/jobs", bytes.NewBufferString(`{"agent_id":"worker-a","capability":"ocr"}`))
	if err != nil {
		t.Fatalf("job create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("job create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var claimedJob struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claimedJob); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/agents/jobs/claim?agent_id=worker-a", nil)
	if err != nil {
		t.Fatalf("claim request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/agents/jobs", bytes.NewBufferString(`{"agent_id":"worker-a","capability":"ocr"}`))
	if err != nil {
		t.Fatalf("second job create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second job create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var cancelledJob struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cancelledJob); err != nil {
		t.Fatalf("decode second job: %v", err)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/agents/jobs/"+cancelledJob.ID, nil)
	if err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/agent-groups", bytes.NewBufferString(`{"name":"workers"}`))
	if err != nil {
		t.Fatalf("group create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("group create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var group struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&group); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/agent-groups/"+group.ID+"/members", bytes.NewBufferString(`{"agent_id":"worker-a"}`))
	if err != nil {
		t.Fatalf("add member request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/agent-groups/"+group.ID+"/members", bytes.NewBufferString(`{"agent_id":"worker-a"}`))
	if err != nil {
		t.Fatalf("duplicate add member request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("duplicate add member: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+group.ID+"/members/worker-a", nil)
	if err != nil {
		t.Fatalf("remove member request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+group.ID, nil)
	if err != nil {
		t.Fatalf("delete group request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/events/history?limit=20", nil)
	if err != nil {
		t.Fatalf("history request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var history struct {
		Count  int `json:"count"`
		Events []struct {
			Type     string `json:"type"`
			TenantID string `json:"tenant_id"`
		} `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	resp.Body.Close()
	if history.Count == 0 {
		t.Fatal("expected tenant-a history entries")
	}
	seen := map[string]bool{}
	eventCounts := map[string]int{}
	for _, ev := range history.Events {
		if ev.TenantID != "tenant-a" {
			t.Fatalf("unexpected tenant in history: %+v", ev)
		}
		seen[ev.Type] = true
		eventCounts[ev.Type]++
	}
	for _, want := range []string{
		string(events.EventJobCreated),
		string(events.EventJobClaimed),
		string(events.EventJobCancelled),
		string(events.EventGroupCreated),
		string(events.EventGroupMemberAdded),
		string(events.EventGroupMemberRemoved),
		string(events.EventGroupDeleted),
	} {
		if !seen[want] {
			t.Fatalf("expected event type %q in history, got %v", want, seen)
		}
	}
	if eventCounts[string(events.EventGroupMemberAdded)] != 1 {
		t.Fatalf("expected exactly 1 group.member_added event, got %d", eventCounts[string(events.EventGroupMemberAdded)])
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/events/history?limit=10&type="+string(events.EventJobCreated), nil)
	if err != nil {
		t.Fatalf("filtered history request: %v", err)
	}
	req.Header.Set("X-API-Key", "key-b")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tenant-b history: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var otherHistory struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&otherHistory); err != nil {
		t.Fatalf("decode tenant-b history: %v", err)
	}
	resp.Body.Close()
	if otherHistory.Count != 0 {
		t.Fatalf("expected tenant-b to see 0 matching events, got %d", otherHistory.Count)
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

func TestAgentPatchEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_requests_total_agpatch", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/agents/register", "application/json",
		bytes.NewBufferString(`{"id":"patch-agent","addr":"10.0.0.1:9001","status":"healthy","ttl_seconds":300}`))

	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/agents?id=patch-agent", bytes.NewBufferString(
		`{"status":"degraded","addr":"10.0.0.2:9002","capabilities":["vision","ocr"],"tags":{"region":"us-east","tier":"edge"}}`,
	))
	if err != nil {
		t.Fatalf("patch request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch agent: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated struct {
		ID           string            `json:"id"`
		Addr         string            `json:"addr"`
		Status       string            `json:"status"`
		Capabilities []string          `json:"capabilities"`
		Tags         map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.ID != "patch-agent" || updated.Status != "degraded" || updated.Addr != "10.0.0.2:9002" {
		t.Fatalf("unexpected patched agent: %+v", updated)
	}
	if len(updated.Capabilities) != 2 || updated.Capabilities[0] != "vision" || updated.Tags["region"] != "us-east" {
		t.Fatalf("expected capabilities and tags to update, got %+v", updated)
	}

	req, err = http.NewRequest(http.MethodPatch, srv.URL+"/agents?id=patch-agent", bytes.NewBufferString(`{"status":"broken"}`))
	if err != nil {
		t.Fatalf("invalid patch request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invalid patch agent: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
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
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
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
		agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), pipeline.NewWithStore(db), billing.New(db))
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
	var submitted struct {
		ID string `json:"id"`
	}
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
		agentReg, corsConfig{}, false, broker, nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), pipeline.NewWithStore(db), billing.New(db))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register an agent.
	regBody := `{"id":"worker-1","addr":"10.0.0.1:9000","status":"healthy","capabilities":["ocr"]}`
	rr, _ := http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(regBody))
	_ = rr.Body.Close()

	// Submit a job — auto-dispatched to worker-1.
	jResp, _ := http.Post(srv.URL+"/agents/jobs", "application/json",
		bytes.NewBufferString(`{"capability":"ocr","payload":{"file":"doc.pdf"}}`))
	var submitted struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(jResp.Body).Decode(&submitted)
	_ = jResp.Body.Close()
	if submitted.ID == "" {
		t.Fatal("expected job ID from auto-dispatch")
	}

	// Submit a second job for cancel test.
	jResp2, _ := http.Post(srv.URL+"/agents/jobs", "application/json",
		bytes.NewBufferString(`{"capability":"ocr"}`))
	var submitted2 struct {
		ID string `json:"id"`
	}
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

func TestJobItemEndpointsAreTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_jobs_tenant_scope_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	postReq, err := http.NewRequest(http.MethodPost, srv.URL+"/agents/jobs", bytes.NewBufferString(`{"agent_id":"worker-a","capability":"ocr"}`))
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-API-Key", "key-a")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", postResp.StatusCode)
	}
	var submitted struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&submitted); err != nil {
		t.Fatalf("decode submitted job: %v", err)
	}
	if submitted.ID == "" {
		t.Fatal("expected submitted job id")
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/agents/jobs/"+submitted.ID, nil)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	getReq.Header.Set("X-API-Key", "key-b")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("cross-tenant get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getResp.StatusCode)
	}

	listReq, err := http.NewRequest(http.MethodGet, srv.URL+"/agents/jobs", nil)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	listReq.Header.Set("X-API-Key", "key-b")
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("cross-tenant list: %v", err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode job list: %v", err)
	}
	if listed.Count != 0 {
		t.Fatalf("expected tenant-b to see 0 jobs, got %d", listed.Count)
	}

	delReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/agents/jobs/"+submitted.ID, nil)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	delReq.Header.Set("X-API-Key", "key-b")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", delResp.StatusCode)
	}

	delReq, err = http.NewRequest(http.MethodDelete, srv.URL+"/agents/jobs/"+submitted.ID, nil)
	if err != nil {
		t.Fatalf("owner delete request: %v", err)
	}
	delReq.Header.Set("X-API-Key", "key-a")
	delResp, err = http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", delResp.StatusCode)
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
		agent.NewRegistry(), corsConfig{}, false, events.NewBroker(), nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), pipeline.NewWithStore(db), billing.New(db))
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
		agent.NewRegistry(), corsConfig{}, false, events.NewBroker(), nil, "", jq, nil, kv.New(), notify.New(), circuit.New(5, 30*time.Second), agentgroup.New(), scheduler.New(jq), pipeline.NewWithStore(db), billing.New(db))
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
	var entry struct {
		Value string `json:"value"`
	}
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
		Routes []struct {
			Destination string `json:"destination"`
		} `json:"routes"`
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

	body := `{"name":"r1","webhook_url":"http://example.com/hook","event_types":["agent_registered"]}`
	cr, _ := http.Post(srv.URL+"/admin/notifications", "application/json", bytes.NewBufferString(body))
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusCreated {
		t.Fatalf("POST expected 201, got %d", cr.StatusCode)
	}
	var rule struct {
		ID string `json:"id"`
	}
	json.NewDecoder(cr.Body).Decode(&rule)

	gr, _ := http.Get(srv.URL + "/admin/notifications/" + rule.ID)
	defer gr.Body.Close()
	if gr.StatusCode != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", gr.StatusCode)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/notifications/"+rule.ID, bytes.NewBufferString(`{"name":"r2","active":false,"event_types":["agent_heartbeat"]}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, _ := http.DefaultClient.Do(patchReq)
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH expected 200, got %d", patchResp.StatusCode)
	}
	var updated struct {
		Name       string   `json:"name"`
		Active     bool     `json:"active"`
		EventTypes []string `json:"event_types"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated rule: %v", err)
	}
	if updated.Name != "r2" || updated.Active {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}
	if len(updated.EventTypes) != 1 || updated.EventTypes[0] != "agent_heartbeat" {
		t.Fatalf("unexpected updated event types: %+v", updated.EventTypes)
	}

	dr, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/notifications/"+rule.ID, nil)
	delResp, _ := http.DefaultClient.Do(dr)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", delResp.StatusCode)
	}
}

func TestNotificationRulesPersistAcrossHandlerRestart(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_notif_persist_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler1 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv1 := httptest.NewServer(handler1)
	body := `{"name":"persisted","webhook_url":"http://example.com/hook","event_types":["agent_registered"]}`
	resp, err := http.Post(srv1.URL+"/admin/notifications", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	srv1.Close()

	handler2 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv2 := httptest.NewServer(handler2)
	t.Cleanup(srv2.Close)

	listResp, err := http.Get(srv2.URL + "/admin/notifications")
	if err != nil {
		t.Fatalf("list rules after restart: %v", err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Count != 1 {
		t.Fatalf("expected 1 restored rule, got %d", listed.Count)
	}
}

func TestNotificationRuleItemEndpointsAreTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_notif_tenant_scope_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	postReq, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/notifications", bytes.NewBufferString(`{"name":"tenant-a","webhook_url":"http://example.com/hook","event_types":["agent_registered"]}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-API-Key", "key-a")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", postResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}

	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/notifications/"+created.ID, nil)
	getReq.Header.Set("X-API-Key", "key-b")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("cross-tenant get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getResp.StatusCode)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/notifications/"+created.ID, bytes.NewBufferString(`{"active":false}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-API-Key", "key-b")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("cross-tenant patch: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", patchResp.StatusCode)
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/notifications/"+created.ID, nil)
	deleteReq.Header.Set("X-API-Key", "key-b")
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", deleteResp.StatusCode)
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
	var list struct {
		Count int `json:"count"`
	}
	json.NewDecoder(lr.Body).Decode(&list)
	if list.Count != 1 {
		t.Errorf("expected 1 breaker, got %d", list.Count)
	}

	// Reset.
	rr, _ := http.Post(srv.URL+"/circuit/reset?agent_id=agent-x", "application/json", nil)
	defer rr.Body.Close()
	var reset struct {
		Failures int `json:"failures"`
	}
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
	var g struct {
		ID string `json:"id"`
	}
	json.NewDecoder(cr.Body).Decode(&g)

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/agent-groups/"+g.ID, bytes.NewBufferString(`{"name":"workers-v2"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, _ := http.DefaultClient.Do(patchReq)
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH expected 200, got %d", patchResp.StatusCode)
	}
	var patched struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched group: %v", err)
	}
	if patched.Name != "workers-v2" {
		t.Fatalf("unexpected patched group: %+v", patched)
	}

	// Add member.
	mr, _ := http.Post(srv.URL+"/agent-groups/"+g.ID+"/members", "application/json",
		bytes.NewBufferString(`{"agent_id":"agent-1","weight":2}`))
	defer mr.Body.Close()
	if mr.StatusCode != 200 {
		t.Fatalf("add member expected 200, got %d", mr.StatusCode)
	}

	memberPatchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/agent-groups/"+g.ID+"/members/agent-1", bytes.NewBufferString(`{"weight":3}`))
	memberPatchReq.Header.Set("Content-Type", "application/json")
	memberPatchResp, _ := http.DefaultClient.Do(memberPatchReq)
	defer memberPatchResp.Body.Close()
	if memberPatchResp.StatusCode != 200 {
		t.Fatalf("patch member expected 200, got %d", memberPatchResp.StatusCode)
	}
	var patchedMembers struct {
		Members []struct {
			AgentID string `json:"agent_id"`
			Weight  int    `json:"weight"`
		} `json:"members"`
	}
	if err := json.NewDecoder(memberPatchResp.Body).Decode(&patchedMembers); err != nil {
		t.Fatalf("decode patched group: %v", err)
	}
	if len(patchedMembers.Members) != 1 || patchedMembers.Members[0].Weight != 3 {
		t.Fatalf("unexpected patched members: %+v", patchedMembers.Members)
	}

	missingDeleteReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+g.ID+"/members/missing", nil)
	missingDeleteResp, _ := http.DefaultClient.Do(missingDeleteReq)
	defer missingDeleteResp.Body.Close()
	if missingDeleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing member expected 404, got %d", missingDeleteResp.StatusCode)
	}

	// Get next (round-robin).
	nr, _ := http.Get(srv.URL + "/agent-groups/" + g.ID + "/next")
	defer nr.Body.Close()
	if nr.StatusCode != 200 {
		t.Fatalf("next expected 200, got %d", nr.StatusCode)
	}
	var next struct {
		AgentID string `json:"agent_id"`
	}
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

func TestAgentGroupsPersistAcrossHandlerRestart(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_group_persist_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler1 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv1 := httptest.NewServer(handler1)

	createResp, err := http.Post(srv1.URL+"/agent-groups", "application/json", bytes.NewBufferString(`{"name":"workers"}`))
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created group: %v", err)
	}
	createResp.Body.Close()

	memberResp, err := http.Post(srv1.URL+"/agent-groups/"+created.ID+"/members", "application/json", bytes.NewBufferString(`{"agent_id":"agent-1","weight":2}`))
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	memberResp.Body.Close()
	if memberResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", memberResp.StatusCode)
	}
	srv1.Close()

	handler2 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv2 := httptest.NewServer(handler2)
	t.Cleanup(srv2.Close)

	getResp, err := http.Get(srv2.URL + "/agent-groups/" + created.ID)
	if err != nil {
		t.Fatalf("get persisted group: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}
	var restored struct {
		Name    string `json:"name"`
		Members []struct {
			AgentID string `json:"agent_id"`
			Weight  int    `json:"weight"`
		} `json:"members"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&restored); err != nil {
		t.Fatalf("decode restored group: %v", err)
	}
	if restored.Name != "workers" || len(restored.Members) != 1 || restored.Members[0].AgentID != "agent-1" || restored.Members[0].Weight != 2 {
		t.Fatalf("unexpected restored group: %+v", restored)
	}
}

func TestAgentGroupEndpointsAreTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_groups_tenant_scope_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	createReq, err := http.NewRequest(http.MethodPost, srv.URL+"/agent-groups", bytes.NewBufferString(`{"name":"workers"}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-API-Key", "key-a")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var group struct {
		ID     string `json:"id"`
		Tenant string `json:"tenant"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&group); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if group.ID == "" || group.Tenant != "tenant-a" {
		t.Fatalf("unexpected group: %+v", group)
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/agent-groups/"+group.ID, nil)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	getReq.Header.Set("X-API-Key", "key-a")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("get group as owner: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}

	getReq, err = http.NewRequest(http.MethodGet, srv.URL+"/agent-groups/"+group.ID, nil)
	if err != nil {
		t.Fatalf("cross-tenant get request: %v", err)
	}
	getReq.Header.Set("X-API-Key", "key-b")
	getResp, err = http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("cross-tenant get group: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getResp.StatusCode)
	}

	patchReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/agent-groups/"+group.ID+"/members/ghost", bytes.NewBufferString(`{"weight":2}`))
	if err != nil {
		t.Fatalf("cross-tenant member patch request: %v", err)
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-API-Key", "key-b")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("cross-tenant member patch: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", patchResp.StatusCode)
	}

	groupPatchReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/agent-groups/"+group.ID, bytes.NewBufferString(`{"name":"other"}`))
	if err != nil {
		t.Fatalf("cross-tenant patch request: %v", err)
	}
	groupPatchReq.Header.Set("Content-Type", "application/json")
	groupPatchReq.Header.Set("X-API-Key", "key-b")
	groupPatchResp, err := http.DefaultClient.Do(groupPatchReq)
	if err != nil {
		t.Fatalf("cross-tenant patch group: %v", err)
	}
	defer groupPatchResp.Body.Close()
	if groupPatchResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", groupPatchResp.StatusCode)
	}

	listReq, err := http.NewRequest(http.MethodGet, srv.URL+"/agent-groups", nil)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	listReq.Header.Set("X-API-Key", "key-b")
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("cross-tenant list groups: %v", err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode group list: %v", err)
	}
	if listed.Count != 0 {
		t.Fatalf("expected tenant-b to see 0 groups, got %d", listed.Count)
	}

	delReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+group.ID, nil)
	if err != nil {
		t.Fatalf("cross-tenant delete request: %v", err)
	}
	delReq.Header.Set("X-API-Key", "key-b")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("cross-tenant delete group: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", delResp.StatusCode)
	}

	delReq, err = http.NewRequest(http.MethodDelete, srv.URL+"/agent-groups/"+group.ID, nil)
	if err != nil {
		t.Fatalf("owner delete request: %v", err)
	}
	delReq.Header.Set("X-API-Key", "key-a")
	delResp, err = http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("owner delete group: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", delResp.StatusCode)
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
	var s struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(cr.Body).Decode(&s)
	if s.Name != "hourly-sync" || s.ID == "" {
		t.Errorf("unexpected schedule: %+v", s)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/schedules/"+s.ID, bytes.NewBufferString(`{"name":"paused-sync","interval_seconds":7200,"active":false}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, _ := http.DefaultClient.Do(patchReq)
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH expected 200, got %d", patchResp.StatusCode)
	}
	var updated struct {
		Name     string `json:"name"`
		Active   bool   `json:"active"`
		Interval int64  `json:"interval_ns"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patched schedule: %v", err)
	}
	if updated.Name != "paused-sync" || updated.Active || updated.Interval != int64(2*time.Hour) {
		t.Fatalf("unexpected patched schedule: %+v", updated)
	}

	// List.
	lr, _ := http.Get(srv.URL + "/schedules")
	defer lr.Body.Close()
	var list struct {
		Count int `json:"count"`
	}
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

func TestScheduleItemEndpointsAreTenantScoped(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_sched_tenant_scope_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantKeys, err := auth.NewManagerFromEnv("tenant-a:key-a,tenant-b:key-b")
	if err != nil {
		t.Fatalf("tenant key manager: %v", err)
	}
	handler := makeHandler(db, counter, authConfig{tenantKeys: tenantKeys, defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	postReq, err := http.NewRequest(http.MethodPost, srv.URL+"/schedules", bytes.NewBufferString(`{"name":"tenant-a-sync","capability":"sync","interval_seconds":3600}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-API-Key", "key-a")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", postResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created schedule: %v", err)
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/schedules/"+created.ID, nil)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	getReq.Header.Set("X-API-Key", "key-b")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("cross-tenant get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getResp.StatusCode)
	}

	patchReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/schedules/"+created.ID, bytes.NewBufferString(`{"active":false}`))
	if err != nil {
		t.Fatalf("patch request: %v", err)
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-API-Key", "key-b")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("cross-tenant patch: %v", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", patchResp.StatusCode)
	}

	deleteReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/schedules/"+created.ID, nil)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	deleteReq.Header.Set("X-API-Key", "key-b")
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", deleteResp.StatusCode)
	}
}

func TestSchedulesPersistAcrossHandlerRestart(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_schedule_persist_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler1 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv1 := httptest.NewServer(handler1)

	createResp, err := http.Post(srv1.URL+"/schedules", "application/json", bytes.NewBufferString(`{"name":"persisted-sync","capability":"sync","interval_seconds":3600}`))
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created schedule: %v", err)
	}
	createResp.Body.Close()

	pauseReq, err := http.NewRequest(http.MethodPatch, srv1.URL+"/schedules/"+created.ID, bytes.NewBufferString(`{"active":false}`))
	if err != nil {
		t.Fatalf("pause request: %v", err)
	}
	pauseReq.Header.Set("Content-Type", "application/json")
	pauseResp, err := http.DefaultClient.Do(pauseReq)
	if err != nil {
		t.Fatalf("pause schedule: %v", err)
	}
	pauseResp.Body.Close()
	if pauseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", pauseResp.StatusCode)
	}
	srv1.Close()

	handler2 := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv2 := httptest.NewServer(handler2)
	t.Cleanup(srv2.Close)

	getResp, err := http.Get(srv2.URL + "/schedules/" + created.ID)
	if err != nil {
		t.Fatalf("get persisted schedule: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}
	var restored struct {
		Name     string `json:"name"`
		Active   bool   `json:"active"`
		Interval int64  `json:"interval_ns"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&restored); err != nil {
		t.Fatalf("decode restored schedule: %v", err)
	}
	if restored.Name != "persisted-sync" || restored.Active || restored.Interval != int64(time.Hour) {
		t.Fatalf("unexpected restored schedule: %+v", restored)
	}
}

func TestScheduleExecutionCreatesJobs(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_sched_exec_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/schedules", "application/json", bytes.NewBufferString(
		`{"name":"fast-sync","agent_id":"worker-1","capability":"sync","interval_seconds":1}`,
	))
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		listResp, err := http.Get(srv.URL + "/agents/jobs")
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		var list struct {
			Count int `json:"count"`
		}
		if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
			listResp.Body.Close()
			t.Fatalf("decode job list: %v", err)
		}
		listResp.Body.Close()
		if list.Count > 0 {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	t.Fatal("expected schedule to enqueue at least one job")
}

func TestBlockchainHeightEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bc_height_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/blockchain/height")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]int
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["height"]; !ok {
		t.Fatal("expected 'height' field in response")
	}
}

func TestRoutesStatsEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_rt_stats_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=edge-a&latency=10&throughput=100", "text/plain", nil)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-a", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-a"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=edge-b&latency=20&throughput=80", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-b", bytes.NewBufferString(`{"satellite":true,"tags":{"region":"us-west","site":"edge-b"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	resp, _ = http.Get(srv.URL + "/routes/stats")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["total"]; !ok {
		t.Fatal("expected 'total' in routes/stats")
	}
	if result["total"].(float64) != 2 {
		t.Fatalf("expected total=2, got %v", result["total"])
	}
	byRegion, ok := result["by_region"].(map[string]any)
	if !ok {
		t.Fatalf("expected by_region map, got %#v", result["by_region"])
	}
	regionStats, ok := byRegion["us-west"].(map[string]any)
	if !ok {
		t.Fatalf("expected us-west stats, got %#v", byRegion)
	}
	if regionStats["count"].(float64) != 2 || regionStats["satellite"].(float64) != 1 {
		t.Fatalf("unexpected us-west stats: %#v", regionStats)
	}
	bySite, ok := result["by_site"].(map[string]any)
	if !ok {
		t.Fatalf("expected by_site map, got %#v", result["by_site"])
	}
	if _, ok := bySite["edge-a"].(map[string]any); !ok {
		t.Fatalf("expected edge-a site stats, got %#v", bySite)
	}

	filteredResp, _ := http.Get(srv.URL + "/routes/stats?tag=site:edge-a")
	defer filteredResp.Body.Close()
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", filteredResp.StatusCode)
	}
	var filtered map[string]any
	if err := json.NewDecoder(filteredResp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered stats: %v", err)
	}
	if filtered["total"].(float64) != 1 {
		t.Fatalf("expected filtered total=1, got %v", filtered["total"])
	}
	filteredBySite, ok := filtered["by_site"].(map[string]any)
	if !ok || len(filteredBySite) != 1 {
		t.Fatalf("expected single filtered site, got %#v", filtered["by_site"])
	}
	if _, ok := filteredBySite["edge-a"].(map[string]any); !ok {
		t.Fatalf("expected filtered edge-a site stats, got %#v", filteredBySite)
	}
}

func TestRoutesTopologyEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_rt_topology_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=west-primary&latency=15&throughput=90", "text/plain", nil)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-primary", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-a"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=west-sat&latency=5&throughput=60", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/west-sat", bytes.NewBufferString(`{"satellite":true,"tags":{"region":"us-west","site":"edge-b"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=east-primary&latency=8&throughput=100", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/east-primary", bytes.NewBufferString(`{"tags":{"region":"us-east","site":"edge-c"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=untagged&latency=3&throughput=20", "text/plain", nil)

	resp, _ = http.Get(srv.URL + "/routes/topology")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode topology: %v", err)
	}
	if result["total"].(float64) != 4 {
		t.Fatalf("expected total=4, got %v", result["total"])
	}
	regions, ok := result["regions"].(map[string]any)
	if !ok {
		t.Fatalf("expected regions map, got %#v", result["regions"])
	}
	west, ok := regions["us-west"].(map[string]any)
	if !ok {
		t.Fatalf("expected us-west region, got %#v", regions)
	}
	westBest, ok := west["best_route"].(map[string]any)
	if !ok || westBest["destination"] != "west-primary" {
		t.Fatalf("expected west-primary best route, got %#v", west["best_route"])
	}
	westSites, ok := west["sites"].(map[string]any)
	if !ok {
		t.Fatalf("expected site map, got %#v", west["sites"])
	}
	edgeB, ok := westSites["edge-b"].(map[string]any)
	if !ok {
		t.Fatalf("expected edge-b site, got %#v", westSites)
	}
	edgeBBest, ok := edgeB["best_route"].(map[string]any)
	if !ok || edgeBBest["destination"] != "west-sat" {
		t.Fatalf("expected west-sat site best route, got %#v", edgeB["best_route"])
	}
	untagged, ok := result["untagged"].(map[string]any)
	if !ok || untagged["count"].(float64) != 1 {
		t.Fatalf("expected untagged summary, got %#v", result["untagged"])
	}

	filteredResp, _ := http.Get(srv.URL + "/routes/topology?tag=region:us-west&tag=site:edge-b")
	defer filteredResp.Body.Close()
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", filteredResp.StatusCode)
	}
	var filtered map[string]any
	if err := json.NewDecoder(filteredResp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered topology: %v", err)
	}
	if filtered["total"].(float64) != 1 {
		t.Fatalf("expected filtered total=1, got %v", filtered["total"])
	}
	filteredRegions, ok := filtered["regions"].(map[string]any)
	if !ok || len(filteredRegions) != 1 {
		t.Fatalf("expected single filtered region, got %#v", filtered["regions"])
	}
	filteredWest, ok := filteredRegions["us-west"].(map[string]any)
	if !ok {
		t.Fatalf("expected filtered us-west region, got %#v", filteredRegions)
	}
	filteredSites, ok := filteredWest["sites"].(map[string]any)
	if !ok || len(filteredSites) != 1 {
		t.Fatalf("expected single filtered site, got %#v", filteredWest["sites"])
	}
	filteredEdgeB, ok := filteredSites["edge-b"].(map[string]any)
	if !ok {
		t.Fatalf("expected filtered edge-b site, got %#v", filteredSites)
	}
	filteredBest, ok := filteredEdgeB["best_route"].(map[string]any)
	if !ok || filteredBest["destination"] != "west-sat" {
		t.Fatalf("expected filtered west-sat best route, got %#v", filteredEdgeB["best_route"])
	}
	if filtered["untagged"] != nil {
		t.Fatalf("expected no untagged bucket after filtering, got %#v", filtered["untagged"])
	}
}

func TestAgentsStatsEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_ag_stats_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/agents/stats")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["total"]; !ok {
		t.Fatal("expected 'total' in agents/stats")
	}
}

func TestBlockchainStatsEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bc_stats_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/blockchain/stats")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["height"]; !ok {
		t.Fatal("expected 'height' in blockchain/stats")
	}
}

func TestSystemInfoEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_sysinfo_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/system/info")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["version"]; !ok {
		t.Fatal("expected 'version' in system/info")
	}
	if _, ok := result["goroutines"]; !ok {
		t.Fatal("expected 'goroutines' in system/info")
	}
}

func TestAgentsHealthEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_ag_health_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/agents/health")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["status"]; !ok {
		t.Fatal("expected 'status' in agents/health")
	}
}

func TestMetricsTimeseriesEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mts_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/metrics/timeseries?n=5")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRoutesFilterEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_rfilt_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// First add a route
	body := `{"destination":"10.0.0.1:9000","metric":{"latency":10,"throughput":100}}`
	pr, _ := http.Post(srv.URL+"/routes", "application/json", bytes.NewBufferString(body))
	defer pr.Body.Close()

	// Filter with max_latency
	resp, _ := http.Get(srv.URL + "/routes/filter?max_latency=50")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["routes"]; !ok {
		t.Fatal("expected 'routes' key")
	}
}

func TestRoutesFilterEndpointWithMultipleTags(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_rfilt_multi_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, _ = http.Post(srv.URL+"/routes?destination=edge-a&latency=10&throughput=100", "text/plain", nil)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-a", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-a","tier":"standard"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	_, _ = http.Post(srv.URL+"/routes?destination=edge-b&latency=12&throughput=110", "text/plain", nil)
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/routes/edge-b", bytes.NewBufferString(`{"tags":{"region":"us-west","site":"edge-b","tier":"premium"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	resp, _ = http.Get(srv.URL + "/routes/filter?tag=region:us-west&tag=site:edge-b")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Routes []map[string]any `json:"routes"`
		Count  int              `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Count != 1 || len(result.Routes) != 1 || result.Routes[0]["destination"] != "edge-b" {
		t.Fatalf("unexpected multi-tag filter result: %+v", result)
	}
}

func TestAdminBackupList_NilStatus(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bkup_list_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/admin/backup/list")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with nil status, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["backups"] == nil {
		t.Fatal("expected 'backups' key")
	}
}

func TestAdminBackup_NilStatus(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bkup_nil_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Post(srv.URL+"/admin/backup", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil status, got %d", resp.StatusCode)
	}
}

func TestAdminCompact_NilStatus(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_cmp_nil_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Post(srv.URL+"/admin/compact", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil status, got %d", resp.StatusCode)
	}
}

func TestAdminMeshEndpoint(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_mesh_ep_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Get(srv.URL + "/admin/mesh")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBlockchainHeightMethodNotAllowed(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_bc_ht_405_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, _ := http.Post(srv.URL+"/blockchain/height", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestJobsSubmitWithPriority(t *testing.T) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_jobs_prio_total", Help: "test"}, []string{"path", "method", "code"})
	tmp := t.TempDir()
	db, _ := store.Open(store.DBPath(tmp))
	defer db.Close()
	handler := makeHandler(db, counter, authConfig{defaultTenant: "default"}, nil, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Register agent first
	regBody := `{"id":"prio-agent","tenant_id":"default","capabilities":["compute"],"status":"healthy"}`
	rr, _ := http.Post(srv.URL+"/agents/register", "application/json", bytes.NewBufferString(regBody))
	io.ReadAll(rr.Body)
	rr.Body.Close()

	// Submit with priority
	body := `{"agent_id":"prio-agent","capability":"compute","priority":5,"ttl_seconds":3600}`
	resp, _ := http.Post(srv.URL+"/agents/jobs", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var job map[string]any
	json.NewDecoder(resp.Body).Decode(&job)
	if job["id"] == nil {
		t.Fatal("expected id field in response")
	}
	if job["expires_at"] == nil {
		t.Fatal("expected expires_at field for ttl_seconds=3600")
	}
}

// ---------------------------------------------------------------------------
// Utility function tests: parseKeyList, parseTags, validateTransactions,
// getEnvInt, getEnvFloat, getEnvBool, getEnvUint32, stringInSlice,
// isHTTPMethod, isWriteMethod, secureEquals, pruneExpiredRoutes,
// parseIntQuery, parseIntQueryMulti, hasValidAPIKey, matchPathRules
// ---------------------------------------------------------------------------

func TestParseKeyList_EmptyMain(t *testing.T) {
	if got := parseKeyList(""); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestParseKeyList_Single(t *testing.T) {
	got := parseKeyList("abc")
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestParseKeyList_Multiple(t *testing.T) {
	got := parseKeyList("a, b, c")
	if len(got) != 3 {
		t.Fatalf("expected 3 keys, got: %v", got)
	}
}

func TestParseKeyList_SpacesOnly(t *testing.T) {
	if got := parseKeyList("  ,  ,  "); got != nil && len(got) != 0 {
		t.Fatalf("expected empty, got: %v", got)
	}
}

func TestParseTags_EmptyMain(t *testing.T) {
	if got := parseTags(nil); got != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestParseTags_ValidMain(t *testing.T) {
	got := parseTags([]string{"region:us-east", "tier:premium"})
	if got["region"] != "us-east" || got["tier"] != "premium" {
		t.Fatalf("unexpected tags: %v", got)
	}
}

func TestParseTags_InvalidSkipped(t *testing.T) {
	got := parseTags([]string{"invalid", "key:value"})
	if _, ok := got["invalid"]; ok {
		t.Fatal("invalid entry should be skipped")
	}
	if got["key"] != "value" {
		t.Fatalf("expected 'key' -> 'value', got: %v", got)
	}
}

func TestParseTags_AllInvalidMain(t *testing.T) {
	got := parseTags([]string{"nocolon", "alsonocolon"})
	if got != nil {
		t.Fatalf("expected nil when all invalid, got: %v", got)
	}
}

func TestValidateTransactions_ValidMain(t *testing.T) {
	txs := []blockchain.Transaction{
		{Sender: "alice", Recipient: "bob", Amount: 10},
	}
	if err := validateTransactions(txs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTransactions_EmptySenderMain(t *testing.T) {
	txs := []blockchain.Transaction{{Sender: "", Recipient: "bob", Amount: 5}}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for empty sender")
	}
}

func TestValidateTransactions_NegativeAmountMain(t *testing.T) {
	txs := []blockchain.Transaction{{Sender: "alice", Recipient: "bob", Amount: -1}}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestValidateTransactions_TooManyMain(t *testing.T) {
	txs := make([]blockchain.Transaction, 1001)
	for i := range txs {
		txs[i] = blockchain.Transaction{Sender: "a", Recipient: "b", Amount: 1}
	}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for too many transactions")
	}
}

func TestGetEnvInt_Default(t *testing.T) {
	os.Unsetenv("TEST_ENV_INT")
	if got := getEnvInt("TEST_ENV_INT", 42); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestGetEnvInt_Set(t *testing.T) {
	os.Setenv("TEST_ENV_INT", "99")
	defer os.Unsetenv("TEST_ENV_INT")
	if got := getEnvInt("TEST_ENV_INT", 0); got != 99 {
		t.Fatalf("expected 99, got %d", got)
	}
}

func TestGetEnvInt_Invalid(t *testing.T) {
	os.Setenv("TEST_ENV_INT", "notanint")
	defer os.Unsetenv("TEST_ENV_INT")
	if got := getEnvInt("TEST_ENV_INT", 7); got != 7 {
		t.Fatalf("expected default 7, got %d", got)
	}
}

func TestGetEnvFloat_Default(t *testing.T) {
	os.Unsetenv("TEST_ENV_F")
	if got := getEnvFloat("TEST_ENV_F", 3.14); got != 3.14 {
		t.Fatalf("expected 3.14, got %f", got)
	}
}

func TestGetEnvFloat_Set(t *testing.T) {
	os.Setenv("TEST_ENV_F", "2.71")
	defer os.Unsetenv("TEST_ENV_F")
	if got := getEnvFloat("TEST_ENV_F", 0); got != 2.71 {
		t.Fatalf("expected 2.71, got %f", got)
	}
}

func TestGetEnvFloat_Invalid(t *testing.T) {
	os.Setenv("TEST_ENV_F", "bad")
	defer os.Unsetenv("TEST_ENV_F")
	if got := getEnvFloat("TEST_ENV_F", 1.5); got != 1.5 {
		t.Fatalf("expected 1.5, got %f", got)
	}
}

func TestGetEnvBool_Default(t *testing.T) {
	os.Unsetenv("TEST_ENV_B")
	if got := getEnvBool("TEST_ENV_B", true); !got {
		t.Fatal("expected true default")
	}
}

func TestGetEnvBool_True(t *testing.T) {
	for _, val := range []string{"1", "true", "yes", "y"} {
		os.Setenv("TEST_ENV_B", val)
		if got := getEnvBool("TEST_ENV_B", false); !got {
			t.Fatalf("expected true for value %q", val)
		}
	}
	os.Unsetenv("TEST_ENV_B")
}

func TestGetEnvBool_False(t *testing.T) {
	for _, val := range []string{"0", "false", "no", "n"} {
		os.Setenv("TEST_ENV_B", val)
		if got := getEnvBool("TEST_ENV_B", true); got {
			t.Fatalf("expected false for value %q", val)
		}
	}
	os.Unsetenv("TEST_ENV_B")
}

func TestGetEnvBool_Invalid(t *testing.T) {
	os.Setenv("TEST_ENV_B", "maybe")
	defer os.Unsetenv("TEST_ENV_B")
	if got := getEnvBool("TEST_ENV_B", true); !got {
		t.Fatal("expected default true for invalid value")
	}
}

func TestGetEnvUint32_Default(t *testing.T) {
	os.Unsetenv("TEST_ENV_U32")
	if got := getEnvUint32("TEST_ENV_U32", 100); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
}

func TestGetEnvUint32_Set(t *testing.T) {
	os.Setenv("TEST_ENV_U32", "42")
	defer os.Unsetenv("TEST_ENV_U32")
	if got := getEnvUint32("TEST_ENV_U32", 0); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestStringInSlice_Found(t *testing.T) {
	if !stringInSlice("b", []string{"a", "b", "c"}) {
		t.Fatal("expected true")
	}
}

func TestStringInSlice_NotFound(t *testing.T) {
	if stringInSlice("x", []string{"a", "b", "c"}) {
		t.Fatal("expected false")
	}
}

func TestIsHTTPMethodMain(t *testing.T) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		if !isHTTPMethod(m) {
			t.Fatalf("expected true for %s", m)
		}
	}
	if isHTTPMethod("FETCH") {
		t.Fatal("expected false for FETCH")
	}
}

func TestIsWriteMethodMain(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if !isWriteMethod(m) {
			t.Fatalf("expected true for %s", m)
		}
	}
	if isWriteMethod("GET") {
		t.Fatal("expected false for GET")
	}
}

func TestSecureEquals(t *testing.T) {
	if !secureEquals("hello", "hello") {
		t.Fatal("expected true for equal strings")
	}
	if secureEquals("hello", "world") {
		t.Fatal("expected false for different strings")
	}
	if secureEquals("abc", "abcd") {
		t.Fatal("expected false for different lengths")
	}
}

func TestPruneExpiredRoutes(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	routes := []routing.Route{
		{Destination: "keep-future", ExpiresAt: &future},
		{Destination: "drop-past", ExpiresAt: &past},
		{Destination: "keep-noexpiry"},
	}
	result := pruneExpiredRoutes(routes)
	if len(result) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(result))
	}
	for _, r := range result {
		if r.Destination == "drop-past" {
			t.Fatal("expired route should have been pruned")
		}
	}
}

func TestParseIntQuery_Missing(t *testing.T) {
	req := httptest.NewRequest("GET", "/?foo=bar", nil)
	v, ok, err := parseIntQuery(req, "missing")
	if v != 0 || ok || err != nil {
		t.Fatalf("expected 0, false, nil; got %d, %v, %v", v, ok, err)
	}
}

func TestParseIntQuery_Valid(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=5", nil)
	v, ok, err := parseIntQuery(req, "limit")
	if v != 5 || !ok || err != nil {
		t.Fatalf("expected 5, true, nil; got %d, %v, %v", v, ok, err)
	}
}

func TestParseIntQuery_Invalid(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=abc", nil)
	_, ok, err := parseIntQuery(req, "limit")
	if !ok || err == nil {
		t.Fatal("expected ok=true, err!=nil for invalid int")
	}
}

func TestParseIntQueryMulti_FirstFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/?n=10", nil)
	v, ok, err := parseIntQueryMulti(req, "count", "n", "limit")
	if v != 10 || !ok || err != nil {
		t.Fatalf("expected 10, true, nil; got %d, %v, %v", v, ok, err)
	}
}

func TestParseIntQueryMulti_NoneFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	v, ok, err := parseIntQueryMulti(req, "a", "b")
	if v != 0 || ok || err != nil {
		t.Fatalf("expected 0, false, nil; got %d, %v, %v", v, ok, err)
	}
}

func TestHasValidAPIKey_BearerMain(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer mysecret")
	if !hasValidAPIKey(req, "mysecret") {
		t.Fatal("expected valid for Bearer token")
	}
}

func TestHasValidAPIKey_XAPIKeyMain(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "mysecret")
	if !hasValidAPIKey(req, "mysecret") {
		t.Fatal("expected valid for X-API-Key header")
	}
}

func TestHasValidAPIKey_WrongMain(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if hasValidAPIKey(req, "mysecret") {
		t.Fatal("expected false for wrong token")
	}
}

func TestHasValidAPIKey_MissingMain(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if hasValidAPIKey(req, "mysecret") {
		t.Fatal("expected false for missing token")
	}
}

func TestMatchPathRules_ExactMain(t *testing.T) {
	rules := []pathRule{{value: "/health", prefix: false}}
	if !matchPathRules("/health", "GET", rules) {
		t.Fatal("expected match for exact path")
	}
	if matchPathRules("/healthz", "GET", rules) {
		t.Fatal("expected no match for different path")
	}
}

func TestMatchPathRules_PrefixMain(t *testing.T) {
	rules := []pathRule{{value: "/admin/", prefix: true}}
	if !matchPathRules("/admin/backup", "POST", rules) {
		t.Fatal("expected prefix match")
	}
	if matchPathRules("/public/data", "GET", rules) {
		t.Fatal("expected no prefix match")
	}
}

func TestMatchPathRules_WithMethod(t *testing.T) {
	rules := []pathRule{{value: "/blockchain/add", prefix: false, method: "POST"}}
	if !matchPathRules("/blockchain/add", "POST", rules) {
		t.Fatal("expected match for POST")
	}
	if matchPathRules("/blockchain/add", "GET", rules) {
		t.Fatal("expected no match for GET")
	}
}

func TestMatchPathRules_Empty(t *testing.T) {
	if matchPathRules("/anything", "GET", nil) {
		t.Fatal("expected no match for empty rules")
	}
}

func TestParsePeerKeys_EmptyMain(t *testing.T) {
	got, err := parsePeerKeys("")
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty map, got %v %v", got, err)
	}
}

func TestParsePeerKeys_ValidMain(t *testing.T) {
	got, err := parsePeerKeys("node1=key1,node2=key2")
	if err != nil || got["node1"] != "key1" || got["node2"] != "key2" {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestParsePeerKeys_Invalid(t *testing.T) {
	_, err := parsePeerKeys("badentry")
	if err == nil {
		t.Fatal("expected error for invalid peer key entry")
	}
}

func TestParseCIDRList_EmptyMain(t *testing.T) {
	got, err := parseCIDRList("")
	if err != nil || got != nil {
		t.Fatalf("expected nil, nil; got %v %v", got, err)
	}
}

func TestParseCIDRList_ValidMain(t *testing.T) {
	got, err := parseCIDRList("192.168.0.0/24,10.0.0.0/8")
	if err != nil || len(got) != 2 {
		t.Fatalf("expected 2 CIDRs, got %v %v", got, err)
	}
}

func TestParseCIDRList_InvalidMain(t *testing.T) {
	_, err := parseCIDRList("notacidr")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestParsePathRule_ExactMatch(t *testing.T) {
	rule, ok := parsePathRule("/health")
	if !ok || rule.value != "/health" || rule.prefix {
		t.Fatalf("unexpected: %+v %v", rule, ok)
	}
}

func TestParsePathRule_Prefix(t *testing.T) {
	rule, ok := parsePathRule("/admin/*")
	if !ok || rule.value != "/admin/" || !rule.prefix {
		t.Fatalf("unexpected: %+v %v", rule, ok)
	}
}

func TestParsePathRule_WithMethod(t *testing.T) {
	rule, ok := parsePathRule("POST:/blockchain/add")
	if !ok || rule.method != "POST" || rule.value != "/blockchain/add" {
		t.Fatalf("unexpected: %+v %v", rule, ok)
	}
}

func TestParsePathRule_EmptyMain(t *testing.T) {
	_, ok := parsePathRule("")
	if ok {
		t.Fatal("expected false for empty rule")
	}
}
