package jobs

import (
	"testing"
	"time"
)

func TestSubmitAndGet(t *testing.T) {
	q := NewQueue()
	j := q.Submit("tenant1", "agent-1", "inference", map[string]any{"prompt": "hello"})
	if j.ID == "" {
		t.Fatal("job ID should not be empty")
	}
	if j.Status != StatusPending {
		t.Fatalf("expected pending, got %s", j.Status)
	}

	got, ok := q.Get(j.ID)
	if !ok {
		t.Fatal("expected job to be found")
	}
	if got.Tenant != "tenant1" {
		t.Errorf("tenant mismatch: %s", got.Tenant)
	}
}

func TestUpdate(t *testing.T) {
	q := NewQueue()
	j := q.Submit("t", "a", "cap", nil)
	if !q.Update(j.ID, StatusRunning, "", "") {
		t.Fatal("update should succeed")
	}
	got, _ := q.Get(j.ID)
	if got.Status != StatusRunning {
		t.Errorf("expected running, got %s", got.Status)
	}
	if !q.Update(j.ID, StatusDone, "ok", "") {
		t.Fatal("second update should succeed")
	}
	got, _ = q.Get(j.ID)
	if got.Status != StatusDone || got.Result != "ok" {
		t.Errorf("unexpected state: %+v", got)
	}
}

func TestUpdateMissing(t *testing.T) {
	q := NewQueue()
	if q.Update("nonexistent", StatusDone, "", "") {
		t.Fatal("update of missing job should return false")
	}
}

func TestList(t *testing.T) {
	q := NewQueue()
	q.Submit("t1", "a", "c", nil)
	q.Submit("t1", "b", "c", nil)
	q.Submit("t2", "c", "c", nil)

	all := q.List("")
	if len(all) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(all))
	}
	t1 := q.List("t1")
	if len(t1) != 2 {
		t.Errorf("expected 2 jobs for t1, got %d", len(t1))
	}
}

func TestListOrder(t *testing.T) {
	q := NewQueue()
	j1 := q.Submit("t", "a", "c", nil)
	time.Sleep(2 * time.Millisecond)
	j2 := q.Submit("t", "b", "c", nil)

	list := q.List("t")
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	// newest first
	if list[0].ID != j2.ID {
		t.Errorf("expected %s first, got %s", j2.ID, list[0].ID)
	}
	if list[1].ID != j1.ID {
		t.Errorf("expected %s second, got %s", j1.ID, list[1].ID)
	}
}

func TestPrune(t *testing.T) {
	q := NewQueue()
	j := q.Submit("t", "a", "c", nil)
	q.Update(j.ID, StatusDone, "ok", "")

	// Prune with very small max age — should remove the done job.
	n := q.Prune(time.Nanosecond)
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}
	if q.Count() != 0 {
		t.Errorf("expected 0 jobs after prune, got %d", q.Count())
	}
}

func TestPruneKeepsPending(t *testing.T) {
	q := NewQueue()
	q.Submit("t", "a", "c", nil) // pending — should not be pruned
	n := q.Prune(time.Nanosecond)
	if n != 0 {
		t.Errorf("expected 0 pruned, got %d", n)
	}
	if q.Count() != 1 {
		t.Errorf("expected 1 job after prune, got %d", q.Count())
	}
}
