// Package agentgroup manages named groups of agents for load-balanced dispatch.
// Members are selected round-robin. The circuit breaker (if wired in) is
// consulted so that open-circuited agents are skipped.
package agentgroup

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Member is a single agent within a group.
type Member struct {
	AgentID string `json:"agent_id"`
	Weight  int    `json:"weight"` // reserved for future weighted routing; 1 = normal
}

// Group is a named collection of agents.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager holds all agent groups.
type Manager struct {
	mu      sync.RWMutex
	groups  map[string]*Group
	counter uint64
	// rrIndex tracks round-robin position per group.
	rrIndex map[string]*uint64
}

func New() *Manager {
	return &Manager{
		groups:  make(map[string]*Group),
		rrIndex: make(map[string]*uint64),
	}
}

// Create adds a new group and returns it.
func (m *Manager) Create(name string) (*Group, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	id := fmt.Sprintf("grp-%d", atomic.AddUint64(&m.counter, 1))
	g := &Group{
		ID:        id,
		Name:      name,
		Members:   []Member{},
		CreatedAt: time.Now().UTC(),
	}
	var idx uint64
	m.mu.Lock()
	m.groups[id] = g
	m.rrIndex[id] = &idx
	m.mu.Unlock()
	return g, nil
}

// Delete removes a group by ID. Returns false if not found.
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.groups[id]
	if ok {
		delete(m.groups, id)
		delete(m.rrIndex, id)
	}
	return ok
}

// Get returns the group by ID.
func (m *Manager) Get(id string) (Group, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.groups[id]
	if !ok {
		return Group{}, false
	}
	return *g, true
}

// List returns all groups sorted by name.
func (m *Manager) List() []Group {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Group, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddMember adds agentID to the group. No-ops if already a member.
func (m *Manager) AddMember(groupID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok {
		return fmt.Errorf("group %s not found", groupID)
	}
	for _, mem := range g.Members {
		if mem.AgentID == agentID {
			return nil // already a member
		}
	}
	g.Members = append(g.Members, Member{AgentID: agentID, Weight: 1})
	return nil
}

// RemoveMember removes agentID from the group.
func (m *Manager) RemoveMember(groupID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok {
		return fmt.Errorf("group %s not found", groupID)
	}
	filtered := g.Members[:0]
	for _, mem := range g.Members {
		if mem.AgentID != agentID {
			filtered = append(filtered, mem)
		}
	}
	g.Members = filtered
	return nil
}

// Next returns the next agentID in round-robin order for the group.
// allowFn, if non-nil, is called per candidate; skip if it returns false.
// Returns ("", false) if group is empty or all members are blocked.
func (m *Manager) Next(groupID string, allowFn func(agentID string) bool) (string, bool) {
	m.mu.RLock()
	g, ok := m.groups[groupID]
	idxPtr := m.rrIndex[groupID]
	if !ok || idxPtr == nil || len(g.Members) == 0 {
		m.mu.RUnlock()
		return "", false
	}
	members := make([]Member, len(g.Members))
	copy(members, g.Members)
	m.mu.RUnlock()

	n := uint64(len(members))
	start := atomic.AddUint64(idxPtr, 1) - 1
	for i := uint64(0); i < n; i++ {
		idx := (start + i) % n
		candidate := members[idx].AgentID
		if allowFn == nil || allowFn(candidate) {
			return candidate, true
		}
	}
	return "", false
}
