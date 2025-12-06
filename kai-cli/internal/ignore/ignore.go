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
	pattern  string
	negated  bool
	dirOnly  bool
	anchored bool // Pattern starts with / (matches from root only)
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

// Match checks if a path should be ignored.
// The path should be relative to the matcher's base path.
// isDir indicates whether the path is a directory.
func (m *Matcher) Match(path string, isDir bool) bool {
	// Normalize path separators
	path = filepath.ToSlash(path)

	// Remove leading ./
	path = strings.TrimPrefix(path, "./")

	ignored := false

	for _, p := range m.patterns {
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

// LoadDefaults loads default ignore patterns (common directories to skip).
func (m *Matcher) LoadDefaults() {
	defaults := []string{
		// ------------------------------
		// Version control
		// ------------------------------
		".git/",
		".kai/",
		".ivcs/",
		".svn/",
		".hg/",

		// ------------------------------
		// Universal junk / OS files
		// ------------------------------
		".DS_Store",
		"Thumbs.db",
		"ehthumbs.db",
		"Icon?",
		"Desktop.ini",
		"*.tmp",
		"*.temp",
		"*.swp",
		"*.swo",
		"*.bak",
		"*.orig",
		"*.lock",
		"*.log",
		"*.cache",
		"*.pid",
		"*.seed",
		"*.retry",
		"*.out",
		"*.err",

		// ------------------------------
		// Node / JS / TS
		// ------------------------------
		"node_modules/",
		"npm-debug.log*",
		"yarn-debug.log*",
		"yarn-error.log*",
		"pnpm-debug.log*",
		"lerna-debug.log*",
		"jspm_packages/",
		".bower-cache/",
		".bower-registry/",
		".bower-tmp/",
		"dist/",
		"dist-ssr/",
		"build/",
		"out/",
		".out/",
		".next/",
		".nuxt/",
		".svelte-kit/",
		".storybook/",
		"storybook-static/",
		"coverage/",
		".vscode-test/",
		".eslintcache",
		".stylelintcache",
		"parcel-cache/",
		".next-cache/",
		".vercel/",
		".netlify/",
		".snowpack/",
		"vendor/bundle/",
		"temp/",
		"tmp/",

		// Turbo / Nx
		".turbo/",
		".nx/",

		// Vitest / Jest outputs
		".vitest/",
		".jest/",

		// ------------------------------
		// Python
		// ------------------------------
		"__pycache__/",
		"*.py[cod]",
		"*.so",
		"*.pyd",
		"*.pyo",
		"*.pdb",
		"*.egg",
		"*.egg-info/",
		".eggs/",
		"env/",
		"venv/",
		".venv/",
		"pip-wheel-metadata/",
		"pip-cache/",
		"pytest_cache/",
		".pytest_cache/",
		".mypy_cache/",
		".dmypy.json",
		"ruff_cache/",
		".ruff_cache/",
		".tox/",
		".nox/",
		"site-packages/",

		// ------------------------------
		// Go
		// ------------------------------
		"bin/",
		"pkg/",
		"*.test",
		"*.prof",
		"*.cover",
		".go-cache/",
		"go.work.sum",

		// ------------------------------
		// Rust / Cargo
		// ------------------------------
		"target/",
		"cargo-cache/",
		".cargo/",
		"rustfmt-backup/",

		// ------------------------------
		// Java / Kotlin / Scala
		// ------------------------------
		"*.class",
		"*.jar",
		"*.war",
		"*.ear",
		"*.iml",
		"*.iws",
		"*.ipr",
		".gradle/",
		"dependency-reduced-pom.xml",
		".mvn/",
		"jars/",

		// ------------------------------
		// C / C++
		// ------------------------------
		"*.o",
		"*.a",
		"*.dll",
		"*.exe",
		"*.dSYM/",
		"*.obj",
		"*.pch",
		"*.manifest",
		"Build/",
		"build*/",
		"cmake-build*/",
		".coverage/",

		// ------------------------------
		// Swift / iOS / Xcode
		// ------------------------------
		"DerivedData/",
		"*.xcworkspace/",
		"*.xcodeproj/",
		"*.xcuserstate",
		"*.xcuserdata/",
		"*.swiftpm/",

		// ------------------------------
		// Android
		// ------------------------------
		"captures/",
		"outputs/",
		"*.apk",
		"*.aar",

		// ------------------------------
		// PHP / Composer
		// ------------------------------
		"vendor/",
		"composer.lock",
		"composer.phar",

		// ------------------------------
		// Ruby / Rails
		// ------------------------------
		".bundle/",
		"log/",
		".sass-cache/",
		".ruby-version",
		".ruby-gemset",

		// ------------------------------
		// Haskell
		// ------------------------------
		"dist-newstyle/",
		".stack-work/",

		// ------------------------------
		// Terraform / Infra / Cloud
		// ------------------------------
		".terraform/",
		".terraform.lock.hcl",
		"*.tfstate",
		"*.tfstate.backup",
		"tfplan*",
		"cdk.out/",
		".sam-cache/",
		".aws-sam/",
		"serverless/.serverless/",
		"serverless/outputs/",
		"pulumi.log",
		".pulumi/",

		// ------------------------------
		// Docker / containers
		// ------------------------------
		".docker/",
		"docker-data/",
		"docker-compose.override.yml",

		// ------------------------------
		// VSCode / JetBrains / Editors
		// ------------------------------
		".vscode/",
		".idea/",
		"*.sublime-workspace",

		// ------------------------------
		// Framework-specific
		// ------------------------------
		".angular/",
		".public/",
		".cache/",
		".build/",
		".astro/",

		// ------------------------------
		// Misc runtime
		// ------------------------------
		"logs/",
		"cache/",
		"*.sqlite-journal",
		"*.db-shm",
		"*.db-wal",
		"*.sqlite*",
		"*~backup*",
		"debug/",
		"data/",
		"dump/",

		// ------------------------------
		// Lock files (large, not useful for analysis)
		// ------------------------------
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"Pipfile.lock",
		"poetry.lock",
		"Cargo.lock",
		"go.sum",
	}
	m.AddPatterns(defaults)
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
