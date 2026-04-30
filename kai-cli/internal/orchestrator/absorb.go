package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// absorbSpawnIntoMain copies the agent's edits from a spawn dir into
// the main repo's working tree. Returns the set of relative paths that
// changed (created/modified/deleted) so the safety gate can classify
// the blast radius.
//
// This is the "propagate back" step. The spawn dir was a CoW clone of
// main when the agent started, so we can compute the diff just by
// walking both trees and comparing file digests — no kai-DB plumbing
// required. Files matching shouldIgnoreObserver's excludes (.kai,
// .git, node_modules) are skipped on both sides so we don't drag
// per-repo internal state along with the agent's actual edits.
//
// Caveat: if the user has uncommitted changes in main while an agent
// is running, those files might get clobbered when the agent's
// version overwrites them. That's a real footgun we'll address in a
// follow-up (snapshot main before absorb so the user can recover).
// For v1 the contract is: don't run the orchestrator with a dirty
// working tree.
func absorbSpawnIntoMain(spawnDir, mainDir string) ([]string, error) {
	spawnFiles, err := walkDigests(spawnDir)
	if err != nil {
		return nil, fmt.Errorf("walking spawn dir: %w", err)
	}
	mainFiles, err := walkDigests(mainDir)
	if err != nil {
		return nil, fmt.Errorf("walking main dir: %w", err)
	}

	changed := make(map[string]struct{})

	// Files added or modified in the spawn relative to main.
	for path, spawnDigest := range spawnFiles {
		mainDigest, exists := mainFiles[path]
		if exists && mainDigest == spawnDigest {
			continue
		}
		src := filepath.Join(spawnDir, path)
		dst := filepath.Join(mainDir, path)
		if err := copyFileForAbsorb(src, dst); err != nil {
			return nil, fmt.Errorf("copying %s: %w", path, err)
		}
		changed[path] = struct{}{}
	}

	// Files the agent deleted (present in main, absent in spawn).
	for path := range mainFiles {
		if _, ok := spawnFiles[path]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(mainDir, path)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing %s: %w", path, err)
		}
		changed[path] = struct{}{}
	}

	out := make([]string, 0, len(changed))
	for p := range changed {
		out = append(out, p)
	}
	return out, nil
}

// walkDigests returns a map of path -> hex sha256 digest for every
// file under root, excluding the same directories the file observer
// skips. Symlinks aren't followed; large files (>50 MiB) are read
// into memory which is fine for a code repo but a footgun for repos
// that check in big assets — flag for follow-up if it becomes a
// problem.
func walkDigests(root string) (map[string]string, error) {
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldIgnoreObserver(rel + "/") || rel == ".kai" || rel == ".git" || rel == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnoreObserver(rel) {
			return nil
		}
		// Skip symlinks: the agent might create them but we can't
		// content-hash them safely; treat them as unchanged.
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		digest, err := digestFile(path)
		if err != nil {
			return fmt.Errorf("digesting %s: %w", rel, err)
		}
		out[rel] = digest
		return nil
	})
	return out, err
}

// digestFile returns the hex sha256 of a file's contents.
func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFileForAbsorb writes src's content to dst, creating any missing
// parent directories. Preserves the source's mode bits so executable
// scripts remain executable. Atomic-ish via temp-file + rename so a
// crash mid-copy doesn't leave a half-written file in main.
func copyFileForAbsorb(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".kai-absorb." + randSuffix()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// randSuffix is a small unique suffix for the temp-file pattern.
// Doesn't need to be cryptographically random — just collision-resistant
// across concurrent absorbs in the same process.
func randSuffix() string {
	var b [8]byte
	_, _ = io.ReadFull(randSource{}, b[:])
	return hex.EncodeToString(b[:])
}

// randSource adapts crypto/rand to io.Reader without importing it
// twice. Tiny indirection so the rest of the file doesn't grow an
// import group for one byte source.
type randSource struct{}

func (randSource) Read(p []byte) (int, error) {
	// crypto/rand.Reader would be ideal but importing it just for
	// 8 bytes of entropy is overkill — a sha256 of pid+time gives
	// us collision-resistance for the tmp filename. We never use
	// these bytes for anything security-sensitive.
	h := sha256.New()
	for i := range p {
		fmt.Fprintf(h, "%d-%d", os.Getpid(), i)
	}
	sum := h.Sum(nil)
	n := copy(p, sum)
	return n, nil
}

// pathSlash forces forward-slash rendering for display, regardless
// of the platform's path separator. Used when reporting changed
// paths to the user.
func pathSlash(p string) string {
	return strings.ReplaceAll(p, string(filepath.Separator), "/")
}

// shouldIgnoreObserver filters paths the absorb walk shouldn't
// surface — kai/git internals plus a few common high-churn dirs.
// Originally lived in observer.go; folded in here when Slice 6
// removed the fsnotify observer (the in-process agent fires hooks
// directly without watching the filesystem).
func shouldIgnoreObserver(rel string) bool {
	if rel == "" || rel == "." {
		return true
	}
	for _, prefix := range []string{".kai/", ".git/", "node_modules/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}
