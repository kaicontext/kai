// Package intent provides the template matching engine for intent generation.
package intent

import (
	"sort"
	"strings"

	"kai-core/detect"
)

// IntentCandidate represents a potential intent with confidence.
type IntentCandidate struct {
	Text       string   `json:"text"`
	Confidence float64  `json:"confidence"`
	Template   string   `json:"template"`
	Reasoning  string   `json:"reasoning"`
}

// IntentResult contains the generated intent with alternatives.
type IntentResult struct {
	Primary      *IntentCandidate   `json:"primary"`
	Alternatives []*IntentCandidate `json:"alternatives,omitempty"`
	Warnings     []string           `json:"warnings,omitempty"`
	Clusters     []*ChangeCluster   `json:"clusters,omitempty"`
}

// Engine generates intents using template matching and confidence scoring.
type Engine struct {
	templates []Template
	clusterer *Clusterer
}

// NewEngine creates a new intent generation engine with default templates.
func NewEngine() *Engine {
	return &Engine{
		templates: DefaultTemplates,
		clusterer: NewClusterer(),
	}
}

// NewEngineWithTemplates creates an engine with custom templates.
func NewEngineWithTemplates(templates []Template) *Engine {
	return &Engine{
		templates: templates,
		clusterer: NewClusterer(),
	}
}

// SetCallGraph sets the file dependency graph for clustering.
func (e *Engine) SetCallGraph(graph map[string][]string) {
	e.clusterer.SetCallGraph(graph)
}

// SetModules sets the file to module mapping for clustering.
func (e *Engine) SetModules(modules map[string]string) {
	e.clusterer.SetModules(modules)
}

// GenerateIntent generates an intent from change signals.
func (e *Engine) GenerateIntent(signals []*detect.ChangeSignal, modules []string, files []string) *IntentResult {
	result := &IntentResult{}

	if len(signals) == 0 {
		result.Primary = &IntentCandidate{
			Text:       "No changes detected",
			Confidence: 1.0,
			Template:   "none",
			Reasoning:  "Empty changeset",
		}
		return result
	}

	// Cluster the signals
	clusters := e.clusterer.ClusterChanges(signals, modules)
	result.Clusters = clusters

	if len(clusters) == 0 {
		// Create a single cluster from all signals
		clusters = []*ChangeCluster{{
			ID:          "A",
			Signals:     signals,
			Files:       files,
			Modules:     modules,
			PrimaryArea: "codebase",
			ClusterType: ClusterTypeMixed,
			Cohesion:    0.5,
		}}
	}

	// Generate candidates for the primary cluster
	primaryCluster := clusters[0]
	candidates := e.generateCandidates(primaryCluster, modules)
	if primaryCluster.IsMixed && len(primaryCluster.SubIntents) > 0 {
		candidates = append(candidates, buildMixedSummaryCandidate(primaryCluster, modules))
	}

	if len(candidates) == 0 {
		result.Primary = &IntentCandidate{
			Text:       "Update " + primaryCluster.PrimaryArea + " in " + getModule(modules),
			Confidence: 0.3,
			Template:   "generic_update",
			Reasoning:  "No template matched",
		}
		return result
	}

	// Sort candidates by confidence
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})

	result.Primary = candidates[0]
	if primaryCluster.IsMixed && len(primaryCluster.SubIntents) > 0 {
		shouldPreferMixed := len(primaryCluster.SubIntents) > 1
		if !shouldPreferMixed {
			shouldPreferMixed = result.Primary.Confidence < 0.6 || result.Primary.Template == "generic_update"
		}

		if shouldPreferMixed {
			mixedCandidate := buildMixedSummaryCandidate(primaryCluster, modules)
			if mixedCandidate != nil && mixedCandidate.Text != "" {
				if result.Primary.Template != mixedCandidate.Template {
					result.Alternatives = append([]*IntentCandidate{result.Primary}, result.Alternatives...)
				}
				result.Primary = mixedCandidate
			}
		}
	}
	if len(candidates) > 1 {
		result.Alternatives = candidates[1:]
	}

	// Add warnings for low confidence or many unrelated changes
	if result.Primary.Confidence < 0.5 {
		result.Warnings = append(result.Warnings, "Low confidence intent - consider reviewing manually")
	}

	if len(clusters) > 3 {
		result.Warnings = append(result.Warnings, "Many unrelated changes detected - consider splitting into multiple commits")
	}

	// Warn about mixed/unfocused clusters
	if primaryCluster.IsMixed {
		result.Warnings = append(result.Warnings, "Mixed changes detected - intent may not fully capture all changes")
		if len(primaryCluster.SubIntents) > 0 {
			for _, sub := range primaryCluster.SubIntents {
				result.Warnings = append(result.Warnings, "  - "+sub)
			}
		}
	}

	// Warn about large number of signals
	if len(signals) > MaxClusterSize {
		result.Warnings = append(result.Warnings, "Large changeset - consider smaller, focused commits")
	}

	// Check for breaking changes
	hasBreaking := false
	for _, sig := range signals {
		if sig.IsBreaking() {
			hasBreaking = true
			break
		}
	}
	if hasBreaking {
		result.Warnings = append(result.Warnings, "Contains breaking changes")
	}

	// Warn about security-sensitive changes
	for _, sig := range signals {
		if sig.Category == detect.CredentialChanged {
			result.Warnings = append(result.Warnings, "Contains credential/secret changes - review carefully")
			break
		}
	}

	// Warn about schema changes
	for _, sig := range signals {
		if sig.Category == detect.SchemaFieldRemoved {
			result.Warnings = append(result.Warnings, "Contains schema field removal - may require data migration")
			break
		}
	}

	return result
}

