package views

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kai/internal/agentprompt"
	"kai/internal/graph"
	"kai/internal/orchestrator"
	"kai/internal/planner"
	"kai/internal/safetygate"
)

// PlannerServices is the engine handle the REPL needs to operate the
// natural-language path. The TUI parent constructs this once at
// startup and hands it in via NewREPL. A nil PlannerServices keeps
// the REPL in shell-out-only mode (every input goes to a cobra
// subprocess), which is what tests want.
type PlannerServices struct {
	DB             *graph.DB
	LLM            planner.Completer
	GateConfig     safetygate.Config
	PlannerCfg     planner.Config
	OrchestratorCfg orchestrator.Config
	PromptCtx      agentprompt.Context
	MainRepo       string
	KaiDir         string
}

// PlanReadyMsg is emitted when the LLM returns a parseable plan.
type PlanReadyMsg struct {
	Request string
	Plan    *planner.WorkPlan
	Err     error
}

// ExecuteDoneMsg is emitted when the orchestrator finishes a plan.
type ExecuteDoneMsg struct {
	Result *orchestrator.Result
	Err    error
}

// runPlan emits a tea.Cmd that calls planner.Plan asynchronously so
// the UI stays responsive during the LLM round-trip. The result
// arrives as a PlanReadyMsg the REPL handles in Update.
func runPlan(s *PlannerServices, request string) tea.Cmd {
	if s == nil {
		return func() tea.Msg {
			return PlanReadyMsg{Request: request, Err: fmt.Errorf("planner not configured")}
		}
	}
	return func() tea.Msg {
		ctx := context.Background()
		plan, err := planner.Plan(ctx, request, s.DB, s.GateConfig, s.PlannerCfg, s.LLM)
		return PlanReadyMsg{Request: request, Plan: plan, Err: err}
	}
}

// runReplan combines the original request and feedback into a fresh
// plan. The original is preserved so the user can iterate without
// retyping the whole prompt.
func runReplan(s *PlannerServices, original, feedback string) tea.Cmd {
	if s == nil {
		return func() tea.Msg {
			return PlanReadyMsg{Request: original, Err: fmt.Errorf("planner not configured")}
		}
	}
	return func() tea.Msg {
		ctx := context.Background()
		plan, err := planner.Replan(ctx, original, feedback, s.DB, s.GateConfig, s.PlannerCfg, s.LLM)
		// Track the combined request as the "original" going forward
		// so further feedback layers correctly.
		req := strings.TrimSpace(original) + " // " + strings.TrimSpace(feedback)
		return PlanReadyMsg{Request: req, Plan: plan, Err: err}
	}
}

// runExecute kicks off orchestrator.Execute. This subprocess + push/
// pull dance can take minutes in real use, but it's still wrapped in
// one tea.Cmd so the message-driven UI keeps working. Subsequent
// keypresses queue while it runs; the REPL guards against starting
// a second execute concurrently via the `executing` flag.
func runExecute(s *PlannerServices, plan *planner.WorkPlan) tea.Cmd {
	if s == nil {
		return func() tea.Msg {
			return ExecuteDoneMsg{Err: fmt.Errorf("orchestrator not configured")}
		}
	}
	return func() tea.Msg {
		ctx := context.Background()
		res, err := orchestrator.Execute(ctx, plan, s.OrchestratorCfg, s.DB, s.MainRepo, s.KaiDir)
		return ExecuteDoneMsg{Result: res, Err: err}
	}
}

// formatPlan renders a WorkPlan for the REPL scrollback. Multi-line
// markdown-ish output that fits the existing dim/error styling.
func formatPlan(p *planner.WorkPlan) string {
	if p == nil {
		return styleError.Render("(no plan)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %d agent(s)\n", len(p.Agents))
	if s := strings.TrimSpace(p.Summary); s != "" {
		fmt.Fprintf(&b, "  %s\n", styleDim.Render(s))
	}
	for _, a := range p.Agents {
		fmt.Fprintf(&b, "  • %s — %s\n", a.Name, strings.TrimSpace(a.Prompt))
		if len(a.Files) > 0 {
			fmt.Fprintf(&b, "    files: %s\n", strings.Join(a.Files, ", "))
		}
	}
	if len(p.RiskNotes) > 0 {
		b.WriteString("Risk:\n")
		for _, n := range p.RiskNotes {
			fmt.Fprintf(&b, "  · %s\n", styleWarn.Render(n))
		}
	}
	b.WriteString(styleDim.Render("[go / cancel / type feedback to replan]"))
	return b.String()
}

// stripPlannerPrefix avoids double-prefixing in the REPL. Errors
// returned by planner.Plan / Replan already start with "planner: "
// (either ErrTooVague's literal or fmt.Errorf("planner: ...")), so
// re-prepending in the REPL renders "planner: planner: ...". Strip
// once if present; leave anything else untouched.
func stripPlannerPrefix(s string) string {
	const p = "planner: "
	if strings.HasPrefix(s, p) {
		return s[len(p):]
	}
	return s
}

// formatExecuteResult renders the final orchestrator output.
func formatExecuteResult(res *orchestrator.Result, err error) string {
	if err != nil {
		return styleError.Render("orchestrator: " + err.Error())
	}
	if res == nil {
		return styleError.Render("(no result)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %d auto-promoted, %d held, %d failed\n",
		res.AutoPromoted, res.Held, res.Failed)
	for _, r := range res.Runs {
		// Compact one-line summary of which files landed; full list
		// in `git status` / `kai status` if the user wants more.
		filesNote := ""
		switch n := len(r.ChangedPaths); {
		case n == 1:
			filesNote = " — " + r.ChangedPaths[0]
		case n > 1:
			filesNote = fmt.Sprintf(" — %d files (incl. %s)", n, r.ChangedPaths[0])
		}

		switch {
		case r.ExitErr != nil:
			fmt.Fprintf(&b, "  • %s — %s\n    %s\n", r.Task.Name,
				styleError.Render("agent error: "+r.ExitErr.Error()),
				styleDim.Render("logs: "+r.SpawnDir+"/.kai/agent.log"))
		case r.IntegrateErr != nil:
			fmt.Fprintf(&b, "  • %s — %s\n    %s\n", r.Task.Name,
				styleError.Render("integrate error: "+r.IntegrateErr.Error()),
				styleDim.Render("logs: "+r.SpawnDir+"/.kai/agent.log"))
		case r.Verdict == nil:
			// Agent ran but produced no observable changes.
			fmt.Fprintf(&b, "  • %s — %s\n", r.Task.Name,
				styleDim.Render("no changes"))
		case r.Verdict.Verdict == string(safetygate.Auto):
			fmt.Fprintf(&b, "  • %s — applied to your repo (snap.latest advanced)%s\n",
				r.Task.Name, filesNote)
		case r.Verdict.Verdict == string(safetygate.Block):
			fmt.Fprintf(&b, "  • %s — %s (blast %d)%s\n", r.Task.Name,
				styleError.Render("BLOCKED — kept in working tree"), r.Verdict.BlastRadius, filesNote)
		default:
			fmt.Fprintf(&b, "  • %s — %s (blast %d)%s\n", r.Task.Name,
				styleWarn.Render("HELD for review"), r.Verdict.BlastRadius, filesNote)
		}
	}
	if res.Held > 0 {
		b.WriteString(styleDim.Render("Held changes are in your working tree. `kai gate list` to inspect, `kai gate approve <id>` to publish."))
	}
	return b.String()
}
