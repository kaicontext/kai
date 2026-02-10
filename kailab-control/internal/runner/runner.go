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
	"strings"
	"time"

	"kailab-control/internal/model"
	"kailab-control/internal/store"
	"kailab-control/internal/workflow"
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
	StorePath       string // Local store path for caches/artifacts (default: /tmp/kailab-ci-store)
}

// Runner executes CI jobs.
type Runner struct {
	cfg      *Config
	client   *http.Client
	executor *Executor
}

// New creates a new runner.
func New(cfg *Config) (*Runner, error) {
	storePath := cfg.StorePath
	if storePath == "" {
		storePath = "/tmp/kailab-ci-store"
	}
	ciStore, err := store.NewLocalStore(storePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create ci store: %w", err)
	}

	executor, err := NewExecutor(cfg.Namespace, cfg.Kubeconfig, ciStore)
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
		"runner_id": r.cfg.RunnerID,
		"labels":    r.cfg.Labels,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/-/ci/runners/claim", r.cfg.ControlPlaneURL)
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

// buildExprContext creates an expression context from the job claim.
func buildExprContext(claim *model.JobClaimResponse) *workflow.ExprContext {
	ec := workflow.NewExprContext()

	// Populate github.* context
	if repo, ok := claim.Context["repo"].(string); ok {
		ec.GitHub["repository"] = repo
	}
	if ref, ok := claim.Context["ref"].(string); ok {
		ec.GitHub["ref"] = ref
		// Derive ref_name
		name := ref
		name = strings.TrimPrefix(name, "refs/heads/")
		name = strings.TrimPrefix(name, "refs/tags/")
		ec.GitHub["ref_name"] = name
	}
	if sha, ok := claim.Context["sha"].(string); ok {
		ec.GitHub["sha"] = sha
	}
	if event, ok := claim.Context["event"].(string); ok {
		ec.GitHub["event_name"] = event
	}
	if runID, ok := claim.Context["run_id"].(string); ok {
		ec.GitHub["run_id"] = runID
	}
	// Nested event data (for workflow_dispatch inputs, etc.)
	if eventData, ok := claim.Context["event_data"].(map[string]interface{}); ok {
		ec.GitHub["event"] = eventData
	}

	// Populate secrets
	if secrets, ok := claim.Context["secrets"].(map[string]string); ok {
		ec.Secrets = secrets
	}

	// Populate matrix values
	if matrixJSON, ok := claim.Context["matrix"].(string); ok && matrixJSON != "" {
		var matrix map[string]interface{}
		if err := json.Unmarshal([]byte(matrixJSON), &matrix); err == nil {
			ec.Matrix = matrix
		}
	}
	if matrix, ok := claim.Context["matrix"].(map[string]interface{}); ok {
		ec.Matrix = matrix
	}

	// Populate runner context
	ec.Runner["os"] = "Linux"
	ec.Runner["arch"] = "X64"
	ec.Runner["name"] = "kailab-runner"

	// Populate inputs (workflow_dispatch)
	if inputs, ok := claim.Context["inputs"].(map[string]string); ok {
		ec.Inputs = inputs
	}

	// Default job status to success
	ec.GitHub["job_status"] = "success"

	return ec
}

// interpolateStep applies expression interpolation to a step definition.
func interpolateStep(step *StepDefinition, ec *workflow.ExprContext) {
	step.Run = workflow.Interpolate(step.Run, ec)
	step.Uses = workflow.Interpolate(step.Uses, ec)
	step.Name = workflow.Interpolate(step.Name, ec)
	step.Shell = workflow.Interpolate(step.Shell, ec)
	step.WorkingDir = workflow.Interpolate(step.WorkingDir, ec)
	step.With = workflow.InterpolateMap(step.With, ec)
	step.Env = workflow.InterpolateMap(step.Env, ec)
}

// executeJob runs a job to completion using one pod for all steps.
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

	// Build expression context from claim data
	exprCtx := buildExprContext(claim)

	// If job specifies a container image, pass it to the pod
	if jobDef.Container != nil && jobDef.Container.Image != "" {
		claim.Context["image"] = workflow.Interpolate(jobDef.Container.Image, exprCtx)
	}

	// Pass runs-on to the pod for image selection
	if len(jobDef.RunsOn) > 0 {
		claim.Context["runs_on"] = jobDef.RunsOn[0]
	}

	// Pass services to the pod for sidecar creation
	if len(jobDef.Services) > 0 {
		claim.Context["services"] = jobDef.Services
	}

	// Add job-level env vars to expression context
	for k, v := range jobDef.Env {
		exprCtx.Env[k] = workflow.Interpolate(v, exprCtx)
	}

	// Create a single pod for the entire job
	jobLog := &logWriter{runner: r, ctx: ctx, jobID: job.ID, stepID: ""}
	fmt.Fprintf(jobLog, "=== Creating job pod ===\n")

	jobPod, err := r.executor.CreateJobPod(ctx, job.ID, job.Name, claim.Context)
	if err != nil {
		return fmt.Errorf("create job pod: %w", err)
	}
	defer func() {
		fmt.Fprintf(jobLog, "\n=== Cleaning up job pod ===\n")
		jobPod.Cleanup(ctx)
	}()

	fmt.Fprintf(jobLog, "Pod %s ready\n\n", jobPod.Name)

	// Execute steps sequentially in the same pod
	allSuccess := true
	for i, step := range claim.Steps {
		stepLog := &logWriter{runner: r, ctx: ctx, jobID: job.ID, stepID: step.ID}

		fmt.Fprintf(stepLog, "=== Step %d: %s ===\n", i+1, step.Name)

		// Get step definition
		var stepDef *StepDefinition
		if i < len(jobDef.Steps) {
			sd := jobDef.Steps[i] // copy to avoid mutating original
			stepDef = &sd
		}

		if stepDef == nil {
			fmt.Fprintf(stepLog, "No step definition found, skipping\n")
			r.completeStep(ctx, job.ID, i, model.ConclusionSkipped)
			continue
		}

		// Evaluate if: conditional
		if stepDef.If != "" {
			if !workflow.EvalExprBool(stepDef.If, exprCtx) {
				fmt.Fprintf(stepLog, "Skipped (if: %s evaluated to false)\n\n", stepDef.If)
				r.completeStep(ctx, job.ID, i, model.ConclusionSkipped)
				continue
			}
		}

		// Interpolate expressions in step fields
		interpolateStep(stepDef, exprCtx)

		// Execute step in the job pod
		result, err := jobPod.ExecuteStep(ctx, stepDef, claim.Context, stepLog)

		conclusion := model.ConclusionSuccess
		if err != nil {
			fmt.Fprintf(stepLog, "Error: %v\n", err)
			conclusion = model.ConclusionFailure
		} else if result.ExitCode != 0 {
			fmt.Fprintf(stepLog, "Exit code: %d\n", result.ExitCode)
			conclusion = model.ConclusionFailure
		}

		// Capture step outputs from GITHUB_OUTPUT file
		stepOutputs := make(map[string]string)
		if stepDef.ID != "" {
			outputResult, outputErr := jobPod.ExecuteStep(ctx, &StepDefinition{
				Run:   "cat /tmp/github_output 2>/dev/null || true",
				Shell: "bash",
			}, claim.Context, io.Discard)
			if outputErr == nil && outputResult.Output != "" {
				stepOutputs = parseEnvFile(outputResult.Output)
			}
			// Clear the output file for the next step
			jobPod.ExecuteStep(ctx, &StepDefinition{
				Run:   "truncate -s 0 /tmp/github_output 2>/dev/null || true",
				Shell: "bash",
			}, claim.Context, io.Discard)
		}

		// Record step result for steps.* context
		stepOutcome := "success"
		if conclusion == model.ConclusionFailure {
			stepOutcome = "failure"
		}
		if stepDef.ID != "" {
			exprCtx.Steps[stepDef.ID] = workflow.StepResult{
				Outputs:    stepOutputs,
				Outcome:    stepOutcome,
				Conclusion: stepOutcome,
			}
		}

		// Complete step
		r.completeStep(ctx, job.ID, i, conclusion)
		fmt.Fprintf(stepLog, "Step completed: %s\n\n", conclusion)

		if conclusion == model.ConclusionFailure {
			allSuccess = false
			exprCtx.GitHub["job_status"] = "failure"
			// Check if we should continue on error
			if stepDef != nil && !stepDef.ContinueOnError {
				break
			}
		}
	}

	// Complete job
	conclusion := model.ConclusionSuccess
	if !allSuccess {
		conclusion = model.ConclusionFailure
	}
	return r.completeJob(ctx, job.ID, conclusion)
}

