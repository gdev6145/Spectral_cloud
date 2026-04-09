package mesh

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	return "127.0.0.1:0"
}

func startTestNode(t *testing.T, extra ...func(*Config)) *Node {
	t.Helper()
	router := routing.NewRoutingEngine()
	cfg := Config{
		NodeID:            1,
		BindAddr:          freeAddr(t),
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
	}
	for _, fn := range extra {
		fn(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	node, err := Start(ctx, cfg, router)
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = node.Close()
	})
	return node
}

func TestStart_MissingRouter(t *testing.T) {
	cfg := Config{BindAddr: "127.0.0.1:0"}
	_, err := Start(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected error when router is nil")
	}
}

func TestStart_MissingBindAddr(t *testing.T) {
	_, err := Start(context.Background(), Config{}, routing.NewRoutingEngine())
	if err == nil {
		t.Fatal("expected error when BindAddr is empty")
	}
}

func TestStart_InvalidBindAddr(t *testing.T) {
	_, err := Start(context.Background(), Config{BindAddr: "not-an-addr"}, routing.NewRoutingEngine())
	if err == nil {
		t.Fatal("expected error for invalid bind address")
	}
}

func TestStart_Success(t *testing.T) {
	node := startTestNode(t)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
}

func TestClose_Idempotent(t *testing.T) {
	node := startTestNode(t)
	if err := node.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("second Close should be no-op: %v", err)
	}
}

func TestSnapshot(t *testing.T) {
	node := startTestNode(t)
	stats, cfg := node.Snapshot()
	if cfg.NodeID != 1 {
		t.Fatalf("expected NodeID=1, got %d", cfg.NodeID)
	}
	if stats.Received != 0 {
		t.Fatalf("expected 0 received on fresh node, got %d", stats.Received)
	}
}

func TestPeerHealth_EmptyInitially(t *testing.T) {
	node := startTestNode(t)
	health := node.PeerHealth()
	if len(health) != 0 {
		t.Fatalf("expected empty peer health on fresh node, got %d entries", len(health))
	}
}

func TestPeerHealth_ReturnsCopy(t *testing.T) {
	node := startTestNode(t)
	h1 := node.PeerHealth()
	h2 := node.PeerHealth()
	// Mutating one should not affect the other.
	h1["fake"] = PeerStat{Addr: "fake"}
	if _, ok := h2["fake"]; ok {
		t.Fatal("PeerHealth should return independent copies")
	}
}

func TestDefaultIntervals(t *testing.T) {
	router := routing.NewRoutingEngine()
	cfg := Config{
		NodeID:   2,
		BindAddr: "127.0.0.1:0",
		// Leave HeartbeatInterval and RouteTTL at zero to test defaults.
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()

	_, got := node.Snapshot()
	if got.HeartbeatInterval != 5*time.Second {
		t.Fatalf("expected default HeartbeatInterval=5s, got %v", got.HeartbeatInterval)
	}
	if got.RouteTTL != 30*time.Second {
		t.Fatalf("expected default RouteTTL=30s, got %v", got.RouteTTL)
	}
}

func TestGossipMaxRoutesDefault(t *testing.T) {
	router := routing.NewRoutingEngine()
	cfg := Config{BindAddr: "127.0.0.1:0", GossipMaxRoutes: 0}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()
	_, got := node.Snapshot()
	if got.GossipMaxRoutes != 50 {
		t.Fatalf("expected default GossipMaxRoutes=50, got %d", got.GossipMaxRoutes)
	}
}

func TestTwoNodeCommunication(t *testing.T) {
	router1 := routing.NewRoutingEngine()
	router2 := routing.NewRoutingEngine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node2 first so we know its address.
	node2, err := Start(ctx, Config{
		NodeID:            2,
		BindAddr:          "127.0.0.1:0",
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
		SharedKeys:        []string{"shared-secret"},
	}, router2)
	if err != nil {
		t.Fatalf("Start node2: %v", err)
	}
	defer func() { _ = node2.Close() }()

	node2Addr := node2.conn.LocalAddr().String()

	node1, err := Start(ctx, Config{
		NodeID:            1,
		BindAddr:          "127.0.0.1:0",
		Peers:             []string{node2Addr},
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
		SharedKeys:        []string{"shared-secret"},
	}, router1)
	if err != nil {
		t.Fatalf("Start node1: %v", err)
	}
	defer func() { _ = node1.Close() }()

	// Give the handshake packet time to arrive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, _ := node2.Snapshot()
		if stats.Accepted > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("node2 did not receive any accepted packets from node1")
}

// ---------------------------------------------------------------------------
// handlePacket
// ---------------------------------------------------------------------------

func makeHeartbeatWire(t *testing.T, payload controlPayload, key string) []byte {
	t.Helper()
	payloadBytes, err := marshalControlPayload(payload, key)
	if err != nil {
		t.Fatalf("marshalControlPayload: %v", err)
	}
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_HEARTBEAT,
		NodeId:      42,
		Payload:     payloadBytes,
	}
	wire, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	return wire
}

