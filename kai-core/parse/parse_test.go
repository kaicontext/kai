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

func TestParser_ParseUnsupportedLanguage(t *testing.T) {
	parser := NewParser()

	code := []byte(`
public class Main {
	public static void main(String[] args) {
		System.out.println("Hello, World!");
	}
}
`)

	_, err := parser.Parse(code, "java")
	if err == nil {
		t.Fatal("Expected error for unsupported language 'java', got nil")
	}

	if err.Error() != "unsupported language: java" {
		t.Errorf("Expected error message to contain 'unsupported', got: %v", err)
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

// --- SQL tests ---

func TestSQL_CreateTable(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT UNIQUE
);
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(parsed.Symbols))
	}

	sym := parsed.Symbols[0]
	if sym.Name != "users" {
		t.Errorf("expected name 'users', got %q", sym.Name)
	}
	if sym.Kind != "class" {
		t.Errorf("expected kind 'class', got %q", sym.Kind)
	}
	if sym.Signature != "CREATE TABLE users (id, name, email)" {
		t.Errorf("unexpected signature: %q", sym.Signature)
	}
}

func TestSQL_CreateTableNoColumns(t *testing.T) {
	parser := NewParser()
	// Some SQL dialects allow CREATE TABLE ... AS SELECT
	code := []byte(`CREATE TABLE backup AS SELECT * FROM users;`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "backup" && sym.Kind == "class" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find table 'backup'")
	}
}

func TestSQL_CreateIndex(t *testing.T) {
	parser := NewParser()
	code := []byte(`CREATE INDEX idx_users_email ON users(email);`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(parsed.Symbols))
	}

	sym := parsed.Symbols[0]
	if sym.Name != "idx_users_email" {
		t.Errorf("expected name 'idx_users_email', got %q", sym.Name)
	}
	if sym.Kind != "variable" {
		t.Errorf("expected kind 'variable', got %q", sym.Kind)
	}
	if sym.Signature != "CREATE INDEX idx_users_email" {
		t.Errorf("unexpected signature: %q", sym.Signature)
	}
}

func TestSQL_CreateUniqueIndex(t *testing.T) {
	parser := NewParser()
	code := []byte(`CREATE UNIQUE INDEX idx_email_unique ON users(email);`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "idx_email_unique" && sym.Kind == "variable" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find index 'idx_email_unique', got symbols: %v", parsed.Symbols)
	}
}

func TestSQL_CreateView(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE VIEW active_users AS
  SELECT * FROM users WHERE active = true;
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(parsed.Symbols))
	}

	sym := parsed.Symbols[0]
	if sym.Name != "active_users" {
		t.Errorf("expected name 'active_users', got %q", sym.Name)
	}
	if sym.Kind != "class" {
		t.Errorf("expected kind 'class', got %q", sym.Kind)
	}
	if sym.Signature != "CREATE VIEW active_users" {
		t.Errorf("unexpected signature: %q", sym.Signature)
	}
}

func TestSQL_CreateFunction(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE FUNCTION get_user(user_id INT) RETURNS TABLE(id INT, name TEXT) AS $$
BEGIN
  RETURN QUERY SELECT id, name FROM users WHERE id = user_id;
END;
$$ LANGUAGE plpgsql;
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(parsed.Symbols))
	}

	sym := parsed.Symbols[0]
	if sym.Name != "get_user" {
		t.Errorf("expected name 'get_user', got %q", sym.Name)
	}
	if sym.Kind != "function" {
		t.Errorf("expected kind 'function', got %q", sym.Kind)
	}
	if sym.Signature != "CREATE FUNCTION get_user(user_id INT)" {
		t.Errorf("unexpected signature: %q", sym.Signature)
	}
}

func TestSQL_MultipleStatements(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE TABLE orders (
  id SERIAL PRIMARY KEY,
  user_id INT NOT NULL,
  total DECIMAL(10, 2)
);

CREATE INDEX idx_orders_user ON orders(user_id);

CREATE VIEW order_summary AS
  SELECT user_id, COUNT(*) as order_count FROM orders GROUP BY user_id;

CREATE FUNCTION total_orders(uid INT) RETURNS INT AS $$
  SELECT COUNT(*) FROM orders WHERE user_id = uid;
$$ LANGUAGE sql;
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expected := map[string]string{
		"orders":        "class",
		"idx_orders_user": "variable",
		"order_summary": "class",
		"total_orders":  "function",
	}

	found := make(map[string]bool)
	for _, sym := range parsed.Symbols {
		if expectedKind, ok := expected[sym.Name]; ok {
			if sym.Kind != expectedKind {
				t.Errorf("symbol %q: expected kind %q, got %q", sym.Name, expectedKind, sym.Kind)
			}
			found[sym.Name] = true
		}
	}

	for name := range expected {
		if !found[name] {
			t.Errorf("expected to find symbol %q", name)
		}
	}
}

