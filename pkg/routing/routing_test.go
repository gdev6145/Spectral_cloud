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

func TestSatellitePenalty(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRouteWithOptions("terrestrial", RouteMetric{Latency: 50, Throughput: 10}, RouteOptions{})
	_ = re.AddRouteWithOptions("satellite", RouteMetric{Latency: 20, Throughput: 10}, RouteOptions{Satellite: true})

	// Without penalty satellite wins (lower latency).
	best0, _ := re.SelectBestNextHopOpts(0)
	if best0.Destination != "satellite" {
		t.Fatalf("without penalty expected satellite, got %s", best0.Destination)
	}
	// With penalty of 300 satellite costs 320, terrestrial costs 40 → terrestrial wins.
	bestP, _ := re.SelectBestNextHopOpts(300)
	if bestP.Destination != "terrestrial" {
		t.Fatalf("with penalty expected terrestrial, got %s", bestP.Destination)
	}
}

func TestFilterByTags(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRouteWithOptions("us-west", RouteMetric{Latency: 10}, RouteOptions{
		Tags: map[string]string{"region": "us-west", "tier": "premium"},
	})
	_ = re.AddRouteWithOptions("eu-central", RouteMetric{Latency: 20}, RouteOptions{
		Tags: map[string]string{"region": "eu-central", "tier": "standard"},
	})
	_ = re.AddRoute("untagged", RouteMetric{Latency: 5, Throughput: 100})

	west := re.FilterByTags(map[string]string{"region": "us-west"})
	if len(west) != 1 || west[0].Destination != "us-west" {
		t.Fatalf("expected 1 us-west route, got %+v", west)
	}

	premium := re.FilterByTags(map[string]string{"tier": "premium"})
	if len(premium) != 1 {
		t.Fatalf("expected 1 premium route, got %d", len(premium))
	}

	none := re.FilterByTags(map[string]string{"region": "ap-southeast"})
	if len(none) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(none))
	}

	all := re.FilterByTags(map[string]string{})
	if len(all) != 3 {
		t.Fatalf("expected all 3 routes for empty filter, got %d", len(all))
	}
}

