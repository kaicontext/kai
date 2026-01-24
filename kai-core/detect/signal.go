// Package detect provides extended signal types for change detection with confidence scoring.
package detect

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
}

// SignatureChange captures function signature changes.
type SignatureChange struct {
	OldParams      string `json:"oldParams"`
	NewParams      string `json:"newParams"`
	OldReturnType  string `json:"oldReturnType"`
	NewReturnType  string `json:"newReturnType"`
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

	return &ChangeSignal{
		Category: ct.Category,
		Evidence: ExtendedEvidence{
			FileRanges: ct.Evidence.FileRanges,
			Symbols:    ct.Evidence.Symbols,
		},
		Weight:     weight,
		Confidence: 1.0, // Default high confidence for AST-based detection
		Tags:       inferTags(ct),
	}
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
	case FunctionRemoved, ParameterRemoved, DependencyRemoved:
		tags = append(tags, "breaking")
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
	patterns := []string{"_test.go", ".test.js", ".test.ts", ".spec.js", ".spec.ts", "test_", "_test.py", "_spec.rb"}
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

// IsBreaking returns true if this signal represents a breaking change.
func (cs *ChangeSignal) IsBreaking() bool {
	return cs.HasTag("breaking")
}

// IsAPIChange returns true if this signal affects the API surface.
func (cs *ChangeSignal) IsAPIChange() bool {
	return cs.HasTag("api")
}
