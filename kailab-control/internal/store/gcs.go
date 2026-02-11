package store

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// GCSStore implements Store using Google Cloud Storage.
type GCSStore struct {
	client *storage.Client
	bucket string
	prefix string
}

// NewGCSStore creates a new GCS-backed store.
func NewGCSStore(ctx context.Context, bucket, prefix string) (*GCSStore, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &GCSStore{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *GCSStore) key(k string) string {
	if s.prefix != "" {
		return s.prefix + "/" + k
	}
	return k
}

func (s *GCSStore) Put(ctx context.Context, key string, r io.Reader, _ int64) error {
	w := s.client.Bucket(s.bucket).Object(s.key(key)).NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (s *GCSStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.client.Bucket(s.bucket).Object(s.key(key)).NewReader(ctx)
}

func (s *GCSStore) Delete(ctx context.Context, key string) error {
	err := s.client.Bucket(s.bucket).Object(s.key(key)).Delete(ctx)
	if err == storage.ErrObjectNotExist {
		return nil
	}
	return err
}

func (s *GCSStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.Bucket(s.bucket).Object(s.key(key)).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *GCSStore) ListByPrefix(ctx context.Context, prefix string) ([]string, error) {
	fullPrefix := s.key(prefix)
	it := s.client.Bucket(s.bucket).Objects(ctx, &storage.Query{Prefix: fullPrefix})

	type keyTime struct {
		key     string
		updated time.Time
	}
	var results []keyTime

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		// Strip the store prefix to return keys relative to the store
		relKey := attrs.Name
		if s.prefix != "" {
			relKey = strings.TrimPrefix(relKey, s.prefix+"/")
		}
		results = append(results, keyTime{key: relKey, updated: attrs.Updated})
	}

	// Sort by most recently updated first
	sort.Slice(results, func(i, j int) bool {
		return results[i].updated.After(results[j].updated)
	})

	keys := make([]string, len(results))
	for i, r := range results {
		keys[i] = r.key
	}
	return keys, nil
}
