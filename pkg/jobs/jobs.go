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
	Status     Status         `json:"status"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
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

// Submit creates a new pending job and returns it.
func (q *Queue) Submit(tenant, agentID, capability string, payload map[string]any) *Job {
	id := fmt.Sprintf("job-%d", atomic.AddUint64(&q.counter, 1))
	now := time.Now().UTC()
	j := &Job{
		ID:         id,
		Tenant:     tenant,
		AgentID:    agentID,
		Capability: capability,
		Payload:    payload,
		Status:     StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
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

// Claim atomically finds the oldest pending job matching agentID or capability
// and transitions it to running, assigning it to the claimant. agentID takes
// priority: if provided, only jobs assigned to that agent are considered.
// Otherwise, jobs whose Capability matches are eligible (including unassigned
// jobs with a matching capability).  Returns (Job, true) on success.
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
		if best == nil || j.CreatedAt.Before(best.CreatedAt) {
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
