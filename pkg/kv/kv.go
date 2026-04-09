// Package kv provides a thread-safe in-memory key-value store with optional
// per-entry TTL, namespaced by tenant.
package kv

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is a single KV record.
type Entry struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	Tenant    string     `json:"tenant"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Store is a multi-tenant in-memory KV store.
type Store struct {
	mu      sync.RWMutex
	entries map[string]map[string]*Entry // tenant → key → entry
}

func New() *Store {
	return &Store{entries: make(map[string]map[string]*Entry)}
}

// Set creates or overwrites key for tenant. ttl=0 means no expiry.
func (s *Store) Set(tenant, key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries[tenant] == nil {
		s.entries[tenant] = make(map[string]*Entry)
	}
	var exp *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		exp = &t
	}
	s.entries[tenant][key] = &Entry{
		Key:       key,
		Value:     value,
		Tenant:    tenant,
		ExpiresAt: exp,
		UpdatedAt: time.Now().UTC(),
	}
}

// Get returns the entry for tenant+key, or (_, false) if missing/expired.
func (s *Store) Get(tenant, key string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.entries[tenant]
	if m == nil {
		return Entry{}, false
	}
	e, ok := m[key]
	if !ok {
		return Entry{}, false
	}
	if e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
		return Entry{}, false
	}
	return *e, true
}

// Delete removes key from tenant. Returns false if not found.
func (s *Store) Delete(tenant, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.entries[tenant]
	if _, ok := m[key]; !ok {
		return false
	}
	delete(m, key)
	return true
}

// List returns all live entries for tenant, filtered by prefix. Sorted by key.
func (s *Store) List(tenant, prefix string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.entries[tenant]
	now := time.Now()
	var out []Entry
	for k, e := range m {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		if e.ExpiresAt != nil && now.After(*e.ExpiresAt) {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Count returns the total number of live entries across all tenants.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, m := range s.entries {
		for _, e := range m {
			if e.ExpiresAt == nil || now.Before(*e.ExpiresAt) {
				n++
			}
		}
	}
	return n
}

// Prune removes expired entries and returns count deleted.
func (s *Store) Prune() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := 0
	for tenant, m := range s.entries {
		for k, e := range m {
			if e.ExpiresAt != nil && now.After(*e.ExpiresAt) {
				delete(m, k)
				n++
			}
		}
		if len(m) == 0 {
			delete(s.entries, tenant)
		}
	}
	return n
}
