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

// ---------------------------------------------------------------------------
// Cancel
// ---------------------------------------------------------------------------

func TestCancel_Pending(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "a", "cap", nil)
if !q.Cancel(j.ID) {
t.Fatal("expected Cancel to return true for pending job")
}
got, _ := q.Get(j.ID)
if got.Status != StatusCancelled {
t.Fatalf("expected cancelled, got %s", got.Status)
}
}

func TestCancel_Running(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "a", "cap", nil)
q.Update(j.ID, StatusRunning, "", "")
if !q.Cancel(j.ID) {
t.Fatal("expected Cancel to return true for running job")
}
got, _ := q.Get(j.ID)
if got.Status != StatusCancelled {
t.Fatalf("expected cancelled, got %s", got.Status)
}
}

func TestCancel_AlreadyDone(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "a", "cap", nil)
q.Update(j.ID, StatusDone, "ok", "")
if q.Cancel(j.ID) {
t.Fatal("expected Cancel to return false for terminal job")
}
}

func TestCancel_AlreadyCancelled(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "a", "cap", nil)
q.Cancel(j.ID)
if q.Cancel(j.ID) {
t.Fatal("expected false on double-cancel")
}
}

func TestCancel_Missing(t *testing.T) {
q := NewQueue()
if q.Cancel("nonexistent") {
t.Fatal("expected false for missing job")
}
}

// ---------------------------------------------------------------------------
// Claim
// ---------------------------------------------------------------------------

func TestClaim_ByAgentID(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "agent-1", "cap", nil)
got, ok := q.Claim("agent-1", "")
if !ok {
t.Fatal("expected claim to succeed")
}
if got.ID != j.ID {
t.Fatalf("expected job %s, got %s", j.ID, got.ID)
}
if got.Status != StatusRunning {
t.Fatalf("expected running, got %s", got.Status)
}
}

func TestClaim_ByCapability(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "", "inference", nil)
got, ok := q.Claim("", "inference")
if !ok {
t.Fatal("expected claim to succeed")
}
if got.ID != j.ID {
t.Fatalf("expected job %s, got %s", j.ID, got.ID)
}
if got.Status != StatusRunning {
t.Fatalf("expected running, got %s", got.Status)
}
}

func TestClaim_OldestFirst(t *testing.T) {
q := NewQueue()
j1 := q.Submit("t", "agent-x", "cap", nil)
time.Sleep(2 * time.Millisecond)
_ = q.Submit("t", "agent-x", "cap", nil)
got, ok := q.Claim("agent-x", "")
if !ok {
t.Fatal("expected claim to succeed")
}
if got.ID != j1.ID {
t.Fatalf("expected oldest job %s first, got %s", j1.ID, got.ID)
}
}

func TestClaim_NoPendingJobs(t *testing.T) {
q := NewQueue()
_, ok := q.Claim("agent-z", "")
if ok {
t.Fatal("expected false when no pending jobs")
}
}

func TestClaim_SkipsRunning(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "agent-1", "cap", nil)
q.Update(j.ID, StatusRunning, "", "")
_, ok := q.Claim("agent-1", "")
if ok {
t.Fatal("expected false — running job should not be claimable")
}
}

func TestClaim_SkipsCancelled(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "agent-1", "cap", nil)
q.Cancel(j.ID)
_, ok := q.Claim("agent-1", "")
if ok {
t.Fatal("expected false — cancelled job should not be claimable")
}
}

func TestPrune_IncludesCancelled(t *testing.T) {
q := NewQueue()
j := q.Submit("t", "a", "c", nil)
q.Cancel(j.ID)
n := q.Prune(time.Nanosecond)
if n != 1 {
t.Fatalf("expected 1 pruned (cancelled), got %d", n)
}
}
