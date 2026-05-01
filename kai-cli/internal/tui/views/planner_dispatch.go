package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kai/internal/agent"
	"kai/internal/agentprompt"
	"kai/internal/graph"
	"kai/internal/orchestrator"
	"kai/internal/planner"
	"kai/internal/safetygate"
)

// formatTokens renders a one-line "· 1.2k in / 380 out" trailer for
// chat replies. Uses k-suffix above 999 so the line stays short even
// for chatty turns.
func formatTokens(in, out int) string {
	return fmt.Sprintf("· %s in / %s out", humanCount(in), humanCount(out))
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// summarizeToolCall renders a one-line, human-readable label for a
// tool dispatch, used in the inline activity stream. The agent's
// inputJSON is structured per-tool — we pluck the most informative
// field (file_path for file tools, command for bash, the kai_* tools'
// target args) so the user sees "→ write package.json" instead of
// "→ write {...}". Falls back to bare tool name on parse failure.
func summarizeToolCall(name, inputJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
		return "→ " + name
	}
	pluck := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	switch name {
	case "view", "write", "edit":
		if p := pluck("file_path"); p != "" {
			return "→ " + name + " " + p
		}
	case "bash":
		if c := pluck("command"); c != "" {
			if len(c) > 60 {
				c = c[:57] + "..."
			}
			return "→ bash: " + c
		}
	case "kai_callers", "kai_dependents", "kai_context":
		if p := pluck("file_path"); p != "" {
			return "→ " + name + " " + p
		}
		if p := pluck("symbol"); p != "" {
			return "→ " + name + " " + p
		}
	}
	return "→ " + name
}

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

	// ChatActivityCh, when non-nil, receives live tool-call and
	// file-change events from the chat-fallback agent so the REPL
	// can render them inline as the agent works. Sends are
	// non-blocking — a full channel drops events rather than
	// stalling the agent loop.
	ChatActivityCh chan<- ChatActivityEvent

	// Version is the kai CLI version, used in the startup banner.
	// Empty falls back to "dev".
	Version string
}

// PlanReadyMsg is emitted when the LLM returns a parseable plan.
type PlanReadyMsg struct {
	Request string
	Plan    *planner.WorkPlan
	Err     error
	// ChatReply, when non-empty, indicates the request was too vague
	// to plan and the runner fell back to a conversational answer.
	// REPL renders ChatReply inline as assistant prose instead of
	// surfacing ErrTooVague as an error.
	ChatReply string
	// ChatSessionID is the persisted-session id for the chat
	// fallback's agent run. The REPL stickies this across chat-mode
	// turns within a single TUI session so the model remembers
	// prior conversation. Empty when the response came from the
	// planner path (which doesn't share session state with chat).
	ChatSessionID string
	// ChatTokensIn / ChatTokensOut are the per-turn usage counts the
	// chat-fallback agent reported. Surfaced as a dim trailer line
	// after the reply so the user can see what each turn cost. Zero
	// when the response came from the planner path.
	ChatTokensIn  int
	ChatTokensOut int
}

// ExecuteDoneMsg is emitted when the orchestrator finishes a plan.
type ExecuteDoneMsg struct {
	Result *orchestrator.Result
	Err    error
}

// runPlan emits a tea.Cmd that calls planner.Plan asynchronously so
// the UI stays responsive during the LLM round-trip. The result
// arrives as a PlanReadyMsg the REPL handles in Update.
//
// When the planner returns ErrTooVague (greeting, chitchat, vague
// "make it better"), runPlan falls back to a one-shot Chat reply
// instead of surfacing the error. The REPL renders ChatReply as
// inline prose so the user can keep the conversation flowing.
func runPlan(s *PlannerServices, request, chatSessionID string) tea.Cmd {
	if s == nil {
		return func() tea.Msg {
			return PlanReadyMsg{Request: request, Err: fmt.Errorf("planner not configured")}
		}
	}
	return func() tea.Msg {
		ctx := context.Background()
		plan, err := planner.Plan(ctx, request, s.DB, s.GateConfig, s.PlannerCfg, s.LLM)
		if errors.Is(err, planner.ErrTooVague) {
			reply, newSessionID, tIn, tOut, chatErr := runChatAgent(ctx, s, request, chatSessionID)
			if chatErr != nil {
				return PlanReadyMsg{Request: request, Err: chatErr}
			}
			return PlanReadyMsg{
				Request:       request,
				ChatReply:     reply,
				ChatSessionID: newSessionID,
				ChatTokensIn:  tIn,
				ChatTokensOut: tOut,
			}
		}
		return PlanReadyMsg{Request: request, Plan: plan, Err: err}
	}
}