// generateCandidates generates intent candidates for a cluster.
func (e *Engine) generateCandidates(cluster *ChangeCluster, modules []string) []*IntentCandidate {
	var candidates []*IntentCandidate

	// Sort templates by priority (highest first)
	templates := make([]Template, len(e.templates))
	copy(templates, e.templates)
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Priority > templates[j].Priority
	})

	// Try each template
	for _, t := range templates {
		if MatchTemplate(&t, cluster) {
			vars := ExtractVariables(cluster, modules)
			text := RenderTemplate(t.Pattern, vars)

			// Calculate confidence
			confidence := calculateConfidence(t.BaseConfidence, cluster)

			candidate := &IntentCandidate{
				Text:       text,
				Confidence: confidence,
				Template:   t.ID,
				Reasoning:  buildReasoning(&t, cluster),
			}
			candidates = append(candidates, candidate)
		}
	}

	return candidates
}

// calculateConfidence calculates the final confidence score.
func calculateConfidence(baseConfidence float64, cluster *ChangeCluster) float64 {
	// Start with template's base confidence
	confidence := baseConfidence

	// Multiply by cluster cohesion
	confidence *= cluster.Cohesion

	// Multiply by average signal confidence
	avgSignalConf := cluster.AverageConfidence()
	confidence *= avgSignalConf

	// Apply evidence-based adjustments
	confidence = applyEvidenceBoosts(confidence, cluster)

	// Penalize noisy/mixed clusters
	confidence = applyNoisePenalty(confidence, cluster)

	// Ensure confidence is in valid range
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	return confidence
}

