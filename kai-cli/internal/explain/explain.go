// Package explain provides human-readable explanations for Kai commands.
// When --explain is passed to any command, this package generates
// context about what nouns are being used and why.
package explain

import (
	"fmt"
	"io"
	"strings"
)

// Context holds information about an operation to explain
type Context struct {
	Command     string
	Nouns       []Noun
	Description string
	Steps       []string
	Tips        []string
}

// Noun represents a Kai concept being used
type Noun struct {
	Name        string
	Description string
	WhyUsed     string
}

// Print writes a formatted explanation to the given writer
func (c *Context) Print(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "╭─ Explain: %s\n", c.Command)
	fmt.Fprintln(w, "│")

	if c.Description != "" {
		fmt.Fprintf(w, "│  %s\n", c.Description)
		fmt.Fprintln(w, "│")
	}

	if len(c.Nouns) > 0 {
		fmt.Fprintln(w, "│  📦 Concepts used:")
		for _, n := range c.Nouns {
			fmt.Fprintf(w, "│     • %s: %s\n", n.Name, n.Description)
			if n.WhyUsed != "" {
				fmt.Fprintf(w, "│       → %s\n", n.WhyUsed)
			}
		}
		fmt.Fprintln(w, "│")
	}

	if len(c.Steps) > 0 {
		fmt.Fprintln(w, "│  📋 What this command does:")
		for i, step := range c.Steps {
			fmt.Fprintf(w, "│     %d. %s\n", i+1, step)
		}
		fmt.Fprintln(w, "│")
	}

	if len(c.Tips) > 0 {
		fmt.Fprintln(w, "│  💡 Tips:")
		for _, tip := range c.Tips {
			fmt.Fprintf(w, "│     %s\n", tip)
		}
		fmt.Fprintln(w, "│")
	}

	fmt.Fprintln(w, "╰────────────────────────────────────────")
	fmt.Fprintln(w)
}

// ExplainCapture returns explanation context for the capture command
func ExplainCapture(dir string, moduleCount int) *Context {
	return &Context{
		Command:     "kai capture",
		Description: "Captures a semantic snapshot of your codebase in one step.",
		Nouns: []Noun{
			{
				Name:        "Snapshot",
				Description: "A semantic capture of your codebase at a point in time",
				WhyUsed:     "Created automatically from " + dir,
			},
			{
				Name:        "Symbols",
				Description: "Functions, classes, and variables in your code",
				WhyUsed:     "Extracted to enable semantic diffs",
			},
			{
				Name:        "Modules",
				Description: "Logical groupings of files (e.g., Auth, Billing)",
				WhyUsed:     fmt.Sprintf("%d modules configured to categorize changes", moduleCount),
			},
		},
		Steps: []string{
			"Create a snapshot of all files in " + dir,
			"Parse code to extract symbols (functions, classes)",
			"Build the call graph (imports, function calls)",
			"Match files to modules for intent generation",
		},
		Tips: []string{
			"Run 'kai diff' after making changes to see semantic differences",
			"Use 'kai status' to preview pending changes before capturing",
		},
	}
}

// ExplainDiff returns explanation context for the diff command
func ExplainDiff(baseRef, headRef string, fileCount int, changeTypes []string) *Context {
	ctx := &Context{
		Command:     "kai diff",
		Description: "Shows semantic differences between snapshots.",
		Nouns: []Noun{
			{
				Name:        "Snapshot",
				Description: "Semantic capture of code at a point in time",
				WhyUsed:     fmt.Sprintf("Comparing %s to %s", baseRef, headRef),
			},
		},
		Steps: []string{
			"Load base snapshot: " + baseRef,
			"Load head snapshot: " + headRef,
			fmt.Sprintf("Compare %d file(s) for changes", fileCount),
			"Classify change types (function added, condition changed, etc.)",
		},
	}

	if len(changeTypes) > 0 {
		ctx.Nouns = append(ctx.Nouns, Noun{
			Name:        "ChangeTypes",
			Description: "Semantic classifications of what changed",
			WhyUsed:     fmt.Sprintf("Detected: %s", strings.Join(changeTypes, ", ")),
		})
	}

	ctx.Tips = []string{
		"Add --semantic for symbol-level details",
		"Add --json for machine-readable output",
	}

	return ctx
}

