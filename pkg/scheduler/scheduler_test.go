package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

func TestAddList(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	s, err := m.Add("daily-sync", "t1", "", "sync", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.Name != "daily-sync" {
		t.Errorf("unexpected schedule: %+v", s)
	}
	list := m.List()
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}
}

func TestAddValidation(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	if _, err := m.Add("", "t1", "", "sync", nil, time.Hour); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := m.Add("ok", "t1", "", "sync", nil, 10*time.Millisecond); err == nil {
		t.Error("expected error for sub-50ms interval")
	}
}

func TestDelete(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	s, _ := m.Add("s1", "t1", "", "sync", nil, time.Hour)
	if !m.Delete(s.ID) {
		t.Fatal("expected true")
	}
	if m.Delete(s.ID) {
		t.Fatal("expected false on second delete")
	}
}

func TestGet(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	s, _ := m.Add("s1", "t1", "agent-1", "infer", map[string]any{"k": "v"}, time.Hour)
	snap, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("expected ok")
	}
	if snap.Capability != "infer" || snap.AgentID != "agent-1" {
		t.Errorf("unexpected snap: %+v", snap)
	}
}

func TestUpdatePauseResume(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	s, err := m.Add("fast", "t1", "agent-1", "ping", map[string]any{"mode": "slow"}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	paused := false
	updatedInterval := 100 * time.Millisecond
	updated, ok, err := m.Update(s.ID, UpdateParams{
		Name:     strPtr("fast-paused"),
		Payload:  &map[string]any{"mode": "fast"},
		Interval: &updatedInterval,
		Active:   &paused,
	})
	if err != nil {
		t.Fatalf("pause schedule: %v", err)
	}
	if !ok {
		t.Fatal("expected schedule to exist")
	}
	if updated.Active || updated.Name != "fast-paused" || updated.Interval != updatedInterval {
		t.Fatalf("unexpected paused schedule: %+v", updated)
	}

	time.Sleep(120 * time.Millisecond)
	if q.Count() != 0 {
		t.Fatalf("expected paused schedule to submit no jobs, got %d", q.Count())
	}

	resumed := true
	updated, ok, err = m.Update(s.ID, UpdateParams{Active: &resumed})
	if err != nil {
		t.Fatalf("resume schedule: %v", err)
	}
	if !ok {
		t.Fatal("expected schedule to exist")
	}
	if !updated.Active {
		t.Fatal("expected resumed schedule to be active")
	}

	time.Sleep(150 * time.Millisecond)
	if q.Count() == 0 {
		t.Fatal("expected resumed schedule to submit jobs")
	}

	snap, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("expected updated schedule")
	}
	if snap.Payload["mode"] != "fast" {
		t.Fatalf("expected updated payload, got %+v", snap.Payload)
	}
}

func TestUpdateActiveScheduleUsesLatestSubmissionFields(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	s, err := m.Add("fast", "t1", "agent-1", "ping", map[string]any{"mode": "slow"}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.StopAll)

	time.Sleep(70 * time.Millisecond)

	updated, ok, err := m.Update(s.ID, UpdateParams{
		AgentID:    strPtr("agent-2"),
		Capability: strPtr("sync"),
		Payload:    &map[string]any{"mode": "fast"},
	})
	if err != nil {
		t.Fatalf("update active schedule: %v", err)
	}
	if !ok {
		t.Fatal("expected schedule to exist")
	}
	if updated.AgentID != "agent-2" || updated.Capability != "sync" {
		t.Fatalf("unexpected updated schedule: %+v", updated)
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, job := range q.List("t1") {
			if job.AgentID == "agent-2" && job.Capability == "sync" {
				if job.Payload["mode"] != "fast" {
					t.Fatalf("expected updated payload in submitted job, got %+v", job.Payload)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("expected an active schedule update to affect later submitted jobs")
}

func TestRunSubmitsJob(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	_, err := m.Add("fast", "t1", "agent-1", "ping", nil, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if q.Count() == 0 {
		t.Error("expected at least one job submitted by scheduler")
	}
	// RunCount should be > 0.
	list := m.List()
	if list[0].RunCount == 0 {
		t.Error("expected RunCount > 0")
	}
}

func TestStopAll(t *testing.T) {
	q := jobs.NewQueue()
	m := New(q)
	m.Add("s1", "t1", "", "sync", nil, time.Hour)
	m.Add("s2", "t1", "", "sync", nil, time.Hour)
	m.StopAll() // should not panic
}

func TestLoadFromStoreRestoresSchedules(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q1 := jobs.NewQueueWithStore(db)
	m1 := NewWithStore(q1, db)
	sched, err := m1.Add("daily-sync", "t1", "agent-1", "sync", map[string]any{"k": "v"}, time.Hour)
	if err != nil {
		t.Fatalf("add schedule: %v", err)
	}
	m1.StopAll()

	q2 := jobs.NewQueueWithStore(db)
	m2 := NewWithStore(q2, db)
	t.Cleanup(m2.StopAll)
	n, err := m2.LoadFromStore(context.Background(), "t1")
	if err != nil {
		t.Fatalf("load schedules: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 loaded schedule, got %d", n)
	}
	got, ok := m2.Get(sched.ID)
	if !ok {
		t.Fatal("expected restored schedule")
	}
	if got.Tenant != "t1" || got.AgentID != "agent-1" || got.Capability != "sync" {
		t.Fatalf("unexpected restored schedule: %+v", got)
	}
}

func TestLoadFromStoreRestoresInactiveSchedule(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q1 := jobs.NewQueueWithStore(db)
	m1 := NewWithStore(q1, db)
	sched, err := m1.Add("paused-sync", "t1", "agent-1", "sync", nil, time.Hour)
	if err != nil {
		t.Fatalf("add schedule: %v", err)
	}
	paused := false
	if _, ok, err := m1.Update(sched.ID, UpdateParams{Active: &paused}); err != nil {
		t.Fatalf("pause schedule: %v", err)
	} else if !ok {
		t.Fatal("expected paused schedule to exist")
	}
	m1.StopAll()

	q2 := jobs.NewQueueWithStore(db)
	m2 := NewWithStore(q2, db)
	t.Cleanup(m2.StopAll)
	n, err := m2.LoadFromStore(context.Background(), "t1")
	if err != nil {
		t.Fatalf("load schedules: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 loaded schedule, got %d", n)
	}
	got, ok := m2.Get(sched.ID)
	if !ok {
		t.Fatal("expected inactive schedule to be restored")
	}
	if got.Active {
		t.Fatalf("expected restored schedule to stay inactive: %+v", got)
	}
}

func TestDeleteRemovesPersistedSchedule(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(store.DBPath(tmp))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q := jobs.NewQueueWithStore(db)
	m := NewWithStore(q, db)
	sched, err := m.Add("cleanup", "t1", "", "sync", nil, time.Hour)
	if err != nil {
		t.Fatalf("add schedule: %v", err)
	}
	if !m.Delete(sched.ID) {
		t.Fatal("expected delete to succeed")
	}

	reloaded := NewWithStore(q, db)
	t.Cleanup(reloaded.StopAll)
	n, err := reloaded.LoadFromStore(context.Background(), "t1")
	if err != nil {
		t.Fatalf("reload schedules: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 restored schedules after delete, got %d", n)
	}
}

func strPtr(s string) *string {
	return &s
}
