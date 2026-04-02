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

// FullAnalysis contains both symbols and calls from a single parse pass.
type FullAnalysis struct {
	Symbols []*Symbol
	Calls   *ParsedCalls
}

// AnalyzeFull extracts symbols, calls, imports, and exports in a single tree-sitter parse.
// This is ~2x faster than calling Parse() + ExtractCalls() separately.
func (p *Parser) AnalyzeFull(content []byte, lang string) (*FullAnalysis, error) {
	parsed, err := p.Parse(content, lang)
	if err != nil {
		return nil, err
	}

	result := &FullAnalysis{
		Symbols: parsed.Symbols,
	}

	calls := &ParsedCalls{
		Calls:   make([]*CallSite, 0),
		Imports: make([]*Import, 0),
		Exports: make([]string, 0),
	}

	root := parsed.Tree.RootNode()

	switch lang {
	case "go", "golang":
		calls.Imports = extractGoImports(root, content)
		calls.Calls = extractGoCallSites(root, content)
		calls.Exports = extractGoExports(root, content)
	case "rb", "ruby":
		calls.Imports = extractRubyImports(root, content)
		calls.Calls = extractRubyCallSites(root, content)
		calls.Exports = extractRubyExports(root, content)
	case "rs", "rust":
		calls.Imports = extractRustImports(root, content)
		calls.Calls = extractRustCallSites(root, content)
		calls.Exports = extractRustExports(root, content)
	case "sql":
		// no-op
	default:
		// JavaScript/TypeScript/Python/PHP/C#
		calls.Imports = extractImports(root, content)
		calls.Calls = extractCallSites(root, content)
		calls.Exports = extractExports(root, content)
	}

	result.Calls = calls
	return result, nil
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
	case "sql":
		// SQL has no imports/exports/calls in the traditional sense
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

	callee := node.Child(0) // First child is the thing being called

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
		// (foo)() - unwrap and find the identifier inside parentheses
		for i := 0; i < int(callee.ChildCount()); i++ {
			inner := callee.Child(i)
			if inner != nil && inner.Type() == "identifier" {
				call.CalleeName = inner.Content(content)
				break
			}
		}

	default:
		// Other cases: new_expression, await_expression, etc.
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
		case "export_statement":
			// Re-export: export { x } from './y' or export * from './y'
			imp := parseReexportSource(n, content)
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
		}
	}

	if imp.Source == "" {
		return nil
	}

	return imp
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

		}
	}
}

// parseDynamicImport checks for import("./foo") calls.
func parseDynamicImport(node *sitter.Node, content []byte) *Import {
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
	var source string
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			source = strings.Trim(child.Content(content), "\"'`")
			break
		}
	}

	return &Import{
		Source:     source,
		IsRelative: strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/"),
		Named:      make(map[string]string),
		Range:      nodeRange(node),
	}
}

// parseRequireCall checks for CommonJS require("./foo") calls.
// Handles:
//   - require('./foo')
//   - const foo = require('./foo')
//   - const { a, b } = require('./foo')
func parseRequireCall(node *sitter.Node, content []byte) *Import {
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
	var source string
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			source = strings.Trim(child.Content(content), "\"'`")
			break
		}
	}

	return &Import{
		Source:     source,
		IsRelative: strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/"),
		Named:      make(map[string]string),
		Range:      nodeRange(node),
	}
}

// parseReexportSource extracts the source path from a re-export statement.
// Handles:
//   - export { a, b } from './foo'
//   - export * from './foo'
//   - export { default as Foo } from './foo'
//
// Skips plain exports like `export const x = ...` (no from clause = no string child).
func parseReexportSource(node *sitter.Node, content []byte) *Import {
	// A re-export has a `string` child containing the source path.
	// Plain exports (export const x, export function f) do not have one.
	var source string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string" {
			source = strings.Trim(child.Content(content), "\"'`")
			break
		}
	}
	if source == "" {
		return nil
	}

	return &Import{
		Source:     source,
		IsRelative: strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/"),
		Named:      make(map[string]string),
		Range:      nodeRange(node),
	}
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
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name = child.Content(content)
			break
		}
	}
	return name
}

func extractClassName(node *sitter.Node, content []byte) string {
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			name = child.Content(content)
			break
		}
	}
	return name
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
	// First child is the function being called
	callee := node.Child(0)

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
		for i := 0; i < int(callee.ChildCount()); i++ {
			inner := callee.Child(i)
			if inner != nil && inner.Type() == "identifier" {
				call.CalleeName = inner.Content(content)
				break
			}
		}

	default:
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

