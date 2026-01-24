// Package intent provides template definitions for intent generation.
package intent

import (
	"regexp"
	"strings"

	"kai-core/detect"
)

// Template represents an intent generation template.
type Template struct {
	ID             string           `json:"id"`
	Pattern        string           `json:"pattern"`        // e.g., "Rename {oldName} to {newName} in {module}"
	Conditions     []MatchCondition `json:"conditions"`
	Priority       int              `json:"priority"`       // Higher priority templates are matched first
	BaseConfidence float64          `json:"baseConfidence"` // 0.0-1.0 confidence when this template matches
}

// MatchCondition represents a condition for template matching.
type MatchCondition struct {
	Type       string `json:"type"`       // "has_category", "category_count", "has_tag", "cluster_type", etc.
	Category   string `json:"category"`   // For category-related conditions
	Comparator string `json:"comparator"` // "eq", "gt", "lt", "gte", "lte"
	Value      interface{} `json:"value"` // The value to compare against
}

// DefaultTemplates defines the standard templates ordered by specificity.
var DefaultTemplates = []Template{
	// === High-specificity templates (high confidence) ===

	// Rename detection (highest specificity)
	{
		ID:             "rename_function",
		Pattern:        "Rename {oldName} to {newName} in {module}",
		Priority:       100,
		BaseConfidence: 0.95,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FUNCTION_RENAMED"},
		},
	},

	// Single function added
	{
		ID:             "add_single_function",
		Pattern:        "Add {functionName} in {module}",
		Priority:       95,
		BaseConfidence: 0.92,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "FUNCTION_ADDED", Comparator: "eq", Value: 1},
			{Type: "category_count", Category: "FUNCTION_REMOVED", Comparator: "eq", Value: 0},
		},
	},

	// Single function removed
	{
		ID:             "remove_single_function",
		Pattern:        "Remove {functionName} from {module}",
		Priority:       95,
		BaseConfidence: 0.92,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "FUNCTION_REMOVED", Comparator: "eq", Value: 1},
			{Type: "category_count", Category: "FUNCTION_ADDED", Comparator: "eq", Value: 0},
		},
	},

	// Multiple functions added
	{
		ID:             "add_multiple_functions",
		Pattern:        "Add {functions} in {module}",
		Priority:       90,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "FUNCTION_ADDED", Comparator: "gt", Value: 1},
			{Type: "category_count", Category: "FUNCTION_REMOVED", Comparator: "eq", Value: 0},
		},
	},

	// Refactor (add + remove functions)
	{
		ID:             "refactor_functions",
		Pattern:        "Refactor {functions} in {module}",
		Priority:       85,
		BaseConfidence: 0.88,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FUNCTION_ADDED"},
			{Type: "has_category", Category: "FUNCTION_REMOVED"},
		},
	},

	// === Medium-specificity templates ===

	// API change (parameter modification)
	{
		ID:             "update_api_parameters",
		Pattern:        "Update {functionName} parameters in {module}",
		Priority:       80,
		BaseConfidence: 0.85,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "PARAMETER_ADDED"},
		},
	},
	{
		ID:             "update_api_parameters_removed",
		Pattern:        "Update {functionName} parameters in {module}",
		Priority:       80,
		BaseConfidence: 0.85,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "PARAMETER_REMOVED"},
		},
	},

	// API surface change
	{
		ID:             "update_api_surface",
		Pattern:        "Update API in {module}",
		Priority:       75,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "API_SURFACE_CHANGED"},
		},
	},

	// Dependency changes
	{
		ID:             "add_dependency",
		Pattern:        "Add {dependencyName} dependency",
		Priority:       75,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "DEPENDENCY_ADDED", Comparator: "eq", Value: 1},
			{Type: "category_count", Category: "DEPENDENCY_REMOVED", Comparator: "eq", Value: 0},
		},
	},
	{
		ID:             "remove_dependency",
		Pattern:        "Remove {dependencyName} dependency",
		Priority:       75,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "DEPENDENCY_REMOVED", Comparator: "eq", Value: 1},
			{Type: "category_count", Category: "DEPENDENCY_ADDED", Comparator: "eq", Value: 0},
		},
	},
	{
		ID:             "update_dependency",
		Pattern:        "Update {dependencyName} to {version}",
		Priority:       75,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "DEPENDENCY_UPDATED", Comparator: "eq", Value: 1},
		},
	},
	{
		ID:             "update_dependencies",
		Pattern:        "Update dependencies in {module}",
		Priority:       70,
		BaseConfidence: 0.85,
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "DEPENDENCY_UPDATED", Comparator: "gt", Value: 1},
		},
	},

	// Function body change
	{
		ID:             "modify_function",
		Pattern:        "Modify {functionName} in {module}",
		Priority:       65,
		BaseConfidence: 0.75,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FUNCTION_BODY_CHANGED"},
		},
	},

	// Import changes
	{
		ID:             "update_imports",
		Pattern:        "Update imports in {module}",
		Priority:       60,
		BaseConfidence: 0.70,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "IMPORT_ADDED"},
		},
	},

	// === Lower-specificity templates ===

	// Condition change
	{
		ID:             "modify_condition",
		Pattern:        "Modify condition in {area}",
		Priority:       55,
		BaseConfidence: 0.65,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "CONDITION_CHANGED"},
		},
	},

	// Constant update
	{
		ID:             "update_constant",
		Pattern:        "Update constant in {area}",
		Priority:       50,
		BaseConfidence: 0.60,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "CONSTANT_UPDATED"},
		},
	},

	// File added
	{
		ID:             "add_file",
		Pattern:        "Add {fileName}",
		Priority:       50,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FILE_ADDED"},
		},
	},

	// File removed
	{
		ID:             "remove_file",
		Pattern:        "Remove {fileName}",
		Priority:       50,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FILE_DELETED"},
		},
	},

	// JSON/YAML config changes
	{
		ID:             "update_json_config",
		Pattern:        "Update {configField} in {fileName}",
		Priority:       45,
		BaseConfidence: 0.55,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "JSON_VALUE_CHANGED"},
		},
	},
	{
		ID:             "update_yaml_config",
		Pattern:        "Update {configField} in {fileName}",
		Priority:       45,
		BaseConfidence: 0.55,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "YAML_VALUE_CHANGED"},
		},
	},

	// Test-specific
	{
		ID:             "update_tests",
		Pattern:        "Update tests for {area}",
		Priority:       40,
		BaseConfidence: 0.60,
		Conditions: []MatchCondition{
			{Type: "cluster_type", Value: "test"},
		},
	},

	// Config-specific
	{
		ID:             "update_config",
		Pattern:        "Update configuration in {module}",
		Priority:       35,
		BaseConfidence: 0.55,
		Conditions: []MatchCondition{
			{Type: "cluster_type", Value: "config"},
		},
	},

	// === Semantic Config Templates ===

	// Feature flag changes
	{
		ID:             "toggle_feature_flag",
		Pattern:        "Toggle {configField} feature flag in {module}",
		Priority:       72,
		BaseConfidence: 0.85,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FEATURE_FLAG_CHANGED"},
		},
	},

	// Timeout changes
	{
		ID:             "update_timeout",
		Pattern:        "Update {configField} timeout in {module}",
		Priority:       68,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "TIMEOUT_CHANGED"},
		},
	},

	// Limit changes
	{
		ID:             "update_limit",
		Pattern:        "Update {configField} limit in {module}",
		Priority:       68,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "LIMIT_CHANGED"},
		},
	},

	// Retry config changes
	{
		ID:             "update_retry_config",
		Pattern:        "Update retry configuration in {module}",
		Priority:       65,
		BaseConfidence: 0.78,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "RETRY_CONFIG_CHANGED"},
		},
	},

	// Endpoint changes
	{
		ID:             "update_endpoint",
		Pattern:        "Update {configField} endpoint in {module}",
		Priority:       67,
		BaseConfidence: 0.80,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "ENDPOINT_CHANGED"},
		},
	},

	// Credential changes (high priority, security-sensitive)
	{
		ID:             "update_credential",
		Pattern:        "Update {configField} credential in {module}",
		Priority:       78,
		BaseConfidence: 0.88,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "CREDENTIAL_CHANGED"},
		},
	},

	// === Schema/Migration Templates ===

	// Schema field added
	{
		ID:             "add_schema_field",
		Pattern:        "Add {schemaField} to {schemaEntity}",
		Priority:       82,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "SCHEMA_FIELD_ADDED"},
		},
	},

	// Schema field removed (breaking)
	{
		ID:             "remove_schema_field",
		Pattern:        "Remove {schemaField} from {schemaEntity}",
		Priority:       82,
		BaseConfidence: 0.90,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "SCHEMA_FIELD_REMOVED"},
		},
	},

	// Schema field changed
	{
		ID:             "update_schema_field",
		Pattern:        "Update {schemaField} in {schemaEntity}",
		Priority:       80,
		BaseConfidence: 0.88,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "SCHEMA_FIELD_CHANGED"},
		},
	},

	// Migration added
	{
		ID:             "add_migration",
		Pattern:        "Add database migration for {migrationName}",
		Priority:       88,
		BaseConfidence: 0.92,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "MIGRATION_ADDED"},
		},
	},

	// === Auth/Security Templates ===

	// Generic auth flow change (compound: function changed + has auth-related symbols)
	{
		ID:             "update_auth_flow",
		Pattern:        "Update authentication flow in {module}",
		Priority:       76,
		BaseConfidence: 0.85,
		Conditions: []MatchCondition{
			{Type: "has_tag", Value: "security"},
			{Type: "has_category", Category: "FUNCTION_BODY_CHANGED"},
		},
	},

	// Permission change
	{
		ID:             "update_permissions",
		Pattern:        "Update permission checks in {module}",
		Priority:       74,
		BaseConfidence: 0.82,
		Conditions: []MatchCondition{
			{Type: "has_tag", Value: "security"},
		},
	},

	// === Fallback templates (lowest confidence) ===

	// Generic file content change
	{
		ID:             "update_file",
		Pattern:        "Update {area} in {module}",
		Priority:       20,
		BaseConfidence: 0.40,
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FILE_CONTENT_CHANGED"},
		},
	},

	// Generic update (catch-all)
	{
		ID:             "generic_update",
		Pattern:        "Update {area} in {module}",
		Priority:       0,
		BaseConfidence: 0.30,
		Conditions:     []MatchCondition{}, // Always matches as fallback
	},
}

