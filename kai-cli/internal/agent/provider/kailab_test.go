package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kai/internal/agent/message"
	"kai/internal/agent/tools"
)

// TestKailab_TranslatesRequestAndResponse drives a full round-trip
// against a fake kai-server: sends an Anthropic-shaped request, gets
// back a tool_use block, parses it. Pins the wire format so when
// Anthropic adds new content-block types we know to update.
func TestKailab_TranslatesRequestAndResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/llm/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing auth header: %q", got)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "claude-sonnet-4-6" {
			t.Errorf("model wrong: %v", req["model"])
		}
		// Tools serialized as expected
		toolsList, _ := req["tools"].([]interface{})
		if len(toolsList) != 1 {
			t.Fatalf("expected 1 tool, got %v", toolsList)
		}
		// Messages: one user message with one text block
		msgs, _ := req["messages"].([]interface{})
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %v", msgs)
		}

		// Send back a synthetic tool_use response
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant",
			"content":[
				{"type":"text","text":"let me check"},
				{"type":"tool_use","id":"toolu_1","name":"view","input":{"file_path":"x.go"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":50,"output_tokens":12}
		}`))
	}))
	defer srv.Close()

	k := NewKailab(srv.URL, "test-token")
	resp, err := k.Send(context.Background(), Request{
		Model:     "claude-sonnet-4-6",
		System:    "you are an agent",
		Messages:  []message.Message{{Role: message.RoleUser, Parts: []message.ContentPart{message.TextContent{Text: "look"}}}},
		Tools:     []tools.ToolInfo{{Name: "view", Description: "read file", Parameters: map[string]any{"file_path": map[string]any{"type": "string"}}, Required: []string{"file_path"}}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != message.FinishReasonToolUse {
		t.Errorf("expected tool_use, got %s", resp.FinishReason)
	}
	if resp.InputTokens != 50 || resp.OutputTokens != 12 {
		t.Errorf("token counts wrong: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Parts))
	}
	tc, ok := resp.Parts[1].(message.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %T", resp.Parts[1])
	}
	if tc.Name != "view" || !strings.Contains(tc.Input, "x.go") {
		t.Errorf("tool call wrong: %+v", tc)
	}
}

func TestKailab_PropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer srv.Close()
	k := NewKailab(srv.URL, "tok")
	_, err := k.Send(context.Background(), Request{
		Model:     "x",
		Messages:  []message.Message{{Role: message.RoleUser, Parts: []message.ContentPart{message.TextContent{Text: "x"}}}},
		MaxTokens: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("expected upstream error to be forwarded, got %v", err)
	}
}

func TestKailab_RejectsMissingAuth(t *testing.T) {
	k := NewKailab("https://example.invalid", "")
	_, err := k.Send(context.Background(), Request{Model: "x", MaxTokens: 1})
	if err == nil || !strings.Contains(err.Error(), "logged in") {
		t.Fatalf("expected login hint, got %v", err)
	}
}

func TestKailab_SerializesToolResults(t *testing.T) {
	// Verify that tool_result messages translate to the wire shape
	// Anthropic expects (block under user role with tool_use_id).
	captured := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r.Body)
		captured = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()
	k := NewKailab(srv.URL, "tok")
	_, err := k.Send(context.Background(), Request{
		Model: "x",
		Messages: []message.Message{
			{Role: message.RoleUser, Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "tu_1", Name: "view", Content: "alpha"},
			}},
		},
		MaxTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tool_result"`, `"tool_use_id":"tu_1"`, `"content":"alpha"`} {
		if !strings.Contains(captured, want) {
			t.Errorf("serialized body missing %s\nfull: %s", want, captured)
		}
	}
}

// readAll is a tiny helper to avoid an extra import inline.
func readAll(r interface {
	Read([]byte) (int, error)
}) ([]byte, error) {
	var b []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return b, nil
			}
			return b, err
		}
	}
}
