package sshserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kailab/pack"
	"kailab/repo"
	"kailab/store"
)

func TestReadReceivePackRequest(t *testing.T) {
	var buf bytes.Buffer
	if err := writePktLine(&buf, "0000000000000000000000000000000000000000 "+
		"1111111111111111111111111111111111111111 refs/heads/main\x00report-status\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writePktLine(&buf, "1111111111111111111111111111111111111111 "+
		"2222222222222222222222222222222222222222 refs/heads/dev\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writeFlush(&buf); err != nil {
		t.Fatalf("write flush: %v", err)
	}
	buf.WriteString("PACKDATA")
	reader := bufio.NewReader(&buf)

	req, err := readReceivePackRequest(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(req.Updates))
	}
	if req.Updates[0].Ref != "refs/heads/main" {
		t.Fatalf("unexpected ref: %s", req.Updates[0].Ref)
	}
	if string(req.Pack) != "PACKDATA" {
		t.Fatalf("unexpected pack: %q", string(req.Pack))
	}
}

func TestCreateChangeSetFromPackStoresUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	reg := repo.NewRegistry(repo.RegistryConfig{DataDir: tmpDir})
	defer reg.Close()

	handle, err := reg.Create(context.Background(), "test", "repo")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	reg.Acquire(handle)
	defer reg.Release(handle)

	req := &receivePackRequest{
		Updates: []receivePackUpdate{
			{Old: strings.Repeat("0", 40), New: strings.Repeat("1", 40), Ref: "refs/heads/main"},
		},
		Pack: []byte("PACKDATA"),
	}

	csDigest, _, err := createChangeSetFromPack(handle.DB, req)
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	ref, err := store.GetRef(handle.DB, "snap.main")
	if err != nil {
		t.Fatalf("get ref: %v", err)
	}
	if !bytes.Equal(ref.Target, csDigest) {
		t.Fatalf("expected ref target to be changeset")
	}

	content, kind, err := pack.ExtractObjectFromDB(handle.DB, csDigest)
	if err != nil {
		t.Fatalf("extract changeset: %v", err)
	}
	if kind != "ChangeSet" {
		t.Fatalf("expected ChangeSet, got %s", kind)
	}

	payload := content
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		payload = content[idx+1:]
	}
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	updates, ok := data["gitUpdates"].([]interface{})
	if !ok || len(updates) != 1 {
		t.Fatalf("expected 1 gitUpdates entry, got %v", data["gitUpdates"])
	}
	entry, ok := updates[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected update entry: %T", updates[0])
	}
	if entry["ref"] != "refs/heads/main" {
		t.Fatalf("unexpected ref: %v", entry["ref"])
	}
	if entry["new"] != strings.Repeat("1", 40) {
		t.Fatalf("unexpected new: %v", entry["new"])
	}
}
