// Package watcher provides file system watching for live graph updates.
// When files change, the watcher incrementally updates the semantic graph
// (symbols, imports, calls, tests) without a full recapture.
package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"lukechampine.com/blake3"

	"kai/internal/dirio"
	"kai/internal/graph"
	"kai/internal/ignore"
	"kai/internal/snapshot"
	"kai-core/parse"
)

// Watcher watches a directory for file changes and incrementally
// updates the semantic graph.
type Watcher struct {
	workDir string
	kaiDir  string
	db      *graph.DB
	creator *snapshot.Creator
	parser  *parse.Parser
	matcher *ignore.Matcher
	fsw     *fsnotify.Watcher

	// Debouncing: collect changes for 100ms before processing
	pending   map[string]fsnotify.Op
	pendingMu sync.Mutex
	timer     *time.Timer

	// Cached file map from snapshot (lazy loaded)
	fileMapOnce sync.Once
	fileMap     map[string][]byte // path -> file node ID
	exportMap   map[string]string // exported symbol name -> file path

	// Activity tracking — recent file changes for kai_activity
	activityMu sync.RWMutex
	activity   []ActivityEntry

	// Callbacks
	OnUpdate   func(path string, op string) // called after each file is processed
	OnError    func(err error)
	OnActivity func(entries []ActivityEntry) // called periodically with recent activity (for server push)

	stop chan struct{}
	done chan struct{}
}

