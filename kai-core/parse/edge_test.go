package parse

import (
	"testing"
)

// These tests exercise edge cases and defensive code paths.

func TestEdge_JSClassNoName(t *testing.T) {
	parser := NewParser()
	code := []byte(`const x = class {};`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_JSEmptyClassBody(t *testing.T) {
	parser := NewParser()
	code := []byte(`class Empty {}`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Empty" {
			found = true
		}
	}
	if !found {
		t.Error("expected class Empty")
	}
}

func TestEdge_JSComputedPropertyMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Foo {
  [Symbol.iterator]() {}
  get name() { return ""; }
}
`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_GoMethodThreeParamLists(t *testing.T) {
	parser := NewParser()
	// Method with named return values (third parameter list)
	code := []byte(`package main
type Foo struct{}
func (f Foo) Bar(x int) (result string, err error) {
	return "", nil
}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "Foo.Bar" {
			found = true
		}
	}
	if !found {
		t.Error("expected Foo.Bar")
	}
}

func TestEdge_GoPointerReceiverFallback(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
type Foo struct{}
func (f *Foo) Method() {}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	found := false
	for _, sym := range parsed.Symbols {
		if sym.Name == "*Foo.Method" {
			found = true
		}
	}
	if !found {
		t.Error("expected *Foo.Method")
	}
}

func TestEdge_RubyClassWithScopeResolution(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Outer::Inner
  def method
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RubyModuleWithScopeResolution(t *testing.T) {
	parser := NewParser()
	code := []byte(`
module Outer::Inner
  def method
  end
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustGenericImpl(t *testing.T) {
	parser := NewParser()
	code := []byte(`
struct Container<T> {
    item: T,
}

impl<T> Container<T> {
    fn new(item: T) -> Self {
        Container { item }
    }
}
`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpClassNoBody(t *testing.T) {
	parser := NewParser()
	code := []byte(`public class Empty;`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RubyRequireWithParens(t *testing.T) {
	parser := NewParser()
	code := []byte(`
require("json")
require_relative("./helper")
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	if !sources["json"] {
		t.Error("expected require of json")
	}
}

func TestEdge_RubyRequireWithDirectString(t *testing.T) {
	parser := NewParser()
	// require 'json' — without parentheses, string is direct child
	code := []byte(`
require 'json'
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, imp := range result.Imports {
		if imp.Source == "json" {
			found = true
		}
	}
	if !found {
		t.Error("expected require of json")
	}
}

func TestEdge_RubyPrivateProtectedPublic(t *testing.T) {
	parser := NewParser()
	// Use private/protected/public as method calls with arguments
	// which makes them 'call' nodes in tree-sitter
	code := []byte(`
class Foo
  def visible1
  end

  private :visible1

  def visible2
  end

  protected :visible2

  def visible3
  end

  public :visible3
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	_ = result
}

func TestEdge_GoCallDefault(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func main() {
	foo()
	pkg.Bar()
	a.b.C()
}
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	callNames := map[string]bool{}
	for _, c := range result.Calls {
		callNames[c.CalleeName] = true
	}
	if !callNames["foo"] {
		t.Error("missing foo")
	}
	if !callNames["Bar"] {
		t.Error("missing Bar")
	}
}

func TestEdge_ExtractCallsEmpty(t *testing.T) {
	parser := NewParser()
	result, err := parser.ExtractCalls([]byte(""), "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Calls) != 0 {
		t.Errorf("expected no calls, got %d", len(result.Calls))
	}
}

func TestEdge_RubyCallNoName(t *testing.T) {
	parser := NewParser()
	code := []byte(`
Foo.bar
puts "hello"
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	_ = result
}

// ==================== Malformed/broken code edge cases ====================
// These exercise defensive nil-return guards with code that tree-sitter
// partially parses (producing ERROR nodes or missing identifiers).

func TestEdge_JSMalformedFunction(t *testing.T) {
	parser := NewParser()
	// function declaration missing name
	code := []byte(`export function() {}`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_JSMalformedClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`class { method() {} }`)
	parsed, err := parser.Parse(code, "js")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PythonMalformedDef(t *testing.T) {
	parser := NewParser()
	code := []byte(`def (): pass`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PythonMalformedClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`class : pass`)
	parsed, err := parser.Parse(code, "py")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_GoMalformedFunc(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func () {}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_GoMalformedType(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
type struct {}
`)
	parsed, err := parser.Parse(code, "go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RubyMalformedDef(t *testing.T) {
	parser := NewParser()
	code := []byte(`def ; end`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedFn(t *testing.T) {
	parser := NewParser()
	code := []byte(`fn () {}`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedStruct(t *testing.T) {
	parser := NewParser()
	code := []byte(`struct {}`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedEnum(t *testing.T) {
	parser := NewParser()
	code := []byte(`enum { A, B }`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedTrait(t *testing.T) {
	parser := NewParser()
	code := []byte(`trait {}`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedConst(t *testing.T) {
	parser := NewParser()
	code := []byte(`const = 42;`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedStatic(t *testing.T) {
	parser := NewParser()
	code := []byte(`static = 1;`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedMod(t *testing.T) {
	parser := NewParser()
	code := []byte(`mod {}`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedMacro(t *testing.T) {
	parser := NewParser()
	code := []byte(`macro_rules! { () => {} }`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_RustMalformedType(t *testing.T) {
	parser := NewParser()
	code := []byte(`type = i32;`)
	parsed, err := parser.Parse(code, "rs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_SQLMalformed(t *testing.T) {
	parser := NewParser()
	code := []byte(`CREATE TABLE;`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_SQLMalformedIndex(t *testing.T) {
	parser := NewParser()
	code := []byte(`CREATE INDEX ON users;`)
	parsed, err := parser.Parse(code, "sql")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PHPMalformedFunction(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php function() {} `)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PHPMalformedClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php class {} `)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PHPMalformedInterface(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php interface {} `)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PHPMalformedTrait(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php trait {} `)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_PHPMalformedNamespace(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php namespace ; `)
	parsed, err := parser.Parse(code, "php")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpMalformedClass(t *testing.T) {
	parser := NewParser()
	code := []byte(`class {}`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpMalformedNamespace(t *testing.T) {
	parser := NewParser()
	code := []byte(`namespace {}`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpMalformedDelegate(t *testing.T) {
	parser := NewParser()
	code := []byte(`delegate void ();`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpMalformedMethod(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Foo {
    void () {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}

func TestEdge_CSharpMalformedConstructor(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Foo {
    () {}
}
`)
	parsed, err := parser.Parse(code, "cs")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_ = parsed.Symbols
}
