// Package orchestrator turns a planner.WorkPlan into running agents
// and integrated changes. It owns the agent subprocess lifecycle —
// spawn produces a workspace, but spawn does not start an agent;
// that's this package.
//
// Pipeline per agent:
//
//	1. shell out `kai spawn` to provision a CoW workspace
//	2. build the agent's prompt via internal/agentprompt
//	3. exec the configured agent command (e.g. claude -p {prompt})
//	4. wait for exit; capture stdout/stderr to <spawn>/.kai/agent.log
//	5. shell out `kai capture` to snapshot whatever the agent wrote
//	6. shell out `kai push origin` from the spawn dir
//	7. shell out `kai pull origin` in the main repo
//	8. in-process Manager.Integrate against the synced workspace
//	9. record verdict; optional `kai despawn`
//
// All v1 agents run in parallel — the planner deliberately avoids
// DependsOn for now (push/pull adds enough latency that ordering would
// feel slow, and live sync handles inter-agent visibility). Future
// phase can add ordering if real usage shows it's needed.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kai/internal/agentprompt"
	"kai/internal/graph"
	"kai/internal/planner"
	"kai/internal/safetygate"
	"kai/internal/workspace"
)

// AgentRun captures everything that happened to one agent: its task,
// where it ran, whether it exited cleanly, and what the gate said
// when its work was integrated.
type AgentRun struct {
	Task         planner.AgentTask
	SpawnDir     string                          // empty if spawn failed
	WorkspaceName string                         // "spawn-N", set by `kai spawn`
	ExitErr      error                           // nil = agent exited 0
	IntegrateErr error                           // nil = integrate ran (with any verdict)
	Verdict      *workspace.IntegrationDecision  // nil if integrate didn't run
	AdvancedRefs []string                        // populated when verdict == Auto
}

// Result is the orchestrator's aggregate report. Fed back to the REPL
// so the user gets a one-line summary plus per-agent detail on demand.
type Result struct {
	Runs         []AgentRun
	AutoPromoted int
	Held         int
	Failed       int
}

// Config controls orchestrator behavior. Caller composes from
// internal/config (agent command + timeout) plus a few orchestrator-
// specific knobs that don't fit in the user-facing config.
type Config struct {
	// AgentCommand is the argv template. The literal "{prompt}" is
	// substituted with the absolute path to a temp file containing
	// the agent's full prompt; "{prompt_text}" (less common) is
	// substituted inline. Defaults: ["claude", "-p", "{prompt}"].
	AgentCommand []string

	// AgentTimeout caps a single agent run. 0 means no timeout (not
	// recommended; agents can hang).
	AgentTimeout time.Duration

	// PushRemote is the remote name agents push to. Default "origin".
	PushRemote string

	// KaiBinary overrides the path to the kai executable for shellouts
	// (spawn, capture, push, pull, despawn). Empty falls back to
	// os.Executable() — the natural choice when the orchestrator runs
	// inside the kai binary itself. Tests pass an explicit path.
	KaiBinary string

	// Despawn, if true, runs `kai despawn <path>` after a successful
	// integration. Off by default — the user can inspect spawn dirs
	// for diagnostics. The TUI will likely flip this to true.
	Despawn bool

	// SpawnPrefix sets the path prefix for spawn dirs (passed to
	// `kai spawn --prefix`). Default "/tmp/kai-".
	SpawnPrefix string

	// GateConfig is forwarded to every Manager.Integrate call.
	GateConfig safetygate.Config

	// PromptContext is the per-repo agentprompt.Context. The
	// orchestrator passes it to agentprompt.Build for each task.
	PromptContext agentprompt.Context
}

