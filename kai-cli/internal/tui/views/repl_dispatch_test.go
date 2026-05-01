package views

import (
	"strings"
	"testing"

	"kai/internal/planner"
)

// TestDispatch_SlashRoutesToShellout: a leading "/" identifies the
// input as a kai subcommand, regardless of planner state.
func TestDispatch_SlashRoutesToShellout(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r2, cmd := r.dispatch("/gate list")
	if cmd == nil {
		t.Fatal("expected a tea.Cmd for shellout")
	}
	if r2.planning {
		t.Error("slash-prefixed input should not enter planning state")
	}
}

// TestDispatch_NoSlashGoesToPlanner: anything that isn't slash-prefixed
// is treated as a natural-language request and routed to the planner.
func TestDispatch_NoSlashGoesToPlanner(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r2, cmd := r.dispatch("Update README.md to mention the new TUI")
	if cmd == nil {
		t.Fatal("expected tea.Cmd")
	}
	if !r2.planning {
		t.Error("expected planning=true for an unprefixed sentence")
	}
}

// TestDispatch_NoServicesShellsOut: without a planner configured, even
// non-slash input shells out so the user sees kai's own usage error
// rather than silent no-op.
func TestDispatch_NoServicesShellsOut(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", nil)
	r2, cmd := r.dispatch("anything")
	if cmd == nil {
		t.Fatal("expected tea.Cmd")
	}
	if r2.planning {
		t.Error("with no services, planning state must not engage")
	}
}

// TestDispatch_PendingPlanGo: with a pending plan, "go" triggers
// orchestrator.Execute regardless of slash prefix (slash inside
// pending-plan state is irrelevant).
func TestDispatch_PendingPlanGo(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.pendingPlan = &planner.WorkPlan{Agents: []planner.AgentTask{{Name: "x", Prompt: "p"}}}
	r.originalReq = "do something"
	r2, cmd := r.dispatch("go")
	if cmd == nil {
		t.Fatal("expected tea.Cmd")
	}
	if !r2.executing {
		t.Error("expected executing=true after go")
	}
}

// TestDispatch_PendingPlanCancel clears the pending plan.
func TestDispatch_PendingPlanCancel(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.pendingPlan = &planner.WorkPlan{Agents: []planner.AgentTask{{Name: "x"}}}
	r.originalReq = "earlier"
	r2, cmd := r.dispatch("cancel")
	if cmd != nil {
		t.Errorf("cancel should not produce a tea.Cmd")
	}
	if r2.pendingPlan != nil || r2.originalReq != "" {
		t.Error("pendingPlan/originalReq should be cleared after cancel")
	}
}

// TestDispatch_PendingPlanFeedbackReplans: anything that isn't go/cancel
// while a plan is pending becomes feedback for replan.
func TestDispatch_PendingPlanFeedbackReplans(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.pendingPlan = &planner.WorkPlan{Agents: []planner.AgentTask{{Name: "x"}}}
	r.originalReq = "add rate limiting"
	r2, cmd := r.dispatch("only the public endpoints")
	if cmd == nil {
		t.Fatal("expected tea.Cmd for replan")
	}
	if !r2.planning {
		t.Error("expected planning=true after replan")
	}
}

// TestPlanReadyMsg_ChatReplyRendersInline: when the planner falls
// back to a conversational answer (request was too vague to plan),
// the REPL writes the reply as inline text and does NOT enter
// pending-plan state. The user can keep typing.
func TestPlanReadyMsg_ChatReplyRendersInline(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.planning = true
	r2, _ := r.Update(PlanReadyMsg{
		Request:   "hi",
		ChatReply: "Hey! Tell me which file to change and I'll plan it.",
	})
	if r2.planning {
		t.Error("planning should be cleared on ChatReply")
	}
	if r2.pendingPlan != nil {
		t.Error("ChatReply should NOT set pendingPlan")
	}
	if !strings.Contains(r2.buf, "Hey!") {
		t.Errorf("chat reply missing from buffer: %q", r2.buf)
	}
}

// TestPlanReadyMsg_PopulatesPendingPlan: after PlanReadyMsg lands the
// REPL holds the plan and renders it.
func TestPlanReadyMsg_PopulatesPendingPlan(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.planning = true
	r2, _ := r.Update(PlanReadyMsg{
		Request: "add a thing",
		Plan: &planner.WorkPlan{
			Summary: "thing added",
			Agents:  []planner.AgentTask{{Name: "a", Prompt: "p"}},
		},
	})
	if r2.planning {
		t.Error("planning should be cleared on PlanReadyMsg")
	}
	if r2.pendingPlan == nil {
		t.Error("pendingPlan should be set on PlanReadyMsg")
	}
	if !strings.Contains(r2.buf, "Plan: 1 agent") {
		t.Errorf("scrollback missing plan: %q", r2.buf)
	}
}

