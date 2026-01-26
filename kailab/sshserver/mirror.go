package sshserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"kailab/background"
	"kailab/pack"
	"kailab/repo"
	"kailab/store"
)

// MirrorConfig configures Kai->Git mirroring.
type MirrorConfig struct {
	Enabled    bool
	BaseDir    string
	AllowRepos []string
	Rollback   bool
	Logger     *log.Logger
}

// GitMirror mirrors Kai refs into a bare Git repository.
type GitMirror struct {
	cfg MirrorConfig
}

// NewGitMirror returns a configured Git mirror helper.
func NewGitMirror(cfg MirrorConfig) *GitMirror {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &GitMirror{cfg: cfg}
}

// SyncRefs mirrors the provided ref names into the Git mirror repo.
func (m *GitMirror) SyncRefs(ctx context.Context, h *repo.Handle, refNames []string) error {
	if m == nil || !m.cfg.Enabled || h == nil {
		return nil
	}
	if m.cfg.Rollback {
		m.cfg.Logger.Printf("git mirror rollback enabled; skipping sync for %s/%s", h.Tenant, h.Name)
		return nil
	}
	if !repoAllowed(m.cfg.AllowRepos, h.Tenant, h.Name) {
		return nil
	}
	if m.cfg.BaseDir == "" {
		return fmt.Errorf("git mirror base dir not configured")
	}

	repoPath, err := m.ensureRepo(h.Tenant, h.Name)
	if err != nil {
		return err
	}

	cache := make(map[string]snapshotObjects)
	seen := make(map[string]bool)
	for _, refName := range refNames {
		if refName == "" || seen[refName] {
			continue
		}
		seen[refName] = true

		ref, err := store.GetRef(h.DB, refName)
		if err != nil {
			if err == store.ErrRefNotFound {
				gitRef := MapRefName(refName)
				if delErr := m.deleteRef(repoPath, gitRef); delErr != nil {
					m.cfg.Logger.Printf("git mirror: delete ref %s failed in %s/%s: %v", gitRef, h.Tenant, h.Name, delErr)
				} else {
					m.cfg.Logger.Printf("git mirror delete: repo=%s/%s ref=%s git_ref=%s", h.Tenant, h.Name, refName, gitRef)
				}
				continue
			}
			m.cfg.Logger.Printf("git mirror: ref %s not found in %s/%s: %v", refName, h.Tenant, h.Name, err)
			continue
		}

		gitRef, commit, objects, sigStatus, signer, err := buildMirrorObjects(ctx, h.DB, ref, cache)
		if err != nil {
			m.cfg.Logger.Printf("git mirror: build error for %s in %s/%s: %v", refName, h.Tenant, h.Name, err)
			continue
		}

		for _, obj := range objects {
			if err := m.writeGitObject(repoPath, obj); err != nil {
				return err
			}
		}
		if err := m.writeGitObject(repoPath, commit); err != nil {
			return err
		}

		if err := m.updateRef(repoPath, gitRef, commit.OID); err != nil {
			return err
		}
		if err := m.verifyRef(repoPath, gitRef, commit.OID); err != nil {
			m.cfg.Logger.Printf("git mirror drift warning: %s %s -> %s (%v)", h.Name, gitRef, commit.OID, err)
		}

		sigInfo := sigStatus
		if signer != "" {
			sigInfo += " (" + signer + ")"
		}
		m.cfg.Logger.Printf("git mirror sync: repo=%s/%s ref=%s git_ref=%s commit=%s signature=%s actor=%s",
			h.Tenant, h.Name, refName, gitRef, commit.OID, sigInfo, ref.Actor)
	}

	return nil
}

// SyncAllRefs mirrors every ref in the repo.
func (m *GitMirror) SyncAllRefs(ctx context.Context, h *repo.Handle) error {
	if m == nil || h == nil {
		return nil
	}
	refs, err := store.ListRefs(h.DB, "")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return m.SyncRefs(ctx, h, names)
}

func repoAllowed(allow []string, tenant, repo string) bool {
	if len(allow) == 0 {
		return true
	}
	needle := tenant + "/" + repo
	for _, entry := range allow {
		entry = strings.TrimSpace(entry)
		if entry == needle {
			return true
		}
	}
	return false
}

