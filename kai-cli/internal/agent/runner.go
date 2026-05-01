package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"kai/internal/agent/message"
	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agent/tools"
	"kai/internal/safetygate"
)

// classifyAndEmit runs the safety gate on a freshly-mutated set of
// paths and forwards the verdict to the TUI hook. Cheap no-op when
// the gate isn't configured (no Graph or zero BlockThreshold) so
// callers can invoke it unconditionally after each mutation.
//
// We don't revert on Block here — agent-side rollback would mean
// re-reading the file's prior content per mutation, which we don't
// keep around. The verdict is informational; the existing
// orchestrator+gate path is the chokepoint that actually holds
// changes back from publish. In chat mode the user sees the verdict
// inline and can decide to revert, run /gate, or continue.
func classifyAndEmit(opts Options, paths []string) {
	if opts.Hooks.OnGateVerdict == nil {
		return
	}
	if opts.Graph == nil || opts.GateConfig.BlockThreshold == 0 {
		return
	}
	if len(paths) == 0 {
		return
	}
	dec, err := safetygate.Classify(context.Background(), paths, opts.Graph, opts.GateConfig)
	if err != nil {
		// Surface as a verdict with a single reason — better than
		// silently dropping the signal when the graph is mid-rebuild.
		opts.Hooks.OnGateVerdict(paths, "error", 0, []string{err.Error()})
		return
	}
	opts.Hooks.OnGateVerdict(paths, string(dec.Verdict), dec.BlastRadius, dec.Reasons)
}

// readOnlyTools is the set of tools safe to dispatch concurrently —
// they don't mutate the workspace, don't depend on each other's
// output, and don't compete for shared resources. Adding bash here
// would be wrong even for "ls": users issue `bash` for arbitrary
// commands and we don't introspect the command. Adding new read-only
// tools is intentional, not automatic — verify before extending.
var readOnlyTools = map[string]bool{
	"view":           true,
	"kai_callers":    true,
	"kai_dependents": true,
	"kai_context":    true,
}

// dispatchToolCalls runs the model's tool calls and returns one
// tool_result per call, preserving call order in the result slice
// (Anthropic matches by tool_use_id, but ordered results read better
// when persisted to the transcript and replayed). Read-only calls
// fan out into goroutines; mutating calls run inline. The two
// classes are interleaved correctly because we collect a slot per
// call and fill it in place — ordering is by call index, not finish
// time.
func dispatchToolCalls(
	ctx context.Context,
	calls []message.ToolCall,
	registry map[string]tools.BaseTool,
	onCall func(name, inputJSON string),
) []message.ContentPart {
	results := make([]message.ContentPart, len(calls))
	var wg sync.WaitGroup

	exec := func(idx int, call message.ToolCall) message.ToolResult {
		if onCall != nil {
			onCall(call.Name, call.Input)
		}
		tool, ok := registry[call.Name]
		if !ok {
			return message.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    fmt.Sprintf("unknown tool: %s", call.Name),
				IsError:    true,
			}
		}
		tr, err := tool.Run(ctx, tools.ToolCall{
			ID:    call.ID,
			Name:  call.Name,
			Input: call.Input,
		})
		if err != nil {
			return message.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    fmt.Sprintf("tool error: %s", err.Error()),
				IsError:    true,
			}
		}
		return message.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    tr.Content,
			Metadata:   tr.Metadata,
			IsError:    tr.IsError,
		}
	}

	for i, call := range calls {
		if readOnlyTools[call.Name] {
			wg.Add(1)
			go func(idx int, c message.ToolCall) {
				defer wg.Done()
				results[idx] = exec(idx, c)
			}(i, call)
			continue
		}
		// Mutating call: drain any in-flight reads so writes observe
		// a consistent state, then run inline. Subsequent reads in
		// the same batch will spawn fresh goroutines after the write
		// completes — happens-before is by-call-index, which is the
		// model's intent.
		wg.Wait()
		results[i] = exec(i, call)
	}
	wg.Wait()
	return results
}

