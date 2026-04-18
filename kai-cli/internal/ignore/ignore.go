// Package ignore provides gitignore-style pattern matching for file filtering.
package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Pattern represents a single ignore pattern with its properties.
type Pattern struct {
	pattern      string
	negated      bool
	dirOnly      bool
	anchored     bool // Pattern starts with / (matches from root only)
	semanticOnly bool // @semantic-ignore: include in snapshot, skip in analysis
}

// Matcher holds compiled ignore patterns and provides matching functionality.
type Matcher struct {
	patterns []Pattern
	basePath string
}

// NewMatcher creates a new empty Matcher with the given base path.
func NewMatcher(basePath string) *Matcher {
	return &Matcher{
		patterns: []Pattern{},
		basePath: basePath,
	}
}

// AddPattern adds a single pattern string to the matcher.
func (m *Matcher) AddPattern(line string) {
	line = strings.TrimSpace(line)

	// Skip empty lines and comments
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}

	p := Pattern{}

	// Check for @semantic-ignore annotation
	if idx := strings.Index(line, "@semantic-ignore"); idx >= 0 {
		p.semanticOnly = true
		line = strings.TrimSpace(line[:idx])
		if line == "" {
			return
		}
	}

	// Check for negation
	if strings.HasPrefix(line, "!") {
		p.negated = true
		line = line[1:]
	}

	// Check for directory-only pattern
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}

	// Check for anchored pattern (starts with /)
	if strings.HasPrefix(line, "/") {
		p.anchored = true
		line = line[1:]
	}

	// Handle patterns without slashes - they match at any level
	// Unless anchored, patterns without / match basename anywhere
	if !p.anchored && !strings.Contains(line, "/") {
		line = "**/" + line
	}

	p.pattern = line
	m.patterns = append(m.patterns, p)
}

// AddPatterns adds multiple pattern strings to the matcher.
func (m *Matcher) AddPatterns(lines []string) {
	for _, line := range lines {
		m.AddPattern(line)
	}
}

// LoadFile loads patterns from a gitignore-style file.
func (m *Matcher) LoadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Ignore files that don't exist
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		m.AddPattern(scanner.Text())
	}

	return scanner.Err()
}

// Match checks if a path should be excluded from capture (not in snapshot at all).
// Semantic-only patterns are NOT matched here — those files are captured.
// The path should be relative to the matcher's base path.
// isDir indicates whether the path is a directory.
func (m *Matcher) Match(path string, isDir bool) bool {
	return m.match(path, isDir, false)
}

// MatchSemantic checks if a path should be excluded from semantic analysis.
// Returns true for both fully excluded files AND @semantic-ignore files.
// Use this in the symbol analyzer and call graph builder.
func (m *Matcher) MatchSemantic(path string, isDir bool) bool {
	return m.match(path, isDir, true)
}

func (m *Matcher) match(path string, isDir bool, includeSemantic bool) bool {
	// Normalize path separators
	path = filepath.ToSlash(path)

	// Remove leading ./
	path = strings.TrimPrefix(path, "./")

	ignored := false

	for _, p := range m.patterns {
		// Skip semantic-only patterns unless we're doing semantic matching
		if p.semanticOnly && !includeSemantic {
			continue
		}

		// For dirOnly patterns matching a file, we need to check if
		// the file is inside a matching directory
		if p.dirOnly && !isDir {
			// Check if any parent directory matches
			matched := m.matchDirPattern(p.pattern, path)
			if matched {
				ignored = !p.negated
			}
			continue
		}

		matched := m.matchPattern(p.pattern, path)

		if matched {
			ignored = !p.negated
		}
	}

	return ignored
}

// matchDirPattern checks if a path is inside a directory matching the pattern.
func (m *Matcher) matchDirPattern(pattern, path string) bool {
	// Split path into segments and check if any parent directory matches
	// We check prefixes up to but NOT including the full path (since the full path is a file)
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if m.matchPattern(pattern, prefix) {
			return true
		}
	}
	return false
}

