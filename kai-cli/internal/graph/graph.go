// Package graph provides the SQLite-backed node/edge graph storage.
package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"kai-core/cas"
	coregraph "kai-core/graph"
)

// Re-export types from kai-core/graph for backward compatibility
type NodeKind = coregraph.NodeKind
type EdgeType = coregraph.EdgeType
type Node = coregraph.Node
type Edge = coregraph.Edge

// Re-export constants from kai-core/graph
const (
	KindFile          = coregraph.KindFile
	KindModule        = coregraph.KindModule
	KindSymbol        = coregraph.KindSymbol
	KindSnapshot      = coregraph.KindSnapshot
	KindChangeSet     = coregraph.KindChangeSet
	KindChangeType    = coregraph.KindChangeType
	KindWorkspace     = coregraph.KindWorkspace
	KindReview        = coregraph.KindReview
	KindReviewComment = coregraph.KindReviewComment
	KindIntent        = coregraph.KindIntent
	KindAuthorshipLog = coregraph.KindAuthorshipLog

	EdgeContains     = coregraph.EdgeContains
	EdgeDefinesIn    = coregraph.EdgeDefinesIn
	EdgeHasFile      = coregraph.EdgeHasFile
	EdgeModifies     = coregraph.EdgeModifies
	EdgeHas          = coregraph.EdgeHas
	EdgeAffects      = coregraph.EdgeAffects
	EdgeBasedOn      = coregraph.EdgeBasedOn
	EdgeHeadAt       = coregraph.EdgeHeadAt
	EdgeHasChangeSet = coregraph.EdgeHasChangeSet
	EdgeReviewOf     = coregraph.EdgeReviewOf
	EdgeHasComment   = coregraph.EdgeHasComment
	EdgeAnchorsTo    = coregraph.EdgeAnchorsTo
	EdgeSupersedes   = coregraph.EdgeSupersedes
	EdgeHasIntent    = coregraph.EdgeHasIntent
	EdgeCalls        = coregraph.EdgeCalls
	EdgeImports      = coregraph.EdgeImports
	EdgeTests        = coregraph.EdgeTests
	EdgeAttributedIn = coregraph.EdgeAttributedIn
)

// DB wraps the SQLite database connection.
type DB struct {
	conn       *sql.DB
	objectsDir string
}

// Open opens or creates the database at the given path.
func Open(dbPath, objectsDir string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Fail early if connection is bad
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	// Enable WAL mode
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	// Wait up to 5s on lock instead of failing immediately
	conn.Exec("PRAGMA busy_timeout=5000")

	// NORMAL sync is safe with WAL — only risks loss on OS crash (not app crash).
	// Cuts fsync calls roughly in half vs FULL.
	conn.Exec("PRAGMA synchronous=NORMAL")

	// 64MB page cache (default is ~2MB). Keeps hot pages in memory during
	// snapshot creation and symbol analysis.
	conn.Exec("PRAGMA cache_size=-65536")

	// Memory-map up to 256MB of the DB file. Avoids read() syscalls for
	// frequently accessed pages.
	conn.Exec("PRAGMA mmap_size=268435456")

	// Future-proof: enforce foreign key constraints if we add them
	conn.Exec("PRAGMA foreign_keys=ON")

	db := &DB{conn: conn, objectsDir: objectsDir}

	// Auto-create schema if this is a fresh database (no nodes table)
	db.ensureSchema()

	// Auto-migrate
	db.migrateAuthorship()
	db.migratePathIndex()
	db.migrateRefMeta()

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// ApplySchema applies the schema from a SQL file.
func (db *DB) ApplySchema(schemaPath string) error {
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("reading schema file: %w", err)
	}

	_, err = db.conn.Exec(string(schema))
	if err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}

	// Run migrations
	db.migrateEdgesPK()
	db.migrateAuthorship()
	db.migratePathIndex()
	db.migrateRefMeta()

	return nil
}

