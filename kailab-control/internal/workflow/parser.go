// Package workflow provides GitHub Actions-compatible workflow parsing and execution.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflow represents a parsed workflow definition.
type Workflow struct {
	Name        string            `yaml:"name" json:"name"`
	On          WorkflowTrigger   `yaml:"on" json:"on"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Jobs        map[string]Job    `yaml:"jobs" json:"jobs"`
	Defaults    *Defaults         `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Concurrency *Concurrency      `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	Permissions Permissions       `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

// Permissions represents GitHub Actions permissions for the GITHUB_TOKEN.
// Can be a map of scope→access or a single string ("read-all", "write-all").
type Permissions map[string]string

// UnmarshalYAML implements yaml.Unmarshaler for Permissions.
func (p *Permissions) UnmarshalYAML(value *yaml.Node) error {
	// Try as a string first ("read-all" or "write-all")
	var single string
	if err := value.Decode(&single); err == nil {
		*p = Permissions{"_all": single}
		return nil
	}

	// Try as map
	var m map[string]string
	if err := value.Decode(&m); err != nil {
		return err
	}
	*p = Permissions(m)
	return nil
}

// WorkflowTrigger represents workflow trigger configuration.
// Supports both simple form (on: push) and complex form (on: push: branches: [main]).
type WorkflowTrigger struct {
	Push             *PushTrigger             `yaml:"push,omitempty" json:"push,omitempty"`
	PullRequest      *PullRequestTrigger      `yaml:"pull_request,omitempty" json:"pull_request,omitempty"`
	Review           *ReviewTrigger           `yaml:"review,omitempty" json:"review,omitempty"`
	WorkflowDispatch *WorkflowDispatchTrigger `yaml:"workflow_dispatch,omitempty" json:"workflow_dispatch,omitempty"`
	WorkflowCall     *WorkflowCallTrigger     `yaml:"workflow_call,omitempty" json:"workflow_call,omitempty"`
	Schedule         []ScheduleTrigger        `yaml:"schedule,omitempty" json:"schedule,omitempty"`
}

// PushTrigger configures push event triggers.
type PushTrigger struct {
	Branches       []string `yaml:"branches,omitempty" json:"branches,omitempty"`
	BranchesIgnore []string `yaml:"branches-ignore,omitempty" json:"branches_ignore,omitempty"`
	Tags           []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	TagsIgnore     []string `yaml:"tags-ignore,omitempty" json:"tags_ignore,omitempty"`
	Paths          []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	PathsIgnore    []string `yaml:"paths-ignore,omitempty" json:"paths_ignore,omitempty"`
}

// PullRequestTrigger configures pull_request event triggers (GitHub Actions compatible).
// Mapped internally to review events.
type PullRequestTrigger struct {
	Branches       []string `yaml:"branches,omitempty" json:"branches,omitempty"`
	BranchesIgnore []string `yaml:"branches-ignore,omitempty" json:"branches_ignore,omitempty"`
	Paths          []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	PathsIgnore    []string `yaml:"paths-ignore,omitempty" json:"paths_ignore,omitempty"`
	Types          []string `yaml:"types,omitempty" json:"types,omitempty"` // opened, synchronize, closed, etc.
}

// ReviewTrigger configures review event triggers.
type ReviewTrigger struct {
	Types []string `yaml:"types,omitempty" json:"types,omitempty"` // opened, synchronize, closed, etc.
}

// WorkflowDispatchTrigger configures manual dispatch triggers.
type WorkflowDispatchTrigger struct {
	Inputs map[string]DispatchInput `yaml:"inputs,omitempty" json:"inputs,omitempty"`
}

// DispatchInput represents a manual dispatch input parameter.
type DispatchInput struct {
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Default     string   `yaml:"default,omitempty" json:"default,omitempty"`
	Type        string   `yaml:"type,omitempty" json:"type,omitempty"` // string, boolean, choice, environment
	Options     []string `yaml:"options,omitempty" json:"options,omitempty"`
}

// WorkflowCallTrigger configures a reusable workflow that can be called by other workflows.
type WorkflowCallTrigger struct {
	Inputs  map[string]WorkflowCallInput  `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Outputs map[string]WorkflowCallOutput `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Secrets map[string]WorkflowCallSecret `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// WorkflowCallInput defines an input parameter for a reusable workflow.
type WorkflowCallInput struct {
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Default     string `yaml:"default,omitempty" json:"default,omitempty"`
	Type        string `yaml:"type,omitempty" json:"type,omitempty"` // string, boolean, number
}

// WorkflowCallOutput defines an output of a reusable workflow.
type WorkflowCallOutput struct {
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Value       string `yaml:"value" json:"value"` // Expression like ${{ jobs.build.outputs.result }}
}

// WorkflowCallSecret defines a secret parameter for a reusable workflow.
type WorkflowCallSecret struct {
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// ScheduleTrigger configures scheduled triggers.
type ScheduleTrigger struct {
	Cron string `yaml:"cron" json:"cron"`
}

// Job represents a job in the workflow.
type Job struct {
	Name        string             `yaml:"name,omitempty" json:"name,omitempty"`
	RunsOn      StringOrSlice      `yaml:"runs-on" json:"runs_on"`
	Uses        string             `yaml:"uses,omitempty" json:"uses,omitempty"` // Reusable workflow reference
	With        map[string]string  `yaml:"with,omitempty" json:"with,omitempty"` // Inputs for reusable workflow
	Needs       StringOrSlice      `yaml:"needs,omitempty" json:"needs,omitempty"`
	If          string             `yaml:"if,omitempty" json:"if,omitempty"`
	Steps       []Step             `yaml:"steps" json:"steps"`
	Env         map[string]string  `yaml:"env,omitempty" json:"env,omitempty"`
	Outputs     map[string]string  `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Secrets     JobSecrets         `yaml:"secrets,omitempty" json:"secrets,omitempty"` // Secret mapping for reusable workflow
	Services    map[string]Service `yaml:"services,omitempty" json:"services,omitempty"`
	Container   *Container         `yaml:"container,omitempty" json:"container,omitempty"`
	Strategy    *Strategy          `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Concurrency *Concurrency       `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	Timeout     int                `yaml:"timeout-minutes,omitempty" json:"timeout_minutes,omitempty"`
	ContinueOn  *ContinueOnError   `yaml:"continue-on-error,omitempty" json:"continue_on_error,omitempty"`
	Permissions Permissions        `yaml:"permissions,omitempty" json:"permissions,omitempty"`
}

// IsReusableWorkflowCall returns true if this job calls a reusable workflow.
func (j *Job) IsReusableWorkflowCall() bool {
	return j.Uses != "" && strings.HasSuffix(j.Uses, ".yml") || strings.HasSuffix(j.Uses, ".yaml")
}

// JobSecrets can be "inherit" (string) or a map of secret name -> value expression.
type JobSecrets struct {
	Inherit bool              `json:"inherit,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
}

// UnmarshalYAML handles both `secrets: inherit` and `secrets: {name: value}`.
func (s *JobSecrets) UnmarshalYAML(value *yaml.Node) error {
	var str string
	if err := value.Decode(&str); err == nil {
		if str == "inherit" {
			s.Inherit = true
		}
		return nil
	}

	var m map[string]string
	if err := value.Decode(&m); err != nil {
		return err
	}
	s.Values = m
	return nil
}

// UnmarshalJSON handles both "inherit" and map forms.
func (s *JobSecrets) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if str == "inherit" {
			s.Inherit = true
		}
		return nil
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.Values = m
	return nil
}

// MarshalJSON serializes JobSecrets.
func (s JobSecrets) MarshalJSON() ([]byte, error) {
	if s.Inherit {
		return json.Marshal("inherit")
	}
	if len(s.Values) > 0 {
		return json.Marshal(s.Values)
	}
	return []byte("null"), nil
}

// Step represents a step in a job.
type Step struct {
	ID               string            `yaml:"id,omitempty" json:"id,omitempty"`
	Name             string            `yaml:"name,omitempty" json:"name,omitempty"`
	Uses             string            `yaml:"uses,omitempty" json:"uses,omitempty"`
	Run              string            `yaml:"run,omitempty" json:"run,omitempty"`
	Shell            string            `yaml:"shell,omitempty" json:"shell,omitempty"`
	With             map[string]string `yaml:"with,omitempty" json:"with,omitempty"`
	Env              map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	If               string            `yaml:"if,omitempty" json:"if,omitempty"`
	ContinueOnError  bool              `yaml:"continue-on-error,omitempty" json:"continue_on_error,omitempty"`
	TimeoutMinutes   int               `yaml:"timeout-minutes,omitempty" json:"timeout_minutes,omitempty"`
	WorkingDirectory string            `yaml:"working-directory,omitempty" json:"working_directory,omitempty"`
}

// Service represents a service container.
type Service struct {
	Image       string            `yaml:"image" json:"image"`
	Credentials *Credentials      `yaml:"credentials,omitempty" json:"credentials,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Ports       []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Options     string            `yaml:"options,omitempty" json:"options,omitempty"`
}

// Container represents the job container.
type Container struct {
	Image       string            `yaml:"image" json:"image"`
	Credentials *Credentials      `yaml:"credentials,omitempty" json:"credentials,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Ports       []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Options     string            `yaml:"options,omitempty" json:"options,omitempty"`
}

// Credentials for private container registries.
type Credentials struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// Strategy represents job execution strategy.
type Strategy struct {
	Matrix      Matrix `yaml:"matrix,omitempty" json:"matrix,omitempty"`
	FailFast    *bool  `yaml:"fail-fast,omitempty" json:"fail_fast,omitempty"`
	MaxParallel int    `yaml:"max-parallel,omitempty" json:"max_parallel,omitempty"`
}

// Matrix represents the matrix strategy configuration.
type Matrix struct {
	Include []map[string]interface{} `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []map[string]interface{} `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Values  map[string][]interface{} `yaml:"-" json:"values,omitempty"` // Dynamically populated
}

// Defaults represents workflow-level defaults.
type Defaults struct {
	Run *RunDefaults `yaml:"run,omitempty" json:"run,omitempty"`
}

// RunDefaults represents default run settings.
type RunDefaults struct {
	Shell            string `yaml:"shell,omitempty" json:"shell,omitempty"`
	WorkingDirectory string `yaml:"working-directory,omitempty" json:"working_directory,omitempty"`
}

// Concurrency represents concurrency settings.
type Concurrency struct {
	Group            string `yaml:"group" json:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress,omitempty" json:"cancel_in_progress,omitempty"`
}

// ContinueOnError can be a bool or expression.
type ContinueOnError struct {
	Value bool
}

// StringOrSlice handles YAML fields that can be either a string or slice.
type StringOrSlice []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *StringOrSlice) UnmarshalYAML(value *yaml.Node) error {
	var single string
	if err := value.Decode(&single); err == nil {
		*s = []string{single}
		return nil
	}

	var multi []string
	if err := value.Decode(&multi); err != nil {
		return err
	}
	*s = multi
	return nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *StringOrSlice) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}

	var multi []string
	if err := json.Unmarshal(data, &multi); err != nil {
		return err
	}
	*s = multi
	return nil
}

