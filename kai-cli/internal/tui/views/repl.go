// Package views holds the individual Bubble Tea sub-models that the
// root TUI app stitches into a single layout.
package views

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kai/internal/planner"
)

// REPL is the input/output pane. It accepts free-form text, treats
// the first word as a kai subcommand, and shells out to the running
// binary to execute it. Output streams into the viewport.
//
// Shell-out (vs. invoking the cobra command tree in-process) is the
// simplest honest path: it keeps the REPL's notion of "running a
// command" identical to what a user would get at their normal shell,
// and avoids tangling Bubble Tea's stdout capture with cobra's
// internal output writers. Switching to in-process dispatch later is
// straightforward if shell-out becomes a bottleneck.
type REPL struct {
	input   textinput.Model
	output  viewport.Model
	// buf is a shadow of viewport content; viewport has no read-back.
	// Plain string (not strings.Builder) because Bubble Tea uses value
	// receivers and copies the model on every Update — copying a
	// non-zero strings.Builder panics at runtime.
	buf     string
	history []string
	histIdx int // -1 = not browsing history
	width   int
	height  int
	binary  string
	workDir string

	// Planner state. nil services → REPL operates in shell-out-only
	// mode and unrecognized commands fail through to the kai binary
	// (which prints its usual "unknown command" message).
	services      *PlannerServices
	pendingPlan   *planner.WorkPlan
	originalReq   string // the request that produced pendingPlan; carries through Replan
	planning      bool   // true while an LLM call is in flight
	executing     bool   // true while orchestrator is running
}

// NewREPL builds a fresh REPL view. binary is the path to the kai
// executable to dispatch input to; workDir is the cwd for child
// commands. services is optional — when non-nil it enables natural-
// language requests via the planner; nil means shell-out-only.
func NewREPL(binary, workDir string, services *PlannerServices) REPL {
	in := textinput.New()
	in.Placeholder = "describe a change, or /command (e.g. /gate list, /push)"
	in.Prompt = "› "
	in.Focus()
	in.CharLimit = 4096

	r := REPL{
		input:    in,
		output:   viewport.New(0, 0),
		histIdx:  -1,
		binary:   binary,
		workDir:  workDir,
		services: services,
	}
	if services != nil {
		r.write(styleDim.Render("kai TUI — describe a change in English, or /command for kai subcommands. ↑/↓ history, Esc to switch panes."))
	} else {
		r.write(replGreeting())
	}
	return r
}

// SetSize lays out the view within the given outer dimensions. The
// input takes one line; the rest goes to the viewport.
func (r *REPL) SetSize(width, height int) {
	r.width, r.height = width, height
	r.input.Width = width - 2
	if height > 1 {
		r.output.Width = width
		r.output.Height = height - 1
		r.output.GotoBottom()
	}
}

// Focus gives focus to the input. Returns the underlying tea.Cmd so
// the parent can chain it into Init/Update.
func (r *REPL) Focus() tea.Cmd { return r.input.Focus() }

// Blur removes focus from the input.
func (r *REPL) Blur() { r.input.Blur() }

// Update handles key input and the async CmdResultMsg that arrives
// when a child command finishes.
func (r REPL) Update(msg tea.Msg) (REPL, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			line := strings.TrimSpace(r.input.Value())
			if line == "" {
				return r, nil
			}
			// Refuse new input while an LLM call or orchestrator run
			// is in flight — the result will land in the viewport and
			// the user can resume.
			if r.planning || r.executing {
				r.write(styleDim.Render("(busy — wait for the current step to finish)"))
				return r, nil
			}
			r.history = append(r.history, line)
			r.histIdx = -1
			r.input.Reset()
			r.write(replPrompt() + line)
			return r.dispatch(line)

		case "up":
			if len(r.history) > 0 {
				if r.histIdx < 0 {
					r.histIdx = len(r.history) - 1
				} else if r.histIdx > 0 {
					r.histIdx--
				}
				r.input.SetValue(r.history[r.histIdx])
				r.input.CursorEnd()
				return r, nil
			}
		case "down":
			if r.histIdx >= 0 {
				r.histIdx++
				if r.histIdx >= len(r.history) {
					r.histIdx = -1
					r.input.SetValue("")
				} else {
					r.input.SetValue(r.history[r.histIdx])
					r.input.CursorEnd()
				}
				return r, nil
			}
		}

	case CmdResultMsg:
		r.write(formatCmdResult(msg))
		return r, nil

	case PlanReadyMsg:
		r.planning = false
		switch {
		case msg.Err != nil:
			// stripPlannerPrefix avoids "planner: planner: ..." since
			// errors from the planner package already carry that prefix.
			r.write(styleError.Render("planner: " + stripPlannerPrefix(msg.Err.Error())))
			r.pendingPlan = nil
		case msg.Plan == nil:
			r.write(styleError.Render("planner: empty result"))
			r.pendingPlan = nil
		default:
			r.pendingPlan = msg.Plan
			r.originalReq = msg.Request
			r.write(formatPlan(msg.Plan))
		}
		return r, nil

	case ExecuteDoneMsg:
		r.executing = false
		r.write(formatExecuteResult(msg.Result, msg.Err))
		r.pendingPlan = nil
		r.originalReq = ""
		return r, nil
	}

	var inCmd, vpCmd tea.Cmd
	r.input, inCmd = r.input.Update(msg)
	r.output, vpCmd = r.output.Update(msg)
	return r, tea.Batch(inCmd, vpCmd)
}

