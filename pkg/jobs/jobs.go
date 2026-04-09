// Package jobs provides an in-memory agent job queue.
package jobs

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

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

// Queue is a thread-safe in-memory job store.
type Queue struct {
	mu      sync.RWMutex
	jobs    map[string]*Job
	counter uint64
}

func NewQueue() *Queue {
	return &Queue{jobs: make(map[string]*Job)}
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
			delete(q.jobs, id)
			n++
		}
	}
	return n
}