// TemplateVariables holds the variables that can be substituted in templates.
type TemplateVariables struct {
	Module         string
	Area           string
	FunctionName   string
	Functions      string // Formatted list of functions
	OldName        string
	NewName        string
	FileName       string
	ConfigField    string
	DependencyName string
	Version        string

	// Schema/migration variables
	SchemaField    string
	SchemaEntity   string
	MigrationName  string

	// Security variables
	Permission     string
	Flow           string
	Setting        string
}

// MatchTemplate checks if a cluster matches a template's conditions.
func MatchTemplate(t *Template, cluster *ChangeCluster) bool {
	if len(t.Conditions) == 0 {
		return true // Fallback templates always match
	}

	for _, cond := range t.Conditions {
		if !matchCondition(cond, cluster) {
			return false
		}
	}

	return true
}

// matchCondition checks if a single condition matches.
func matchCondition(cond MatchCondition, cluster *ChangeCluster) bool {
	switch cond.Type {
	case "has_category":
		category := detect.ChangeCategory(cond.Category)
		return cluster.HasCategory(category)

	case "category_count":
		category := detect.ChangeCategory(cond.Category)
		count := cluster.CategoryCount(category)
		return compareInt(count, cond.Comparator, toInt(cond.Value))

	case "has_tag":
		tagValue, ok := cond.Value.(string)
		if !ok {
			return false
		}
		for _, sig := range cluster.Signals {
			if sig.HasTag(tagValue) {
				return true
			}
		}
		return false

	case "cluster_type":
		typeValue, ok := cond.Value.(string)
		if !ok {
			return false
		}
		return string(cluster.ClusterType) == typeValue

	default:
		return false
	}
}

