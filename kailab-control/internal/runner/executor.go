package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Executor runs steps in Kubernetes pods.
type Executor struct {
	client    *kubernetes.Clientset
	namespace string
}

// NewExecutor creates a new Kubernetes executor.
func NewExecutor(namespace, kubeconfig string) (*Executor, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		// Out-of-cluster config
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// In-cluster config
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
		namespace: namespace,
	}, nil
}

// ExecutionRequest describes what to execute.
type ExecutionRequest struct {
	JobID     string
	StepID    string
	StepDef   *StepDefinition
	Context   map[string]interface{}
	LogWriter io.Writer
}

// ExecutionResult contains the result of an execution.
type ExecutionResult struct {
	ExitCode int
	Output   string
}

// Execute runs a step in a Kubernetes pod.
func (e *Executor) Execute(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	// Handle actions (uses:) vs run commands
	if req.StepDef.Uses != "" {
		return e.executeAction(ctx, req)
	}

	if req.StepDef.Run != "" {
		return e.executeRun(ctx, req)
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

// executeAction handles "uses:" steps.
func (e *Executor) executeAction(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	action := req.StepDef.Uses

	// Built-in actions
	switch {
	case strings.HasPrefix(action, "actions/checkout"):
		return e.actionCheckout(ctx, req)
	case strings.HasPrefix(action, "actions/setup-go"):
		return e.actionSetupGo(ctx, req)
	case strings.HasPrefix(action, "actions/setup-node"):
		return e.actionSetupNode(ctx, req)
	case strings.HasPrefix(action, "actions/cache"):
		return e.actionCache(ctx, req)
	default:
		// Unknown action - log and skip
		fmt.Fprintf(req.LogWriter, "Unknown action: %s (skipping)\n", action)
		return &ExecutionResult{ExitCode: 0}, nil
	}
}

// executeRun handles "run:" steps.
func (e *Executor) executeRun(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	// Build environment variables
	env := buildEnvVars(req.Context, req.StepDef.Env)

	// Determine shell
	shell := req.StepDef.Shell
	if shell == "" {
		shell = "bash"
	}

	// Create pod spec
	podName := fmt.Sprintf("ci-%s-%s", sanitizeName(req.JobID), sanitizeName(req.StepID))
	if len(podName) > 63 {
		podName = podName[:63]
	}

	// Get image from context or default
	image := "ubuntu:22.04"
	if img, ok := req.Context["image"].(string); ok && img != "" {
		image = img
	}

	// Build the command
	command := []string{shell, "-c", req.StepDef.Run}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: e.namespace,
			Labels: map[string]string{
				"app":    "kailab-ci",
				"job-id": req.JobID,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:            "step",
					Image:           image,
					Command:         command,
					Env:             env,
					ImagePullPolicy: corev1.PullIfNotPresent,
					WorkingDir:      req.StepDef.WorkingDir,
				},
			},
		},
	}

	// Create the pod
	fmt.Fprintf(req.LogWriter, "Creating pod %s...\n", podName)
	_, err := e.client.CoreV1().Pods(e.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	// Ensure pod is cleaned up
	defer func() {
		deletePolicy := metav1.DeletePropagationForeground
		e.client.CoreV1().Pods(e.namespace).Delete(context.Background(), podName, metav1.DeleteOptions{
			PropagationPolicy: &deletePolicy,
		})
	}()

	// Wait for pod to complete
	result, err := e.waitForPod(ctx, podName, req.LogWriter, req.StepDef.TimeoutMinutes)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// waitForPod waits for a pod to complete and streams its logs.
func (e *Executor) waitForPod(ctx context.Context, podName string, logWriter io.Writer, timeoutMinutes int) (*ExecutionResult, error) {
	timeout := 60 * time.Minute
	if timeoutMinutes > 0 {
		timeout = time.Duration(timeoutMinutes) * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for pod to be running or completed
	for {
		pod, err := e.client.CoreV1().Pods(e.namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get pod: %w", err)
		}

		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			// Stream logs
			e.streamLogs(ctx, podName, logWriter)
			return &ExecutionResult{ExitCode: 0}, nil

		case corev1.PodFailed:
			// Stream logs
			e.streamLogs(ctx, podName, logWriter)
			exitCode := 1
			if len(pod.Status.ContainerStatuses) > 0 {
				if term := pod.Status.ContainerStatuses[0].State.Terminated; term != nil {
					exitCode = int(term.ExitCode)
				}
			}
			return &ExecutionResult{ExitCode: exitCode}, nil

		case corev1.PodRunning:
			// Start streaming logs
			go e.streamLogs(ctx, podName, logWriter)

			// Continue waiting for completion
			time.Sleep(time.Second)

		case corev1.PodPending:
			// Wait for pod to start
			time.Sleep(time.Second)

		default:
			time.Sleep(time.Second)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
}

// streamLogs streams pod logs to the writer.
func (e *Executor) streamLogs(ctx context.Context, podName string, logWriter io.Writer) {
	req := e.client.CoreV1().Pods(e.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow: true,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		fmt.Fprintf(logWriter, "Failed to stream logs: %v\n", err)
		return
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		fmt.Fprintln(logWriter, scanner.Text())
	}
}

// Built-in action implementations

func (e *Executor) actionCheckout(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	repo, _ := req.Context["repo"].(string)
	ref, _ := req.Context["ref"].(string)
	sha, _ := req.Context["sha"].(string)

	// For now, just log what we would do
	// In production, this would clone from the kailab repo
	fmt.Fprintf(req.LogWriter, "Checkout: %s @ %s (%s)\n", repo, ref, sha)

	// Create a run step that does the checkout
	checkoutStep := &StepDefinition{
		Run: fmt.Sprintf(`
echo "Checking out %s @ %s"
# In production, this would use the kailab CLI or git clone
# git clone --depth 1 --branch %s <repo-url> .
echo "Checkout complete"
`, repo, sha, strings.TrimPrefix(ref, "refs/heads/")),
	}

	newReq := *req
	newReq.StepDef = checkoutStep
	return e.executeRun(ctx, &newReq)
}

func (e *Executor) actionSetupGo(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	goVersion := "1.22"
	if v, ok := req.StepDef.With["go-version"]; ok {
		goVersion = v
	}

	fmt.Fprintf(req.LogWriter, "Setting up Go %s\n", goVersion)

	setupStep := &StepDefinition{
		Run: fmt.Sprintf(`
# Install Go %s
if ! command -v go &> /dev/null; then
    apt-get update && apt-get install -y wget
    wget -q https://go.dev/dl/go%s.linux-amd64.tar.gz
    tar -C /usr/local -xzf go%s.linux-amd64.tar.gz
    rm go%s.linux-amd64.tar.gz
fi
export PATH=$PATH:/usr/local/go/bin
go version
`, goVersion, goVersion, goVersion, goVersion),
	}

	newReq := *req
	newReq.StepDef = setupStep
	return e.executeRun(ctx, &newReq)
}

func (e *Executor) actionSetupNode(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	nodeVersion := "20"
	if v, ok := req.StepDef.With["node-version"]; ok {
		nodeVersion = v
	}

	fmt.Fprintf(req.LogWriter, "Setting up Node.js %s\n", nodeVersion)

	setupStep := &StepDefinition{
		Run: fmt.Sprintf(`
# Install Node.js %s
if ! command -v node &> /dev/null; then
    apt-get update && apt-get install -y curl
    curl -fsSL https://deb.nodesource.com/setup_%s.x | bash -
    apt-get install -y nodejs
fi
node --version
npm --version
`, nodeVersion, nodeVersion),
	}

	newReq := *req
	newReq.StepDef = setupStep
	return e.executeRun(ctx, &newReq)
}

func (e *Executor) actionCache(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	path, _ := req.StepDef.With["path"]
	key, _ := req.StepDef.With["key"]

	fmt.Fprintf(req.LogWriter, "Cache: path=%s, key=%s\n", path, key)
	fmt.Fprintf(req.LogWriter, "Cache not implemented yet, skipping\n")

	return &ExecutionResult{ExitCode: 0}, nil
}

// Helper functions

func buildEnvVars(context map[string]interface{}, stepEnv map[string]string) []corev1.EnvVar {
	var env []corev1.EnvVar

	// Add context variables
	if repo, ok := context["repo"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_REPOSITORY", Value: repo})
	}
	if ref, ok := context["ref"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_REF", Value: ref})
	}
	if sha, ok := context["sha"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_SHA", Value: sha})
	}
	if event, ok := context["event"].(string); ok {
		env = append(env, corev1.EnvVar{Name: "GITHUB_EVENT_NAME", Value: event})
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

	return env
}

func sanitizeName(s string) string {
	// Kubernetes names must be lowercase alphanumeric with hyphens
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
	// Remove leading/trailing hyphens
	result = string(sanitized)
	for len(result) > 0 && result[0] == '-' {
		result = result[1:]
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return result
}
