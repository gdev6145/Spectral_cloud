package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

const testTenant = "t1"

func TestAddList(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "rule1", "http://example.com", "secret", []string{string(events.EventAgentRegistered)})
	if r.ID == "" || r.Name != "rule1" || r.Tenant != testTenant {
		t.Errorf("unexpected rule: %+v", r)
	}
	rules := m.List(testTenant)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if other := m.List("other"); len(other) != 0 {
		t.Errorf("expected 0 for other tenant, got %d", len(other))
	}
}

func TestDelete(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "", nil)
	if !m.Delete(testTenant, r.ID) {
		t.Fatal("expected true")
	}
	if m.Delete(testTenant, r.ID) {
		t.Fatal("expected false on second delete")
	}
	if len(m.List(testTenant)) != 0 {
		t.Fatal("expected empty list after delete")
	}
	r2 := m.Add(testTenant, "r2", "http://x.com", "", nil)
	if m.Delete("other", r2.ID) {
		t.Fatal("wrong tenant should not be able to delete")
	}
}

func TestGetUpdate(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "old", []string{string(events.EventAgentRegistered)})

	got, ok := m.Get(testTenant, r.ID)
	if !ok {
		t.Fatal("expected rule")
	}
	if got.Name != "r" {
		t.Fatalf("unexpected rule: %+v", got)
	}

	name := "r2"
	url := "http://y.com"
	secret := "new"
	eventTypes := []string{string(events.EventAgentHeartbeat)}
	active := false
	updated, ok, err := m.Update(testTenant, r.ID, UpdateParams{
		Name:       &name,
		WebhookURL: &url,
		Secret:     &secret,
		EventTypes: &eventTypes,
		Active:     &active,
	})
	if err != nil {
		t.Fatalf("update rule: %v", err)
	}
	if !ok {
		t.Fatal("expected updated rule")
	}
	if updated.Name != name || updated.WebhookURL != url || updated.Secret != secret || updated.Active {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}
	if len(updated.EventTypes) != 1 || updated.EventTypes[0] != string(events.EventAgentHeartbeat) {
		t.Fatalf("unexpected event types: %+v", updated.EventTypes)
	}
}

func TestUpdateValidationAndTenantScope(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "", nil)

	empty := ""
	if _, ok, err := m.Update(testTenant, r.ID, UpdateParams{Name: &empty}); err == nil || !ok {
		t.Fatal("expected validation error for empty name")
	}
	if _, ok := m.Get("other", r.ID); ok {
		t.Fatal("wrong tenant should not get rule")
	}
	if _, ok, err := m.Update("other", r.ID, UpdateParams{}); err != nil || ok {
		t.Fatal("wrong tenant should not update rule")
	}
}

func TestLoadFromStoreRestoresRules(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m1 := NewWithStore(db)
	rule := m1.Add(testTenant, "persisted", "http://example.com", "secret", []string{string(events.EventAgentRegistered)})
	active := false
	if _, ok, err := m1.Update(testTenant, rule.ID, UpdateParams{Active: &active}); err != nil || !ok {
		t.Fatalf("update persisted rule: ok=%v err=%v", ok, err)
	}

	m2 := NewWithStore(db)
	n, err := m2.LoadFromStore(testTenant)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 restored rule, got %d", n)
	}
	got, ok := m2.Get(testTenant, rule.ID)
	if !ok {
		t.Fatal("expected restored rule")
	}
	if got.Name != "persisted" || got.Active {
		t.Fatalf("unexpected restored rule: %+v", got)
	}
}

func TestDeleteRemovesPersistedRule(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m1 := NewWithStore(db)
	rule := m1.Add(testTenant, "persisted", "http://example.com", "", nil)
	if !m1.Delete(testTenant, rule.ID) {
		t.Fatal("expected delete to succeed")
	}

	m2 := NewWithStore(db)
	n, err := m2.LoadFromStore(testTenant)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 restored rules, got %d", n)
	}
}

func TestMatches(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "", []string{"agent.registered"})
	ev1 := events.Event{Type: "agent.registered", TenantID: testTenant}
	ev2 := events.Event{Type: "other.event", TenantID: testTenant}
	if !m.matches(r, ev1) {
		t.Error("expected match")
	}
	if m.matches(r, ev2) {
		t.Error("expected no match")
	}
}

func TestMatchesAcceptsSeparatorAliases(t *testing.T) {
	m := New()
	ruleDot := m.Add(testTenant, "dot", "http://x.com", "", []string{"agent.registered"})
	if !m.matches(ruleDot, events.Event{Type: events.EventAgentRegistered, TenantID: testTenant}) {
		t.Fatal("expected dotted rule to match underscored event")
	}

	ruleUnderscore := m.Add(testTenant, "underscore", "http://x.com", "", []string{string(events.EventAgentRegistered)})
	if !m.matches(ruleUnderscore, events.Event{Type: "agent.registered", TenantID: testTenant}) {
		t.Fatal("expected underscored rule to match dotted event")
	}
}

func TestMatchesAllEvents(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "", nil)
	ev := events.Event{Type: "anything", TenantID: testTenant}
	if !m.matches(r, ev) {
		t.Error("expected match for empty event types")
	}
}

func TestMatchesTenantIsolation(t *testing.T) {
	m := New()
	r := m.Add(testTenant, "r", "http://x.com", "", nil)
	evOther := events.Event{Type: "anything", TenantID: "other"}
	if m.matches(r, evOther) {
		t.Error("rule should not match event from different tenant")
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
	r := m.Add(testTenant, "webhook-test", ts.URL, "", []string{"test.event"})
	ev := events.Event{Type: "test.event", TenantID: testTenant, Timestamp: time.Now().UTC()}
	m.fire(r, ev)

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received")
	}

	rules := m.List(testTenant)
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
	m.Add(testTenant, "dispatch-test", ts.URL, "", []string{"ping"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, broker)

	time.Sleep(10 * time.Millisecond)
	broker.Publish(events.Event{Type: "ping", TenantID: testTenant, Timestamp: time.Now().UTC()})

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
	r := m.Add(testTenant, "hmac-test", ts.URL, "my-secret", []string{"test.event"})
	ev := events.Event{Type: "test.event", TenantID: testTenant, Timestamp: time.Now().UTC()}
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
	r := m.Add(testTenant, "bad-url", "http://invalid-host-that-does-not-exist.xyz", "", nil)
	ev := events.Event{Type: "test", TenantID: testTenant, Timestamp: time.Now().UTC()}
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
	r := m.Add(testTenant, "inactive", ts.URL, "", []string{"test.event"})
	m.mu.Lock()
	m.rules[r.ID].Active = false
	m.mu.Unlock()

	m.dispatch(events.Event{Type: "test.event", TenantID: testTenant})
	time.Sleep(100 * time.Millisecond)
	if called {
		t.Fatal("inactive rule should not fire")
	}
}

func TestStart_NilBroker(t *testing.T) {
	m := New()
	m.Start(context.Background(), nil)
}

func TestStart_ContextCancel(t *testing.T) {
	broker := events.NewBroker()
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, broker)
	time.Sleep(10 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}
