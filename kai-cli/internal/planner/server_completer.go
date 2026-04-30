package planner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kai/internal/ai"
)

// ServerCompleter calls the kailab-control LLM proxy at
// /api/v1/llm/messages instead of going direct to api.anthropic.com.
// This is the production path: developers don't hold their own
// Anthropic API key, the server does, and the server can do rate
// limiting / billing / audit / caching in one place.
//
// Compared to NewAIAdapter (direct-to-Anthropic), the wire format is
// identical — kailab-control's handler forwards verbatim — so the
// only differences here are the URL and the auth header.
type ServerCompleter struct {
	BaseURL    string        // kailab-control base URL, e.g. https://kaicontext.com
	AuthToken  string        // Bearer token from ~/.kai/credentials.json
	Model      string        // Anthropic model id (e.g. "claude-sonnet-4-6")
	HTTPClient *http.Client  // optional; defaults to a 120s-timeout client
}

// NewServerCompleter constructs a Completer that routes through the
// kai-server. BaseURL must be the kailab-control endpoint (NOT a
// per-repo data-plane URL); AuthToken must be a valid bearer token
// (typically from remote.GetValidAccessToken); model picks which
// Anthropic model to use ("claude-sonnet-4-6", "claude-haiku-4-5",
// etc.). Empty model falls back to a sensible default.
func NewServerCompleter(baseURL, authToken, model string) *ServerCompleter {
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &ServerCompleter{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		AuthToken: authToken,
		Model:     model,
		HTTPClient: &http.Client{
			// 120s matches the server-side timeout to api.anthropic.com.
			// One end timing out before the other masks the real cause.
			Timeout: 120 * time.Second,
		},
	}
}

// Complete satisfies the Completer interface used by Plan / Replan.
// Builds the same Anthropic-shaped JSON the direct adapter does and
// posts to the server's proxy endpoint.
func (c *ServerCompleter) Complete(system string, messages []ai.Message, maxTokens int) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("planner: server completer has no BaseURL")
	}
	if c.AuthToken == "" {
		return "", fmt.Errorf("planner: server completer has no auth token (run `kai auth login`)")
	}

	// Match the schema kailab-control's handler validates: model,
	// max_tokens, messages required; system optional. Model is set
	// to a sane default; the planner caller will eventually thread
	// its config through if model selection moves into the wire.
	body, err := json.Marshal(map[string]interface{}{
		"model":      c.Model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   messages,
	})
	if err != nil {
		return "", fmt.Errorf("planner: marshaling request: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/llm/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("planner: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AuthToken)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("planner: sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("planner: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Surface the upstream message verbatim — the kailab-control
		// proxy forwards Anthropic's error responses, so this gives
		// the user the actual upstream reason (rate-limited, model
		// invalid, etc.) rather than a generic wrapper.
		return "", fmt.Errorf("planner: server returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Parse Anthropic's response shape and return the first text block.
	// Same shape as ai.Response so we reuse it directly.
	var apiResp ai.Response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("planner: unmarshaling response: %w", err)
	}
	if apiResp.Error != nil {
		return "", fmt.Errorf("planner: API error: %s: %s",
			apiResp.Error.Type, apiResp.Error.Message)
	}
	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("planner: empty response")
	}
	return apiResp.Content[0].Text, nil
}
