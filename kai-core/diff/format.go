package diff

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatText formats a semantic diff as human-readable plain text.
// Callers wanting ANSI-colored output for terminals should use FormatTextColor.
func (sd *SemanticDiff) FormatText() string {
	return sd.formatText(false)
}

// FormatTextColor formats a semantic diff with ANSI color codes:
// added symbols in green, removed in red, modified in yellow, bold file
// headers, dim '->' in signature changes. Produces the exact same content
// as FormatText — only styling differs.
func (sd *SemanticDiff) FormatTextColor() string {
	return sd.formatText(true)
}

func (sd *SemanticDiff) formatText(color bool) string {
	var sb strings.Builder

	for _, f := range sd.Files {
		// File header with action indicator
		var actionChar string
		switch f.Action {
		case ActionAdded:
			actionChar = "+"
		case ActionRemoved:
			actionChar = "-"
		case ActionModified:
			actionChar = "~"
		}

		// File headers get the action color and bold styling in color mode.
		if color {
			sb.WriteString(fmt.Sprintf("%s %s\n",
				colorizeAction(actionChar, actionChar),
				ansiBold(f.Path)))
		} else {
			sb.WriteString(fmt.Sprintf("%s %s\n", actionChar, f.Path))
		}

		// Unit changes
		for _, u := range f.Units {
			sb.WriteString(formatUnitWithColor(u, color))
		}

		if len(f.Units) > 0 {
			sb.WriteString("\n")
		}
	}

	// Summary — colorize the add/modify/remove counts so the eye lands on
	// what changed rather than the prose around them.
	if sd.Summary.FilesAdded > 0 || sd.Summary.FilesModified > 0 || sd.Summary.FilesRemoved > 0 {
		label := "Summary:"
		if color {
			label = ansiBold("Summary:")
		}
		sb.WriteString(fmt.Sprintf("\n%s %d files (%s, %s, %s)\n",
			label,
			sd.Summary.FilesAdded+sd.Summary.FilesModified+sd.Summary.FilesRemoved,
			countLabel(sd.Summary.FilesAdded, "added", "+", color),
			countLabel(sd.Summary.FilesModified, "modified", "~", color),
			countLabel(sd.Summary.FilesRemoved, "removed", "-", color)))
		sb.WriteString(fmt.Sprintf("         %d units (%s, %s, %s)\n",
			sd.Summary.UnitsAdded+sd.Summary.UnitsModified+sd.Summary.UnitsRemoved,
			countLabel(sd.Summary.UnitsAdded, "added", "+", color),
			countLabel(sd.Summary.UnitsModified, "modified", "~", color),
			countLabel(sd.Summary.UnitsRemoved, "removed", "-", color)))
	}

	return sb.String()
}

// countLabel renders "N added" / "N modified" / "N removed" with the
// appropriate color when nonzero. Zero counts stay dim so they don't
// compete with the nonzero numbers for attention.
func countLabel(n int, word, action string, color bool) string {
	base := fmt.Sprintf("%d %s", n, word)
	if !color {
		return base
	}
	if n == 0 {
		return ansiDim(base)
	}
	switch action {
	case "+":
		return "\033[32m" + base + "\033[0m"
	case "-":
		return "\033[31m" + base + "\033[0m"
	case "~":
		return "\033[33m" + base + "\033[0m"
	}
	return base
}

// ANSI helpers — small, local, no external deps. colorize receives the raw
// action char so we know which color to apply; the visible text can differ.
func colorizeAction(action, visible string) string {
	switch action {
	case "+":
		return "\033[32m" + visible + "\033[0m" // green
	case "-":
		return "\033[31m" + visible + "\033[0m" // red
	case "~":
		return "\033[33m" + visible + "\033[0m" // yellow
	default:
		return visible
	}
}

func ansiBold(s string) string { return "\033[1m" + s + "\033[0m" }
func ansiDim(s string) string  { return "\033[2m" + s + "\033[0m" }

// formatUnit formats a single unit diff (uncolored, kept for back-compat).
func formatUnit(u UnitDiff) string {
	return formatUnitWithColor(u, false)
}

