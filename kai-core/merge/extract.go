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
	case "rb", "ruby":
		e.extractRbUnits(parsed, content, path, fu)
	case "rs", "rust":
		e.extractRsUnits(parsed, content, path, fu)
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

// ==================== Ruby Extraction ====================

// extractRbUnits extracts merge units from Ruby AST.
func (e *Extractor) extractRbUnits(parsed *parse.ParsedFile, content []byte, path string, fu *FileUnits) {
	root := parsed.GetRootNode()
	e.walkRbNode(root, content, path, nil, fu)
}

func (e *Extractor) walkRbNode(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "method":
		unit := e.extractRbMethod(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "singleton_method":
		unit := e.extractRbSingletonMethod(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "class":
		unit := e.extractRbClass(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "module":
		unit := e.extractRbModule(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "call":
		// Check for require/require_relative
		unit := e.extractRbRequire(node, content, path)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}
	}

	// Recurse for program-level children
	if node.Type() == "program" {
		for i := 0; i < int(node.ChildCount()); i++ {
			e.walkRbNode(node.Child(i), content, path, parentPath, fu)
		}
	}
}

func (e *Extractor) extractRbMethod(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
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

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	signature := "def " + name
	if params != "" {
		signature += params
	}

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitFunction,
		},
		Kind:      UnitFunction,
		Name:      name,
		Signature: signature,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRbSingletonMethod(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
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

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	signature := "def " + object + "." + name
	if params != "" {
		signature += params
	}

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitFunction,
		},
		Kind:      UnitFunction,
		Name:      name,
		Signature: signature,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRbClass(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string
	var classBody *sitter.Node

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
		case "body_statement":
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
			switch child.Type() {
			case "method":
				method := e.extractRbMethod(child, content, path, symbolPath)
				if method != nil {
					method.Kind = UnitMethod
					method.Key.Kind = UnitMethod
					unit.Children = append(unit.Children, method)
					fu.Units[method.Key.String()] = method
				}
			case "singleton_method":
				method := e.extractRbSingletonMethod(child, content, path, symbolPath)
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

func (e *Extractor) extractRbModule(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string
	var moduleBody *sitter.Node

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
		case "body_statement":
			moduleBody = child
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
			Kind:       UnitModule,
		},
		Kind:      UnitModule,
		Name:      name,
		Signature: "module " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}

	// Extract methods
	if moduleBody != nil {
		for i := 0; i < int(moduleBody.ChildCount()); i++ {
			child := moduleBody.Child(i)
			switch child.Type() {
			case "method":
				method := e.extractRbMethod(child, content, path, symbolPath)
				if method != nil {
					method.Kind = UnitMethod
					method.Key.Kind = UnitMethod
					unit.Children = append(unit.Children, method)
					fu.Units[method.Key.String()] = method
				}
			case "singleton_method":
				method := e.extractRbSingletonMethod(child, content, path, symbolPath)
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

func (e *Extractor) extractRbRequire(node *sitter.Node, content []byte, path string) *MergeUnit {
	var methodName string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			methodName = child.Content(content)
			break
		}
	}

	// Only handle require/require_relative/load
	if methodName != "require" && methodName != "require_relative" && methodName != "load" {
		return nil
	}

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

// ==================== Rust Extraction ====================

// extractRsUnits extracts merge units from Rust AST.
func (e *Extractor) extractRsUnits(parsed *parse.ParsedFile, content []byte, path string, fu *FileUnits) {
	root := parsed.GetRootNode()
	e.walkRsNode(root, content, path, nil, fu)
}

func (e *Extractor) walkRsNode(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "function_item":
		unit := e.extractRsFunction(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "struct_item":
		unit := e.extractRsStruct(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "enum_item":
		unit := e.extractRsEnum(node, content, path, parentPath)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "trait_item":
		unit := e.extractRsTrait(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "impl_item":
		e.extractRsImpl(node, content, path, parentPath, fu)

	case "mod_item":
		unit := e.extractRsMod(node, content, path, parentPath, fu)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}

	case "use_declaration":
		unit := e.extractRsUse(node, content, path)
		if unit != nil {
			fu.Units[unit.Key.String()] = unit
		}
	}

	// Recurse for source_file children
	if node.Type() == "source_file" {
		for i := 0; i < int(node.ChildCount()); i++ {
			e.walkRsNode(node.Child(i), content, path, parentPath, fu)
		}
	}
}

func (e *Extractor) extractRsFunction(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
	var name string
	var params string
	var returnType string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = child.Content(content)
			}
		case "parameters":
			params = child.Content(content)
		case "type_identifier", "generic_type", "reference_type", "pointer_type":
			returnType = child.Content(content)
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	signature := "fn " + name + params
	if returnType != "" {
		signature += " -> " + returnType
	}

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitFunction,
		},
		Kind:      UnitFunction,
		Name:      name,
		Signature: signature,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRsStruct(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_identifier" {
			name = child.Content(content)
			break
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
			Kind:       UnitClass,
		},
		Kind:      UnitClass,
		Name:      name,
		Signature: "struct " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRsEnum(node *sitter.Node, content []byte, path string, parentPath []string) *MergeUnit {
	var name string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_identifier" {
			name = child.Content(content)
			break
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
			Kind:       UnitClass,
		},
		Kind:      UnitClass,
		Name:      name,
		Signature: "enum " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRsTrait(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
	var name string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_identifier" {
			name = child.Content(content)
			break
		}
	}

	if name == "" {
		return nil
	}

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	// Extract trait methods
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "declaration_list" {
			for j := 0; j < int(child.ChildCount()); j++ {
				item := child.Child(j)
				if item.Type() == "function_item" || item.Type() == "function_signature_item" {
					methodUnit := e.extractRsFunction(item, content, path, symbolPath)
					if methodUnit != nil {
						fu.Units[methodUnit.Key.String()] = methodUnit
					}
				}
			}
		}
	}

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitClass, // Using class for trait
		},
		Kind:      UnitClass,
		Name:      name,
		Signature: "trait " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRsImpl(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) {
	var implType string

	// Get the type being implemented
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			if implType == "" {
				implType = child.Content(content)
			}
		case "generic_type":
			if implType == "" {
				implType = child.Content(content)
			}
		}
	}

	implPath := parentPath
	if implType != "" {
		implPath = append(parentPath, implType)
	}

	// Extract methods from impl block
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "declaration_list" {
			for j := 0; j < int(child.ChildCount()); j++ {
				item := child.Child(j)
				if item.Type() == "function_item" {
					methodUnit := e.extractRsFunction(item, content, path, implPath)
					if methodUnit != nil {
						fu.Units[methodUnit.Key.String()] = methodUnit
					}
				}
			}
		}
	}
}

func (e *Extractor) extractRsMod(node *sitter.Node, content []byte, path string, parentPath []string, fu *FileUnits) *MergeUnit {
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

	symbolPath := append(parentPath, name)
	bodyContent := node.Content(content)
	bodyHash := sha256.Sum256([]byte(bodyContent))

	// Check for inline module (has declaration_list) vs file module
	hasBody := false
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "declaration_list" {
			hasBody = true
			// Recursively extract from inline module
			for j := 0; j < int(child.ChildCount()); j++ {
				e.walkRsNode(child.Child(j), content, path, symbolPath, fu)
			}
			break
		}
	}

	_ = hasBody // Could be used for different handling

	return &MergeUnit{
		Key: UnitKey{
			File:       path,
			SymbolPath: symbolPath,
			Kind:       UnitModule,
		},
		Kind:      UnitModule,
		Name:      name,
		Signature: "mod " + name,
		BodyHash:  bodyHash[:],
		Range:     parse.GetNodeRange(node),
		Content:   []byte(bodyContent),
		RawNode:   node,
	}
}

func (e *Extractor) extractRsUse(node *sitter.Node, content []byte, path string) *MergeUnit {
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