// New creates a new file watcher for the given project directory.
func New(workDir string, db *graph.DB) (*Watcher, error) {
	kaiDir := filepath.Join(workDir, ".kai")

	matcher, err := ignore.LoadFromDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("loading ignore patterns: %w", err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating fsnotify watcher: %w", err)
	}

	w := &Watcher{
		workDir: workDir,
		kaiDir:  kaiDir,
		db:      db,
		creator: snapshot.NewCreator(db, nil),
		parser:  parse.NewParser(),
		matcher: matcher,
		fsw:     fsw,
		pending: make(map[string]fsnotify.Op),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	return w, nil
}

// Start begins watching for file changes. Call Stop() to shut down.
func (w *Watcher) Start() error {
	// Walk directories and add them to the watcher
	// fsnotify watches directories, not individual files
	err := filepath.WalkDir(w.workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(w.workDir, path)
		relPath = filepath.ToSlash(relPath)

		// Skip ignored directories
		if relPath != "." && w.matcher != nil && w.matcher.Match(relPath, true) {
			return filepath.SkipDir
		}
		// Always skip .kai and .git
		base := filepath.Base(path)
		if base == ".kai" || base == ".git" || base == "node_modules" {
			return filepath.SkipDir
		}

		if err := w.fsw.Add(path); err != nil {
			// Non-fatal: some dirs may not be watchable
			return nil
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directories: %w", err)
	}

	go w.eventLoop()
	return nil
}

// Stop shuts down the watcher and waits for cleanup.
func (w *Watcher) Stop() {
	close(w.stop)
	w.fsw.Close()
	<-w.done
}

// eventLoop processes fsnotify events with debouncing.
func (w *Watcher) eventLoop() {
	defer close(w.done)

	// Periodically push activity to callbacks (every 30 seconds)
	activityTicker := time.NewTicker(30 * time.Second)
	defer activityTicker.Stop()

	for {
		select {
		case <-w.stop:
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.queueEvent(event)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if w.OnError != nil {
				w.OnError(err)
			}

		case <-activityTicker.C:
			if w.OnActivity != nil {
				entries := w.GetActivity()
				if len(entries) > 0 {
					w.OnActivity(entries)
				}
			}
		}
	}
}

// queueEvent adds an event to the pending map and resets the debounce timer.
func (w *Watcher) queueEvent(event fsnotify.Event) {
	relPath, err := filepath.Rel(w.workDir, event.Name)
	if err != nil {
		return
	}
	relPath = filepath.ToSlash(relPath)

	// Skip non-source files and ignored paths
	if w.shouldIgnore(relPath, event.Name) {
		return
	}

	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	w.pending[relPath] = event.Op

	// Reset debounce timer
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(100*time.Millisecond, w.processPending)
}

// shouldIgnore returns true if the file should not trigger a graph update.
func (w *Watcher) shouldIgnore(relPath, absPath string) bool {
	// Skip .kai directory
	if strings.HasPrefix(relPath, ".kai/") || strings.HasPrefix(relPath, ".git/") {
		return true
	}

	// Check ignore matcher
	if w.matcher != nil && w.matcher.Match(relPath, false) {
		return true
	}

	// Only process files with known language extensions
	lang := dirio.DetectLang(absPath)
	if lang == "" {
		return true
	}

	return false
}

// processPending processes all queued file changes.
func (w *Watcher) processPending() {
	w.pendingMu.Lock()
	batch := w.pending
	w.pending = make(map[string]fsnotify.Op)
	w.pendingMu.Unlock()

	for relPath, op := range batch {
		absPath := filepath.Join(w.workDir, filepath.FromSlash(relPath))

		if op&fsnotify.Remove != 0 || op&fsnotify.Rename != 0 {
			w.handleDelete(relPath)
		} else if op&fsnotify.Create != 0 || op&fsnotify.Write != 0 {
			w.handleCreateOrModify(relPath, absPath)
		}
	}
}

// handleDelete removes a file's symbols and edges from the graph.
func (w *Watcher) handleDelete(relPath string) {
	w.ensureFileMap()

	fileNode := w.findFileByPath(relPath)
	if fileNode == nil {
		return
	}

	// Delete all edges involving this file
	w.db.DeleteEdgesByDst(graph.EdgeDefinesIn, fileNode.ID)
	w.db.DeleteEdgesBySrc(graph.EdgeImports, fileNode.ID)
	w.db.DeleteEdgesByDst(graph.EdgeImports, fileNode.ID)
	w.db.DeleteEdgesBySrc(graph.EdgeCalls, fileNode.ID)
	w.db.DeleteEdgesByDst(graph.EdgeCalls, fileNode.ID)
	w.db.DeleteEdgesBySrc(graph.EdgeTests, fileNode.ID)
	w.db.DeleteEdgesByDst(graph.EdgeTests, fileNode.ID)
	// Delete HAS_FILE edge from snapshot
	w.db.DeleteEdgesByDst(graph.EdgeHasFile, fileNode.ID)

	// Remove from file map
	delete(w.fileMap, relPath)

	// Remove from export map
	for name, path := range w.exportMap {
		if path == relPath {
			delete(w.exportMap, name)
		}
	}

	w.recordActivity(relPath, "deleted")
	if w.OnUpdate != nil {
		w.OnUpdate(relPath, "delete")
	}
}

// handleCreateOrModify re-parses a file and updates its symbols and edges.
func (w *Watcher) handleCreateOrModify(relPath, absPath string) {
	// Read file content
	content, err := os.ReadFile(absPath)
	if err != nil {
		return
	}

	// Skip large files
	if len(content) > 500*1024 {
		return
	}

	lang := dirio.DetectLang(absPath)
	if lang == "" {
		return
	}

	// Normalize language name
	lang = normalizeLang(lang)

	// Only process parseable languages
	if !isParseableLang(lang) {
		return
	}

	// Find existing file node or create one for new files
	fileNode := w.findFileByPath(relPath)
	if fileNode == nil {
		// New file — create a file node and add it to the file map
		w.ensureFileMap()
		snapID := w.getLatestSnapshotID()
		if snapID == nil {
			return
		}

		digest := fmt.Sprintf("%x", blake3.Sum256(content))
		payload := map[string]interface{}{
			"path":   relPath,
			"lang":   lang,
			"digest": digest,
		}
		fileID, err := w.db.InsertNode(nil, graph.KindFile, payload)
		if err != nil || fileID == nil {
			return
		}

		// Link to snapshot
		w.db.InsertEdgeDirect(snapID, graph.EdgeHasFile, fileID, snapID)
		w.fileMap[relPath] = fileID

		fileNode = &graph.Node{ID: fileID, Kind: graph.KindFile, Payload: payload}
	}

	// Parse symbols
	parsed, err := w.parser.Parse(content, lang)
	if err != nil {
		return
	}

	// Delete old DEFINES_IN edges for this file
	w.db.DeleteEdgesByTypeAndDst(graph.EdgeDefinesIn, fileNode.ID)

	// Get the latest snapshot ID for edge context
	snapID := w.getLatestSnapshotID()
	if snapID == nil {
		return
	}

	// Insert new symbols
	for _, sym := range parsed.Symbols {
		payload := map[string]interface{}{
			"fqName":    sym.Name,
			"kind":      sym.Kind,
			"signature": sym.Signature,
			"range": map[string]interface{}{
				"startLine": sym.Range.Start[0],
				"startCol":  sym.Range.Start[1],
				"endLine":   sym.Range.End[0],
				"endCol":    sym.Range.End[1],
			},
		}
		symID, err := w.db.InsertNode(nil, graph.KindSymbol, payload)
		if err != nil || symID == nil {
			continue
		}
		w.db.InsertEdgeDirect(symID, graph.EdgeDefinesIn, fileNode.ID, snapID)
	}

	// Re-parse calls and update IMPORTS/CALLS edges
	callsParsed, err := w.parser.ExtractCalls(content, lang)
	if err == nil {
		// Delete old IMPORTS and CALLS edges from this file
		w.db.DeleteEdgesBySrc(graph.EdgeImports, fileNode.ID)
		w.db.DeleteEdgesBySrc(graph.EdgeCalls, fileNode.ID)

		// Resolve imports against the file map
		w.ensureFileMap()
		for _, imp := range callsParsed.Imports {
			resolved := w.resolveImport(imp, relPath, lang)
			for _, targetPath := range resolved {
				if targetID, ok := w.fileMap[targetPath]; ok {
					w.db.InsertEdgeDirect(fileNode.ID, graph.EdgeImports, targetID, snapID)
				}
			}
		}

		// Resolve calls against exports in imported files
		importedPaths := make(map[string]bool)
		importEdges, _ := w.db.GetEdges(fileNode.ID, graph.EdgeImports)
		for _, e := range importEdges {
			node, _ := w.db.GetNode(e.Dst)
			if node != nil {
				if p, ok := node.Payload["path"].(string); ok {
					importedPaths[p] = true
				}
			}
		}

		for _, call := range callsParsed.Calls {
			calleeName := call.CalleeName
			// Strip scope prefix (Rust: Analyzer::analyze -> analyze)
			if idx := strings.LastIndex(calleeName, "::"); idx >= 0 {
				calleeName = calleeName[idx+2:]
			}
			// Strip receiver prefix (Go: pkg.Function -> Function)
			if idx := strings.LastIndex(calleeName, "."); idx >= 0 {
				calleeName = calleeName[idx+1:]
			}

			// Check exports in imported files
			if targetPath, ok := w.exportMap[calleeName]; ok {
				if importedPaths[targetPath] || targetPath != relPath {
					if targetID, ok := w.fileMap[targetPath]; ok {
						w.db.InsertEdgeDirect(fileNode.ID, graph.EdgeCalls, targetID, snapID)
					}
				}
			}
		}

		// Update exports for this file
		for _, exp := range callsParsed.Exports {
			w.exportMap[exp] = relPath
		}
	}

	// Determine if this was a create or modify
	op := "modified"
	if fileNode != nil && fileNode.Payload != nil {
		if _, existed := w.fileMap[relPath]; !existed {
			op = "created"
		}
	}
	w.recordActivity(relPath, op)

	if w.OnUpdate != nil {
		w.OnUpdate(relPath, "updated")
	}
}

// ensureFileMap lazily loads the file map from the latest snapshot.
func (w *Watcher) ensureFileMap() {
	w.fileMapOnce.Do(func() {
		w.fileMap = make(map[string][]byte)
		w.exportMap = make(map[string]string)

		snapID := w.getLatestSnapshotID()
		if snapID == nil {
			return
		}

		// Get all files from snapshot
		edges, err := w.db.GetEdges(snapID, graph.EdgeHasFile)
		if err != nil {
			return
		}

		for _, edge := range edges {
			node, err := w.db.GetNode(edge.Dst)
			if err != nil || node == nil {
				continue
			}
			path, _ := node.Payload["path"].(string)
			if path != "" {
				w.fileMap[path] = edge.Dst
			}
		}

		// Build export map from existing symbols
		for path, fileID := range w.fileMap {
			edges, err := w.db.GetEdgesByDst(graph.EdgeDefinesIn, fileID)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				symNode, err := w.db.GetNode(edge.Src)
				if err != nil || symNode == nil {
					continue
				}
				name, _ := symNode.Payload["fqName"].(string)
				if name != "" {
					w.exportMap[name] = path
				}
			}
		}
	})
}

// findFileByPath finds a file node by its path in the graph.
func (w *Watcher) findFileByPath(relPath string) *graph.Node {
	// Use the indexed query
	nodes, err := w.db.FindNodesByPayloadPath("File", relPath)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// getLatestSnapshotID returns the latest snapshot's ID.
func (w *Watcher) getLatestSnapshotID() []byte {
	row := w.db.QueryRow(`SELECT target_id FROM refs WHERE name = 'snap.latest'`)
	var id []byte
	if row.Scan(&id) != nil {
		return nil
	}
	return id
}

// ActivityEntry records a file change event.
type ActivityEntry struct {
	Path      string    `json:"path"`
	Operation string    `json:"op"`   // "modified", "created", "deleted"
	Timestamp time.Time `json:"time"`
}

// GetActivity returns recent file change activity (last 5 minutes).
func (w *Watcher) GetActivity() []ActivityEntry {
	w.activityMu.RLock()
	defer w.activityMu.RUnlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	var recent []ActivityEntry
	for _, a := range w.activity {
		if a.Timestamp.After(cutoff) {
			recent = append(recent, a)
		}
	}
	return recent
}

// recordActivity adds an entry and prunes old ones.
func (w *Watcher) recordActivity(path, op string) {
	w.activityMu.Lock()
	defer w.activityMu.Unlock()

	w.activity = append(w.activity, ActivityEntry{
		Path:      path,
		Operation: op,
		Timestamp: time.Now(),
	})

	// Prune entries older than 10 minutes
	cutoff := time.Now().Add(-10 * time.Minute)
	i := 0
	for _, a := range w.activity {
		if a.Timestamp.After(cutoff) {
			w.activity[i] = a
			i++
		}
	}
	w.activity = w.activity[:i]
}

// resolveImport resolves an import to file paths using the cached file map.
func (w *Watcher) resolveImport(imp *parse.Import, importingFile, lang string) []string {
	w.ensureFileMap()
	pathExists := func(p string) bool {
		_, ok := w.fileMap[p]
		return ok
	}

	switch lang {
	case "go":
		// Go: match package path suffix against known directories
		if !strings.Contains(imp.Source, "/") {
			return nil // stdlib
		}
		parts := strings.Split(imp.Source, "/")
		for i := 0; i < len(parts); i++ {
			suffix := strings.Join(parts[i:], "/")
			for path := range w.fileMap {
				dir := filepath.Dir(path)
				if strings.HasSuffix(dir, suffix) && filepath.Dir(importingFile) != dir {
					return []string{path}
				}
			}
		}
	case "rs":
		// Rust: crate::, super::, self::, mod:
		if strings.HasPrefix(imp.Source, "crate::") {
			path := strings.TrimPrefix(imp.Source, "crate::")
			segments := strings.Split(path, "::")
			for i := len(segments); i > 0; i-- {
				candidate := "src/" + filepath.Join(segments[:i]...) + ".rs"
				if pathExists(candidate) {
					return []string{candidate}
				}
				candidate = "src/" + filepath.Join(segments[:i]...) + "/mod.rs"
				if pathExists(candidate) {
					return []string{candidate}
				}
			}
		}
	default:
		// JS/TS/Python/Ruby: relative imports
		if imp.IsRelative {
			dir := filepath.Dir(importingFile)
			base := filepath.Join(dir, imp.Source)
			for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx", ".py", ".rb"} {
				if pathExists(base + ext) {
					return []string{base + ext}
				}
			}
			// index files
			for _, idx := range []string{"/index.ts", "/index.js", "/index.tsx", "/index.jsx"} {
				if pathExists(base + idx) {
					return []string{base + idx}
				}
			}
		}
	}
	return nil
}

// normalizeLang converts long language names to short forms.
func normalizeLang(lang string) string {
	switch lang {
	case "ruby":
		return "rb"
	case "python":
		return "py"
	case "golang":
		return "go"
	case "csharp":
		return "cs"
	case "rust":
		return "rs"
	case "javascript":
		return "js"
	case "typescript":
		return "ts"
	}
	return lang
}

// isParseableLang returns true for languages tree-sitter can parse.
func isParseableLang(lang string) bool {
	switch lang {
	case "go", "py", "rb", "rs", "js", "ts", "jsx", "tsx", "sql", "php", "cs":
		return true
	}
	return false
}
