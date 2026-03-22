package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/mark3labs/mcp-go/mcp"
	sitter "github.com/smacker/go-tree-sitter"

	"kai-core/parse"
)

// nodeTypeAliases maps user-friendly names to per-language tree-sitter node types.
var nodeTypeAliases = map[string]map[string][]string{
	"string": {
		"go":     {"interpreted_string_literal", "raw_string_literal"},
		"js":     {"string", "template_string"},
		"ts":     {"string", "template_string"},
		"python": {"string"},
		"ruby":   {"string", "string_content"},
		"rust":   {"string_literal", "raw_string_literal"},
	},
	"comment": {
		"go":     {"comment"},
		"js":     {"comment"},
		"ts":     {"comment"},
		"python": {"comment"},
		"ruby":   {"comment"},
		"rust":   {"line_comment", "block_comment"},
	},
	"identifier": {
		"go":     {"identifier", "field_identifier", "type_identifier"},
		"js":     {"identifier"},
		"ts":     {"identifier"},
		"python": {"identifier"},
		"ruby":   {"identifier"},
		"rust":   {"identifier"},
	},
}

// parseableLangs are the languages supported by kai-core/parse.
var parseableLangs = map[string]bool{
	"go": true, "golang": true,
	"js": true, "javascript": true,
	"ts": true, "typescript": true,
	"py": true, "python": true,
	"rb": true, "ruby": true,
	"rs": true, "rust": true,
}

const maxLineLen = 200

type grepMatch struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Text     string `json:"text"`
	NodeType string `json:"node_type,omitempty"`
}

type fileInfo struct {
	digest string
	lang   string
}

// normalizeLang maps language variants to the canonical short form.
func normalizeLang(lang string) string {
	switch strings.ToLower(lang) {
	case "go", "golang":
		return "go"
	case "js", "javascript":
		return "js"
	case "ts", "typescript":
		return "ts"
	case "py", "python":
		return "python"
	case "rb", "ruby":
		return "ruby"
	case "rs", "rust":
		return "rust"
	default:
		return strings.ToLower(lang)
	}
}

// resolveNodeTypes returns the set of tree-sitter node types to match.
// If nodeType is a known alias ("string", "comment", "identifier"), expands per language.
// Otherwise treats it as a raw tree-sitter type name.
func resolveNodeTypes(nodeType, lang string) map[string]bool {
	result := make(map[string]bool)
	normalized := normalizeLang(lang)
	if aliasMap, ok := nodeTypeAliases[nodeType]; ok {
		if types, ok := aliasMap[normalized]; ok {
			for _, t := range types {
				result[t] = true
			}
			return result
		}
		// Unknown language for this alias — collect all types across languages
		for _, types := range aliasMap {
			for _, t := range types {
				result[t] = true
			}
		}
		return result
	}
	// Raw tree-sitter type
	result[nodeType] = true
	return result
}

// getOrCreateParser returns the cached parser, creating it on first use.
// Safe because MCP stdio server handles one request at a time.
func (s *Server) getOrCreateParser() *parse.Parser {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parser == nil {
		s.parser = parse.NewParser()
	}
	return s.parser
}

// snapshotFileInfo returns path -> {digest, lang} for all files in a snapshot.
func (s *Server) snapshotFileInfo(snapshotID []byte) (map[string]fileInfo, error) {
	node, err := s.db.GetNode(snapshotID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("snapshot not found")
	}

	result := make(map[string]fileInfo)

	if fileList, ok := node.Payload["files"].([]interface{}); ok {
		for _, f := range fileList {
			if fm, ok := f.(map[string]interface{}); ok {
				path, _ := fm["path"].(string)
				digest, _ := fm["contentDigest"].(string)
				lang, _ := fm["lang"].(string)
				if path != "" {
					result[path] = fileInfo{digest: digest, lang: lang}
				}
			}
		}
		return result, nil
	}

	return result, nil
}

