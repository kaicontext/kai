package tools

import (
	"context"
	"strings"
	"testing"

	"kai/internal/graph"
)

// fakeKaiGraph is an in-memory KaiGrapher. Just enough surface to
// test the three kai_* tools. Edges are stored verbatim by edge type;
// nodes are looked up by path or by the byte-id we assign on insert.
type fakeKaiGraph struct {
	// callsByCallee: when a tool calls GetEdgesOfType(EdgeCalls), we
	// return all edges. Each edge's `At` field points at a "call
	// node" whose payload has calleeName/callerFile/line.
	callEdges []*graph.Edge

	// importsToFile: GetEdgesToByPath(file, EdgeImports) returns
	// edges whose Src is a file that imports `file`.
	importsToFile map[string][]*graph.Edge

	// callsToFile: GetEdgesToByPath(file, EdgeCalls).
	callsToFile map[string][]*graph.Edge

	// definesInToFile: GetEdgesByDst(EdgeDefinesIn, fileNodeID).
	// keyed by hex(fileNodeID).
	definesInByFile map[string][]*graph.Edge

	// nodes: id-keyed (we use path bytes as id for files; arbitrary
	// strings for call/symbol nodes).
	nodes map[string]*graph.Node

	// fileByPath: FindNodesByPayloadPath("File", path).
	fileByPath map[string]*graph.Node
}

func newFakeKaiGraph() *fakeKaiGraph {
	return &fakeKaiGraph{
		importsToFile:   map[string][]*graph.Edge{},
		callsToFile:     map[string][]*graph.Edge{},
		definesInByFile: map[string][]*graph.Edge{},
		nodes:           map[string]*graph.Node{},
		fileByPath:      map[string]*graph.Node{},
	}
}

func (f *fakeKaiGraph) addFile(path string) *graph.Node {
	id := []byte("file:" + path)
	n := &graph.Node{
		ID:      id,
		Kind:    graph.KindFile,
		Payload: map[string]interface{}{"path": path},
	}
	f.nodes[string(id)] = n
	f.fileByPath[path] = n
	return n
}

func (f *fakeKaiGraph) addSymbol(name, kind string, file *graph.Node) {
	symID := []byte("sym:" + name)
	f.nodes[string(symID)] = &graph.Node{
		ID:      symID,
		Kind:    graph.KindSymbol,
		Payload: map[string]interface{}{"fqName": name, "kind": kind},
	}
	edge := &graph.Edge{Src: symID, Dst: file.ID}
	key := string(file.ID)
	f.definesInByFile[key] = append(f.definesInByFile[key], edge)
}

// addCall: register a CALLS edge from caller-file to callee-file with
// a Call node payload describing the call site.
func (f *fakeKaiGraph) addCall(callerFile, calleeFile, calleeName string, line int) {
	callNodeID := []byte("call:" + callerFile + "->" + calleeName + ":" +
		string(rune(line)))
	f.nodes[string(callNodeID)] = &graph.Node{
		ID:   callNodeID,
		Kind: "Call",
		Payload: map[string]interface{}{
			"calleeName": calleeName,
			"callerFile": callerFile,
			"calleeFile": calleeFile,
			"line":       float64(line),
		},
	}
	edge := &graph.Edge{
		Src: []byte("file:" + callerFile),
		Dst: []byte("file:" + calleeFile),
		At:  callNodeID,
	}
	f.callEdges = append(f.callEdges, edge)
	f.callsToFile[calleeFile] = append(f.callsToFile[calleeFile], edge)
}

// addImport: file -> file IMPORTS edge.
func (f *fakeKaiGraph) addImport(importerFile, importedFile string) {
	edge := &graph.Edge{
		Src: []byte("file:" + importerFile),
		Dst: []byte("file:" + importedFile),
	}
	f.importsToFile[importedFile] = append(f.importsToFile[importedFile], edge)
}

// --- KaiGrapher implementation ---------------------------------------

func (f *fakeKaiGraph) GetEdgesToByPath(file string, et graph.EdgeType) ([]*graph.Edge, error) {
	switch et {
	case graph.EdgeImports:
		return f.importsToFile[file], nil
	case graph.EdgeCalls:
		return f.callsToFile[file], nil
	}
	return nil, nil
}

func (f *fakeKaiGraph) GetEdgesOfType(et graph.EdgeType) ([]*graph.Edge, error) {
	if et == graph.EdgeCalls {
		return f.callEdges, nil
	}
	return nil, nil
}

func (f *fakeKaiGraph) GetEdgesByDst(et graph.EdgeType, dst []byte) ([]*graph.Edge, error) {
	if et == graph.EdgeDefinesIn {
		return f.definesInByFile[string(dst)], nil
	}
	return nil, nil
}

func (f *fakeKaiGraph) GetNode(id []byte) (*graph.Node, error) {
	return f.nodes[string(id)], nil
}

func (f *fakeKaiGraph) FindNodesByPayloadPath(kind, path string) ([]*graph.Node, error) {
	if kind != string(graph.KindFile) {
		return nil, nil
	}
	if n, ok := f.fileByPath[path]; ok {
		return []*graph.Node{n}, nil
	}
	return nil, nil
}

// --- tests -----------------------------------------------------------

