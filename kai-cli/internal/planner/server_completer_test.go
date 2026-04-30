package planner

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kai/internal/ai"
)

// TestServerCompleter_Success verifies the happy path: client sends
// the right shape, server returns Anthropic-shaped JSON, completer
// extracts the text block.
func TestServerCompleter_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/llm/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/wrong auth header: %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["max_tokens"].(float64) != 100 {
			t.Errorf("max_tokens forwarded incorrectly: %v", body["max_tokens"])
		}
		// Send back an Anthropic-shaped response.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ai.Response{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: "hello from server"}},
			StopReason: "end_turn",
		})
	}))
	defer srv.Close()

	c := NewServerCompleter(srv.URL, "test-token", "")
	out, err := c.Complete("you are a planner", []ai.Message{
		{Role: "user", Content: "do something"},
	}, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello from server" {
		t.Errorf("unexpected response: %q", out)
	}
}

// TestServerCompleter_PropagatesUpstreamError verifies that a non-200
// status from the proxy is surfaced verbatim — important so the user
// sees rate-limit or model-not-found messages without an opaque wrapper.
func TestServerCompleter_PropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error": "rate_limited"}`)
	}))
	defer srv.Close()

	c := NewServerCompleter(srv.URL, "test-token", "")
	_, err := c.Complete("", []ai.Message{{Role: "user", Content: "x"}}, 50)
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate_limited") {
		t.Fatalf("expected upstream-error message, got %v", err)
	}
}

// TestServerCompleter_RejectsMissingAuth: a sane error fires before
// hitting the network when the user isn't logged in.
func TestServerCompleter_RejectsMissingAuth(t *testing.T) {
	c := NewServerCompleter("https://example.invalid", "", "")
	_, err := c.Complete("", []ai.Message{{Role: "user", Content: "x"}}, 50)
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

// TestServerCompleter_RejectsMissingURL same shape, missing baseURL.
func TestServerCompleter_RejectsMissingURL(t *testing.T) {
	c := NewServerCompleter("", "tok", "")
	_, err := c.Complete("", []ai.Message{{Role: "user", Content: "x"}}, 50)
	if err == nil || !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("expected URL error, got %v", err)
	}
}
