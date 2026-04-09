package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

func TestAddList(t *testing.T) {
	m := New()
	r := m.Add("rule1", "http://example.com", "secret", []string{"agent.registered"})
	if r.ID == "" || r.Name != "rule1" {
		t.Errorf("unexpected rule: %+v", r)
	}
	rules := m.List()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
}

func TestDelete(t *testing.T) {
	m := New()
	r := m.Add("r", "http://x.com", "", nil)
	if !m.Delete(r.ID) {
		t.Fatal("expected true")
	}
	if m.Delete(r.ID) {
		t.Fatal("expected false on second delete")
	}
	if len(m.List()) != 0 {
		t.Fatal("expected empty list after delete")
	}
}

func TestMatches(t *testing.T) {
	m := New()
	r := m.Add("r", "http://x.com", "", []string{"agent.registered"})
	ev1 := events.Event{Type: "agent.registered"}
	ev2 := events.Event{Type: "other.event"}
	if !m.matches(r, ev1) {
		t.Error("expected match")
	}
	if m.matches(r, ev2) {
		t.Error("expected no match")
	}
}

func TestMatchesAllEvents(t *testing.T) {
	m := New()
	r := m.Add("r", "http://x.com", "", nil) // empty = all
	ev := events.Event{Type: "anything"}
	if !m.matches(r, ev) {
		t.Error("expected match for empty event types")
	}
}

func TestFireWebhook(t *testing.T) {
	received := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Spectral-Rule") != "" {
			received <- struct{}{}
		}
	}))
	defer ts.Close()

	m := New()
	r := m.Add("webhook-test", ts.URL, "", []string{"test.event"})
	ev := events.Event{Type: "test.event", TenantID: "t1", Timestamp: time.Now().UTC()}
	m.fire(r, ev)

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received")
	}

	rules := m.List()
	if rules[0].FiredTotal != 1 {
		t.Errorf("expected FiredTotal=1, got %d", rules[0].FiredTotal)
	}
}

func TestStartDispatch(t *testing.T) {
	received := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
	}))
	defer ts.Close()

	broker := events.NewBroker()
	m := New()
	m.Add("dispatch-test", ts.URL, "", []string{"ping"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, broker)

	time.Sleep(10 * time.Millisecond) // let goroutine subscribe
	broker.Publish(events.Event{Type: "ping", TenantID: "t1", Timestamp: time.Now().UTC()})

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not dispatched via broker")
	}
}

func TestFireWebhook_WithHMAC(t *testing.T) {
sigCh := make(chan string, 1)
ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
sigCh <- r.Header.Get("X-Spectral-Signature")
w.WriteHeader(http.StatusOK)
}))
defer ts.Close()

m := New()
r := m.Add("hmac-test", ts.URL, "my-secret", []string{"test.event"})
ev := events.Event{Type: "test.event", TenantID: "t1", Timestamp: time.Now().UTC()}
m.fire(r, ev)

select {
case sig := <-sigCh:
if len(sig) == 0 {
t.Fatal("expected HMAC signature header")
}
if sig[:7] != "sha256=" {
t.Fatalf("expected sha256= prefix, got %q", sig)
}
case <-time.After(2 * time.Second):
t.Fatal("webhook not received")
}
}

func TestFireWebhook_BadURL(t *testing.T) {
m := New()
r := m.Add("bad-url", "http://invalid-host-that-does-not-exist.xyz", "", nil)
ev := events.Event{Type: "test", TenantID: "t", Timestamp: time.Now().UTC()}
// Should not panic — error is silently discarded
m.fire(r, ev)
time.Sleep(200 * time.Millisecond)
}

func TestDispatch_InactiveRule(t *testing.T) {
called := false
ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
called = true
}))
defer ts.Close()

m := New()
r := m.Add("inactive", ts.URL, "", []string{"test.event"})
// Deactivate rule
m.mu.Lock()
m.rules[r.ID].Active = false
m.mu.Unlock()

m.dispatch(events.Event{Type: "test.event"})
time.Sleep(100 * time.Millisecond)
if called {
t.Fatal("inactive rule should not fire")
}
}

func TestStart_NilBroker(t *testing.T) {
m := New()
// Should return immediately without panic
m.Start(context.Background(), nil)
}

func TestStart_ContextCancel(t *testing.T) {
broker := events.NewBroker()
m := New()
ctx, cancel := context.WithCancel(context.Background())
m.Start(ctx, broker)
time.Sleep(10 * time.Millisecond)
cancel() // should cause goroutine to exit cleanly
time.Sleep(50 * time.Millisecond)
}
