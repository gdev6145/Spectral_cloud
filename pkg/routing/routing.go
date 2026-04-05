// Package routing implements a routing engine.

package routing

import (
	"errors"
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
	Destination string      `json:"destination"`
	Metric      RouteMetric `json:"metric"`
	AddedAt     time.Time   `json:"added_at"`
	ExpiresAt   *time.Time  `json:"expires_at,omitempty"`
}

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

// SelectBestNextHop implements an algorithm to select the best next hop.
func (re *RoutingEngine) SelectBestNextHop() (Route, error) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	if len(re.Routes) == 0 {
		return Route{}, errors.New("no routes available")
	}
	// Simplified for example purposes
	return re.Routes[0], nil
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

// PrintRoutes prints all routes for debugging.
func (re *RoutingEngine) PrintRoutes() {
	re.mu.RLock()
	defer re.mu.RUnlock()
	for _, route := range re.Routes {
		// Placeholder to avoid unused variable; real logging can be added later.
		_ = route
	}
}
