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
	"strings"
	"sync"
	"time"

	"kai/internal/agent"
	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agentprompt"
	"kai/internal/graph"
	"kai/internal/planner"
	"kai/internal/ref"
	"kai/internal/safetygate"
	"kai/internal/util"
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
	// ChangedPaths is the set of files the agent actually modified
	// in the main repo (post-absorb). Used by the gate for blast-
	// radius classification and surfaced in the result message so
	// the user can see at a glance what landed.
	ChangedPaths []string
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
// internal/config (agent timeout + bash allowlist) plus a few
// orchestrator-specific knobs that don't fit in the user-facing
// config.
//
// As of Slice 6 the orchestrator only drives the in-process agent
// runner (`internal/agent`). The external-subprocess fields
// (AgentCommand, prompt-file plumbing, dual-path env-var dispatch)
// are gone.
type Config struct {
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

	// Despawn controls cleanup of /tmp/kai-* dirs after each agent
	// finishes. The orchestrator only despawns runs that succeeded
	// (no ExitErr, no IntegrateErr) regardless of this flag — failed
	// runs always stay so you can inspect agent.log post-mortem.
	//
	// Default true in the TUI: by the time we report the result,
	// the agent's edits are already in the user's working tree and
	// captured in kai's snap history; the spawn dir is redundant.
	// Set false if you want to keep all dirs (e.g. for offline review
	// of how an agent reached its answer).
	Despawn bool

	// SpawnPrefix sets the path prefix for spawn dirs (passed to
	// `kai spawn --prefix`). Default "/tmp/kai-".
	SpawnPrefix string

	// GateConfig is forwarded to every Manager.Integrate call.
	GateConfig safetygate.Config

	// PromptContext is the per-repo agentprompt.Context. The
	// orchestrator passes it to agentprompt.Build for each task.
	PromptContext agentprompt.Context

	// OnActivity, when set, is invoked from a per-spawn fsnotify
	// observer for every file change the agent makes. Lets the TUI
	// surface real-time agent edits in its sync pane without pulling
	// in the kai MCP. spawnName is the AgentTask.Name; relPath is
	// relative to the spawn dir; op is "created"/"modified"/"deleted".
	//
	// Callbacks fire from the observer goroutine — the receiver must
	// not block. A non-blocking channel send is the typical shape.
	OnActivity func(spawnName, relPath, op string)

	// MaxAgentTokens caps token usage per agent run when the
	// in-process agent runner is enabled. 0 means "no cap" — the
	// kailab proxy may meter independently. Enforced by the runner
	// after each turn lands.
	MaxAgentTokens int

	// AgentProvider is the LLM provider the in-process runner uses.
	// nil produces a clear ExitErr from runOneAgent so users see why
	// (typically: not logged in to kailab via `kai auth login`).
	AgentProvider provider.Provider

	// AgentModel overrides the default model (claude-sonnet-4-6) the
	// in-process runner picks. Empty uses the default.
	AgentModel string

	// MainGraph is the main repo's graph DB. When non-nil, the
	// in-process runner registers kai_callers / kai_dependents /
	// kai_context tools the model can call mid-edit. nil disables
	// those tools (file ops still work).
	MainGraph *graph.DB

	// LiveSync, when set, broadcasts every agent file write to the
	// kailab live-sync channel. The TUI populates it after
	// subscribing a channel via remote.Client.SubscribeSync; nil
	// means live sync is disabled (run `kai live on` to enable).
	// Receiver must not block — it fires from the agent loop.
	LiveSync func(relPath, digest, contentBase64 string)

	// AgentBashEnabled turns on the in-process agent's bash tool.
	// AgentBashAllow optionally restricts it to a first-token
	// allowlist. Both are sourced from .kai/config.yaml's
	// agent.bash_allow.
	AgentBashEnabled bool
	AgentBashAllow   []string

	// AgentSessionStore, when set, persists each agent's
	// conversation to the kai DB (`<kaiDir>/db.sqlite`). The TUI
	// passes the main repo's graph.DB; tests pass a fake. nil
	// disables persistence — agents run with in-memory transcripts
	// only.
	AgentSessionStore session.Store
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

	// Cleanup. Despawn only successful runs — keep failed spawn dirs
	// around so the user can read agent.log and figure out what went
	// wrong. The opt-in flag toggles whether successful runs get
	// cleaned at all.
	if cfg.Despawn {
		for _, r := range runs {
			if r.SpawnDir == "" {
				continue
			}
			if r.ExitErr != nil || r.IntegrateErr != nil {
				continue // keep failures for diagnosis
			}
			c := exec.CommandContext(ctx, kaiBinary(cfg), "despawn", r.SpawnDir, "--force")
			c.Dir = mainRepo
			_ = c.Run() // best-effort
		}
	}

	res := &Result{Runs: runs}
	for _, r := range runs {
		switch {
		case r.ExitErr != nil, r.IntegrateErr != nil:
			res.Failed++
		case r.Verdict == nil:
			// v1: integrate isn't wired across spawn → main yet, so
			// successful runs land here. Treated as Held (the user
			// inspects the spawn dir and decides) rather than Failed.
			res.Held++
		case r.Verdict.Verdict == string(safetygate.Auto):
			res.AutoPromoted++
		default:
			res.Held++
		}
	}
	return res, nil
}