func (s *Server) handleGrep(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	pattern, err := req.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'pattern'"), nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	nodeType := optString(req, "node_type")
	langFilter := optString(req, "lang")
	pathPattern := optString(req, "path")
	excludeTests := optBool(req, "exclude_tests")
	maxResults := int(optFloat(req, "max_results", 200))
	if maxResults <= 0 {
		maxResults = 200
	}

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	files, err := s.snapshotFileInfo(snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error reading snapshot files: %v", err)), nil
	}

	// Sort paths for deterministic results when truncated
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var matches []grepMatch
	filesSearched := 0

	for _, path := range paths {
		fi := files[path]

		// Apply filters
		if langFilter != "" && normalizeLang(fi.lang) != normalizeLang(langFilter) {
			continue
		}
		if pathPattern != "" {
			matched, _ := doublestar.Match(pathPattern, path)
			if !matched {
				continue
			}
		}
		if excludeTests && isTestFile(path) {
			continue
		}
		if fi.digest == "" {
			continue
		}

		content, err := s.db.ReadObject(fi.digest)
		if err != nil {
			continue
		}
		filesSearched++

		remaining := maxResults - len(matches)
		if remaining <= 0 {
			break
		}

		if nodeType != "" && parseableLangs[strings.ToLower(fi.lang)] {
			matches = append(matches, s.grepAST(content, fi.lang, re, nodeType, path, remaining)...)
		} else {
			matches = append(matches, grepRaw(content, re, path, remaining)...)
		}

		if len(matches) >= maxResults {
			matches = matches[:maxResults]
			break
		}
	}

	return jsonResult(map[string]interface{}{
		"pattern":        pattern,
		"node_type":      nodeType,
		"count":          len(matches),
		"files_searched": filesSearched,
		"truncated":      len(matches) >= maxResults,
		"matches":        matches,
	})
}

// grepRaw performs line-by-line regex matching on raw file content.
func grepRaw(content []byte, re *regexp.Regexp, filePath string, remaining int) []grepMatch {
	var matches []grepMatch
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if remaining <= 0 {
			break
		}
		if re.MatchString(line) {
			text := strings.TrimRight(line, "\r")
			if len(text) > maxLineLen {
				text = text[:maxLineLen] + "..."
			}
			matches = append(matches, grepMatch{
				File: filePath,
				Line: i + 1,
				Text: text,
			})
			remaining--
		}
	}
	return matches
}

// grepAST performs regex matching restricted to specific AST node types.
func (s *Server) grepAST(content []byte, lang string, re *regexp.Regexp, nodeType, filePath string, remaining int) []grepMatch {
	if remaining <= 0 {
		return nil
	}

	parser := s.getOrCreateParser()
	parsed, err := parser.Parse(content, lang)
	if err != nil {
		// Fall back to raw grep if parsing fails
		return grepRaw(content, re, filePath, remaining)
	}

	targetTypes := resolveNodeTypes(nodeType, lang)

	var matches []grepMatch
	iter := sitter.NewIterator(parsed.Tree.RootNode(), sitter.DFSMode)
	for {
		node, err := iter.Next()
		if err != nil || node == nil {
			break
		}
		if remaining <= 0 {
			break
		}

		if !targetTypes[node.Type()] {
			continue
		}

		nodeText := string(content[node.StartByte():node.EndByte()])
		if !re.MatchString(nodeText) {
			continue
		}

		text := nodeText
		// For multiline nodes, take the first matching line
		if strings.Contains(text, "\n") {
			for _, line := range strings.Split(text, "\n") {
				if re.MatchString(line) {
					text = line
					break
				}
			}
		}
		text = strings.TrimRight(text, "\r")
		if len(text) > maxLineLen {
			text = text[:maxLineLen] + "..."
		}

		matches = append(matches, grepMatch{
			File:     filePath,
			Line:     int(node.StartPoint().Row) + 1,
			Text:     text,
			NodeType: node.Type(),
		})
		remaining--
	}
	return matches
}
