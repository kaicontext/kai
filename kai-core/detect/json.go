// Package detect provides JSON-specific change detection.
package detect

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// JSONSymbol represents a key path in a JSON document.
type JSONSymbol struct {
	Path  string      // Dot-separated path like "dependencies.react"
	Value interface{} // The value at this path
	Kind  string      // "object", "array", "string", "number", "boolean", "null"
}

// ExtractJSONSymbols extracts top-level and nested key paths from JSON.
func ExtractJSONSymbols(content []byte, maxDepth int) ([]*JSONSymbol, error) {
	var data interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	var symbols []*JSONSymbol
	extractPaths("", data, &symbols, 0, maxDepth)
	return symbols, nil
}

// extractPaths recursively extracts key paths from JSON.
func extractPaths(prefix string, data interface{}, symbols *[]*JSONSymbol, depth, maxDepth int) {
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

			*symbols = append(*symbols, &JSONSymbol{
				Path:  path,
				Value: v[k],
				Kind:  jsonKind(v[k]),
			})

			// Recurse into nested objects/arrays
			extractPaths(path, v[k], symbols, depth+1, maxDepth)
		}

	case []interface{}:
		// For arrays, create a symbol for the array itself but don't enumerate items
		// (would be too noisy for large arrays)
	}
}

// jsonKind returns the JSON type name.
func jsonKind(v interface{}) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

// DetectJSONChanges compares two JSON documents and returns change types.
func DetectJSONChanges(path string, before, after []byte) ([]*ChangeType, error) {
	var beforeData, afterData interface{}

	if err := json.Unmarshal(before, &beforeData); err != nil {
		return nil, fmt.Errorf("parsing before JSON: %w", err)
	}
	if err := json.Unmarshal(after, &afterData); err != nil {
		return nil, fmt.Errorf("parsing after JSON: %w", err)
	}

	var changes []*ChangeType
	compareJSON("", beforeData, afterData, path, &changes)
	return changes, nil
}

// compareJSON recursively compares two JSON values.
func compareJSON(keyPath string, before, after interface{}, filePath string, changes *[]*ChangeType) {
	// Handle type changes
	if reflect.TypeOf(before) != reflect.TypeOf(after) {
		*changes = append(*changes, &ChangeType{
			Category: JSONValueChanged,
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
					Category: JSONFieldRemoved,
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
					Category: JSONFieldAdded,
					Evidence: Evidence{
						FileRanges: []FileRange{{Path: filePath}},
						Symbols:    []string{path},
					},
				})
			} else {
				// Recurse to check for nested changes
				compareJSON(path, beforeVal, afterVal, filePath, changes)
			}
		}

	case []interface{}:
		av := after.([]interface{})

		// Simple array comparison - just check if different
		if !reflect.DeepEqual(bv, av) {
			*changes = append(*changes, &ChangeType{
				Category: JSONArrayChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case string:
		if bv != after.(string) {
			*changes = append(*changes, &ChangeType{
				Category: JSONValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case float64:
		if bv != after.(float64) {
			*changes = append(*changes, &ChangeType{
				Category: JSONValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}

	case bool:
		if bv != after.(bool) {
			*changes = append(*changes, &ChangeType{
				Category: JSONValueChanged,
				Evidence: Evidence{
					FileRanges: []FileRange{{Path: filePath}},
					Symbols:    []string{keyPath},
				},
			})
		}
	}
}

// FormatJSONPath formats a key path for display.
func FormatJSONPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

// IsPackageJSON returns true if the path looks like package.json.
func IsPackageJSON(path string) bool {
	return strings.HasSuffix(path, "package.json")
}

// IsTSConfig returns true if the path looks like tsconfig.json.
func IsTSConfig(path string) bool {
	return strings.HasSuffix(path, "tsconfig.json") || strings.Contains(path, "tsconfig.")
}
