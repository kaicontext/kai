// Package provider abstracts LLM providers behind a single Send call
// that the agent runner uses on every turn. The runner doesn't care
// whether requests go to api.anthropic.com directly, through kailab's
// proxy, or through some other vendor — only that Send takes a
// uniform Request and returns a uniform Response.
//
// Slice 1 ships one implementation: KailabProvider in `kailab.go`,
// which routes through the kai-server's `POST /api/v1/llm/messages`
// endpoint using the user's stored bearer token. Direct-Anthropic
// (with a per-developer API key) is a deferred slice; on-prem
// deployments will need it eventually but no v1 user does.
package provider

import (
	"context"

	"kai/internal/agent/message"
	"kai/internal/agent/tools"
)

// Request is one turn worth of input to the model.
type Request struct {
	// Model is the Anthropic model id (e.g. "claude-sonnet-4-6").
	// Required: empty string is a misconfiguration, not a default.
	Model string

	// System is the top-level system prompt. May be empty for
	// follow-up turns where the system prompt is already implicit.
	System string

	// Messages is the conversation history including the latest
	// user turn. Tool results from previous turns appear as parts
	// of user-role messages here.
	Messages []message.Message

	// Tools is the catalog of tools the model may call this turn.
	// Each tool's schema is sent as JSON-Schema; the model produces
	// matching ToolCalls in the response.
	Tools []tools.ToolInfo

	// MaxTokens caps a single response. The runner sums these to
	// enforce per-run budgets.
	MaxTokens int
}

// Response is the model's reply for one turn.
type Response struct {
	// Parts is the structured content the model produced. For a
	// non-tool turn this is typically a single TextContent. For a
	// tool turn it includes ToolCall parts the runner dispatches.
	Parts []message.ContentPart

	// FinishReason matches the model's stop reason. When this is
	// FinishReasonToolUse the runner runs tools and loops; when it's
	// FinishReasonEndTurn the runner exits cleanly.
	FinishReason message.FinishReason

	// InputTokens / OutputTokens are billed-as accounting. Plumbed
	// for the orchestrator's MaxAgentTokens cap.
	InputTokens  int
	OutputTokens int
}

// Provider sends a Request to an LLM and returns its Response.
// Implementations must be safe to call concurrently from multiple
// goroutines (the orchestrator may run agents in parallel).
type Provider interface {
	Send(ctx context.Context, req Request) (Response, error)
}