// runChatAgent handles the chat fallback when the planner says the
// request is too vague. Instead of a one-shot text completion (which
// can't see the user's repo), we run the agent loop against the main
// repo so the model can use `view`, `bash`, edit/write, and the
// kai_* graph tools to actually answer the user. The trust boundary
// is the user's review of the reply — chat-mode runs unsandboxed in
// the working tree, same blast radius as anything the user could do
// at their own shell.
func runChatAgent(ctx context.Context, s *PlannerServices, request, sessionID string) (text, newSessionID string, tokensIn, tokensOut int, err error) {
	if s.OrchestratorCfg.AgentProvider == nil {
		return "", "", 0, 0, fmt.Errorf("chat: agent provider not configured (run `kai auth login`)")
	}
	// The system role is sent per-request via req.System (not stored
	// in persisted history), so we re-send it on every turn — both
	// fresh and resumed. Without it the resumed model has no
	// instructions and drifts toward whatever pattern the prior
	// transcript established.
	runPrompt := "System: " + chatSystemPrompt + "\n\n" + request

	// Live activity: pipe tool dispatches and file mutations into the
	// chat-activity channel so the REPL can render them inline. Sends
	// are non-blocking — a slow renderer drops events instead of
	// stalling the agent loop.
	emit := func(kind, summary string) {
		if s.ChatActivityCh == nil {
			return
		}
		select {
		case s.ChatActivityCh <- ChatActivityEvent{Kind: kind, Summary: summary, When: time.Now()}:
		default:
		}
	}

	// Bracket the run with start/end markers so the status bar can
	// display a live "agents: N" counter. agent_end fires regardless
	// of success/error path via defer.
	emit("agent_start", "")
	defer emit("agent_end", "")

	res, err := agent.Run(ctx, agent.Options{
		Workspace:      s.MainRepo,
		Prompt:         runPrompt,
		Model:          s.PlannerCfg.Model,
		Provider:       s.OrchestratorCfg.AgentProvider,
		Graph:          s.OrchestratorCfg.MainGraph,
		EnableBash:     true,
		BashAllow:      s.OrchestratorCfg.AgentBashAllow,
		MaxTotalTokens: s.OrchestratorCfg.MaxAgentTokens,
		SessionStore:   s.OrchestratorCfg.AgentSessionStore,
		SessionID:      sessionID,
		TaskName:       "chat",
		GateConfig:     s.GateConfig,
		Hooks: agent.Hooks{
			OnToolCall: func(name, inputJSON string) {
				emit("tool", summarizeToolCall(name, inputJSON))
			},
			OnBashOutput: func(line string) {
				// Stream each line of bash output as a "bash" kind
				// event. Keeps long-running commands (brew install,
				// npm test) visible in real time.
				emit("bash", line)
			},
			// OnFileChange is intentionally not wired — OnFileDiff
			// covers the same paths with richer info (we'd be
			// emitting two events per write otherwise).
			OnFileDiff: func(relPath, op, patch string, added, removed int) {
				if s.ChatActivityCh == nil {
					return
				}
				select {
				case s.ChatActivityCh <- ChatActivityEvent{
					Kind:    "diff",
					Path:    relPath,
					Op:      op,
					Diff:    patch,
					Added:   added,
					Removed: removed,
					When:    time.Now(),
				}:
				default:
				}
			},
			OnGateVerdict: func(paths []string, verdict string, blastRadius int, reasons []string) {
				if s.ChatActivityCh == nil {
					return
				}
				select {
				case s.ChatActivityCh <- ChatActivityEvent{
					Kind:       "gate",
					GatePaths:  paths,
					GateVerdict: verdict,
					GateRadius: blastRadius,
					GateReasons: reasons,
					When:       time.Now(),
				}:
				default:
				}
			},
			OnTurnComplete: func(tIn, tOut int) {
				if s.ChatActivityCh == nil {
					return
				}
				select {
				case s.ChatActivityCh <- ChatActivityEvent{
					Kind:      "tokens",
					TokensIn:  tIn,
					TokensOut: tOut,
					When:      time.Now(),
				}:
				default:
				}
			},
		},
	})
	if err != nil {
		return "", "", 0, 0, err
	}
	if res == nil || len(res.Transcript) == 0 {
		return "", "", 0, 0, fmt.Errorf("chat: empty transcript")
	}
	// The last assistant turn carries the answer.
	for i := len(res.Transcript) - 1; i >= 0; i-- {
		m := res.Transcript[i]
		if m.Role == "assistant" {
			if t := strings.TrimSpace(m.Text()); t != "" {
				return t, res.SessionID, res.TokensIn, res.TokensOut, nil
			}
		}
	}
	return "", res.SessionID, res.TokensIn, res.TokensOut, fmt.Errorf("chat: assistant returned no text")
}

// chatSystemPrompt is the model brief for the chat-fallback path.
// Short on purpose — the user typed something conversational; the
// model should match that energy and only call tools when the user
// actually asked an inspection question.
const chatSystemPrompt = `You are a hands-on coding assistant inside the kai CLI. You have full read/write access to the user's workspace via these tools:

  - bash: run any shell command (npm init, mkdir, mv, rm, git, npm test, …). Use it freely to do what the user asks.
  - view: read a file with line numbers.
  - write: create or overwrite a file.
  - edit: replace a unique substring inside an existing file.
  - kai_callers / kai_dependents / kai_context: query the semantic graph.

You are NOT read-only. You are NOT restricted to inspection. If the user asks you to create a file, run a command, scaffold a project, or make a small change — just do it with the tools above. Do not tell the user to run commands themselves; run them.

Style:
  - Be brief. 1–4 sentences after you finish, unless the user asked for detail.
  - Don't paste large file contents; summarize.
  - For multi-step changes that touch several files or need careful planning, suggest the user re-phrase as a concrete change request so the planner can route it through review — but for small one-shot tasks (create a file, run a command, fix a typo), just do them.`

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
