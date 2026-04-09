package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

func TestPingHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := agent.NewRegistry()
	_ = reg.Register(agent.RegisterRequest{ID: "a1", TenantID: "t", Addr: srv.Listener.Addr().String(), Status: agent.StatusUnknown})

	checker := New(reg, events.NewBroker(), time.Hour, 2*time.Second)
	checker.runChecks()

	r, ok := checker.ResultFor("a1")
	if !ok {
		t.Fatal("result not found for a1")
	}
	if !r.Healthy {
		t.Errorf("expected healthy, got error: %s", r.Error)
	}
	if r.LatencyMs < 0 {
		t.Errorf("unexpected latency: %d", r.LatencyMs)
	}
}

func TestPingUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	reg := agent.NewRegistry()
	_ = reg.Register(agent.RegisterRequest{ID: "a2", TenantID: "t", Addr: srv.Listener.Addr().String(), Status: agent.StatusHealthy})

	checker := New(reg, events.NewBroker(), time.Hour, 2*time.Second)
	checker.runChecks()

	r, ok := checker.ResultFor("a2")
	if !ok {
		t.Fatal("result not found for a2")
	}
	if r.Healthy {
		t.Error("expected unhealthy")
	}
	if r.HTTPCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", r.HTTPCode)
	}
}

func TestPingUnreachable(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(agent.RegisterRequest{ID: "a3", TenantID: "t", Addr: "127.0.0.1:1", Status: agent.StatusHealthy})

	checker := New(reg, events.NewBroker(), time.Hour, 200*time.Millisecond)
	checker.runChecks()

	r, ok := checker.ResultFor("a3")
	if !ok {
		t.Fatal("result not found for a3")
	}
	if r.Healthy {
		t.Error("expected unhealthy for unreachable addr")
	}
	if r.Error == "" {
		t.Error("expected error message")
	}
}

func TestNoAddrSkipped(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(agent.RegisterRequest{ID: "no-addr", TenantID: "t", Status: agent.StatusHealthy})

	checker := New(reg, events.NewBroker(), time.Hour, 2*time.Second)
	checker.runChecks()

	_, ok := checker.ResultFor("no-addr")
	if ok {
		t.Error("agent without addr should not have a result")
	}
}

func TestResultsAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := agent.NewRegistry()
	_ = reg.Register(agent.RegisterRequest{ID: "b1", TenantID: "t", Addr: srv.Listener.Addr().String()})
	_ = reg.Register(agent.RegisterRequest{ID: "b2", TenantID: "t", Addr: srv.Listener.Addr().String()})

	checker := New(reg, events.NewBroker(), time.Hour, 2*time.Second)
	checker.runChecks()

	results := checker.Results()
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestStartAndStop(t *testing.T) {
	reg := agent.NewRegistry()
	checker := New(reg, nil, 50*time.Millisecond, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	checker.Start(ctx)
	<-ctx.Done() // just verify it doesn't panic or deadlock
}