// compareInt compares two integers based on a comparator string.
func compareInt(a int, comparator string, b int) bool {
	switch comparator {
	case "eq":
		return a == b
	case "gt":
		return a > b
	case "lt":
		return a < b
	case "gte":
		return a >= b
	case "lte":
		return a <= b
	default:
		return false
	}
}

// toInt converts an interface{} to int.
func toInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case int64:
		return int(val)
	default:
		return 0
	}
}

// RenderTemplate renders a template pattern with variables.
func RenderTemplate(pattern string, vars *TemplateVariables) string {
	result := pattern

	replacements := map[string]string{
		"{module}":         vars.Module,
		"{area}":           vars.Area,
		"{functionName}":   vars.FunctionName,
		"{functions}":      vars.Functions,
		"{oldName}":        vars.OldName,
		"{newName}":        vars.NewName,
		"{fileName}":       vars.FileName,
		"{configField}":    vars.ConfigField,
		"{dependencyName}": vars.DependencyName,
		"{version}":        vars.Version,
		// Schema/migration placeholders
		"{schemaField}":    vars.SchemaField,
		"{schemaEntity}":   vars.SchemaEntity,
		"{migrationName}":  vars.MigrationName,
		// Security placeholders
		"{permission}":     vars.Permission,
		"{flow}":           vars.Flow,
		"{setting}":        vars.Setting,
	}

	for placeholder, value := range replacements {
		if value != "" {
			result = strings.ReplaceAll(result, placeholder, value)
		}
	}

	// Clean up any unreplaced placeholders
	re := regexp.MustCompile(`\{[^}]+\}`)
	result = re.ReplaceAllString(result, "")

	// Clean up double spaces
	result = strings.Join(strings.Fields(result), " ")

	return result
}