func TestSQL_EmptyFile(t *testing.T) {
	parser := NewParser()
	parsed, err := parser.Parse([]byte(""), "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty SQL, got %d", len(parsed.Symbols))
	}
}

func TestSQL_SelectOnly(t *testing.T) {
	parser := NewParser()
	// Plain SELECT statements shouldn't produce symbols
	code := []byte(`SELECT * FROM users WHERE id = 1;`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for SELECT-only SQL, got %d", len(parsed.Symbols))
	}
}

func TestSQL_TableWithManyColumns(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE TABLE events (
  id BIGSERIAL PRIMARY KEY,
  event_name TEXT NOT NULL,
  timestamp TIMESTAMPTZ NOT NULL,
  install_id TEXT NOT NULL,
  version TEXT,
  os TEXT,
  arch TEXT,
  command TEXT,
  dur_ms BIGINT,
  result TEXT,
  error_class TEXT,
  stats JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(parsed.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(parsed.Symbols))
	}

	sym := parsed.Symbols[0]
	if sym.Name != "events" {
		t.Errorf("expected name 'events', got %q", sym.Name)
	}
	// Verify all columns are in the signature
	for _, col := range []string{"id", "event_name", "timestamp", "install_id", "version", "os", "arch", "command", "dur_ms", "result", "error_class", "stats", "created_at"} {
		if !contains(sym.Signature, col) {
			t.Errorf("expected column %q in signature: %q", col, sym.Signature)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ==================== Ruby Tests ====================

func TestParser_ParseRubyMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
def hello(name)
  "Hello, #{name}"
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
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
		t.Error("Expected to find method 'hello'")
	}
}

func TestParser_ParseRubyClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class User
  def initialize(name)
    @name = name
  end

  def greet
    "Hello, #{@name}"
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
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
		if sym.Name == "User#initialize" && sym.Kind == "function" {
			foundInit = true
		}
		if sym.Name == "User#greet" && sym.Kind == "function" {
			foundGreet = true
		}
	}
	if !foundClass {
		t.Error("Expected to find class 'User'")
	}
	if !foundInit {
		t.Error("Expected to find method 'User#initialize'")
	}
	if !foundGreet {
		t.Error("Expected to find method 'User#greet'")
	}
}

func TestParser_ParseRubyInheritance(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Admin < User
  def promote
    true
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundClass := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Admin" && sym.Kind == "class" {
			foundClass = true
			if sym.Signature != "class Admin < User" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
		}
		if sym.Name == "Admin#promote" && sym.Kind == "function" {
			foundMethod = true
		}
	}
	if !foundClass {
		t.Error("Expected to find class 'Admin'")
	}
	if !foundMethod {
		t.Error("Expected to find method 'Admin#promote'")
	}
}

func TestParser_ParseRubySingletonMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Config
  def self.load(path)
    new(path)
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Config.load" && sym.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find singleton method 'Config.load'")
	}
}

func TestParser_ParseRubyModule(t *testing.T) {
	parser := NewParser()
	code := []byte(`
module Greetable
  def greet
    "Hello!"
  end

  def farewell
    "Goodbye!"
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundModule := false
	foundGreet := false
	foundFarewell := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Greetable" && sym.Kind == "module" {
			foundModule = true
		}
		if sym.Name == "Greetable#greet" && sym.Kind == "function" {
			foundGreet = true
		}
		if sym.Name == "Greetable#farewell" && sym.Kind == "function" {
			foundFarewell = true
		}
	}
	if !foundModule {
		t.Error("Expected to find module 'Greetable'")
	}
	if !foundGreet {
		t.Error("Expected to find method 'Greetable#greet'")
	}
	if !foundFarewell {
		t.Error("Expected to find method 'Greetable#farewell'")
	}
}

func TestParser_ParseRubyConstant(t *testing.T) {
	parser := NewParser()
	code := []byte(`
MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundMax := false
	foundTimeout := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "MAX_RETRIES" {
			foundMax = true
		}
		if sym.Name == "DEFAULT_TIMEOUT" {
			foundTimeout = true
		}
	}
	if !foundMax {
		t.Error("Expected to find constant 'MAX_RETRIES'")
	}
	if !foundTimeout {
		t.Error("Expected to find constant 'DEFAULT_TIMEOUT'")
	}
}

