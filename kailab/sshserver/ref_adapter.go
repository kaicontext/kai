package sshserver

import (
	"context"
	"database/sql"
	"fmt"

	"kailab/store"
)

// DBRefAdapter maps Kai refs to git refs and builds commit objects from snapshots.
type DBRefAdapter struct {
	db *sql.DB
}

// NewDBRefAdapter returns a ref adapter backed by the Kai store.
func NewDBRefAdapter(db *sql.DB) *DBRefAdapter {
	return &DBRefAdapter{db: db}
}

func (a *DBRefAdapter) BuildRefCommits(ctx context.Context) (map[string]RefCommitInfo, map[string]string, error) {
	return buildRefCommits(a.db)
}

func (a *DBRefAdapter) ListRefs(ctx context.Context) ([]GitRef, string, error) {
	refs, err := store.ListRefs(a.db, "")
	if err != nil {
		return nil, "", err
	}
	if len(refs) == 0 {
		return nil, "", nil
	}

	_, refToOID, err := buildRefCommits(a.db)
	if err != nil {
		return nil, "", err
	}

	mapped := make([]*store.Ref, 0, len(refs))
	gitRefs := make([]GitRef, 0, len(refs))
	for _, ref := range refs {
		name := mapRefName(ref.Name)
		oid, ok := refToOID[name]
		if !ok {
			continue
		}
		mapped = append(mapped, &store.Ref{Name: name, Target: ref.Target})
		gitRefs = append(gitRefs, GitRef{Name: name, OID: oid})
	}

	if len(gitRefs) == 0 {
		return nil, "", fmt.Errorf("no resolvable refs")
	}
	headRef := selectHeadRef(mapped)
	return gitRefs, headRef, nil
}
