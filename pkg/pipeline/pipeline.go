// Package pipeline manages multi-stage agent pipelines.
//
// A Pipeline is a named, ordered sequence of Stages. Each Stage targets an
// agent by ID or by capability. Callers start a Run against a pipeline; the
// run advances one stage at a time as each stage's job completes, letting the
// output of one job flow into the payload of the next.
package pipeline

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Persister is the subset of store.Store methods that pipeline needs.
type Persister interface {
	PutKV(tenant, key string, value []byte) error
	DeleteKV(tenant, key string) error
	ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error
}

const (
	pipelineKeyPrefix = "pipeline_"
	runKeyPrefix      = "prun_"
)

// Stage is one step in a pipeline. Either AgentID or Capability must be set.
// Payload carries static fields merged into the job payload for this stage;
// a "_prev_result" key is automatically injected from the prior stage result.
type Stage struct {
	Name       string         `json:"name"`
	AgentID    string         `json:"agent_id,omitempty"`
	Capability string         `json:"capability,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// Pipeline is a named sequence of stages.
type Pipeline struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Name      string    `json:"name"`
	Stages    []Stage   `json:"stages"`
	CreatedAt time.Time `json:"created_at"`
}

// RunStatus is the lifecycle state of a pipeline run.
type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusDone    RunStatus = "done"
	RunStatusFailed  RunStatus = "failed"
)

// Run tracks the execution state of a single pipeline invocation.
type Run struct {
	ID           string         `json:"id"`
	PipelineID   string         `json:"pipeline_id"`
	Tenant       string         `json:"tenant"`
	Status       RunStatus      `json:"status"`
	CurrentStage int            `json:"current_stage"`
	// JobIDs holds the job ID submitted for each stage (index = stage index).
	JobIDs       []string       `json:"job_ids"`
	// Input is the initial payload passed to the first stage.
	Input        map[string]any `json:"input,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// UpdateParams describes a partial pipeline update.
type UpdateParams struct {
	Name   *string
	Stages []Stage // nil = unchanged
}

// Manager holds all pipelines and runs.
type Manager struct {
	mu              sync.RWMutex
	pipelines       map[string]*Pipeline
	runs            map[string]*Run
	pipelineCounter uint64
	runCounter      uint64
	store           Persister
}

// New creates an empty in-memory Manager.
func New() *Manager {
	return &Manager{
		pipelines: make(map[string]*Pipeline),
		runs:      make(map[string]*Run),
	}
}

// NewWithStore creates a Manager backed by a Persister.
// Call LoadFromStore after construction to restore state from a previous run.
func NewWithStore(p Persister) *Manager {
	return &Manager{
		pipelines: make(map[string]*Pipeline),
		runs:      make(map[string]*Run),
		store:     p,
	}
}

func parsePipelineNum(id string) uint64 {
	if !strings.HasPrefix(id, "pipe-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[5:], "%d", &n)
	return n
}

func parseRunNum(id string) uint64 {
	if !strings.HasPrefix(id, "prun-") {
		return 0
	}
	var n uint64
	fmt.Sscanf(id[5:], "%d", &n)
	return n
}

func (m *Manager) persistPipeline(p *Pipeline) {
	if m.store == nil {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		log.Printf("warn: failed to marshal pipeline %q for tenant %q: %v", p.ID, p.Tenant, err)
		return
	}
	if err := m.store.PutKV(p.Tenant, pipelineKeyPrefix+p.ID, data); err != nil {
		log.Printf("warn: failed to persist pipeline %q for tenant %q: %v", p.ID, p.Tenant, err)
	}
}

func (m *Manager) unpersistPipeline(p *Pipeline) {
	if m.store == nil {
		return
	}
	if err := m.store.DeleteKV(p.Tenant, pipelineKeyPrefix+p.ID); err != nil {
		log.Printf("warn: failed to delete pipeline %q for tenant %q: %v", p.ID, p.Tenant, err)
	}
}

func (m *Manager) persistRun(r *Run) {
	if m.store == nil {
		return
	}
	data, err := json.Marshal(r)
	if err != nil {
		log.Printf("warn: failed to marshal pipeline run %q for tenant %q: %v", r.ID, r.Tenant, err)
		return
	}
	if err := m.store.PutKV(r.Tenant, runKeyPrefix+r.ID, data); err != nil {
		log.Printf("warn: failed to persist pipeline run %q for tenant %q: %v", r.ID, r.Tenant, err)
	}
}

// LoadFromStore reads persisted pipelines and runs for a tenant.
// Returns (pipelines loaded, runs loaded, error).
func (m *Manager) LoadFromStore(tenant string) (int, int, error) {
	if m.store == nil {
		return 0, 0, nil
	}
	var pipelines []Pipeline
	if err := m.store.ScanPrefix(tenant, pipelineKeyPrefix, func(_, val []byte) error {
		var p Pipeline
		if err := json.Unmarshal(val, &p); err != nil {
			log.Printf("warn: skipping corrupted pipeline for tenant %q: %v", tenant, err)
			return nil
		}
		pipelines = append(pipelines, p)
		return nil
	}); err != nil {
		return 0, 0, err
	}
	var runs []Run
	if err := m.store.ScanPrefix(tenant, runKeyPrefix, func(_, val []byte) error {
		var r Run
		if err := json.Unmarshal(val, &r); err != nil {
			log.Printf("warn: skipping corrupted pipeline run for tenant %q: %v", tenant, err)
			return nil
		}
		runs = append(runs, r)
		return nil
	}); err != nil {
		return 0, 0, err
	}
	m.mu.Lock()
	for i := range pipelines {
		p := pipelines[i]
		if n := parsePipelineNum(p.ID); n > m.pipelineCounter {
			m.pipelineCounter = n
		}
		pCopy := p
		m.pipelines[p.ID] = &pCopy
	}
	for i := range runs {
		r := runs[i]
		if n := parseRunNum(r.ID); n > m.runCounter {
			m.runCounter = n
		}
		rCopy := r
		m.runs[r.ID] = &rCopy
	}
	m.mu.Unlock()
	return len(pipelines), len(runs), nil
}

// validateStages checks that every stage has at least an agent_id or capability.
func validateStages(stages []Stage) error {
	if len(stages) == 0 {
		return fmt.Errorf("at least one stage is required")
	}
	for i, s := range stages {
		if s.AgentID == "" && s.Capability == "" {
			return fmt.Errorf("stage %d (%q): agent_id or capability is required", i, s.Name)
		}
	}
	return nil
}

// CreatePipeline creates a new pipeline for the tenant.
func (m *Manager) CreatePipeline(tenant, name string, stages []Stage) (*Pipeline, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if err := validateStages(stages); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("pipe-%d", atomic.AddUint64(&m.pipelineCounter, 1))
	p := &Pipeline{
		ID:        id,
		Tenant:    tenant,
		Name:      name,
		Stages:    stages,
		CreatedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	m.pipelines[id] = p
	m.persistPipeline(p)
	m.mu.Unlock()
	return p, nil
}

// GetPipeline returns a pipeline by ID for the given tenant.
func (m *Manager) GetPipeline(tenant, id string) (Pipeline, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pipelines[id]
	if !ok || p.Tenant != tenant {
		return Pipeline{}, false
	}
	return *p, true
}

// ListPipelines returns all pipelines for the tenant sorted by name.
// Pass "" to list across all tenants.
func (m *Manager) ListPipelines(tenant string) []Pipeline {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Pipeline, 0, len(m.pipelines))
	for _, p := range m.pipelines {
		if tenant != "" && p.Tenant != tenant {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UpdatePipeline applies a partial update to a pipeline. Returns (updated, found, err).
func (m *Manager) UpdatePipeline(tenant, id string, params UpdateParams) (Pipeline, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pipelines[id]
	if !ok || p.Tenant != tenant {
		return Pipeline{}, false, nil
	}
	if params.Name != nil {
		if *params.Name == "" {
			return Pipeline{}, true, fmt.Errorf("name is required")
		}
		p.Name = *params.Name
	}
	if params.Stages != nil {
		if err := validateStages(params.Stages); err != nil {
			return Pipeline{}, true, err
		}
		p.Stages = params.Stages
	}
	m.persistPipeline(p)
	return *p, true, nil
}

// DeletePipeline removes a pipeline. Returns false if not found.
func (m *Manager) DeletePipeline(tenant, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pipelines[id]
	if !ok || p.Tenant != tenant {
		return false
	}
	m.unpersistPipeline(p)
	delete(m.pipelines, id)
	return true
}

// StartRun creates a new run for the pipeline. Returns the run and the first
// stage so the caller can submit the stage job. Input is the initial payload
// passed to stage 0.
func (m *Manager) StartRun(tenant, pipelineID string, input map[string]any) (Run, Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pipelines[pipelineID]
	if !ok || p.Tenant != tenant {
		return Run{}, Stage{}, fmt.Errorf("pipeline not found")
	}
	id := fmt.Sprintf("prun-%d", atomic.AddUint64(&m.runCounter, 1))
	now := time.Now().UTC()
	r := &Run{
		ID:           id,
		PipelineID:   pipelineID,
		Tenant:       tenant,
		Status:       RunStatusRunning,
		CurrentStage: 0,
		JobIDs:       make([]string, 0),
		Input:        input,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.runs[id] = r
	m.persistRun(r)
	return *r, p.Stages[0], nil
}

// RecordStageJob records the job ID submitted for the current stage.
func (m *Manager) RecordStageJob(tenant, runID, jobID string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok || r.Tenant != tenant {
		return Run{}, fmt.Errorf("run not found")
	}
	r.JobIDs = append(r.JobIDs, jobID)
	r.UpdatedAt = time.Now().UTC()
	m.persistRun(r)
	return *r, nil
}

// AdvanceRun advances the run to the next stage (or marks it done/failed).
// Returns the updated run and the next Stage to execute (nil if terminal).
// failed=true immediately marks the run as failed regardless of remaining stages.
func (m *Manager) AdvanceRun(tenant, runID string, failed bool) (Run, *Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok || r.Tenant != tenant {
		return Run{}, nil, fmt.Errorf("run not found")
	}
	if r.Status != RunStatusRunning {
		return Run{}, nil, fmt.Errorf("run is not running (status: %s)", r.Status)
	}
	p, ok := m.pipelines[r.PipelineID]
	if !ok {
		return Run{}, nil, fmt.Errorf("pipeline no longer exists")
	}
	r.UpdatedAt = time.Now().UTC()
	if failed {
		r.Status = RunStatusFailed
		m.persistRun(r)
		return *r, nil, nil
	}
	nextStage := r.CurrentStage + 1
	if nextStage >= len(p.Stages) {
		r.Status = RunStatusDone
		m.persistRun(r)
		return *r, nil, nil
	}
	r.CurrentStage = nextStage
	m.persistRun(r)
	stage := p.Stages[nextStage]
	return *r, &stage, nil
}

// GetRun returns a run by ID.
func (m *Manager) GetRun(tenant, runID string) (Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.runs[runID]
	if !ok || r.Tenant != tenant {
		return Run{}, false
	}
	return *r, true
}

// ListRuns returns all runs for the given pipeline (or all runs for the tenant
// if pipelineID is ""), sorted by created_at ascending.
func (m *Manager) ListRuns(tenant, pipelineID string) []Run {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Run, 0)
	for _, r := range m.runs {
		if r.Tenant != tenant {
			continue
		}
		if pipelineID != "" && r.PipelineID != pipelineID {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