// applyEvidenceBoosts increases confidence for high-quality evidence types.
func applyEvidenceBoosts(confidence float64, cluster *ChangeCluster) float64 {
	hasHighValueSignal := false

	for _, sig := range cluster.Signals {
		// High-value signal types get confidence boosts
		switch sig.Category {
		case detect.FunctionRenamed:
			// Rename detection is very reliable
			confidence = minFloat(confidence*1.15, 1.0)
			hasHighValueSignal = true

		case detect.SchemaFieldAdded, detect.SchemaFieldRemoved, detect.SchemaFieldChanged, detect.MigrationAdded:
			// Schema changes are well-typed
			confidence = minFloat(confidence*1.12, 1.0)
			hasHighValueSignal = true

		case detect.FeatureFlagChanged, detect.CredentialChanged:
			// Semantic config changes are specific
			confidence = minFloat(confidence*1.10, 1.0)
			hasHighValueSignal = true

		case detect.DependencyAdded, detect.DependencyRemoved, detect.DependencyUpdated:
			// Dependency changes are clear
			confidence = minFloat(confidence*1.08, 1.0)
			hasHighValueSignal = true

		case detect.ParameterAdded, detect.ParameterRemoved:
			// API changes are well-typed
			confidence = minFloat(confidence*1.08, 1.0)
			hasHighValueSignal = true
		}

		// Check for rich evidence that increases confidence
		if sig.Evidence.Signature != nil && len(sig.Evidence.Signature.ParamsAdded) > 0 {
			confidence = minFloat(confidence*1.05, 1.0)
		}
		if sig.Evidence.SymbolMeta != nil && sig.Evidence.SymbolMeta.Role != detect.RoleUnknown {
			confidence = minFloat(confidence*1.03, 1.0)
		}
	}

	// If no high-value signals, slight penalty
	if !hasHighValueSignal && len(cluster.Signals) > 0 {
		confidence *= 0.95
	}

	return confidence
}

// applyNoisePenalty reduces confidence for noisy or mixed clusters.
func applyNoisePenalty(confidence float64, cluster *ChangeCluster) float64 {
	// Penalize if many signals (likely unfocused)
	if len(cluster.Signals) > 5 {
		penalty := 1.0 - float64(len(cluster.Signals)-5)*0.04
		if penalty < 0.5 {
			penalty = 0.5
		}
		confidence *= penalty
	}

	// Penalize mixed clusters
	if cluster.IsMixed {
		confidence *= 0.85
	}

	// Penalize low cohesion
	if cluster.Cohesion < CohesionThreshold {
		// Scale penalty based on how far below threshold
		ratio := cluster.Cohesion / CohesionThreshold
		confidence *= (0.7 + 0.3*ratio) // Ranges from 0.7 to 1.0
	}

	// Penalize many different categories (scattered changes)
	categories := make(map[detect.ChangeCategory]bool)
	for _, sig := range cluster.Signals {
		categories[sig.Category] = true
	}
	if len(categories) > 4 {
		confidence *= 0.9
	}

	// Penalize many different files (not focused)
	if len(cluster.Files) > 5 {
		confidence *= 0.92
	}

	return confidence
}

// EvidenceQuality represents the quality of evidence for a signal.
type EvidenceQuality int

const (
	EvidenceQualityLow    EvidenceQuality = 1
	EvidenceQualityMedium EvidenceQuality = 2
	EvidenceQualityHigh   EvidenceQuality = 3
)

// GetEvidenceQuality returns the evidence quality for a signal.
func GetEvidenceQuality(sig *detect.ChangeSignal) EvidenceQuality {
	// High quality: rename, schema, specific config
	switch sig.Category {
	case detect.FunctionRenamed, detect.SchemaFieldAdded, detect.SchemaFieldRemoved,
		detect.SchemaFieldChanged, detect.MigrationAdded, detect.FeatureFlagChanged,
		detect.CredentialChanged:
		return EvidenceQualityHigh
	}

	// Medium quality: dependencies, parameters, semantic changes
	switch sig.Category {
	case detect.DependencyAdded, detect.DependencyRemoved, detect.DependencyUpdated,
		detect.ParameterAdded, detect.ParameterRemoved, detect.APISurfaceChanged,
		detect.TimeoutChanged, detect.LimitChanged, detect.EndpointChanged:
		return EvidenceQualityMedium
	}

	// Low quality: generic changes
	return EvidenceQualityLow
}