// extractRubyImports finds all require/require_relative/load statements in Ruby source,
// plus inferred imports from constant references (for Rails Zeitwerk autoloading).
func extractRubyImports(node *sitter.Node, content []byte) []*Import {
	var imports []*Import
	seen := make(map[string]bool)

	// Track which constants are defined in this file (don't import yourself)
	localConstants := make(map[string]bool)

	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}

		switch n.Type() {
		case "call":
			imp := parseRubyRequireCall(n, content)
			if imp != nil {
				imports = append(imports, imp)
			}
		case "class":
			// Track locally defined classes
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "constant" || child.Type() == "scope_resolution" {
					localConstants[child.Content(content)] = true
				}
			}
		case "module":
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child.Type() == "constant" || child.Type() == "scope_resolution" {
					localConstants[child.Content(content)] = true
				}
			}
		}
	}

	// Second pass: find constant references that aren't locally defined.
	// These are inferred imports via Rails autoloading (Zeitwerk).
	// e.g. referencing User in a controller means app/models/user.rb
	iter2 := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter2.Next()
		if err != nil || n == nil {
			break
		}

		var constName string

		switch n.Type() {
		case "constant":
			// Skip constants that are part of a scope_resolution (handled as full path)
			parent := n.Parent()
			if parent != nil && parent.Type() == "scope_resolution" {
				continue
			}
			// Skip if this constant is part of a class/module definition
			if parent != nil && (parent.Type() == "class" || parent.Type() == "module") {
				isDefinition := false
				for i := 0; i < int(parent.ChildCount()); i++ {
					child := parent.Child(i)
					if child.Type() == "constant" || child.Type() == "scope_resolution" {
						if child == n {
							isDefinition = true
						}
						break
					}
				}
				if isDefinition {
					continue
				}
			}
			// Skip superclass references (e.g. < ApplicationRecord)
			if parent != nil && parent.Type() == "superclass" {
				continue
			}
			constName = n.Content(content)

		case "scope_resolution":
			// e.g. ActiveRecord::Base, Admin::UsersController
			// Skip nested scope_resolutions (only emit the outermost)
			parent := n.Parent()
			if parent != nil && parent.Type() == "scope_resolution" {
				continue
			}
			constName = n.Content(content)
			// Skip if this is a class/module definition name
			if parent != nil && (parent.Type() == "class" || parent.Type() == "module") {
				isDefinition := false
				for i := 0; i < int(parent.ChildCount()); i++ {
					child := parent.Child(i)
					if child.Type() == "constant" || child.Type() == "scope_resolution" {
						if child == n {
							isDefinition = true
						}
						break
					}
				}
				if isDefinition {
					continue
				}
			}
		}

		if constName == "" {
			continue
		}

		// Skip locally defined constants
		if localConstants[constName] {
			continue
		}
		// Skip common Ruby/Rails base constants that aren't project files
		if isRubyStdlibConstant(constName) {
			continue
		}
		// Deduplicate
		if seen[constName] {
			continue
		}
		seen[constName] = true

		imports = append(imports, &Import{
			Source:     "autoload:" + constName,
			IsRelative: false,
			Default:    constName,
			Named:      make(map[string]string),
			Range:      nodeRange(n),
		})
	}

	// Third pass: infer model dependencies from Rails association DSL calls.
	// has_many :posts -> Post, belongs_to :user -> User, has_one :profile -> Profile
	iter3 := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter3.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() != "call" {
			continue
		}

		var methodName, symbolArg string
		for i := 0; i < int(n.ChildCount()); i++ {
			child := n.Child(i)
			switch child.Type() {
			case "identifier":
				if methodName == "" {
					methodName = child.Content(content)
				}
			case "argument_list":
				for j := 0; j < int(child.ChildCount()); j++ {
					arg := child.Child(j)
					if arg.Type() == "simple_symbol" || arg.Type() == "symbol" {
						symbolArg = strings.TrimPrefix(arg.Content(content), ":")
						break
					}
				}
			case "simple_symbol", "symbol":
				if symbolArg == "" {
					symbolArg = strings.TrimPrefix(child.Content(content), ":")
				}
			}
		}

		if symbolArg == "" {
			continue
		}

		// Convert association name to model constant
		var constName string
		switch methodName {
		case "has_many", "has_and_belongs_to_many":
			// has_many :posts -> Post (singularize)
			constName = rubyInflectSingularize(symbolArg)
		case "belongs_to", "has_one":
			// belongs_to :user -> User
			constName = symbolArg
		default:
			continue
		}

		constName = rubySnakeToCamel(constName)
		if constName == "" || seen[constName] || localConstants[constName] || isRubyStdlibConstant(constName) {
			continue
		}
		seen[constName] = true

		imports = append(imports, &Import{
			Source:     "autoload:" + constName,
			IsRelative: false,
			Default:    constName,
			Named:      make(map[string]string),
			Range:      nodeRange(n),
		})
	}

	return imports
}

