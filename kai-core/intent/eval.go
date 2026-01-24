// Package intent provides evaluation infrastructure for intent generation.
package intent

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"kai-core/detect"
)

// EvalCase represents a single test case for intent evaluation.
type EvalCase struct {
	// Metadata
	ID          string `json:"id"`
	Description string `json:"description"`
	Category    string `json:"category"` // e.g., "rename", "add_feature", "bugfix"

	// Input
	Signals []*detect.ChangeSignal `json:"signals"`
	Modules []string               `json:"modules"`
	Files   []string               `json:"files"`

	// Expected output
	ExpectedIntent     string   `json:"expected_intent"`      // The gold standard intent
	AcceptableIntents  []string `json:"acceptable_intents"`   // Alternative acceptable intents
	ExpectedTemplate   string   `json:"expected_template"`    // Expected template ID
	MinConfidence      float64  `json:"min_confidence"`       // Minimum acceptable confidence
	ExpectedBreaking   bool     `json:"expected_breaking"`    // Should this be flagged as breaking?
	ExpectedCategories []string `json:"expected_categories"`  // Expected intent categories
}

// EvalResult represents the result of evaluating a single test case.
type EvalResult struct {
	CaseID          string  `json:"case_id"`
	Passed          bool    `json:"passed"`
	Score           float64 `json:"score"` // 0.0-1.0 match quality
	MatchType       string  `json:"match_type"` // "exact", "acceptable", "partial", "none"
	GeneratedIntent string  `json:"generated_intent"`
	GeneratedConf   float64 `json:"generated_confidence"`
	TemplateUsed    string  `json:"template_used"`
	Errors          []string `json:"errors,omitempty"`
	Details         string  `json:"details,omitempty"`
}

// EvalReport represents the overall evaluation report.
type EvalReport struct {
	TotalCases     int           `json:"total_cases"`
	PassedCases    int           `json:"passed_cases"`
	FailedCases    int           `json:"failed_cases"`
	OverallScore   float64       `json:"overall_score"`     // Average score
	PassRate       float64       `json:"pass_rate"`         // Percentage passed
	ByCategory     map[string]*CategoryStats `json:"by_category"`
	ByTemplate     map[string]*TemplateStats `json:"by_template"`
	Results        []*EvalResult `json:"results"`
	FailedResults  []*EvalResult `json:"failed_results,omitempty"`
}

// CategoryStats tracks statistics by intent category.
type CategoryStats struct {
	Total   int     `json:"total"`
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	AvgScore float64 `json:"avg_score"`
}

// TemplateStats tracks statistics by template.
type TemplateStats struct {
	Expected int `json:"expected"` // Times this template was expected
	Used     int `json:"used"`     // Times this template was actually used
	Correct  int `json:"correct"`  // Times expected == used
}

// Evaluator runs intent generation evaluation.
type Evaluator struct {
	engine       *Engine
	cases        []*EvalCase
	matchOpts    MatchOptions
}

// MatchOptions configures how intent matching is performed.
type MatchOptions struct {
	// Exact match requires identical text
	RequireExactMatch bool
	// Partial match threshold (0.0-1.0) for similarity
	PartialMatchThreshold float64
	// Whether to normalize text before comparison (lowercase, trim)
	NormalizeText bool
	// Whether to ignore punctuation differences
	IgnorePunctuation bool
	// Minimum confidence to count as passed
	MinConfidence float64
}

// DefaultMatchOptions returns sensible defaults for evaluation.
func DefaultMatchOptions() MatchOptions {
	return MatchOptions{
		RequireExactMatch:     false,
		PartialMatchThreshold: 0.7,
		NormalizeText:         true,
		IgnorePunctuation:     true,
		MinConfidence:         0.5,
	}
}

// NewEvaluator creates a new intent evaluator.
func NewEvaluator(engine *Engine) *Evaluator {
	return &Evaluator{
		engine:    engine,
		cases:     make([]*EvalCase, 0),
		matchOpts: DefaultMatchOptions(),
	}
}

