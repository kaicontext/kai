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

// DependencyChange represents a change to a package dependency.
type DependencyChange struct {
	Name       string
	OldVersion string
	NewVersion string
	Category   ChangeCategory
	DepType    string // "dependencies", "devDependencies", "peerDependencies", etc.
}

// DetectDependencyChanges detects changes to package.json dependencies.
// Returns specialized ChangeSignals for dependency changes.
func DetectDependencyChanges(path string, before, after []byte) ([]*ChangeSignal, error) {
	var beforeData, afterData map[string]interface{}

	if err := json.Unmarshal(before, &beforeData); err != nil {
		return nil, fmt.Errorf("parsing before JSON: %w", err)
	}
	if err := json.Unmarshal(after, &afterData); err != nil {
		return nil, fmt.Errorf("parsing after JSON: %w", err)
	}

	var signals []*ChangeSignal

	// Check all dependency types
	depTypes := []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}

	for _, depType := range depTypes {
		beforeDeps := getDependencyMap(beforeData, depType)
		afterDeps := getDependencyMap(afterData, depType)

		// Check for removed dependencies
		for name, oldVersion := range beforeDeps {
			if _, exists := afterDeps[name]; !exists {
				signals = append(signals, &ChangeSignal{
					Category: DependencyRemoved,
					Evidence: ExtendedEvidence{
						FileRanges:  []FileRange{{Path: path}},
						Symbols:     []string{depType + "." + name},
						BeforeValue: oldVersion,
						OldName:     name,
					},
					Weight:     0.75,
					Confidence: 1.0,
					Tags:       []string{"config", "breaking"},
				})
			}
		}

		// Check for added or updated dependencies
		for name, newVersion := range afterDeps {
			oldVersion, existed := beforeDeps[name]
			if !existed {
				signals = append(signals, &ChangeSignal{
					Category: DependencyAdded,
					Evidence: ExtendedEvidence{
						FileRanges: []FileRange{{Path: path}},
						Symbols:    []string{depType + "." + name},
						AfterValue: newVersion,
						NewName:    name,
					},
					Weight:     0.75,
					Confidence: 1.0,
					Tags:       []string{"config"},
				})
			} else if oldVersion != newVersion {
				signals = append(signals, &ChangeSignal{
					Category: DependencyUpdated,
					Evidence: ExtendedEvidence{
						FileRanges:  []FileRange{{Path: path}},
						Symbols:     []string{depType + "." + name},
						BeforeValue: oldVersion,
						AfterValue:  newVersion,
						OldName:     name,
						NewName:     name,
					},
					Weight:     0.7,
					Confidence: 1.0,
					Tags:       determineDependencyTags(oldVersion, newVersion),
				})
			}
		}
	}

	return signals, nil
}

// getDependencyMap extracts dependencies from a package.json structure.
func getDependencyMap(data map[string]interface{}, depType string) map[string]string {
	deps := make(map[string]string)
	if depObj, ok := data[depType].(map[string]interface{}); ok {
		for name, version := range depObj {
			if vStr, ok := version.(string); ok {
				deps[name] = vStr
			}
		}
	}
	return deps
}

// determineDependencyTags determines tags for a dependency update based on version change.
func determineDependencyTags(oldVersion, newVersion string) []string {
	tags := []string{"config"}

	// Try to detect if this is a major version bump (breaking change)
	oldMajor := extractMajorVersion(oldVersion)
	newMajor := extractMajorVersion(newVersion)

	if oldMajor != "" && newMajor != "" && oldMajor != newMajor {
		tags = append(tags, "breaking")
	}

	return tags
}

// extractMajorVersion extracts the major version from a semver string.
func extractMajorVersion(version string) string {
	// Strip common prefixes
	version = strings.TrimLeft(version, "^~>=<")

	// Find the first dot
	for i, c := range version {
		if c == '.' {
			return version[:i]
		}
	}
	return version
}

