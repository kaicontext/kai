package runner

import (
	"bytes"
	"context"
	"encoding/base64"
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
	client             *kubernetes.Clientset
	config             *rest.Config
	namespace          string
	store              store.Store
	serviceAccountName string
}

// NewExecutor creates a new Kubernetes executor.
func NewExecutor(namespace, kubeconfig, serviceAccountName string, ciStore store.Store) (*Executor, error) {
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
		client:             client,
		config:             config,
		namespace:          namespace,
		store:              ciStore,
		serviceAccountName: serviceAccountName,
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
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: e.serviceAccountName,
			Containers:         containers,
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
	case strings.HasPrefix(action, "docker/setup-qemu-action"):
		return jp.actionDockerSetupQemu(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "docker/setup-buildx-action"):
		return jp.actionDockerSetupBuildx(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "docker/login-action"):
		return jp.actionDockerLogin(ctx, stepDef, logWriter)
	case strings.HasPrefix(action, "docker/metadata-action"):
		return jp.actionDockerMetadata(ctx, stepDef, jobContext, logWriter)
	case strings.HasPrefix(action, "docker/build-push-action"):
		return jp.actionDockerBuildPush(ctx, stepDef, logWriter)
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

	// Try to restore cache: exact key first, then restore-keys prefix matching
	cacheKey := "cache/" + key
	exists, _ := jp.executor.store.Exists(ctx, cacheKey)
	if !exists && restoreKeys != "" {
		// Try restore-keys with real prefix matching via ListByPrefix
		for _, rk := range strings.Split(restoreKeys, "\n") {
			rk = strings.TrimSpace(rk)
			if rk == "" {
				continue
			}
			prefixKey := "cache/" + rk
			keys, err := jp.executor.store.ListByPrefix(ctx, prefixKey)
			if err != nil {
				fmt.Fprintf(logWriter, "Cache prefix search error: %v\n", err)
				continue
			}
			if len(keys) > 0 {
				// ListByPrefix returns most recent first
				cacheKey = keys[0]
				exists = true
				fmt.Fprintf(logWriter, "Cache restored from prefix key: %s (matched %d keys)\n", cacheKey, len(keys))
				break
			}
		}
	}

	// Track whether this is an exact key match (not a restore-key fallback)
	exactHit := exists && cacheKey == "cache/"+key

	if exists {
		fmt.Fprintf(logWriter, "Cache hit, restoring...\n")
		rc, err := jp.executor.store.Get(ctx, cacheKey)
		if err != nil {
			fmt.Fprintf(logWriter, "Cache restore failed: %v\n", err)
			return &ExecutionResult{ExitCode: 0}, nil
		}
		defer rc.Close()

		// Transfer binary tar data via base64 to avoid corruption
		tarData, _ := io.ReadAll(rc)
		if len(tarData) > 0 {
			encoded := base64Encode(tarData)
			// Write base64 data to file in pod, decode, extract
			// Split into chunks to avoid command-line length limits
			chunkSize := 65536
			writeScript := "rm -f /tmp/cache_restore.b64\n"
			for i := 0; i < len(encoded); i += chunkSize {
				end := i + chunkSize
				if end > len(encoded) {
					end = len(encoded)
				}
				writeScript += fmt.Sprintf("printf '%%s' %q >> /tmp/cache_restore.b64\n", encoded[i:end])
			}
			writeScript += `
base64 -d /tmp/cache_restore.b64 > /tmp/cache_restore.tar.gz 2>/dev/null
tar xzf /tmp/cache_restore.tar.gz -C / 2>/dev/null || true
rm -f /tmp/cache_restore.tar.gz /tmp/cache_restore.b64
echo "Cache restored"
`
			_, err := jp.executeCommand(ctx, writeScript, "bash", nil, "", logWriter)
			if err != nil {
				fmt.Fprintf(logWriter, "Cache extract failed: %v\n", err)
			}
		}
		fmt.Fprintf(logWriter, "Cache restored successfully (%d bytes)\n", len(tarData))
	} else {
		fmt.Fprintf(logWriter, "Cache miss\n")
	}

	// Write cache-hit output so steps can check: if steps.cache.outputs.cache-hit != 'true'
	// cache-hit is only 'true' on exact key match, not on restore-key prefix fallback
	cacheHitValue := "false"
	if exactHit {
		cacheHitValue = "true"
	}
	jp.executeCommand(ctx, fmt.Sprintf("echo 'cache-hit=%s' >> /tmp/github_output", cacheHitValue), "bash", nil, "", io.Discard)

	// Save cache: tar the paths, base64-encode, read back, decode, and upload
	// Save even on hit (to update the key if restore-keys was used with a different key)
	shouldSave := !exists || cacheKey != "cache/"+key
	if shouldSave && paths != "" {
		pathList := parseMultiline(paths)
		if len(pathList) > 0 {
			// Quote each path for the tar command
			var quotedPaths []string
			for _, p := range pathList {
				quotedPaths = append(quotedPaths, fmt.Sprintf("%q", p))
			}
			// Create tar, base64 encode, and output
			tarCmd := fmt.Sprintf(
				"tar czf /tmp/cache_save.tar.gz -C / %s 2>/dev/null && base64 /tmp/cache_save.tar.gz && rm -f /tmp/cache_save.tar.gz",
				strings.Join(quotedPaths, " "))
			result, err := jp.executeCommand(ctx, tarCmd, "bash", nil, "", io.Discard)
			if err == nil && result.ExitCode == 0 && result.Output != "" {
				decoded := base64Decode(strings.TrimSpace(result.Output))
				if len(decoded) > 0 {
					err := jp.executor.store.Put(ctx, "cache/"+key, bytes.NewReader(decoded), int64(len(decoded)))
					if err != nil {
						fmt.Fprintf(logWriter, "Cache save error: %v\n", err)
					} else {
						fmt.Fprintf(logWriter, "Cache saved: %s (%d bytes)\n", key, len(decoded))
					}
				}
			}
		}
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

func (jp *JobPod) actionUploadArtifact(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	name, _ := stepDef.With["name"]
	path, _ := stepDef.With["path"]
	mergeMultiple, _ := stepDef.With["merge-multiple"]
	_ = mergeMultiple
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
	pathList := parseMultiline(path)
	if len(pathList) == 0 {
		fmt.Fprintf(logWriter, "Warning: no valid paths for artifact\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	var quotedPaths []string
	for _, p := range pathList {
		quotedPaths = append(quotedPaths, fmt.Sprintf("%q", p))
	}

	// Create tar and base64-encode to avoid binary corruption
	tarCmd := fmt.Sprintf("tar czf /tmp/artifact_upload.tar.gz %s 2>/dev/null && base64 /tmp/artifact_upload.tar.gz && rm -f /tmp/artifact_upload.tar.gz",
		strings.Join(quotedPaths, " "))
	result, err := jp.executeCommand(ctx, tarCmd, "bash", nil, "", io.Discard)
	if err != nil || result.ExitCode != 0 {
		fmt.Fprintf(logWriter, "Warning: failed to create artifact archive\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	decoded := base64Decode(strings.TrimSpace(result.Output))
	if len(decoded) > 0 {
		err := jp.executor.store.Put(ctx, storeKey, bytes.NewReader(decoded), int64(len(decoded)))
		if err != nil {
			fmt.Fprintf(logWriter, "Warning: artifact upload failed: %v\n", err)
		} else {
			fmt.Fprintf(logWriter, "Artifact uploaded: %s (%d bytes)\n", name, len(decoded))
		}
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

func (jp *JobPod) actionDownloadArtifact(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	name, _ := stepDef.With["name"]
	downloadPath, _ := stepDef.With["path"]
	mergeMultiple, _ := stepDef.With["merge-multiple"]
	if name == "" && mergeMultiple != "true" {
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

	// If merge-multiple is true and no specific name, download all artifacts for this run
	var artifactKeys []string
	if mergeMultiple == "true" && name == "" {
		prefix := fmt.Sprintf("artifacts/%s/", runID)
		keys, err := jp.executor.store.ListByPrefix(ctx, prefix)
		if err == nil {
			artifactKeys = keys
		}
	} else {
		artifactKeys = []string{fmt.Sprintf("artifacts/%s/%s.tar.gz", runID, name)}
	}

	for _, storeKey := range artifactKeys {
		exists, _ := jp.executor.store.Exists(ctx, storeKey)
		if !exists {
			fmt.Fprintf(logWriter, "Warning: artifact %q not found\n", storeKey)
			continue
		}

		rc, err := jp.executor.store.Get(ctx, storeKey)
		if err != nil {
			fmt.Fprintf(logWriter, "Warning: failed to get artifact: %v\n", err)
			continue
		}

		data, _ := io.ReadAll(rc)
		rc.Close()

		if len(data) > 0 {
			// Transfer via base64 to avoid binary corruption
			encoded := base64Encode(data)
			chunkSize := 65536
			writeScript := fmt.Sprintf("mkdir -p %q\nrm -f /tmp/artifact_download.b64\n", downloadPath)
			for i := 0; i < len(encoded); i += chunkSize {
				end := i + chunkSize
				if end > len(encoded) {
					end = len(encoded)
				}
				writeScript += fmt.Sprintf("printf '%%s' %q >> /tmp/artifact_download.b64\n", encoded[i:end])
			}
			writeScript += fmt.Sprintf(`
base64 -d /tmp/artifact_download.b64 > /tmp/artifact_download.tar.gz 2>/dev/null
tar xzf /tmp/artifact_download.tar.gz -C %q 2>/dev/null || true
rm -f /tmp/artifact_download.tar.gz /tmp/artifact_download.b64
`, downloadPath)
			jp.executeCommand(ctx, writeScript, "bash", nil, "", logWriter)
			fmt.Fprintf(logWriter, "Artifact downloaded: %s (%d bytes)\n", storeKey, len(data))
		}
	}

	return &ExecutionResult{ExitCode: 0}, nil
}

// Docker action handlers

func (jp *JobPod) actionDockerSetupQemu(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	fmt.Fprintf(logWriter, "Setting up QEMU (multi-arch support)\n")

	script := `
set -e
echo "==> docker/setup-qemu-action"
if command -v qemu-aarch64-static &> /dev/null; then
    echo "QEMU already installed"
else
    echo "Installing qemu-user-static..."
    apt-get update -qq && apt-get install -y -qq qemu-user-static binfmt-support > /dev/null 2>&1 || {
        echo "Warning: could not install QEMU (may need privileged mode). Continuing without multi-arch support."
    }
fi
echo "QEMU setup complete"
`
	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionDockerSetupBuildx(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	fmt.Fprintf(logWriter, "Setting up Buildah (Buildx equivalent)\n")

	script := `
set -e
echo "==> docker/setup-buildx-action (using Buildah)"

if command -v buildah &> /dev/null; then
    echo "Buildah already installed: $(buildah --version)"
else
    echo "Installing Buildah..."
    apt-get update -qq && apt-get install -y -qq buildah fuse-overlayfs > /dev/null 2>&1 || {
        # Fallback: try adding the Kubic repo for newer Ubuntu
        . /etc/os-release
        echo "deb https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/xUbuntu_${VERSION_ID}/ /" > /etc/apt/sources.list.d/devel:kubic.list 2>/dev/null || true
        apt-get update -qq 2>/dev/null
        apt-get install -y -qq buildah > /dev/null 2>&1
    }
fi

# Configure Buildah for unprivileged operation with VFS storage driver
mkdir -p /etc/containers
cat > /etc/containers/storage.conf << 'STORAGEEOF'
[storage]
driver = "vfs"
STORAGEEOF

# Configure default policy to allow all images
if [ ! -f /etc/containers/policy.json ]; then
    cat > /etc/containers/policy.json << 'POLICYEOF'
{"default":[{"type":"insecureAcceptAnything"}]}
POLICYEOF
fi

# Configure registries
if [ ! -f /etc/containers/registries.conf ]; then
    cat > /etc/containers/registries.conf << 'REGEOF'
[registries.search]
registries = ['docker.io']
REGEOF
fi

buildah --version
echo "Buildah setup complete (VFS storage driver)"
`
	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionDockerLogin(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	registry := stepDef.With["registry"]
	username := stepDef.With["username"]
	password := stepDef.With["password"]

	if registry == "" {
		registry = "docker.io"
	}

	fmt.Fprintf(logWriter, "Logging in to %s\n", registry)

	// Detect GCP Artifact Registry and use Workload Identity
	if strings.Contains(registry, "-docker.pkg.dev") && username == "" && password == "" {
		script := fmt.Sprintf(`
set -e
echo "==> docker/login-action (Artifact Registry via Workload Identity)"

# Install gcloud if not available
if ! command -v gcloud &> /dev/null; then
    echo "Installing gcloud CLI..."
    apt-get update -qq && apt-get install -y -qq curl ca-certificates apt-transport-https gnupg > /dev/null 2>&1
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" > /etc/apt/sources.list.d/google-cloud-sdk.list
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg 2>/dev/null
    apt-get update -qq && apt-get install -y -qq google-cloud-cli > /dev/null 2>&1
fi

# Get access token via Workload Identity (metadata server)
echo "Fetching Workload Identity token..."
TOKEN=$(gcloud auth print-access-token 2>/dev/null) || {
    echo "Warning: gcloud auth failed, trying metadata server directly..."
    TOKEN=$(curl -s -H "Metadata-Flavor: Google" \
        "http://169.254.169.254/computeMetadata/v1/instance/service-accounts/default/token" | \
        python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])" 2>/dev/null) || {
        # Last resort: try with jq
        TOKEN=$(curl -s -H "Metadata-Flavor: Google" \
            "http://169.254.169.254/computeMetadata/v1/instance/service-accounts/default/token" | \
            grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
    }
}

if [ -z "$TOKEN" ]; then
    echo "Error: could not obtain access token"
    exit 1
fi

# Install buildah if not available (login-action may run before setup-buildx)
if ! command -v buildah &> /dev/null; then
    apt-get update -qq && apt-get install -y -qq buildah > /dev/null 2>&1
fi

echo "$TOKEN" | buildah login --storage-driver=vfs -u oauth2accesstoken --password-stdin %s
echo "Login to %s successful"
`, registry, registry)

		return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
	}

	// Explicit username/password login
	if username == "" || password == "" {
		fmt.Fprintf(logWriter, "Warning: no credentials provided for %s, skipping login\n", registry)
		return &ExecutionResult{ExitCode: 0}, nil
	}

	script := fmt.Sprintf(`
set -e
echo "==> docker/login-action"

# Install buildah if not available
if ! command -v buildah &> /dev/null; then
    apt-get update -qq && apt-get install -y -qq buildah > /dev/null 2>&1
fi

echo %q | buildah login --storage-driver=vfs -u %q --password-stdin %s
echo "Login to %s successful"
`, password, username, registry, registry)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionDockerMetadata(ctx context.Context, stepDef *StepDefinition, jobContext map[string]interface{}, logWriter io.Writer) (*ExecutionResult, error) {
	images := stepDef.With["images"]
	tags := stepDef.With["tags"]

	fmt.Fprintf(logWriter, "Generating Docker metadata\n")

	ref, _ := jobContext["ref"].(string)
	sha, _ := jobContext["sha"].(string)
	defaultBranch, _ := jobContext["default_branch"].(string)

	// Derive ref name
	refName := ref
	refName = strings.TrimPrefix(refName, "refs/heads/")
	refName = strings.TrimPrefix(refName, "refs/tags/")

	isTag := strings.HasPrefix(ref, "refs/tags/")
	isDefaultBranch := refName == defaultBranch || (defaultBranch == "" && refName == "main")

	// Parse image list
	imageList := parseMultiline(images)
	if len(imageList) == 0 {
		fmt.Fprintf(logWriter, "Warning: no images specified\n")
		return &ExecutionResult{ExitCode: 0}, nil
	}

	// Parse tag rules and generate tags
	tagRules := parseMultiline(tags)
	var generatedTags []string
	var labels []string

	for _, rule := range tagRules {
		parsed := parseTagRule(rule)
		tagType := parsed["type"]
		enabled := parsed["enable"]

		// Check enable conditions
		if enabled != "" && !evaluateMetadataCondition(enabled, isDefaultBranch, isTag) {
			continue
		}

		var tagValue string
		switch tagType {
		case "sha":
			prefix := parsed["prefix"]
			if prefix == "" {
				prefix = "sha-"
			}
			tagValue = prefix + sha[:minInt(7, len(sha))]
		case "ref":
			if event := parsed["event"]; event == "branch" || event == "" {
				tagValue = sanitizeDockerTag(refName)
			} else if event == "tag" && isTag {
				tagValue = refName
			}
		case "raw":
			tagValue = parsed["value"]
		case "semver":
			if isTag {
				pattern := parsed["pattern"]
				tagValue = applySemverPattern(pattern, refName)
			}
		case "schedule":
			tagValue = parsed["pattern"]
			if tagValue == "" {
				tagValue = "nightly"
			}
		default:
			// Default: treat the whole rule as a raw tag if it doesn't look like type=
			if !strings.Contains(rule, "type=") {
				tagValue = strings.TrimSpace(rule)
			}
		}

		if tagValue == "" {
			continue
		}

		// Apply tag to all images
		for _, img := range imageList {
			generatedTags = append(generatedTags, img+":"+tagValue)
		}
	}

	// If no tag rules produced anything, apply defaults
	if len(generatedTags) == 0 {
		tag := sanitizeDockerTag(refName)
		if tag == "" {
			tag = "latest"
		}
		for _, img := range imageList {
			generatedTags = append(generatedTags, img+":"+tag)
		}
	}

	// Generate OCI labels
	labels = append(labels,
		fmt.Sprintf("org.opencontainers.image.revision=%s", sha),
		fmt.Sprintf("org.opencontainers.image.source=%s", ref),
	)

	// Write to GITHUB_OUTPUT
	tagsOutput := strings.Join(generatedTags, "\n")
	labelsOutput := strings.Join(labels, "\n")

	script := fmt.Sprintf(`
echo "==> docker/metadata-action"
cat >> /tmp/github_output << 'METAEOF'
tags<<TAGDELIM
%s
TAGDELIM
labels<<LABELDELIM
%s
LABELDELIM
METAEOF

echo "Generated tags:"
echo %q
echo "Generated labels:"
echo %q
`, tagsOutput, labelsOutput, tagsOutput, labelsOutput)

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

func (jp *JobPod) actionDockerBuildPush(ctx context.Context, stepDef *StepDefinition, logWriter io.Writer) (*ExecutionResult, error) {
	dockerContext := stepDef.With["context"]
	dockerfile := stepDef.With["file"]
	tagsRaw := stepDef.With["tags"]
	labelsRaw := stepDef.With["labels"]
	buildArgs := stepDef.With["build-args"]
	target := stepDef.With["target"]
	push := stepDef.With["push"]
	platforms := stepDef.With["platforms"]
	cacheFrom := stepDef.With["cache-from"]
	cacheTo := stepDef.With["cache-to"]

	if dockerContext == "" {
		dockerContext = "."
	}
	if dockerfile == "" {
		dockerfile = dockerContext + "/Dockerfile"
	}

	fmt.Fprintf(logWriter, "Building Docker image with Buildah\n")

	// Build tag arguments
	tags := parseMultiline(tagsRaw)
	var tagArgs string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			tagArgs += fmt.Sprintf(" -t %q", t)
		}
	}

	// Build label arguments
	labels := parseMultiline(labelsRaw)
	var labelArgs string
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l != "" {
			labelArgs += fmt.Sprintf(" --label %q", l)
		}
	}

	// Build build-arg arguments
	args := parseMultiline(buildArgs)
	var buildArgArgs string
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a != "" {
			buildArgArgs += fmt.Sprintf(" --build-arg %q", a)
		}
	}

	// Target stage
	var targetArg string
	if target != "" {
		targetArg = fmt.Sprintf(" --target %q", target)
	}

	// Platform argument (Buildah supports --platform)
	var platformArg string
	if platforms != "" {
		platformArg = fmt.Sprintf(" --platform %q", platforms)
	}

	// Layer caching: translate GitHub Actions cache directives to Buildah --layers
	var cacheArgs string
	if cacheFrom != "" || cacheTo != "" {
		// Buildah supports --layers for layer caching
		cacheArgs = " --layers"
		fmt.Fprintf(logWriter, "Layer caching enabled (--layers)\n")
		if cacheFrom != "" {
			fmt.Fprintf(logWriter, "Note: cache-from=%s mapped to Buildah --layers (GHA cache type not directly supported)\n", cacheFrom)
		}
	}

	script := fmt.Sprintf(`
set -e
echo "==> docker/build-push-action (using Buildah)"

# Ensure buildah is available
if ! command -v buildah &> /dev/null; then
    echo "Error: buildah not installed. Add docker/setup-buildx-action step first."
    exit 1
fi

echo "Building image..."
echo "  Context: %s"
echo "  Dockerfile: %s"

buildah bud --storage-driver=vfs \
    -f %q \
    %s%s%s%s%s%s \
    %s

echo "Build complete"
`, dockerContext, dockerfile, dockerfile, tagArgs, labelArgs, buildArgArgs, targetArg, platformArg, cacheArgs, dockerContext)

	// Add push commands if push=true
	if push == "true" {
		for _, t := range tags {
			t = strings.TrimSpace(t)
			if t != "" {
				script += fmt.Sprintf(`
echo "Pushing %s..."
buildah push --storage-driver=vfs %q
`, t, t)
			}
		}
		script += `echo "Push complete"
`
	}

	return jp.executeCommand(ctx, script, "bash", nil, "", logWriter)
}

// Docker metadata helpers

// parseMultiline splits a string on newlines and trims whitespace.
func parseMultiline(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// parseTagRule parses a tag rule like "type=sha,prefix=sha-" into a map.
func parseTagRule(rule string) map[string]string {
	result := make(map[string]string)
	for _, part := range strings.Split(rule, ",") {
		part = strings.TrimSpace(part)
		if eqIdx := strings.Index(part, "="); eqIdx >= 0 {
			result[part[:eqIdx]] = part[eqIdx+1:]
		}
	}
	return result
}

// evaluateMetadataCondition handles simple metadata enable conditions.
func evaluateMetadataCondition(expr string, isDefault, isTag bool) bool {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "true":
		return true
	case "false":
		return false
	case "{{is_default_branch}}":
		return isDefault
	}
	return true
}

// sanitizeDockerTag makes a string safe for use as a Docker tag.
func sanitizeDockerTag(s string) string {
	var result []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	// Docker tags can't start with . or -
	for len(result) > 0 && (result[0] == '.' || result[0] == '-') {
		result = result[1:]
	}
	if len(result) == 0 {
		return "latest"
	}
	// Limit to 128 chars
	if len(result) > 128 {
		result = result[:128]
	}
	return string(result)
}

// applySemverPattern applies a semver pattern to a version string.
func applySemverPattern(pattern, version string) string {
	// Strip v prefix for parsing
	v := strings.TrimPrefix(version, "v")
	parts := strings.SplitN(v, ".", 3)

	major := ""
	minor := ""
	patch := ""
	if len(parts) >= 1 {
		major = parts[0]
	}
	if len(parts) >= 2 {
		minor = parts[1]
	}
	if len(parts) >= 3 {
		patch = parts[2]
	}

	if pattern == "" {
		pattern = "{{version}}"
	}

	result := pattern
	result = strings.ReplaceAll(result, "{{version}}", v)
	result = strings.ReplaceAll(result, "{{major}}", major)
	result = strings.ReplaceAll(result, "{{minor}}", minor)
	result = strings.ReplaceAll(result, "{{patch}}", patch)
	result = strings.ReplaceAll(result, "{{major}}.{{minor}}", major+"."+minor)
	result = strings.ReplaceAll(result, "{{major}}.{{minor}}.{{patch}}", v)
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	actor, _ := context["actor"].(string)
	workflowName, _ := context["workflow_name"].(string)
	jobName, _ := context["job_name"].(string)
	serverURL, _ := context["server_url"].(string)

	// Run number (comes as float64 from JSON)
	runNumber := "1"
	if n, ok := context["run_number"].(float64); ok {
		runNumber = fmt.Sprintf("%d", int(n))
	} else if n, ok := context["run_number"].(int); ok {
		runNumber = fmt.Sprintf("%d", n)
	}

	// Derive ref_name from ref
	refName := ref
	refName = strings.TrimPrefix(refName, "refs/heads/")
	refName = strings.TrimPrefix(refName, "refs/tags/")

	// Derive head_ref and base_ref for pull requests
	headRef := ""
	baseRef := ""
	if event == "pull_request" || event == "review_created" || event == "review_updated" {
		headRef = refName
		if eventData, ok := context["event_data"].(map[string]interface{}); ok {
			if br, ok := eventData["base_ref"].(string); ok {
				baseRef = br
			}
		}
	}

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
		corev1.EnvVar{Name: "GITHUB_RUN_NUMBER", Value: runNumber},
		corev1.EnvVar{Name: "GITHUB_RUN_ATTEMPT", Value: "1"},
		corev1.EnvVar{Name: "GITHUB_ACTOR", Value: actor},
		corev1.EnvVar{Name: "GITHUB_TRIGGERING_ACTOR", Value: actor},
		corev1.EnvVar{Name: "GITHUB_WORKFLOW", Value: workflowName},
		corev1.EnvVar{Name: "GITHUB_JOB", Value: jobName},
		corev1.EnvVar{Name: "GITHUB_SERVER_URL", Value: serverURL},
		corev1.EnvVar{Name: "GITHUB_API_URL", Value: serverURL + "/api/v1"},
		corev1.EnvVar{Name: "GITHUB_HEAD_REF", Value: headRef},
		corev1.EnvVar{Name: "GITHUB_BASE_REF", Value: baseRef},
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

	// Add workflow/job-level env vars
	if wfEnv, ok := context["workflow_env"].(map[string]string); ok {
		for name, value := range wfEnv {
			env = append(env, corev1.EnvVar{Name: name, Value: value})
		}
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

// base64Encode encodes binary data to base64 string.
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// base64Decode decodes a base64 string to binary data.
func base64Decode(s string) []byte {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return data
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
