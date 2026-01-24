package intent

import (
	"strings"
	"testing"

	"kai-core/detect"
)

func TestMatchTemplate_HasCategory(t *testing.T) {
	template := &Template{
		ID: "test",
		Conditions: []MatchCondition{
			{Type: "has_category", Category: "FUNCTION_ADDED"},
		},
	}

	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionAdded},
		},
	}

	if !MatchTemplate(template, cluster) {
		t.Error("expected template to match cluster with FUNCTION_ADDED")
	}

	clusterNoMatch := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionRemoved},
		},
	}

	if MatchTemplate(template, clusterNoMatch) {
		t.Error("expected template to not match cluster without FUNCTION_ADDED")
	}
}

func TestMatchTemplate_CategoryCount(t *testing.T) {
	template := &Template{
		ID: "single_add",
		Conditions: []MatchCondition{
			{Type: "category_count", Category: "FUNCTION_ADDED", Comparator: "eq", Value: 1},
		},
	}

	singleAdd := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionAdded},
		},
	}
	if !MatchTemplate(template, singleAdd) {
		t.Error("expected template to match cluster with exactly 1 FUNCTION_ADDED")
	}

	multiAdd := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionAdded},
			{Category: detect.FunctionAdded},
		},
	}
	if MatchTemplate(template, multiAdd) {
		t.Error("expected template to not match cluster with 2 FUNCTION_ADDED")
	}
}

func TestMatchTemplate_CategoryCount_Comparators(t *testing.T) {
	tests := []struct {
		comparator string
		value      int
		count      int
		expected   bool
	}{
		{"eq", 2, 2, true},
		{"eq", 2, 3, false},
		{"gt", 2, 3, true},
		{"gt", 2, 2, false},
		{"lt", 2, 1, true},
		{"lt", 2, 2, false},
		{"gte", 2, 2, true},
		{"gte", 2, 1, false},
		{"lte", 2, 2, true},
		{"lte", 2, 3, false},
	}

	for _, tc := range tests {
		template := &Template{
			Conditions: []MatchCondition{
				{Type: "category_count", Category: "FUNCTION_ADDED", Comparator: tc.comparator, Value: tc.value},
			},
		}

		signals := make([]*detect.ChangeSignal, tc.count)
		for i := 0; i < tc.count; i++ {
			signals[i] = &detect.ChangeSignal{Category: detect.FunctionAdded}
		}
		cluster := &ChangeCluster{Signals: signals}

		result := MatchTemplate(template, cluster)
		if result != tc.expected {
			t.Errorf("comparator %s, value %d, count %d: expected %v, got %v",
				tc.comparator, tc.value, tc.count, tc.expected, result)
		}
	}
}

func TestMatchTemplate_HasTag(t *testing.T) {
	template := &Template{
		Conditions: []MatchCondition{
			{Type: "has_tag", Value: "breaking"},
		},
	}

	clusterWithTag := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Tags: []string{"breaking", "api"}},
		},
	}
	if !MatchTemplate(template, clusterWithTag) {
		t.Error("expected template to match cluster with 'breaking' tag")
	}

	clusterWithoutTag := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Tags: []string{"api"}},
		},
	}
	if MatchTemplate(template, clusterWithoutTag) {
		t.Error("expected template to not match cluster without 'breaking' tag")
	}
}

func TestMatchTemplate_ClusterType(t *testing.T) {
	template := &Template{
		Conditions: []MatchCondition{
			{Type: "cluster_type", Value: "test"},
		},
	}

	testCluster := &ChangeCluster{ClusterType: ClusterTypeTest}
	if !MatchTemplate(template, testCluster) {
		t.Error("expected template to match test cluster")
	}

	featureCluster := &ChangeCluster{ClusterType: ClusterTypeFeature}
	if MatchTemplate(template, featureCluster) {
		t.Error("expected template to not match feature cluster")
	}
}

func TestMatchTemplate_Fallback(t *testing.T) {
	template := &Template{
		ID:         "generic",
		Conditions: []MatchCondition{}, // Empty conditions = always match
	}

	anyCluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FileContentChanged},
		},
	}

	if !MatchTemplate(template, anyCluster) {
		t.Error("expected fallback template to match any cluster")
	}
}

func TestRenderTemplate_Basic(t *testing.T) {
	pattern := "Add {functionName} in {module}"
	vars := &TemplateVariables{
		FunctionName: "handleClick",
		Module:       "Auth",
	}

	result := RenderTemplate(pattern, vars)

	if result != "Add handleClick in Auth" {
		t.Errorf("expected 'Add handleClick in Auth', got %q", result)
	}
}

func TestRenderTemplate_Rename(t *testing.T) {
	pattern := "Rename {oldName} to {newName} in {module}"
	vars := &TemplateVariables{
		OldName: "oldFunc",
		NewName: "newFunc",
		Module:  "Utils",
	}

	result := RenderTemplate(pattern, vars)

	if result != "Rename oldFunc to newFunc in Utils" {
		t.Errorf("expected 'Rename oldFunc to newFunc in Utils', got %q", result)
	}
}

