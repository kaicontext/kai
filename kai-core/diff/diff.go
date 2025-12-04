package diff

import (
	"path/filepath"
	"strings"

	"kai-core/detect"
	"kai-core/parse"
)

// Differ computes semantic diffs between file versions.
type Differ struct {
	parser *parse.Parser
}

// NewDiffer creates a new semantic differ.
func NewDiffer() *Differ {
	return &Differ{
		parser: parse.NewParser(),
	}
}

// DiffFile computes semantic diff for a single file.
func (d *Differ) DiffFile(path string, before, after []byte) (*FileDiff, error) {
	ext := strings.ToLower(filepath.Ext(path))
	lang := extToLang(ext)

	fd := &FileDiff{
		Path: path,
		Lang: lang,
	}

	// Determine file-level action
	if before == nil && after != nil {
		fd.Action = ActionAdded
		fd.NewSize = len(after)
	} else if before != nil && after == nil {
		fd.Action = ActionRemoved
		fd.OldSize = len(before)
	} else if before != nil && after != nil {
		fd.Action = ActionModified
		fd.OldSize = len(before)
		fd.NewSize = len(after)
	}

	// Compute unit-level diffs based on file type
	switch {
	case lang == "json":
		units, err := d.diffJSON(path, before, after)
		if err != nil {
			return fd, nil // Return file-level diff even if unit diff fails
		}
		fd.Units = units

	case lang == "yaml" || lang == "yml":
		units, err := d.diffYAML(path, before, after)
		if err != nil {
			return fd, nil
		}
		fd.Units = units

	case lang == "sql":
		units, err := d.diffSQL(path, before, after)
		if err != nil {
			return fd, nil
		}
		fd.Units = units

	case isCodeLang(lang):
		units, err := d.diffCode(path, before, after, lang)
		if err != nil {
			return fd, nil
		}
		fd.Units = units
	}

	return fd, nil
}

// DiffFiles computes semantic diff for multiple files.
func (d *Differ) DiffFiles(files map[string][2][]byte) (*SemanticDiff, error) {
	sd := &SemanticDiff{
		Files: make([]FileDiff, 0, len(files)),
	}

	for path, versions := range files {
		before, after := versions[0], versions[1]
		fd, err := d.DiffFile(path, before, after)
		if err != nil {
			continue
		}
		if fd != nil {
			sd.Files = append(sd.Files, *fd)
		}
	}

	sd.ComputeSummary()
	return sd, nil
}

// diffCode computes unit diffs for code files (JS/TS/Python/Go).
func (d *Differ) diffCode(path string, before, after []byte, lang string) ([]UnitDiff, error) {
	var units []UnitDiff

	// Parse both versions using the parse package
	var beforeSymbols, afterSymbols map[string]*parse.Symbol

	if before != nil {
		parsed, err := d.parser.Parse(before, lang)
		if err != nil {
			return nil, err
		}
		beforeSymbols = symbolsToMap(parsed.Symbols)
	} else {
		beforeSymbols = make(map[string]*parse.Symbol)
	}

	if after != nil {
		parsed, err := d.parser.Parse(after, lang)
		if err != nil {
			return nil, err
		}
		afterSymbols = symbolsToMap(parsed.Symbols)
	} else {
		afterSymbols = make(map[string]*parse.Symbol)
	}

	// Find added and modified
	for name, afterSym := range afterSymbols {
		beforeSym, exists := beforeSymbols[name]
		if !exists {
			units = append(units, UnitDiff{
				Kind:     symbolKindToUnitKind(afterSym.Kind),
				Name:     name,
				Action:   ActionAdded,
				AfterSig: afterSym.Signature,
				Range:    parseRangeToRange(afterSym.Range),
			})
		} else if afterSym.Signature != beforeSym.Signature {
			// Signature changed
			units = append(units, UnitDiff{
				Kind:       symbolKindToUnitKind(afterSym.Kind),
				Name:       name,
				Action:     ActionModified,
				BeforeSig:  beforeSym.Signature,
				AfterSig:   afterSym.Signature,
				Range:      parseRangeToRange(afterSym.Range),
				ChangeType: "API_SURFACE_CHANGED",
			})
		}
		// Note: We could also compare function bodies if we extracted content
	}

	// Find removed
	for name, beforeSym := range beforeSymbols {
		if _, exists := afterSymbols[name]; !exists {
			units = append(units, UnitDiff{
				Kind:      symbolKindToUnitKind(beforeSym.Kind),
				Name:      name,
				Action:    ActionRemoved,
				BeforeSig: beforeSym.Signature,
			})
		}
	}

	return units, nil
}

// symbolsToMap converts a slice of symbols to a map keyed by name.
func symbolsToMap(symbols []*parse.Symbol) map[string]*parse.Symbol {
	m := make(map[string]*parse.Symbol)
	for _, s := range symbols {
		m[s.Name] = s
	}
	return m
}

// parseRangeToRange converts parse.Range to diff.Range.
func parseRangeToRange(r parse.Range) *Range {
	return &Range{
		StartLine: r.Start[0] + 1, // Convert to 1-based
		StartCol:  r.Start[1],
		EndLine:   r.End[0] + 1,
		EndCol:    r.End[1],
	}
}

