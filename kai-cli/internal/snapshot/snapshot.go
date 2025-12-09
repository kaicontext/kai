// Package snapshot handles creating and managing snapshots from file sources.
package snapshot

import (
	"database/sql"
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

	// Create/ensure module nodes first
	moduleIDs := make(map[string][]byte)
	for _, mod := range c.matcher.GetAllModules() {
		payload := c.matcher.GetModulePayload(mod.Name)
		moduleID, err := c.db.InsertNode(tx, graph.KindModule, payload)
		if err != nil {
			return nil, fmt.Errorf("inserting module: %w", err)
		}
		moduleIDs[mod.Name] = moduleID
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

		fileInfos = append(fileInfos, fileInfo{
			id:            fileID,
			path:          file.Path,
			lang:          file.Lang,
			contentDigest: digest,
			modules:       c.matcher.MatchPath(file.Path),
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
		"createdAt":   util.NowMs(),
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
	for i, edge := range edges {
		fileNode, err := c.db.GetNode(edge.Dst)
		if err != nil {
			return fmt.Errorf("getting file node: %w", err)
		}
		if fileNode == nil {
			continue
		}

		// Get filename for progress reporting
		filename, _ := fileNode.Payload["path"].(string)
		if progress != nil {
			progress(i+1, total, filename)
		}

		// Skip binary and image files - they can't be parsed for symbols
		if isBinaryOrImageFile(filename) {
			continue
		}

		// Read the file content
		digest, ok := fileNode.Payload["digest"].(string)
		if !ok {
			continue
		}

		content, err := c.db.ReadObject(digest)
		if err != nil {
			return fmt.Errorf("reading object: %w", err)
		}

		// Skip very large files (likely minified or generated)
		// 500KB is a reasonable limit for symbol extraction
		if len(content) > 500*1024 {
			continue
		}

		// Parse the file
		lang, _ := fileNode.Payload["lang"].(string)
		parsed, err := parser.Parse(content, lang)
		if err != nil {
			// Skip files that can't be parsed
			continue
		}

		// Create symbol nodes
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

			// Create edge: Symbol DEFINES_IN File
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

	// First pass: collect all files and their paths, build a map of file paths to IDs
	type fileInfo struct {
		id       []byte
		path     string
		lang     string
		content  []byte
		isTest   bool
		exported []string // exported symbols
	}
	files := make([]*fileInfo, 0, len(edges))
	filesByPath := make(map[string]*fileInfo)

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

		// Only process supported languages
		if lang != "js" && lang != "ts" && lang != "jsx" && lang != "tsx" && lang != "go" && lang != "py" {
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

	// Second pass: extract imports and build import graph
	// importGraph maps file path -> list of imported file paths
	importGraph := make(map[string][]string)

	total := len(files)
	for i, fi := range files {
		if progress != nil {
			progress(i+1, total, fi.path)
		}

		// Parse for calls
		parsed, err := parser.ExtractCalls(fi.content, fi.lang)
		if err != nil {
			continue
		}

		fi.exported = parsed.Exports

		// Build import graph and collect resolved imports
		var imports []string
		for _, imp := range parsed.Imports {
			if !imp.IsRelative {
				continue // Skip node_modules imports
			}

			// Try to resolve the import
			dir := filepath.Dir(fi.path)
			basePath := parse.ResolveImportPath(dir, imp.Source)

			// Try possible file paths
			var resolved string
			for _, candidate := range parse.PossibleFilePaths(basePath) {
				if _, ok := filesByPath[candidate]; ok {
					resolved = candidate
					break
				}
			}

			if resolved != "" {
				imports = append(imports, resolved)
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
	for _, fi := range files {
		if !fi.isTest {
			continue
		}

		// BFS to find all transitive dependencies
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

	// Now process calls to create edges
	for i, fi := range files {
		if progress != nil {
			progress(i+1, total, fi.path)
		}

		parsed, err := parser.ExtractCalls(fi.content, fi.lang)
		if err != nil {
			continue
		}

		// For each call, try to resolve it
		for _, call := range parsed.Calls {
			// Skip method calls for now (obj.method())
			if call.IsMethodCall {
				continue
			}

			// Check if this call matches an import
			for _, imp := range parsed.Imports {
				if !imp.IsRelative {
					continue
				}

				// Check if the call name is imported from this source
				var importedAs string
				if imp.Default == call.CalleeName {
					importedAs = imp.Default
				} else if originalName, ok := imp.Named[call.CalleeName]; ok {
					importedAs = originalName
				}

				if importedAs != "" {
					// Resolve the import source
					dir := filepath.Dir(fi.path)
					basePath := parse.ResolveImportPath(dir, imp.Source)

					// Try possible file paths
					var resolved string
					for _, candidate := range parse.PossibleFilePaths(basePath) {
						if _, ok := filesByPath[candidate]; ok {
							resolved = candidate
							break
						}
					}

					if resolved != "" {
						if targetFile, ok := filesByPath[resolved]; ok {
							// Get the Symbol node for the caller and callee
							// For now, create a lightweight edge: Caller file -> Callee file with the call info
							// A full implementation would link Symbol -> Symbol

							// Find or create a placeholder for the call relationship
							// Store as edge metadata: src=file, dst=file, with call info in a new Call node
							callPayload := map[string]interface{}{
								"calleeName": call.CalleeName,
								"callerFile": fi.path,
								"calleeFile": resolved,
								"line":       call.Range.Start[0], // Line is first element of [2]int
							}

							// Insert a Call relationship node
							callID, err := c.db.InsertNode(tx, graph.KindSymbol, callPayload)
							if err != nil {
								// Skip errors
								continue
							}

							// Create edge: CallerFile --CALLS--> CalleeFile (via the Call node)
							if err := c.db.InsertEdge(tx, fi.id, graph.EdgeCalls, targetFile.id, callID); err != nil {
								// Skip errors
								continue
							}
						}
					}
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
