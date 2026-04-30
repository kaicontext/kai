package agentprompt

import (
	"strings"
	"testing"

	"kai/internal/planner"
)

func TestBuild_MinimalTask(t *testing.T) {
	out := Build(planner.AgentTask{
		Name:   "tests",
		Prompt: "add unit tests for the rate limiter",
	}, Context{})

	mustContain(t, out, []string{
		`agent "tests"`,
		"Task: add unit tests for the rate limiter",
		"orchestrator will integrate",
	})
	mustNotContain(t, out, []string{
		"Files you should focus on:",
		"Files you must NOT modify:",
		"Graph context",
	})
}

func TestBuild_WithFilesAndForbidden(t *testing.T) {
	out := Build(planner.AgentTask{
		Name:      "backend-api",
		Prompt:    "add rate limit middleware",
		Files:     []string{"middleware/ratelimit.go", "router.go"},
		DontTouch: []string{"pkg/auth/login.go"},
	}, Context{
		RepoRoot:  "/repo",
		Language:  "go",
		Protected: []string{"pkg/billing/**"},
	})

	mustContain(t, out, []string{
		"Working directory: /repo",
		"Primary language: go",
		"Files you should focus on:",
		"middleware/ratelimit.go",
		"router.go",
		"Files you must NOT modify:",
		"pkg/auth/login.go",
		"pkg/billing/**",
		"If changing one of these is genuinely necessary",
	})
}

func TestBuild_DeterministicOrdering(t *testing.T) {
	// Same inputs in different order must produce the same output —
	// stable for golden-file tests and for cache keys.
	a := Build(planner.AgentTask{
		Name:      "x",
		Prompt:    "p",
		Files:     []string{"b.go", "a.go", "c.go"},
		DontTouch: []string{"z.go", "m.go"},
	}, Context{Protected: []string{"forbidden/**"}})

	b := Build(planner.AgentTask{
		Name:      "x",
		Prompt:    "p",
		Files:     []string{"c.go", "a.go", "b.go"},
		DontTouch: []string{"m.go", "z.go"},
	}, Context{Protected: []string{"forbidden/**"}})

	if a != b {
		t.Fatalf("Build is non-deterministic across input ordering:\n%s\n---\n%s", a, b)
	}
}

// TestBuild_DontTouchAndProtectedMerged verifies the forbidden list
// is dedup-merged across DontTouch and Protected so the agent sees
// one clean list rather than two.
func TestBuild_DontTouchAndProtectedMerged(t *testing.T) {
	out := Build(planner.AgentTask{
		Name:      "x",
		Prompt:    "p",
		DontTouch: []string{"shared.go", "x.go"},
	}, Context{
		Protected: []string{"shared.go", "y.go"},
	})

	count := strings.Count(out, "shared.go")
	if count != 1 {
		t.Fatalf("expected shared.go to appear once (deduped), got %d times in:\n%s", count, out)
	}
	mustContain(t, out, []string{"x.go", "y.go"})
}

func TestBuild_GraphContextRendered(t *testing.T) {
	gctx := "router.go: called by api/server.go, api/health.go"
	out := Build(planner.AgentTask{Name: "x", Prompt: "p"}, Context{
		GraphContext: gctx,
	})
	mustContain(t, out, []string{"Graph context for the files in scope:", gctx})
}

func mustContain(t *testing.T, s string, parts []string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Errorf("output missing %q\nfull output:\n%s", p, s)
		}
	}
}

func mustNotContain(t *testing.T, s string, parts []string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(s, p) {
			t.Errorf("output should not contain %q\nfull output:\n%s", p, s)
		}
	}
}