func buildMixedSummaryCandidate(cluster *ChangeCluster, modules []string) *IntentCandidate {
	if cluster == nil || len(cluster.SubIntents) == 0 {
		return nil
	}

	module := getModule(modules)
	if len(cluster.Modules) > 0 {
		module = cluster.Modules[0]
	}

	summary := formatSubIntentSummary(cluster.SubIntents)
	if summary == "" {
		return nil
	}

	return &IntentCandidate{
		Text:       "Mixed changes in " + module + ": " + summary,
		Confidence: mixedSummaryConfidence(cluster),
		Template:   "mixed_summary",
		Reasoning:  "Summarized mixed changes from sub-intents",
	}
}

func mixedSummaryConfidence(cluster *ChangeCluster) float64 {
	if cluster == nil {
		return 0.55
	}

	confidence := 0.55
	if cluster.AverageConfidence() >= 0.8 {
		confidence += 0.1
	}

	for _, sig := range cluster.Signals {
		if GetEvidenceQuality(sig) == EvidenceQualityHigh {
			confidence += 0.05
			break
		}
	}

	if len(cluster.Signals) > MaxClusterSize {
		confidence -= 0.05
	}

	if confidence < 0.45 {
		confidence = 0.45
	}
	if confidence > 0.75 {
		confidence = 0.75
	}

	return confidence
}

func formatSubIntentSummary(subIntents []string) string {
	if len(subIntents) == 0 {
		return ""
	}

	maxItems := 3
	if len(subIntents) <= maxItems {
		return joinWithConjunction(subIntents)
	}

	shown := subIntents[:maxItems]
	remaining := len(subIntents) - maxItems
	return joinWithConjunction(shown) + " and " + itoa(remaining) + " more"
}

