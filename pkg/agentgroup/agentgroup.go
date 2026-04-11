// Package agentgroup manages named groups of agents for load-balanced dispatch.
// Members are selected round-robin. The circuit breaker (if wired in) is
// consulted so that open-circuited agents are skipped.
// Groups are persisted to BoltDB when a Persister is provided.
package agentgroup

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Persister is the subset of store.Store methods that agentgroup needs.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

const groupKeyPrefix = "grp_"

// Member is a single agent within a group.
type Member struct {
	AgentID string `json:"agent_id"`
	Weight  int    `json:"weight"` // weighted round-robin share; 1 = normal
}

// Group is a named collection of agents.
type Group struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
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
	store   Persister
}

// UpdateParams describes a partial group update.
type UpdateParams struct {
	Name *string
}

func normalizeWeight(weight int) (int, error) {
	if weight <= 0 {
		return 0, fmt.Errorf("weight must be greater than zero")
	}
	return weight, nil
}

func memberWeight(weight int) int {
	if weight <= 0 {
		return 1
	}
	return weight
}

func New() *Manager {
	return &Manager{
		groups:  make(map[string]*Group),
		rrIndex: make(map[string]*uint64),
	}
}

// NewWithStore creates a Manager that persists groups via p.
// Call LoadFromStore after construction to restore groups from a previous run.
func NewWithStore(p Persister) *Manager {
	return &Manager{
		groups:  make(map[string]*Group),
		rrIndex: make(map[string]*uint64),
		store:   p,
	}
}

// LoadFromStore reads all persisted groups for a tenant and restores them.
// Returns the number of groups loaded.
func (m *Manager) LoadFromStore(tenant string) (int, error) {
	if m.store == nil {
		return 0, nil
	}
	var loaded []Group
	if err := m.store.ScanPrefix(tenant, groupKeyPrefix, func(_, val []byte) error {
		var g Group
		if err := json.Unmarshal(val, &g); err != nil {
			log.Printf("warn: skipping corrupted persisted agent group for tenant %q: %v", tenant, err)
			return nil
		}
		loaded = append(loaded, g)
		return nil
	}); err != nil {
		return 0, err
	}
	m.mu.Lock()
	for i := range loaded {
		g := loaded[i]
		if n := parseGroupNum(g.ID); n > m.counter {
			m.counter = n
		}
		var idx uint64
		gCopy := g
		m.groups[g.ID] = &gCopy
		m.rrIndex[g.ID] = &idx
	}
	m.mu.Unlock()
	return len(loaded), nil
}

