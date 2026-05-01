// Package views holds the individual Bubble Tea sub-models that the
// root TUI app stitches into a single layout.
package views

import (
	"bytes"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
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
	input   textarea.Model
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

	// chatSessionID stickies the chat-fallback agent session across
	// turns within this TUI run so multi-turn chats remember prior
	// context. Empty until the first chat reply lands. Forgotten on
	// exit — chat state is intentionally not persisted across `kai
	// code` invocations.
	chatSessionID string

	// transientStart is the byte offset where the current "planning…"
	// or "running plan…" status line begins. -1 means no transient
	// is active. clearTransient truncates back to this offset so
	// stale status indicators don't pile up above each response.
	transientStart int

	// Token counter animation. tokenTarget is the "true" cumulative
	// count from the most recent OnTurnComplete; tokenShown is the
	// currently-rendered count (interpolated toward target via
	// tickTokenAnim). When shown == target, the tweener idles and
	// no further ticks are scheduled. Cleared on each new chat
	// turn so each reply animates from 0.
	tokenTargetIn  int
	tokenTargetOut int
	tokenShownIn   int
	tokenShownOut  int
	tokenAnimating bool
}

// NewREPL builds a fresh REPL view. binary is the path to the kai
// executable to dispatch input to; workDir is the cwd for child
// commands. services is optional — when non-nil it enables natural-
// language requests via the planner; nil means shell-out-only.
func NewREPL(binary, workDir string, services *PlannerServices) REPL {
	in := textarea.New()
	in.Placeholder = "describe a change, or /command (e.g. /gate list, /push)"
	in.Prompt = "› "
	in.Focus()
	in.CharLimit = 8192
	// One row by default, growing up to 8 as the user types more.
	// Layout() recomputes height each Update so the bordered box
	// expands and the status bar stays glued to the bottom.
	in.SetHeight(1)
	in.ShowLineNumbers = false
	// Enter SUBMITS the input (handled in REPL.Update); newline is
	// alt+enter / ctrl+j. Without this rebind, every Enter would
	// just insert a newline and the user could never send.
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	// macOS muscle memory: option+delete erases the word to the
	// left. textarea's defaults match textinput's; reaffirm here so
	// terminals that send alt+delete instead of alt+backspace also
	// delete backward.
	in.KeyMap.DeleteWordBackward = key.NewBinding(
		key.WithKeys("alt+backspace", "alt+delete", "ctrl+w"),
	)
	in.KeyMap.DeleteWordForward = key.NewBinding(key.WithKeys("alt+d"))
	// Static cursor — blinking causes the layout to re-render every
	// half-second, which made the status bar / top of the viewport
	// appear to flash on slower terminals.
	in.Cursor.SetMode(cursor.CursorStatic)

	r := REPL{
		input:          in,
		output:         viewport.New(0, 0),
		histIdx:        -1,
		binary:         binary,
		workDir:        workDir,
		services:       services,
		transientStart: -1,
	}
	// Startup banner: mascot + identity on launch. writeRaw because
	// the banner's lipgloss styles embed ANSI escapes that would
	// otherwise be mangled by the word wrapper.
	r.writeRaw(renderBanner(services))
	return r
}

// SetSize lays out the view within the given outer dimensions. The
// input area takes inputBoxHeight lines (textarea content + 2 border
// rows); the rest goes to the viewport. textarea.Height auto-grows
// up to maxInputRows as the user types more.
func (r *REPL) SetSize(width, height int) {
	r.width, r.height = width, height
	r.input.SetWidth(width - 2)
	boxH := r.inputBoxHeight()
	if height > boxH+1 {
		r.output.Width = width
		r.output.Height = height - boxH
		r.output.GotoBottom()
	}
}

// maxInputRows caps how tall the input box grows. Beyond this the
// textarea scrolls internally — protects the viewport from being
// shrunk to nothing when a user pastes a 40-line prompt.
const maxInputRows = 8

// inputBoxHeight returns the total rendered height of the input
// area: the textarea's current line count + 2 for the border. Used
// to decide how much vertical room the viewport gets so the layout
// re-flows as the user types multi-line prompts.
func (r *REPL) inputBoxHeight() int {
	rows := r.input.LineCount()
	if rows < 1 {
		rows = 1
	}
	if rows > maxInputRows {
		rows = maxInputRows
	}
	if r.input.Height() != rows {
		r.input.SetHeight(rows)
	}
	return rows + 2 // +2 for top + bottom border lines
}

// InputValue returns the current draft text. Used by the parent
// model's Ctrl+C handler to decide between "clear draft" and "quit"
// without poking at the underlying textarea directly.
func (r REPL) InputValue() string { return r.input.Value() }

