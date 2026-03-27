// Package dirio provides directory-based file source operations.
package dirio

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"lukechampine.com/blake3"

	"kai/internal/filesource"
	"kai/internal/ignore"
)

// DirectorySource reads files from a filesystem directory.
type DirectorySource struct {
	rootPath   string
	files      []*filesource.FileInfo
	identifier string
	ignore     *ignore.Matcher
	statCache  *StatCache
}

// Option configures a DirectorySource.
type Option func(*DirectorySource)

// WithIgnore sets a custom ignore matcher.
func WithIgnore(m *ignore.Matcher) Option {
	return func(ds *DirectorySource) {
		ds.ignore = m
	}
}

// WithStatCache provides a stat cache to skip reading unchanged files.
func WithStatCache(sc *StatCache) Option {
	return func(ds *DirectorySource) {
		ds.statCache = sc
	}
}

// OpenDirectory opens a directory as a file source.
// Options can be passed to configure behavior (e.g., WithIgnore, WithStatCache).
func OpenDirectory(dirPath string, opts ...Option) (*DirectorySource, error) {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("getting absolute path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absPath)
	}

	ds := &DirectorySource{rootPath: absPath}

	// Apply options
	for _, opt := range opts {
		opt(ds)
	}

	// If no ignore matcher provided, load from directory
	if ds.ignore == nil {
		ds.ignore, err = ignore.LoadFromDir(absPath)
		if err != nil {
			return nil, fmt.Errorf("loading ignore patterns: %w", err)
		}
	}

	// Walk directory and collect files
	if err := ds.collectFiles(); err != nil {
		return nil, err
	}

	// Compute content hash identifier
	ds.computeIdentifier()

	// If using stat cache, prune entries for deleted files and save
	if ds.statCache != nil {
		currentPaths := make(map[string]bool, len(ds.files))
		for _, f := range ds.files {
			currentPaths[f.Path] = true
		}
		ds.statCache.Prune(currentPaths)
	}

	return ds, nil
}

// GetFiles returns all supported source files.
func (ds *DirectorySource) GetFiles() ([]*filesource.FileInfo, error) {
	return ds.files, nil
}

// GetFile returns a specific file by path.
func (ds *DirectorySource) GetFile(path string) (*filesource.FileInfo, error) {
	for _, f := range ds.files {
		if f.Path == path {
			return f, nil
		}
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

// Identifier returns the content hash of all files.
func (ds *DirectorySource) Identifier() string {
	return ds.identifier
}

// SourceType returns "directory".
func (ds *DirectorySource) SourceType() string {
	return "directory"
}

// fileEntry holds walk results before parallel reading.
type fileEntry struct {
	absPath string
	relPath string
	lang    string
	info    fs.FileInfo
}

// collectFiles walks the directory using WalkDir (avoids extra Stat per entry),
// then reads file contents in parallel using a worker pool.
// If a stat cache is available, unchanged files skip the read entirely.
func (ds *DirectorySource) collectFiles() error {
	// Phase 1: Walk directory tree, collecting entries (no file reads yet).
	var entries []fileEntry

	err := filepath.WalkDir(ds.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(ds.rootPath, path)
		if err != nil {
			return fmt.Errorf("getting relative path: %w", err)
		}
		relPath = filepath.ToSlash(relPath)

		if ds.ignore != nil && ds.ignore.Match(relPath, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		lang := detectLang(path)
		if lang == "" {
			return nil
		}

		// Only call Info() (which does a Stat) when we don't have a stat cache,
		// or when we need to check if the file changed. We always need it for
		// the stat cache lookup, so just get it.
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat file %s: %w", path, err)
		}

		entries = append(entries, fileEntry{
			absPath: path,
			relPath: relPath,
			lang:    lang,
			info:    info,
		})

		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory: %w", err)
	}

	// Phase 2: Read file contents in parallel.
	files := make([]*filesource.FileInfo, len(entries))
	errs := make([]error, len(entries))

	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	work := make(chan int, len(entries))

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				e := entries[i]

				// Check stat cache — if mtime+size match, we can skip the read.
				if ds.statCache != nil {
					if cachedDigest, cachedLang, ok := ds.statCache.Lookup(e.relPath, e.info); ok {
						// File unchanged — we still need content for snapshot creation,
						// but we can note the cache hit. For now, we must read anyway
						// because FileInfo.Content is required by downstream consumers.
						// However, if the cached lang differs (shouldn't happen), use detected.
						lang := e.lang
						if cachedLang != "" {
							lang = cachedLang
						}
						_ = cachedDigest // Will be used when snapshot can accept digests directly.
						content, err := os.ReadFile(e.absPath)
						if err != nil {
							errs[i] = fmt.Errorf("reading file %s: %w", e.absPath, err)
							continue
						}
						files[i] = &filesource.FileInfo{
							Path:    e.relPath,
							Content: content,
							Lang:    lang,
						}
						continue
					}
				}

				content, err := os.ReadFile(e.absPath)
				if err != nil {
					errs[i] = fmt.Errorf("reading file %s: %w", e.absPath, err)
					continue
				}

				files[i] = &filesource.FileInfo{
					Path:    e.relPath,
					Content: content,
					Lang:    e.lang,
				}

				// Update stat cache with the new file's info.
				if ds.statCache != nil {
					digest := fmt.Sprintf("%x", blake3.Sum256(content))
					ds.statCache.Update(e.relPath, e.info, digest, e.lang)
				}
			}
		}()
	}

	for i := range entries {
		work <- i
	}
	close(work)
	wg.Wait()

	// Check for errors.
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	// Filter out any nil entries (shouldn't happen, but be safe).
	result := make([]*filesource.FileInfo, 0, len(files))
	for _, f := range files {
		if f != nil {
			result = append(result, f)
		}
	}

	ds.files = result
	return nil
}

// computeIdentifier computes a BLAKE3 hash of all file paths and contents.
func (ds *DirectorySource) computeIdentifier() {
	// Sort files by path for deterministic ordering
	sortedFiles := make([]*filesource.FileInfo, len(ds.files))
	copy(sortedFiles, ds.files)
	sort.Slice(sortedFiles, func(i, j int) bool {
		return sortedFiles[i].Path < sortedFiles[j].Path
	})

	hasher := blake3.New(32, nil)

	for _, f := range sortedFiles {
		hasher.Write([]byte(f.Path))
		hasher.Write([]byte("\n"))
		hasher.Write(f.Content)
		hasher.Write([]byte("\n"))
	}

	ds.identifier = fmt.Sprintf("%x", hasher.Sum(nil))
}

// detectLang detects the language based on file extension.
func detectLang(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	// JavaScript/TypeScript
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "js"
	// Structured data
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	// Documentation
	case ".md", ".markdown":
		return "markdown"
	case ".txt", ".text":
		return "text"
	// Config
	case ".ini", ".cfg", ".conf":
		return "ini"
	case ".env":
		return "env"
	// Other code (tracked but no semantic analysis yet)
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".sql":
		return "sql"
	case ".html", ".htm":
		return "html"
	case ".css", ".scss", ".sass", ".less":
		return "css"
	default:
		return "blob"
	}
}
