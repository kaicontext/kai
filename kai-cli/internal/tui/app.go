// Package tui is the Bubble Tea front-end for kai. When the kai binary
// is invoked with no subcommand, cmd/kai/tui.go calls Run, which boots
// a three-pane interface: gate (held integrations), sync (live agent
// activity), and REPL (input/output).
//
// All rendering happens here; engine work is delegated in-process to
// kai-cli/internal/* packages. Nothing in this package owns business
// logic beyond layout and event routing.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kai/internal/graph"
	"kai/internal/tui/views"
	"kai/internal/watcher"
)

// Options configures a TUI session. The TUI reads from a live graph
// DB and a kai data directory; the caller (cmd/kai) opens both before
// handing them in so the TUI doesn't duplicate path resolution logic.
type Options struct {
	DB      *graph.DB
	KaiDir  string
	WorkDir string
	// Binary is the path to the kai executable used by the REPL when
	// shelling out subcommands. Defaults to os.Args[0] if empty.
	Binary string
	// Planner enables natural-language input in the REPL. Nil → REPL
	// shells out everything (no planner path).
	Planner *views.PlannerServices
}

// Run starts the TUI event loop. Blocks until the user quits.
func Run(ctx context.Context, opts Options) error {
	if opts.DB == nil {
		return fmt.Errorf("tui.Run: DB is required")
	}
	if opts.Binary == "" {
		opts.Binary = os.Args[0]
	}

	// Buffered so the watcher's callback (which fires from its event
	// loop goroutine) never blocks waiting for the pump to drain.
	syncCh := make(chan views.SyncEvent, 256)

	// Chat-activity channel: the chat-fallback agent's tool/file
	// hooks push tool dispatches and file mutations through here so
	// REPL renders them inline ("→ write package.json"). Sized large
	// enough that a chatty turn doesn't drop events; non-blocking
	// sends in the hook handle overflow gracefully.
	chatCh := make(chan views.ChatActivityEvent, 64)
	if opts.Planner != nil {
		opts.Planner.ChatActivityCh = chatCh
	}

	w, watcherErr := startWatcher(opts, syncCh)
	if w != nil {
		defer w.Stop()
	}

	// Wire the orchestrator's per-spawn agent activity into the same
	// sync channel the main-repo watcher uses. Tagged with the spawn
	// name so the user can see which agent did what; non-blocking
	// send drops on backpressure (better to lose a render than stall
	// the agent's session).
	if opts.Planner != nil && opts.Planner.OrchestratorCfg.OnActivity == nil {
		opts.Planner.OrchestratorCfg.OnActivity = func(spawnName, relPath, op string) {
			select {
			case syncCh <- views.SyncEvent{
				Path: spawnName + ": " + relPath,
				Op:   op,
				When: time.Now(),
			}:
			default:
			}
		}
	}

	m := initialModel(opts, syncCh, chatCh, watcherErr)
	// WithMouseCellMotion enables wheel events; the REPL routes
	// MouseMsg to its viewport so scrollback works regardless of
	// which pane the cursor is over (Claude Code-style "whole page
	// scrolls"). WithAltScreen keeps the alternate screen so we
	// don't dirty the user's terminal scrollback.
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	_, err := p.Run()
	return err
}

// startWatcher boots a watcher rooted at opts.WorkDir and wires its
// OnUpdate callback to the sync channel. Returns the watcher (so the
// caller can Stop it) plus any startup error. A failed startup is
// non-fatal — the sync pane shows "watcher unavailable" and the rest
// of the TUI works normally.
func startWatcher(opts Options, ch chan<- views.SyncEvent) (*watcher.Watcher, error) {
	w, err := watcher.New(opts.WorkDir, opts.DB)
	if err != nil {
		return nil, err
	}
	w.OnUpdate = func(path, op string) {
		// Non-blocking send: if the channel is full, drop. Better to
		// lose a render than to stall the watcher loop.
		select {
		case ch <- views.SyncEvent{Path: path, Op: op, When: time.Now()}:
		default:
		}
	}
	if err := w.Start(); err != nil {
		return nil, err
	}
	return w, nil
}

// model is the Bubble Tea root. Owns REPL, gate, and sync sub-views,
// plus a channel for live watcher events. The polished three-pane
// layout lands in task 13.
type model struct {
	opts   Options
	width  int
	height int

	repl    views.REPL
	gate    views.Gate
	sync    views.Sync
	status  views.StatusBar
	syncCh  <-chan views.SyncEvent
	chatCh  <-chan views.ChatActivityEvent
	focused focus
}

type focus int

const (
	focusREPL focus = iota
	focusGate
	focusSync
)

