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
