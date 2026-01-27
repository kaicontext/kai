package sshserver

import (
	"bytes"
	"context"
	"testing"

	"kailab/repo"
)

func TestPackBuilderWritesPack(t *testing.T) {
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

	store := NewMemoryObjectStore()
	builder := NewPackBuilder(refAdapter, store)

	var buf bytes.Buffer
	_, err = builder.BuildPack(context.Background(), PackRequest{Wants: []string{refs[0].OID}}, &buf)
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if buf.Len() < 4 || string(buf.Bytes()[:4]) != "PACK" {
		t.Fatalf("expected PACK header")
	}
	if !store.Has(refs[0].OID) {
		t.Fatalf("expected commit to be cached")
	}
}

func TestPackBuilderEmptyWants(t *testing.T) {
	builder := NewPackBuilder(NewDBRefAdapter(nil), NewMemoryObjectStore())
	var buf bytes.Buffer

	_, err := builder.BuildPack(context.Background(), PackRequest{}, &buf)
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if buf.Len() < 4 || string(buf.Bytes()[:4]) != "PACK" {
		t.Fatalf("expected PACK header")
	}
}

func TestPackBuilderSkipsHaves(t *testing.T) {
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

	store := NewMemoryObjectStore()
	builder := NewPackBuilder(refAdapter, store)

	var buf bytes.Buffer
	want := refs[0].OID
	_, err = builder.BuildPack(context.Background(), PackRequest{
		Wants: []string{want},
		Haves: []string{want},
	}, &buf)
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if buf.Len() < 4 || string(buf.Bytes()[:4]) != "PACK" {
		t.Fatalf("expected PACK header")
	}
	if store.Has(want) {
		t.Fatalf("expected want to be skipped due to have")
	}
}

func TestPackBuilderShallowClone(t *testing.T) {
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

	store := NewMemoryObjectStore()
	builder := NewPackBuilder(refAdapter, store)

	var buf bytes.Buffer
	result, err := builder.BuildPack(context.Background(), PackRequest{
		Wants: []string{refs[0].OID},
		Depth: 1, // Shallow clone
	}, &buf)
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if buf.Len() < 4 || string(buf.Bytes()[:4]) != "PACK" {
		t.Fatalf("expected PACK header")
	}
	// Result should be non-nil (may or may not have shallow commits depending on data)
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
}
