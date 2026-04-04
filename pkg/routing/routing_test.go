package routing

import "testing"
import "time"

func TestRoutingAddAndGet(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.AddRoute("node-1", RouteMetric{Latency: 10, Throughput: 100}); err != nil {
		t.Fatalf("add route failed: %v", err)
	}
	route, err := re.GetRoute("node-1")
	if err != nil {
		t.Fatalf("get route failed: %v", err)
	}
	if route.Metric.Latency != 10 || route.Metric.Throughput != 100 {
		t.Fatalf("unexpected route metrics: %+v", route.Metric)
	}
}

func TestRoutingTTL(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.AddRouteWithTTL("node-ttl", RouteMetric{Latency: 1, Throughput: 1}, 10*time.Millisecond); err != nil {
		t.Fatalf("add route failed: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := re.GetRoute("node-ttl"); err == nil {
		t.Fatalf("expected route to expire")
	}
	re.RemoveExpiredRoutes()
	if got := re.RouteCount(); got != 0 {
		t.Fatalf("expected 0 routes after prune, got %d", got)
	}
}

func TestRoutingValidation(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.AddRoute("", RouteMetric{Latency: 1, Throughput: 1}); err == nil {
		t.Fatalf("expected error for empty destination")
	}
	if err := re.AddRoute("node", RouteMetric{Latency: -1, Throughput: 1}); err == nil {
		t.Fatalf("expected error for negative latency")
	}
}
