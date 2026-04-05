// Package dirio provides directory-based file source operations.
package dirio

import (
	"encoding/gob"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StatCache stores file stat metadata to skip re-reading unchanged files.
// Analogous to git's .git/index — if mtime+size match, the file hasn't changed.
type StatCache struct {
	Entries  map[string]*StatEntry `json:"entries"`
	DirTimes map[string]int64     `json:"dirTimes,omitempty"` // dir path -> mtime (UnixNano)
	mu       sync.RWMutex
}

// StatEntry holds cached stat info and content digest for one file.
type StatEntry struct {
	ModTime int64  `json:"mtime"`  // UnixNano
	Size    int64  `json:"size"`
	Digest  string `json:"digest"` // BLAKE3 hex digest of content
	Lang    string `json:"lang"`
}

// LoadStatCache loads the stat cache from disk, or returns an empty cache.
// Tries gob format first (fast), falls back to JSON (legacy).
func LoadStatCache(kaiDir string) *StatCache {
	sc := &StatCache{Entries: make(map[string]*StatEntry), DirTimes: make(map[string]int64)}

	// Try gob format first
	gobPath := filepath.Join(kaiDir, "statcache.gob")
	if f, err := os.Open(gobPath); err == nil {
		defer f.Close()
		if gob.NewDecoder(f).Decode(sc) == nil {
			if sc.Entries == nil {
				sc.Entries = make(map[string]*StatEntry)
			}
			if sc.DirTimes == nil {
				sc.DirTimes = make(map[string]int64)
			}
			return sc
		}
	}

	// Fall back to JSON (legacy migration)
	jsonPath := filepath.Join(kaiDir, "statcache.json")
	if data, err := os.ReadFile(jsonPath); err == nil {
		_ = json.Unmarshal(data, sc)
	}
	if sc.Entries == nil {
		sc.Entries = make(map[string]*StatEntry)
	}
	if sc.DirTimes == nil {
		sc.DirTimes = make(map[string]int64)
	}
	return sc
}

// Save writes the stat cache to disk in gob format.
// Removes legacy JSON file if present.
func (sc *StatCache) Save(kaiDir string) error {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	gobPath := filepath.Join(kaiDir, "statcache.gob")
	f, err := os.Create(gobPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := gob.NewEncoder(f).Encode(sc); err != nil {
		return err
	}

	// Clean up legacy JSON file
	os.Remove(filepath.Join(kaiDir, "statcache.json"))
	return nil
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
	cachedTime := time.Unix(0, entry.ModTime).Truncate(time.Microsecond)
	fileTime := info.ModTime().Truncate(time.Microsecond)
	if cachedTime.Equal(fileTime) && entry.Size == info.Size() {
		return entry.Digest, entry.Lang, true
	}
	return "", "", false
}

// LookupByPath checks if a file exists in the cache by path only.
func (sc *StatCache) LookupByPath(relPath string) (string, string, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	entry, ok := sc.Entries[relPath]
	if !ok {
		return "", "", false
	}
	return entry.Digest, entry.Lang, true
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

// DirUnchanged checks if a directory's mtime matches the cache.
func (sc *StatCache) DirUnchanged(relPath string, info os.FileInfo) bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	cached, ok := sc.DirTimes[relPath]
	if !ok {
		return false
	}
	cachedTime := time.Unix(0, cached).Truncate(time.Microsecond)
	dirTime := info.ModTime().Truncate(time.Microsecond)
	return cachedTime.Equal(dirTime)
}

// UpdateDir records a directory's mtime.
func (sc *StatCache) UpdateDir(relPath string, info os.FileInfo) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.DirTimes[relPath] = info.ModTime().UnixNano()
}


// PruneDirs removes directory entries that no longer exist.
func (sc *StatCache) PruneDirs(currentDirs map[string]bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for path := range sc.DirTimes {
		if !currentDirs[path] {
			delete(sc.DirTimes, path)
		}
	}
}
