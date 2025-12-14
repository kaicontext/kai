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

// ExtractCalls extracts function calls and imports from source code.
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

	switch lang {
	case "go", "golang":
		result.Imports = extractGoImports(root, content)
		result.Calls = extractGoCallSites(root, content)
		result.Exports = extractGoExports(root, content)
	case "rb", "ruby":
		result.Imports = extractRubyImports(root, content)
		result.Calls = extractRubyCallSites(root, content)
		result.Exports = extractRubyExports(root, content)
	case "rs", "rust":
		result.Imports = extractRustImports(root, content)
		result.Calls = extractRustCallSites(root, content)
		result.Exports = extractRustExports(root, content)
	default:
		// JavaScript/TypeScript/Python
		result.Imports = extractImports(root, content)
		result.Calls = extractCallSites(root, content)
		result.Exports = extractExports(root, content)
	}

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

	// Check filename patterns - JavaScript/TypeScript
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

	// Check filename patterns - Ruby (RSpec/Minitest)
	if strings.HasSuffix(base, "_spec.rb") ||
		strings.HasSuffix(base, "_test.rb") {
		return true
	}

	// Check filename patterns - Go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// Check filename patterns - Python
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") ||
		strings.HasSuffix(base, "_test.py") {
		return true
	}

	// Check directory patterns
	if strings.Contains(dir, "__tests__") ||
		strings.Contains(dir, "__test__") ||
		strings.HasSuffix(dir, "/test") ||
		strings.HasSuffix(dir, "/tests") ||
		strings.HasSuffix(dir, "/spec") || // Ruby RSpec convention
		dir == "test" ||
		dir == "tests" ||
		dir == "spec" ||
		strings.HasPrefix(dir, "test/") ||
		strings.HasPrefix(dir, "tests/") ||
		strings.HasPrefix(dir, "spec/") ||
		strings.Contains(dir, "/spec/") {
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

	// Ruby patterns: foo.rb -> foo_spec.rb, foo_test.rb, spec/foo_spec.rb
	if ext == ".rb" {
		patterns = append(patterns,
			basePath+"_spec.rb",
			basePath+"_test.rb",
			filepath.Join(dir, "spec", baseName+"_spec.rb"),
			filepath.Join("spec", strings.TrimPrefix(dir, "lib/"), baseName+"_spec.rb"),
		)
	}

	// Go patterns: foo.go -> foo_test.go
	if ext == ".go" {
		patterns = append(patterns, basePath+"_test.go")
	}

	// Python patterns: foo.py -> test_foo.py, foo_test.py
	if ext == ".py" {
		patterns = append(patterns,
			filepath.Join(dir, "test_"+baseName+".py"),
			basePath+"_test.py",
		)
	}

	// Rust patterns: src/foo.rs -> tests/foo.rs, tests/test_foo.rs
	if ext == ".rs" {
		// Strip src/ prefix if present for tests/ directory
		testBase := baseName
		if strings.HasPrefix(sourcePath, "src/") {
			testBase = strings.TrimPrefix(basePath, "src/")
		}
		patterns = append(patterns,
			filepath.Join("tests", testBase+".rs"),
			filepath.Join("tests", "test_"+baseName+".rs"),
			basePath+"_test.rs",
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

// ==================== Go Import/Call Extraction ====================

// extractGoImports finds all import declarations in Go source.
func extractGoImports(node *sitter.Node, content []byte) []*Import {
	var imports []*Import

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		if n.Type() == "import_declaration" {
			imps := parseGoImportDeclaration(n, content)
			imports = append(imports, imps...)
		}
	}

	return imports
}

// parseGoImportDeclaration parses Go import declaration.
// Handles:
//   - import "fmt"
//   - import alias "pkg"
//   - import . "pkg"
//   - import _ "pkg"
//   - import ( "fmt"; "os" )
func parseGoImportDeclaration(node *sitter.Node, content []byte) []*Import {
	var imports []*Import

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "import_spec":
			imp := parseGoImportSpec(child, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		case "import_spec_list":
			// Multiple imports in parentheses
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "import_spec" {
					imp := parseGoImportSpec(spec, content)
					if imp != nil {
						imports = append(imports, imp)
					}
				}
			}
		}
	}

	return imports
}

