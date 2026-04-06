package agent

import (
	"testing"
	"time"
)

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	err := r.Register(RegisterRequest{ID: "a1", TenantID: "t1", Addr: "10.0.0.1:9000", Status: StatusHealthy})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	a, ok := r.Get("t1", "a1")
	if !ok {
		t.Fatal("expected agent to be found")
	}
	if a.Status != StatusHealthy || a.Addr != "10.0.0.1:9000" {
		t.Fatalf("unexpected agent fields: %+v", a)
	}
}

func TestRegisterRequiresID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(RegisterRequest{TenantID: "t1"}); err == nil {
		t.Fatal("expected error for missing ID")
	}
}

func TestRegisterRequiresTenant(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(RegisterRequest{ID: "a1"}); err == nil {
		t.Fatal("expected error for missing tenant")
	}
}

func TestReregisterPreservesRegisteredAt(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1"})
	a1, _ := r.Get("t1", "a1")
	time.Sleep(2 * time.Millisecond)
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1", Status: StatusDegraded})
	a2, _ := r.Get("t1", "a1")
	if !a1.RegisteredAt.Equal(a2.RegisteredAt) {
		t.Fatalf("RegisteredAt changed on re-register: %v vs %v", a1.RegisteredAt, a2.RegisteredAt)
	}
	if a2.Status != StatusDegraded {
		t.Fatalf("expected status to be updated to degraded, got %s", a2.Status)
	}
}

func TestTTLExpiry(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1", TTLSeconds: 0})
	// TTLSeconds=0 means no expiry.
	_, ok := r.Get("t1", "a1")
	if !ok {
		t.Fatal("expected agent without TTL to be present")
	}

	// Use a negative approach since we can't sleep long: register with a fake
	// past expiry by registering normally, then manually expire.
	r2 := NewRegistry()
	_ = r2.Register(RegisterRequest{ID: "a2", TenantID: "t1", TTLSeconds: 1})
	// Manipulate internal state to simulate expiry for determinism in tests.
	r2.mu.Lock()
	past := time.Now().UTC().Add(-2 * time.Second)
	r2.agents["t1/a2"].ExpiresAt = &past
	r2.mu.Unlock()

	_, ok = r2.Get("t1", "a2")
	if ok {
		t.Fatal("expected expired agent to be invisible")
	}
}

func TestHeartbeat(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1", TTLSeconds: 60})
	before, _ := r.Get("t1", "a1")
	time.Sleep(2 * time.Millisecond)
	if err := r.Heartbeat("t1", "a1", 120); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	after, _ := r.Get("t1", "a1")
	if !after.LastSeen.After(before.LastSeen) {
		t.Fatal("expected LastSeen to be updated by heartbeat")
	}
	if after.ExpiresAt == nil || after.ExpiresAt.Before(before.ExpiresAt.Add(30*time.Second)) {
		t.Fatal("expected TTL to be extended by heartbeat")
	}
}

func TestHeartbeatUnknownAgent(t *testing.T) {
	r := NewRegistry()
	if err := r.Heartbeat("t1", "nonexistent", 60); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestDeregister(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1"})
	if err := r.Deregister("t1", "a1"); err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if _, ok := r.Get("t1", "a1"); ok {
		t.Fatal("expected agent to be gone after deregister")
	}
}

func TestDeregisterUnknown(t *testing.T) {
	r := NewRegistry()
	if err := r.Deregister("t1", "ghost"); err == nil {
		t.Fatal("expected error for deregistering unknown agent")
	}
}

func TestList(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1"})
	_ = r.Register(RegisterRequest{ID: "a2", TenantID: "t1"})
	_ = r.Register(RegisterRequest{ID: "a3", TenantID: "t2"})

	t1agents := r.List("t1")
	if len(t1agents) != 2 {
		t.Fatalf("expected 2 agents for t1, got %d", len(t1agents))
	}
	all := r.List("")
	if len(all) != 3 {
		t.Fatalf("expected 3 agents total, got %d", len(all))
	}
}

func TestListExcludesExpired(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "live", TenantID: "t1"})
	_ = r.Register(RegisterRequest{ID: "dead", TenantID: "t1", TTLSeconds: 1})
	r.mu.Lock()
	past := time.Now().UTC().Add(-2 * time.Second)
	r.agents["t1/dead"].ExpiresAt = &past
	r.mu.Unlock()

	agents := r.List("t1")
	if len(agents) != 1 || agents[0].ID != "live" {
		t.Fatalf("expected only live agent, got %+v", agents)
	}
}

func TestUpdateStatus(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1", Status: StatusHealthy})
	if err := r.UpdateStatus("t1", "a1", StatusDegraded); err != nil {
		t.Fatalf("update status: %v", err)
	}
	a, _ := r.Get("t1", "a1")
	if a.Status != StatusDegraded {
		t.Fatalf("expected degraded, got %s", a.Status)
	}
}

func TestPrune(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "live", TenantID: "t1"})
	_ = r.Register(RegisterRequest{ID: "dead1", TenantID: "t1", TTLSeconds: 1})
	_ = r.Register(RegisterRequest{ID: "dead2", TenantID: "t1", TTLSeconds: 1})

	past := time.Now().UTC().Add(-2 * time.Second)
	r.mu.Lock()
	r.agents["t1/dead1"].ExpiresAt = &past
	r.agents["t1/dead2"].ExpiresAt = &past
	r.mu.Unlock()

	n := r.Prune()
	if n != 2 {
		t.Fatalf("expected 2 pruned, got %d", n)
	}
	if r.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", r.Count())
	}
}

func TestCount(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Fatal("expected 0 on empty registry")
	}
	_ = r.Register(RegisterRequest{ID: "a1", TenantID: "t1"})
	_ = r.Register(RegisterRequest{ID: "a2", TenantID: "t1"})
	if r.Count() != 2 {
		t.Fatalf("expected count 2, got %d", r.Count())
	}
}

func TestListSortedByRegisteredAt(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(RegisterRequest{ID: "first", TenantID: "t1"})
	time.Sleep(2 * time.Millisecond)
	_ = r.Register(RegisterRequest{ID: "second", TenantID: "t1"})
	agents := r.List("t1")
	if len(agents) < 2 {
		t.Fatal("expected at least 2 agents")
	}
	if agents[0].ID != "first" {
		t.Fatalf("expected first to come before second, got %s first", agents[0].ID)
	}
}
