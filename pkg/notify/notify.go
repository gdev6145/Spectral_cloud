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
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

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

// Manager holds in-memory notification rules and dispatches webhooks.
type Manager struct {
	mu      sync.RWMutex
	rules   map[string]*Rule
	counter uint64
	client  *http.Client
}

func New() *Manager {
	return &Manager{
		rules:  make(map[string]*Rule),
		client: &http.Client{Timeout: 5 * time.Second},
	}
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
	m.mu.Unlock()
	return r
}

// Delete removes a rule by ID for the given tenant. Returns false if not found or wrong tenant.
func (m *Manager) Delete(tenant, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[id]
	if !ok || r.Tenant != tenant {
		return false
	}
	delete(m.rules, id)
	return true
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
	evType := string(ev.Type)
	for _, t := range r.EventTypes {
		if t == evType {
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
	}
	m.mu.Unlock()
}
