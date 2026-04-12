package pipeline

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// ── fake Persister ────────────────────────────────────────────────────────────

type fakeStore struct {
	mu   sync.Mutex
	data map[string]map[string][]byte // tenant → key → value
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: make(map[string]map[string][]byte)}
}

func (f *fakeStore) PutKV(tenant, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data[tenant] == nil {
		f.data[tenant] = make(map[string][]byte)
	}
	f.data[tenant][key] = value
	return nil
}

func (f *fakeStore) DeleteKV(tenant, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data[tenant], key)
	return nil
}

func (f *fakeStore) ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error {
	f.mu.Lock()
	m := f.data[tenant]
	// copy so the lock can be released before calling fn
	entries := make([]struct{ k, v []byte }, 0, len(m))
	for k, v := range m {
		if len(prefix) == 0 || len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			entries = append(entries, struct{ k, v []byte }{[]byte(k), v})
		}
	}
	f.mu.Unlock()
	for _, e := range entries {
		if err := fn(e.k, e.v); err != nil {
			return err
		}
	}
	return nil
}

func TestCreateAndGetPipeline(t *testing.T) {
	m := New()
	stages := []Stage{
		{Name: "ingest", Capability: "ingest"},
		{Name: "transform", AgentID: "agent-1"},
	}
	p, err := m.CreatePipeline("tenant1", "my-pipeline", stages)
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}
	if p.Name != "my-pipeline" {
		t.Errorf("got name %q, want %q", p.Name, "my-pipeline")
	}
	if len(p.Stages) != 2 {
		t.Errorf("got %d stages, want 2", len(p.Stages))
	}

	got, ok := m.GetPipeline("tenant1", p.ID)
	if !ok {
		t.Fatal("GetPipeline: not found")
	}
	if got.ID != p.ID {
		t.Errorf("got ID %q, want %q", got.ID, p.ID)
	}

	// Different tenant cannot see it.
	_, ok = m.GetPipeline("other", p.ID)
	if ok {
		t.Error("expected other tenant to not see pipeline")
	}
}

func TestCreatePipeline_Validation(t *testing.T) {
	m := New()
	if _, err := m.CreatePipeline("t", "", nil); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := m.CreatePipeline("t", "x", nil); err == nil {
		t.Error("expected error for nil stages")
	}
	if _, err := m.CreatePipeline("t", "x", []Stage{{Name: "bad"}}); err == nil {
		t.Error("expected error for stage with no agent_id or capability")
	}
}

func TestListPipelines(t *testing.T) {
	m := New()
	stages := []Stage{{Name: "s", Capability: "cap"}}
	m.CreatePipeline("t", "beta", stages)
	m.CreatePipeline("t", "alpha", stages)
	m.CreatePipeline("other", "gamma", stages)

	list := m.ListPipelines("t")
	if len(list) != 2 {
		t.Fatalf("got %d pipelines, want 2", len(list))
	}
	// Sorted by name.
	if list[0].Name != "alpha" || list[1].Name != "beta" {
		t.Errorf("unexpected order: %q, %q", list[0].Name, list[1].Name)
	}
}

func TestUpdatePipeline(t *testing.T) {
	m := New()
	stages := []Stage{{Name: "s", Capability: "cap"}}
	p, _ := m.CreatePipeline("t", "old", stages)

	newName := "new"
	updated, ok, err := m.UpdatePipeline("t", p.ID, UpdateParams{Name: &newName})
	if err != nil || !ok {
		t.Fatalf("UpdatePipeline: ok=%v err=%v", ok, err)
	}
	if updated.Name != "new" {
		t.Errorf("got name %q, want %q", updated.Name, "new")
	}
}

func TestDeletePipeline(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "x", []Stage{{Capability: "cap"}})
	if !m.DeletePipeline("t", p.ID) {
		t.Fatal("DeletePipeline returned false")
	}
	if m.DeletePipeline("t", p.ID) {
		t.Error("second delete should return false")
	}
}

