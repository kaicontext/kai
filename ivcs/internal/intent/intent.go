// Package intent generates intent sentences from change analysis.
package intent

import (
	"path/filepath"
	"strings"

	"ivcs/internal/classify"
	"ivcs/internal/graph"
	"ivcs/internal/util"
)

// Generator generates intent sentences.
type Generator struct {
	db *graph.DB
}

// NewGenerator creates a new intent generator.
func NewGenerator(db *graph.DB) *Generator {
	return &Generator{db: db}
}

// GenerateIntent generates an intent sentence for a changeset.
func (g *Generator) GenerateIntent(changeSetID []byte, changeTypes []*classify.ChangeType, modules []string, symbols []*graph.Node, changedFiles []string) string {
	// Determine verb from change types (priority order)
	verb := determineVerb(changeTypes)

	// Determine module
	module := "General"
	if len(modules) > 0 {
		module = modules[0]
	}

	// Determine area (symbol name or common path prefix)
	area := determineArea(symbols, changedFiles)

	return verb + " " + module + " " + area
}

// determineVerb determines the verb based on change types.
func determineVerb(changeTypes []*classify.ChangeType) string {
	// Priority order: API_SURFACE_CHANGED, CONDITION_CHANGED, CONSTANT_UPDATED
	hasAPI := false
	hasCondition := false
	hasConstant := false

	for _, ct := range changeTypes {
		switch ct.Category {
		case classify.APISurfaceChanged:
			hasAPI = true
		case classify.ConditionChanged:
			hasCondition = true
		case classify.ConstantUpdated:
			hasConstant = true
		}
	}

	if hasAPI {
		return "Update"
	}
	if hasCondition {
		return "Modify"
	}
	if hasConstant {
		return "Update"
	}

	return "Change"
}

// determineArea determines the area from symbols or paths.
func determineArea(symbols []*graph.Node, changedFiles []string) string {
	// Try to get a symbol name first
	if len(symbols) > 0 {
		for _, sym := range symbols {
			if name, ok := sym.Payload["fqName"].(string); ok && name != "" {
				// Return the simple name (last part if dotted)
				parts := strings.Split(name, ".")
				return parts[len(parts)-1]
			}
		}
	}

	// Fallback to common path prefix
	if len(changedFiles) > 0 {
		return getCommonArea(changedFiles)
	}

	return "codebase"
}

// getCommonArea extracts a meaningful area name from file paths.
func getCommonArea(paths []string) string {
	if len(paths) == 0 {
		return "codebase"
	}

	if len(paths) == 1 {
		// Use the file name without extension
		base := filepath.Base(paths[0])
		ext := filepath.Ext(base)
		return strings.TrimSuffix(base, ext)
	}

	// Find common directory prefix
	dirs := make([][]string, len(paths))
	minLen := -1

	for i, p := range paths {
		dirs[i] = strings.Split(filepath.Dir(p), string(filepath.Separator))
		if minLen == -1 || len(dirs[i]) < minLen {
			minLen = len(dirs[i])
		}
	}

	if minLen <= 0 {
		return "codebase"
	}

	// Find the longest common prefix
	var common []string
	for i := 0; i < minLen; i++ {
		val := dirs[0][i]
		allMatch := true
		for j := 1; j < len(dirs); j++ {
			if dirs[j][i] != val {
				allMatch = false
				break
			}
		}
		if allMatch {
			common = append(common, val)
		} else {
			break
		}
	}

	if len(common) > 0 {
		// Return the last meaningful directory name
		for i := len(common) - 1; i >= 0; i-- {
			if common[i] != "" && common[i] != "." {
				return common[i]
			}
		}
	}

	return "codebase"
}

// GetSymbolsForChangeSet retrieves all symbols associated with a changeset.
func (g *Generator) GetSymbolsForChangeSet(changeSetID []byte) ([]*graph.Node, error) {
	// Get MODIFIES edges from changeset to symbols
	edges, err := g.db.GetEdges(changeSetID, graph.EdgeModifies)
	if err != nil {
		return nil, err
	}

	var symbols []*graph.Node
	for _, edge := range edges {
		node, err := g.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil && node.Kind == graph.KindSymbol {
			symbols = append(symbols, node)
		}
	}

	return symbols, nil
}