// runLoop is the in-process agent loop. It dispatches tool calls, feeds
// results back to the model, and stops when the model emits an
// end_turn (or hits a budget / cancellation).
//
// Slice 1 contract:
//   - Single Provider, single Hooks, fixed tool set.
//   - Tool registry is built from opts; runner doesn't know about
//     specific tools (file vs graph vs bash) — it just dispatches by
//     name against the registry.
//   - Conversation lives in memory; persistence lands in Slice 5.
//   - Per-run token cap enforced via opts.MaxTokens summed across
//     turns (see budget check below).
//
// The function is unexported; callers reach it via Run() in agent.go.
func runLoop(ctx context.Context, opts Options) (*Result, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("agent: Provider required")
	}
	if opts.Workspace == "" {
		return nil, fmt.Errorf("agent: Workspace required")
	}
	if strings.TrimSpace(opts.Prompt) == "" {
		return nil, fmt.Errorf("agent: Prompt required")
	}

	registry := buildToolRegistry(opts)
	system, user := splitSystemAndUser(opts.Prompt)
	model := opts.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTokensPerTurn := opts.MaxTokens
	if maxTokensPerTurn <= 0 {
		maxTokensPerTurn = 4096
	}

	// Resolve session: resume if id given, else start fresh, else
	// run without persistence. Errors during session setup are
	// surfaced as run errors rather than silently degrading — if
	// the caller asked for persistence, they expect it.
	sess, history, err := resolveSession(opts, model)
	if err != nil {
		return nil, err
	}
	// Seed the new user turn. On a fresh session this is the only
	// message; on a resumed session we append after the prior turns
	// so the conversation ends with a user message (Anthropic rejects
	// requests that end on assistant — assistant-prefill is opt-in
	// and we don't use it here).
	//
	// Write-ahead: the AppendMessage below happens BEFORE provider.Send
	// so a crash/SIGKILL mid-API-call leaves the user turn durable in
	// SQLite. Resume picks up exactly where we left off — at worst the
	// model re-answers the same question, never silently drops it.
	// Don't reorder this with the for-loop below.
	if strings.TrimSpace(user) != "" {
		newUser := message.Message{
			Role:  message.RoleUser,
			Parts: []message.ContentPart{message.TextContent{Text: user}},
		}
		history = append(history, newUser)
		if sess != nil {
			if err := sess.AppendMessage(newUser, 0, 0); err != nil {
				return nil, err
			}
		}
	} else if len(history) == 0 {
		// Caller passed an empty prompt and there's no prior history
		// to continue from — nothing to send.
		return nil, fmt.Errorf("agent: prompt empty and no session history to resume")
	}
	res := &Result{}
	if sess != nil {
		res.SessionID = sess.ID
	}

	const maxTurns = 25 // pathological loops shouldn't melt billing
	// Budget-exhaustion continuation: when the model truncates a
	// response because of MaxTokens (resp.FinishReason ==
	// FinishReasonMaxTokens) and emitted no tool calls, we inject a
	// "Continue from where you stopped. No recap." user message and
	// re-call so the model can finish its thought. Cap at 3
	// consecutive continuations — beyond that the response is
	// genuinely too long and the user should split the request.
	const maxContinuations = 3
	continuations := 0

	// Graph-context injector: before each provider.Send, scan the
	// latest turn's content for file paths and prepend their
	// depth-1 callers / dependents / protected status to the system
	// role. Stops the model from having to call kai_callers itself
	// — kai's graph signal arrives whether the model asks for it
	// or not.
	graphCtx := newGraphContextInjector(opts.Graph)

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			res.FinishReason = message.FinishReasonCanceled
			if sess != nil {
				_ = sess.End(session.StatusErrored)
			}
			return res, err
		}

		systemForTurn := system
		if extra := graphCtx.buildBlock(history, opts.GateConfig.Protected); extra != "" {
			if systemForTurn == "" {
				systemForTurn = extra
			} else {
				systemForTurn = systemForTurn + "\n\n" + extra
			}
		}

		req := provider.Request{
			Model:     model,
			System:    systemForTurn,
			Messages:  history,
			Tools:     toolInfos(registry),
			MaxTokens: maxTokensPerTurn,
		}
		resp, err := opts.Provider.Send(ctx, req)
		if err != nil {
			res.FinishReason = message.FinishReasonError
			if sess != nil {
				_ = sess.End(session.StatusErrored)
			}
			return res, err
		}
		res.TokensIn += resp.InputTokens
		res.TokensOut += resp.OutputTokens
		if opts.Hooks.OnTurnComplete != nil {
			opts.Hooks.OnTurnComplete(res.TokensIn, res.TokensOut)
		}

		// Surface assistant-visible text via the hook so the TUI can
		// render the agent narrating its work.
		for _, p := range resp.Parts {
			if t, ok := p.(message.TextContent); ok && opts.Hooks.OnAssistantText != nil {
				if s := strings.TrimSpace(t.Text); s != "" {
					opts.Hooks.OnAssistantText(s)
				}
			}
		}

		// Append the assistant turn to history.
		assistantMsg := message.Message{
			Role:     message.RoleAssistant,
			Parts:    resp.Parts,
			Finished: resp.FinishReason,
			Model:    model,
		}
		history = append(history, assistantMsg)
		if sess != nil {
			// Persist with this turn's token deltas (resp's counts,
			// not res's cumulative — session row aggregates separately).
			if err := sess.AppendMessage(assistantMsg, resp.InputTokens, resp.OutputTokens); err != nil {
				return res, err
			}
		}

		// If the model didn't ask for tools, we're either done or
		// just truncated. A clean end_turn / stop_sequence finishes
		// the run; a max_tokens stop with no tool calls is a
		// truncated reply we can resume by nudging the model to
		// continue. Tool-use stops always carry tool calls — handled
		// below.
		toolCalls := extractToolCalls(resp.Parts)
		if len(toolCalls) == 0 {
			if resp.FinishReason == message.FinishReasonMaxTokens && continuations < maxContinuations {
				continuations++
				cont := message.Message{
					Role: message.RoleUser,
					Parts: []message.ContentPart{
						message.TextContent{Text: "Continue from where you stopped. No recap."},
					},
				}
				history = append(history, cont)
				if sess != nil {
					if err := sess.AppendMessage(cont, 0, 0); err != nil {
						return res, err
					}
				}
				continue
			}
			res.FinishReason = resp.FinishReason
			res.Transcript = history
			if sess != nil {
				_ = sess.End(session.StatusEnded)
			}
			return res, nil
		}
		// Model issued tool calls → it's making progress, reset the
		// continuation counter so any later truncation gets its own
		// fresh allotment of resumes.
		continuations = 0

		// Per-run token budget check. Enforce after a turn completes
		// so we always include the model's final output in the total.
		if exceeded, cap := budgetExceeded(res, opts); exceeded {
			res.FinishReason = message.FinishReasonError
			res.Transcript = history
			if sess != nil {
				_ = sess.End(session.StatusErrored)
			}
			return res, fmt.Errorf("agent: token budget exceeded (used %d, cap %d)",
				res.TokensIn+res.TokensOut, cap)
		}

		// Dispatch tool calls. Read-only tools (view, kai_callers,
		// kai_dependents, kai_context) run concurrently — they don't
		// touch the workspace and don't depend on each other, so
		// blocking any one of them on the others is wasted wall-
		// clock. Mutating tools (write, edit, bash) run serially in
		// the order the model emitted them — concurrent writes risk
		// stale-read interactions and out-of-order edits to the same
		// file. The OnToolCall hook fires from the dispatching
		// goroutine; consumers must be safe for concurrent calls
		// (the TUI's chat-activity channel is non-blocking, so it
		// is).
		resultParts := dispatchToolCalls(ctx, toolCalls, registry, opts.Hooks.OnToolCall)
		toolMsg := message.Message{
			Role:  message.RoleUser,
			Parts: resultParts,
		}
		history = append(history, toolMsg)
		if sess != nil {
			if err := sess.AppendMessage(toolMsg, 0, 0); err != nil {
				return res, err
			}
		}
	}

	res.FinishReason = message.FinishReasonError
	res.Transcript = history
	if sess != nil {
		_ = sess.End(session.StatusErrored)
	}
	return res, errors.New("agent: max turns exceeded — possible loop")
}

