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
		name := MapRefName(ref.Name)
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

	// Sort refs: refs/heads/* first (with main/master at very top), then others
	sortRefs(gitRefs)

	headRef := selectHeadRef(mapped)

	// Add explicit HEAD ref pointing to the same commit as headRef
	if headRef != "" {
		for _, ref := range gitRefs {
			if ref.Name == headRef {
				// Insert HEAD at the beginning
				headGitRef := GitRef{Name: "HEAD", OID: ref.OID}
				gitRefs = append([]GitRef{headGitRef}, gitRefs...)
				break
			}
		}
	}

	return gitRefs, headRef, nil
}

func sortRefs(refs []GitRef) {
	// Simple bubble sort to prioritize heads refs
	for i := 0; i < len(refs); i++ {
		for j := i + 1; j < len(refs); j++ {
			if refPriority(refs[j].Name) < refPriority(refs[i].Name) {
				refs[i], refs[j] = refs[j], refs[i]
			}
		}
	}
}

func refPriority(name string) int {
	switch {
	case name == "refs/heads/main":
		return 0
	case name == "refs/heads/master":
		return 1
	case len(name) > 11 && name[:11] == "refs/heads/":
		return 2
	case len(name) > 10 && name[:10] == "refs/tags/":
		return 3
	default:
		return 4
	}
}
