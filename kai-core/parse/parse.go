// Package parse provides Tree-sitter based parsing for TypeScript, JavaScript, Python, Go, and Ruby.
package parse

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
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
	goParser *sitter.Parser
	rbParser *sitter.Parser
}

// NewParser creates a new parser with support for JavaScript/TypeScript, Python, Go, and Ruby.
func NewParser() *Parser {
	jsParser := sitter.NewParser()
	jsParser.SetLanguage(javascript.GetLanguage())

	pyParser := sitter.NewParser()
	pyParser.SetLanguage(python.GetLanguage())

	goParser := sitter.NewParser()
	goParser.SetLanguage(golang.GetLanguage())

	rbParser := sitter.NewParser()
	rbParser.SetLanguage(ruby.GetLanguage())

	return &Parser{
		jsParser: jsParser,
		pyParser: pyParser,
		goParser: goParser,
		rbParser: rbParser,
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
	case "go", "golang":
		parser = p.goParser
		extractFn = extractGoSymbols
	case "js", "ts", "javascript", "typescript":
		parser = p.jsParser
		extractFn = extractSymbols
	case "rb", "ruby":
		parser = p.rbParser
		extractFn = extractRubySymbols
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

// ==================== Go Symbol Extraction ====================

// extractGoSymbols walks the Go AST and extracts function, type, and variable declarations.
func extractGoSymbols(node *sitter.Node, content []byte) []*Symbol {
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
		case "function_declaration":
			sym := extractGoFunction(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "method_declaration":
			sym := extractGoMethod(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "type_declaration":
			syms := extractGoTypes(n, content)
			symbols = append(symbols, syms...)
		case "var_declaration", "const_declaration":
			syms := extractGoVarConst(n, content)
			symbols = append(symbols, syms...)
		}
	}

	return symbols
}

func extractGoFunction(node *sitter.Node, content []byte) *Symbol {
	var name string
	var params string
	var result string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "parameter_list":
			params = child.Content(content)
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type":
			result = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	signature := "func " + name + params
	if result != "" {
		signature += " " + result
	}

	return &Symbol{
		Name:      name,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractGoMethod(node *sitter.Node, content []byte) *Symbol {
	var name string
	var receiver string
	var params string
	var result string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "parameter_list":
			if receiver == "" {
				// First parameter_list is the receiver
				receiver = extractGoReceiverType(child, content)
			} else if params == "" {
				// Second parameter_list is the params
				params = child.Content(content)
			} else {
				// Third would be result
				result = child.Content(content)
			}
		case "field_identifier":
			name = child.Content(content)
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type":
			result = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	fullName := name
	if receiver != "" {
		fullName = receiver + "." + name
	}

	signature := "func "
	if receiver != "" {
		signature += "(" + receiver + ") "
	}
	signature += name + params
	if result != "" {
		signature += " " + result
	}

	return &Symbol{
		Name:      fullName,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractGoReceiverType(paramList *sitter.Node, content []byte) string {
	// parameter_list contains parameter_declaration(s)
	// Find the type in the first parameter_declaration
	for i := 0; i < int(paramList.ChildCount()); i++ {
		child := paramList.Child(i)
		if child.Type() == "parameter_declaration" {
			// Look for the type
			for j := 0; j < int(child.ChildCount()); j++ {
				typeChild := child.Child(j)
				switch typeChild.Type() {
				case "type_identifier":
					return typeChild.Content(content)
				case "pointer_type":
					// Extract the base type from pointer
					for k := 0; k < int(typeChild.ChildCount()); k++ {
						ptrChild := typeChild.Child(k)
						if ptrChild.Type() == "type_identifier" {
							return "*" + ptrChild.Content(content)
						}
					}
					return typeChild.Content(content)
				}
			}
		}
	}
	return ""
}

func extractGoTypes(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	// type_declaration contains type_spec(s)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_spec" {
			sym := extractGoTypeSpec(child, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		}
	}

	return symbols
}

func extractGoTypeSpec(node *sitter.Node, content []byte) *Symbol {
	var name string
	var kind = "type"
	var typeKind string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "struct_type":
			kind = "class" // Use "class" for structs to match other languages
			typeKind = "struct"
		case "interface_type":
			kind = "interface"
			typeKind = "interface"
		}
	}

	if name == "" {
		return nil
	}

	signature := "type " + name
	if typeKind != "" {
		signature += " " + typeKind
	}

	return &Symbol{
		Name:      name,
		Kind:      kind,
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractGoVarConst(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol
	isConst := node.Type() == "const_declaration"
	declKind := "var"
	if isConst {
		declKind = "const"
	}

	// var/const_declaration contains var_spec or const_spec
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "var_spec" || child.Type() == "const_spec" {
			syms := extractGoVarSpec(child, content, declKind)
			symbols = append(symbols, syms...)
		}
	}

	return symbols
}

func extractGoVarSpec(node *sitter.Node, content []byte, declKind string) []*Symbol {
	var symbols []*Symbol
	var names []string
	var typeStr string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			name := child.Content(content)
			names = append(names, name)
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type", "array_type", "function_type":
			typeStr = child.Content(content)
		}
	}

	for _, name := range names {
		signature := declKind + " " + name
		if typeStr != "" {
			signature += " " + typeStr
		}

		// Determine if this is a function variable
		kind := "variable"
		if strings.HasPrefix(typeStr, "func") {
			kind = "function"
		}

		symbols = append(symbols, &Symbol{
			Name:      name,
			Kind:      kind,
			Range:     nodeRange(node),
			Signature: signature,
		})
	}

	return symbols
}

// ==================== Ruby Symbol Extraction ====================

// extractRubySymbols walks the Ruby AST and extracts method, class, and module declarations.
func extractRubySymbols(node *sitter.Node, content []byte) []*Symbol {
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
		case "method":
			sym := extractRubyMethod(n, content, "")
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "singleton_method":
			sym := extractRubySingletonMethod(n, content, "")
			if sym != nil {
				symbols = append(symbols, sym)
			}
		case "class":
			sym := extractRubyClass(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
			// Also extract methods within the class
			methods := extractRubyClassMethods(n, content)
			symbols = append(symbols, methods...)
		case "module":
			sym := extractRubyModule(n, content)
			if sym != nil {
				symbols = append(symbols, sym)
			}
			// Also extract methods within the module
			methods := extractRubyModuleMethods(n, content)
			symbols = append(symbols, methods...)
		case "assignment":
			// Top-level constant assignments (CONSTANT = value)
			parent := n.Parent()
			if parent != nil && parent.Type() == "program" {
				syms := extractRubyAssignment(n, content)
				symbols = append(symbols, syms...)
			}
		}
	}

	return symbols
}

func extractRubyMethod(node *sitter.Node, content []byte, className string) *Symbol {
	var name string
	var params string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "method_parameters":
			params = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	fullName := name
	if className != "" {
		fullName = className + "#" + name
	}

	signature := "def " + name
	if params != "" {
		signature += params
	}

	return &Symbol{
		Name:      fullName,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractRubySingletonMethod(node *sitter.Node, content []byte, className string) *Symbol {
	var name string
	var params string
	var object string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if object == "" {
				object = child.Content(content)
			} else if name == "" {
				name = child.Content(content)
			}
		case "self":
			object = "self"
		case "method_parameters":
			params = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	fullName := name
	if className != "" {
		fullName = className + "." + name
	} else if object == "self" {
		fullName = "self." + name
	}

	signature := "def " + object + "." + name
	if params != "" {
		signature += params
	}

	return &Symbol{
		Name:      fullName,
		Kind:      "function",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractRubyClass(node *sitter.Node, content []byte) *Symbol {
	var name string
	var superclass string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "constant":
			if name == "" {
				name = child.Content(content)
			}
		case "scope_resolution":
			if name == "" {
				name = child.Content(content)
			}
		case "superclass":
			// superclass node contains the parent class
			for j := 0; j < int(child.ChildCount()); j++ {
				superChild := child.Child(j)
				if superChild.Type() == "constant" || superChild.Type() == "scope_resolution" {
					superclass = superChild.Content(content)
					break
				}
			}
		}
	}

	if name == "" {
		return nil
	}

	signature := "class " + name
	if superclass != "" {
		signature += " < " + superclass
	}

	return &Symbol{
		Name:      name,
		Kind:      "class",
		Range:     nodeRange(node),
		Signature: signature,
	}
}

func extractRubyModule(node *sitter.Node, content []byte) *Symbol {
	var name string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "constant":
			if name == "" {
				name = child.Content(content)
			}
		case "scope_resolution":
			if name == "" {
				name = child.Content(content)
			}
		}
	}

	if name == "" {
		return nil
	}

	return &Symbol{
		Name:      name,
		Kind:      "module",
		Range:     nodeRange(node),
		Signature: "module " + name,
	}
}

func extractRubyClassMethods(classNode *sitter.Node, content []byte) []*Symbol {
	var methods []*Symbol

	// Find class name
	var className string
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "constant" || child.Type() == "scope_resolution" {
			className = child.Content(content)
			break
		}
	}

	// Find body_statement (class body)
	var classBody *sitter.Node
	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		if child.Type() == "body_statement" {
			classBody = child
			break
		}
	}

	if classBody == nil {
		return methods
	}

	// Find method definitions inside the body
	for i := 0; i < int(classBody.ChildCount()); i++ {
		child := classBody.Child(i)
		switch child.Type() {
		case "method":
			sym := extractRubyMethod(child, content, className)
			if sym != nil {
				methods = append(methods, sym)
			}
		case "singleton_method":
			sym := extractRubySingletonMethod(child, content, className)
			if sym != nil {
				methods = append(methods, sym)
			}
		}
	}

	return methods
}

