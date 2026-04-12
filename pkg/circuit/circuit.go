// Package circuit implements a per-agent circuit breaker.
// Each breaker tracks consecutive failures and moves through
// Closed → Open → HalfOpen → Closed states automatically.
package circuit

import (
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State string

const (
	StateClosed   State = "closed"    // normal, requests pass through
	StateOpen     State = "open"      // tripped, requests are rejected
	StateHalfOpen State = "half_open" // probe phase, one request allowed
)

// Breaker is a single circuit breaker for one agent.
type Breaker struct {
	AgentID      string        `json:"agent_id"`
	State        State         `json:"state"`
	Failures     int           `json:"failures"`
	Successes    int           `json:"successes"`
	Threshold    int           `json:"threshold"` // failures before opening
	ResetTimeout time.Duration `json:"-"`
	OpenedAt     *time.Time    `json:"opened_at,omitempty"`
	LastFailure  *time.Time    `json:"last_failure,omitempty"`
	mu           sync.Mutex
}

// Manager holds circuit breakers for all agents.
type Manager struct {
	mu           sync.RWMutex
	breakers     map[string]*Breaker
	threshold    int
	resetTimeout time.Duration
}

// New creates a Manager with the given default threshold and reset timeout.
func New(threshold int, resetTimeout time.Duration) *Manager {
	if threshold <= 0 {
		threshold = 5
	}
	if resetTimeout <= 0 {
		resetTimeout = 30 * time.Second
	}
	return &Manager{
		breakers:     make(map[string]*Breaker),
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

func (m *Manager) getOrCreate(agentID string) *Breaker {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.breakers[agentID]
	if !ok {
		b = &Breaker{
			AgentID:      agentID,
			State:        StateClosed,
			Threshold:    m.threshold,
			ResetTimeout: m.resetTimeout,
		}
		m.breakers[agentID] = b
	}
	return b
}

// RecordSuccess records a successful call for agentID.
// If in HalfOpen, closes the circuit.
func (m *Manager) RecordSuccess(agentID string) {
	b := m.getOrCreate(agentID)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Successes++
	b.Failures = 0
	if b.State == StateHalfOpen {
		b.State = StateClosed
		b.OpenedAt = nil
	}
}

// RecordFailure records a failed call. Opens the circuit if threshold reached.
// Returns the new state.
func (m *Manager) RecordFailure(agentID string) State {
	b := m.getOrCreate(agentID)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	b.Failures++
	b.LastFailure = &now
	if b.State == StateClosed && b.Failures >= b.Threshold {
		b.State = StateOpen
		b.OpenedAt = &now
	} else if b.State == StateHalfOpen {
		// probe failed, reopen
		b.State = StateOpen
		b.OpenedAt = &now
	}
	return b.State
}

// Allow returns true if a request should be allowed through.
// Open circuits auto-transition to HalfOpen after ResetTimeout.
func (m *Manager) Allow(agentID string) bool {
	b := m.getOrCreate(agentID)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.State {
	case StateClosed:
		return true
	case StateOpen:
		if b.OpenedAt != nil && time.Since(*b.OpenedAt) >= b.ResetTimeout {
			b.State = StateHalfOpen
			return true
		}
		return false
	case StateHalfOpen:
		return true
	}
	return true
}

// Reset manually closes the circuit for agentID.
func (m *Manager) Reset(agentID string) {
	b := m.getOrCreate(agentID)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.State = StateClosed
	b.Failures = 0
	b.OpenedAt = nil
}

// Get returns a snapshot of the breaker for agentID.
func (m *Manager) Get(agentID string) Breaker {
	b := m.getOrCreate(agentID)
	b.mu.Lock()
	defer b.mu.Unlock()
	return Breaker{
		AgentID:     b.AgentID,
		State:       b.State,
		Failures:    b.Failures,
		Successes:   b.Successes,
		Threshold:   b.Threshold,
		OpenedAt:    b.OpenedAt,
		LastFailure: b.LastFailure,
	}
}

// List returns snapshots of all breakers.
func (m *Manager) List() []Breaker {
	m.mu.RLock()
	ids := make([]string, 0, len(m.breakers))
	for id := range m.breakers {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	out := make([]Breaker, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.Get(id))
	}
	return out
}

// Delete removes the breaker for agentID.
func (m *Manager) Delete(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.breakers, agentID)
}
