// Package store defines interfaces for graph and object storage.
package store

import (
	"context"
	"database/sql"

	"kai-core/graph"
)

// NodeStore provides node storage operations.
type NodeStore interface {
	// InsertNode inserts a node if it doesn't already exist (idempotent).
	InsertNode(tx *sql.Tx, kind graph.NodeKind, payload map[string]interface{}) ([]byte, error)

	// InsertNodeDirect inserts a node directly without transaction.
	InsertNodeDirect(kind graph.NodeKind, payload map[string]interface{}) ([]byte, error)

	// GetNode retrieves a node by ID.
	GetNode(id []byte) (*graph.Node, error)

	// GetNodesByKind retrieves all nodes of a specific kind.
	GetNodesByKind(kind graph.NodeKind) ([]*graph.Node, error)

	// UpdateNodePayload updates the payload of an existing node.
	UpdateNodePayload(id []byte, payload map[string]interface{}) error

	// InsertWorkspace inserts a workspace with a provided ID (UUID-based, not content-addressed).
	InsertWorkspace(tx *sql.Tx, id []byte, payload map[string]interface{}) error

	// GetWorkspaceByName finds a workspace by name.
	GetWorkspaceByName(name string) (*graph.Node, error)
}

// EdgeStore provides edge storage operations.
type EdgeStore interface {
	// InsertEdge inserts an edge if it doesn't already exist (idempotent).
	InsertEdge(tx *sql.Tx, src []byte, edgeType graph.EdgeType, dst []byte, at []byte) error

	// InsertEdgeDirect inserts an edge directly without transaction.
	InsertEdgeDirect(src []byte, edgeType graph.EdgeType, dst []byte, at []byte) error

	// GetEdges retrieves edges from a source node.
	GetEdges(src []byte, edgeType graph.EdgeType) ([]*graph.Edge, error)

	// GetEdgesTo retrieves edges pointing to a destination node.
	GetEdgesTo(dst []byte, edgeType graph.EdgeType) ([]*graph.Edge, error)

	// GetEdgesOfType retrieves all edges of a specific type.
	GetEdgesOfType(edgeType graph.EdgeType) ([]*graph.Edge, error)

	// GetEdgesByContext retrieves edges with a specific context (at).
	GetEdgesByContext(at []byte, edgeType graph.EdgeType) ([]*graph.Edge, error)

	// GetEdgesByContextAndDst retrieves edges with a specific context and destination.
	GetEdgesByContextAndDst(at []byte, edgeType graph.EdgeType, dst []byte) ([]*graph.Edge, error)

	// DeleteEdge deletes all edges matching (src, type, dst) across all contexts.
	DeleteEdge(tx *sql.Tx, src []byte, edgeType graph.EdgeType, dst []byte) error

	// DeleteEdgeAt deletes a specific edge including its context (at).
	DeleteEdgeAt(tx *sql.Tx, src []byte, edgeType graph.EdgeType, dst []byte, at []byte) error
}

// ObjectStore provides content-addressable object storage.
type ObjectStore interface {
	// WriteObject writes raw file bytes and returns the digest.
	WriteObject(content []byte) (string, error)

	// ReadObject reads raw file bytes by digest.
	ReadObject(digest string) ([]byte, error)
}

// TransactionStore provides transaction support.
type TransactionStore interface {
	// BeginTx starts a new transaction.
	BeginTx() (*sql.Tx, error)

	// BeginTxCtx starts a new transaction with context for cancellation support.
	BeginTxCtx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// QueryStore provides raw query access.
type QueryStore interface {
	// Query executes a query that returns rows.
	Query(query string, args ...interface{}) (*sql.Rows, error)

	// QueryRow executes a query that returns a single row.
	QueryRow(query string, args ...interface{}) *sql.Row

	// Exec executes a query that doesn't return rows.
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// Store combines all storage interfaces.
type Store interface {
	NodeStore
	EdgeStore
	ObjectStore
	TransactionStore
	QueryStore

	// Close closes the store.
	Close() error

	// ApplySchema applies the schema from a SQL file.
	ApplySchema(schemaPath string) error

	// GetAllNodesAndEdgesForChangeSet retrieves all nodes and edges related to a changeset.
	GetAllNodesAndEdgesForChangeSet(changeSetID []byte) (map[string]interface{}, error)
}
