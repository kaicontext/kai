// Package detect provides YAML-specific change detection.
package detect

import (
	"fmt"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// YAMLSymbol represents a key path in a YAML document.
type YAMLSymbol struct {
	Path  string      // Dot-separated path like "services.web.ports"
	Value interface{} // The value at this path
	Kind  string      // "map", "sequence", "string", "int", "float", "bool", "null"
}

// ExtractYAMLSymbols extracts top-level and nested key paths from YAML.
func ExtractYAMLSymbols(content []byte, maxDepth int) ([]*YAMLSymbol, error) {
	var data interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	var symbols []*YAMLSymbol
	extractYAMLPaths("", data, &symbols, 0, maxDepth)
	return symbols, nil
}

// extractYAMLPaths recursively extracts key paths from YAML.
func extractYAMLPaths(prefix string, data interface{}, symbols *[]*YAMLSymbol, depth, maxDepth int) {
	if depth > maxDepth {
		return
	}

	switch v := data.(type) {
	case map[string]interface{}:
		// Sort keys for deterministic output
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}

			*symbols = append(*symbols, &YAMLSymbol{
				Path:  path,
				Value: v[k],
				Kind:  yamlKind(v[k]),
			})

			// Recurse into nested maps/sequences
			extractYAMLPaths(path, v[k], symbols, depth+1, maxDepth)
		}

	case []interface{}:
		// For sequences, create a symbol for the sequence itself but don't enumerate items
		// (would be too noisy for large sequences)
	}
}

// yamlKind returns the YAML type name.
func yamlKind(v interface{}) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case map[string]interface{}:
		return "map"
	case []interface{}:
		return "sequence"
	case string:
		return "string"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case bool:
		return "bool"
	default:
		return "unknown"
	}
}

// DetectYAMLChanges compares two YAML documents and returns change types.
func DetectYAMLChanges(path string, before, after []byte) ([]*ChangeType, error) {
	var beforeData, afterData interface{}

	if err := yaml.Unmarshal(before, &beforeData); err != nil {
		return nil, fmt.Errorf("parsing before YAML: %w", err)
	}
	if err := yaml.Unmarshal(after, &afterData); err != nil {
		return nil, fmt.Errorf("parsing after YAML: %w", err)
	}

	var changes []*ChangeType
	compareYAML("", beforeData, afterData, path, &changes)
	return changes, nil
}

// compareYAML recursively compares two YAML values.
func compareYAML(keyPath string, before, after interface{}, filePath string, changes *[]*ChangeType) {
	// Handle nil cases
	if before == nil && after == nil {
		return
	}
	if before == nil {
		*changes = append(*changes, &ChangeType{
			Category: YAMLKeyAdded,
			Evidence: Evidence{
				FileRanges: []FileRange{{Path: filePath}},
				Symbols:    []string{keyPath},
			},
		})
		return
	}
	if after == nil {
		*changes = append(*changes, &ChangeType{
			Category: YAMLKeyRemoved,
			Evidence: Evidence{
				FileRanges: []FileRange{{Path: filePath}},
				Symbols:    []string{keyPath},
			},
		})
		return
	}

	// Handle type changes
	if reflect.TypeOf(before) != reflect.TypeOf(after) {
		*changes = append(*changes, &ChangeType{
			Category: YAMLValueChanged,
			Evidence: Evidence{
				FileRanges: []FileRange{{Path: filePath}},
				Symbols:    []string{keyPath},
			},
		})
		return
	}

	switch bv := before.(type) {
	case map[string]interface{}:
		av := after.(map[string]interface{})

		// Check for removed keys
		for k := range bv {
			path := k
			if keyPath != "" {
				path = keyPath + "." + k
			}

			if _, exists := av[k]; !exists {
				*changes = append(*changes, &ChangeType{
					Category: YAMLKeyRemoved,
					Evidence: Evidence{
						FileRanges: []FileRange{{Path: filePath}},
						Symbols:    []string{path},
					},
				})
			}
		}

		// Check for added keys and recurse into existing keys
		for k, afterVal := range av {
			path := k
			if keyPath != "" {
				path = keyPath + "." + k
			}

			if beforeVal, exists := bv[k]; !exists {
				*changes = append(*changes, &ChangeType{
					Category: YAMLKeyAdded,
					Evidence: Evidence{
						FileRanges: []FileRange{{Path: filePath}},
						Symbols:    []string{path},
					},
				})
			} else {
				// Recurse to check for nested changes
				compareYAML(path, beforeVal, afterVal, filePath, changes)
			}
		}

	case []interface{}:
		av := after.([]interface{})

		// Simple sequence comparison - just check if different
		if !reflect.DeepEqual(bv, av) {
			category := YAMLValueChanged
			if keyPath == "" {
				keyPath = "(root)"
			}
			*changes = append(*changes, &ChangeType{
				Category: category,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case string:
		if bv != after.(string) {
			*changes = append(*changes, &ChangeType{
				Category: YAMLValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case int:
		if bv != after.(int) {
			*changes = append(*changes, &ChangeType{
				Category: YAMLValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case int64:
		if bv != after.(int64) {
			*changes = append(*changes, &ChangeType{
				Category: YAMLValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case float64:
		if bv != after.(float64) {
			*changes = append(*changes, &ChangeType{
				Category: YAMLValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case bool:
		if bv != after.(bool) {
			*changes = append(*changes, &ChangeType{
				Category: YAMLValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}
	}
}

// FormatYAMLPath formats a key path for display.
func FormatYAMLPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}
