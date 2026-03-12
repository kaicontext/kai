// Package mcp provides an MCP (Model Context Protocol) server that exposes
// Kai's semantic graph to AI coding assistants like Claude Code and Kilo Code.
package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"kai/internal/graph"
	"kai/internal/ref"
	"kai/internal/snapshot"
)

// Server wraps the MCP server with access to the Kai graph database.
type Server struct {
	db       *graph.DB
	resolver *ref.Resolver
	snap     *snapshot.Creator
}

// NewServer creates a new MCP server backed by the given graph database.
func NewServer(db *graph.DB) *Server {
	return &Server{
		db:       db,
		resolver: ref.NewResolver(db),
		snap:     snapshot.NewCreator(db, nil),
	}
}

// Serve starts the MCP server on stdio and blocks until the connection closes.
func (s *Server) Serve(ctx context.Context) error {
	srv := server.NewMCPServer(
		"kai",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	s.registerTools(srv)

	return server.ServeStdio(srv)
}

// readOnly returns tool options marking the tool as read-only, non-destructive, and idempotent.
func readOnly() mcp.ToolOption {
	return mcp.WithToolAnnotation(mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(true),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(false),
	})
}

func (s *Server) registerTools(srv *server.MCPServer) {
	// kai_symbols — list all symbols in a file
	srv.AddTool(
		mcp.NewTool("kai_symbols",
			readOnly(),
			mcp.WithDescription("List symbols defined in a file. Returns names, kinds, and line numbers. For large files, prefer kai_impact/kai_callers/kai_tests over listing all symbols. Use 'kind' to filter (e.g. only functions). Use 'exported=true' for Go to see only public symbols."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root (e.g. src/auth.go)")),
			mcp.WithString("kind", mcp.Description("Filter by symbol kind: function, method, class, variable, interface, struct, type, constant")),
			mcp.WithBoolean("exported", mcp.Description("If true, only return exported/public symbols (Go: uppercase-first)")),
			mcp.WithBoolean("signatures", mcp.Description("If true, include full signatures in output (default: false to save tokens)")),
		),
		s.handleSymbols,
	)

	// kai_callers — find all callers of a symbol
	srv.AddTool(
		mcp.NewTool("kai_callers",
			readOnly(),
			mcp.WithDescription("Find all functions/files that call a given symbol. Walks the CALLS edge in the semantic graph. More accurate than grep — finds indirect callers through imports."),
			mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name to find callers of (e.g. validateToken, Resolve). Use bare function name — receiver prefixes like *Type. are stripped automatically.")),
			mcp.WithString("file", mcp.Description("File where the symbol is defined, to disambiguate (e.g. auth/token.go)")),
		),
		s.handleCallers,
	)

	// kai_callees — find all symbols called by a symbol
	srv.AddTool(
		mcp.NewTool("kai_callees",
			readOnly(),
			mcp.WithDescription("Find all functions/symbols that a given symbol calls. Walks the CALLS edge outward from the symbol."),
			mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name to find callees of")),
			mcp.WithString("file", mcp.Description("File where the symbol is defined, to disambiguate")),
		),
		s.handleCallees,
	)

	// kai_dependents — find files that import/depend on a file
	srv.AddTool(
		mcp.NewTool("kai_dependents",
			readOnly(),
			mcp.WithDescription("Find all files that import or depend on the given file. Answers: 'what breaks if I change this file?'"),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
		),
		s.handleDependents,
	)

	// kai_dependencies — find files that a file imports
	srv.AddTool(
		mcp.NewTool("kai_dependencies",
			readOnly(),
			mcp.WithDescription("Find all files that the given file imports or depends on. Answers: 'what does this file need?'"),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
		),
		s.handleDependencies,
	)

	// kai_tests — find tests that cover a file or symbol
	srv.AddTool(
		mcp.NewTool("kai_tests",
			readOnly(),
			mcp.WithDescription("Find test files that cover the given source file. Uses both static analysis (TESTS edges) and coverage data if available."),
			mcp.WithString("file", mcp.Required(), mcp.Description("Source file path to find tests for")),
		),
		s.handleTests,
	)

	// kai_diff — semantic diff between two refs
	srv.AddTool(
		mcp.NewTool("kai_diff",
			readOnly(),
			mcp.WithDescription("Show semantic differences between two snapshots or git refs. Returns symbol-level changes (added/modified/removed functions, classes, etc.) not just file diffs."),
			mcp.WithString("base", mcp.Description("Base ref (snapshot ID, @snap:prev, or ref name). Defaults to @snap:prev")),
			mcp.WithString("head", mcp.Description("Head ref (snapshot ID, @snap:last, or ref name). Defaults to @snap:last")),
		),
		s.handleDiff,
	)

	// kai_context — bundled context for a location (the high-leverage tool)
	srv.AddTool(
		mcp.NewTool("kai_context",
			readOnly(),
			mcp.WithDescription("Get everything relevant to a file location: the enclosing symbol, its callers, callees, related tests, and file dependencies. One call instead of multiple. Use this when editing code to understand the impact of changes."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
			mcp.WithString("symbol", mcp.Description("Symbol name to focus on (optional, returns all symbols in file if omitted)")),
			mcp.WithNumber("depth", mcp.Description("How many hops to traverse in the graph (default: 1)")),
		),
		s.handleContext,
	)

	// kai_impact — transitive downstream impact analysis
	srv.AddTool(
		mcp.NewTool("kai_impact",
			readOnly(),
			mcp.WithDescription("Analyze the transitive downstream impact of changing a file. Walks the dependency graph to find all files and tests that could be affected, with hop distance."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path to analyze impact for")),
			mcp.WithNumber("max_depth", mcp.Description("Maximum graph traversal depth (default: 3)")),
		),
		s.handleImpact,
	)
}

// latestSnapshotID resolves the most recent snapshot.
func (s *Server) latestSnapshotID() ([]byte, error) {
	kind := ref.KindSnapshot
	result, err := s.resolver.Resolve("@snap:last", &kind)
	if err != nil {
		return nil, fmt.Errorf("no snapshots found — run 'kai capture' first: %w", err)
	}
	return result.ID, nil
}

// resolveSnapshotRef resolves a snapshot reference string, defaulting to @snap:last.
func (s *Server) resolveSnapshotRef(input string) ([]byte, error) {
	if input == "" {
		return s.latestSnapshotID()
	}
	kind := ref.KindSnapshot
	result, err := s.resolver.Resolve(input, &kind)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve ref %q: %w", input, err)
	}
	return result.ID, nil
}

// findFileNodeByPath finds a file node by its path within a snapshot.
func (s *Server) findFileNodeByPath(snapshotID []byte, filePath string) (*graph.Node, error) {
	edges, err := s.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && path == filePath {
			return node, nil
		}
	}
	return nil, fmt.Errorf("file %q not found in latest snapshot", filePath)
}

// findSymbolByName finds a symbol node by name within a file.
func (s *Server) findSymbolByName(snapshotID, fileID []byte, symbolName string) (*graph.Node, error) {
	symbols, err := s.snap.GetSymbolsInFile(fileID, snapshotID)
	if err != nil {
		return nil, err
	}
	for _, sym := range symbols {
		if name, ok := sym.Payload["fqName"].(string); ok {
			// Match on full name or just the last segment
			parts := strings.Split(name, ".")
			if name == symbolName || (len(parts) > 0 && parts[len(parts)-1] == symbolName) {
				return sym, nil
			}
		}
	}
	return nil, fmt.Errorf("symbol %q not found in file", symbolName)
}

// --- Tool Handlers ---

func (s *Server) handleSymbols(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	kindFilter := optString(req, "kind")
	exportedOnly := optBool(req, "exported")
	includeSignatures := optBool(req, "signatures")

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	symbols, err := s.snap.GetSymbolsInFile(fileNode.ID, snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error getting symbols: %v", err)), nil
	}

	type symbolInfo struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Signature string `json:"signature,omitempty"`
		Line      int    `json:"line,omitempty"`
	}

	results := make([]symbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		name, _ := sym.Payload["fqName"].(string)
		kind, _ := sym.Payload["kind"].(string)

		// Apply kind filter
		if kindFilter != "" && !strings.EqualFold(kind, kindFilter) {
			continue
		}

		// Apply exported filter: check if the bare name starts with uppercase
		if exportedOnly {
			bareName := name
			if idx := strings.LastIndex(bareName, "."); idx >= 0 {
				bareName = bareName[idx+1:]
			}
			if bareName == "" || !unicode.IsUpper(rune(bareName[0])) {
				continue
			}
		}

		info := symbolInfo{
			Name: name,
			Kind: kind,
		}
		if includeSignatures {
			if v, ok := sym.Payload["signature"].(string); ok {
				info.Signature = v
			}
		}
		if r, ok := sym.Payload["range"].(map[string]interface{}); ok {
			if line, ok := r["startLine"].(float64); ok {
				info.Line = int(line)
			}
		}
		results = append(results, info)
	}

	return jsonResult(map[string]interface{}{
		"file":    filePath,
		"count":   len(results),
		"symbols": results,
	})
}

