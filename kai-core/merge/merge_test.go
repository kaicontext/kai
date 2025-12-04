package merge

import (
	"testing"
)

func TestMerge3Way_NoConflict_LeftChanged(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)
	left := []byte(`function foo() {
  return 2;
}`)
	right := []byte(`function foo() {
  return 1;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}

	merged := result.Files["file"]
	if merged == nil {
		t.Fatal("expected merged content")
	}
}

func TestMerge3Way_NoConflict_RightChanged(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)
	left := []byte(`function foo() {
  return 1;
}`)
	right := []byte(`function foo() {
  return 2;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_NoConflict_BothChangedSame(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)
	left := []byte(`function foo() {
  return 2;
}`)
	right := []byte(`function foo() {
  return 2;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Conflict_BothChangedDifferently(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)
	left := []byte(`function foo() {
  return 2;
}`)
	right := []byte(`function foo() {
  return 3;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Success {
		t.Error("expected conflict, got success")
	}

	if len(result.Conflicts) == 0 {
		t.Error("expected at least one conflict")
	}
}

func TestMerge3Way_NoConflict_DifferentFunctions(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}

function bar() {
  return 2;
}`)

	left := []byte(`function foo() {
  return 10;
}

function bar() {
  return 2;
}`)

	right := []byte(`function foo() {
  return 1;
}

function bar() {
  return 20;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success (different functions changed), got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_FunctionAdded(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)

	left := []byte(`function foo() {
  return 1;
}

function newLeft() {
  return "left";
}`)

	right := []byte(`function foo() {
  return 1;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_FunctionDeleted_NoModify(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}

function toDelete() {
  return 2;
}`)

	left := []byte(`function foo() {
  return 1;
}`)

	right := []byte(`function foo() {
  return 1;
}

function toDelete() {
  return 2;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success (delete with no modify), got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Conflict_DeleteVsModify(t *testing.T) {
	base := []byte(`function foo() {
  return 1;
}`)

	left := []byte(``) // deleted

	right := []byte(`function foo() {
  return 2;
}`) // modified

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Success {
		t.Error("expected conflict (delete vs modify)")
	}

	foundConflict := false
	for _, c := range result.Conflicts {
		if c.Kind == ConflictDeleteVsModify {
			foundConflict = true
			break
		}
	}
	if !foundConflict {
		t.Error("expected DELETE_vs_MODIFY conflict")
	}
}

func TestMerge3Way_Conflict_SignatureDiverged(t *testing.T) {
	base := []byte(`function foo(a) {
  return a;
}`)

	left := []byte(`function foo(a, b) {
  return a + b;
}`)

	right := []byte(`function foo(a, c) {
  return a * c;
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Success {
		t.Error("expected conflict (signature diverged)")
	}

	foundConflict := false
	for _, c := range result.Conflicts {
		if c.Kind == ConflictAPISignatureDiverged {
			foundConflict = true
			break
		}
	}
	if !foundConflict {
		t.Errorf("expected API_SIGNATURE_DIVERGED conflict, got: %v", result.Conflicts)
	}
}

func TestMerge3Way_ConstConflict(t *testing.T) {
	base := []byte(`const TIMEOUT = 3600;`)
	left := []byte(`const TIMEOUT = 1800;`)
	right := []byte(`const TIMEOUT = 2700;`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Success {
		t.Error("expected conflict (const value diverged)")
	}

	foundConflict := false
	for _, c := range result.Conflicts {
		if c.Kind == ConflictConstValueConflict {
			foundConflict = true
			break
		}
	}
	if !foundConflict {
		t.Errorf("expected CONST_VALUE_CONFLICT, got: %v", result.Conflicts)
	}
}

func TestMerge3Way_ConcurrentCreate(t *testing.T) {
	base := []byte(`function existing() { return 1; }`)

	left := []byte(`function existing() { return 1; }

function newFunc() {
  return "left version";
}`)

	right := []byte(`function existing() { return 1; }

function newFunc() {
  return "right version";
}`)

	result, err := Merge3Way(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Success {
		t.Error("expected conflict (concurrent create)")
	}

	foundConflict := false
	for _, c := range result.Conflicts {
		if c.Kind == ConflictConcurrentCreate {
			foundConflict = true
			break
		}
	}
	if !foundConflict {
		t.Errorf("expected CONCURRENT_CREATE conflict, got: %v", result.Conflicts)
	}
}

func TestExtractUnits_JS(t *testing.T) {
	code := []byte(`
function foo() {
  return 1;
}

const bar = 42;

class MyClass {
  method() {
    return "hello";
  }
}
`)

	extractor := NewExtractor()
	units, err := extractor.ExtractUnits("test.js", code, "js")
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	if len(units.Units) == 0 {
		t.Error("expected units to be extracted")
	}

	// Check for function
	foundFoo := false
	for _, u := range units.Units {
		if u.Name == "foo" && u.Kind == UnitFunction {
			foundFoo = true
		}
	}
	if !foundFoo {
		t.Error("expected to find function 'foo'")
	}

	// Check for const
	foundBar := false
	for _, u := range units.Units {
		if u.Name == "bar" && u.Kind == UnitConst {
			foundBar = true
		}
	}
	if !foundBar {
		t.Error("expected to find const 'bar'")
	}

	// Check for class
	foundClass := false
	for _, u := range units.Units {
		if u.Name == "MyClass" && u.Kind == UnitClass {
			foundClass = true
		}
	}
	if !foundClass {
		t.Error("expected to find class 'MyClass'")
	}
}

func TestExtractUnits_Python(t *testing.T) {
	code := []byte(`
def foo():
    return 1

class MyClass:
    def method(self):
        return "hello"
`)

	extractor := NewExtractor()
	units, err := extractor.ExtractUnits("test.py", code, "py")
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	if len(units.Units) == 0 {
		t.Error("expected units to be extracted")
	}

	foundFoo := false
	foundClass := false
	for _, u := range units.Units {
		if u.Name == "foo" && u.Kind == UnitFunction {
			foundFoo = true
		}
		if u.Name == "MyClass" && u.Kind == UnitClass {
			foundClass = true
		}
	}

	if !foundFoo {
		t.Error("expected to find function 'foo'")
	}
	if !foundClass {
		t.Error("expected to find class 'MyClass'")
	}
}
