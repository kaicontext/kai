// Package planner turns a natural-language request plus the semantic
// graph into a structured WorkPlan. Phase 3's REPL routes unrecognized
// input through Plan(); the orchestrator consumes the resulting plan.
//
// This file holds the public types so other packages (agentprompt,
// orchestrator) can depend on them without depending on the LLM call
// machinery in plan.go.
package planner

// WorkPlan is the structured output of a single planner call. The
// orchestrator iterates Agents in parallel; RiskNotes is rendered to
// the user before they confirm "go".
type WorkPlan struct {
	// Summary is one line describing what the whole plan accomplishes.
	Summary string `json:"summary"`

	// Agents is the work split. Empty means the request was too vague
	// to plan — Plan() returns an error in that case so callers don't
	// silently execute nothing.
	Agents []AgentTask `json:"agents"`

	// RiskNotes are advisory bullets the LLM flagged (e.g. "router.go
	// is called by 4 services"). Surfaced verbatim in the REPL.
	RiskNotes []string `json:"risk_notes,omitempty"`
}

// AgentTask is one agent's scoped assignment. Files / DontTouch are
// not enforced by a sandbox in v1 — they go into the agent's prompt
// and the agent is expected to honor them. Phase 3.x adds post-hoc
// verification; v1 trusts the prompt.
type AgentTask struct {
	// Name is a short identifier ("backend-api", "tests"). Used as
	// the agent's --agent label and in the spawn directory name.
	Name string `json:"name"`

	// Prompt is the human-readable description of the task, written
	// by the planner LLM. agentprompt.Build wraps this with file
	// boundaries and graph context to produce the final prompt.
	Prompt string `json:"prompt"`

	// Files lists the paths this agent should focus on (planner's
	// best guess). May be empty for agents whose work touches new
	// files exclusively.
	Files []string `json:"files,omitempty"`

	// DontTouch lists paths this agent must avoid. Typically a subset
	// of the gate's protected globs plus other agents' Files lists.
	DontTouch []string `json:"dont_touch,omitempty"`
}
