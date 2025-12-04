package intent

import (
	"testing"

	"kai-core/detect"
	"kai-core/graph"
)

func TestGenerateIntent_FunctionAdded(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:handleClick"},
			},
		},
	}

	result := GenerateIntent(changeTypes, []string{"UI"}, nil, nil)
	if result != "Add handleClick in UI" {
		t.Errorf("expected 'Add handleClick in UI', got %q", result)
	}
}

func TestGenerateIntent_FunctionRemoved(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionRemoved,
			Evidence: detect.Evidence{
				Symbols: []string{"name:deprecatedFunc"},
			},
		},
	}

	result := GenerateIntent(changeTypes, []string{"Core"}, nil, nil)
	if result != "Remove deprecatedFunc in Core" {
		t.Errorf("expected 'Remove deprecatedFunc in Core', got %q", result)
	}
}

func TestGenerateIntent_Refactor(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:newFunc"},
			},
		},
		{
			Category: detect.FunctionRemoved,
			Evidence: detect.Evidence{
				Symbols: []string{"name:oldFunc"},
			},
		},
	}

	result := GenerateIntent(changeTypes, []string{"Utils"}, nil, nil)
	if result != "Refactor newFunc and oldFunc in Utils" {
		t.Errorf("expected 'Refactor newFunc and oldFunc in Utils', got %q", result)
	}
}

func TestGenerateIntent_MultipleFunctions(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:func1"},
			},
		},
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:func2"},
			},
		},
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:func3"},
			},
		},
	}

	result := GenerateIntent(changeTypes, []string{"API"}, nil, nil)
	if result != "Add func1, func2 and others in API" {
		t.Errorf("expected 'Add func1, func2 and others in API', got %q", result)
	}
}

func TestGenerateIntent_APIChange(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.APISurfaceChanged,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Server"}, nil, []string{"routes.js"})
	if result != "Update Server routes" {
		t.Errorf("expected 'Update Server routes', got %q", result)
	}
}

func TestGenerateIntent_ConditionChanged(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.ConditionChanged,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Auth"}, nil, []string{"login.js"})
	if result != "Modify Auth login" {
		t.Errorf("expected 'Modify Auth login', got %q", result)
	}
}

func TestGenerateIntent_ConstantUpdated(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.ConstantUpdated,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Config"}, nil, []string{"config.js"})
	if result != "Update Config config" {
		t.Errorf("expected 'Update Config config', got %q", result)
	}
}

func TestGenerateIntent_FileAdded(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FileAdded,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Tests"}, nil, []string{"test.spec.js"})
	if result != "Add Tests test.spec" {
		t.Errorf("expected 'Add Tests test.spec', got %q", result)
	}
}

func TestGenerateIntent_FileDeleted(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FileDeleted,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Legacy"}, nil, []string{"old.js"})
	if result != "Remove Legacy old" {
		t.Errorf("expected 'Remove Legacy old', got %q", result)
	}
}

func TestGenerateIntent_JSONChange(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.JSONFieldAdded,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Config"}, nil, []string{"package.json"})
	if result != "Update Config package" {
		t.Errorf("expected 'Update Config package', got %q", result)
	}
}

func TestGenerateIntent_JSONValueChanged(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.JSONValueChanged,
		},
	}

	result := GenerateIntent(changeTypes, []string{"Settings"}, nil, []string{"settings.json"})
	if result != "Modify Settings settings" {
		t.Errorf("expected 'Modify Settings settings', got %q", result)
	}
}

func TestGenerateIntent_NoModule(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FileContentChanged,
		},
	}

	result := GenerateIntent(changeTypes, nil, nil, []string{"readme.md"})
	if result != "Update General readme" {
		t.Errorf("expected 'Update General readme', got %q", result)
	}
}

func TestGenerateIntent_NoChanges(t *testing.T) {
	result := GenerateIntent(nil, nil, nil, nil)
	if result != "Change General codebase" {
		t.Errorf("expected 'Change General codebase', got %q", result)
	}
}

func TestDetermineVerb(t *testing.T) {
	tests := []struct {
		name        string
		changeTypes []*detect.ChangeType
		expected    string
	}{
		{
			name: "function added",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FunctionAdded},
			},
			expected: "Add",
		},
		{
			name: "function removed",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FunctionRemoved},
			},
			expected: "Remove",
		},
		{
			name: "both added and removed",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FunctionAdded},
				{Category: detect.FunctionRemoved},
			},
			expected: "Refactor",
		},
		{
			name: "API change",
			changeTypes: []*detect.ChangeType{
				{Category: detect.APISurfaceChanged},
			},
			expected: "Update",
		},
		{
			name: "condition change",
			changeTypes: []*detect.ChangeType{
				{Category: detect.ConditionChanged},
			},
			expected: "Modify",
		},
		{
			name: "constant update",
			changeTypes: []*detect.ChangeType{
				{Category: detect.ConstantUpdated},
			},
			expected: "Update",
		},
		{
			name: "file added",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FileAdded},
			},
			expected: "Add",
		},
		{
			name: "file deleted",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FileDeleted},
			},
			expected: "Remove",
		},
		{
			name: "JSON field added",
			changeTypes: []*detect.ChangeType{
				{Category: detect.JSONFieldAdded},
			},
			expected: "Update",
		},
		{
			name: "JSON value changed",
			changeTypes: []*detect.ChangeType{
				{Category: detect.JSONValueChanged},
			},
			expected: "Modify",
		},
		{
			name: "file content changed",
			changeTypes: []*detect.ChangeType{
				{Category: detect.FileContentChanged},
			},
			expected: "Update",
		},
		{
			name:        "no changes",
			changeTypes: nil,
			expected:    "Change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineVerb(tt.changeTypes)
			if result != tt.expected {
				t.Errorf("determineVerb() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestExtractFunctionNames(t *testing.T) {
	changeTypes := []*detect.ChangeType{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.Evidence{
				Symbols: []string{"name:foo", "hexid123"},
			},
		},
		{
			Category: detect.FunctionRemoved,
			Evidence: detect.Evidence{
				Symbols: []string{"name:bar"},
			},
		},
		{
			Category: detect.ConditionChanged,
			Evidence: detect.Evidence{
				Symbols: []string{"name:ignored"},
			},
		},
	}

	names := extractFunctionNames(changeTypes)
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}

	expected := map[string]bool{"foo": false, "bar": false}
	for _, name := range names {
		expected[name] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected to find name %q", name)
		}
	}
}

