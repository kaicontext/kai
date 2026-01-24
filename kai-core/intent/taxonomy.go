// Package intent provides taxonomy definitions for intent classification.
package intent

import "strings"

// IntentCategory represents a high-level intent category.
// These are the semantic categories that users understand.
type IntentCategory string

const (
	// === Code Structure Changes ===

	// IntentRename - Renaming symbols (functions, variables, types, files)
	IntentRename IntentCategory = "rename"

	// IntentAddFeature - Adding new functionality
	IntentAddFeature IntentCategory = "add_feature"

	// IntentRemoveFeature - Removing functionality
	IntentRemoveFeature IntentCategory = "remove_feature"

	// IntentRefactor - Restructuring without changing behavior
	IntentRefactor IntentCategory = "refactor"

	// IntentFixBug - Correcting incorrect behavior
	IntentFixBug IntentCategory = "fix_bug"

	// === API Changes ===

	// IntentAPIChange - Modifying public interfaces
	IntentAPIChange IntentCategory = "api_change"

	// IntentAPIBreaking - Breaking API changes (removed params, changed signatures)
	IntentAPIBreaking IntentCategory = "api_breaking"

	// === Configuration Changes ===

	// IntentConfigChange - General configuration updates
	IntentConfigChange IntentCategory = "config_change"

	// IntentFeatureFlag - Feature flag toggling
	IntentFeatureFlag IntentCategory = "feature_flag"

	// IntentTuning - Performance tuning (timeouts, limits, thresholds)
	IntentTuning IntentCategory = "tuning"

	// IntentEnvironment - Environment-specific settings
	IntentEnvironment IntentCategory = "environment"

	// === Schema & Data Changes ===

	// IntentSchemaMigration - Database schema changes
	IntentSchemaMigration IntentCategory = "schema_migration"

	// IntentDataMigration - Data transformation scripts
	IntentDataMigration IntentCategory = "data_migration"

	// IntentModelChange - Data model/type definition changes
	IntentModelChange IntentCategory = "model_change"

	// === Dependency Changes ===

	// IntentDependencyAdd - Adding new dependencies
	IntentDependencyAdd IntentCategory = "dependency_add"

	// IntentDependencyRemove - Removing dependencies
	IntentDependencyRemove IntentCategory = "dependency_remove"

	// IntentDependencyUpdate - Updating dependency versions
	IntentDependencyUpdate IntentCategory = "dependency_update"

	// IntentDependencyMajor - Major version upgrades (potential breaking)
	IntentDependencyMajor IntentCategory = "dependency_major"

	// === Security & Auth Changes ===

	// IntentAuthChange - Authentication flow modifications
	IntentAuthChange IntentCategory = "auth_change"

	// IntentPermissionChange - Authorization/permission changes
	IntentPermissionChange IntentCategory = "permission_change"

	// IntentSecurityFix - Security vulnerability fixes
	IntentSecurityFix IntentCategory = "security_fix"

	// IntentSecretRotation - Credential/secret updates
	IntentSecretRotation IntentCategory = "secret_rotation"

	// === Testing Changes ===

	// IntentAddTest - Adding new tests
	IntentAddTest IntentCategory = "add_test"

	// IntentUpdateTest - Modifying existing tests
	IntentUpdateTest IntentCategory = "update_test"

	// IntentTestFixture - Test fixture/mock changes
	IntentTestFixture IntentCategory = "test_fixture"

	// === Documentation ===

	// IntentDocumentation - Documentation updates
	IntentDocumentation IntentCategory = "documentation"

	// IntentComment - Code comment changes
	IntentComment IntentCategory = "comment"

	// === Build & CI ===

	// IntentBuildConfig - Build configuration changes
	IntentBuildConfig IntentCategory = "build_config"

	// IntentCIConfig - CI/CD pipeline changes
	IntentCIConfig IntentCategory = "ci_config"

	// === Fallback ===

	// IntentMixed - Multiple unrelated changes
	IntentMixed IntentCategory = "mixed"

	// IntentUnknown - Cannot determine intent
	IntentUnknown IntentCategory = "unknown"
)

// IntentCategoryInfo provides metadata about an intent category.
type IntentCategoryInfo struct {
	Category    IntentCategory
	Label       string   // Human-readable label
	Description string   // What this category means
	Keywords    []string // Keywords that suggest this category
	Weight      float64  // Base importance weight (0.0-1.0)
	Breaking    bool     // Whether this is typically a breaking change
}

