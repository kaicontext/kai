// Package detect provides change type detection for code changes.
package detect

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"kai-core/cas"
	"kai-core/graph"
	"kai-core/parse"
)

// functionNodeTypes maps each language to the AST node types that represent
// function-like declarations (functions, methods, etc.)
var functionNodeTypes = map[string][]string{
	"js": {
		"function_declaration", // function foo() {}
		"method_definition",    // class methods
		"lexical_declaration",  // const foo = () => {}
		"variable_declaration", // var foo = function() {}
	},
	"ts": {
		"function_declaration",
		"method_definition",
		"lexical_declaration",
		"variable_declaration",
	},
	"py": {
		"function_definition", // Both standalone functions and methods
	},
	"go": {
		"function_declaration", // func Foo() {}
		"method_declaration",   // func (T) Method() {}
	},
	"rb": {
		"method",           // def foo
		"singleton_method", // def self.foo
	},
	"rs": {
		"function_item",
	},
}

// ChangeCategory represents a type of change.
type ChangeCategory string

const (
	// Code-level semantic changes (JS/TS)
	ConditionChanged  ChangeCategory = "CONDITION_CHANGED"
	ConstantUpdated   ChangeCategory = "CONSTANT_UPDATED"
	APISurfaceChanged ChangeCategory = "API_SURFACE_CHANGED"
	FunctionAdded     ChangeCategory = "FUNCTION_ADDED"
	FunctionRemoved   ChangeCategory = "FUNCTION_REMOVED"

	// File-level changes (fallback for non-parsed files)
	FileContentChanged ChangeCategory = "FILE_CONTENT_CHANGED"
	FileAdded          ChangeCategory = "FILE_ADDED"
	FileDeleted        ChangeCategory = "FILE_DELETED"

	// JSON-specific changes
	JSONFieldAdded   ChangeCategory = "JSON_FIELD_ADDED"
	JSONFieldRemoved ChangeCategory = "JSON_FIELD_REMOVED"
	JSONValueChanged ChangeCategory = "JSON_VALUE_CHANGED"
	JSONArrayChanged ChangeCategory = "JSON_ARRAY_CHANGED"

	// YAML-specific changes (future)
	YAMLKeyAdded     ChangeCategory = "YAML_KEY_ADDED"
	YAMLKeyRemoved   ChangeCategory = "YAML_KEY_REMOVED"
	YAMLValueChanged ChangeCategory = "YAML_VALUE_CHANGED"

	// Enhanced function changes
	FunctionRenamed     ChangeCategory = "FUNCTION_RENAMED"
	FunctionBodyChanged ChangeCategory = "FUNCTION_BODY_CHANGED"

	// Parameter changes
	ParameterAdded   ChangeCategory = "PARAMETER_ADDED"
	ParameterRemoved ChangeCategory = "PARAMETER_REMOVED"

	// Import changes
	ImportAdded   ChangeCategory = "IMPORT_ADDED"
	ImportRemoved ChangeCategory = "IMPORT_REMOVED"

	// Dependency changes (for package.json, etc.)
	DependencyAdded   ChangeCategory = "DEPENDENCY_ADDED"
	DependencyRemoved ChangeCategory = "DEPENDENCY_REMOVED"
	DependencyUpdated ChangeCategory = "DEPENDENCY_UPDATED"

	// Semantic config changes
	FeatureFlagChanged ChangeCategory = "FEATURE_FLAG_CHANGED"
	TimeoutChanged     ChangeCategory = "TIMEOUT_CHANGED"
	LimitChanged       ChangeCategory = "LIMIT_CHANGED"
	RetryConfigChanged ChangeCategory = "RETRY_CONFIG_CHANGED"
	EndpointChanged    ChangeCategory = "ENDPOINT_CHANGED"
	CredentialChanged  ChangeCategory = "CREDENTIAL_CHANGED"

	// Schema/migration changes
	SchemaFieldAdded   ChangeCategory = "SCHEMA_FIELD_ADDED"
	SchemaFieldRemoved ChangeCategory = "SCHEMA_FIELD_REMOVED"
	SchemaFieldChanged ChangeCategory = "SCHEMA_FIELD_CHANGED"
	MigrationAdded     ChangeCategory = "MIGRATION_ADDED"
)

// FileRange represents a range in a file.
type FileRange struct {
	Path  string `json:"path"`
	Start [2]int `json:"start"`
	End   [2]int `json:"end"`
}