func fakeUDPAddr(t *testing.T) *net.UDPAddr {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", "127.0.0.1:19999")
	if err != nil {
		t.Fatalf("ResolveUDPAddr: %v", err)
	}
	return a
}

func TestHandlePacket_InvalidProto(t *testing.T) {
	node := startTestNode(t)
	node.handlePacket([]byte("not-protobuf"), fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.Rejected == 0 {
		t.Fatal("expected Rejected > 0 for invalid proto")
	}
	if stats.RejectedParse == 0 {
		t.Fatal("expected RejectedParse > 0 for invalid proto")
	}
}

func TestHandlePacket_WrongControlType(t *testing.T) {
	node := startTestNode(t)
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_ControlType(99), // neither HEARTBEAT nor HANDSHAKE
		NodeId:      1,
		Payload:     []byte(`{}`),
	}
	wire, _ := proto.Marshal(msg)
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.Rejected == 0 {
		t.Fatal("expected Rejected > 0 for wrong control type")
	}
}

func TestHandlePacket_TenantMismatch(t *testing.T) {
	router := routing.NewRoutingEngine()
	cfg := Config{
		NodeID:            5,
		BindAddr:          "127.0.0.1:0",
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
		TenantID:          "tenant-A",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()

	payload := controlPayload{Timestamp: time.Now().UTC().UnixMilli()}
	payloadBytes, _ := marshalControlPayload(payload, "")
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_HEARTBEAT,
		NodeId:      2,
		Payload:     payloadBytes,
		TenantId:    "tenant-B", // mismatch
	}
	wire, _ := proto.Marshal(msg)
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.RejectedAuth == 0 {
		t.Fatal("expected RejectedAuth > 0 for tenant mismatch")
	}
}

func TestHandlePacket_InvalidJSONPayload(t *testing.T) {
	node := startTestNode(t)
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_HEARTBEAT,
		NodeId:      1,
		Payload:     []byte("not-json{{{"),
	}
	wire, _ := proto.Marshal(msg)
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.RejectedParse == 0 {
		t.Fatal("expected RejectedParse > 0 for invalid JSON payload")
	}
}

