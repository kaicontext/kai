// Package graph provides the SQLite-backed node/edge graph storage.
package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"ivcs/internal/util"
)

// NodeKind represents the type of a node.
type NodeKind string

const (
	KindFile       NodeKind = "File"
	KindModule     NodeKind = "Module"
	KindSymbol     NodeKind = "Symbol"
	KindSnapshot   NodeKind = "Snapshot"
	KindChangeSet  NodeKind = "ChangeSet"
	KindChangeType NodeKind = "ChangeType"
	KindWorkspace  NodeKind = "Workspace"
)

// EdgeType represents the type of relationship between nodes.
type EdgeType string

const (
	EdgeContains     EdgeType = "CONTAINS"
	EdgeDefinesIn    EdgeType = "DEFINES_IN"
	EdgeHasFile      EdgeType = "HAS_FILE"
	EdgeModifies     EdgeType = "MODIFIES"
	EdgeHas          EdgeType = "HAS"
	EdgeAffects      EdgeType = "AFFECTS"
	EdgeBasedOn      EdgeType = "BASED_ON"      // Workspace -> base Snapshot
	EdgeHeadAt       EdgeType = "HEAD_AT"       // Workspace -> head Snapshot
	EdgeHasChangeSet EdgeType = "HAS_CHANGESET" // Workspace -> ChangeSet (ordered)
)

// Node represents a node in the graph.
type Node struct {
	ID        []byte
	Kind      NodeKind
	Payload   map[string]interface{}
	CreatedAt int64
}

// Edge represents an edge in the graph.
type Edge struct {
	Src       []byte
	Type      EdgeType
	Dst       []byte
	At        []byte // context (snapshot or changeset ID), can be nil
	CreatedAt int64
}

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

	// Enable WAL mode
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	return &DB{conn: conn, objectsDir: objectsDir}, nil
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

	return nil
}

// BeginTx starts a new transaction.
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.conn.Begin()
}

// InsertNode inserts a node if it doesn't already exist (idempotent).
func (db *DB) InsertNode(tx *sql.Tx, kind NodeKind, payload map[string]interface{}) ([]byte, error) {
	id, err := util.NodeID(string(kind), payload)
	if err != nil {
		return nil, fmt.Errorf("computing node ID: %w", err)
	}

	payloadJSON, err := util.CanonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT OR IGNORE INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(kind), string(payloadJSON), util.NowMs())
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
	`, src, string(edgeType), dst, at, util.NowMs())
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

// GetNodesByKind retrieves all nodes of a specific kind.
func (db *DB) GetNodesByKind(kind NodeKind) ([]*Node, error) {
	rows, err := db.conn.Query(`
		SELECT id, payload, created_at FROM nodes WHERE kind = ?
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
		SELECT dst, at, created_at FROM edges WHERE src = ? AND type = ?
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

// UpdateNodePayload updates the payload of an existing node.
func (db *DB) UpdateNodePayload(id []byte, payload map[string]interface{}) error {
	payloadJSON, err := util.CanonicalJSON(payload)
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
func (db *DB) WriteObject(content []byte) (string, error) {
	digest := util.Blake3HashHex(content)
	objPath := filepath.Join(db.objectsDir, digest)

	// Check if already exists
	if _, err := os.Stat(objPath); err == nil {
		return digest, nil
	}

	if err := os.WriteFile(objPath, content, 0644); err != nil {
		return "", fmt.Errorf("writing object: %w", err)
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
		"id":      util.BytesToHex(csNode.ID),
		"kind":    string(csNode.Kind),
		"payload": csNode.Payload,
	}

	// Get related edges and nodes
	var relatedNodes []map[string]interface{}
	var relatedEdges []map[string]interface{}

	// Get all edge types from this changeset
	for _, edgeType := range []EdgeType{EdgeModifies, EdgeHas, EdgeAffects} {
		edges, err := db.GetEdges(changeSetID, edgeType)
		if err != nil {
			return nil, err
		}

		for _, edge := range edges {
			relatedEdges = append(relatedEdges, map[string]interface{}{
				"src":  util.BytesToHex(edge.Src),
				"type": string(edge.Type),
				"dst":  util.BytesToHex(edge.Dst),
			})

			// Get the destination node
			node, err := db.GetNode(edge.Dst)
			if err != nil {
				return nil, err
			}
			if node != nil {
				relatedNodes = append(relatedNodes, map[string]interface{}{
					"id":      util.BytesToHex(node.ID),
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
	payloadJSON, err := util.CanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO nodes (id, kind, payload, created_at)
		VALUES (?, ?, ?, ?)
	`, id, string(KindWorkspace), string(payloadJSON), util.NowMs())
	if err != nil {
		return fmt.Errorf("inserting workspace: %w", err)
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

// DeleteEdge deletes an edge.
func (db *DB) DeleteEdge(tx *sql.Tx, src []byte, edgeType EdgeType, dst []byte) error {
	_, err := tx.Exec(`
		DELETE FROM edges WHERE src = ? AND type = ? AND dst = ?
	`, src, string(edgeType), dst)
	return err
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
		SELECT src, at, created_at FROM edges WHERE dst = ? AND type = ?
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
