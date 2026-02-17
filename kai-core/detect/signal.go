// Package detect provides extended signal types for change detection with confidence scoring.
package detect

import "strings"

// ChangeSignal extends ChangeType with weight and confidence scoring.
type ChangeSignal struct {
	Category   ChangeCategory   `json:"category"`
	Evidence   ExtendedEvidence `json:"evidence"`
	Weight     float64          `json:"weight"`     // 0.0-1.0 importance of this signal
	Confidence float64          `json:"confidence"` // 0.0-1.0 detection confidence
	Tags       []string         `json:"tags"`       // ["breaking", "api", "test", "config"]
}

// ExtendedEvidence contains enhanced evidence for change detection.
type ExtendedEvidence struct {
	FileRanges  []FileRange      `json:"fileRanges"`
	Symbols     []string         `json:"symbols"`     // symbol node IDs as hex
	BeforeValue string           `json:"beforeValue"` // for constant/value changes
	AfterValue  string           `json:"afterValue"`
	OldName     string           `json:"oldName"` // for renames
	NewName     string           `json:"newName"`
	Signature   *SignatureChange `json:"signature,omitempty"` // for API changes

	// Enhanced metadata fields
	SymbolMeta   *SymbolMetadata   `json:"symbolMeta,omitempty"`   // Rich symbol information
	ConfigChange *ConfigChangeInfo `json:"configChange,omitempty"` // For config changes
	Layer        FileLayerHint     `json:"layer,omitempty"`        // Architectural layer
	Module       string            `json:"module,omitempty"`       // Inferred module name
}

// SignatureChange captures function signature changes.
type SignatureChange struct {
	OldParams      string `json:"oldParams"`
	NewParams      string `json:"newParams"`
	OldReturnType  string `json:"oldReturnType"`
	NewReturnType  string `json:"newReturnType"`

	// Enhanced parameter details
	ParamsAdded    []ParameterInfo `json:"paramsAdded,omitempty"`
	ParamsRemoved  []ParameterInfo `json:"paramsRemoved,omitempty"`
	ParamsChanged  []ParameterDiff `json:"paramsChanged,omitempty"`
	ReturnChanged  bool            `json:"returnChanged,omitempty"`
}

// ParameterInfo describes a function parameter.
type ParameterInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
	IsOptional   bool   `json:"isOptional,omitempty"`
	IsRest       bool   `json:"isRest,omitempty"` // ...args
	Position     int    `json:"position"`
}

// ParameterDiff describes a change to a parameter.
type ParameterDiff struct {
	Name        string `json:"name"`
	OldType     string `json:"oldType,omitempty"`
	NewType     string `json:"newType,omitempty"`
	OldDefault  string `json:"oldDefault,omitempty"`
	NewDefault  string `json:"newDefault,omitempty"`
	Position    int    `json:"position"`
	TypeChanged bool   `json:"typeChanged,omitempty"`
}

// FileLayerHint represents architectural layer based on file path.
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

// SymbolRole represents the role/purpose of a code symbol.
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

// ConfigKeyCategory categorizes configuration key types.
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

// SymbolMetadata provides enriched information about a code symbol.
type SymbolMetadata struct {
	Name      string        `json:"name"`
	ShortName string        `json:"shortName,omitempty"` // Without namespace prefix
	Role      SymbolRole    `json:"role,omitempty"`
	Layer     FileLayerHint `json:"layer,omitempty"`
	Module    string        `json:"module,omitempty"`
	FilePath  string        `json:"filePath,omitempty"`
}

// ConfigChangeInfo provides detailed information about config changes.
type ConfigChangeInfo struct {
	Key         string            `json:"key"`
	KeyCategory ConfigKeyCategory `json:"keyCategory"`
	OldValue    interface{}       `json:"oldValue,omitempty"`
	NewValue    interface{}       `json:"newValue,omitempty"`
	ValueType   string            `json:"valueType,omitempty"` // "string", "number", "boolean", "object", "array"
}

