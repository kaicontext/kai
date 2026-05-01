package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"kai/internal/agent/message"
	"kai/internal/agent/provider"
	"kai/internal/agent/session"
	"kai/internal/agent/tools"
)

// fakeProvider returns canned Responses in order. Lets the runner test
// scripted multi-turn flows without touching a real LLM. Each call to
// Send pops the next response from the queue.
type fakeProvider struct {
	queue []provider.Response
	calls int32
	last  provider.Request
}

func (f *fakeProvider) Send(_ context.Context, req provider.Request) (provider.Response, error) {
	atomic.AddInt32(&f.calls, 1)
	f.last = req
	if len(f.queue) == 0 {
		return provider.Response{}, errors.New("fakeProvider: queue empty")
	}
	r := f.queue[0]
	f.queue = f.queue[1:]
	return r, nil
}

// TestRunLoop_ToolUseThenEndTurn covers the canonical two-turn dance:
// model asks for a tool, runner dispatches, runner re-prompts, model
// returns text and end_turn.
func TestRunLoop_ToolUseThenEndTurn(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &fakeProvider{queue: []provider.Response{
		{
			Parts: []message.ContentPart{
				message.ToolCall{
					ID:    "call-1",
					Name:  "view",
					Input: `{"file_path":"hello.txt"}`,
					Type:  "tool_use",
				},
			},
			FinishReason: message.FinishReasonToolUse,
			OutputTokens: 10,
		},
		{
			Parts: []message.ContentPart{
				message.TextContent{Text: "I read hello.txt and it has two lines."},
			},
			FinishReason: message.FinishReasonEndTurn,
			OutputTokens: 25,
		},
	}}

	res, err := Run(context.Background(), Options{
		Workspace: ws,
		Prompt:    "Read hello.txt and tell me what it says.",
		Model:     "claude-sonnet-4-6",
		Provider:  p,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.FinishReason != message.FinishReasonEndTurn {
		t.Errorf("expected end_turn, got %s", res.FinishReason)
	}
	if p.calls != 2 {
		t.Errorf("expected 2 provider calls, got %d", p.calls)
	}
	// Transcript should be: user, assistant(tool_use), user(tool_result), assistant(text)
	if got := len(res.Transcript); got != 4 {
		t.Fatalf("expected 4 transcript entries, got %d: %+v", got, res.Transcript)
	}
	if res.Transcript[2].Role != message.RoleUser {
		t.Errorf("third entry should be user (tool result), got %s", res.Transcript[2].Role)
	}
	// Tool result content should mention the file's lines
	tr, _ := res.Transcript[2].Parts[0].(message.ToolResult)
	if !strings.Contains(tr.Content, "first") || !strings.Contains(tr.Content, "second") {
		t.Errorf("tool result missing file content: %q", tr.Content)
	}
}

// TestRunLoop_FileWriteFiresHook ensures the OnFileChange hook is
// called from inside the write tool — that's the linchpin for live
// activity in the TUI's sync pane.
func TestRunLoop_FileWriteFiresHook(t *testing.T) {
	ws := t.TempDir()
	var got struct {
		path string
		op   string
	}
	hookCalls := 0

	p := &fakeProvider{queue: []provider.Response{
		{
			Parts: []message.ContentPart{
				message.ToolCall{
					ID:    "c1",
					Name:  "write",
					Input: marshalInput(map[string]interface{}{"file_path": "out.txt", "content": "hi"}),
					Type:  "tool_use",
				},
			},
			FinishReason: message.FinishReasonToolUse,
		},
		{
			Parts:        []message.ContentPart{message.TextContent{Text: "done"}},
			FinishReason: message.FinishReasonEndTurn,
		},
	}}

	_, err := Run(context.Background(), Options{
		Workspace: ws,
		Prompt:    "write hi",
		Provider:  p,
		Hooks: Hooks{
			OnFileChange: func(rel, op string) {
				hookCalls++
				got.path = rel
				got.op = op
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hookCalls != 1 {
		t.Errorf("expected 1 hook call, got %d", hookCalls)
	}
	if got.path != "out.txt" || got.op != "created" {
		t.Errorf("hook fired with wrong args: %+v", got)
	}
	// File should exist on disk too
	body, err := os.ReadFile(filepath.Join(ws, "out.txt"))
	if err != nil || string(body) != "hi" {
		t.Errorf("write did not produce file: err=%v body=%q", err, string(body))
	}
}

// TestRunLoop_RejectsMissingProvider catches the most common
// misconfiguration: orchestrator forgot to set AgentProvider.
func TestRunLoop_RejectsMissingProvider(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Workspace: t.TempDir(),
		Prompt:    "x",
	})
	if err == nil || !strings.Contains(err.Error(), "Provider") {
		t.Fatalf("expected Provider-required error, got %v", err)
	}
}

// TestRunLoop_TokenBudgetEnforced verifies MaxTotalTokens trips after
// the cumulative cap is exceeded. The run still includes the turn that
// pushed it over (so the user sees the model's last output).
func TestRunLoop_TokenBudgetEnforced(t *testing.T) {
	p := &fakeProvider{queue: []provider.Response{
		{
			// One tool call so the runner loops at least once.
			Parts: []message.ContentPart{
				message.ToolCall{
					ID:    "c1",
					Name:  "view",
					Input: `{"file_path":"missing.txt"}`,
					Type:  "tool_use",
				},
			},
			FinishReason: message.FinishReasonToolUse,
			InputTokens:  500,
			OutputTokens: 600, // total 1100, over the 1000 cap
		},
		// Should never be called — budget trips before turn 2.
	}}
	_, err := Run(context.Background(), Options{
		Workspace:      t.TempDir(),
		Prompt:         "x",
		Provider:       p,
		MaxTotalTokens: 1000,
	})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected budget error, got %v", err)
	}
	if p.calls != 1 {
		t.Errorf("expected 1 provider call before budget trip, got %d", p.calls)
	}
}

// TestRunLoop_ContextCancellation shuts down a running loop cleanly
// when ctx is canceled between turns.
func TestRunLoop_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Options{
		Workspace: t.TempDir(),
		Prompt:    "x",
		Provider:  &fakeProvider{queue: []provider.Response{{}}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func marshalInput(m map[string]interface{}) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// TestRunLoop_PersistsToSession runs a 2-turn agent with a session
// store wired and confirms the transcript landed in the DB.
func TestRunLoop_PersistsToSession(t *testing.T) {
	db, err := openSessionTestDB()
	if err != nil {
		t.Fatalf("session db: %v", err)
	}
	defer db.Close()
	if err := session.EnsureSchema(dbAdapter{db}); err != nil {
		t.Fatal(err)
	}

	p := &fakeProvider{queue: []provider.Response{
		{
			Parts: []message.ContentPart{
				message.TextContent{Text: "ok"},
			},
			FinishReason: message.FinishReasonEndTurn,
			InputTokens:  20,
			OutputTokens: 10,
		},
	}}

	res, err := Run(context.Background(), Options{
		Workspace:    t.TempDir(),
		Prompt:       "say ok",
		Provider:     p,
		SessionStore: dbAdapter{db},
		TaskName:     "smoke",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("expected SessionID populated when SessionStore is set")
	}

	// Read back history via session.Resume so we exercise the full
	// round-trip including type-discriminated part decoding.
	resumed, err := session.Resume(dbAdapter{db}, res.SessionID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	hist, err := resumed.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 messages (user + assistant), got %d", len(hist))
	}
	if hist[0].Role != message.RoleUser || hist[1].Role != message.RoleAssistant {
		t.Errorf("roles: %v / %v", hist[0].Role, hist[1].Role)
	}
	if resumed.Status != session.StatusEnded {
		t.Errorf("status: %s (expected ended after clean exit)", resumed.Status)
	}
}

// TestRunLoop_ResumesFromExistingSession seeds a session with prior
// turns, then runs the agent with that SessionID. The runner must
// load history and pass it to the provider's first call.
func TestRunLoop_ResumesFromExistingSession(t *testing.T) {
	db, err := openSessionTestDB()
	if err != nil {
		t.Fatalf("session db: %v", err)
	}
	defer db.Close()
	store := dbAdapter{db}
	if err := session.EnsureSchema(store); err != nil {
		t.Fatal(err)
	}

	// Seed a fresh session with one user turn.
	prior, err := session.New(store, "resumed", "/ws", "claude")
	if err != nil {
		t.Fatal(err)
	}
	_ = prior.AppendMessage(message.Message{
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextContent{Text: "earlier message"}},
	}, 0, 0)

	p := &fakeProvider{queue: []provider.Response{
		{
			Parts:        []message.ContentPart{message.TextContent{Text: "got it"}},
			FinishReason: message.FinishReasonEndTurn,
		},
	}}

	res, err := Run(context.Background(), Options{
		Workspace:    t.TempDir(),
		Prompt:       "follow-up question",
		Provider:     p,
		SessionStore: store,
		SessionID:    prior.ID,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.SessionID != prior.ID {
		t.Errorf("SessionID changed across resume: %s -> %s", prior.ID, res.SessionID)
	}
	// The provider must have received both the prior user message
	// AND the new user turn — the conversation must end with a user
	// message or Anthropic rejects the call (no assistant prefill).
	got := p.last.Messages
	if len(got) != 2 || got[0].Role != message.RoleUser || got[1].Role != message.RoleUser {
		t.Fatalf("expected prior + new user messages, got %+v", got)
	}
	if t1 := got[0].Parts[0].(message.TextContent).Text; t1 != "earlier message" {
		t.Errorf("first message text: %q", t1)
	}
	if t2 := got[1].Parts[0].(message.TextContent).Text; t2 != "follow-up question" {
		t.Errorf("second message text: %q", t2)
	}
	// History after the run: prior user + new user + assistant reply.
	hist, _ := prior.History()
	if len(hist) != 3 {
		t.Errorf("expected 3 messages after resume run, got %d", len(hist))
	}
}

// TestRunLoop_BudgetContinuation: a max_tokens stop with no tool
// calls injects a "continue" prompt and re-calls the provider. Caps
// at 3 continuations.
func TestRunLoop_BudgetContinuation(t *testing.T) {
	p := &fakeProvider{queue: []provider.Response{
		{
			Parts:        []message.ContentPart{message.TextContent{Text: "first half"}},
			FinishReason: message.FinishReasonMaxTokens,
		},
		{
			Parts:        []message.ContentPart{message.TextContent{Text: "second half"}},
			FinishReason: message.FinishReasonEndTurn,
		},
	}}
	res, err := Run(context.Background(), Options{
		Workspace: t.TempDir(),
		Prompt:    "write a long essay",
		Provider:  p,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("expected 2 provider calls (truncated + continuation), got %d", p.calls)
	}
	// History should contain the injected "Continue from where you
	// stopped" user message between the two assistant turns.
	gotContinue := false
	for _, m := range res.Transcript {
		if m.Role != message.RoleUser {
			continue
		}
		for _, part := range m.Parts {
			if t, ok := part.(message.TextContent); ok && strings.Contains(t.Text, "Continue from where you stopped") {
				gotContinue = true
			}
		}
	}
	if !gotContinue {
		t.Errorf("continuation prompt not found in transcript")
	}
}

// TestRunLoop_BudgetContinuationCap: after 3 continuations the runner
// gives up rather than looping forever on a model that won't stop
// truncating.
func TestRunLoop_BudgetContinuationCap(t *testing.T) {
	queue := make([]provider.Response, 0, 5)
	for i := 0; i < 5; i++ {
		queue = append(queue, provider.Response{
			Parts:        []message.ContentPart{message.TextContent{Text: "more..."}},
			FinishReason: message.FinishReasonMaxTokens,
		})
	}
	p := &fakeProvider{queue: queue}
	_, err := Run(context.Background(), Options{
		Workspace: t.TempDir(),
		Prompt:    "endless",
		Provider:  p,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Initial call + 3 continuations = 4 total. Beyond that the
	// runner returns the truncated reply rather than re-calling.
	if p.calls != 4 {
		t.Errorf("expected 4 calls (initial + 3 continuations), got %d", p.calls)
	}
}

// TestRunLoop_ConcurrentReadDispatch verifies that read-only tools in
// the same batch run in parallel. We measure by recording overlap
// between two view tools' Run goroutines.
func TestRunLoop_ConcurrentReadDispatch(t *testing.T) {
	ws := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(ws, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A tool that records when it starts/stops so we can detect
	// overlap. Wraps view to keep behavior real.
	starts := make(chan struct{}, 2)
	releases := make(chan struct{})
	slowView := &slowTool{
		name: "view",
		run: func(ctx context.Context, c tools.ToolCall) (tools.ToolResponse, error) {
			starts <- struct{}{}
			<-releases
			return tools.ToolResponse{Content: "ok"}, nil
		},
	}

	p := &fakeProvider{queue: []provider.Response{
		{
			Parts: []message.ContentPart{
				message.ToolCall{ID: "1", Name: "view", Input: `{"file_path":"a.txt"}`, Type: "tool_use"},
				message.ToolCall{ID: "2", Name: "view", Input: `{"file_path":"b.txt"}`, Type: "tool_use"},
			},
			FinishReason: message.FinishReasonToolUse,
		},
		{
			Parts:        []message.ContentPart{message.TextContent{Text: "done"}},
			FinishReason: message.FinishReasonEndTurn,
		},
	}}

	// Drive the test: kick off Run in a goroutine, wait for both
	// tools to be in-flight, then release them. If dispatch were
	// serial the second start would block on the first's release
	// and the test would deadlock.
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), Options{
			Workspace:  ws,
			Prompt:     "go",
			Provider:   p,
			ExtraTools: []tools.BaseTool{slowView},
		})
		done <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-starts:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d tool start(s) observed before timeout — dispatch is serial", i)
		}
	}
	close(releases)

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// slowTool is a controllable BaseTool used to verify dispatch
// concurrency. The runner finds it by name in the registry; "view"
// here shadows the real view tool for the duration of the test.
type slowTool struct {
	name string
	run  func(ctx context.Context, c tools.ToolCall) (tools.ToolResponse, error)
}

func (s *slowTool) Info() tools.ToolInfo {
	return tools.ToolInfo{Name: s.name, Description: "test", Parameters: map[string]any{}}
}
func (s *slowTool) Run(ctx context.Context, c tools.ToolCall) (tools.ToolResponse, error) {
	return s.run(ctx, c)
}

// dbAdapter mirrors the one in session_test.go but local here because
// Go won't share package-internal helpers across _test.go files in
// different packages. Tiny, no maintenance burden.
type dbAdapter struct{ *sql.DB }

func openSessionTestDB() (*sql.DB, error) {
	return sql.Open("sqlite", ":memory:")
}
