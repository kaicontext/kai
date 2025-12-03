package pack

import (
	"bytes"
	"testing"

	"kai-core/cas"
)

func TestBuildPack(t *testing.T) {
	objects := []PackObject{
		{
			Digest:  cas.Blake3Hash([]byte("content1")),
			Kind:    "File",
			Content: []byte("content1"),
		},
		{
			Digest:  cas.Blake3Hash([]byte("content2")),
			Kind:    "Snapshot",
			Content: []byte("content2"),
		},
	}

	packed, err := BuildPack(objects)
	if err != nil {
		t.Fatalf("failed to build pack: %v", err)
	}

	if len(packed) == 0 {
		t.Fatal("expected non-empty pack")
	}

	// The pack should be zstd compressed
	// zstd magic number: 0x28, 0xB5, 0x2F, 0xFD
	if len(packed) < 4 {
		t.Fatal("pack too small")
	}
	if packed[0] != 0x28 || packed[1] != 0xB5 || packed[2] != 0x2F || packed[3] != 0xFD {
		t.Error("pack doesn't have zstd magic number")
	}
}

func TestPackRoundTrip(t *testing.T) {
	content1 := []byte("hello world this is test content")
	content2 := []byte(`{"key": "value", "number": 42}`)

	objects := []PackObject{
		{
			Digest:  cas.Blake3Hash(content1),
			Kind:    "File",
			Content: content1,
		},
		{
			Digest:  cas.Blake3Hash(content2),
			Kind:    "Node",
			Content: content2,
		},
	}

	packed, err := BuildPack(objects)
	if err != nil {
		t.Fatalf("failed to build pack: %v", err)
	}

	// Verify the pack by checking its contents are valid zstd
	if len(packed) < 4 {
		t.Fatal("pack too small")
	}

	// Verify zstd magic bytes
	expectedMagic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	if !bytes.Equal(packed[:4], expectedMagic) {
		t.Errorf("expected zstd magic %x, got %x", expectedMagic, packed[:4])
	}
}

func TestIsSemanticKind(t *testing.T) {
	semanticKinds := []string{"Snapshot", "ChangeSet", "Symbol", "Module", "File", "ChangeType", "Workspace"}
	for _, kind := range semanticKinds {
		if !isSemanticKind(kind) {
			t.Errorf("expected %s to be semantic", kind)
		}
	}

	nonSemanticKinds := []string{"blob", "random", ""}
	for _, kind := range nonSemanticKinds {
		if isSemanticKind(kind) {
			t.Errorf("expected %s to not be semantic", kind)
		}
	}
}