// kaiBinary returns the kai executable to use for shellouts. Order:
// explicit Config override → os.Executable() → "kai" on PATH.
func kaiBinary(cfg Config) string {
	if cfg.KaiBinary != "" {
		return cfg.KaiBinary
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "kai"
}

// Execute runs every agent in the plan, then integrates each one's
// work. Errors from individual agents do not abort the whole run —
// the Result will list each agent's outcome and the caller decides
// what to do.
//
// Parameters:
//   - ctx       — cancellation propagates to subprocesses via exec.CommandContext
//   - plan      — what to run
//   - cfg       — agent command + integration knobs
//   - db        — main repo's live DB (for in-process Integrate)
//   - mainRepo  — absolute path to the main repo (cwd of `kai pull` / Integrate)
//   - kaiDir    — main repo's kai data dir (for resolveSnapshotID-type calls)
func Execute(ctx context.Context, plan *planner.WorkPlan, cfg Config, db *graph.DB, mainRepo, kaiDir string) (*Result, error) {
	if plan == nil || len(plan.Agents) == 0 {
		return nil, fmt.Errorf("orchestrator: empty plan")
	}
	if db == nil {
		return nil, fmt.Errorf("orchestrator: nil db")
	}
	if cfg.PushRemote == "" {
		cfg.PushRemote = "origin"
	}
	if cfg.SpawnPrefix == "" {
		cfg.SpawnPrefix = "/tmp/kai-"
	}
	if len(cfg.AgentCommand) == 0 {
		cfg.AgentCommand = []string{"claude", "-p", "{prompt}"}
	}

	runs := make([]AgentRun, len(plan.Agents))
	for i := range plan.Agents {
		runs[i].Task = plan.Agents[i]
	}

	// Phase A — spawn + run agents in parallel. Each agent owns its
	// slot in `runs` so we don't need a mutex for individual fields.
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runOneAgent(ctx, &runs[i], cfg, mainRepo)
		}(i)
	}
	wg.Wait()

	// Phase B — push, pull, integrate sequentially. Doing this in
	// parallel risks racing on the main repo's DB and on
	// snap.latest advancement. Sequential is simple and predictable;
	// optimize later if it's a bottleneck.
	for i := range runs {
		integrateOneAgent(ctx, &runs[i], cfg, db, mainRepo)
	}

	// Optional cleanup after everyone has integrated.
	if cfg.Despawn {
		for _, r := range runs {
			if r.SpawnDir == "" {
				continue
			}
			c := exec.CommandContext(ctx, kaiBinary(cfg), "despawn", r.SpawnDir, "--force")
			c.Dir = mainRepo
			_ = c.Run() // best-effort
		}
	}

	res := &Result{Runs: runs}
	for _, r := range runs {
		switch {
		case r.ExitErr != nil, r.IntegrateErr != nil, r.Verdict == nil:
			res.Failed++
		case r.Verdict.Verdict == string(safetygate.Auto):
			res.AutoPromoted++
		default:
			res.Held++
		}
	}
	return res, nil
}

// runOneAgent: spawn → write prompt → exec → wait. Errors land on
// run.ExitErr; SpawnDir is empty if spawn failed.
func runOneAgent(ctx context.Context, run *AgentRun, cfg Config, mainRepo string) {
	dir, wsName, err := spawnFor(ctx, run.Task.Name, cfg, mainRepo)
	if err != nil {
		run.ExitErr = fmt.Errorf("spawn: %w", err)
		return
	}
	run.SpawnDir = dir
	run.WorkspaceName = wsName

	prompt := agentprompt.Build(run.Task, cfg.PromptContext)
	promptFile, err := writePromptFile(dir, prompt)
	if err != nil {
		run.ExitErr = fmt.Errorf("write prompt: %w", err)
		return
	}

	argv := substituteArgv(cfg.AgentCommand, promptFile, prompt)
	if len(argv) == 0 {
		run.ExitErr = fmt.Errorf("empty agent command")
		return
	}

	runCtx := ctx
	if cfg.AgentTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.AgentTimeout)
		defer cancel()
	}

	logFile, err := os.Create(filepath.Join(dir, ".kai", "agent.log"))
	if err == nil {
		defer logFile.Close()
	}

	c := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	c.Dir = dir
	if logFile != nil {
		c.Stdout = logFile
		c.Stderr = logFile
	}
	if err := c.Run(); err != nil {
		run.ExitErr = fmt.Errorf("agent %s: %w", run.Task.Name, err)
	}
}