// diffJSON computes unit diffs for JSON files.
func (d *Differ) diffJSON(path string, before, after []byte) ([]UnitDiff, error) {
	if before == nil || after == nil {
		return nil, nil
	}

	changes, err := detect.DetectJSONChanges(path, before, after)
	if err != nil {
		return nil, err
	}

	var units []UnitDiff
	for _, c := range changes {
		ud := UnitDiff{
			Kind:       KindJSONKey,
			ChangeType: string(c.Category),
		}

		// Extract key path from evidence
		if len(c.Evidence.Symbols) > 0 {
			ud.Name = c.Evidence.Symbols[0]
			ud.Path = c.Evidence.Symbols[0]
		}

		switch c.Category {
		case detect.JSONFieldAdded:
			ud.Action = ActionAdded
		case detect.JSONFieldRemoved:
			ud.Action = ActionRemoved
		default:
			ud.Action = ActionModified
		}

		units = append(units, ud)
	}

	return units, nil
}

// diffYAML computes unit diffs for YAML files.
func (d *Differ) diffYAML(path string, before, after []byte) ([]UnitDiff, error) {
	if before == nil || after == nil {
		return nil, nil
	}

	changes, err := detect.DetectYAMLChanges(path, before, after)
	if err != nil {
		return nil, err
	}

	var units []UnitDiff
	for _, c := range changes {
		ud := UnitDiff{
			Kind:       KindYAMLKey,
			ChangeType: string(c.Category),
		}

		if len(c.Evidence.Symbols) > 0 {
			ud.Name = c.Evidence.Symbols[0]
			ud.Path = c.Evidence.Symbols[0]
		}

		switch c.Category {
		case detect.YAMLKeyAdded:
			ud.Action = ActionAdded
		case detect.YAMLKeyRemoved:
			ud.Action = ActionRemoved
		default:
			ud.Action = ActionModified
		}

		units = append(units, ud)
	}

	return units, nil
}

// diffSQL computes unit diffs for SQL schema files.
func (d *Differ) diffSQL(path string, before, after []byte) ([]UnitDiff, error) {
	var beforeTables, afterTables map[string]*sqlTable

	if before != nil {
		beforeTables = parseSQL(string(before))
	} else {
		beforeTables = make(map[string]*sqlTable)
	}

	if after != nil {
		afterTables = parseSQL(string(after))
	} else {
		afterTables = make(map[string]*sqlTable)
	}

	var units []UnitDiff

	// Find added and modified tables
	for name, afterTable := range afterTables {
		beforeTable, exists := beforeTables[name]
		if !exists {
			// Table added
			units = append(units, UnitDiff{
				Kind:   KindSQLTable,
				Name:   name,
				Action: ActionAdded,
				After:  afterTable.Definition,
			})
			// Add all columns as added
			for colName, col := range afterTable.Columns {
				units = append(units, UnitDiff{
					Kind:   KindSQLColumn,
					Name:   colName,
					Path:   name + "." + colName,
					Action: ActionAdded,
					After:  col.Definition,
				})
			}
		} else {
			// Check for column changes
			for colName, afterCol := range afterTable.Columns {
				beforeCol, colExists := beforeTable.Columns[colName]
				if !colExists {
					units = append(units, UnitDiff{
						Kind:   KindSQLColumn,
						Name:   colName,
						Path:   name + "." + colName,
						Action: ActionAdded,
						After:  afterCol.Definition,
					})
				} else if afterCol.Definition != beforeCol.Definition {
					units = append(units, UnitDiff{
						Kind:   KindSQLColumn,
						Name:   colName,
						Path:   name + "." + colName,
						Action: ActionModified,
						Before: beforeCol.Definition,
						After:  afterCol.Definition,
					})
				}
			}
			// Check for removed columns
			for colName, beforeCol := range beforeTable.Columns {
				if _, exists := afterTable.Columns[colName]; !exists {
					units = append(units, UnitDiff{
						Kind:   KindSQLColumn,
						Name:   colName,
						Path:   name + "." + colName,
						Action: ActionRemoved,
						Before: beforeCol.Definition,
					})
				}
			}
		}
	}

	// Find removed tables
	for name, beforeTable := range beforeTables {
		if _, exists := afterTables[name]; !exists {
			units = append(units, UnitDiff{
				Kind:   KindSQLTable,
				Name:   name,
				Action: ActionRemoved,
				Before: beforeTable.Definition,
			})
		}
	}

	return units, nil
}

// Helper functions

func extToLang(ext string) string {
	switch ext {
	case ".js":
		return "js"
	case ".ts", ".tsx":
		return "ts"
	case ".py":
		return "py"
	case ".go":
		return "go"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func isCodeLang(lang string) bool {
	switch lang {
	case "js", "ts", "py", "go", "rust", "java":
		return true
	default:
		return false
	}
}

func symbolKindToUnitKind(kind string) UnitKind {
	switch kind {
	case "function":
		return KindFunction
	case "class":
		return KindClass
	case "method":
		return KindMethod
	case "variable", "const":
		return KindVariable
	default:
		return UnitKind(kind)
	}
}