// resolveSession centralizes the session-setup branching:
//   - SessionStore nil + SessionID empty → no persistence; runner
//     proceeds with empty history.
//   - SessionStore set + SessionID empty → start a fresh session.
//   - SessionStore set + SessionID populated → resume; load history.
//
// Returned history may be empty (fresh session); the runner seeds it
// with the prompt above.
func resolveSession(opts Options, model string) (*session.Session, []message.Message, error) {
	if opts.SessionStore == nil {
		return nil, nil, nil
	}
	if opts.SessionID != "" {
		s, err := session.Resume(opts.SessionStore, opts.SessionID)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: resuming session %s: %w", opts.SessionID, err)
		}
		hist, err := s.History()
		if err != nil {
			return nil, nil, fmt.Errorf("agent: loading history: %w", err)
		}
		return s, hist, nil
	}
	s, err := session.New(opts.SessionStore, opts.TaskName, opts.Workspace, model)
	if err != nil {
		return nil, nil, fmt.Errorf("agent: creating session: %w", err)
	}
	return s, nil, nil
}

// buildToolRegistry registers the file tools (Slice 1) and any
// pre-built tools the caller passed. Future slices will register
// kai_* graph tools and bash here.
func buildToolRegistry(opts Options) map[string]tools.BaseTool {
	reg := map[string]tools.BaseTool{}

	ft := &tools.FileTools{
		Workspace: opts.Workspace,
		ReadOnly:  opts.ReadOnly,
		OnChange: func(rel, op string) {
			if opts.Hooks.OnFileChange != nil {
				opts.Hooks.OnFileChange(rel, op)
			}
		},
		OnBroadcast: func(rel, digest, contentBase64 string) {
			if opts.Hooks.OnFileBroadcast != nil {
				opts.Hooks.OnFileBroadcast(rel, digest, contentBase64)
			}
		},
		OnDiff: func(rel, op, patch string, added, removed int) {
			if opts.Hooks.OnFileDiff != nil {
				opts.Hooks.OnFileDiff(rel, op, patch, added, removed)
			}
			classifyAndEmit(opts, []string{rel})
		},
	}
	for _, t := range ft.All() {
		reg[t.Info().Name] = t
	}

	// Graph tools land when the caller wires the main repo's DB.
	// Tests that don't need graph context leave it nil.
	if opts.Graph != nil {
		kt := &tools.KaiTools{DB: opts.Graph}
		for _, t := range kt.All() {
			reg[t.Info().Name] = t
		}
	}

	// Bash is opt-in: tests stay shell-free by default; the TUI
	// flips EnableBash=true so the agent can run npm test, etc.
	if opts.EnableBash {
		bt := &tools.BashTool{
			Workspace: opts.Workspace,
			Allow:     opts.BashAllow,
			OnOutput: func(line string) {
				if opts.Hooks.OnBashOutput != nil {
					opts.Hooks.OnBashOutput(line)
				}
			},
			OnFilesChanged: func(paths []string) {
				classifyAndEmit(opts, paths)
			},
		}
		reg[bt.Info().Name] = bt
	}

	for _, t := range opts.ExtraTools {
		reg[t.Info().Name] = t
	}
	return reg
}

