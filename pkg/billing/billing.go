package billing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

const (
	profileKey = "billing_profile"
	keyPrefix  = "billing_key_"
)

type Plan string

const (
	PlanFree       Plan = "free"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

type Quota struct {
	Agents    int   `json:"agents"`
	JobsDay   int64 `json:"jobs_day"`
	APIDay    int64 `json:"api_day"`
	TokensDay int64 `json:"tokens_day"`
}

type Metric string

const (
	MetricAPICalls Metric = "api_calls"
	MetricAITokens Metric = "ai_tokens"
	MetricJobs     Metric = "jobs"
)

type TenantProfile struct {
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	Plan      Plan      `json:"plan"`
	Quota     Quota     `json:"quota"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type SubKey struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Key        string    `json:"key,omitempty"`
	Prefix     string    `json:"prefix"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

type Meter struct {
	db *store.Store

	mu    sync.Mutex
	usage map[string]map[string]int64 // tenant -> metric -> count (current UTC day)
	day   string
}

func New(db *store.Store) *Meter {
	return &Meter{
		db:    db,
		usage: make(map[string]map[string]int64),
		day:   time.Now().UTC().Format("2006-01-02"),
	}
}

func QuotaFor(p Plan) Quota {
	switch p {
	case PlanPro:
		return Quota{Agents: 50, JobsDay: 200_000, APIDay: 1_000_000, TokensDay: 100_000_000}
	case PlanEnterprise:
		return Quota{Agents: 250, JobsDay: 2_000_000, APIDay: 10_000_000, TokensDay: 2_000_000_000}
	default:
		return Quota{Agents: 10, JobsDay: 20_000, APIDay: 100_000, TokensDay: 5_000_000}
	}
}

func (m *Meter) rotateDayIfNeededLocked() {
	today := time.Now().UTC().Format("2006-01-02")
	if today == m.day {
		return
	}
	m.day = today
	m.usage = make(map[string]map[string]int64)
}

func (m *Meter) Record(tenant string, metric Metric, delta int64) {
	if m == nil || strings.TrimSpace(tenant) == "" || delta == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rotateDayIfNeededLocked()
	metrics := m.usage[tenant]
	if metrics == nil {
		metrics = make(map[string]int64)
		m.usage[tenant] = metrics
	}
	metrics[string(metric)] += delta
}

func (m *Meter) Usage(tenant string) map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rotateDayIfNeededLocked()
	out := map[string]int64{
		string(MetricAPICalls): 0,
		string(MetricAITokens): 0,
		string(MetricJobs):     0,
	}
	if src, ok := m.usage[tenant]; ok {
		for k, v := range src {
			out[k] = v
		}
	}
	return out
}

func (m *Meter) SaveProfile(p TenantProfile) error {
	if m == nil || m.db == nil {
		return errors.New("billing meter not initialized")
	}
	p.TenantID = strings.TrimSpace(p.TenantID)
	if p.TenantID == "" {
		return errors.New("tenant id is required")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return m.db.PutKV(p.TenantID, profileKey, raw)
}

func (m *Meter) GetProfile(tenant string) (TenantProfile, bool) {
	var p TenantProfile
	if m == nil || m.db == nil {
		return p, false
	}
	raw, err := m.db.GetKV(tenant, profileKey)
	if err != nil || len(raw) == 0 {
		return p, false
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return TenantProfile{}, false
	}
	return p, true
}

func (m *Meter) GenerateKey(tenant, name string) (SubKey, error) {
	var sk SubKey
	if m == nil || m.db == nil {
		return sk, errors.New("billing meter not initialized")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return sk, errors.New("tenant id is required")
	}
	if strings.TrimSpace(name) == "" {
		name = "default"
	}
	id, err := newID(8)
	if err != nil {
		return sk, err
	}
	secretRaw, err := newID(24)
	if err != nil {
		return sk, err
	}
	secret := "sk-" + secretRaw
	now := time.Now().UTC()
	sk = SubKey{
		ID:        id,
		TenantID:  tenant,
		Name:      name,
		Key:       secret,
		Prefix:    prefixFor(secret),
		CreatedAt: now,
	}
	raw, err := json.Marshal(sk)
	if err != nil {
		return SubKey{}, err
	}
	if err := m.db.PutKV(tenant, keyPrefix+id, raw); err != nil {
		return SubKey{}, err
	}
	return sk, nil
}

func (m *Meter) ListKeys(tenant string) ([]SubKey, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("billing meter not initialized")
	}
	keys := make([]SubKey, 0)
	err := m.db.ScanPrefix(tenant, keyPrefix, func(_, val []byte) error {
		var sk SubKey
		if err := json.Unmarshal(val, &sk); err != nil {
			return nil
		}
		// Redact secret in list responses.
		sk.Key = ""
		keys = append(keys, sk)
		return nil
	})
	return keys, err
}

func (m *Meter) DeleteKey(tenant, keyID string) error {
	if m == nil || m.db == nil {
		return errors.New("billing meter not initialized")
	}
	if strings.TrimSpace(keyID) == "" {
		return errors.New("key id is required")
	}
	return m.db.DeleteKV(tenant, keyPrefix+keyID)
}

func (m *Meter) GetKeyBySecret(tenants []string, secret string) (SubKey, bool) {
	if m == nil || m.db == nil || strings.TrimSpace(secret) == "" {
		return SubKey{}, false
	}
	for _, tenant := range tenants {
		var found SubKey
		matched := false
		_ = m.db.ScanPrefix(tenant, keyPrefix, func(_, val []byte) error {
			var sk SubKey
			if err := json.Unmarshal(val, &sk); err != nil {
				return nil
			}
			if subtle.ConstantTimeCompare([]byte(sk.Key), []byte(secret)) == 1 {
				found = sk
				matched = true
				return errors.New("found")
			}
			return nil
		})
		if matched {
			return found, true
		}
	}
	return SubKey{}, false
}

func (m *Meter) TouchKey(tenant, keyID string) error {
	if m == nil || m.db == nil {
		return errors.New("billing meter not initialized")
	}
	raw, err := m.db.GetKV(tenant, keyPrefix+keyID)
	if err != nil || len(raw) == 0 {
		return err
	}
	var sk SubKey
	if err := json.Unmarshal(raw, &sk); err != nil {
		return err
	}
	sk.LastUsedAt = time.Now().UTC()
	out, err := json.Marshal(sk)
	if err != nil {
		return err
	}
	return m.db.PutKV(tenant, keyPrefix+keyID, out)
}

func (m *Meter) Flush() {}

func prefixFor(secret string) string {
	if len(secret) <= 10 {
		return secret
	}
	return secret[:10]
}

func newID(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
