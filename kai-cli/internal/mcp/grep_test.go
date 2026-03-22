package mcp

import (
	"regexp"
	"testing"
)

func TestNormalizeLang(t *testing.T) {
	tests := []struct{ in, want string }{
		{"go", "go"}, {"golang", "go"}, {"Go", "go"},
		{"js", "js"}, {"javascript", "js"},
		{"ts", "ts"}, {"typescript", "ts"},
		{"py", "python"}, {"python", "python"},
		{"rb", "ruby"}, {"ruby", "ruby"},
		{"rs", "rust"}, {"rust", "rust"},
		{"yaml", "yaml"}, {"", ""},
	}
	for _, tt := range tests {
		if got := normalizeLang(tt.in); got != tt.want {
			t.Errorf("normalizeLang(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveNodeTypes_Alias(t *testing.T) {
	// "string" alias for Go should resolve to the two Go string literal types
	types := resolveNodeTypes("string", "go")
	if !types["interpreted_string_literal"] || !types["raw_string_literal"] {
		t.Errorf("expected Go string types, got %v", types)
	}
	if len(types) != 2 {
		t.Errorf("expected 2 types for Go string, got %d", len(types))
	}

	// "comment" alias for rust
	types = resolveNodeTypes("comment", "rust")
	if !types["line_comment"] || !types["block_comment"] {
		t.Errorf("expected Rust comment types, got %v", types)
	}

	// "identifier" alias for Go
	types = resolveNodeTypes("identifier", "golang")
	if !types["identifier"] || !types["field_identifier"] || !types["type_identifier"] {
		t.Errorf("expected Go identifier types, got %v", types)
	}
}

func TestResolveNodeTypes_RawType(t *testing.T) {
	types := resolveNodeTypes("function_declaration", "go")
	if !types["function_declaration"] || len(types) != 1 {
		t.Errorf("expected raw type passthrough, got %v", types)
	}
}

func TestResolveNodeTypes_UnknownLang(t *testing.T) {
	// Unknown language with a known alias should return all types across languages
	types := resolveNodeTypes("string", "yaml")
	if len(types) == 0 {
		t.Error("expected fallback types for unknown language")
	}
	// Should contain types from multiple languages
	if !types["interpreted_string_literal"] || !types["string"] {
		t.Errorf("expected cross-language types, got %v", types)
	}
}

func TestGrepRaw_Basic(t *testing.T) {
	content := []byte("line one\nfoo password here\nline three\ntoken = secret\n")
	re := regexp.MustCompile(`password|token`)

	matches := grepRaw(content, re, "test.txt", 100)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}

	if matches[0].File != "test.txt" || matches[0].Line != 2 || matches[0].Text != "foo password here" {
		t.Errorf("unexpected match[0]: %+v", matches[0])
	}
	if matches[1].Line != 4 || matches[1].Text != "token = secret" {
		t.Errorf("unexpected match[1]: %+v", matches[1])
	}
}

func TestGrepRaw_Remaining(t *testing.T) {
	content := []byte("match1\nmatch2\nmatch3\n")
	re := regexp.MustCompile(`match`)

	matches := grepRaw(content, re, "test.txt", 2)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches (capped by remaining), got %d", len(matches))
	}
}

func TestGrepRaw_LongLine(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	content := append([]byte("secret "), long...)
	re := regexp.MustCompile(`secret`)

	matches := grepRaw(content, re, "test.txt", 100)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if len(matches[0].Text) > maxLineLen+10 { // +10 for "..." suffix
		t.Errorf("line not truncated: len=%d", len(matches[0].Text))
	}
}

func TestGrepRaw_NoMatch(t *testing.T) {
	content := []byte("nothing here\n")
	re := regexp.MustCompile(`password`)
	matches := grepRaw(content, re, "test.txt", 100)
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}
