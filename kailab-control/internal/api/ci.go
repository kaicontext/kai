package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kailab-control/internal/db"
	"kailab-control/internal/model"
	"kailab-control/internal/workflow"
)

// qualifyShardURL converts a short Kubernetes hostname to a FQDN so that
// CI runner pods in the kailab-ci namespace can resolve services in the
// kailab namespace. e.g. "http://kailab:7447" -> "http://kailab.kailab.svc.cluster.local:7447"
func qualifyShardURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	// If the hostname already contains a dot, it's already qualified
	if strings.Contains(host, ".") {
		return rawURL
	}
	// Qualify with the kailab namespace FQDN
	port := u.Port()
	fqdn := host + ".kailab.svc.cluster.local"
	if port != "" {
		u.Host = fqdn + ":" + port
	} else {
		u.Host = fqdn
	}
	return u.String()
}

// ----- Workflow Types -----

type WorkflowResponse struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Name        string   `json:"name"`
	Triggers    []string `json:"triggers"`
	Active      bool     `json:"active"`
	ContentHash string   `json:"content_hash"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type ListWorkflowsResponse struct {
	Workflows []WorkflowResponse `json:"workflows"`
}

func workflowToResponse(w *model.Workflow) WorkflowResponse {
	return WorkflowResponse{
		ID:          w.ID,
		Path:        w.Path,
		Name:        w.Name,
		Triggers:    w.Triggers,
		Active:      w.Active,
		ContentHash: w.ContentHash,
		CreatedAt:   w.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   w.UpdatedAt.Format(time.RFC3339),
	}
}

// ListWorkflows lists all workflows for a repository.
func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())

	workflows, err := h.db.ListRepoWorkflows(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflows", err)
		return
	}

	resp := ListWorkflowsResponse{Workflows: make([]WorkflowResponse, len(workflows))}
	for i, wf := range workflows {
		resp.Workflows[i] = workflowToResponse(wf)
	}
	writeJSON(w, http.StatusOK, resp)
}

// SyncWorkflowsRequest syncs workflows from the repository.
type SyncWorkflowsRequest struct {
	Files map[string]string `json:"files"` // path -> content
}

type SyncWorkflowsResponse struct {
	Synced  []string `json:"synced"`
	Errors  []string `json:"errors"`
	Removed []string `json:"removed"`
}

// SyncWorkflows syncs workflow files from the repository.
func (h *Handler) SyncWorkflows(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	user := UserFromContext(r.Context())

	var req SyncWorkflowsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	resp := SyncWorkflowsResponse{
		Synced:  []string{},
		Errors:  []string{},
		Removed: []string{},
	}

	// Get existing workflows
	existing, err := h.db.ListRepoWorkflows(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflows", err)
		return
	}
	existingByPath := make(map[string]*model.Workflow)
	for _, wf := range existing {
		existingByPath[wf.Path] = wf
	}

	// Process each file
	processedPaths := make(map[string]bool)
	for path, content := range req.Files {
		processedPaths[path] = true

		// Parse workflow
		wf, err := workflow.Parse([]byte(content))
		if err != nil {
			resp.Errors = append(resp.Errors, path+": "+err.Error())
			continue
		}

		contentHash := workflow.ContentHash([]byte(content))
		parsedJSON, _ := wf.ToJSON()
		triggers := wf.GetTriggerTypes()

		// Check if workflow exists
		if existing, ok := existingByPath[path]; ok {
			// Update if content changed
			if existing.ContentHash != contentHash {
				if err := h.db.UpdateWorkflow(existing.ID, wf.Name, contentHash, parsedJSON, triggers, true); err != nil {
					resp.Errors = append(resp.Errors, path+": "+err.Error())
					continue
				}
			}
		} else {
			// Create new workflow
			_, err := h.db.CreateWorkflow(repo.ID, path, wf.Name, contentHash, parsedJSON, triggers)
			if err != nil {
				resp.Errors = append(resp.Errors, path+": "+err.Error())
				continue
			}
		}
		resp.Synced = append(resp.Synced, path)
	}

	// Mark workflows not in the sync as inactive (soft delete)
	for path, wf := range existingByPath {
		if !processedPaths[path] {
			if err := h.db.UpdateWorkflow(wf.ID, wf.Name, wf.ContentHash, wf.ParsedJSON, wf.Triggers, false); err == nil {
				resp.Removed = append(resp.Removed, path)
			}
		}
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "workflow.sync", "repo", repo.ID, map[string]string{
		"synced_count": strconv.Itoa(len(resp.Synced)),
	})

	writeJSON(w, http.StatusOK, resp)
}

// DiscoverWorkflows syncs workflows from the data plane snapshot.
func (h *Handler) DiscoverWorkflows(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	org := OrgFromContext(r.Context())

	if err := h.syncWorkflowsFromDataPlane(repo.ID, org.Slug, repo.Name, "refs/heads/main"); err != nil {
		log.Printf("Workflow discovery failed for %s/%s: %v", org.Slug, repo.Name, err)
	}

	workflows, err := h.db.ListRepoWorkflows(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflows", err)
		return
	}

	resp := ListWorkflowsResponse{Workflows: make([]WorkflowResponse, len(workflows))}
	for i, wf := range workflows {
		resp.Workflows[i] = workflowToResponse(wf)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ----- Workflow Runs -----

type WorkflowRunResponse struct {
	ID           string `json:"id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name,omitempty"`
	WorkflowPath string `json:"workflow_path,omitempty"`
	RunNumber    int    `json:"run_number"`
	TriggerEvent string `json:"trigger_event"`
	TriggerRef   string `json:"trigger_ref,omitempty"`
	TriggerSHA   string `json:"trigger_sha,omitempty"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by,omitempty"`
}

type ListWorkflowRunsResponse struct {
	Runs []WorkflowRunResponse `json:"runs"`
}