// MarshalJSON implements json.Marshaler.
// Always marshals as an array for consistent deserialization.
func (s StringOrSlice) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(s))
}

// UnmarshalYAML implements yaml.Unmarshaler for WorkflowTrigger.
// Handles both simple (on: [push, pull_request]) and complex (on: push: branches: [main]) forms.
func (t *WorkflowTrigger) UnmarshalYAML(value *yaml.Node) error {
	// Try simple string first
	var single string
	if err := value.Decode(&single); err == nil {
		return t.parseSimple([]string{single})
	}

	// Try slice of strings
	var slice []string
	if err := value.Decode(&slice); err == nil {
		return t.parseSimple(slice)
	}

	// Complex form - decode as map
	type triggerAlias WorkflowTrigger
	var alias triggerAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*t = WorkflowTrigger(alias)
	return nil
}

func (t *WorkflowTrigger) parseSimple(events []string) error {
	for _, event := range events {
		switch event {
		case "push":
			t.Push = &PushTrigger{}
		case "pull_request":
			t.PullRequest = &PullRequestTrigger{}
		case "review":
			t.Review = &ReviewTrigger{}
		case "workflow_dispatch":
			t.WorkflowDispatch = &WorkflowDispatchTrigger{}
		case "workflow_call":
			t.WorkflowCall = &WorkflowCallTrigger{}
		}
	}
	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler for Matrix.
// Handles the matrix configuration which can have arbitrary keys.
func (m *Matrix) UnmarshalYAML(value *yaml.Node) error {
	m.Values = make(map[string][]interface{})

	// Parse as a map
	var raw map[string]interface{}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	for key, val := range raw {
		switch key {
		case "include":
			if includes, ok := val.([]interface{}); ok {
				for _, inc := range includes {
					if incMap, ok := inc.(map[string]interface{}); ok {
						m.Include = append(m.Include, incMap)
					}
				}
			}
		case "exclude":
			if excludes, ok := val.([]interface{}); ok {
				for _, exc := range excludes {
					if excMap, ok := exc.(map[string]interface{}); ok {
						m.Exclude = append(m.Exclude, excMap)
					}
				}
			}
		default:
			// Matrix dimension
			if arr, ok := val.([]interface{}); ok {
				m.Values[key] = arr
			}
		}
	}

	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler for ContinueOnError.
func (c *ContinueOnError) UnmarshalYAML(value *yaml.Node) error {
	var boolVal bool
	if err := value.Decode(&boolVal); err == nil {
		c.Value = boolVal
		return nil
	}

	// Could be an expression like ${{ always() }}
	var strVal string
	if err := value.Decode(&strVal); err == nil {
		// For now, treat any expression as true
		c.Value = true
		return nil
	}

	return nil
}

// Parse parses a workflow YAML file.
func Parse(content []byte) (*Workflow, error) {
	var wf Workflow
	if err := yaml.Unmarshal(content, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse workflow YAML: %w", err)
	}

	// Validate
	if err := wf.Validate(); err != nil {
		return nil, err
	}

	return &wf, nil
}

// ParseMultiple parses multiple workflow files and returns them keyed by path.
func ParseMultiple(files map[string][]byte) (map[string]*Workflow, []error) {
	workflows := make(map[string]*Workflow)
	var errors []error

	for path, content := range files {
		wf, err := Parse(content)
		if err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", path, err))
			continue
		}
		workflows[path] = wf
	}

	return workflows, errors
}

// Validate validates the workflow configuration.
func (w *Workflow) Validate() error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(w.Jobs) == 0 {
		return fmt.Errorf("workflow must have at least one job")
	}

	for name, job := range w.Jobs {
		if err := job.Validate(name); err != nil {
			return fmt.Errorf("job %q: %w", name, err)
		}
	}

	// Validate job dependencies form a DAG (no cycles)
	if err := w.validateDependencies(); err != nil {
		return err
	}

	return nil
}

