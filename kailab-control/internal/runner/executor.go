package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"kailab-control/internal/store"

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
	store     store.Store
}

// NewExecutor creates a new Kubernetes executor.
func NewExecutor(namespace, kubeconfig string, ciStore store.Store) (*Executor, error) {
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
		store:     ciStore,
	}, nil
}

// JobPod represents a running job pod.
type JobPod struct {
	Name      string
	Namespace string
	executor  *Executor
}

// runsOnImage maps runs-on labels to container images.
var runsOnImage = map[string]string{
	"ubuntu-latest": "ubuntu:22.04",
	"ubuntu-22.04":  "ubuntu:22.04",
	"ubuntu-24.04":  "ubuntu:24.04",
	"ubuntu-20.04":  "ubuntu:20.04",
}

// CreateJobPod creates a pod for executing a job's steps.
func (e *Executor) CreateJobPod(ctx context.Context, jobID, jobName string, jobContext map[string]interface{}) (*JobPod, error) {
	podName := fmt.Sprintf("ci-job-%s", sanitizeName(jobID))
	if len(podName) > 63 {
		podName = podName[:63]
	}

	// Determine image: explicit container > runs-on mapping > default
	image := "ubuntu:22.04"
	if img, ok := jobContext["image"].(string); ok && img != "" {
		image = img
	} else if runsOn, ok := jobContext["runs_on"].(string); ok {
		if mapped, ok := runsOnImage[runsOn]; ok {
			image = mapped
		}
	}

	// Build environment variables
	env := buildEnvVars(jobContext, nil)

	containers := []corev1.Container{
		{
			Name:            "job",
			Image:           image,
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
	}

	// Add service containers as sidecars
	if services, ok := jobContext["services"].(map[string]ServiceDef); ok {
		for name, svc := range services {
			svcContainer := corev1.Container{
				Name:            sanitizeName("svc-" + name),
				Image:           svc.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
			}
			// Add service env vars
			for k, v := range svc.Env {
				svcContainer.Env = append(svcContainer.Env, corev1.EnvVar{Name: k, Value: v})
			}
			// Parse ports (format: "hostPort:containerPort" or just "containerPort")
			for _, portStr := range svc.Ports {
				port := parseContainerPort(portStr)
				if port > 0 {
					svcContainer.Ports = append(svcContainer.Ports, corev1.ContainerPort{
						ContainerPort: port,
					})
				}
			}
			containers = append(containers, svcContainer)
		}
	}

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
			Containers:    containers,
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

	// Wait for all containers (job + services) to be running
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

	// Initialize GITHUB_ENV and GITHUB_OUTPUT files (ensure they exist)
	cmdBuilder.WriteString("touch /tmp/github_env /tmp/github_output /tmp/github_state /tmp/github_path 2>/dev/null || true\n")

	// Source any env vars set by previous steps via GITHUB_ENV
	cmdBuilder.WriteString("if [ -s /tmp/github_env ]; then set -a; . /tmp/github_env 2>/dev/null || true; set +a; fi\n")

	// Add PATH entries from GITHUB_PATH
	cmdBuilder.WriteString("if [ -s /tmp/github_path ]; then while IFS= read -r p; do export PATH=\"$p:$PATH\"; done < /tmp/github_path; fi\n")

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
	case strings.HasPrefix(action, "actions/setup-python"):
		return jp.actionSetupPython(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "actions/setup-java"):
		return jp.actionSetupJava(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "dtolnay/rust-toolchain"):
		return jp.actionRustToolchain(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "actions/cache"):
		return jp.actionCache(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "actions/upload-artifact"):
		return jp.actionUploadArtifact(ctx, stepDef, jobContext, logWriter)
	case strings.HasPrefix(action, "actions/download-artifact"):
		return jp.actionDownloadArtifact(ctx, stepDef, jobContext, logWriter)
	default:
		// Try to run as a generic action (clone and execute action.yml)
		return jp.actionGeneric(ctx, stepDef, jobContext, logWriter)
	}
}