// Evidence contains the evidence for a change type detection.
type Evidence struct {
	FileRanges []FileRange `json:"fileRanges"`
	Symbols    []string    `json:"symbols"` // symbol node IDs as hex
}

// ChangeType represents a detected change type.
type ChangeType struct {
	Category ChangeCategory
	Evidence Evidence
}

// Detector detects change types between two versions of a file.
type Detector struct {
	parser  *parse.Parser
	symbols map[string][]*graph.Node // fileID -> symbols
}

// NewDetector creates a new change detector.
func NewDetector() *Detector {
	return &Detector{
		parser:  parse.NewParser(),
		symbols: make(map[string][]*graph.Node),
	}
}

// SetSymbols sets the symbols for a file (used for mapping changes to symbols).
func (d *Detector) SetSymbols(fileID string, symbols []*graph.Node) {
	d.symbols[fileID] = symbols
}

// DetectChanges detects all change types between two versions of a file.
// The lang parameter specifies the language for proper parsing (e.g., "py", "js", "ts").
func (d *Detector) DetectChanges(path string, beforeContent, afterContent []byte, fileID string, lang string) ([]*ChangeType, error) {
	beforeParsed, err := d.parser.Parse(beforeContent, lang)
	if err != nil {
		return nil, fmt.Errorf("parsing before: %w", err)
	}

	afterParsed, err := d.parser.Parse(afterContent, lang)
	if err != nil {
		return nil, fmt.Errorf("parsing after: %w", err)
	}

	var changes []*ChangeType

	// Detect function additions/removals (most important for intent)
	funcChanges := d.detectFunctionChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID, lang)
	changes = append(changes, funcChanges...)

	// Detect condition changes
	condChanges := d.detectConditionChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, condChanges...)

	// Detect constant updates
	constChanges := d.detectConstantUpdates(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, constChanges...)

	// Detect API surface changes
	apiChanges := d.detectAPISurfaceChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID, lang)
	changes = append(changes, apiChanges...)

	return changes, nil
}

// detectFunctionChanges detects added, removed, or modified functions.
func (d *Detector) detectFunctionChanges(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string, lang string) []*ChangeType {
	var changes []*ChangeType

	// Get all function declarations from both versions
	beforeFuncs := GetAllFunctions(before, beforeContent, lang)
	afterFuncs := GetAllFunctions(after, afterContent, lang)

	// Check for added functions
	for name, afterFunc := range afterFuncs {
		if _, exists := beforeFuncs[name]; !exists {
			afterRange := parse.GetNodeRange(afterFunc.Node)
			// Get symbol IDs and always include the function name for intent generation
			symbolIDs := d.findOverlappingSymbols(fileID, afterRange)
			symbols := append([]string{"name:" + name}, symbolIDs...)
			change := &ChangeType{
				Category: FunctionAdded,
				Evidence: Evidence{
					FileRanges: []FileRange{{
						Path:  path,
						Start: afterRange.Start,
						End:   afterRange.End,
					}},
					Symbols: symbols,
				},
			}
			changes = append(changes, change)
		}
	}

	// Check for removed functions
	for name, beforeFunc := range beforeFuncs {
		if _, exists := afterFuncs[name]; !exists {
			beforeRange := parse.GetNodeRange(beforeFunc.Node)
			change := &ChangeType{
				Category: FunctionRemoved,
				Evidence: Evidence{
					FileRanges: []FileRange{{
						Path:  path,
						Start: beforeRange.Start,
						End:   beforeRange.End,
					}},
					Symbols: []string{"name:" + name},
				},
			}
			changes = append(changes, change)
		}
	}

	// Check for body changes in functions that exist in both versions
	for name, beforeFunc := range beforeFuncs {
		if afterFunc, exists := afterFuncs[name]; exists {
			// Compare function bodies
			if beforeFunc.Body != afterFunc.Body && beforeFunc.Body != "" && afterFunc.Body != "" {
				afterRange := parse.GetNodeRange(afterFunc.Node)
				symbolIDs := d.findOverlappingSymbols(fileID, afterRange)
				symbols := append([]string{"name:" + name}, symbolIDs...)
				change := &ChangeType{
					Category: FunctionBodyChanged,
					Evidence: Evidence{
						FileRanges: []FileRange{{
							Path:  path,
							Start: afterRange.Start,
							End:   afterRange.End,
						}},
						Symbols: symbols,
					},
				}
				changes = append(changes, change)
			}
		}
	}

	return changes
}

