package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kai/internal/agentprompt"
	"kai/internal/graph"
	"kai/internal/planner"
	"kai/internal/safetygate"
)

// TestExecuteE2E_SpawnAndAgent runs the orchestrator against a real
// kai binary with a fake-agent shell command. The test stops short of
// push/pull/integrate because kai's remote is HTTP-only (kailab) and
// standing up a mock kailab server in a unit test is more harness
// than the value justifies for v1.
//
// What this verifies:
//
//   - orchestrator.Execute calls `kai spawn` correctly
//   - the agent subprocess is exec'd with the substituted prompt path
//   - the prompt file lands at <spawn>/.kai/agent.prompt
//   - the agent's writes survive in the spawn workspace
//   - the agent.log captures stdout/stderr
//   - push fails predictably (no remote configured) — proving we got
//     all the way to the integrate phase before hitting infra limits
//
// Skipped unless KAI_BIN points at a buildable kai binary. The CI
// matrix (when it exists) sets KAI_BIN so this runs there; locally
// it's opt-in to keep the default `go test ./...` fast and dep-free.
//
// Push/pull/integrate end-to-end is covered by manual testing for now;
// see docs/phase-3-plan.md for the recipe.
func TestExecuteE2E_SpawnAndAgent(t *testing.T) {
	kaiBin := os.Getenv("KAI_BIN")
	if kaiBin == "" {
		t.Skip("KAI_BIN not set — skipping e2e (set to a built kai binary path)")
	}
	if _, err := os.Stat(kaiBin); err != nil {
		t.Skipf("KAI_BIN=%s not stat-able: %v", kaiBin, err)
	}

	// 1. Set up a temp source repo. `kai init` makes it a kai repo;
	//    `kai capture` produces a baseline snapshot we can spawn from.
	src := t.TempDir()
	mustRun(t, src, kaiBin, "init")
	// Drop a tiny file so capture has something to snapshot.
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, src, kaiBin, "capture", "-m", "baseline")

	// 2. Open the source DB so the orchestrator can run the in-process
	//    integrate path. .kai/ is wherever kaipath.Resolve put it
	//    (.kai/ for non-git repos like this temp dir).
	kaiDir := filepath.Join(src, ".kai")
	dbPath := filepath.Join(kaiDir, "db.sqlite")
	objPath := filepath.Join(kaiDir, "objects")
	db, err := graph.Open(dbPath, objPath)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()

	// 3. Build the plan. One agent, fake command that writes a
	//    sentinel file and exits.
	plan := &planner.WorkPlan{
		Summary: "fake change for e2e",
		Agents: []planner.AgentTask{
			{
				Name:   "writer",
				Prompt: "write a sentinel file to confirm the agent ran",
				Files:  []string{"hello.txt"},
			},
		},
	}

	// 4. The "agent" is a tiny shell command. {prompt} is substituted
	//    with the prompt-file path; the script writes a sentinel and
	//    appends a line to hello.txt so the spawn workspace has a
	//    real change to integrate.
	agentScript := `set -e; echo "agent ran" > .agent-ran; echo "v2 from agent" >> hello.txt`
	cfg := Config{
		AgentCommand: []string{"/bin/sh", "-c", agentScript, "{prompt}"},
		AgentTimeout: 30 * time.Second,
		KaiBinary:    kaiBin,
		SpawnPrefix:  filepath.Join(t.TempDir(), "kai-e2e-"),
		PushRemote:   "origin", // nothing configured; push will fail predictably
		GateConfig:   safetygate.DefaultConfig(),
		PromptContext: agentprompt.Context{
			RepoRoot: src,
		},
	}

	// 5. Execute. We expect the orchestrator to:
	//    - spawn successfully (real kai binary)
	//    - run the fake agent (writes .agent-ran + appends to hello.txt)
	//    - fail at the capture/push step because no remote is configured
	res, err := Execute(context.Background(), plan, cfg, db, src, kaiDir)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(res.Runs))
	}
	run := res.Runs[0]

	// Phase A assertions — spawn + agent ran cleanly.
	if run.SpawnDir == "" {
		t.Fatal("spawn dir not populated; spawn step failed")
	}
	if run.ExitErr != nil {
		t.Fatalf("agent exited with error: %v", run.ExitErr)
	}
	if _, err := os.Stat(filepath.Join(run.SpawnDir, ".agent-ran")); err != nil {
		t.Errorf("sentinel file missing — agent didn't run in spawn dir: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(run.SpawnDir, "hello.txt")); !strings.Contains(string(body), "v2 from agent") {
		t.Errorf("agent's hello.txt edit didn't land: %q", string(body))
	}
	logPath := filepath.Join(run.SpawnDir, ".kai", "agent.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("agent.log missing: %v", err)
	}
	promptPath := filepath.Join(run.SpawnDir, ".kai", "agent.prompt")
	if _, err := os.Stat(promptPath); err != nil {
		t.Errorf("agent.prompt missing: %v", err)
	} else if body, _ := os.ReadFile(promptPath); !strings.Contains(string(body), "writer") {
		t.Errorf("agent.prompt missing identity: %q", string(body))
	}

	// Phase B — without a kailab remote configured, push must fail.
	// That's the expected outcome for v1; flagging it tells us the
	// pipeline got past the agent. A green push here would actually
	// be suspicious (it'd mean push silently no-op'd).
	if run.IntegrateErr == nil {
		t.Errorf("expected integrate err (no remote configured), got nil")
	}
	if run.Verdict != nil {
		t.Errorf("expected nil verdict (integrate skipped on push fail), got %+v", run.Verdict)
	}

	// Result aggregation should treat this as a failure (verdict nil
	// + IntegrateErr non-nil → res.Failed++).
	if res.Failed != 1 {
		t.Errorf("expected res.Failed=1, got %d (auto=%d held=%d)", res.Failed, res.AutoPromoted, res.Held)
	}
}

// mustRun execs cmd in dir, fatal on non-zero. Used by the e2e test
// for setup steps where any failure would invalidate everything.
func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in %s failed: %v\n%s", name, strings.Join(args, " "), dir, err, string(out))
	}
}
