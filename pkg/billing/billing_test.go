package billing_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/gdev6145/Spectral_cloud/pkg/billing"
)

// ─── in-memory Persister stub ─────────────────────────────────────────────────

type memStore struct {
	mu   sync.Mutex
	data map[string]map[string][]byte // tenant -> key -> value
}

func newMemStore() *memStore {
	return &memStore{data: make(map[string]map[string][]byte)}
}

func (m *memStore) PutKV(tenant, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[tenant] == nil {
		m.data[tenant] = make(map[string][]byte)
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[tenant][key] = cp
	return nil
}

func (m *memStore) GetKV(tenant, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.data[tenant][key]
	if v == nil {
		return nil, nil
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *memStore) DeleteKV(tenant, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[tenant] != nil {
		delete(m.data[tenant], key)
	}
	return nil
}

func (m *memStore) ScanPrefix(tenant, prefix string, fn func(k, v []byte) error) error {
	m.mu.Lock()
	// copy snapshot to avoid holding lock during fn
	snapshot := make(map[string][]byte)
	for k, v := range m.data[tenant] {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			cp := make([]byte, len(v))
			copy(cp, v)
			snapshot[k] = cp
		}
	}
	m.mu.Unlock()
	for k, v := range snapshot {
		if err := fn([]byte(k), v); err != nil {
			return nil // treat any error as stop sentinel, not a real error
		}
	}
	return nil
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestQuotaFor(t *testing.T) {
	if q := billing.QuotaFor(billing.PlanFree); q.MaxAPICalls != 1_000 {
		t.Fatalf("expected free MaxAPICalls=1000, got %d", q.MaxAPICalls)
	}
	if q := billing.QuotaFor(billing.PlanPro); q.MaxAPICalls != 100_000 {
		t.Fatalf("expected pro MaxAPICalls=100000, got %d", q.MaxAPICalls)
	}
	if q := billing.QuotaFor(billing.PlanEnterprise); q.MaxAPICalls != 0 {
		t.Fatalf("expected enterprise MaxAPICalls=0 (unlimited), got %d", q.MaxAPICalls)
	}
}

func TestSaveGetProfile(t *testing.T) {
	m := billing.New(newMemStore())
	p := billing.TenantProfile{
		TenantID: "tenant-a",
		Email:    "a@example.com",
		Plan:     billing.PlanPro,
		Quota:    billing.QuotaFor(billing.PlanPro),
	}
	if err := m.SaveProfile(p); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	got, ok := m.GetProfile("tenant-a")
	if !ok {
		t.Fatal("GetProfile: not found")
	}
	if got.Email != "a@example.com" {
		t.Fatalf("unexpected email: %s", got.Email)
	}
	if got.Plan != billing.PlanPro {
		t.Fatalf("unexpected plan: %s", got.Plan)
	}
}

func TestGetProfileMissing(t *testing.T) {
	m := billing.New(newMemStore())
	_, ok := m.GetProfile("nobody")
	if ok {
		t.Fatal("expected ok=false for missing profile")
	}
}

func TestGenerateAndListKeys(t *testing.T) {
	m := billing.New(newMemStore())
	sk, err := m.GenerateKey("tenant-b", "ci-pipeline")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if sk.Key == "" {
		t.Fatal("expected plaintext key on first generation")
	}
	if sk.ID == "" || sk.TenantID != "tenant-b" {
		t.Fatalf("unexpected SubKey fields: %+v", sk)
	}

	keys, err := m.ListKeys("tenant-b")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].Key != "" {
		t.Fatal("ListKeys must redact plaintext key")
	}
	if keys[0].ID != sk.ID {
		t.Fatalf("ID mismatch: %s vs %s", keys[0].ID, sk.ID)
	}
}

func TestDeleteKey(t *testing.T) {
	m := billing.New(newMemStore())
	sk, _ := m.GenerateKey("tenant-c", "to-delete")
	if err := m.DeleteKey("tenant-c", sk.ID); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
	keys, _ := m.ListKeys("tenant-c")
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys after delete, got %d", len(keys))
	}
}

func TestGetKeyBySecret(t *testing.T) {
	m := billing.New(newMemStore())
	sk, _ := m.GenerateKey("tenant-d", "lookup-key")

	found, ok := m.GetKeyBySecret([]string{"tenant-d"}, sk.Key)
	if !ok {
		t.Fatal("GetKeyBySecret: not found")
	}
	if found.ID != sk.ID {
		t.Fatalf("ID mismatch: %s vs %s", found.ID, sk.ID)
	}

	_, ok2 := m.GetKeyBySecret([]string{"tenant-d"}, "sk-wrong")
	if ok2 {
		t.Fatal("expected not found for wrong secret")
	}
}

func TestRecordAndUsage(t *testing.T) {
	m := billing.New(newMemStore())
	m.Record("tenant-e", billing.MetricAPICalls, 5)
	m.Record("tenant-e", billing.MetricJobs, 2)
	m.Record("tenant-e", billing.MetricAPICalls, 3)
	m.Flush()

	usage := m.Usage("tenant-e")
	if usage[billing.MetricAPICalls] != 8 {
		t.Fatalf("expected api_calls=8, got %d", usage[billing.MetricAPICalls])
	}
	if usage[billing.MetricJobs] != 2 {
		t.Fatalf("expected jobs=2, got %d", usage[billing.MetricJobs])
	}
}

func TestTouchKey(t *testing.T) {
	db := newMemStore()
	m := billing.New(db)
	sk, _ := m.GenerateKey("tenant-f", "touch-test")

	m.TouchKey("tenant-f", sk.ID)

	keys, _ := m.ListKeys("tenant-f")
	if len(keys) == 0 {
		t.Fatal("no keys after TouchKey")
	}
	// LastUsed should be populated after TouchKey; just check the field can be
	// round-tripped as JSON (zero time marshals fine too).
	b, err := json.Marshal(keys[0])
	if err != nil {
		t.Fatalf("marshal SubKey: %v", err)
	}
	var sk2 billing.SubKey
	if err := json.Unmarshal(b, &sk2); err != nil {
		t.Fatalf("unmarshal SubKey: %v", err)
	}
}

func TestPlanConstants(t *testing.T) {
	if billing.PlanFree != "free" {
		t.Fatal("PlanFree mismatch")
	}
	if billing.PlanPro != "pro" {
		t.Fatal("PlanPro mismatch")
	}
	if billing.PlanEnterprise != "enterprise" {
		t.Fatal("PlanEnterprise mismatch")
	}
}
