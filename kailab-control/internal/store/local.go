package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalStore implements Store using the local filesystem.
type LocalStore struct {
	root string
}

// NewLocalStore creates a new local filesystem store rooted at the given directory.
func NewLocalStore(root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) path(key string) string {
	return filepath.Join(s.root, key)
}

func (s *LocalStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(s.path(key))
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	err := os.Remove(s.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalStore) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(s.path(key))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *LocalStore) ListByPrefix(_ context.Context, prefix string) ([]string, error) {
	searchDir := filepath.Dir(s.path(prefix))
	prefixBase := filepath.Base(s.path(prefix))

	type keyTime struct {
		key     string
		modTime int64
	}
	var results []keyTime

	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}
		// Get the key relative to store root
		relPath, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		// Check if this key starts with the prefix
		if strings.HasPrefix(relPath, prefix) || strings.HasPrefix(filepath.Base(path), prefixBase) {
			results = append(results, keyTime{key: relPath, modTime: info.ModTime().UnixNano()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by most recently modified first
	sort.Slice(results, func(i, j int) bool {
		return results[i].modTime > results[j].modTime
	})

	keys := make([]string, len(results))
	for i, r := range results {
		keys[i] = r.key
	}
	return keys, nil
}