// Validate validates a job configuration.
func (j *Job) Validate(name string) error {
	if j.IsReusableWorkflowCall() {
		// Reusable workflow call: uses is set, steps and runs-on not required
		if len(j.Steps) > 0 {
			return fmt.Errorf("job with 'uses' cannot also have 'steps'")
		}
		return nil
	}

	if len(j.RunsOn) == 0 {
		return fmt.Errorf("runs-on is required")
	}

	if len(j.Steps) == 0 {
		return fmt.Errorf("job must have at least one step")
	}

	for i, step := range j.Steps {
		if err := step.Validate(i); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}

	return nil
}

// Validate validates a step configuration.
func (s *Step) Validate(index int) error {
	if s.Uses == "" && s.Run == "" {
		return fmt.Errorf("step must have either 'uses' or 'run'")
	}

	if s.Uses != "" && s.Run != "" {
		return fmt.Errorf("step cannot have both 'uses' and 'run'")
	}

	return nil
}

// validateDependencies checks for cycles in job dependencies.
func (w *Workflow) validateDependencies() error {
	// Build adjacency list
	deps := make(map[string][]string)
	for name, job := range w.Jobs {
		deps[name] = []string(job.Needs)
	}

	// Check that all dependencies exist
	for name, needs := range deps {
		for _, need := range needs {
			if _, ok := w.Jobs[need]; !ok {
				return fmt.Errorf("job %q depends on non-existent job %q", name, need)
			}
		}
	}

	// Detect cycles using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(node string) bool
	hasCycle = func(node string) bool {
		visited[node] = true
		recStack[node] = true

		for _, dep := range deps[node] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if recStack[dep] {
				return true
			}
		}

		recStack[node] = false
		return false
	}

	for name := range w.Jobs {
		if !visited[name] {
			if hasCycle(name) {
				return fmt.Errorf("circular dependency detected involving job %q", name)
			}
		}
	}

	return nil
}