// CategoryRegistry maps intent categories to their metadata.
var CategoryRegistry = map[IntentCategory]IntentCategoryInfo{
	IntentRename: {
		Category:    IntentRename,
		Label:       "Rename",
		Description: "Renaming symbols without changing behavior",
		Keywords:    []string{"rename", "move", "alias"},
		Weight:      0.9,
		Breaking:    false,
	},
	IntentAddFeature: {
		Category:    IntentAddFeature,
		Label:       "Add Feature",
		Description: "Adding new functionality or capabilities",
		Keywords:    []string{"add", "new", "implement", "create", "introduce"},
		Weight:      0.85,
		Breaking:    false,
	},
	IntentRemoveFeature: {
		Category:    IntentRemoveFeature,
		Label:       "Remove Feature",
		Description: "Removing existing functionality",
		Keywords:    []string{"remove", "delete", "deprecate", "drop"},
		Weight:      0.85,
		Breaking:    true,
	},
	IntentRefactor: {
		Category:    IntentRefactor,
		Label:       "Refactor",
		Description: "Restructuring code without changing external behavior",
		Keywords:    []string{"refactor", "restructure", "reorganize", "cleanup", "extract", "inline"},
		Weight:      0.75,
		Breaking:    false,
	},
	IntentFixBug: {
		Category:    IntentFixBug,
		Label:       "Fix Bug",
		Description: "Correcting incorrect behavior",
		Keywords:    []string{"fix", "bug", "issue", "correct", "patch", "resolve"},
		Weight:      0.8,
		Breaking:    false,
	},
	IntentAPIChange: {
		Category:    IntentAPIChange,
		Label:       "API Change",
		Description: "Modifying public interfaces (non-breaking)",
		Keywords:    []string{"api", "interface", "signature", "endpoint"},
		Weight:      0.85,
		Breaking:    false,
	},
	IntentAPIBreaking: {
		Category:    IntentAPIBreaking,
		Label:       "Breaking API Change",
		Description: "Breaking changes to public interfaces",
		Keywords:    []string{"breaking", "removed", "changed signature"},
		Weight:      0.95,
		Breaking:    true,
	},
	IntentConfigChange: {
		Category:    IntentConfigChange,
		Label:       "Config Change",
		Description: "General configuration updates",
		Keywords:    []string{"config", "configuration", "setting", "option"},
		Weight:      0.6,
		Breaking:    false,
	},
	IntentFeatureFlag: {
		Category:    IntentFeatureFlag,
		Label:       "Feature Flag",
		Description: "Feature flag or toggle changes",
		Keywords:    []string{"flag", "toggle", "feature", "enabled", "disabled"},
		Weight:      0.7,
		Breaking:    false,
	},
	IntentTuning: {
		Category:    IntentTuning,
		Label:       "Tuning",
		Description: "Performance or behavior tuning",
		Keywords:    []string{"timeout", "limit", "threshold", "retry", "interval", "max", "min"},
		Weight:      0.65,
		Breaking:    false,
	},
	IntentEnvironment: {
		Category:    IntentEnvironment,
		Label:       "Environment",
		Description: "Environment-specific configuration",
		Keywords:    []string{"env", "environment", "prod", "staging", "dev"},
		Weight:      0.6,
		Breaking:    false,
	},
	IntentSchemaMigration: {
		Category:    IntentSchemaMigration,
		Label:       "Schema Migration",
		Description: "Database schema changes",
		Keywords:    []string{"schema", "migration", "table", "column", "index", "constraint"},
		Weight:      0.9,
		Breaking:    true,
	},
	IntentDataMigration: {
		Category:    IntentDataMigration,
		Label:       "Data Migration",
		Description: "Data transformation or migration scripts",
		Keywords:    []string{"migrate", "transform", "backfill", "seed"},
		Weight:      0.85,
		Breaking:    false,
	},
	IntentModelChange: {
		Category:    IntentModelChange,
		Label:       "Model Change",
		Description: "Data model or type definition changes",
		Keywords:    []string{"model", "type", "struct", "interface", "schema"},
		Weight:      0.8,
		Breaking:    false,
	},
	IntentDependencyAdd: {
		Category:    IntentDependencyAdd,
		Label:       "Add Dependency",
		Description: "Adding new external dependencies",
		Keywords:    []string{"add", "install", "dependency", "package"},
		Weight:      0.75,
		Breaking:    false,
	},
	IntentDependencyRemove: {
		Category:    IntentDependencyRemove,
		Label:       "Remove Dependency",
		Description: "Removing external dependencies",
		Keywords:    []string{"remove", "uninstall", "dependency"},
		Weight:      0.75,
		Breaking:    true,
	},
	IntentDependencyUpdate: {
		Category:    IntentDependencyUpdate,
		Label:       "Update Dependency",
		Description: "Updating dependency versions",
		Keywords:    []string{"update", "upgrade", "bump", "version"},
		Weight:      0.7,
		Breaking:    false,
	},
	IntentDependencyMajor: {
		Category:    IntentDependencyMajor,
		Label:       "Major Dependency Update",
		Description: "Major version upgrades with potential breaking changes",
		Keywords:    []string{"major", "breaking", "upgrade"},
		Weight:      0.85,
		Breaking:    true,
	},
	IntentAuthChange: {
		Category:    IntentAuthChange,
		Label:       "Auth Change",
		Description: "Authentication flow modifications",
		Keywords:    []string{"auth", "login", "logout", "session", "token", "oauth", "jwt"},
		Weight:      0.9,
		Breaking:    false,
	},
	IntentPermissionChange: {
		Category:    IntentPermissionChange,
		Label:       "Permission Change",
		Description: "Authorization and permission changes",
		Keywords:    []string{"permission", "role", "access", "authorize", "rbac", "acl"},
		Weight:      0.9,
		Breaking:    false,
	},
	IntentSecurityFix: {
		Category:    IntentSecurityFix,
		Label:       "Security Fix",
		Description: "Security vulnerability fixes",
		Keywords:    []string{"security", "vulnerability", "cve", "xss", "injection", "sanitize"},
		Weight:      0.95,
		Breaking:    false,
	},
	IntentSecretRotation: {
		Category:    IntentSecretRotation,
		Label:       "Secret Rotation",
		Description: "Credential or secret updates",
		Keywords:    []string{"secret", "key", "credential", "password", "rotate"},
		Weight:      0.85,
		Breaking:    false,
	},
	IntentAddTest: {
		Category:    IntentAddTest,
		Label:       "Add Test",
		Description: "Adding new tests",
		Keywords:    []string{"test", "spec", "coverage"},
		Weight:      0.7,
		Breaking:    false,
	},
	IntentUpdateTest: {
		Category:    IntentUpdateTest,
		Label:       "Update Test",
		Description: "Modifying existing tests",
		Keywords:    []string{"test", "spec", "fixture", "mock"},
		Weight:      0.6,
		Breaking:    false,
	},
	IntentTestFixture: {
		Category:    IntentTestFixture,
		Label:       "Test Fixture",
		Description: "Test fixture or mock changes",
		Keywords:    []string{"fixture", "mock", "stub", "fake", "factory"},
		Weight:      0.5,
		Breaking:    false,
	},
	IntentDocumentation: {
		Category:    IntentDocumentation,
		Label:       "Documentation",
		Description: "Documentation updates",
		Keywords:    []string{"doc", "readme", "changelog", "comment"},
		Weight:      0.4,
		Breaking:    false,
	},
	IntentComment: {
		Category:    IntentComment,
		Label:       "Comment",
		Description: "Code comment changes only",
		Keywords:    []string{"comment", "jsdoc", "docstring"},
		Weight:      0.3,
		Breaking:    false,
	},
	IntentBuildConfig: {
		Category:    IntentBuildConfig,
		Label:       "Build Config",
		Description: "Build configuration changes",
		Keywords:    []string{"build", "webpack", "vite", "rollup", "esbuild", "makefile"},
		Weight:      0.6,
		Breaking:    false,
	},
	IntentCIConfig: {
		Category:    IntentCIConfig,
		Label:       "CI Config",
		Description: "CI/CD pipeline changes",
		Keywords:    []string{"ci", "cd", "pipeline", "workflow", "action", "jenkins"},
		Weight:      0.6,
		Breaking:    false,
	},
	IntentMixed: {
		Category:    IntentMixed,
		Label:       "Mixed Changes",
		Description: "Multiple unrelated changes in one commit",
		Keywords:    []string{},
		Weight:      0.3,
		Breaking:    false,
	},
	IntentUnknown: {
		Category:    IntentUnknown,
		Label:       "Unknown",
		Description: "Cannot determine intent from changes",
		Keywords:    []string{},
		Weight:      0.1,
		Breaking:    false,
	},
}