// View renders the REPL. Output viewport on top, input on the bottom.
func (r REPL) View() string {
	if r.height <= 1 {
		return r.input.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		r.output.View(),
		r.input.View(),
	)
}

// dispatch routes user input. The discipline is simple and explicit:
//
//   - Lines that start with "/" are kai subcommands (the leading "/"
//     is stripped, then shelled out). Examples:
//        /gate list           → kai gate list
//        /integrate --ws feat → kai integrate --ws feat
//   - Everything else is a natural-language request for the planner
//     (when configured) — a request like "Update index.js to have
//     multiple greetings" goes straight to the LLM, no ambiguity.
//   - With no planner services available, non-slash lines fall through
//     to shellout so the bare kai binary surfaces its own "unknown
//     command" error rather than swallowing the input silently.
//   - The pending-plan state machine takes priority over routing —
//     while a plan is awaiting confirmation, any input is "go",
//     "cancel", or feedback to replan.
func (r REPL) dispatch(line string) (REPL, tea.Cmd) {
	// Pending-plan state machine: once a plan is up for confirmation,
	// any input is interpreted in that context.
	if r.pendingPlan != nil {
		lower := strings.ToLower(strings.TrimSpace(line))
		switch lower {
		case "go", "yes", "y":
			plan := r.pendingPlan
			r.executing = true
			r.write(styleDim.Render("running plan…"))
			return r, runExecute(r.services, plan)
		case "cancel", "no", "n", "abort":
			r.pendingPlan = nil
			r.originalReq = ""
			r.write(styleDim.Render("plan canceled"))
			return r, nil
		default:
			original := r.originalReq
			r.planning = true
			r.write(styleDim.Render("replanning…"))
			return r, runReplan(r.services, original, line)
		}
	}

	if strings.HasPrefix(line, "/") {
		// Strip the leading "/" before handing to cobra. We trim only
		// the single leading slash; arguments preserve their own
		// punctuation.
		return r, runShellCommand(r.binary, strings.TrimPrefix(line, "/"), r.workDir)
	}

	// No slash → planner if configured, else fall back to shellout
	// so the bare kai binary can render its own usage message.
	if r.services != nil {
		r.planning = true
		r.write(styleDim.Render("planning…"))
		return r, runPlan(r.services, line)
	}
	return r, runShellCommand(r.binary, line, r.workDir)
}

// write appends a line to both the shadow buffer and the viewport,
// then auto-scrolls so the newest content stays visible.
func (r *REPL) write(line string) {
	if r.buf == "" {
		r.buf = line
	} else {
		r.buf = r.buf + "\n" + line
	}
	r.output.SetContent(r.buf)
	r.output.GotoBottom()
}

// CmdResultMsg is the Bubble Tea message produced when a shell-out
// command completes.
type CmdResultMsg struct {
	Cmd    string
	Stdout string
	Stderr string
	Err    error
}

// runShellCommand invokes `kai <args>` in workDir and returns the
// result as a Bubble Tea message. Wrapped in tea.Cmd so the program
// loop drives it asynchronously and the UI stays responsive.
func runShellCommand(binary, line, workDir string) tea.Cmd {
	return func() tea.Msg {
		args := strings.Fields(line)
		// Bare `kai` no longer launches the TUI (that's `kai code`),
		// so no recursion guard is needed — a child `kai` invocation
		// just prints help unless the user explicitly typed `code`.
		c := exec.Command(binary, args...)
		c.Dir = workDir
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		return CmdResultMsg{
			Cmd:    line,
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
		}
	}
}

func formatCmdResult(m CmdResultMsg) string {
	var b strings.Builder
	if s := strings.TrimRight(m.Stdout, "\n"); s != "" {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if s := strings.TrimRight(m.Stderr, "\n"); s != "" {
		b.WriteString(replError(s))
		b.WriteByte('\n')
	}
	if m.Err != nil && m.Stderr == "" {
		b.WriteString(replError(m.Err.Error()))
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		b.WriteString(replDim("(no output)"))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

var (
	stylePrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func replGreeting() string {
	return styleDim.Render("kai TUI — /command for kai subcommands, ↑/↓ for history, Esc to switch panes")
}

func replPrompt() string         { return stylePrompt.Render("› ") }
func replError(s string) string  { return styleError.Render(s) }
func replDim(s string) string    { return styleDim.Render(s) }
