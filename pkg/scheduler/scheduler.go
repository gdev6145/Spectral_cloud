// Package scheduler runs recurring job submissions on a cron-like schedule.
// Schedules are persisted to BoltDB when a Persister is provided, and survive
// restarts; without one they are in-memory only.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
)

// Persister is the subset of store.Store methods that the scheduler needs.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

const schedKeyPrefix = "sched_"

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
	mu        sync.RWMutex
	schedules map[string]*scheduleEntry
	counter   uint64
	queue     *jobs.Queue
	store     Persister
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

// NewWithStore creates a Manager that persists schedules via p.
// Call LoadFromStore after construction to restore schedules from a previous run.
func NewWithStore(queue *jobs.Queue, p Persister) *Manager {
	return &Manager{
		schedules: make(map[string]*scheduleEntry),
		queue:     queue,
		store:     p,
	}
}

// LoadFromStore reads all persisted schedules for a tenant and re-arms them.
// Returns the number of schedules loaded.
func (m *Manager) LoadFromStore(ctx context.Context, tenant string) (int, error) {
	if m.store == nil {
		return 0, nil
	}
	var loaded []Schedule
	if err := m.store.ScanPrefix(tenant, schedKeyPrefix, func(_, val []byte) error {
		var s Schedule
		if err := json.Unmarshal(val, &s); err != nil {
			log.Printf("warn: skipping corrupted persisted schedule for tenant %q: %v", tenant, err)
			return nil
		}
		loaded = append(loaded, s)
		return nil
	}); err != nil {
		return 0, err
	}

	m.mu.Lock()
	for i := range loaded {
		s := loaded[i]
		if n := parseSchedNum(s.ID); n > m.counter {
			m.counter = n
		}
		if !s.Active {
			continue
		}
		schedCtx, cancel := context.WithCancel(ctx)
		entry := &scheduleEntry{sched: &s, cancel: cancel}
		m.schedules[s.ID] = entry
		go m.run(schedCtx, entry)
	}
	n := len(loaded)
	m.mu.Unlock()
	return n, nil
}

func parseSchedNum(id string) uint64 {
	if !strings.HasPrefix(id, "sched-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[6:], "%d", &n)
	return n
}

func (m *Manager) persist(s *Schedule) {
	if m.store == nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		log.Printf("warn: failed to marshal schedule %q for tenant %q: %v", s.ID, s.Tenant, err)
		return
	}
	if err := m.store.PutKV(s.Tenant, schedKeyPrefix+s.ID, data); err != nil {
		log.Printf("warn: failed to persist schedule %q for tenant %q: %v", s.ID, s.Tenant, err)
	}
}

func (m *Manager) unpersist(s *Schedule) {
	if m.store == nil {
		return
	}
	if err := m.store.DeleteKV(s.Tenant, schedKeyPrefix+s.ID); err != nil {
		log.Printf("warn: failed to delete persisted schedule %q for tenant %q: %v", s.ID, s.Tenant, err)
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
	m.persist(s)
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
				m.persist(e.sched)
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
	m.unpersist(entry.sched)
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
