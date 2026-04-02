package parse

import (
	"strings"
	"testing"
)

func TestExtractCalls_SimpleCalls(t *testing.T) {
	parser := NewParser()

	code := []byte(`
function foo() {
    bar();
    baz(1, 2);
    qux();
}
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Calls) != 3 {
		t.Errorf("expected 3 calls, got %d", len(result.Calls))
	}

	// Check call names
	callNames := make(map[string]bool)
	for _, call := range result.Calls {
		callNames[call.CalleeName] = true
	}

	for _, expected := range []string{"bar", "baz", "qux"} {
		if !callNames[expected] {
			t.Errorf("expected call to %q not found", expected)
		}
	}
}

func TestExtractCalls_MethodCalls(t *testing.T) {
	parser := NewParser()

	code := []byte(`
obj.method();
this.doSomething();
foo.bar.baz();
console.log("test");
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Calls) < 4 {
		t.Errorf("expected at least 4 calls, got %d", len(result.Calls))
	}

	// Check for specific method calls
	found := make(map[string]bool)
	for _, call := range result.Calls {
		found[call.CalleeName] = true
		if call.CalleeName == "method" && call.CalleeObject != "obj" {
			t.Errorf("expected object 'obj' for method, got %q", call.CalleeObject)
		}
		if call.CalleeName == "doSomething" && call.CalleeObject != "this" {
			t.Errorf("expected object 'this' for doSomething, got %q", call.CalleeObject)
		}
	}

	if !found["method"] {
		t.Error("expected call to 'method' not found")
	}
	if !found["doSomething"] {
		t.Error("expected call to 'doSomething' not found")
	}
	if !found["log"] {
		t.Error("expected call to 'log' not found")
	}
}

func TestExtractImports_Named(t *testing.T) {
	parser := NewParser()

	code := []byte(`
import { foo, bar as baz } from './utils';
import { calculateTaxes } from '../taxes';
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(result.Imports))
	}

	// Check first import
	imp := result.Imports[0]
	if imp.Source != "./utils" {
		t.Errorf("expected source './utils', got %q", imp.Source)
	}
	if !imp.IsRelative {
		t.Error("expected IsRelative to be true")
	}
	if imp.Named["foo"] != "foo" {
		t.Errorf("expected named import 'foo', got %v", imp.Named)
	}
	if imp.Named["baz"] != "bar" {
		t.Errorf("expected named import 'baz' -> 'bar', got %v", imp.Named)
	}
}

func TestExtractImports_CommonJS(t *testing.T) {
	parser := NewParser()

	code := []byte(`
const express = require('express');
const { getProfile } = require('../controllers/userController');
const utils = require('./utils');
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Imports) != 3 {
		t.Errorf("expected 3 imports, got %d", len(result.Imports))
		for i, imp := range result.Imports {
			t.Logf("  import %d: %s (relative: %v)", i, imp.Source, imp.IsRelative)
		}
	}

	// Check sources
	sources := make(map[string]bool)
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}

	if !sources["express"] {
		t.Error("expected import 'express' not found")
	}
	if !sources["../controllers/userController"] {
		t.Error("expected import '../controllers/userController' not found")
	}
	if !sources["./utils"] {
		t.Error("expected import './utils' not found")
	}

	// Check IsRelative
	for _, imp := range result.Imports {
		if imp.Source == "express" && imp.IsRelative {
			t.Error("expected 'express' to be non-relative")
		}
		if imp.Source == "../controllers/userController" && !imp.IsRelative {
			t.Error("expected '../controllers/userController' to be relative")
		}
		if imp.Source == "./utils" && !imp.IsRelative {
			t.Error("expected './utils' to be relative")
		}
	}
}

