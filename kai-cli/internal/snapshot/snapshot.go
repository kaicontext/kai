// Package snapshot handles creating and managing snapshots from file sources.
package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kai/internal/filesource"
	"kai/internal/graph"
	"kai/internal/module"
	"kai/internal/parse"
	"kai/internal/util"
)

// Creator handles snapshot creation.
type Creator struct {
	db      *graph.DB
	matcher *module.Matcher
}

// NewCreator creates a new snapshot creator.
func NewCreator(db *graph.DB, matcher *module.Matcher) *Creator {
	return &Creator{db: db, matcher: matcher}
}

// CreateSnapshot creates a snapshot from a file source.
func (c *Creator) CreateSnapshot(source filesource.FileSource) ([]byte, error) {
	// Get all files from source
	files, err := source.GetFiles()
	if err != nil {
		return nil, fmt.Errorf("getting files: %w", err)
	}

	// Start transaction
	tx, err := c.db.BeginTx()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Create/ensure module nodes first (matcher may be nil during git import)
	moduleIDs := make(map[string][]byte)
	if c.matcher != nil {
		for _, mod := range c.matcher.GetAllModules() {
			payload := c.matcher.GetModulePayload(mod.Name)
			moduleID, err := c.db.InsertNode(tx, graph.KindModule, payload)
			if err != nil {
				return nil, fmt.Errorf("inserting module: %w", err)
			}
			moduleIDs[mod.Name] = moduleID
		}
	}

	// First pass: create all file nodes and collect their IDs
	type fileInfo struct {
		id            []byte
		path          string
		lang          string
		contentDigest string
		modules       []string
	}
	fileInfos := make([]fileInfo, 0, len(files))

	for _, file := range files {
		// Write content to objects
		digest, err := c.db.WriteObject(file.Content)
		if err != nil {
			return nil, fmt.Errorf("writing object: %w", err)
		}

		// Create file node
		filePayload := map[string]interface{}{
			"path":   file.Path,
			"lang":   file.Lang,
			"digest": digest,
		}
		fileID, err := c.db.InsertNode(tx, graph.KindFile, filePayload)
		if err != nil {
			return nil, fmt.Errorf("inserting file: %w", err)
		}

		var fileModules []string
		if c.matcher != nil {
			fileModules = c.matcher.MatchPath(file.Path)
		}
		fileInfos = append(fileInfos, fileInfo{
			id:            fileID,
			path:          file.Path,
			lang:          file.Lang,
			contentDigest: digest,
			modules:       fileModules,
		})
	}

	// Build file digests list (hex-encoded) for the snapshot payload
	fileDigests := make([]string, len(fileInfos))
	for i, fi := range fileInfos {
		fileDigests[i] = util.BytesToHex(fi.id)
	}

	// Build files array with metadata for fast listing (no extra DB lookups needed)
	filesMetadata := make([]map[string]interface{}, len(fileInfos))
	for i, fi := range fileInfos {
		filesMetadata[i] = map[string]interface{}{
			"path":          fi.path,
			"lang":          fi.lang,
			"digest":        util.BytesToHex(fi.id),
			"contentDigest": fi.contentDigest,
		}
	}

	// Create snapshot node with file digests embedded
	snapshotPayload := map[string]interface{}{
		"sourceType":  source.SourceType(),
		"sourceRef":   source.Identifier(),
		"fileCount":   len(files),
		"fileDigests": fileDigests,
		"files":       filesMetadata, // New: inline file metadata for fast listing
		// Note: createdAt is NOT in the payload because snapshots are content-addressed.
		// Including a timestamp would make identical directories produce different snapshot IDs.
		// Use the ref's updatedAt for creation time.
	}
	snapshotID, err := c.db.InsertNode(tx, graph.KindSnapshot, snapshotPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting snapshot: %w", err)
	}

	// Second pass: create edges now that we have the snapshot ID
	for _, fi := range fileInfos {
		// Create edge: Snapshot HAS_FILE File
		if err := c.db.InsertEdge(tx, snapshotID, graph.EdgeHasFile, fi.id, nil); err != nil {
			return nil, fmt.Errorf("inserting HAS_FILE edge: %w", err)
		}

		// Map file to modules
		for _, modName := range fi.modules {
			if moduleID, ok := moduleIDs[modName]; ok {
				// Create edge: Module CONTAINS File
				if err := c.db.InsertEdge(tx, moduleID, graph.EdgeContains, fi.id, snapshotID); err != nil {
					return nil, fmt.Errorf("inserting CONTAINS edge: %w", err)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return snapshotID, nil
}

// ProgressFunc is called during long operations to report progress.
// current is the current item number (1-based), total is the total count, filename is the current file.
type ProgressFunc func(current, total int, filename string)

// AnalyzeSymbols extracts symbols from all files in a snapshot.
// Files whose content was already analyzed (DEFINES_IN edges exist) are skipped.
func (c *Creator) AnalyzeSymbols(snapshotID []byte, progress ProgressFunc) error {
	// Get all files in the snapshot
	edges, err := c.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return fmt.Errorf("getting snapshot files: %w", err)
	}

	parser := parse.NewParser()

	tx, err := c.db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	total := len(edges)
	skipped := 0
	for i, edge := range edges {
		fileNode, err := c.db.GetNode(edge.Dst)
		if err != nil {
			return fmt.Errorf("getting file node: %w", err)
		}
		if fileNode == nil {
			continue
		}

		filename, _ := fileNode.Payload["path"].(string)
		if progress != nil {
			progress(i+1, total, filename)
		}

		if isBinaryOrImageFile(filename) {
			continue
		}

		// Skip files already analyzed: if DEFINES_IN edges point to this file ID,
		// the symbols were extracted in a prior capture with identical content.
		if already, _ := c.db.HasEdgeByDst(graph.EdgeDefinesIn, edge.Dst); already {
			skipped++
			continue
		}

		digest, ok := fileNode.Payload["digest"].(string)
		if !ok {
			continue
		}

		content, err := c.db.ReadObject(digest)
		if err != nil {
			return fmt.Errorf("reading object: %w", err)
		}

		if len(content) > 500*1024 {
			continue
		}

		lang, _ := fileNode.Payload["lang"].(string)
		parsed, err := parser.Parse(content, lang)
		if err != nil {
			continue
		}

		fileIDHex := util.BytesToHex(edge.Dst)
		for _, sym := range parsed.Symbols {
			symbolPayload := map[string]interface{}{
				"fqName":    sym.Name,
				"kind":      sym.Kind,
				"fileId":    fileIDHex,
				"range":     map[string]interface{}{"start": sym.Range.Start, "end": sym.Range.End},
				"signature": sym.Signature,
			}

			symbolID, err := c.db.InsertNode(tx, graph.KindSymbol, symbolPayload)
			if err != nil {
				return fmt.Errorf("inserting symbol: %w", err)
			}

			if err := c.db.InsertEdge(tx, symbolID, graph.EdgeDefinesIn, edge.Dst, snapshotID); err != nil {
				return fmt.Errorf("inserting DEFINES_IN edge: %w", err)
			}
		}
	}

	return tx.Commit()
}

// AnalyzeCalls extracts function calls and imports from all files in a snapshot.
// This builds a call graph: Symbol --CALLS--> Symbol, File --IMPORTS--> File.
func (c *Creator) AnalyzeCalls(snapshotID []byte, progress ProgressFunc) error {
	// Get all files in the snapshot
	edges, err := c.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return fmt.Errorf("getting snapshot files: %w", err)
	}

	parser := parse.NewParser()

	// Single pass: collect all files, parse once, and extract imports+exports+calls together.
	type fileInfo struct {
		id       []byte
		path     string
		lang     string
		content  []byte
		isTest   bool
		exported []string              // exported symbols
		parsed   *parse.ParsedCalls    // cached parse result — avoids double tree-sitter pass
	}
	files := make([]*fileInfo, 0, len(edges))
	filesByPath := make(map[string]*fileInfo)

	// Collect package.json files separately for workspace resolution
	type pkgJSONInfo struct {
		path    string
		content []byte
	}
	var pkgJSONFiles []pkgJSONInfo

	for _, edge := range edges {
		fileNode, err := c.db.GetNode(edge.Dst)
		if err != nil {
			return fmt.Errorf("getting file node: %w", err)
		}
		if fileNode == nil {
			continue
		}

		path, _ := fileNode.Payload["path"].(string)
		lang, _ := fileNode.Payload["lang"].(string)

		// Read content for package.json files (needed for workspace resolution)
		if filepath.Base(path) == "package.json" {
			digest, ok := fileNode.Payload["digest"].(string)
			if ok {
				content, err := c.db.ReadObject(digest)
				if err == nil {
					pkgJSONFiles = append(pkgJSONFiles, pkgJSONInfo{path: path, content: content})
				}
			}
		}

		// Only process supported languages for graph analysis
		if lang != "js" && lang != "ts" && lang != "jsx" && lang != "tsx" &&
			lang != "go" && lang != "py" && lang != "rb" && lang != "rs" {
			continue
		}

		// Read content
		digest, ok := fileNode.Payload["digest"].(string)
		if !ok {
			continue
		}
		content, err := c.db.ReadObject(digest)
		if err != nil {
			continue
		}

		// Skip large files
		if len(content) > 500*1024 {
			continue
		}

		fi := &fileInfo{
			id:      edge.Dst,
			path:    path,
			lang:    lang,
			content: content,
			isTest:  parse.IsTestFile(path),
		}
		files = append(files, fi)
		filesByPath[path] = fi
	}

	// Build Go package directory index: import path suffix -> list of .go files
	// For a file at "kai-cli/internal/graph/graph.go", we index:
	//   "graph" -> [kai-cli/internal/graph/graph.go, ...]
	//   "internal/graph" -> [...]
	//   "kai-cli/internal/graph" -> [...]
	//   etc. (all suffixes of the directory path)
	goPkgIndex := make(map[string][]string) // pkg suffix -> file paths
	for path, fi := range filesByPath {
		if fi.lang != "go" {
			continue
		}
		dir := filepath.Dir(path)
		parts := strings.Split(dir, "/")
		// Index all suffixes: "a/b/c" -> ["c", "b/c", "a/b/c"]
		for i := len(parts) - 1; i >= 0; i-- {
			suffix := strings.Join(parts[i:], "/")
			goPkgIndex[suffix] = append(goPkgIndex[suffix], path)
		}
	}

	// Build Python module index: dotted import -> file paths
	// "auth.token" -> "auth/token.py"
	pyFileIndex := make(map[string]string) // module path -> file path
	allPaths := make(map[string]bool)      // simple existence set for all file paths
	for path, fi := range filesByPath {
		allPaths[path] = true
		if fi.lang != "py" {
			continue
		}
		// Convert file path to module path: auth/token.py -> auth.token
		modPath := strings.TrimSuffix(path, ".py")
		modPath = strings.TrimSuffix(modPath, "/__init__")
		modPath = strings.ReplaceAll(modPath, "/", ".")
		pyFileIndex[modPath] = path
	}

	// Build Ruby autoload index: constant name -> file path
	// Maps CamelCase class/module names to snake_case file paths using
	// Rails/Zeitwerk conventions. Searches app/, lib/, and root directories.
	// e.g. "User" -> "app/models/user.rb", "PostsController" -> "app/controllers/posts_controller.rb"
	rubyAutoloadIndex := make(map[string]string) // constant name -> file path
	for path, fi := range filesByPath {
		if fi.lang != "rb" {
			continue
		}
		// Derive the constant name from the file path using Zeitwerk conventions:
		// app/models/user.rb -> User
		// app/controllers/posts_controller.rb -> PostsController
		// app/models/admin/user.rb -> Admin::User
		// lib/payment_gateway.rb -> PaymentGateway
		constName := rubyPathToConstant(path)
		if constName != "" {
			rubyAutoloadIndex[constName] = path
		}
	}

	// Build JS/TS workspace package index: package name -> entry file path
	// Scans package.json files in the snapshot to map "@scope/pkg" or "pkg"
	// to the package's main/types entry point (e.g., "packages/foo/src/index.ts").
	// This enables resolving non-relative imports like "@kai-demo/normalize"
	// to actual files in the repo, creating proper IMPORTS edges across packages.
	jsWorkspaceIndex := make(map[string]string) // package name -> resolved entry path
	for _, pjf := range pkgJSONFiles {
		var pkg struct {
			Name    string `json:"name"`
			Main    string `json:"main"`
			Types   string `json:"types"`
			Exports json.RawMessage `json:"exports"`
		}
		if err := json.Unmarshal(pjf.content, &pkg); err != nil || pkg.Name == "" {
			continue
		}
		pkgDir := filepath.Dir(pjf.path)

		// Determine entry point: try main, types, then common defaults
		entry := pkg.Main
		if entry == "" {
			entry = pkg.Types
		}
		if entry == "" {
			// Try common conventions
			for _, candidate := range []string{
				"src/index.ts", "src/index.tsx", "src/index.js",
				"index.ts", "index.tsx", "index.js",
				"lib/index.ts", "lib/index.js",
			} {
				full := filepath.Join(pkgDir, candidate)
				if _, ok := filesByPath[full]; ok {
					entry = candidate
					break
				}
			}
		}
		if entry == "" {
			continue
		}

		entryPath := filepath.Clean(filepath.Join(pkgDir, entry))
		// Resolve entry point: it might omit the extension
		if _, ok := filesByPath[entryPath]; ok {
			jsWorkspaceIndex[pkg.Name] = entryPath
		} else {
			// Try adding extensions (main: "src/index" without .ts)
			for _, candidate := range parse.PossibleFilePaths(entryPath) {
				if _, ok := filesByPath[candidate]; ok {
					jsWorkspaceIndex[pkg.Name] = candidate
					break
				}
			}
		}
	}

	// Second pass: extract imports and build import graph
	// importGraph maps file path -> list of imported file paths
	importGraph := make(map[string][]string)

	total := len(files)
	for i, fi := range files {
		if progress != nil {
			progress(i+1, total, fi.path)
		}

		// Parse for calls (cached on fileInfo to avoid double tree-sitter pass)
		callsParsed, err := parser.ExtractCalls(fi.content, fi.lang)
		if err != nil {
			continue
		}

		fi.parsed = callsParsed
		fi.exported = callsParsed.Exports

		// Build import graph — language-specific resolution
		var imports []string
		for _, imp := range callsParsed.Imports {
			switch fi.lang {
			case "go":
				// Go: match import path against directory suffixes
				// e.g. "kai/internal/graph" matches files in any dir ending with "kai/internal/graph"
				resolved := resolveGoImport(imp.Source, fi.path, goPkgIndex)
				imports = append(imports, resolved...)

			case "py":
				// Python: convert dotted import to path
				resolved := resolvePythonImport(imp.Source, fi.path, pyFileIndex, allPaths)
				imports = append(imports, resolved...)

			case "rb":
				// Ruby: handle both explicit require and autoloaded constants
				if strings.HasPrefix(imp.Source, "autoload:") {
					// Zeitwerk autoload: resolve constant name to file
					constName := strings.TrimPrefix(imp.Source, "autoload:")
					resolved := resolveRubyAutoload(constName, fi.path, rubyAutoloadIndex)
					imports = append(imports, resolved...)
				} else if imp.IsRelative {
					// require_relative: resolve against file directory
					dir := filepath.Dir(fi.path)
					basePath := filepath.Join(dir, imp.Source)
					// Try with and without .rb extension
					for _, candidate := range []string{basePath + ".rb", basePath} {
						if _, ok := filesByPath[candidate]; ok {
							imports = append(imports, candidate)
							break
						}
					}
				} else {
					// require 'foo': try lib/foo.rb, app/**/foo.rb
					for _, prefix := range []string{"lib/", "app/models/", "app/controllers/", "app/helpers/", "app/services/", "app/jobs/", "app/mailers/", ""} {
						candidate := prefix + imp.Source + ".rb"
						if _, ok := filesByPath[candidate]; ok {
							imports = append(imports, candidate)
							break
						}
					}
				}

			default:
				// JS/TS/Ruby/Rust: import resolution
				if imp.IsRelative {
					// Relative import: resolve against file directory
					dir := filepath.Dir(fi.path)
					basePath := parse.ResolveImportPath(dir, imp.Source)
					for _, candidate := range parse.PossibleFilePaths(basePath) {
						if _, ok := filesByPath[candidate]; ok {
							imports = append(imports, candidate)
							break
						}
					}
				} else if fi.lang == "js" || fi.lang == "ts" || fi.lang == "jsx" || fi.lang == "tsx" {
					// Non-relative import: try workspace package resolution
					if entryPath, ok := jsWorkspaceIndex[imp.Source]; ok {
						imports = append(imports, entryPath)
					} else {
						// Subpath import (e.g., "@kai-demo/container/tokens")
						source := imp.Source
						for {
							lastSlash := strings.LastIndex(source, "/")
							if lastSlash <= 0 {
								break
							}
							if source[0] == '@' && strings.Count(source[:lastSlash], "/") == 0 {
								break
							}
							parent := source[:lastSlash]
							subpath := imp.Source[len(parent)+1:]
							if entryPath, ok := jsWorkspaceIndex[parent]; ok {
								pkgDir := filepath.Dir(entryPath)
								subBase := filepath.Join(pkgDir, subpath)
								resolved := false
								if _, ok := filesByPath[subBase]; ok {
									imports = append(imports, subBase)
									resolved = true
								}
								if !resolved {
									for _, candidate := range parse.PossibleFilePaths(subBase) {
										if _, ok := filesByPath[candidate]; ok {
											imports = append(imports, candidate)
											resolved = true
											break
										}
									}
								}
								if !resolved {
									subBaseSrc := filepath.Join(pkgDir, "src", subpath)
									for _, candidate := range parse.PossibleFilePaths(subBaseSrc) {
										if _, ok := filesByPath[candidate]; ok {
											imports = append(imports, candidate)
											break
										}
									}
								}
								break
							}
							source = parent
						}
					}
				}
			}
		}
		importGraph[fi.path] = imports
	}

	// Third pass: store edges in database
	tx, err := c.db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Store IMPORTS edges
	for _, fi := range files {
		for _, importedPath := range importGraph[fi.path] {
			if targetFile, ok := filesByPath[importedPath]; ok {
				if err := c.db.InsertEdge(tx, fi.id, graph.EdgeImports, targetFile.id, snapshotID); err != nil {
					return fmt.Errorf("inserting IMPORTS edge: %w", err)
				}
			}
		}
	}

	// For test files, trace the full import graph transitively to find all dependencies
	// Then create TESTS edges from test file to all source files it depends on
	//
	// Go-specific: test files in the same directory as source files are part of the
	// same package (no import needed). Create TESTS edges from *_test.go to all
	// non-test .go files in the same directory.
	goFilesByDir := make(map[string][]*fileInfo)
	for _, fi := range files {
		if fi.lang == "go" {
			dir := filepath.Dir(fi.path)
			goFilesByDir[dir] = append(goFilesByDir[dir], fi)
		}
	}

	for _, fi := range files {
		if !fi.isTest {
			continue
		}

		// Go same-package test coverage: _test.go covers all .go files in same dir
		if fi.lang == "go" {
			dir := filepath.Dir(fi.path)
			for _, sibling := range goFilesByDir[dir] {
				if sibling.path != fi.path && !sibling.isTest {
					if err := c.db.InsertEdge(tx, fi.id, graph.EdgeTests, sibling.id, snapshotID); err != nil {
						return fmt.Errorf("inserting TESTS edge: %w", err)
					}
				}
			}
		}

		// BFS to find all transitive import dependencies
		visited := make(map[string]bool)
		queue := []string{fi.path}
		visited[fi.path] = true

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]

			for _, imported := range importGraph[current] {
				if visited[imported] {
					continue
				}
				visited[imported] = true
				queue = append(queue, imported)

				// Create TESTS edge from test file to this dependency (if it's not a test file itself)
				if !parse.IsTestFile(imported) {
					if targetFile, ok := filesByPath[imported]; ok {
						if err := c.db.InsertEdge(tx, fi.id, graph.EdgeTests, targetFile.id, snapshotID); err != nil {
							return fmt.Errorf("inserting TESTS edge: %w", err)
						}
					}
				}
			}
		}
	}

	// Third pass: create CALLS edges between symbols
	// This requires matching call names to exported symbols
	// For now, create edges based on import/export matching

	// Build export map: symbol name -> file info
	exportMap := make(map[string]*fileInfo)
	for _, fi := range files {
		for _, exp := range fi.exported {
			exportMap[exp] = fi
		}
	}

	// Now process calls to create edges (reusing cached parse results — no second tree-sitter pass)
	for i, fi := range files {
		if progress != nil {
			progress(i+1, total, fi.path)
		}

		// Reuse cached parse result from first pass
		parsed := fi.parsed
		if parsed == nil {
			continue
		}

		// For Go files, build a map of import alias -> resolved file paths
		// so we can match pkg.Function() calls to target files
		var goImportAliasMap map[string][]string // alias -> file paths
		if fi.lang == "go" {
			goImportAliasMap = make(map[string][]string)
			for _, imp := range parsed.Imports {
				alias := imp.Default // last path component or explicit alias
				if alias == "" || alias == "_" {
					continue
				}
				resolved := resolveGoImport(imp.Source, fi.path, goPkgIndex)
				if len(resolved) > 0 {
					goImportAliasMap[alias] = resolved
				}
			}
		}

		// For each call, try to resolve it
		for _, call := range parsed.Calls {
			var targetFiles []string

			if fi.lang == "go" {
				// Go: match pkg.Function() calls via import alias map
				if call.IsMethodCall && call.CalleeObject != "" {
					// pkg.Function() — CalleeObject is the package alias
					if files, ok := goImportAliasMap[call.CalleeObject]; ok {
						// Check if CalleeName is exported in the target package
						for _, f := range files {
							if tf, ok := filesByPath[f]; ok {
								for _, exp := range tf.exported {
									if exp == call.CalleeName {
										targetFiles = append(targetFiles, f)
									}
								}
							}
						}
					}
				} else if !call.IsMethodCall {
					// Direct call to an exported symbol (same package or dot-import)
					if target, ok := exportMap[call.CalleeName]; ok {
						if target.path != fi.path {
							targetFiles = append(targetFiles, target.path)
						}
					}
				}
			} else {
				// JS/TS/Python: match non-method calls via imports
				if call.IsMethodCall {
					continue
				}
				for _, imp := range parsed.Imports {
					var importedAs string
					if imp.Default == call.CalleeName {
						importedAs = imp.Default
					} else if originalName, ok := imp.Named[call.CalleeName]; ok {
						importedAs = originalName
					}
					if importedAs == "" {
						continue
					}
					if imp.IsRelative {
						dir := filepath.Dir(fi.path)
						basePath := parse.ResolveImportPath(dir, imp.Source)
						for _, candidate := range parse.PossibleFilePaths(basePath) {
							if _, ok := filesByPath[candidate]; ok {
								targetFiles = append(targetFiles, candidate)
								break
							}
						}
					} else if fi.lang == "js" || fi.lang == "ts" || fi.lang == "jsx" || fi.lang == "tsx" {
						// Workspace package: resolve via index
						if entryPath, ok := jsWorkspaceIndex[imp.Source]; ok {
							targetFiles = append(targetFiles, entryPath)
						} else {
							// Subpath import resolution for calls
							source := imp.Source
							for {
								lastSlash := strings.LastIndex(source, "/")
								if lastSlash <= 0 {
									break
								}
								if source[0] == '@' && strings.Count(source[:lastSlash], "/") == 0 {
									break
								}
								parent := source[:lastSlash]
								subpath := imp.Source[len(parent)+1:]
								if entryPath, ok := jsWorkspaceIndex[parent]; ok {
									pkgDir := filepath.Dir(entryPath)
									subBase := filepath.Join(pkgDir, subpath)
									if _, ok := filesByPath[subBase]; ok {
										targetFiles = append(targetFiles, subBase)
									} else {
										for _, candidate := range parse.PossibleFilePaths(subBase) {
											if _, ok := filesByPath[candidate]; ok {
												targetFiles = append(targetFiles, candidate)
												break
											}
										}
									}
									break
								}
								source = parent
							}
						}
					}
				}
			}

			// Create CALLS edges for resolved targets
			for _, resolved := range targetFiles {
				targetFile, ok := filesByPath[resolved]
				if !ok {
					continue
				}
				callPayload := map[string]interface{}{
					"calleeName": call.CalleeName,
					"callerFile": fi.path,
					"calleeFile": resolved,
					"line":       call.Range.Start[0],
				}
				callID, err := c.db.InsertNode(tx, graph.KindSymbol, callPayload)
				if err != nil {
					continue
				}
				if err := c.db.InsertEdge(tx, fi.id, graph.EdgeCalls, targetFile.id, callID); err != nil {
					continue
				}
			}
		}
	}

	return tx.Commit()
}

