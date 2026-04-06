// Package agent provides an in-memory registry for edge AI agents.
//
// Agents register themselves with an optional TTL. If a TTL is set the entry
// is considered expired once that deadline passes; agents must send periodic
// Heartbeat calls to stay alive. Expired agents are pruned lazily on List/Get.
package agent

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Status represents the self-reported health of an agent.
type Status string

const (
	StatusHealthy  Status = "healthy"
	StatusDegraded Status = "degraded"
	StatusUnknown  Status = "unknown"
)

// Agent is a registered edge AI agent.
type Agent struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	Addr         string            `json:"addr,omitempty"`
	Status       Status            `json:"status"`
	Tags         map[string]string `json:"tags,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
	LastSeen     time.Time         `json:"last_seen"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
}

// RegisterRequest carries the fields a caller may set when registering.
type RegisterRequest struct {
	ID       string            `json:"id"`
	TenantID string            `json:"tenant_id"`
	Addr     string            `json:"addr,omitempty"`
	Status   Status            `json:"status,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	// TTLSeconds, if > 0, sets an expiry on the registration.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
}

// Registry is a thread-safe in-memory store for agents.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*Agent // keyed by "<tenantID>/<agentID>"
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*Agent)}
}

func agentKey(tenantID, id string) string {
	return tenantID + "/" + id
}

// Register adds or refreshes an agent. On re-registration the original
// RegisteredAt timestamp is preserved.
func (r *Registry) Register(req RegisterRequest) error {
	if req.ID == "" {
		return errors.New("agent ID is required")
	}
	if req.TenantID == "" {
		return errors.New("tenant ID is required")
	}
	status := req.Status
	if status == "" {
		status = StatusUnknown
	}
	now := time.Now().UTC()
	var expiresAt *time.Time
	if req.TTLSeconds > 0 {
		exp := now.Add(time.Duration(req.TTLSeconds) * time.Second)
		expiresAt = &exp
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := agentKey(req.TenantID, req.ID)
	registeredAt := now
	if existing, ok := r.agents[key]; ok {
		registeredAt = existing.RegisteredAt
	}

	tags := req.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	r.agents[key] = &Agent{
		ID:           req.ID,
		TenantID:     req.TenantID,
		Addr:         req.Addr,
		Status:       status,
		Tags:         tags,
		RegisteredAt: registeredAt,
		LastSeen:     now,
		ExpiresAt:    expiresAt,
	}
	return nil
}

// Heartbeat refreshes LastSeen and optionally extends the TTL.
// Returns an error if the agent is not found or has already expired.
func (r *Registry) Heartbeat(tenantID, id string, ttlSeconds int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := agentKey(tenantID, id)
	a, ok := r.agents[key]
	if !ok {
		return errors.New("agent not found")
	}
	now := time.Now().UTC()
	if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
		delete(r.agents, key)
		return errors.New("agent not found")
	}
	a.LastSeen = now
	if ttlSeconds > 0 {
		exp := now.Add(time.Duration(ttlSeconds) * time.Second)
		a.ExpiresAt = &exp
	}
	return nil
}

// UpdateStatus updates the self-reported status of a registered agent.
func (r *Registry) UpdateStatus(tenantID, id string, status Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := agentKey(tenantID, id)
	a, ok := r.agents[key]
	if !ok {
		return errors.New("agent not found")
	}
	now := time.Now().UTC()
	if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
		delete(r.agents, key)
		return errors.New("agent not found")
	}
	a.Status = status
	a.LastSeen = now
	return nil
}

// Deregister removes an agent. Returns an error if not found.
func (r *Registry) Deregister(tenantID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := agentKey(tenantID, id)
	if _, ok := r.agents[key]; !ok {
		return errors.New("agent not found")
	}
	delete(r.agents, key)
	return nil
}

// Get returns an agent by tenant and ID. Returns false if not found or expired.
func (r *Registry) Get(tenantID, id string) (Agent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := agentKey(tenantID, id)
	a, ok := r.agents[key]
	if !ok {
		return Agent{}, false
	}
	now := time.Now().UTC()
	if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
		delete(r.agents, key)
		return Agent{}, false
	}
	return *a, true
}

// List returns all live agents for a tenant, sorted by RegisteredAt.
// Pass "" for tenantID to list agents across all tenants.
func (r *Registry) List(tenantID string) []Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	out := make([]Agent, 0, len(r.agents))
	for key, a := range r.agents {
		if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
			delete(r.agents, key)
			continue
		}
		if tenantID != "" && a.TenantID != tenantID {
			continue
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RegisteredAt.Before(out[j].RegisteredAt)
	})
	return out
}

// Prune removes all expired agents and returns the count removed.
func (r *Registry) Prune() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	count := 0
	for key, a := range r.agents {
		if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
			delete(r.agents, key)
			count++
		}
	}
	return count
}

// Count returns the number of live agents across all tenants.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now().UTC()
	count := 0
	for _, a := range r.agents {
		if a.ExpiresAt != nil && now.After(*a.ExpiresAt) {
			continue
		}
		count++
	}
	return count
}
