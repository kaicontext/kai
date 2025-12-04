package diff

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatText formats a semantic diff as human-readable text.
func (sd *SemanticDiff) FormatText() string {
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

		sb.WriteString(fmt.Sprintf("%s %s\n", actionChar, f.Path))

		// Unit changes
		for _, u := range f.Units {
			sb.WriteString(formatUnit(u))
		}

		if len(f.Units) > 0 {
			sb.WriteString("\n")
		}
	}

	// Summary
	if sd.Summary.FilesAdded > 0 || sd.Summary.FilesModified > 0 || sd.Summary.FilesRemoved > 0 {
		sb.WriteString(fmt.Sprintf("\nSummary: %d files (%d added, %d modified, %d removed)\n",
			sd.Summary.FilesAdded+sd.Summary.FilesModified+sd.Summary.FilesRemoved,
			sd.Summary.FilesAdded, sd.Summary.FilesModified, sd.Summary.FilesRemoved))
		sb.WriteString(fmt.Sprintf("         %d units (%d added, %d modified, %d removed)\n",
			sd.Summary.UnitsAdded+sd.Summary.UnitsModified+sd.Summary.UnitsRemoved,
			sd.Summary.UnitsAdded, sd.Summary.UnitsModified, sd.Summary.UnitsRemoved))
	}

	return sb.String()
}

// formatUnit formats a single unit diff.
func formatUnit(u UnitDiff) string {
	var sb strings.Builder

	actionChar := getActionChar(u.Action)
	kindStr := formatKind(u.Kind)

	switch u.Kind {
	case KindFunction, KindMethod:
		if u.Action == ActionModified && u.BeforeSig != u.AfterSig {
			// Signature change - signature already includes "function" keyword
			sb.WriteString(fmt.Sprintf("  %s %s -> %s\n", actionChar, u.BeforeSig, u.AfterSig))
		} else if u.Action == ActionAdded {
			sig := u.AfterSig
			if sig == "" {
				sig = kindStr + " " + u.Name
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n", actionChar, sig))
		} else if u.Action == ActionRemoved {
			sig := u.BeforeSig
			if sig == "" {
				sig = kindStr + " " + u.Name
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n", actionChar, sig))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s %s\n", actionChar, kindStr, u.Name))
		}

	case KindClass:
		sb.WriteString(fmt.Sprintf("  %s %s %s\n", actionChar, kindStr, u.Name))

	case KindVariable, KindConst:
		if u.Action == ActionModified && u.Before != u.After {
			// Show value change for simple values
			before := truncateValue(u.Before)
			after := truncateValue(u.After)
			sb.WriteString(fmt.Sprintf("  %s %s: %s -> %s\n", actionChar, u.Name, before, after))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s %s\n", actionChar, kindStr, u.Name))
		}

	case KindJSONKey, KindYAMLKey:
		path := u.Path
		if path == "" {
			path = u.Name
		}
		if u.Action == ActionModified && u.Before != "" && u.After != "" {
			sb.WriteString(fmt.Sprintf("  %s %s: %s -> %s\n", actionChar, path, truncateValue(u.Before), truncateValue(u.After)))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s\n", actionChar, path))
		}

	case KindSQLTable:
		sb.WriteString(fmt.Sprintf("  %s table %s\n", actionChar, u.Name))

	case KindSQLColumn:
		if u.Action == ActionModified {
			sb.WriteString(fmt.Sprintf("  %s %s: %s -> %s\n", actionChar, u.Path, truncateValue(u.Before), truncateValue(u.After)))
		} else {
			defStr := ""
			if u.After != "" {
				defStr = ": " + truncateValue(u.After)
			} else if u.Before != "" {
				defStr = ": " + truncateValue(u.Before)
			}
			sb.WriteString(fmt.Sprintf("  %s %s%s\n", actionChar, u.Path, defStr))
		}

	default:
		sb.WriteString(fmt.Sprintf("  %s %s %s\n", actionChar, kindStr, u.Name))
	}

	return sb.String()
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
