// Package graph provides the SQLite-backed node/edge graph storage.
package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GCPlan describes what would be deleted by garbage collection.
type GCPlan struct {
	// Nodes that will be deleted
	NodesToDelete []*Node

	// Object digests that will be deleted from the objects directory
	ObjectsToDelete []string

	// Edges that will be deleted (count only, for summary)
	EdgesDeleted int

	// Counts by kind for summary
	SnapshotCount  int
	ChangeSetCount int
	SymbolCount    int
	ModuleCount    int
	FileCount      int

	// Total bytes that will be reclaimed
	BytesReclaimed int64
}

// GCOptions configures the garbage collector.
type GCOptions struct {
	// SinceDays only sweeps nodes older than N days (0 = no limit)
	SinceDays int

	// Aggressive also sweeps Symbols and Modules with no incoming edges
	Aggressive bool

	// DryRun computes plan without executing
	DryRun bool
}

// BuildGCPlan computes what would be deleted by garbage collection.
// It uses a mark-and-sweep algorithm:
// 1. Collect all roots (refs targets, workspace nodes)
// 2. Mark all reachable nodes from roots
// 3. Anything not marked is eligible for deletion
func (db *DB) BuildGCPlan(opts GCOptions) (*GCPlan, error) {
	plan := &GCPlan{}

	// Compute cutoff time
	var cutoffMs int64
	if opts.SinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(opts.SinceDays) * 24 * time.Hour)
		cutoffMs = cutoff.UnixMilli()
	}

	// 1. Collect roots
	roots, err := db.collectRoots()
	if err != nil {
		return nil, fmt.Errorf("collecting roots: %w", err)
	}

	// 2. Mark reachable nodes (BFS)
	marked := make(map[string]bool)
	markedDigests := make(map[string]bool)

	queue := make([][]byte, 0, len(roots))
	for id := range roots {
		queue = append(queue, []byte(id))
		marked[id] = true
	}

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]

		// Get the node
		node, err := db.GetNode(nodeID)
		if err != nil || node == nil {
			continue
		}

		// Mark any file digests
		if node.Kind == KindFile {
			if digest, ok := node.Payload["digest"].(string); ok && digest != "" {
				markedDigests[digest] = true
			}
		}

		// Follow outgoing edges to mark reachable nodes
		edges, err := db.getAllEdgesFrom(nodeID)
		if err != nil {
			continue
		}

		for _, edge := range edges {
			dstKey := string(edge.Dst)
			if !marked[dstKey] {
				marked[dstKey] = true
				queue = append(queue, edge.Dst)
			}
		}
	}

	// 3. Find all nodes not marked
	allNodes, err := db.getAllNodes()
	if err != nil {
		return nil, fmt.Errorf("getting all nodes: %w", err)
	}

	for _, node := range allNodes {
		nodeKey := string(node.ID)
		if marked[nodeKey] {
			continue
		}

		// Check cutoff time
		if cutoffMs > 0 && node.CreatedAt > cutoffMs {
			continue // Too recent, skip
		}

		// In non-aggressive mode, skip Symbols and Modules
		if !opts.Aggressive && (node.Kind == KindSymbol || node.Kind == KindModule) {
			continue
		}

		plan.NodesToDelete = append(plan.NodesToDelete, node)

		switch node.Kind {
		case KindSnapshot:
			plan.SnapshotCount++
		case KindChangeSet:
			plan.ChangeSetCount++
		case KindSymbol:
			plan.SymbolCount++
		case KindModule:
			plan.ModuleCount++
		case KindFile:
			plan.FileCount++
			if digest, ok := node.Payload["digest"].(string); ok && digest != "" {
				if !markedDigests[digest] {
					plan.ObjectsToDelete = append(plan.ObjectsToDelete, digest)
					// Get file size
					objPath := filepath.Join(db.objectsDir, digest)
					if info, err := os.Stat(objPath); err == nil {
						plan.BytesReclaimed += info.Size()
					}
				}
			}
		}
	}

	return plan, nil
}

