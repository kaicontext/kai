package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar is the one-line strip pinned to the bottom of the TUI
// that summarizes Gate + Sync state. Replaces the older 40%-tall
// top-row split: most users only need to know "anything held? any
// recent activity?" — the full panes are accessible via the
// /gate / /sync subcommands when they need detail.
type StatusBar struct {
	width int

	gateHeld     int
	gateErr      error
	lastSyncPath string
	lastSyncOp   string
	lastSyncWhen time.Time
	// agentsActive is the count of chat-fallback agent runs
	// currently in flight. Updated via "agent_start" / "agent_end"
	// events on the chat-activity channel.
	agentsActive int
}

// SetSize stores the pane width so the bar can pad/clip correctly.
// Height is always one row; ignored.
func (s *StatusBar) SetSize(w, _ int) { s.width = w }

// Update receives any message the parent broadcasts and snapshots
// the bits it needs from gate refresh + sync events. Decoupled from
// the underlying Gate/Sync types so the bar can render without
// holding pointers to them.
func (s StatusBar) Update(msg interface{}) StatusBar {
	switch m := msg.(type) {
	case gateRefreshedMsg:
		if m.err != nil {
			s.gateErr = m.err
		} else {
			s.gateErr = nil
			s.gateHeld = len(m.items)
		}
	case SyncEventMsg:
		s.lastSyncPath = m.Event.Path
		s.lastSyncOp = m.Event.Op
		s.lastSyncWhen = m.Event.When
	case SyncErrorMsg:
		// SyncErrorMsg surfaces a watcher startup failure. Forward
		// the underlying error message verbatim — users debugging
		// "why doesn't sync work" need the real reason, not a
		// generic "unavailable".
		if m.Err != nil {
			s.lastSyncPath = m.Err.Error()
		} else {
			s.lastSyncPath = "watcher unavailable"
		}
		s.lastSyncOp = "error"
		s.lastSyncWhen = time.Now()
	case ChatActivityMsg:
		switch m.Event.Kind {
		case "agent_start":
			s.agentsActive++
		case "agent_end":
			if s.agentsActive > 0 {
				s.agentsActive--
			}
		case "gate":
			// Reflect held/blocked verdicts in the bar's gate
			// counter so the user sees kai's classifier working
			// in real time. The next gate-list refresh will
			// reconcile against persisted state.
			if m.Event.GateVerdict == "review" || m.Event.GateVerdict == "block" {
				s.gateHeld++
			}
		}
	}
	return s
}

// View renders the status bar. Format:
//
//	Gate: 0 held │ Sync: idle
//	Gate: 2 held │ Sync: index.js modified 4s ago
func (s StatusBar) View() string {
	gateText := fmt.Sprintf("Gate: %d held", s.gateHeld)
	if s.gateErr != nil {
		gateText = "Gate: error"
	}

	// Spinner-ish prefix when at least one agent is running so the
	// counter reads as live work, not a stale number. ● for active,
	// ○ for idle.
	agentGlyph := "○"
	if s.agentsActive > 0 {
		agentGlyph = "●"
	}
	agentText := fmt.Sprintf("%s Agents: %d", agentGlyph, s.agentsActive)

	syncText := "Sync: idle"
	if s.lastSyncPath != "" {
		ago := humanAgo(time.Since(s.lastSyncWhen))
		syncText = fmt.Sprintf("Sync: %s %s %s", s.lastSyncPath, s.lastSyncOp, ago)
	}

	line := agentText + "  │  " + gateText + "  │  " + syncText
	// Truncate / pad to width so the bar's background extends
	// edge-to-edge without wrapping.
	if s.width > 0 {
		line = clipOrPad(line, s.width)
	}
	return statusBarStyle.Render(line)
}

func clipOrPad(s string, width int) string {
	n := runeCount(s)
	if n > width {
		return truncateRunes(s, width)
	}
	return s + strings.Repeat(" ", width-n)
}

// humanAgo renders a duration as "now", "5s ago", "2m ago", etc.
// Bar is one line — readability beats precision.
func humanAgo(d time.Duration) string {
	switch {
	case d < 2*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

var statusBarStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("8")).
	Background(lipgloss.Color("0"))
