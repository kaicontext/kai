package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// formatDiffEvent renders a ChatActivityEvent of kind "diff" as the
// inline block the user sees in the REPL: a header line summarizing
// what changed, followed by additions/removals colored green/red and
// context lines dimmed. Mirrors the Claude Code "Update(path)" style.
//
// width is the pane's wrap width; each diff line gets padded out to
// it so the colored backgrounds extend to the right edge — same as a
// real terminal diff viewer. Lines longer than width are truncated
// with an ellipsis (we do NOT word-wrap diff lines: a wrap inside a
// `+const PORT = ...` would lose the leading + and confuse the user
// about which line is the addition).
//
// We intentionally don't render the unified-diff hunk markers
// (`@@ -a,b +c,d @@`) — they're noise for an inline activity feed.
// The "Soon" spec calls for a proper scrollable diff view; until then
// the patch text is rendered as-is, line by line.
func formatDiffEvent(ev ChatActivityEvent, width int) string {
	if width <= 0 {
		width = 80
	}
	verb := "Update"
	if ev.Op == "created" {
		verb = "Create"
	}
	var b strings.Builder

	// Header: "● Update(path/to/file.go)"
	header := lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("● %s(%s)", verb, ev.Path))
	b.WriteString(header)
	b.WriteByte('\n')

	// Sub-header: "  └ Added N lines, removed M lines"
	stats := summarizeAddedRemoved(ev.Added, ev.Removed)
	b.WriteString(styleDim.Render("  └ " + stats))
	b.WriteByte('\n')

	// Determine the line-number column width from the largest
	// number that will appear, so all numbers right-align in a
	// fixed gutter. Default to 4 when we can't tell.
	gutterWidth := computeGutterWidth(ev.Diff)
	gutter := gutterWidth + 2 // number + " " + marker
	bodyMax := width - gutter

	// Body: render each diff line. Skip the "--- a/" / "+++ b/"
	// preamble since the header already tells the user the path.
	// "@@" lines mark a hunk break — render as a dim ellipsis
	// strip so the user sees that some unchanged lines were
	// elided between hunks.
	for _, line := range strings.Split(ev.Diff, "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if line == "" {
			continue
		}
		if line == "@@" {
			b.WriteString(styleDim.Render(strings.Repeat("·", min(width, 40))))
			b.WriteByte('\n')
			continue
		}
		// Split off the leading "<num>\x1f" produced by
		// unifiedDiff. Fall back to no-gutter rendering on
		// malformed input rather than dropping the line.
		num, rest, ok := strings.Cut(line, "\x1f")
		if !ok {
			rest = line
			num = ""
		}
		marker := byte(' ')
		body := rest
		if len(rest) > 0 {
			marker = rest[0]
			body = rest[1:]
		}
		body = truncateRunes(body, bodyMax-1)
		body = body + strings.Repeat(" ", max0(bodyMax-1-runeCount(body)))

		// Gutter: right-aligned line number. Empty for the rare
		// case that splitting failed.
		numField := num
		if numField == "" {
			numField = strings.Repeat(" ", gutterWidth)
		} else if len(numField) < gutterWidth {
			numField = strings.Repeat(" ", gutterWidth-len(numField)) + numField
		}

		gut := styleDiffGutter.Render(numField + " ")
		var styled string
		switch marker {
		case '+':
			styled = gut + styleDiffAdd.Render(string(marker)+body)
		case '-':
			styled = gut + styleDiffDel.Render(string(marker)+body)
		default:
			styled = gut + styleDiffCtx.Render(" "+body)
		}
		b.WriteString(styled)
		b.WriteByte('\n')
	}
	// Trim the trailing newline so write() doesn't double-space the
	// scrollback's separator logic.
	return strings.TrimRight(b.String(), "\n")
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if runeCount(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	for _, r := range s {
		if len(out) >= n-1 {
			break
		}
		out = append(out, r)
	}
	return string(out) + "…"
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func summarizeAddedRemoved(added, removed int) string {
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("Added %d line%s, removed %d line%s",
			added, plural(added), removed, plural(removed))
	case added > 0:
		return fmt.Sprintf("Added %d line%s", added, plural(added))
	case removed > 0:
		return fmt.Sprintf("Removed %d line%s", removed, plural(removed))
	default:
		return "No line changes"
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Diff line styles. Strong saturated backgrounds so additions and
// removals are unmistakable at a glance; context lines stay neutral.
// formatGateVerdict renders a one-line, color-coded summary of the
// safety gate's classification of a freshly-mutated set of paths.
// Auto verdicts are green (no action needed), Review verdicts are
// amber (kai pane will hold the change for the user to inspect),
// Block verdicts are red (touched a protected path or exceeded the
// block threshold). Mirrors the look of the existing tool-call
// breadcrumbs so the verdict reads as a continuation of the
// preceding edit, not a separate event.
func formatGateVerdict(ev ChatActivityEvent) string {
	var glyph, label string
	var styled lipgloss.Style
	switch ev.GateVerdict {
	case "auto":
		glyph, label = "✓", "auto"
		styled = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	case "review":
		glyph, label = "⚠", "held"
		styled = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	case "block":
		glyph, label = "✗", "blocked"
		styled = lipgloss.NewStyle().Foreground(lipgloss.Color("9")) // red
	default:
		glyph, label = "·", ev.GateVerdict
		styled = styleDim
	}
	suffix := fmt.Sprintf("%d downstream", ev.GateRadius)
	if len(ev.GateReasons) > 0 {
		suffix = ev.GateReasons[0]
	}
	pathLabel := strings.Join(ev.GatePaths, ", ")
	if pathLabel == "" {
		pathLabel = "(no paths)"
	}
	return "  " + styled.Render(fmt.Sprintf("%s %s — %s", glyph, label, suffix)) +
		styleDim.Render("  "+pathLabel)
}

var (
	styleDiffAdd = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).      // bright white
			Background(lipgloss.Color("#1f3f1f")). // dark green
			Bold(false)
	styleDiffDel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).      // bright white
			Background(lipgloss.Color("#3f1f1f")). // dark red
			Bold(false)
	styleDiffCtx = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // dim gray, no background
	styleDiffGutter = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // line numbers — same dim
)

// computeGutterWidth scans the patch for the largest line number it
// will emit so we can right-align all gutters to a consistent width.
// Falls back to 4 (room for "9999") on malformed or empty input.
func computeGutterWidth(patch string) int {
	maxLen := 0
	for _, line := range strings.Split(patch, "\n") {
		if i := strings.IndexByte(line, '\x1f'); i > 0 {
			if i > maxLen {
				maxLen = i
			}
		}
	}
	if maxLen < 4 {
		maxLen = 4
	}
	return maxLen
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
