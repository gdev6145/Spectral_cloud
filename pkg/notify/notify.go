// Package notify manages persistent notification rules. Each rule watches
// for specific event types from the event broker and fires a webhook URL
// when they arrive.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

const ruleKeyPrefix = "notify_"

// Persister is the subset of store.Store methods that the notification manager needs.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

// Rule describes a single notification subscription.
type Rule struct {
	ID         string    `json:"id"`
	Tenant     string    `json:"tenant"`
	Name       string    `json:"name"`
	EventTypes []string  `json:"event_types"` // empty = all event types for this tenant
	WebhookURL string    `json:"webhook_url"`
	Secret     string    `json:"secret,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Active     bool      `json:"active"`
	FiredTotal uint64    `json:"fired_total"`
}

// Manager holds notification rules in memory and can persist them via a store.
type Manager struct {
	mu      sync.RWMutex
	rules   map[string]*Rule
	counter uint64
	client  *http.Client
	store   Persister
}

func New() *Manager {
	return &Manager{
		rules:  make(map[string]*Rule),
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// NewWithStore creates a manager that persists rules via p.
func NewWithStore(p Persister) *Manager {
	return &Manager{
		rules:  make(map[string]*Rule),
		client: &http.Client{Timeout: 5 * time.Second},
		store:  p,
	}
}

// UpdateParams describes a partial notification rule update.
type UpdateParams struct {
	Name       *string
	WebhookURL *string
	Secret     *string
	EventTypes *[]string
	Active     *bool
}

func canonicalEventType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.ReplaceAll(value, ".", "_")
}

// Add registers a new rule for a tenant and returns it.
func (m *Manager) Add(tenant, name, webhookURL, secret string, eventTypes []string) *Rule {
	id := fmt.Sprintf("rule-%d", atomic.AddUint64(&m.counter, 1))
	r := &Rule{
		ID:         id,
		Tenant:     tenant,
		Name:       name,
		WebhookURL: webhookURL,
		Secret:     secret,
		EventTypes: eventTypes,
		CreatedAt:  time.Now().UTC(),
		Active:     true,
	}
	m.mu.Lock()
	m.rules[id] = r
	m.persist(r)
	m.mu.Unlock()
	return r
}

// Get returns a snapshot of a rule by tenant and ID.
func (m *Manager) Get(tenant, id string) (Rule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rules[id]
	if !ok || r.Tenant != tenant {
		return Rule{}, false
	}
	return *r, true
}

// Update mutates a rule in place for the given tenant.
func (m *Manager) Update(tenant, id string, params UpdateParams) (Rule, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.rules[id]
	if !ok || r.Tenant != tenant {
		return Rule{}, false, nil
	}
	if params.Name != nil {
		if *params.Name == "" {
			return Rule{}, true, fmt.Errorf("name is required")
		}
		r.Name = *params.Name
	}
	if params.WebhookURL != nil {
		if *params.WebhookURL == "" {
			return Rule{}, true, fmt.Errorf("webhook_url is required")
		}
		r.WebhookURL = *params.WebhookURL
	}
	if params.Secret != nil {
		r.Secret = *params.Secret
	}
	if params.EventTypes != nil {
		r.EventTypes = *params.EventTypes
	}
	if params.Active != nil {
		r.Active = *params.Active
	}
	m.persist(r)
	return *r, true, nil
}

// Delete removes a rule by ID for the given tenant. Returns false if not found or wrong tenant.
func (m *Manager) Delete(tenant, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[id]
	if !ok || r.Tenant != tenant {
		return false
	}
	m.unpersist(r)
	delete(m.rules, id)
	return true
}

// LoadFromStore reads persisted rules for a tenant and restores them.
func (m *Manager) LoadFromStore(tenant string) (int, error) {
	if m.store == nil {
		return 0, nil
	}
	var loaded []Rule
	if err := m.store.ScanPrefix(tenant, ruleKeyPrefix, func(_, val []byte) error {
		var r Rule
		if err := json.Unmarshal(val, &r); err != nil {
			log.Printf("warn: skipping corrupted persisted notification rule for tenant %q: %v", tenant, err)
			return nil
		}
		loaded = append(loaded, r)
		return nil
	}); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range loaded {
		r := loaded[i]
		if n := parseRuleNum(r.ID); n > m.counter {
			m.counter = n
		}
		rCopy := r
		m.rules[r.ID] = &rCopy
	}
	return len(loaded), nil
}

func parseRuleNum(id string) uint64 {
	if !strings.HasPrefix(id, "rule-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[5:], "%d", &n)
	return n
}

func (m *Manager) persist(r *Rule) {
	if m.store == nil {
		return
	}
	data, err := json.Marshal(r)
	if err != nil {
		log.Printf("warn: failed to marshal notification rule %q for tenant %q: %v", r.ID, r.Tenant, err)
		return
	}
	if err := m.store.PutKV(r.Tenant, ruleKeyPrefix+r.ID, data); err != nil {
		log.Printf("warn: failed to persist notification rule %q for tenant %q: %v", r.ID, r.Tenant, err)
	}
}

func (m *Manager) unpersist(r *Rule) {
	if m.store == nil {
		return
	}
	if err := m.store.DeleteKV(r.Tenant, ruleKeyPrefix+r.ID); err != nil {
		log.Printf("warn: failed to delete persisted notification rule %q for tenant %q: %v", r.ID, r.Tenant, err)
	}
}

// List returns a snapshot of rules for the given tenant, sorted by created time.
// Pass "" to list all rules across tenants.
func (m *Manager) List(tenant string) []Rule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Rule, 0, len(m.rules))
	for _, r := range m.rules {
		if tenant != "" && r.Tenant != tenant {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// Start subscribes to the broker and dispatches webhooks in the background.
func (m *Manager) Start(ctx context.Context, broker *events.Broker) {
	if broker == nil {
		return
	}
	subID := "notify-manager"
	ch := broker.Subscribe(subID, 256)
	go func() {
		defer broker.Unsubscribe(subID)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, open := <-ch:
				if !open {
					return
				}
				m.dispatch(ev)
			}
		}
	}()
}

func (m *Manager) dispatch(ev events.Event) {
	m.mu.RLock()
	rules := make([]*Rule, 0, len(m.rules))
	for _, r := range m.rules {
		if r.Active {
			rules = append(rules, r)
		}
	}
	m.mu.RUnlock()

	for _, r := range rules {
		if !m.matches(r, ev) {
			continue
		}
		go m.fire(r, ev)
	}
}

func (m *Manager) matches(r *Rule, ev events.Event) bool {
	// Tenant must match (empty rule tenant = global/admin rule that fires for all).
	if r.Tenant != "" && r.Tenant != ev.TenantID {
		return false
	}
	if len(r.EventTypes) == 0 {
		return true
	}
	evType := canonicalEventType(string(ev.Type))
	for _, t := range r.EventTypes {
		if canonicalEventType(t) == evType {
			return true
		}
	}
	return false
}

func (m *Manager) fire(r *Rule, ev events.Event) {
	body, err := json.Marshal(map[string]any{
		"rule_id":    r.ID,
		"rule_name":  r.Name,
		"event_type": ev.Type,
		"tenant_id":  ev.TenantID,
		"timestamp":  ev.Timestamp,
		"data":       ev.Data,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, r.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spectral-Rule", r.ID)
	if r.Secret != "" {
		mac := hmac.New(sha256.New, []byte(r.Secret))
		mac.Write(body)
		req.Header.Set("X-Spectral-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := m.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}

	m.mu.Lock()
	if live, ok := m.rules[r.ID]; ok {
		atomic.AddUint64(&live.FiredTotal, 1)
		m.persist(live)
	}
	m.mu.Unlock()
}
