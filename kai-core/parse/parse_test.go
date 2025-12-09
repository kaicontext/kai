package parse

import (
	"testing"
)

func TestNewParser(t *testing.T) {
	parser := NewParser()
	if parser == nil {
		t.Fatal("NewParser returned nil")
	}
	if parser.jsParser == nil {
		t.Error("JavaScript parser not initialized")
	}
	if parser.pyParser == nil {
		t.Error("Python parser not initialized")
	}
}

func TestParser_ParseFunction(t *testing.T) {
	parser := NewParser()

	code := []byte(`
function hello(name) {
  return "Hello, " + name;
}
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) == 0 {
		t.Fatal("Expected at least one symbol")
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "hello" && sym.Kind == "function" {
			found = true
			if sym.Signature == "" {
				t.Error("expected function signature")
			}
			break
		}
	}

	if !found {
		t.Error("Expected to find function 'hello'")
	}
}

func TestParser_ParseClass(t *testing.T) {
	parser := NewParser()

	code := []byte(`
class User {
  constructor(name) {
    this.name = name;
  }

  greet() {
    return "Hello, " + this.name;
  }
}
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundClass := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			foundClass = true
		}
		if sym.Name == "User.greet" && sym.Kind == "function" {
			foundMethod = true
		}
	}

	if !foundClass {
		t.Error("Expected to find class 'User'")
	}

	if !foundMethod {
		t.Error("Expected to find method 'User.greet'")
	}
}

func TestParser_ParseVariables(t *testing.T) {
	parser := NewParser()

	code := []byte(`
const MAX_SIZE = 100;
let count = 0;
var name = "test";
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := map[string]bool{
		"MAX_SIZE": false,
		"count":    false,
		"name":     false,
	}

	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("Expected to find variable '%s'", name)
		}
	}
}

func TestParser_ParseArrowFunction(t *testing.T) {
	parser := NewParser()

	code := []byte(`
const add = (a, b) => a + b;
const multiply = (a, b) => {
  return a * b;
};
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundAdd := false
	foundMultiply := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "add" {
			foundAdd = true
			if sym.Kind != "function" {
				t.Errorf("expected 'add' to be function, got %s", sym.Kind)
			}
		}
		if sym.Name == "multiply" {
			foundMultiply = true
			if sym.Kind != "function" {
				t.Errorf("expected 'multiply' to be function, got %s", sym.Kind)
			}
		}
	}

	if !foundAdd {
		t.Error("Expected to find arrow function 'add'")
	}

	if !foundMultiply {
		t.Error("Expected to find arrow function 'multiply'")
	}
}

func TestRangesOverlap(t *testing.T) {
	tests := []struct {
		name     string
		r1       Range
		r2       Range
		expected bool
	}{
		{
			name:     "Same range",
			r1:       Range{Start: [2]int{1, 0}, End: [2]int{5, 10}},
			r2:       Range{Start: [2]int{1, 0}, End: [2]int{5, 10}},
			expected: true,
		},
		{
			name:     "r1 contains r2",
			r1:       Range{Start: [2]int{0, 0}, End: [2]int{10, 0}},
			r2:       Range{Start: [2]int{2, 0}, End: [2]int{5, 0}},
			expected: true,
		},
		{
			name:     "No overlap - r1 before r2",
			r1:       Range{Start: [2]int{0, 0}, End: [2]int{5, 0}},
			r2:       Range{Start: [2]int{6, 0}, End: [2]int{10, 0}},
			expected: false,
		},
		{
			name:     "No overlap - r2 before r1",
			r1:       Range{Start: [2]int{6, 0}, End: [2]int{10, 0}},
			r2:       Range{Start: [2]int{0, 0}, End: [2]int{5, 0}},
			expected: false,
		},
		{
			name:     "Partial overlap",
			r1:       Range{Start: [2]int{0, 0}, End: [2]int{5, 0}},
			r2:       Range{Start: [2]int{3, 0}, End: [2]int{8, 0}},
			expected: true,
		},
		{
			name:     "Same line different columns - overlap",
			r1:       Range{Start: [2]int{5, 0}, End: [2]int{5, 10}},
			r2:       Range{Start: [2]int{5, 5}, End: [2]int{5, 15}},
			expected: true,
		},
		{
			name:     "Same line different columns - no overlap",
			r1:       Range{Start: [2]int{5, 0}, End: [2]int{5, 5}},
			r2:       Range{Start: [2]int{5, 10}, End: [2]int{5, 15}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RangesOverlap(tt.r1, tt.r2)
			if result != tt.expected {
				t.Errorf("RangesOverlap(%v, %v) = %v, expected %v", tt.r1, tt.r2, result, tt.expected)
			}
		})
	}
}