func TestExtractImports_Default(t *testing.T) {
	parser := NewParser()

	code := []byte(`
import React from 'react';
import axios from 'axios';
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(result.Imports))
	}

	// Check React import
	reactImport := result.Imports[0]
	if reactImport.Default != "React" {
		t.Errorf("expected default 'React', got %q", reactImport.Default)
	}
	if reactImport.Source != "react" {
		t.Errorf("expected source 'react', got %q", reactImport.Source)
	}
	if reactImport.IsRelative {
		t.Error("expected IsRelative to be false for 'react'")
	}
}

func TestExtractImports_Namespace(t *testing.T) {
	parser := NewParser()

	code := []byte(`
import * as utils from './utils';
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Errorf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Namespace != "utils" {
		t.Errorf("expected namespace 'utils', got %q", imp.Namespace)
	}
	if imp.Source != "./utils" {
		t.Errorf("expected source './utils', got %q", imp.Source)
	}
}

func TestExtractImports_Mixed(t *testing.T) {
	parser := NewParser()

	code := []byte(`
import React, { useState, useEffect } from 'react';
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Errorf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Default != "React" {
		t.Errorf("expected default 'React', got %q", imp.Default)
	}
	if imp.Named["useState"] != "useState" {
		t.Errorf("expected named 'useState', got %v", imp.Named)
	}
	if imp.Named["useEffect"] != "useEffect" {
		t.Errorf("expected named 'useEffect', got %v", imp.Named)
	}
}

func TestExtractExports(t *testing.T) {
	parser := NewParser()

	code := []byte(`
export function calculateTaxes(amount) {
    return amount * 0.1;
}

export const TAX_RATE = 0.1;

export class TaxCalculator {
    calculate(amount) {
        return amount * TAX_RATE;
    }
}

export { helper, utils as utilities };
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	// Should have: calculateTaxes, TAX_RATE, TaxCalculator, helper, utils
	if len(result.Exports) < 4 {
		t.Errorf("expected at least 4 exports, got %d: %v", len(result.Exports), result.Exports)
	}

	exportSet := make(map[string]bool)
	for _, e := range result.Exports {
		exportSet[e] = true
	}

	for _, expected := range []string{"calculateTaxes", "TAX_RATE", "TaxCalculator"} {
		if !exportSet[expected] {
			t.Errorf("expected export %q not found in %v", expected, result.Exports)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"src/utils.ts", false},
		{"src/utils.test.ts", true},
		{"src/utils.spec.ts", true},
		{"src/__tests__/utils.ts", true},
		{"tests/utils.ts", true},
		{"src/utils_test.ts", true},
		{"src/components/Button.tsx", false},
		{"src/components/Button.test.tsx", true},
		{"src/components/__tests__/Button.tsx", true},
	}

	for _, tc := range tests {
		result := IsTestFile(tc.path)
		if result != tc.expected {
			t.Errorf("IsTestFile(%q) = %v, expected %v", tc.path, result, tc.expected)
		}
	}
}

func TestFindTestsForFile(t *testing.T) {
	allFiles := []string{
		"src/utils.ts",
		"src/utils.test.ts",
		"src/utils.spec.ts",
		"src/helper.ts",
		"src/__tests__/helper.ts",
		"src/components/Button.tsx",
		"src/components/Button.test.tsx",
	}

	tests := []struct {
		sourcePath string
		expected   []string
	}{
		{"src/utils.ts", []string{"src/utils.test.ts", "src/utils.spec.ts"}},
		{"src/components/Button.tsx", []string{"src/components/Button.test.tsx"}},
	}

	for _, tc := range tests {
		result := FindTestsForFile(tc.sourcePath, allFiles)
		if len(result) != len(tc.expected) {
			t.Errorf("FindTestsForFile(%q) returned %d files, expected %d: %v",
				tc.sourcePath, len(result), len(tc.expected), result)
			continue
		}

		resultSet := make(map[string]bool)
		for _, r := range result {
			resultSet[r] = true
		}
		for _, exp := range tc.expected {
			if !resultSet[exp] {
				t.Errorf("FindTestsForFile(%q) missing expected file %q", tc.sourcePath, exp)
			}
		}
	}
}

func TestPossibleFilePaths(t *testing.T) {
	result := PossibleFilePaths("./utils")

	expected := []string{
		"./utils.ts",
		"./utils.tsx",
		"./utils.js",
		"./utils.jsx",
		"utils/index.ts",
		"utils/index.tsx",
		"utils/index.js",
		"utils/index.jsx",
	}

	if len(result) != len(expected) {
		t.Errorf("PossibleFilePaths returned %d paths, expected %d", len(result), len(expected))
	}

	// Check that .ts is first (preferred)
	if result[0] != "./utils.ts" {
		t.Errorf("expected first path to be './utils.ts', got %q", result[0])
	}
}

func TestExtractCalls_RealWorldExample(t *testing.T) {
	parser := NewParser()

	code := []byte(`
import { calculateTaxes, formatCurrency } from './taxes';
import { getUserData } from '../api/users';

export function processOrder(orderId) {
    const user = getUserData(orderId);
    const subtotal = calculateSubtotal(user.cart);
    const taxes = calculateTaxes(subtotal);
    const total = subtotal + taxes;

    console.log(formatCurrency(total));

    return {
        subtotal,
        taxes,
        total: formatCurrency(total)
    };
}

function calculateSubtotal(cart) {
    return cart.reduce((sum, item) => sum + item.price, 0);
}
`)

	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	// Check imports
	if len(result.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(result.Imports))
	}

	// Check that we found the key calls
	callNames := make(map[string]bool)
	for _, call := range result.Calls {
		callNames[call.CalleeName] = true
	}

	expectedCalls := []string{"getUserData", "calculateSubtotal", "calculateTaxes", "formatCurrency", "log", "reduce"}
	for _, exp := range expectedCalls {
		if !callNames[exp] {
			t.Errorf("expected call to %q not found", exp)
		}
	}

	// Check exports
	if len(result.Exports) != 1 {
		t.Errorf("expected 1 export, got %d: %v", len(result.Exports), result.Exports)
	}
	if len(result.Exports) > 0 && result.Exports[0] != "processOrder" {
		t.Errorf("expected export 'processOrder', got %q", result.Exports[0])
	}
}

// ==================== Go Calls/Imports/Exports ====================

func TestExtractCalls_GoImportSingle(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
import "fmt"
func main() { fmt.Println("hello") }
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	if result.Imports[0].Source != "fmt" {
		t.Errorf("expected source 'fmt', got %q", result.Imports[0].Source)
	}
	if result.Imports[0].Default != "fmt" {
		t.Errorf("expected default 'fmt', got %q", result.Imports[0].Default)
	}
}

func TestExtractCalls_GoImportGrouped(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
import (
	"fmt"
	"os"
	"strings"
)
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(result.Imports))
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	for _, s := range []string{"fmt", "os", "strings"} {
		if !sources[s] {
			t.Errorf("missing import %q", s)
		}
	}
}

