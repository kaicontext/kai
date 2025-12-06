// Package parse provides call graph extraction for JavaScript and TypeScript.
package parse

import (
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// CallSite represents a function/method call in source code.
type CallSite struct {
	CalleeName   string `json:"calleeName"`   // Name being called (e.g., "calculateTaxes")
	CalleeObject string `json:"calleeObject"` // Object if method call (e.g., "math" in math.add())
	Range        Range  `json:"range"`        // Location of the call
	IsMethodCall bool   `json:"isMethodCall"` // true if obj.method() style
}

// Import represents an import statement.
type Import struct {
	Source      string            `json:"source"`      // Import path (e.g., "./taxes", "lodash")
	Default     string            `json:"default"`     // Default import name (import X from ...)
	Namespace   string            `json:"namespace"`   // Namespace import (import * as X from ...)
	Named       map[string]string `json:"named"`       // Named imports {local: exported} (import {a as b} from ...)
	IsRelative  bool              `json:"isRelative"`  // true if starts with . or ..
	Range       Range             `json:"range"`       // Location of import statement
}

// ParsedCalls contains extracted calls and imports from a file.
type ParsedCalls struct {
	Calls   []*CallSite `json:"calls"`
	Imports []*Import   `json:"imports"`
	Exports []string    `json:"exports"` // Exported symbol names
}

// ExtractCalls extracts function calls and imports from JavaScript/TypeScript source.
func (p *Parser) ExtractCalls(content []byte, lang string) (*ParsedCalls, error) {
	parsed, err := p.Parse(content, lang)
	if err != nil {
		return nil, err
	}

	result := &ParsedCalls{
		Calls:   make([]*CallSite, 0),
		Imports: make([]*Import, 0),
		Exports: make([]string, 0),
	}

	root := parsed.Tree.RootNode()

	// Extract imports
	result.Imports = extractImports(root, content)

	// Extract calls
	result.Calls = extractCallSites(root, content)

	// Extract exports
	result.Exports = extractExports(root, content)

	return result, nil
}

// extractCallSites finds all function/method calls in the AST.
func extractCallSites(node *sitter.Node, content []byte) []*CallSite {
	var calls []*CallSite

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		if n.Type() != "call_expression" {
			continue
		}

		call := parseCallExpression(n, content)
		if call != nil {
			calls = append(calls, call)
		}
	}

	return calls
}

// parseCallExpression extracts call information from a call_expression node.
func parseCallExpression(node *sitter.Node, content []byte) *CallSite {
	// call_expression has children: function (what's being called) and arguments
	// function can be: identifier, member_expression, or another call_expression

	if node.ChildCount() == 0 {
		return nil
	}

	callee := node.Child(0) // First child is the thing being called
	if callee == nil {
		return nil
	}

	call := &CallSite{
		Range: nodeRange(node),
	}

	switch callee.Type() {
	case "identifier":
		// Direct call: foo()
		call.CalleeName = callee.Content(content)
		call.IsMethodCall = false

	case "member_expression":
		// Method call: obj.method() or obj.prop.method()
		call.IsMethodCall = true
		parseMemberExpression(callee, content, call)

	case "call_expression":
		// Chained call: foo()() - the result of foo() is being called
		// We track the inner call, not the outer one
		return nil

	case "parenthesized_expression":
		// (foo)() - unwrap and try again
		if callee.ChildCount() > 0 {
			inner := callee.Child(0)
			if inner != nil && inner.Type() == "identifier" {
				call.CalleeName = inner.Content(content)
			}
		}

	default:
		// Other cases: new_expression, await_expression, etc.
		return nil
	}

	if call.CalleeName == "" {
		return nil
	}

	return call
}

// parseMemberExpression extracts object and property from member_expression.
func parseMemberExpression(node *sitter.Node, content []byte, call *CallSite) {
	// member_expression: object.property
	// object can be: identifier, member_expression, this, call_expression
	// property is usually: property_identifier

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			// This is the object (leftmost part)
			if call.CalleeObject == "" {
				call.CalleeObject = child.Content(content)
			}
		case "property_identifier":
			// This is the property/method name
			call.CalleeName = child.Content(content)
		case "member_expression":
			// Nested: a.b.c() - recurse but keep the deepest property as CalleeName
			parseMemberExpression(child, content, call)
		case "this":
			call.CalleeObject = "this"
		case "call_expression":
			// foo().bar() - the object is a call result
			call.CalleeObject = "(call)"
		}
	}
}

