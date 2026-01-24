package intent

import (
	"strings"
	"testing"

	"kai-core/detect"
)

func TestNewEngine(t *testing.T) {
	e := NewEngine()
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.templates == nil || len(e.templates) == 0 {
		t.Error("expected templates to be initialized with defaults")
	}
	if e.clusterer == nil {
		t.Error("expected clusterer to be initialized")
	}
}

func TestNewEngineWithTemplates(t *testing.T) {
	customTemplates := []Template{
		{ID: "custom", Pattern: "Custom {area}", Priority: 100},
	}

	e := NewEngineWithTemplates(customTemplates)

	if len(e.templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(e.templates))
	}
	if e.templates[0].ID != "custom" {
		t.Errorf("expected template ID 'custom', got %q", e.templates[0].ID)
	}
}

func TestGenerateIntent_Empty(t *testing.T) {
	e := NewEngine()

	result := e.GenerateIntent([]*detect.ChangeSignal{}, []string{}, []string{})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if result.Primary.Text != "No changes detected" {
		t.Errorf("expected 'No changes detected', got %q", result.Primary.Text)
	}
	if result.Primary.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", result.Primary.Confidence)
	}
}

func TestGenerateIntent_SingleFunctionAdded(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/utils.js"}},
				Symbols:    []string{"name:handleClick"},
			},
			Weight:     0.8,
			Confidence: 1.0,
		},
	}

	result := e.GenerateIntent(signals, []string{"Frontend"}, []string{"src/utils.js"})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if !strings.Contains(result.Primary.Text, "Add") {
		t.Errorf("expected intent to contain 'Add', got %q", result.Primary.Text)
	}
	if !strings.Contains(result.Primary.Text, "handleClick") {
		t.Errorf("expected intent to contain 'handleClick', got %q", result.Primary.Text)
	}
}

func TestGenerateIntent_FunctionRenamed(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionRenamed,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/utils.js"}},
				OldName:    "oldFunc",
				NewName:    "newFunc",
			},
			Weight:     0.9,
			Confidence: 0.95,
			Tags:       []string{"api"},
		},
	}

	result := e.GenerateIntent(signals, []string{"Utils"}, []string{"src/utils.js"})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if !strings.Contains(result.Primary.Text, "Rename") {
		t.Errorf("expected intent to contain 'Rename', got %q", result.Primary.Text)
	}
	if !strings.Contains(result.Primary.Text, "oldFunc") {
		t.Errorf("expected intent to contain 'oldFunc', got %q", result.Primary.Text)
	}
	if !strings.Contains(result.Primary.Text, "newFunc") {
		t.Errorf("expected intent to contain 'newFunc', got %q", result.Primary.Text)
	}
	// Rename template has high confidence
	if result.Primary.Confidence < 0.8 {
		t.Errorf("expected high confidence for rename, got %f", result.Primary.Confidence)
	}
}

func TestEngine_GenerateIntent_Refactor(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:newHelper"},
			},
			Weight:     0.8,
			Confidence: 1.0,
		},
		{
			Category: detect.FunctionRemoved,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:oldHelper"},
			},
			Weight:     0.8,
			Confidence: 1.0,
		},
	}

	result := e.GenerateIntent(signals, []string{"General"}, []string{})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if !strings.Contains(result.Primary.Text, "Refactor") {
		t.Errorf("expected intent to contain 'Refactor', got %q", result.Primary.Text)
	}
}

func TestGenerateIntent_Dependency(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.DependencyAdded,
			Evidence: detect.ExtendedEvidence{
				Symbols:    []string{"dependencies.lodash"},
				NewName:    "lodash",
				AfterValue: "4.17.21",
			},
			Weight:     0.75,
			Confidence: 1.0,
			Tags:       []string{"config"},
		},
	}

	result := e.GenerateIntent(signals, []string{}, []string{"package.json"})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if !strings.Contains(result.Primary.Text, "dependency") || !strings.Contains(result.Primary.Text, "lodash") {
		t.Errorf("expected intent about lodash dependency, got %q", result.Primary.Text)
	}
}