// ExtractVariables extracts template variables from a cluster.
func ExtractVariables(cluster *ChangeCluster, modules []string) *TemplateVariables {
	vars := &TemplateVariables{
		Module: "General",
		Area:   cluster.PrimaryArea,
	}

	// Set module
	if len(cluster.Modules) > 0 {
		vars.Module = cluster.Modules[0]
	} else if len(modules) > 0 {
		vars.Module = modules[0]
	}

	// Extract function names
	var funcNames []string
	for _, sig := range cluster.Signals {
		for _, sym := range sig.Evidence.Symbols {
			if strings.HasPrefix(sym, "name:") {
				name := strings.TrimPrefix(sym, "name:")
				funcNames = append(funcNames, name)
			}
		}
	}
	funcNames = uniqueStrings(funcNames)

	if len(funcNames) > 0 {
		vars.FunctionName = funcNames[0]
		vars.Functions = formatFunctionList(funcNames)
	}

	// Extract rename info
	for _, sig := range cluster.Signals {
		if sig.Category == detect.FunctionRenamed {
			vars.OldName = sig.Evidence.OldName
			vars.NewName = sig.Evidence.NewName
			break
		}
	}

	// Extract file names
	if len(cluster.Files) > 0 {
		vars.FileName = extractBaseName(cluster.Files[0])
	}

	// Extract config field for JSON/YAML changes
	for _, sig := range cluster.Signals {
		if sig.Category == detect.JSONValueChanged ||
			sig.Category == detect.YAMLValueChanged ||
			sig.Category == detect.JSONFieldAdded ||
			sig.Category == detect.JSONFieldRemoved {
			if len(sig.Evidence.Symbols) > 0 {
				vars.ConfigField = sig.Evidence.Symbols[0]
				break
			}
		}
	}

	// Extract dependency info
	for _, sig := range cluster.Signals {
		if sig.Category == detect.DependencyAdded ||
			sig.Category == detect.DependencyRemoved ||
			sig.Category == detect.DependencyUpdated {
			vars.DependencyName = sig.Evidence.NewName
			if vars.DependencyName == "" {
				vars.DependencyName = sig.Evidence.OldName
			}
			vars.Version = sig.Evidence.AfterValue
			break
		}
	}

	// Extract semantic config fields
	for _, sig := range cluster.Signals {
		if sig.Category == detect.FeatureFlagChanged ||
			sig.Category == detect.TimeoutChanged ||
			sig.Category == detect.LimitChanged ||
			sig.Category == detect.RetryConfigChanged ||
			sig.Category == detect.EndpointChanged ||
			sig.Category == detect.CredentialChanged {
			if len(sig.Evidence.Symbols) > 0 {
				vars.ConfigField = extractConfigFieldNameForCategory(sig.Evidence.Symbols[0], sig.Category)
			}
			if sig.Evidence.ConfigChange != nil {
				vars.Setting = sig.Evidence.ConfigChange.Key
			}
			break
		}
	}

	// Extract schema/migration info
	for _, sig := range cluster.Signals {
		if sig.Category == detect.SchemaFieldAdded ||
			sig.Category == detect.SchemaFieldRemoved ||
			sig.Category == detect.SchemaFieldChanged {
			if len(sig.Evidence.Symbols) > 0 {
				entity, field := parseSchemaSymbol(sig.Evidence.Symbols[0])
				vars.SchemaEntity = entity
				vars.SchemaField = field
			}
			break
		}
		if sig.Category == detect.MigrationAdded {
			if len(sig.Evidence.Symbols) > 0 {
				vars.MigrationName = sig.Evidence.Symbols[0]
			}
			break
		}
	}

	return vars
}

