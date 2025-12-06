package modulematch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchPath(t *testing.T) {
	modules := []ModuleRule{
		{Name: "App", Paths: []string{"src/app.js"}},
		{Name: "Utils", Paths: []string{"src/utils/**"}},
		{Name: "Tests", Paths: []string{"tests/**/*.test.js"}},
	}
	matcher := NewMatcher(modules)

	tests := []struct {
		path     string
		expected []string
	}{
		{"src/app.js", []string{"App"}},
		{"src/utils/math.js", []string{"Utils"}},
		{"src/utils/helpers/format.js", []string{"Utils"}},
		{"tests/app.test.js", []string{"Tests"}},
		{"tests/unit/math.test.js", []string{"Tests"}},
		{"src/server.js", nil},
		{"random.txt", nil},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := matcher.MatchPath(tc.path)
			if len(result) != len(tc.expected) {
				t.Errorf("MatchPath(%q) = %v, want %v", tc.path, result, tc.expected)
				return
			}
			for i, v := range result {
				if v != tc.expected[i] {
					t.Errorf("MatchPath(%q) = %v, want %v", tc.path, result, tc.expected)
					return
				}
			}
		})
	}
}

func TestMatchPaths(t *testing.T) {
	modules := []ModuleRule{
		{Name: "App", Paths: []string{"src/app.js"}},
		{Name: "Utils", Paths: []string{"src/utils/**"}},
	}
	matcher := NewMatcher(modules)

	paths := []string{
		"src/app.js",
		"src/utils/math.js",
		"src/utils/format.js",
		"src/server.js",
	}

	result := matcher.MatchPaths(paths)

	if len(result["App"]) != 1 || result["App"][0] != "src/app.js" {
		t.Errorf("App module should match src/app.js, got %v", result["App"])
	}

	if len(result["Utils"]) != 2 {
		t.Errorf("Utils module should match 2 files, got %v", result["Utils"])
	}
}

func TestAddModule(t *testing.T) {
	matcher := NewMatcher(nil)

	matcher.AddModule("App", []string{"src/app.js"})
	if len(matcher.modules) != 1 {
		t.Errorf("Expected 1 module, got %d", len(matcher.modules))
	}

	// Update existing module
	matcher.AddModule("App", []string{"src/app.js", "src/main.js"})
	if len(matcher.modules) != 1 {
		t.Errorf("Expected 1 module after update, got %d", len(matcher.modules))
	}
	if len(matcher.modules[0].Paths) != 2 {
		t.Errorf("Expected 2 paths after update, got %d", len(matcher.modules[0].Paths))
	}

	// Add new module
	matcher.AddModule("Utils", []string{"src/utils/**"})
	if len(matcher.modules) != 2 {
		t.Errorf("Expected 2 modules, got %d", len(matcher.modules))
	}
}

func TestRemoveModule(t *testing.T) {
	modules := []ModuleRule{
		{Name: "App", Paths: []string{"src/app.js"}},
		{Name: "Utils", Paths: []string{"src/utils/**"}},
	}
	matcher := NewMatcher(modules)

	removed := matcher.RemoveModule("App")
	if !removed {
		t.Error("RemoveModule should return true for existing module")
	}
	if len(matcher.modules) != 1 {
		t.Errorf("Expected 1 module after removal, got %d", len(matcher.modules))
	}

	removed = matcher.RemoveModule("NonExistent")
	if removed {
		t.Error("RemoveModule should return false for non-existent module")
	}
}

func TestGetModule(t *testing.T) {
	modules := []ModuleRule{
		{Name: "App", Paths: []string{"src/app.js"}},
	}
	matcher := NewMatcher(modules)

	mod := matcher.GetModule("App")
	if mod == nil {
		t.Error("GetModule should return module for existing name")
	}
	if mod.Name != "App" {
		t.Errorf("Expected module name 'App', got %q", mod.Name)
	}

	mod = matcher.GetModule("NonExistent")
	if mod != nil {
		t.Error("GetModule should return nil for non-existent module")
	}
}

func TestLoadRulesOrEmpty(t *testing.T) {
	// Test with non-existent file
	matcher, err := LoadRulesOrEmpty("/non/existent/path/modules.yaml")
	if err != nil {
		t.Errorf("LoadRulesOrEmpty should not error for non-existent file: %v", err)
	}
	if matcher == nil {
		t.Error("LoadRulesOrEmpty should return non-nil matcher")
	}
	if len(matcher.modules) != 0 {
		t.Errorf("LoadRulesOrEmpty should return empty matcher, got %d modules", len(matcher.modules))
	}
}

func TestSaveAndLoadRules(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "modulematch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rulesPath := filepath.Join(tmpDir, "rules", "modules.yaml")

	// Create and save matcher
	modules := []ModuleRule{
		{Name: "App", Paths: []string{"src/app.js"}},
		{Name: "Utils", Paths: []string{"src/utils/**"}},
	}
	matcher := NewMatcher(modules)

	err = matcher.SaveRules(rulesPath)
	if err != nil {
		t.Fatalf("SaveRules failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadRules(rulesPath)
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	if len(loaded.modules) != 2 {
		t.Errorf("Expected 2 modules, got %d", len(loaded.modules))
	}

	// Verify matching still works
	if result := loaded.MatchPath("src/app.js"); len(result) != 1 || result[0] != "App" {
		t.Errorf("Loaded matcher should match src/app.js to App, got %v", result)
	}
}

func TestDoublestarPatterns(t *testing.T) {
	modules := []ModuleRule{
		{Name: "AllJS", Paths: []string{"**/*.js"}},
		{Name: "SrcOnly", Paths: []string{"src/**"}},
		{Name: "DeepNested", Paths: []string{"src/components/**/*.tsx"}},
	}
	matcher := NewMatcher(modules)

	tests := []struct {
		path     string
		expected []string
	}{
		{"app.js", []string{"AllJS"}},
		{"src/app.js", []string{"AllJS", "SrcOnly"}},
		{"src/utils/math.js", []string{"AllJS", "SrcOnly"}},
		{"src/components/Button.tsx", []string{"SrcOnly", "DeepNested"}},
		{"src/components/ui/Modal.tsx", []string{"SrcOnly", "DeepNested"}},
		{"lib/helper.js", []string{"AllJS"}},
		{"README.md", nil},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := matcher.MatchPath(tc.path)
			if len(result) != len(tc.expected) {
				t.Errorf("MatchPath(%q) = %v, want %v", tc.path, result, tc.expected)
				return
			}
			// Check all expected are present (order may vary)
			for _, exp := range tc.expected {
				found := false
				for _, r := range result {
					if r == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("MatchPath(%q) = %v, missing %q", tc.path, result, exp)
				}
			}
		})
	}
}