// SetMatchOptions configures matching behavior.
func (e *Evaluator) SetMatchOptions(opts MatchOptions) {
	e.matchOpts = opts
}

// AddCase adds a test case.
func (e *Evaluator) AddCase(c *EvalCase) {
	e.cases = append(e.cases, c)
}

// AddCases adds multiple test cases.
func (e *Evaluator) AddCases(cases []*EvalCase) {
	e.cases = append(e.cases, cases...)
}

// LoadCasesFromJSON loads test cases from a JSON file.
func (e *Evaluator) LoadCasesFromJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading cases file: %w", err)
	}

	var cases []*EvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return fmt.Errorf("parsing cases JSON: %w", err)
	}

	e.AddCases(cases)
	return nil
}

// Run executes all test cases and returns a report.
func (e *Evaluator) Run() *EvalReport {
	report := &EvalReport{
		TotalCases:    len(e.cases),
		ByCategory:    make(map[string]*CategoryStats),
		ByTemplate:    make(map[string]*TemplateStats),
		Results:       make([]*EvalResult, 0, len(e.cases)),
		FailedResults: make([]*EvalResult, 0),
	}

	totalScore := 0.0

	for _, tc := range e.cases {
		result := e.evaluateCase(tc)
		report.Results = append(report.Results, result)

		if result.Passed {
			report.PassedCases++
		} else {
			report.FailedCases++
			report.FailedResults = append(report.FailedResults, result)
		}
		totalScore += result.Score

		// Update category stats
		cat := tc.Category
		if cat == "" {
			cat = "unknown"
		}
		if report.ByCategory[cat] == nil {
			report.ByCategory[cat] = &CategoryStats{}
		}
		stats := report.ByCategory[cat]
		stats.Total++
		if result.Passed {
			stats.Passed++
		} else {
			stats.Failed++
		}

		// Update template stats
		if tc.ExpectedTemplate != "" {
			if report.ByTemplate[tc.ExpectedTemplate] == nil {
				report.ByTemplate[tc.ExpectedTemplate] = &TemplateStats{}
			}
			report.ByTemplate[tc.ExpectedTemplate].Expected++
			if result.TemplateUsed == tc.ExpectedTemplate {
				report.ByTemplate[tc.ExpectedTemplate].Correct++
			}
		}
		if result.TemplateUsed != "" {
			if report.ByTemplate[result.TemplateUsed] == nil {
				report.ByTemplate[result.TemplateUsed] = &TemplateStats{}
			}
			report.ByTemplate[result.TemplateUsed].Used++
		}
	}

	// Calculate averages
	if report.TotalCases > 0 {
		report.OverallScore = totalScore / float64(report.TotalCases)
		report.PassRate = float64(report.PassedCases) / float64(report.TotalCases) * 100

		for _, stats := range report.ByCategory {
			if stats.Total > 0 {
				stats.AvgScore = (stats.AvgScore + float64(stats.Passed)) / float64(stats.Total)
			}
		}
	}

	return report
}

