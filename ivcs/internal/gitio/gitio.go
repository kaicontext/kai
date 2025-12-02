// Package gitio provides Git repository I/O operations using go-git.
package gitio

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FileInfo contains information about a file in a commit.
type FileInfo struct {
	Path    string
	Content []byte
	Lang    string // "ts", "js", or empty
}

// Repository wraps a go-git repository.
type Repository struct {
	repo *git.Repository
	path string
}

// Open opens an existing Git repository.
func Open(repoPath string) (*Repository, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}
	return &Repository{repo: repo, path: repoPath}, nil
}

// ResolveRef resolves a git reference (branch name, tag, or commit hash) to a commit.
func (r *Repository) ResolveRef(refName string) (*object.Commit, error) {
	// Try as a branch first
	ref, err := r.repo.Reference(plumbing.NewBranchReferenceName(refName), true)
	if err == nil {
		commit, err := r.repo.CommitObject(ref.Hash())
		if err != nil {
			return nil, fmt.Errorf("getting commit: %w", err)
		}
		return commit, nil
	}

	// Try as a tag
	ref, err = r.repo.Reference(plumbing.NewTagReferenceName(refName), true)
	if err == nil {
		commit, err := r.repo.CommitObject(ref.Hash())
		if err != nil {
			return nil, fmt.Errorf("getting commit: %w", err)
		}
		return commit, nil
	}

	// Try as a commit hash
	hash := plumbing.NewHash(refName)
	commit, err := r.repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("resolving ref %q: not a branch, tag, or commit hash", refName)
	}
	return commit, nil
}

// GetTreeFiles returns all files in the tree at a given commit.
func (r *Repository) GetTreeFiles(commit *object.Commit) ([]*FileInfo, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("getting tree: %w", err)
	}

	var files []*FileInfo
	err = tree.Files().ForEach(func(f *object.File) error {
		// Only process TS/JS files
		lang := detectLang(f.Name)
		if lang == "" {
			return nil
		}

		content, err := f.Contents()
		if err != nil {
			return fmt.Errorf("reading file %s: %w", f.Name, err)
		}

		files = append(files, &FileInfo{
			Path:    f.Name,
			Content: []byte(content),
			Lang:    lang,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// GetFile returns a specific file from a commit.
func (r *Repository) GetFile(commit *object.Commit, path string) (*FileInfo, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("getting tree: %w", err)
	}

	f, err := tree.File(path)
	if err != nil {
		return nil, fmt.Errorf("getting file %s: %w", path, err)
	}

	reader, err := f.Reader()
	if err != nil {
		return nil, fmt.Errorf("opening file %s: %w", path, err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}

	return &FileInfo{
		Path:    path,
		Content: content,
		Lang:    detectLang(path),
	}, nil
}

// DiffFiles returns the paths of files that differ between two commits.
func (r *Repository) DiffFiles(baseCommit, headCommit *object.Commit) (added, modified, deleted []string, err error) {
	baseTree, err := baseCommit.Tree()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting base tree: %w", err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting head tree: %w", err)
	}

	changes, err := baseTree.Diff(headTree)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("computing diff: %w", err)
	}

	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			continue
		}

		switch action {
		case 1: // Insert
			if detectLang(change.To.Name) != "" {
				added = append(added, change.To.Name)
			}
		case 2: // Delete
			if detectLang(change.From.Name) != "" {
				deleted = append(deleted, change.From.Name)
			}
		case 0: // Modify
			if detectLang(change.From.Name) != "" {
				modified = append(modified, change.From.Name)
			}
		}
	}

	return added, modified, deleted, nil
}

// detectLang detects the language based on file extension.
func detectLang(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".jsx":
		return "js"
	default:
		return ""
	}
}

// GetCommitHash returns the hash of a commit as a string.
func GetCommitHash(commit *object.Commit) string {
	return commit.Hash.String()
}
