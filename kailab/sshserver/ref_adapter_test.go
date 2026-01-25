package sshserver

import (
	"context"
	"testing"

	"kailab/repo"
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

	if err := seedTestRepo(handle.DB); err != nil {
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