func toolInfos(reg map[string]tools.BaseTool) []tools.ToolInfo {
	out := make([]tools.ToolInfo, 0, len(reg))
	for _, t := range reg {
		out = append(out, t.Info())
	}
	return out
}

// extractToolCalls returns the ToolCall parts from a content slice.
// Pure helper kept here (not on Message) because it's only used by the
// runner; promoting it to message.Message would muddy that package's
// scope.
func extractToolCalls(parts []message.ContentPart) []message.ToolCall {
	var out []message.ToolCall
	for _, p := range parts {
		if tc, ok := p.(message.ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}

// splitSystemAndUser pulls a "System: ..." prefix off the prompt if
// the agentprompt builder emitted one. Slice 1's agentprompt produces
// a single string; future agentprompt revisions can return roles
// directly and this helper goes away.
func splitSystemAndUser(prompt string) (system, user string) {
	const sysPrefix = "System:"
	if strings.HasPrefix(prompt, sysPrefix) {
		// Take everything up to the first blank line as system.
		rest := strings.TrimPrefix(prompt, sysPrefix)
		if i := strings.Index(rest, "\n\n"); i >= 0 {
			return strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+2:])
		}
		return strings.TrimSpace(rest), ""
	}
	// No explicit system role — let the model treat the whole thing
	// as the user message and use its default system prompt. The
	// agent prompt builder already includes identity + boundaries.
	return "", prompt
}

// budgetExceeded checks the cumulative token usage against the
// per-run cap if one was set. Returns (exceeded, cap).
func budgetExceeded(res *Result, opts Options) (bool, int) {
	if opts.MaxTotalTokens <= 0 {
		return false, 0
	}
	used := res.TokensIn + res.TokensOut
	return used > opts.MaxTotalTokens, opts.MaxTotalTokens
}