func TestKaiCallers_FindsMatches(t *testing.T) {
	g := newFakeKaiGraph()
	g.addFile("router.go")
	g.addFile("api/server.go")
	g.addFile("api/health.go")
	g.addCall("api/server.go", "router.go", "Register", 42)
	g.addCall("api/health.go", "router.go", "Register", 17)
	// Unrelated call shouldn't show up:
	g.addCall("api/server.go", "router.go", "Other", 50)

	tool := (&KaiTools{DB: g}).All()[0] // kai_callers is first
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_callers",
		Input: `{"symbol":"Register"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	for _, want := range []string{"api/server.go:42", "api/health.go:17", "Register"} {
		if !strings.Contains(resp.Content, want) {
			t.Errorf("missing %q in output:\n%s", want, resp.Content)
		}
	}
	if strings.Contains(resp.Content, "Other") {
		t.Errorf("output should not include unrelated callee: %s", resp.Content)
	}
}

func TestKaiCallers_NoMatches(t *testing.T) {
	g := newFakeKaiGraph()
	tool := (&KaiTools{DB: g}).All()[0]
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_callers",
		Input: `{"symbol":"Nonexistent"}`,
	})
	if !strings.Contains(resp.Content, "no callers") {
		t.Errorf("expected 'no callers' message, got: %s", resp.Content)
	}
}

func TestKaiCallers_NormalizesQualifiedNames(t *testing.T) {
	// CalleeName stored with scope prefix; tool should still match
	// when the agent asks by short name.
	g := newFakeKaiGraph()
	g.addFile("a.go")
	g.addFile("b.go")
	g.addCall("a.go", "b.go", "Resolver::resolve", 10) // Rust-style
	g.addCall("a.go", "b.go", "Type.Method", 20)       // Go-style

	tool := (&KaiTools{DB: g}).All()[0]
	for _, q := range []string{"resolve", "Method"} {
		resp, _ := tool.Run(context.Background(), ToolCall{
			Name:  "kai_callers",
			Input: `{"symbol":"` + q + `"}`,
		})
		if resp.IsError || !strings.Contains(resp.Content, "a.go") {
			t.Errorf("query %q: unexpected output: %s", q, resp.Content)
		}
	}
}

func TestKaiDependents_ReportsImporters(t *testing.T) {
	g := newFakeKaiGraph()
	g.addFile("util.go")
	g.addFile("api/a.go")
	g.addFile("api/b.go")
	g.addImport("api/a.go", "util.go")
	g.addImport("api/b.go", "util.go")

	tool := (&KaiTools{DB: g}).All()[1] // kai_dependents is second
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_dependents",
		Input: `{"file":"util.go"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	for _, want := range []string{"api/a.go", "api/b.go"} {
		if !strings.Contains(resp.Content, want) {
			t.Errorf("missing %q in output:\n%s", want, resp.Content)
		}
	}
}

func TestKaiDependents_NoneFound(t *testing.T) {
	g := newFakeKaiGraph()
	g.addFile("isolated.go")
	tool := (&KaiTools{DB: g}).All()[1]
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_dependents",
		Input: `{"file":"isolated.go"}`,
	})
	if !strings.Contains(resp.Content, "nothing depends") {
		t.Errorf("expected 'nothing depends' message, got: %s", resp.Content)
	}
}

func TestKaiContext_ReportsSymbolsAndDependents(t *testing.T) {
	g := newFakeKaiGraph()
	router := g.addFile("router.go")
	g.addFile("api/server.go")
	g.addSymbol("Register", "function", router)
	g.addSymbol("Mux", "type", router)
	g.addImport("api/server.go", "router.go")

	tool := (&KaiTools{DB: g}).All()[2] // kai_context is third
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_context",
		Input: `{"file":"router.go"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	for _, want := range []string{"Register", "Mux", "[function]", "[type]", "api/server.go", "depth 1"} {
		if !strings.Contains(resp.Content, want) {
			t.Errorf("missing %q in output:\n%s", want, resp.Content)
		}
	}
}

func TestKaiContext_FileNotFound(t *testing.T) {
	g := newFakeKaiGraph()
	tool := (&KaiTools{DB: g}).All()[2]
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "kai_context",
		Input: `{"file":"missing.go"}`,
	})
	if !resp.IsError || !strings.Contains(resp.Content, "not found") {
		t.Errorf("expected not-found error, got: %+v", resp)
	}
}

// TestKaiTools_NilDBReturnsNoTools confirms that a nil graph results
// in no tools registered — the runner uses this to skip kai_* when
// the orchestrator didn't pass a graph.
func TestKaiTools_NilDBReturnsNoTools(t *testing.T) {
	if got := (&KaiTools{DB: nil}).All(); got != nil {
		t.Errorf("nil DB should produce nil tool slice, got %v", got)
	}
}

func TestKaiTools_AllReturnsThreeTools(t *testing.T) {
	g := newFakeKaiGraph()
	tools := (&KaiTools{DB: g}).All()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	names := []string{tools[0].Info().Name, tools[1].Info().Name, tools[2].Info().Name}
	for _, want := range []string{"kai_callers", "kai_dependents", "kai_context"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing tool %q in registry: %v", want, names)
		}
	}
}

func TestNormalizeSymbolName(t *testing.T) {
	cases := map[string]string{
		"Foo":               "Foo",
		"Type.Method":       "Method",
		"*Resolver.Resolve": "Resolve",
		"crate::foo::bar":   "bar",
		"Module::Class.fn":  "fn",
	}
	for in, want := range cases {
		if got := normalizeSymbolName(in); got != want {
			t.Errorf("normalizeSymbolName(%q): got %q, want %q", in, got, want)
		}
	}
}
