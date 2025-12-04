// Package parse provides Tree-sitter based parsing for TypeScript, JavaScript, and Python.
package parse

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
)

// Range represents a source code range (0-based line and column).
type Range struct {
	Start [2]int `json:"start"` // [line, col]
	End   [2]int `json:"end"`   // [line, col]
}

// Symbol represents an extracted symbol from source code.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "function", "class", "variable"
	Range     Range  `json:"range"`
	Signature string `json:"signature"`
}

// ParsedFile contains the parsed AST and extracted symbols.
type ParsedFile struct {
	Tree    *sitter.Tree
	Content []byte
	Symbols []*Symbol
}

// Parser wraps the Tree-sitter parser with multi-language support.
type Parser struct {
	jsParser *sitter.Parser
	pyParser *sitter.Parser
}

// NewParser creates a new parser with support for JavaScript/TypeScript and Python.
func NewParser() *Parser {
	jsParser := sitter.NewParser()
	jsParser.SetLanguage(javascript.GetLanguage())

	pyParser := sitter.NewParser()
	pyParser.SetLanguage(python.GetLanguage())

	return &Parser{
		jsParser: jsParser,
		pyParser: pyParser,
	}
}

// Parse parses source code and extracts symbols based on language.
func (p *Parser) Parse(content []byte, lang string) (*ParsedFile, error) {
	var parser *sitter.Parser
	var extractFn func(*sitter.Node, []byte) []*Symbol

	switch lang {
	case "py", "python":
		parser = p.pyParser
		extractFn = extractPythonSymbols
	case "js", "ts", "javascript", "typescript":
		parser = p.jsParser
		extractFn = extractSymbols
	default:
		// Default to JavaScript parser for unknown languages
		parser = p.jsParser
		extractFn = extractSymbols
	}

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parsing failed: %w", err)
	}

	symbols := extractFn(tree.RootNode(), content)

	return &ParsedFile{
		Tree:    tree,
		Content: content,
		Symbols: symbols,
	}, nil
}

// extractSymbols walks the AST and extracts function, class, and variable declarations.
func extractSymbols(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil {
			break
		}
		if n == nil {
			break
		}

		switch n.Type() {
		case "function_declaration", "function":
			sym := extractFunctionSymbol(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "class_declaration":
			sym := extractClassSymbol(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
			// Also extract methods within the class
			methods := extractMethodsFromClass(n, content)
			symbols = append(symbols, methods...)
		case "lexical_declaration", "variable_declaration":
			syms := extractVariableSymbols(n, content)
			symbols = append(symbols, syms...)
		case "arrow_function":
			// Arrow functions assigned to variables are handled in variable declarations
		case "export_statement":
			// Export statements are handled for API surface detection
		case "method_definition":
			// Methods inside classes - handled by extractMethodsFromClass
		}
	}

	return symbols
}

func extractFunctionSymbol(node *sitter.Node, content []byte) *Symbol {
	// Find the function name
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name = child.Content(content)
			break
		}
	}

	if name == "" {
		return nil
	}

	// Build signature from parameters
	signature := buildFunctionSignature(node, content)

	return &Symbol{
		Name:      name,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractClassSymbol(node *sitter.Node, content []byte) *Symbol {
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name = child.Content(content)
			break
		}
	}

	if name == "" {
		return nil
	}

	return &Symbol{
		Name:      name,
		Kind:      "class",
		Range:     nodeRange(node),
		Signature: fmt.Sprintf("class %s", name),
	}
}

func extractMethodsFromClass(classNode *sitter.Node, content []byte) []*Symbol {
	var methods []*Symbol

	// Find class_body
	var classBody *sitter.Node
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "class_body" {
			classBody = child
			break
		}
	}

	if classBody == nil {
		return methods
	}

	// Find class name
	var className string
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "identifier" {
			className = child.Content(content)
			break
		}
	}

	// Find method definitions
	for i := 0; i < int(classBody.ChildCount()); i++ {
		child := classBody.Child(i)
		if child.Type() == "method_definition" {
			sym := extractMethodSymbol(child, content, className)
			if sym != nil {
				methods = append(methods, sym)
			}
		}
	}

	return methods
}

func extractMethodSymbol(node *sitter.Node, content []byte, className string) *Symbol {
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "property_identifier" {
			name = child.Content(content)
			break
		}
	}

	if name == "" {
		return nil
	}

	signature := buildFunctionSignature(node, content)

	fullName := name
	if className != "" {
		fullName = className + "." + name
	}

	return &Symbol{
		Name:      fullName,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractVariableSymbols(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	// Find variable_declarator children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			sym := extractVariableDeclarator(child, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		}
	}

	return symbols
}

func extractVariableDeclarator(node *sitter.Node, content []byte) *Symbol {
	var name string
	var kind = "variable"
	var signature string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name = child.Content(content)
		}
		// Check if it's an arrow function or function expression
		if child.Type() == "arrow_function" || child.Type() == "function" {
			kind = "function"
			signature = buildFunctionSignature(child, content)
		}
	}

	if name == "" {
		return nil
	}

	if signature == "" {
		signature = fmt.Sprintf("const %s", name)
	}

	return &Symbol{
		Name:      name,
		Kind:      kind,
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func buildFunctionSignature(node *sitter.Node, content []byte) string {
	// Find function name or method name
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" || child.Type() == "property_identifier" {
			name = child.Content(content)
			break
		}
	}

	// Find formal_parameters
	var params string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "formal_parameters" {
			params = child.Content(content)
			break
		}
	}

	if name != "" {
		return fmt.Sprintf("function %s%s", name, params)
	}
	return fmt.Sprintf("(%s) => ...", params)
}

