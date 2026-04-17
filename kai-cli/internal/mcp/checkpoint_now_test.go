package mcp

import (
	"fmt"
	"testing"

	"kai/internal/graph"
)

func TestValidateAssert_ValidValues(t *testing.T) {
	for _, a := range graph.ValidAssertions {
		if !isValidAssertion(a) {
			t.Errorf("expected %q to be valid", a)
		}
	}
}

func TestValidateAssert_InvalidValue(t *testing.T) {
	if isValidAssertion("bogus") {
		t.Error("expected 'bogus' to be invalid")
	}
	if isValidAssertion("") {
		t.Error("expected empty string to be invalid")
	}
}

func TestValidateAssert_PlanHashRules(t *testing.T) {
	// plan_hash without assert → error
	err := validateCheckpointNowParams("", "abc123")
	if err == nil {
		t.Error("expected error for plan_hash without assert")
	}

	// plan_hash with assert != tests-pass → error
	err = validateCheckpointNowParams("types-ok", "abc123")
	if err == nil {
		t.Error("expected error for plan_hash with assert=types-ok")
	}

	// plan_hash with assert=tests-pass → ok
	err = validateCheckpointNowParams("tests-pass", "abc123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// assert=tests-pass without plan_hash → ok (optional)
	err = validateCheckpointNowParams("tests-pass", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// no assert, no plan_hash → ok
	err = validateCheckpointNowParams("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// isValidAssertion checks if a value is in ValidAssertions.
func isValidAssertion(val string) bool {
	if val == "" {
		return false
	}
	for _, a := range graph.ValidAssertions {
		if a == val {
			return true
		}
	}
	return false
}

// validateCheckpointNowParams mirrors the validation logic in handleCheckpointNow.
func validateCheckpointNowParams(assert, planHash string) error {
	if assert != "" && !isValidAssertion(assert) {
		return fmt.Errorf("invalid assert value %q", assert)
	}
	if planHash != "" && assert == "" {
		return fmt.Errorf("plan_hash requires assert field")
	}
	if planHash != "" && assert != "tests-pass" {
		return fmt.Errorf("plan_hash only valid with assert=tests-pass")
	}
	return nil
}
