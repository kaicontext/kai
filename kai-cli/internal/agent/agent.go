// Package agent is kai's in-process LLM agent runner. It replaces the
// orchestrator's external-subprocess path (exec.Cmd("claude", -p)) so
// kai owns the full agent loop: the LLM call, the tool dispatch, the
// graph context injection, the file-edit hooks.
//
// As of Slice 6 this is the only path the orchestrator drives — the
// external-subprocess fallback (Claude Code, Cursor, etc. via
// exec.Cmd) is gone. The Run signature stays stable so future
// extensions (streaming responses, multi-turn replan) can land here
// without changing the orchestrator's invocation site.
//
// See ../../docs/phase-3-plan.md and the spec at
// ~/.claude/plans/spec-kai-code-frolicking-origami.md for the full
// migration sequence.
package agent

import (
	"context"

	"kai/internal/agent/message"
	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agent/tools"
	"kai/internal/graph"
	"kai/internal/safetygate"
)

// Hooks lets the orchestrator observe agent activity without coupling
// the agent package to the TUI. Each callback fires from the runner's
// goroutine; receivers must not block (use non-blocking channel sends
// or buffered queues).
type Hooks struct {
	// OnFileChange fires after the agent's view/edit/write tools
	// modify a file in the workspace. The path is relative to the
	// workspace root; op is "created" / "modified" / "deleted" so it
	// matches `internal/orchestrator/observer.go`'s vocabulary.
	OnFileChange func(relPath, op string)

	// OnFileBroadcast fires after a successful write or edit with
	// the file's content (base64-encoded) and content digest. The
	// orchestrator wires this to `remote.SyncPushFile` so other
	// agents subscribed to the live-sync channel see the change in
	// real time. Distinct from OnFileChange so callers that only
	// need notification (e.g. TUI sync pane) don't have to deal with
	// content payload memory churn.
	//
	// digest may be empty — the kailab side computes its own hash
	// when blank, but supplying one lets the receiver dedupe quickly.
	OnFileBroadcast func(relPath, digest, contentBase64 string)

	// OnAssistantText fires when the model emits user-visible text.
	// The TUI surfaces it inline as the agent narrates its work.
	OnAssistantText func(text string)

	// OnToolCall fires when the model dispatches a tool. Useful for
	// the sync pane to render a "called kai_callers(file=router.go)"
	// breadcrumb.
	OnToolCall func(name, inputJSON string)

	// OnTurnComplete fires after each provider response with the
	// run's cumulative token counts. The TUI uses this to animate a
	// live token counter as turns complete (the agent's billed
	// usage climbs from 0 → final over the run). Fires once per
	// model call regardless of whether the model also issued tool
	// calls, so the counter ticks as work progresses.
	OnTurnComplete func(tokensIn, tokensOut int)

	// OnFileDiff fires after each successful write/edit with a
	// unified diff (`--- a/path` / `+++ b/path` + `+/-/space`
	// lines) plus pre-counted additions/removals. The TUI renders
	// this as an inline "Update(path) — Added N lines" block,
	// mirroring Claude Code's per-edit display. Distinct from
	// OnFileChange so consumers that just need notification don't
	// pay for diff computation.
	OnFileDiff func(relPath, op, unifiedDiff string, added, removed int)

	// OnBashOutput fires once per line of bash stdout/stderr while a
	// command is running, so the TUI can stream long-running
	// commands (brew install, npm test, go test ./...) inline
	// instead of leaving the user staring at a frozen pane.
	OnBashOutput func(line string)

	// OnGateVerdict fires after every workspace mutation with the
	// safety gate's classification (auto / review / block) plus
	// blast radius and human-readable reasons. Lets the TUI render
	// kai's per-edit verdict inline ("auto ✓ — 0 downstream", "held
	// ⚠ — 3 callers affected"). Fires once per mutation event:
	// once per write/edit tool call, and once per batch of files
	// touched by a single bash invocation.
	OnGateVerdict func(paths []string, verdict string, blastRadius int, reasons []string)
}