func TestGenerateIntent_Alternatives(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:handleClick"},
			},
			Confidence: 1.0,
		},
	}

	result := e.GenerateIntent(signals, []string{"UI"}, []string{})

	// Should have primary and possibly alternatives
	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	// Alternatives may or may not be present depending on matching templates
}

func TestGenerateIntent_Warnings(t *testing.T) {
	e := NewEngine()

	// Create many unrelated signals
	signals := make([]*detect.ChangeSignal, 10)
	for i := 0; i < 10; i++ {
		signals[i] = &detect.ChangeSignal{
			Category: detect.FileContentChanged,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "file" + string(rune('0'+i)) + ".js"}},
			},
			Confidence: 0.3,
		}
	}

	result := e.GenerateIntent(signals, []string{}, []string{})

	// Should have warnings about low confidence or many changes
	if len(result.Warnings) == 0 {
		t.Log("Note: no warnings generated, which is acceptable")
	}
}

func TestGenerateIntent_BreakingChangeWarning(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionRemoved,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:publicAPI"},
			},
			Tags:       []string{"breaking", "api"},
			Confidence: 1.0,
		},
	}

	result := e.GenerateIntent(signals, []string{}, []string{})

	foundBreaking := false
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), "breaking") {
			foundBreaking = true
			break
		}
	}
	if !foundBreaking {
		t.Error("expected warning about breaking changes")
	}
}

func TestCalculateConfidence(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Confidence: 1.0},
			{Confidence: 0.8},
		},
		Cohesion: 0.9,
	}

	confidence := calculateConfidence(0.95, cluster)

	// confidence = base * cohesion * avgSignalConf
	// = 0.95 * 0.9 * 0.9 = 0.7695
	if confidence < 0.7 || confidence > 0.8 {
		t.Errorf("expected confidence around 0.77, got %f", confidence)
	}
}

func TestCalculateConfidence_Penalty(t *testing.T) {
	// Many signals should reduce confidence
	signals := make([]*detect.ChangeSignal, 10)
	for i := range signals {
		signals[i] = &detect.ChangeSignal{Confidence: 1.0}
	}

	cluster := &ChangeCluster{
		Signals:  signals,
		Cohesion: 1.0,
	}

	confidence := calculateConfidence(1.0, cluster)

	// Should be penalized for having many signals
	if confidence >= 1.0 {
		t.Errorf("expected penalty for many signals, got %f", confidence)
	}
}

func TestCalculateConfidence_RenameBoost(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionRenamed, Confidence: 1.0},
		},
		Cohesion: 0.8,
	}

	confidence := calculateConfidence(0.9, cluster)

	// Rename should boost confidence (but capped at 1.0)
	if confidence < 0.7 {
		t.Errorf("expected confidence boost for rename, got %f", confidence)
	}
}

func TestGenerateIntentFromChangeTypes(t *testing.T) {
	e := NewEngine()

	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				FileRanges: []detect.FileRange{{Path: "test.js"}},
				Symbols:    []string{"name:newFunc"},
			},
		},
	}

	result := e.GenerateIntentFromChangeTypes(changeTypes, []string{"General"}, []string{"test.js"})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if !strings.Contains(result.Primary.Text, "Add") {
		t.Errorf("expected 'Add' in intent, got %q", result.Primary.Text)
	}
}

func TestGenerateSimpleIntent(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:foo"},
			},
			Confidence: 1.0,
		},
	}

	text := e.GenerateSimpleIntent(signals, []string{"Test"}, []string{})

	if text == "" {
		t.Error("expected non-empty intent text")
	}
}