func extractRubyModuleMethods(moduleNode *sitter.Node, content []byte) []*Symbol {
	var methods []*Symbol

	// Find module name
	var moduleName string
	for i := 0; i < int(moduleNode.ChildCount()); i++ {
		child := moduleNode.Child(i)
		if child.Type() == "constant" || child.Type() == "scope_resolution" {
			moduleName = child.Content(content)
			break
		}
	}

	// Find body_statement (module body)
	var moduleBody *sitter.Node
	for i := 0; i < int(moduleNode.ChildCount()); i++ {
		child := moduleNode.Child(i)
		if child.Type() == "body_statement" {
			moduleBody = child
			break
		}
	}

	if moduleBody == nil {
		return methods
	}

	// Find method definitions inside the body
	for i := 0; i < int(moduleBody.ChildCount()); i++ {
		child := moduleBody.Child(i)
		switch child.Type() {
		case "method":
			sym := extractRubyMethod(child, content, moduleName)
			if sym != nil {
				methods = append(methods, sym)
			}
		case "singleton_method":
			sym := extractRubySingletonMethod(child, content, moduleName)
			if sym != nil {
				methods = append(methods, sym)
			}
		}
	}

	return methods
}

func extractRubyAssignment(node *sitter.Node, content []byte) []*Symbol {
	var symbols []*Symbol

	// Look for constant on the left side (CONSTANT = value)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "constant" {
			name := child.Content(content)
			symbols = append(symbols, &Symbol{
				Name:      name,
				Kind:      "variable",
				Range:     nodeRange(node),
				Signature: name,
			})
			break
		}
	}

	return symbols
}