func (s *Server) handleCallers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, err := req.RequireString("symbol")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'symbol'"), nil
	}
	filePath := optString(req, "file")

	// CALLS edges are File --CALLS--> File with a Call node as context (at).
	// The Call node payload has "calleeName" which we match against symbolName.
	// To find callers: find CALLS edges where the calleeName matches and
	// optionally the target file matches.

	// First try symbol-level edges (future-proof)
	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callers, err := s.findCallersViaFileEdges(snapID, symbolName, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return jsonResult(map[string]interface{}{
		"symbol":  symbolName,
		"count":   len(callers),
		"callers": callers,
	})
}

func (s *Server) handleCallees(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, err := req.RequireString("symbol")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'symbol'"), nil
	}
	filePath := optString(req, "file")

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callees, err := s.findCalleesViaFileEdges(snapID, symbolName, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return jsonResult(map[string]interface{}{
		"symbol":  symbolName,
		"count":   len(callees),
		"callees": callees,
	})
}

func (s *Server) handleDependents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	// Use GetEdgesToByPath for IMPORTS edges — finds files that import this file
	edges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying dependents: %v", err)), nil
	}

	dependents := make([]string, 0, len(edges))
	seen := make(map[string]bool)
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			dependents = append(dependents, path)
			seen[path] = true
		}
	}
	sort.Strings(dependents)

	return jsonResult(map[string]interface{}{
		"file":       filePath,
		"count":      len(dependents),
		"dependents": dependents,
	})
}