// evaluateCase runs a single test case.
func (e *Evaluator) evaluateCase(tc *EvalCase) *EvalResult {
	result := &EvalResult{
		CaseID: tc.ID,
		Errors: make([]string, 0),
	}

	// Generate intent
	intentResult := e.engine.GenerateIntent(tc.Signals, tc.Modules, tc.Files)

	if intentResult.Primary == nil {
		result.Passed = false
		result.Score = 0
		result.MatchType = "none"
		result.Errors = append(result.Errors, "no intent generated")
		return result
	}

	result.GeneratedIntent = intentResult.Primary.Text
	result.GeneratedConf = intentResult.Primary.Confidence
	result.TemplateUsed = intentResult.Primary.Template

	// Check confidence threshold
	minConf := tc.MinConfidence
	if minConf == 0 {
		minConf = e.matchOpts.MinConfidence
	}
	if result.GeneratedConf < minConf {
		result.Errors = append(result.Errors, fmt.Sprintf("confidence %.2f below threshold %.2f", result.GeneratedConf, minConf))
	}

	// Evaluate match quality
	score, matchType := e.scoreMatch(result.GeneratedIntent, tc.ExpectedIntent, tc.AcceptableIntents)
	result.Score = score
	result.MatchType = matchType

	// Check template match if specified
	if tc.ExpectedTemplate != "" && result.TemplateUsed != tc.ExpectedTemplate {
		result.Errors = append(result.Errors, fmt.Sprintf("expected template %q, got %q", tc.ExpectedTemplate, result.TemplateUsed))
	}

	// Check breaking flag
	hasBreakingWarning := false
	for _, w := range intentResult.Warnings {
		if strings.Contains(strings.ToLower(w), "breaking") {
			hasBreakingWarning = true
			break
		}
	}
	if tc.ExpectedBreaking && !hasBreakingWarning {
		result.Errors = append(result.Errors, "expected breaking change warning, none found")
	}

	// Determine pass/fail
	result.Passed = (matchType == "exact" || matchType == "acceptable") &&
		len(result.Errors) == 0 &&
		result.GeneratedConf >= minConf

	// Allow partial match if configured
	if !result.Passed && matchType == "partial" && score >= e.matchOpts.PartialMatchThreshold {
		// Partial pass - note this in details
		result.Details = fmt.Sprintf("partial match with score %.2f", score)
		if len(result.Errors) == 0 && result.GeneratedConf >= minConf {
			result.Passed = true
		}
	}

	return result
}

// scoreMatch calculates the match score between generated and expected intents.
func (e *Evaluator) scoreMatch(generated, expected string, acceptable []string) (float64, string) {
	// Normalize if configured
	genNorm := generated
	expNorm := expected
	if e.matchOpts.NormalizeText {
		genNorm = normalizeIntent(generated)
		expNorm = normalizeIntent(expected)
	}
	if e.matchOpts.IgnorePunctuation {
		genNorm = removePunctuation(genNorm)
		expNorm = removePunctuation(expNorm)
	}

	// Check exact match
	if genNorm == expNorm {
		return 1.0, "exact"
	}

	// Check acceptable alternatives
	for _, alt := range acceptable {
		altNorm := alt
		if e.matchOpts.NormalizeText {
			altNorm = normalizeIntent(alt)
		}
		if e.matchOpts.IgnorePunctuation {
			altNorm = removePunctuation(altNorm)
		}
		if genNorm == altNorm {
			return 0.95, "acceptable"
		}
	}

	// Calculate partial match score using token overlap
	score := tokenSimilarity(genNorm, expNorm)
	if score >= e.matchOpts.PartialMatchThreshold {
		return score, "partial"
	}

	return score, "none"
}

