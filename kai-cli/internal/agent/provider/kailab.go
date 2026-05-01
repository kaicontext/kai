package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kai/internal/agent/message"
	"kai/internal/agent/tools"
)

// Kailab routes Anthropic Messages API calls through the kai-server
// proxy at `POST /api/v1/llm/messages`. Server-side ANTHROPIC_API_KEY
// is held by kailab; the user only needs a kailab bearer token.
//
// Unlike `internal/planner.ServerCompleter` (which is single-shot
// JSON-output for plans), this client supports tool_use blocks and
// multi-turn conversations. The two coexist on the same proxy
// endpoint — the proxy forwards the request body to Anthropic
// verbatim, so any Anthropic-compatible request shape works.
type Kailab struct {
	BaseURL    string
	AuthToken  string
	HTTPClient *http.Client

	// InitialBackoff is the first sleep between retry attempts.
	// Doubles each attempt up to MaxBackoff. Zero falls back to a
	// 1-second default so production behavior is unchanged unless a
	// caller (typically a test) explicitly shrinks it.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxAttempts    int
}

// NewKailab builds a Kailab provider. baseURL is kai-server's URL
// (e.g. https://kaicontext.com); authToken is the user's bearer.
// Both are required; nil checks happen on Send to keep construction
// trivially testable.
func NewKailab(baseURL, authToken string) *Kailab {
	return &Kailab{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		AuthToken: authToken,
		HTTPClient: &http.Client{
			// Match the kai-server-side timeout to api.anthropic.com
			// (120s). One end timing out before the other masks the
			// real failure source.
			Timeout: 120 * time.Second,
		},
	}
}

// Send translates the internal Request to Anthropic's Messages API
// shape, posts it, and translates the response back. Error messages
// from upstream are forwarded verbatim so the user sees the real
// upstream reason (rate limit, invalid model, no credit, etc.).
//
// Transient upstream errors (429 rate-limit, 529 overloaded, 502/503/
// 504 gateway hiccups, network errors) are retried with exponential
// backoff before surfacing. Non-transient errors (400 invalid request,
// 401 unauthorized, 404, 413 too-large) bubble up immediately — no
// amount of retrying fixes them and the user wants the real reason.
func (k *Kailab) Send(ctx context.Context, req Request) (Response, error) {
	if k.BaseURL == "" {
		return Response{}, fmt.Errorf("kailab provider: BaseURL not set")
	}
	if k.AuthToken == "" {
		return Response{}, fmt.Errorf("kailab provider: not logged in (run `kai auth login`)")
	}
	if req.Model == "" {
		return Response{}, fmt.Errorf("kailab provider: Model required")
	}

	body, err := json.Marshal(buildAnthropicRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("kailab provider: marshaling request: %w", err)
	}

	maxAttempts := k.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	backoff := k.InitialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	maxBackoff := k.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := k.sendOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// Honor cancellation immediately — don't retry through a
		// user-initiated Ctrl+C.
		if cerr := ctx.Err(); cerr != nil {
			return Response{}, cerr
		}
		if !isTransient(err) || attempt == maxAttempts {
			return Response{}, err
		}
		// Sleep with cancellation awareness. tea.Tick semantics: the
		// timer fires once; ctx.Done() preempts it.
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return Response{}, ctx.Err()
		case <-t.C:
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return Response{}, lastErr
}

// sendOnce performs a single HTTP round-trip. Returns a typed
// transientError for retryable upstream conditions; everything else
// is returned as a regular error.
func (k *Kailab) sendOnce(ctx context.Context, body []byte) (Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		k.BaseURL+"/api/v1/llm/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("kailab provider: building http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+k.AuthToken)

	client := k.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		// Network errors (DNS, connection reset, EOF mid-stream,
		// timeout) are transient — the next attempt may succeed.
		return Response{}, &transientError{cause: fmt.Errorf("kailab provider: sending request: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, &transientError{cause: fmt.Errorf("kailab provider: reading response: %w", err)}
	}
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Errorf("kailab provider: %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
		if isRetryableStatus(resp.StatusCode) {
			return Response{}, &transientError{cause: errMsg}
		}
		return Response{}, errMsg
	}

	var raw anthropicResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return Response{}, fmt.Errorf("kailab provider: parsing response: %w", err)
	}
	return parseAnthropicResponse(raw), nil
}

// transientError marks an error as worth retrying. Wrapping (rather
// than a status-code int) keeps the public API status-code-agnostic
// for future provider implementations that signal retry-worthiness
// differently (e.g. direct-Anthropic via SDK).
type transientError struct{ cause error }

func (t *transientError) Error() string { return t.cause.Error() }
func (t *transientError) Unwrap() error { return t.cause }

func isTransient(err error) bool {
	var te *transientError
	return errAs(err, &te)
}