func (s *Server) handleDependencies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Find edges FROM this file (what it imports)
	edges, err := s.db.GetEdges(fileNode.ID, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying dependencies: %v", err)), nil
	}

	deps := make([]string, 0, len(edges))
	seen := make(map[string]bool)
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Dst)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			deps = append(deps, path)
			seen[path] = true
		}
	}
	sort.Strings(deps)

	return jsonResult(map[string]interface{}{
		"file":         filePath,
		"count":        len(deps),
		"dependencies": deps,
	})
}

func (s *Server) handleTests(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	// Find test files via TESTS edges
	testEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeTests)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying tests: %v", err)), nil
	}

	// Also find test files that import this file
	importEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying import edges: %v", err)), nil
	}

	tests := make([]string, 0)
	seen := make(map[string]bool)

	// Direct TESTS edges
	for _, edge := range testEdges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			tests = append(tests, path)
			seen[path] = true
		}
	}

	// Files that import this file and look like tests
	for _, edge := range importEdges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] && isTestFile(path) {
			tests = append(tests, path)
			seen[path] = true
		}
	}

	sort.Strings(tests)

	return jsonResult(map[string]interface{}{
		"file":  filePath,
		"count": len(tests),
		"tests": tests,
	})
}

func (s *Server) handleDiff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseRef := optString(req, "base")
	headRef := optString(req, "head")

	if baseRef == "" {
		baseRef = "@snap:prev"
	}
	if headRef == "" {
		headRef = "@snap:last"
	}

	baseID, err := s.resolveSnapshotRef(baseRef)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("cannot resolve base ref: %v", err)), nil
	}
	headID, err := s.resolveSnapshotRef(headRef)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("cannot resolve head ref: %v", err)), nil
	}

	// Get files from both snapshots
	baseFiles, err := s.snapshotFiles(baseID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error reading base snapshot: %v", err)), nil
	}
	headFiles, err := s.snapshotFiles(headID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error reading head snapshot: %v", err)), nil
	}

	type fileDelta struct {
		Path   string `json:"path"`
		Status string `json:"status"` // added, removed, modified
	}

	var deltas []fileDelta

	// Find added and modified files
	for path, headDigest := range headFiles {
		baseDigest, exists := baseFiles[path]
		if !exists {
			deltas = append(deltas, fileDelta{Path: path, Status: "added"})
		} else if headDigest != baseDigest {
			deltas = append(deltas, fileDelta{Path: path, Status: "modified"})
		}
	}

	// Find removed files
	for path := range baseFiles {
		if _, exists := headFiles[path]; !exists {
			deltas = append(deltas, fileDelta{Path: path, Status: "removed"})
		}
	}

	sort.Slice(deltas, func(i, j int) bool { return deltas[i].Path < deltas[j].Path })

	return jsonResult(map[string]interface{}{
		"base":    hex.EncodeToString(baseID)[:12],
		"head":    hex.EncodeToString(headID)[:12],
		"count":   len(deltas),
		"changes": deltas,
	})
}