// FileLayerHint represents a layer classification based on file path.
type FileLayerHint string

const (
	LayerController FileLayerHint = "controller"
	LayerService    FileLayerHint = "service"
	LayerRepository FileLayerHint = "repository"
	LayerModel      FileLayerHint = "model"
	LayerMiddleware FileLayerHint = "middleware"
	LayerHandler    FileLayerHint = "handler"
	LayerAPI        FileLayerHint = "api"
	LayerDB         FileLayerHint = "db"
	LayerMigration  FileLayerHint = "migration"
	LayerConfig     FileLayerHint = "config"
	LayerTest       FileLayerHint = "test"
	LayerUtil       FileLayerHint = "util"
	LayerUnknown    FileLayerHint = "unknown"
)

// LayerPatterns maps path patterns to layer hints.
var LayerPatterns = []struct {
	Pattern string
	Layer   FileLayerHint
}{
	// Common directory patterns
	{"controller", LayerController},
	{"controllers", LayerController},
	{"handler", LayerHandler},
	{"handlers", LayerHandler},
	{"service", LayerService},
	{"services", LayerService},
	{"repository", LayerRepository},
	{"repositories", LayerRepository},
	{"repo", LayerRepository},
	{"repos", LayerRepository},
	{"model", LayerModel},
	{"models", LayerModel},
	{"entity", LayerModel},
	{"entities", LayerModel},
	{"middleware", LayerMiddleware},
	{"middlewares", LayerMiddleware},
	{"api", LayerAPI},
	{"routes", LayerAPI},
	{"router", LayerAPI},
	{"db", LayerDB},
	{"database", LayerDB},
	{"migration", LayerMigration},
	{"migrations", LayerMigration},
	{"config", LayerConfig},
	{"configuration", LayerConfig},
	{"settings", LayerConfig},
	{"test", LayerTest},
	{"tests", LayerTest},
	{"__tests__", LayerTest},
	{"spec", LayerTest},
	{"specs", LayerTest},
	{"util", LayerUtil},
	{"utils", LayerUtil},
	{"helper", LayerUtil},
	{"helpers", LayerUtil},
	{"lib", LayerUtil},
	{"common", LayerUtil},
	{"shared", LayerUtil},
}

