package sshserver

import (
	"bytes"
	"testing"
)

func TestGenerateDelta_SmallChange(t *testing.T) {
	src := []byte("The quick brown fox jumps over the lazy dog. This is a test of the delta encoding system.")
	tgt := []byte("The quick brown cat jumps over the lazy dog. This is a test of the delta encoding system.")

	delta := GenerateDelta(src, tgt)
	if delta == nil {
		t.Fatal("expected delta to be generated")
	}

	// Delta should be smaller than target
	if len(delta)+20 >= len(tgt) {
		t.Fatalf("delta too large: %d bytes (target is %d)", len(delta), len(tgt))
	}

	// Verify delta can reconstruct target
	result, err := applyDelta(src, delta)
	if err != nil {
		t.Fatalf("failed to apply delta: %v", err)
	}
	if !bytes.Equal(result, tgt) {
		t.Fatalf("reconstructed data doesn't match target:\ngot:  %q\nwant: %q", result, tgt)
	}
}

func TestGenerateDelta_AppendedData(t *testing.T) {
	src := []byte("Hello, this is the original content of the file.")
	tgt := []byte("Hello, this is the original content of the file. And here is some new content appended to it.")

	delta := GenerateDelta(src, tgt)
	if delta == nil {
		t.Fatal("expected delta to be generated")
	}

	result, err := applyDelta(src, delta)
	if err != nil {
		t.Fatalf("failed to apply delta: %v", err)
	}
	if !bytes.Equal(result, tgt) {
		t.Fatalf("reconstructed data doesn't match target")
	}
}

func TestGenerateDelta_PrependedData(t *testing.T) {
	src := []byte("This is the original content of the file that we are testing.")
	tgt := []byte("HEADER: This is the original content of the file that we are testing.")

	delta := GenerateDelta(src, tgt)
	if delta == nil {
		t.Fatal("expected delta to be generated")
	}

	result, err := applyDelta(src, delta)
	if err != nil {
		t.Fatalf("failed to apply delta: %v", err)
	}
	if !bytes.Equal(result, tgt) {
		t.Fatalf("reconstructed data doesn't match target")
	}
}

func TestGenerateDelta_NoCommonData(t *testing.T) {
	src := []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	tgt := []byte("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	delta := GenerateDelta(src, tgt)
	// Should return nil because delta wouldn't save space
	if delta != nil && len(delta)+20 < len(tgt) {
		// If a delta was generated, verify it works
		result, err := applyDelta(src, delta)
		if err != nil {
			t.Fatalf("failed to apply delta: %v", err)
		}
		if !bytes.Equal(result, tgt) {
			t.Fatalf("reconstructed data doesn't match target")
		}
	}
}

func TestGenerateDelta_IdenticalContent(t *testing.T) {
	src := []byte("This content is exactly the same in both source and target versions.")
	tgt := []byte("This content is exactly the same in both source and target versions.")

	delta := GenerateDelta(src, tgt)
	if delta == nil {
		t.Fatal("expected delta to be generated for identical content")
	}

	// Delta should be very small for identical content
	if len(delta) >= len(tgt)/2 {
		t.Fatalf("delta too large for identical content: %d bytes", len(delta))
	}

	result, err := applyDelta(src, delta)
	if err != nil {
		t.Fatalf("failed to apply delta: %v", err)
	}
	if !bytes.Equal(result, tgt) {
		t.Fatalf("reconstructed data doesn't match target")
	}
}

func TestGenerateDelta_TooSmall(t *testing.T) {
	src := []byte("short")
	tgt := []byte("tiny")

	delta := GenerateDelta(src, tgt)
	if delta != nil {
		t.Fatal("expected nil delta for very small content")
	}
}

func TestDeltaEncodeSize(t *testing.T) {
	tests := []struct {
		size int
		want []byte
	}{
		{0, []byte{0}},
		{1, []byte{1}},
		{127, []byte{127}},
		{128, []byte{0x80, 1}},
		{255, []byte{0xff, 1}},
		{256, []byte{0x80, 2}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 1}},
	}

	for _, tt := range tests {
		got := deltaEncodeSize(tt.size)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("deltaEncodeSize(%d) = %v, want %v", tt.size, got, tt.want)
		}
	}
}