func (s *Server) handleContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}
	symbolName := optString(req, "symbol")
	depth := int(optFloat(req, "depth", 1))
	_ = depth // reserved for future multi-hop traversal

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]interface{}{
		"file": filePath,
	}

	// Get all symbols in the file
	symbols, err := s.snap.GetSymbolsInFile(fileNode.ID, snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error getting symbols: %v", err)), nil
	}

	type symbolSummary struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Signature string `json:"signature,omitempty"`
		Line      int    `json:"line,omitempty"`
	}

	symSummaries := make([]symbolSummary, 0, len(symbols))
	for _, sym := range symbols {
		info := symbolSummary{}
		if v, ok := sym.Payload["fqName"].(string); ok {
			info.Name = v
		}
		if v, ok := sym.Payload["kind"].(string); ok {
			info.Kind = v
		}
		if v, ok := sym.Payload["signature"].(string); ok {
			info.Signature = v
		}
		if r, ok := sym.Payload["range"].(map[string]interface{}); ok {
			if line, ok := r["startLine"].(float64); ok {
				info.Line = int(line)
			}
		}
		symSummaries = append(symSummaries, info)
	}
	result["symbols"] = symSummaries

	// If a specific symbol is requested, get its callers and callees
	if symbolName != "" {
		symNode, err := s.findSymbolByName(snapID, fileNode.ID, symbolName)
		if err == nil {
			// Callers
			callerEdges, err := s.db.GetEdgesTo(symNode.ID, graph.EdgeCalls)
			if err == nil {
				callers, _ := s.edgesToSymbolLocations(callerEdges, true)
				result["callers"] = callers
			}

			// Callees
			calleeEdges, err := s.db.GetEdges(symNode.ID, graph.EdgeCalls)
			if err == nil {
				callees, _ := s.edgesToSymbolLocations(calleeEdges, false)
				result["callees"] = callees
			}
		}
		result["focus_symbol"] = symbolName
	}

	// Dependencies (what this file imports)
	importEdges, err := s.db.GetEdges(fileNode.ID, graph.EdgeImports)
	if err == nil {
		deps := make([]string, 0)
		for _, edge := range importEdges {
			node, err := s.db.GetNode(edge.Dst)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok {
				deps = append(deps, path)
			}
		}
		sort.Strings(deps)
		result["dependencies"] = deps
	}

	// Dependents (what imports this file)
	depEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err == nil {
		dependents := make([]string, 0)
		seen := make(map[string]bool)
		for _, edge := range depEdges {
			node, err := s.db.GetNode(edge.Src)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok && !seen[path] {
				dependents = append(dependents, path)
				seen[path] = true
			}
		}
		sort.Strings(dependents)
		result["dependents"] = dependents
	}

	// Tests
	testEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeTests)
	if err == nil {
		tests := make([]string, 0)
		seen := make(map[string]bool)
		for _, edge := range testEdges {
			node, err := s.db.GetNode(edge.Src)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok && !seen[path] {
				tests = append(tests, path)
				seen[path] = true
			}
		}
		sort.Strings(tests)
		result["tests"] = tests
	}

	return jsonResult(result)
}

