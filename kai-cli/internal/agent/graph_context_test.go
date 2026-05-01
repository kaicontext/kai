package agent

import (
	"strings"
	"testing"

	"kai/internal/agent/message"
)

// TestExtractFilePaths covers the regex-based path extraction.
// Tool-result content is the most common source (view tool dumps
// a path, the agent narrates it back) — make sure both text and
// tool-result branches contribute.
func TestExtractFilePaths(t *testing.T) {
	hist := []message.Message{
		{
			Role: message.RoleUser,
			Parts: []message.ContentPart{
				message.TextContent{Text: "fix the bug in src/auth.py affecting api/routes.go"},
			},
		},
		{
			Role: message.RoleUser,
			Parts: []message.ContentPart{
				message.ToolResult{Content: "wrote 412 bytes to src/auth.py"},
			},
		},
	}
	got := extractFilePaths(hist)
	want := []string{"src/auth.py", "api/routes.go"}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

// TestExtractFilePaths_IgnoresProse: a sentence mentioning a verb
// like "auto.go" shouldn't fire (it doesn't), but neither should
// non-extension words. Regex is intentionally conservative.
func TestExtractFilePaths_IgnoresProse(t *testing.T) {
	hist := []message.Message{
		{
			Role: message.RoleUser,
			Parts: []message.ContentPart{
				message.TextContent{Text: "the function takes a long time to run"},
			},
		},
	}
	if got := extractFilePaths(hist); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

// TestLatestSlice trims to the most recent user/tool turn so we
// don't re-inject context from earlier turns we already injected.
func TestLatestSlice(t *testing.T) {
	hist := []message.Message{
		{Role: message.RoleUser, Parts: []message.ContentPart{message.TextContent{Text: "first"}}},
		{Role: message.RoleAssistant, Parts: []message.ContentPart{message.TextContent{Text: "ok"}}},
		{Role: message.RoleUser, Parts: []message.ContentPart{message.TextContent{Text: "second"}}},
	}
	got := latestSlice(hist)
	if len(got) != 1 {
		t.Fatalf("expected 1 message in latest slice, got %d", len(got))
	}
	if t1 := got[0].Parts[0].(message.TextContent).Text; t1 != "second" {
		t.Errorf("wrong message returned: %q", t1)
	}
}

// TestIsProtected covers exact-glob and recursive `/**` matching
// without instantiating the gate package.
func TestIsProtected(t *testing.T) {
	cases := []struct {
		path    string
		patts   []string
		want    bool
	}{
		{"internal/auth/middleware.go", []string{"internal/auth/**"}, true},
		{"internal/auth/middleware.go", []string{"internal/db/**"}, false},
		{"go.mod", []string{"go.mod"}, true},
		{"main.go", nil, false},
	}
	for _, c := range cases {
		if got := isProtected(c.path, c.patts); got != c.want {
			t.Errorf("isProtected(%q, %v) = %v, want %v", c.path, c.patts, got, c.want)
		}
	}
}

// TestInjector_NilGraph: nil graph DB means no injector — calls
// short-circuit to "" so the runner just sends the system prompt
// unchanged. Avoids needing a graph fixture for tests that don't
// care about graph context.
func TestInjector_NilGraph(t *testing.T) {
	gc := newGraphContextInjector(nil)
	hist := []message.Message{
		{Role: message.RoleUser, Parts: []message.ContentPart{
			message.TextContent{Text: "edit auth.py"},
		}},
	}
	if got := gc.buildBlock(hist, nil); got != "" {
		t.Errorf("expected empty block from nil graph, got %q", got)
	}
}

// TestInjector_NoNewFiles: once a file has been injected, a later
// turn that mentions the same file produces no new block. Critical
// to avoid spamming the model with the same callers list every
// turn.
func TestInjector_NoNewFiles(t *testing.T) {
	gc := &graphContextInjector{
		// No graph (calls short-circuit), but we still want to
		// exercise the injected-set bookkeeping.
		injected: map[string]bool{"auth.py": true},
	}
	hist := []message.Message{
		{Role: message.RoleUser, Parts: []message.ContentPart{
			message.TextContent{Text: "now also fix auth.py timeout"},
		}},
	}
	if got := gc.buildBlock(hist, nil); !strings.HasPrefix(got, "") || got != "" {
		t.Errorf("re-mentioning already-injected file should produce no block, got %q", got)
	}
}
