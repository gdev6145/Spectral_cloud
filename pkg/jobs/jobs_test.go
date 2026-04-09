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

func TestSubmitWithOpts_Priority(t *testing.T) {
q := NewQueue()
j := q.SubmitWithOpts("t", "a", "cap", nil, SubmitOptions{Priority: 5, TTLSeconds: 0})
if j.Priority != 5 {
t.Fatalf("expected priority 5, got %d", j.Priority)
}
if j.ExpiresAt != nil {
t.Fatal("expected nil ExpiresAt when TTLSeconds=0")
}
}

func TestSubmitWithOpts_TTL(t *testing.T) {
q := NewQueue()
j := q.SubmitWithOpts("t", "a", "cap", nil, SubmitOptions{TTLSeconds: 60})
if j.ExpiresAt == nil {
t.Fatal("expected non-nil ExpiresAt")
}
if j.ExpiresAt.Before(time.Now()) {
t.Fatal("ExpiresAt should be in the future")
}
}

func TestExpireStale(t *testing.T) {
q := NewQueue()
// job with TTL already expired
j1 := q.SubmitWithOpts("t", "a", "cap", nil, SubmitOptions{TTLSeconds: 1})
// backdating ExpiresAt
past := time.Now().Add(-2 * time.Second)
q.mu.Lock()
q.jobs[j1.ID].ExpiresAt = &past
q.mu.Unlock()

// job with future TTL — should not be expired
q.SubmitWithOpts("t", "a", "cap", nil, SubmitOptions{TTLSeconds: 3600})

// job with no TTL — should not be expired
q.Submit("t", "a", "cap", nil)

n := q.ExpireStale()
if n != 1 {
t.Fatalf("expected 1 expired, got %d", n)
}
got, _ := q.Get(j1.ID)
if got.Status != StatusFailed {
t.Fatalf("expected failed, got %s", got.Status)
}
if got.Error != "ttl expired" {
t.Fatalf("unexpected error: %s", got.Error)
}
}

func TestExpireStale_SkipsNonPending(t *testing.T) {
q := NewQueue()
j := q.SubmitWithOpts("t", "a", "cap", nil, SubmitOptions{TTLSeconds: 1})
past := time.Now().Add(-2 * time.Second)
q.mu.Lock()
q.jobs[j.ID].ExpiresAt = &past
q.jobs[j.ID].Status = StatusRunning // already running — must not be expired
q.mu.Unlock()
n := q.ExpireStale()
if n != 0 {
t.Fatalf("expected 0 expired, got %d", n)
}
}

func TestClaim_PriorityOrder(t *testing.T) {
q := NewQueue()
// lower priority first
q.SubmitWithOpts("t", "agent-1", "cap", nil, SubmitOptions{Priority: 1})
// higher priority second
high := q.SubmitWithOpts("t", "agent-1", "cap", nil, SubmitOptions{Priority: 10})

claimed, ok := q.Claim("agent-1", "")
if !ok {
t.Fatal("expected a claim")
}
if claimed.ID != high.ID {
t.Fatalf("expected high priority job %s, got %s", high.ID, claimed.ID)
}
}

func TestListByStatus(t *testing.T) {
q := NewQueue()
q.Submit("t1", "a", "cap", nil)
q.Submit("t1", "a", "cap", nil)
j3 := q.Submit("t1", "a", "cap", nil)
q.Update(j3.ID, StatusDone, "ok", "")
q.Submit("t2", "a", "cap", nil)

pending := q.ListByStatus("t1", StatusPending)
if len(pending) != 2 {
t.Fatalf("expected 2 pending for t1, got %d", len(pending))
}
done := q.ListByStatus("t1", StatusDone)
if len(done) != 1 {
t.Fatalf("expected 1 done for t1, got %d", len(done))
}
// all tenants
all := q.ListByStatus("", StatusPending)
if len(all) != 3 {
t.Fatalf("expected 3 pending across all tenants, got %d", len(all))
}
}

func TestCountByStatus(t *testing.T) {
q := NewQueue()
j1 := q.Submit("t1", "a", "cap", nil)
j2 := q.Submit("t1", "a", "cap", nil)
q.Update(j1.ID, StatusDone, "ok", "")
q.Update(j2.ID, StatusFailed, "", "err")
q.Submit("t2", "a", "cap", nil)

counts := q.CountByStatus("t1")
if counts[StatusDone] != 1 {
t.Errorf("expected done=1, got %d", counts[StatusDone])
}
if counts[StatusFailed] != 1 {
t.Errorf("expected failed=1, got %d", counts[StatusFailed])
}
if counts[StatusPending] != 0 {
t.Errorf("expected pending=0, got %d", counts[StatusPending])
}

all := q.CountByStatus("")
if all[StatusPending] != 1 { // t2's job
t.Errorf("expected pending=1 across all, got %d", all[StatusPending])
}
}