// FuncInfo holds information about a function declaration.
// Exported for use by rename detection.
type FuncInfo struct {
	Name string
	Node *sitter.Node
	Body string // function body text for similarity comparison
}

// GetAllFunctions extracts all function declarations from a parsed file.
// Exported for use by rename detection. The lang parameter selects
// language-appropriate AST node types (defaults to JS/TS).
func GetAllFunctions(parsed *parse.ParsedFile, content []byte, lang ...string) map[string]*FuncInfo {
	funcs := make(map[string]*FuncInfo)

	l := "js"
	if len(lang) > 0 && lang[0] != "" {
		l = lang[0]
	}

	nodeTypes, ok := functionNodeTypes[l]
	if !ok {
		// Fallback to JS if language not in map
		nodeTypes = functionNodeTypes["js"]
	}

	// Search for all node types for this language
	for _, nodeType := range nodeTypes {
		for _, node := range parsed.FindNodesOfType(nodeType) {
			var name string
			var bodyNode *sitter.Node

			// Handle special cases per node type
			switch nodeType {
			case "lexical_declaration":
				// JS/TS: const foo = () => {}
				name, bodyNode = getArrowFunctionName(node, content)
			case "variable_declaration":
				// JS/TS: var foo = function() {}
				name, bodyNode = getVariableFunctionName(node, content)
			case "singleton_method":
				// Ruby: def self.foo
				name = getFunctionName(node, content)
				if name != "" {
					name = "self." + name
				}
				bodyNode = node
			case "method_declaration":
				// Go: func (T) Method() {}
				name = getGoMethodName(node, content)
				bodyNode = node
			case "function_definition":
				// Python: check if inside a class
				name = getPythonFunctionName(node, content)
				bodyNode = node
			case "method":
				// Ruby: check if inside a class
				name = getRubyMethodName(node, content)
				bodyNode = node
			case "function_item":
				// Rust: check if inside impl block
				name = getRustFunctionName(node, content)
				bodyNode = node
			default:
				// Standard function extraction
				name = getFunctionName(node, content)
				bodyNode = node
			}

			if name != "" && bodyNode != nil {
				body := getFunctionBody(bodyNode, content)
				funcs[name] = &FuncInfo{Name: name, Node: node, Body: body}
			}
		}
	}

	return funcs
}

// getFunctionBody extracts the body content of a function.
func getFunctionBody(node *sitter.Node, content []byte) string {
	// Find the statement_block or body node
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "statement_block", "block", "expression_statement", "body_statement":
			return parse.GetNodeContent(child, content)
		}
	}
	// For arrow functions without braces, the body is the expression
	if node.Type() == "arrow_function" {
		// The body is usually the last child
		if node.ChildCount() > 0 {
			lastChild := node.Child(int(node.ChildCount()) - 1)
			if lastChild.Type() != "formal_parameters" && lastChild.Type() != "=>" {
				return parse.GetNodeContent(lastChild, content)
			}
		}
	}
	return ""
}

// getArrowFunctionName extracts the name from an arrow function assignment.
func getArrowFunctionName(node *sitter.Node, content []byte) (string, *sitter.Node) {
	// Look for: const/let NAME = () => {}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			var name string
			var arrowNode *sitter.Node
			for j := 0; j < int(child.ChildCount()); j++ {
				c := child.Child(j)
				if c.Type() == "identifier" {
					name = parse.GetNodeContent(c, content)
				}
				if c.Type() == "arrow_function" {
					arrowNode = c
				}
			}
			if name != "" && arrowNode != nil {
				return name, arrowNode
			}
		}
	}
	return "", nil
}

// getVariableFunctionName extracts the name from a function expression assignment.
func getVariableFunctionName(node *sitter.Node, content []byte) (string, *sitter.Node) {
	// Look for: var NAME = function() {}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			var name string
			var funcNode *sitter.Node
			for j := 0; j < int(child.ChildCount()); j++ {
				c := child.Child(j)
				if c.Type() == "identifier" {
					name = parse.GetNodeContent(c, content)
				}
				if c.Type() == "function" || c.Type() == "function_expression" {
					funcNode = c
				}
			}
			if name != "" && funcNode != nil {
				return name, funcNode
			}
		}
	}
	return "", nil
}