func TestHandlePacket_MissingSignatureWithKey(t *testing.T) {
	router := routing.NewRoutingEngine()
	cfg := Config{
		NodeID:            6,
		BindAddr:          "127.0.0.1:0",
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
		SharedKeys:        []string{"secret-key"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()

	// Build a payload without a signature (empty key → no sig)
	payload := controlPayload{Timestamp: time.Now().UTC().UnixMilli(), Addr: "127.0.0.1:19999"}
	payloadBytes, _ := marshalControlPayload(payload, "") // no key → no sig
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_HEARTBEAT,
		NodeId:      1,
		Payload:     payloadBytes,
	}
	wire, _ := proto.Marshal(msg)
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.RejectedAuth == 0 {
		t.Fatal("expected RejectedAuth > 0 when signature missing but key required")
	}
}

func TestHandlePacket_ValidHeartbeat_NoKey(t *testing.T) {
	node := startTestNode(t)
	payload := controlPayload{
		Timestamp: time.Now().UTC().UnixMilli(),
		Addr:      "127.0.0.1:19999",
	}
	wire := makeHeartbeatWire(t, payload, "")
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.Accepted == 0 {
		t.Fatal("expected Accepted > 0 for valid heartbeat with no key")
	}
	if stats.Received == 0 {
		t.Fatal("expected Received > 0")
	}
}

func TestHandlePacket_ValidHeartbeat_WithGossipRoutes(t *testing.T) {
	node := startTestNode(t)
	payload := controlPayload{
		Timestamp: time.Now().UTC().UnixMilli(),
		Addr:      "127.0.0.1:19999",
		Routes: []gossipRoute{
			{Dst: "node-gossip-1", Lat: 10, Thr: 100},
			{Dst: "node-gossip-2", Lat: 20, Thr: 200},
		},
	}
	wire := makeHeartbeatWire(t, payload, "")
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.Accepted == 0 {
		t.Fatal("expected Accepted > 0 for valid heartbeat with gossip routes")
	}
}

func TestHandlePacket_ValidHandshake(t *testing.T) {
	node := startTestNode(t)
	payload := controlPayload{Timestamp: time.Now().UTC().UnixMilli()}
	payloadBytes, _ := marshalControlPayload(payload, "")
	msg := &meshpb.ControlMessage{
		ControlType: meshpb.ControlMessage_HANDSHAKE,
		NodeId:      7,
		Payload:     payloadBytes,
	}
	wire, _ := proto.Marshal(msg)
	node.handlePacket(wire, fakeUDPAddr(t))
	stats, _ := node.Snapshot()
	if stats.Accepted == 0 {
		t.Fatal("expected Accepted > 0 for valid handshake")
	}
}

// ---------------------------------------------------------------------------
// broadcastGossip
// ---------------------------------------------------------------------------

func TestBroadcastGossip_NoRoutes(t *testing.T) {
	// With an empty router, broadcastGossip should be a no-op (no panic).
	node := startTestNode(t)
	node.broadcastGossip() // must not panic
}

func TestBroadcastGossip_SendsToFakePeer(t *testing.T) {
	// Start a real UDP listener to act as the "peer".
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer pc.Close()
	peerAddr := pc.LocalAddr().String()

	router := routing.NewRoutingEngine()
	_ = router.AddRoute("dst-gossip", routing.RouteMetric{Latency: 5, Throughput: 100})

	cfg := Config{
		NodeID:            10,
		BindAddr:          "127.0.0.1:0",
		HeartbeatInterval: 10 * time.Second,
		RouteTTL:          30 * time.Second,
		Peers:             []string{peerAddr},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := pc.ReadFrom(buf)
		if err == nil && n > 0 {
			close(done)
		}
	}()

	node.broadcastGossip()

	select {
	case <-done:
		// peer received a packet — success
	case <-time.After(2 * time.Second):
		t.Fatal("peer did not receive a gossip packet within 2s")
	}
}

// ---------------------------------------------------------------------------
// gossipLoop
// ---------------------------------------------------------------------------

func TestGossipLoop_Fires(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer pc.Close()
	peerAddr := pc.LocalAddr().String()

	router := routing.NewRoutingEngine()
	_ = router.AddRoute("dst-loop", routing.RouteMetric{Latency: 1, Throughput: 10})

	cfg := Config{
		NodeID:            20,
		BindAddr:          "127.0.0.1:0",
		HeartbeatInterval: 30 * time.Second,
		GossipInterval:    50 * time.Millisecond, // very short so the test is fast
		RouteTTL:          30 * time.Second,
		GossipMaxRoutes:   10,
		Peers:             []string{peerAddr},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := Start(ctx, cfg, router)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = node.Close() }()

	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, readErr := pc.ReadFrom(buf)
	if readErr != nil || n == 0 {
		t.Fatal("gossipLoop did not fire within 2s")
	}
}

// ---------------------------------------------------------------------------
// verifyControlPayload
// ---------------------------------------------------------------------------

func TestVerifyControlPayload_NoKeys(t *testing.T) {
	p := controlPayload{Timestamp: 123}
	if !verifyControlPayload(p, nil) {
		t.Fatal("expected true with no keys")
	}
}

func TestVerifyControlPayload_EmptySignature(t *testing.T) {
	p := controlPayload{Timestamp: 123, Signature: ""}
	if verifyControlPayload(p, []string{"somekey"}) {
		t.Fatal("expected false when signature is empty but key is required")
	}
}

func TestVerifyControlPayload_WrongKey(t *testing.T) {
	p := controlPayload{Timestamp: 456, Addr: "test"}
	data, _ := marshalControlPayload(p, "correct-key")
	var signed controlPayload
	_ = json.Unmarshal(data, &signed)
	if verifyControlPayload(signed, []string{"wrong-key"}) {
		t.Fatal("expected false with wrong key")
	}
}

func TestVerifyControlPayload_CorrectKey(t *testing.T) {
	p := controlPayload{Timestamp: 789, Addr: "test"}
	data, _ := marshalControlPayload(p, "mykey")
	var signed controlPayload
	_ = json.Unmarshal(data, &signed)
	if !verifyControlPayload(signed, []string{"mykey"}) {
		t.Fatal("expected true with correct key")
	}
}

func TestVerifyControlPayload_FirstKeyMatches(t *testing.T) {
	p := controlPayload{Timestamp: 101, Addr: "x"}
	data, _ := marshalControlPayload(p, "key-a")
	var signed controlPayload
	_ = json.Unmarshal(data, &signed)
	// multiple keys — first one is wrong, second is correct
	if !verifyControlPayload(signed, []string{"wrong", "key-a"}) {
		t.Fatal("expected true when one of the candidate keys matches")
	}
}

// ---------------------------------------------------------------------------
// server.go — NewServer / SendData / SendControl
// ---------------------------------------------------------------------------

func TestNewServer(t *testing.T) {
	m, err := auth.NewManagerFromEnv("testtenant:testkey")
	if err != nil {
		t.Fatalf("NewManagerFromEnv: %v", err)
	}
	s := NewServer(m)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestSendData_Unauthenticated(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("t:k")
	s := NewServer(m)
	_, err := s.SendData(context.Background(), &meshpb.DataMessage{})
	if err == nil {
		t.Fatal("expected error for unauthenticated request")
	}
}

func TestSendData_TenantMismatch(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("tenant1:key1")
	s := NewServer(m)
	md := metadata.New(map[string]string{"x-api-key": "key1"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	msg := &meshpb.DataMessage{TenantId: "other-tenant"}
	_, err := s.SendData(ctx, msg)
	if err == nil {
		t.Fatal("expected error for tenant mismatch")
	}
}

func TestSendData_Success(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("tenant1:key1")
	s := NewServer(m)
	md := metadata.New(map[string]string{"x-api-key": "key1"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	msg := &meshpb.DataMessage{
		SourceId:      1,
		DestinationId: 2,
		TenantId:      "tenant1",
	}
	ack, err := s.SendData(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.TenantId != "tenant1" {
		t.Fatalf("expected tenant1, got %s", ack.TenantId)
	}
	// server swaps src/dst in the ack
	if ack.SourceId != 2 {
		t.Fatalf("expected SourceId=2 (swapped from DestinationId), got %d", ack.SourceId)
	}
}

func TestSendControl_Unauthenticated(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("t:k")
	s := NewServer(m)
	_, err := s.SendControl(context.Background(), &meshpb.ControlMessage{})
	if err == nil {
		t.Fatal("expected error for unauthenticated request")
	}
}

func TestSendControl_TenantMismatch(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("tenant1:key1")
	s := NewServer(m)
	md := metadata.New(map[string]string{"x-api-key": "key1"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, err := s.SendControl(ctx, &meshpb.ControlMessage{TenantId: "bad-tenant"})
	if err == nil {
		t.Fatal("expected error for tenant mismatch")
	}
}

func TestSendControl_Success(t *testing.T) {
	m, _ := auth.NewManagerFromEnv("tenant1:key1")
	s := NewServer(m)
	md := metadata.New(map[string]string{"x-api-key": "key1"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	msg := &meshpb.ControlMessage{
		NodeId:   99,
		TenantId: "tenant1",
	}
	ack, err := s.SendControl(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.TenantId != "tenant1" {
		t.Fatalf("expected tenant1, got %s", ack.TenantId)
	}
}