func TestParser_ParseRubyEmptyFile(t *testing.T) {
	parser := NewParser()
	parsed, err := parser.Parse([]byte(""), "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParseRubyComments(t *testing.T) {
	parser := NewParser()
	code := []byte(`
# This is a comment
def commented
  # inline comment
  true
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "commented" && sym.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find method 'commented'")
	}
}

func TestParser_ParseRubyMultipleMethods(t *testing.T) {
	parser := NewParser()
	code := []byte(`
def foo; end
def bar(x); end
def baz(x, y); end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	expected := map[string]bool{"foo": false, "bar": false, "baz": false}
	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("Expected to find method '%s'", name)
		}
	}
}

// ==================== Rust Tests ====================

func TestParser_ParseRustFunction(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn hello(name: &str) -> String {
    format!("Hello, {}", name)
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
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

func TestParser_ParseRustStruct(t *testing.T) {
	parser := NewParser()
	code := []byte(`
struct User {
    name: String,
    email: String,
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			found = true
			if sym.Signature != "struct User" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find struct 'User'")
	}
}

func TestParser_ParseRustEnum(t *testing.T) {
	parser := NewParser()
	code := []byte(`
enum Status {
    Active,
    Inactive,
    Pending,
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Status" && sym.Kind == "class" {
			found = true
			if sym.Signature != "enum Status" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find enum 'Status'")
	}
}

func TestParser_ParseRustTrait(t *testing.T) {
	parser := NewParser()
	code := []byte(`
trait Greetable {
    fn greet(&self) -> String;
    fn farewell(&self) -> String;
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundTrait := false
	foundGreet := false
	foundFarewell := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Greetable" && sym.Kind == "type" {
			foundTrait = true
		}
		if sym.Name == "Greetable::greet" && sym.Kind == "function" {
			foundGreet = true
		}
		if sym.Name == "Greetable::farewell" && sym.Kind == "function" {
			foundFarewell = true
		}
	}
	if !foundTrait {
		t.Error("Expected to find trait 'Greetable'")
	}
	if !foundGreet {
		t.Error("Expected to find trait method 'Greetable::greet'")
	}
	if !foundFarewell {
		t.Error("Expected to find trait method 'Greetable::farewell'")
	}
}

func TestParser_ParseRustImpl(t *testing.T) {
	parser := NewParser()
	code := []byte(`
struct Counter {
    value: i32,
}

impl Counter {
    fn new() -> Self {
        Counter { value: 0 }
    }

    fn increment(&mut self) {
        self.value += 1;
    }
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundStruct := false
	foundNew := false
	foundIncrement := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Counter" && sym.Kind == "class" {
			foundStruct = true
		}
		if sym.Name == "Counter::new" && sym.Kind == "function" {
			foundNew = true
		}
		if sym.Name == "Counter::increment" && sym.Kind == "function" {
			foundIncrement = true
		}
	}
	if !foundStruct {
		t.Error("Expected to find struct 'Counter'")
	}
	if !foundNew {
		t.Error("Expected to find impl method 'Counter::new'")
	}
	if !foundIncrement {
		t.Error("Expected to find impl method 'Counter::increment'")
	}
}

func TestParser_ParseRustTypeAlias(t *testing.T) {
	parser := NewParser()
	code := []byte(`type Result<T> = std::result::Result<T, Error>;`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Result" && sym.Kind == "type" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find type alias 'Result'")
	}
}

func TestParser_ParseRustConstAndStatic(t *testing.T) {
	parser := NewParser()
	code := []byte(`
const MAX_SIZE: usize = 1024;
static GREETING: &str = "Hello";
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundConst := false
	foundStatic := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "MAX_SIZE" && sym.Kind == "variable" {
			foundConst = true
		}
		if sym.Name == "GREETING" && sym.Kind == "variable" {
			foundStatic = true
		}
	}
	if !foundConst {
		t.Error("Expected to find const 'MAX_SIZE'")
	}
	if !foundStatic {
		t.Error("Expected to find static 'GREETING'")
	}
}

func TestParser_ParseRustMod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
mod utils {
    pub fn helper() {}
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "utils" && sym.Kind == "module" {
			found = true
			if sym.Signature != "mod utils" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find mod 'utils'")
	}
}

func TestParser_ParseRustMacro(t *testing.T) {
	parser := NewParser()
	code := []byte(`
macro_rules! say_hello {
    () => { println!("Hello!"); };
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "say_hello!" && sym.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find macro 'say_hello!'")
	}
}

func TestParser_ParseRustEmptyFile(t *testing.T) {
	parser := NewParser()
	parsed, err := parser.Parse([]byte(""), "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParseRustMultipleFunctions(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn add(a: i32, b: i32) -> i32 { a + b }
fn subtract(a: i32, b: i32) -> i32 { a - b }
fn multiply(a: i32, b: i32) -> i32 { a * b }
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	expected := map[string]bool{"add": false, "subtract": false, "multiply": false}
	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("Expected to find function '%s'", name)
		}
	}
}

// ==================== PHP Tests ====================

func TestParser_ParsePHPFunction(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
function hello($name) {
    return "Hello, " . $name;
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
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

func TestParser_ParsePHPClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
class User {
    public function __construct($name) {
        $this->name = $name;
    }

    public function greet() {
        return "Hello, " . $this->name;
    }
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundClass := false
	foundConstruct := false
	foundGreet := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			foundClass = true
		}
		if sym.Name == "User::__construct" && sym.Kind == "function" {
			foundConstruct = true
		}
		if sym.Name == "User::greet" && sym.Kind == "function" {
			foundGreet = true
		}
	}
	if !foundClass {
		t.Error("Expected to find class 'User'")
	}
	if !foundConstruct {
		t.Error("Expected to find method 'User::__construct'")
	}
	if !foundGreet {
		t.Error("Expected to find method 'User::greet'")
	}
}

func TestParser_ParsePHPInheritance(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
class Admin extends User {
    public function promote() {
        return true;
    }
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundClass := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Admin" && sym.Kind == "class" {
			foundClass = true
			if sym.Signature != "class Admin extends User" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
		}
		if sym.Name == "Admin::promote" && sym.Kind == "function" {
			foundMethod = true
		}
	}
	if !foundClass {
		t.Error("Expected to find class 'Admin'")
	}
	if !foundMethod {
		t.Error("Expected to find method 'Admin::promote'")
	}
}

func TestParser_ParsePHPInterface(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
interface Authenticatable {
    public function login($user, $pass);
    public function logout();
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Authenticatable" && sym.Kind == "interface" {
			found = true
			if sym.Signature != "interface Authenticatable" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find interface 'Authenticatable'")
	}
}

func TestParser_ParsePHPTrait(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
trait Timestamps {
    public function createdAt() {
        return $this->created_at;
    }

    public function updatedAt() {
        return $this->updated_at;
    }
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundTrait := false
	foundCreated := false
	foundUpdated := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Timestamps" && sym.Kind == "class" {
			foundTrait = true
		}
		if sym.Name == "Timestamps::createdAt" && sym.Kind == "function" {
			foundCreated = true
		}
		if sym.Name == "Timestamps::updatedAt" && sym.Kind == "function" {
			foundUpdated = true
		}
	}
	if !foundTrait {
		t.Error("Expected to find trait 'Timestamps'")
	}
	if !foundCreated {
		t.Error("Expected to find method 'Timestamps::createdAt'")
	}
	if !foundUpdated {
		t.Error("Expected to find method 'Timestamps::updatedAt'")
	}
}