// SymbolRole represents the role of a code symbol.
type SymbolRole string

const (
	RoleHandler    SymbolRole = "handler"
	RoleController SymbolRole = "controller"
	RoleValidator  SymbolRole = "validator"
	RoleSerializer SymbolRole = "serializer"
	RoleFactory    SymbolRole = "factory"
	RoleBuilder    SymbolRole = "builder"
	RoleHelper     SymbolRole = "helper"
	RoleCallback   SymbolRole = "callback"
	RoleHook       SymbolRole = "hook"
	RoleMiddleware SymbolRole = "middleware"
	RoleUnknown    SymbolRole = "unknown"
)

// RolePatterns maps name patterns to symbol roles.
var RolePatterns = []struct {
	Suffix string
	Prefix string
	Role   SymbolRole
}{
	// Suffix patterns
	{Suffix: "Handler", Role: RoleHandler},
	{Suffix: "Controller", Role: RoleController},
	{Suffix: "Validator", Role: RoleValidator},
	{Suffix: "Serializer", Role: RoleSerializer},
	{Suffix: "Factory", Role: RoleFactory},
	{Suffix: "Builder", Role: RoleBuilder},
	{Suffix: "Helper", Role: RoleHelper},
	{Suffix: "Callback", Role: RoleCallback},
	{Suffix: "Hook", Role: RoleHook},
	{Suffix: "Middleware", Role: RoleMiddleware},
	// Prefix patterns
	{Prefix: "handle", Role: RoleHandler},
	{Prefix: "validate", Role: RoleValidator},
	{Prefix: "serialize", Role: RoleSerializer},
	{Prefix: "create", Role: RoleFactory},
	{Prefix: "build", Role: RoleBuilder},
	{Prefix: "use", Role: RoleHook}, // React hooks
	{Prefix: "on", Role: RoleCallback},
}