// matchPattern checks if a path matches a single pattern.
func (m *Matcher) matchPattern(pattern, path string) bool {
	// Try exact match first
	matched, _ := doublestar.Match(pattern, path)
	if matched {
		return true
	}

	// For directory patterns, also try matching with trailing content
	// e.g., "node_modules" should match "node_modules/foo/bar.js"
	if !strings.HasSuffix(pattern, "/**") {
		matched, _ = doublestar.Match(pattern+"/**", path)
		if matched {
			return true
		}
	}

	return false
}

// MatchPath is a convenience method that determines if a path is a directory
// by checking if it exists on the filesystem.
func (m *Matcher) MatchPath(path string) bool {
	fullPath := filepath.Join(m.basePath, path)
	info, err := os.Stat(fullPath)
	if err != nil {
		// If we can't stat, assume it's a file
		return m.Match(path, false)
	}
	return m.Match(path, info.IsDir())
}

// LoadDefaults loads default ignore patterns.
// Patterns without annotation are fully excluded from snapshots.
// Patterns with @semantic-ignore are captured but skipped during analysis.
func (m *Matcher) LoadDefaults() {
	// @exclude: never captured, not in snapshot at all
	excludes := []string{
		// Version control
		".git/",
		".kai/",
		".ivcs/",
		".svn/",
		".hg/",

		// Secrets / Environment files
		".env",
		".env.*",
		"*.env",
		".envrc",
		"*.pem",
		"*.key",
		"*.p12",
		"*.pfx",
		"credentials.json",
		"secrets.json",
		"secrets.yaml",
		"secrets.yml",

		// OS junk
		".DS_Store",
		"Thumbs.db",
		"ehthumbs.db",
		"Icon?",
		"Desktop.ini",

		// Editor temp files
		"*.swp",
		"*.swo",
		"*.bak",
		"*.orig",
		"*.tmp",
		"*.temp",

		// Dependency directories (huge, fetched by package manager)
		"node_modules/",
		"jspm_packages/",
		"site-packages/",
		"vendor/",
		".bundle/",

		// Framework-generated code (regenerated on every build)
		".svelte-kit/generated/",
		".next/cache/",
		".nuxt/dist/",
		".astro/types.d.ts",
		".angular/cache/",

		// Virtual environments
		"env/",
		"venv/",
		".venv/",

		// Editor / IDE config
		".vscode/",
		".idea/",
		"*.sublime-workspace",
	}
	m.AddPatterns(excludes)

	// @semantic-ignore: captured in snapshot (CI needs them),
	// but skipped during semantic graph analysis
	semanticIgnores := []string{
		// Build outputs
		"dist/ @semantic-ignore",
		"dist-ssr/ @semantic-ignore",
		"build/ @semantic-ignore",
		"Build/ @semantic-ignore",
		"build*/ @semantic-ignore",
		"cmake-build*/ @semantic-ignore",
		"out/ @semantic-ignore",
		".out/ @semantic-ignore",
		".next/ @semantic-ignore",
		".nuxt/ @semantic-ignore",
		".svelte-kit/ @semantic-ignore",
		".astro/ @semantic-ignore",
		".angular/ @semantic-ignore",
		".public/ @semantic-ignore",
		".cache/ @semantic-ignore",
		".build/ @semantic-ignore",
		"bin/ @semantic-ignore",
		"target/ @semantic-ignore",
		"DerivedData/ @semantic-ignore",

		// Lock files
		"package-lock.json @semantic-ignore",
		"yarn.lock @semantic-ignore",
		"pnpm-lock.yaml @semantic-ignore",
		"Pipfile.lock @semantic-ignore",
		"poetry.lock @semantic-ignore",
		"Cargo.lock @semantic-ignore",
		"go.sum @semantic-ignore",
		"composer.lock @semantic-ignore",
		"Gemfile.lock @semantic-ignore",
		"go.work.sum @semantic-ignore",
		".terraform.lock.hcl @semantic-ignore",

		// Test/coverage outputs
		"coverage/ @semantic-ignore",
		".coverage/ @semantic-ignore",
		".vitest/ @semantic-ignore",
		".jest/ @semantic-ignore",
		".vscode-test/ @semantic-ignore",
		"pytest_cache/ @semantic-ignore",
		".pytest_cache/ @semantic-ignore",
		".mypy_cache/ @semantic-ignore",
		"ruff_cache/ @semantic-ignore",
		".ruff_cache/ @semantic-ignore",

		// Compiled/binary artifacts
		"*.o @semantic-ignore",
		"*.a @semantic-ignore",
		"*.so @semantic-ignore",
		"*.dll @semantic-ignore",
		"*.exe @semantic-ignore",
		"*.class @semantic-ignore",
		"*.jar @semantic-ignore",
		"*.war @semantic-ignore",
		"*.ear @semantic-ignore",
		"*.pyc @semantic-ignore",
		"*.pyo @semantic-ignore",
		"*.pyd @semantic-ignore",
		"*.apk @semantic-ignore",
		"*.aar @semantic-ignore",
		"*.dSYM/ @semantic-ignore",

		// Caches and generated
		"__pycache__/ @semantic-ignore",
		".egg-info/ @semantic-ignore",
		".eggs/ @semantic-ignore",
		".gradle/ @semantic-ignore",
		".mvn/ @semantic-ignore",
		".cargo/ @semantic-ignore",
		".go-cache/ @semantic-ignore",
		".turbo/ @semantic-ignore",
		".nx/ @semantic-ignore",
		".tox/ @semantic-ignore",
		".nox/ @semantic-ignore",
		".sass-cache/ @semantic-ignore",
		"parcel-cache/ @semantic-ignore",
		".next-cache/ @semantic-ignore",
		".storybook/ @semantic-ignore",
		"storybook-static/ @semantic-ignore",
		".docker/ @semantic-ignore",
		".terraform/ @semantic-ignore",

		// Cloud / deploy artifacts
		"cdk.out/ @semantic-ignore",
		".sam-cache/ @semantic-ignore",
		".aws-sam/ @semantic-ignore",
		".pulumi/ @semantic-ignore",
		".vercel/ @semantic-ignore",
		".netlify/ @semantic-ignore",
		".snowpack/ @semantic-ignore",

		// Logs and runtime data
		"*.log @semantic-ignore",
		"*.pid @semantic-ignore",
		"*.seed @semantic-ignore",
		"*.cache @semantic-ignore",
		"*.sqlite-journal @semantic-ignore",
		"*.db-shm @semantic-ignore",
		"*.db-wal @semantic-ignore",
		"logs/ @semantic-ignore",
		"log/ @semantic-ignore",
		"debug/ @semantic-ignore",
		"dump/ @semantic-ignore",
		"temp/ @semantic-ignore",
		"tmp/ @semantic-ignore",

		// Infra state
		"*.tfstate @semantic-ignore",
		"*.tfstate.backup @semantic-ignore",

		// Misc
		"*.prof @semantic-ignore",
		"*.cover @semantic-ignore",
		"*.test @semantic-ignore",
		"*.out @semantic-ignore",
		"*.err @semantic-ignore",
		"*~backup* @semantic-ignore",
	}
	m.AddPatterns(semanticIgnores)
}

// LoadFromDir loads .gitignore and .kaiignore from a directory.
// Patterns are loaded in order: defaults, .gitignore, .kaiignore
// Later patterns can override earlier ones using negation.
func LoadFromDir(dir string) (*Matcher, error) {
	m := NewMatcher(dir)

	// Load default patterns
	m.LoadDefaults()

	// Load .gitignore if present
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := m.LoadFile(gitignorePath); err != nil {
		return nil, err
	}

	// Load .kaiignore if present (takes precedence)
	kaiignorePath := filepath.Join(dir, ".kaiignore")
	if err := m.LoadFile(kaiignorePath); err != nil {
		return nil, err
	}

	return m, nil
}

// Compile creates a matcher from a list of pattern strings.
func Compile(patterns []string) *Matcher {
	m := NewMatcher("")
	m.AddPatterns(patterns)
	return m
}