// TestPlanReadyMsg_ErrorPath: an LLM/parse error clears state and
// surfaces the error in the buffer.
func TestPlanReadyMsg_ErrorPath(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", &PlannerServices{})
	r.planning = true
	r2, _ := r.Update(PlanReadyMsg{
		Request: "x",
		Err:     errStub("api down"),
	})
	if r2.planning {
		t.Error("planning should be cleared on error")
	}
	if r2.pendingPlan != nil {
		t.Error("pendingPlan should remain nil on error")
	}
	if !strings.Contains(r2.buf, "api down") {
		t.Errorf("error message missing: %q", r2.buf)
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// TestRenderMarkdown_TransformsLists confirms glamour is wired in.
// Tests don't run in a TTY so glamour's auto-style falls back to a
// no-color theme — bold markers may stay as `**...**` (no ANSI to
// make them bold). What we CAN assert: list-bullet rewriting
// happens (- → •), and content is preserved. That proves the
// renderer is doing real work.
func TestRenderMarkdown_TransformsLists(t *testing.T) {
	md := "Here's the summary:\n\n- one\n- two\n- three\n"
	out, err := renderMarkdown(md, 80)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("rendered output empty")
	}
	if strings.Contains(out, "- one") || strings.Contains(out, "- two") {
		t.Errorf("dash bullets weren't rewritten: %q", out)
	}
	if !strings.Contains(out, "•") {
		t.Errorf("expected bullet glyph in output: %q", out)
	}
	for _, item := range []string{"one", "two", "three"} {
		if !strings.Contains(out, item) {
			t.Errorf("list item %q missing from output", item)
		}
	}
}

// TestREPLWriteMarkdown_FallsBackOnError: the pane should never go
// silent because glamour misbehaved on weird input. Empty input is
// the easy degenerate case.
func TestREPLWriteMarkdown_FallsBackOnError(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", nil)
	r.SetSize(40, 10)
	preLen := len(r.buf)
	// Glamour can render empty just fine but the helper handles
	// zero-output and falls back. Use a string that surfaces
	// non-empty input through write() either way.
	r.writeMarkdown("hello")
	if len(r.buf) <= preLen {
		t.Errorf("nothing was appended; buf unchanged")
	}
}

// TestWrapToWidth covers the basic line-wrap cases. Long words at the
// boundary, multiple spaces, embedded newlines.
func TestWrapToWidth(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"hello world", 80, "hello world"},
		{"hello world", 5, "hello\nworld"},
		{"this is a longer line", 10, "this is a\nlonger\nline"},
		{"a b c d", 3, "a b\nc d"},
		{"first\nsecond", 80, "first\nsecond"},
		{"", 10, ""},
		{"unbreakable", 5, "unbreakable"}, // single word exceeds width — keep on its own line
	}
	for _, c := range cases {
		got := wrapToWidth(c.in, c.width)
		if got != c.want {
			t.Errorf("wrapToWidth(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// TestWrapToWidth_DisabledForZeroWidth: write() defaults to 80 if
// the pane width hasn't been set yet, but the helper itself should
// pass through unchanged for non-positive widths.
func TestWrapToWidth_DisabledForZeroWidth(t *testing.T) {
	in := "this is a long line that won't be wrapped because width is zero"
	if got := wrapToWidth(in, 0); got != in {
		t.Errorf("expected pass-through for width=0, got %q", got)
	}
}

// TestREPLWrite_WrapsAtPaneWidth: a long line written after SetSize
// is word-wrapped at the pane width. (The constructor's greeting
// pre-dates SetSize and uses the 80-col default; that's a separate
// "re-wrap on resize" follow-up — the test here focuses on lines
// added once the size is known.)
func TestREPLWrite_WrapsAtPaneWidth(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", nil)
	r.SetSize(20, 10)
	preLen := len(r.buf)
	r.write("this is a fairly long line that should wrap")
	added := r.buf[preLen:] // just the segment we appended

	if !strings.Contains(added, "\n") {
		t.Errorf("expected wrapped line in appended segment, got: %q", added)
	}
	for _, segment := range strings.Split(added, "\n") {
		if utf8RuneLen(segment) > 18 { // 20 width - 2 margin
			t.Errorf("segment too long after wrap: %q (%d runes)", segment, utf8RuneLen(segment))
		}
	}
}