// Options configures one agent run.
type Options struct {
	// Workspace is the absolute path to the spawn dir (CoW workspace)
	// the agent should treat as its working directory. Tools resolve
	// paths relative to this — not against process cwd.
	Workspace string

	// Prompt is the system+user prompt the planner produced. The
	// runner splits a leading "System: ..." block off as the system
	// role; everything else is the user turn. Future revisions can
	// pass an explicit []Message instead.
	Prompt string

	// Model is the Anthropic model id (e.g. "claude-sonnet-4-6"). If
	// empty the runner picks a sensible default.
	Model string

	// MaxTokens caps a single LLM call's response. Defaults to a
	// reasonable per-turn budget if zero.
	MaxTokens int

	// MaxTotalTokens caps cumulative token use across all turns in
	// this run. 0 disables the cap. Wired to the orchestrator's
	// MaxAgentTokens field.
	MaxTotalTokens int

	// Provider is the LLM transport. Required. Typically a
	// `provider.Kailab` wrapping the user's bearer token.
	Provider provider.Provider

	// ExtraTools is the optional list of pre-built tools to register
	// alongside the default file tools. Used for one-off tools the
	// caller wants to bolt on; the standard kai_* graph tools come
	// from Graph below.
	ExtraTools []tools.BaseTool

	// Graph is the main repo's graph DB. When set, the runner
	// registers kai_callers, kai_dependents, kai_context as tools
	// the model can call to reason about call structure mid-edit.
	// nil disables those tools (e.g. tests that don't need them).
	Graph *graph.DB

	// EnableBash registers the `bash` tool. Default off so tests
	// that don't need shell access never accidentally execute
	// commands. Production wiring (cmd/kai/tui.go) sets this true.
	EnableBash bool

	// ReadOnly registers only the view tool from the file set
	// (write and edit are skipped). Used by the chat-fallback path
	// where the agent should be able to inspect the workspace
	// (`ls`, view a file) without risking modifications.
	ReadOnly bool

	// BashAllow is the optional first-token allowlist enforced by
	// the bash tool. Empty (with EnableBash=true) means "no
	// restriction". Only consulted when EnableBash is true.
	BashAllow []string

	// GateConfig drives in-loop safety classification. When non-zero
	// (BlockThreshold > 0), every file mutation the agent makes —
	// via write/edit tools and via bash — is run through
	// safetygate.Classify against opts.Graph; the verdict (auto /
	// review / block) fires Hooks.OnGateVerdict so the TUI can
	// render it inline. Block verdicts also revert the offending
	// edit before returning the tool result so the model sees the
	// rollback. Leave Graph nil or BlockThreshold=0 to disable.
	GateConfig safetygate.Config

	// SessionStore, when set, persists every turn (assistant +
	// tool-result) to the kai DB so the conversation survives
	// process restarts. nil disables persistence; the run lives
	// only in memory. The orchestrator passes its main DB here
	// (graph.DB satisfies the session.Store interface).
	SessionStore session.Store

	// SessionID, when set, resumes an existing conversation
	// instead of starting fresh. The runner loads History() to
	// seed the model with prior turns. Empty + non-nil
	// SessionStore creates a new session row.
	SessionID string

	// TaskName is recorded on the session row for "what was this
	// agent supposed to do" lookups later. Optional; defaults to
	// "" if unset. The orchestrator threads run.Task.Name here.
	TaskName string

	// Hooks plugs in the orchestrator's observers.
	Hooks Hooks
}

// Result captures everything the run produced for the caller (the
// orchestrator's `runOneAgent`) to consume.
type Result struct {
	// Transcript is the full message history. When SessionStore is
	// set the same content has also been persisted to the DB; the
	// in-memory slice is just a convenience for the immediate caller.
	Transcript []message.Message

	// FinishReason matches the last model turn's reason. Most runs
	// end with EndTurn; ToolUse here would indicate the runner gave
	// up mid-loop, which is a bug.
	FinishReason message.FinishReason

	// TokensIn / TokensOut accumulate across all model calls in the
	// run. Plumbed for budget accounting (orchestrator.Config.MaxAgentTokens).
	TokensIn  int
	TokensOut int

	// SessionID is the id of the persisted session row (empty when
	// no SessionStore was provided). Callers can pass this back as
	// Options.SessionID on a future Run to resume the conversation.
	SessionID string
}

// Run executes a single agent task in-process. Returns when the model
// emits an EndTurn turn (or hits an error / cancellation / token
// budget cap / max-turns guard).
//
// Slice 1: full agent loop wired in. For the orchestrator's invocation
// pattern (one-shot per AgentTask), call Run once per task and inspect
// Result.FinishReason.
func Run(ctx context.Context, opts Options) (*Result, error) {
	return runLoop(ctx, opts)
}
