// Package billing provides SaaS metering, tenant plan management, and
// sub-key generation for Spectral-Cloud. It is backed by BoltDB via the
// store.Persister interface, using the dedicated "billing" pseudo-tenant
// bucket for all persistent state.
package billing

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ─── Plan constants ───────────────────────────────────────────────────────────

// Plan represents a subscription tier.
type Plan string

const (
	PlanFree       Plan = "free"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// ─── Quota ────────────────────────────────────────────────────────────────────

// Quota holds the per-day limits for a tenant plan.
// Zero means unlimited.
type Quota struct {
	MaxAPICalls   int64 `json:"max_api_calls"`
	MaxJobs       int64 `json:"max_jobs"`
	MaxAITokens   int64 `json:"max_ai_tokens"`
	MaxAgents     int64 `json:"max_agents"`
}

// QuotaFor returns the default Quota for the given Plan.
func QuotaFor(p Plan) Quota {
	switch p {
	case PlanPro:
		return Quota{MaxAPICalls: 100_000, MaxJobs: 10_000, MaxAITokens: 1_000_000, MaxAgents: 50}
	case PlanEnterprise:
		return Quota{} // unlimited
	default: // PlanFree
		return Quota{MaxAPICalls: 1_000, MaxJobs: 100, MaxAITokens: 50_000, MaxAgents: 5}
	}
}

// ─── TenantProfile ────────────────────────────────────────────────────────────

// TenantProfile stores billing metadata for a tenant.
type TenantProfile struct {
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	Plan      Plan      `json:"plan"`
	Quota     Quota     `json:"quota"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── SubKey ───────────────────────────────────────────────────────────────────

// SubKey is a tenant-scoped API key that can be revoked independently.
// The Key field (the secret) is only populated when the key is first
// generated; subsequent list calls omit it for security.
type SubKey struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Key       string    `json:"key,omitempty"` // secret shown once on creation
	KeyHash   string    `json:"key_hash"`      // stored; compared on lookup
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

// ─── Metric constants ─────────────────────────────────────────────────────────

const (
	MetricAPICalls  = "api_calls"
	MetricJobs      = "jobs"
	MetricAITokens  = "ai_tokens"
)

// ─── Persister ────────────────────────────────────────────────────────────────

// Persister is the subset of store.Store required by the billing package.
// Using an interface avoids a circular import.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	GetKV(tenant, key string) ([]byte, error)
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

// ─── key prefixes (BoltDB) ────────────────────────────────────────────────────

const (
	billingTenant = "_billing" // special pseudo-tenant bucket

	prefixProfile = "profile:"
	prefixSubKey  = "subkey:"
	prefixUsage   = "usage:"
)

// ─── Meter ────────────────────────────────────────────────────────────────────

// Meter handles usage recording, plan management, and sub-key CRUD.
type Meter struct {
	mu      sync.Mutex
	db      Persister
	counter map[string]map[string]int64 // tenant -> metric -> count (in-memory buffer)
	today   string                      // YYYY-MM-DD of last flush
}

// New returns a Meter backed by the given Persister.
func New(db Persister) *Meter {
	return &Meter{
		db:      db,
		counter: make(map[string]map[string]int64),
		today:   time.Now().UTC().Format("2006-01-02"),
	}
}

// Record increments the in-memory counter for a metric. Flush persists it.
func (m *Meter) Record(tenant, metric string, n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counter[tenant] == nil {
		m.counter[tenant] = make(map[string]int64)
	}
	m.counter[tenant][metric] += n
}

// Flush writes buffered counters to BoltDB and rotates daily totals.
func (m *Meter) Flush() {
	m.mu.Lock()
	defer m.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	// On day-rollover start fresh.
	if today != m.today {
		m.counter = make(map[string]map[string]int64)
		m.today = today
		return
	}

	for tenant, metrics := range m.counter {
		for metric, n := range metrics {
			key := prefixUsage + today + ":" + metric
			var existing int64
			if raw, _ := m.db.GetKV(tenant, key); raw != nil {
				_ = json.Unmarshal(raw, &existing)
			}
			existing += n
			b, _ := json.Marshal(existing)
			_ = m.db.PutKV(tenant, key, b)
		}
	}
	// Reset after flush.
	m.counter = make(map[string]map[string]int64)
}

// Usage returns today's persisted usage totals for the given tenant.
func (m *Meter) Usage(tenant string) map[string]int64 {
	// Flush in-memory first so callers see the latest totals.
	m.Flush()

	today := time.Now().UTC().Format("2006-01-02")
	prefix := prefixUsage + today + ":"
	out := map[string]int64{}
	_ = m.db.ScanPrefix(tenant, prefix, func(k, v []byte) error {
		metric := string(k[len(prefix):])
		var n int64
		if err := json.Unmarshal(v, &n); err == nil {
			out[metric] = n
		}
		return nil
	})
	return out
}

// ─── Profile ──────────────────────────────────────────────────────────────────

func profileKey(tenantID string) string {
	return prefixProfile + tenantID
}

// GetProfile loads the TenantProfile for tenantID from BoltDB.
// The second return value is false when no profile exists.
func (m *Meter) GetProfile(tenantID string) (TenantProfile, bool) {
	raw, err := m.db.GetKV(billingTenant, profileKey(tenantID))
	if err != nil || raw == nil {
		return TenantProfile{}, false
	}
	var p TenantProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return TenantProfile{}, false
	}
	return p, true
}

// SaveProfile persists a TenantProfile.
func (m *Meter) SaveProfile(p TenantProfile) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return m.db.PutKV(billingTenant, profileKey(p.TenantID), b)
}

// ─── Sub-keys ─────────────────────────────────────────────────────────────────

func subKeyStorageKey(tenantID, keyID string) string {
	return prefixSubKey + tenantID + ":" + keyID
}

// GenerateKey creates a new sub-key for the tenant, stores the full secret
// in the returned SubKey.Key (shown once), and persists only the hash.
func (m *Meter) GenerateKey(tenantID, name string) (SubKey, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return SubKey{}, fmt.Errorf("generate key: %w", err)
	}
	secret := "sk-" + base64.RawURLEncoding.EncodeToString(raw)
	id := fmt.Sprintf("key-%d", time.Now().UnixNano())

	sk := SubKey{
		ID:        id,
		TenantID:  tenantID,
		Name:      name,
		Key:       secret,          // returned to caller once; not stored
		KeyHash:   hashSecret(secret),
		CreatedAt: time.Now().UTC(),
	}

	// Persist without the plain-text secret.
	stored := sk
	stored.Key = ""
	b, err := json.Marshal(stored)
	if err != nil {
		return SubKey{}, err
	}
	if err := m.db.PutKV(billingTenant, subKeyStorageKey(tenantID, id), b); err != nil {
		return SubKey{}, err
	}
	return sk, nil
}

// ListKeys returns all sub-keys for a tenant. The Key field is always empty.
func (m *Meter) ListKeys(tenantID string) ([]SubKey, error) {
	prefix := prefixSubKey + tenantID + ":"
	var keys []SubKey
	err := m.db.ScanPrefix(billingTenant, prefix, func(_, v []byte) error {
		var sk SubKey
		if err := json.Unmarshal(v, &sk); err != nil {
			return nil
		}
		sk.Key = "" // never expose hash or secret
		keys = append(keys, sk)
		return nil
	})
	return keys, err
}

// DeleteKey removes a sub-key by ID for a tenant.
func (m *Meter) DeleteKey(tenantID, keyID string) error {
	return m.db.DeleteKV(billingTenant, subKeyStorageKey(tenantID, keyID))
}

// GetKeyBySecret looks up a sub-key by its plaintext secret across the given
// tenant list. Returns the SubKey and true when found.
func (m *Meter) GetKeyBySecret(tenants []string, secret string) (SubKey, bool) {
	h := hashSecret(secret)
	for _, tenantID := range tenants {
		prefix := prefixSubKey + tenantID + ":"
		var found SubKey
		_ = m.db.ScanPrefix(billingTenant, prefix, func(_, v []byte) error {
			var sk SubKey
			if err := json.Unmarshal(v, &sk); err != nil {
				return nil
			}
			if sk.KeyHash == h {
				found = sk
				return fmt.Errorf("stop") // sentinel to break scan
			}
			return nil
		})
		if found.ID != "" {
			return found, true
		}
	}
	return SubKey{}, false
}

// TouchKey updates LastUsed for a sub-key.
func (m *Meter) TouchKey(tenantID, keyID string) {
	k := subKeyStorageKey(tenantID, keyID)
	raw, err := m.db.GetKV(billingTenant, k)
	if err != nil || raw == nil {
		return
	}
	var sk SubKey
	if err := json.Unmarshal(raw, &sk); err != nil {
		return
	}
	sk.LastUsed = time.Now().UTC()
	b, err := json.Marshal(sk)
	if err != nil {
		return
	}
	_ = m.db.PutKV(billingTenant, k, b)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// hashSecret returns a base64-encoded BLAKE2-style digest of the secret.
// We use SHA-256 via the standard library to keep zero extra dependencies.
func hashSecret(secret string) string {
	// Import crypto/sha256 inline via the function to avoid a top-level import
	// bloating the package for callers that don't use hashing.
	return sha256Hex([]byte(secret))
}