// migrateEdgesPK fixes the edges table PK from (src,type,dst,at) to (src,type,dst).
// The old PK included nullable `at`, and SQLite treats each NULL as unique,
// causing unbounded edge accumulation on every capture.
func (db *DB) migrateEdgesPK() {
	// Check if migration is needed by looking for duplicate (src,type,dst) tuples
	var dupeCount int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT src, type, dst FROM edges GROUP BY src, type, dst HAVING COUNT(*) > 1 LIMIT 1
		)
	`).Scan(&dupeCount)
	if err != nil || dupeCount == 0 {
		return // no dupes or error — skip migration
	}

	log.Printf("Migrating edges table: deduplicating...")

	tx, err := db.conn.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	// Create new table with correct PK
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS edges_new (
			src BLOB NOT NULL,
			type TEXT NOT NULL,
			dst BLOB NOT NULL,
			at BLOB,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (src, type, dst)
		)
	`)
	if err != nil {
		return
	}

	// Copy deduplicated rows (keep the one with the latest created_at)
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO edges_new (src, type, dst, at, created_at)
		SELECT src, type, dst, at, MAX(created_at)
		FROM edges
		GROUP BY src, type, dst
	`)
	if err != nil {
		return
	}

	// Swap tables
	tx.Exec(`DROP TABLE edges`)
	tx.Exec(`ALTER TABLE edges_new RENAME TO edges`)

	// Recreate indexes
	tx.Exec(`CREATE INDEX IF NOT EXISTS edges_src ON edges(src)`)
	tx.Exec(`CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst)`)
	tx.Exec(`CREATE INDEX IF NOT EXISTS edges_type ON edges(type)`)
	tx.Exec(`CREATE INDEX IF NOT EXISTS edges_at ON edges(at)`)

	if err := tx.Commit(); err != nil {
		log.Printf("Edge migration failed: %v", err)
		return
	}
	log.Printf("Edge migration complete: deduplicated edges table")
}

// BeginTx starts a new transaction.
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.conn.Begin()
}

// BeginTxCtx starts a new transaction with context for cancellation support.
func (db *DB) BeginTxCtx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.conn.BeginTx(ctx, opts)
}

// InsertNode inserts a node if it doesn't already exist (idempotent).
func (db *DB) InsertNode(tx *sql.Tx, kind NodeKind, payload map[string]interface{}) ([]byte, error) {
	id, err := cas.NodeID(string(kind), payload)
	if err != nil {
		return nil, fmt.Errorf("computing node ID: %w", err)
	}

	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT OR IGNORE INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(kind), string(payloadJSON), cas.NowMs())
	if err != nil {
		return nil, fmt.Errorf("inserting node: %w", err)
	}

	return id, nil
}

// InsertNodeDirect inserts a node directly without transaction.
func (db *DB) InsertNodeDirect(kind NodeKind, payload map[string]interface{}) ([]byte, error) {
	tx, err := db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	id, err := db.InsertNode(tx, kind, payload)
	if err != nil {
		return nil, err
	}

	return id, tx.Commit()
}

// InsertEdge inserts an edge if it doesn't already exist (idempotent).
func (db *DB) InsertEdge(tx *sql.Tx, src []byte, edgeType EdgeType, dst []byte, at []byte) error {
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO edges (src, type, dst, at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, src, string(edgeType), dst, at, cas.NowMs())
	if err != nil {
		return fmt.Errorf("inserting edge: %w", err)
	}
	return nil
}

// InsertEdgeDirect inserts an edge directly without transaction.
func (db *DB) InsertEdgeDirect(src []byte, edgeType EdgeType, dst []byte, at []byte) error {
	tx, err := db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := db.InsertEdge(tx, src, edgeType, dst, at); err != nil {
		return err
	}

	return tx.Commit()
}

// GetNode retrieves a node by ID.
func (db *DB) GetNode(id []byte) (*Node, error) {
	var kind string
	var payloadJSON string
	var createdAt int64

	err := db.conn.QueryRow(`
		SELECT kind, payload, created_at FROM nodes WHERE id = ?
	`, id).Scan(&kind, &payloadJSON, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying node: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling payload: %w", err)
	}

	return &Node{
		ID:        id,
		Kind:      NodeKind(kind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// GetNodeRawPayload retrieves a node's raw JSON payload by ID.
// This is useful for pushing to remote servers where re-serializing
// the payload might produce different JSON due to type changes during unmarshaling.
func (db *DB) GetNodeRawPayload(id []byte) (kind NodeKind, rawPayloadJSON []byte, err error) {
	var kindStr string
	var payloadJSON string

	err = db.conn.QueryRow(`
		SELECT kind, payload FROM nodes WHERE id = ?
	`, id).Scan(&kindStr, &payloadJSON)
	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("querying node: %w", err)
	}

	return NodeKind(kindStr), []byte(payloadJSON), nil
}

// HasNode checks if a node with the given ID exists.
func (db *DB) HasNode(id []byte) (bool, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking node: %w", err)
	}
	return count > 0, nil
}

// GetNodesByKind retrieves all nodes of a specific kind.
func (db *DB) GetNodesByKind(kind NodeKind) ([]*Node, error) {
	rows, err := db.conn.Query(`
		SELECT id, payload, created_at FROM nodes WHERE kind = ? ORDER BY created_at DESC
	`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("querying nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*Node
	for rows.Next() {
		var id []byte
		var payloadJSON string
		var createdAt int64
		if err := rows.Scan(&id, &payloadJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("unmarshaling payload: %w", err)
		}

		nodes = append(nodes, &Node{
			ID:        id,
			Kind:      kind,
			Payload:   payload,
			CreatedAt: createdAt,
		})
	}

	return nodes, rows.Err()
}

// GetEdges retrieves edges from a source node.
func (db *DB) GetEdges(src []byte, edgeType EdgeType) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT dst, at, created_at FROM edges WHERE src = ? AND type = ?
	`, src, string(edgeType))
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var dst, at []byte
		var createdAt int64
		if err := rows.Scan(&dst, &at, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// GetAllEdgesFrom retrieves all edges from a source node across all edge types in a single query.
// Much faster than calling GetEdges once per edge type.
func (db *DB) GetAllEdgesFrom(src []byte) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT type, dst, at, created_at FROM edges WHERE src = ?
	`, src)
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var edgeTypeStr string
		var dst, at []byte
		var createdAt int64
		if err := rows.Scan(&edgeTypeStr, &dst, &at, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		edges = append(edges, &Edge{
			Src:       src,
			Type:      EdgeType(edgeTypeStr),
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}
	return edges, rows.Err()
}

// GetAllEdgesByContext retrieves all edges with a specific context (at) value in a single query.
func (db *DB) GetAllEdgesByContext(at []byte) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT src, type, dst, created_at FROM edges WHERE at = ?
	`, at)
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var edgeTypeStr string
		var src, dst []byte
		var createdAt int64
		if err := rows.Scan(&src, &edgeTypeStr, &dst, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		edges = append(edges, &Edge{
			Src:       src,
			Type:      EdgeType(edgeTypeStr),
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}
	return edges, rows.Err()
}

// GetEdgesOfType retrieves all edges of a specific type.
func (db *DB) GetEdgesOfType(edgeType EdgeType) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT src, dst, at, created_at FROM edges WHERE type = ?
	`, string(edgeType))
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src, dst, at []byte
		var createdAt int64
		if err := rows.Scan(&src, &dst, &at, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// GetEdgesByContext retrieves edges with a specific context (at).
func (db *DB) GetEdgesByContext(at []byte, edgeType EdgeType) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT src, dst, created_at FROM edges WHERE at = ? AND type = ?
	`, at, string(edgeType))
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src, dst []byte
		var createdAt int64
		if err := rows.Scan(&src, &dst, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// GetEdgesByContextAndDst retrieves edges with a specific context and destination.
// More efficient than GetEdgesByContext when you know the destination node.
func (db *DB) GetEdgesByContextAndDst(at []byte, edgeType EdgeType, dst []byte) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT src, created_at FROM edges WHERE at = ? AND type = ? AND dst = ?
	`, at, string(edgeType), dst)
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src []byte
		var createdAt int64
		if err := rows.Scan(&src, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// HasEdgeByDst checks if at least one edge of the given type points to dst.
// Used to skip re-analysis of files that were already parsed in a prior snapshot.
func (db *DB) HasEdgeByDst(edgeType EdgeType, dst []byte) (bool, error) {
	var exists int
	err := db.conn.QueryRow(`
		SELECT 1 FROM edges WHERE type = ? AND dst = ? LIMIT 1
	`, string(edgeType), dst).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetEdgesToByPath finds edges of a given type where the destination node is a File
// with the specified path. This is useful for finding TESTS edges regardless of
// which content-addressed version of the file they point to.
func (db *DB) GetEdgesToByPath(filePath string, edgeType EdgeType) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT e.src, e.dst, e.at, e.created_at
		FROM edges e
		JOIN nodes n ON e.dst = n.id
		WHERE e.type = ?
		AND n.kind = 'File'
		AND json_extract(n.payload, '$.path') = ?
	`, string(edgeType), filePath)
	if err != nil {
		return nil, fmt.Errorf("querying edges by path: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src, dst, at []byte
		var createdAt int64
		if err := rows.Scan(&src, &dst, &at, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// GetEdgesByDst returns all edges of a given type pointing to dst (any context).
func (db *DB) GetEdgesByDst(edgeType EdgeType, dst []byte) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT src, dst, at, created_at FROM edges WHERE type = ? AND dst = ?
	`, string(edgeType), dst)
	if err != nil {
		return nil, fmt.Errorf("querying edges by dst: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src, d, at []byte
		var createdAt int64
		if err := rows.Scan(&src, &d, &at, &createdAt); err != nil {
			return nil, err
		}
		edges = append(edges, &Edge{Src: src, Type: edgeType, Dst: d, At: at, CreatedAt: createdAt})
	}
	return edges, rows.Err()
}

// BatchGetImportersOf finds all files that import any of the given file paths.
// Returns a map: source file path -> true (the set of all importers).
// Single SQL query instead of N queries — critical for BFS impact analysis.
func (db *DB) BatchGetImportersOf(filePaths []string, edgeType EdgeType) (map[string]bool, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}

	// Build placeholders
	placeholders := make([]string, len(filePaths))
	args := make([]interface{}, 0, len(filePaths)+1)
	args = append(args, string(edgeType))
	for i, p := range filePaths {
		placeholders[i] = "?"
		args = append(args, p)
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT json_extract(src_n.payload, '$.path')
		FROM edges e
		JOIN nodes dst_n ON e.dst = dst_n.id
		JOIN nodes src_n ON e.src = src_n.id
		WHERE e.type = ?
		AND dst_n.kind = 'File'
		AND src_n.kind = 'File'
		AND json_extract(dst_n.payload, '$.path') IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch querying importers: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var srcPath string
		if err := rows.Scan(&srcPath); err != nil {
			continue
		}
		result[srcPath] = true
	}
	return result, rows.Err()
}

// UpdateNodePayload updates the payload of an existing node.
func (db *DB) UpdateNodePayload(id []byte, payload map[string]interface{}) error {
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	result, err := db.conn.Exec(`
		UPDATE nodes SET payload = ? WHERE id = ?
	`, string(payloadJSON), id)
	if err != nil {
		return fmt.Errorf("updating node: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("node not found")
	}

	return nil
}

// WriteObject writes raw file bytes to the objects directory.
// Uses atomic write (tmp + rename) to avoid partial writes on crash.
func (db *DB) WriteObject(content []byte) (string, error) {
	digest := cas.Blake3HashHex(content)
	finalPath := filepath.Join(db.objectsDir, digest)

	// Check if already exists
	if _, err := os.Stat(finalPath); err == nil {
		return digest, nil
	}

	// Write to temp file first, then atomic rename
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return "", fmt.Errorf("writing tmp object: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath) // Clean up on failure
		return "", fmt.Errorf("atomic rename: %w", err)
	}

	return digest, nil
}

// ReadObject reads raw file bytes from the objects directory.
func (db *DB) ReadObject(digest string) ([]byte, error) {
	objPath := filepath.Join(db.objectsDir, digest)
	return os.ReadFile(objPath)
}

// GetAllNodesAndEdgesForChangeSet retrieves all nodes and edges related to a changeset.
func (db *DB) GetAllNodesAndEdgesForChangeSet(changeSetID []byte) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Get the changeset node
	csNode, err := db.GetNode(changeSetID)
	if err != nil {
		return nil, err
	}
	if csNode == nil {
		return nil, fmt.Errorf("changeset not found")
	}

	result["changeset"] = map[string]interface{}{
		"id":      cas.BytesToHex(csNode.ID),
		"kind":    string(csNode.Kind),
		"payload": csNode.Payload,
	}

	// Get related edges and nodes
	var relatedNodes []map[string]interface{}
	var relatedEdges []map[string]interface{}

	// Get all edge types from this changeset
	for _, edgeType := range []EdgeType{EdgeModifies, EdgeHas, EdgeAffects, EdgeHasIntent} {
		edges, err := db.GetEdges(changeSetID, edgeType)
		if err != nil {
			return nil, err
		}

		for _, edge := range edges {
			relatedEdges = append(relatedEdges, map[string]interface{}{
				"src":  cas.BytesToHex(edge.Src),
				"type": string(edge.Type),
				"dst":  cas.BytesToHex(edge.Dst),
			})

			// Get the destination node
			node, err := db.GetNode(edge.Dst)
			if err != nil {
				return nil, err
			}
			if node != nil {
				relatedNodes = append(relatedNodes, map[string]interface{}{
					"id":      cas.BytesToHex(node.ID),
					"kind":    string(node.Kind),
					"payload": node.Payload,
				})
			}
		}
	}

	result["nodes"] = relatedNodes
	result["edges"] = relatedEdges

	return result, nil
}

// InsertWorkspace inserts a workspace with a provided ID (UUID-based, not content-addressed).
func (db *DB) InsertWorkspace(tx *sql.Tx, id []byte, payload map[string]interface{}) error {
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(KindWorkspace), string(payloadJSON), cas.NowMs())
	if err != nil {
		return fmt.Errorf("inserting workspace: %w", err)
	}

	return nil
}

// InsertReview inserts a review with a provided ID (UUID-based, not content-addressed).
func (db *DB) InsertReview(tx *sql.Tx, id []byte, payload map[string]interface{}) error {
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(KindReview), string(payloadJSON), cas.NowMs())
	if err != nil {
		return fmt.Errorf("inserting review: %w", err)
	}

	return nil
}

// InsertReviewComment inserts a review comment with a provided ID (UUID-based).
func (db *DB) InsertReviewComment(tx *sql.Tx, id []byte, payload map[string]interface{}) error {
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(KindReviewComment), string(payloadJSON), cas.NowMs())
	if err != nil {
		return fmt.Errorf("inserting review comment: %w", err)
	}

	return nil
}

// GetWorkspaceByName finds a workspace by name.
func (db *DB) GetWorkspaceByName(name string) (*Node, error) {
	rows, err := db.conn.Query(`
		SELECT id, payload, created_at FROM nodes WHERE kind = ?
	`, string(KindWorkspace))
	if err != nil {
		return nil, fmt.Errorf("querying workspaces: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id []byte
		var payloadJSON string
		var createdAt int64
		if err := rows.Scan(&id, &payloadJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("unmarshaling payload: %w", err)
		}

		if wsName, ok := payload["name"].(string); ok && wsName == name {
			return &Node{
				ID:        id,
				Kind:      KindWorkspace,
				Payload:   payload,
				CreatedAt: createdAt,
			}, nil
		}
	}

	return nil, nil
}

// DeleteEdge deletes all edges matching (src, type, dst) across all contexts.
func (db *DB) DeleteEdge(tx *sql.Tx, src []byte, edgeType EdgeType, dst []byte) error {
	_, err := tx.Exec(`
		DELETE FROM edges WHERE src = ? AND type = ? AND dst = ?
	`, src, string(edgeType), dst)
	return err
}

// DeleteEdgeAt deletes a specific edge including its context (at).
// Use this when you need to delete a single edge in a specific context.
func (db *DB) DeleteEdgeAt(tx *sql.Tx, src []byte, edgeType EdgeType, dst []byte, at []byte) error {
	_, err := tx.Exec(`
		DELETE FROM edges WHERE src = ? AND type = ? AND dst = ? AND at = ?
	`, src, string(edgeType), dst, at)
	return err
}

// DeleteEdgeDirect deletes an edge directly without transaction.
func (db *DB) DeleteEdgeDirect(src []byte, edgeType EdgeType, dst []byte) error {
	tx, err := db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := db.DeleteEdge(tx, src, edgeType, dst); err != nil {
		return err
	}

	return tx.Commit()
}

// Query executes a query that returns rows.
func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}

// QueryRow executes a query that returns a single row.
func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return db.conn.QueryRow(query, args...)
}

// Exec executes a query that doesn't return rows.
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.conn.Exec(query, args...)
}

// GetEdgesTo retrieves edges pointing to a destination node.
func (db *DB) GetEdgesTo(dst []byte, edgeType EdgeType) ([]*Edge, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT src, at, created_at FROM edges WHERE dst = ? AND type = ?
	`, dst, string(edgeType))
	if err != nil {
		return nil, fmt.Errorf("querying edges: %w", err)
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var src, at []byte
		var createdAt int64
		if err := rows.Scan(&src, &at, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      edgeType,
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// ensureSchema creates the core tables if the database is fresh.
// This allows graph.Open to work without a prior kai init.
func (db *DB) ensureSchema() {
	// Quick check: if nodes table exists, schema is already applied
	var exists int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='nodes'`).Scan(&exists)
	if err != nil || exists > 0 {
		return
	}

	// Fresh database — apply core schema
	db.conn.Exec(`
CREATE TABLE IF NOT EXISTS nodes (
  id BLOB PRIMARY KEY,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS nodes_created_at ON nodes(created_at);

CREATE TABLE IF NOT EXISTS edges (
  src BLOB NOT NULL,
  type TEXT NOT NULL,
  dst BLOB NOT NULL,
  at  BLOB,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst)
);
CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_type ON edges(type);
CREATE INDEX IF NOT EXISTS edges_at ON edges(at);

CREATE TABLE IF NOT EXISTS refs (
  name TEXT PRIMARY KEY,
  target_id BLOB NOT NULL,
  target_kind TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS refs_kind ON refs(target_kind);

CREATE TABLE IF NOT EXISTS slugs (
  target_id BLOB PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS logs (
  kind TEXT NOT NULL,
  seq INTEGER NOT NULL,
  id BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (kind, seq)
);
CREATE INDEX IF NOT EXISTS logs_id ON logs(id);

CREATE TABLE IF NOT EXISTS ref_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  old_target BLOB,
  new_target BLOB NOT NULL,
  actor TEXT,
  moved_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ref_log_name ON ref_log(name);
CREATE INDEX IF NOT EXISTS ref_log_moved_at ON ref_log(moved_at);
	`)
}

// migrateRefMeta adds a meta column to the refs table for storing git commit info.
func (db *DB) migrateRefMeta() {
	db.conn.Exec(`ALTER TABLE refs ADD COLUMN meta TEXT`)
}

// migratePathIndex creates an index on json_extract(payload, '$.path') for File nodes.
// This makes GetEdgesToByPath queries fast on large repos (64K+ nodes).
func (db *DB) migratePathIndex() {
	// Composite index on (type, dst) for edge lookups by type + destination
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS edges_type_dst ON edges(type, dst)`)
	// Expression index on file path for fast path-based joins
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS nodes_file_path ON nodes(json_extract(payload, '$.path')) WHERE kind = 'File'`)
}

// --- Authorship ---

// migrateAuthorship creates the authorship_ranges table if it doesn't exist.
func (db *DB) migrateAuthorship() {
	db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS authorship_ranges (
			snapshot_id BLOB NOT NULL,
			file_path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			author_type TEXT NOT NULL,
			agent TEXT DEFAULT '',
			model TEXT DEFAULT '',
			session_id TEXT DEFAULT '',
			created_at INTEGER NOT NULL,
			PRIMARY KEY (snapshot_id, file_path, start_line)
		)
	`)
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS authorship_snap ON authorship_ranges(snapshot_id)`)
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS authorship_file ON authorship_ranges(snapshot_id, file_path)`)
}

// AuthorshipRange represents a line range with AI/human attribution.
type AuthorshipRange struct {
	FilePath   string
	StartLine  int
	EndLine    int
	AuthorType string // "ai" or "human"
	Agent      string
	Model      string
	SessionID  string
	CreatedAt  int64
}

// InsertAuthorshipRange inserts an authorship range record.
func (db *DB) InsertAuthorshipRange(tx *sql.Tx, snapshotID []byte, r AuthorshipRange) error {
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO authorship_ranges (snapshot_id, file_path, start_line, end_line, author_type, agent, model, session_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, snapshotID, r.FilePath, r.StartLine, r.EndLine, r.AuthorType, r.Agent, r.Model, r.SessionID, r.CreatedAt)
	return err
}

// GetAuthorshipRanges returns all authorship ranges for a file in a snapshot.
func (db *DB) GetAuthorshipRanges(snapshotID []byte, filePath string) ([]AuthorshipRange, error) {
	rows, err := db.conn.Query(`
		SELECT file_path, start_line, end_line, author_type, agent, model, session_id, created_at
		FROM authorship_ranges
		WHERE snapshot_id = ? AND file_path = ?
		ORDER BY start_line
	`, snapshotID, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ranges []AuthorshipRange
	for rows.Next() {
		var r AuthorshipRange
		if err := rows.Scan(&r.FilePath, &r.StartLine, &r.EndLine, &r.AuthorType, &r.Agent, &r.Model, &r.SessionID, &r.CreatedAt); err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}
	return ranges, rows.Err()
}

// GetAllAuthorshipRanges returns all authorship ranges for a snapshot.
func (db *DB) GetAllAuthorshipRanges(snapshotID []byte) ([]AuthorshipRange, error) {
	rows, err := db.conn.Query(`
		SELECT file_path, start_line, end_line, author_type, agent, model, session_id, created_at
		FROM authorship_ranges
		WHERE snapshot_id = ?
		ORDER BY file_path, start_line
	`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ranges []AuthorshipRange
	for rows.Next() {
		var r AuthorshipRange
		if err := rows.Scan(&r.FilePath, &r.StartLine, &r.EndLine, &r.AuthorType, &r.Agent, &r.Model, &r.SessionID, &r.CreatedAt); err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}
	return ranges, rows.Err()
}