// rubySnakeToCamel converts snake_case to CamelCase.
func rubySnakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(p[:1]))
		result.WriteString(p[1:])
	}
	return result.String()
}

// rubyInflectSingularize does a basic English singularization for Rails conventions.
func rubyInflectSingularize(s string) string {
	if strings.HasSuffix(s, "ies") {
		return strings.TrimSuffix(s, "ies") + "y"
	}
	if strings.HasSuffix(s, "sses") {
		return strings.TrimSuffix(s, "es")
	}
	if strings.HasSuffix(s, "ses") {
		return strings.TrimSuffix(s, "es")
	}
	if strings.HasSuffix(s, "ves") {
		return strings.TrimSuffix(s, "ves") + "f"
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && !strings.HasSuffix(s, "us") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// isRubyStdlibConstant returns true for constants from Ruby stdlib or Rails framework
// that should not be resolved as project file imports.
func isRubyStdlibConstant(name string) bool {
	// Strip scope resolution to check the root constant
	root := name
	if idx := strings.Index(name, "::"); idx > 0 {
		root = name[:idx]
	}
	stdlib := map[string]bool{
		// Ruby stdlib
		"Object": true, "String": true, "Integer": true, "Float": true,
		"Array": true, "Hash": true, "Symbol": true, "NilClass": true,
		"TrueClass": true, "FalseClass": true, "Numeric": true,
		"Comparable": true, "Enumerable": true, "Enumerator": true,
		"Kernel": true, "Module": true, "Class": true, "BasicObject": true,
		"Proc": true, "Method": true, "IO": true, "File": true, "Dir": true,
		"Regexp": true, "Range": true, "Struct": true, "Exception": true,
		"StandardError": true, "RuntimeError": true, "ArgumentError": true,
		"TypeError": true, "NameError": true, "NoMethodError": true,
		"NotImplementedError": true, "Errno": true, "SystemCallError": true,
		"Thread": true, "Mutex": true, "Fiber": true, "Time": true,
		"Date": true, "DateTime": true, "Set": true, "OpenStruct": true,
		"StringIO": true, "Tempfile": true, "Logger": true, "URI": true,
		"JSON": true, "YAML": true, "CSV": true, "ERB": true,
		"Digest": true, "Base64": true, "SecureRandom": true,
		"Net": true, "OpenSSL": true, "Socket": true, "Pathname": true,
		"FileUtils": true, "Benchmark": true, "Forwardable": true,
		"Singleton": true, "Observable": true, "Delegator": true,
		"SimpleDelegator": true, "BigDecimal": true, "Rational": true,
		"Complex": true, "Math": true, "GC": true, "ObjectSpace": true,
		"Marshal": true, "Encoding": true, "ENV": true, "ARGV": true,
		"STDIN": true, "STDOUT": true, "STDERR": true,
		// Rails framework
		"ActiveRecord": true, "ActiveModel": true, "ActiveSupport": true,
		"ActiveStorage": true, "ActiveJob": true, "ActionController": true,
		"ActionView": true, "ActionMailer": true, "ActionCable": true,
		"ActionDispatch": true, "ActionPack": true, "ActionText": true,
		"ActionMailbox": true, "ApplicationRecord": true, "ApplicationController": true,
		"ApplicationMailer": true, "ApplicationJob": true,
		"ApplicationHelper": true, "ApplicationCable": true,
		"Rails": true, "Rack": true, "Mime": true, "Arel": true,
		"Sprockets": true, "Webpacker": true, "Turbo": true, "Stimulus": true,
		"Devise": true, "Pundit": true, "CanCanCan": true, "Doorkeeper": true,
		"Sidekiq": true, "Resque": true, "DelayedJob": true,
		"RSpec": true, "FactoryBot": true, "Faker": true, "Capybara": true,
		"Minitest": true, "Test": true,
		"Bundler": true, "Gem": true, "Rake": true,
		"I18n": true, "Concurrent": true, "Zeitwerk": true,
	}
	return stdlib[root]
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
			// e.g., self.method or obj.func
			call.IsMethodCall = true
			for j := 0; j < int(child.ChildCount()); j++ {
				fc := child.Child(j)
				switch fc.Type() {
				case "field_identifier":
					call.CalleeName = fc.Content(content)
				case "identifier":
					if call.CalleeObject == "" {
						call.CalleeObject = fc.Content(content)
					}
				default:
					if call.CalleeObject == "" {
						call.CalleeObject = "(expr)"
					}
				}
			}
		}
	}

	return call
}

// parseRustMethodCall parses method call expressions.
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
	var name string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == nodeType {
			name = child.Content(content)
			break
		}
	}
	return name
}