// GetSnapshotFiles returns all file nodes in a snapshot.
func (c *Creator) GetSnapshotFiles(snapshotID []byte) ([]*graph.Node, error) {
	edges, err := c.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}

	var files []*graph.Node
	for _, edge := range edges {
		node, err := c.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil {
			files = append(files, node)
		}
	}

	return files, nil
}

// GetSymbolsInFile returns all symbols defined in a file for a given snapshot context.
func (c *Creator) GetSymbolsInFile(fileID, snapshotID []byte) ([]*graph.Node, error) {
	// Query edges where Symbol DEFINES_IN File with the given snapshot context
	// Uses targeted query instead of scanning all edges for the context
	edges, err := c.db.GetEdgesByContextAndDst(snapshotID, graph.EdgeDefinesIn, fileID)
	if err != nil {
		return nil, err
	}

	symbols := make([]*graph.Node, 0, len(edges))
	for _, edge := range edges {
		node, err := c.db.GetNode(edge.Src)
		if err != nil {
			return nil, err
		}
		if node != nil {
			symbols = append(symbols, node)
		}
	}

	return symbols, nil
}

// FindSnapshotByRef finds a snapshot by its source ref (git ref or content hash).
func FindSnapshotByRef(db *graph.DB, sourceRef string) ([]byte, error) {
	snapshots, err := db.GetNodesByKind(graph.KindSnapshot)
	if err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		// Check new sourceRef field
		if ref, ok := snap.Payload["sourceRef"].(string); ok && ref == sourceRef {
			return snap.ID, nil
		}
		// Backward compatibility: check old gitRef field
		if ref, ok := snap.Payload["gitRef"].(string); ok && ref == sourceRef {
			return snap.ID, nil
		}
	}

	return nil, sql.ErrNoRows
}

