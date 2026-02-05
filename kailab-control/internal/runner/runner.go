// Package runner implements the CI runner that executes jobs in Kubernetes.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"kailab-control/internal/model"
)

// Config holds runner configuration.
type Config struct {
	ControlPlaneURL string
	RunnerName      string
	RunnerID        string
	Namespace       string
	PollInterval    time.Duration
	Labels          []string
	Kubeconfig      string
}

// Runner executes CI jobs.
type Runner struct {
	cfg      *Config
	client   *http.Client
	executor *Executor
}

// New creates a new runner.
func New(cfg *Config) (*Runner, error) {
	executor, err := NewExecutor(cfg.Namespace, cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	return &Runner{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		executor: executor,
	}, nil
}

// Run starts the runner's main loop.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.poll(ctx); err != nil {
				log.Printf("Poll error: %v", err)
			}
		}
	}
}

// poll checks for available jobs and executes one if found.
func (r *Runner) poll(ctx context.Context) error {
	// Claim a job
	claim, err := r.claimJob(ctx)
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}

	if claim.Job == nil {
		// No jobs available
		return nil
	}

	log.Printf("Claimed job %s: %s", claim.Job.ID, claim.Job.Name)

	// Execute the job
	if err := r.executeJob(ctx, claim); err != nil {
		log.Printf("Job %s failed: %v", claim.Job.ID, err)
		// Mark job as failed
		r.completeJob(ctx, claim.Job.ID, model.ConclusionFailure)
		return nil
	}

	log.Printf("Job %s completed successfully", claim.Job.ID)
	return nil
}

// claimJob attempts to claim a job from the control plane.
func (r *Runner) claimJob(ctx context.Context) (*model.JobClaimResponse, error) {
	reqBody := map[string]interface{}{
		"labels": r.cfg.Labels,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/internal/runners/%s/jobs/claim", r.cfg.ControlPlaneURL, r.cfg.RunnerID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("claim failed: %s - %s", resp.Status, string(body))
	}

	var claim model.JobClaimResponse
	if err := json.NewDecoder(resp.Body).Decode(&claim); err != nil {
		return nil, err
	}

	return &claim, nil
}

// executeJob runs a job to completion.
func (r *Runner) executeJob(ctx context.Context, claim *model.JobClaimResponse) error {
	job := claim.Job

	// Mark job as started
	if err := r.startJob(ctx, job.ID); err != nil {
		return fmt.Errorf("start job: %w", err)
	}

	// Parse workflow to get step definitions
	parsedWF, err := parseWorkflowJSON(claim.Workflow.ParsedJSON)
	if err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}

	// Find the job definition
	var jobDef *JobDefinition
	for _, jd := range parsedWF.Jobs {
		if jd.Name == job.Name || getJobDisplayName(&jd, "") == job.Name {
			jobDef = &jd
			break
		}
	}

	// If we couldn't match by display name, try to match by key
	if jobDef == nil {
		// The job.Name might include matrix suffix, try to find the base job
		for key, jd := range parsedWF.Jobs {
			if job.Name == key || containsPrefix(job.Name, key) {
				jdCopy := jd
				jobDef = &jdCopy
				break
			}
		}
	}

	if jobDef == nil {
		return fmt.Errorf("job definition not found")
	}

	// Execute steps
	allSuccess := true
	for i, step := range claim.Steps {
		// Start step
		r.appendLogs(ctx, job.ID, step.ID, fmt.Sprintf("=== Step %d: %s ===\n", i+1, step.Name))

		// Get step definition
		var stepDef *StepDefinition
		if i < len(jobDef.Steps) {
			stepDef = &jobDef.Steps[i]
		}

		// Execute step
		conclusion, err := r.executeStep(ctx, job, &step, stepDef, claim.Context)
		if err != nil {
			r.appendLogs(ctx, job.ID, step.ID, fmt.Sprintf("Error: %v\n", err))
			conclusion = model.ConclusionFailure
		}

		// Complete step
		r.completeStep(ctx, job.ID, i, conclusion)

		if conclusion == model.ConclusionFailure {
			allSuccess = false
			// Check if we should continue on error
			if stepDef != nil && !stepDef.ContinueOnError {
				break
			}
		}

		r.appendLogs(ctx, job.ID, step.ID, fmt.Sprintf("Step completed: %s\n\n", conclusion))
	}

	// Complete job
	conclusion := model.ConclusionSuccess
	if !allSuccess {
		conclusion = model.ConclusionFailure
	}
	return r.completeJob(ctx, job.ID, conclusion)
}