// GetTriggerTypes returns the list of trigger types for this workflow.
func (w *Workflow) GetTriggerTypes() []string {
	var triggers []string
	if w.On.Push != nil {
		triggers = append(triggers, "push")
	}
	if w.On.PullRequest != nil {
		triggers = append(triggers, "pull_request")
	}
	if w.On.Review != nil {
		triggers = append(triggers, "review")
	}
	if w.On.WorkflowDispatch != nil {
		triggers = append(triggers, "workflow_dispatch")
	}
	if w.On.WorkflowCall != nil {
		triggers = append(triggers, "workflow_call")
	}
	if len(w.On.Schedule) > 0 {
		triggers = append(triggers, "schedule")
	}
	return triggers
}

// ToJSON converts the workflow to JSON.
func (w *Workflow) ToJSON() (string, error) {
	b, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FromJSON parses a workflow from JSON.
func FromJSON(data string) (*Workflow, error) {
	var wf Workflow
	if err := json.Unmarshal([]byte(data), &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

// ContentHash computes a SHA256 hash of the workflow content.
func ContentHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// GetDisplayName returns a display name for a step.
func (s *Step) GetDisplayName(index int) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Uses != "" {
		return s.Uses
	}
	if s.Run != "" {
		// Truncate long run commands
		run := strings.TrimSpace(s.Run)
		if len(run) > 50 {
			run = run[:47] + "..."
		}
		return fmt.Sprintf("Run %s", run)
	}
	return fmt.Sprintf("Step %d", index+1)
}

// GetJobDisplayName returns a display name for a job.
func (j *Job) GetJobDisplayName(key string) string {
	if j.Name != "" {
		return j.Name
	}
	return key
}