// parseGoImportSpec parses a single Go import spec.
func parseGoImportSpec(node *sitter.Node, content []byte) *Import {
	imp := &Import{
		Named: make(map[string]string),
		Range: nodeRange(node),
	}

	var alias string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "package_identifier", "identifier", "blank_identifier", "dot":
			// Alias or . or _
			alias = child.Content(content)
		case "interpreted_string_literal", "raw_string_literal":
			// Import path
			source := strings.Trim(child.Content(content), "\"'`")
			imp.Source = source
			// Go imports are typically not "relative" in the same way as JS
			// But we can mark local/internal packages
			imp.IsRelative = strings.HasPrefix(source, "./") ||
				strings.HasPrefix(source, "../") ||
				strings.Contains(source, "/internal/")
		}
	}

	if imp.Source == "" {
		return nil
	}

	// Set alias info
	if alias != "" {
		switch alias {
		case ".":
			// Dot import - all exported identifiers available directly
			imp.Namespace = "."
		case "_":
			// Blank import - side effects only
			imp.Default = "_"
		default:
			// Named alias
			imp.Default = alias
		}
	} else {
		// No alias - use last component of path as default import name
		parts := strings.Split(imp.Source, "/")
		if len(parts) > 0 {
			imp.Default = parts[len(parts)-1]
		}
	}

	return imp
}

// extractGoCallSites finds all function/method calls in Go source.
func extractGoCallSites(node *sitter.Node, content []byte) []*CallSite {
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

		call := parseGoCallExpression(n, content)
		if call != nil {
			calls = append(calls, call)
		}
	}

	return calls
}

// parseGoCallExpression extracts call info from a Go call_expression.
func parseGoCallExpression(node *sitter.Node, content []byte) *CallSite {
	if node.ChildCount() == 0 {
		return nil
	}

	// First child is the function being called
	callee := node.Child(0)
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

	case "selector_expression":
		// Method/package call: pkg.Func() or obj.Method()
		call.IsMethodCall = true
		parseGoSelectorExpression(callee, content, call)

	case "call_expression":
		// Chained call: foo()()
		return nil

	case "parenthesized_expression":
		// (foo)()
		if callee.ChildCount() > 0 {
			for i := 0; i < int(callee.ChildCount()); i++ {
				inner := callee.Child(i)
				if inner != nil && inner.Type() == "identifier" {
					call.CalleeName = inner.Content(content)
					break
				}
			}
		}

	case "type_conversion_expression":
		// Type conversion: int(x)
		// Find the type being converted to
		for i := 0; i < int(callee.ChildCount()); i++ {
			child := callee.Child(i)
			if child.Type() == "type_identifier" {
				call.CalleeName = child.Content(content)
				break
			}
		}

	default:
		return nil
	}

	if call.CalleeName == "" {
		return nil
	}

	return call
}

// parseGoSelectorExpression extracts object and field from selector_expression.
func parseGoSelectorExpression(node *sitter.Node, content []byte, call *CallSite) {
	// selector_expression: operand.field_identifier
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "identifier":
			// Package or object name
			if call.CalleeObject == "" {
				call.CalleeObject = child.Content(content)
			}
		case "field_identifier":
			// Method/function name
			call.CalleeName = child.Content(content)
		case "selector_expression":
			// Nested: a.b.c()
			parseGoSelectorExpression(child, content, call)
		case "call_expression":
			// foo().bar()
			call.CalleeObject = "(call)"
		}
	}
}

// extractGoExports finds exported symbols in Go source.
// In Go, exported symbols are those starting with uppercase letter.
func extractGoExports(node *sitter.Node, content []byte) []string {
	var exports []string
	seen := make(map[string]bool)

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		var name string

		switch n.Type() {
		case "function_declaration":
			// Find function name
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "identifier" {
					name = child.Content(content)
					break
				}
			}

		case "method_declaration":
			// Find method name
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "field_identifier" {
					name = child.Content(content)
					break
				}
			}

		case "type_spec":
			// Find type name
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "type_identifier" {
					name = child.Content(content)
					break
				}
			}

		case "var_spec", "const_spec":
			// Find variable/constant names
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "identifier" {
					varName := child.Content(content)
					if isGoExported(varName) && !seen[varName] {
						seen[varName] = true
						exports = append(exports, varName)
					}
				}
			}
			continue
		}

		if name != "" && isGoExported(name) && !seen[name] {
			seen[name] = true
			exports = append(exports, name)
		}
	}

	return exports
}

// isGoExported returns true if the name is exported (starts with uppercase).
func isGoExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

// ==================== Ruby Import/Call Extraction ====================

// extractRubyImports finds all require/require_relative/load statements in Ruby source.
func extractRubyImports(node *sitter.Node, content []byte) []*Import {
	var imports []*Import

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		// Look for method calls that are require, require_relative, or load
		if n.Type() == "call" {
			imp := parseRubyRequireCall(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		}
	}

	return imports
}