func (s *Server) handleImpact(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}
	maxDepth := int(optFloat(req, "max_depth", 3))

	type impactEntry struct {
		Path   string `json:"path"`
		Hop    int    `json:"hop"`
		IsTest bool   `json:"is_test"`
	}

	visited := make(map[string]int) // path -> hop distance
	visited[filePath] = 0
	frontier := []string{filePath}
	var results []impactEntry

	for hop := 1; hop <= maxDepth && len(frontier) > 0; hop++ {
		var nextFrontier []string
		for _, current := range frontier {
			// Find files that import the current file
			edges, err := s.db.GetEdgesToByPath(current, graph.EdgeImports)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				node, err := s.db.GetNode(edge.Src)
				if err != nil || node == nil {
					continue
				}
				path, ok := node.Payload["path"].(string)
				if !ok {
					continue
				}
				if _, already := visited[path]; already {
					continue
				}
				visited[path] = hop
				entry := impactEntry{Path: path, Hop: hop, IsTest: isTestFile(path)}
				results = append(results, entry)
				nextFrontier = append(nextFrontier, path)
			}

			// Also follow CALLS edges at file level
			callEdges, err := s.db.GetEdgesToByPath(current, graph.EdgeCalls)
			if err != nil {
				continue
			}
			for _, edge := range callEdges {
				node, err := s.db.GetNode(edge.Src)
				if err != nil || node == nil {
					continue
				}
				path, ok := node.Payload["path"].(string)
				if !ok {
					continue
				}
				if _, already := visited[path]; already {
					continue
				}
				visited[path] = hop
				entry := impactEntry{Path: path, Hop: hop, IsTest: isTestFile(path)}
				results = append(results, entry)
				nextFrontier = append(nextFrontier, path)
			}
		}
		frontier = nextFrontier
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Hop != results[j].Hop {
			return results[i].Hop < results[j].Hop
		}
		return results[i].Path < results[j].Path
	})

	// Separate tests from source files
	var testFiles, sourceFiles []impactEntry
	for _, r := range results {
		if r.IsTest {
			testFiles = append(testFiles, r)
		} else {
			sourceFiles = append(sourceFiles, r)
		}
	}

	return jsonResult(map[string]interface{}{
		"file":           filePath,
		"max_depth":      maxDepth,
		"affected_files": sourceFiles,
		"affected_tests": testFiles,
		"total_affected": len(results),
	})
}

// --- Call Graph Helpers ---

type callInfo struct {
	CallerFile string `json:"caller_file,omitempty"`
	CalleeFile string `json:"callee_file,omitempty"`
	CalleeName string `json:"callee_name"`
	Line       int    `json:"line,omitempty"`
}

