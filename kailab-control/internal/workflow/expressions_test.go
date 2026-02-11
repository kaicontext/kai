package workflow

import (
	"testing"
)

func TestInterpolateSimple(t *testing.T) {
	ctx := NewExprContext()
	ctx.Matrix["os"] = "ubuntu-latest"
	ctx.Env["CARGO_TERM_COLOR"] = "always"

	tests := []struct {
		input    string
		expected string
	}{
		{"no expressions", "no expressions"},
		{"${{ matrix.os }}", "ubuntu-latest"},
		{"Build (${{ matrix.os }})", "Build (ubuntu-latest)"},
		{"${{ env.CARGO_TERM_COLOR }}", "always"},
		{"${{ matrix.os }}-cargo-key", "ubuntu-latest-cargo-key"},
		{"a ${{ matrix.os }} b ${{ env.CARGO_TERM_COLOR }} c", "a ubuntu-latest b always c"},
	}

	for _, tt := range tests {
		result := Interpolate(tt.input, ctx)
		if result != tt.expected {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestInterpolateGitHub(t *testing.T) {
	ctx := NewExprContext()
	ctx.GitHub["actor"] = "jacobschatz"
	ctx.GitHub["ref_name"] = "main"
	ctx.GitHub["repository"] = "kai-org/kai"
	ctx.GitHub["event"] = map[string]interface{}{
		"inputs": map[string]interface{}{
			"tag": "v1.0.0",
		},
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"${{ github.actor }}", "jacobschatz"},
		{"${{ github.ref_name }}", "main"},
		{"${{ github.repository }}", "kai-org/kai"},
		{"${{ github.event.inputs.tag }}", "v1.0.0"},
	}

	for _, tt := range tests {
		result := Interpolate(tt.input, ctx)
		if result != tt.expected {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestInterpolateSecrets(t *testing.T) {
	ctx := NewExprContext()
	ctx.Secrets["GITHUB_TOKEN"] = "ghp_abc123"
	ctx.Secrets["DEPLOY_KEY"] = "secret-key"

	result := Interpolate("${{ secrets.GITHUB_TOKEN }}", ctx)
	if result != "ghp_abc123" {
		t.Errorf("expected ghp_abc123, got %q", result)
	}
}

func TestInterpolateSteps(t *testing.T) {
	ctx := NewExprContext()
	ctx.Steps["meta"] = StepResult{
		Outputs: map[string]string{
			"tags":   "v1.0.0,latest",
			"labels": "org.opencontainers.image.source=...",
		},
		Outcome:    "success",
		Conclusion: "success",
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"${{ steps.meta.outputs.tags }}", "v1.0.0,latest"},
		{"${{ steps.meta.outputs.labels }}", "org.opencontainers.image.source=..."},
		{"${{ steps.meta.outcome }}", "success"},
	}

	for _, tt := range tests {
		result := Interpolate(tt.input, ctx)
		if result != tt.expected {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestInterpolateNeeds(t *testing.T) {
	ctx := NewExprContext()
	ctx.Needs["build"] = JobResult{
		Outputs: map[string]string{"version": "1.2.3"},
		Result:  "success",
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"${{ needs.build.result }}", "success"},
		{"${{ needs.build.outputs.version }}", "1.2.3"},
	}

	for _, tt := range tests {
		result := Interpolate(tt.input, ctx)
		if result != tt.expected {
			t.Errorf("Interpolate(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestEvalExprBooleans(t *testing.T) {
	ctx := NewExprContext()

	tests := []struct {
		expr     string
		expected bool
	}{
		{"true", true},
		{"false", false},
		{"!false", true},
		{"!true", false},
		{"true && true", true},
		{"true && false", false},
		{"false || true", true},
		{"false || false", false},
	}

	for _, tt := range tests {
		result := EvalExprBool(tt.expr, ctx)
		if result != tt.expected {
			t.Errorf("EvalExprBool(%q) = %v, want %v", tt.expr, result, tt.expected)
		}
	}
}

func TestEvalExprComparisons(t *testing.T) {
	ctx := NewExprContext()
	ctx.Matrix["os"] = "ubuntu-latest"
	ctx.GitHub["event_name"] = "push"

	tests := []struct {
		expr     string
		expected bool
	}{
		{"matrix.os == 'ubuntu-latest'", true},
		{"matrix.os == 'macos-latest'", false},
		{"matrix.os != 'macos-latest'", true},
		{"github.event_name == 'push'", true},
		{"github.event_name == 'pull_request'", false},
		{"1 < 2", true},
		{"2 > 1", true},
		{"1 == 1", true},
		{"1 <= 1", true},
		{"1 >= 1", true},
	}

	for _, tt := range tests {
		result := EvalExprBool(tt.expr, ctx)
		if result != tt.expected {
			t.Errorf("EvalExprBool(%q) = %v, want %v", tt.expr, result, tt.expected)
		}
	}
}

func TestEvalExprOrDefault(t *testing.T) {
	ctx := NewExprContext()
	ctx.GitHub["event"] = map[string]interface{}{
		"inputs": map[string]interface{}{
			"tag": "v2.0.0",
		},
	}
	ctx.GitHub["ref_name"] = "main"

	// Test || as default operator: first truthy value wins
	result := Interpolate("${{ github.event.inputs.tag || github.ref_name }}", ctx)
	if result != "v2.0.0" {
		t.Errorf("expected 'v2.0.0', got %q", result)
	}

	// When first value is empty, falls back
	ctx2 := NewExprContext()
	ctx2.GitHub["event"] = map[string]interface{}{
		"inputs": map[string]interface{}{},
	}
	ctx2.GitHub["ref_name"] = "main"
	result2 := Interpolate("${{ github.event.inputs.tag || github.ref_name }}", ctx2)
	if result2 != "main" {
		t.Errorf("expected 'main', got %q", result2)
	}
}

func TestFunctionContains(t *testing.T) {
	ctx := NewExprContext()
	ctx.GitHub["ref"] = "refs/heads/feature/my-branch"

	tests := []struct {
		expr     string
		expected bool
	}{
		{"contains(github.ref, 'feature')", true},
		{"contains(github.ref, 'release')", false},
		{"contains('hello world', 'hello')", true},
	}

	for _, tt := range tests {
		result := EvalExprBool(tt.expr, ctx)
		if result != tt.expected {
			t.Errorf("EvalExprBool(%q) = %v, want %v", tt.expr, result, tt.expected)
		}
	}
}

func TestFunctionStartsWithEndsWith(t *testing.T) {
	ctx := NewExprContext()

	tests := []struct {
		expr     string
		expected bool
	}{
		{"startsWith('hello world', 'hello')", true},
		{"startsWith('hello world', 'world')", false},
		{"endsWith('hello world', 'world')", true},
		{"endsWith('hello world', 'hello')", false},
	}

	for _, tt := range tests {
		result := EvalExprBool(tt.expr, ctx)
		if result != tt.expected {
			t.Errorf("EvalExprBool(%q) = %v, want %v", tt.expr, result, tt.expected)
		}
	}
}

func TestFunctionFormat(t *testing.T) {
	ctx := NewExprContext()

	result := EvalExpr("format('Hello {0}, you are {1}!', 'world', 'great')", ctx)
	expected := "Hello world, you are great!"
	if result != expected {
		t.Errorf("format() = %q, want %q", result, expected)
	}
}

func TestFunctionToJSON(t *testing.T) {
	ctx := NewExprContext()
	ctx.Matrix["os"] = "ubuntu-latest"

	result := EvalExpr("toJSON(matrix.os)", ctx)
	if result != `"ubuntu-latest"` {
		t.Errorf("toJSON() = %q, want '\"ubuntu-latest\"'", result)
	}
}

func TestFunctionStatusChecks(t *testing.T) {
	ctx := NewExprContext()
	ctx.GitHub["job_status"] = "success"

	if !EvalExprBool("success()", ctx) {
		t.Error("success() should be true when status is success")
	}
	if EvalExprBool("failure()", ctx) {
		t.Error("failure() should be false when status is success")
	}
	if !EvalExprBool("always()", ctx) {
		t.Error("always() should always be true")
	}

	ctx.GitHub["job_status"] = "failure"
	if EvalExprBool("success()", ctx) {
		t.Error("success() should be false when status is failure")
	}
	if !EvalExprBool("failure()", ctx) {
		t.Error("failure() should be true when status is failure")
	}
}

func TestEvalExprBoolWithDollarBraces(t *testing.T) {
	ctx := NewExprContext()
	ctx.Matrix["use_cross"] = true

	// if: conditions can come with or without ${{ }}
	if !EvalExprBool("matrix.use_cross", ctx) {
		t.Error("bare expression should work")
	}
	if !EvalExprBool("${{ matrix.use_cross }}", ctx) {
		t.Error("wrapped expression should work")
	}
}

func TestComplexExpression(t *testing.T) {
	ctx := NewExprContext()
	ctx.Matrix["cross"] = true
	ctx.Matrix["use_cross"] = false

	// matrix.cross && !matrix.use_cross
	result := EvalExprBool("matrix.cross && !matrix.use_cross", ctx)
	if !result {
		t.Error("expected true for matrix.cross && !matrix.use_cross")
	}

	// !matrix.cross && !matrix.use_cross
	result2 := EvalExprBool("!matrix.cross && !matrix.use_cross", ctx)
	if result2 {
		t.Error("expected false for !matrix.cross && !matrix.use_cross")
	}
}

func TestInterpolateMissing(t *testing.T) {
	ctx := NewExprContext()

	// Missing values should interpolate to empty string
	result := Interpolate("${{ matrix.nonexistent }}", ctx)
	if result != "" {
		t.Errorf("expected empty string for missing value, got %q", result)
	}

	result2 := Interpolate("prefix-${{ matrix.nonexistent }}-suffix", ctx)
	if result2 != "prefix--suffix" {
		t.Errorf("expected 'prefix--suffix', got %q", result2)
	}
}

func TestInterpolateRunner(t *testing.T) {
	ctx := NewExprContext()
	ctx.Runner["os"] = "Linux"
	ctx.Runner["arch"] = "X64"

	result := Interpolate("${{ runner.os }}", ctx)
	if result != "Linux" {
		t.Errorf("expected 'Linux', got %q", result)
	}
}

func TestInterpolateInputs(t *testing.T) {
	ctx := NewExprContext()
	ctx.Inputs["tag"] = "v1.0.0"

	result := Interpolate("${{ inputs.tag }}", ctx)
	if result != "v1.0.0" {
		t.Errorf("expected 'v1.0.0', got %q", result)
	}
}

func TestHashFilesWithCallback(t *testing.T) {
	ctx := NewExprContext()
	ctx.HashFilesFunc = func(patterns []string) string {
		// Simulate a hash result
		if len(patterns) == 1 && patterns[0] == "**/Cargo.lock" {
			return "abc123def456"
		}
		return ""
	}

	result := Interpolate("${{ hashFiles('**/Cargo.lock') }}", ctx)
	if result != "abc123def456" {
		t.Errorf("expected 'abc123def456', got %q", result)
	}

	// Multiple patterns
	ctx.HashFilesFunc = func(patterns []string) string {
		if len(patterns) == 2 {
			return "multi-hash"
		}
		return ""
	}
	result2 := Interpolate("${{ hashFiles('**/Cargo.lock', '**/Cargo.toml') }}", ctx)
	if result2 != "multi-hash" {
		t.Errorf("expected 'multi-hash', got %q", result2)
	}
}

func TestHashFilesNoCallback(t *testing.T) {
	ctx := NewExprContext()
	// No HashFilesFunc set - should return empty string
	result := Interpolate("${{ hashFiles('**/Cargo.lock') }}", ctx)
	if result != "" {
		t.Errorf("expected empty string without callback, got %q", result)
	}
}

func TestHashFilesInCacheKey(t *testing.T) {
	ctx := NewExprContext()
	ctx.Runner["os"] = "Linux"
	ctx.HashFilesFunc = func(patterns []string) string {
		return "a1b2c3d4e5f6"
	}

	// Simulates the real cache key pattern
	result := Interpolate("${{ runner.os }}-cargo-${{ hashFiles('**/Cargo.lock') }}", ctx)
	if result != "Linux-cargo-a1b2c3d4e5f6" {
		t.Errorf("expected 'Linux-cargo-a1b2c3d4e5f6', got %q", result)
	}
}
