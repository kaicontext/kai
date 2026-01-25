package sshserver

import (
	"context"
	"database/sql"
	"encoding/hex"
	"testing"

	"kai-core/cas"
	"kailab/repo"
	"kailab/store"
)

func TestDBRefAdapterListRefs(t *testing.T) {
	tmpDir := t.TempDir()
	reg := repo.NewRegistry(repo.RegistryConfig{DataDir: tmpDir})
	defer reg.Close()

	handle, err := reg.Create(context.Background(), "test", "repo")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	reg.Acquire(handle)
	defer reg.Release(handle)

	if err := seedRepoForRefAdapter(handle.DB); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	adapter := NewDBRefAdapter(handle.DB)
	refs, headRef, err := adapter.ListRefs(context.Background())
	if err != nil {
		t.Fatalf("list refs: %v", err)
	}
	if headRef != "refs/heads/main" {
		t.Fatalf("unexpected head ref: %s", headRef)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Name != "refs/heads/main" {
		t.Fatalf("unexpected ref name: %s", refs[0].Name)
	}
	if len(refs[0].OID) != 40 {
		t.Fatalf("expected 40-hex oid, got %q", refs[0].OID)
	}
}

func seedRepoForRefAdapter(db *sql.DB) error {
	content := []byte("hello\n")
	contentDigest := cas.Blake3Hash(content)

	filePayload := map[string]interface{}{
		"path":   "README.md",
		"digest": hex.EncodeToString(contentDigest),
		"lang":   "txt",
	}
	fileDigest, err := cas.NodeID("File", filePayload)
	if err != nil {
		return err
	}
	fileContent, err := cas.CanonicalJSON(filePayload)
	if err != nil {
		return err
	}
	fileObject := append([]byte("File\n"), fileContent...)

	snapshotPayload := map[string]interface{}{
		"sourceType":  "dir",
		"sourceRef":   "seed",
		"fileCount":   1,
		"fileDigests": []string{hex.EncodeToString(fileDigest)},
		"files": []map[string]interface{}{
			{
				"path":          "README.md",
				"lang":          "txt",
				"digest":        hex.EncodeToString(fileDigest),
				"contentDigest": hex.EncodeToString(contentDigest),
			},
		},
		"createdAt": cas.NowMs(),
	}
	snapshotDigest, err := cas.NodeID("Snapshot", snapshotPayload)
	if err != nil {
		return err
	}
	snapshotContent, err := cas.CanonicalJSON(snapshotPayload)
	if err != nil {
		return err
	}
	snapshotObject := append([]byte("Snapshot\n"), snapshotContent...)

	segment := append([]byte{}, content...)
	offsetContent := int64(0)
	segment = append(segment, fileObject...)
	offsetFile := int64(len(content))
	segment = append(segment, snapshotObject...)
	offsetSnap := int64(len(content) + len(fileObject))
	checksum := cas.Blake3Hash(segment)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	segmentID, err := store.InsertSegmentTx(tx, checksum, segment)
	if err != nil {
		return err
	}
	if err := store.InsertObjectTx(tx, contentDigest, segmentID, offsetContent, int64(len(content)), "blob"); err != nil {
		return err
	}
	if err := store.InsertObjectTx(tx, fileDigest, segmentID, offsetFile, int64(len(fileObject)), "File"); err != nil {
		return err
	}
	if err := store.InsertObjectTx(tx, snapshotDigest, segmentID, offsetSnap, int64(len(snapshotObject)), "Snapshot"); err != nil {
		return err
	}
	if err := store.ForceSetRef(db, tx, "snap.main", snapshotDigest, "seed", "seed"); err != nil {
		return err
	}

	return tx.Commit()
}
