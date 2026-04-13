// Package billing provides SaaS metering, tenant profile management, and
// sub-key (API key) lifecycle for Spectral Cloud.
package billing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

// Plan names.
type Plan string

const (
	PlanFree       Plan = "free"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// Metric names used with Meter.Record.
const (
	MetricAPICalls = "api_calls"
	MetricJobs     = "jobs"
	MetricAITokens = "ai_tokens"
)

// Quota holds per-plan daily usage limits (0 = unlimited).
type Quota struct {
	APICalls  int64 `json:"api_calls_per_day"`
	Jobs      int64 `json:"jobs_per_day"`
	AITokens  int64 `json:"ai_tokens_per_day"`
	Agents    int   `json:"agents_max"`
	StorageGB int   `json:"storage_gb"`
}

// QuotaFor returns the default Quota for a given Plan.
func QuotaFor(p Plan) Quota {
	switch p {
	case PlanPro:
		return Quota{APICalls: 50_000, Jobs: 5_000, AITokens: 500_000, Agents: 50, StorageGB: 100}
	case PlanEnterprise:
		return Quota{APICalls: 0, Jobs: 0, AITokens: 0, Agents: 0, StorageGB: 0} // unlimited
	default: // free
		return Quota{APICalls: 1_000, Jobs: 100, AITokens: 10_000, Agents: 5, StorageGB: 5}
	}
}

// TenantProfile holds registration and plan information for a tenant.
type TenantProfile struct {
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	Plan      Plan      `json:"plan"`
	Quota     Quota     `json:"quota"`
	CreatedAt time.Time `json:"created_at"`
}

// SubKey is a named API key belonging to a tenant.
type SubKey struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Key       string    `json:"key,omitempty"` // only shown at creation
	KeyHash   string    `json:"-"`             // stored, not exposed
	LastUsed  time.Time `json:"last_used,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Meter tracks per-tenant daily usage and manages profiles/sub-keys.
// It is backed by a BoltDB store for persistence.
type Meter struct {
	mu       sync.Mutex
	db       *store.Store
	usage    map[string]map[string]int64 // tenantID → metric → count (today)
	usageDay string                      // "YYYY-MM-DD" — reset when day changes
	profiles map[string]TenantProfile
	keys     map[string]map[string]*SubKey // tenantID → keyID → SubKey
}

// New creates a Meter backed by the given store.
func New(db *store.Store) *Meter {
	m := &Meter{
		db:       db,
		usage:    make(map[string]map[string]int64),
		usageDay: time.Now().UTC().Format("2006-01-02"),
		profiles: make(map[string]TenantProfile),
		keys:     make(map[string]map[string]*SubKey),
	}
	return m
}

func (m *Meter) today() string {
	return time.Now().UTC().Format("2006-01-02")
}

func (m *Meter) maybeResetDay() {
	today := m.today()
	if m.usageDay != today {
		m.usage = make(map[string]map[string]int64)
		m.usageDay = today
	}
}

// Record increments a usage counter for tenant by n.
func (m *Meter) Record(tenant, metric string, n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maybeResetDay()
	if m.usage[tenant] == nil {
		m.usage[tenant] = make(map[string]int64)
	}
	m.usage[tenant][metric] += n
}

// Usage returns a copy of today's usage counters for tenant.
func (m *Meter) Usage(tenant string) map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maybeResetDay()
	src := m.usage[tenant]
	out := make(map[string]int64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Flush is a no-op placeholder (usage data is in-memory only today).
func (m *Meter) Flush() {}

// GetProfile returns the TenantProfile for tenantID.
func (m *Meter) GetProfile(tenantID string) (TenantProfile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[tenantID]
	return p, ok
}

// SaveProfile stores or updates a TenantProfile.
func (m *Meter) SaveProfile(p TenantProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[p.TenantID] = p
	return nil
}

// GenerateKey creates a new sub-key for the tenant and returns it.
// The raw secret (Key field) is only available from the returned value.
func (m *Meter) GenerateKey(tenantID, name string) (*SubKey, error) {
	raw, err := randomHex(32)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	secret := "sk-" + raw
	id := "key-" + randomShort()
	sk := &SubKey{
		ID:        id,
		TenantID:  tenantID,
		Name:      name,
		Key:       secret,
		KeyHash:   secret, // store plain for now (MVP)
		CreatedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	if m.keys[tenantID] == nil {
		m.keys[tenantID] = make(map[string]*SubKey)
	}
	m.keys[tenantID][id] = sk
	m.mu.Unlock()
	return sk, nil
}

// ListKeys returns all sub-keys for a tenant (Key field is redacted).
func (m *Meter) ListKeys(tenantID string) ([]SubKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ks := m.keys[tenantID]
	out := make([]SubKey, 0, len(ks))
	for _, sk := range ks {
		masked := *sk
		masked.Key = "" // redact
		out = append(out, masked)
	}
	return out, nil
}

// DeleteKey removes a sub-key by ID.
func (m *Meter) DeleteKey(tenantID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys[tenantID] == nil {
		return fmt.Errorf("key not found")
	}
	if _, ok := m.keys[tenantID][keyID]; !ok {
		return fmt.Errorf("key not found")
	}
	delete(m.keys[tenantID], keyID)
	return nil
}

// GetKeyBySecret finds a SubKey across multiple tenants by its raw secret.
func (m *Meter) GetKeyBySecret(tenantNames []string, secret string) (SubKey, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tn := range tenantNames {
		for _, sk := range m.keys[tn] {
			if sk.KeyHash == secret {
				return *sk, true
			}
		}
	}
	return SubKey{}, false
}

// TouchKey updates the LastUsed timestamp for a key.
func (m *Meter) TouchKey(tenantID, keyID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys[tenantID] != nil {
		if sk, ok := m.keys[tenantID][keyID]; ok {
			sk.LastUsed = time.Now().UTC()
		}
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomShort() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
