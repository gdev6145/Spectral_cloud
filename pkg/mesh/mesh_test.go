package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/routing"
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