// findCallersViaFileEdges finds files that call the given symbol by scanning
// CALLS edges and matching the call node's calleeName payload.
func (s *Server) findCallersViaFileEdges(snapID []byte, symbolName, filePath string) ([]callInfo, error) {
	// Normalize Go receiver-qualified names: *Resolver.Resolve → Resolve, Type.Method → Method
	if idx := strings.LastIndex(symbolName, "."); idx >= 0 {
		symbolName = symbolName[idx+1:]
	}

	// If file specified, find the file node and get edges TO it
	// Then filter by calleeName matching symbolName
	var edges []*graph.Edge
	var err error

	if filePath != "" {
		edges, err = s.db.GetEdgesToByPath(filePath, graph.EdgeCalls)
	} else {
		// No file specified — scan all CALLS edges (expensive but correct)
		edges, err = s.db.GetEdgesOfType(graph.EdgeCalls)
	}
	if err != nil {
		return nil, err
	}

	var results []callInfo
	seen := make(map[string]bool)

	for _, edge := range edges {
		// The "at" field is a Call node with payload {calleeName, callerFile, calleeFile, line}
		if len(edge.At) == 0 {
			continue
		}
		callNode, err := s.db.GetNode(edge.At)
		if err != nil || callNode == nil {
			continue
		}
		calleeName, _ := callNode.Payload["calleeName"].(string)
		if calleeName != symbolName {
			continue
		}

		callerFile, _ := callNode.Payload["callerFile"].(string)
		line := 0
		if l, ok := callNode.Payload["line"].(float64); ok {
			line = int(l)
		}

		key := fmt.Sprintf("%s:%d", callerFile, line)
		if seen[key] {
			continue
		}
		seen[key] = true

		results = append(results, callInfo{
			CallerFile: callerFile,
			CalleeName: calleeName,
			Line:       line,
		})
	}

	return results, nil
}

// findCalleesViaFileEdges finds symbols called from a file containing the given symbol.
func (s *Server) findCalleesViaFileEdges(snapID []byte, symbolName, filePath string) ([]callInfo, error) {
	if filePath == "" {
		// Need a file to find outgoing calls from
		node, err := s.findSymbolInGraph(snapID, symbolName, "")
		if err != nil {
			return nil, err
		}
		if fileIDStr, ok := node.Payload["fileId"].(string); ok {
			fileID, err := hex.DecodeString(fileIDStr)
			if err == nil {
				fileNode, err := s.db.GetNode(fileID)
				if err == nil && fileNode != nil {
					filePath, _ = fileNode.Payload["path"].(string)
				}
			}
		}
		if filePath == "" {
			return nil, fmt.Errorf("cannot determine file for symbol %q", symbolName)
		}
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return nil, err
	}

	edges, err := s.db.GetEdges(fileNode.ID, graph.EdgeCalls)
	if err != nil {
		return nil, err
	}

	var results []callInfo
	seen := make(map[string]bool)

	for _, edge := range edges {
		if len(edge.At) == 0 {
			continue
		}
		callNode, err := s.db.GetNode(edge.At)
		if err != nil || callNode == nil {
			continue
		}
		calleeName, _ := callNode.Payload["calleeName"].(string)
		calleeFile, _ := callNode.Payload["calleeFile"].(string)
		line := 0
		if l, ok := callNode.Payload["line"].(float64); ok {
			line = int(l)
		}

		key := fmt.Sprintf("%s:%s", calleeFile, calleeName)
		if seen[key] {
			continue
		}
		seen[key] = true

		results = append(results, callInfo{
			CalleeFile: calleeFile,
			CalleeName: calleeName,
			Line:       line,
		})
	}

	return results, nil
}

// --- Helpers ---

