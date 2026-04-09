// Package workspace provides integration operations for workspaces.
package workspace

import (
	"fmt"

	"kai-core/merge"

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

	// Attempt semantic merge for files modified on both sides
	var conflicts []Conflict
	merger := merge.NewMerger()
	semanticMerged := make(map[string][]byte)

	for path := range wsModified {
		if !targetModified[path] {
			continue
		}

		baseDigest := baseFiles[path]
		headDigest := headFiles[path]
		targetDigest := targetFiles[path]

		if baseDigest == "" || headDigest == "" || targetDigest == "" {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: "File modified in both workspace and target",
				BaseDigest:  baseDigest,
				HeadDigest:  headDigest,
				NewDigest:   targetDigest,
			})
			continue
		}

		baseContent, err := m.db.ReadObject(baseDigest)
		headContent, err2 := m.db.ReadObject(headDigest)
		targetContent, err3 := m.db.ReadObject(targetDigest)
		if err != nil || err2 != nil || err3 != nil {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: "File modified in both workspace and target (content not available for semantic merge)",
				BaseDigest:  baseDigest,
				HeadDigest:  headDigest,
				NewDigest:   targetDigest,
			})
			continue
		}

		lang := normalizeMergeLang(path)
		if lang == "" {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: "File modified in both workspace and target (unsupported language for semantic merge)",
				BaseDigest:  baseDigest,
				HeadDigest:  headDigest,
				NewDigest:   targetDigest,
			})
			continue
		}

		mergeResult, mergeErr := merger.MergeFiles(
			map[string][]byte{path: baseContent},
			map[string][]byte{path: headContent},
			map[string][]byte{path: targetContent},
			lang,
		)
		if mergeErr != nil {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: fmt.Sprintf("Semantic merge failed: %v", mergeErr),
				BaseDigest:  baseDigest,
				HeadDigest:  headDigest,
				NewDigest:   targetDigest,
			})
			continue
		}

		if !mergeResult.Success {
			for _, mc := range mergeResult.Conflicts {
				conflicts = append(conflicts, Conflict{
					Path:        path,
					Description: fmt.Sprintf("[%s] %s", mc.Kind, mc.Message),
					BaseDigest:  baseDigest,
					HeadDigest:  headDigest,
					NewDigest:   targetDigest,
				})
			}
			continue
		}

		if content, ok := mergeResult.Files[path]; ok {
			semanticMerged[path] = content
		}
	}

	if len(conflicts) > 0 {
		return &IntegrateResult{
			Conflicts: conflicts,
		}, nil
	}

	// Build merged file set: start with target, apply workspace changes
	mergedFiles := make(map[string]string)
	for path, digest := range targetFiles {
		mergedFiles[path] = digest
	}
	for path, digest := range headFiles {
		if wsModified[path] {
			mergedFiles[path] = digest
		}
	}
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

	// Write semantically merged files
	semanticMergedNodes := make(map[string]*graph.Node)
	for path, content := range semanticMerged {
		digest, err := m.db.WriteObject(content)
		if err != nil {
			return nil, fmt.Errorf("writing merged content for %s: %w", path, err)
		}
		lang := normalizeMergeLang(path)
		filePayload := map[string]interface{}{
			"path":   path,
			"lang":   lang,
			"digest": digest,
		}
		fileID, err := m.db.InsertNode(tx, graph.KindFile, filePayload)
		if err != nil {
			return nil, fmt.Errorf("inserting merged file node for %s: %w", path, err)
		}
		semanticMergedNodes[path] = &graph.Node{ID: fileID, Kind: graph.KindFile, Payload: filePayload}
		mergedFiles[path] = digest
	}

	autoResolved := len(semanticMerged)

	mergedSnapPayload := map[string]interface{}{
		"sourceType":     "merged",
		"sourceRef":      fmt.Sprintf("integrate:%s->%s", util.BytesToHex(ws.ID)[:12], targetHex[:12]),
		"fileCount":      len(mergedFiles),
		"createdAt":      util.NowMs(),
		"integratedFrom": util.BytesToHex(ws.ID),
		"targetSnapshot": targetHex,
	}
	if autoResolved > 0 {
		mergedSnapPayload["autoResolved"] = autoResolved
	}

	mergedSnapID, err := m.db.InsertNode(tx, graph.KindSnapshot, mergedSnapPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting merged snapshot: %w", err)
	}

	headFileNodes, err := m.getSnapshotFileNodes(ws.HeadSnapshot)
	if err != nil {
		return nil, err
	}
	targetFileNodes, err := m.getSnapshotFileNodes(targetSnapshotID)
	if err != nil {
		return nil, err
	}

	for path := range mergedFiles {
		var fileNode *graph.Node
		if semanticMergedNodes[path] != nil {
			fileNode = semanticMergedNodes[path]
		} else if wsModified[path] {
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
		AutoResolved:      autoResolved,
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