func (m *GitMirror) ensureRepo(tenant, name string) (string, error) {
	repoPath := filepath.Join(m.cfg.BaseDir, tenant, name+".git")
	if _, err := os.Stat(filepath.Join(repoPath, "HEAD")); err == nil {
		return repoPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(repoPath), 0755); err != nil {
		return "", fmt.Errorf("create mirror dir: %w", err)
	}
	cmd := exec.Command("git", "init", "--bare", repoPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git init --bare: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return repoPath, nil
}

func (m *GitMirror) writeGitObject(repoPath string, obj GitObject) error {
	kind, err := gitObjectKind(obj.Type)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", repoPath, "hash-object", "-w", "-t", kind, "--stdin")
	cmd.Stdin = bytes.NewReader(obj.Data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git hash-object: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	oid := strings.TrimSpace(string(out))
	if oid != "" && oid != obj.OID {
		m.cfg.Logger.Printf("git mirror oid mismatch: expected %s got %s", obj.OID, oid)
	}
	return nil
}

func (m *GitMirror) updateRef(repoPath, refName, oid string) error {
	cmd := exec.Command("git", "-C", repoPath, "update-ref", refName, oid)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git update-ref %s: %w (%s)", refName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *GitMirror) deleteRef(repoPath, refName string) error {
	cmd := exec.Command("git", "-C", repoPath, "update-ref", "-d", refName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git update-ref -d %s: %w (%s)", refName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *GitMirror) verifyRef(repoPath, refName, expected string) error {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", refName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %w (%s)", refName, err, strings.TrimSpace(string(out)))
	}
	got := strings.TrimSpace(string(out))
	if got == "" || got != expected {
		return fmt.Errorf("expected %s got %s", expected, got)
	}
	return nil
}

func gitObjectKind(kind ObjectType) (string, error) {
	switch kind {
	case ObjectBlob:
		return "blob", nil
	case ObjectTree:
		return "tree", nil
	case ObjectCommit:
		return "commit", nil
	default:
		return "", fmt.Errorf("unsupported git object type %d", kind)
	}
}

func buildMirrorObjects(ctx context.Context, db *sql.DB, ref *store.Ref, cache map[string]snapshotObjects) (string, GitObject, []GitObject, string, string, error) {
	gitRef := MapRefName(ref.Name)
	targetHex := hex.EncodeToString(ref.Target)

	snapshotDigest, err := resolveSnapshotDigest(db, ref.Target)
	if err != nil {
		return "", GitObject{}, nil, "", "", err
	}

	snapshotAdapter := NewDBSnapshotAdapter(db)
	var objects snapshotObjects
	if snapshotDigest != nil {
		key := hex.EncodeToString(snapshotDigest)
		if cached, ok := cache[key]; ok {
			objects = cached
		} else {
			treeOID, objs, err := snapshotAdapter.SnapshotObjects(ctx, snapshotDigest)
			if err != nil {
				return "", GitObject{}, nil, "", "", err
			}
			cache[key] = snapshotObjects{treeOID: treeOID, objects: objs}
			objects = cache[key]
		}
	} else {
		objects = snapshotObjects{
			treeOID: emptyTreeOID,
			objects: []GitObject{buildEmptyTreeObject()},
		}
	}

	sigStatus, signer, err := changesetSignatureStatus(db, ref.Target)
	if err != nil {
		sigStatus = "error"
	}

	commit := buildCommitObject(gitRef, targetHex, objects.treeOID)
	return gitRef, commit, objects.objects, sigStatus, signer, nil
}

func changesetSignatureStatus(db *sql.DB, target []byte) (string, string, error) {
	if len(target) == 0 {
		return "unsigned", "", nil
	}

	content, kind, err := pack.ExtractObjectFromDB(db, target)
	if err != nil {
		return "error", "", err
	}
	if kind != "ChangeSet" {
		return "n/a", "", nil
	}

	payload, err := background.ParseObjectPayload(content)
	if err != nil {
		return "error", "", err
	}

	signer, _ := payload["signer"].(string)
	hasSig := payload["signature"] != nil
	ok, err := background.VerifyChangeSetSignature(payload)
	if err != nil {
		return "error", signer, err
	}
	if !hasSig {
		return "unsigned", signer, nil
	}
	if ok {
		return "verified", signer, nil
	}
	return "unverified", signer, nil
}