func workflowRunToResponse(r *model.WorkflowRunWithDetails) WorkflowRunResponse {
	resp := WorkflowRunResponse{
		ID:           r.ID,
		WorkflowID:   r.WorkflowID,
		WorkflowName: r.WorkflowName,
		WorkflowPath: r.WorkflowPath,
		RunNumber:    r.RunNumber,
		TriggerEvent: r.TriggerEvent,
		TriggerRef:   r.TriggerRef,
		TriggerSHA:   r.TriggerSHA,
		Status:       r.Status,
		Conclusion:   r.Conclusion,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		CreatedBy:    r.CreatedBy,
	}
	if !r.StartedAt.IsZero() {
		resp.StartedAt = r.StartedAt.Format(time.RFC3339)
	}
	if !r.CompletedAt.IsZero() {
		resp.CompletedAt = r.CompletedAt.Format(time.RFC3339)
	}
	return resp
}

// ListWorkflowRuns lists workflow runs for a repository.
func (h *Handler) ListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	runs, err := h.db.ListRepoWorkflowRuns(repo.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runs", err)
		return
	}

	resp := ListWorkflowRunsResponse{Runs: make([]WorkflowRunResponse, len(runs))}
	for i, run := range runs {
		resp.Runs[i] = workflowRunToResponse(run)
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetWorkflowRun gets a workflow run by ID.
func (h *Handler) GetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")

	run, err := h.db.GetWorkflowRunByID(runID)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "run not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get run", err)
		return
	}

	// Get workflow details
	wf, _ := h.db.GetWorkflowByID(run.WorkflowID)
	details := &model.WorkflowRunWithDetails{WorkflowRun: *run}
	if wf != nil {
		details.WorkflowName = wf.Name
		details.WorkflowPath = wf.Path
	}

	writeJSON(w, http.StatusOK, workflowRunToResponse(details))
}

// DispatchWorkflowRequest triggers a workflow manually.
type DispatchWorkflowRequest struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// DispatchWorkflow triggers a workflow manually.
func (h *Handler) DispatchWorkflow(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	user := UserFromContext(r.Context())
	workflowID := r.PathValue("workflow_id")

	var req DispatchWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	wf, err := h.db.GetWorkflowByID(workflowID)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "workflow not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get workflow", err)
		return
	}

	// Check workflow belongs to this repo
	if wf.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "workflow not found", nil)
		return
	}

	// Check workflow has workflow_dispatch trigger
	hasDispatch := false
	for _, t := range wf.Triggers {
		if t == "workflow_dispatch" {
			hasDispatch = true
			break
		}
	}
	if !hasDispatch {
		writeError(w, http.StatusBadRequest, "workflow does not support manual dispatch", nil)
		return
	}

	// Create payload
	payload := map[string]interface{}{
		"inputs": req.Inputs,
	}
	payloadJSON, _ := json.Marshal(payload)

	// Create workflow run
	run, err := h.db.CreateWorkflowRun(wf.ID, repo.ID, model.TriggerWorkflowDispatch, req.Ref, "", string(payloadJSON), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run", err)
		return
	}

	// Create jobs from workflow
	if err := h.createJobsFromWorkflow(wf, run); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create jobs", err)
		return
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "workflow.dispatch", "workflow_run", run.ID, map[string]string{
		"workflow_id": wf.ID,
		"ref":         req.Ref,
	})

	details := &model.WorkflowRunWithDetails{WorkflowRun: *run, WorkflowName: wf.Name, WorkflowPath: wf.Path}
	writeJSON(w, http.StatusCreated, workflowRunToResponse(details))
}

// CancelWorkflowRun cancels a running workflow.
func (h *Handler) CancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	user := UserFromContext(r.Context())

	run, err := h.db.GetWorkflowRunByID(runID)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "run not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get run", err)
		return
	}

	if run.Status == model.RunStatusCompleted {
		writeError(w, http.StatusBadRequest, "run already completed", nil)
		return
	}

	if err := h.db.CompleteWorkflowRun(runID, model.ConclusionCancelled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel run", err)
		return
	}

	// Cancel all pending/in-progress jobs
	jobs, _ := h.db.ListWorkflowRunJobs(runID)
	for _, job := range jobs {
		if job.Status != model.JobStatusCompleted {
			h.db.CompleteJob(job.ID, model.ConclusionCancelled)
		}
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "workflow.cancel", "workflow_run", runID, nil)

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// RerunWorkflowRun re-runs a workflow.
func (h *Handler) RerunWorkflowRun(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	user := UserFromContext(r.Context())
	runID := r.PathValue("run_id")

	run, err := h.db.GetWorkflowRunByID(runID)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "run not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get run", err)
		return
	}

	wf, err := h.db.GetWorkflowByID(run.WorkflowID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get workflow", err)
		return
	}

	// Create new run with same trigger info
	newRun, err := h.db.CreateWorkflowRun(wf.ID, repo.ID, run.TriggerEvent, run.TriggerRef, run.TriggerSHA, run.TriggerPayload, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run", err)
		return
	}

	// Create jobs from workflow
	if err := h.createJobsFromWorkflow(wf, newRun); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create jobs", err)
		return
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "workflow.rerun", "workflow_run", newRun.ID, map[string]string{
		"original_run_id": runID,
	})

	details := &model.WorkflowRunWithDetails{WorkflowRun: *newRun, WorkflowName: wf.Name, WorkflowPath: wf.Path}
	writeJSON(w, http.StatusCreated, workflowRunToResponse(details))
}

// ----- Jobs -----

type JobResponse struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	Conclusion   string         `json:"conclusion,omitempty"`
	RunnerName   string         `json:"runner_name,omitempty"`
	MatrixValues map[string]any `json:"matrix_values,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	StartedAt    string         `json:"started_at,omitempty"`
	CompletedAt  string         `json:"completed_at,omitempty"`
	CreatedAt    string         `json:"created_at"`
	Steps        []StepResponse `json:"steps,omitempty"`
}

type StepResponse struct {
	Number      int    `json:"number"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type ListJobsResponse struct {
	Jobs []JobResponse `json:"jobs"`
}

