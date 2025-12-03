package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRepoDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kailab-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := OpenRepoDB(tmpDir, "test-tenant", "test-repo")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Check that the directory structure was created
	expectedPath := filepath.Join(tmpDir, "test-tenant", "test-repo", "kailab.db")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected database file at %s", expectedPath)
	}
}

func TestRefOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kailab-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := OpenRepoDB(tmpDir, "test", "repo")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Test SetRefFF with new ref
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	target := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	err = db.SetRefFF(tx, "snap.main", nil, target, "testuser", "push-123")
	if err != nil {
		t.Fatalf("failed to set ref: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Test GetRef
	ref, err := db.GetRef("snap.main")
	if err != nil {
		t.Fatalf("failed to get ref: %v", err)
	}
	if ref == nil {
		t.Fatal("expected ref to exist")
	}
	if ref.Name != "snap.main" {
		t.Errorf("expected name 'snap.main', got %s", ref.Name)
	}
	if ref.Actor != "testuser" {
		t.Errorf("expected actor 'testuser', got %s", ref.Actor)
	}

	// Test ref mismatch (should fail)
	tx2, _ := db.BeginTx()
	wrongTarget := []byte{99, 99, 99, 99}
	err = db.SetRefFF(tx2, "snap.main", wrongTarget, target, "testuser", "push-456")
	if err != ErrRefMismatch {
		t.Errorf("expected ErrRefMismatch, got %v", err)
	}
	tx2.Rollback()

	// Test ListRefs
	refs, err := db.ListRefs("")
	if err != nil {
		t.Fatalf("failed to list refs: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 ref, got %d", len(refs))
	}

	// Test GetRefHistory
	history, err := db.GetRefHistory("", 0, 100)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(history))
	}
}

func TestObjectOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kailab-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := OpenRepoDB(tmpDir, "test", "repo")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Insert a segment
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	checksum := []byte{1, 2, 3, 4}
	blob := []byte("hello world")
	segmentID, err := db.InsertSegment(tx, checksum, blob)
	if err != nil {
		t.Fatalf("failed to insert segment: %v", err)
	}

	// Insert an object
	digest := []byte{10, 20, 30, 40, 50, 60, 70, 80}
	err = db.InsertObject(tx, digest, segmentID, 0, int64(len(blob)), "test")
	if err != nil {
		t.Fatalf("failed to insert object: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Test GetObject
	info, err := db.GetObject(digest)
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}
	if info.SegmentID != segmentID {
		t.Errorf("expected segment %d, got %d", segmentID, info.SegmentID)
	}

	// Test HasObject
	has, err := db.HasObject(digest)
	if err != nil {
		t.Fatalf("failed to check object: %v", err)
	}
	if !has {
		t.Error("expected object to exist")
	}

	// Test ReadObjectContent
	content, err := db.ReadObjectContent(digest)
	if err != nil {
		t.Fatalf("failed to read content: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got %s", string(content))
	}
}

func TestEnrichQueue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kailab-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := OpenRepoDB(tmpDir, "test", "repo")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Enqueue an item
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	nodeID := []byte{1, 2, 3, 4}
	err = db.EnqueueForEnrichment(tx, nodeID, "Snapshot")
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}
	tx.Commit()

	// Claim the item
	item, err := db.ClaimEnrichmentItem()
	if err != nil {
		t.Fatalf("failed to claim: %v", err)
	}
	if item == nil {
		t.Fatal("expected item to be claimed")
	}
	if item.Kind != "Snapshot" {
		t.Errorf("expected kind 'Snapshot', got %s", item.Kind)
	}
	if item.Status != "processing" {
		t.Errorf("expected status 'processing', got %s", item.Status)
	}

	// Complete the item
	err = db.CompleteEnrichmentItem(item.ID, "")
	if err != nil {
		t.Fatalf("failed to complete: %v", err)
	}

	// Should return nil when queue is empty
	item2, err := db.ClaimEnrichmentItem()
	if err != nil {
		t.Fatalf("failed to claim: %v", err)
	}
	if item2 != nil {
		t.Error("expected nil when queue is empty")
	}
}
