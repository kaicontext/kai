// Package filesource provides abstractions for reading source files from different sources.
package filesource

// FileInfo contains information about a source file.
type FileInfo struct {
	Path    string
	Content []byte
	Lang    string // "ts", "js", or empty
}

// FileSource abstracts the source of files (Git, filesystem, etc.).
type FileSource interface {
	// GetFiles returns all supported source files.
	GetFiles() ([]*FileInfo, error)

	// GetFile returns a specific file by path.
	GetFile(path string) (*FileInfo, error)

	// Identifier returns a unique identifier for this source state.
	// For Git: commit hash. For directories: content hash.
	Identifier() string

	// SourceType returns the type of source ("git" or "directory").
	SourceType() string
}
