package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

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

	return tui.Run(context.Background(), tui.Options{
		DB:      asGraphDB(db),
		KaiDir:  kaiDir,
		WorkDir: cwd,
		Planner: buildPlannerServices(asGraphDB(db), kaiDir, cwd),
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
func buildPlannerServices(db *graph.DB, kaiDir, workDir string) *views.PlannerServices {
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
		LLM:        planner.NewServerCompleter(creds.ServerURL, authToken),
		GateConfig: gateCfg,
		PlannerCfg: planner.Config{
			Model:     cfg.Planner.Model,
			MaxAgents: cfg.Planner.MaxAgents,
		},
		OrchestratorCfg: orchestrator.Config{
			AgentCommand: cfg.Agent.Command,
			AgentTimeout: time.Duration(cfg.Agent.TimeoutSeconds) * time.Second,
			GateConfig:   gateCfg,
			PromptContext: agentprompt.Context{
				RepoRoot:  workDir,
				Protected: gateCfg.Protected,
			},
		},
		PromptCtx: agentprompt.Context{RepoRoot: workDir, Protected: gateCfg.Protected},
		MainRepo:  workDir,
		KaiDir:    kaiDir,
		IsKnownCommand: func(name string) bool {
			if name == "" {
				return false
			}
			for _, c := range rootCmd.Commands() {
				if c.Name() == name {
					return true
				}
				for _, alias := range c.Aliases {
					if alias == name {
						return true
					}
				}
			}
			return false
		},
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