// ConfigKeyCategory represents categories of configuration keys.
type ConfigKeyCategory string

const (
	ConfigFeatureFlag ConfigKeyCategory = "feature_flag"
	ConfigTimeout     ConfigKeyCategory = "timeout"
	ConfigLimit       ConfigKeyCategory = "limit"
	ConfigRetry       ConfigKeyCategory = "retry"
	ConfigEndpoint    ConfigKeyCategory = "endpoint"
	ConfigCredential  ConfigKeyCategory = "credential"
	ConfigGeneral     ConfigKeyCategory = "general"
)

// ConfigKeyPatterns maps config key patterns to categories.
var ConfigKeyPatterns = []struct {
	Contains string
	Category ConfigKeyCategory
}{
	// Feature flags
	{"enabled", ConfigFeatureFlag},
	{"disabled", ConfigFeatureFlag},
	{"feature", ConfigFeatureFlag},
	{"flag", ConfigFeatureFlag},
	{"toggle", ConfigFeatureFlag},
	// Timeouts
	{"timeout", ConfigTimeout},
	{"ttl", ConfigTimeout},
	{"expire", ConfigTimeout},
	{"duration", ConfigTimeout},
	// Limits
	{"limit", ConfigLimit},
	{"max", ConfigLimit},
	{"min", ConfigLimit},
	{"threshold", ConfigLimit},
	{"size", ConfigLimit},
	{"count", ConfigLimit},
	// Retry
	{"retry", ConfigRetry},
	{"attempt", ConfigRetry},
	{"backoff", ConfigRetry},
	// Endpoints
	{"url", ConfigEndpoint},
	{"host", ConfigEndpoint},
	{"port", ConfigEndpoint},
	{"endpoint", ConfigEndpoint},
	{"api", ConfigEndpoint},
	// Credentials (should trigger warnings)
	{"secret", ConfigCredential},
	{"key", ConfigCredential},
	{"token", ConfigCredential},
	{"password", ConfigCredential},
	{"credential", ConfigCredential},
}

// InferLayerFromPath determines the layer hint from a file path.
func InferLayerFromPath(path string) FileLayerHint {
	lowerPath := strings.ToLower(path)
	for _, lp := range LayerPatterns {
		if strings.Contains(lowerPath, "/"+lp.Pattern+"/") ||
			strings.Contains(lowerPath, "/"+lp.Pattern+".") ||
			strings.HasSuffix(lowerPath, "/"+lp.Pattern) {
			return lp.Layer
		}
	}
	return LayerUnknown
}

// InferSymbolRole determines the role from a symbol name.
func InferSymbolRole(name string) SymbolRole {
	for _, rp := range RolePatterns {
		if rp.Suffix != "" && strings.HasSuffix(name, rp.Suffix) {
			return rp.Role
		}
		if rp.Prefix != "" && strings.HasPrefix(strings.ToLower(name), strings.ToLower(rp.Prefix)) {
			return rp.Role
		}
	}
	return RoleUnknown
}

// InferConfigKeyCategory determines the category from a config key name.
func InferConfigKeyCategory(key string) ConfigKeyCategory {
	lowerKey := strings.ToLower(key)
	for _, ckp := range ConfigKeyPatterns {
		if strings.Contains(lowerKey, ckp.Contains) {
			return ckp.Category
		}
	}
	return ConfigGeneral
}

// GetCategoryInfo returns the metadata for an intent category.
func GetCategoryInfo(cat IntentCategory) IntentCategoryInfo {
	if info, ok := CategoryRegistry[cat]; ok {
		return info
	}
	return CategoryRegistry[IntentUnknown]
}

// IsBreakingCategory returns true if the intent category typically represents a breaking change.
func IsBreakingCategory(cat IntentCategory) bool {
	if info, ok := CategoryRegistry[cat]; ok {
		return info.Breaking
	}
	return false
}

// GetCategoryWeight returns the base weight for an intent category.
func GetCategoryWeight(cat IntentCategory) float64 {
	if info, ok := CategoryRegistry[cat]; ok {
		return info.Weight
	}
	return 0.1
}
