// Package jobs provides an agent job queue backed by optional BoltDB persistence.
package jobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Persister is the subset of store.Store methods that jobs needs.
// Using an interface avoids a circular import.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

const jobKeyPrefix = "job_"

// Status values for a job.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Job represents a unit of work dispatched to an agent.
type Job struct {
	ID         string         `json:"id"`
	Tenant     string         `json:"tenant"`
	AgentID    string         `json:"agent_id,omitempty"`
	Capability string         `json:"capability,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Priority   int            `json:"priority,omitempty"`   // higher = more urgent; default 0
	TTLSeconds int            `json:"ttl_seconds,omitempty"` // 0 = no expiry
	Status     Status         `json:"status"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ExpiresAt  *time.Time     `json:"expires_at,omitempty"`
}

// Queue is a thread-safe job store with optional BoltDB persistence.
type Queue struct {
	mu      sync.RWMutex
	jobs    map[string]*Job
	counter uint64
	store   Persister
}

func NewQueue() *Queue {
	return &Queue{jobs: make(map[string]*Job)}
}

// NewQueueWithStore creates a Queue backed by the given Persister.
// Call LoadFromStore after construction to restore jobs from a previous run.
func NewQueueWithStore(p Persister) *Queue {
	return &Queue{jobs: make(map[string]*Job), store: p}
}

// LoadFromStore reads all persisted jobs for a tenant and restores them into
// memory. It also advances the counter past the highest seen job number so new
// jobs don't collide with loaded ones. Returns the number of jobs loaded.
func (q *Queue) LoadFromStore(tenant string) (int, error) {
	if q.store == nil {
		return 0, nil
	}
	var loaded []Job
	if err := q.store.ScanPrefix(tenant, jobKeyPrefix, func(_, val []byte) error {
		var j Job
		if err := json.Unmarshal(val, &j); err != nil {
			return nil // skip corrupted entries
		}
		loaded = append(loaded, j)
		return nil
	}); err != nil {
		return 0, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range loaded {
		j := loaded[i]
		q.jobs[j.ID] = &j
		// Keep counter above any loaded ID to prevent collisions.
		if n := parseJobNum(j.ID); n > q.counter {
			q.counter = n
		}
	}
	return len(loaded), nil
}

func parseJobNum(id string) uint64 {
	// IDs are "job-N"; extract N.
	if !strings.HasPrefix(id, "job-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[4:], "%d", &n)
	return n
}

// persist writes a single job to the store. Called with q.mu held (read or write).
// Errors are silently dropped — persistence is best-effort.
func (q *Queue) persist(j *Job) {
	if q.store == nil {
		return
	}
	data, err := json.Marshal(j)
	if err != nil {
		return
	}
	_ = q.store.PutKV(j.Tenant, jobKeyPrefix+j.ID, data)
}

// unpersist removes a job from the store.
func (q *Queue) unpersist(j *Job) {
	if q.store == nil {
		return
	}
	_ = q.store.DeleteKV(j.Tenant, jobKeyPrefix+j.ID)
}

// SubmitOptions carries optional fields for job submission.
type SubmitOptions struct {
	Priority   int
	TTLSeconds int
}

// Submit creates a new pending job and returns it.
func (q *Queue) Submit(tenant, agentID, capability string, payload map[string]any) *Job {
	return q.SubmitWithOpts(tenant, agentID, capability, payload, SubmitOptions{})
}

// SubmitWithOpts creates a new pending job with extended options.
func (q *Queue) SubmitWithOpts(tenant, agentID, capability string, payload map[string]any, opts SubmitOptions) *Job {
	id := fmt.Sprintf("job-%d", atomic.AddUint64(&q.counter, 1))
	now := time.Now().UTC()
	var expiresAt *time.Time
	if opts.TTLSeconds > 0 {
		t := now.Add(time.Duration(opts.TTLSeconds) * time.Second)
		expiresAt = &t
	}
	j := &Job{
		ID:         id,
		Tenant:     tenant,
		AgentID:    agentID,
		Capability: capability,
		Payload:    payload,
		Priority:   opts.Priority,
		TTLSeconds: opts.TTLSeconds,
		Status:     StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  expiresAt,
	}
	q.mu.Lock()
	q.jobs[id] = j
	q.persist(j)
	q.mu.Unlock()
	return j
}

// Get returns a copy of the job, or false if not found.
func (q *Queue) Get(id string) (Job, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	j, ok := q.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// Update changes a job's status, result, and error message.
// Returns false if the job ID is not found.
func (q *Queue) Update(id string, status Status, result, errMsg string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return false
	}
	j.Status = status
	j.Result = result
	j.Error = errMsg
	j.UpdatedAt = time.Now().UTC()
	q.persist(j)
	return true
}

// List returns all jobs for tenant, newest first. Pass "" for all tenants.
func (q *Queue) List(tenant string) []Job {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []Job
	for _, j := range q.jobs {
		if tenant == "" || j.Tenant == tenant {
			out = append(out, *j)
		}
	}
	// insertion-sort descending by CreatedAt
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].CreatedAt.After(out[k-1].CreatedAt); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

// Count returns the total number of jobs.
func (q *Queue) Count() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.jobs)
}