// formatUnitWithColor formats a single unit diff, optionally with ANSI color.
// Visual grammar when color is on:
//
//	~ file.js                       <- yellow tilde, bold filename
//	  + added_thing                 <- whole line green
//	  - removed_thing               <- whole line red
//	  ~ modified_thing              <- yellow tilde, plain text
//
// A modified function/method whose signature actually changed is rendered
// as a red line + green line pair (git convention) rather than a single
// "~ before -> after" line — it's substantially easier to read at a glance.
func formatUnitWithColor(u UnitDiff, color bool) string {
	var sb strings.Builder

	kindStr := formatKind(u.Kind)

	// Emit a fully-colored line for the add/remove case. Modified cases handle
	// their own coloring per-branch below.
	addedLine := func(body string) string {
		line := fmt.Sprintf("  + %s\n", body)
		if color {
			return colorizeLine("+", line)
		}
		return line
	}
	removedLine := func(body string) string {
		line := fmt.Sprintf("  - %s\n", body)
		if color {
			return colorizeLine("-", line)
		}
		return line
	}
	modifiedLine := func(body string) string {
		tilde := "~"
		if color {
			tilde = colorizeAction("~", "~")
		}
		return fmt.Sprintf("  %s %s\n", tilde, body)
	}

	switch u.Kind {
	case KindFunction, KindMethod:
		if u.Action == ActionModified && u.BeforeSig != u.AfterSig {
			// Signature changed -> render as a remove+add pair, like git does.
			sb.WriteString(removedLine(u.BeforeSig))
			sb.WriteString(addedLine(u.AfterSig))
		} else if u.Action == ActionAdded {
			sig := u.AfterSig
			if sig == "" {
				sig = kindStr + " " + u.Name
			}
			sb.WriteString(addedLine(sig))
		} else if u.Action == ActionRemoved {
			sig := u.BeforeSig
			if sig == "" {
				sig = kindStr + " " + u.Name
			}
			sb.WriteString(removedLine(sig))
		} else {
			sb.WriteString(modifiedLine(fmt.Sprintf("%s %s", kindStr, u.Name)))
		}

	case KindClass:
		body := fmt.Sprintf("%s %s", kindStr, u.Name)
		switch u.Action {
		case ActionAdded:
			sb.WriteString(addedLine(body))
		case ActionRemoved:
			sb.WriteString(removedLine(body))
		default:
			sb.WriteString(modifiedLine(body))
		}

	case KindVariable, KindConst:
		if u.Action == ActionModified && u.Before != u.After {
			// Value changed -> remove/add pair shows the old and new values cleanly.
			sb.WriteString(removedLine(fmt.Sprintf("%s = %s", u.Name, truncateValue(u.Before))))
			sb.WriteString(addedLine(fmt.Sprintf("%s = %s", u.Name, truncateValue(u.After))))
		} else {
			body := fmt.Sprintf("%s %s", kindStr, u.Name)
			switch u.Action {
			case ActionAdded:
				sb.WriteString(addedLine(body))
			case ActionRemoved:
				sb.WriteString(removedLine(body))
			default:
				sb.WriteString(modifiedLine(body))
			}
		}

	case KindJSONKey, KindYAMLKey:
		path := u.Path
		if path == "" {
			path = u.Name
		}
		if u.Action == ActionModified && u.Before != "" && u.After != "" {
			sb.WriteString(removedLine(fmt.Sprintf("%s: %s", path, truncateValue(u.Before))))
			sb.WriteString(addedLine(fmt.Sprintf("%s: %s", path, truncateValue(u.After))))
		} else {
			switch u.Action {
			case ActionAdded:
				sb.WriteString(addedLine(path))
			case ActionRemoved:
				sb.WriteString(removedLine(path))
			default:
				sb.WriteString(modifiedLine(path))
			}
		}

	case KindSQLTable:
		body := fmt.Sprintf("table %s", u.Name)
		switch u.Action {
		case ActionAdded:
			sb.WriteString(addedLine(body))
		case ActionRemoved:
			sb.WriteString(removedLine(body))
		default:
			sb.WriteString(modifiedLine(body))
		}

	case KindSQLColumn:
		if u.Action == ActionModified {
			sb.WriteString(removedLine(fmt.Sprintf("%s %s", u.Path, truncateValue(u.Before))))
			sb.WriteString(addedLine(fmt.Sprintf("%s %s", u.Path, truncateValue(u.After))))
		} else {
			defStr := ""
			if u.After != "" {
				defStr = ": " + truncateValue(u.After)
			} else if u.Before != "" {
				defStr = ": " + truncateValue(u.Before)
			}
			body := fmt.Sprintf("%s%s", u.Path, defStr)
			switch u.Action {
			case ActionAdded:
				sb.WriteString(addedLine(body))
			case ActionRemoved:
				sb.WriteString(removedLine(body))
			default:
				sb.WriteString(modifiedLine(body))
			}
		}

	default:
		body := fmt.Sprintf("%s %s", kindStr, u.Name)
		switch u.Action {
		case ActionAdded:
			sb.WriteString(addedLine(body))
		case ActionRemoved:
			sb.WriteString(removedLine(body))
		default:
			sb.WriteString(modifiedLine(body))
		}
	}

	return sb.String()
}

