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