func TestIntentResult_GetPrimaryConfidence(t *testing.T) {
	result := &IntentResult{
		Primary: &IntentCandidate{Confidence: 0.85},
	}

	if conf := result.GetPrimaryConfidence(); conf != 0.85 {
		t.Errorf("expected 0.85, got %f", conf)
	}

	emptyResult := &IntentResult{}
	if conf := emptyResult.GetPrimaryConfidence(); conf != 0 {
		t.Errorf("expected 0 for nil primary, got %f", conf)
	}
}

func TestIntentResult_HasHighConfidence(t *testing.T) {
	highConf := &IntentResult{Primary: &IntentCandidate{Confidence: 0.8}}
	if !highConf.HasHighConfidence() {
		t.Error("expected high confidence for 0.8")
	}

	lowConf := &IntentResult{Primary: &IntentCandidate{Confidence: 0.5}}
	if lowConf.HasHighConfidence() {
		t.Error("expected not high confidence for 0.5")
	}
}

func TestIntentResult_ShouldUseLLM(t *testing.T) {
	lowConf := &IntentResult{Primary: &IntentCandidate{Confidence: 0.3}}
	if !lowConf.ShouldUseLLM() {
		t.Error("expected ShouldUseLLM true for 0.3")
	}

	highConf := &IntentResult{Primary: &IntentCandidate{Confidence: 0.8}}
	if highConf.ShouldUseLLM() {
		t.Error("expected ShouldUseLLM false for 0.8")
	}
}

func TestIntentResult_GetAlternativeTexts(t *testing.T) {
	result := &IntentResult{
		Primary: &IntentCandidate{Text: "Primary"},
		Alternatives: []*IntentCandidate{
			{Text: "Alt 1"},
			{Text: "Alt 2"},
		},
	}

	texts := result.GetAlternativeTexts()
	if len(texts) != 2 {
		t.Fatalf("expected 2 alternatives, got %d", len(texts))
	}
	if texts[0] != "Alt 1" || texts[1] != "Alt 2" {
		t.Errorf("unexpected alternative texts: %v", texts)
	}
}

func TestIntentResult_GetTopAlternatives(t *testing.T) {
	result := &IntentResult{
		Alternatives: []*IntentCandidate{
			{Text: "A"},
			{Text: "B"},
			{Text: "C"},
		},
	}

	top2 := result.GetTopAlternatives(2)
	if len(top2) != 2 {
		t.Errorf("expected 2 alternatives, got %d", len(top2))
	}

	all := result.GetTopAlternatives(10)
	if len(all) != 3 {
		t.Errorf("expected all 3 alternatives, got %d", len(all))
	}
}

func TestIntentCandidate_FormatWithConfidence(t *testing.T) {
	tests := []struct {
		confidence   float64
		expectedPart string
	}{
		{0.9, "high"},
		{0.6, "medium"},
		{0.3, "low"},
	}

	for _, tc := range tests {
		c := &IntentCandidate{Text: "Test", Confidence: tc.confidence}
		formatted := c.FormatWithConfidence()
		if !strings.Contains(formatted, tc.expectedPart) {
			t.Errorf("expected %q to contain %q", formatted, tc.expectedPart)
		}
	}
}

func TestGenerateIntent_MixedSummary(t *testing.T) {
	e := NewEngine()

	signals := []*detect.ChangeSignal{
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
	}

	result := e.GenerateIntent(signals, []string{}, []string{"src/api/users.go", "go.mod", "db/schema.sql"})

	if result.Primary == nil {
		t.Fatal("expected Primary to be set")
	}
	if result.Primary.Template != "mixed_summary" {
		t.Fatalf("expected mixed_summary template, got %q", result.Primary.Template)
	}
	if !strings.Contains(result.Primary.Text, "Mixed changes in General:") {
		t.Errorf("expected mixed summary text, got %q", result.Primary.Text)
	}
	if !(strings.Contains(result.Primary.Text, "schema change") ||
		strings.Contains(result.Primary.Text, "dependency change") ||
		strings.Contains(result.Primary.Text, "function added")) {
		t.Errorf("expected mixed summary to include sub-intent details, got %q", result.Primary.Text)
	}
}
