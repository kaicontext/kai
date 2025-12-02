// Package snapshot handles creating and managing snapshots from Git refs.
package snapshot

import (
	"database/sql"
	"fmt"

	"ivcs/internal/gitio"
	"ivcs/internal/graph"
	"ivcs/internal/module"
	"ivcs/internal/parse"
	"ivcs/internal/util"
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

// CreateSnapshot creates a snapshot from a Git ref.
func (c *Creator) CreateSnapshot(repoPath, gitRef string) ([]byte, error) {
	// Open the repository
	repo, err := gitio.Open(repoPath)
	if err != nil {
		return nil, fmt.Errorf("opening repo: %w", err)
	}

	// Resolve the ref to a commit
	commit, err := repo.ResolveRef(gitRef)
	if err != nil {
		return nil, fmt.Errorf("resolving ref: %w", err)
	}

	// Get all TS/JS files
	files, err := repo.GetTreeFiles(commit)
	if err != nil {
		return nil, fmt.Errorf("getting files: %w", err)
	}

	// Start transaction
	tx, err := c.db.BeginTx()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Create snapshot node
	snapshotPayload := map[string]interface{}{
		"gitRef":    gitRef,
		"fileCount": len(files),
		"createdAt": util.NowMs(),
	}
	snapshotID, err := c.db.InsertNode(tx, graph.KindSnapshot, snapshotPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting snapshot: %w", err)
	}

	// Create/ensure module nodes
	moduleIDs := make(map[string][]byte)
	for _, mod := range c.matcher.GetAllModules() {
		payload := c.matcher.GetModulePayload(mod.Name)
		moduleID, err := c.db.InsertNode(tx, graph.KindModule, payload)
		if err != nil {
			return nil, fmt.Errorf("inserting module: %w", err)
		}
		moduleIDs[mod.Name] = moduleID
	}

	// Process each file
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

		// Create edge: Snapshot HAS_FILE File
		if err := c.db.InsertEdge(tx, snapshotID, graph.EdgeHasFile, fileID, nil); err != nil {
			return nil, fmt.Errorf("inserting HAS_FILE edge: %w", err)
		}

		// Map file to modules
		modules := c.matcher.MatchPath(file.Path)
		for _, modName := range modules {
			if moduleID, ok := moduleIDs[modName]; ok {
				// Create edge: Module CONTAINS File
				if err := c.db.InsertEdge(tx, moduleID, graph.EdgeContains, fileID, snapshotID); err != nil {
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

// AnalyzeSymbols extracts symbols from all files in a snapshot.
func (c *Creator) AnalyzeSymbols(snapshotID []byte) error {
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

	for _, edge := range edges {
		fileNode, err := c.db.GetNode(edge.Dst)
		if err != nil {
			return fmt.Errorf("getting file node: %w", err)
		}
		if fileNode == nil {
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
	edges, err := c.db.GetEdgesByContext(snapshotID, graph.EdgeDefinesIn)
	if err != nil {
		return nil, err
	}

	var symbols []*graph.Node
	for _, edge := range edges {
		if string(edge.Dst) == string(fileID) {
			node, err := c.db.GetNode(edge.Src)
			if err != nil {
				return nil, err
			}
			if node != nil {
				symbols = append(symbols, node)
			}
		}
	}

	return symbols, nil
}

// FindSnapshotByRef finds a snapshot by its git ref.
func FindSnapshotByRef(db *graph.DB, gitRef string) ([]byte, error) {
	snapshots, err := db.GetNodesByKind(graph.KindSnapshot)
	if err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		if ref, ok := snap.Payload["gitRef"].(string); ok && ref == gitRef {
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
