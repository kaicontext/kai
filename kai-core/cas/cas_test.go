package cas

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestNowMs(t *testing.T) {
	// Just verify it returns a reasonable timestamp (after year 2024)
	ts := NowMs()
	// Year 2024 in milliseconds is approximately 1704067200000
	if ts < 1704067200000 {
		t.Errorf("NowMs() returned %d, expected timestamp after 2024", ts)
	}
}

func TestCanonicalJSON_SimpleObject(t *testing.T) {
	input := map[string]interface{}{
		"z": 1,
		"a": 2,
		"m": 3,
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	// Keys should be sorted alphabetically
	expected := `{"a":2,"m":3,"z":1}`
	if string(result) != expected {
		t.Errorf("expected %s, got %s", expected, string(result))
	}
}

func TestCanonicalJSON_NestedObject(t *testing.T) {
	input := map[string]interface{}{
		"z": map[string]interface{}{
			"b": 1,
			"a": 2,
		},
		"a": 3,
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	// Both outer and inner keys should be sorted
	expected := `{"a":3,"z":{"a":2,"b":1}}`
	if string(result) != expected {
		t.Errorf("expected %s, got %s", expected, string(result))
	}
}

func TestCanonicalJSON_Array(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{"z": 1, "a": 2},
		map[string]interface{}{"b": 3, "a": 4},
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	// Array order preserved, object keys sorted
	expected := `[{"a":2,"z":1},{"a":4,"b":3}]`
	if string(result) != expected {
		t.Errorf("expected %s, got %s", expected, string(result))
	}
}

func TestCanonicalJSON_Primitives(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"string", "hello", `"hello"`},
		{"number", 42, "42"},
		{"float", 3.14, "3.14"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"null", nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CanonicalJSON(tt.input)
			if err != nil {
				t.Fatalf("CanonicalJSON failed: %v", err)
			}
			if string(result) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(result))
			}
		})
	}
}

func TestCanonicalJSON_Deterministic(t *testing.T) {
	// Run multiple times to ensure deterministic output
	input := map[string]interface{}{
		"c": 1,
		"a": 2,
		"b": 3,
	}

	var previous string
	for i := 0; i < 10; i++ {
		result, err := CanonicalJSON(input)
		if err != nil {
			t.Fatalf("CanonicalJSON failed: %v", err)
		}

		if previous != "" && string(result) != previous {
			t.Errorf("non-deterministic output: got %s, previous was %s", string(result), previous)
		}
		previous = string(result)
	}
}

func TestBlake3Hash(t *testing.T) {
	input := []byte("hello world")
	hash := Blake3Hash(input)

	// Blake3 produces 32-byte hash
	if len(hash) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(hash))
	}

	// Same input should produce same hash
	hash2 := Blake3Hash(input)
	if string(hash) != string(hash2) {
		t.Error("same input produced different hashes")
	}

	// Different input should produce different hash
	hash3 := Blake3Hash([]byte("different input"))
	if string(hash) == string(hash3) {
		t.Error("different inputs produced same hash")
	}
}

func TestBlake3HashHex(t *testing.T) {
	input := []byte("hello world")
	hashHex := Blake3HashHex(input)

	// 32 bytes = 64 hex characters
	if len(hashHex) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hashHex))
	}

	// Verify it's valid hex
	_, err := hex.DecodeString(hashHex)
	if err != nil {
		t.Errorf("invalid hex output: %v", err)
	}

	// Should match Blake3Hash
	hash := Blake3Hash(input)
	if hashHex != hex.EncodeToString(hash) {
		t.Error("Blake3HashHex doesn't match Blake3Hash")
	}
}

func TestNodeID(t *testing.T) {
	kind := "file"
	payload := map[string]interface{}{
		"path": "test.js",
		"lang": "js",
	}

	id, err := NodeID(kind, payload)
	if err != nil {
		t.Fatalf("NodeID failed: %v", err)
	}

	if len(id) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(id))
	}

	// Same inputs should produce same ID
	id2, err := NodeID(kind, payload)
	if err != nil {
		t.Fatalf("NodeID failed: %v", err)
	}
	if string(id) != string(id2) {
		t.Error("same inputs produced different IDs")
	}

	// Different kind should produce different ID
	id3, err := NodeID("symbol", payload)
	if err != nil {
		t.Fatalf("NodeID failed: %v", err)
	}
	if string(id) == string(id3) {
		t.Error("different kinds produced same ID")
	}

	// Different payload should produce different ID
	payload2 := map[string]interface{}{
		"path": "other.js",
		"lang": "js",
	}
	id4, err := NodeID(kind, payload2)
	if err != nil {
		t.Fatalf("NodeID failed: %v", err)
	}
	if string(id) == string(id4) {
		t.Error("different payloads produced same ID")
	}
}