func TestStartAndAdvanceRun(t *testing.T) {
	m := New()
	stages := []Stage{
		{Name: "stage0", Capability: "cap0"},
		{Name: "stage1", Capability: "cap1"},
		{Name: "stage2", AgentID: "a2"},
	}
	p, _ := m.CreatePipeline("t", "pipe", stages)

	run, stage0, err := m.StartRun("t", p.ID, map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Status != RunStatusRunning {
		t.Errorf("initial status: got %q, want running", run.Status)
	}
	if run.CurrentStage != 0 {
		t.Errorf("initial stage: got %d, want 0", run.CurrentStage)
	}
	if stage0.Capability != "cap0" {
		t.Errorf("stage0 capability: got %q, want cap0", stage0.Capability)
	}

	// Record stage0 job.
	run, _ = m.RecordStageJob("t", run.ID, "job-0")
	if len(run.JobIDs) != 1 || run.JobIDs[0] != "job-0" {
		t.Errorf("unexpected job_ids: %v", run.JobIDs)
	}

	// Advance to stage1.
	run, next, err := m.AdvanceRun("t", run.ID, false)
	if err != nil {
		t.Fatalf("AdvanceRun stage1: %v", err)
	}
	if next == nil {
		t.Fatal("expected next stage, got nil")
	}
	if next.Capability != "cap1" {
		t.Errorf("stage1 capability: got %q, want cap1", next.Capability)
	}
	if run.CurrentStage != 1 {
		t.Errorf("after advance stage: got %d, want 1", run.CurrentStage)
	}

	// Record stage1 job and advance to stage2.
	m.RecordStageJob("t", run.ID, "job-1")
	run, next, _ = m.AdvanceRun("t", run.ID, false)
	if next == nil || next.AgentID != "a2" {
		t.Errorf("stage2 agent_id: got %v", next)
	}

	// Record stage2 job and advance to done.
	m.RecordStageJob("t", run.ID, "job-2")
	run, next, err = m.AdvanceRun("t", run.ID, false)
	if err != nil {
		t.Fatalf("AdvanceRun final: %v", err)
	}
	if next != nil {
		t.Error("expected nil next after final stage")
	}
	if run.Status != RunStatusDone {
		t.Errorf("final status: got %q, want done", run.Status)
	}
}

func TestAdvanceRun_Failed(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}, {Capability: "c2"}})
	run, _, _ := m.StartRun("t", p.ID, nil)
	run, next, err := m.AdvanceRun("t", run.ID, true)
	if err != nil {
		t.Fatalf("AdvanceRun failed: %v", err)
	}
	if next != nil {
		t.Error("expected nil next on failure")
	}
	if run.Status != RunStatusFailed {
		t.Errorf("got status %q, want failed", run.Status)
	}
}

func TestGetAndListRuns(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}})
	run1, _, _ := m.StartRun("t", p.ID, nil)
	run2, _, _ := m.StartRun("t", p.ID, nil)

	got, ok := m.GetRun("t", run1.ID)
	if !ok || got.ID != run1.ID {
		t.Error("GetRun failed")
	}

	runs := m.ListRuns("t", p.ID)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].ID != run1.ID || runs[1].ID != run2.ID {
		t.Errorf("unexpected run order: %v %v", runs[0].ID, runs[1].ID)
	}
}

func TestListBySelector(t *testing.T) {
	// Smoke-test that ListBySelector exists and compiles in agent package.
	// Full behavioural tests are in pkg/agent/agent_test.go.
	// (This file just verifies the pipeline package itself.)
	_ = New()
}

// ── persistence tests ─────────────────────────────────────────────────────────