// Cancel transitions a pending or running job to cancelled.
// Returns false if the job is not found or is already in a terminal state.
func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return false
	}
	if j.Status == StatusDone || j.Status == StatusFailed || j.Status == StatusCancelled {
		return false
	}
	j.Status = StatusCancelled
	j.UpdatedAt = time.Now().UTC()
	q.persist(j)
	return true
}

// Claim atomically finds the highest-priority (then oldest) pending job matching
// agentID or capability and transitions it to running. agentID takes priority:
// if provided, only jobs assigned to that agent are considered. Otherwise jobs
// whose Capability matches are eligible. Returns (Job, true) on success.
func (q *Queue) Claim(agentID, capability string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var best *Job
	for _, j := range q.jobs {
		if j.Status != StatusPending {
			continue
		}
		var match bool
		if agentID != "" {
			match = j.AgentID == agentID
		} else if capability != "" {
			match = j.Capability == capability
		}
		if !match {
			continue
		}
		if best == nil ||
			j.Priority > best.Priority ||
			(j.Priority == best.Priority && j.CreatedAt.Before(best.CreatedAt)) {
			best = j
		}
	}
	if best == nil {
		return Job{}, false
	}
	if agentID != "" {
		best.AgentID = agentID
	}
	best.Status = StatusRunning
	best.UpdatedAt = time.Now().UTC()
	q.persist(best)
	return *best, true
}

// ClaimForTenant is like Claim but restricts candidates to the given tenant.
func (q *Queue) ClaimForTenant(tenant, agentID, capability string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var best *Job
	for _, j := range q.jobs {
		if j.Status != StatusPending {
			continue
		}
		if j.Tenant != tenant {
			continue
		}
		var match bool
		if agentID != "" {
			match = j.AgentID == agentID
		} else if capability != "" {
			match = j.Capability == capability
		}
		if !match {
			continue
		}
		if best == nil ||
			j.Priority > best.Priority ||
			(j.Priority == best.Priority && j.CreatedAt.Before(best.CreatedAt)) {
			best = j
		}
	}
	if best == nil {
		return Job{}, false
	}
	if agentID != "" {
		best.AgentID = agentID
	}
	best.Status = StatusRunning
	best.UpdatedAt = time.Now().UTC()
	q.persist(best)
	return *best, true
}

// ExpireStale marks pending jobs whose TTL has elapsed as failed.
// Returns the number of jobs expired.
func (q *Queue) ExpireStale() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now().UTC()
	n := 0
	for _, j := range q.jobs {
		if j.Status == StatusPending && j.ExpiresAt != nil && now.After(*j.ExpiresAt) {
			j.Status = StatusFailed
			j.Error = "ttl expired"
			j.UpdatedAt = now
			q.persist(j)
			n++
		}
	}
	return n
}

// ListByStatus returns all jobs for tenant with the given status, newest first.
// Pass "" for tenant to list across all tenants.
func (q *Queue) ListByStatus(tenant string, status Status) []Job {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var out []Job
	for _, j := range q.jobs {
		if (tenant == "" || j.Tenant == tenant) && j.Status == status {
			out = append(out, *j)
		}
	}
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].CreatedAt.After(out[k-1].CreatedAt); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

// CountByStatus returns a map of status → count for the given tenant.
// Pass "" for tenant to count across all tenants.
func (q *Queue) CountByStatus(tenant string) map[Status]int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make(map[Status]int)
	for _, j := range q.jobs {
		if tenant == "" || j.Tenant == tenant {
			out[j.Status]++
		}
	}
	return out
}

// Prune removes completed/failed/cancelled jobs older than maxAge. Returns count removed.
func (q *Queue) Prune(maxAge time.Duration) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for id, j := range q.jobs {
		if (j.Status == StatusDone || j.Status == StatusFailed || j.Status == StatusCancelled) && j.UpdatedAt.Before(cutoff) {
			q.unpersist(j)
			delete(q.jobs, id)
			n++
		}
	}
	return n
}