// EnrichConfigSignal enriches a JSON/YAML change signal with semantic config information.
// It analyzes the key path and value to determine if this is a feature flag, timeout, limit, etc.
func EnrichConfigSignal(sig *ChangeSignal) {
	if len(sig.Evidence.Symbols) == 0 {
		return
	}

	keyPath := sig.Evidence.Symbols[0]
	keyCategory := InferConfigKeyCategory(keyPath)

	// Create config change info
	sig.Evidence.ConfigChange = &ConfigChangeInfo{
		Key:         keyPath,
		KeyCategory: keyCategory,
	}

	// Set old/new values if available
	if sig.Evidence.BeforeValue != "" {
		sig.Evidence.ConfigChange.OldValue = sig.Evidence.BeforeValue
	}
	if sig.Evidence.AfterValue != "" {
		sig.Evidence.ConfigChange.NewValue = sig.Evidence.AfterValue
	}

	// Upgrade generic JSON/YAML change to semantic category
	switch keyCategory {
	case ConfigFeatureFlag:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = FeatureFlagChanged
			sig.Weight = SignalWeight[FeatureFlagChanged]
			sig.Tags = appendUnique(sig.Tags, "feature-flag")
		}
	case ConfigTimeout:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = TimeoutChanged
			sig.Weight = SignalWeight[TimeoutChanged]
			sig.Tags = appendUnique(sig.Tags, "tuning")
		}
	case ConfigLimit:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = LimitChanged
			sig.Weight = SignalWeight[LimitChanged]
			sig.Tags = appendUnique(sig.Tags, "tuning")
		}
	case ConfigRetry:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = RetryConfigChanged
			sig.Weight = SignalWeight[RetryConfigChanged]
			sig.Tags = appendUnique(sig.Tags, "tuning")
		}
	case ConfigEndpoint:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = EndpointChanged
			sig.Weight = SignalWeight[EndpointChanged]
			sig.Tags = appendUnique(sig.Tags, "config")
		}
	case ConfigCredential:
		if sig.Category == JSONValueChanged || sig.Category == YAMLValueChanged {
			sig.Category = CredentialChanged
			sig.Weight = SignalWeight[CredentialChanged]
			sig.Tags = appendUnique(sig.Tags, "security")
			// Add warning about credential changes
		}
	}
}

// appendUnique appends a string to a slice if not already present.
func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

// DetectJSONChangesWithSemantics detects JSON changes and enriches them with semantic config information.
func DetectJSONChangesWithSemantics(path string, before, after []byte) ([]*ChangeSignal, error) {
	changes, err := DetectJSONChanges(path, before, after)
	if err != nil {
		return nil, err
	}

	signals := make([]*ChangeSignal, 0, len(changes))
	for _, ct := range changes {
		sig := NewChangeSignal(ct)
		EnrichConfigSignal(sig)
		signals = append(signals, sig)
	}

	return signals, nil
}

// MergeDependencySignals merges dependency-specific signals with regular JSON change signals.
// This provides richer information for package.json files while maintaining backward compatibility.
func MergeDependencySignals(depSignals []*ChangeSignal, jsonChanges []*ChangeType) []*ChangeSignal {
	// Convert JSON changes to signals, excluding dependency-related ones
	depPaths := make(map[string]bool)
	for _, sig := range depSignals {
		for _, sym := range sig.Evidence.Symbols {
			depPaths[sym] = true
		}
	}

	result := append([]*ChangeSignal{}, depSignals...)

	for _, ct := range jsonChanges {
		// Check if this change is already covered by a dependency signal
		isCovered := false
		for _, sym := range ct.Evidence.Symbols {
			if depPaths[sym] {
				isCovered = true
				break
			}
			// Check if it's a sub-path of a dependency
			for depPath := range depPaths {
				if strings.HasPrefix(sym, depPath+".") || strings.HasPrefix(depPath, sym+".") {
					isCovered = true
					break
				}
			}
		}

		if !isCovered {
			result = append(result, NewChangeSignal(ct))
		}
	}

	return result
}