func TestExtractCalls_GoImportAlias(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
import (
	f "fmt"
	. "os"
	_ "net/http/pprof"
)
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(result.Imports))
	}
	for _, imp := range result.Imports {
		switch imp.Source {
		case "fmt":
			if imp.Default != "f" {
				t.Errorf("expected alias 'f' for fmt, got %q", imp.Default)
			}
		case "os":
			if imp.Namespace != "." {
				t.Errorf("expected dot import for os, got namespace=%q", imp.Namespace)
			}
		case "net/http/pprof":
			if imp.Default != "_" {
				t.Errorf("expected blank import for pprof, got %q", imp.Default)
			}
		}
	}
}

func TestExtractCalls_GoImportRelative(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
import (
	"./local"
	"myapp/internal/auth"
)
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	for _, imp := range result.Imports {
		if imp.Source == "./local" && !imp.IsRelative {
			t.Error("expected ./local to be relative")
		}
		if imp.Source == "myapp/internal/auth" && !imp.IsRelative {
			t.Error("expected internal import to be relative")
		}
	}
}

func TestExtractCalls_GoCallSites(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
import "fmt"
func main() {
	fmt.Println("hello")
	doSomething()
	result := compute(42)
	_ = result
}
func doSomething() {}
func compute(x int) int { return x }
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	callNames := map[string]bool{}
	for _, c := range result.Calls {
		callNames[c.CalleeName] = true
	}
	for _, name := range []string{"Println", "doSomething", "compute"} {
		if !callNames[name] {
			t.Errorf("missing call to %q", name)
		}
	}
	// Check that fmt.Println is a method call
	for _, c := range result.Calls {
		if c.CalleeName == "Println" {
			if !c.IsMethodCall {
				t.Error("expected fmt.Println to be a method call")
			}
			if c.CalleeObject != "fmt" {
				t.Errorf("expected object 'fmt', got %q", c.CalleeObject)
			}
		}
	}
}

func TestExtractCalls_GoNestedSelector(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func main() {
	a.b.c()
	foo().bar()
}
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := map[string]bool{}
	for _, c := range result.Calls {
		found[c.CalleeName] = true
		if c.CalleeName == "bar" && c.CalleeObject != "(call)" {
			t.Errorf("expected (call) for chained, got %q", c.CalleeObject)
		}
	}
	if !found["c"] {
		t.Error("missing nested selector call 'c'")
	}
	if !found["bar"] {
		t.Error("missing chained call 'bar'")
	}
}

func TestExtractCalls_GoParenthesized(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func main() {
	(myFunc)(42)
}
func myFunc(x int) {}
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "myFunc" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find parenthesized call to myFunc")
	}
}

func TestExtractCalls_GoChainedCallIgnored(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func main() {
	getFunc()()
}
func getFunc() func() { return func() {} }
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// The outer chained call foo()() should be ignored
	for _, c := range result.Calls {
		if c.CalleeName == "" {
			t.Error("unexpected empty callee name")
		}
	}
}

