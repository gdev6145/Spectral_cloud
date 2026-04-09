// Package scheduler runs recurring job submissions on a cron-like schedule.
// Schedules are in-memory only; they do not survive restarts.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
)

// Schedule describes a recurring job submission.
type Schedule struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Tenant     string            `json:"tenant"`
	AgentID    string            `json:"agent_id"`
	Capability string            `json:"capability"`
	Payload    map[string]any    `json:"payload,omitempty"`
	Interval   time.Duration     `json:"interval_ns"` // stored as nanoseconds for JSON
	Active     bool              `json:"active"`
	CreatedAt  time.Time         `json:"created_at"`
	LastRun    *time.Time        `json:"last_run,omitempty"`
	RunCount   uint64            `json:"run_count"`
}

// Manager runs schedule tickers and submits jobs.
type Manager struct {
	mu       sync.RWMutex
	schedules map[string]*scheduleEntry
	counter  uint64
	queue    *jobs.Queue
}

type scheduleEntry struct {
	sched  *Schedule
	cancel context.CancelFunc
}

func New(queue *jobs.Queue) *Manager {
	return &Manager{
		schedules: make(map[string]*scheduleEntry),
		queue:     queue,
	}
}

// Add creates and starts a new schedule. Returns error on invalid input.
func (m *Manager) Add(name, tenant, agentID, capability string, payload map[string]any, interval time.Duration) (*Schedule, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if interval < 50*time.Millisecond {
		return nil, fmt.Errorf("interval must be at least 50ms")
	}
	id := fmt.Sprintf("sched-%d", atomic.AddUint64(&m.counter, 1))
	s := &Schedule{
		ID:         id,
		Name:       name,
		Tenant:     tenant,
		AgentID:    agentID,
		Capability: capability,
		Payload:    payload,
		Interval:   interval,
		Active:     true,
		CreatedAt:  time.Now().UTC(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry := &scheduleEntry{sched: s, cancel: cancel}
	m.mu.Lock()
	m.schedules[id] = entry
	m.mu.Unlock()
	go m.run(ctx, entry)
	return s, nil
}

func (m *Manager) run(ctx context.Context, entry *scheduleEntry) {
	s := entry.sched
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if m.queue != nil {
				m.queue.Submit(s.Tenant, s.AgentID, s.Capability, s.Payload)
			}
			now := t.UTC()
			m.mu.Lock()
			if e, ok := m.schedules[s.ID]; ok {
				e.sched.LastRun = &now
				atomic.AddUint64(&e.sched.RunCount, 1)
			}
			m.mu.Unlock()
		}
	}
}

// Delete stops and removes the schedule by ID. Returns false if not found.
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.schedules[id]
	if !ok {
		return false
	}
	entry.cancel()
	delete(m.schedules, id)
	return true
}

// Get returns a snapshot of the schedule by ID.
func (m *Manager) Get(id string) (Schedule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.schedules[id]
	if !ok {
		return Schedule{}, false
	}
	return *entry.sched, true
}

// List returns snapshots of all schedules.
func (m *Manager) List() []Schedule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Schedule, 0, len(m.schedules))
	for _, e := range m.schedules {
		out = append(out, *e.sched)
	}
	return out
}

// StopAll cancels all running schedules (called on shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.schedules {
		e.cancel()
	}
}