// extractImports finds all import statements in the AST.
func extractImports(node *sitter.Node, content []byte) []*Import {
	var imports []*Import

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		switch n.Type() {
		case "import_statement":
			imp := parseImportStatement(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		case "call_expression":
			// Check for dynamic import: import("./foo")
			imp := parseDynamicImport(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
			// Check for CommonJS require: require("./foo")
			imp = parseRequireCall(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		}
	}

	return imports
}

// parseImportStatement parses an import statement.
// Handles:
//   - import foo from './bar'           (default)
//   - import * as foo from './bar'      (namespace)
//   - import { a, b as c } from './bar' (named)
//   - import './bar'                    (side-effect)
//   - import foo, { a, b } from './bar' (default + named)
func parseImportStatement(node *sitter.Node, content []byte) *Import {
	imp := &Import{
		Named: make(map[string]string),
		Range: nodeRange(node),
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "string", "string_fragment":
			// The import source path
			source := strings.Trim(child.Content(content), "\"'`")
			imp.Source = source
			imp.IsRelative = strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/")

		case "import_clause":
			parseImportClause(child, content, imp)

		case "identifier":
			// Side-effect import or default import
			// This shouldn't happen at top level, but handle it
			imp.Default = child.Content(content)

		case "namespace_import":
			// import * as foo
			parseNamespaceImport(child, content, imp)

		case "named_imports":
			// import { a, b }
			parseNamedImports(child, content, imp)
		}
	}

	// Also check for source in nested string node
	if imp.Source == "" {
		// Try to find string in any child
		findImportSource(node, content, imp)
	}

	if imp.Source == "" {
		return nil
	}

	return imp
}

// findImportSource recursively finds the import source string.
func findImportSource(node *sitter.Node, content []byte, imp *Import) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string" {
			source := strings.Trim(child.Content(content), "\"'`")
			imp.Source = source
			imp.IsRelative = strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/")
			return
		}
		findImportSource(child, content, imp)
	}
}

// parseImportClause parses the import clause (everything between 'import' and 'from').
func parseImportClause(node *sitter.Node, content []byte, imp *Import) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "identifier":
			// Default import: import foo from ...
			imp.Default = child.Content(content)

		case "namespace_import":
			// import * as foo
			parseNamespaceImport(child, content, imp)

		case "named_imports":
			// import { a, b as c }
			parseNamedImports(child, content, imp)
		}
	}
}

// parseNamespaceImport parses: * as foo
func parseNamespaceImport(node *sitter.Node, content []byte, imp *Import) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			imp.Namespace = child.Content(content)
			break
		}
	}
}

// parseNamedImports parses: { a, b as c, d }
func parseNamedImports(node *sitter.Node, content []byte, imp *Import) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "import_specifier":
			// Can be: identifier OR identifier as identifier
			var exported, local string
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "identifier" {
					if exported == "" {
						exported = spec.Content(content)
						local = exported // Default: local name = exported name
					} else {
						local = spec.Content(content) // "as" clause
					}
				}
			}
			if exported != "" {
				imp.Named[local] = exported
			}

		case "identifier":
			// Direct identifier (no "as")
			name := child.Content(content)
			imp.Named[name] = name
		}
	}
}

// parseDynamicImport checks for import("./foo") calls.
func parseDynamicImport(node *sitter.Node, content []byte) *Import {
	if node.ChildCount() < 2 {
		return nil
	}

	// First child should be "import"
	callee := node.Child(0)
	if callee == nil || callee.Type() != "import" {
		return nil
	}

	// Second child is arguments
	args := node.Child(1)
	if args == nil || args.Type() != "arguments" {
		return nil
	}

	// Find the string argument
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			source := strings.Trim(child.Content(content), "\"'`")
			return &Import{
				Source:     source,
				IsRelative: strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/"),
				Named:      make(map[string]string),
				Range:      nodeRange(node),
			}
		}
	}

	return nil
}

// parseRequireCall checks for CommonJS require("./foo") calls.
// Handles:
//   - require('./foo')
//   - const foo = require('./foo')
//   - const { a, b } = require('./foo')
func parseRequireCall(node *sitter.Node, content []byte) *Import {
	if node.ChildCount() < 2 {
		return nil
	}

	// First child should be identifier "require"
	callee := node.Child(0)
	if callee == nil {
		return nil
	}

	// Check if it's "require"
	if callee.Type() != "identifier" || callee.Content(content) != "require" {
		return nil
	}

	// Second child is arguments
	args := node.Child(1)
	if args == nil || args.Type() != "arguments" {
		return nil
	}

	// Find the string argument
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			source := strings.Trim(child.Content(content), "\"'`")
			return &Import{
				Source:     source,
				IsRelative: strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/"),
				Named:      make(map[string]string),
				Range:      nodeRange(node),
			}
		}
	}

	return nil
}

// extractExports finds exported symbol names.
func extractExports(node *sitter.Node, content []byte) []string {
	var exports []string
	seen := make(map[string]bool)

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		if n.Type() != "export_statement" {
			continue
		}

		names := parseExportStatement(n, content)
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				exports = append(exports, name)
			}
		}
	}

	return exports
}