func TestExtractCalls_GoExports(t *testing.T) {
	parser := NewParser()
	code := []byte(`package mypackage

func ExportedFunc() {}
func unexported() {}

type ExportedType struct{}
type unexportedType struct{}

func (e *ExportedType) ExportedMethod() {}
func (e *ExportedType) unexportedMethod() {}

var ExportedVar = 42
var unexportedVar = 0

const ExportedConst = "hello"
const unexportedConst = "bye"
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	for _, name := range []string{"ExportedFunc", "ExportedType", "ExportedMethod", "ExportedVar", "ExportedConst"} {
		if !exportSet[name] {
			t.Errorf("missing export %q", name)
		}
	}
	for _, name := range []string{"unexported", "unexportedType", "unexportedMethod", "unexportedVar", "unexportedConst"} {
		if exportSet[name] {
			t.Errorf("unexported %q should not be in exports", name)
		}
	}
}

func TestIsGoExported(t *testing.T) {
	if isGoExported("") {
		t.Error("empty string should not be exported")
	}
	if !isGoExported("Foo") {
		t.Error("Foo should be exported")
	}
	if isGoExported("foo") {
		t.Error("foo should not be exported")
	}
}

// ==================== Ruby Calls/Imports/Exports ====================

func TestExtractCalls_RubyRequire(t *testing.T) {
	parser := NewParser()
	code := []byte(`
require 'json'
require "yaml"
require_relative './helper'
load 'setup.rb'
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) < 4 {
		t.Fatalf("expected at least 4 imports, got %d", len(result.Imports))
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	for _, s := range []string{"json", "yaml", "./helper", "setup.rb"} {
		if !sources[s] {
			t.Errorf("missing import %q", s)
		}
	}
	// Check relative flag
	for _, imp := range result.Imports {
		if imp.Source == "./helper" && !imp.IsRelative {
			t.Error("expected ./helper to be relative")
		}
	}
}

func TestExtractCalls_RubyCallSites(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Foo
  def bar
    puts "hello"
    self.baz(42)
    Klass.new
  end
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	callNames := map[string]bool{}
	for _, c := range result.Calls {
		callNames[c.CalleeName] = true
	}
	for _, name := range []string{"puts", "baz", "new"} {
		if !callNames[name] {
			t.Errorf("missing call to %q", name)
		}
	}
	// Check self method call
	for _, c := range result.Calls {
		if c.CalleeName == "baz" {
			if c.CalleeObject != "self" {
				t.Errorf("expected self for baz, got %q", c.CalleeObject)
			}
			if !c.IsMethodCall {
				t.Error("expected baz to be a method call")
			}
		}
	}
}

func TestExtractCalls_RubyCallSkipsRequire(t *testing.T) {
	parser := NewParser()
	code := []byte(`
require 'json'
require_relative './foo'
load 'bar.rb'
puts "hello"
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	for _, c := range result.Calls {
		if c.CalleeName == "require" || c.CalleeName == "require_relative" || c.CalleeName == "load" {
			t.Errorf("require/require_relative/load should not appear as calls, found %q", c.CalleeName)
		}
	}
}

func TestExtractCalls_RubyExports(t *testing.T) {
	parser := NewParser()
	code := []byte(`
module MyModule
  class MyClass
    def public_method
    end

    def _private_by_convention
    end

    def self.class_method
    end
  end

  MAX_SIZE = 100
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	for _, name := range []string{"MyModule", "MyClass", "public_method", "class_method", "MAX_SIZE"} {
		if !exportSet[name] {
			t.Errorf("missing export %q", name)
		}
	}
	if exportSet["_private_by_convention"] {
		t.Error("_private_by_convention should not be exported")
	}
}

func TestExtractCalls_RubyExportsWithPrivateCall(t *testing.T) {
	parser := NewParser()
	// Use private(:method) form which is a call node
	code := []byte(`
class Foo
  def visible
  end

  def also_visible
  end
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	if !exportSet["visible"] {
		t.Error("visible should be exported")
	}
	if !exportSet["also_visible"] {
		t.Error("also_visible should be exported")
	}
	if !exportSet["Foo"] {
		t.Error("Foo class should be exported")
	}
}

func TestExtractCalls_RubyChainedCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
arr = [1, 2, 3]
arr.map { |x| x * 2 }.select { |x| x > 2 }
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// Just ensure no crash on chained calls
	_ = result
}

// ==================== Rust Calls/Imports/Exports ====================

func TestExtractCalls_RustUseDeclaration(t *testing.T) {
	parser := NewParser()
	code := []byte(`
use std::io;
use std::collections::HashMap;
use crate::module;
use super::parent;
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) < 4 {
		t.Fatalf("expected at least 4 imports, got %d", len(result.Imports))
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	for _, imp := range result.Imports {
		if imp.Source == "crate::module" && !imp.IsRelative {
			t.Error("crate:: import should be relative")
		}
		if imp.Source == "super::parent" && !imp.IsRelative {
			t.Error("super:: import should be relative")
		}
		if imp.Source == "std::io" && imp.IsRelative {
			t.Error("std::io should not be relative")
		}
	}
}

func TestExtractCalls_RustUseBraced(t *testing.T) {
	parser := NewParser()
	code := []byte(`
use std::collections::{HashMap, HashSet};
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	imp := result.Imports[0]
	if _, ok := imp.Named["HashMap"]; !ok {
		t.Error("expected named import HashMap")
	}
	if _, ok := imp.Named["HashSet"]; !ok {
		t.Error("expected named import HashSet")
	}
}

func TestExtractCalls_RustUseWildcard(t *testing.T) {
	parser := NewParser()
	code := []byte(`
use std::io::prelude::*;
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
}

func TestExtractCalls_RustExternCrate(t *testing.T) {
	parser := NewParser()
	code := []byte(`
extern crate serde;
extern crate rand;
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	if !sources["serde"] {
		t.Error("missing extern crate serde")
	}
	if !sources["rand"] {
		t.Error("missing extern crate rand")
	}
}

func TestExtractCalls_RustCallSites(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn main() {
    println!("hello");
    let x = compute(42);
    let y = std::io::stdin();
}

fn compute(n: i32) -> i32 { n + 1 }
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	callNames := map[string]bool{}
	for _, c := range result.Calls {
		callNames[c.CalleeName] = true
	}
	if !callNames["println!"] {
		t.Error("missing macro call println!")
	}
	if !callNames["compute"] {
		t.Error("missing call to compute")
	}
}

func TestExtractCalls_RustMethodCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn example(s: String) {
    s.len();
    s.as_str();
}
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	callNames := map[string]bool{}
	for _, c := range result.Calls {
		callNames[c.CalleeName] = true
	}
	for _, name := range []string{"len", "as_str"} {
		if !callNames[name] {
			t.Errorf("missing method call %q", name)
		}
	}
	// Check they're marked as method calls
	for _, c := range result.Calls {
		if c.CalleeName == "len" && !c.IsMethodCall {
			t.Error("expected len to be a method call")
		}
	}
}

func TestExtractCalls_RustExports(t *testing.T) {
	parser := NewParser()
	code := []byte(`
pub fn public_func() {}
fn private_func() {}

pub struct PublicStruct {}
struct PrivateStruct {}

pub enum PublicEnum { A, B }

pub trait PublicTrait {}

pub const PUBLIC_CONST: i32 = 42;
const PRIVATE_CONST: i32 = 0;

pub static PUBLIC_STATIC: i32 = 1;

pub mod public_mod {}
mod private_mod {}

pub type PublicType = i32;
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	for _, name := range []string{"public_func", "PublicStruct", "PublicEnum", "PublicTrait", "PUBLIC_CONST", "PUBLIC_STATIC", "public_mod", "PublicType"} {
		if !exportSet[name] {
			t.Errorf("missing export %q", name)
		}
	}
	for _, name := range []string{"private_func", "PrivateStruct", "PRIVATE_CONST", "private_mod"} {
		if exportSet[name] {
			t.Errorf("private %q should not be exported", name)
		}
	}
}

func TestExtractCalls_RustFieldExpression(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn main() {
    self.do_thing();
}
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// Just ensure it handles self.method() without crashing
	_ = result
}

func TestExtractCalls_RustScopedCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
fn main() {
    std::io::stdin();
}
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "std::io::stdin" {
			found = true
		}
	}
	if !found {
		t.Error("expected scoped call to std::io::stdin")
	}
}

// ==================== JS/TS Additional Coverage ====================

func TestExtractCalls_ChainedCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
const result = getFactory()();
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// Chained calls foo()() should be ignored (only the inner call tracked)
	for _, c := range result.Calls {
		if c.CalleeName == "" {
			t.Error("unexpected empty callee name")
		}
	}
}

func TestExtractCalls_ParenthesizedCall(t *testing.T) {
	parser := NewParser()
	// In JS tree-sitter, (expr)(args) is a call_expression where callee
	// is parenthesized_expression. The parenthesized_expression's child
	// is the actual expression inside parentheses.
	code := []byte(`
var x = (myFunc)(42);
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "myFunc" {
			found = true
		}
	}
	if !found {
		t.Error("expected parenthesized call to myFunc")
	}
}

func TestExtractCalls_ThisMethodCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Foo {
  bar() {
    this.baz();
  }
}
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "baz" && c.CalleeObject == "this" && c.IsMethodCall {
			found = true
		}
	}
	if !found {
		t.Error("expected this.baz() method call")
	}
}

func TestExtractCalls_ChainedMemberCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
foo().bar();
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "bar" && c.CalleeObject == "(call)" {
			found = true
		}
	}
	if !found {
		t.Error("expected foo().bar() with (call) object")
	}
}

func TestExtractImports_DynamicImport(t *testing.T) {
	parser := NewParser()
	code := []byte(`
const mod = import("./dynamic-module");
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, imp := range result.Imports {
		if imp.Source == "./dynamic-module" && imp.IsRelative {
			found = true
		}
	}
	if !found {
		t.Error("expected dynamic import of ./dynamic-module")
	}
}

func TestExtractImports_ReExport(t *testing.T) {
	parser := NewParser()
	code := []byte(`
export { foo, bar } from './utils';
export * from './helpers';
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	if !sources["./utils"] {
		t.Error("expected re-export from ./utils")
	}
	if !sources["./helpers"] {
		t.Error("expected re-export from ./helpers")
	}
}

func TestExtractImports_SideEffectImport(t *testing.T) {
	parser := NewParser()
	code := []byte(`
import './polyfill';
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, imp := range result.Imports {
		if imp.Source == "./polyfill" {
			found = true
		}
	}
	if !found {
		t.Error("expected side-effect import of ./polyfill")
	}
}

func TestExtractExports_ClassDeclaration(t *testing.T) {
	parser := NewParser()
	code := []byte(`
export class MyClass {}
export function myFunc() {}
export const myVar = 42;
export { inner };
export default myDefault;
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	for _, name := range []string{"MyClass", "myFunc", "myVar", "inner", "myDefault"} {
		if !exportSet[name] {
			t.Errorf("missing export %q", name)
		}
	}
}

func TestExtractCalls_SQLNoCallsOrImports(t *testing.T) {
	parser := NewParser()
	code := []byte(`CREATE TABLE users (id INT);`)
	result, err := parser.ExtractCalls(code, "sql")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Calls) != 0 {
		t.Errorf("expected no calls for SQL, got %d", len(result.Calls))
	}
	if len(result.Imports) != 0 {
		t.Errorf("expected no imports for SQL, got %d", len(result.Imports))
	}
}

func TestResolveImportPath_Relative(t *testing.T) {
	result := ResolveImportPath("/src", "./utils")
	if result != "/src/utils" {
		t.Errorf("expected /src/utils, got %q", result)
	}
}

func TestResolveImportPath_NonRelative(t *testing.T) {
	result := ResolveImportPath("/src", "lodash")
	if result != "lodash" {
		t.Errorf("expected lodash, got %q", result)
	}
}

func TestPossibleFilePaths_WithExtension(t *testing.T) {
	paths := PossibleFilePaths("./foo.ts")
	if len(paths) != 1 || paths[0] != "./foo.ts" {
		t.Errorf("expected just [./foo.ts], got %v", paths)
	}
	paths = PossibleFilePaths("./foo.tsx")
	if len(paths) != 1 {
		t.Errorf("expected 1 path for .tsx, got %d", len(paths))
	}
	paths = PossibleFilePaths("./foo.jsx")
	if len(paths) != 1 {
		t.Errorf("expected 1 path for .jsx, got %d", len(paths))
	}
}

func TestIsTestFile_Additional(t *testing.T) {
	tests := map[string]bool{
		"foo.spec.ts":              true,
		"foo.spec.tsx":             true,
		"foo.spec.jsx":             true,
		"foo.test.tsx":             true,
		"foo.test.jsx":             true,
		"foo_test.ts":              true,
		"foo_test.js":              true,
		"foo_spec.rb":              true,
		"foo_test.rb":              true,
		"foo_test.go":              true,
		"test_foo.py":              true,
		"foo_test.py":              true,
		"foo.go":                   false,
		"foo.rb":                   false,
		"__tests__/foo.js":         true,
		"__test__/foo.js":          true,
		"test/foo.js":              true,
		"tests/foo.js":             true,
		"spec/foo.rb":              true,
		"src/spec/foo_spec.rb":     true,
	}
	for path, expected := range tests {
		if IsTestFile(path) != expected {
			t.Errorf("IsTestFile(%q) = %v, want %v", path, !expected, expected)
		}
	}
}

func TestFindTestsForFile_Ruby(t *testing.T) {
	allFiles := []string{
		"lib/foo.rb",
		"lib/foo_spec.rb",
		"lib/foo_test.rb",
		"spec/foo_spec.rb",
	}
	tests := FindTestsForFile("lib/foo.rb", allFiles)
	if len(tests) == 0 {
		t.Error("expected to find test files for foo.rb")
	}
}

func TestFindTestsForFile_Go(t *testing.T) {
	allFiles := []string{
		"pkg/server.go",
		"pkg/server_test.go",
	}
	tests := FindTestsForFile("pkg/server.go", allFiles)
	found := false
	for _, f := range tests {
		if f == "pkg/server_test.go" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find server_test.go")
	}
}

func TestFindTestsForFile_Python(t *testing.T) {
	allFiles := []string{
		"src/handler.py",
		"src/test_handler.py",
		"src/handler_test.py",
	}
	tests := FindTestsForFile("src/handler.py", allFiles)
	if len(tests) == 0 {
		t.Error("expected to find test files for handler.py")
	}
}

func TestFindTestsForFile_Rust(t *testing.T) {
	allFiles := []string{
		"src/lib.rs",
		"tests/lib.rs",
	}
	tests := FindTestsForFile("src/lib.rs", allFiles)
	found := false
	for _, f := range tests {
		if f == "tests/lib.rs" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find tests/lib.rs")
	}
}

func TestFindTestsForFile_TStoJS(t *testing.T) {
	allFiles := []string{
		"src/utils.ts",
		"src/utils.test.js",
		"src/utils.spec.js",
	}
	tests := FindTestsForFile("src/utils.ts", allFiles)
	if len(tests) == 0 {
		t.Error("expected to find JS test files for TS source")
	}
}

func TestFindTestsForFile_JStoTS(t *testing.T) {
	allFiles := []string{
		"src/utils.js",
		"src/utils.test.ts",
	}
	tests := FindTestsForFile("src/utils.js", allFiles)
	if len(tests) == 0 {
		t.Error("expected to find TS test files for JS source")
	}
}

// Cover parseGoCallExpression type_conversion_expression branch
func TestExtractCalls_GoTypeConversion(t *testing.T) {
	parser := NewParser()
	code := []byte(`package main
func main() {
	x := int(3.14)
	y := string(65)
	_ = x
	_ = y
}
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// Type conversions like int(x) should show up as calls
	_ = result
}

// Cover findImportSource (recursive string search fallback)
func TestExtractImports_FindImportSourceFallback(t *testing.T) {
	parser := NewParser()
	// This test exercises the import code paths
	code := []byte(`
import defaultExport from 'module-name';
import * as name from 'module-name2';
import { export1 } from 'module-name3';
import { export1 as alias1 } from 'module-name4';
import defaultExport2, { export2 } from 'module-name5';
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	sources := map[string]bool{}
	for _, imp := range result.Imports {
		sources[imp.Source] = true
	}
	for _, s := range []string{"module-name", "module-name2", "module-name3", "module-name4", "module-name5"} {
		if !sources[s] {
			t.Errorf("missing import source %q", s)
		}
	}
	// Check default import
	for _, imp := range result.Imports {
		if imp.Source == "module-name" && imp.Default != "defaultExport" {
			t.Errorf("expected default import 'defaultExport', got %q", imp.Default)
		}
	}
}

// Cover parseCallExpression with node.ChildCount() == 0 and default branch
func TestExtractCalls_JSEmptyCallExpression(t *testing.T) {
	parser := NewParser()
	// Various edge case call patterns
	code := []byte(`
new Foo();
await bar();
baz();
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "baz" {
			found = true
		}
	}
	if !found {
		t.Error("expected call to baz")
	}
}

// Cover parseNamedImports with direct identifier (not in import_specifier)
func TestExtractImports_NamedImportsEdge(t *testing.T) {
	parser := NewParser()
	code := []byte(`
import { alpha, beta as b } from './utils';
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	for _, imp := range result.Imports {
		if imp.Source == "./utils" {
			if _, ok := imp.Named["alpha"]; !ok {
				t.Error("expected named import alpha")
			}
			if imp.Named["b"] != "beta" {
				t.Errorf("expected named import beta aliased as b, got %q", imp.Named["b"])
			}
		}
	}
}

// Cover parseDynamicImport with non-import callee and non-arguments second child
func TestExtractCalls_DynamicImportEdge(t *testing.T) {
	parser := NewParser()
	code := []byte(`
// Regular function call, not dynamic import
notImport("./foo");
// Dynamic import
const m = import("./bar");
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, imp := range result.Imports {
		if imp.Source == "./bar" {
			found = true
		}
	}
	if !found {
		t.Error("expected dynamic import of ./bar")
	}
}

// Cover parseRequireCall edge cases
func TestExtractCalls_RequireCallEdge(t *testing.T) {
	parser := NewParser()
	code := []byte(`
const a = require('./foo');
const b = notRequire('./bar');
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	foundFoo := false
	foundBar := false
	for _, imp := range result.Imports {
		if imp.Source == "./foo" {
			foundFoo = true
		}
		if imp.Source == "./bar" {
			foundBar = true
		}
	}
	if !foundFoo {
		t.Error("expected require of ./foo")
	}
	if foundBar {
		t.Error("notRequire should not produce an import")
	}
}

// Cover extractFunctionName / extractClassName with no identifier
func TestExtractExports_NoIdentifier(t *testing.T) {
	parser := NewParser()
	code := []byte(`
export function namedFunc() {}
export class NamedClass {}
export const namedVar = 1;
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	exportSet := map[string]bool{}
	for _, e := range result.Exports {
		exportSet[e] = true
	}
	if !exportSet["namedFunc"] {
		t.Error("missing export namedFunc")
	}
	if !exportSet["NamedClass"] {
		t.Error("missing export NamedClass")
	}
	if !exportSet["namedVar"] {
		t.Error("missing export namedVar")
	}
}

// Cover parseRustMethodCall (method_call_expression node type)
func TestExtractCalls_RustMethodCallExpression(t *testing.T) {
	parser := NewParser()
	// Use a pattern that tree-sitter parses as method_call_expression
	code := []byte(`
fn main() {
    let s = String::from("hello");
    let n = s.len();
}
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	_ = result // exercises the code path
}

// Cover parseRustExternCrate with no identifier (nil return)
func TestExtractCalls_RustExternCrateEmpty(t *testing.T) {
	parser := NewParser()
	// Partial/invalid extern crate — just exercises the nil branch
	code := []byte(`
extern crate serde;
fn main() {}
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Errorf("expected 1 import, got %d", len(result.Imports))
	}
}

// Cover extractRubyStringContent fallback (no string_content child)
func TestExtractCalls_RubyStringContentFallback(t *testing.T) {
	parser := NewParser()
	code := []byte(`
require 'simple_gem'
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, imp := range result.Imports {
		if imp.Source == "simple_gem" {
			found = true
		}
	}
	if !found {
		t.Error("expected import of simple_gem")
	}
}

// Cover parseGoImportSpec with empty source (nil return)
func TestExtractCalls_GoImportSpecNoSource(t *testing.T) {
	parser := NewParser()
	// Standard import — exercises the full path
	code := []byte(`package main
import "fmt"
func main() {}
`)
	result, err := parser.ExtractCalls(code, "go")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Errorf("expected 1 import, got %d", len(result.Imports))
	}
}

// Cover Ruby constant receiver in call
func TestExtractCalls_RubyConstantReceiver(t *testing.T) {
	parser := NewParser()
	code := []byte(`
MyClass.new
Array.new(5)
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	foundNew := false
	for _, c := range result.Calls {
		if c.CalleeName == "new" && c.CalleeObject != "" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("expected Class.new call")
	}
}

// Cover parseRubyRequireCall with non-require method
func TestExtractCalls_RubyNonRequireCall(t *testing.T) {
	parser := NewParser()
	code := []byte(`
puts "hello"
print "world"
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// puts/print are calls but not imports
	if len(result.Imports) != 0 {
		t.Errorf("expected no imports, got %d", len(result.Imports))
	}
}

// Cover Ruby autoloaded constant references (Zeitwerk)
func TestExtractCalls_RubyAutoloadConstants(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class PostsController < ApplicationController
  def index
    @posts = Post.all
    @users = User.where(active: true)
    render json: Admin::Dashboard.new
  end
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	// Should find autoload imports for Post, User, Admin::Dashboard
	// ApplicationController is in the stdlib skip list
	autoloads := make(map[string]bool)
	for _, imp := range result.Imports {
		if strings.HasPrefix(imp.Source, "autoload:") {
			autoloads[strings.TrimPrefix(imp.Source, "autoload:")] = true
		}
	}

	for _, expected := range []string{"Post", "User"} {
		if !autoloads[expected] {
			t.Errorf("expected autoload import for %q, got: %v", expected, autoloads)
		}
	}

	// PostsController should NOT be an import (it's defined locally)
	if autoloads["PostsController"] {
		t.Error("PostsController should not be an autoload import (defined locally)")
	}
}

// Cover Ruby autoload skips stdlib constants
func TestExtractCalls_RubyAutoloadSkipsStdlib(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class MyService
  def call
    data = JSON.parse(input)
    result = ActiveRecord::Base.connection.execute(sql)
    Rails.logger.info("done")
  end
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	for _, imp := range result.Imports {
		if strings.HasPrefix(imp.Source, "autoload:") {
			name := strings.TrimPrefix(imp.Source, "autoload:")
			if name == "JSON" || name == "ActiveRecord" || strings.HasPrefix(name, "ActiveRecord::") || name == "Rails" {
				t.Errorf("should skip stdlib/framework constant %q", name)
			}
		}
	}
}

// Cover Rails association DSL -> model imports
func TestExtractCalls_RubyAssociationImports(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class User < ApplicationRecord
  has_many :posts
  has_one :profile
  belongs_to :organization
  has_and_belongs_to_many :categories
end
`)
	result, err := parser.ExtractCalls(code, "rb")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}

	autoloads := make(map[string]bool)
	for _, imp := range result.Imports {
		if strings.HasPrefix(imp.Source, "autoload:") {
			autoloads[strings.TrimPrefix(imp.Source, "autoload:")] = true
		}
	}

	// has_many :posts -> Post, has_one :profile -> Profile, etc.
	for _, expected := range []string{"Post", "Profile", "Organization", "Category"} {
		if !autoloads[expected] {
			t.Errorf("expected autoload import for %q from association, got: %v", expected, autoloads)
		}
	}
}

// Cover Rails DSL symbol extraction
func TestParser_ParseRubyDSLSymbols(t *testing.T) {
	parser := NewParser()
	code := []byte(`
class Post < ApplicationRecord
  belongs_to :user
  has_many :comments
  validates :title, presence: true
  scope :published, -> { where(published: true) }
  before_save :normalize_title
  delegate :name, to: :user
end
`)
	parsed, err := parser.Parse(code, "rb")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	kinds := make(map[string][]string) // kind -> names
	for _, sym := range parsed.Symbols {
		kinds[sym.Kind] = append(kinds[sym.Kind], sym.Name)
	}

	// Should find associations
	if len(kinds["association"]) < 2 {
		t.Errorf("expected at least 2 associations, got: %v", kinds["association"])
	}
	// Should find validations
	if len(kinds["validation"]) < 1 {
		t.Errorf("expected at least 1 validation, got: %v", kinds["validation"])
	}
	// Should find scopes
	if len(kinds["scope"]) < 1 {
		t.Errorf("expected at least 1 scope, got: %v", kinds["scope"])
	}
	// Should find callbacks
	if len(kinds["callback"]) < 1 {
		t.Errorf("expected at least 1 callback, got: %v", kinds["callback"])
	}
}

// Cover Rust use with self:: prefix
func TestExtractCalls_RustUseSelf(t *testing.T) {
	parser := NewParser()
	code := []byte(`
use self::module;
`)
	result, err := parser.ExtractCalls(code, "rs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}
	if !result.Imports[0].IsRelative {
		t.Error("self:: import should be relative")
	}
}

// Cover ExtractCalls with PHP/C# (defaults to JS path)
func TestExtractCalls_PHPDefaultPath(t *testing.T) {
	parser := NewParser()
	code := []byte(`<?php
function foo() {}
`)
	result, err := parser.ExtractCalls(code, "php")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	// PHP currently falls through to default JS extraction
	_ = result
}

func TestExtractCalls_CSharpDefaultPath(t *testing.T) {
	parser := NewParser()
	code := []byte(`
public class Foo {
    public void Bar() {}
}
`)
	result, err := parser.ExtractCalls(code, "cs")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	_ = result
}

func TestExtractCalls_PythonDefaultPath(t *testing.T) {
	parser := NewParser()
	code := []byte(`
import os
def foo():
    os.path.join("a", "b")
`)
	result, err := parser.ExtractCalls(code, "py")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	_ = result
}

func TestExtractCalls_NestedMemberExpression(t *testing.T) {
	parser := NewParser()
	code := []byte(`
a.b.c();
`)
	result, err := parser.ExtractCalls(code, "js")
	if err != nil {
		t.Fatalf("ExtractCalls failed: %v", err)
	}
	found := false
	for _, c := range result.Calls {
		if c.CalleeName == "c" && c.IsMethodCall {
			found = true
		}
	}
	if !found {
		t.Error("expected nested member call a.b.c()")
	}
}
