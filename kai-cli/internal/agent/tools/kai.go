package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kai/internal/graph"
)

// KaiTools wraps semantic-graph queries as agent tools so the model
// can reason about call structure, dependents, and impact mid-edit
// instead of deferring all of that to the post-run safety gate. This
// is the differentiator vs. vanilla file-editing agents — Kai's loop
// can ask "who calls this function?" before changing it.
//
// The graph DB this constructs against is the *main repo's* DB, not
// the spawn dir's. The agent's writes happen in the spawn dir, but
// "who calls X" is a question about the broader codebase, so we want
// the parent repo's view of the world.
//
// MIGRATION NOTE: these tools duplicate a small amount of logic that
// already lives on `*mcp.Server` in `internal/mcp/server.go`
// (handleCallers, handleDependents, handleImpact, handleContext, plus
// helpers like findCallersViaFileEdges). The plan's "extract pure
// functions to internal/graph and have MCP + agent both call them"
// refactor is intentionally deferred to a follow-up — doing it
// during slice 2 would touch the MCP server while we're already
// reshaping how agents work, and risks regressing the MCP path that
// other clients (Claude Code, Cursor) still use.
//
// The duplication is ~150 lines and is bounded. When the agent path
// is the only one (Slice 6 + something), we'll consolidate.
// KaiGrapher is the subset of *graph.DB the kai_* tools need.
// Defined as an interface so unit tests can substitute a minimal
// in-memory fake instead of spinning up SQLite. *graph.DB satisfies
// it directly.
type KaiGrapher interface {
	GetEdgesToByPath(filePath string, edgeType graph.EdgeType) ([]*graph.Edge, error)
	GetEdgesOfType(edgeType graph.EdgeType) ([]*graph.Edge, error)
	GetEdgesByDst(edgeType graph.EdgeType, dst []byte) ([]*graph.Edge, error)
	GetNode(id []byte) (*graph.Node, error)
	FindNodesByPayloadPath(kind, path string) ([]*graph.Node, error)
}

type KaiTools struct {
	DB KaiGrapher
}

// All returns kai_callers, kai_dependents, kai_context as a slice
// for easy registration in the runner's tool registry.
func (k *KaiTools) All() []BaseTool {
	if k == nil || k.DB == nil {
		return nil
	}
	return []BaseTool{
		&kaiCallersTool{db: k.DB},
		&kaiDependentsTool{db: k.DB},
		&kaiContextTool{db: k.DB},
	}
}

// --- kai_callers -----------------------------------------------------

type kaiCallersTool struct{ db KaiGrapher }

type kaiCallersParams struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
}

func (t *kaiCallersTool) Info() ToolInfo {
	return ToolInfo{
		Name: "kai_callers",
		Description: "Find files and line numbers that call the given symbol (function/method). " +
			"Optionally scope to a file (faster + more accurate when the symbol is common). " +
			"Use this BEFORE editing a function to understand who depends on it.",
		Parameters: map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "Function or method name. Trailing receiver is stripped automatically (e.g. *Resolver.Resolve → Resolve).",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Optional file path to scope the search to (faster).",
			},
		},
		Required: []string{"symbol"},
	}
}

func (t *kaiCallersTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p kaiCallersParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("kai_callers: invalid input json: " + err.Error()), nil
	}
	if strings.TrimSpace(p.Symbol) == "" {
		return NewTextErrorResponse("kai_callers: symbol required"), nil
	}
	target := normalizeSymbolName(p.Symbol)

	var edges []*graph.Edge
	var err error
	if p.File != "" {
		edges, err = t.db.GetEdgesToByPath(p.File, graph.EdgeCalls)
	} else {
		edges, err = t.db.GetEdgesOfType(graph.EdgeCalls)
	}
	if err != nil {
		return NewTextErrorResponse("kai_callers: " + err.Error()), nil
	}

	type hit struct{ file string; line int; callee string }
	var hits []hit
	seen := map[string]bool{}
	for _, e := range edges {
		if len(e.At) == 0 {
			continue
		}
		callNode, err := t.db.GetNode(e.At)
		if err != nil || callNode == nil {
			continue
		}
		callee, _ := callNode.Payload["calleeName"].(string)
		if normalizeSymbolName(callee) != target {
			continue
		}
		caller, _ := callNode.Payload["callerFile"].(string)
		line := 0
		if l, ok := callNode.Payload["line"].(float64); ok {
			line = int(l)
		}
		key := fmt.Sprintf("%s:%d", caller, line)
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, hit{file: caller, line: line, callee: callee})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].file != hits[j].file {
			return hits[i].file < hits[j].file
		}
		return hits[i].line < hits[j].line
	})

	if len(hits) == 0 {
		return NewTextResponse(fmt.Sprintf("kai_callers: no callers of %q found", p.Symbol)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "callers of %s (%d hits):\n", p.Symbol, len(hits))
	for _, h := range hits {
		if h.line > 0 {
			fmt.Fprintf(&b, "  %s:%d  → %s\n", h.file, h.line, h.callee)
		} else {
			fmt.Fprintf(&b, "  %s  → %s\n", h.file, h.callee)
		}
	}
	return NewTextResponse(strings.TrimRight(b.String(), "\n")), nil
}

// --- kai_dependents --------------------------------------------------

type kaiDependentsTool struct{ db KaiGrapher }

type kaiDependentsParams struct {
	File string `json:"file"`
}