func initialModel(opts Options, syncCh <-chan views.SyncEvent, chatCh <-chan views.ChatActivityEvent, watcherErr error) model {
	s := views.NewSync(200)
	var status views.StatusBar
	if watcherErr != nil {
		s, _ = s.Update(views.SyncErrorMsg{Err: watcherErr})
		status = status.Update(views.SyncErrorMsg{Err: watcherErr})
	}
	return model{
		opts:    opts,
		repl:    views.NewREPL(opts.Binary, opts.WorkDir, opts.Planner),
		gate:    views.NewGate(opts.DB),
		sync:    s,
		status:  status,
		syncCh:  syncCh,
		chatCh:  chatCh,
		focused: focusREPL,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.repl.Focus(),
		m.gate.Refresh(),
	}
	if m.syncCh != nil {
		cmds = append(cmds, views.PumpEvents(m.syncCh))
	}
	if m.chatCh != nil {
		cmds = append(cmds, views.PumpChatActivity(m.chatCh))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.MouseMsg:
		// Whole-page scroll: wheel events route to the REPL
		// viewport regardless of which pane the cursor is over.
		// Anything else (clicks, motion) is dropped — we don't
		// support click-to-focus yet.
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			var c tea.Cmd
			m.repl, c = m.repl.Update(msg)
			return m, c
		}
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			// Two-step exit, like Claude Code / readline: first
			// Ctrl+C clears the input draft so a half-typed prompt
			// doesn't disappear forever to a misfire; the second
			// (with input already empty) actually quits.
			if strings.TrimSpace(m.repl.InputValue()) != "" {
				m.repl.ClearInput()
				return m, nil
			}
			return m, tea.Quit
		}
		switch msg.String() {
		case "ctrl+g":
			m.setFocus(focusGate)
			return m, nil
		case "ctrl+s":
			m.setFocus(focusSync)
			return m, nil
		case "ctrl+r":
			m.setFocus(focusREPL)
			return m, m.repl.Focus()
		case "esc":
			// Esc anywhere returns to REPL — keeps the keyboard
			// shortcut consistent regardless of which pane is active.
			if m.focused != focusREPL {
				m.setFocus(focusREPL)
				return m, m.repl.Focus()
			}
		}

	case views.SyncEventMsg:
		// Re-arm the pump immediately so the next event flows in.
		var cmds []tea.Cmd
		if m.syncCh != nil {
			cmds = append(cmds, views.PumpEvents(m.syncCh))
		}
		var c tea.Cmd
		m.sync, c = m.sync.Update(msg)
		cmds = append(cmds, c)
		// Status bar mirrors the most recent sync activity.
		m.status = m.status.Update(msg)
		return m, tea.Batch(cmds...)

	case views.ChatActivityMsg:
		// Status bar snapshots agent_start/agent_end here so the
		// "Agents: N" counter updates the moment a run kicks off,
		// not after the bar's next refresh.
		m.status = m.status.Update(msg)
		// Re-arm the chat-activity pump and let the REPL append the
		// inline event line.
		var cmds []tea.Cmd
		if m.chatCh != nil {
			cmds = append(cmds, views.PumpChatActivity(m.chatCh))
		}
		var c tea.Cmd
		m.repl, c = m.repl.Update(msg)
		cmds = append(cmds, c)
		return m, tea.Batch(cmds...)
	}

	// Async messages (CmdResultMsg, gateActionMsg, gateRefreshedMsg,
	// SyncErrorMsg) route to every view; key input gets filtered
	// inside each view by the focused flag.
	var cmds []tea.Cmd
	var c tea.Cmd
	m.repl, c = m.repl.Update(msg)
	cmds = append(cmds, c)
	m.gate, c = m.gate.Update(msg)
	cmds = append(cmds, c)
	m.sync, c = m.sync.Update(msg)
	cmds = append(cmds, c)
	// Status bar snapshots gate + sync state from the same broadcast.
	m.status = m.status.Update(msg)
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	// Single-pane layout: REPL takes the full window minus a
	// 1-line status strip pinned to the bottom that summarizes
	// gate + sync state. Detail views are accessible via /gate
	// and /sync subcommands. Mirrors the Claude Code "everything
	// scrolls in one column" UX the user asked for.
	return lipgloss.JoinVertical(lipgloss.Left,
		m.repl.View(),
		m.status.View(),
	)
}

// layout recomputes child sizes from the latest window dimensions.
// REPL gets the full width and all height minus one row reserved
// for the status bar at the bottom. Gate + Sync still receive
// SetSize so the /gate and /sync detail commands render correctly
// when the user shells out to them.
func (m *model) layout() {
	statusHeight := 1
	replHeight := m.height - statusHeight
	if replHeight < 4 {
		replHeight = 4
	}
	m.repl.SetSize(m.width, replHeight)
	m.status.SetSize(m.width, statusHeight)
	// Gate/Sync are background sinks now — give them sensible
	// defaults so any list/view their /command paths show is
	// shaped to the window.
	m.gate.SetSize(m.width, replHeight)
	m.sync.SetSize(m.width, replHeight)
}

// setFocus moves input focus between sub-views and updates each
// view's visual state.
func (m *model) setFocus(f focus) {
	m.focused = f
	m.gate.Blur()
	m.sync.Blur()
	m.repl.Blur()
	switch f {
	case focusREPL:
		// REPL focus is reasserted via the returned tea.Cmd by the caller.
	case focusGate:
		m.gate.Focus()
	case focusSync:
		m.sync.Focus()
	}
}