// GetFileByPath finds a file node by path within a snapshot.
func GetFileByPath(db *graph.DB, snapshotID []byte, path string) (*graph.Node, error) {
	edges, err := db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}

	for _, edge := range edges {
		node, err := db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil {
			if filePath, ok := node.Payload["path"].(string); ok && filePath == path {
				return node, nil
			}
		}
	}

	return nil, nil
}

// CheckoutResult contains the result of a checkout operation.
type CheckoutResult struct {
	FilesWritten  int
	FilesDeleted  int
	FilesSkipped  int
	TargetDir     string
}

// Checkout restores the filesystem to match a snapshot's state.
func (c *Creator) Checkout(snapshotID []byte, targetDir string, clean bool) (*CheckoutResult, error) {
	// Get the snapshot node to verify it exists
	snapNode, err := c.db.GetNode(snapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting snapshot: %w", err)
	}
	if snapNode == nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	if snapNode.Kind != graph.KindSnapshot {
		return nil, fmt.Errorf("not a snapshot: %s", snapNode.Kind)
	}

	// Get all files in the snapshot
	files, err := c.GetSnapshotFiles(snapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting snapshot files: %w", err)
	}

	result := &CheckoutResult{
		TargetDir: targetDir,
	}

	// Build a map of paths in the snapshot
	snapshotPaths := make(map[string]bool)

	// Write each file from the snapshot
	for _, fileNode := range files {
		path, ok := fileNode.Payload["path"].(string)
		if !ok {
			continue
		}
		snapshotPaths[path] = true

		digest, ok := fileNode.Payload["digest"].(string)
		if !ok {
			result.FilesSkipped++
			continue
		}

		// Build full path
		fullPath := filepath.Join(targetDir, path)

		// Skip if file already exists with same content
		if existing, err := os.ReadFile(fullPath); err == nil {
			if util.Blake3HashHex(existing) == digest {
				result.FilesSkipped++
				continue
			}
		}

		// Read content from object store
		content, err := c.db.ReadObject(digest)
		if err != nil {
			return nil, fmt.Errorf("reading object %s: %w", digest[:12], err)
		}

		// Create parent directories
		parentDir := filepath.Dir(fullPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return nil, fmt.Errorf("creating directory %s: %w", parentDir, err)
		}

		// Atomic write: write to temp file then rename
		tmpPath := fullPath + ".tmp"
		if err := os.WriteFile(tmpPath, content, 0644); err != nil {
			return nil, fmt.Errorf("writing temp file %s: %w", path, err)
		}
		if err := os.Rename(tmpPath, fullPath); err != nil {
			os.Remove(tmpPath) // Clean up on failure
			return nil, fmt.Errorf("atomic rename %s: %w", path, err)
		}

		result.FilesWritten++
	}

	// If clean mode, delete files not in snapshot
	if clean {
		deleted, err := cleanDirectory(targetDir, snapshotPaths)
		if err != nil {
			return nil, fmt.Errorf("cleaning directory: %w", err)
		}
		result.FilesDeleted = deleted
	}

	return result, nil
}

