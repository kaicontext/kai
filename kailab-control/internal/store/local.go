package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
