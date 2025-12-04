package merge

import (
	"bytes"
	"crypto/sha256"

	sitter "github.com/smacker/go-tree-sitter"
	"kai-core/parse"
)

// Extractor extracts merge units from parsed code.
type Extractor struct {
	parser *parse.Parser
}

// NewExtractor creates a new unit extractor.
func NewExtractor() *Extractor {
	return &Extractor{
		parser: parse.NewParser(),
	}
}

// ExtractUnits parses code and extracts merge units.
func (e *Extractor) ExtractUnits(path string, content []byte, lang string) (*FileUnits, error) {
	parsed, err := e.parser.Parse(content, lang)
	if err != nil {
		return nil, err
	}

	fu := &FileUnits{
		Path:    path,
		Lang:    lang,
		Units:   make(map[string]*MergeUnit),
		Content: content,
	}

	// Extract units based on language
	switch lang {
	case "js", "ts", "javascript", "typescript":
		e.extractJSUnits(parsed, content, path, fu)
	case "py", "python":
		e.extractPyUnits(parsed, content, path, fu)
	default:
		e.extractJSUnits(parsed, content, path, fu) // fallback to JS
	}

	return fu, nil
}

// extractJSUnits extracts merge units from JavaScript/TypeScript AST.
func (e *Extractor) extractJSUnits(parsed *parse.ParsedFile, content []byte, path string, fu *FileUnits) {
	root := parsed.GetRootNode()
	e.walkJSNode(root, content, path, nil, fu)
}

func (e *Extractor) walkJSNode(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "function_declaration":
		unit := e.extractJSFunction(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "lexical_declaration", "variable_declaration":
		units := e.extractJSVariables(node, content, path, parentPath)
		for _, unit := range units {
			fu.Units[unit.Key.String()] = unit
		}

	case "class_declaration":
		unit := e.extractJSClass(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "export_statement":
		// Walk into export to find the actual declaration
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			e.walkJSNode(child, content, path, parentPath, fu)
		}

	case "import_statement":
		unit := e.extractJSImport(node, content, path)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}
	}

	// Recurse for program-level children
	if node.Type() == "program" {
		for i := 0; i < int(node.ChildCount()); i++ {
			e.walkJSNode(node.Child(i), content, path, parentPath, fu)
		}
	}
}

func (e *Extractor) extractJSFunction(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
	var name string
	var params string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "formal_parameters":
			params = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitFunction,
		},
		Kind:      UnitFunction,
		Name:      name,
		Signature: "function " + name + params,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractJSVariables(node *sitter.Node, content []byte, path string, parentPath []string) []*MergeUnit {
	var units []*MergeUnit
	var declKind string

	// Get declaration kind (const, let, var)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "const" || child.Type() == "let" || child.Type() == "var" {
			declKind = child.Type()
			break
		}
	}

	// Find variable_declarator children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			unit := e.extractJSVariableDeclarator(child, content, path, parentPath, declKind)
			if unit != nil {
				units = append(units, unit)
			}
		}
	}

	return units
}

func (e *Extractor) extractJSVariableDeclarator(node *sitter.Node, content []byte, path string, parentPath []string, declKind string) *MergeUnit {
	var name string
	var kind UnitKind = UnitVariable
	var signature string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "arrow_function", "function":
			kind = UnitFunction
			// Find parameters
			for j := 0; j < int(child.ChildCount()); j++ {
				param := child.Child(j)
				if param.Type() == "formal_parameters" {
					signature = "const " + name + " = " + param.Content(content) + " => ..."
					break
				}
			}
		}
	}

	if name == "" {
		return nil
	}

	if declKind == "const" && kind == UnitVariable {
		kind = UnitConst
	}

	if signature == "" {
		signature = declKind + " " + name
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       kind,
		},
		Kind:      kind,
		Name:      name,
		Signature: signature,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractJSClass(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string
	var classBody *sitter.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "class_body":
			classBody = child
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	unit := &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitClass,
		},
		Kind:      UnitClass,
		Name:      name,
		Signature: "class " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}

	// Extract methods
	if classBody != nil {
		for i := 0; i < int(classBody.ChildCount()); i++ {
			child := classBody.Child(i)
			if child.Type() == "method_definition" {
				method := e.extractJSMethod(child, content, path, symbolPath)
				if method != nil {
					unit.Children = append(unit.Children, method)
					fu.Units[method.Key.String()] = method
				}
			}
		}
	}

	return unit
}

func (e *Extractor) extractJSMethod(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
	var name string
	var params string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "property_identifier":
			name = child.Content(content)
		case "formal_parameters":
			params = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitMethod,
		},
		Kind:      UnitMethod,
		Name:      name,
		Signature: name + params,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractJSImport(node *sitter.Node, content []byte, path string) *MergeUnit {
	importContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(importContent))

	// Use import content as the key identifier
	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: []string{"import:" + importContent},
			Kind:       UnitImport,
		},
		Kind:     UnitImport,
		Name:     importContent,
		BodyHash: bodyHash[:],
		Range:    parse.GetNodeRange(node),
		Content:  []byte(importContent),
		RawNode:  node,
	}
}

// extractPyUnits extracts merge units from Python AST.
func (e *Extractor) extractPyUnits(parsed *parse.ParsedFile, content []byte, path string, fu *FileUnits) {
	root := parsed.GetRootNode()
	e.walkPyNode(root, content, path, nil, fu)
}

func (e *Extractor) walkPyNode(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "function_definition":
		unit := e.extractPyFunction(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "class_definition":
		unit := e.extractPyClass(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "import_statement", "import_from_statement":
		unit := e.extractPyImport(node, content, path)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}
	}

	// Recurse for module-level children
	if node.Type() == "module" {
		for i := 0; i < int(node.ChildCount()); i++ {
			e.walkPyNode(node.Child(i), content, path, parentPath, fu)
		}
	}
}

func (e *Extractor) extractPyFunction(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
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

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitFunction,
		},
		Kind:      UnitFunction,
		Name:      name,
		Signature: "def " + name + params,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractPyClass(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string
	var classBody *sitter.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "block":
			classBody = child
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	unit := &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitClass,
		},
		Kind:      UnitClass,
		Name:      name,
		Signature: "class " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}

	// Extract methods
	if classBody != nil {
		for i := 0; i < int(classBody.ChildCount()); i++ {
			child := classBody.Child(i)
			if child.Type() == "function_definition" {
				method := e.extractPyFunction(child, content, path, symbolPath)
				if method != nil {
					method.Kind = UnitMethod
					method.Key.Kind = UnitMethod
					unit.Children = append(unit.Children, method)
					fu.Units[method.Key.String()] = method
				}
			}
		}
	}

	return unit
}

func (e *Extractor) extractPyImport(node *sitter.Node, content []byte, path string) *MergeUnit {
	importContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(importContent))

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: []string{"import:" + importContent},
			Kind:       UnitImport,
		},
		Kind:     UnitImport,
		Name:     importContent,
		BodyHash: bodyHash[:],
		Range:    parse.GetNodeRange(node),
		Content:  []byte(importContent),
		RawNode:  node,
	}
}

// EquivalentUnits checks if two merge units are semantically equivalent.
func EquivalentUnits(a, b *MergeUnit) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a.BodyHash, b.BodyHash)
}

// Changed checks if a unit changed from base.
func Changed(unit, base *MergeUnit) bool {
	return !EquivalentUnits(unit, base)
}