// GetFileContent reads file content by its digest from the object store.
func (c *Creator) GetFileContent(digest string) ([]byte, error) {
	return c.db.ReadObject(digest)
}

// cleanDirectory removes files that aren't in the snapshot
func cleanDirectory(targetDir string, snapshotPaths map[string]bool) (int, error) {
	deleted := 0

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			name := info.Name()
			// Skip hidden directories and common large/generated directories
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden files
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(targetDir, path)
		if err != nil {
			return err
		}

		// Check if this file is in the snapshot
		if !snapshotPaths[relPath] {
			// Only delete supported file types
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("removing %s: %w", relPath, err)
				}
				deleted++
			}
		}

		return nil
	})

	return deleted, err
}

// isBinaryOrImageFile returns true if the file extension indicates a binary or image file
// that shouldn't be parsed for symbols.
func isBinaryOrImageFile(filename string) bool {
	// Check lock files by name (captured in snapshots for CI, but not analyzed)
	if isLockFile(filename) {
		return true
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	// Images
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".svg", ".webp", ".tiff", ".tif":
		return true
	// Fonts
	case ".woff", ".woff2", ".ttf", ".otf", ".eot":
		return true
	// Media
	case ".mp3", ".mp4", ".wav", ".avi", ".mov", ".webm", ".ogg", ".flac":
		return true
	// Archives
	case ".zip", ".tar", ".gz", ".rar", ".7z", ".bz2":
		return true
	// Documents
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx":
		return true
	// Binaries
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a":
		return true
	// Other non-parseable
	case ".lock", ".map", ".min.js", ".min.css":
		return true
	}
	return false
}

