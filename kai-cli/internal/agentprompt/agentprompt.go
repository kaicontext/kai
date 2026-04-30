// Package agentprompt builds the final prompt string handed to an
// agent process (Claude Code, Cursor, etc.) when the orchestrator
// launches it inside a spawned workspace.
//
// The contract: planner.AgentTask says WHAT the agent should do.
// agentprompt.Build says HOW to tell the agent. Splitting these
// concerns means we can iterate on prompt phrasing without touching
// the planner's LLM call, and vice versa.
//
// Pure function, no side effects, no I/O. Easy to unit-test against
// golden files.
package agentprompt

import (
	"fmt"
	"sort"
	"strings"

	"kai/internal/planner"
)

// Context is the per-repo information that's the same across all
// agents in a single plan. The orchestrator builds it once and passes
// it to Build for each AgentTask.
type Context struct {
	// RepoRoot is an absolute path; surfaced in the prompt so the
	// agent has an anchor when reading or writing files.
	RepoRoot string

	// Language is a short identifier ("go", "python", "ts") used to
	// hint the agent at its environment. Best-effort; safe to leave
	// empty if unknown.
	Language string

	// GraphContext is a pre-rendered string the orchestrator builds
	// from the semantic graph: a few lines per file in the agent's
	// allowed set listing callers and dependents at depth 1. Goes
	// in verbatim — agentprompt doesn't query the graph itself.
	GraphContext string

	// Protected globs from the safety gate config. Surfaced in
	// every prompt so the agent knows the gate's rules and doesn't
	// try to "fix" something it shouldn't.
	Protected []string
}

// Build composes the final prompt. Sections are clearly delimited so
// agents (which are LLMs and parse plain prose, not structured input)
// can navigate the prompt without confusion.
func Build(task planner.AgentTask, ctx Context) string {
	var b strings.Builder

	// Identity: tell the agent who it is and what it's doing. Short
	// and concrete is better than long and inspirational.
	fmt.Fprintf(&b, "You are agent %q.\n\n", task.Name)
	fmt.Fprintf(&b, "Task: %s\n\n", strings.TrimSpace(task.Prompt))

	if ctx.RepoRoot != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", ctx.RepoRoot)
	}
	if ctx.Language != "" {
		fmt.Fprintf(&b, "Primary language: %s\n", ctx.Language)
	}
	if ctx.RepoRoot != "" || ctx.Language != "" {
		b.WriteByte('\n')
	}

	// File boundaries: the planner's intent for what this agent
	// should and shouldn't touch. v1 has no sandbox enforcement;
	// the agent is expected to honor these via the prompt.
	if len(task.Files) > 0 {
		b.WriteString("Files you should focus on:\n")
		for _, p := range sortedCopy(task.Files) {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteByte('\n')
	}

	// DontTouch + Protected merged so the agent sees one forbidden
	// list rather than two slightly different ones. Dedup so a path
	// that's both DontTouch and Protected appears once.
	forbidden := mergeUnique(task.DontTouch, ctx.Protected)
	if len(forbidden) > 0 {
		b.WriteString("Files you must NOT modify:\n")
		for _, p := range forbidden {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		b.WriteString("\nIf changing one of these is genuinely necessary, stop and explain why instead of editing it.\n\n")
	}

	if s := strings.TrimSpace(ctx.GraphContext); s != "" {
		b.WriteString("Graph context for the files in scope:\n")
		b.WriteString(s)
		if !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	// Coordination + checkpointing hint. Agents that don't have the
	// kai MCP installed will skip the checkpoint instruction; that's
	// fine — the watcher catches file changes regardless.
	b.WriteString(`Coordination notes:
  - Other agents may be working in sibling workspaces; live sync keeps the graph current.
  - If the kai_checkpoint tool is available, call it whenever you finish a logical unit of work.
  - When your task is done, exit cleanly. The orchestrator will integrate your changes through the safety gate.
`)

	return b.String()
}

// mergeUnique returns the sorted union of two string slices.
func mergeUnique(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		if s != "" {
			seen[s] = struct{}{}
		}
	}
	for _, s := range b {
		if s != "" {
			seen[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sortedCopy returns a sorted copy so prompt output is deterministic.
// Determinism matters for golden-file tests and for cache-key stability
// if we ever start caching prompts.
func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
