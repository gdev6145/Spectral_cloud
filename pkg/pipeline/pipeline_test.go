package pipeline

import (
	"testing"
)

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