// parseRubyRequireCall parses require/require_relative/load calls.
// Handles:
//   - require 'foo'
//   - require "foo"
//   - require_relative './foo'
//   - load 'foo.rb'
func parseRubyRequireCall(node *sitter.Node, content []byte) *Import {
	var methodName string
	var source string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "identifier":
			if methodName == "" {
				methodName = child.Content(content)
			}
		case "argument_list":
			// Find string argument
			for j := 0; j < int(child.ChildCount()); j++ {
				arg := child.Child(j)
				if arg.Type() == "string" {
					source = extractRubyStringContent(arg, content)
					break
				}
			}
		case "string":
			// Direct string argument (without parentheses)
			source = extractRubyStringContent(child, content)
		}
	}

	// Check if it's a require-type call
	if methodName != "require" && methodName != "require_relative" && methodName != "load" {
		return nil
	}

	if source == "" {
		return nil
	}

	isRelative := methodName == "require_relative" ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../")

	return &Import{
		Source:     source,
		IsRelative: isRelative,
		Default:    filepath.Base(strings.TrimSuffix(source, ".rb")),
		Named:      make(map[string]string),
		Range:      nodeRange(node),
	}
}

// extractRubyStringContent extracts the content from a Ruby string node.
func extractRubyStringContent(node *sitter.Node, content []byte) string {
	// Ruby strings have structure: string -> string_content or interpolation
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string_content" {
			return child.Content(content)
		}
	}
	// Fallback: try to get content directly and trim quotes
	s := node.Content(content)
	s = strings.Trim(s, "\"'")
	return s
}

// extractRubyCallSites finds all method calls in Ruby source.
func extractRubyCallSites(node *sitter.Node, content []byte) []*CallSite {
	var calls []*CallSite

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		if n.Type() != "call" {
			continue
		}

		call := parseRubyCallExpression(n, content)
		if call != nil {
			calls = append(calls, call)
		}
	}

	return calls
}

// parseRubyCallExpression extracts call info from a Ruby call node.
func parseRubyCallExpression(node *sitter.Node, content []byte) *CallSite {
	call := &CallSite{
		Range: nodeRange(node),
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "identifier":
			// Method name (for simple calls like `foo()`)
			if call.CalleeName == "" {
				call.CalleeName = child.Content(content)
			}
		case "constant":
			// Class/module name (for calls like `Foo.bar`)
			if call.CalleeObject == "" {
				call.CalleeObject = child.Content(content)
			}
		case "call":
			// Chained call: foo.bar.baz
			call.CalleeObject = "(call)"
		case "self":
			call.CalleeObject = "self"
			call.IsMethodCall = true
		}
	}

	// Check for method call pattern (receiver.method)
	// In Ruby tree-sitter, this might be represented differently
	if call.CalleeObject != "" {
		call.IsMethodCall = true
	}

	if call.CalleeName == "" {
		return nil
	}

	// Skip require/require_relative/load as they're handled as imports
	if call.CalleeName == "require" || call.CalleeName == "require_relative" || call.CalleeName == "load" {
		return nil
	}

	return call
}

// extractRubyExports finds public methods and constants in Ruby source.
// In Ruby, methods are public by default unless marked private/protected.
func extractRubyExports(node *sitter.Node, content []byte) []string {
	var exports []string
	seen := make(map[string]bool)
	inPrivateSection := false

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		switch n.Type() {
		case "call":
			// Check for private/protected/public visibility modifiers
			methodName := ""
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "identifier" {
					methodName = child.Content(content)
					break
				}
			}
			switch methodName {
			case "private", "protected":
				inPrivateSection = true
			case "public":
				inPrivateSection = false
			}

		case "method":
			if inPrivateSection {
				continue
			}
			// Find method name
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "identifier" {
					name := child.Content(content)
					// Skip methods starting with underscore (convention for private)
					if !strings.HasPrefix(name, "_") && !seen[name] {
						seen[name] = true
						exports = append(exports, name)
					}
					break
				}
			}

		case "singleton_method":
			if inPrivateSection {
				continue
			}
			// Find class method name (def self.foo)
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "identifier" {
					name := child.Content(content)
					if name != "self" && !strings.HasPrefix(name, "_") && !seen[name] {
						seen[name] = true
						exports = append(exports, name)
					}
				}
			}

		case "class", "module":
			// Export class/module names
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "constant" {
					name := child.Content(content)
					if !seen[name] {
						seen[name] = true
						exports = append(exports, name)
					}
					break
				}
			}

		case "assignment":
			// Check for constant assignments (CONSTANT = value)
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "constant" {
					name := child.Content(content)
					if !seen[name] {
						seen[name] = true
						exports = append(exports, name)
					}
					break
				}
			}
		}
	}

	return exports
}

// ==================== Rust Import/Call Extraction ====================

