// Package healthcheck runs periodic HTTP pings against registered agents
// and updates their status in the agent registry accordingly.
package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

// Result holds the outcome of a single agent health check.
type Result struct {
	AgentID   string    `json:"agent_id"`
	Tenant    string    `json:"tenant"`
	Addr      string    `json:"addr"`
	Healthy   bool      `json:"healthy"`
	HTTPCode  int       `json:"http_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	LatencyMs int64     `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
}

// Checker periodically pings agents and updates their registry status.
type Checker struct {
	registry *agent.Registry
	broker   *events.Broker
	interval time.Duration
	timeout  time.Duration
	client   *http.Client

	mu      sync.RWMutex
	results map[string]Result // keyed by "tenant/agent_id"
}

// New creates a Checker. interval is how often to ping; timeout is per-request.
func New(reg *agent.Registry, broker *events.Broker, interval, timeout time.Duration) *Checker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Checker{
		registry: reg,
		broker:   broker,
		interval: interval,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
		results:  make(map[string]Result),
	}
}

// Start launches the background ping loop. It returns immediately.
func (c *Checker) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *Checker) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runChecks()
		}
	}
}

func (c *Checker) runChecks() {
	// Collect all active agents with a non-empty address.
	type agentRef struct {
		tenant string
		ag     agent.Agent
	}
	// The registry doesn't expose tenants directly, so we ping all agents.
	all := c.registry.List("")
	refs := make([]agentRef, 0, len(all))
	for _, ag := range all {
		if strings.TrimSpace(ag.Addr) != "" {
			// Infer tenant from agent key format "tenant/id".
			refs = append(refs, agentRef{tenant: "", ag: ag})
		}
	}

	for _, ref := range refs {
		result := c.ping(ref.ag)
		key := ref.ag.ID
		c.mu.Lock()
		c.results[key] = result
		c.mu.Unlock()

		// Auto-update agent status based on ping result.
		newStatus := agent.StatusHealthy
		if !result.Healthy {
			newStatus = agent.StatusDegraded
		}
		_ = c.registry.UpdateStatus(ref.tenant, ref.ag.ID, newStatus)

		// Publish health event.
		if c.broker != nil {
			evType := events.EventType("agent_health_ok")
			if !result.Healthy {
				evType = "agent_health_fail"
			}
			c.broker.Publish(events.Event{
				Type:      evType,
				TenantID:  ref.tenant,
				Timestamp: result.CheckedAt,
				Data: map[string]any{
					"id":         ref.ag.ID,
					"addr":       ref.ag.Addr,
					"healthy":    result.Healthy,
					"latency_ms": result.LatencyMs,
					"error":      result.Error,
				},
			})
		}
	}
}

func (c *Checker) ping(ag agent.Agent) Result {
	addr := strings.TrimSpace(ag.Addr)
	url := addr
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		url = "http://" + addr + "/health"
	}

	start := time.Now()
	result := Result{
		AgentID:   ag.ID,
		Addr:      addr,
		CheckedAt: start.UTC(),
	}

	resp, err := c.client.Get(url)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Healthy = false
		result.Error = fmt.Sprintf("connection failed: %v", err)
		return result
	}
	_ = resp.Body.Close()
	result.HTTPCode = resp.StatusCode
	result.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !result.Healthy {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return result
}

// Results returns a snapshot of the latest check results.
func (c *Checker) Results() []Result {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Result, 0, len(c.results))
	for _, r := range c.results {
		out = append(out, r)
	}
	return out
}

// ResultFor returns the latest check result for a specific agent ID.
func (c *Checker) ResultFor(agentID string) (Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.results[agentID]
	return r, ok
}
