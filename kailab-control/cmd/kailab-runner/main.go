// kailab-runner is a CI runner that executes workflow jobs in Kubernetes.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kailab-control/internal/runner"
)

func main() {
	// Parse flags
	var (
		controlPlane = flag.String("control-plane", getEnv("KR_CONTROL_PLANE", "http://localhost:8080"), "Control plane URL")
		runnerName   = flag.String("name", getEnv("KR_NAME", "runner-1"), "Runner name")
		runnerID     = flag.String("id", getEnv("KR_ID", ""), "Runner ID (leave empty to auto-register)")
		namespace    = flag.String("namespace", getEnv("KR_NAMESPACE", "kailab-ci"), "Kubernetes namespace for job pods")
		pollInterval = flag.Duration("poll-interval", getDurationEnv("KR_POLL_INTERVAL", 5*time.Second), "Job poll interval")
		labels       = flag.String("labels", getEnv("KR_LABELS", ""), "Comma-separated runner labels")
		kubeconfig   = flag.String("kubeconfig", getEnv("KUBECONFIG", ""), "Path to kubeconfig (uses in-cluster config if empty)")
		gcsBucket    = flag.String("gcs-bucket", getEnv("KR_GCS_BUCKET", ""), "GCS bucket for CI caches/artifacts (uses local store if empty)")
		gcsPrefix    = flag.String("gcs-prefix", getEnv("KR_GCS_PREFIX", "ci"), "GCS key prefix")
		storePath      = flag.String("store-path", getEnv("KR_STORE_PATH", "/tmp/kailab-ci-store"), "Local store path (used when GCS is not configured)")
		serviceAccount = flag.String("service-account", getEnv("KR_SERVICE_ACCOUNT", "kailab-runner"), "Kubernetes service account for job pods")
	)
	flag.Parse()

	// Build config
	cfg := &runner.Config{
		ControlPlaneURL:    *controlPlane,
		RunnerName:         *runnerName,
		RunnerID:           *runnerID,
		Namespace:          *namespace,
		PollInterval:       *pollInterval,
		Labels:             parseLabels(*labels),
		Kubeconfig:         *kubeconfig,
		GCSBucket:          *gcsBucket,
		GCSPrefix:          *gcsPrefix,
		StorePath:          *storePath,
		ServiceAccountName: *serviceAccount,
	}

	// Create runner
	r, err := runner.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create runner: %v", err)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	// Run
	log.Printf("Starting kailab-runner %s", cfg.RunnerName)
	log.Printf("Control plane: %s", cfg.ControlPlaneURL)
	log.Printf("Namespace: %s", cfg.Namespace)
	log.Printf("Labels: %v", cfg.Labels)

	if err := r.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Runner error: %v", err)
	}

	log.Println("Runner stopped")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultVal
}

func parseLabels(s string) []string {
	if s == "" {
		return []string{}
	}
	var labels []string
	for _, l := range splitAndTrim(s, ",") {
		if l != "" {
			labels = append(labels, l)
		}
	}
	return labels
}

func splitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range split(s, sep) {
		trimmed := trim(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func split(s, sep string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trim(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