func joinWithConjunction(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

// buildReasoning explains why a template was chosen.
func buildReasoning(t *Template, cluster *ChangeCluster) string {
	return "Matched " + t.ID + " due to " + describeDominantSignal(cluster)
}

// describeDominantSignal returns a human-readable description of the dominant signal.
func describeDominantSignal(cluster *ChangeCluster) string {
	if len(cluster.Signals) == 0 {
		return "no signals"
	}

	// Count categories
	categories := make(map[detect.ChangeCategory]int)
	for _, sig := range cluster.Signals {
		categories[sig.Category]++
	}

	// Find dominant category
	var dominant detect.ChangeCategory
	maxCount := 0
	for cat, count := range categories {
		if count > maxCount {
			dominant = cat
			maxCount = count
		}
	}

	return categoryToDescription(dominant)
}

// categoryToDescription converts a category to a human-readable description.
func categoryToDescription(cat detect.ChangeCategory) string {
	descriptions := map[detect.ChangeCategory]string{
		detect.FunctionAdded:       "FunctionAdded signal",
		detect.FunctionRemoved:     "FunctionRemoved signal",
		detect.FunctionRenamed:     "FunctionRenamed signal",
		detect.FunctionBodyChanged: "FunctionBodyChanged signal",
		detect.ParameterAdded:      "ParameterAdded signal",
		detect.ParameterRemoved:    "ParameterRemoved signal",
		detect.APISurfaceChanged:   "APISurfaceChanged signal",
		detect.DependencyAdded:     "DependencyAdded signal",
		detect.DependencyRemoved:   "DependencyRemoved signal",
		detect.DependencyUpdated:   "DependencyUpdated signal",
		detect.ImportAdded:         "ImportAdded signal",
		detect.ImportRemoved:       "ImportRemoved signal",
		detect.ConditionChanged:    "ConditionChanged signal",
		detect.ConstantUpdated:     "ConstantUpdated signal",
		detect.JSONValueChanged:    "JSONValueChanged signal",
		detect.YAMLValueChanged:    "YAMLValueChanged signal",
		detect.FeatureFlagChanged:  "FeatureFlagChanged signal",
		detect.TimeoutChanged:      "TimeoutChanged signal",
		detect.LimitChanged:        "LimitChanged signal",
		detect.CredentialChanged:   "CredentialChanged signal",
		detect.SchemaFieldAdded:    "SchemaFieldAdded signal",
		detect.SchemaFieldRemoved:  "SchemaFieldRemoved signal",
		detect.SchemaFieldChanged:  "SchemaFieldChanged signal",
		detect.MigrationAdded:      "MigrationAdded signal",
		detect.FileAdded:           "FileAdded signal",
		detect.FileDeleted:         "FileDeleted signal",
		detect.FileContentChanged:  "file content changes",
	}

	if desc, ok := descriptions[cat]; ok {
		return desc
	}
	return string(cat) + " signal"
}

// DetailedReasoning provides verbose reasoning for an intent candidate.
type DetailedReasoning struct {
	Template      string                 `json:"template"`
	TemplateDesc  string                 `json:"templateDescription"`
	MatchedSignals []string              `json:"matchedSignals"`
	ConfidenceFactors []string           `json:"confidenceFactors"`
	EvidenceQuality string               `json:"evidenceQuality"`
}

// GetDetailedReasoning returns detailed reasoning for an intent candidate.
func GetDetailedReasoning(candidate *IntentCandidate, cluster *ChangeCluster) *DetailedReasoning {
	dr := &DetailedReasoning{
		Template:      candidate.Template,
		TemplateDesc:  getTemplateDescription(candidate.Template),
	}

	// List matched signals
	for _, sig := range cluster.Signals {
		desc := string(sig.Category)
		if len(sig.Evidence.Symbols) > 0 {
			for _, sym := range sig.Evidence.Symbols {
				if len(sym) > 0 {
					desc += ": " + sym
					break
				}
			}
		}
		dr.MatchedSignals = append(dr.MatchedSignals, desc)
	}

	// Explain confidence factors
	if cluster.Cohesion >= 0.8 {
		dr.ConfidenceFactors = append(dr.ConfidenceFactors, "High cluster cohesion")
	} else if cluster.Cohesion < CohesionThreshold {
		dr.ConfidenceFactors = append(dr.ConfidenceFactors, "Low cluster cohesion (penalty applied)")
	}

	if len(cluster.Signals) > 5 {
		dr.ConfidenceFactors = append(dr.ConfidenceFactors, "Many signals (penalty applied)")
	}

	hasHighQuality := false
	for _, sig := range cluster.Signals {
		if GetEvidenceQuality(sig) == EvidenceQualityHigh {
			hasHighQuality = true
			dr.ConfidenceFactors = append(dr.ConfidenceFactors, "High-quality evidence ("+string(sig.Category)+")")
			break
		}
	}

	if hasHighQuality {
		dr.EvidenceQuality = "high"
	} else if cluster.AverageConfidence() >= 0.8 {
		dr.EvidenceQuality = "medium"
	} else {
		dr.EvidenceQuality = "low"
	}

	return dr
}

// getTemplateDescription returns a human-readable description of a template.
func getTemplateDescription(templateID string) string {
	descriptions := map[string]string{
		"rename_function":        "Detected a function rename based on body similarity",
		"add_single_function":    "Detected a single new function being added",
		"remove_single_function": "Detected a single function being removed",
		"add_multiple_functions": "Detected multiple new functions being added",
		"refactor_functions":     "Detected both additions and removals suggesting refactoring",
		"update_api_parameters":  "Detected changes to function parameters",
		"update_api_surface":     "Detected API surface changes",
		"add_dependency":         "Detected a new package dependency",
		"remove_dependency":      "Detected a removed package dependency",
		"update_dependency":      "Detected a dependency version update",
		"toggle_feature_flag":    "Detected a feature flag toggle",
		"update_timeout":         "Detected timeout configuration change",
		"update_limit":           "Detected limit configuration change",
		"update_credential":      "Detected credential/secret change",
		"add_schema_field":       "Detected a schema field addition",
		"remove_schema_field":    "Detected a schema field removal",
		"add_migration":          "Detected a new database migration",
		"mixed_summary":          "Summarized multiple unrelated changes",
		"generic_update":         "Fallback template - no specific pattern matched",
	}

	if desc, ok := descriptions[templateID]; ok {
		return desc
	}
	return "Template: " + templateID
}

// getModule returns the first module or "General" as default.
func getModule(modules []string) string {
	if len(modules) > 0 {
		return modules[0]
	}
	return "General"
}

// itoa converts int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// minFloat returns the minimum of two float64 values.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// GenerateIntentFromChangeTypes is a convenience method that converts
// ChangeTypes to Signals and generates intent.
func (e *Engine) GenerateIntentFromChangeTypes(changeTypes []*detect.ChangeType, modules []string, files []string) *IntentResult {
	signals := detect.ConvertToSignals(changeTypes)
	return e.GenerateIntent(signals, modules, files)
}

// GenerateSimpleIntent returns just the intent text (for backward compatibility).
func (e *Engine) GenerateSimpleIntent(signals []*detect.ChangeSignal, modules []string, files []string) string {
	result := e.GenerateIntent(signals, modules, files)
	if result.Primary != nil {
		return result.Primary.Text
	}
	return "Update codebase"
}

// GetPrimaryConfidence returns the confidence of the primary intent.
func (r *IntentResult) GetPrimaryConfidence() float64 {
	if r.Primary == nil {
		return 0
	}
	return r.Primary.Confidence
}

// HasHighConfidence returns true if the primary intent has confidence >= 0.7.
func (r *IntentResult) HasHighConfidence() bool {
	return r.GetPrimaryConfidence() >= 0.7
}

// HasWarnings returns true if there are any warnings.
func (r *IntentResult) HasWarnings() bool {
	return len(r.Warnings) > 0
}

// GetAlternativeTexts returns the text of all alternative intents.
func (r *IntentResult) GetAlternativeTexts() []string {
	var texts []string
	for _, alt := range r.Alternatives {
		texts = append(texts, alt.Text)
	}
	return texts
}

// GetTopAlternatives returns up to n top alternative intents.
func (r *IntentResult) GetTopAlternatives(n int) []*IntentCandidate {
	if n >= len(r.Alternatives) {
		return r.Alternatives
	}
	return r.Alternatives[:n]
}

// FormatWithConfidence returns the intent text with confidence indicator.
func (c *IntentCandidate) FormatWithConfidence() string {
	confidence := "low"
	if c.Confidence >= 0.8 {
		confidence = "high"
	} else if c.Confidence >= 0.5 {
		confidence = "medium"
	}
	return c.Text + " [" + confidence + " confidence]"
}

// ShouldUseLLM returns true if confidence is too low and LLM fallback is recommended.
func (r *IntentResult) ShouldUseLLM() bool {
	return r.GetPrimaryConfidence() < 0.4
}

// ConfidenceBand represents a confidence level band.
type ConfidenceBand string

const (
	ConfidenceBandHigh   ConfidenceBand = "high"   // >= 0.8
	ConfidenceBandMedium ConfidenceBand = "medium" // >= 0.5
	ConfidenceBandLow    ConfidenceBand = "low"    // < 0.5
)

// GetConfidenceBand returns the confidence band for a value.
func GetConfidenceBand(confidence float64) ConfidenceBand {
	if confidence >= 0.8 {
		return ConfidenceBandHigh
	}
	if confidence >= 0.5 {
		return ConfidenceBandMedium
	}
	return ConfidenceBandLow
}

// GetConfidenceBandLabel returns a user-friendly label for the confidence band.
func GetConfidenceBandLabel(band ConfidenceBand) string {
	switch band {
	case ConfidenceBandHigh:
		return "High confidence"
	case ConfidenceBandMedium:
		return "Medium confidence"
	default:
		return "Low confidence"
	}
}

// FormattedAlternative represents an alternative with display formatting.
type FormattedAlternative struct {
	Text           string         `json:"text"`
	Confidence     float64        `json:"confidence"`
	ConfidenceBand ConfidenceBand `json:"confidenceBand"`
	Template       string         `json:"template"`
	Reasoning      string         `json:"reasoning"`
}

// GetFormattedAlternatives returns alternatives with confidence band formatting.
func (r *IntentResult) GetFormattedAlternatives() []*FormattedAlternative {
	var result []*FormattedAlternative

	for _, alt := range r.Alternatives {
		band := GetConfidenceBand(alt.Confidence)
		result = append(result, &FormattedAlternative{
			Text:           alt.Text,
			Confidence:     alt.Confidence,
			ConfidenceBand: band,
			Template:       alt.Template,
			Reasoning:      alt.Reasoning,
		})
	}

	return result
}

// GetFormattedPrimary returns the primary intent with confidence band formatting.
func (r *IntentResult) GetFormattedPrimary() *FormattedAlternative {
	if r.Primary == nil {
		return nil
	}
	band := GetConfidenceBand(r.Primary.Confidence)
	return &FormattedAlternative{
		Text:           r.Primary.Text,
		Confidence:     r.Primary.Confidence,
		ConfidenceBand: band,
		Template:       r.Primary.Template,
		Reasoning:      r.Primary.Reasoning,
	}
}

// IntentOverride represents a user override for an intent.
type IntentOverride struct {
	OriginalIntent string `json:"originalIntent"`
	OverrideIntent string `json:"overrideIntent"`
	Reason         string `json:"reason,omitempty"`
}

// SuggestOverrides returns suggested overrides based on common patterns.
func (r *IntentResult) SuggestOverrides() []string {
	if r.Primary == nil {
		return nil
	}

	var suggestions []string

	// Suggest alternatives if confidence is medium or low
	if r.Primary.Confidence < 0.8 {
		for _, alt := range r.Alternatives {
			if alt.Confidence > 0.4 && alt.Text != r.Primary.Text {
				suggestions = append(suggestions, alt.Text)
			}
		}
	}

	// Suggest common verbs if the intent is generic
	if r.Primary.Template == "generic_update" {
		area := extractAreaFromIntent(r.Primary.Text)
		suggestions = append(suggestions,
			"Fix "+area,
			"Improve "+area,
			"Refactor "+area,
			"Add feature to "+area,
		)
	}

	// Limit suggestions
	if len(suggestions) > 4 {
		suggestions = suggestions[:4]
	}

	return suggestions
}

// extractAreaFromIntent extracts the area/module from an intent string.
func extractAreaFromIntent(intent string) string {
	// Simple extraction: look for "in {module}" pattern
	parts := strings.SplitN(intent, " in ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return "codebase"
}

// FormatReasoningVerbose returns a verbose multi-line reasoning string.
func (c *IntentCandidate) FormatReasoningVerbose(cluster *ChangeCluster) string {
	if cluster == nil {
		return c.Reasoning
	}

	dr := GetDetailedReasoning(c, cluster)
	var lines []string

	lines = append(lines, "Template: "+dr.Template)
	lines = append(lines, "Description: "+dr.TemplateDesc)
	lines = append(lines, "Evidence Quality: "+dr.EvidenceQuality)

	if len(dr.MatchedSignals) > 0 {
		lines = append(lines, "Matched Signals:")
		for _, sig := range dr.MatchedSignals {
			lines = append(lines, "  - "+sig)
		}
	}

	if len(dr.ConfidenceFactors) > 0 {
		lines = append(lines, "Confidence Factors:")
		for _, factor := range dr.ConfidenceFactors {
			lines = append(lines, "  - "+factor)
		}
	}

	return strings.Join(lines, "\n")
}
