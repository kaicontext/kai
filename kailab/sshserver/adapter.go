package sshserver

import (
	"context"
	"io"
)

// RefAdapter provides access to ref metadata and resolved git objects.
type RefAdapter interface {
	BuildRefCommits(ctx context.Context) (map[string]RefCommitInfo, map[string]string, error)
	ListRefs(ctx context.Context) ([]GitRef, string, error)
}

// SnapshotAdapter converts Kai snapshots/changesets into git objects.
type SnapshotAdapter interface {
	SnapshotObjects(ctx context.Context, snapshotDigest []byte) (treeOID string, objects []GitObject, err error)
}

// ObjectStore caches git objects by OID.
type ObjectStore interface {
	Get(oid string) (GitObject, bool)
	Has(oid string) bool
	Put(obj GitObject)
}

// PackBuilder assembles packfiles for git clients.
type PackBuilder interface {
	BuildPack(ctx context.Context, req PackRequest, w io.Writer) (*PackResult, error)
}