// findSymbolInGraph finds a symbol by name, optionally scoped to a file.
func (s *Server) findSymbolInGraph(snapID []byte, symbolName, filePath string) (*graph.Node, error) {
	if filePath != "" {
		fileNode, err := s.findFileNodeByPath(snapID, filePath)
		if err != nil {
			return nil, err
		}
		return s.findSymbolByName(snapID, fileNode.ID, symbolName)
	}

	// No file specified — scan all files in snapshot for the symbol
	edges, err := s.db.GetEdges(snapID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		sym, err := s.findSymbolByName(snapID, edge.Dst, symbolName)
		if err == nil {
			return sym, nil
		}
	}
	return nil, fmt.Errorf("symbol %q not found in any file", symbolName)
}

type symbolLocation struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// edgesToSymbolLocations resolves edge src/dst nodes to symbol locations.
// If useSrc is true, resolves edge.Src (for callers); otherwise edge.Dst (for callees).
func (s *Server) edgesToSymbolLocations(edges []*graph.Edge, useSrc bool) ([]symbolLocation, error) {
	results := make([]symbolLocation, 0, len(edges))
	seen := make(map[string]bool)

	for _, edge := range edges {
		nodeID := edge.Dst
		if useSrc {
			nodeID = edge.Src
		}
		idHex := hex.EncodeToString(nodeID)
		if seen[idHex] {
			continue
		}
		seen[idHex] = true

		node, err := s.db.GetNode(nodeID)
		if err != nil || node == nil {
			continue
		}

		loc := symbolLocation{}
		if v, ok := node.Payload["fqName"].(string); ok {
			loc.Name = v
		}
		if v, ok := node.Payload["kind"].(string); ok {
			loc.Kind = v
		}
		if v, ok := node.Payload["signature"].(string); ok {
			loc.Signature = v
		}

		// Resolve file path from the symbol's fileId
		if fileIDStr, ok := node.Payload["fileId"].(string); ok {
			fileID, err := hex.DecodeString(fileIDStr)
			if err == nil {
				fileNode, err := s.db.GetNode(fileID)
				if err == nil && fileNode != nil {
					if path, ok := fileNode.Payload["path"].(string); ok {
						loc.File = path
					}
				}
			}
		}

		if r, ok := node.Payload["range"].(map[string]interface{}); ok {
			if line, ok := r["startLine"].(float64); ok {
				loc.Line = int(line)
			}
		}

		results = append(results, loc)
	}

	return results, nil
}

// snapshotFiles returns a map of file path -> content digest for a snapshot.
func (s *Server) snapshotFiles(snapshotID []byte) (map[string]string, error) {
	node, err := s.db.GetNode(snapshotID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("snapshot not found")
	}

	files := make(map[string]string)

	// Try inline file list first (fast path)
	if fileList, ok := node.Payload["files"].([]interface{}); ok {
		for _, f := range fileList {
			if fm, ok := f.(map[string]interface{}); ok {
				path, _ := fm["path"].(string)
				digest, _ := fm["contentDigest"].(string)
				if path != "" {
					files[path] = digest
				}
			}
		}
		return files, nil
	}

	// Fall back to edge traversal
	edges, err := s.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		fileNode, err := s.db.GetNode(edge.Dst)
		if err != nil || fileNode == nil {
			continue
		}
		path, _ := fileNode.Payload["path"].(string)
		digest, _ := fileNode.Payload["contentDigest"].(string)
		if path != "" {
			files[path] = digest
		}
	}
	return files, nil
}

// isTestFile returns true if the file path looks like a test file.
func isTestFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "_test.") ||
		strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.") ||
		strings.Contains(lower, "test_") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "test/") ||
		strings.Contains(lower, "__tests__/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/test/")
}

// jsonResult marshals data to a JSON text result.
func jsonResult(data interface{}) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error marshaling result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// optString returns an optional string argument, or "" if not present.
func optString(req mcp.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

// optBool returns an optional boolean argument, or false if not present.
func optBool(req mcp.CallToolRequest, key string) bool {
	return req.GetBool(key, false)
}

// optFloat returns an optional float argument, or the default if not present.
func optFloat(req mcp.CallToolRequest, key string, def float64) float64 {
	return req.GetFloat(key, def)
}