// startJob marks a job as started.
func (r *Runner) startJob(ctx context.Context, jobID string) error {
	reqBody := map[string]string{
		"job_id":      jobID,
		"runner_name": r.cfg.RunnerName,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/-/ci/jobs/start", r.cfg.ControlPlaneURL)
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
		"job_id":  jobID,
		"step_id": stepID,
		"content": content,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/-/ci/jobs/logs", r.cfg.ControlPlaneURL)
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
	reqBody := map[string]interface{}{
		"job_id":      jobID,
		"step_number": stepNumber,
		"conclusion":  conclusion,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/-/ci/jobs/step-complete", r.cfg.ControlPlaneURL)
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
		"job_id":     jobID,
		"conclusion": conclusion,
	}
	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/-/ci/jobs/complete", r.cfg.ControlPlaneURL)
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
	Name      string                     `json:"name"`
	RunsOn    []string                   `json:"runs_on"`
	Container *ContainerDef              `json:"container,omitempty"`
	Services  map[string]ServiceDef      `json:"services,omitempty"`
	Steps     []StepDefinition           `json:"steps"`
	Env       map[string]string          `json:"env,omitempty"`
}

type ServiceDef struct {
	Image   string            `json:"image"`
	Env     map[string]string `json:"env,omitempty"`
	Ports   []string          `json:"ports,omitempty"`
	Volumes []string          `json:"volumes,omitempty"`
	Options string            `json:"options,omitempty"`
}

type ContainerDef struct {
	Image       string            `json:"image"`
	Env         map[string]string `json:"env,omitempty"`
	Ports       []string          `json:"ports,omitempty"`
	Volumes     []string          `json:"volumes,omitempty"`
	Options     string            `json:"options,omitempty"`
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

// parseEnvFile parses a GITHUB_OUTPUT/GITHUB_ENV style file.
// Format: KEY=VALUE (one per line) or KEY<<DELIMITER\nVALUE\nDELIMITER for multiline.
func parseEnvFile(content string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx == -1 {
			i++
			continue
		}
		key := line[:eqIdx]
		value := line[eqIdx+1:]

		// Check for multiline value: KEY<<DELIMITER
		if strings.HasPrefix(value, "<<") {
			delimiter := strings.TrimPrefix(value, "<<")
			var multiline strings.Builder
			i++
			for i < len(lines) {
				if strings.TrimSpace(lines[i]) == delimiter {
					break
				}
				if multiline.Len() > 0 {
					multiline.WriteString("\n")
				}
				multiline.WriteString(lines[i])
				i++
			}
			result[key] = multiline.String()
		} else {
			result[key] = value
		}
		i++
	}
	return result
}
