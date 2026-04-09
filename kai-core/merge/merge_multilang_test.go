package merge

import (
	"strings"
	"testing"
)

// --- Python merge tests ---

func TestMerge3Way_Python_NoConflict_DifferentFunctions(t *testing.T) {
	base := []byte(`def foo():
    return 1

def bar():
    return 2
`)
	left := []byte(`def foo():
    return 10

def bar():
    return 2
`)
	right := []byte(`def foo():
    return 1

def bar():
    return 20
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success (different functions changed), got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Python_Conflict_SameFunction(t *testing.T) {
	base := []byte(`def process(data):
    return data.strip()
`)
	left := []byte(`def process(data):
    return data.strip().lower()
`)
	right := []byte(`def process(data):
    return data.strip().upper()
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (same function changed differently)")
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected at least one conflict")
	}
}

func TestMerge3Way_Python_NoConflict_OnlyLeftChanged(t *testing.T) {
	base := []byte(`def greet(name):
    return f"Hello, {name}"
`)
	left := []byte(`def greet(name):
    return f"Hi, {name}!"
`)
	right := []byte(`def greet(name):
    return f"Hello, {name}"
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Python_FunctionAdded(t *testing.T) {
	base := []byte(`def existing():
    pass
`)
	left := []byte(`def existing():
    pass

def new_left():
    return "left"
`)
	right := []byte(`def existing():
    pass
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Python_ClassMethodConflict(t *testing.T) {
	base := []byte(`class Handler:
    def process(self):
        return None
`)
	left := []byte(`class Handler:
    def process(self):
        return "left"
`)
	right := []byte(`class Handler:
    def process(self):
        return "right"
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (class modified on both sides)")
	}
}

func TestMerge3Way_Python_DeleteVsModify(t *testing.T) {
	base := []byte(`def foo():
    return 1

def bar():
    return 2
`)
	left := []byte(`def foo():
    return 1
`)
	right := []byte(`def foo():
    return 1

def bar():
    return 20
`)
	result, err := Merge3Way(base, left, right, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (delete vs modify)")
	}
	found := false
	for _, c := range result.Conflicts {
		if c.Kind == ConflictDeleteVsModify {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DELETE_vs_MODIFY, got: %v", result.Conflicts)
	}
}

// --- Ruby merge tests ---

func TestMerge3Way_Ruby_NoConflict_DifferentMethods(t *testing.T) {
	base := []byte(`def foo
  1
end

def bar
  2
end
`)
	left := []byte(`def foo
  10
end

def bar
  2
end
`)
	right := []byte(`def foo
  1
end

def bar
  20
end
`)
	result, err := Merge3Way(base, left, right, "ruby")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Ruby_Conflict_SameMethod(t *testing.T) {
	base := []byte(`def process(data)
  data.strip
end
`)
	left := []byte(`def process(data)
  data.strip.downcase
end
`)
	right := []byte(`def process(data)
  data.strip.upcase
end
`)
	result, err := Merge3Way(base, left, right, "ruby")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict")
	}
}

func TestMerge3Way_Ruby_MethodAdded(t *testing.T) {
	base := []byte(`def existing
  true
end
`)
	left := []byte(`def existing
  true
end

def new_method
  "added"
end
`)
	right := []byte(`def existing
  true
end
`)
	result, err := Merge3Way(base, left, right, "ruby")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

// --- Rust merge tests ---

func TestMerge3Way_Rust_NoConflict_DifferentFunctions(t *testing.T) {
	base := []byte(`fn foo() -> i32 {
    1
}

fn bar() -> i32 {
    2
}
`)
	left := []byte(`fn foo() -> i32 {
    10
}

fn bar() -> i32 {
    2
}
`)
	right := []byte(`fn foo() -> i32 {
    1
}

fn bar() -> i32 {
    20
}
`)
	result, err := Merge3Way(base, left, right, "rust")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Rust_Conflict_SameFunction(t *testing.T) {
	base := []byte(`fn process(data: &str) -> String {
    data.trim().to_string()
}
`)
	left := []byte(`fn process(data: &str) -> String {
    data.trim().to_lowercase()
}
`)
	right := []byte(`fn process(data: &str) -> String {
    data.trim().to_uppercase()
}
`)
	result, err := Merge3Way(base, left, right, "rust")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict")
	}
}

func TestMerge3Way_Rust_FunctionAdded(t *testing.T) {
	base := []byte(`fn existing() -> bool {
    true
}
`)
	left := []byte(`fn existing() -> bool {
    true
}

fn new_fn() -> &'static str {
    "added"
}
`)
	right := []byte(`fn existing() -> bool {
    true
}
`)
	result, err := Merge3Way(base, left, right, "rust")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_Rust_SignatureConflict(t *testing.T) {
	base := []byte(`fn compute(x: i32) -> i32 {
    x * 2
}
`)
	left := []byte(`fn compute(x: i32, y: i32) -> i32 {
    x + y
}
`)
	right := []byte(`fn compute(x: i32, z: f64) -> f64 {
    x as f64 * z
}
`)
	result, err := Merge3Way(base, left, right, "rust")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (signature diverged)")
	}
}

// --- TypeScript merge tests ---

func TestMerge3Way_TS_NoConflict_DifferentFunctions(t *testing.T) {
	base := []byte(`function foo(): number {
  return 1;
}

function bar(): string {
  return "hello";
}
`)
	left := []byte(`function foo(): number {
  return 10;
}

function bar(): string {
  return "hello";
}
`)
	right := []byte(`function foo(): number {
  return 1;
}

function bar(): string {
  return "world";
}
`)
	result, err := Merge3Way(base, left, right, "ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
}

func TestMerge3Way_TS_SameFunction_Conflict(t *testing.T) {
	// Test TS with a clear function conflict instead of interface
	// (interfaces are treated as single units — no field-level merge yet)
	base := []byte(`function configure(): Config {
  return { timeout: 30 };
}
`)
	left := []byte(`function configure(): Config {
  return { timeout: 60, retries: 3 };
}
`)
	right := []byte(`function configure(): Config {
  return { timeout: 10, debug: true };
}
`)
	result, err := Merge3Way(base, left, right, "ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (same function body diverged)")
	}
}

// --- Multi-file merge tests ---

func TestMergeFiles_MultiFile_NoConflict(t *testing.T) {
	m := NewMerger()

	base := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 2; }`),
	}
	left := map[string][]byte{
		"a.js": []byte(`function a() { return 10; }`),
		"b.js": []byte(`function b() { return 2; }`),
	}
	right := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 20; }`),
	}

	result, err := m.MergeFiles(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
	if len(result.Files) != 2 {
		t.Errorf("expected 2 merged files, got %d", len(result.Files))
	}
}

func TestMergeFiles_MultiFile_OneConflict(t *testing.T) {
	m := NewMerger()

	base := map[string][]byte{
		"a.js":   []byte(`function a() { return 1; }`),
		"b.js":   []byte(`function b() { return 2; }`),
	}
	left := map[string][]byte{
		"a.js":   []byte(`function a() { return 10; }`),
		"b.js":   []byte(`function b() { return 20; }`),
	}
	right := map[string][]byte{
		"a.js":   []byte(`function a() { return 1; }`),
		"b.js":   []byte(`function b() { return 30; }`),
	}

	result, err := m.MergeFiles(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict on b.js")
	}
	// a.js should merge clean, b.js should conflict
	foundBConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c.UnitKey.File, "b.js") {
			foundBConflict = true
		}
	}
	if !foundBConflict {
		t.Error("expected conflict in b.js")
	}
}

func TestMergeFiles_FileAddedOnLeft(t *testing.T) {
	m := NewMerger()

	base := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
	}
	left := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"new.js": []byte(`function newFunc() { return "new"; }`),
	}
	right := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
	}

	result, err := m.MergeFiles(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got conflicts: %v", result.Conflicts)
	}
	if _, ok := result.Files["new.js"]; !ok {
		t.Error("expected new.js in merged files")
	}
}

func TestMergeFiles_FileDeletedOnLeft_UnmodifiedOnRight(t *testing.T) {
	m := NewMerger()

	base := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 2; }`),
	}
	left := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		// b.js deleted
	}
	right := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 2; }`), // unchanged
	}

	result, err := m.MergeFiles(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success (delete + no modify), got conflicts: %v", result.Conflicts)
	}
}

func TestMergeFiles_FileDeletedOnLeft_ModifiedOnRight(t *testing.T) {
	m := NewMerger()

	base := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 2; }`),
	}
	left := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		// b.js deleted
	}
	right := map[string][]byte{
		"a.js": []byte(`function a() { return 1; }`),
		"b.js": []byte(`function b() { return 20; }`), // modified
	}

	result, err := m.MergeFiles(base, left, right, "js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected conflict (delete vs modify)")
	}
}