// ExecuteGC performs garbage collection according to the plan.
func (db *DB) ExecuteGC(plan *GCPlan) error {
	if len(plan.NodesToDelete) == 0 && len(plan.ObjectsToDelete) == 0 {
		return nil
	}

	tx, err := db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete edges touching deleted nodes
	for _, node := range plan.NodesToDelete {
		// Delete edges where this node is src
		_, err := tx.Exec(`DELETE FROM edges WHERE src = ?`, node.ID)
		if err != nil {
			return fmt.Errorf("deleting outgoing edges: %w", err)
		}

		// Delete edges where this node is dst
		_, err = tx.Exec(`DELETE FROM edges WHERE dst = ?`, node.ID)
		if err != nil {
			return fmt.Errorf("deleting incoming edges: %w", err)
		}

		// Delete the node
		_, err = tx.Exec(`DELETE FROM nodes WHERE id = ?`, node.ID)
		if err != nil {
			return fmt.Errorf("deleting node: %w", err)
		}

		// Delete from logs if present
		_, _ = tx.Exec(`DELETE FROM logs WHERE id = ?`, node.ID)

		// Delete from slugs if present
		_, _ = tx.Exec(`DELETE FROM slugs WHERE target_id = ?`, node.ID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// Delete object files (outside transaction since it's filesystem)
	for _, digest := range plan.ObjectsToDelete {
		objPath := filepath.Join(db.objectsDir, digest)
		os.Remove(objPath) // Ignore errors (file might not exist)
	}

	return nil
}

// collectRoots gathers all root node IDs that should not be garbage collected.
// Roots are:
// - All ref targets
// - All workspace nodes (and their base/head snapshots)
func (db *DB) collectRoots() (map[string]bool, error) {
	roots := make(map[string]bool)

	// 1. All ref targets
	rows, err := db.Query(`SELECT target_id FROM refs`)
	if err != nil {
		return nil, fmt.Errorf("querying refs: %w", err)
	}
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		roots[string(id)] = true
	}
	rows.Close()

	// 2. All workspace nodes and their referenced snapshots/changesets
	workspaces, err := db.GetNodesByKind(KindWorkspace)
	if err != nil {
		return nil, fmt.Errorf("getting workspaces: %w", err)
	}

	for _, ws := range workspaces {
		roots[string(ws.ID)] = true

		// Add base and head snapshots
		if baseHex, ok := ws.Payload["baseSnapshot"].(string); ok {
			if baseID, err := hexToBytes(baseHex); err == nil {
				roots[string(baseID)] = true
			}
		}
		if headHex, ok := ws.Payload["headSnapshot"].(string); ok {
			if headID, err := hexToBytes(headHex); err == nil {
				roots[string(headID)] = true
			}
		}

		// Add open changesets
		if csArr, ok := ws.Payload["openChangeSets"].([]interface{}); ok {
			for _, csHex := range csArr {
				if hexStr, ok := csHex.(string); ok {
					if csID, err := hexToBytes(hexStr); err == nil {
						roots[string(csID)] = true
					}
				}
			}
		}
	}

	return roots, nil
}

// getAllNodes returns all nodes in the database.
func (db *DB) getAllNodes() ([]*Node, error) {
	rows, err := db.Query(`SELECT id, kind, payload, created_at FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*Node
	for rows.Next() {
		var id []byte
		var kind, payloadJSON string
		var createdAt int64
		if err := rows.Scan(&id, &kind, &payloadJSON, &createdAt); err != nil {
			return nil, err
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, err
		}

		nodes = append(nodes, &Node{
			ID:        id,
			Kind:      NodeKind(kind),
			Payload:   payload,
			CreatedAt: createdAt,
		})
	}

	return nodes, rows.Err()
}

// getAllEdgesFrom returns all edges from a source node.
func (db *DB) getAllEdgesFrom(src []byte) ([]*Edge, error) {
	rows, err := db.Query(`SELECT type, dst, at, created_at FROM edges WHERE src = ?`, src)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []*Edge
	for rows.Next() {
		var edgeType string
		var dst, at []byte
		var createdAt int64
		if err := rows.Scan(&edgeType, &dst, &at, &createdAt); err != nil {
			return nil, err
		}

		edges = append(edges, &Edge{
			Src:       src,
			Type:      EdgeType(edgeType),
			Dst:       dst,
			At:        at,
			CreatedAt: createdAt,
		})
	}

	return edges, rows.Err()
}

// hexToBytes converts a hex string to bytes.
func hexToBytes(s string) ([]byte, error) {
	if len(s) == 0 {
		return nil, fmt.Errorf("empty hex string")
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v*16 + (c - '0')
			case c >= 'a' && c <= 'f':
				v = v*16 + (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				v = v*16 + (c - 'A' + 10)
			default:
				return nil, fmt.Errorf("invalid hex char: %c", c)
			}
		}
		b[i] = v
	}
	return b, nil
}