// ClearInput drops whatever the user has typed and resets history
// browsing. The two-step Ctrl+C calls this on the first press so a
// misfire doesn't kill the TUI with a half-written prompt.
func (r *REPL) ClearInput() {
	r.input.Reset()
	r.histIdx = -1
	if r.width > 0 && r.height > 0 {
		r.SetSize(r.width, r.height)
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
	case tea.MouseMsg:
		// Wheel events come straight to the viewport so the user
		// can scroll the scrollback from anywhere on screen.
		var c tea.Cmd
		r.output, c = r.output.Update(msg)
		return r, c
	case tea.KeyMsg:
		switch msg.String() {
		case "pgup", "pgdown", "shift+up", "shift+down", "ctrl+u", "ctrl+d":
			// Page keys route to the viewport instead of typing into
			// the input. Lets the user scroll back through prior
			// activity without losing focus on the prompt.
			var c tea.Cmd
			r.output, c = r.output.Update(msg)
			return r, c
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
			// Visual separator between turns so the previous reply
			// doesn't crowd the next prompt echo. Skip on the very
			// first turn (buf still has just the greeting).
			r.appendSeparator()
			r.write(replPrompt() + line)
			return r.dispatch(line)

		case "up":
			// History only fires when the cursor is on the first
			// row of the textarea — otherwise let textarea handle
			// up-arrow as line navigation within a multi-line draft.
			if r.input.Line() == 0 && len(r.history) > 0 {
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
			// Same: only navigate history from the bottom row.
			if r.input.Line() == r.input.LineCount()-1 && r.histIdx >= 0 {
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

	case ChatActivityMsg:
		// Inline activity from a chat-fallback agent run.
		switch msg.Event.Kind {
		case "tokens":
			// Update animation target. If we're not already running
			// the tweener, kick off a tick. Each tick steps the
			// shown value toward target and re-renders the live
			// line in-place by swapping the current transient.
			r.tokenTargetIn = msg.Event.TokensIn
			r.tokenTargetOut = msg.Event.TokensOut
			if !r.tokenAnimating {
				r.tokenAnimating = true
				return r, scheduleTokenTick()
			}
			return r, nil
		case "diff":
			// Per-edit diff. Render header + colorized hunk lines,
			// like Claude Code's inline "Update(path) — Added N
			// lines" block. Routed through writeRaw because the
			// lipgloss styles embed ANSI escapes and the word
			// wrapper would split mid-escape and trash the colors.
			r.clearTransient()
			r.writeRaw(formatDiffEvent(msg.Event, r.wrapWidth()))
		case "bash":
			// Live bash stdout/stderr line. Two-space indent + dim
			// styling so streamed output reads as subordinate to
			// the "→ bash: cmd" line above it without needing its
			// own header per line.
			r.clearTransient()
			r.writeRaw(styleDim.Render("  " + msg.Event.Summary))
		case "gate":
			r.clearTransient()
			r.writeRaw(formatGateVerdict(msg.Event))
			if r.tokenAnimating || r.tokenShownIn > 0 || r.tokenShownOut > 0 {
				r.renderTokenLine()
			}
			return r, nil
		default:
			// Render dim so it sits in the background of the
			// conversation. Cleared transient first so any pending
			// status line ("planning…") doesn't pin above.
			r.clearTransient()
			r.write(styleDim.Render(msg.Event.Summary))
			// Re-render the token line below the activity entry so
			// the live counter stays as the bottom-most line.
			if r.tokenAnimating || r.tokenShownIn > 0 || r.tokenShownOut > 0 {
				r.renderTokenLine()
			}
			return r, nil
		}

	case tokenTickMsg:
		// Drop stale ticks: the PlanReadyMsg handler resets target to
		// 0 once the reply lands, so any in-flight tick scheduled
		// before the reply arrived would otherwise render a phantom
		// "0 in / 0 out" line below the real one.
		if r.tokenTargetIn == 0 && r.tokenTargetOut == 0 {
			r.tokenAnimating = false
			return r, nil
		}
		r.tokenShownIn = stepToward(r.tokenShownIn, r.tokenTargetIn)
		r.tokenShownOut = stepToward(r.tokenShownOut, r.tokenTargetOut)
		r.renderTokenLine()
		// Schedule another frame if there's still distance to cover.
		// When shown matches target, idle — the next OnTurnComplete
		// re-arms us by setting tokenAnimating in the tokens case.
		if r.tokenShownIn != r.tokenTargetIn || r.tokenShownOut != r.tokenTargetOut {
			return r, scheduleTokenTick()
		}
		r.tokenAnimating = false
		return r, nil

	case PlanReadyMsg:
		r.planning = false
		r.clearTransient()
		switch {
		case msg.Err != nil:
			// stripPlannerPrefix avoids "planner: planner: ..." since
			// errors from the planner package already carry that prefix.
			r.write(styleError.Render("planner: " + stripPlannerPrefix(msg.Err.Error())))
			r.pendingPlan = nil
		case msg.ChatReply != "":
			// Conversational fallback: the request wasn't a planable
			// change, so render the model's chat reply inline. The
			// reply is markdown (lists, bold, code spans) — feed it
			// through glamour so it renders in the terminal style.
			if msg.ChatSessionID != "" {
				r.chatSessionID = msg.ChatSessionID
			}
			// Drop the live counter line before printing the reply so
			// it doesn't sit awkwardly above the assistant's text.
			r.clearTransient()
			r.writeMarkdown(msg.ChatReply)
			if msg.ChatTokensIn > 0 || msg.ChatTokensOut > 0 {
				r.write(styleDim.Render(formatTokens(msg.ChatTokensIn, msg.ChatTokensOut)))
			}
			// Reset counters so the next chat turn animates from 0.
			r.tokenShownIn, r.tokenShownOut = 0, 0
			r.tokenTargetIn, r.tokenTargetOut = 0, 0
			r.tokenAnimating = false
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
		r.clearTransient()
		r.write(formatExecuteResult(msg.Result, msg.Err))
		r.pendingPlan = nil
		r.originalReq = ""
		return r, nil
	}

	var inCmd, vpCmd tea.Cmd
	priorLines := r.input.LineCount()
	r.input, inCmd = r.input.Update(msg)
	r.output, vpCmd = r.output.Update(msg)
	// If the textarea grew or shrank a line (newline added,
	// backspace collapsed two), re-flow the layout so the viewport
	// gives up / takes back the matching height.
	if r.input.LineCount() != priorLines && r.width > 0 && r.height > 0 {
		r.SetSize(r.width, r.height)
	}
	return r, tea.Batch(inCmd, vpCmd)
}

// View renders the REPL. Output viewport on top, input on the
// bottom wrapped in a thin top+bottom rule (mimics Claude Code's
// chat input — clear visual separator between scrollback and the
// active prompt).
func (r REPL) View() string {
	if r.height <= 1 {
		return r.input.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		r.output.View(),
		r.inputBoxStyle().Render(r.input.View()),
	)
}

// inputBoxStyle returns the lipgloss style applied to the input —
// horizontal rules above and below, dim color so it sits in the
// background and doesn't compete with text. Constructed per-call so
// it picks up the latest pane width on resize.
func (r REPL) inputBoxStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, true, false).
		BorderForeground(lipgloss.Color("8")).
		Width(r.width)
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
			r.writeTransient(styleDim.Render("running plan…"))
			return r, runExecute(r.services, plan)
		case "cancel", "no", "n", "abort":
			r.pendingPlan = nil
			r.originalReq = ""
			r.write(styleDim.Render("plan canceled"))
			return r, nil
		default:
			original := r.originalReq
			r.planning = true
			r.writeTransient(styleDim.Render("replanning…"))
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
		r.writeTransient(styleDim.Render("planning…"))
		return r, runPlan(r.services, line, r.chatSessionID)
	}
	return r, runShellCommand(r.binary, line, r.workDir)
}

// write appends a line to both the shadow buffer and the viewport,
// then auto-scrolls so the newest content stays visible. Long lines
// are word-wrapped to the pane width so chat replies and tool output
// don't trail off the right edge.
func (r *REPL) write(line string) {
	r.append(wrapToWidth(line, r.wrapWidth()))
}

// writeRaw appends pre-formatted text without word-wrapping. Used for
// content that already manages its own line breaks AND carries ANSI
// escape codes — the wrapper's strings.Fields-based split tokenizes
// inside escape sequences and silently destroys colors. Diff blocks
// are the canonical caller; their lines are typically short enough
// that the viewport's own wrap-on-overflow is fine.
func (r *REPL) writeRaw(text string) {
	r.append(text)
}

// writeMarkdown renders the input as markdown via glamour, then
// appends the result. Used for chat replies (which the model
// produces as markdown — bold, lists, fenced code, etc.). Falls
// back to plain wrap if glamour fails for any reason.
func (r *REPL) writeMarkdown(md string) {
	width := r.wrapWidth()
	rendered, err := renderMarkdown(md, width)
	if err != nil || strings.TrimSpace(rendered) == "" {
		r.write(md)
		return
	}
	// Glamour wraps internally; don't double-wrap. Trim trailing
	// blank lines glamour likes to add so successive prints don't
	// stack visual whitespace.
	r.append(strings.TrimRight(rendered, "\n"))
}

// append is the shared "add to buf + refresh viewport" tail used by
// write and writeMarkdown.
func (r *REPL) append(text string) {
	if r.buf == "" {
		r.buf = text
	} else {
		r.buf = r.buf + "\n" + text
	}
	r.output.SetContent(r.buf)
	r.output.GotoBottom()
}

// tokenTickMsg is the self-scheduled tween tick for the live token
// counter. Each tick interpolates tokenShown toward tokenTarget and,
// if there's still distance to cover, schedules the next tick.
type tokenTickMsg struct{}

// tokenAnimFrame is the inter-frame delay for the counter tween.
// 33ms (~30 fps) feels live without saturating the event loop.
const tokenAnimFrame = 33 * time.Millisecond

func scheduleTokenTick() tea.Cmd {
	return tea.Tick(tokenAnimFrame, func(time.Time) tea.Msg { return tokenTickMsg{} })
}

// stepToward returns the new value of `shown` advanced toward
// `target` for one animation frame. Eases out — moves a fraction of
// the remaining distance each frame, with a minimum step so small
// gaps still close in finite time.
func stepToward(shown, target int) int {
	if shown == target {
		return shown
	}
	delta := target - shown
	step := delta / 5 // ~5 frames to close the gap
	if step == 0 {
		if delta > 0 {
			return shown + 1
		}
		return shown - 1
	}
	return shown + step
}

// renderTokenLine swaps the current transient (if any) for the
// formatted live counter line so the user sees it climb in place.
func (r *REPL) renderTokenLine() {
	r.clearTransient()
	r.writeTransient(styleDim.Render(formatTokens(r.tokenShownIn, r.tokenShownOut)))
}

// writeTransient writes a status line ("planning…", "running plan…")
// and remembers its position so clearTransient can drop it later.
// Always pair a writeTransient with a clearTransient call from the
// matching async-result handler so stale indicators don't pile up.
func (r *REPL) writeTransient(text string) {
	r.transientStart = len(r.buf)
	r.write(text)
}

// clearTransient drops the most recent transient line if any. No-op
// otherwise, so it's safe to call defensively from message handlers
// that may or may not have been preceded by a transient write.
func (r *REPL) clearTransient() {
	if r.transientStart < 0 || r.transientStart > len(r.buf) {
		r.transientStart = -1
		return
	}
	r.buf = r.buf[:r.transientStart]
	r.transientStart = -1
	r.output.SetContent(r.buf)
	r.output.GotoBottom()
}

// appendSeparator inserts a blank line before the next entry so each
// conversational turn has visual breathing room. No-op when the buf
// is empty (first turn) or when the most recent line is already
// blank — keeps the gap to one line, never doubles up.
func (r *REPL) appendSeparator() {
	if r.buf == "" {
		return
	}
	if strings.HasSuffix(r.buf, "\n\n") {
		return
	}
	r.buf += "\n"
	r.output.SetContent(r.buf)
}

// renderMarkdown turns markdown into ANSI-styled terminal text using
// glamour. The renderer is reconstructed each call because the pane
// width can change between calls (resize); glamour caches per-width.
//
// We pin the "dark" style rather than using glamour.WithAutoStyle().
// AutoStyle queries the terminal for its background color via an
// OSC 11 sequence; in alt-screen mode (which Bubble Tea uses) the
// terminal's reply leaks back into the visible buffer as garbled
// text like `> 11;rgb:158e/193a/1e75\`. Pinned style avoids the
// query entirely. Light-terminal users can override via a future
// `repl.markdown_style` config — not worth the complexity right now.
func renderMarkdown(md string, width int) (string, error) {
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return r.Render(md)
}

// wrapWidth returns the column count to wrap at, leaving a tiny
// margin on the right so terminal-edge artifacts don't bite.
func (r *REPL) wrapWidth() int {
	w := r.width
	if w <= 0 {
		w = 80
	}
	if w > 4 {
		w -= 2
	}
	return w
}

// wrapToWidth word-wraps `s` at `width` visible columns. Naïve about
// ANSI escapes (treats them as normal runes) which is acceptable for
// our content — chat replies are plain text and styled prompts are
// short enough that they never overflow. Preserves explicit newlines
// in the input. Falls through unchanged when width <= 0.
func wrapToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(wrapLine(line, width))
	}
	return out.String()
}

func wrapLine(line string, width int) string {
	if utf8RuneLen(line) <= width {
		return line
	}
	var out strings.Builder
	col := 0
	first := true
	for _, word := range strings.Fields(line) {
		wlen := utf8RuneLen(word)
		switch {
		case first:
			out.WriteString(word)
			col = wlen
			first = false
		case col+1+wlen <= width:
			out.WriteByte(' ')
			out.WriteString(word)
			col += 1 + wlen
		default:
			out.WriteByte('\n')
			out.WriteString(word)
			col = wlen
		}
	}
	return out.String()
}

// utf8RuneLen counts visible runes, not bytes, so a CJK character
// counts as one column. ANSI escapes still pollute the count — but
// the content we wrap (chat replies, tool output) is mostly plain.
func utf8RuneLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
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
