package parse

import (
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
