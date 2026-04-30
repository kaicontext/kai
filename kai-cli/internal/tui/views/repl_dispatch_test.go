package views

import (
	"strings"
	"testing"

	"kai/internal/planner"
)

// TestDispatch_ShellOutWithoutPlanner: nil services means every input
// gets shelled out, regardless of whether it looks like a known
// command. Preserves the v1 behavior for users who don't set
// ANTHROPIC_API_KEY.
func TestDispatch_ShellOutWithoutPlanner(t *testing.T) {
	r := NewREPL("/usr/bin/true", "/tmp", nil)
	_, cmd := r.dispatch("anything goes here")
	if cmd == nil {
		t.Fatal("expected a tea.Cmd for shellout, got nil")
	}
	// We can't easily inspect the Cmd; reaching here without panic
	// confirms the dispatch took the shellout branch.
}

// TestDispatch_KnownCommandShellsOut: when the planner is configured,
// known commands still shell out (preserving every existing CLI flow).
func TestDispatch_KnownCommandShellsOut(t *testing.T) {
	knownCalls := 0
	services := &PlannerServices{
		IsKnownCommand: func(name string) bool {
			knownCalls++
			return name == "gate"
		},
	}
	r := NewREPL("/usr/bin/true", "/tmp", services)
	_, cmd := r.dispatch("gate list")
	if cmd == nil {
		t.Fatal("expected tea.Cmd")
	}
	if knownCalls == 0 {
		t.Error("IsKnownCommand was never consulted")
	}
	if r.planning {
		t.Error("known command should not enter planning state")
	}
}

// TestDispatch_UnknownCommandStartsPlanning: a string that isn't a
// known cobra subcommand and isn't a pending-plan response gets
// routed to the planner.
func TestDispatch_UnknownCommandStartsPlanning(t *testing.T) {
	services := &PlannerServices{
		IsKnownCommand: func(name string) bool { return false },
		// LLM is nil but the test only inspects state transition,
		// not the eventual PlanReadyMsg.
	}
	r := NewREPL("/usr/bin/true", "/tmp", services)
	r2, cmd := r.dispatch("add rate limiting to the API")
	if cmd == nil {
		t.Fatal("expected tea.Cmd")
	}
	if !r2.planning {
		t.Error("expected planning=true after unknown-command dispatch")
	}
}

// TestDispatch_PendingPlanGo: with a pending plan, "go" triggers
// orchestrator.Execute (we just verify the state transition; the
// actual run is covered by orchestrator tests + e2e).
func TestDispatch_PendingPlanGo(t *testing.T) {
	services := &PlannerServices{
		IsKnownCommand: func(name string) bool { return false },
	}
	r := NewREPL("/usr/bin/true", "/tmp", services)
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

// TestDispatch_PendingPlanCancel clears the pending plan and never
// runs anything.
func TestDispatch_PendingPlanCancel(t *testing.T) {
	services := &PlannerServices{}
	r := NewREPL("/usr/bin/true", "/tmp", services)
	r.pendingPlan = &planner.WorkPlan{Agents: []planner.AgentTask{{Name: "x"}}}
	r.originalReq = "earlier request"

	r2, cmd := r.dispatch("cancel")
	if cmd != nil {
		t.Errorf("cancel should not produce a tea.Cmd")
	}
	if r2.pendingPlan != nil || r2.originalReq != "" {
		t.Error("pendingPlan/originalReq should be cleared after cancel")
	}
}

// TestDispatch_PendingPlanFeedbackReplans: anything that's not "go"
// or "cancel" while a plan is pending becomes feedback.
func TestDispatch_PendingPlanFeedbackReplans(t *testing.T) {
	services := &PlannerServices{}
	r := NewREPL("/usr/bin/true", "/tmp", services)
	r.pendingPlan = &planner.WorkPlan{Agents: []planner.AgentTask{{Name: "x"}}}
	r.originalReq = "add rate limiting"

	r2, cmd := r.dispatch("actually only the public endpoints")
	if cmd == nil {
		t.Fatal("expected tea.Cmd for replan")
	}
	if !r2.planning {
		t.Error("expected planning=true after replan")
	}
}

// TestFirstToken handles a few normalization cases: empty, with
// whitespace, mixed case, single token.
func TestFirstToken(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"   ":               "",
		"gate":              "gate",
		"  gate list  ":     "gate",
		"GATE list":         "gate",
		"integrate\t--ws x": "integrate",
	}
	for in, want := range cases {
		if got := firstToken(in); got != want {
			t.Errorf("firstToken(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestPlanReadyMsg_PopulatesPendingPlan: after PlanReadyMsg lands the
// REPL holds the plan and renders it in scrollback.
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