func TestParser_ParsePHPNamespace(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
namespace App\Controllers;

class HomeController {
    public function index() {}
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundNS := false
	foundClass := false
	for _, sym := range parsed.Symbols {
		if sym.Kind == "module" && sym.Signature == "namespace App\\Controllers" {
			foundNS = true
		}
		if sym.Name == "HomeController" && sym.Kind == "class" {
			foundClass = true
		}
	}
	if !foundNS {
		t.Error("Expected to find namespace 'App\\Controllers'")
	}
	if !foundClass {
		t.Error("Expected to find class 'HomeController'")
	}
}

func TestParser_ParsePHPEmptyFile(t *testing.T) {
	parser := NewParser()
	parsed, err := parser.Parse([]byte("<?php"), "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty PHP file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParsePHPMultipleFunctions(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
function add($a, $b) { return $a + $b; }
function subtract($a, $b) { return $a - $b; }
function multiply($a, $b) { return $a * $b; }
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	expected := map[string]bool{"add": false, "subtract": false, "multiply": false}
	for _, sym := range parsed.Symbols {
		if _, ok := expected[sym.Name]; ok {
			expected[sym.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("Expected to find function '%s'", name)
		}
	}
}

func TestParser_ParsePHPStaticMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
class DB {
    public static function connect($dsn) {
        return new self($dsn);
    }
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "DB::connect" && sym.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find static method 'DB::connect'")
	}
}

// ==================== C# Tests ====================

func TestParser_ParseCSharpClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class User {
    public string Name { get; set; }
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "User" && sym.Kind == "class" {
			found = true
			if sym.Signature != "class User" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find class 'User'")
	}
}

func TestParser_ParseCSharpMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class UserService {
    public User GetById(int id) {
        return null;
    }

    public void Delete(int id) {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundClass := false
	foundGet := false
	foundDelete := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "UserService" && sym.Kind == "class" {
			foundClass = true
		}
		if sym.Name == "UserService.GetById" && sym.Kind == "function" {
			foundGet = true
		}
		if sym.Name == "UserService.Delete" && sym.Kind == "function" {
			foundDelete = true
		}
	}
	if !foundClass {
		t.Error("Expected to find class 'UserService'")
	}
	if !foundGet {
		t.Error("Expected to find method 'UserService.GetById'")
	}
	if !foundDelete {
		t.Error("Expected to find method 'UserService.Delete'")
	}
}

func TestParser_ParseCSharpConstructor(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class Repository {
    public Repository(IDbContext ctx) {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Repository.Repository" && sym.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find constructor 'Repository.Repository'")
	}
}

func TestParser_ParseCSharpInheritance(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class AdminService : UserService {
    public void Promote(int userId) {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "AdminService" && sym.Kind == "class" {
			found = true
			if sym.Signature != "class AdminService : UserService" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find class 'AdminService'")
	}
}

func TestParser_ParseCSharpInterface(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public interface IUserService {
    User GetById(int id);
    void Delete(int id);
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundInterface := false
	foundGet := false
	foundDelete := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "IUserService" && sym.Kind == "interface" {
			foundInterface = true
		}
		if sym.Name == "IUserService.GetById" && sym.Kind == "function" {
			foundGet = true
		}
		if sym.Name == "IUserService.Delete" && sym.Kind == "function" {
			foundDelete = true
		}
	}
	if !foundInterface {
		t.Error("Expected to find interface 'IUserService'")
	}
	if !foundGet {
		t.Error("Expected to find interface method 'IUserService.GetById'")
	}
	if !foundDelete {
		t.Error("Expected to find interface method 'IUserService.Delete'")
	}
}

func TestParser_ParseCSharpStruct(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public struct Point {
    public int X { get; set; }
    public int Y { get; set; }

    public double Distance() { return 0; }
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundStruct := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Point" && sym.Kind == "class" {
			foundStruct = true
			if sym.Signature != "struct Point" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
		}
		if sym.Name == "Point.Distance" && sym.Kind == "function" {
			foundMethod = true
		}
	}
	if !foundStruct {
		t.Error("Expected to find struct 'Point'")
	}
	if !foundMethod {
		t.Error("Expected to find method 'Point.Distance'")
	}
}

func TestParser_ParseCSharpEnum(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public enum Status {
    Active,
    Inactive,
    Pending
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Status" && sym.Kind == "class" {
			found = true
			if sym.Signature != "enum Status" {
				t.Errorf("unexpected signature: %q", sym.Signature)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find enum 'Status'")
	}
}

func TestParser_ParseCSharpRecord(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public record UserDto(string Name, string Email) {
    public string Display() => Name;
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundRecord := false
	foundMethod := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "UserDto" && sym.Kind == "class" {
			foundRecord = true
		}
		if sym.Name == "UserDto.Display" && sym.Kind == "function" {
			foundMethod = true
		}
	}
	if !foundRecord {
		t.Error("Expected to find record 'UserDto'")
	}
	if !foundMethod {
		t.Error("Expected to find method 'UserDto.Display'")
	}
}

func TestParser_ParseCSharpNamespace(t *testing.T) {
	parser := NewParser()
	code := []byte(`
namespace MyApp.Services {
    public class FooService {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundNS := false
	foundClass := false
	for _, sym := range parsed.Symbols {
		if sym.Kind == "module" && sym.Name == "MyApp.Services" {
			foundNS = true
		}
		if sym.Name == "FooService" && sym.Kind == "class" {
			foundClass = true
		}
	}
	if !foundNS {
		t.Error("Expected to find namespace 'MyApp.Services'")
	}
	if !foundClass {
		t.Error("Expected to find class 'FooService'")
	}
}

func TestParser_ParseCSharpDelegate(t *testing.T) {
	parser := NewParser()
	code := []byte(`public delegate void EventHandler(object sender, EventArgs e);`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "EventHandler" && sym.Kind == "type" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find delegate 'EventHandler'")
	}
}

func TestParser_ParseCSharpEmptyFile(t *testing.T) {
	parser := NewParser()
	parsed, err := parser.Parse([]byte(""), "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("expected 0 symbols for empty file, got %d", len(parsed.Symbols))
	}
}

func TestParser_ParseCSharpFileScopedNamespace(t *testing.T) {
	parser := NewParser()
	code := []byte(`
namespace MyApp.Controllers;

public class HomeController {
    public string Index() => "Home";
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	foundNS := false
	foundClass := false
	for _, sym := range parsed.Symbols {
		if sym.Kind == "module" {
			foundNS = true
		}
		if sym.Name == "HomeController" && sym.Kind == "class" {
			foundClass = true
		}
	}
	if !foundNS {
		t.Error("Expected to find file-scoped namespace")
	}
	if !foundClass {
		t.Error("Expected to find class 'HomeController'")
	}
}

// ==================== Additional coverage tests ====================

// Test Parse with alternate language aliases
func TestParser_ParseLanguageAliases(t *testing.T) {
	parser := NewParser()

	// "python" alias
	code := []byte(`def foo(): pass`)
	parsed, err := parser.Parse(code, "python")
	if err != nil {
		t.Fatalf("Parse with 'python' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for python alias")
	}

	// "golang" alias
	code = []byte(`package main
func Foo() {}`)
	parsed, err = parser.Parse(code, "golang")
	if err != nil {
		t.Fatalf("Parse with 'golang' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for golang alias")
	}

	// "javascript" alias
	code = []byte(`function foo() {}`)
	parsed, err = parser.Parse(code, "javascript")
	if err != nil {
		t.Fatalf("Parse with 'javascript' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for javascript alias")
	}

	// "typescript" alias
	parsed, err = parser.Parse(code, "typescript")
	if err != nil {
		t.Fatalf("Parse with 'typescript' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for typescript alias")
	}

	// "ruby" alias
	code = []byte(`def foo; end`)
	parsed, err = parser.Parse(code, "ruby")
	if err != nil {
		t.Fatalf("Parse with 'ruby' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for ruby alias")
	}

	// "rust" alias
	code = []byte(`fn foo() {}`)
	parsed, err = parser.Parse(code, "rust")
	if err != nil {
		t.Fatalf("Parse with 'rust' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for rust alias")
	}

	// "csharp" and "c#" aliases
	code = []byte(`public class Foo {}`)
	parsed, err = parser.Parse(code, "csharp")
	if err != nil {
		t.Fatalf("Parse with 'csharp' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for csharp alias")
	}
	parsed, err = parser.Parse(code, "c#")
	if err != nil {
		t.Fatalf("Parse with 'c#' failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected symbols for c# alias")
	}

	// Unknown language falls back to JS
	code = []byte(`function foo() {}`)
	parsed, err = parser.Parse(code, "unknown_lang")
	if err != nil {
		t.Fatalf("Parse with unknown lang failed: %v", err)
	}
	if len(parsed.Symbols) == 0 {
		t.Error("expected JS fallback for unknown language")
	}
}

// Python: tuple unpacking assignment
func TestParser_ParsePythonTupleUnpacking(t *testing.T) {
	parser := NewParser()
	code := []byte(`
a, b = 1, 2
_private, c = 3, 4
`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["a"] {
		t.Error("expected variable 'a' from tuple unpacking")
	}
	if !names["b"] {
		t.Error("expected variable 'b' from tuple unpacking")
	}
	if names["_private"] {
		t.Error("_private should be filtered out")
	}
	if !names["c"] {
		t.Error("expected variable 'c' from tuple unpacking")
	}
}

// Python: private variable filtering
func TestParser_ParsePythonPrivateVariable(t *testing.T) {
	parser := NewParser()
	code := []byte(`
_private = 1
public = 2
`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if names["_private"] {
		t.Error("_private should be excluded")
	}
	if !names["public"] {
		t.Error("public should be included")
	}
}

// Go: receiver type extraction edge cases
func TestParser_ParseGoPointerReceiver(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
type MyStruct struct{}
func (m *MyStruct) PointerMethod() {}
func (m MyStruct) ValueMethod() {}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["*MyStruct.PointerMethod"] {
		t.Error("expected *MyStruct.PointerMethod")
	}
	if !names["MyStruct.ValueMethod"] {
		t.Error("expected MyStruct.ValueMethod")
	}
}

// Go: var/const blocks
func TestParser_ParseGoVarConstBlock(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
var (
	x = 1
	y = 2
)
const (
	A = "a"
	B = "b"
)
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	for _, name := range []string{"x", "y", "A", "B"} {
		if !names[name] {
			t.Errorf("expected variable/const %q", name)
		}
	}
}

// Ruby: module with methods inside
func TestParser_ParseRubyModuleWithMethods(t *testing.T) {
	parser := NewParser()
	code := []byte(`
module Helper
  def self.format(value)
    value.to_s
  end

  def instance_method
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Helper"] {
		t.Error("expected module Helper")
	}
	if !names["Helper.format"] {
		t.Error("expected Helper.format singleton method")
	}
}

// SQL: index without UNIQUE
func TestSQL_PlainIndex(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE INDEX idx_users_email ON users (email);
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "idx_users_email" {
			found = true
		}
	}
	if !found {
		t.Error("expected index idx_users_email")
	}
}

// PHP: namespace with class and methods
func TestParser_ParsePHPNamespaceWithClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
namespace App\Controllers;

class UserController {
    public function index() {}
    public static function create() {}
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["UserController"] {
		t.Error("expected class UserController")
	}
}

// C#: method with no return type (should handle gracefully)
func TestParser_ParseCSharpMethodNoReturnType(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class Foo {
    public void Bar() {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Foo.Bar" && sym.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Error("expected Foo.Bar")
	}
}

// C#: record with methods
func TestParser_ParseCSharpRecordWithMembers(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public record Person(string Name, int Age) {
    public string Display() { return Name; }
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Person"] {
		t.Error("expected record Person")
	}
}

// C#: struct with methods
func TestParser_ParseCSharpStructWithMethods(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public struct Vector {
    public double Length() { return 0; }
    public Vector(double x) {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Vector"] {
		t.Error("expected struct Vector")
	}
	if !names["Vector.Length"] {
		t.Error("expected method Vector.Length")
	}
	if !names["Vector.Vector"] {
		t.Error("expected constructor Vector.Vector")
	}
}

// C#: delegate with no name should return nil
func TestParser_ParseCSharpEmptyDelegate(t *testing.T) {
	parser := NewParser()
	// Parse something with a delegate
	code := []byte(`
public delegate void Handler(string msg);
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Handler" && sym.Kind == "type" {
			found = true
		}
	}
	if !found {
		t.Error("expected delegate Handler")
	}
}

// Rust: impl block with methods
func TestParser_ParseRustImplBlock(t *testing.T) {
	parser := NewParser()
	code := []byte(`
struct Point {
    x: f64,
    y: f64,
}

impl Point {
    fn new(x: f64, y: f64) -> Point {
        Point { x, y }
    }
    fn distance(&self) -> f64 {
        (self.x * self.x + self.y * self.y).sqrt()
    }
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Point"] {
		t.Error("expected struct Point")
	}
	if !names["Point::new"] {
		t.Error("expected Point::new")
	}
	if !names["Point::distance"] {
		t.Error("expected Point::distance")
	}
}

// PHP: interface and trait
func TestParser_ParsePHPInterfaceAndTrait(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
interface Cacheable {
    public function cacheKey();
}

trait Timestamps {
    public function createdAt() {}
    public function updatedAt() {}
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Cacheable"] {
		t.Error("expected interface Cacheable")
	}
	if !names["Timestamps"] {
		t.Error("expected trait Timestamps")
	}
}

// PHP: class with inheritance
func TestParser_ParsePHPClassInheritance(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
class Animal {}
class Dog extends Animal {
    public function bark() {}
}
`)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["Dog"] {
		t.Error("expected class Dog")
	}
}

// Go: interface with multiple methods
func TestParser_ParseGoInterfaceMultipleMethods(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
type Reader interface {
	Read(p []byte) (n int, err error)
	Close() error
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
		}
	}
	if !found {
		t.Error("expected interface Reader")
	}
}

// Python: class with no methods (empty body)
func TestParser_ParsePythonEmptyClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Empty:
    pass
`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Empty" && sym.Kind == "class" {
			found = true
		}
	}
	if !found {
		t.Error("expected class Empty")
	}
}

// Rust: all item types to cover nil branches
func TestParser_ParseRustAllItems(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn standalone() {}
pub fn public_standalone() {}
struct MyStruct { x: i32 }
enum MyEnum { A, B }
trait MyTrait { fn required(&self); }
type Alias = i32;
const CONSTANT: i32 = 42;
static STATIC_VAR: i32 = 1;
mod mymod {}
macro_rules! mymacro { () => {} }
impl MyStruct { fn method(&self) {} }
impl MyTrait for MyStruct { fn required(&self) {} }
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	for _, name := range []string{"standalone", "public_standalone", "MyStruct", "MyEnum", "MyTrait", "Alias", "CONSTANT", "STATIC_VAR", "mymod", "mymacro!"} {
		if !names[name] {
			t.Errorf("missing %q", name)
		}
	}
}

// SQL: view and function
func TestSQL_ViewAndFunction(t *testing.T) {
	parser := NewParser()
	code := []byte(`
CREATE VIEW active_users AS SELECT * FROM users WHERE active = true;
CREATE OR REPLACE FUNCTION get_user(p_id INT)
RETURNS TABLE(id INT, name TEXT) AS $$
BEGIN
  RETURN QUERY SELECT id, name FROM users WHERE id = p_id;
END;
$$ LANGUAGE plpgsql;
`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["active_users"] {
		t.Error("expected view active_users")
	}
}

// Go: function with multiple return values
func TestParser_ParseGoFunctionMultiReturn(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func divide(a, b int) (int, error) {
	return a / b, nil
}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "divide" {
			found = true
		}
	}
	if !found {
		t.Error("expected function divide")
	}
}

// Go: typed variable declaration
func TestParser_ParseGoTypedVar(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
var handler func()
var count int
var items []string
var lookup map[string]int
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	kinds := map[string]string{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
		kinds[sym.Name] = sym.Kind
	}
	if !names["handler"] {
		t.Error("expected variable handler")
	}
	// func type should be "function" kind
	if kinds["handler"] != "function" {
		t.Errorf("expected handler to be function kind, got %q", kinds["handler"])
	}
	if !names["count"] {
		t.Error("expected variable count")
	}
}

// Ruby: class with inheritance
func TestParser_ParseRubyClassInheritance(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Dog < Animal
  def bark
    puts "woof"
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Dog" && sym.Kind == "class" {
			found = true
			if sym.Signature == "" {
				t.Error("expected non-empty signature for Dog")
			}
		}
	}
	if !found {
		t.Error("expected class Dog")
	}
}

// Python: function with no body (syntax edge)
func TestParser_ParsePythonMethodInClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class MyClass:
    def __init__(self, name):
        self.name = name

    def greet(self):
        return f"Hello, {self.name}"

    @staticmethod
    def static_method():
        pass
`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	names := map[string]bool{}
	for _, sym := range parsed.Symbols {
		names[sym.Name] = true
	}
	if !names["MyClass"] {
		t.Error("expected class MyClass")
	}
	if !names["MyClass.__init__"] {
		t.Error("expected MyClass.__init__")
	}
	if !names["MyClass.greet"] {
		t.Error("expected MyClass.greet")
	}
}

// JS: class with no identifier in function_expression (anonymous)
func TestParser_ParseJSAnonymousFunctionExpression(t *testing.T) {
	parser := NewParser()
	code := []byte(`
const handler = function() {};
`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "handler" {
			found = true
		}
	}
	if !found {
		t.Error("expected variable handler")
	}
}
