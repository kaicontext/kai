// Package dirio provides directory-based file source operations.
package dirio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StatCache stores file stat metadata to skip re-reading unchanged files.
// Analogous to git's .git/index — if mtime+size match, the file hasn't changed.
type StatCache struct {
	Entries map[string]*StatEntry `json:"entries"`
	mu      sync.RWMutex
}

// StatEntry holds cached stat info and content digest for one file.
type StatEntry struct {
	ModTime int64  `json:"mtime"`  // UnixNano
	Size    int64  `json:"size"`
	Digest  string `json:"digest"` // BLAKE3 hex digest of content
	Lang    string `json:"lang"`
}

// LoadStatCache loads the stat cache from disk, or returns an empty cache.
func LoadStatCache(kaiDir string) *StatCache {
	sc := &StatCache{Entries: make(map[string]*StatEntry)}
	data, err := os.ReadFile(filepath.Join(kaiDir, "statcache.json"))
	if err != nil {
		return sc
	}
	_ = json.Unmarshal(data, sc)
	if sc.Entries == nil {
		sc.Entries = make(map[string]*StatEntry)
	}
	return sc
}

// Save writes the stat cache to disk.
func (sc *StatCache) Save(kaiDir string) error {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kaiDir, "statcache.json"), data, 0644)
}

// Lookup checks if a file's stat matches the cache. Returns the cached digest
// and true if the file is unchanged, or ("", false) if it needs re-reading.
func (sc *StatCache) Lookup(relPath string, info os.FileInfo) (string, string, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	entry, ok := sc.Entries[relPath]
	if !ok {
		return "", "", false
	}
	// Compare mtime (truncated to microseconds to avoid filesystem precision issues)
	// and size — same heuristic as git's ce_uptodate check.
	cachedTime := time.Unix(0, entry.ModTime).Truncate(time.Microsecond)
	fileTime := info.ModTime().Truncate(time.Microsecond)
	if cachedTime.Equal(fileTime) && entry.Size == info.Size() {
		return entry.Digest, entry.Lang, true
	}
	return "", "", false
}

// Update records a file's stat + digest in the cache.
func (sc *StatCache) Update(relPath string, info os.FileInfo, digest string, lang string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Entries[relPath] = &StatEntry{
		ModTime: info.ModTime().UnixNano(),
		Size:    info.Size(),
		Digest:  digest,
		Lang:    lang,
	}
}

// Prune removes entries for files that no longer exist in the given set.
func (sc *StatCache) Prune(currentPaths map[string]bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for path := range sc.Entries {
		if !currentPaths[path] {
			delete(sc.Entries, path)
		}
	}
}
