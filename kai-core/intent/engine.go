// Package intent provides the template matching engine for intent generation.
package intent

import (
	"sort"

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

	// Check for breaking changes
	for _, sig := range signals {
		if sig.IsBreaking() {
			result.Warnings = append(result.Warnings, "Contains breaking changes")
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

	// Penalize if many unrelated signals
	if len(cluster.Signals) > 5 {
		penalty := 1.0 - float64(len(cluster.Signals)-5)*0.05
		if penalty < 0.5 {
			penalty = 0.5
		}
		confidence *= penalty
	}

	// Boost confidence for rename detection (high-value)
	for _, sig := range cluster.Signals {
		if sig.Category == detect.FunctionRenamed {
			confidence = minFloat(confidence*1.1, 1.0)
			break
		}
	}

	// Ensure confidence is in valid range
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	return confidence
}

// buildReasoning explains why a template was chosen.
func buildReasoning(t *Template, cluster *ChangeCluster) string {
	var parts []string

	parts = append(parts, "Template: "+t.ID)
	parts = append(parts, "Priority: "+itoa(t.Priority))

	// Explain what signals matched
	categories := make(map[detect.ChangeCategory]int)
	for _, sig := range cluster.Signals {
		categories[sig.Category]++
	}

	for cat, count := range categories {
		parts = append(parts, string(cat)+": "+itoa(count))
	}

	if len(parts) > 0 {
		return parts[0] // Return just the template name for brevity
	}

	return ""
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