// detectConditionChanges detects changes in binary/logical/relational expressions.
func (d *Detector) detectConditionChanges(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	// Node types that represent conditions
	conditionTypes := []string{"binary_expression", "logical_expression", "relational_expression"}

	beforeNodes := make(map[string][]*sitter.Node)
	afterNodes := make(map[string][]*sitter.Node)

	for _, nodeType := range conditionTypes {
		beforeNodes[nodeType] = before.FindNodesOfType(nodeType)
		afterNodes[nodeType] = after.FindNodesOfType(nodeType)
	}

	// Compare nodes by approximate position
	for _, nodeType := range conditionTypes {
		for _, beforeNode := range beforeNodes[nodeType] {
			beforeRange := parse.GetNodeRange(beforeNode)
			beforeText := parse.GetNodeContent(beforeNode, beforeContent)

			// Find a corresponding node in after (by line proximity)
			for _, afterNode := range afterNodes[nodeType] {
				afterRange := parse.GetNodeRange(afterNode)

				// Check if they're on the same or nearby lines
				if abs(beforeRange.Start[0]-afterRange.Start[0]) <= 2 {
					afterText := parse.GetNodeContent(afterNode, afterContent)

					// Compare the expressions
					if beforeText != afterText {
						// Check if operator or boundary changed
						if hasOperatorOrBoundaryChange(beforeNode, afterNode, beforeContent, afterContent) {
							change := &ChangeType{
								Category: ConditionChanged,
								Evidence: Evidence{
									FileRanges: []FileRange{{
										Path:  path,
										Start: afterRange.Start,
										End:   afterRange.End,
									}},
									Symbols: d.findOverlappingSymbols(fileID, afterRange),
								},
							}
							changes = append(changes, change)
						}
					}
				}
			}
		}
	}

	return changes
}

// detectConstantUpdates detects changes in literal values.
func (d *Detector) detectConstantUpdates(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	literalTypes := []string{"number", "string"}

	for _, nodeType := range literalTypes {
		beforeNodes := before.FindNodesOfType(nodeType)
		afterNodes := after.FindNodesOfType(nodeType)

		for _, beforeNode := range beforeNodes {
			beforeRange := parse.GetNodeRange(beforeNode)
			beforeText := parse.GetNodeContent(beforeNode, beforeContent)

			for _, afterNode := range afterNodes {
				afterRange := parse.GetNodeRange(afterNode)

				// Match by line proximity
				if abs(beforeRange.Start[0]-afterRange.Start[0]) <= 2 &&
					abs(beforeRange.Start[1]-afterRange.Start[1]) <= 10 {
					afterText := parse.GetNodeContent(afterNode, afterContent)

					if beforeText != afterText {
						change := &ChangeType{
							Category: ConstantUpdated,
							Evidence: Evidence{
								FileRanges: []FileRange{{
									Path:  path,
									Start: afterRange.Start,
									End:   afterRange.End,
								}},
								Symbols: d.findOverlappingSymbols(fileID, afterRange),
							},
						}
						changes = append(changes, change)
					}
				}
			}
		}
	}

	return changes
}

// detectAPISurfaceChanges detects changes in function signatures or exports.
func (d *Detector) detectAPISurfaceChanges(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string, lang string) []*ChangeType {
	var changes []*ChangeType

	// Check function declarations
	funcChanges := d.compareFunctions(path, before, after, beforeContent, afterContent, fileID, lang)
	changes = append(changes, funcChanges...)

	// Check export statements
	exportChanges := d.compareExports(path, before, after, beforeContent, afterContent, fileID, lang)
	changes = append(changes, exportChanges...)

	return changes
}