// errAs is a thin wrapper around errors.As to keep the import
// surface obvious and let us tweak matching later without hunting
// through call sites.
func errAs(err error, target interface{}) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if t, ok := target.(**transientError); ok {
			if v, ok2 := err.(*transientError); ok2 {
				*t = v
				return true
			}
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// isRetryableStatus picks the upstream codes that warrant a retry:
//
//   - 408 request timeout (intermediary)
//   - 425 too early (rare, but transient by definition)
//   - 429 rate-limited (Anthropic throttling)
//   - 500 server error (genuine intermittent failure)
//   - 502/503/504 gateway hiccups
//   - 529 overloaded (Anthropic-specific "we're saturated")
//
// Notably *not* retryable: 400 (bad request — won't fix itself),
// 401 (auth — needs login), 403, 404, 413 (too-large — needs
// compaction, not a retry).
func isRetryableStatus(code int) bool {
	switch code {
	case 408, 425, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// --- request translation ---------------------------------------------

// buildAnthropicRequest converts the internal Request to the JSON
// shape Anthropic's Messages API accepts. Tool definitions are
// flattened to {name, description, input_schema}; messages are
// serialized as content-block arrays so tool_use / tool_result
// blocks fit naturally.
func buildAnthropicRequest(req Request) map[string]interface{} {
	out := map[string]interface{}{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   serializeMessages(req.Messages),
	}
	if s := strings.TrimSpace(req.System); s != "" {
		out["system"] = s
	}
	if len(req.Tools) > 0 {
		out["tools"] = serializeTools(req.Tools)
	}
	return out
}

func serializeTools(ts []tools.ToolInfo) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(ts))
	for _, t := range ts {
		schema := map[string]interface{}{
			"type":       "object",
			"properties": t.Parameters,
		}
		if len(t.Required) > 0 {
			schema["required"] = t.Required
		}
		out = append(out, map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return out
}

// serializeMessages converts our internal Message slice to Anthropic's
// message array. Each Message becomes one entry whose content is an
// array of typed blocks (text / tool_use / tool_result). Roles are
// passed through unchanged ("user", "assistant"); system role is
// hoisted to the top-level `system` field by the caller.
func serializeMessages(msgs []message.Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == message.RoleSystem {
			continue // handled by buildAnthropicRequest
		}
		blocks := make([]map[string]interface{}, 0, len(m.Parts))
		for _, p := range m.Parts {
			switch v := p.(type) {
			case message.TextContent:
				blocks = append(blocks, map[string]interface{}{
					"type": "text",
					"text": v.Text,
				})
			case message.ToolCall:
				var input map[string]interface{}
				_ = json.Unmarshal([]byte(v.Input), &input)
				if input == nil {
					input = map[string]interface{}{}
				}
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    v.ID,
					"name":  v.Name,
					"input": input,
				})
			case message.ToolResult:
				blocks = append(blocks, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": v.ToolCallID,
					"content":     v.Content,
					"is_error":    v.IsError,
				})
			}
		}
		out = append(out, map[string]interface{}{
			"role":    string(m.Role),
			"content": blocks,
		})
	}
	return out
}

// --- response translation --------------------------------------------

// anthropicResponse mirrors Anthropic's Messages API response shape,
// limited to the fields the runner consumes.
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicContent struct {
	Type string `json:"type"`
	// text block
	Text string `json:"text,omitempty"`
	// tool_use block
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// thinking block (optional, when extended thinking is on)
	Thinking string `json:"thinking,omitempty"`
}

// parseAnthropicResponse maps the wire shape back into our internal
// ContentPart slice + finish reason. Unknown content-block types are
// ignored (forward compat with future Anthropic additions).
func parseAnthropicResponse(raw anthropicResponse) Response {
	out := Response{
		FinishReason: mapStopReason(raw.StopReason),
		InputTokens:  raw.Usage.InputTokens,
		OutputTokens: raw.Usage.OutputTokens,
	}
	for _, c := range raw.Content {
		switch c.Type {
		case "text":
			out.Parts = append(out.Parts, message.TextContent{Text: c.Text})
		case "thinking":
			out.Parts = append(out.Parts, message.ReasoningContent{Thinking: c.Thinking})
		case "tool_use":
			inputJSON, _ := json.Marshal(c.Input)
			out.Parts = append(out.Parts, message.ToolCall{
				ID:       c.ID,
				Name:     c.Name,
				Input:    string(inputJSON),
				Type:     "tool_use",
				Finished: true,
			})
		}
	}
	return out
}

func mapStopReason(r string) message.FinishReason {
	switch r {
	case "end_turn":
		return message.FinishReasonEndTurn
	case "tool_use":
		return message.FinishReasonToolUse
	case "max_tokens":
		return message.FinishReasonMaxTokens
	case "stop_sequence":
		return message.FinishReasonEndTurn
	default:
		return message.FinishReasonUnknown
	}
}
