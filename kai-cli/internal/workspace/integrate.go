// Package workspace provides integration operations for workspaces.
package workspace

import (
	"fmt"

	"kai/internal/graph"
	"kai/internal/util"
)

// IntegrateResult contains the result of integrating a workspace.
type IntegrateResult struct {
	ResultSnapshot    []byte
	AppliedChangeSets [][]byte
	Conflicts         []Conflict
	AutoResolved      int
}

// Integrate merges a workspace's changes into a target snapshot.
func (m *Manager) Integrate(nameOrID string, targetSnapshotID []byte) (*IntegrateResult, error) {
	ws, err := m.Get(nameOrID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace not found: %s", nameOrID)
	}
	if ws.Status == StatusClosed {
		return nil, fmt.Errorf("workspace is closed")
	}
	if len(ws.OpenChangeSets) == 0 {
		return nil, fmt.Errorf("workspace has no changes to integrate")
	}

	// Verify target snapshot exists
	targetSnap, err := m.db.GetNode(targetSnapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting target snapshot: %w", err)
	}
	if targetSnap == nil {
		return nil, fmt.Errorf("target snapshot not found")
	}
	if targetSnap.Kind != graph.KindSnapshot {
		return nil, fmt.Errorf("target must be a snapshot, got %s", targetSnap.Kind)
	}

	// For now, we do a simple fast-forward if possible:
	// If target == base, we can just use head as the result
	// Otherwise, we need to do a proper merge (future enhancement)

	baseHex := util.BytesToHex(ws.BaseSnapshot)
	targetHex := util.BytesToHex(targetSnapshotID)

	if baseHex == targetHex {
		// Fast-forward: target hasn't changed since we branched
		// The workspace head becomes the new target
		return &IntegrateResult{
			ResultSnapshot:    ws.HeadSnapshot,
			AppliedChangeSets: ws.OpenChangeSets,
			AutoResolved:      0,
		}, nil
	}

	// Non-fast-forward case: need to check for conflicts
	// For now, we detect if any files were modified in both target and workspace

	// Get files from base, target, and head
	baseFiles, err := m.getSnapshotFileMap(ws.BaseSnapshot)
	if err != nil {
		return nil, fmt.Errorf("getting base files: %w", err)
	}

	targetFiles, err := m.getSnapshotFileMap(targetSnapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting target files: %w", err)
	}

	headFiles, err := m.getSnapshotFileMap(ws.HeadSnapshot)
	if err != nil {
		return nil, fmt.Errorf("getting head files: %w", err)
	}

	// Find files modified in workspace (base -> head)
	wsModified := make(map[string]bool)
	for path, headDigest := range headFiles {
		baseDigest, exists := baseFiles[path]
		if !exists || baseDigest != headDigest {
			wsModified[path] = true
		}
	}
	// Files deleted in workspace
	for path := range baseFiles {
		if _, exists := headFiles[path]; !exists {
			wsModified[path] = true
		}
	}

	// Find files modified in target (base -> target)
	targetModified := make(map[string]bool)
	for path, targetDigest := range targetFiles {
		baseDigest, exists := baseFiles[path]
		if !exists || baseDigest != targetDigest {
			targetModified[path] = true
		}
	}
	// Files deleted in target
	for path := range baseFiles {
		if _, exists := targetFiles[path]; !exists {
			targetModified[path] = true
		}
	}

	// Check for conflicts: files modified in both
	var conflicts []Conflict
	for path := range wsModified {
		if targetModified[path] {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: "File modified in both workspace and target",
				BaseDigest:  baseFiles[path],
				HeadDigest:  headFiles[path],
				NewDigest:   targetFiles[path],
			})
		}
	}

	if len(conflicts) > 0 {
		return &IntegrateResult{
			Conflicts: conflicts,
		}, nil
	}

	// No conflicts: create merged snapshot
	// Start with target files, apply workspace changes
	mergedFiles := make(map[string]string)
	for path, digest := range targetFiles {
		mergedFiles[path] = digest
	}

	// Apply workspace changes (additions and modifications)
	for path, digest := range headFiles {
		if wsModified[path] {
			mergedFiles[path] = digest
		}
	}

	// Apply workspace deletions
	for path := range baseFiles {
		if _, existsInHead := headFiles[path]; !existsInHead {
			delete(mergedFiles, path)
		}
	}

	// Create the merged snapshot
	tx, err := m.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	mergedSnapPayload := map[string]interface{}{
		"sourceType":     "merged",
		"sourceRef":      fmt.Sprintf("integrate:%s->%s", util.BytesToHex(ws.ID)[:12], targetHex[:12]),
		"fileCount":      len(mergedFiles),
		"createdAt":      util.NowMs(),
		"integratedFrom": util.BytesToHex(ws.ID),
		"targetSnapshot": targetHex,
	}

	mergedSnapID, err := m.db.InsertNode(tx, graph.KindSnapshot, mergedSnapPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting merged snapshot: %w", err)
	}

	// Get file nodes from head and target to reuse
	headFileNodes, err := m.getSnapshotFileNodes(ws.HeadSnapshot)
	if err != nil {
		return nil, err
	}
	targetFileNodes, err := m.getSnapshotFileNodes(targetSnapshotID)
	if err != nil {
		return nil, err
	}

	// Create HAS_FILE edges for merged snapshot
	for path := range mergedFiles {
		var fileNode *graph.Node
		if wsModified[path] {
			fileNode = headFileNodes[path]
		} else {
			fileNode = targetFileNodes[path]
		}
		if fileNode != nil {
			if err := m.db.InsertEdge(tx, mergedSnapID, graph.EdgeHasFile, fileNode.ID, nil); err != nil {
				return nil, fmt.Errorf("inserting HAS_FILE edge: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &IntegrateResult{
		ResultSnapshot:    mergedSnapID,
		AppliedChangeSets: ws.OpenChangeSets,
		AutoResolved:      len(wsModified) + len(targetModified) - len(conflicts),
	}, nil
}

// getSnapshotFileMap returns a map of path -> digest for a snapshot.
func (m *Manager) getSnapshotFileMap(snapshotID []byte) (map[string]string, error) {
	edges, err := m.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}

	fileMap := make(map[string]string)
	for _, edge := range edges {
		node, err := m.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil {
			path, _ := node.Payload["path"].(string)
			digest, _ := node.Payload["digest"].(string)
			fileMap[path] = digest
		}
	}

	return fileMap, nil
}

// getSnapshotFileNodes returns a map of path -> Node for a snapshot.
func (m *Manager) getSnapshotFileNodes(snapshotID []byte) (map[string]*graph.Node, error) {
	edges, err := m.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}

	nodeMap := make(map[string]*graph.Node)
	for _, edge := range edges {
		node, err := m.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node != nil {
			path, _ := node.Payload["path"].(string)
			nodeMap[path] = node
		}
	}

	return nodeMap, nil
}