// SignalWeight defines standard weights for different change categories.
var SignalWeight = map[ChangeCategory]float64{
	// High impact changes
	FunctionRenamed:     0.9,
	APISurfaceChanged:   0.9,
	ParameterAdded:      0.85,
	ParameterRemoved:    0.85,
	FunctionAdded:       0.8,
	FunctionRemoved:     0.8,
	DependencyAdded:     0.75,
	DependencyRemoved:   0.75,
	DependencyUpdated:   0.7,

	// Medium impact changes
	FunctionBodyChanged: 0.6,
	ImportAdded:         0.5,
	ImportRemoved:       0.5,
	ConditionChanged:    0.5,
	ConstantUpdated:     0.4,

	// Semantic config changes (medium-high impact)
	FeatureFlagChanged:  0.7,
	CredentialChanged:   0.85, // Security-sensitive
	EndpointChanged:     0.6,
	TimeoutChanged:      0.55,
	LimitChanged:        0.55,
	RetryConfigChanged:  0.5,

	// Schema/migration changes (high impact)
	SchemaFieldAdded:    0.8,
	SchemaFieldRemoved:  0.85, // Breaking
	SchemaFieldChanged:  0.8,
	MigrationAdded:      0.9,

	// Low impact changes (config/data)
	JSONFieldAdded:      0.3,
	JSONFieldRemoved:    0.3,
	JSONValueChanged:    0.2,
	JSONArrayChanged:    0.2,
	YAMLKeyAdded:        0.3,
	YAMLKeyRemoved:      0.3,
	YAMLValueChanged:    0.2,

	// File level changes (lowest)
	FileAdded:           0.5,
	FileDeleted:         0.5,
	FileContentChanged:  0.1,
}

// NewChangeSignal creates a ChangeSignal from a ChangeType with default weight and confidence.
func NewChangeSignal(ct *ChangeType) *ChangeSignal {
	weight := SignalWeight[ct.Category]
	if weight == 0 {
		weight = 0.1 // default for unknown categories
	}

	sig := &ChangeSignal{
		Category: ct.Category,
		Evidence: ExtendedEvidence{
			FileRanges: ct.Evidence.FileRanges,
			Symbols:    ct.Evidence.Symbols,
		},
		Weight:     weight,
		Confidence: 1.0, // Default high confidence for AST-based detection
		Tags:       inferTags(ct),
	}

	// Automatically enrich with layer/role/module metadata
	EnrichSignalWithMetadata(sig)

	return sig
}

// inferTags determines tags based on the change type and evidence.
func inferTags(ct *ChangeType) []string {
	var tags []string

	// API-related changes
	switch ct.Category {
	case APISurfaceChanged, ParameterAdded, ParameterRemoved, FunctionRenamed:
		tags = append(tags, "api")
	}

	// Breaking changes
	switch ct.Category {
	case FunctionRemoved, ParameterRemoved, DependencyRemoved, SchemaFieldRemoved:
		tags = append(tags, "breaking")
	}

	// Security-related changes
	switch ct.Category {
	case CredentialChanged:
		tags = append(tags, "security")
	}

	// Tuning/config changes
	switch ct.Category {
	case TimeoutChanged, LimitChanged, RetryConfigChanged:
		tags = append(tags, "tuning")
	case FeatureFlagChanged:
		tags = append(tags, "feature-flag")
	case EndpointChanged:
		tags = append(tags, "config")
	}

	// Schema/migration changes
	switch ct.Category {
	case SchemaFieldAdded, SchemaFieldRemoved, SchemaFieldChanged, MigrationAdded:
		tags = append(tags, "schema")
	}

	// Test file detection (based on path)
	for _, fr := range ct.Evidence.FileRanges {
		if isTestFile(fr.Path) {
			tags = append(tags, "test")
			break
		}
	}

	// Config file detection
	for _, fr := range ct.Evidence.FileRanges {
		if isConfigFile(fr.Path) {
			tags = append(tags, "config")
			break
		}
	}

	return tags
}

// isTestFile checks if a path is a test file.
func isTestFile(path string) bool {
	// Common test file patterns
	patterns := []string{"_test.go", ".test.js", ".test.ts", ".spec.js", ".spec.ts", "test_", "_test.py", "_spec.rb", "_test.rb"}
	for _, p := range patterns {
		if len(path) >= len(p) && path[len(path)-len(p):] == p {
			return true
		}
		if len(path) >= len(p) && path[:min(len(p), len(path))] == p {
			return true
		}
	}
	return false
}

