package detect

import (
	"testing"
)

func TestNewRenameDetector(t *testing.T) {
	d := NewRenameDetector()
	if d == nil {
		t.Fatal("NewRenameDetector returned nil")
	}
	if d.SimilarityThreshold != 0.7 {
		t.Errorf("expected default threshold 0.7, got %f", d.SimilarityThreshold)
	}
}

func TestComputeSimilarity_Identical(t *testing.T) {
	a := "function foo() { return 1; }"
	b := "function foo() { return 1; }"

	similarity := computeSimilarity(a, b)
	if similarity != 1.0 {
		t.Errorf("expected similarity 1.0 for identical strings, got %f", similarity)
	}
}

func TestComputeSimilarity_CompletelyDifferent(t *testing.T) {
	a := "aaaa"
	b := "bbbb"

	similarity := computeSimilarity(a, b)
	if similarity >= 0.5 {
		t.Errorf("expected low similarity for completely different strings, got %f", similarity)
	}
}

func TestComputeSimilarity_Similar(t *testing.T) {
	a := "function handleClick() { console.log('clicked'); }"
	b := "function onClick() { console.log('clicked'); }"

	similarity := computeSimilarity(a, b)
	if similarity < 0.7 {
		t.Errorf("expected high similarity for similar code, got %f", similarity)
	}
}

func TestComputeSimilarity_EmptyStrings(t *testing.T) {
	if similarity := computeSimilarity("", ""); similarity != 1.0 {
		t.Errorf("expected 1.0 for two empty strings, got %f", similarity)
	}
	if similarity := computeSimilarity("foo", ""); similarity != 0.0 {
		t.Errorf("expected 0.0 when one string is empty, got %f", similarity)
	}
	if similarity := computeSimilarity("", "bar"); similarity != 0.0 {
		t.Errorf("expected 0.0 when one string is empty, got %f", similarity)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "b", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "ab", 1},
		{"abc", "abcd", 1},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
	}

	for _, tc := range tests {
		result := levenshteinDistance(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("levenshteinDistance(%q, %q) = %d, expected %d", tc.a, tc.b, result, tc.expected)
		}
	}
}