func TestEncodeCopy(t *testing.T) {
	// Test basic copy
	result := encodeCopy(0, 16)
	if result[0]&0x80 == 0 {
		t.Error("copy instruction should have high bit set")
	}

	// Large offset
	result = encodeCopy(0x12345678, 100)
	if len(result) < 5 {
		t.Errorf("expected at least 5 bytes for large offset, got %d", len(result))
	}
}

func TestEncodeInsert(t *testing.T) {
	data := []byte("hello")
	result := encodeInsert(data)
	if len(result) != 6 {
		t.Errorf("expected 6 bytes (1 length + 5 data), got %d", len(result))
	}
	if result[0] != 5 {
		t.Errorf("expected length byte 5, got %d", result[0])
	}
	if !bytes.Equal(result[1:], data) {
		t.Error("insert data mismatch")
	}
}

func BenchmarkGenerateDelta(b *testing.B) {
	// Create realistic test data - source code with small changes
	src := bytes.Repeat([]byte("func example() {\n\treturn 42\n}\n"), 100)
	tgt := bytes.Repeat([]byte("func example() {\n\treturn 43\n}\n"), 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateDelta(src, tgt)
	}
}

func TestWritePackRefDelta(t *testing.T) {
	// Create a source and target blob
	srcData := []byte("The quick brown fox jumps over the lazy dog. This is some sample content for testing delta encoding.")
	tgtData := []byte("The quick brown cat jumps over the lazy dog. This is some sample content for testing delta encoding.")

	srcOID := computeGitOID("blob", srcData)
	delta := GenerateDelta(srcData, tgtData)
	if delta == nil {
		t.Fatal("expected delta to be generated")
	}

	var buf bytes.Buffer
	err := writePackRefDelta(&buf, srcOID, delta)
	if err != nil {
		t.Fatalf("writePackRefDelta failed: %v", err)
	}

	// Verify the output has the right structure:
	// - Header byte with type 7 (REF_DELTA)
	// - 20-byte base OID
	// - zlib-compressed delta

	data := buf.Bytes()
	if len(data) < 21 {
		t.Fatalf("output too short: %d bytes", len(data))
	}

	// Check header byte type
	headerType := (data[0] >> 4) & 0x07
	if headerType != 7 {
		t.Errorf("expected type 7 (REF_DELTA), got %d", headerType)
	}
}

func TestWritePackWithDeltas(t *testing.T) {
	// Create test objects
	blob1 := GitObject{
		Type: ObjectBlob,
		Data: []byte("Original content that will be used as a base."),
		OID:  computeGitOID("blob", []byte("Original content that will be used as a base.")),
	}
	blob2 := GitObject{
		Type: ObjectBlob,
		Data: []byte("Modified content that will be used as a base."),
		OID:  computeGitOID("blob", []byte("Modified content that will be used as a base.")),
	}

	delta := GenerateDelta(blob1.Data, blob2.Data)

	candidates := []DeltaCandidate{
		{Object: blob1}, // Full object
		{Object: blob2, BaseOID: blob1.OID, Delta: delta}, // Delta
	}

	var buf bytes.Buffer
	err := writePackWithDeltas(&buf, candidates)
	if err != nil {
		t.Fatalf("writePackWithDeltas failed: %v", err)
	}

	// Verify pack header
	data := buf.Bytes()
	if string(data[:4]) != "PACK" {
		t.Error("missing PACK header")
	}

	// Version should be 2
	version := uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	if version != 2 {
		t.Errorf("expected version 2, got %d", version)
	}

	// Object count should be 2
	count := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	if count != 2 {
		t.Errorf("expected 2 objects, got %d", count)
	}
}