func jobToResponse(j *model.Job, steps []*model.Step) JobResponse {
	resp := JobResponse{
		ID:         j.ID,
		Name:       j.Name,
		Status:     j.Status,
		Conclusion: j.Conclusion,
		RunnerName: j.RunnerName,
		CreatedAt:  j.CreatedAt.Format(time.RFC3339),
	}
	if !j.StartedAt.IsZero() {
		resp.StartedAt = j.StartedAt.Format(time.RFC3339)
	}
	if !j.CompletedAt.IsZero() {
		resp.CompletedAt = j.CompletedAt.Format(time.RFC3339)
	}
	if j.MatrixValues != "" {
		json.Unmarshal([]byte(j.MatrixValues), &resp.MatrixValues)
	}

	if steps != nil {
		resp.Steps = make([]StepResponse, len(steps))
		for i, s := range steps {
			resp.Steps[i] = StepResponse{
				Number:     s.Number,
				Name:       s.Name,
				Status:     s.Status,
				Conclusion: s.Conclusion,
			}
			if !s.StartedAt.IsZero() {
				resp.Steps[i].StartedAt = s.StartedAt.Format(time.RFC3339)
			}
			if !s.CompletedAt.IsZero() {
				resp.Steps[i].CompletedAt = s.CompletedAt.Format(time.RFC3339)
			}
		}
	}

	return resp
}

