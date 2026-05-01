package views

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ChatActivityEvent is a single agent observation streamed back to the
// REPL while a chat-fallback turn is in flight. Tool dispatches and
// file changes both flow through here so the user sees the agent
// working in real time instead of staring at a silent prompt.
type ChatActivityEvent struct {
	// Kind is one of:
	//   - "tool"        tool dispatch ("→ bash: ls"); Summary set
	//   - "file"        minimal file mutation note ("created package.json")
	//   - "diff"        per-edit unified diff; Diff/Path/Op/Added/Removed set
	//   - "tokens"      cumulative usage after a model turn; TokensIn/Out set
	//   - "agent_start" a new chat-fallback agent run is in flight
	//   - "agent_end"   an agent run has finished (or errored)
	Kind      string
	Summary   string
	TokensIn  int
	TokensOut int

	// Diff-event fields. Populated only when Kind == "diff".
	Path    string
	Op      string // "created" | "modified"
	Diff    string // full unified diff
	Added   int
	Removed int

	// Gate-event fields. Populated only when Kind == "gate".
	GatePaths   []string // workspace-relative paths the verdict covers
	GateVerdict string   // "auto" | "review" | "block" | "error"
	GateRadius  int      // depth-1 callers + dependents count
	GateReasons []string // human-readable reasons (protected paths, threshold breach)

	When time.Time
}

// ChatActivityMsg wraps a ChatActivityEvent for delivery via tea.Cmd.
// REPL.Update appends it to the scrollback as a dim line.
type ChatActivityMsg struct {
	Event ChatActivityEvent
}

// PumpChatActivity reads one event from the chat-activity channel and
// emits it as a ChatActivityMsg. Mirrors PumpEvents for SyncEvents —
// the parent re-arms after each delivery so we never block shutdown.
func PumpChatActivity(ch <-chan ChatActivityEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return ChatActivityMsg{Event: ev}
	}
}
