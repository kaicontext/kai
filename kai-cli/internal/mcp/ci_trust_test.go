package mcp

import (
	"testing"

	"kai/internal/graph"
)

func TestValidAssertions_ContainsExpectedValues(t *testing.T) {
	expected := []string{"tests-pass", "types-ok", "lints-clean", "manual-verified"}
	if len(graph.ValidAssertions) != len(expected) {
		t.Fatalf("expected %d assertions, got %d", len(expected), len(graph.ValidAssertions))
	}
	for i, v := range expected {
		if graph.ValidAssertions[i] != v {
			t.Errorf("expected ValidAssertions[%d] = %q, got %q", i, v, graph.ValidAssertions[i])
		}
	}
}

func TestEdgeHasCIRun_Exists(t *testing.T) {
	// Verify the edge type constant is defined and has the expected value
	if graph.EdgeHasCIRun != "HAS_CI_RUN" {
		t.Errorf("expected EdgeHasCIRun = %q, got %q", "HAS_CI_RUN", graph.EdgeHasCIRun)
	}
}