// isConfigFile checks if a path is a config file.
func isConfigFile(path string) bool {
	configFiles := []string{
		"package.json", "tsconfig.json", "jest.config", "webpack.config",
		".eslintrc", ".prettierrc", "Makefile", "Dockerfile",
		".yaml", ".yml", ".toml", ".ini", ".env",
	}
	for _, cf := range configFiles {
		if len(path) >= len(cf) && path[len(path)-len(cf):] == cf {
			return true
		}
	}
	return false
}

// min returns the minimum of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ConvertToSignals converts a slice of ChangeTypes to ChangeSignals.
func ConvertToSignals(changeTypes []*ChangeType) []*ChangeSignal {
	signals := make([]*ChangeSignal, 0, len(changeTypes))
	for _, ct := range changeTypes {
		signals = append(signals, NewChangeSignal(ct))
	}
	return signals
}

// GetSignalPayload returns the payload for a ChangeSignal node.
func GetSignalPayload(cs *ChangeSignal) map[string]interface{} {
	fileRanges := make([]interface{}, len(cs.Evidence.FileRanges))
	for i, fr := range cs.Evidence.FileRanges {
		fileRanges[i] = map[string]interface{}{
			"path":  fr.Path,
			"start": fr.Start,
			"end":   fr.End,
		}
	}

	symbols := make([]interface{}, len(cs.Evidence.Symbols))
	for i, s := range cs.Evidence.Symbols {
		symbols[i] = s
	}

	evidence := map[string]interface{}{
		"fileRanges":  fileRanges,
		"symbols":     symbols,
		"beforeValue": cs.Evidence.BeforeValue,
		"afterValue":  cs.Evidence.AfterValue,
		"oldName":     cs.Evidence.OldName,
		"newName":     cs.Evidence.NewName,
	}

	if cs.Evidence.Signature != nil {
		evidence["signature"] = map[string]interface{}{
			"oldParams":     cs.Evidence.Signature.OldParams,
			"newParams":     cs.Evidence.Signature.NewParams,
			"oldReturnType": cs.Evidence.Signature.OldReturnType,
			"newReturnType": cs.Evidence.Signature.NewReturnType,
		}
	}

	tags := make([]interface{}, len(cs.Tags))
	for i, t := range cs.Tags {
		tags[i] = t
	}

	return map[string]interface{}{
		"category":   string(cs.Category),
		"evidence":   evidence,
		"weight":     cs.Weight,
		"confidence": cs.Confidence,
		"tags":       tags,
	}
}