// runOneAgent: spawn a CoW workspace, then dispatch the in-process
// agent runner against it. Errors land on run.ExitErr; SpawnDir is
// empty if spawn itself failed.
//
// As of Slice 6 this only runs the in-process path. The external-
// subprocess (Claude Code, Cursor, etc.) flow is gone — kai owns the
// full agent loop. Spawn dirs remain because they still provide CoW
// isolation between parallel agents.
func runOneAgent(ctx context.Context, run *AgentRun, cfg Config, mainRepo string) {
	if cfg.AgentProvider == nil {
		run.ExitErr = fmt.Errorf("agent %s: AgentProvider is nil — run `kai auth login` and re-launch `kai code`", run.Task.Name)
		return
	}

	dir, wsName, err := spawnFor(ctx, run.Task.Name, cfg, mainRepo)
	if err != nil {
		run.ExitErr = fmt.Errorf("spawn: %w", err)
		return
	}
	run.SpawnDir = dir
	run.WorkspaceName = wsName

	prompt := agentprompt.Build(run.Task, cfg.PromptContext)

	runCtx := ctx
	if cfg.AgentTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.AgentTimeout)
		defer cancel()
	}

	taskName := run.Task.Name
	hooks := agent.Hooks{
		OnFileChange: func(relPath, op string) {
			if cfg.OnActivity != nil {
				cfg.OnActivity(taskName, relPath, op)
			}
		},
		OnAssistantText: func(text string) {
			if cfg.OnActivity != nil {
				cfg.OnActivity(taskName, "(assistant)", text)
			}
		},
		OnToolCall: func(name, _ string) {
			if cfg.OnActivity != nil {
				cfg.OnActivity(taskName, "(tool)", name)
			}
		},
		OnFileBroadcast: func(relPath, digest, contentBase64 string) {
			if cfg.LiveSync != nil {
				cfg.LiveSync(relPath, digest, contentBase64)
			}
		},
	}

	if _, err := agent.Run(runCtx, agent.Options{
		Workspace:      dir,
		Prompt:         prompt,
		Model:          cfg.AgentModel,
		MaxTotalTokens: cfg.MaxAgentTokens,
		Provider:       cfg.AgentProvider,
		Graph:          cfg.MainGraph,
		EnableBash:     cfg.AgentBashEnabled,
		BashAllow:      cfg.AgentBashAllow,
		SessionStore:   cfg.AgentSessionStore,
		TaskName:       taskName,
		Hooks:          hooks,
	}); err != nil {
		run.ExitErr = fmt.Errorf("agent %s: %w", taskName, err)
	}
}