// executeStep runs a single step.
func (r *Runner) executeStep(ctx context.Context, job *model.Job, step *model.Step, stepDef *StepDefinition, jobContext map[string]interface{}) (string, error) {
	if stepDef == nil {
		return model.ConclusionSkipped, nil
	}

	// Build execution request
	execReq := &ExecutionRequest{
		JobID:     job.ID,
		StepID:    step.ID,
		StepDef:   stepDef,
		Context:   jobContext,
		LogWriter: &logWriter{runner: r, ctx: ctx, jobID: job.ID, stepID: step.ID},
	}

	// Execute in Kubernetes
	result, err := r.executor.Execute(ctx, execReq)
	if err != nil {
		return model.ConclusionFailure, err
	}

	if result.ExitCode != 0 {
		return model.ConclusionFailure, nil
	}

	return model.ConclusionSuccess, nil
}

// startJob marks a job as started.
func (r *Runner) startJob(ctx context.Context, jobID string) error {
	reqBody := map[string]string{
		"runner_name": r.cfg.RunnerName,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/internal/jobs/%s/start", r.cfg.ControlPlaneURL, jobID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("start job failed: %s", resp.Status)
	}

	return nil
}

// appendLogs sends logs to the control plane.
func (r *Runner) appendLogs(ctx context.Context, jobID, stepID, content string) error {
	reqBody := map[string]string{
		"step_id": stepID,
		"content": content,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/internal/jobs/%s/logs", r.cfg.ControlPlaneURL, jobID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// completeStep marks a step as completed.
func (r *Runner) completeStep(ctx context.Context, jobID string, stepNumber int, conclusion string) error {
	reqBody := map[string]string{
		"conclusion": conclusion,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/internal/jobs/%s/steps/%d/complete", r.cfg.ControlPlaneURL, jobID, stepNumber)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// completeJob marks a job as completed.
func (r *Runner) completeJob(ctx context.Context, jobID, conclusion string) error {
	reqBody := map[string]string{
		"conclusion": conclusion,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/internal/jobs/%s/complete", r.cfg.ControlPlaneURL, jobID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// logWriter implements io.Writer that sends logs to the control plane.
type logWriter struct {
	runner *Runner
	ctx    context.Context
	jobID  string
	stepID string
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.runner.appendLogs(w.ctx, w.jobID, w.stepID, string(p))
	return len(p), nil
}

// Helper types for parsing workflow JSON

type ParsedWorkflow struct {
	Name string                   `json:"name"`
	Jobs map[string]JobDefinition `json:"jobs"`
}

type JobDefinition struct {
	Name  string           `json:"name"`
	Steps []StepDefinition `json:"steps"`
}

type StepDefinition struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Uses            string            `json:"uses"`
	Run             string            `json:"run"`
	Shell           string            `json:"shell"`
	With            map[string]string `json:"with"`
	Env             map[string]string `json:"env"`
	If              string            `json:"if"`
	ContinueOnError bool              `json:"continue_on_error"`
	TimeoutMinutes  int               `json:"timeout_minutes"`
	WorkingDir      string            `json:"working_directory"`
}

func parseWorkflowJSON(s string) (*ParsedWorkflow, error) {
	var wf ParsedWorkflow
	if err := json.Unmarshal([]byte(s), &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func getJobDisplayName(j *JobDefinition, key string) string {
	if j.Name != "" {
		return j.Name
	}
	return key
}

func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