// isLockFile returns true if the filename is a package manager lock file.
// Lock files are included in snapshots (needed for CI builds) but skipped
// during semantic analysis (no useful symbols/calls).
func isLockFile(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	switch base {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"pipfile.lock", "poetry.lock", "cargo.lock",
		"go.sum", "composer.lock", "gemfile.lock":
		return true
	}
	return false
}

// resolveGoImport resolves a Go import path to files in the snapshot.
// Go imports are package paths like "fmt", "kai/internal/graph", "kai-core/diff".
// We match by finding directories whose path suffix matches the import path.
// Returns all .go files in the matched package directory (excluding the importing file's own dir).
func resolveGoImport(importPath, importingFile string, goPkgIndex map[string][]string) []string {
	// Skip stdlib imports (no slash = stdlib like "fmt", "os", "strings")
	if !strings.Contains(importPath, "/") {
		return nil
	}

	importingDir := filepath.Dir(importingFile)

	// Try exact match first, then progressively shorter suffixes
	// For "kai/internal/graph", try: "kai/internal/graph", "internal/graph", "graph"
	parts := strings.Split(importPath, "/")
	for i := 0; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], "/")
		if files, ok := goPkgIndex[suffix]; ok {
			// Filter out files from the same directory (don't self-import)
			var result []string
			for _, f := range files {
				if filepath.Dir(f) != importingDir {
					result = append(result, f)
				}
			}
			if len(result) > 0 {
				return result
			}
		}
	}

	return nil
}