func (d *Detector) compareFunctions(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string, lang string) []*ChangeType {
	var changes []*ChangeType

	var beforeFuncs, afterFuncs []*sitter.Node

	switch lang {
	case "rb":
		beforeFuncs = before.FindNodesOfType("method")
		afterFuncs = after.FindNodesOfType("method")
		beforeFuncs = append(beforeFuncs, before.FindNodesOfType("singleton_method")...)
		afterFuncs = append(afterFuncs, after.FindNodesOfType("singleton_method")...)
	case "py":
		beforeFuncs = before.FindNodesOfType("function_definition")
		afterFuncs = after.FindNodesOfType("function_definition")
	default:
		beforeFuncs = before.FindNodesOfType("function_declaration")
		afterFuncs = after.FindNodesOfType("function_declaration")
		// Also check arrow functions and method definitions
		beforeFuncs = append(beforeFuncs, before.FindNodesOfType("method_definition")...)
		afterFuncs = append(afterFuncs, after.FindNodesOfType("method_definition")...)
	}

	// Build a map of function names to nodes
	beforeByName := make(map[string]*sitter.Node)
	afterByName := make(map[string]*sitter.Node)

	for _, node := range beforeFuncs {
		name := getFunctionName(node, beforeContent)
		if name != "" {
			beforeByName[name] = node
		}
	}

	for _, node := range afterFuncs {
		name := getFunctionName(node, afterContent)
		if name != "" {
			afterByName[name] = node
		}
	}

	// Compare functions with same name
	for name, beforeFunc := range beforeByName {
		if afterFunc, ok := afterByName[name]; ok {
			beforeParams := getFunctionParams(beforeFunc, beforeContent)
			afterParams := getFunctionParams(afterFunc, afterContent)

			if beforeParams != afterParams {
				afterRange := parse.GetNodeRange(afterFunc)
				change := &ChangeType{
					Category: APISurfaceChanged,
					Evidence: Evidence{
						FileRanges: []FileRange{{
							Path:  path,
							Start: afterRange.Start,
							End:   afterRange.End,
						}},
						Symbols: d.findOverlappingSymbols(fileID, afterRange),
					},
				}
				changes = append(changes, change)
			}
		}
	}

	return changes
}

func (d *Detector) compareExports(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string, lang string) []*ChangeType {
	var changes []*ChangeType

	// Get exported identifiers based on language
	beforeSet := d.getExportedIdentifiers(before, beforeContent, lang)
	afterSet := d.getExportedIdentifiers(after, afterContent, lang)

	// DEBUG: Print what we found
	// fmt.Printf("DEBUG compareExports lang=%s before=%v after=%v\n", lang, beforeSet, afterSet)

	// Check for differences
	hasDiff := false
	for id := range beforeSet {
		if !afterSet[id] {
			hasDiff = true
			break
		}
	}
	if !hasDiff {
		for id := range afterSet {
			if !beforeSet[id] {
				hasDiff = true
				break
			}
		}
	}

	// If there are differences, create a change event
	if hasDiff {
		// Try to get a meaningful range from the after file
		var changeRange parse.Range
		hasRange := false

		switch lang {
		case "js", "ts":
			afterExports := after.FindNodesOfType("export_statement")
			if len(afterExports) > 0 {
				changeRange = parse.GetNodeRange(afterExports[0])
				hasRange = true
			}
		case "go":
			// Use first exported function/type as range
			funcs := after.FindNodesOfType("function_declaration")
			types := after.FindNodesOfType("type_declaration")
			if len(funcs) > 0 {
				changeRange = parse.GetNodeRange(funcs[0])
				hasRange = true
			} else if len(types) > 0 {
				changeRange = parse.GetNodeRange(types[0])
				hasRange = true
			}
		case "rs":
			// Use first pub item as range
			funcs := after.FindNodesOfType("function_item")
			for _, fn := range funcs {
				if hasVisibilityModifier(fn) {
					changeRange = parse.GetNodeRange(fn)
					hasRange = true
					break
				}
			}
		case "py":
			// Use first function or __all__ as range
			all := after.FindNodesOfType("assignment")
			funcs := after.FindNodesOfType("function_definition")
			if len(all) > 0 {
				changeRange = parse.GetNodeRange(all[0])
				hasRange = true
			} else if len(funcs) > 0 {
				changeRange = parse.GetNodeRange(funcs[0])
				hasRange = true
			}
		case "rb":
			// Use first class as range
			classes := after.FindNodesOfType("class")
			if len(classes) > 0 {
				changeRange = parse.GetNodeRange(classes[0])
				hasRange = true
			}
		}

		if hasRange {
			change := &ChangeType{
				Category: APISurfaceChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{
						Path:  path,
						Start: changeRange.Start,
						End:   changeRange.End,
					}},
					Symbols: d.findOverlappingSymbols(fileID, changeRange),
				},
			}
			changes = append(changes, change)
		}
	}

	return changes
}

func (d *Detector) findOverlappingSymbols(fileID string, r parse.Range) []string {
	symbols, ok := d.symbols[fileID]
	if !ok {
		return nil
	}

	var result []string
	for _, sym := range symbols {
		rangeData, ok := sym.Payload["range"].(map[string]interface{})
		if !ok {
			continue
		}

		startArr, ok1 := rangeData["start"].([]interface{})
		endArr, ok2 := rangeData["end"].([]interface{})
		if !ok1 || !ok2 || len(startArr) != 2 || len(endArr) != 2 {
			continue
		}

		symRange := parse.Range{
			Start: [2]int{int(startArr[0].(float64)), int(startArr[1].(float64))},
			End:   [2]int{int(endArr[0].(float64)), int(endArr[1].(float64))},
		}

		if parse.RangesOverlap(r, symRange) {
			result = append(result, cas.BytesToHex(sym.ID))
		}
	}

	return result
}

