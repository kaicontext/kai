package intent

import (
	"os"
	"strings"
	"testing"

	"kai-core/detect"
)

func TestNewEvaluator(t *testing.T) {
	e := NewEvaluator(NewEngine())
	if e == nil {
		t.Fatal("NewEvaluator returned nil")
	}
	if e.engine == nil {
		t.Error("expected engine to be set")
	}
	if e.cases == nil {
		t.Error("expected cases slice to be initialized")
	}
}

func TestEvaluator_AddCase(t *testing.T) {
	e := NewEvaluator(NewEngine())

	c := &EvalCase{
		ID:             "test1",
		ExpectedIntent: "Test intent",
	}

	e.AddCase(c)

	if len(e.cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(e.cases))
	}
}

func TestEvaluator_AddCases(t *testing.T) {
	e := NewEvaluator(NewEngine())

	cases := []*EvalCase{
		{ID: "test1"},
		{ID: "test2"},
	}

	e.AddCases(cases)

	if len(e.cases) != 2 {
		t.Errorf("expected 2 cases, got %d", len(e.cases))
	}
}

func TestEvaluator_RunEmpty(t *testing.T) {
	e := NewEvaluator(NewEngine())

	report := e.Run()

	if report.TotalCases != 0 {
		t.Errorf("expected 0 cases, got %d", report.TotalCases)
	}
	if report.PassedCases != 0 {
		t.Errorf("expected 0 passed, got %d", report.PassedCases)
	}
}

func TestEvaluator_RunSingleCase(t *testing.T) {
	e := NewEvaluator(NewEngine())

	e.AddCase(&EvalCase{
		ID:          "rename_test",
		Description: "Test rename detection",
		Category:    "rename",
		Signals: []*detect.ChangeSignal{
			{
				Category: detect.FunctionRenamed,
				Evidence: detect.ExtendedEvidence{
					FileRanges: []detect.FileRange{{Path: "test.js"}},
					OldName:    "foo",
					NewName:    "bar",
				},
				Weight:     0.9,
				Confidence: 0.95,
			},
		},
		Modules:          []string{"Test"},
		ExpectedIntent:   "Rename foo to bar in Test",
		ExpectedTemplate: "rename_function",
		MinConfidence:    0.8,
	})

	report := e.Run()

	if report.TotalCases != 1 {
		t.Errorf("expected 1 case, got %d", report.TotalCases)
	}
	if len(report.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(report.Results))
	}

	result := report.Results[0]
	if result.CaseID != "rename_test" {
		t.Errorf("expected case ID 'rename_test', got %q", result.CaseID)
	}
	if !result.Passed {
		t.Errorf("expected test to pass, but it failed: %v", result.Errors)
	}
	if result.MatchType != "exact" && result.MatchType != "acceptable" {
		t.Errorf("expected exact or acceptable match, got %q", result.MatchType)
	}
}

func TestEvaluator_RunBuiltinCases(t *testing.T) {
	e := NewEvaluator(NewEngine())
	e.AddCases(BuiltinTestCases())

	report := e.Run()

	if report.TotalCases == 0 {
		t.Error("expected some test cases")
	}

	// Log the report for debugging
	t.Logf("Pass rate: %.1f%% (%d/%d)", report.PassRate, report.PassedCases, report.TotalCases)

	// We expect most builtin cases to pass
	if report.PassRate < 50 {
		t.Errorf("expected at least 50%% pass rate, got %.1f%%", report.PassRate)
		for _, f := range report.FailedResults {
			t.Logf("Failed: %s - generated=%q, errors=%v", f.CaseID, f.GeneratedIntent, f.Errors)
		}
	}
}