// resolvePythonImport resolves a Python import to files in the snapshot.
// Handles "from auth.token import validate" and "import auth.token".
func resolvePythonImport(importSource, importingFile string, pyIndex map[string]string, allPaths map[string]bool) []string {
	// Try the import source directly as a dotted module path
	if path, ok := pyIndex[importSource]; ok {
		return []string{path}
	}

	// Try as a directory path: "auth.token" -> "auth/token.py" or "auth/token/__init__.py"
	dirPath := strings.ReplaceAll(importSource, ".", "/")
	candidates := []string{
		dirPath + ".py",
		filepath.Join(dirPath, "__init__.py"),
	}
	for _, c := range candidates {
		if allPaths[c] {
			return []string{c}
		}
	}

	// Try progressively shorter prefixes for "from X.Y import Z"
	// "auth.token" might match "auth/token.py"
	parts := strings.Split(importSource, ".")
	for i := len(parts); i > 0; i-- {
		prefix := strings.Join(parts[:i], ".")
		if path, ok := pyIndex[prefix]; ok {
			return []string{path}
		}
	}

	return nil
}

// rubyPathToConstant converts a Ruby file path to its Zeitwerk constant name.
// Examples:
//
//	app/models/user.rb -> User
//	app/controllers/posts_controller.rb -> PostsController
//	app/models/admin/user.rb -> Admin::User
//	lib/payment_gateway.rb -> PaymentGateway
//	app/services/stripe/charge_service.rb -> Stripe::ChargeService
func rubyPathToConstant(path string) string {
	// Strip .rb extension
	if !strings.HasSuffix(path, ".rb") {
		return ""
	}
	path = strings.TrimSuffix(path, ".rb")

	// Strip known Rails root prefixes
	for _, prefix := range []string{
		"app/models/", "app/controllers/", "app/helpers/",
		"app/services/", "app/jobs/", "app/mailers/",
		"app/channels/", "app/serializers/", "app/policies/",
		"app/decorators/", "app/forms/", "app/validators/",
		"app/uploaders/", "app/presenters/", "app/workers/",
		"app/components/", "app/interactors/",
		"lib/",
	} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}

	// Skip files that don't map to constants (config, db, spec, etc.)
	if strings.HasPrefix(path, "config/") || strings.HasPrefix(path, "db/") ||
		strings.HasPrefix(path, "spec/") || strings.HasPrefix(path, "test/") ||
		strings.HasPrefix(path, "bin/") || strings.HasPrefix(path, "script/") ||
		strings.HasPrefix(path, "vendor/") || strings.HasPrefix(path, "node_modules/") {
		return ""
	}

	// Skip special files
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || base == "application" ||
		base == "routes" || base == "schema" || base == "seeds" {
		return ""
	}

	// Convert path segments to CamelCase and join with ::
	// admin/user -> Admin::User
	parts := strings.Split(path, "/")
	var constParts []string
	for _, part := range parts {
		constParts = append(constParts, snakeToCamel(part))
	}

	return strings.Join(constParts, "::")
}

// snakeToCamel converts a snake_case string to CamelCase.
// e.g. "posts_controller" -> "PostsController", "user" -> "User"
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(p[:1]))
		result.WriteString(p[1:])
	}
	return result.String()
}

// resolveRubyAutoload resolves a Ruby constant name to file paths using the autoload index.
// Handles both exact matches and nested constant lookups.
// e.g. "User" -> ["app/models/user.rb"]
//
//	"Admin::User" -> ["app/models/admin/user.rb"]
func resolveRubyAutoload(constName, importingFile string, index map[string]string) []string {
	// Don't self-import
	if path, ok := index[constName]; ok {
		if path != importingFile {
			return []string{path}
		}
		return nil
	}

	// Try stripping outer module for nested references
	// e.g. if we have Admin::UsersController and index has UsersController
	if idx := strings.LastIndex(constName, "::"); idx > 0 {
		inner := constName[idx+2:]
		if path, ok := index[inner]; ok {
			if path != importingFile {
				return []string{path}
			}
		}
	}

	return nil
}