// ExplainReviewOpen returns explanation context for review open
func ExplainReviewOpen(target, title string) *Context {
	return &Context{
		Command:     "kai review open",
		Description: "Creates a code review for the specified changes.",
		Nouns: []Noun{
			{
				Name:        "Review",
				Description: "A code review session with comments and approvals",
				WhyUsed:     fmt.Sprintf("Created for: %s", title),
			},
			{
				Name:        "ChangeSet",
				Description: "The set of changes being reviewed",
				WhyUsed:     fmt.Sprintf("Target: %s", target),
			},
		},
		Steps: []string{
			"Create review record linked to the changeset",
			"Set review status to 'pending'",
			"Ready to accept comments and approvals",
		},
		Tips: []string{
			"Use 'kai review view <id>' to see the review",
			"Use 'kai review approve <id>' to approve",
		},
	}
}

// ExplainCIPlan returns explanation context for ci plan
func ExplainCIPlan(changesetRef string, strategy string, affectedFiles, affectedTests int) *Context {
	return &Context{
		Command:     "kai ci plan",
		Description: "Computes which tests and builds need to run based on changes.",
		Nouns: []Noun{
			{
				Name:        "ChangeSet",
				Description: "The set of code changes to analyze",
				WhyUsed:     fmt.Sprintf("Analyzing: %s", changesetRef),
			},
			{
				Name:        "CallGraph",
				Description: "Map of which files import/call other files",
				WhyUsed:     fmt.Sprintf("Used to trace impact across %d changed files", affectedFiles),
			},
			{
				Name:        "Modules",
				Description: "Logical groupings for risk assessment",
				WhyUsed:     "Determines cross-module risk level",
			},
			{
				Name:        "Strategy",
				Description: "How to determine affected tests",
				WhyUsed:     fmt.Sprintf("Using '%s' strategy", strategy),
			},
		},
		Steps: []string{
			fmt.Sprintf("Load changeset and identify %d modified files", affectedFiles),
			"Trace the call graph to find affected files",
			"Identify test files that cover affected code",
			"Assess structural risks (config changes, dynamic imports)",
			fmt.Sprintf("Select %d tests to run", affectedTests),
		},
		Tips: []string{
			"Use --strategy=symbols for most precise selection",
			"Use --safety-mode=shadow to learn without affecting CI",
			"Use --out plan.json to save for CI consumption",
		},
	}
}

// ExplainSnapshot returns explanation context for snapshot creation
func ExplainSnapshot(source, sourceType string, fileCount int) *Context {
	ctx := &Context{
		Command:     "kai snapshot",
		Description: "Creates an immutable semantic capture of your codebase.",
		Nouns: []Noun{
			{
				Name:        "Snapshot",
				Description: "Immutable semantic capture of code at this moment",
				WhyUsed:     fmt.Sprintf("Created from %s (%s)", source, sourceType),
			},
			{
				Name:        "File",
				Description: "Source files included in the snapshot",
				WhyUsed:     fmt.Sprintf("%d files captured", fileCount),
			},
		},
		Steps: []string{
			"Read all source files from " + sourceType,
			"Compute content-addressed hash for deduplication",
			"Store file contents in object store",
			"Create snapshot node with file references",
			"Analyze symbols in each file",
		},
	}

	if sourceType == "git" {
		ctx.Tips = []string{
			"Use 'kai snap' for quick directory snapshots without Git",
			"Snapshots from Git refs are immutable and reproducible",
		}
	} else {
		ctx.Tips = []string{
			"Directory snapshots include uncommitted changes",
			"Use 'kai snapshot --git <ref>' to snapshot a specific Git commit",
		}
	}

	return ctx
}

// ExplainWorkspaceCreate returns explanation context for workspace creation
func ExplainWorkspaceCreate(name, base string) *Context {
	return &Context{
		Command:     "kai ws create",
		Description: "Creates a lightweight branch overlay for isolated work.",
		Nouns: []Noun{
			{
				Name:        "Workspace",
				Description: "A mutable branch overlay on top of immutable snapshots",
				WhyUsed:     fmt.Sprintf("Creating '%s' based on %s", name, base),
			},
			{
				Name:        "Snapshot",
				Description: "The base state the workspace starts from",
				WhyUsed:     fmt.Sprintf("Base: %s", base),
			},
		},
		Steps: []string{
			"Create workspace record with 'active' status",
			"Link workspace to base snapshot",
			"Set as current workspace (tracked in .kai/workspace)",
		},
		Tips: []string{
			"Use 'kai ws stage' to add changes to the workspace",
			"Use 'kai ws shelve' to temporarily freeze the workspace",
			"Use 'kai integrate' to merge workspace changes into a target",
		},
	}
}
