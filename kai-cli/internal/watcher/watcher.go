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

	// Callbacks
	OnUpdate func(path string, op string) // called after each file is processed
	OnError  func(err error)

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
	// Find the file node by path
	fileNode := w.findFileByPath(relPath)
	if fileNode == nil {
		return
	}

	// Delete DEFINES_IN edges where this file is the destination
	w.db.DeleteEdgesByDst(graph.EdgeDefinesIn, fileNode.ID)
	// Delete IMPORTS edges where this file is the source
	w.db.DeleteEdgesBySrc(graph.EdgeImports, fileNode.ID)
	// Delete CALLS edges where this file is the source
	w.db.DeleteEdgesBySrc(graph.EdgeCalls, fileNode.ID)
	// Delete TESTS edges involving this file
	w.db.DeleteEdgesBySrc(graph.EdgeTests, fileNode.ID)
	w.db.DeleteEdgesByDst(graph.EdgeTests, fileNode.ID)

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

	// Find existing file node
	fileNode := w.findFileByPath(relPath)
	if fileNode == nil {
		// New file — we can't create a proper file node without a snapshot context.
		// The file will be picked up on the next kai capture.
		if w.OnUpdate != nil {
			w.OnUpdate(relPath, "new (pending capture)")
		}
		return
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

		// Re-resolve imports (simplified — full resolution needs all files)
		// For now, just clear the old edges. Full re-resolution happens on next capture.
		_ = callsParsed
	}

	if w.OnUpdate != nil {
		w.OnUpdate(relPath, "updated")
	}
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
