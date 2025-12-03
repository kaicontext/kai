// Package dirio provides directory-based file source operations.
package dirio

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
}

// Option configures a DirectorySource.
type Option func(*DirectorySource)

// WithIgnore sets a custom ignore matcher.
func WithIgnore(m *ignore.Matcher) Option {
	return func(ds *DirectorySource) {
		ds.ignore = m
	}
}

// OpenDirectory opens a directory as a file source.
// Options can be passed to configure behavior (e.g., WithIgnore).
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

// collectFiles walks the directory and collects all TS/JS files.
func (ds *DirectorySource) collectFiles() error {
	var files []*filesource.FileInfo

	err := filepath.Walk(ds.rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Make path relative to root
		relPath, err := filepath.Rel(ds.rootPath, path)
		if err != nil {
			return fmt.Errorf("getting relative path: %w", err)
		}

		// Normalize path separators to forward slashes
		relPath = filepath.ToSlash(relPath)

		// Skip ignored paths
		if ds.ignore != nil && ds.ignore.Match(relPath, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (but continue walking into them)
		if info.IsDir() {
			return nil
		}

		// Check if it's a supported file type
		lang := detectLang(path)
		if lang == "" {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading file %s: %w", path, err)
		}

		files = append(files, &filesource.FileInfo{
			Path:    relPath,
			Content: content,
			Lang:    lang,
		})

		return nil
	})

	if err != nil {
		return fmt.Errorf("walking directory: %w", err)
	}

	ds.files = files
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
		// Hash path + newline + content + newline
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
