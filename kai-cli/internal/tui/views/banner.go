package views

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderBanner builds the startup splash that appears once at the top
// of the REPL scrollback. Mirrors the Claude Code banner shape: a
// small mascot on the left, identity + connection details on the
// right. Designed to fit a single visual block (~7 rows) so it
// doesn't dominate the viewport on launch.
func renderBanner(s *PlannerServices) string {
	mascot := mascotArt()
	mascotLines := strings.Split(mascot, "\n")

	version := "dev"
	if s != nil && s.Version != "" {
		version = s.Version
	}
	model := "(not configured — run `kai auth login`)"
	provider := "offline"
	if s != nil {
		if s.PlannerCfg.Model != "" {
			model = s.PlannerCfg.Model
		}
		if s.OrchestratorCfg.AgentProvider != nil {
			provider = "kailab"
		}
	}
	workspace := compactPath(workspaceFor(s))

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")) // amber, matches mascot
	dim := styleDim
	bullet := dim.Render("›")

	right := []string{
		titleStyle.Render("kai") + dim.Render(" v"+version),
		bullet + " " + dim.Render(provider+" → "+model),
		bullet + " " + dim.Render(workspace),
		"",
		dim.Render("Enter sends · Alt+Enter for newline · ↑/↓ history"),
		dim.Render("/command for kai subcommands · Ctrl+C twice to exit"),
	}

	// Pad the shorter column up to the longer one so JoinHorizontal
	// produces a clean rectangle (no ragged trailing rows).
	rows := max(len(mascotLines), len(right))
	for len(mascotLines) < rows {
		mascotLines = append(mascotLines, strings.Repeat(" ", visibleWidth(mascotLines[0])))
	}
	for len(right) < rows {
		right = append(right, "")
	}

	left := strings.Join(mascotLines, "\n")
	rightCol := strings.Join(right, "\n")

	return lipgloss.JoinHorizontal(lipgloss.Top,
		left,
		"  ",
		rightCol,
	)
}

// mascotArt returns the pixel-art kai icon. Sourced from
// banner_art.go; rendered raw (escape codes paint each cell's
// background) so the artwork is identical across terminals that
// support 256-color SGR.
func mascotArt() string { return mascotPixelArt }

// workspaceFor returns the workspace root for the banner. Falls
// back to the process cwd when services aren't configured.
func workspaceFor(s *PlannerServices) string {
	if s != nil && s.MainRepo != "" {
		return s.MainRepo
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "(unknown)"
}

// compactPath replaces $HOME with "~" for terser display in the
// banner. Path stays absolute when not under home.
func compactPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// visibleWidth approximates the rendered column width of a styled
// string. Strips ANSI SGR sequences and counts runes — good enough
// for our right-padding needs (mascot lines never contain CJK).
func visibleWidth(s string) int {
	stripped := stripSGR(s)
	n := 0
	for range stripped {
		n++
	}
	return n
}

func stripSGR(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !in && ch == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			in = true
			i++ // skip '['
			continue
		}
		if in {
			if ch == 'm' {
				in = false
			}
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// helper: avoid shadowing built-in min from older Go versions.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// formatBannerError renders a single dim line for the no-services
// case so the user still sees identity at startup.
func formatBannerError() string {
	return styleDim.Render("kai TUI · /command for kai subcommands · ↑/↓ history · Ctrl+C ×2 to exit")
}