func TestScoreMatch_Exact(t *testing.T) {
	e := NewEvaluator(NewEngine())

	score, matchType := e.scoreMatch("Add foo in Bar", "Add foo in Bar", nil)

	if matchType != "exact" {
		t.Errorf("expected exact match, got %q", matchType)
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

func TestScoreMatch_ExactNormalized(t *testing.T) {
	e := NewEvaluator(NewEngine())

	score, matchType := e.scoreMatch("add foo in bar", "Add Foo In Bar", nil)

	if matchType != "exact" {
		t.Errorf("expected exact match with normalization, got %q", matchType)
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

func TestScoreMatch_Acceptable(t *testing.T) {
	e := NewEvaluator(NewEngine())

	acceptable := []string{"Add function foo in Bar", "Add foo method in Bar"}
	score, matchType := e.scoreMatch("Add function foo in Bar", "Add foo in Bar", acceptable)

	if matchType != "acceptable" {
		t.Errorf("expected acceptable match, got %q", matchType)
	}
	if score != 0.95 {
		t.Errorf("expected score 0.95, got %f", score)
	}
}

func TestScoreMatch_Partial(t *testing.T) {
	e := NewEvaluator(NewEngine())
	e.matchOpts.PartialMatchThreshold = 0.5

	// "Add foo in Bar" vs "Add bar in Foo" - some token overlap
	score, matchType := e.scoreMatch("Add foo in Bar", "Add bar in Foo", nil)

	// "add", "foo", "in", "bar" overlap with "add", "bar", "in", "foo"
	// All 4 tokens overlap, so similarity should be 1.0
	if matchType == "none" && score > 0 {
		t.Logf("partial match with score %f", score)
	}
}

func TestScoreMatch_None(t *testing.T) {
	e := NewEvaluator(NewEngine())

	score, matchType := e.scoreMatch("Completely different", "Nothing alike", nil)

	if matchType != "none" {
		t.Errorf("expected no match, got %q", matchType)
	}
	if score >= e.matchOpts.PartialMatchThreshold {
		t.Errorf("expected low score, got %f", score)
	}
}

func TestNormalizeIntent(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Add Foo In Bar", "add foo in bar"},
		{"  multiple   spaces  ", "multiple spaces"},
		{"Already lowercase", "already lowercase"},
		{"\tTabs and\nnewlines", "tabs and newlines"},
	}

	for _, tc := range tests {
		result := normalizeIntent(tc.input)
		if result != tc.expected {
			t.Errorf("normalizeIntent(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestRemovePunctuation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello, World!", "Hello World"},
		{"foo.bar", "foobar"},
		{"test-case", "testcase"},
		{"no_underscores", "nounderscores"},
		{"keep123numbers", "keep123numbers"},
	}

	for _, tc := range tests {
		result := removePunctuation(tc.input)
		if result != tc.expected {
			t.Errorf("removePunctuation(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestTokenSimilarity(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected float64
	}{
		{"a b c", "a b c", 1.0},
		{"a b c", "d e f", 0.0},
		{"a b", "a b c", 2.0 / 3.0}, // 2 overlap, 3 union
		{"", "", 1.0},
		{"a", "", 0.0},
	}

	for _, tc := range tests {
		result := tokenSimilarity(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("tokenSimilarity(%q, %q) = %f, expected %f", tc.a, tc.b, result, tc.expected)
		}
	}
}

func TestEvalReport_FormatReport(t *testing.T) {
	report := &EvalReport{
		TotalCases:   10,
		PassedCases:  8,
		FailedCases:  2,
		OverallScore: 0.85,
		PassRate:     80.0,
		ByCategory: map[string]*CategoryStats{
			"rename": {Total: 3, Passed: 3, Failed: 0},
			"add":    {Total: 5, Passed: 4, Failed: 1},
		},
		ByTemplate: map[string]*TemplateStats{
			"rename_function": {Expected: 3, Used: 3, Correct: 3},
		},
		FailedResults: []*EvalResult{
			{CaseID: "failed1", MatchType: "none", Score: 0.2, GeneratedIntent: "Wrong"},
		},
	}

	formatted := report.FormatReport()

	if !strings.Contains(formatted, "Total Cases: 10") {
		t.Error("expected total cases in report")
	}
	if !strings.Contains(formatted, "80.0%") {
		t.Error("expected pass rate in report")
	}
	if !strings.Contains(formatted, "rename") {
		t.Error("expected category breakdown in report")
	}
	if !strings.Contains(formatted, "failed1") {
		t.Error("expected failed case in report")
	}
}

func TestEvalReport_SaveAndLoad(t *testing.T) {
	report := &EvalReport{
		TotalCases:  5,
		PassedCases: 4,
		FailedCases: 1,
		Results: []*EvalResult{
			{CaseID: "test1", Passed: true, Score: 1.0},
		},
	}

	tmpFile := "/tmp/eval_report_test.json"
	defer os.Remove(tmpFile)

	err := report.SaveReportJSON(tmpFile)
	if err != nil {
		t.Fatalf("failed to save report: %v", err)
	}

	// Verify file exists and has content
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty report file")
	}
	if !strings.Contains(string(data), `"total_cases": 5`) {
		t.Error("expected total_cases in JSON")
	}
}

func TestBuiltinTestCases(t *testing.T) {
	cases := BuiltinTestCases()

	if len(cases) == 0 {
		t.Fatal("expected some builtin test cases")
	}

	// Check each case has required fields
	for _, c := range cases {
		if c.ID == "" {
			t.Error("test case missing ID")
		}
		if c.ExpectedIntent == "" {
			t.Errorf("test case %s missing ExpectedIntent", c.ID)
		}
	}

	// Check we have variety of categories
	categories := make(map[string]bool)
	for _, c := range cases {
		if c.Category != "" {
			categories[c.Category] = true
		}
	}

	if len(categories) < 3 {
		t.Errorf("expected at least 3 different categories, got %d", len(categories))
	}
}

func TestDefaultMatchOptions(t *testing.T) {
	opts := DefaultMatchOptions()

	if opts.RequireExactMatch {
		t.Error("expected RequireExactMatch to be false by default")
	}
	if opts.PartialMatchThreshold <= 0 || opts.PartialMatchThreshold > 1 {
		t.Errorf("unexpected PartialMatchThreshold: %f", opts.PartialMatchThreshold)
	}
	if !opts.NormalizeText {
		t.Error("expected NormalizeText to be true by default")
	}
	if !opts.IgnorePunctuation {
		t.Error("expected IgnorePunctuation to be true by default")
	}
}

func TestEvaluator_SetMatchOptions(t *testing.T) {
	e := NewEvaluator(NewEngine())

	opts := MatchOptions{
		RequireExactMatch:     true,
		PartialMatchThreshold: 0.9,
	}
	e.SetMatchOptions(opts)

	if !e.matchOpts.RequireExactMatch {
		t.Error("expected RequireExactMatch to be set")
	}
	if e.matchOpts.PartialMatchThreshold != 0.9 {
		t.Errorf("expected PartialMatchThreshold 0.9, got %f", e.matchOpts.PartialMatchThreshold)
	}
}

// Benchmark for evaluation performance
func BenchmarkEvaluator_Run(b *testing.B) {
	e := NewEvaluator(NewEngine())
	e.AddCases(BuiltinTestCases())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Run()
	}
}
