//go:build integration

package sshserver

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"kai-core/cas"
	"kailab/repo"
	"kailab/store"
)

func TestSSHUploadPackClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	reg := repo.NewRegistry(repo.RegistryConfig{DataDir: tmpDir})
	defer reg.Close()

	handle, err := reg.Create(context.Background(), "test", "repo")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	reg.Acquire(handle)
	defer reg.Release(handle)

	if err := seedRepo(handle.DB); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := StartWithListener(listener, NewGitHandler(reg, nil), nil)
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	defer Stop(context.Background(), srv)

	cloneDir := filepath.Join(tmpDir, "clone")
	port := listener.Addr().(*net.TCPAddr).Port
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p %d", port)
	cmd := exec.Command("git", "clone", "ssh://git@"+listener.Addr().String()+"/test/repo", cloneDir)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, string(out))
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

func seedRepo(db *sql.DB) error {
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

	checksum := make([]byte, 32)
	if _, err := rand.Read(checksum); err != nil {
		return err
	}

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
