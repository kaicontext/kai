package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agentprompt"
	"kai/internal/config"
	"kai/internal/graph"
	"kai/internal/orchestrator"
	"kai/internal/planner"
	"kai/internal/remote"
	"kai/internal/safetygate"
	"kai/internal/tui"
	"kai/internal/tui/views"
)

// codeCmd launches the TUI. Bare `kai` (no subcommand) falls back to
// cobra's default help output — explicit invocation via `kai code`
// makes the TUI an opt-in surface rather than a magic no-args trigger.
var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Launch the kai TUI (REPL + sync + gate panes)",
	Long: `Open the interactive Bubble Tea front-end. Three panes:
  - REPL: type kai subcommands or, if ANTHROPIC_API_KEY is set,
    describe a change in plain English to invoke the planner.
  - Sync: live agent activity from the file watcher.
  - Gate: integrations held by the safety gate, with approve/reject
    hotkeys.

If stdin or stdout isn't a terminal (piped or redirected), prints
this help instead of launching the TUI.`,
	RunE: runCodeTUI,
}

// runCodeTUI is wired as codeCmd.RunE — `kai code` enters the TUI.
//
// Refuses in the following cases:
//   - stdin or stdout is not a terminal (piped or redirected)
//   - openDB fails (no .kai directory yet — run `kai init` first)
func runCodeTUI(cmd *cobra.Command, args []string) error {
	if !isTerminal() {
		// Non-interactive context: print help so scripts get a sensible
		// response instead of a TUI that immediately exits.
		return cmd.Help()
	}

	db, err := openDB()
	if err != nil {
		return fmt.Errorf("opening kai data: %w (run `kai init` first)", err)
	}
	defer db.Close()

	cwd, _ := os.Getwd()

	// Ensure the agent_sessions / agent_messages tables exist.
	// Idempotent — safe on every TUI launch. Failure is non-fatal:
	// the TUI still works without persistence.
	if err := session.EnsureSchema(asGraphDB(db)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: agent session schema: %v\n", err)
	}

	// Live sync setup: best-effort. If `kai live on` was run earlier,
	// subscribe a fresh channel for this TUI session so the agent's
	// edits broadcast in real time. If anything's missing (no remote,
	// no auth, sync not enabled), we just skip — the TUI still works
	// without live sync.
	liveSync, err := setupLiveSync(kaiDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if liveSync != nil {
		defer liveSync.Stop()
	}

	planner := buildPlannerServices(asGraphDB(db), kaiDir, cwd, liveSync)
	if planner != nil {
		// Pass the binary's version through so the startup banner
		// can show "kai v0.16.0" instead of "kai vdev". Single
		// source of truth lives in main.go's Version var.
		planner.Version = Version
	}
	return tui.Run(context.Background(), tui.Options{
		DB:      asGraphDB(db),
		KaiDir:  kaiDir,
		WorkDir: cwd,
		Planner: planner,
	})
}

// buildPlannerServices wires up the engine handles the REPL needs for
// natural-language input.
//
// LLM completions route through kailab-control's POST /api/v1/llm/messages
// rather than calling api.anthropic.com directly. That means the user
// must be logged in (`kai auth login`) — they don't need a personal
// ANTHROPIC_API_KEY. Returns nil with a warning if login is missing
// or any required config can't load; the REPL then falls back to
// shellout-only mode.
func buildPlannerServices(db *graph.DB, kaiDir, workDir string, liveSync *liveSyncWiring) *views.PlannerServices {
	creds, err := remote.LoadCredentials()
	if err != nil || creds == nil || creds.AccessToken == "" || creds.ServerURL == "" {
		fmt.Fprintf(os.Stderr, "warning: not logged in — natural-language input disabled (run `kai auth login`)\n")
		return nil
	}
	authToken, err := remote.GetValidAccessToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (planner disabled)\n", err)
		return nil
	}
	cfg, err := config.Load(kaiDir)
	if err != nil {
		// Bad yaml shouldn't block the TUI; log to stderr and skip
		// the planner path. The user can still use shellout commands.
		fmt.Fprintf(os.Stderr, "warning: %v (planner disabled)\n", err)
		return nil
	}
	gateCfg, err := safetygate.LoadConfig(kaiDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (planner disabled)\n", err)
		return nil
	}

	return &views.PlannerServices{
		DB:         db,
		LLM:        planner.NewServerCompleter(creds.ServerURL, authToken, cfg.Planner.Model),
		GateConfig: gateCfg,
		PlannerCfg: planner.Config{
			Model:     cfg.Planner.Model,
			MaxAgents: cfg.Planner.MaxAgents,
		},
		OrchestratorCfg: orchestrator.Config{
			AgentTimeout: time.Duration(cfg.Agent.TimeoutSeconds) * time.Second,
			GateConfig:   gateCfg,
			// In-process runner is the only path post-Slice 6.
			// Provider routes through kailab's LLM proxy with the
			// user's bearer token. Reuses the same auth as planner.
			AgentProvider: provider.NewKailab(creds.ServerURL, authToken),
			AgentModel:    cfg.Planner.Model,
			// Pass the main repo's graph DB so the in-process runner
			// can register kai_callers / kai_dependents / kai_context
			// as native agent tools.
			MainGraph: db,
			// LiveSync, when set, broadcasts every agent file write
			// to kailab so other clients on the same channel see the
			// change in real time. nil if `kai live on` wasn't run
			// or live-sync setup failed (we just skip rather than
			// blocking the TUI).
			LiveSync: orchLiveSync(liveSync),
			// Bash tool: on by default for the in-process runner so
			// agents can run tests, build, lint, etc. Allowlist
			// (optional) comes from .kai/config.yaml's agent.bash_allow.
			AgentBashEnabled: true,
			AgentBashAllow:   cfg.Agent.BashAllow,
			// Session persistence: pass the same DB the graph uses
			// so agent conversations land in <kaiDir>/db.sqlite.
			// One backup story, one migration story.
			AgentSessionStore: db,
			// TUI default: clean up successful spawns so /tmp doesn't
			// accumulate. Failed runs are kept regardless so the user
			// can read agent.log post-mortem (the orchestrator skips
			// despawn on ExitErr / IntegrateErr).
			Despawn: true,
			PromptContext: agentprompt.Context{
				RepoRoot:  workDir,
				Protected: gateCfg.Protected,
			},
		},
		PromptCtx: agentprompt.Context{RepoRoot: workDir, Protected: gateCfg.Protected},
		MainRepo:  workDir,
		KaiDir:    kaiDir,
	}
}

// asGraphDB unwraps openDB's return into a *graph.DB pointer. openDB
// returns an interface; the TUI needs the concrete type for in-process
// engine calls.
func asGraphDB(db interface{}) *graph.DB {
	if g, ok := db.(*graph.DB); ok {
		return g
	}
	return nil
}

// isTerminal reports whether both stdin and stdout are connected to
// a TTY. Both must be true: stdout-only TTY would mean piped input
// (e.g. `echo foo | kai`); stdin-only would mean redirected output.
func isTerminal() bool {
	stdoutFd := os.Stdout.Fd()
	stdinFd := os.Stdin.Fd()
	return (isatty.IsTerminal(stdoutFd) || isatty.IsCygwinTerminal(stdoutFd)) &&
		(isatty.IsTerminal(stdinFd) || isatty.IsCygwinTerminal(stdinFd))
}