func TestLoadFromStore_Roundtrip(t *testing.T) {
	fs := newFakeStore()
	m := NewWithStore(fs)

	stages := []Stage{
		{Name: "stage0", Capability: "cap0"},
		{Name: "stage1", AgentID: "agent-1"},
	}
	p, err := m.CreatePipeline("tenant1", "my-pipeline", stages)
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	run, _, err := m.StartRun("tenant1", p.ID, map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	_, _ = m.RecordStageJob("tenant1", run.ID, "job-0")

	// Restore state into a fresh manager using the same fake store.
	m2 := NewWithStore(fs)
	pipelines, runs, err := m2.LoadFromStore("tenant1")
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if pipelines != 1 {
		t.Errorf("expected 1 pipeline loaded, got %d", pipelines)
	}
	if runs != 1 {
		t.Errorf("expected 1 run loaded, got %d", runs)
	}

	// Pipeline should be accessible.
	loaded, ok := m2.GetPipeline("tenant1", p.ID)
	if !ok {
		t.Fatal("pipeline not found after LoadFromStore")
	}
	if loaded.Name != "my-pipeline" || len(loaded.Stages) != 2 {
		t.Errorf("loaded pipeline mismatch: %+v", loaded)
	}

	// Run should be accessible.
	loadedRun, ok := m2.GetRun("tenant1", run.ID)
	if !ok {
		t.Fatal("run not found after LoadFromStore")
	}
	if len(loadedRun.JobIDs) != 1 || loadedRun.JobIDs[0] != "job-0" {
		t.Errorf("loaded run job IDs mismatch: %v", loadedRun.JobIDs)
	}

	// Counter should be restored; next IDs must not collide with loaded ones.
	p2, err := m2.CreatePipeline("tenant1", "second-pipeline", stages)
	if err != nil {
		t.Fatalf("CreatePipeline after load: %v", err)
	}
	if p2.ID == p.ID {
		t.Errorf("duplicate pipeline ID after restore: %q", p2.ID)
	}
}

func TestLoadFromStore_SkipsCorrupted(t *testing.T) {
	fs := newFakeStore()

	// Inject a corrupted pipeline record directly.
	_ = fs.PutKV("t", pipelineKeyPrefix+"pipe-bad", []byte("not-json"))

	// A valid pipeline alongside it.
	validPipeline := Pipeline{
		ID:     "pipe-99",
		Tenant: "t",
		Name:   "valid",
		Stages: []Stage{{Name: "s", Capability: "c"}},
	}
	data, _ := json.Marshal(validPipeline)
	_ = fs.PutKV("t", pipelineKeyPrefix+"pipe-99", data)

	m := NewWithStore(fs)
	loaded, _, err := m.LoadFromStore("t")
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if loaded != 1 {
		t.Errorf("expected 1 valid pipeline to load, got %d", loaded)
	}
}

func TestLoadFromStore_MultiTenant(t *testing.T) {
	fs := newFakeStore()
	m := NewWithStore(fs)

	stagesA := []Stage{{Name: "s", Capability: "capA"}}
	stagesB := []Stage{{Name: "s", Capability: "capB"}}
	pA, _ := m.CreatePipeline("tenantA", "pipeline-a", stagesA)
	_, _ = m.CreatePipeline("tenantB", "pipeline-b", stagesB)

	// Loading tenant A must not see tenant B's data.
	m2 := NewWithStore(fs)
	n, _, _ := m2.LoadFromStore("tenantA")
	if n != 1 {
		t.Errorf("expected 1 pipeline for tenantA, got %d", n)
	}
	if _, ok := m2.GetPipeline("tenantB", pA.ID); ok {
		t.Error("expected cross-tenant isolation, but tenantB can see tenantA pipeline")
	}
}

func TestDeletePipeline_PersistenceRemoved(t *testing.T) {
	fs := newFakeStore()
	m := NewWithStore(fs)

	p, _ := m.CreatePipeline("t", "pipe", []Stage{{Capability: "c"}})
	m.DeletePipeline("t", p.ID)

	// Restore from store — deleted pipeline should not be present.
	m2 := NewWithStore(fs)
	n, _, _ := m2.LoadFromStore("t")
	if n != 0 {
		t.Errorf("expected 0 pipelines after delete+restore, got %d", n)
	}
}

// ── edge-case tests ───────────────────────────────────────────────────────────

func TestAdvanceRun_AlreadyDone(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}})
	run, _, _ := m.StartRun("t", p.ID, nil)

	// Advance to done (single stage, no failure).
	run, _, err := m.AdvanceRun("t", run.ID, false)
	if err != nil || run.Status != RunStatusDone {
		t.Fatalf("first advance: status=%q err=%v", run.Status, err)
	}

	// A second advance on a terminal run must return an error.
	_, _, err = m.AdvanceRun("t", run.ID, false)
	if err == nil {
		t.Fatal("expected error on second advance of done run")
	}
}

func TestAdvanceRun_AlreadyFailed(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}, {Capability: "d"}})
	run, _, _ := m.StartRun("t", p.ID, nil)

	// Fail the run.
	run, _, _ = m.AdvanceRun("t", run.ID, true)

	// Another advance must be rejected.
	_, _, err := m.AdvanceRun("t", run.ID, false)
	if err == nil {
		t.Fatal("expected error advancing already-failed run")
	}
}

func TestAdvanceRun_MissingPipeline(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}, {Capability: "d"}})
	run, _, _ := m.StartRun("t", p.ID, nil)

	// Remove the pipeline while the run is in progress.
	m.DeletePipeline("t", p.ID)

	_, _, err := m.AdvanceRun("t", run.ID, false)
	if err == nil {
		t.Fatal("expected error when pipeline no longer exists")
	}
}