// GetModulesForChangeSet retrieves all module names associated with a changeset.
func (g *Generator) GetModulesForChangeSet(changeSetID []byte) ([]string, error) {
	// Get AFFECTS edges from changeset to modules
	edges, err := g.db.GetEdges(changeSetID, graph.EdgeAffects)
	if err != nil {
		return nil, err
	}

	var modules []string
	for _, edge := range edges {
		node, err := g.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil && node.Kind == graph.KindModule {
			if name, ok := node.Payload["name"].(string); ok {
				modules = append(modules, name)
			}
		}
	}

	return modules, nil
}

// GetChangeTypesForChangeSet retrieves all change types for a changeset.
func (g *Generator) GetChangeTypesForChangeSet(changeSetID []byte) ([]*classify.ChangeType, error) {
	edges, err := g.db.GetEdges(changeSetID, graph.EdgeHas)
	if err != nil {
		return nil, err
	}

	var changeTypes []*classify.ChangeType
	for _, edge := range edges {
		node, err := g.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil && node.Kind == graph.KindChangeType {
			ct := payloadToChangeType(node.Payload)
			if ct != nil {
				changeTypes = append(changeTypes, ct)
			}
		}
	}

	return changeTypes, nil
}

func payloadToChangeType(payload map[string]interface{}) *classify.ChangeType {
	category, ok := payload["category"].(string)
	if !ok {
		return nil
	}

	ct := &classify.ChangeType{
		Category: classify.ChangeCategory(category),
	}

	if evidence, ok := payload["evidence"].(map[string]interface{}); ok {
		if fileRanges, ok := evidence["fileRanges"].([]interface{}); ok {
			for _, fr := range fileRanges {
				if frMap, ok := fr.(map[string]interface{}); ok {
					fileRange := classify.FileRange{}
					if path, ok := frMap["path"].(string); ok {
						fileRange.Path = path
					}
					if start, ok := frMap["start"].([]interface{}); ok && len(start) == 2 {
						fileRange.Start = [2]int{int(start[0].(float64)), int(start[1].(float64))}
					}
					if end, ok := frMap["end"].([]interface{}); ok && len(end) == 2 {
						fileRange.End = [2]int{int(end[0].(float64)), int(end[1].(float64))}
					}
					ct.Evidence.FileRanges = append(ct.Evidence.FileRanges, fileRange)
				}
			}
		}
		if symbols, ok := evidence["symbols"].([]interface{}); ok {
			for _, s := range symbols {
				if sym, ok := s.(string); ok {
					ct.Evidence.Symbols = append(ct.Evidence.Symbols, sym)
				}
			}
		}
	}

	return ct
}

// GetChangedFilesForChangeSet retrieves file paths for a changeset.
func (g *Generator) GetChangedFilesForChangeSet(changeSetID []byte) ([]string, error) {
	edges, err := g.db.GetEdges(changeSetID, graph.EdgeModifies)
	if err != nil {
		return nil, err
	}

	var paths []string
	seen := make(map[string]bool)

	for _, edge := range edges {
		node, err := g.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil && node.Kind == graph.KindFile {
			if path, ok := node.Payload["path"].(string); ok {
				if !seen[path] {
					seen[path] = true
					paths = append(paths, path)
				}
			}
		}
	}

	return paths, nil
}

// UpdateChangeSetIntent updates the intent field in a changeset.
func (g *Generator) UpdateChangeSetIntent(changeSetID []byte, intent string) error {
	node, err := g.db.GetNode(changeSetID)
	if err != nil {
		return err
	}
	if node == nil {
		return nil
	}

	node.Payload["intent"] = intent
	return g.db.UpdateNodePayload(changeSetID, node.Payload)
}

// RenderIntent renders the intent for a changeset.
func (g *Generator) RenderIntent(changeSetID []byte, editText string) (string, error) {
	if editText != "" {
		// Use provided text
		if err := g.UpdateChangeSetIntent(changeSetID, editText); err != nil {
			return "", err
		}
		return editText, nil
	}

	// Generate from data
	changeTypes, err := g.GetChangeTypesForChangeSet(changeSetID)
	if err != nil {
		return "", err
	}

	modules, err := g.GetModulesForChangeSet(changeSetID)
	if err != nil {
		return "", err
	}

	symbols, err := g.GetSymbolsForChangeSet(changeSetID)
	if err != nil {
		return "", err
	}

	files, err := g.GetChangedFilesForChangeSet(changeSetID)
	if err != nil {
		return "", err
	}

	intent := g.GenerateIntent(changeSetID, changeTypes, modules, symbols, files)

	if err := g.UpdateChangeSetIntent(changeSetID, intent); err != nil {
		return "", err
	}

	return intent, nil
}

// Placeholder to satisfy import
var _ = util.NowMs