func nodeRange(node *sitter.Node) Range {
	startPoint := node.StartPoint()
	endPoint := node.EndPoint()

	return Range{
		Start: [2]int{int(startPoint.Row), int(startPoint.Column)},
		End:   [2]int{int(endPoint.Row), int(endPoint.Column)},
	}
}

// GetTree returns the underlying sitter.Tree for advanced analysis.
func (pf *ParsedFile) GetTree() *sitter.Tree {
	return pf.Tree
}

// GetRootNode returns the root node of the AST.
func (pf *ParsedFile) GetRootNode() *sitter.Node {
	return pf.Tree.RootNode()
}

// FindNodesOfType finds all nodes of a specific type in the AST.
func (pf *ParsedFile) FindNodesOfType(nodeType string) []*sitter.Node {
	var nodes []*sitter.Node
	iter := sitter.NewIterator(pf.Tree.RootNode(), sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() == nodeType {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// GetNodeRange returns the Range for a sitter.Node.
func GetNodeRange(node *sitter.Node) Range {
	return nodeRange(node)
}

// GetNodeContent returns the text content of a node.
func GetNodeContent(node *sitter.Node, content []byte) string {
	return node.Content(content)
}

// RangesOverlap checks if two ranges overlap.
func RangesOverlap(r1, r2 Range) bool {
	// Check if r1 ends before r2 starts or r2 ends before r1 starts
	if r1.End[0] < r2.Start[0] || (r1.End[0] == r2.Start[0] && r1.End[1] < r2.Start[1]) {
		return false
	}
	if r2.End[0] < r1.Start[0] || (r2.End[0] == r1.Start[0] && r2.End[1] < r1.Start[1]) {
		return false
	}
	return true
}

// ==================== Python Symbol Extraction ====================

// extractPythonSymbols walks the Python AST and extracts function, class, and variable declarations.
func extractPythonSymbols(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil {
			break
		}
		if n == nil {
			break
		}

		switch n.Type() {
		case "function_definition":
			sym := extractPythonFunction(n, content, "")
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "class_definition":
			sym := extractPythonClass(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
			// Also extract methods within the class
			methods := extractPythonMethods(n, content)
			symbols = append(symbols, methods...)
		case "assignment":
			// Top-level assignments (module-level variables)
			// In Python, assignments are wrapped in expression_statement within module
			parent := n.Parent()
			if parent != nil {
				grandparent := parent.Parent()
				if parent.Type() == "expression_statement" && grandparent != nil && grandparent.Type() == "module" {
					syms := extractPythonAssignment(n, content)
					symbols = append(symbols, syms...)
				}
			}
		}
	}

	return symbols
}

func extractPythonFunction(node *sitter.Node, content []byte, className string) *Symbol {
	var name string
	var params string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "parameters":
			params = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	fullName := name
	if className != "" {
		fullName = className + "." + name
	}

	return &Symbol{
		Name:      fullName,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: fmt.Sprintf("def %s%s", name, params),
	}
}

func extractPythonClass(node *sitter.Node, content []byte) *Symbol {
	var name string
	var bases string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "argument_list":
			bases = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	signature := "class " + name
	if bases != "" {
		signature += bases
	}

	return &Symbol{
		Name:      name,
		Kind:      "class",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractPythonMethods(classNode *sitter.Node, content []byte) []*Symbol {
	var methods []*Symbol

	// Find class name
	var className string
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "identifier" {
			className = child.Content(content)
			break
		}
	}

	// Find block (class body)
	var classBody *sitter.Node
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "block" {
			classBody = child
			break
		}
	}

	if classBody == nil {
		return methods
	}

	// Find function definitions inside the block
	for i := 0; i < int(classBody.ChildCount()); i++ {
		child := classBody.Child(i)
		if child.Type() == "function_definition" {
			sym := extractPythonFunction(child, content, className)
			if sym != nil {
				methods = append(methods, sym)
			}
		}
	}

	return methods
}

func extractPythonAssignment(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	// Look for identifier on the left side
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name := child.Content(content)
			// Skip private/dunder variables for cleaner output
			if len(name) > 0 && name[0] != '_' {
				symbols = append(symbols, &Symbol{
					Name:      name,
					Kind:      "variable",
					Range:     nodeRange(node),
					Signature: name,
				})
			}
			break
		}
		// Handle tuple unpacking like a, b = 1, 2
		if child.Type() == "pattern_list" || child.Type() == "tuple_pattern" {
			for j := 0; j < int(child.ChildCount()); j++ {
				subChild := child.Child(j)
				if subChild.Type() == "identifier" {
					name := subChild.Content(content)
					if len(name) > 0 && name[0] != '_' {
						symbols = append(symbols, &Symbol{
							Name:      name,
							Kind:      "variable",
							Range:     nodeRange(node),
							Signature: name,
						})
					}
				}
			}
			break
		}
	}

	return symbols
}
