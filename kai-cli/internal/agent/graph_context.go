package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"kai/internal/agent/message"
	"kai/internal/graph"
)

// graphContextInjector tracks which files have already had their
// graph relationships sent to the model so we don't repeat the same
// context block on every turn. The state lives on the runner stack;
// it doesn't survive across runs (resumed sessions re-inject from
// scratch on first turn — small redundancy in exchange for
// stateless persistence).
type graphContextInjector struct {
	graph    *graph.DB
	injected map[string]bool // workspace-relative paths
}

func newGraphContextInjector(g *graph.DB) *graphContextInjector {
	if g == nil {
		return nil // nil receiver short-circuits all methods
	}
	return &graphContextInjector{
		graph:    g,
		injected: make(map[string]bool),
	}
}

// buildBlock looks at the new content added since the last
// injection (the just-appended user message + any tool results from
// the prior turn), pulls file paths out, queries the graph for
// each path's depth-1 callers + protected status, and returns a
// short text block ready to prepend to the system role. Returns ""
// when nothing new is in scope so the caller can skip the prefix.
//
// We deliberately stay file-level rather than symbol-level for now:
// graph traversal is cheap, the block stays compact (1 line per
// file), and a follow-up can promote to symbol granularity once we
// have a clear UX win to point at.
func (gc *graphContextInjector) buildBlock(history []message.Message, protected []string) string {
	if gc == nil {
		return ""
	}
	// Scan only the latest user-role message + any preceding
	// tool-result message, since prior turns have already
	// contributed their context. Walking from the end backward
	// until we hit an assistant turn.
	paths := extractFilePaths(latestSlice(history))
	if len(paths) == 0 {
		return ""
	}

	var lines []string
	for _, p := range paths {
		if gc.injected[p] {
			continue
		}
		gc.injected[p] = true
		line := gc.summarize(p, protected)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Files in scope (kai graph):\n" + strings.Join(lines, "\n")
}

// summarize renders one line for a single file:
//
//	"- relpath/file.go — called by: a.go, b.go [PROTECTED]"
//
// Empty when the path isn't in the graph (unindexed file, just
// created, etc.) — we only want to surface real signal.
func (gc *graphContextInjector) summarize(path string, protected []string) string {
	callers := gc.callersOf(path)
	importers := gc.importersOf(path)
	merged := mergeUnique(callers, importers)

	parts := []string{"- " + path}
	if len(merged) > 0 {
		const cap = 5
		display := merged
		if len(display) > cap {
			display = append(append([]string{}, merged[:cap]...),
				fmt.Sprintf("… +%d more", len(merged)-cap))
		}
		parts = append(parts, "called by: "+strings.Join(display, ", "))
	} else {
		// No callers in the graph — could be entry point, dead
		// code, or unindexed. Note "no inbound edges" so the
		// model knows changes here have low blast radius.
		parts = append(parts, "no inbound edges")
	}
	if isProtected(path, protected) {
		parts = append(parts, "[PROTECTED]")
	}
	return strings.Join(parts, " — ")
}

func (gc *graphContextInjector) callersOf(path string) []string {
	edges, err := gc.graph.GetEdgesToByPath(path, graph.EdgeCalls)
	if err != nil {
		return nil
	}
	return resolveSrcPaths(gc.graph, edges, path)
}

func (gc *graphContextInjector) importersOf(path string) []string {
	edges, err := gc.graph.GetEdgesToByPath(path, graph.EdgeImports)
	if err != nil {
		return nil
	}
	return resolveSrcPaths(gc.graph, edges, path)
}

func resolveSrcPaths(g *graph.DB, edges []*graph.Edge, exclude string) []string {
	if len(edges) == 0 {
		return nil
	}
	out := make([]string, 0, len(edges))
	seen := make(map[string]bool)
	for _, e := range edges {
		node, err := g.GetNode(e.Src)
		if err != nil || node == nil {
			continue
		}
		p, _ := node.Payload["path"].(string)
		if p == "" || p == exclude || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// isProtected matches a path against the gate's protected glob
// list. Mirrors safetygate's logic but kept local so this helper
// doesn't reach across packages just to surface an annotation.
func isProtected(path string, protected []string) bool {
	for _, pat := range protected {
		if ok, _ := filepath.Match(pat, path); ok {
			return true
		}
		// Approximate "**" recursive glob: stdlib doesn't grok it.
		if strings.HasSuffix(pat, "/**") && strings.HasPrefix(path, strings.TrimSuffix(pat, "/**")+"/") {
			return true
		}
	}
	return false
}

// pathPattern matches workspace-relative file paths embedded in
// prose: at least one path segment + a recognized extension. The
// list is intentionally narrow — picking up every "log.txt" string
// in tool output would inject noise, but missing the actual files
// the conversation is about is worse. Add to extensions on demand.
var pathPattern = regexp.MustCompile(
	`\b[\w./-]+?\.(?:go|js|jsx|ts|tsx|py|rb|java|c|h|cc|cpp|hpp|rs|md|yaml|yml|json|toml|sql|sh|html|css)\b`,
)

// extractFilePaths pulls likely workspace-relative file paths out
// of a slice of messages. Looks at text content and tool_result
// content; ignores tool_use input (already a structured arg slot).
// Filters absolute paths to bare filenames since the graph is
// keyed by workspace-relative form.
func extractFilePaths(msgs []message.Message) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range msgs {
		for _, p := range m.Parts {
			text := ""
			switch v := p.(type) {
			case message.TextContent:
				text = v.Text
			case message.ToolResult:
				text = v.Content
			}
			for _, hit := range pathPattern.FindAllString(text, -1) {
				clean := strings.TrimPrefix(hit, "/")
				if seen[clean] {
					continue
				}
				seen[clean] = true
				out = append(out, clean)
			}
		}
	}
	return out
}

// latestSlice returns history from the most recent user turn back
// to (but excluding) the prior assistant turn. That window is what
// the agent is reasoning about right now — earlier turns have
// already had their graph context injected on previous calls.
func latestSlice(history []message.Message) []message.Message {
	if len(history) == 0 {
		return nil
	}
	end := len(history)
	for i := end - 1; i >= 0; i-- {
		if history[i].Role == message.RoleAssistant {
			return history[i+1 : end]
		}
	}
	return history
}