// colorizeLine wraps a whole line in the action's color, preserving the
// trailing newline outside the reset so terminals don't inherit the color
// across line boundaries.
func colorizeLine(action, line string) string {
	trimmed := strings.TrimRight(line, "\n")
	trailing := line[len(trimmed):]
	var prefix string
	switch action {
	case "+":
		prefix = "\033[32m"
	case "-":
		prefix = "\033[31m"
	case "~":
		prefix = "\033[33m"
	default:
		return line
	}
	return prefix + trimmed + "\033[0m" + trailing
}

func getActionChar(action Action) string {
	switch action {
	case ActionAdded:
		return "+"
	case ActionRemoved:
		return "-"
	case ActionModified:
		return "~"
	default:
		return " "
	}
}

func formatKind(kind UnitKind) string {
	switch kind {
	case KindFunction:
		return "function"
	case KindClass:
		return "class"
	case KindMethod:
		return "method"
	case KindConst:
		return "const"
	case KindVariable:
		return "var"
	case KindJSONKey:
		return ""
	case KindYAMLKey:
		return ""
	case KindSQLTable:
		return "table"
	case KindSQLColumn:
		return ""
	default:
		return string(kind)
	}
}

func truncateValue(s string) string {
	// Remove newlines and excessive whitespace
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")

	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

// FormatJSON formats a semantic diff as JSON.
func (sd *SemanticDiff) FormatJSON() ([]byte, error) {
	return json.MarshalIndent(sd, "", "  ")
}

// FormatCompact formats a semantic diff as compact single-line output.
func (sd *SemanticDiff) FormatCompact() string {
	var parts []string

	for _, f := range sd.Files {
		actionChar := getActionChar(f.Action)
		if len(f.Units) == 0 {
			parts = append(parts, fmt.Sprintf("%s %s", actionChar, f.Path))
		} else {
			for _, u := range f.Units {
				unitAction := getActionChar(u.Action)
				name := u.Name
				if u.Path != "" {
					name = u.Path
				}
				parts = append(parts, fmt.Sprintf("%s %s:%s", unitAction, f.Path, name))
			}
		}
	}

	return strings.Join(parts, "\n")
}

// FormatStats returns just the statistics line.
func (sd *SemanticDiff) FormatStats() string {
	return fmt.Sprintf("%d files changed (%d+, %d~, %d-), %d units (%d+, %d~, %d-)",
		sd.Summary.FilesAdded+sd.Summary.FilesModified+sd.Summary.FilesRemoved,
		sd.Summary.FilesAdded, sd.Summary.FilesModified, sd.Summary.FilesRemoved,
		sd.Summary.UnitsAdded+sd.Summary.UnitsModified+sd.Summary.UnitsRemoved,
		sd.Summary.UnitsAdded, sd.Summary.UnitsModified, sd.Summary.UnitsRemoved)
}