func TestAddRouteWithOptions(t *testing.T) {
	re := NewRoutingEngine()
	err := re.AddRouteWithOptions("node-opts", RouteMetric{Latency: 10, Throughput: 100}, RouteOptions{
		Satellite: true,
		Tags:      map[string]string{"region": "us-west"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, err := re.GetRoute("node-opts")
	if err != nil {
		t.Fatalf("route not found: %v", err)
	}
	if !r.Satellite {
		t.Fatal("expected satellite flag to be set")
	}
	if r.Tags["region"] != "us-west" {
		t.Fatalf("expected region=us-west, got %q", r.Tags["region"])
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

func TestUpdateRoute(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRouteWithOptions("node-1", RouteMetric{Latency: 10, Throughput: 100}, RouteOptions{
		Satellite: true,
		Tags:      map[string]string{"region": "us-east"},
	})
	ttl := 30 * time.Second
	satellite := false
	tags := map[string]string{"region": "eu-west"}
	route, err := re.UpdateRoute("node-1", UpdateRouteOptions{
		Metric:    &RouteMetric{Latency: 20, Throughput: 200},
		TTL:       &ttl,
		Satellite: &satellite,
		Tags:      &tags,
	})
	if err != nil {
		t.Fatalf("unexpected error updating route: %v", err)
	}
	if route.Metric.Latency != 20 || route.Metric.Throughput != 200 {
		t.Fatalf("unexpected metric: %+v", route.Metric)
	}
	if route.Satellite {
		t.Fatal("expected satellite flag to be updated")
	}
	if route.Tags["region"] != "eu-west" {
		t.Fatalf("unexpected tags: %+v", route.Tags)
	}
	if route.ExpiresAt == nil {
		t.Fatal("expected ttl to set expires_at")
	}
}

func TestUpdateRouteValidationAndNotFound(t *testing.T) {
	re := NewRoutingEngine()
	if _, err := re.UpdateRoute("missing", UpdateRouteOptions{}); err == nil {
		t.Fatal("expected missing route error")
	}
	_ = re.AddRoute("node-1", RouteMetric{Latency: 1, Throughput: 1})
	if _, err := re.UpdateRoute("node-1", UpdateRouteOptions{
		Metric: &RouteMetric{Latency: -1, Throughput: 1},
	}); err == nil {
		t.Fatal("expected validation error for negative latency")
	}
}

func TestAddRouteWithOptionsClonesTags(t *testing.T) {
	re := NewRoutingEngine()
	tags := map[string]string{"region": "us-east"}
	if err := re.AddRouteWithOptions("node-1", RouteMetric{Latency: 10, Throughput: 100}, RouteOptions{
		Tags: tags,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tags["region"] = "eu-west"

	route, err := re.GetRoute("node-1")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	if route.Tags["region"] != "us-east" {
		t.Fatalf("expected stored tags to be isolated from caller mutation, got %+v", route.Tags)
	}
}

func TestUpdateRouteClonesTags(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.AddRoute("node-1", RouteMetric{Latency: 10, Throughput: 100}); err != nil {
		t.Fatalf("add route: %v", err)
	}
	tags := map[string]string{"region": "us-east"}
	if _, err := re.UpdateRoute("node-1", UpdateRouteOptions{Tags: &tags}); err != nil {
		t.Fatalf("update route: %v", err)
	}
	tags["region"] = "eu-west"

	route, err := re.GetRoute("node-1")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	if route.Tags["region"] != "us-east" {
		t.Fatalf("expected updated tags to be isolated from caller mutation, got %+v", route.Tags)
	}
}

func TestBestRouteFromRoutes(t *testing.T) {
	routes := []Route{
		{Destination: "terrestrial", Metric: RouteMetric{Latency: 15, Throughput: 20}},
		{Destination: "satellite", Metric: RouteMetric{Latency: 5, Throughput: 20}, Satellite: true},
	}

	best, err := BestRouteFromRoutes(routes, SatellitePenaltyMs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best.Destination != "terrestrial" {
		t.Fatalf("expected terrestrial route, got %s", best.Destination)
	}

	best, err = BestRouteFromRoutes(routes, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if best.Destination != "satellite" {
		t.Fatalf("expected satellite route without penalty, got %s", best.Destination)
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

// ---------------------------------------------------------------------------
// Additional coverage: NewRoutingEngineFromRoutes, UpdateLinkQuality,
// GetLinkQuality, ListRoutes, Load, PrintRoutes
// ---------------------------------------------------------------------------

func TestNewRoutingEngineFromRoutes(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)
	routes := []Route{
		{Destination: "live", Metric: RouteMetric{Latency: 10}, ExpiresAt: &future},
		{Destination: "expired", Metric: RouteMetric{Latency: 5}, ExpiresAt: &past},
	}
	re := NewRoutingEngineFromRoutes(routes)
	// Expired route should be pruned during construction.
	if re.RouteCount() != 1 {
		t.Fatalf("expected 1 live route, got %d", re.RouteCount())
	}
	if _, err := re.GetRoute("live"); err != nil {
		t.Fatalf("expected live route, got %v", err)
	}
}

func TestUpdateLinkQuality_Exists(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("node-a", RouteMetric{Latency: 10, Throughput: 100})
	if err := re.UpdateLinkQuality("node-a", RouteMetric{Latency: 20, Throughput: 200}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, _ := re.GetLinkQuality("node-a")
	if m.Latency != 20 || m.Throughput != 200 {
		t.Fatalf("expected updated metrics, got %+v", m)
	}
}

func TestUpdateLinkQuality_NotFound(t *testing.T) {
	re := NewRoutingEngine()
	if err := re.UpdateLinkQuality("missing", RouteMetric{}); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestGetLinkQuality_Exists(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("node-b", RouteMetric{Latency: 5, Throughput: 50})
	m, err := re.GetLinkQuality("node-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Latency != 5 || m.Throughput != 50 {
		t.Fatalf("unexpected metric: %+v", m)
	}
}

func TestGetLinkQuality_NotFound(t *testing.T) {
	re := NewRoutingEngine()
	if _, err := re.GetLinkQuality("ghost"); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestGetLinkQuality_Expired(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRouteWithTTL("node-exp", RouteMetric{Latency: 1}, 5*time.Millisecond)
	time.Sleep(15 * time.Millisecond)
	if _, err := re.GetLinkQuality("node-exp"); err == nil {
		t.Fatal("expected error for expired route metric")
	}
}

func TestListRoutes_PrunesExpired(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("live", RouteMetric{Latency: 1})
	_ = re.AddRouteWithTTL("dead", RouteMetric{Latency: 2}, 5*time.Millisecond)
	time.Sleep(15 * time.Millisecond)
	routes := re.ListRoutes()
	if len(routes) != 1 || routes[0].Destination != "live" {
		t.Fatalf("expected only 'live' route, got %v", routes)
	}
}

func TestLoad_ReplacesRoutes(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("old", RouteMetric{Latency: 99})
	newRoutes := []Route{
		{Destination: "new1", Metric: RouteMetric{Latency: 1}},
		{Destination: "new2", Metric: RouteMetric{Latency: 2}},
	}
	re.Load(newRoutes)
	if re.RouteCount() != 2 {
		t.Fatalf("expected 2 routes after load, got %d", re.RouteCount())
	}
	if _, err := re.GetRoute("old"); err == nil {
		t.Fatal("old route should have been replaced")
	}
}

func TestPrintRoutes_DoesNotPanic(t *testing.T) {
	re := NewRoutingEngine()
	_ = re.AddRoute("node-print", RouteMetric{Latency: 5, Throughput: 100})
	past := time.Now().UTC().Add(-time.Hour)
	_ = re.AddRouteWithTTL("expired-print", RouteMetric{Latency: 1}, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	// PrintRoutes writes to stdout; we only care it doesn't panic.
	re.PrintRoutes()
	_ = past
}
