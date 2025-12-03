// Package intent generates intent sentences from change analysis.
package intent

import (
	"path/filepath"
	"strings"

	"kai-core/detect"
	"kai-core/graph"
)

// GenerateIntent generates an intent sentence for a changeset.
func GenerateIntent(changeTypes []*detect.ChangeType, modules []string, symbols []*graph.Node, changedFiles []string) string {
	// Determine verb from change types (priority order)
	verb := determineVerb(changeTypes)

	// Determine module
	module := "General"
	if len(modules) > 0 {
		module = modules[0]
	}

	// Try to get function names from change types first (most specific)
	funcNames := extractFunctionNames(changeTypes)
	if len(funcNames) > 0 {
		area := formatFunctionNames(funcNames)
		return verb + " " + area + " in " + module
	}

	// Fall back to symbol names or file paths
	area := determineArea(symbols, changedFiles)

	return verb + " " + module + " " + area
}

// extractFunctionNames extracts function names from FUNCTION_ADDED/REMOVED change types.
func extractFunctionNames(changeTypes []*detect.ChangeType) []string {
	var names []string
	seen := make(map[string]bool)

	for _, ct := range changeTypes {
		if ct.Category == detect.FunctionAdded || ct.Category == detect.FunctionRemoved {
			for _, sym := range ct.Evidence.Symbols {
				// Function names are stored as "name:functionName"
				if strings.HasPrefix(sym, "name:") {
					name := strings.TrimPrefix(sym, "name:")
					if !seen[name] {
						seen[name] = true
						names = append(names, name)
					}
				}
			}
		}
	}

	return names
}

// formatFunctionNames formats a list of function names for display.
func formatFunctionNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	if len(names) == 2 {
		return names[0] + " and " + names[1]
	}
	// More than 2 functions
	return strings.Join(names[:2], ", ") + " and others"
}

// determineVerb determines the verb based on change types.
func determineVerb(changeTypes []*detect.ChangeType) string {
	// Priority order for semantic changes
	hasFuncAdded := false
	hasFuncRemoved := false
	hasAPI := false
	hasCondition := false
	hasConstant := false
	hasJSONField := false
	hasJSONValue := false
	hasFileContent := false

	for _, ct := range changeTypes {
		switch ct.Category {
		case detect.FunctionAdded:
			hasFuncAdded = true
		case detect.FunctionRemoved:
			hasFuncRemoved = true
		case detect.APISurfaceChanged:
			hasAPI = true
		case detect.ConditionChanged:
			hasCondition = true
		case detect.ConstantUpdated:
			hasConstant = true
		case detect.JSONFieldAdded, detect.JSONFieldRemoved:
			hasJSONField = true
		case detect.JSONValueChanged, detect.JSONArrayChanged:
			hasJSONValue = true
		case detect.FileContentChanged:
			hasFileContent = true
		case detect.FileAdded:
			return "Add"
		case detect.FileDeleted:
			return "Remove"
		}
	}

	// Function changes take highest priority
	if hasFuncAdded && hasFuncRemoved {
		return "Refactor"
	}
	if hasFuncAdded {
		return "Add"
	}
	if hasFuncRemoved {
		return "Remove"
	}
	// Semantic code changes
	if hasAPI {
		return "Update"
	}
	if hasCondition {
		return "Modify"
	}
	if hasConstant {
		return "Update"
	}
	// JSON changes
	if hasJSONField {
		return "Update"
	}
	if hasJSONValue {
		return "Modify"
	}
	// File-level fallback
	if hasFileContent {
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

// PayloadToChangeType converts a node payload to a ChangeType.
func PayloadToChangeType(payload map[string]interface{}) *detect.ChangeType {
	category, ok := payload["category"].(string)
	if !ok {
		return nil
	}

	ct := &detect.ChangeType{
		Category: detect.ChangeCategory(category),
	}

	if evidence, ok := payload["evidence"].(map[string]interface{}); ok {
		if fileRanges, ok := evidence["fileRanges"].([]interface{}); ok {
			for _, fr := range fileRanges {
				if frMap, ok := fr.(map[string]interface{}); ok {
					fileRange := detect.FileRange{}
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
