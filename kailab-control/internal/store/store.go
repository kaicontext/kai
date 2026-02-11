// Package store provides a storage interface for CI artifacts and caches.
package store

import (
	"context"
	"io"
)

// Store is the interface for CI blob storage (caches, artifacts).
type Store interface {
	// Put writes data to the given key.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Get returns a reader for the given key.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the given key.
	Delete(ctx context.Context, key string) error
	// Exists checks if the given key exists.
	Exists(ctx context.Context, key string) (bool, error)
	// ListByPrefix returns all keys that start with the given prefix,
	// sorted in reverse chronological order (most recently modified first).
	ListByPrefix(ctx context.Context, prefix string) ([]string, error)
}
