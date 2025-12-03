// Package classify provides change type detection for code changes.
package classify

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"kai/internal/graph"
	"kai/internal/parse"
	"kai/internal/util"
)

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
)

// FileRange represents a range in a file.
type FileRange struct {
	Path  string  `json:"path"`
	Start [2]int  `json:"start"`
	End   [2]int  `json:"end"`
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
	parser   *parse.Parser
	symbols  map[string][]*graph.Node // fileID -> symbols
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
func (d *Detector) DetectChanges(path string, beforeContent, afterContent []byte, fileID string) ([]*ChangeType, error) {
	beforeParsed, err := d.parser.Parse(beforeContent, "")
	if err != nil {
		return nil, fmt.Errorf("parsing before: %w", err)
	}

	afterParsed, err := d.parser.Parse(afterContent, "")
	if err != nil {
		return nil, fmt.Errorf("parsing after: %w", err)
	}

	var changes []*ChangeType

	// Detect function additions/removals (most important for intent)
	funcChanges := d.detectFunctionChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, funcChanges...)

	// Detect condition changes
	condChanges := d.detectConditionChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, condChanges...)

	// Detect constant updates
	constChanges := d.detectConstantUpdates(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, constChanges...)

	// Detect API surface changes
	apiChanges := d.detectAPISurfaceChanges(path, beforeParsed, afterParsed, beforeContent, afterContent, fileID)
	changes = append(changes, apiChanges...)

	return changes, nil
}

// detectFunctionChanges detects added or removed functions.
func (d *Detector) detectFunctionChanges(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	// Get all function declarations from both versions
	beforeFuncs := getAllFunctions(before, beforeContent)
	afterFuncs := getAllFunctions(after, afterContent)

	// Check for added functions
	for name, afterFunc := range afterFuncs {
		if _, exists := beforeFuncs[name]; !exists {
			afterRange := parse.GetNodeRange(afterFunc.node)
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
			beforeRange := parse.GetNodeRange(beforeFunc.node)
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

	return changes
}

// funcInfo holds information about a function declaration.
type funcInfo struct {
	name string
	node *sitter.Node
}

// getAllFunctions extracts all function declarations from a parsed file.
func getAllFunctions(parsed *parse.ParsedFile, content []byte) map[string]*funcInfo {
	funcs := make(map[string]*funcInfo)

	// Function declarations: function foo() {}
	for _, node := range parsed.FindNodesOfType("function_declaration") {
		name := getFunctionName(node, content)
		if name != "" {
			funcs[name] = &funcInfo{name: name, node: node}
		}
	}

	// Arrow functions assigned to variables: const foo = () => {}
	for _, node := range parsed.FindNodesOfType("lexical_declaration") {
		name, arrowNode := getArrowFunctionName(node, content)
		if name != "" && arrowNode != nil {
			funcs[name] = &funcInfo{name: name, node: node}
		}
	}

	// Variable declarations: var foo = function() {}
	for _, node := range parsed.FindNodesOfType("variable_declaration") {
		name, funcNode := getVariableFunctionName(node, content)
		if name != "" && funcNode != nil {
			funcs[name] = &funcInfo{name: name, node: node}
		}
	}

	// Method definitions in classes/objects
	for _, node := range parsed.FindNodesOfType("method_definition") {
		name := getFunctionName(node, content)
		if name != "" {
			funcs[name] = &funcInfo{name: name, node: node}
		}
	}

	return funcs
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
func (d *Detector) detectAPISurfaceChanges(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	// Check function declarations
	funcChanges := d.compareFunctions(path, before, after, beforeContent, afterContent, fileID)
	changes = append(changes, funcChanges...)

	// Check export statements
	exportChanges := d.compareExports(path, before, after, beforeContent, afterContent, fileID)
	changes = append(changes, exportChanges...)

	return changes
}

func (d *Detector) compareFunctions(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	beforeFuncs := before.FindNodesOfType("function_declaration")
	afterFuncs := after.FindNodesOfType("function_declaration")

	// Also check arrow functions and method definitions
	beforeFuncs = append(beforeFuncs, before.FindNodesOfType("method_definition")...)
	afterFuncs = append(afterFuncs, after.FindNodesOfType("method_definition")...)

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

func (d *Detector) compareExports(path string, before, after *parse.ParsedFile, beforeContent, afterContent []byte, fileID string) []*ChangeType {
	var changes []*ChangeType

	beforeExports := before.FindNodesOfType("export_statement")
	afterExports := after.FindNodesOfType("export_statement")

	// Get exported identifiers
	beforeSet := make(map[string]bool)
	afterSet := make(map[string]bool)

	for _, node := range beforeExports {
		ids := getExportedIdentifiers(node, beforeContent)
		for _, id := range ids {
			beforeSet[id] = true
		}
	}

	for _, node := range afterExports {
		ids := getExportedIdentifiers(node, afterContent)
		for _, id := range ids {
			afterSet[id] = true
		}
	}

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

	if hasDiff && len(afterExports) > 0 {
		afterRange := parse.GetNodeRange(afterExports[0])
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
			result = append(result, util.BytesToHex(sym.ID))
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

func getFunctionParams(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "formal_parameters" {
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
	case "ts", "js", "json":
		return true
	default:
		return false
	}
}

// DetectFileChange creates a FILE_CONTENT_CHANGED for non-parseable files.
func (d *Detector) DetectFileChange(path string, lang string) *ChangeType {
	return NewFileChange(FileContentChanged, path)
}