// integrateOneAgent absorbs a finished agent's edits into the main
// repo, then runs the safety gate against the result. Sequence:
//
//  1. Capture in the spawn dir (audit trail; spawn DB stays
//     self-consistent for debugging).
//  2. Diff spawn vs main and copy/delete the changed files into the
//     main repo's working tree.
//  3. Run `kai capture` in main to record the new state. snap.latest
//     auto-advances at this point.
//  4. Run safetygate.Classify on the changed paths. Tag the new snap
//     with the verdict so `kai gate list` surfaces held integrations.
//  5. If the verdict is non-Auto, roll snap.latest back to its
//     previous value — the new snap stays in the DB (with verdict
//     metadata) for review, but it's not team-visible. The
//     filesystem changes stay in main's working tree either way;
//     the user can `git diff` and decide what to do.
//
// Skips entirely if the agent failed in phase A, or if the agent
// produced no observable changes.
func integrateOneAgent(ctx context.Context, run *AgentRun, cfg Config, db *graph.DB, mainRepo string) {
	if run.ExitErr != nil || run.SpawnDir == "" {
		return
	}

	// Spawn-side capture: best-effort, non-fatal. Even if this
	// fails the absorb below works directly against the filesystem.
	_ = runIn(ctx, run.SpawnDir, kaiBinary(cfg), "capture", "-m",
		fmt.Sprintf("orchestrator: agent %s", run.Task.Name))

	// Apply the agent's filesystem edits to main.
	changed, err := absorbSpawnIntoMain(run.SpawnDir, mainRepo)
	if err != nil {
		run.IntegrateErr = fmt.Errorf("absorb: %w", err)
		return
	}
	run.ChangedPaths = changed
	if len(changed) == 0 {
		// No-op agent; nothing to gate or capture.
		return
	}

	// Snapshot main's snap.latest before capture so we can roll it
	// back if the gate doesn't approve.
	prevLatest, _ := resolveLatestSnap(db)

	if err := runIn(ctx, mainRepo, kaiBinary(cfg), "capture", "-m",
		fmt.Sprintf("orchestrator: %s", run.Task.Name)); err != nil {
		run.IntegrateErr = fmt.Errorf("capture in main: %w", err)
		return
	}
	newLatest, err := resolveLatestSnap(db)
	if err != nil {
		run.IntegrateErr = fmt.Errorf("resolve new snap.latest: %w", err)
		return
	}

	gateCfg := cfg.GateConfig
	verdict, err := safetygate.Classify(ctx, changed, db, gateCfg)
	if err != nil {
		run.IntegrateErr = fmt.Errorf("classify: %w", err)
		return
	}
	run.Verdict = &workspace.IntegrationDecision{
		Verdict:     string(verdict.Verdict),
		BlastRadius: verdict.BlastRadius,
		Reasons:     verdict.Reasons,
		Touches:     verdict.Touches,
	}

	// Tag the new snap so `kai gate list` can find held integrations
	// later. We mirror the same payload keys integrateInternal writes
	// (gateVerdict, gateReasons, etc.) so the kai gate commands work
	// without code changes.
	if newSnap, err := db.GetNode(newLatest); err == nil && newSnap != nil && newSnap.Payload != nil {
		newSnap.Payload["gateVerdict"] = string(verdict.Verdict)
		newSnap.Payload["gateBlastRadius"] = verdict.BlastRadius
		if len(verdict.Reasons) > 0 {
			newSnap.Payload["gateReasons"] = verdict.Reasons
		}
		if len(verdict.Touches) > 0 {
			newSnap.Payload["gateTouches"] = verdict.Touches
		}
		newSnap.Payload["orchestratorAgent"] = run.Task.Name
		_ = db.UpdateNodePayload(newLatest, newSnap.Payload)
	}

	if verdict.Verdict == safetygate.Auto {
		run.AdvancedRefs = []string{"snap.latest"}
		return
	}

	// Held: roll snap.latest back to its previous value. The new
	// snap stays in the DB tagged for review; nothing was lost.
	if len(prevLatest) > 0 {
		_ = ref.NewRefManager(db).Set("snap.latest", prevLatest, ref.KindSnapshot)
	}
	// Avoid unused-import in builds where util is otherwise unused.
	_ = util.BytesToHex
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
