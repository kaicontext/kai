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

	m := initialModel(opts, syncCh, watcherErr)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
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
	syncCh  <-chan views.SyncEvent
	focused focus
}

type focus int

const (
	focusREPL focus = iota
	focusGate
	focusSync
)

func initialModel(opts Options, syncCh <-chan views.SyncEvent, watcherErr error) model {
	s := views.NewSync(200)
	if watcherErr != nil {
		s, _ = s.Update(views.SyncErrorMsg{Err: watcherErr})
	}
	return model{
		opts:    opts,
		repl:    views.NewREPL(opts.Binary, opts.WorkDir, opts.Planner),
		gate:    views.NewGate(opts.DB),
		sync:    s,
		syncCh:  syncCh,
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
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
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
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	// Three-pane layout:
	//   ┌─ gate (1/3 W) ──┬─ sync (2/3 W) ──┐
	//   │                 │                 │
	//   ├─ REPL (full W) ────────────────────┤
	//   └────────────────────────────────────┘
	top := lipgloss.JoinHorizontal(lipgloss.Top, m.gate.View(), m.sync.View())
	return lipgloss.JoinVertical(lipgloss.Left, top, m.repl.View())
}

// layout recomputes child sizes from the latest window dimensions.
// Top row is roughly 40% of the window; gate gets 1/3 of the width,
// sync gets the remaining 2/3. REPL gets whatever vertical space is
// left after the top row and the joining newline.
func (m *model) layout() {
	topHeight := m.height * 2 / 5
	if topHeight < 8 {
		topHeight = 8
	}
	if topHeight > m.height-4 {
		// Always leave room for at least the REPL prompt + a couple
		// of output lines, even on tiny windows.
		topHeight = m.height - 4
	}

	gateWidth := m.width / 3
	syncWidth := m.width - gateWidth
	if gateWidth < 24 {
		gateWidth = 24
	}
	if syncWidth < m.width-gateWidth {
		syncWidth = m.width - gateWidth
	}

	m.gate.SetSize(gateWidth, topHeight)
	m.sync.SetSize(syncWidth, topHeight)
	m.repl.SetSize(m.width, m.height-topHeight-1)
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