func TestParsedFile_GetTree(t *testing.T) {
	parser := NewParser()
	code := []byte(`const x = 1;`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	tree := parsed.GetTree()
	if tree == nil {
		t.Error("expected non-nil tree")
	}
}

func TestParsedFile_GetRootNode(t *testing.T) {
	parser := NewParser()
	code := []byte(`const x = 1;`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	root := parsed.GetRootNode()
	if root == nil {
		t.Error("expected non-nil root node")
	}
	if root.Type() != "program" {
		t.Errorf("expected root type 'program', got %s", root.Type())
	}
}

func TestParsedFile_FindNodesOfType(t *testing.T) {
	parser := NewParser()
	code := []byte(`
function foo() {}
function bar() {}
const x = 1;
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcNodes := parsed.FindNodesOfType("function_declaration")
	if len(funcNodes) != 2 {
		t.Errorf("expected 2 function declarations, got %d", len(funcNodes))
	}

	constNodes := parsed.FindNodesOfType("lexical_declaration")
	if len(constNodes) != 1 {
		t.Errorf("expected 1 lexical declaration, got %d", len(constNodes))
	}

	notFound := parsed.FindNodesOfType("class_declaration")
	if len(notFound) != 0 {
		t.Errorf("expected 0 class declarations, got %d", len(notFound))
	}
}

func TestGetNodeRange(t *testing.T) {
	parser := NewParser()
	code := []byte(`function test() {}`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nodes := parsed.FindNodesOfType("function_declaration")
	if len(nodes) == 0 {
		t.Fatal("no function declarations found")
	}

	r := GetNodeRange(nodes[0])
	if r.Start[0] != 0 || r.Start[1] != 0 {
		t.Errorf("expected start [0,0], got %v", r.Start)
	}
	if r.End[0] != 0 || r.End[1] != 18 {
		t.Errorf("expected end [0,18], got %v", r.End)
	}
}

func TestGetNodeContent(t *testing.T) {
	parser := NewParser()
	code := []byte(`const name = "hello";`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nodes := parsed.FindNodesOfType("string")
	if len(nodes) == 0 {
		t.Fatal("no string nodes found")
	}

	content := GetNodeContent(nodes[0], code)
	if content != `"hello"` {
		t.Errorf("expected '\"hello\"', got %q", content)
	}
}

func TestParser_ParseNestedClass(t *testing.T) {
	parser := NewParser()

	code := []byte(`
class Outer {
  inner() {
    class Inner {
      method() {}
    }
  }
}
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundOuter := false
	foundInner := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Outer" && sym.Kind == "class" {
			foundOuter = true
		}
		if sym.Name == "Inner" && sym.Kind == "class" {
			foundInner = true
		}
	}

	if !foundOuter {
		t.Error("Expected to find class 'Outer'")
	}

	if !foundInner {
		t.Error("Expected to find class 'Inner'")
	}
}

func TestParser_ParseExportDefault(t *testing.T) {
	parser := NewParser()

	code := []byte(`
export default function main() {
  return "main";
}
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "main" && sym.Kind == "function" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find exported function 'main'")
	}
}

func TestParser_ParseEmptyFile(t *testing.T) {
	parser := NewParser()

	code := []byte(``)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParseComments(t *testing.T) {
	parser := NewParser()

	code := []byte(`
// This is a comment
function commented() {
  /* block comment */
}
`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "commented" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find function 'commented'")
	}
}

func TestParser_SymbolRange(t *testing.T) {
	parser := NewParser()

	code := []byte(`function test() {}`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) == 0 {
		t.Fatal("expected at least one symbol")
	}

	sym := parsed.Symbols[0]
	if sym.Range.Start[0] != 0 {
		t.Errorf("expected start line 0, got %d", sym.Range.Start[0])
	}
	if sym.Range.End[0] != 0 {
		t.Errorf("expected end line 0, got %d", sym.Range.End[0])
	}
}

func TestParser_FunctionExpression(t *testing.T) {
	parser := NewParser()

	code := []byte(`var handler = function() { return 1; };`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "handler" {
			found = true
			// Note: the parser treats function expressions as variables
			// with function kind when the value is detected as a function
			if sym.Kind != "function" && sym.Kind != "variable" {
				t.Errorf("expected kind 'function' or 'variable', got %q", sym.Kind)
			}
			break
		}
	}

	if !found {
		t.Error("Expected to find function expression 'handler'")
	}
}

func TestParser_MultipleVariablesOneLine(t *testing.T) {
	parser := NewParser()

	code := []byte(`const a = 1, b = 2, c = 3;`)

	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := map[string]bool{"a": false, "b": false, "c": false}
	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("Expected to find variable '%s'", name)
		}
	}
}

// ==================== Python Tests ====================

func TestParser_ParsePythonFunction(t *testing.T) {
	parser := NewParser()

	code := []byte(`
def hello(name):
    return f"Hello, {name}"
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) == 0 {
		t.Fatal("Expected at least one symbol")
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "hello" && sym.Kind == "function" {
			found = true
			if sym.Signature == "" {
				t.Error("expected function signature")
			}
			break
		}
	}

	if !found {
		t.Error("Expected to find function 'hello'")
	}
}

func TestParser_ParsePythonClass(t *testing.T) {
	parser := NewParser()

	code := []byte(`
class User:
    def __init__(self, name):
        self.name = name

    def greet(self):
        return f"Hello, {self.name}"
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundClass := false
	foundInit := false
	foundGreet := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			foundClass = true
		}
		if sym.Name == "User.__init__" && sym.Kind == "function" {
			foundInit = true
		}
		if sym.Name == "User.greet" && sym.Kind == "function" {
			foundGreet = true
		}
	}

	if !foundClass {
		t.Error("Expected to find class 'User'")
	}
	if !foundInit {
		t.Error("Expected to find method 'User.__init__'")
	}
	if !foundGreet {
		t.Error("Expected to find method 'User.greet'")
	}
}

func TestParser_ParsePythonVariables(t *testing.T) {
	parser := NewParser()

	code := []byte(`
MAX_SIZE = 100
name = "test"
config = {"key": "value"}
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := map[string]bool{
		"MAX_SIZE": false,
		"name":     false,
		"config":   false,
	}

	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}

	for varName, found := range expected {
		if !found {
			t.Errorf("Expected to find variable '%s'", varName)
		}
	}
}

func TestParser_ParsePythonDecorator(t *testing.T) {
	parser := NewParser()

	code := []byte(`
@staticmethod
def helper():
    pass

@property
def value(self):
    return self._value
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundHelper := false
	foundValue := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "helper" && sym.Kind == "function" {
			foundHelper = true
		}
		if sym.Name == "value" && sym.Kind == "function" {
			foundValue = true
		}
	}

	if !foundHelper {
		t.Error("Expected to find decorated function 'helper'")
	}
	if !foundValue {
		t.Error("Expected to find decorated function 'value'")
	}
}

func TestParser_ParsePythonAsync(t *testing.T) {
	parser := NewParser()

	code := []byte(`
async def fetch_data(url):
    return await http.get(url)
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "fetch_data" && sym.Kind == "function" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find async function 'fetch_data'")
	}
}

func TestParser_ParsePythonInheritance(t *testing.T) {
	parser := NewParser()

	code := []byte(`
class Admin(User):
    def promote(self):
        pass
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundClass := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Admin" && sym.Kind == "class" {
			foundClass = true
			if sym.Signature == "" {
				t.Error("expected class signature with inheritance")
			}
		}
		if sym.Name == "Admin.promote" && sym.Kind == "function" {
			foundMethod = true
		}
	}

	if !foundClass {
		t.Error("Expected to find class 'Admin'")
	}
	if !foundMethod {
		t.Error("Expected to find method 'Admin.promote'")
	}
}

func TestParser_ParsePythonEmptyFile(t *testing.T) {
	parser := NewParser()

	code := []byte(``)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParsePythonComments(t *testing.T) {
	parser := NewParser()

	code := []byte(`
# This is a comment
def commented():
    """Docstring comment"""
    pass
`)

	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "commented" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find function 'commented'")
	}
}

// ==================== Go Tests ====================

func TestParser_ParseGoFunction(t *testing.T) {
	parser := NewParser()

	code := []byte(`
package main

func Hello(name string) string {
	return "Hello, " + name
}
`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) == 0 {
		t.Fatal("Expected at least one symbol")
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Hello" && sym.Kind == "function" {
			found = true
			if sym.Signature == "" {
				t.Error("expected function signature")
			}
			break
		}
	}

	if !found {
		t.Error("Expected to find function 'Hello'")
	}
}

func TestParser_ParseGoStruct(t *testing.T) {
	parser := NewParser()

	code := []byte(`
package main

type User struct {
	Name  string
	Email string
}
`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find struct 'User'")
	}
}

func TestParser_ParseGoMethod(t *testing.T) {
	parser := NewParser()

	code := []byte(`
package main

type User struct {
	Name string
}

func (u *User) Greet() string {
	return "Hello, " + u.Name
}
`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundStruct := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			foundStruct = true
		}
		if sym.Name == "*User.Greet" && sym.Kind == "function" {
			foundMethod = true
		}
	}

	if !foundStruct {
		t.Error("Expected to find struct 'User'")
	}
	if !foundMethod {
		t.Error("Expected to find method '*User.Greet'")
	}
}

func TestParser_ParseGoInterface(t *testing.T) {
	parser := NewParser()

	code := []byte(`
package main

type Reader interface {
	Read(p []byte) (n int, err error)
}
`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Reader" && sym.Kind == "interface" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find interface 'Reader'")
	}
}

func TestParser_ParseGoVariables(t *testing.T) {
	parser := NewParser()

	code := []byte(`
package main

var MaxSize = 100
const Version = "1.0.0"
`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	foundVar := false
	foundConst := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "MaxSize" && sym.Kind == "variable" {
			foundVar = true
		}
		if sym.Name == "Version" && sym.Kind == "variable" {
			foundConst = true
		}
	}

	if !foundVar {
		t.Error("Expected to find variable 'MaxSize'")
	}
	if !foundConst {
		t.Error("Expected to find constant 'Version'")
	}
}

func TestParser_ParseGoEmptyFile(t *testing.T) {
	parser := NewParser()

	code := []byte(`package main`)

	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Package declaration doesn't create symbols
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for package-only file, got %d", len(parsed.Symbols))
	}
}
