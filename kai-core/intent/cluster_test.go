package intent

import (
	"testing"

	"kai-core/detect"
)

func TestNewClusterer(t *testing.T) {
	c := NewClusterer()
	if c == nil {
		t.Fatal("NewClusterer returned nil")
	}
	if c.CallGraph == nil {
		t.Error("CallGraph not initialized")
	}
	if c.Modules == nil {
		t.Error("Modules not initialized")
	}
}

func TestClusterChanges_SingleSignal(t *testing.T) {
	c := NewClusterer()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/foo.js"}},
				Symbols:    []string{"name:foo"},
			},
			Weight:     0.8,
			Confidence: 1.0,
		},
	}

	clusters := c.ClusterChanges(signals, []string{"Frontend"})

	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}

	cluster := clusters[0]
	if len(cluster.Signals) != 1 {
		t.Errorf("expected 1 signal in cluster, got %d", len(cluster.Signals))
	}
	if cluster.Modules[0] != "Frontend" {
		t.Errorf("expected module 'Frontend', got %q", cluster.Modules[0])
	}
}

func TestClusterChanges_MultipleModules(t *testing.T) {
	c := NewClusterer()
	c.SetModules(map[string]string{
		"src/auth/login.js": "Auth",
		"src/ui/button.js":  "UI",
	})

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/auth/login.js"}},
			},
			Weight: 0.8,
		},
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/ui/button.js"}},
			},
			Weight: 0.8,
		},
	}

	clusters := c.ClusterChanges(signals, []string{})

	// Should create separate clusters for different modules
	if len(clusters) < 1 {
		t.Fatalf("expected at least 1 cluster, got %d", len(clusters))
	}

	// Check that modules are correctly assigned
	foundAuth := false
	foundUI := false
	for _, cluster := range clusters {
		for _, mod := range cluster.Modules {
			if mod == "Auth" {
				foundAuth = true
			}
			if mod == "UI" {
				foundUI = true
			}
		}
	}
	if !foundAuth || !foundUI {
		t.Errorf("expected both Auth and UI modules in clusters")
	}
}

func TestClusterChanges_SameDirectory(t *testing.T) {
	c := NewClusterer()

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/utils/helpers.js"}},
			},
		},
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/utils/format.js"}},
			},
		},
	}

	clusters := c.ClusterChanges(signals, []string{"General"})

	// Files in same directory should be clustered together
	if len(clusters) != 1 {
		t.Errorf("expected files in same directory to be in 1 cluster, got %d", len(clusters))
	}
}

func TestClusterChanges_DependencyBased(t *testing.T) {
	c := NewClusterer()
	c.SetCallGraph(map[string][]string{
		"src/main.js":   {"src/helper.js"},
		"src/helper.js": {},
	})

	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/main.js"}},
			},
		},
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/helper.js"}},
			},
		},
	}

	clusters := c.ClusterChanges(signals, []string{"General"})

	// Files with import relationship should be clustered together
	if len(clusters) != 1 {
		t.Errorf("expected dependent files to be in 1 cluster, got %d", len(clusters))
	}
}

func TestClassifyCluster_Feature(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded, Tags: []string{}},
		{Category: detect.FunctionAdded, Tags: []string{}},
	}

	clusterType := classifyCluster(signals)

	if clusterType != ClusterTypeFeature {
		t.Errorf("expected cluster type 'feature', got %q", clusterType)
	}
}

func TestClassifyCluster_Refactor(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded, Tags: []string{}},
		{Category: detect.FunctionRemoved, Tags: []string{}},
	}

	clusterType := classifyCluster(signals)

	if clusterType != ClusterTypeRefactor {
		t.Errorf("expected cluster type 'refactor', got %q", clusterType)
	}
}

func TestClassifyCluster_Test(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionBodyChanged,
			Tags:     []string{"test"},
		},
	}

	clusterType := classifyCluster(signals)

	if clusterType != ClusterTypeTest {
		t.Errorf("expected cluster type 'test', got %q", clusterType)
	}
}

func TestClassifyCluster_Config(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{
			Category: detect.JSONValueChanged,
			Tags:     []string{"config"},
		},
	}

	clusterType := classifyCluster(signals)

	if clusterType != ClusterTypeConfig {
		t.Errorf("expected cluster type 'config', got %q", clusterType)
	}
}

func TestComputeCohesion_SingleSignal(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded, Confidence: 1.0},
	}

	cohesion := computeCohesion(signals)

	if cohesion != 1.0 {
		t.Errorf("expected cohesion 1.0 for single signal, got %f", cohesion)
	}
}

func TestComputeCohesion_SameCategory(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded, Confidence: 1.0, Evidence: detect.ExtendedEvidence{FileRanges: []detect.FileRange{{Path: "a.js"}}}},
		{Category: detect.FunctionAdded, Confidence: 1.0, Evidence: detect.ExtendedEvidence{FileRanges: []detect.FileRange{{Path: "a.js"}}}},
	}

	cohesion := computeCohesion(signals)

	// Same category, same file, high confidence should give high cohesion
	if cohesion < 0.7 {
		t.Errorf("expected high cohesion for same category/file, got %f", cohesion)
	}
}