func TestTokenBasedSimilarity(t *testing.T) {
	tests := []struct {
		a, b        string
		minExpected float64
	}{
		{"a + b", "a + b", 1.0},
		{"a + b", "a + c", 0.5},
		{"function foo() { return 1; }", "function bar() { return 1; }", 0.7},
		{"", "", 1.0},
		{"x", "", 0.0},
	}

	for _, tc := range tests {
		result := TokenBasedSimilarity(tc.a, tc.b)
		if result < tc.minExpected {
			t.Errorf("TokenBasedSimilarity(%q, %q) = %f, expected >= %f", tc.a, tc.b, result, tc.minExpected)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		code     string
		expected []string
	}{
		{"a + b", []string{"a", "+", "b"}},
		{"foo(x, y)", []string{"foo", "(", "x", ",", "y", ")"}},
		{"return 1;", []string{"return", "1", ";"}},
	}

	for _, tc := range tests {
		result := tokenize(tc.code)
		if len(result) != len(tc.expected) {
			t.Errorf("tokenize(%q) = %v, expected %v", tc.code, result, tc.expected)
			continue
		}
		for i := range result {
			if result[i] != tc.expected[i] {
				t.Errorf("tokenize(%q)[%d] = %q, expected %q", tc.code, i, result[i], tc.expected[i])
			}
		}
	}
}

func TestDetectRenames_SimpleRename(t *testing.T) {
	d := NewRenameDetector()

	beforeFuncs := map[string]*FuncInfo{
		"oldFunc": {
			Name: "oldFunc",
			Body: "{ return x + 1; }",
		},
	}
	afterFuncs := map[string]*FuncInfo{
		"newFunc": {
			Name: "newFunc",
			Body: "{ return x + 1; }",
		},
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	if len(signals) != 1 {
		t.Fatalf("expected 1 rename signal, got %d", len(signals))
	}

	sig := signals[0]
	if sig.Category != FunctionRenamed {
		t.Errorf("expected category FUNCTION_RENAMED, got %s", sig.Category)
	}
	if sig.Evidence.OldName != "oldFunc" {
		t.Errorf("expected old name 'oldFunc', got %q", sig.Evidence.OldName)
	}
	if sig.Evidence.NewName != "newFunc" {
		t.Errorf("expected new name 'newFunc', got %q", sig.Evidence.NewName)
	}
	if sig.Confidence < 0.9 {
		t.Errorf("expected high confidence for identical bodies, got %f", sig.Confidence)
	}
}

func TestDetectRenames_NoRename(t *testing.T) {
	d := NewRenameDetector()

	beforeFuncs := map[string]*FuncInfo{
		"funcA": {Name: "funcA", Body: "{ return 1; }"},
	}
	afterFuncs := map[string]*FuncInfo{
		"funcB": {Name: "funcB", Body: "{ return completely_different_code(); }"},
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	if len(signals) != 0 {
		t.Errorf("expected no rename signal for completely different functions, got %d", len(signals))
	}
}

func TestDetectRenames_BelowThreshold(t *testing.T) {
	d := NewRenameDetector()
	d.SimilarityThreshold = 0.95 // Very high threshold

	beforeFuncs := map[string]*FuncInfo{
		"oldFunc": {Name: "oldFunc", Body: "{ const result = x + 1; return result; }"},
	}
	afterFuncs := map[string]*FuncInfo{
		"newFunc": {Name: "newFunc", Body: "{ const value = x + 2; return value; }"}, // More different
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	// With very high threshold (0.95), these moderately different bodies should not be detected as rename
	if len(signals) > 0 {
		similarity := signals[0].Confidence
		if similarity >= 0.95 {
			t.Errorf("expected similarity below 0.95 threshold, got %f", similarity)
		}
	}
}

func TestDetectRenames_MultipleRenames(t *testing.T) {
	d := NewRenameDetector()

	beforeFuncs := map[string]*FuncInfo{
		"oldA": {Name: "oldA", Body: "{ return a; }"},
		"oldB": {Name: "oldB", Body: "{ return b; }"},
	}
	afterFuncs := map[string]*FuncInfo{
		"newA": {Name: "newA", Body: "{ return a; }"},
		"newB": {Name: "newB", Body: "{ return b; }"},
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	if len(signals) != 2 {
		t.Fatalf("expected 2 rename signals, got %d", len(signals))
	}

	// Verify both renames were detected
	renames := make(map[string]string)
	for _, sig := range signals {
		renames[sig.Evidence.OldName] = sig.Evidence.NewName
	}

	if renames["oldA"] != "newA" || renames["oldB"] != "newB" {
		t.Errorf("rename mapping incorrect: %v", renames)
	}
}

func TestDetectRenames_EmptyBody(t *testing.T) {
	d := NewRenameDetector()

	beforeFuncs := map[string]*FuncInfo{
		"oldFunc": {Name: "oldFunc", Body: ""}, // Empty body
	}
	afterFuncs := map[string]*FuncInfo{
		"newFunc": {Name: "newFunc", Body: "{ return 1; }"},
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	if len(signals) != 0 {
		t.Errorf("expected no rename for empty body, got %d", len(signals))
	}
}

func TestDetectRenames_FalsePositivePrevention(t *testing.T) {
	d := NewRenameDetector()

	// Two functions with very different bodies shouldn't match
	beforeFuncs := map[string]*FuncInfo{
		"handleLogin": {
			Name: "handleLogin",
			Body: `{
				const user = await authenticate(username, password);
				if (!user) throw new Error('Invalid credentials');
				return generateToken(user);
			}`,
		},
	}
	afterFuncs := map[string]*FuncInfo{
		"processPayment": {
			Name: "processPayment",
			Body: `{
				const payment = new Payment(amount, currency);
				await payment.process();
				return payment.id;
			}`,
		},
	}

	signals := d.DetectRenames(beforeFuncs, afterFuncs, "test.js")

	if len(signals) != 0 {
		t.Errorf("expected no false positive rename, got %d signals", len(signals))
	}
}