func hasOperatorOrBoundaryChange(before, after *sitter.Node, beforeContent, afterContent []byte) bool {
	// Check if operator differs
	beforeOp := findOperator(before, beforeContent)
	afterOp := findOperator(after, afterContent)
	if beforeOp != afterOp {
		return true
	}

	// Check if numeric literals in the expression differ
	beforeNums := findNumbers(before, beforeContent)
	afterNums := findNumbers(after, afterContent)
	if !equalStringSlices(beforeNums, afterNums) {
		return true
	}

	return false
}

func findOperator(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case ">", "<", ">=", "<=", "==", "===", "!=", "!==", "&&", "||", "+", "-", "*", "/":
			return child.Type()
		}
		// Check the actual content for operator-like nodes
		childContent := parse.GetNodeContent(child, content)
		switch childContent {
		case ">", "<", ">=", "<=", "==", "===", "!=", "!==", "&&", "||":
			return childContent
		}
	}
	return ""
}

func findNumbers(node *sitter.Node, content []byte) []string {
	var nums []string
	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() == "number" {
			nums = append(nums, parse.GetNodeContent(n, content))
		}
	}
	return nums
}

func getFunctionName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" || child.Type() == "property_identifier" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}

// getGoMethodName extracts the qualified name for a Go method (e.g., "User.login")
func getGoMethodName(node *sitter.Node, content []byte) string {
	var receiverType string
	var methodName string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "parameter_list":
			// First parameter_list is the receiver
			if receiverType == "" {
				receiverType = getGoReceiverType(child, content)
			}
		case "field_identifier":
			methodName = parse.GetNodeContent(child, content)
		}
	}

	if methodName == "" {
		return ""
	}

	if receiverType != "" {
		return receiverType + "." + methodName
	}
	return methodName
}

// getGoReceiverType extracts the type from a Go method receiver parameter list
func getGoReceiverType(paramList *sitter.Node, content []byte) string {
	for i := 0; i < int(paramList.ChildCount()); i++ {
		child := paramList.Child(i)
		if child.Type() == "parameter_declaration" {
			for j := 0; j < int(child.ChildCount()); j++ {
				typeChild := child.Child(j)
				switch typeChild.Type() {
				case "type_identifier":
					return parse.GetNodeContent(typeChild, content)
				case "pointer_type":
					// Extract base type from pointer (e.g., "*User" -> "User")
					for k := 0; k < int(typeChild.ChildCount()); k++ {
						ptrChild := typeChild.Child(k)
						if ptrChild.Type() == "type_identifier" {
							return parse.GetNodeContent(ptrChild, content)
						}
					}
				}
			}
		}
	}
	return ""
}

// getPythonFunctionName extracts qualified name for Python functions (e.g., "User.login")
func getPythonFunctionName(node *sitter.Node, content []byte) string {
	funcName := getFunctionName(node, content)
	if funcName == "" {
		return ""
	}

	// Check if this function is inside a class
	parent := node.Parent()
	for parent != nil {
		if parent.Type() == "class_definition" {
			// Found parent class, get its name
			for i := 0; i < int(parent.ChildCount()); i++ {
				child := parent.Child(i)
				if child.Type() == "identifier" {
					className := parse.GetNodeContent(child, content)
					return className + "." + funcName
				}
			}
		}
		parent = parent.Parent()
	}
	return funcName
}

// getRubyMethodName extracts qualified name for Ruby methods (e.g., "User#login")
func getRubyMethodName(node *sitter.Node, content []byte) string {
	methodName := getFunctionName(node, content)
	if methodName == "" {
		return ""
	}

	// Check if this method is inside a class
	parent := node.Parent()
	for parent != nil {
		if parent.Type() == "class" {
			// Found parent class, get its name
			for i := 0; i < int(parent.ChildCount()); i++ {
				child := parent.Child(i)
				if child.Type() == "constant" {
					className := parse.GetNodeContent(child, content)
					return className + "#" + methodName
				}
			}
		}
		parent = parent.Parent()
	}
	return methodName
}

