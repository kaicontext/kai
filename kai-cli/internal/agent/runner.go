package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"kai/internal/agent/message"
	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agent/tools"
)

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
	if len(history) == 0 {
		history = []message.Message{{
			Role:  message.RoleUser,
			Parts: []message.ContentPart{message.TextContent{Text: user}},
		}}
		if sess != nil {
			if err := sess.AppendMessage(history[0], 0, 0); err != nil {
				return nil, err
			}
		}
	}
	res := &Result{}
	if sess != nil {
		res.SessionID = sess.ID
	}

	const maxTurns = 25 // pathological loops shouldn't melt billing
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			res.FinishReason = message.FinishReasonCanceled
			if sess != nil {
				_ = sess.End(session.StatusErrored)
			}
			return res, err
		}

		req := provider.Request{
			Model:     model,
			System:    system,
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

		// If the model didn't ask for tools, we're done.
		toolCalls := extractToolCalls(resp.Parts)
		if len(toolCalls) == 0 {
			res.FinishReason = resp.FinishReason
			res.Transcript = history
			if sess != nil {
				_ = sess.End(session.StatusEnded)
			}
			return res, nil
		}

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

		// Dispatch each tool call and append a single user-role
		// message containing all the tool_result blocks. Anthropic's
		// API accepts multiple tool_result parts per message; one
		// message keeps the conversation graph small.
		resultParts := make([]message.ContentPart, 0, len(toolCalls))
		for _, call := range toolCalls {
			if opts.Hooks.OnToolCall != nil {
				opts.Hooks.OnToolCall(call.Name, call.Input)
			}
			tool, ok := registry[call.Name]
			if !ok {
				resultParts = append(resultParts, message.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    fmt.Sprintf("unknown tool: %s", call.Name),
					IsError:    true,
				})
				continue
			}
			tr, err := tool.Run(ctx, tools.ToolCall{
				ID:    call.ID,
				Name:  call.Name,
				Input: call.Input,
			})
			if err != nil {
				resultParts = append(resultParts, message.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    fmt.Sprintf("tool error: %s", err.Error()),
					IsError:    true,
				})
				continue
			}
			resultParts = append(resultParts, message.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    tr.Content,
				Metadata:   tr.Metadata,
				IsError:    tr.IsError,
			})
		}
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