func TestFormatFunctionNames(t *testing.T) {
	tests := []struct {
		names    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"foo"}, "foo"},
		{[]string{"foo", "bar"}, "foo and bar"},
		{[]string{"foo", "bar", "baz"}, "foo, bar and others"},
		{[]string{"a", "b", "c", "d"}, "a, b and others"},
	}

	for _, tt := range tests {
		result := formatFunctionNames(tt.names)
		if result != tt.expected {
			t.Errorf("formatFunctionNames(%v) = %q, expected %q", tt.names, result, tt.expected)
		}
	}
}

func TestDetermineArea_WithSymbols(t *testing.T) {
	symbols := []*graph.Node{
		{
			Payload: map[string]interface{}{
				"fqName": "MyClass.myMethod",
			},
		},
	}

	area := determineArea(symbols, nil)
	if area != "myMethod" {
		t.Errorf("expected 'myMethod', got %q", area)
	}
}

func TestDetermineArea_WithChangedFiles(t *testing.T) {
	area := determineArea(nil, []string{"src/utils/helper.ts"})
	if area != "helper" {
		t.Errorf("expected 'helper', got %q", area)
	}
}

func TestDetermineArea_MultipleFiles(t *testing.T) {
	files := []string{
		"src/components/button.tsx",
		"src/components/input.tsx",
	}
	area := determineArea(nil, files)
	if area != "components" {
		t.Errorf("expected 'components', got %q", area)
	}
}

func TestDetermineArea_NoContext(t *testing.T) {
	area := determineArea(nil, nil)
	if area != "codebase" {
		t.Errorf("expected 'codebase', got %q", area)
	}
}

func TestGetCommonArea(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name:     "empty",
			paths:    nil,
			expected: "codebase",
		},
		{
			name:     "single file",
			paths:    []string{"src/utils.ts"},
			expected: "utils",
		},
		{
			name:     "common directory",
			paths:    []string{"src/api/routes.ts", "src/api/handlers.ts"},
			expected: "api",
		},
		{
			name:     "root level",
			paths:    []string{"foo.ts", "bar.ts"},
			expected: "codebase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCommonArea(tt.paths)
			if result != tt.expected {
				t.Errorf("getCommonArea(%v) = %q, expected %q", tt.paths, result, tt.expected)
			}
		})
	}
}

func TestPayloadToChangeType(t *testing.T) {
	payload := map[string]interface{}{
		"category": "FUNCTION_ADDED",
		"evidence": map[string]interface{}{
			"fileRanges": []interface{}{
				map[string]interface{}{
					"path":  "test.js",
					"start": []interface{}{float64(1), float64(0)},
					"end":   []interface{}{float64(10), float64(5)},
				},
			},
			"symbols": []interface{}{"name:foo", "abc123"},
		},
	}

	ct := PayloadToChangeType(payload)
	if ct == nil {
		t.Fatal("expected non-nil ChangeType")
	}

	if ct.Category != detect.FunctionAdded {
		t.Errorf("expected FunctionAdded, got %s", ct.Category)
	}

	if len(ct.Evidence.FileRanges) != 1 {
		t.Errorf("expected 1 file range, got %d", len(ct.Evidence.FileRanges))
	}

	fr := ct.Evidence.FileRanges[0]
	if fr.Path != "test.js" {
		t.Errorf("expected path 'test.js', got %q", fr.Path)
	}
	if fr.Start[0] != 1 || fr.Start[1] != 0 {
		t.Errorf("expected start [1,0], got %v", fr.Start)
	}
	if fr.End[0] != 10 || fr.End[1] != 5 {
		t.Errorf("expected end [10,5], got %v", fr.End)
	}

	if len(ct.Evidence.Symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(ct.Evidence.Symbols))
	}
}

func TestPayloadToChangeType_InvalidCategory(t *testing.T) {
	payload := map[string]interface{}{
		"notCategory": "FUNCTION_ADDED",
	}

	ct := PayloadToChangeType(payload)
	if ct != nil {
		t.Error("expected nil for missing category")
	}
}

func TestPayloadToChangeType_EmptyEvidence(t *testing.T) {
	payload := map[string]interface{}{
		"category": "FILE_ADDED",
	}

	ct := PayloadToChangeType(payload)
	if ct == nil {
		t.Fatal("expected non-nil ChangeType")
	}

	if ct.Category != detect.FileAdded {
		t.Errorf("expected FileAdded, got %s", ct.Category)
	}

	if len(ct.Evidence.FileRanges) != 0 {
		t.Error("expected empty file ranges")
	}
	if len(ct.Evidence.Symbols) != 0 {
		t.Error("expected empty symbols")
	}
}