func TestComputeCohesion_MixedCategories(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded, Confidence: 1.0, Evidence: detect.ExtendedEvidence{FileRanges: []detect.FileRange{{Path: "a.js"}}}},
		{Category: detect.ConditionChanged, Confidence: 1.0, Evidence: detect.ExtendedEvidence{FileRanges: []detect.FileRange{{Path: "b.js"}}}},
		{Category: detect.JSONValueChanged, Confidence: 0.5, Evidence: detect.ExtendedEvidence{FileRanges: []detect.FileRange{{Path: "c.json"}}}},
	}

	cohesion := computeCohesion(signals)

	// Different categories, different files, mixed confidence should give lower cohesion
	if cohesion > 0.6 {
		t.Errorf("expected lower cohesion for mixed categories, got %f", cohesion)
	}
}

func TestShouldForceMixed_CategoryDiversity(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.FunctionAdded},
		{Category: detect.DependencyUpdated},
		{Category: detect.SchemaFieldRemoved},
	}

	if !shouldForceMixed(signals) {
		t.Error("expected force mixed for category diversity")
	}
}

func TestShouldForceMixed_ConfigWithCode(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{Category: detect.TimeoutChanged},
		{Category: detect.FunctionAdded},
	}

	if !shouldForceMixed(signals) {
		t.Error("expected force mixed for config + code")
	}
}

func TestDeterminePrimaryArea_FunctionName(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{
			Category: detect.FunctionAdded,
			Evidence: detect.ExtendedEvidence{
				Symbols: []string{"name:handleSubmit"},
			},
		},
	}

	area := determinePrimaryArea(signals)

	if area != "handleSubmit" {
		t.Errorf("expected area 'handleSubmit', got %q", area)
	}
}

func TestDeterminePrimaryArea_FileBased(t *testing.T) {
	signals := []*detect.ChangeSignal{
		{
			Category: detect.FileContentChanged,
			Evidence: detect.ExtendedEvidence{
				FileRanges: []detect.FileRange{{Path: "src/components/Button.tsx"}},
			},
		},
	}

	area := determinePrimaryArea(signals)

	if area != "Button" {
		t.Errorf("expected area 'Button', got %q", area)
	}
}

func TestChangeCluster_HasCategory(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionAdded},
			{Category: detect.FunctionRemoved},
		},
	}

	if !cluster.HasCategory(detect.FunctionAdded) {
		t.Error("expected HasCategory to return true for FunctionAdded")
	}
	if !cluster.HasCategory(detect.FunctionRemoved) {
		t.Error("expected HasCategory to return true for FunctionRemoved")
	}
	if cluster.HasCategory(detect.ConditionChanged) {
		t.Error("expected HasCategory to return false for ConditionChanged")
	}
}

func TestChangeCluster_CategoryCount(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Category: detect.FunctionAdded},
			{Category: detect.FunctionAdded},
			{Category: detect.FunctionRemoved},
		},
	}

	if count := cluster.CategoryCount(detect.FunctionAdded); count != 2 {
		t.Errorf("expected count 2 for FunctionAdded, got %d", count)
	}
	if count := cluster.CategoryCount(detect.FunctionRemoved); count != 1 {
		t.Errorf("expected count 1 for FunctionRemoved, got %d", count)
	}
	if count := cluster.CategoryCount(detect.ConditionChanged); count != 0 {
		t.Errorf("expected count 0 for ConditionChanged, got %d", count)
	}
}

func TestChangeCluster_TotalWeight(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Weight: 0.5},
			{Weight: 0.3},
			{Weight: 0.2},
		},
	}

	weight := cluster.TotalWeight()
	if weight != 1.0 {
		t.Errorf("expected total weight 1.0, got %f", weight)
	}
}

func TestChangeCluster_AverageConfidence(t *testing.T) {
	cluster := &ChangeCluster{
		Signals: []*detect.ChangeSignal{
			{Confidence: 1.0},
			{Confidence: 0.8},
			{Confidence: 0.6},
		},
	}

	avg := cluster.AverageConfidence()
	expected := (1.0 + 0.8 + 0.6) / 3
	// Use approximate comparison for floating point
	diff := avg - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.0001 {
		t.Errorf("expected average confidence %f, got %f", expected, avg)
	}
}

func TestUnique(t *testing.T) {
	strs := []string{"a", "b", "a", "c", "b"}
	result := unique(strs)

	if len(result) != 3 {
		t.Errorf("expected 3 unique strings, got %d", len(result))
	}

	// Check order is preserved
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("expected order [a, b, c], got %v", result)
	}
}

func TestHasCommonModule(t *testing.T) {
	if !hasCommonModule([]string{"A", "B"}, []string{"B", "C"}) {
		t.Error("expected true when modules share 'B'")
	}
	if hasCommonModule([]string{"A", "B"}, []string{"C", "D"}) {
		t.Error("expected false when modules don't share any")
	}
}