// HasTag checks if a signal has a specific tag.
func (cs *ChangeSignal) HasTag(tag string) bool {
	for _, t := range cs.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// InferLayerFromPath determines the architectural layer from a file path.
func InferLayerFromPath(path string) FileLayerHint {
	lowerPath := strings.ToLower(path)

	layerPatterns := []struct {
		Pattern string
		Layer   FileLayerHint
	}{
		{"controller", LayerController},
		{"controllers", LayerController},
		{"handler", LayerHandler},
		{"handlers", LayerHandler},
		{"service", LayerService},
		{"services", LayerService},
		{"repository", LayerRepository},
		{"repositories", LayerRepository},
		{"repo/", LayerRepository},
		{"repos/", LayerRepository},
		{"model", LayerModel},
		{"models", LayerModel},
		{"entity", LayerModel},
		{"entities", LayerModel},
		{"middleware", LayerMiddleware},
		{"middlewares", LayerMiddleware},
		{"/api/", LayerAPI},
		{"routes", LayerAPI},
		{"router", LayerAPI},
		{"/db/", LayerDB},
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
		{"/lib/", LayerUtil},
		{"common", LayerUtil},
		{"shared", LayerUtil},
	}

	for _, lp := range layerPatterns {
		if strings.Contains(lowerPath, lp.Pattern) {
			return lp.Layer
		}
	}
	return LayerUnknown
}

// InferSymbolRole determines the role from a symbol name.
func InferSymbolRole(name string) SymbolRole {
	lowerName := strings.ToLower(name)

	rolePatterns := []struct {
		Suffix string
		Prefix string
		Role   SymbolRole
	}{
		{Suffix: "handler", Role: RoleHandler},
		{Suffix: "controller", Role: RoleController},
		{Suffix: "validator", Role: RoleValidator},
		{Suffix: "serializer", Role: RoleSerializer},
		{Suffix: "factory", Role: RoleFactory},
		{Suffix: "builder", Role: RoleBuilder},
		{Suffix: "helper", Role: RoleHelper},
		{Suffix: "callback", Role: RoleCallback},
		{Suffix: "hook", Role: RoleHook},
		{Suffix: "middleware", Role: RoleMiddleware},
		{Prefix: "handle", Role: RoleHandler},
		{Prefix: "validate", Role: RoleValidator},
		{Prefix: "serialize", Role: RoleSerializer},
		{Prefix: "create", Role: RoleFactory},
		{Prefix: "build", Role: RoleBuilder},
		{Prefix: "use", Role: RoleHook}, // React hooks
		{Prefix: "on", Role: RoleCallback},
	}

	for _, rp := range rolePatterns {
		if rp.Suffix != "" && strings.HasSuffix(lowerName, rp.Suffix) {
			return rp.Role
		}
		if rp.Prefix != "" && strings.HasPrefix(lowerName, rp.Prefix) {
			return rp.Role
		}
	}
	return RoleUnknown
}

// InferConfigKeyCategory determines the category from a config key name.
func InferConfigKeyCategory(key string) ConfigKeyCategory {
	lowerKey := strings.ToLower(key)

	patterns := []struct {
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
		// Credentials
		{"secret", ConfigCredential},
		{"key", ConfigCredential},
		{"token", ConfigCredential},
		{"password", ConfigCredential},
		{"credential", ConfigCredential},
	}

	for _, p := range patterns {
		if strings.Contains(lowerKey, p.Contains) {
			return p.Category
		}
	}
	return ConfigGeneral
}

// InferModuleFromPath extracts a module name from a file path.
func InferModuleFromPath(path string) string {
	// Remove file extension
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}

	// Try to find a meaningful directory name
	skipDirs := map[string]bool{
		"src": true, "lib": true, "app": true, "internal": true,
		"pkg": true, "cmd": true, "test": true, "tests": true,
	}

	for i := len(parts) - 2; i >= 0; i-- {
		dir := parts[i]
		if dir != "" && !skipDirs[strings.ToLower(dir)] {
			// Capitalize first letter
			if len(dir) > 0 {
				return strings.ToUpper(dir[:1]) + dir[1:]
			}
		}
	}

	// Fallback to filename without extension
	filename := parts[len(parts)-1]
	if idx := strings.LastIndex(filename, "."); idx > 0 {
		filename = filename[:idx]
	}
	if len(filename) > 0 {
		return strings.ToUpper(filename[:1]) + filename[1:]
	}
	return "General"
}

// EnrichSignalWithMetadata adds layer, role, and module information to a signal.
func EnrichSignalWithMetadata(sig *ChangeSignal) {
	if len(sig.Evidence.FileRanges) > 0 {
		path := sig.Evidence.FileRanges[0].Path
		sig.Evidence.Layer = InferLayerFromPath(path)
		if sig.Evidence.Module == "" {
			sig.Evidence.Module = InferModuleFromPath(path)
		}
	}

	// Extract symbol name and infer role
	for _, sym := range sig.Evidence.Symbols {
		if strings.HasPrefix(sym, "name:") {
			name := strings.TrimPrefix(sym, "name:")
			role := InferSymbolRole(name)
			if sig.Evidence.SymbolMeta == nil {
				sig.Evidence.SymbolMeta = &SymbolMetadata{
					Name: name,
				}
			}
			sig.Evidence.SymbolMeta.Role = role
			if sig.Evidence.Layer != LayerUnknown {
				sig.Evidence.SymbolMeta.Layer = sig.Evidence.Layer
			}
			break
		}
	}
}

// IsBreaking returns true if this signal represents a breaking change.
func (cs *ChangeSignal) IsBreaking() bool {
	return cs.HasTag("breaking")
}

// IsAPIChange returns true if this signal affects the API surface.
func (cs *ChangeSignal) IsAPIChange() bool {
	return cs.HasTag("api")
}