// getRustFunctionName extracts qualified name for Rust functions (e.g., "User::login")
func getRustFunctionName(node *sitter.Node, content []byte) string {
	funcName := getFunctionName(node, content)
	if funcName == "" {
		return ""
	}

	// Check if this function is inside an impl block
	parent := node.Parent()
	for parent != nil {
		if parent.Type() == "impl_item" {
			// Found impl block, get the type name
			for i := 0; i < int(parent.ChildCount()); i++ {
				child := parent.Child(i)
				if child.Type() == "type_identifier" {
					typeName := parse.GetNodeContent(child, content)
					return typeName + "::" + funcName
				}
			}
		}
		parent = parent.Parent()
	}
	return funcName
}

func getFunctionParams(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "formal_parameters" || child.Type() == "method_parameters" || child.Type() == "parameters" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}

func getExportedIdentifiers(node *sitter.Node, content []byte) []string {
	var ids []string
	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() == "identifier" {
			ids = append(ids, parse.GetNodeContent(n, content))
		}
	}
	return ids
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetCategoryPayload returns the payload for a ChangeType node.
func GetCategoryPayload(ct *ChangeType) map[string]interface{} {
	fileRanges := make([]interface{}, len(ct.Evidence.FileRanges))
	for i, fr := range ct.Evidence.FileRanges {
		fileRanges[i] = map[string]interface{}{
			"path":  fr.Path,
			"start": fr.Start,
			"end":   fr.End,
		}
	}

	symbols := make([]interface{}, len(ct.Evidence.Symbols))
	for i, s := range ct.Evidence.Symbols {
		symbols[i] = s
	}

	return map[string]interface{}{
		"category": string(ct.Category),
		"evidence": map[string]interface{}{
			"fileRanges": fileRanges,
			"symbols":    symbols,
		},
	}
}

// NewFileChange creates a file-level change type (for non-parsed files).
func NewFileChange(category ChangeCategory, path string) *ChangeType {
	return &ChangeType{
		Category: category,
		Evidence: Evidence{
			FileRanges: []FileRange{{Path: path}},
		},
	}
}

// IsParseable returns true if the language supports semantic parsing.
func IsParseable(lang string) bool {
	switch lang {
	case "ts", "js", "json", "py", "yaml", "rb", "go", "rs":
		return true
	default:
		return false
	}
}

// DetectFileChange creates a FILE_CONTENT_CHANGED for non-parseable files.
func (d *Detector) DetectFileChange(path string, lang string) *ChangeType {
	return NewFileChange(FileContentChanged, path)
}

// ============================================================================
// Export Detection Helpers (Multi-Language)
// ============================================================================

// getExportedIdentifiers extracts all exported identifiers based on language-specific rules
func (d *Detector) getExportedIdentifiers(parsed *parse.ParsedFile, content []byte, lang string) map[string]bool {
	exports := make(map[string]bool)

	switch lang {
	case "js", "ts":
		// JavaScript/TypeScript: look for export statements
		exportNodes := parsed.FindNodesOfType("export_statement")
		for _, node := range exportNodes {
			ids := getJSExportedIdentifiers(node, content)
			for _, id := range ids {
				exports[id] = true
			}
		}

	case "go":
		// Go: capitalized identifiers are exported
		// Check functions
		funcs := parsed.FindNodesOfType("function_declaration")
		for _, fn := range funcs {
			name := getFunctionName(fn, content)
			if isGoExported(name) {
				exports[name] = true
			}
		}
		// Check methods
		methods := parsed.FindNodesOfType("method_declaration")
		for _, method := range methods {
			name := getGoMethodName(method, content)
			if isGoExported(name) {
				exports[name] = true
			}
		}
		// Check types
		types := parsed.FindNodesOfType("type_declaration")
		for _, typ := range types {
			name := getGoTypeName(typ, content)
			if isGoExported(name) {
				exports[name] = true
			}
		}

	case "rs":
		// Rust: pub keyword indicates exported
		// Check functions
		funcs := parsed.FindNodesOfType("function_item")
		for _, fn := range funcs {
			if hasVisibilityModifier(fn) {
				name := getFunctionName(fn, content)
				if name != "" {
					exports[name] = true
				}
			}
		}
		// Check structs
		structs := parsed.FindNodesOfType("struct_item")
		for _, st := range structs {
			if hasVisibilityModifier(st) {
				name := getRustTypeName(st, content)
				if name != "" {
					exports[name] = true
				}
			}
		}

	case "py":
		// Python: check for __all__ first, otherwise all non-_ prefixed top-level items
		allList := getPythonAllList(parsed, content)
		if len(allList) > 0 {
			// Use __all__ if present
			for _, name := range allList {
				exports[name] = true
			}
		} else {
			// No __all__, so export all non-_ prefixed top-level functions/classes
			funcs := parsed.FindNodesOfType("function_definition")
			for _, fn := range funcs {
				name := getFunctionName(fn, content)
				if name != "" && !isPythonPrivate(name) && isTopLevel(fn) {
					exports[name] = true
				}
			}
			classes := parsed.FindNodesOfType("class_definition")
			for _, cls := range classes {
				name := getPythonClassName(cls, content)
				if name != "" && !isPythonPrivate(name) && isTopLevel(cls) {
					exports[name] = true
				}
			}
		}

	case "rb":
		// Ruby: all top-level classes and modules are public
		classes := parsed.FindNodesOfType("class")
		for _, cls := range classes {
			name := getRubyClassName(cls, content)
			if name != "" {
				exports[name] = true
			}
		}
		modules := parsed.FindNodesOfType("module")
		for _, mod := range modules {
			name := getRubyModuleName(mod, content)
			if name != "" {
				exports[name] = true
			}
		}
	}

	return exports
}