// ListJobs lists jobs for a workflow run.
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")

	jobs, err := h.db.ListWorkflowRunJobs(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list jobs", err)
		return
	}

	resp := ListJobsResponse{Jobs: make([]JobResponse, len(jobs))}
	for i, job := range jobs {
		steps, _ := h.db.ListJobSteps(job.ID)
		resp.Jobs[i] = jobToResponse(job, steps)
		if job.Status == model.JobStatusCompleted {
			if summary, err := h.db.GetJobSummary(job.ID); err == nil && summary != "" {
				resp.Jobs[i].Summary = summary
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetJobLogs gets logs for a job.
func (h *Handler) GetJobLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")

	// Support streaming via ?after=N
	afterSeq := -1
	if after := r.URL.Query().Get("after"); after != "" {
		if parsed, err := strconv.Atoi(after); err == nil {
			afterSeq = parsed
		}
	}

	var logs []*model.JobLog
	var err error
	if afterSeq >= 0 {
		logs, err = h.db.GetJobLogsSince(jobID, afterSeq)
	} else {
		logs, err = h.db.GetJobLogs(jobID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get logs", err)
		return
	}

	// Return logs as JSON array
	type LogEntry struct {
		ChunkSeq  int    `json:"chunk_seq"`
		StepID    string `json:"step_id,omitempty"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	}

	entries := make([]LogEntry, len(logs))
	for i, log := range logs {
		entries[i] = LogEntry{
			ChunkSeq:  log.ChunkSeq,
			StepID:    log.StepID,
			Content:   log.Content,
			Timestamp: log.Timestamp.Format(time.RFC3339),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs": entries,
	})
}

// ----- Artifacts -----

type ArtifactResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	JobID     string `json:"job_id"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

type ListArtifactsResponse struct {
	Artifacts []ArtifactResponse `json:"artifacts"`
}

// ListArtifacts lists artifacts for a workflow run.
func (h *Handler) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")

	artifacts, err := h.db.ListWorkflowRunArtifacts(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list artifacts", err)
		return
	}

	resp := ListArtifactsResponse{Artifacts: make([]ArtifactResponse, len(artifacts))}
	for i, a := range artifacts {
		resp.Artifacts[i] = ArtifactResponse{
			ID:        a.ID,
			Name:      a.Name,
			Size:      a.Size,
			JobID:     a.JobID,
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		}
		if !a.ExpiresAt.IsZero() {
			resp.Artifacts[i].ExpiresAt = a.ExpiresAt.Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ----- Secrets -----

type SecretResponse struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"` // "repo" or "org"
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type ListSecretsResponse struct {
	Secrets []SecretResponse `json:"secrets"`
}

// ListSecrets lists secrets for a repository.
func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	org := OrgFromContext(r.Context())

	secrets, err := h.db.ListRepoSecrets(repo.ID, org.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list secrets", err)
		return
	}

	resp := ListSecretsResponse{Secrets: make([]SecretResponse, len(secrets))}
	for i, s := range secrets {
		scope := "repo"
		if s.RepoID == "" {
			scope = "org"
		}
		resp.Secrets[i] = SecretResponse{
			Name:      s.Name,
			Scope:     scope,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
			UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type SetSecretRequest struct {
	Value string `json:"value"`
}

// SetSecret creates or updates a secret.
func (h *Handler) SetSecret(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	user := UserFromContext(r.Context())
	secretName := r.PathValue("secret_name")

	var req SetSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if secretName == "" || len(req.Value) == 0 {
		writeError(w, http.StatusBadRequest, "name and value are required", nil)
		return
	}

	// TODO: Encrypt the secret value properly
	// For now, we'll store it as-is (in production, use proper encryption)
	encrypted := []byte(req.Value)

	// Check if secret exists
	existing, err := h.db.GetWorkflowSecretByName(repo.ID, "", secretName)
	if err == nil {
		// Update existing
		if err := h.db.UpdateWorkflowSecret(existing.ID, encrypted); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update secret", err)
			return
		}
	} else if err == db.ErrNotFound {
		// Create new
		_, err := h.db.CreateWorkflowSecret(repo.ID, "", secretName, encrypted, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create secret", err)
			return
		}
	} else {
		writeError(w, http.StatusInternalServerError, "failed to check secret", err)
		return
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "secret.set", "repo", repo.ID, map[string]string{
		"name": secretName,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteSecret deletes a secret.
func (h *Handler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	user := UserFromContext(r.Context())
	secretName := r.PathValue("secret_name")

	secret, err := h.db.GetWorkflowSecretByName(repo.ID, "", secretName)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "secret not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get secret", err)
		return
	}

	if err := h.db.DeleteWorkflowSecret(secret.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete secret", err)
		return
	}

	org := OrgFromContext(r.Context())
	h.db.WriteAudit(&org.ID, &user.ID, "secret.delete", "repo", repo.ID, map[string]string{
		"name": secretName,
	})

	w.WriteHeader(http.StatusNoContent)
}

// ----- Variables -----

type VariableResponse struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
	UpdatedAt string `json:"updated_at"`
}

type ListVariablesResponse struct {
	Variables []VariableResponse `json:"variables"`
}

// ListVariables lists all variables for a repo.
func (h *Handler) ListVariables(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	org := OrgFromContext(r.Context())

	vars, err := h.db.ListRepoVariables(repo.ID, org.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list variables", err)
		return
	}

	resp := ListVariablesResponse{Variables: make([]VariableResponse, len(vars))}
	for i, v := range vars {
		scope := "repo"
		if v.RepoID == "" {
			scope = "org"
		}
		resp.Variables[i] = VariableResponse{
			Name:      v.Name,
			Value:     v.Value,
			Scope:     scope,
			UpdatedAt: v.UpdatedAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type SetVariableRequest struct {
	Value string `json:"value"`
}

// SetVariable creates or updates a variable.
func (h *Handler) SetVariable(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	varName := r.PathValue("var_name")

	var req SetVariableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if varName == "" {
		writeError(w, http.StatusBadRequest, "variable name is required", nil)
		return
	}

	if err := h.db.SetVariable(repo.ID, "", varName, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set variable", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteVariable deletes a variable.
func (h *Handler) DeleteVariable(w http.ResponseWriter, r *http.Request) {
	repo := RepoFromContext(r.Context())
	varName := r.PathValue("var_name")

	v, err := h.db.GetVariableByName(repo.ID, "", varName)
	if err == db.ErrNotFound {
		writeError(w, http.StatusNotFound, "variable not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get variable", err)
		return
	}

	if err := h.db.DeleteVariable(v.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete variable", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----- Internal Endpoints (for runner and kailabd) -----

// syncWorkflowsFromDataPlane fetches workflow files from the data plane and syncs them to the database.
// This is a best-effort sync - if it fails, we continue with existing workflows.
func (h *Handler) syncWorkflowsFromDataPlane(repoID, orgSlug, repoName, ref string) error {
	// Get the shard URL for this repo - use default shard
	shardURL := h.shards.GetShardURL("default")
	if shardURL == "" {
		return nil // No shard configured, skip sync
	}

	// Try multiple approaches to get files

	// Approach 1: Try snap.main directly
	filesRef := "snap.main"
	if strings.HasPrefix(ref, "refs/heads/") {
		branchName := strings.TrimPrefix(ref, "refs/heads/")
		filesRef = "snap." + branchName
	}

	filesURL := fmt.Sprintf("%s/%s/%s/v1/files/%s", shardURL, orgSlug, repoName, filesRef)
	resp, err := http.Get(filesURL)
	if err != nil {
		return nil // Network error, skip sync
	}
	defer resp.Body.Close()

	// If that failed, try snap.latest
	if resp.StatusCode != http.StatusOK {
		latestURL := fmt.Sprintf("%s/%s/%s/v1/files/snap.latest", shardURL, orgSlug, repoName)
		latestResp, err := http.Get(latestURL)
		if err == nil && latestResp.StatusCode == http.StatusOK {
			resp.Body.Close()
			resp = latestResp
		} else {
			if latestResp != nil {
				latestResp.Body.Close()
			}
		}
	}

	// If still failed, try getting head snapshot from latest changeset
	if resp.StatusCode != http.StatusOK {
		csURL := fmt.Sprintf("%s/%s/%s/v1/changesets/latest", shardURL, orgSlug, repoName)
		csResp, err := http.Get(csURL)
		if err != nil {
			return nil // Can't get changeset, skip sync
		}
		defer csResp.Body.Close()

		if csResp.StatusCode != http.StatusOK {
			return nil // No changeset, skip sync
		}

		var csData struct {
			Head string `json:"head"`
		}
		if err := json.NewDecoder(csResp.Body).Decode(&csData); err != nil || csData.Head == "" {
			return nil // Can't parse changeset, skip sync
		}

		// Try with head snapshot
		filesURL = fmt.Sprintf("%s/%s/%s/v1/files/%s", shardURL, orgSlug, repoName, csData.Head)
		resp, err = http.Get(filesURL)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil // Still can't get files, skip sync
		}
	}

	var filesResp struct {
		Files []struct {
			Path          string `json:"path"`
			Digest        string `json:"digest"`
			ContentDigest string `json:"contentDigest"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&filesResp); err != nil {
		return fmt.Errorf("failed to decode files response: %w", err)
	}

	// Find workflow files
	workflowFiles := make(map[string]string) // path -> digest (file object digest)
	for _, f := range filesResp.Files {
		if strings.HasPrefix(f.Path, ".kailab/workflows/") && strings.HasSuffix(f.Path, ".yml") {
			workflowFiles[f.Path] = f.Digest
		}
	}

	if len(workflowFiles) == 0 {
		return nil // No workflow files
	}

	// Fetch and sync each workflow file
	for path, digest := range workflowFiles {
		// Fetch content via file object digest
		contentURL := fmt.Sprintf("%s/%s/%s/v1/content/%s", shardURL, orgSlug, repoName, digest)
		contentResp, err := http.Get(contentURL)
		if err != nil {
			log.Printf("Failed to fetch workflow content for %s: %v", path, err)
			continue
		}

		var contentObj struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(contentResp.Body).Decode(&contentObj); err != nil {
			contentResp.Body.Close()
			log.Printf("Failed to decode content response for %s: %v", path, err)
			continue
		}
		contentResp.Body.Close()

		content, err := base64.StdEncoding.DecodeString(contentObj.Content)
		if err != nil {
			log.Printf("Failed to base64 decode workflow content for %s: %v", path, err)
			continue
		}

		// Parse and sync workflow
		hash := sha256.Sum256(content)
		contentHash := hex.EncodeToString(hash[:])

		// Check if workflow exists and has changed
		existing, dbErr := h.db.GetWorkflowByRepoAndPath(repoID, path)
		if dbErr == nil && existing.ContentHash == contentHash {
			continue // No change
		}

		// Parse workflow
		parsed, err := workflow.Parse(content)
		if err != nil {
			log.Printf("Failed to parse workflow %s: %v", path, err)
			continue
		}

		parsedJSON, err := parsed.ToJSON()
		if err != nil {
			log.Printf("Failed to serialize workflow %s: %v", path, err)
			continue
		}

		triggers := parsed.GetTriggerTypes()

		if dbErr == db.ErrNotFound {
			// Create new workflow
			_, err = h.db.CreateWorkflow(repoID, path, parsed.Name, contentHash, parsedJSON, triggers)
			if err != nil {
				log.Printf("Failed to create workflow %s: %v", path, err)
			} else {
				log.Printf("Created workflow %s for %s/%s", path, orgSlug, repoName)
			}
		} else if dbErr == nil {
			// Update existing workflow
			err = h.db.UpdateWorkflow(existing.ID, parsed.Name, contentHash, parsedJSON, triggers, true)
			if err != nil {
				log.Printf("Failed to update workflow %s: %v", path, err)
			} else {
				log.Printf("Updated workflow %s for %s/%s", path, orgSlug, repoName)
			}
		}
	}

	return nil
}

// TriggerCIRequest is sent by kailabd to trigger CI workflows.
type TriggerCIRequest struct {
	Repo    string                 `json:"repo"`    // org/repo format
	Event   string                 `json:"event"`   // push, review_created, etc.
	Ref     string                 `json:"ref"`     // refs/heads/main
	SHA     string                 `json:"sha"`     // Commit SHA
	Payload map[string]interface{} `json:"payload"` // Event-specific data
}

type TriggerCIResponse struct {
	Runs []string `json:"runs"` // Created run IDs
}

// TriggerCI handles CI trigger requests from kailabd.
func (h *Handler) TriggerCI(w http.ResponseWriter, r *http.Request) {
	var req TriggerCIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Parse org/repo
	parts := strings.SplitN(req.Repo, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "invalid repo format", nil)
		return
	}
	orgSlug, repoName := parts[0], parts[1]

	// Get org and repo
	org, err := h.db.GetOrgBySlug(orgSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "org not found", nil)
		return
	}

	repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found", nil)
		return
	}

	// Sync workflows from data plane (auto-sync on trigger)
	if err := h.syncWorkflowsFromDataPlane(repo.ID, orgSlug, repoName, req.Ref); err != nil {
		log.Printf("Warning: failed to sync workflows for %s/%s: %v", orgSlug, repoName, err)
		// Continue anyway - there might be existing workflows in the database
	}

	// Find matching workflows
	var triggerType string
	switch req.Event {
	case "push":
		triggerType = "push"
	case "review_created":
		triggerType = "review"
	case "review_updated":
		triggerType = "review"
	case "schedule":
		triggerType = "schedule"
	case "workflow_call":
		triggerType = "workflow_call"
	default:
		writeError(w, http.StatusBadRequest, "unsupported event type", nil)
		return
	}

	workflows, err := h.db.ListActiveWorkflowsByTrigger(repo.ID, triggerType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflows", err)
		return
	}

	// Filter workflows by trigger rules
	payloadJSON, _ := json.Marshal(req.Payload)
	triggerEvent := &workflow.TriggerEvent{
		Type:    req.Event,
		Ref:     req.Ref,
		SHA:     req.SHA,
		Payload: req.Payload,
	}

	var runIDs []string
	for _, wf := range workflows {
		// Parse workflow to check trigger rules
		parsedWF, err := workflow.FromJSON(wf.ParsedJSON)
		if err != nil {
			continue
		}

		if !parsedWF.MatchTrigger(triggerEvent) {
			continue
		}

		// Check concurrency controls
		var concurrencyGroup string
		if parsedWF.Concurrency != nil && parsedWF.Concurrency.Group != "" {
			ec := workflow.NewExprContext()
			ec.GitHub["repository"] = orgSlug + "/" + repoName
			ec.GitHub["ref"] = req.Ref
			ec.GitHub["ref_name"] = strings.TrimPrefix(strings.TrimPrefix(req.Ref, "refs/heads/"), "refs/tags/")
			ec.GitHub["sha"] = req.SHA
			ec.GitHub["event_name"] = req.Event
			ec.GitHub["workflow"] = wf.Name
			concurrencyGroup = workflow.Interpolate(parsedWF.Concurrency.Group, ec)

			existing, err := h.db.GetConcurrencyLock(concurrencyGroup)
			if err == nil && existing != nil {
				// Another run holds the lock
				if parsedWF.Concurrency.CancelInProgress {
					// Cancel the in-progress run and take the lock
					h.db.CompleteWorkflowRun(existing.WorkflowRunID, model.ConclusionCancelled)
					h.db.ReleaseConcurrencyLock(existing.WorkflowRunID)
				} else {
					// Skip this trigger - group is busy
					continue
				}
			}
		}

		// Create workflow run
		run, err := h.db.CreateWorkflowRun(wf.ID, repo.ID, req.Event, req.Ref, req.SHA, string(payloadJSON), "")
		if err != nil {
			continue
		}

		// Acquire concurrency lock with the run ID
		if concurrencyGroup != "" {
			h.db.AcquireConcurrencyLock(concurrencyGroup, run.ID, repo.ID, "")
		}

		// Create jobs from workflow
		if err := h.createJobsFromWorkflow(wf, run); err != nil {
			continue
		}

		runIDs = append(runIDs, run.ID)
	}

	writeJSON(w, http.StatusOK, TriggerCIResponse{Runs: runIDs})
}

// ClaimJobRequest is sent by runners to claim a job.
type ClaimJobRequest struct {
	RunnerID string   `json:"runner_id"`
	Labels   []string `json:"labels"`
}

// ClaimJob allows a runner to claim the next available job.
func (h *Handler) ClaimJob(w http.ResponseWriter, r *http.Request) {
	var req ClaimJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	runnerID := req.RunnerID

	// Update runner status
	h.db.UpdateRunnerStatus(runnerID, model.RunnerStatusOnline)

	// Try to claim a job
	job, err := h.db.ClaimJob(runnerID, req.Labels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to claim job", err)
		return
	}

	if job == nil {
		// No jobs available
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"job": nil,
		})
		return
	}

	// Get full context for the job
	run, _ := h.db.GetWorkflowRunByID(job.WorkflowRunID)
	wf, _ := h.db.GetWorkflowByID(run.WorkflowID)
	steps, _ := h.db.ListJobSteps(job.ID)

	// Get secrets
	repo, _ := h.db.GetRepoByID(run.RepoID)
	org, _ := h.db.GetOrgByID(repo.OrgID)
	secrets, _ := h.db.ListRepoSecrets(repo.ID, org.ID)

	// Build clone URL from shard (use FQDN for cross-namespace access from CI runners)
	shardURL := qualifyShardURL(h.cfg.Shards["default"])
	if repo.ShardHint != "" {
		if u, ok := h.cfg.Shards[repo.ShardHint]; ok {
			shardURL = qualifyShardURL(u)
		}
	}
	cloneURL := fmt.Sprintf("%s/%s/%s", shardURL, org.Slug, repo.Name)

	// Build context
	context := map[string]interface{}{
		"repo":          org.Slug + "/" + repo.Name,
		"ref":           run.TriggerRef,
		"sha":           run.TriggerSHA,
		"event":         run.TriggerEvent,
		"run_id":        run.ID,
		"clone_url":     cloneURL,
		"actor":         run.CreatedBy,
		"workflow_name": wf.Name,
		"run_number":    run.RunNumber,
		"job_name":      job.Name,
		"server_url":    h.cfg.BaseURL,
	}

	// Add secrets (decrypted - TODO: proper decryption)
	secretsMap := make(map[string]string)
	for _, s := range secrets {
		secretsMap[s.Name] = string(s.Encrypted)
	}
	context["secrets"] = secretsMap

	// Add variables (vars.* context)
	vars, _ := h.db.ListRepoVariables(repo.ID, org.ID)
	if len(vars) > 0 {
		varsMap := make(map[string]string)
		for _, v := range vars {
			varsMap[v.Name] = v.Value
		}
		context["vars"] = varsMap
	}

	// For reusable workflow jobs (name contains "/"), extract inputs from the
	// caller job's env vars (INPUT_*) by parsing the workflow definition.
	if strings.Contains(job.Name, "/") {
		parsedWF, parseErr := workflow.FromJSON(wf.ParsedJSON)
		if parseErr == nil {
			// Find the job definition and extract INPUT_ env vars as inputs
			for _, jobDef := range parsedWF.Jobs {
				displayName := jobDef.Name
				if displayName == "" {
					// Jobs are keyed, not named — we match by checking all jobs
					continue
				}
				if displayName == job.Name {
					inputsMap := make(map[string]string)
					for k, v := range jobDef.Env {
						if strings.HasPrefix(k, "INPUT_") {
							inputName := strings.ToLower(strings.TrimPrefix(k, "INPUT_"))
							inputsMap[inputName] = v
						}
					}
					if len(inputsMap) > 0 {
						context["inputs"] = inputsMap
					}
					break
				}
			}
		}
	}

	// Add dependency job outputs (needs.*)
	if len(job.Needs) > 0 {
		allJobs, _ := h.db.ListWorkflowRunJobs(run.ID)
		needsMap := make(map[string]interface{})
		for _, depName := range job.Needs {
			for _, depJob := range allJobs {
				if depJob.Name == depName && depJob.Status == model.JobStatusCompleted {
					outputs, _ := h.db.GetJobOutputs(depJob.ID)
					if outputs == nil {
						outputs = make(map[string]string)
					}
					needsMap[depName] = map[string]interface{}{
						"outputs": outputs,
						"result":  depJob.Conclusion,
					}
					break
				}
			}
		}
		context["needs"] = needsMap
	}

	writeJSON(w, http.StatusOK, model.JobClaimResponse{
		Job:         job,
		WorkflowRun: run,
		Workflow:    model.WorkflowToRunnerFormat(wf),
		Steps:       stepsToModels(steps),
		Context:     context,
	})
}

func stepsToModels(steps []*model.Step) []model.Step {
	result := make([]model.Step, len(steps))
	for i, s := range steps {
		result[i] = *s
	}
	return result
}

// StartJobRequest marks a job as started.
type StartJobRequest struct {
	JobID      string `json:"job_id"`
	RunnerName string `json:"runner_name"`
}

// StartJob marks a job as started by a runner.
func (h *Handler) StartJob(w http.ResponseWriter, r *http.Request) {
	var req StartJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	jobID := req.JobID

	if err := h.db.StartJob(jobID, req.RunnerName); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start job", err)
		return
	}

	// Also mark the workflow run as in progress if it's queued
	job, _ := h.db.GetJobByID(jobID)
	if job != nil {
		run, _ := h.db.GetWorkflowRunByID(job.WorkflowRunID)
		if run != nil && run.Status == model.RunStatusQueued {
			h.db.UpdateWorkflowRunStatus(run.ID, model.RunStatusInProgress)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// AppendLogsRequest appends logs to a job.
type AppendLogsRequest struct {
	JobID   string `json:"job_id"`
	StepID  string `json:"step_id,omitempty"`
	Content string `json:"content"`
}

// AppendLogs appends logs to a job.
func (h *Handler) AppendLogs(w http.ResponseWriter, r *http.Request) {
	var req AppendLogsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	jobID := req.JobID

	if err := h.db.AppendJobLog(jobID, req.StepID, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to append logs", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CompleteStepRequest marks a step as completed.
type CompleteStepRequest struct {
	JobID      string `json:"job_id"`
	StepNumber int    `json:"step_number"`
	Conclusion string `json:"conclusion"`
}

// CompleteStep marks a step as completed.
func (h *Handler) CompleteStep(w http.ResponseWriter, r *http.Request) {
	var req CompleteStepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	jobID := req.JobID
	stepNumber := req.StepNumber

	step, err := h.db.GetStepByJobAndNumber(jobID, stepNumber)
	if err != nil {
		writeError(w, http.StatusNotFound, "step not found", nil)
		return
	}

	if err := h.db.CompleteStep(step.ID, req.Conclusion); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete step", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CompleteJobRequest marks a job as completed.
type CompleteJobRequest struct {
	JobID      string            `json:"job_id"`
	Conclusion string            `json:"conclusion"`
	Outputs    map[string]string `json:"outputs,omitempty"`
	Summary    string            `json:"summary,omitempty"`
}

// CompleteJob marks a job as completed by a runner.
func (h *Handler) CompleteJob(w http.ResponseWriter, r *http.Request) {
	var req CompleteJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	jobID := req.JobID

	if err := h.db.CompleteJob(jobID, req.Conclusion); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete job", err)
		return
	}

	// Store job outputs if provided
	if len(req.Outputs) > 0 {
		h.db.SetJobOutputs(jobID, req.Outputs)
	}

	// Store step summary if provided
	if req.Summary != "" {
		h.db.SetJobSummary(jobID, req.Summary)
	}

	// Check if all jobs are complete and update workflow run
	job, _ := h.db.GetJobByID(jobID)
	if job != nil {
		h.checkAndCompleteWorkflowRun(job.WorkflowRunID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ----- Helper Functions -----

// createJobsFromWorkflow creates jobs and steps from a workflow definition.
func (h *Handler) createJobsFromWorkflow(wf *model.Workflow, run *model.WorkflowRun) error {
	parsedWF, err := workflow.FromJSON(wf.ParsedJSON)
	if err != nil {
		return err
	}

	// Resolve reusable workflow calls — replace job-level `uses:` with inlined jobs
	if err := h.resolveReusableWorkflows(wf.RepoID, parsedWF); err != nil {
		log.Printf("Warning: failed to resolve reusable workflows: %v", err)
		// Continue with what we have
	}

	// Expand jobs with matrix
	expandedJobs, err := workflow.ExpandJobsWithMatrix(parsedWF)
	if err != nil {
		return err
	}

	// Get job order
	jobOrder, err := workflow.GetJobOrder(parsedWF)
	if err != nil {
		return err
	}

	// Build a map of job key → list of expanded display names.
	// The "needs" field in YAML uses job keys (e.g., "build"), but the jobs
	// table stores display names (e.g., "Build (x86_64-unknown-linux-gnu)").
	// We must convert needs to display names so the ClaimJob dependency
	// check can match against the name column.
	keyToNames := make(map[string][]string)
	for _, jobKey := range jobOrder {
		for _, ej := range expandedJobs[jobKey] {
			keyToNames[jobKey] = append(keyToNames[jobKey], ej.Name)
		}
	}

	// Create jobs in order
	for _, jobKey := range jobOrder {
		jobs := expandedJobs[jobKey]
		for _, ej := range jobs {
			// Create job
			matrixJSON := ""
			if len(ej.MatrixValues) > 0 {
				b, _ := json.Marshal(ej.MatrixValues)
				matrixJSON = string(b)
			}

			// Expand needs from job keys to display names
			var expandedNeeds []string
			for _, need := range ej.Job.Needs {
				if names, ok := keyToNames[need]; ok {
					expandedNeeds = append(expandedNeeds, names...)
				}
			}

			job, err := h.db.CreateJob(run.ID, ej.Name, expandedNeeds, matrixJSON, []string(ej.Job.RunsOn))
			if err != nil {
				return err
			}

			// Create steps
			for i, step := range ej.Job.Steps {
				name := step.GetDisplayName(i)
				_, err := h.db.CreateStep(job.ID, i, name)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// resolveReusableWorkflows resolves job-level `uses:` references to reusable workflows.
// It replaces each reusable-workflow job with the inlined jobs from the called workflow,
// prefixed with the caller job name and wired with proper dependencies.
func (h *Handler) resolveReusableWorkflows(repoID string, parsedWF *workflow.Workflow) error {
	// Collect jobs that reference reusable workflows
	type reusableRef struct {
		jobKey  string
		job     workflow.Job
		usesRef string
	}
	var refs []reusableRef
	for key, job := range parsedWF.Jobs {
		if job.IsReusableWorkflowCall() {
			refs = append(refs, reusableRef{jobKey: key, job: job, usesRef: job.Uses})
		}
	}

	if len(refs) == 0 {
		return nil
	}

	for _, ref := range refs {
		// Resolve the workflow path. Supports:
		//   ./.kailab/workflows/foo.yml  (local reference)
		//   .kailab/workflows/foo.yml    (local reference without ./)
		usesPath := ref.usesRef
		usesPath = strings.TrimPrefix(usesPath, "./")

		// Strip @ref suffix if present (e.g., .kailab/workflows/foo.yml@main)
		if idx := strings.LastIndex(usesPath, "@"); idx > 0 {
			usesPath = usesPath[:idx]
		}

		// Look up the referenced workflow in the same repo
		calledWF, err := h.db.GetWorkflowByRepoAndPath(repoID, usesPath)
		if err != nil {
			log.Printf("reusable workflow: could not resolve %q for repo %s: %v", usesPath, repoID, err)
			continue
		}

		calledParsed, err := workflow.FromJSON(calledWF.ParsedJSON)
		if err != nil {
			log.Printf("reusable workflow: could not parse %q: %v", usesPath, err)
			continue
		}

		// Verify the called workflow supports workflow_call
		if calledParsed.On.WorkflowCall == nil {
			log.Printf("reusable workflow: %q does not have workflow_call trigger", usesPath)
			continue
		}

		// Delete the placeholder job
		delete(parsedWF.Jobs, ref.jobKey)

		// Inline the called workflow's jobs, prefixed with the caller job key
		for calledJobKey, calledJob := range calledParsed.Jobs {
			inlinedKey := ref.jobKey + "/" + calledJobKey

			// Wire dependencies: the inlined job's needs become prefixed too,
			// plus any original caller needs apply to root-level inlined jobs.
			var inlinedNeeds []string
			if len(calledJob.Needs) > 0 {
				for _, n := range calledJob.Needs {
					inlinedNeeds = append(inlinedNeeds, ref.jobKey+"/"+n)
				}
			} else {
				// Root jobs in the called workflow inherit the caller's needs
				inlinedNeeds = []string(ref.job.Needs)
			}
			calledJob.Needs = workflow.StringOrSlice(inlinedNeeds)

			// Merge caller inputs (with:) into the inlined job's env
			if len(ref.job.With) > 0 {
				if calledJob.Env == nil {
					calledJob.Env = make(map[string]string)
				}
				for k, v := range ref.job.With {
					// Inputs are available as `inputs.<name>` in expressions,
					// but also set as env vars for convenience
					calledJob.Env["INPUT_"+strings.ToUpper(k)] = v
				}
			}

			parsedWF.Jobs[inlinedKey] = calledJob
		}

		log.Printf("reusable workflow: inlined %d jobs from %q as %s/*", len(calledParsed.Jobs), usesPath, ref.jobKey)
	}

	return nil
}

// checkAndCompleteWorkflowRun checks if all jobs are complete and updates the run.
func (h *Handler) checkAndCompleteWorkflowRun(runID string) {
	jobs, err := h.db.ListWorkflowRunJobs(runID)
	if err != nil {
		return
	}

	allComplete := true
	anyFailed := false
	anyCancelled := false

	for _, job := range jobs {
		if job.Status != model.JobStatusCompleted {
			allComplete = false
			break
		}
		if job.Conclusion == model.ConclusionFailure {
			anyFailed = true
		}
		if job.Conclusion == model.ConclusionCancelled {
			anyCancelled = true
		}
	}

	if !allComplete {
		return
	}

	var conclusion string
	if anyFailed {
		conclusion = model.ConclusionFailure
	} else if anyCancelled {
		conclusion = model.ConclusionCancelled
	} else {
		conclusion = model.ConclusionSuccess
	}

	h.db.CompleteWorkflowRun(runID, conclusion)

	// Release any concurrency locks held by this run
	h.db.ReleaseConcurrencyLock(runID)

	// Send pipeline notification
	h.sendPipelineNotification(runID, conclusion)
}

// sendPipelineNotification sends an email notification for completed pipelines.
func (h *Handler) sendPipelineNotification(runID, conclusion string) {
	if h.email == nil {
		return
	}

	// Get the workflow run
	run, err := h.db.GetWorkflowRunByID(runID)
	if err != nil || run == nil {
		log.Printf("notify: failed to get workflow run %s: %v", runID, err)
		return
	}

	// Get the workflow
	wf, err := h.db.GetWorkflowByID(run.WorkflowID)
	if err != nil || wf == nil {
		log.Printf("notify: failed to get workflow %s: %v", run.WorkflowID, err)
		return
	}

	// Get the repo
	repo, err := h.db.GetRepoByID(run.RepoID)
	if err != nil || repo == nil {
		log.Printf("notify: failed to get repo %s: %v", run.RepoID, err)
		return
	}

	// Get the org
	org, err := h.db.GetOrgByID(repo.OrgID)
	if err != nil || org == nil {
		log.Printf("notify: failed to get org %s: %v", repo.OrgID, err)
		return
	}

	// Get the author
	if run.CreatedBy == "" {
		return
	}

	user, err := h.db.GetUserByID(run.CreatedBy)
	if err != nil {
		user, err = h.db.GetUserByEmail(run.CreatedBy)
	}
	if err != nil || user == nil {
		return
	}

	// Build run URL
	runURL := h.cfg.BaseURL + "/orgs/" + org.Slug + "/" + repo.Name + "/workflows/runs/" + runID

	// Send the notification
	err = h.email.SendPipelineResult(
		user.Email,
		org.Slug,
		repo.Name,
		wf.Name,
		conclusion,
		runURL,
		run.TriggerRef,
		run.TriggerSHA,
	)
	if err != nil {
		log.Printf("notify: failed to send pipeline email to %s: %v", user.Email, err)
	} else {
		log.Printf("notify: sent pipeline %s notification to %s for %s/%s", conclusion, user.Email, org.Slug, repo.Name)
	}
}

// BootstrapWorkflowRequest creates a test workflow for a repo.
type BootstrapWorkflowRequest struct {
	Repo string `json:"repo"` // org/repo format
}

// BootstrapWorkflow creates a test workflow for CI testing purposes.
// This is an internal endpoint for testing - it creates a simple CI workflow.
func (h *Handler) BootstrapWorkflow(w http.ResponseWriter, r *http.Request) {
	var req BootstrapWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Parse org/repo
	parts := strings.SplitN(req.Repo, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "invalid repo format", nil)
		return
	}
	orgSlug, repoName := parts[0], parts[1]

	// Get org and repo
	org, err := h.db.GetOrgBySlug(orgSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "org not found", err)
		return
	}

	repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
	if err != nil {
		writeError(w, http.StatusNotFound, "repo not found", err)
		return
	}

	// Check if workflow already exists
	existing, err := h.db.GetWorkflowByRepoAndPath(repo.ID, ".kailab/workflows/ci.yml")
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message": "workflow already exists",
			"id":      existing.ID,
		})
		return
	}

	// Create a simple test workflow
	testWorkflowContent := `name: CI
on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: Echo
        run: echo "Hello from Kailab CI!"
      - name: List files
        run: ls -la
`
	// Parse workflow
	parsed, err := workflow.Parse([]byte(testWorkflowContent))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse workflow", err)
		return
	}

	parsedJSON, err := parsed.ToJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize workflow", err)
		return
	}

	hash := sha256.Sum256([]byte(testWorkflowContent))
	contentHash := hex.EncodeToString(hash[:])
	triggers := parsed.GetTriggerTypes()

	// Create workflow
	wf, err := h.db.CreateWorkflow(repo.ID, ".kailab/workflows/ci.yml", parsed.Name, contentHash, parsedJSON, triggers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow", err)
		return
	}

	log.Printf("Created bootstrap workflow %s for %s/%s", wf.ID, orgSlug, repoName)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "workflow created",
		"id":      wf.ID,
	})
}
