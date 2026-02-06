package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// Executor runs jobs in Kubernetes pods.
type Executor struct {
	client    *kubernetes.Clientset
	config    *rest.Config
	namespace string
}

// NewExecutor creates a new Kubernetes executor.
func NewExecutor(namespace, kubeconfig string) (*Executor, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Executor{
		client:    client,
		config:    config,
		namespace: namespace,
	}, nil
}

// JobPod represents a running job pod.
type JobPod struct {
	Name      string
	Namespace string
	executor  *Executor
}

// CreateJobPod creates a pod for executing a job's steps.
func (e *Executor) CreateJobPod(ctx context.Context, jobID, jobName string, jobContext map[string]interface{}) (*JobPod, error) {
	podName := fmt.Sprintf("ci-job-%s", sanitizeName(jobID))
	if len(podName) > 63 {
		podName = podName[:63]
	}

	// Get image from context or default
	image := "ubuntu:22.04"
	if img, ok := jobContext["image"].(string); ok && img != "" {
		image = img
	}

	// Build environment variables
	env := buildEnvVars(jobContext, nil)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: e.namespace,
			Labels: map[string]string{
				"app":      "kailab-ci",
				"job-id":   jobID,
				"job-name": sanitizeName(jobName),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "job",
					Image: image,
					// Keep the container running so we can exec into it
					Command:         []string{"sleep", "infinity"},
					Env:             env,
					ImagePullPolicy: corev1.PullIfNotPresent,
					WorkingDir:      "/workspace",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "workspace",
							MountPath: "/workspace",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "workspace",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	// Create the pod
	_, err := e.client.CoreV1().Pods(e.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create job pod: %w", err)
	}

	// Wait for pod to be running
	if err := e.waitForPodRunning(ctx, podName); err != nil {
		// Clean up on failure
		e.deletePod(podName)
		return nil, fmt.Errorf("pod failed to start: %w", err)
	}

	return &JobPod{
		Name:      podName,
		Namespace: e.namespace,
		executor:  e,
	}, nil
}

// waitForPodRunning waits for a pod to be in Running state.
func (e *Executor) waitForPodRunning(ctx context.Context, podName string) error {
	timeout := 5 * time.Minute
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pod, err := e.client.CoreV1().Pods(e.namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pod: %w", err)
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("pod failed to start")
		case corev1.PodSucceeded:
			return fmt.Errorf("pod exited unexpectedly")
		case corev1.PodPending:
			// Check for container errors
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil && cs.State.Waiting.Reason == "ImagePullBackOff" {
					return fmt.Errorf("failed to pull image: %s", cs.State.Waiting.Message)
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ExecuteStep runs a step in the job pod.
func (jp *JobPod) ExecuteStep(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	// Handle actions (uses:) vs run commands
	if stepDef.Uses != "" {
		return jp.executeAction(ctx, stepDef, jobContext, logWriter)
	}

	if stepDef.Run != "" {
		return jp.executeCommand(ctx, stepDef.Run, stepDef.Shell, stepDef.Env, stepDef.WorkingDir, logWriter)
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

// executeCommand runs a command in the job pod.
func (jp *JobPod) executeCommand(ctx context.Context, script, shell string, env map[string]string, workingDir string, logWriter io.Writer) (*ExecutionResult, error) {
	if shell == "" {
		shell = "bash"
	}

	// Build the command with environment variables
	var cmdBuilder strings.Builder
	for name, value := range env {
		cmdBuilder.WriteString(fmt.Sprintf("export %s=%q\n", name, value))
	}
	if workingDir != "" {
		cmdBuilder.WriteString(fmt.Sprintf("cd %s\n", workingDir))
	}
	cmdBuilder.WriteString(script)

	cmd := []string{shell, "-e", "-c", cmdBuilder.String()}

	req := jp.executor.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(jp.Name).
		Namespace(jp.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "job",
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(jp.executor.config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	multiOut := io.MultiWriter(&stdout, logWriter)
	multiErr := io.MultiWriter(&stderr, logWriter)

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: multiOut,
		Stderr: multiErr,
	})

	exitCode := 0
	if err != nil {
		// Try to extract exit code from error
		if exitErr, ok := err.(interface{ ExitStatus() int }); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			// Non-zero exit or other error
			exitCode = 1
		}
	}

	return &ExecutionResult{
		ExitCode: exitCode,
		Output:   stdout.String(),
	}, nil
}

// executeAction handles "uses:" steps.
func (jp *JobPod) executeAction(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	action := stepDef.Uses

	switch {
	case strings.HasPrefix(action, "actions/checkout"):
		return jp.actionCheckout(ctx, stepDef, jobContext, logWriter)
	case strings.HasPrefix(action, "actions/setup-go"):
		return jp.actionSetupGo(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "actions/setup-node"):
		return jp.actionSetupNode(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "actions/cache"):
		return jp.actionCache(ctx, stepDef, logWriter)
	default:
		fmt.Fprintf(logWriter, "Unknown action: %s (skipping)\n", action)
		return &ExecutionResult{ExitCode: 0}, nil
	}
}

// Built-in action implementations

func (jp *JobPod) actionCheckout(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	repo, _ := jobContext["repo"].(string)
	ref, _ := jobContext["ref"].(string)
	sha, _ := jobContext["sha"].(string)

	fmt.Fprintf(logWriter, "Checking out %s @ %s\n", repo, sha)

	// For now, create a placeholder checkout
	// In production, this would clone from kailab via SSH or HTTP
	script := fmt.Sprintf(`
echo "==> Checkout: %s @ %s"
echo "Ref: %s"
echo "Note: Full git checkout not yet implemented"
echo "Creating workspace placeholder..."
mkdir -p /workspace
echo "%s" > /workspace/.git-sha
echo "Checkout complete"
`, repo, sha, ref, sha)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionSetupGo(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	goVersion := "1.22"
	if v, ok := stepDef.With["go-version"]; ok {
		goVersion = v
	}

	fmt.Fprintf(logWriter, "Setting up Go %s\n", goVersion)

	script := fmt.Sprintf(`
if command -v go &> /dev/null; then
    echo "Go already installed: $(go version)"
else
    echo "Installing Go %s..."
    apt-get update -qq && apt-get install -y -qq wget ca-certificates > /dev/null
    wget -q https://go.dev/dl/go%s.linux-amd64.tar.gz
    tar -C /usr/local -xzf go%s.linux-amd64.tar.gz
    rm go%s.linux-amd64.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi
export PATH=$PATH:/usr/local/go/bin
go version
`, goVersion, goVersion, goVersion, goVersion)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionSetupNode(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	nodeVersion := "20"
	if v, ok := stepDef.With["node-version"]; ok {
		nodeVersion = v
	}

	fmt.Fprintf(logWriter, "Setting up Node.js %s\n", nodeVersion)

	script := fmt.Sprintf(`
if command -v node &> /dev/null; then
    echo "Node.js already installed: $(node --version)"
else
    echo "Installing Node.js %s..."
    apt-get update -qq && apt-get install -y -qq curl ca-certificates > /dev/null
    curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - > /dev/null 2>&1
    apt-get install -y -qq nodejs > /dev/null
fi
node --version
npm --version
`, nodeVersion, nodeVersion)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionCache(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	path, _ := stepDef.With["path"]
	key, _ := stepDef.With["key"]

	fmt.Fprintf(logWriter, "Cache: path=%s, key=%s\n", path, key)
	fmt.Fprintf(logWriter, "Cache not implemented yet, skipping\n")

	return &ExecutionResult{ExitCode: 0}, nil
}

// Cleanup deletes the job pod.
func (jp *JobPod) Cleanup(ctx context.Context) error {
	return jp.executor.deletePod(jp.Name)
}

func (e *Executor) deletePod(podName string) error {
	deletePolicy := metav1.DeletePropagationForeground
	return e.client.CoreV1().Pods(e.namespace).Delete(context.Background(), podName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
}

// ExecutionResult contains the result of an execution.
type ExecutionResult struct {
	ExitCode int
	Output   string
}

// Helper functions

func buildEnvVars(context map[string]interface{}, stepEnv map[string]string) []corev1.EnvVar {
	var env []corev1.EnvVar

	// Add context variables
	if repo, ok := context["repo"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_REPOSITORY", Value: repo})
		env = append(env, corev1.EnvVar{Name: "KAILAB_REPOSITORY", Value: repo})
	}
	if ref, ok := context["ref"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_REF", Value: ref})
		env = append(env, corev1.EnvVar{Name: "KAILAB_REF", Value: ref})
	}
	if sha, ok := context["sha"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_SHA", Value: sha})
		env = append(env, corev1.EnvVar{Name: "KAILAB_SHA", Value: sha})
	}
	if event, ok := context["event"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_EVENT_NAME", Value: event})
		env = append(env, corev1.EnvVar{Name: "KAILAB_EVENT", Value: event})
	}

	// Add secrets as environment variables
	if secrets, ok := context["secrets"].(map[string]string); ok {
		for name, value := range secrets {
			env = append(env, corev1.EnvVar{Name: name, Value: value})
		}
	}

	// Add step-specific environment variables
	for name, value := range stepEnv {
		env = append(env, corev1.EnvVar{Name: name, Value: value})
	}

	// Standard CI environment variables
	env = append(env, corev1.EnvVar{Name: "CI", Value: "true"})
	env = append(env, corev1.EnvVar{Name: "KAILAB_CI", Value: "true"})
	env = append(env, corev1.EnvVar{Name: "HOME", Value: "/root"})
	env = append(env, corev1.EnvVar{Name: "WORKSPACE", Value: "/workspace"})

	return env
}

func sanitizeName(s string) string {
	result := strings.ToLower(s)
	var sanitized []byte
	for i := 0; i < len(result); i++ {
		c := result[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			sanitized = append(sanitized, c)
		} else {
			sanitized = append(sanitized, '-')
		}
	}
	result = string(sanitized)
	for len(result) > 0 && result[0] == '-' {
		result = result[1:]
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return result
}

// Legacy types for compatibility with runner.go

type ExecutionRequest struct {
	JobID     string
	StepID    string
	StepDef   *StepDefinition
	Context   map[string]interface{}
	LogWriter io.Writer
}

// Execute is kept for compatibility but now delegates to the new job pod model.
// Deprecated: Use CreateJobPod and JobPod.ExecuteStep instead.
func (e *Executor) Execute(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	// Create a temporary pod for this single step (legacy behavior)
	jp, err := e.CreateJobPod(ctx, req.JobID, "legacy", req.Context)
	if err != nil {
		return nil, err
	}
	defer jp.Cleanup(ctx)

	return jp.ExecuteStep(ctx, req.StepDef, req.Context, req.LogWriter)
}
