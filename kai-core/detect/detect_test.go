package detect

import (
	"testing"

	"kai-core/graph"
	"kai-core/parse"
)

func TestNewDetector(t *testing.T) {
	d := NewDetector()
	if d == nil {
		t.Fatal("NewDetector returned nil")
	}
	if d.parser == nil {
		t.Error("parser not initialized")
	}
	if d.symbols == nil {
		t.Error("symbols map not initialized")
	}
}

func TestSetSymbols(t *testing.T) {
	d := NewDetector()
	symbols := []*graph.Node{
		{ID: []byte{1, 2, 3}, Kind: "symbol"},
	}
	d.SetSymbols("file1", symbols)

	if got := d.symbols["file1"]; got == nil {
		t.Error("expected symbols to be set for file1")
	}
	if len(d.symbols["file1"]) != 1 {
		t.Errorf("expected 1 symbol, got %d", len(d.symbols["file1"]))
	}
}

func TestDetectChanges_FunctionAdded(t *testing.T) {
	d := NewDetector()

	before := []byte(`
function existing() {
  return 1;
}
`)
	after := []byte(`
function existing() {
  return 1;
}

function newFunc() {
  return 2;
}
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundFuncAdded := false
	for _, c := range changes {
		if c.Category == FunctionAdded {
			foundFuncAdded = true
			if len(c.Evidence.Symbols) == 0 {
				t.Error("expected symbols in evidence")
			}
			// Check that function name is in symbols
			foundName := false
			for _, sym := range c.Evidence.Symbols {
				if sym == "name:newFunc" {
					foundName = true
					break
				}
			}
			if !foundName {
				t.Error("expected 'name:newFunc' in symbols")
			}
		}
	}

	if !foundFuncAdded {
		t.Error("expected FUNCTION_ADDED change")
	}
}

func TestDetectChanges_FunctionRemoved(t *testing.T) {
	d := NewDetector()

	before := []byte(`
function existing() {
  return 1;
}

function toBeRemoved() {
  return 2;
}
`)
	after := []byte(`
function existing() {
  return 1;
}
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundFuncRemoved := false
	for _, c := range changes {
		if c.Category == FunctionRemoved {
			foundFuncRemoved = true
			foundName := false
			for _, sym := range c.Evidence.Symbols {
				if sym == "name:toBeRemoved" {
					foundName = true
					break
				}
			}
			if !foundName {
				t.Error("expected 'name:toBeRemoved' in symbols")
			}
		}
	}

	if !foundFuncRemoved {
		t.Error("expected FUNCTION_REMOVED change")
	}
}

func TestDetectChanges_ArrowFunction(t *testing.T) {
	d := NewDetector()

	before := []byte(`const existing = () => 1;`)
	after := []byte(`
const existing = () => 1;
const newArrow = (a, b) => a + b;
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundFuncAdded := false
	for _, c := range changes {
		if c.Category == FunctionAdded {
			foundFuncAdded = true
		}
	}

	if !foundFuncAdded {
		t.Error("expected FUNCTION_ADDED for arrow function")
	}
}

func TestDetectChanges_ConditionChanged(t *testing.T) {
	d := NewDetector()

	before := []byte(`
function check(x) {
  if (x > 5) {
    return true;
  }
}
`)
	after := []byte(`
function check(x) {
  if (x > 10) {
    return true;
  }
}
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundConditionChanged := false
	for _, c := range changes {
		if c.Category == ConditionChanged {
			foundConditionChanged = true
		}
	}

	if !foundConditionChanged {
		t.Error("expected CONDITION_CHANGED change")
	}
}

func TestDetectChanges_ConstantUpdated(t *testing.T) {
	d := NewDetector()

	before := []byte(`const MAX = 100;`)
	after := []byte(`const MAX = 200;`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundConstUpdated := false
	for _, c := range changes {
		if c.Category == ConstantUpdated {
			foundConstUpdated = true
		}
	}

	if !foundConstUpdated {
		t.Error("expected CONSTANT_UPDATED change")
	}
}

func TestDetectChanges_APISurfaceChanged(t *testing.T) {
	d := NewDetector()

	before := []byte(`function api(a) { return a; }`)
	after := []byte(`function api(a, b) { return a + b; }`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED change")
	}
}

func TestDetectChanges_NoChanges(t *testing.T) {
	d := NewDetector()

	code := []byte(`function same() { return 1; }`)

	changes, err := d.DetectChanges("test.js", code, code, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestNewFileChange(t *testing.T) {
	change := NewFileChange(FileAdded, "new-file.js")

	if change.Category != FileAdded {
		t.Errorf("expected FileAdded, got %s", change.Category)
	}
	if len(change.Evidence.FileRanges) != 1 {
		t.Errorf("expected 1 file range, got %d", len(change.Evidence.FileRanges))
	}
	if change.Evidence.FileRanges[0].Path != "new-file.js" {
		t.Errorf("expected path 'new-file.js', got '%s'", change.Evidence.FileRanges[0].Path)
	}
}

func TestIsParseable(t *testing.T) {
	tests := []struct {
		lang     string
		expected bool
	}{
		{"ts", true},
		{"js", true},
		{"json", true},
		{"py", true},
		{"yaml", true},
		{"go", true},
		{"rb", true},
		{"rust", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := IsParseable(tt.lang)
			if got != tt.expected {
				t.Errorf("IsParseable(%q) = %v, expected %v", tt.lang, got, tt.expected)
			}
		})
	}
}

func TestGetCategoryPayload(t *testing.T) {
	ct := &ChangeType{
		Category: FunctionAdded,
		Evidence: Evidence{
			FileRanges: []FileRange{{
				Path:  "test.js",
				Start: [2]int{1, 0},
				End:   [2]int{5, 1},
			}},
			Symbols: []string{"name:foo", "abc123"},
		},
	}

	payload := GetCategoryPayload(ct)

	if payload["category"] != "FUNCTION_ADDED" {
		t.Errorf("expected category FUNCTION_ADDED, got %v", payload["category"])
	}

	evidence, ok := payload["evidence"].(map[string]interface{})
	if !ok {
		t.Fatal("evidence not a map")
	}

	fileRanges, ok := evidence["fileRanges"].([]interface{})
	if !ok {
		t.Fatal("fileRanges not a slice")
	}
	if len(fileRanges) != 1 {
		t.Errorf("expected 1 file range, got %d", len(fileRanges))
	}

	symbols, ok := evidence["symbols"].([]interface{})
	if !ok {
		t.Fatal("symbols not a slice")
	}
	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-1, 1},
	}

	for _, tt := range tests {
		got := abs(tt.input)
		if got != tt.expected {
			t.Errorf("abs(%d) = %d, expected %d", tt.input, got, tt.expected)
		}
	}
}

func TestEqualStringSlices(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{"both empty", []string{}, []string{}, true},
		{"equal", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different values", []string{"a", "b"}, []string{"a", "c"}, false},
		{"nil vs empty", nil, []string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := equalStringSlices(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("equalStringSlices(%v, %v) = %v, expected %v", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestGetAllFunctions_JavaScript(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`
function regular() {}
const arrow = () => {};
var funcExpr = function() {};

class MyClass {
  method() {}
}
`)

	parsed, err := parser.Parse(content, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content)

	expectedFuncs := []string{"regular", "arrow", "funcExpr", "method"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetAllFunctions_TypeScript(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`function greet(): void {}

class User {
  login(): void {}
}
`)

	parsed, err := parser.Parse(content, "ts")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content, "ts")

	expectedFuncs := []string{"greet", "login"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetAllFunctions_Go(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`package main

func greet() {}

type User struct{}

func (u *User) login() {}
`)

	parsed, err := parser.Parse(content, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content, "go")

	expectedFuncs := []string{"greet", "User.login"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetAllFunctions_Python(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`def greet():
    pass

class User:
    def login(self):
        pass
`)

	parsed, err := parser.Parse(content, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content, "py")

	expectedFuncs := []string{"greet", "User.login"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetAllFunctions_Ruby(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`def greet
end

class User
  def login
  end
end
`)

	parsed, err := parser.Parse(content, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content, "rb")

	expectedFuncs := []string{"greet", "User#login"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetAllFunctions_Rust(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`fn greet() {}

struct User {}

impl User {
    fn login(&self) {}
}
`)

	parsed, err := parser.Parse(content, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs := GetAllFunctions(parsed, content, "rs")

	expectedFuncs := []string{"greet", "User::login"}
	for _, expected := range expectedFuncs {
		if _, ok := funcs[expected]; !ok {
			t.Errorf("expected function %q not found", expected)
		}
	}
}

func TestGetArrowFunctionName(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`const myArrow = (x) => x * 2;`)

	parsed, err := parser.Parse(content, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nodes := parsed.FindNodesOfType("lexical_declaration")
	if len(nodes) == 0 {
		t.Fatal("no lexical declarations found")
	}

	name, arrowNode := getArrowFunctionName(nodes[0], content)
	if name != "myArrow" {
		t.Errorf("expected name 'myArrow', got %q", name)
	}
	if arrowNode == nil {
		t.Error("expected arrow node to be non-nil")
	}
}

func TestGetFunctionName(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`function testFunc(a, b) { return a + b; }`)

	parsed, err := parser.Parse(content, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nodes := parsed.FindNodesOfType("function_declaration")
	if len(nodes) == 0 {
		t.Fatal("no function declarations found")
	}

	name := getFunctionName(nodes[0], content)
	if name != "testFunc" {
		t.Errorf("expected name 'testFunc', got %q", name)
	}
}

func TestGetFunctionParams(t *testing.T) {
	parser := parse.NewParser()
	content := []byte(`function testFunc(a, b, c) { return a + b + c; }`)

	parsed, err := parser.Parse(content, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nodes := parsed.FindNodesOfType("function_declaration")
	if len(nodes) == 0 {
		t.Fatal("no function declarations found")
	}

	params := getFunctionParams(nodes[0], content)
	if params != "(a, b, c)" {
		t.Errorf("expected params '(a, b, c)', got %q", params)
	}
}

func TestDetectFileChange(t *testing.T) {
	d := NewDetector()
	change := d.DetectFileChange("readme.md", "md")

	if change.Category != FileContentChanged {
		t.Errorf("expected FileContentChanged, got %s", change.Category)
	}
	if change.Evidence.FileRanges[0].Path != "readme.md" {
		t.Errorf("expected path 'readme.md', got %s", change.Evidence.FileRanges[0].Path)
	}
}

func TestFindOverlappingSymbols(t *testing.T) {
	d := NewDetector()

	// Set up symbols with proper payload format
	symbols := []*graph.Node{
		{
			ID:   []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			Kind: "symbol",
			Payload: map[string]interface{}{
				"range": map[string]interface{}{
					"start": []interface{}{float64(1), float64(0)},
					"end":   []interface{}{float64(5), float64(10)},
				},
			},
		},
		{
			ID:   []byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
			Kind: "symbol",
			Payload: map[string]interface{}{
				"range": map[string]interface{}{
					"start": []interface{}{float64(10), float64(0)},
					"end":   []interface{}{float64(15), float64(10)},
				},
			},
		},
	}
	d.SetSymbols("file1", symbols)

	// Test overlapping range
	r := parse.Range{Start: [2]int{2, 0}, End: [2]int{4, 0}}
	result := d.findOverlappingSymbols("file1", r)

	if len(result) != 1 {
		t.Errorf("expected 1 overlapping symbol, got %d", len(result))
	}

	// Test non-overlapping range
	r2 := parse.Range{Start: [2]int{20, 0}, End: [2]int{25, 0}}
	result2 := d.findOverlappingSymbols("file1", r2)

	if len(result2) != 0 {
		t.Errorf("expected 0 overlapping symbols, got %d", len(result2))
	}
}

func TestDetectChanges_ExportChanged(t *testing.T) {
	d := NewDetector()

	before := []byte(`
function foo() { return 1; }
export { foo };
`)
	after := []byte(`
function foo() { return 1; }
function bar() { return 2; }
export { foo, bar };
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED for export change")
	}
}

func TestDetectChanges_MethodDefinition(t *testing.T) {
	d := NewDetector()

	before := []byte(`
class Foo {
  bar() { return 1; }
}
`)
	after := []byte(`
class Foo {
  bar() { return 1; }
  baz() { return 2; }
}
`)

	changes, err := d.DetectChanges("test.js", before, after, "file1", "js")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundFuncAdded := false
	for _, c := range changes {
		if c.Category == FunctionAdded {
			foundFuncAdded = true
		}
	}

	if !foundFuncAdded {
		t.Error("expected FUNCTION_ADDED for new method")
	}
}

func TestDetectChanges_ExportChanged_Go(t *testing.T) {
	d := NewDetector()

	before := []byte(`package main

func Login() {}      // Public (exported)
func logout() {}     // Private (not exported)
type User struct{}   // Public (exported)
`)

	after := []byte(`package main

func Login() {}      // Public (exported)
func Register() {}   // New public function
func logout() {}     // Private (not exported)
type User struct{}   // Public (exported)
`)

	changes, err := d.DetectChanges("test.go", before, after, "file1", "go")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when public Go function added")
	}
}

func TestDetectChanges_ExportChanged_Go_PrivateChange(t *testing.T) {
	d := NewDetector()

	before := []byte(`package main

func Login() {}
func logout() {}
`)

	after := []byte(`package main

func Login() {}
func logout() {}
func internal() {}  // Added private function
`)

	changes, err := d.DetectChanges("test.go", before, after, "file1", "go")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if foundAPIChanged {
		t.Error("should NOT trigger API_SURFACE_CHANGED for private function")
	}
}

func TestDetectChanges_ExportChanged_Rust(t *testing.T) {
	d := NewDetector()

	before := []byte(`pub fn login() {}
fn logout() {}
pub struct User {}
`)

	after := []byte(`pub fn login() {}
pub fn register() {}  // New public function
fn logout() {}
pub struct User {}
`)

	changes, err := d.DetectChanges("test.rs", before, after, "file1", "rs")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when pub Rust function added")
	}
}

func TestDetectChanges_ExportChanged_Rust_PrivateChange(t *testing.T) {
	d := NewDetector()

	before := []byte(`pub fn login() {}
fn logout() {}
`)

	after := []byte(`pub fn login() {}
fn logout() {}
fn internal() {}  // Added private function
`)

	changes, err := d.DetectChanges("test.rs", before, after, "file1", "rs")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if foundAPIChanged {
		t.Error("should NOT trigger API_SURFACE_CHANGED for private Rust function")
	}
}

func TestDetectChanges_ExportChanged_Python_All(t *testing.T) {
	d := NewDetector()

	before := []byte(`def login():
    pass

def _internal():
    pass

__all__ = ["login"]
`)

	after := []byte(`def login():
    pass

def register():
    pass

def _internal():
    pass

__all__ = ["login", "register"]
`)

	changes, err := d.DetectChanges("test.py", before, after, "file1", "py")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when __all__ exports change")
	}
}

func TestDetectChanges_ExportChanged_Python_NoAll(t *testing.T) {
	d := NewDetector()

	before := []byte(`def login():
    pass

def _internal():
    pass
`)

	after := []byte(`def login():
    pass

def register():
    pass

def _internal():
    pass
`)

	changes, err := d.DetectChanges("test.py", before, after, "file1", "py")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when public Python function added (no __all__)")
	}
}

func TestDetectChanges_ExportChanged_Ruby(t *testing.T) {
	d := NewDetector()

	before := []byte(`class User
  def login
  end
end
`)

	after := []byte(`class User
  def login
  end
end

class Admin
  def manage
  end
end
`)

	changes, err := d.DetectChanges("test.rb", before, after, "file1", "rb")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when Ruby class added")
	}
}

func TestDetectChanges_ExportChanged_TypeScript(t *testing.T) {
	d := NewDetector()

	before := []byte(`function login(): void {}

export { login };
`)

	after := []byte(`function login(): void {}
function register(): void {}

export { login, register };
`)

	changes, err := d.DetectChanges("test.ts", before, after, "file1", "ts")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	foundAPIChanged := false
	for _, c := range changes {
		if c.Category == APISurfaceChanged {
			foundAPIChanged = true
		}
	}

	if !foundAPIChanged {
		t.Error("expected API_SURFACE_CHANGED when TypeScript export added")
	}
}