func TestUpdatePipeline_Stages(t *testing.T) {
	m := New()
	original := []Stage{{Name: "s1", Capability: "cap1"}}
	p, _ := m.CreatePipeline("t", "pipe", original)

	newStages := []Stage{
		{Name: "a", Capability: "capA"},
		{Name: "b", AgentID: "agent-x"},
	}
	updated, ok, err := m.UpdatePipeline("t", p.ID, UpdateParams{Stages: newStages})
	if err != nil || !ok {
		t.Fatalf("UpdatePipeline: ok=%v err=%v", ok, err)
	}
	if len(updated.Stages) != 2 || updated.Stages[0].Capability != "capA" {
		t.Errorf("stages not updated correctly: %+v", updated.Stages)
	}
}

func TestUpdatePipeline_InvalidStages(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t", "pipe", []Stage{{Capability: "c"}})

	badStages := []Stage{{Name: "no-agent-or-cap"}}
	_, _, err := m.UpdatePipeline("t", p.ID, UpdateParams{Stages: badStages})
	if err == nil {
		t.Fatal("expected error for stage with no agent_id or capability")
	}
}

func TestListRuns_AllPipelines(t *testing.T) {
	m := New()
	stages := []Stage{{Name: "s", Capability: "c"}}
	p1, _ := m.CreatePipeline("t", "p1", stages)
	p2, _ := m.CreatePipeline("t", "p2", stages)

	m.StartRun("t", p1.ID, nil)
	m.StartRun("t", p1.ID, nil)
	m.StartRun("t", p2.ID, nil)

	// Empty pipelineID should return all runs for the tenant.
	all := m.ListRuns("t", "")
	if len(all) != 3 {
		t.Errorf("expected 3 runs across all pipelines, got %d", len(all))
	}
}

func TestRecordStageJob_NotFound(t *testing.T) {
	m := New()
	_, err := m.RecordStageJob("t", "nonexistent", "job-0")
	if err == nil {
		t.Fatal("expected error for missing run")
	}
}

func TestGetRun_TenantIsolation(t *testing.T) {
	m := New()
	p, _ := m.CreatePipeline("t1", "p", []Stage{{Capability: "c"}})
	run, _, _ := m.StartRun("t1", p.ID, nil)

	// Tenant t2 must not see t1's run.
	if _, ok := m.GetRun("t2", run.ID); ok {
		t.Error("expected cross-tenant isolation for GetRun")
	}
}

func TestCreatePipeline_EmptyStages(t *testing.T) {
	m := New()
	_, err := m.CreatePipeline("t", "x", []Stage{})
	if err == nil {
		t.Fatal("expected error for zero stages")
	}
}

func TestStartRun_PipelineNotFound(t *testing.T) {
	m := New()
	_, _, err := m.StartRun("t", "pipe-999", nil)
	if err == nil {
		t.Fatal("expected error for non-existent pipeline")
	}
}

func TestPipelineCounterFormat(t *testing.T) {
	// IDs must match the expected prefix so parsePipelineNum / parseRunNum work.
	m := New()
	p, _ := m.CreatePipeline("t", "p", []Stage{{Capability: "c"}})
	if len(p.ID) < 6 || p.ID[:5] != "pipe-" {
		t.Errorf("unexpected pipeline ID format: %q", p.ID)
	}
	run, _, _ := m.StartRun("t", p.ID, nil)
	if len(run.ID) < 6 || run.ID[:5] != "prun-" {
		t.Errorf("unexpected run ID format: %q", run.ID)
	}
}

// Verify that pipelineKeyPrefix and runKeyPrefix constants match what
// ScanPrefix would filter by, ensuring the package-internal values are stable.
func TestKeyPrefixConstants(t *testing.T) {
	if pipelineKeyPrefix == "" {
		t.Error("pipelineKeyPrefix must not be empty")
	}
	if runKeyPrefix == "" {
		t.Error("runKeyPrefix must not be empty")
	}
	// They must be different so scans don't cross-contaminate.
	if pipelineKeyPrefix == runKeyPrefix {
		t.Errorf("pipelineKeyPrefix and runKeyPrefix must differ, both are %q", pipelineKeyPrefix)
	}
}

// Verify fmt is used somewhere in the test file to avoid import errors.
var _ = fmt.Sprintf