func (t *kaiDependentsTool) Info() ToolInfo {
	return ToolInfo{
		Name: "kai_dependents",
		Description: "List files that import or otherwise depend on the given file (depth 1). " +
			"This is the file-level blast-radius — if you change this file, what else might break? " +
			"Use this BEFORE editing a file with broad imports.",
		Parameters: map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Path of the target file relative to the repo root.",
			},
		},
		Required: []string{"file"},
	}
}

func (t *kaiDependentsTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p kaiDependentsParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("kai_dependents: invalid input json: " + err.Error()), nil
	}
	if strings.TrimSpace(p.File) == "" {
		return NewTextErrorResponse("kai_dependents: file required"), nil
	}

	deps, err := dependentsOfFile(t.db, p.File)
	if err != nil {
		return NewTextErrorResponse("kai_dependents: " + err.Error()), nil
	}
	if len(deps) == 0 {
		return NewTextResponse(fmt.Sprintf("kai_dependents: nothing depends on %q (depth 1)", p.File)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dependents of %s (%d):\n", p.File, len(deps))
	for _, d := range deps {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	return NewTextResponse(strings.TrimRight(b.String(), "\n")), nil
}

// --- kai_context -----------------------------------------------------

type kaiContextTool struct{ db KaiGrapher }

type kaiContextParams struct {
	File string `json:"file"`
}

func (t *kaiContextTool) Info() ToolInfo {
	return ToolInfo{
		Name: "kai_context",
		Description: "Summarize what's in a file (top-level symbols) plus depth-1 dependents " +
			"(files that import it). Cheap-but-informative orientation step before editing — " +
			"shorter than `view` for large files.",
		Parameters: map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Path of the target file relative to the repo root.",
			},
		},
		Required: []string{"file"},
	}
}

func (t *kaiContextTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p kaiContextParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("kai_context: invalid input json: " + err.Error()), nil
	}
	if strings.TrimSpace(p.File) == "" {
		return NewTextErrorResponse("kai_context: file required"), nil
	}

	fileNodes, err := t.db.FindNodesByPayloadPath(string(graph.KindFile), p.File)
	if err != nil {
		return NewTextErrorResponse("kai_context: " + err.Error()), nil
	}
	if len(fileNodes) == 0 {
		return NewTextErrorResponse(fmt.Sprintf("kai_context: file not found in graph: %s", p.File)), nil
	}
	fileNode := fileNodes[0]

	// Top-level symbols defined in the file via DEFINES_IN edges
	// pointing at this file from symbol nodes.
	defEdges, err := t.db.GetEdgesByDst(graph.EdgeDefinesIn, fileNode.ID)
	if err != nil {
		return NewTextErrorResponse("kai_context: " + err.Error()), nil
	}
	type sym struct{ name, kind string }
	var syms []sym
	seenSym := map[string]bool{}
	for _, e := range defEdges {
		n, err := t.db.GetNode(e.Src)
		if err != nil || n == nil {
			continue
		}
		name, _ := n.Payload["fqName"].(string)
		kind, _ := n.Payload["kind"].(string)
		if name == "" || seenSym[name] {
			continue
		}
		seenSym[name] = true
		syms = append(syms, sym{name: name, kind: kind})
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].name < syms[j].name })

	deps, _ := dependentsOfFile(t.db, p.File)

	var b strings.Builder
	fmt.Fprintf(&b, "context for %s\n", p.File)
	if len(syms) == 0 {
		b.WriteString("  symbols: (none indexed)\n")
	} else {
		b.WriteString("  symbols:\n")
		for _, s := range syms {
			if s.kind != "" {
				fmt.Fprintf(&b, "    [%s] %s\n", s.kind, s.name)
			} else {
				fmt.Fprintf(&b, "    %s\n", s.name)
			}
		}
	}
	if len(deps) == 0 {
		b.WriteString("  dependents (depth 1): (none)\n")
	} else {
		fmt.Fprintf(&b, "  dependents (depth 1, %d):\n", len(deps))
		for _, d := range deps {
			fmt.Fprintf(&b, "    %s\n", d)
		}
	}
	return NewTextResponse(strings.TrimRight(b.String(), "\n")), nil
}

// --- shared helpers --------------------------------------------------

// dependentsOfFile collects unique paths that have an inbound IMPORTS
// or CALLS edge to the given file. Mirrors the safety gate's
// blast-radius primitive (`safetygate.blastRadius`); when the MCP +
// agent paths are unified (post-Slice 6) this should consolidate
// into one shared helper.
func dependentsOfFile(db KaiGrapher, filePath string) ([]string, error) {
	out := map[string]bool{}
	for _, et := range []graph.EdgeType{graph.EdgeImports, graph.EdgeCalls} {
		edges, err := db.GetEdgesToByPath(filePath, et)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			n, err := db.GetNode(e.Src)
			if err != nil || n == nil {
				continue
			}
			p, _ := n.Payload["path"].(string)
			if p == "" || p == filePath {
				continue
			}
			out[p] = true
		}
	}
	deps := make([]string, 0, len(out))
	for d := range out {
		deps = append(deps, d)
	}
	sort.Strings(deps)
	return deps, nil
}

// normalizeSymbolName strips qualifying prefixes the parser might
// have stored on a CALLS edge's calleeName payload — `Type.Method`
// → `Method`, `crate::foo::bar` → `bar`. Same logic as the MCP
// server's findCallersViaFileEdges; once we extract to a shared
// helper this duplication goes away.
func normalizeSymbolName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	return s
}
