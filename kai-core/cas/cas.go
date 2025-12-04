// Package cas provides content-addressable storage utilities including
// BLAKE3 hashing and canonical JSON serialization.
package cas

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"lukechampine.com/blake3"
)

// NowMs returns the current time in milliseconds since epoch.
func NowMs() int64 {
	return time.Now().UnixMilli()
}

// CanonicalJSON converts a value to canonical JSON (stable key ordering).
func CanonicalJSON(v interface{}) ([]byte, error) {
	// First marshal to get JSON representation
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Unmarshal into interface{} to process
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}

	// Re-marshal with sorted keys
	return canonicalMarshal(obj)
}

func canonicalMarshal(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		return marshalSortedMap(val)
	case []interface{}:
		return marshalArray(val)
	default:
		return json.Marshal(v)
	}
}

func marshalSortedMap(m map[string]interface{}) ([]byte, error) {
	// Get sorted keys
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}

		// Write key
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		// Write value (recursively canonical)
		valBytes, err := canonicalMarshal(m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func marshalArray(arr []interface{}) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')

	for i, v := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		valBytes, err := canonicalMarshal(v)
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}

	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// Blake3Hash computes a BLAKE3 hash of the input and returns it as bytes.
func Blake3Hash(data []byte) []byte {
	hash := blake3.Sum256(data)
	return hash[:]
}

// Blake3HashHex computes a BLAKE3 hash and returns it as a hex string.
func Blake3HashHex(data []byte) string {
	return hex.EncodeToString(Blake3Hash(data))
}

// NewBlake3Hasher returns a new streaming BLAKE3 hasher.
func NewBlake3Hasher() *blake3.Hasher {
	return blake3.New(32, nil)
}

// NodeID computes the content-addressed ID for a node: blake3(kind + "\n" + canonicalJSON(payload))
func NodeID(kind string, payload interface{}) ([]byte, error) {
	canonicalPayload, err := CanonicalJSON(payload)
	if err != nil {
		return nil, err
	}

	data := append([]byte(kind+"\n"), canonicalPayload...)
	return Blake3Hash(data), nil
}

// NodeIDHex computes the content-addressed ID and returns it as hex.
func NodeIDHex(kind string, payload interface{}) (string, error) {
	id, err := NodeID(kind, payload)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}

// HexToBytes converts a hex string to bytes.
func HexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// BytesToHex converts bytes to hex string.
func BytesToHex(b []byte) string {
	return hex.EncodeToString(b)
}