func TestRenderTemplate_MissingVariable(t *testing.T) {
	pattern := "Update {area} in {module}"
	vars := &TemplateVariables{
		Area: "utils",
		// Module is empty
	}

	result := RenderTemplate(pattern, vars)

	// Should clean up the unreplaced {module}
	if strings.Contains(result, "{module}") {
		t.Errorf("expected unreplaced variable to be removed, got %q", result)
	}
}

func TestRenderTemplate_CleanupDoubleSpaces(t *testing.T) {
	pattern := "Update {area} {extra} in {module}"
	vars := &TemplateVariables{
		Area:   "utils",
		Module: "General",
		// extra is not defined, will be empty
	}

	result := RenderTemplate(pattern, vars)

	if strings.Contains(result, "  ") {
		t.Errorf("expected double spaces to be cleaned up, got %q", result)
	}
}

func TestExtractVariables_FunctionNames(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{
				Category: detect.FunctionAdded,
				Evidence: detect.ExtendedEvidence{
					Symbols: []string{"name:handleClick", "name:handleSubmit"},
				},
			},
		},
		Modules: []string{"UI"},
	}

	vars := ExtractVariables(cluster, []string{})

	if vars.FunctionName != "handleClick" {
		t.Errorf("expected FunctionName 'handleClick', got %q", vars.FunctionName)
	}
	if vars.Module != "UI" {
		t.Errorf("expected Module 'UI', got %q", vars.Module)
	}
}

func TestExtractVariables_RenameInfo(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{
				Category: detect.FunctionRenamed,
				Evidence: detect.ExtendedEvidence{
					OldName: "oldFunc",
					NewName: "newFunc",
				},
			},
		},
	}

	vars := ExtractVariables(cluster, []string{"General"})

	if vars.OldName != "oldFunc" {
		t.Errorf("expected OldName 'oldFunc', got %q", vars.OldName)
	}
	if vars.NewName != "newFunc" {
		t.Errorf("expected NewName 'newFunc', got %q", vars.NewName)
	}
}

func TestExtractVariables_DependencyInfo(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{
				Category: detect.DependencyUpdated,
				Evidence: detect.ExtendedEvidence{
					OldName:     "react",
					NewName:     "react",
					BeforeValue: "17.0.0",
					AfterValue:  "18.0.0",
				},
			},
		},
	}

	vars := ExtractVariables(cluster, []string{})

	if vars.DependencyName != "react" {
		t.Errorf("expected DependencyName 'react', got %q", vars.DependencyName)
	}
	if vars.Version != "18.0.0" {
		t.Errorf("expected Version '18.0.0', got %q", vars.Version)
	}
}

func TestExtractBaseName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"src/components/Button.tsx", "Button"},
		{"lib/utils.js", "utils"},
		{"file.go", "file"},
		{"path/to/index.ts", "index"},
	}

	for _, tc := range tests {
		result := extractBaseName(tc.path)
		if result != tc.expected {
			t.Errorf("extractBaseName(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}

func TestFormatFunctionList(t *testing.T) {
	tests := []struct {
		names    []string
		expected string
	}{
		{[]string{}, ""},
		{[]string{"foo"}, "foo"},
		{[]string{"foo", "bar"}, "foo and bar"},
		{[]string{"foo", "bar", "baz"}, "foo, bar and others"},
		{[]string{"a", "b", "c", "d"}, "a, b and others"},
	}

	for _, tc := range tests {
		result := formatFunctionList(tc.names)
		if result != tc.expected {
			t.Errorf("formatFunctionList(%v) = %q, expected %q", tc.names, result, tc.expected)
		}
	}
}

func TestDefaultTemplates_Priority(t *testing.T) {
	// Verify templates are defined with decreasing priority
	highPriorityIDs := []string{"rename_function", "add_single_function", "remove_single_function"}

	for _, id := range highPriorityIDs {
		found := false
		for _, tmpl := range DefaultTemplates {
			if tmpl.ID == id {
				found = true
				if tmpl.Priority < 90 {
					t.Errorf("template %q should have high priority, got %d", id, tmpl.Priority)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected template %q to be defined", id)
		}
	}
}

func TestDefaultTemplates_FallbackExists(t *testing.T) {
	found := false
	for _, tmpl := range DefaultTemplates {
		if tmpl.ID == "generic_update" && len(tmpl.Conditions) == 0 {
			found = true
			if tmpl.Priority != 0 {
				t.Errorf("fallback template should have priority 0, got %d", tmpl.Priority)
			}
			break
		}
	}
	if !found {
		t.Error("expected generic_update fallback template to be defined")
	}
}

func TestExtractVariables_ConfigFieldTimeout(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{
				Category: detect.TimeoutChanged,
				Evidence: detect.ExtendedEvidence{
					Symbols: []string{"auth.session.timeout"},
				},
			},
		},
	}

	vars := ExtractVariables(cluster, []string{"Auth"})
	if vars.ConfigField != "auth session" {
		t.Errorf("expected config field 'auth session', got %q", vars.ConfigField)
	}
}