// parseExportStatement extracts exported names from export statement.
func parseExportStatement(node *sitter.Node, content []byte) []string {
	var names []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "function_declaration":
			// export function foo() {}
			name := extractFunctionName(child, content)
			if name != "" {
				names = append(names, name)
			}

		case "class_declaration":
			// export class Foo {}
			name := extractClassName(child, content)
			if name != "" {
				names = append(names, name)
			}

		case "lexical_declaration", "variable_declaration":
			// export const foo = ...
			varNames := extractVarNames(child, content)
			names = append(names, varNames...)

		case "export_clause":
			// export { a, b as c }
			clauseNames := parseExportClause(child, content)
			names = append(names, clauseNames...)

		case "identifier":
			// export default foo (the 'foo' identifier)
			names = append(names, child.Content(content))
		}
	}

	return names
}

// parseExportClause parses: { a, b as c }
func parseExportClause(node *sitter.Node, content []byte) []string {
	var names []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		if child.Type() == "export_specifier" {
			// First identifier is the local name, second (if exists) is exported name
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "identifier" {
					names = append(names, spec.Content(content))
					break // Take first identifier
				}
			}
		}
	}

	return names
}

// Helper functions to extract names from declarations

func extractFunctionName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			return child.Content(content)
		}
	}
	return ""
}

func extractClassName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			return child.Content(content)
		}
	}
	return ""
}

func extractVarNames(node *sitter.Node, content []byte) []string {
	var names []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			for j := 0; j < int(child.ChildCount()); j++ {
				decl := child.Child(j)
				if decl.Type() == "identifier" {
					names = append(names, decl.Content(content))
					break
				}
			}
		}
	}

	return names
}

// ResolveImportPath resolves a relative import path to an absolute path.
// basePath is the directory containing the importing file.
// importSource is the import string (e.g., "./foo", "../bar", "lodash").
func ResolveImportPath(basePath, importSource string) string {
	if !strings.HasPrefix(importSource, ".") {
		// Non-relative import (e.g., "lodash", "@org/pkg")
		return importSource
	}

	// Resolve relative path
	resolved := filepath.Join(basePath, importSource)
	resolved = filepath.Clean(resolved)

	return resolved
}

// PossibleFilePaths returns possible file paths for an import.
// Handles: ./foo → ./foo.ts, ./foo.js, ./foo/index.ts, ./foo/index.js
func PossibleFilePaths(importPath string) []string {
	// If already has extension, just return it
	ext := filepath.Ext(importPath)
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		return []string{importPath}
	}

	// Try various extensions and index files
	return []string{
		importPath + ".ts",
		importPath + ".tsx",
		importPath + ".js",
		importPath + ".jsx",
		filepath.Join(importPath, "index.ts"),
		filepath.Join(importPath, "index.tsx"),
		filepath.Join(importPath, "index.js"),
		filepath.Join(importPath, "index.jsx"),
	}
}

// IsTestFile returns true if the file path looks like a test file.
func IsTestFile(path string) bool {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	// Check filename patterns
	if strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".test.js") ||
		strings.HasSuffix(base, ".test.jsx") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".spec.tsx") ||
		strings.HasSuffix(base, ".spec.js") ||
		strings.HasSuffix(base, ".spec.jsx") ||
		strings.HasSuffix(base, "_test.ts") ||
		strings.HasSuffix(base, "_test.js") {
		return true
	}

	// Check directory patterns
	if strings.Contains(dir, "__tests__") ||
		strings.Contains(dir, "__test__") ||
		strings.HasSuffix(dir, "/test") ||
		strings.HasSuffix(dir, "/tests") ||
		dir == "test" ||
		dir == "tests" ||
		strings.HasPrefix(dir, "test/") ||
		strings.HasPrefix(dir, "tests/") {
		return true
	}

	return false
}

// FindTestsForFile finds potential test files for a source file.
func FindTestsForFile(sourcePath string, allFiles []string) []string {
	var tests []string

	// Remove extension
	ext := filepath.Ext(sourcePath)
	basePath := strings.TrimSuffix(sourcePath, ext)
	dir := filepath.Dir(sourcePath)
	baseName := filepath.Base(basePath)

	// Patterns to check
	patterns := []string{
		basePath + ".test" + ext,
		basePath + ".spec" + ext,
		basePath + "_test" + ext,
		filepath.Join(dir, "__tests__", baseName+ext),
		filepath.Join(dir, "__tests__", baseName+".test"+ext),
	}

	// Also check .ts/.tsx if source is .js/.jsx and vice versa
	if ext == ".js" || ext == ".jsx" {
		patterns = append(patterns,
			basePath+".test.ts",
			basePath+".spec.ts",
			basePath+".test.tsx",
			basePath+".spec.tsx",
		)
	}
	if ext == ".ts" || ext == ".tsx" {
		patterns = append(patterns,
			basePath+".test.js",
			basePath+".spec.js",
			basePath+".test.jsx",
			basePath+".spec.jsx",
		)
	}

	// Check which patterns exist in allFiles
	fileSet := make(map[string]bool)
	for _, f := range allFiles {
		fileSet[f] = true
	}

	for _, pattern := range patterns {
		if fileSet[pattern] {
			tests = append(tests, pattern)
		}
	}

	return tests
}
