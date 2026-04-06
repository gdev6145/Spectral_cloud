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

func TestDeleteRoute(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("node-1", RouteMetric{Latency: 10, Throughput: 100})
	_ = re.AddRoute("node-2", RouteMetric{Latency: 20, Throughput: 50})

	if err := re.DeleteRoute("node-1"); err != nil {
		t.Fatalf("unexpected error deleting route: %v", err)
	}
	if re.RouteCount() != 1 {
		t.Fatalf("expected 1 route after delete, got %d", re.RouteCount())
	}
	if _, err := re.GetRoute("node-1"); err == nil {
		t.Fatal("expected deleted route to be gone")
	}
}

func TestDeleteRouteNotFound(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.DeleteRoute("ghost"); err == nil {
		t.Fatal("expected error deleting non-existent route")
	}
}

func TestDeleteAll(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("a", RouteMetric{Latency: 1, Throughput: 1})
	_ = re.AddRoute("b", RouteMetric{Latency: 2, Throughput: 2})
	re.DeleteAll()
	if re.RouteCount() != 0 {
		t.Fatalf("expected 0 routes after DeleteAll, got %d", re.RouteCount())
	}
}

func TestSelectBestNextHopEmpty(t *testing.T) {
	re := NewRoutingEngine()
	if _, err := re.SelectBestNextHop(); err == nil {
		t.Fatalf("expected error for empty routing engine")
	}
}

func TestSelectBestNextHopPreferLowLatency(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("slow", RouteMetric{Latency: 100, Throughput: 0})
	_ = re.AddRoute("fast", RouteMetric{Latency: 10, Throughput: 0})
	_ = re.AddRoute("medium", RouteMetric{Latency: 50, Throughput: 0})
	best, err := re.SelectBestNextHop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best.Destination != "fast" {
		t.Fatalf("expected 'fast', got %q", best.Destination)
	}
}

func TestSelectBestNextHopPreferHighThroughput(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("low-bw", RouteMetric{Latency: 10, Throughput: 10})
	_ = re.AddRoute("high-bw", RouteMetric{Latency: 10, Throughput: 200})
	best, err := re.SelectBestNextHop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best.Destination != "high-bw" {
		t.Fatalf("expected 'high-bw', got %q", best.Destination)
	}
}

func TestSelectBestNextHopSkipsExpired(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRouteWithTTL("expired", RouteMetric{Latency: 1, Throughput: 999}, 1*time.Millisecond)
	_ = re.AddRoute("active", RouteMetric{Latency: 50, Throughput: 0})
	time.Sleep(5 * time.Millisecond)
	best, err := re.SelectBestNextHop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best.Destination != "active" {
		t.Fatalf("expected 'active', got %q", best.Destination)
	}
}