// integrateOneAgent: capture → push → pull → Manager.Integrate.
// Skips the integrate step if the agent failed; integrating an
// unchanged workspace is wasteful and noisy.
func integrateOneAgent(ctx context.Context, run *AgentRun, cfg Config, db *graph.DB, mainRepo string) {
	if run.ExitErr != nil || run.SpawnDir == "" {
		return // skip; phase A failed
	}

	// Capture in the spawn dir to ensure the agent's edits are in a
	// snapshot before pushing. If the agent ran kai_checkpoint we
	// might already have one, but capturing again is idempotent and
	// covers agents that don't touch the MCP at all.
	if err := runIn(ctx, run.SpawnDir, kaiBinary(cfg), "capture", "-m",
		fmt.Sprintf("orchestrator: agent %s", run.Task.Name)); err != nil {
		run.IntegrateErr = fmt.Errorf("capture: %w", err)
		return
	}

	if err := runIn(ctx, run.SpawnDir, kaiBinary(cfg), "push", cfg.PushRemote); err != nil {
		run.IntegrateErr = fmt.Errorf("push: %w", err)
		return
	}

	if err := runIn(ctx, mainRepo, kaiBinary(cfg), "pull", cfg.PushRemote); err != nil {
		run.IntegrateErr = fmt.Errorf("pull: %w", err)
		return
	}

	// Resolve the current target — typically snap.latest. Integrate
	// against whatever it points at right now; if another agent
	// auto-promoted in the meantime that's fine, the workspace's
	// merge will produce the right answer.
	target, err := resolveLatestSnap(db)
	if err != nil {
		run.IntegrateErr = fmt.Errorf("resolve snap.latest: %w", err)
		return
	}

	mgr := workspace.NewManager(db)
	gateCfg := cfg.GateConfig
	res, err := mgr.IntegrateWithOptions(run.WorkspaceName, target, workspace.IntegrateOptions{
		GateConfig: &gateCfg,
	})
	if err != nil {
		run.IntegrateErr = fmt.Errorf("integrate: %w", err)
		return
	}
	run.Verdict = res.Decision

	// Auto-publish on Auto verdict using the same Publish path the
	// CLI uses. PublishToRef honors the verdict; for non-Auto the
	// snapshot stays in the DB and shows up in `kai gate list`.
	if ws, err := mgr.Get(run.WorkspaceName); err == nil && ws != nil {
		report, _ := mgr.PublishToRef(ws, res, "snap.latest", workspace.PublishOptions{})
		if report != nil {
			run.AdvancedRefs = report.AdvancedRefs
		}
	}
}

// spawnFor invokes `kai spawn` for one task. We parse the output via
// the --json flag rather than scraping human-readable text. Returns
// (spawnDir, workspaceName, error).
func spawnFor(ctx context.Context, taskName string, cfg Config, mainRepo string) (string, string, error) {
	// Use one path per task so the workspace name is predictable
	// (workspaceNameFor in cmd/kai/spawn.go always emits "spawn-N"
	// for slot N within a single `kai spawn` invocation; we always
	// invoke with count=1 so N=1 every time, but the directory name
	// keeps tasks distinct).
	dir := fmt.Sprintf("%s%s-%d", cfg.SpawnPrefix, taskName, time.Now().UnixNano())
	c := exec.CommandContext(ctx, kaiBinary(cfg), "spawn", dir, "--agent", taskName)
	c.Dir = mainRepo
	out, err := c.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("kai spawn: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Workspace name inside the spawn dir is always "spawn-1"
	// (workspaceNameFor in cmd/kai/spawn.go). Hardcoding here is
	// brittle; if that helper ever changes we'll learn quickly via
	// the integrate step failing to find the workspace.
	return dir, "spawn-1", nil
}

// writePromptFile drops the agent's full prompt at .kai/agent.prompt
// in the spawn dir. The agent command can reference it via the
// {prompt} substitution token.
func writePromptFile(spawnDir, prompt string) (string, error) {
	p := filepath.Join(spawnDir, ".kai", "agent.prompt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// substituteArgv replaces {prompt} with the prompt file path and
// {prompt_text} with the prompt content inline. Lets users plug in
// agents that read either from a file or from a flag value.
func substituteArgv(argv []string, promptFile, promptText string) []string {
	out := make([]string, len(argv))
	for i, s := range argv {
		s = strings.ReplaceAll(s, "{prompt}", promptFile)
		s = strings.ReplaceAll(s, "{prompt_text}", promptText)
		out[i] = s
	}
	return out
}

// runIn execs a child command in the given directory and discards
// its output. We don't need the output for the orchestrator's own
// flow — push/pull failures show up in the returned error.
func runIn(ctx context.Context, dir, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s in %s: %w: %s", name,
			strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resolveLatestSnap reads snap.latest from the refs table. We don't
// import internal/ref to keep this package's dependency surface
// small — one short SQL query is cheaper than another import edge.
func resolveLatestSnap(db *graph.DB) ([]byte, error) {
	row := db.QueryRow(`SELECT target_id FROM refs WHERE name = 'snap.latest'`)
	var id []byte
	if err := row.Scan(&id); err != nil {
		return nil, fmt.Errorf("snap.latest not found: %w", err)
	}
	if len(id) == 0 {
		return nil, fmt.Errorf("snap.latest is empty")
	}
	return id, nil
}
