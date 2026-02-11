// Package runner implements the CI runner that executes jobs in Kubernetes.
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	StorePath          string // Local store path for caches/artifacts (default: /tmp/kailab-ci-store)
	GCSBucket          string // GCS bucket for caches/artifacts (if set, uses GCS instead of local)
	GCSPrefix          string // GCS key prefix (default: "ci")
	ServiceAccountName string // Kubernetes service account for job pods (default: "kailab-runner")
}

// Runner executes CI jobs.
type Runner struct {
	cfg      *Config
	client   *http.Client
	executor *Executor
}

// New creates a new runner.
func New(cfg *Config) (*Runner, error) {
	var ciStore store.Store

	if cfg.GCSBucket != "" {
		prefix := cfg.GCSPrefix
		if prefix == "" {
			prefix = "ci"
		}
		gcsStore, err := store.NewGCSStore(context.Background(), cfg.GCSBucket, prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to create gcs store: %w", err)
		}
		ciStore = gcsStore
		log.Printf("Using GCS store: gs://%s/%s", cfg.GCSBucket, prefix)
	} else {
		storePath := cfg.StorePath
		if storePath == "" {
			storePath = "/tmp/kailab-ci-store"
		}
		localStore, err := store.NewLocalStore(storePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create ci store: %w", err)
		}
		ciStore = localStore
		log.Printf("Using local store: %s", storePath)
	}

	saName := cfg.ServiceAccountName
	if saName == "" {
		saName = "kailab-runner"
	}

	executor, err := NewExecutor(cfg.Namespace, cfg.Kubeconfig, saName, ciStore)
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
		r.completeJob(ctx, claim.Job.ID, model.ConclusionFailure, nil)
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
	if actor, ok := claim.Context["actor"].(string); ok {
		ec.GitHub["actor"] = actor
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

	// Populate needs.* context (dependency job outputs and results)
	if needsRaw, ok := claim.Context["needs"].(map[string]interface{}); ok {
		for jobName, data := range needsRaw {
			jobData, ok := data.(map[string]interface{})
			if !ok {
				continue
			}
			jr := workflow.JobResult{
				Outputs: make(map[string]string),
			}
			if result, ok := jobData["result"].(string); ok {
				jr.Result = result
			}
			if outputs, ok := jobData["outputs"].(map[string]interface{}); ok {
				for k, v := range outputs {
					if s, ok := v.(string); ok {
						jr.Outputs[k] = s
					}
				}
			}
			ec.Needs[jobName] = jr
		}
	}

	// Populate runner context
	ec.Runner["os"] = "Linux"
	ec.Runner["arch"] = "X64"
	ec.Runner["name"] = "kailab-runner"

	// Populate inputs (workflow_dispatch)
	if inputs, ok := claim.Context["inputs"].(map[string]string); ok {
		ec.Inputs = inputs
	}

	// Populate vars.* context
	if varsRaw, ok := claim.Context["vars"].(map[string]interface{}); ok {
		for k, v := range varsRaw {
			if s, ok := v.(string); ok {
				ec.Vars[k] = s
			}
		}
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

// defaultJobTimeoutMinutes is the default timeout for jobs (same as GitHub Actions).
const defaultJobTimeoutMinutes = 360

// defaultStepTimeoutMinutes is the default timeout for steps (same as GitHub Actions).
const defaultStepTimeoutMinutes = 360

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

	// Apply job-level timeout. Keep the parent context for API calls (completeJob, etc.)
	// so they still work after the job timeout fires.
	parentCtx := ctx
	jobTimeout := defaultJobTimeoutMinutes
	if jobDef.TimeoutMinutes > 0 {
		jobTimeout = jobDef.TimeoutMinutes
	}
	jobCtx, jobCancel := context.WithTimeout(ctx, time.Duration(jobTimeout)*time.Minute)
	defer jobCancel()
	ctx = jobCtx
	_ = parentCtx // used for API calls after timeout

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

	// Add workflow-level env vars to expression context (lower priority than job-level)
	for k, v := range parsedWF.Env {
		exprCtx.Env[k] = workflow.Interpolate(v, exprCtx)
	}

	// Add job-level env vars to expression context (overrides workflow-level)
	for k, v := range jobDef.Env {
		exprCtx.Env[k] = workflow.Interpolate(v, exprCtx)
	}

	// Merge all env vars into the claim context so they're set in the pod
	if _, ok := claim.Context["workflow_env"]; !ok {
		envMap := make(map[string]string)
		for k, v := range exprCtx.Env {
			envMap[k] = v
		}
		claim.Context["workflow_env"] = envMap
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
		// Use parentCtx so cleanup works even after job timeout
		jobPod.Cleanup(parentCtx)
	}()

	fmt.Fprintf(jobLog, "Pod %s ready\n\n", jobPod.Name)

	// Wire up hashFiles() to execute inside the pod
	exprCtx.HashFilesFunc = func(patterns []string) string {
		return hashFilesInPod(ctx, jobPod, patterns, claim.Context)
	}

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

		// Apply step-level timeout
		stepCtx := ctx
		stepTimeout := defaultStepTimeoutMinutes
		if stepDef.TimeoutMinutes > 0 {
			stepTimeout = stepDef.TimeoutMinutes
		}
		stepCtx, stepCancel := context.WithTimeout(ctx, time.Duration(stepTimeout)*time.Minute)

		// Execute step in the job pod
		result, err := jobPod.ExecuteStep(stepCtx, stepDef, claim.Context, stepLog)
		stepCancel()

		conclusion := model.ConclusionSuccess
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				// Job-level timeout — break out, handled below
				fmt.Fprintf(stepLog, "Job timed out\n")
				allSuccess = false
				break
			} else if stepCtx.Err() == context.DeadlineExceeded {
				fmt.Fprintf(stepLog, "Step timed out after %d minutes\n", stepTimeout)
			} else {
				fmt.Fprintf(stepLog, "Error: %v\n", err)
			}
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

	// Check if the job timed out
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(jobLog, "\n=== Job timed out after %d minutes ===\n", jobTimeout)
		return r.completeJob(parentCtx, job.ID, model.ConclusionFailure, nil)
	}

	// Evaluate job outputs by interpolating expressions against final step context
	var jobOutputs map[string]string
	if len(jobDef.Outputs) > 0 {
		jobOutputs = make(map[string]string, len(jobDef.Outputs))
		for key, expr := range jobDef.Outputs {
			jobOutputs[key] = workflow.Interpolate(expr, exprCtx)
		}
		fmt.Fprintf(jobLog, "Job outputs: %v\n", jobOutputs)
	}

	// Read GITHUB_STEP_SUMMARY if it has content
	var jobSummary string
	summaryResult, summaryErr := jobPod.ExecuteStep(parentCtx, &StepDefinition{
		Run:   "cat /tmp/github_step_summary 2>/dev/null || true",
		Shell: "bash",
	}, claim.Context, io.Discard)
	if summaryErr == nil && strings.TrimSpace(summaryResult.Output) != "" {
		jobSummary = strings.TrimSpace(summaryResult.Output)
	}

	// Complete job
	conclusion := model.ConclusionSuccess
	if !allSuccess {
		conclusion = model.ConclusionFailure
	}
	return r.completeJob(parentCtx, job.ID, conclusion, jobOutputs, jobSummary)
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

// completeJob marks a job as completed, optionally with outputs and summary.
func (r *Runner) completeJob(ctx context.Context, jobID, conclusion string, outputs map[string]string, summaries ...string) error {
	reqBody := map[string]interface{}{
		"job_id":     jobID,
		"conclusion": conclusion,
	}
	if len(outputs) > 0 {
		reqBody["outputs"] = outputs
	}
	if len(summaries) > 0 && summaries[0] != "" {
		reqBody["summary"] = summaries[0]
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
	Env  map[string]string        `json:"env,omitempty"`
	Jobs map[string]JobDefinition `json:"jobs"`
}

type JobDefinition struct {
	Name           string                     `json:"name"`
	RunsOn         []string                   `json:"runs_on"`
	Container      *ContainerDef              `json:"container,omitempty"`
	Services       map[string]ServiceDef      `json:"services,omitempty"`
	Steps          []StepDefinition           `json:"steps"`
	Env            map[string]string          `json:"env,omitempty"`
	Outputs        map[string]string          `json:"outputs,omitempty"`
	TimeoutMinutes int                        `json:"timeout_minutes,omitempty"`
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

// hashFilesInPod executes a glob + SHA-256 hash script inside the job pod.
// Matches GitHub Actions hashFiles() behavior: expand glob patterns, sort matched
// file paths, concatenate file contents in order, and return the hex SHA-256 digest.
func hashFilesInPod(ctx context.Context, jp *JobPod, patterns []string, jobContext map[string]interface{}) string {
	// Build a shell script that uses find + sha256sum to hash matching files.
	// We use a Python-free approach with just shell builtins + sha256sum.
	// The script:
	// 1. For each pattern, uses bash globstar to expand **/ patterns
	// 2. Sorts all matched files
	// 3. Hashes each file and feeds into a final SHA-256
	var patternArgs []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" {
			// Shell-escape the pattern for the find command
			patternArgs = append(patternArgs, fmt.Sprintf("%q", p))
		}
	}
	if len(patternArgs) == 0 {
		return ""
	}

	// Use a script that handles ** glob patterns via bash globstar
	script := fmt.Sprintf(`
set +e
cd /workspace 2>/dev/null || true
shopt -s globstar nullglob 2>/dev/null || true

FILES=""
for pattern in %s; do
    for f in $pattern; do
        if [ -f "$f" ]; then
            FILES="$FILES
$f"
        fi
    done
done

# Sort and deduplicate
FILES=$(echo "$FILES" | sort -u | sed '/^$/d')

if [ -z "$FILES" ]; then
    echo ""
    exit 0
fi

# Hash each file in sorted order and feed into final hash
echo "$FILES" | while IFS= read -r f; do
    cat "$f"
done | sha256sum | cut -d' ' -f1
`, strings.Join(patternArgs, " "))

	result, err := jp.executeCommand(ctx, script, "bash", nil, "", io.Discard)
	if err != nil {
		return ""
	}

	hash := strings.TrimSpace(result.Output)
	// Validate it looks like a hex hash
	if len(hash) == 64 {
		if _, err := hex.DecodeString(hash); err == nil {
			return hash
		}
	}

	// Fallback: if sha256sum isn't available, hash locally from output
	// This shouldn't happen on ubuntu containers but handle gracefully
	if hash != "" {
		h := sha256.Sum256([]byte(hash))
		return hex.EncodeToString(h[:])
	}
	return ""
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