// extractRustImports finds all use/mod/extern crate statements in Rust source.
func extractRustImports(node *sitter.Node, content []byte) []*Import {
	var imports []*Import

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		switch n.Type() {
		case "use_declaration":
			imp := parseRustUseDeclaration(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		case "extern_crate_declaration":
			imp := parseRustExternCrate(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		}
	}

	return imports
}

// parseRustUseDeclaration parses use statements.
// Handles:
//   - use std::io;
//   - use std::io::Read;
//   - use std::collections::{HashMap, HashSet};
//   - use crate::module;
//   - use super::parent;
func parseRustUseDeclaration(node *sitter.Node, content []byte) *Import {
	var source string
	named := make(map[string]string)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "scoped_identifier", "identifier", "scoped_use_list", "use_wildcard":
			source = child.Content(content)
		}
	}

	if source == "" {
		return nil
	}

	// Check if it's a relative import (crate::, super::, self::)
	isRelative := strings.HasPrefix(source, "crate::") ||
		strings.HasPrefix(source, "super::") ||
		strings.HasPrefix(source, "self::")

	// Extract the crate/module name (first part before ::)
	parts := strings.Split(source, "::")
	crateName := parts[0]
	if len(parts) > 1 {
		// Last part is what's being imported
		lastPart := parts[len(parts)-1]
		// Handle braced imports {A, B}
		if strings.HasPrefix(lastPart, "{") {
			lastPart = strings.Trim(lastPart, "{}")
			for _, name := range strings.Split(lastPart, ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					named[name] = name
				}
			}
		} else if lastPart != "*" {
			named[lastPart] = lastPart
		}
	}

	return &Import{
		Source:     source,
		IsRelative: isRelative,
		Default:    crateName,
		Named:      named,
		Range:      nodeRange(node),
	}
}

// parseRustExternCrate parses extern crate statements.
func parseRustExternCrate(node *sitter.Node, content []byte) *Import {
	var crateName string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			crateName = child.Content(content)
			break
		}
	}

	if crateName == "" {
		return nil
	}

	return &Import{
		Source:     crateName,
		IsRelative: false,
		Default:    crateName,
		Named:      make(map[string]string),
		Range:      nodeRange(node),
	}
}

// extractRustCallSites finds all function/method calls in Rust source.
func extractRustCallSites(node *sitter.Node, content []byte) []*CallSite {
	var calls []*CallSite

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		switch n.Type() {
		case "call_expression":
			call := parseRustCallExpression(n, content)
			if call != nil {
				calls = append(calls, call)
			}
		case "method_call_expression":
			call := parseRustMethodCall(n, content)
			if call != nil {
				calls = append(calls, call)
			}
		case "macro_invocation":
			call := parseRustMacroInvocation(n, content)
			if call != nil {
				calls = append(calls, call)
			}
		}
	}

	return calls
}

// parseRustCallExpression parses function call expressions.
func parseRustCallExpression(node *sitter.Node, content []byte) *CallSite {
	call := &CallSite{
		Range: nodeRange(node),
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			call.CalleeName = child.Content(content)
		case "scoped_identifier":
			// e.g., std::io::read
			call.CalleeName = child.Content(content)
		case "field_expression":
			// e.g., self.method
			call.CalleeObject = "(field)"
			call.IsMethodCall = true
		}
	}

	if call.CalleeName == "" {
		return nil
	}

	return call
}

// parseRustMethodCall parses method call expressions.
func parseRustMethodCall(node *sitter.Node, content []byte) *CallSite {
	call := &CallSite{
		Range:        nodeRange(node),
		IsMethodCall: true,
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			// Method name
			call.CalleeName = child.Content(content)
		case "field_identifier":
			// Method name in method call position
			call.CalleeName = child.Content(content)
		}
	}

	if call.CalleeName == "" {
		return nil
	}

	return call
}

// parseRustMacroInvocation parses macro invocations.
func parseRustMacroInvocation(node *sitter.Node, content []byte) *CallSite {
	call := &CallSite{
		Range: nodeRange(node),
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			call.CalleeName = child.Content(content) + "!"
			break
		}
	}

	if call.CalleeName == "" {
		return nil
	}

	return call
}

// extractRustExports finds public items in Rust source.
func extractRustExports(node *sitter.Node, content []byte) []string {
	var exports []string
	seen := make(map[string]bool)

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		// Check if this item has pub visibility
		isPub := false
		for i := 0; i < int(n.ChildCount()); i++ {
			child := n.Child(i)
			if child.Type() == "visibility_modifier" {
				isPub = true
				break
			}
		}

		if !isPub {
			continue
		}

		var name string
		switch n.Type() {
		case "function_item":
			name = extractRustItemName(n, content, "identifier")
		case "struct_item", "enum_item", "trait_item", "type_item":
			name = extractRustItemName(n, content, "type_identifier")
		case "const_item", "static_item", "mod_item":
			name = extractRustItemName(n, content, "identifier")
		}

		if name != "" && !seen[name] {
			seen[name] = true
			exports = append(exports, name)
		}
	}

	return exports
}

// extractRustItemName finds the name of a Rust item by looking for the given node type.
func extractRustItemName(node *sitter.Node, content []byte, nodeType string) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == nodeType {
			return child.Content(content)
		}
	}
	return ""
}
