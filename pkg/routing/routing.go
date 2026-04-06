// Package routing implements a routing engine.

package routing

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RouteMetric represents metrics for a route.
type RouteMetric struct {
	Latency    int `json:"latency"`    // Latency in milliseconds
	Throughput int `json:"throughput"` // Throughput in Mbps
}

// Route represents a routing path.
type Route struct {
	Destination string            `json:"destination"`
	Metric      RouteMetric       `json:"metric"`
	AddedAt     time.Time         `json:"added_at"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
	// Satellite marks routes that traverse a satellite link. The routing engine
	// applies a configurable latency penalty to these routes so terrestrial
	// paths are preferred when both are available.
	Satellite bool              `json:"satellite,omitempty"`
	// Tags are arbitrary key-value labels useful for filtering and grouping.
	Tags      map[string]string `json:"tags,omitempty"`
}

// RouteOptions holds extended parameters for AddRouteWithOptions.
type RouteOptions struct {
	TTL       time.Duration
	Satellite bool
	Tags      map[string]string
}

// SatellitePenaltyMs is the extra latency cost (ms) added to satellite routes
// during best-hop selection. It can be overridden via SelectBestNextHopOpts.
const SatellitePenaltyMs = 300

// RoutingEngine manages routes.
type RoutingEngine struct {
	Routes []Route
	mu     sync.RWMutex
}

// NewRoutingEngine creates a new RoutingEngine.
func NewRoutingEngine() *RoutingEngine {
	return &RoutingEngine{
		Routes: []Route{},
	}
}

// NewRoutingEngineFromRoutes creates a routing engine preloaded with routes.
func NewRoutingEngineFromRoutes(routes []Route) *RoutingEngine {
	engine := &RoutingEngine{
		Routes: routes,
	}
	engine.pruneExpiredLocked()
	return engine
}

// AddRoute adds a new route with validation.
func (re *RoutingEngine) AddRoute(destination string, metric RouteMetric) error {
	return re.AddRouteWithTTL(destination, metric, 0)
}

// AddRouteWithOptions adds a route with the full set of options including TTL,
// satellite flag, and arbitrary tags.
func (re *RoutingEngine) AddRouteWithOptions(destination string, metric RouteMetric, opts RouteOptions) error {
	if destination == "" {
		return errors.New("destination cannot be empty")
	}
	if metric.Latency < 0 || metric.Throughput < 0 {
		return errors.New("latency and throughput must be non-negative")
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if opts.TTL > 0 {
		exp := now.Add(opts.TTL)
		expiresAt = &exp
	}
	tags := opts.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	re.mu.Lock()
	defer re.mu.Unlock()
	re.Routes = append(re.Routes, Route{
		Destination: destination,
		Metric:      metric,
		AddedAt:     now,
		ExpiresAt:   expiresAt,
		Satellite:   opts.Satellite,
		Tags:        tags,
	})
	return nil
}

// AddRouteWithTTL adds a route with an optional TTL.
func (re *RoutingEngine) AddRouteWithTTL(destination string, metric RouteMetric, ttl time.Duration) error {
	if destination == "" {
		return errors.New("destination cannot be empty")
	}
	if metric.Latency < 0 || metric.Throughput < 0 {
		return errors.New("latency and throughput must be non-negative")
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttl > 0 {
		exp := now.Add(ttl)
		expiresAt = &exp
	}
	re.mu.Lock()
	defer re.mu.Unlock()
	re.Routes = append(re.Routes, Route{
		Destination: destination,
		Metric:      metric,
		AddedAt:     now,
		ExpiresAt:   expiresAt,
	})
	return nil
}

// GetRoute retrieves a route by destination, checking for expiration.
func (re *RoutingEngine) GetRoute(destination string) (Route, error) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	for _, route := range re.Routes {
		if route.Destination == destination {
			if route.ExpiresAt != nil && time.Now().UTC().After(*route.ExpiresAt) {
				return Route{}, errors.New("route expired")
			}
			return route, nil
		}
	}
	return Route{}, errors.New("route not found")
}

// UpdateLinkQuality updates the link quality metrics.
func (re *RoutingEngine) UpdateLinkQuality(destination string, metric RouteMetric) error {
	re.mu.Lock()
	defer re.mu.Unlock()
	for i, route := range re.Routes {
		if route.Destination == destination {
			re.Routes[i].Metric = metric
			return nil
		}
	}
	return errors.New("route not found")
}

// GetLinkQuality retrieves link quality metrics for a route.
func (re *RoutingEngine) GetLinkQuality(destination string) (RouteMetric, error) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	for _, route := range re.Routes {
		if route.Destination == destination {
			if route.ExpiresAt != nil && time.Now().UTC().After(*route.ExpiresAt) {
				return RouteMetric{}, errors.New("route expired")
			}
			return route.Metric, nil
		}
	}
	return RouteMetric{}, errors.New("route not found")
}

// RouteCount returns the number of routes.
func (re *RoutingEngine) RouteCount() int {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.pruneExpiredLocked()
	return len(re.Routes)
}

// ListRoutes returns a copy of all routes.
func (re *RoutingEngine) ListRoutes() []Route {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.pruneExpiredLocked()
	out := make([]Route, len(re.Routes))
	copy(out, re.Routes)
	return out
}

// Load replaces the current routes with the provided list.
func (re *RoutingEngine) Load(routes []Route) {
	re.mu.Lock()
	defer re.mu.Unlock()
	out := make([]Route, len(routes))
	copy(out, routes)
	re.Routes = out
}

// SelectBestNextHop selects the route with the lowest cost using the default
// satellite penalty (SatellitePenaltyMs). See SelectBestNextHopOpts for full
// control.
func (re *RoutingEngine) SelectBestNextHop() (Route, error) {
	return re.SelectBestNextHopOpts(SatellitePenaltyMs)
}

// SelectBestNextHopOpts selects the route with the lowest cost score.
// satellitePenalty adds extra latency cost (ms) to satellite routes so
// terrestrial paths are preferred. Pass 0 to treat all routes equally.
// Expired routes are skipped. Returns an error if no valid route exists.
func (re *RoutingEngine) SelectBestNextHopOpts(satellitePenalty int) (Route, error) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	now := time.Now().UTC()
	var best *Route
	bestScore := float64(1 << 62)
	for i := range re.Routes {
		r := &re.Routes[i]
		if r.ExpiresAt != nil && now.After(*r.ExpiresAt) {
			continue
		}
		latency := float64(r.Metric.Latency)
		if latency < 0 {
			latency = 0
		}
		if r.Satellite && satellitePenalty > 0 {
			latency += float64(satellitePenalty)
		}
		throughput := float64(r.Metric.Throughput)
		// Score = effective latency minus throughput benefit. Lower is better.
		score := latency
		if throughput > 0 {
			score -= throughput
		}
		if best == nil || score < bestScore {
			best = r
			bestScore = score
		}
	}
	if best == nil {
		return Route{}, errors.New("no routes available")
	}
	return *best, nil
}

// FilterByTags returns all non-expired routes whose Tags contain every
// key-value pair in the filter map. An empty filter returns all live routes.
func (re *RoutingEngine) FilterByTags(filter map[string]string) []Route {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.pruneExpiredLocked()
	out := make([]Route, 0)
	for _, r := range re.Routes {
		if matchTags(r.Tags, filter) {
			out = append(out, r)
		}
	}
	return out
}

func matchTags(routeTags, filter map[string]string) bool {
	for k, v := range filter {
		if routeTags[k] != v {
			return false
		}
	}
	return true
}

// DeleteRoute removes the first route whose Destination matches the given string.
// Returns an error if no matching route is found.
func (re *RoutingEngine) DeleteRoute(destination string) error {
	re.mu.Lock()
	defer re.mu.Unlock()
	for i, route := range re.Routes {
		if route.Destination == destination {
			re.Routes = append(re.Routes[:i], re.Routes[i+1:]...)
			return nil
		}
	}
	return errors.New("route not found")
}

// DeleteAll removes all routes.
func (re *RoutingEngine) DeleteAll() {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.Routes = re.Routes[:0]
}

// RemoveExpiredRoutes cleans up expired routes.
func (re *RoutingEngine) RemoveExpiredRoutes() {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.pruneExpiredLocked()
}

func (re *RoutingEngine) pruneExpiredLocked() {
	if len(re.Routes) == 0 {
		return
	}
	now := time.Now().UTC()
	filtered := re.Routes[:0]
	for _, route := range re.Routes {
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			continue
		}
		filtered = append(filtered, route)
	}
	re.Routes = filtered
}

// PrintRoutes prints all routes to stdout for debugging.
func (re *RoutingEngine) PrintRoutes() {
	re.mu.RLock()
	defer re.mu.RUnlock()
	now := time.Now().UTC()
	for _, route := range re.Routes {
		expired := ""
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			expired = " [EXPIRED]"
		}
		fmt.Printf("route dst=%s latency=%dms throughput=%dMbps added=%s%s\n",
			route.Destination,
			route.Metric.Latency,
			route.Metric.Throughput,
			route.AddedAt.UTC().Format(time.RFC3339),
			expired,
		)
	}
}