// --- Extraction tests for all languages ---

func TestExtractUnits_Ruby(t *testing.T) {
	code := []byte(`
def foo
  1
end

class MyClass
  def bar
    "hello"
  end
end
`)
	extractor := NewExtractor()
	units, err := extractor.ExtractUnits("test.rb", code, "ruby")
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	if len(units.Units) == 0 {
		t.Error("expected units to be extracted")
	}
}

func TestExtractUnits_Rust(t *testing.T) {
	code := []byte(`
fn foo() -> i32 {
    1
}

struct Config {
    timeout: u64,
}

impl Config {
    fn new() -> Self {
        Config { timeout: 30 }
    }
}
`)
	extractor := NewExtractor()
	units, err := extractor.ExtractUnits("test.rs", code, "rust")
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	if len(units.Units) == 0 {
		t.Error("expected units to be extracted")
	}
}

func TestExtractUnits_TypeScript(t *testing.T) {
	code := []byte(`
function greet(name: string): string {
  return "Hello, " + name;
}

interface Config {
  timeout: number;
  retries: number;
}

const MAX_RETRIES = 3;

class Service {
  async fetch(url: string): Promise<Response> {
    return fetch(url);
  }
}
`)
	extractor := NewExtractor()
	units, err := extractor.ExtractUnits("test.ts", code, "ts")
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	if len(units.Units) == 0 {
		t.Error("expected units to be extracted")
	}

	foundGreet := false
	foundConst := false
	foundClass := false
	for _, u := range units.Units {
		if u.Name == "greet" {
			foundGreet = true
		}
		if u.Name == "MAX_RETRIES" {
			foundConst = true
		}
		if u.Name == "Service" {
			foundClass = true
		}
	}
	if !foundGreet {
		t.Error("expected to find function 'greet'")
	}
	if !foundConst {
		t.Error("expected to find const 'MAX_RETRIES'")
	}
	if !foundClass {
		t.Error("expected to find class 'Service'")
	}
}