// Helper functions for export detection

// getJSExportedIdentifiers extracts identifiers from JS/TS export_statement nodes
func getJSExportedIdentifiers(node *sitter.Node, content []byte) []string {
	var ids []string
	iter := sitter.NewIterator(node, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() == "identifier" {
			ids = append(ids, parse.GetNodeContent(n, content))
		}
	}
	return ids
}

// isGoExported checks if a Go identifier is exported (starts with uppercase)
func isGoExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := rune(name[0])
	return first >= 'A' && first <= 'Z'
}

// getGoTypeName extracts the type name from a Go type_declaration
func getGoTypeName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_spec" {
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "type_identifier" {
					return parse.GetNodeContent(spec, content)
				}
			}
		}
	}
	return ""
}

// hasVisibilityModifier checks if a Rust node has a pub modifier
func hasVisibilityModifier(node *sitter.Node) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "visibility_modifier" {
			return true
		}
	}
	return false
}

// getRustTypeName extracts the type name from a Rust struct_item
func getRustTypeName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_identifier" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}

// getPythonAllList extracts the __all__ list if present
func getPythonAllList(parsed *parse.ParsedFile, content []byte) []string {
	var allList []string
	assignments := parsed.FindNodesOfType("assignment")
	for _, assign := range assignments {
		// Check if this is __all__ = [...]
		for i := 0; i < int(assign.ChildCount()); i++ {
			child := assign.Child(i)
			if child.Type() == "identifier" && parse.GetNodeContent(child, content) == "__all__" {
				// Found __all__, now extract the list
				for j := 0; j < int(assign.ChildCount()); j++ {
					listNode := assign.Child(j)
					if listNode.Type() == "list" {
						allList = extractPythonListStrings(listNode, content)
						return allList
					}
				}
			}
		}
	}
	return allList
}

// extractPythonListStrings extracts string literals from a Python list node
func extractPythonListStrings(listNode *sitter.Node, content []byte) []string {
	var strings []string
	iter := sitter.NewIterator(listNode, sitter.DFSMode)
	for {
		n, err := iter.Next()
		if err != nil || n == nil {
			break
		}
		if n.Type() == "string" {
			// Remove quotes from string
			str := parse.GetNodeContent(n, content)
			if len(str) >= 2 {
				str = str[1 : len(str)-1] // Remove first and last char (quotes)
			}
			strings = append(strings, str)
		}
	}
	return strings
}

// isPythonPrivate checks if a Python identifier is private (starts with _)
func isPythonPrivate(name string) bool {
	return len(name) > 0 && name[0] == '_'
}

// isTopLevel checks if a node is at the top level (not inside a class/function)
func isTopLevel(node *sitter.Node) bool {
	parent := node.Parent()
	for parent != nil {
		parentType := parent.Type()
		// If we find a class or function parent, it's not top-level
		if parentType == "class_definition" || parentType == "function_definition" {
			return false
		}
		parent = parent.Parent()
	}
	return true
}

// getPythonClassName extracts the class name from a Python class_definition
func getPythonClassName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}

// getRubyClassName extracts the class name from a Ruby class node
func getRubyClassName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "constant" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}

// getRubyModuleName extracts the module name from a Ruby module node
func getRubyModuleName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "constant" {
			return parse.GetNodeContent(child, content)
		}
	}
	return ""
}