// extractConfigFieldName extracts a clean field name from a config path.
func extractConfigFieldNameForCategory(path string, category detect.ChangeCategory) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}

	last := parts[len(parts)-1]
	keyword := configKeywordForCategory(category)
	if keyword == "" || !strings.EqualFold(last, keyword) {
		return last
	}

	// Use the parent context when the last token is the category keyword.
	if len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:len(parts)-1], " ")
	}
	if len(parts) == 2 {
		return parts[0]
	}
	return last
}

func configKeywordForCategory(category detect.ChangeCategory) string {
	switch category {
	case detect.TimeoutChanged:
		return "timeout"
	case detect.LimitChanged:
		return "limit"
	case detect.RetryConfigChanged:
		return "retry"
	case detect.EndpointChanged:
		return "endpoint"
	case detect.CredentialChanged:
		return "credential"
	case detect.FeatureFlagChanged:
		return "flag"
	default:
		return ""
	}
}

// parseSchemaSymbol parses a schema symbol like "model:User" or "table:users.email"
func parseSchemaSymbol(sym string) (entity, field string) {
	// Handle prefixed symbols like "model:User" or "type:Query"
	if idx := strings.Index(sym, ":"); idx > 0 {
		sym = sym[idx+1:]
	}

	// Handle table.column format
	if idx := strings.Index(sym, "."); idx > 0 {
		return sym[:idx], sym[idx+1:]
	}

	return sym, ""
}

// formatFunctionList formats a list of function names.
func formatFunctionList(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	if len(names) == 2 {
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:2], ", ") + " and others"
}

// extractBaseName extracts the base file name without extension.
func extractBaseName(path string) string {
	// Get just the filename
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]

	// Remove extension
	if idx := strings.LastIndex(name, "."); idx != -1 {
		name = name[:idx]
	}

	return name
}

// uniqueStrings returns unique strings while preserving order.
func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