func TestNodeID_PayloadOrdering(t *testing.T) {
	kind := "test"

	// Different key orderings should produce same ID
	payload1 := map[string]interface{}{
		"a": 1,
		"b": 2,
		"c": 3,
	}
	payload2 := map[string]interface{}{
		"c": 3,
		"a": 1,
		"b": 2,
	}

	id1, _ := NodeID(kind, payload1)
	id2, _ := NodeID(kind, payload2)

	if string(id1) != string(id2) {
		t.Error("payload ordering affected NodeID")
	}
}

func TestNodeIDHex(t *testing.T) {
	kind := "file"
	payload := map[string]interface{}{"path": "test.js"}

	idHex, err := NodeIDHex(kind, payload)
	if err != nil {
		t.Fatalf("NodeIDHex failed: %v", err)
	}

	// 32 bytes = 64 hex characters
	if len(idHex) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(idHex))
	}

	// Should match NodeID
	id, _ := NodeID(kind, payload)
	if idHex != hex.EncodeToString(id) {
		t.Error("NodeIDHex doesn't match NodeID")
	}
}

func TestHexToBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []byte
		wantErr  bool
	}{
		{
			name:     "valid hex",
			input:    "48656c6c6f",
			expected: []byte("Hello"),
			wantErr:  false,
		},
		{
			name:     "empty",
			input:    "",
			expected: []byte{},
			wantErr:  false,
		},
		{
			name:    "invalid hex",
			input:   "not hex",
			wantErr: true,
		},
		{
			name:    "odd length",
			input:   "123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := HexToBytes(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(result) != string(tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestBytesToHex(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "simple",
			input:    []byte("Hello"),
			expected: "48656c6c6f",
		},
		{
			name:     "empty",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "binary",
			input:    []byte{0x00, 0xff, 0x10},
			expected: "00ff10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BytesToHex(tt.input)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestHexRoundTrip(t *testing.T) {
	original := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0xff, 0xfe, 0xfd}

	hexStr := BytesToHex(original)
	roundTrip, err := HexToBytes(hexStr)
	if err != nil {
		t.Fatalf("HexToBytes failed: %v", err)
	}

	if string(original) != string(roundTrip) {
		t.Errorf("round trip failed: original %v, got %v", original, roundTrip)
	}
}

func TestCanonicalJSON_ComplexStructure(t *testing.T) {
	input := map[string]interface{}{
		"meta": map[string]interface{}{
			"version": 1,
			"author":  "test",
		},
		"data": []interface{}{
			map[string]interface{}{"id": 1, "name": "first"},
			map[string]interface{}{"id": 2, "name": "second"},
		},
		"active": true,
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	// Verify it's valid JSON
	var parsed interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}

	// Verify keys are sorted
	expected := `{"active":true,"data":[{"id":1,"name":"first"},{"id":2,"name":"second"}],"meta":{"author":"test","version":1}}`
	if string(result) != expected {
		t.Errorf("expected %s, got %s", expected, string(result))
	}
}

func TestCanonicalJSON_EmptyStructures(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"empty object", map[string]interface{}{}, "{}"},
		{"empty array", []interface{}{}, "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CanonicalJSON(tt.input)
			if err != nil {
				t.Fatalf("CanonicalJSON failed: %v", err)
			}
			if string(result) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(result))
			}
		})
	}
}

func TestNodeID_SpecialCharacters(t *testing.T) {
	kind := "file"
	payload := map[string]interface{}{
		"path": "path/with spaces/and\"quotes",
		"data": "unicode: \u0000\u001f\u007f",
	}

	id, err := NodeID(kind, payload)
	if err != nil {
		t.Fatalf("NodeID failed: %v", err)
	}

	if len(id) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(id))
	}
}
