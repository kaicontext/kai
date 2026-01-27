package sshserver

import (
	"testing"
)

func TestCollectCommitObjectsWithDepth(t *testing.T) {
	// Create a simple commit chain: child -> parent -> grandparent
	// Each commit has a tree
	// Use proper 40-character hex OIDs
	grandparentTreeOID := "1111111111111111111111111111111111111111"
	parentTreeOID := "2222222222222222222222222222222222222222"
	childTreeOID := "3333333333333333333333333333333333333333"
	grandparentCommitOID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	parentCommitOID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	childCommitOID := "cccccccccccccccccccccccccccccccccccccccc"

	grandparentTree := GitObject{
		Type: ObjectTree,
		OID:  grandparentTreeOID,
		Data: []byte{}, // Empty tree for simplicity
	}
	parentTree := GitObject{
		Type: ObjectTree,
		OID:  parentTreeOID,
		Data: []byte{},
	}
	childTree := GitObject{
		Type: ObjectTree,
		OID:  childTreeOID,
		Data: []byte{},
	}

	grandparentCommit := GitObject{
		Type: ObjectCommit,
		OID:  grandparentCommitOID,
		Data: []byte("tree " + grandparentTreeOID + "\nauthor Test <test@test.com> 0 +0000\ncommitter Test <test@test.com> 0 +0000\n\ngrandparent commit\n"),
	}
	parentCommit := GitObject{
		Type: ObjectCommit,
		OID:  parentCommitOID,
		Data: []byte("tree " + parentTreeOID + "\nparent " + grandparentCommitOID + "\nauthor Test <test@test.com> 0 +0000\ncommitter Test <test@test.com> 0 +0000\n\nparent commit\n"),
	}
	childCommit := GitObject{
		Type: ObjectCommit,
		OID:  childCommitOID,
		Data: []byte("tree " + childTreeOID + "\nparent " + parentCommitOID + "\nauthor Test <test@test.com> 0 +0000\ncommitter Test <test@test.com> 0 +0000\n\nchild commit\n"),
	}

	objects := map[string]GitObject{
		grandparentTree.OID:   grandparentTree,
		parentTree.OID:        parentTree,
		childTree.OID:         childTree,
		grandparentCommit.OID: grandparentCommit,
		parentCommit.OID:      parentCommit,
		childCommit.OID:       childCommit,
	}

	t.Run("depth=0 (unlimited)", func(t *testing.T) {
		collected, shallow := collectCommitObjectsWithDepth(objects, childCommitOID, 0)
		// Should collect all 6 objects (3 commits + 3 trees)
		if len(collected) != 6 {
			t.Errorf("expected 6 objects, got %d", len(collected))
		}
		// No shallow boundaries when unlimited
		if len(shallow) != 0 {
			t.Errorf("expected 0 shallow commits, got %d", len(shallow))
		}
	})

	t.Run("depth=1 (shallow)", func(t *testing.T) {
		collected, shallow := collectCommitObjectsWithDepth(objects, childCommitOID, 1)
		// Should collect only child commit + its tree (2 objects)
		if len(collected) != 2 {
			t.Errorf("expected 2 objects, got %d", len(collected))
		}
		// Child commit should be marked as shallow boundary
		if len(shallow) != 1 || shallow[0] != childCommitOID {
			t.Errorf("expected shallow=[%s], got %v", childCommitOID, shallow)
		}
	})

	t.Run("depth=2", func(t *testing.T) {
		collected, shallow := collectCommitObjectsWithDepth(objects, childCommitOID, 2)
		// Should collect child + parent commits + their trees (4 objects)
		if len(collected) != 4 {
			t.Errorf("expected 4 objects, got %d", len(collected))
		}
		// Parent commit should be marked as shallow boundary
		if len(shallow) != 1 || shallow[0] != parentCommitOID {
			t.Errorf("expected shallow=[%s], got %v", parentCommitOID, shallow)
		}
	})
}

func TestCollectCommitObjectsWithDepth_RootCommit(t *testing.T) {
	// A commit without parents should not be marked as shallow
	rootTreeOID := "dddddddddddddddddddddddddddddddddddddddd"
	rootCommitOID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	rootTree := GitObject{
		Type: ObjectTree,
		OID:  rootTreeOID,
		Data: []byte{},
	}
	rootCommit := GitObject{
		Type: ObjectCommit,
		OID:  rootCommitOID,
		Data: []byte("tree " + rootTreeOID + "\nauthor Test <test@test.com> 0 +0000\ncommitter Test <test@test.com> 0 +0000\n\nroot commit\n"),
	}

	objects := map[string]GitObject{
		rootTree.OID:   rootTree,
		rootCommit.OID: rootCommit,
	}

	collected, shallow := collectCommitObjectsWithDepth(objects, rootCommitOID, 1)
	// Should collect commit + tree
	if len(collected) != 2 {
		t.Errorf("expected 2 objects, got %d", len(collected))
	}
	// Root commit has no parents, so no shallow boundary
	if len(shallow) != 0 {
		t.Errorf("expected 0 shallow commits for root commit, got %d", len(shallow))
	}
}