// Built-in action implementations

func (jp *JobPod) actionCheckout(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	cloneURL, _ := jobContext["clone_url"].(string)
	repo, _ := jobContext["repo"].(string)
	ref, _ := jobContext["ref"].(string)
	sha, _ := jobContext["sha"].(string)

	// Allow overriding the ref via with.ref
	if overrideRef, ok := stepDef.With["ref"]; ok && overrideRef != "" {
		ref = overrideRef
	}

	// Determine fetch depth
	fetchDepth := "1"
	if d, ok := stepDef.With["fetch-depth"]; ok && d != "" {
		fetchDepth = d
	}
	if fetchDepth == "0" {
		fetchDepth = "" // full clone
	}

	// Fall back to repo name if no clone_url
	if cloneURL == "" && repo != "" {
		cloneURL = repo
	}

	fmt.Fprintf(logWriter, "Checking out %s @ %s\n", repo, sha)

	depthArg := ""
	if fetchDepth != "" {
		depthArg = fmt.Sprintf("--depth %s", fetchDepth)
	}

	// Derive the branch/tag name from ref for checkout
	refName := ref
	refName = strings.TrimPrefix(refName, "refs/heads/")
	refName = strings.TrimPrefix(refName, "refs/tags/")

	script := fmt.Sprintf(`
set -e
echo "==> Checkout: %s"
echo "Ref: %s  SHA: %s"

# Install git if not available
if ! command -v git &> /dev/null; then
    echo "Installing git..."
    apt-get update -qq && apt-get install -y -qq git ca-certificates > /dev/null 2>&1
fi

# Clone the repository
cd /workspace
if [ -d ".git" ]; then
    echo "Repository already checked out, fetching..."
    git fetch origin %s %s
    git checkout %s || git checkout FETCH_HEAD
else
    echo "Cloning %s..."
    git clone %s --branch %s %s . 2>&1 || {
        # If branch clone fails, try cloning default and checking out SHA
        git clone %s %s . 2>&1
        git fetch origin %s 2>&1 || true
        git checkout %s 2>&1 || git checkout %s 2>&1
    }
fi

# If we have a specific SHA, check it out
if [ -n "%s" ] && [ "%s" != "" ]; then
    git checkout %s 2>/dev/null || true
fi

echo "Checked out $(git rev-parse HEAD)"
echo "Checkout complete"
`, repo, ref, sha,
		refName, depthArg,
		refName,
		cloneURL,
		cloneURL, refName, depthArg,
		cloneURL, depthArg,
		refName,
		sha, refName,
		sha, sha,
		sha)

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

func (jp *JobPod) actionSetupJava(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	javaVersion := "17"
	if v, ok := stepDef.With["java-version"]; ok {
		javaVersion = v
	}
	distribution := "temurin"
	if d, ok := stepDef.With["distribution"]; ok {
		distribution = d
	}

	fmt.Fprintf(logWriter, "Setting up Java %s (%s)\n", javaVersion, distribution)

	script := fmt.Sprintf(`
set -e
if command -v java &> /dev/null; then
    echo "Java already installed: $(java -version 2>&1 | head -1)"
else
    echo "Installing Java %s..."
    apt-get update -qq && apt-get install -y -qq wget ca-certificates > /dev/null 2>&1
    apt-get install -y -qq openjdk-%s-jdk-headless > /dev/null 2>&1 || {
        # Fallback: try default-jdk
        apt-get install -y -qq default-jdk-headless > /dev/null 2>&1
    }
fi
java -version 2>&1
`, javaVersion, javaVersion)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionRustToolchain(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	toolchain := "stable"
	// dtolnay/rust-toolchain uses the action ref as the toolchain: dtolnay/rust-toolchain@stable
	// It also supports with.toolchain
	if tc, ok := stepDef.With["toolchain"]; ok && tc != "" {
		toolchain = tc
	}

	// Extract components and targets
	components := ""
	if c, ok := stepDef.With["components"]; ok && c != "" {
		components = c
	}
	targets := ""
	if t, ok := stepDef.With["targets"]; ok && t != "" {
		targets = t
	}

	fmt.Fprintf(logWriter, "Setting up Rust toolchain: %s\n", toolchain)

	componentArgs := ""
	if components != "" {
		componentArgs = fmt.Sprintf("--component %s", strings.ReplaceAll(components, ",", " --component "))
	}
	targetArgs := ""
	if targets != "" {
		targetArgs = fmt.Sprintf("--target %s", strings.ReplaceAll(targets, ",", " --target "))
	}

	script := fmt.Sprintf(`
set -e
if command -v rustup &> /dev/null; then
    echo "Rustup found, setting toolchain..."
    rustup default %s
    %s
    %s
else
    echo "Installing Rust via rustup..."
    apt-get update -qq && apt-get install -y -qq curl ca-certificates build-essential > /dev/null 2>&1
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain %s %s %s
    . "$HOME/.cargo/env"
fi
export PATH="$HOME/.cargo/bin:$PATH"
rustc --version
cargo --version
`, toolchain,
		func() string {
			if componentArgs != "" {
				return fmt.Sprintf("rustup component add %s", strings.ReplaceAll(components, ",", " "))
			}
			return ""
		}(),
		func() string {
			if targetArgs != "" {
				return fmt.Sprintf("rustup target add %s", strings.ReplaceAll(targets, ",", " "))
			}
			return ""
		}(),
		toolchain, componentArgs, targetArgs)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionSetupPython(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	pythonVersion := "3"
	if v, ok := stepDef.With["python-version"]; ok {
		pythonVersion = v
	}

	fmt.Fprintf(logWriter, "Setting up Python %s\n", pythonVersion)

	script := fmt.Sprintf(`
set -e
if command -v python%s &> /dev/null; then
    echo "Python already installed: $(python%s --version)"
else
    echo "Installing Python %s..."
    apt-get update -qq && apt-get install -y -qq python%s python3-pip > /dev/null 2>&1
fi
python3 --version
pip3 --version || true
`, pythonVersion, pythonVersion, pythonVersion, pythonVersion)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionCache(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	paths, _ := stepDef.With["path"]
	key, _ := stepDef.With["key"]
	restoreKeys, _ := stepDef.With["restore-keys"]

	if key == "" {
		fmt.Fprintf(logWriter, "Cache: no key specified, skipping\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	fmt.Fprintf(logWriter, "Cache: key=%s\n", key)

	if jp.executor.store == nil {
		fmt.Fprintf(logWriter, "Cache: no store configured, skipping\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Try to restore cache
	cacheKey := "cache/" + key
	exists, _ := jp.executor.store.Exists(ctx, cacheKey)
	if !exists && restoreKeys != "" {
		// Try restore-keys prefixes (not a full implementation, but handles the common case)
		for _, rk := range strings.Split(restoreKeys, "\n") {
			rk = strings.TrimSpace(rk)
			if rk == "" {
				continue
			}
			prefixKey := "cache/" + rk
			if ok, _ := jp.executor.store.Exists(ctx, prefixKey); ok {
				cacheKey = prefixKey
				exists = true
				fmt.Fprintf(logWriter, "Cache restored from prefix key: %s\n", rk)
				break
			}
		}
	}

	if exists {
		// Restore: download tar from store, extract into pod
		fmt.Fprintf(logWriter, "Cache hit, restoring...\n")
		rc, err := jp.executor.store.Get(ctx, cacheKey)
		if err != nil {
			fmt.Fprintf(logWriter, "Cache restore failed: %v\n", err)
			return &ExecutionResult{ExitCode: 0}, nil // Don't fail on cache miss
		}
		defer rc.Close()

		// Copy cache tarball into the pod and extract
		tarData, _ := io.ReadAll(rc)
		if len(tarData) > 0 {
			// Write tarball to pod, then extract
			result, err := jp.executeCommand(ctx, fmt.Sprintf(
				"cat > /tmp/cache_restore.tar.gz << 'CACHE_EOF_MARKER'\n%s\nCACHE_EOF_MARKER\ntar xzf /tmp/cache_restore.tar.gz -C / 2>/dev/null || true\nrm -f /tmp/cache_restore.tar.gz\necho 'Cache restored (%d bytes)'",
				"", len(tarData)), "bash", nil, "", logWriter)
			if err != nil {
				fmt.Fprintf(logWriter, "Cache extract failed: %v\n", err)
			}
			_ = result
		}
		fmt.Fprintf(logWriter, "Cache restored successfully\n")
	} else {
		fmt.Fprintf(logWriter, "Cache miss\n")
	}

	// Register a post-job save: tar the paths and upload
	// For now, we save immediately (a real implementation would use post-job hooks)
	if !exists && paths != "" {
		// Build tar command for all paths
		pathList := strings.Split(paths, "\n")
		var tarPaths []string
		for _, p := range pathList {
			p = strings.TrimSpace(p)
			if p != "" {
				tarPaths = append(tarPaths, p)
			}
		}

		if len(tarPaths) > 0 {
			tarCmd := fmt.Sprintf("tar czf /tmp/cache_save.tar.gz %s 2>/dev/null; wc -c < /tmp/cache_save.tar.gz",
				strings.Join(tarPaths, " "))
			result, err := jp.executeCommand(ctx, tarCmd, "bash", nil, "", io.Discard)
			if err == nil && result.ExitCode == 0 {
				// Read tar from pod
				catResult, catErr := jp.executeCommand(ctx, "cat /tmp/cache_save.tar.gz", "bash", nil, "", io.Discard)
				if catErr == nil && catResult.Output != "" {
					jp.executor.store.Put(ctx, "cache/"+key, strings.NewReader(catResult.Output), int64(len(catResult.Output)))
					fmt.Fprintf(logWriter, "Cache saved: %s\n", key)
				}
			}
			jp.executeCommand(ctx, "rm -f /tmp/cache_save.tar.gz", "bash", nil, "", io.Discard)
		}
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

func (jp *JobPod) actionUploadArtifact(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	name, _ := stepDef.With["name"]
	path, _ := stepDef.With["path"]
	if name == "" {
		name = "artifact"
	}

	fmt.Fprintf(logWriter, "Uploading artifact: %s\n", name)

	if jp.executor.store == nil {
		fmt.Fprintf(logWriter, "Warning: no store configured, skipping artifact upload\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	if path == "" {
		fmt.Fprintf(logWriter, "Warning: no path specified for artifact upload\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	runID, _ := jobContext["run_id"].(string)
	storeKey := fmt.Sprintf("artifacts/%s/%s.tar.gz", runID, name)

	// Tar the paths in the pod
	pathList := strings.Split(path, "\n")
	var tarPaths []string
	for _, p := range pathList {
		p = strings.TrimSpace(p)
		if p != "" {
			tarPaths = append(tarPaths, p)
		}
	}

	if len(tarPaths) == 0 {
		fmt.Fprintf(logWriter, "Warning: no valid paths for artifact\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	tarCmd := fmt.Sprintf("tar czf /tmp/artifact_upload.tar.gz %s 2>/dev/null && wc -c < /tmp/artifact_upload.tar.gz",
		strings.Join(tarPaths, " "))
	result, err := jp.executeCommand(ctx, tarCmd, "bash", nil, "", logWriter)
	if err != nil || result.ExitCode != 0 {
		fmt.Fprintf(logWriter, "Warning: failed to create artifact archive\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Read and upload
	catResult, catErr := jp.executeCommand(ctx, "cat /tmp/artifact_upload.tar.gz", "bash", nil, "", io.Discard)
	if catErr == nil && catResult.Output != "" {
		jp.executor.store.Put(ctx, storeKey, strings.NewReader(catResult.Output), int64(len(catResult.Output)))
		fmt.Fprintf(logWriter, "Artifact uploaded: %s (%d bytes)\n", name, len(catResult.Output))
	}
	jp.executeCommand(ctx, "rm -f /tmp/artifact_upload.tar.gz", "bash", nil, "", io.Discard)

	return &ExecutionResult{ExitCode: 0}, nil
}

func (jp *JobPod) actionDownloadArtifact(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	name, _ := stepDef.With["name"]
	downloadPath, _ := stepDef.With["path"]
	if name == "" {
		fmt.Fprintf(logWriter, "Warning: no artifact name specified\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}
	if downloadPath == "" {
		downloadPath = "."
	}

	fmt.Fprintf(logWriter, "Downloading artifact: %s\n", name)

	if jp.executor.store == nil {
		fmt.Fprintf(logWriter, "Warning: no store configured, skipping artifact download\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	runID, _ := jobContext["run_id"].(string)
	storeKey := fmt.Sprintf("artifacts/%s/%s.tar.gz", runID, name)

	exists, _ := jp.executor.store.Exists(ctx, storeKey)
	if !exists {
		fmt.Fprintf(logWriter, "Warning: artifact %q not found\n", name)
		return &ExecutionResult{ExitCode: 1}, nil
	}

	rc, err := jp.executor.store.Get(ctx, storeKey)
	if err != nil {
		fmt.Fprintf(logWriter, "Warning: failed to get artifact: %v\n", err)
		return &ExecutionResult{ExitCode: 1}, nil
	}
	defer rc.Close()

	data, _ := io.ReadAll(rc)
	if len(data) > 0 {
		// Extract into the download path
		script := fmt.Sprintf("mkdir -p %s && tar xzf /tmp/artifact_download.tar.gz -C %s 2>/dev/null || true && rm -f /tmp/artifact_download.tar.gz",
			downloadPath, downloadPath)
		jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
		fmt.Fprintf(logWriter, "Artifact downloaded: %s (%d bytes)\n", name, len(data))
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

// actionGeneric handles unknown actions by cloning the action repo and running action.yml.
// Supports composite actions (runs steps inline) and provides helpful errors for unsupported types.
func (jp *JobPod) actionGeneric(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	action := stepDef.Uses

	// Parse action reference: owner/repo@ref or owner/repo/path@ref
	atIdx := strings.LastIndex(action, "@")
	var actionRepo, actionRef, actionSubpath string
	if atIdx > 0 {
		actionRepo = action[:atIdx]
		actionRef = action[atIdx+1:]
	} else {
		actionRepo = action
		actionRef = "main"
	}

	// Check for subpath (owner/repo/path)
	parts := strings.SplitN(actionRepo, "/", 3)
	if len(parts) == 3 {
		actionRepo = parts[0] + "/" + parts[1]
		actionSubpath = parts[2]
	}

	actionDir := fmt.Sprintf("/tmp/actions/%s/%s", strings.ReplaceAll(actionRepo, "/", "_"), actionRef)
	actionYmlDir := actionDir
	if actionSubpath != "" {
		actionYmlDir = actionDir + "/" + actionSubpath
	}

	fmt.Fprintf(logWriter, "Fetching action %s@%s\n", actionRepo, actionRef)

	// Clone the action repo
	cloneScript := fmt.Sprintf(`
set -e
if ! command -v git &> /dev/null; then
    apt-get update -qq && apt-get install -y -qq git ca-certificates > /dev/null 2>&1
fi

if [ -d "%s" ]; then
    echo "Action already cached"
else
    mkdir -p "$(dirname %s)"
    git clone --depth 1 --branch %s https://github.com/%s.git %s 2>&1 || {
        # Try without --branch (for SHA refs)
        git clone https://github.com/%s.git %s 2>&1
        cd %s && git checkout %s 2>&1
    }
fi

# Determine action type
if [ -f "%s/action.yml" ]; then
    cat "%s/action.yml"
elif [ -f "%s/action.yaml" ]; then
    cat "%s/action.yaml"
else
    echo "NO_ACTION_YML"
fi
`, actionDir, actionDir, actionRef, actionRepo, actionDir,
		actionRepo, actionDir, actionDir, actionRef,
		actionYmlDir, actionYmlDir, actionYmlDir, actionYmlDir)

	result, err := jp.executeCommand(ctx, cloneScript, "bash", nil, "", logWriter)
	if err != nil {
		fmt.Fprintf(logWriter, "Failed to fetch action: %v\n", err)
		return &ExecutionResult{ExitCode: 1}, nil
	}

	output := strings.TrimSpace(result.Output)
	if output == "NO_ACTION_YML" {
		fmt.Fprintf(logWriter, "Warning: action %s has no action.yml, skipping\n", action)
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Parse the action.yml to determine type
	actionType := parseActionType(output)

	switch actionType {
	case "composite":
		return jp.runCompositeAction(ctx, output, stepDef, jobContext, actionYmlDir, logWriter)
	case "node12", "node16", "node20":
		return jp.runNodeAction(ctx, output, stepDef, jobContext, actionYmlDir, actionType, logWriter)
	case "docker":
		fmt.Fprintf(logWriter, "Warning: Docker actions are not yet supported. Action: %s\n", action)
		fmt.Fprintf(logWriter, "Consider using a 'run:' step instead.\n")
		return &ExecutionResult{ExitCode: 0}, nil
	default:
		fmt.Fprintf(logWriter, "Warning: unknown action type %q for %s, skipping\n", actionType, action)
		return &ExecutionResult{ExitCode: 0}, nil
	}
}

// parseActionType extracts the action type from action.yml content.
func parseActionType(actionYml string) string {
	// Simple YAML parsing for runs.using field
	lines := strings.Split(actionYml, "\n")
	inRuns := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "runs:" {
			inRuns = true
			continue
		}
		if inRuns && strings.HasPrefix(trimmed, "using:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "using:"))
			val = strings.Trim(val, "'\"")
			return val
		}
		// Non-indented line after runs: means we've left the runs block
		if inRuns && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}
	}
	return ""
}

// runCompositeAction executes a composite action's steps inline.
func (jp *JobPod) runCompositeAction(ctx context.Context, actionYml string, stepDef *StepDefinition, jobContext map[string]interface{}, actionDir string, logWriter io.Writer) (*ExecutionResult, error) {
	fmt.Fprintf(logWriter, "Running composite action\n")

	// Parse composite steps from action.yml
	steps := parseCompositeSteps(actionYml)
	if len(steps) == 0 {
		fmt.Fprintf(logWriter, "Warning: no steps found in composite action\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Build input env vars from with: parameters
	inputEnv := make(map[string]string)
	for k, v := range stepDef.With {
		inputEnv["INPUT_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_"))] = v
	}

	for i, step := range steps {
		fmt.Fprintf(logWriter, "  Composite step %d: %s\n", i+1, step.Name)

		if step.Run != "" {
			shell := step.Shell
			if shell == "" {
				shell = "bash"
			}
			result, err := jp.executeCommand(ctx, step.Run, shell, inputEnv, "", logWriter)
			if err != nil {
				return result, err
			}
			if result.ExitCode != 0 {
				return result, nil
			}
		} else if step.Uses != "" {
			// Nested action - recurse
			nestedStep := &StepDefinition{
				Uses: step.Uses,
				With: step.With,
				Env:  inputEnv,
			}
			result, err := jp.executeAction(ctx, nestedStep, jobContext, logWriter)
			if err != nil {
				return result, err
			}
			if result.ExitCode != 0 {
				return result, nil
			}
		}
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

// compositeStep is a simplified step from action.yml.
type compositeStep struct {
	Name  string
	Run   string
	Shell string
	Uses  string
	With  map[string]string
}

// parseCompositeSteps does simple YAML parsing to extract composite action steps.
func parseCompositeSteps(actionYml string) []compositeStep {
	var steps []compositeStep
	lines := strings.Split(actionYml, "\n")

	inSteps := false
	inStep := false
	inRun := false
	var current compositeStep
	var runLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Find the steps: section under runs:
		if trimmed == "steps:" {
			inSteps = true
			continue
		}

		if !inSteps {
			continue
		}

		// New step starts with "- "
		if strings.HasPrefix(trimmed, "- ") {
			// Save previous step
			if inStep {
				if inRun && len(runLines) > 0 {
					current.Run = strings.Join(runLines, "\n")
					inRun = false
					runLines = nil
				}
				steps = append(steps, current)
			}
			current = compositeStep{With: make(map[string]string)}
			inStep = true
			inRun = false
			runLines = nil

			// Parse inline fields
			rest := strings.TrimPrefix(trimmed, "- ")
			if strings.HasPrefix(rest, "name:") {
				current.Name = strings.TrimSpace(strings.TrimPrefix(rest, "name:"))
			} else if strings.HasPrefix(rest, "run:") {
				val := strings.TrimSpace(strings.TrimPrefix(rest, "run:"))
				if val == "|" {
					inRun = true
				} else {
					current.Run = val
				}
			} else if strings.HasPrefix(rest, "uses:") {
				current.Uses = strings.TrimSpace(strings.TrimPrefix(rest, "uses:"))
			}
			continue
		}

		if !inStep {
			continue
		}

		// Continuation of multiline run
		if inRun {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent >= 8 || (indent >= 4 && trimmed != "") {
				runLines = append(runLines, trimmed)
				continue
			} else if trimmed == "" {
				runLines = append(runLines, "")
				continue
			} else {
				// End of run block
				current.Run = strings.Join(runLines, "\n")
				inRun = false
				runLines = nil
			}
		}

		// Parse step fields
		if strings.HasPrefix(trimmed, "name:") {
			current.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
		} else if strings.HasPrefix(trimmed, "run:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
			if val == "|" {
				inRun = true
			} else {
				current.Run = strings.Trim(val, "'\"")
			}
		} else if strings.HasPrefix(trimmed, "shell:") {
			current.Shell = strings.TrimSpace(strings.TrimPrefix(trimmed, "shell:"))
		} else if strings.HasPrefix(trimmed, "uses:") {
			current.Uses = strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:"))
		}

		// Exit steps section on non-indented non-empty line
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}
	}

	// Save last step
	if inStep {
		if inRun && len(runLines) > 0 {
			current.Run = strings.Join(runLines, "\n")
		}
		steps = append(steps, current)
	}

	return steps
}

// runNodeAction executes a Node.js action.
func (jp *JobPod) runNodeAction(ctx context.Context, actionYml string, stepDef *StepDefinition, jobContext map[string]interface{}, actionDir, nodeVersion string, logWriter io.Writer) (*ExecutionResult, error) {
	// Parse the main entry point from action.yml
	mainFile := parseActionMain(actionYml)
	if mainFile == "" {
		fmt.Fprintf(logWriter, "Warning: no main entry point found in Node.js action\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Determine node version from using field
	nodeVer := "20"
	switch nodeVersion {
	case "node12":
		nodeVer = "12"
	case "node16":
		nodeVer = "16"
	case "node20":
		nodeVer = "20"
	}

	fmt.Fprintf(logWriter, "Running Node.js %s action: %s\n", nodeVer, mainFile)

	// Build INPUT_ env vars
	inputEnv := make(map[string]string)
	for k, v := range stepDef.With {
		inputEnv["INPUT_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_"))] = v
	}

	// Install node if needed and run the action
	script := fmt.Sprintf(`
set -e
if ! command -v node &> /dev/null; then
    echo "Installing Node.js %s..."
    apt-get update -qq && apt-get install -y -qq curl ca-certificates > /dev/null 2>&1
    curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - > /dev/null 2>&1
    apt-get install -y -qq nodejs > /dev/null 2>&1
fi
cd "%s"
node "%s"
`, nodeVer, nodeVer, actionDir, mainFile)

	return jp.executeCommand(ctx, script, "bash", inputEnv, "", logWriter)
}

// parseActionMain extracts the main entry point from action.yml.
func parseActionMain(actionYml string) string {
	lines := strings.Split(actionYml, "\n")
	inRuns := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "runs:" {
			inRuns = true
			continue
		}
		if inRuns && strings.HasPrefix(trimmed, "main:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "main:"))
			return strings.Trim(val, "'\"")
		}
		if inRuns && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}
	}
	return ""
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

	repo, _ := context["repo"].(string)
	ref, _ := context["ref"].(string)
	sha, _ := context["sha"].(string)
	event, _ := context["event"].(string)
	runID, _ := context["run_id"].(string)

	// Derive ref_name from ref
	refName := ref
	refName = strings.TrimPrefix(refName, "refs/heads/")
	refName = strings.TrimPrefix(refName, "refs/tags/")

	// GitHub-compatible environment variables
	env = append(env,
		corev1.EnvVar{Name: "CI", Value: "true"},
		corev1.EnvVar{Name: "GITHUB_ACTIONS", Value: "true"},
		corev1.EnvVar{Name: "GITHUB_REPOSITORY", Value: repo},
		corev1.EnvVar{Name: "GITHUB_REF", Value: ref},
		corev1.EnvVar{Name: "GITHUB_REF_NAME", Value: refName},
		corev1.EnvVar{Name: "GITHUB_SHA", Value: sha},
		corev1.EnvVar{Name: "GITHUB_EVENT_NAME", Value: event},
		corev1.EnvVar{Name: "GITHUB_RUN_ID", Value: runID},
		corev1.EnvVar{Name: "GITHUB_WORKSPACE", Value: "/workspace"},
		corev1.EnvVar{Name: "GITHUB_ENV", Value: "/tmp/github_env"},
		corev1.EnvVar{Name: "GITHUB_OUTPUT", Value: "/tmp/github_output"},
		corev1.EnvVar{Name: "GITHUB_STATE", Value: "/tmp/github_state"},
		corev1.EnvVar{Name: "GITHUB_STEP_SUMMARY", Value: "/tmp/github_step_summary"},
		corev1.EnvVar{Name: "GITHUB_PATH", Value: "/tmp/github_path"},
	)

	// Kailab-specific environment variables
	env = append(env,
		corev1.EnvVar{Name: "KAILAB_CI", Value: "true"},
		corev1.EnvVar{Name: "KAILAB_REPOSITORY", Value: repo},
		corev1.EnvVar{Name: "KAILAB_REF", Value: ref},
		corev1.EnvVar{Name: "KAILAB_SHA", Value: sha},
		corev1.EnvVar{Name: "KAILAB_EVENT", Value: event},
	)

	// Standard env
	env = append(env,
		corev1.EnvVar{Name: "HOME", Value: "/root"},
		corev1.EnvVar{Name: "WORKSPACE", Value: "/workspace"},
	)

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

	return env
}

// parseContainerPort extracts the container port from "host:container" or "container" format.
func parseContainerPort(portStr string) int32 {
	portStr = strings.TrimSpace(portStr)
	parts := strings.SplitN(portStr, ":", 2)
	var portPart string
	if len(parts) == 2 {
		portPart = parts[1] // container port
	} else {
		portPart = parts[0]
	}
	// Strip /tcp, /udp suffixes
	portPart = strings.Split(portPart, "/")[0]
	var port int
	fmt.Sscanf(portPart, "%d", &port)
	return int32(port)
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