// normalizeIntent normalizes intent text for comparison.
func normalizeIntent(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	// Collapse multiple spaces
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// removePunctuation removes punctuation from a string.
func removePunctuation(s string) string {
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == ' ' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// tokenSimilarity calculates Jaccard similarity between token sets.
func tokenSimilarity(a, b string) float64 {
	tokensA := strings.Fields(a)
	tokensB := strings.Fields(b)

	if len(tokensA) == 0 || len(tokensB) == 0 {
		if len(tokensA) == 0 && len(tokensB) == 0 {
			return 1.0
		}
		return 0.0
	}

	setA := make(map[string]bool)
	for _, t := range tokensA {
		setA[t] = true
	}

	setB := make(map[string]bool)
	for _, t := range tokensB {
		setB[t] = true
	}

	// Count intersection
	intersection := 0
	for t := range setA {
		if setB[t] {
			intersection++
		}
	}

	// Union size
	union := len(setA)
	for t := range setB {
		if !setA[t] {
			union++
		}
	}

	if union == 0 {
		return 1.0
	}

	return float64(intersection) / float64(union)
}

// FormatReport formats the evaluation report as a string.
func (r *EvalReport) FormatReport() string {
	var sb strings.Builder

	sb.WriteString("=== Intent Evaluation Report ===\n\n")

	sb.WriteString(fmt.Sprintf("Total Cases: %d\n", r.TotalCases))
	sb.WriteString(fmt.Sprintf("Passed: %d (%.1f%%)\n", r.PassedCases, r.PassRate))
	sb.WriteString(fmt.Sprintf("Failed: %d\n", r.FailedCases))
	sb.WriteString(fmt.Sprintf("Overall Score: %.2f\n\n", r.OverallScore))

	// Category breakdown
	if len(r.ByCategory) > 0 {
		sb.WriteString("By Category:\n")
		// Sort categories for consistent output
		cats := make([]string, 0, len(r.ByCategory))
		for cat := range r.ByCategory {
			cats = append(cats, cat)
		}
		sort.Strings(cats)
		for _, cat := range cats {
			stats := r.ByCategory[cat]
			rate := 0.0
			if stats.Total > 0 {
				rate = float64(stats.Passed) / float64(stats.Total) * 100
			}
			sb.WriteString(fmt.Sprintf("  %s: %d/%d (%.1f%%)\n", cat, stats.Passed, stats.Total, rate))
		}
		sb.WriteString("\n")
	}

	// Template breakdown
	if len(r.ByTemplate) > 0 {
		sb.WriteString("By Template:\n")
		tmpls := make([]string, 0, len(r.ByTemplate))
		for t := range r.ByTemplate {
			tmpls = append(tmpls, t)
		}
		sort.Strings(tmpls)
		for _, t := range tmpls {
			stats := r.ByTemplate[t]
			sb.WriteString(fmt.Sprintf("  %s: expected=%d, used=%d, correct=%d\n",
				t, stats.Expected, stats.Used, stats.Correct))
		}
		sb.WriteString("\n")
	}

	// Failed cases detail
	if len(r.FailedResults) > 0 {
		sb.WriteString("Failed Cases:\n")
		for _, f := range r.FailedResults {
			sb.WriteString(fmt.Sprintf("  [%s] match=%s, score=%.2f\n", f.CaseID, f.MatchType, f.Score))
			sb.WriteString(fmt.Sprintf("    generated: %q\n", f.GeneratedIntent))
			if len(f.Errors) > 0 {
				sb.WriteString(fmt.Sprintf("    errors: %v\n", f.Errors))
			}
		}
	}

	return sb.String()
}

// SaveReportJSON saves the report to a JSON file.
func (r *EvalReport) SaveReportJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// === Built-in Test Cases ===

// BuiltinTestCases returns a set of standard test cases for regression testing.
func BuiltinTestCases() []*EvalCase {
	return []*EvalCase{
		// Rename cases
		{
			ID:          "rename_simple",
			Description: "Simple function rename",
			Category:    "rename",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionRenamed,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "src/utils.js"}},
						OldName:    "calculateTotal",
						NewName:    "computeSum",
					},
					Weight:     0.9,
					Confidence: 0.95,
					Tags:       []string{"api"},
				},
			},
			Modules:          []string{"Utils"},
			Files:            []string{"src/utils.js"},
			ExpectedIntent:   "Rename calculateTotal to computeSum in Utils",
			ExpectedTemplate: "rename_function",
			MinConfidence:    0.8,
		},

		// Add function cases
		{
			ID:          "add_single_function",
			Description: "Add a single new function",
			Category:    "add_feature",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "src/handlers/auth.js"}},
						Symbols:    []string{"name:validateToken"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
			},
			Modules:           []string{"Auth"},
			Files:             []string{"src/handlers/auth.js"},
			ExpectedIntent:    "Add validateToken in Auth",
			AcceptableIntents: []string{"Add validateToken function in Auth"},
			ExpectedTemplate:  "add_single_function",
			MinConfidence:     0.7,
		},

		// Remove function (breaking)
		{
			ID:          "remove_function_breaking",
			Description: "Remove a public function (breaking change)",
			Category:    "remove_feature",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionRemoved,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "src/api/users.js"}},
						Symbols:    []string{"name:deleteUser"},
					},
					Weight:     0.8,
					Confidence: 1.0,
					Tags:       []string{"breaking", "api"},
				},
			},
			Modules:           []string{"API"},
			Files:             []string{"src/api/users.js"},
			ExpectedIntent:    "Remove deleteUser from API",
			ExpectedTemplate:  "remove_single_function",
			ExpectedBreaking:  true,
			MinConfidence:     0.7,
		},

		// Refactor (add + remove)
		{
			ID:          "refactor_extract",
			Description: "Refactor by extracting helper functions",
			Category:    "refactor",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:parseInput"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:formatOutput"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
				{
					Category: detect.FunctionRemoved,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:processData"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
			},
			Modules:          []string{"Data"},
			ExpectedIntent:   "Refactor parseInput and formatOutput in Data",
			ExpectedTemplate: "refactor_functions",
			MinConfidence:    0.6,
		},

		// Dependency added
		{
			ID:          "add_dependency",
			Description: "Add a new npm dependency",
			Category:    "dependency",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.DependencyAdded,
					Evidence: detect.ExtendedEvidence{
						NewName:    "lodash",
						AfterValue: "^4.17.21",
					},
					Weight:     0.75,
					Confidence: 1.0,
					Tags:       []string{"config"},
				},
			},
			Files:            []string{"package.json"},
			ExpectedIntent:   "Add lodash dependency",
			ExpectedTemplate: "add_dependency",
			MinConfidence:    0.7,
		},

		// Parameter change
		{
			ID:          "parameter_added",
			Description: "Add parameter to function",
			Category:    "api_change",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.ParameterAdded,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "src/service.js"}},
						Symbols:    []string{"name:fetchData"},
					},
					Weight:     0.85,
					Confidence: 1.0,
					Tags:       []string{"api"},
				},
			},
			Modules:           []string{"Service"},
			Files:             []string{"src/service.js"},
			ExpectedIntent:    "Update fetchData parameters in Service",
			ExpectedTemplate:  "update_api_parameters",
			MinConfidence:     0.6,
		},

		// Config change
		{
			ID:          "config_timeout",
			Description: "Update timeout configuration",
			Category:    "config",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.JSONValueChanged,
					Evidence: detect.ExtendedEvidence{
						FileRanges:  []detect.FileRange{{Path: "config.json"}},
						Symbols:     []string{"timeout"},
						BeforeValue: "5000",
						AfterValue:  "10000",
					},
					Weight:     0.2,
					Confidence: 1.0,
					Tags:       []string{"config"},
				},
			},
			Files:            []string{"config.json"},
			ExpectedIntent:   "Update timeout in config",
			ExpectedTemplate: "update_json_config",
			MinConfidence:    0.4,
		},

		// Multiple functions added
		{
			ID:          "add_multiple_functions",
			Description: "Add multiple related functions",
			Category:    "add_feature",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:createUser"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:updateUser"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						Symbols: []string{"name:deleteUser"},
					},
					Weight:     0.8,
					Confidence: 1.0,
				},
			},
			Modules:          []string{"Users"},
			ExpectedIntent:   "Add createUser, updateUser and others in Users",
			ExpectedTemplate: "add_multiple_functions",
			MinConfidence:    0.6,
		},

		// Empty/no changes
		{
			ID:             "no_changes",
			Description:    "No changes detected",
			Category:       "empty",
			Signals:        []*detect.ChangeSignal{},
			ExpectedIntent: "No changes detected",
			MinConfidence:  1.0,
		},

		// Feature flag change
		{
			ID:          "feature_flag_change",
			Description: "Toggle a feature flag",
			Category:    "config",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FeatureFlagChanged,
					Evidence: detect.ExtendedEvidence{
						FileRanges:  []detect.FileRange{{Path: "config.json"}},
						Symbols:     []string{"newFeatureEnabled"},
						BeforeValue: "false",
						AfterValue:  "true",
					},
					Weight:     0.7,
					Confidence: 1.0,
					Tags:       []string{"feature-flag", "config"},
				},
			},
			Files:            []string{"config.json"},
			Modules:          []string{"Config"},
			ExpectedIntent:   "Toggle newFeatureEnabled feature flag in Config",
			ExpectedTemplate: "toggle_feature_flag",
			MinConfidence:    0.6,
		},

		// Schema change
		{
			ID:          "schema_field_added",
			Description: "Add a field to a schema model",
			Category:    "schema",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.SchemaFieldAdded,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "prisma/schema.prisma"}},
						Symbols:    []string{"model:User"},
						NewName:    "User",
					},
					Weight:     0.8,
					Confidence: 1.0,
					Tags:       []string{"schema"},
				},
			},
			Files:            []string{"prisma/schema.prisma"},
			ExpectedIntent:   "Add to User",
			ExpectedTemplate: "add_schema_field",
			MinConfidence:    0.7,
		},

		// Migration added
		{
			ID:          "migration_added",
			Description: "Add a new database migration",
			Category:    "schema",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.MigrationAdded,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "migrations/20210101_create_users.sql"}},
						Symbols:    []string{"create_users"},
					},
					Weight:     0.9,
					Confidence: 1.0,
					Tags:       []string{"schema", "migration"},
				},
			},
			Files:            []string{"migrations/20210101_create_users.sql"},
			ExpectedIntent:   "Add database migration for create_users",
			ExpectedTemplate: "add_migration",
			MinConfidence:    0.8,
		},

		// Mixed changes summary
		{
			ID:          "mixed_summary",
			Description: "Mixed changes across code, deps, and schema",
			Category:    "mixed",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.FunctionAdded,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "src/api/users.go"}},
						Symbols:    []string{"name:createUser"},
					},
					Weight:     0.8,
					Confidence: 0.9,
				},
				{
					Category: detect.DependencyUpdated,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "go.mod"}},
						NewName:    "github.com/foo/bar",
						AfterValue: "v1.2.3",
					},
					Weight:     0.7,
					Confidence: 0.9,
				},
				{
					Category: detect.SchemaFieldRemoved,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "db/schema.sql"}},
						Symbols:    []string{"table:users.legacy_id"},
					},
					Weight:     0.85,
					Confidence: 0.9,
				},
			},
			Files:            []string{"src/api/users.go", "go.mod", "db/schema.sql"},
			ExpectedIntent:   "Mixed changes in General: 1 function added, 1 dependency change, and 1 schema change",
			AcceptableIntents: []string{
				"Mixed changes in General: 1 dependency change, 1 schema change, and 1 function added",
				"Mixed changes in General: 1 schema change, 1 function added, and 1 dependency change",
			},
			ExpectedTemplate: "mixed_summary",
			MinConfidence:    0.45,
		},

		// Config timeout wording
		{
			ID:          "config_timeout_wording",
			Description: "Timeout change should avoid duplicated keyword",
			Category:    "config",
			Signals: []*detect.ChangeSignal{
				{
					Category: detect.TimeoutChanged,
					Evidence: detect.ExtendedEvidence{
						FileRanges: []detect.FileRange{{Path: "config.yaml"}},
						Symbols:    []string{"auth.session.timeout"},
						BeforeValue: "5s",
						AfterValue:  "10s",
					},
					Weight:     0.6,
					Confidence: 0.9,
					Tags:       []string{"config"},
				},
			},
			Modules:          []string{"Auth"},
			Files:            []string{"config.yaml"},
			ExpectedIntent:   "Update auth session timeout in Auth",
			ExpectedTemplate: "update_timeout",
			MinConfidence:    0.6,
		},
	}
}
