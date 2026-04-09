package scheduler

import (
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
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