func parseGroupNum(id string) uint64 {
	if !strings.HasPrefix(id, "grp-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[4:], "%d", &n)
	return n
}

func (m *Manager) persist(g *Group) {
	if m.store == nil {
		return
	}
	data, err := json.Marshal(g)
	if err != nil {
		log.Printf("warn: failed to marshal agent group %q for tenant %q: %v", g.ID, g.Tenant, err)
		return
	}
	if err := m.store.PutKV(g.Tenant, groupKeyPrefix+g.ID, data); err != nil {
		log.Printf("warn: failed to persist agent group %q for tenant %q: %v", g.ID, g.Tenant, err)
	}
}

func (m *Manager) unpersist(g *Group) {
	if m.store == nil {
		return
	}
	if err := m.store.DeleteKV(g.Tenant, groupKeyPrefix+g.ID); err != nil {
		log.Printf("warn: failed to delete persisted agent group %q for tenant %q: %v", g.ID, g.Tenant, err)
	}
}

// Create adds a new group for tenant and returns it.
func (m *Manager) Create(tenant, name string) (*Group, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	id := fmt.Sprintf("grp-%d", atomic.AddUint64(&m.counter, 1))
	g := &Group{
		ID:        id,
		Tenant:    tenant,
		Name:      name,
		Members:   []Member{},
		CreatedAt: time.Now().UTC(),
	}
	var idx uint64
	m.mu.Lock()
	m.groups[id] = g
	m.rrIndex[id] = &idx
	m.persist(g)
	m.mu.Unlock()
	return g, nil
}

// Update mutates a group for the given tenant.
func (m *Manager) Update(tenant, id string, params UpdateParams) (Group, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groups[id]
	if !ok || g.Tenant != tenant {
		return Group{}, false, nil
	}
	if params.Name != nil {
		if *params.Name == "" {
			return Group{}, true, fmt.Errorf("name is required")
		}
		g.Name = *params.Name
	}
	m.persist(g)
	return *g, true, nil
}

// Delete removes a group by ID for the given tenant. Returns false if not found or belongs to another tenant.
func (m *Manager) Delete(tenant, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok || g.Tenant != tenant {
		return false
	}
	m.unpersist(g)
	delete(m.groups, id)
	delete(m.rrIndex, id)
	return true
}

// Get returns the group by ID. Returns false if not found or belongs to another tenant.
func (m *Manager) Get(tenant, id string) (Group, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.groups[id]
	if !ok || g.Tenant != tenant {
		return Group{}, false
	}
	return *g, true
}

// List returns all groups for the given tenant sorted by name.
// Pass "" to list groups across all tenants.
func (m *Manager) List(tenant string) []Group {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Group, 0, len(m.groups))
	for _, g := range m.groups {
		if tenant != "" && g.Tenant != tenant {
			continue
		}
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddMember adds agentID to the group with default weight 1.
func (m *Manager) AddMember(tenant, groupID, agentID string) error {
	return m.AddMemberWithWeight(tenant, groupID, agentID, 1)
}

// AddMemberWithWeight adds agentID to the group. No-ops if already a member.
func (m *Manager) AddMemberWithWeight(tenant, groupID, agentID string, weight int) error {
	weight, err := normalizeWeight(weight)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.Tenant != tenant {
		return fmt.Errorf("group %s not found", groupID)
	}
	for _, mem := range g.Members {
		if mem.AgentID == agentID {
			return nil // already a member
		}
	}
	g.Members = append(g.Members, Member{AgentID: agentID, Weight: weight})
	m.persist(g)
	return nil
}

// UpdateMemberWeight changes a member weight in place.
func (m *Manager) UpdateMemberWeight(tenant, groupID, agentID string, weight int) (Group, bool, error) {
	weight, err := normalizeWeight(weight)
	if err != nil {
		return Group{}, true, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.Tenant != tenant {
		return Group{}, false, nil
	}
	for i := range g.Members {
		if g.Members[i].AgentID == agentID {
			g.Members[i].Weight = weight
			m.persist(g)
			return *g, true, nil
		}
	}
	return Group{}, false, nil
}

// RemoveMember removes agentID from the group.
func (m *Manager) RemoveMember(tenant, groupID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.Tenant != tenant {
		return fmt.Errorf("group %s not found", groupID)
	}
	removed := false
	filtered := g.Members[:0]
	for _, mem := range g.Members {
		if mem.AgentID != agentID {
			filtered = append(filtered, mem)
			continue
		}
		removed = true
	}
	if !removed {
		return fmt.Errorf("group member not found")
	}
	g.Members = filtered
	m.persist(g)
	return nil
}

// Next returns the next agentID in weighted round-robin order for the group.
// allowFn, if non-nil, is called per candidate; skip if it returns false.
// Returns ("", false) if group is empty or all members are blocked.
func (m *Manager) Next(tenant, groupID string, allowFn func(agentID string) bool) (string, bool) {
	m.mu.RLock()
	g, ok := m.groups[groupID]
	idxPtr := m.rrIndex[groupID]
	if !ok || g.Tenant != tenant || idxPtr == nil || len(g.Members) == 0 {
		m.mu.RUnlock()
		return "", false
	}
	members := make([]Member, len(g.Members))
	copy(members, g.Members)
	m.mu.RUnlock()

	totalWeight := 0
	for _, mem := range members {
		totalWeight += memberWeight(mem.Weight)
	}
	if totalWeight == 0 {
		return "", false
	}
	n := uint64(totalWeight)
	start := atomic.AddUint64(idxPtr, 1) - 1
	for i := uint64(0); i < n; i++ {
		slot := int((start + i) % n)
		cursor := 0
		candidate := ""
		for _, mem := range members {
			cursor += memberWeight(mem.Weight)
			if slot < cursor {
				candidate = mem.AgentID
				break
			}
		}
		if candidate == "" {
			continue
		}
		if allowFn == nil || allowFn(candidate) {
			return candidate, true
		}
	}
	return "", false
}
