package sshserver

import (
	"context"
	"testing"

	"kailab/repo"
)

func TestDBSnapshotAdapterSnapshotObjects(t *testing.T) {
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

	refAdapter := NewDBRefAdapter(handle.DB)
	refs, _, err := refAdapter.ListRefs(context.Background())
	if err != nil {
		t.Fatalf("list refs: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("expected refs")
	}

	refCommits, _, err := refAdapter.BuildRefCommits(context.Background())
	if err != nil {
		t.Fatalf("build ref commits: %v", err)
	}

	info, ok := refCommits[refs[0].OID]
	if !ok {
		t.Fatalf("missing commit for ref %s", refs[0].Name)
	}

	if info.Commit.Type != ObjectCommit {
		t.Fatalf("expected commit type, got %d", info.Commit.Type)
	}
	if len(info.Objects) == 0 {
		t.Fatalf("expected snapshot objects")
	}
}
