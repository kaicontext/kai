package model

import (
	"encoding/json"
	"time"
)

// Workflow represents a parsed workflow definition.
type Workflow struct {
	ID          string    `json:"id"`
	RepoID      string    `json:"repo_id"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	ContentHash string    `json:"content_hash"`
	ParsedJSON  string    `json:"-"`
	Triggers    []string  `json:"triggers"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TriggersJSON returns triggers as a JSON string.
func (w *Workflow) TriggersJSON() string {
	b, _ := json.Marshal(w.Triggers)
	return string(b)
}

// ParseTriggers parses a JSON triggers string.
func ParseTriggers(s string) []string {
	var triggers []string
	json.Unmarshal([]byte(s), &triggers)
	return triggers
}

// Workflow trigger events
const (
	TriggerPush             = "push"
	TriggerReviewCreated    = "review_created"
	TriggerReviewUpdated    = "review_updated"
	TriggerWorkflowDispatch = "workflow_dispatch"
	TriggerSchedule         = "schedule"
	TriggerWorkflowCall     = "workflow_call"
)

// WorkflowRun represents an execution instance of a workflow.
type WorkflowRun struct {
	ID             string    `json:"id"`
	WorkflowID     string    `json:"workflow_id"`
	RepoID         string    `json:"repo_id"`
	RunNumber      int       `json:"run_number"`
	TriggerEvent   string    `json:"trigger_event"`
	TriggerRef     string    `json:"trigger_ref,omitempty"`
	TriggerSHA     string    `json:"trigger_sha,omitempty"`
	TriggerPayload string    `json:"-"`
	Status         string    `json:"status"`
	Conclusion     string    `json:"conclusion,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	CreatedBy      string    `json:"created_by,omitempty"`
}

// WorkflowRunWithDetails includes workflow name for display.
type WorkflowRunWithDetails struct {
	WorkflowRun
	WorkflowName string `json:"workflow_name"`
	WorkflowPath string `json:"workflow_path"`
}

// Workflow run status constants
const (
	RunStatusQueued     = "queued"
	RunStatusInProgress = "in_progress"
	RunStatusCompleted  = "completed"
)

// Workflow run conclusion constants
const (
	ConclusionSuccess   = "success"
	ConclusionFailure   = "failure"
	ConclusionCancelled = "cancelled"
	ConclusionSkipped   = "skipped"
)

// Job represents an individual job within a workflow run.
type Job struct {
	ID            string    `json:"id"`
	WorkflowRunID string    `json:"workflow_run_id"`
	Name          string    `json:"name"`
	RunsOn        []string  `json:"runs_on,omitempty"`
	RunnerID      string    `json:"runner_id,omitempty"`
	Status        string    `json:"status"`
	Conclusion    string    `json:"conclusion,omitempty"`
	MatrixValues  string    `json:"-"`
	Needs         []string  `json:"needs,omitempty"`
	RunnerName    string    `json:"runner_name,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// RunsOnJSON returns runs_on labels as a JSON string.
func (j *Job) RunsOnJSON() string {
	b, _ := json.Marshal(j.RunsOn)
	return string(b)
}

// MatrixValuesMap returns matrix values as a map.
func (j *Job) MatrixValuesMap() map[string]string {
	var m map[string]string
	json.Unmarshal([]byte(j.MatrixValues), &m)
	return m
}

// NeedsJSON returns needs as a JSON string.
func (j *Job) NeedsJSON() string {
	b, _ := json.Marshal(j.Needs)
	return string(b)
}

// ParseNeeds parses a JSON needs string.
func ParseNeeds(s string) []string {
	var needs []string
	json.Unmarshal([]byte(s), &needs)
	return needs
}

// Job status constants
const (
	JobStatusQueued     = "queued"
	JobStatusPending    = "pending" // Waiting for dependencies
	JobStatusInProgress = "in_progress"
	JobStatusCompleted  = "completed"
)

// Step represents an individual step within a job.
type Step struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// Step status constants
const (
	StepStatusPending    = "pending"
	StepStatusInProgress = "in_progress"
	StepStatusCompleted  = "completed"
)

// JobLog represents a chunk of log output from a job.
type JobLog struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	StepID    string    `json:"step_id,omitempty"`
	ChunkSeq  int       `json:"chunk_seq"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Artifact represents a file produced by a workflow run.
type Artifact struct {
	ID            string    `json:"id"`
	WorkflowRunID string    `json:"workflow_run_id"`
	JobID         string    `json:"job_id"`
	Name          string    `json:"name"`
	Path          string    `json:"-"` // Storage path, not exposed in API
	Size          int64     `json:"size"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// WorkflowCache represents a cached directory.
type WorkflowCache struct {
	ID        string    `json:"id"`
	RepoID    string    `json:"repo_id"`
	CacheKey  string    `json:"cache_key"`
	Path      string    `json:"-"` // Storage path
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// WorkflowSecret represents an encrypted secret.
type WorkflowSecret struct {
	ID        string    `json:"id"`
	RepoID    string    `json:"repo_id,omitempty"`
	OrgID     string    `json:"org_id,omitempty"`
	Name      string    `json:"name"`
	Encrypted []byte    `json:"-"` // Never expose encrypted value
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy string    `json:"created_by"`
}

// WorkflowVariable represents a non-encrypted variable for workflows (vars.* context).
type WorkflowVariable struct {
	ID        string    `json:"id"`
	RepoID    string    `json:"repo_id,omitempty"`
	OrgID     string    `json:"org_id,omitempty"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Runner represents a CI runner.
type Runner struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	OrgID      string    `json:"org_id,omitempty"`
	Labels     []string  `json:"labels"`
	Status     string    `json:"status"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// LabelsJSON returns labels as a JSON string.
func (r *Runner) LabelsJSON() string {
	b, _ := json.Marshal(r.Labels)
	return string(b)
}

// ParseLabels parses a JSON labels string.
func ParseLabels(s string) []string {
	var labels []string
	json.Unmarshal([]byte(s), &labels)
	return labels
}

// Runner status constants
const (
	RunnerStatusOnline  = "online"
	RunnerStatusOffline = "offline"
	RunnerStatusBusy    = "busy"
)

// CITriggerEvent represents an event that can trigger workflows.
type CITriggerEvent struct {
	Repo    string                 `json:"repo"`    // org/repo format
	Event   string                 `json:"event"`   // push, review_created, etc.
	Ref     string                 `json:"ref"`     // refs/heads/main
	SHA     string                 `json:"sha"`     // Commit SHA
	Payload map[string]interface{} `json:"payload"` // Event-specific data
}

// JobClaimRequest is sent by runners to claim a job.
type JobClaimRequest struct {
	RunnerID string   `json:"runner_id"`
	Labels   []string `json:"labels"`
}

// JobClaimResponse is returned when a runner claims a job.
type JobClaimResponse struct {
	Job         *Job                   `json:"job,omitempty"`
	WorkflowRun *WorkflowRun           `json:"workflow_run,omitempty"`
	Workflow    *WorkflowForRunner     `json:"workflow,omitempty"`
	Steps       []Step                 `json:"steps,omitempty"`
	Context     map[string]interface{} `json:"context,omitempty"` // Env vars, secrets
}

// WorkflowForRunner is a workflow struct that includes ParsedJSON for runner execution.
type WorkflowForRunner struct {
	ID          string    `json:"id"`
	RepoID      string    `json:"repo_id"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	ContentHash string    `json:"content_hash"`
	ParsedJSON  string    `json:"parsed_json"`
	Triggers    []string  `json:"triggers"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkflowToRunnerFormat converts a Workflow to WorkflowForRunner.
func WorkflowToRunnerFormat(w *Workflow) *WorkflowForRunner {
	if w == nil {
		return nil
	}
	return &WorkflowForRunner{
		ID:          w.ID,
		RepoID:      w.RepoID,
		Path:        w.Path,
		Name:        w.Name,
		ContentHash: w.ContentHash,
		ParsedJSON:  w.ParsedJSON,
		Triggers:    w.Triggers,
		Active:      w.Active,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}

// JobStartRequest marks a job as started.
type JobStartRequest struct {
	RunnerName string `json:"runner_name"`
}

// JobCompleteRequest marks a job as completed.
type JobCompleteRequest struct {
	Conclusion string `json:"conclusion"`
}

// StepCompleteRequest marks a step as completed.
type StepCompleteRequest struct {
	Conclusion string `json:"conclusion"`
}

// ConcurrencyLock represents an active concurrency lock.
type ConcurrencyLock struct {
	ID            string    `json:"id"`
	GroupKey      string    `json:"group_key"`
	WorkflowRunID string    `json:"workflow_run_id"`
	JobID         string    `json:"job_id,omitempty"`
	RepoID        string    `json:"repo_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// LogAppendRequest appends logs to a job.
type LogAppendRequest struct {
	StepID  string `json:"step_id,omitempty"`
	Content string `json:"content"`
}
