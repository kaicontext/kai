package workspace

import (
	"fmt"

	"kai/internal/graph"
	"kai/internal/util"
)

// CherryPickResult contains the result of applying a changeset onto a base.
type CherryPickResult struct {
	ResultSnapshot  []byte
	ResultChangeSet []byte
	Conflicts       []Conflict
	AppliedFiles    int
}

// CherryPick applies a changeset onto a target snapshot.
func (m *Manager) CherryPick(changeSetID, targetSnapshotID []byte) (*CherryPickResult, error) {
	csNode, err := m.db.GetNode(changeSetID)
	if err != nil {
		return nil, fmt.Errorf("getting changeset: %w", err)
	}
	if csNode == nil || csNode.Kind != graph.KindChangeSet {
		return nil, fmt.Errorf("changeset not found")
	}

	baseHex, _ := csNode.Payload["base"].(string)
	headHex, _ := csNode.Payload["head"].(string)
	if baseHex == "" || headHex == "" {
		return nil, fmt.Errorf("changeset missing base/head")
	}

	baseID, err := util.HexToBytes(baseHex)
	if err != nil {
		return nil, fmt.Errorf("invalid base: %w", err)
	}
	headID, err := util.HexToBytes(headHex)
	if err != nil {
		return nil, fmt.Errorf("invalid head: %w", err)
	}

	targetSnap, err := m.db.GetNode(targetSnapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting target snapshot: %w", err)
	}
	if targetSnap == nil || targetSnap.Kind != graph.KindSnapshot {
		return nil, fmt.Errorf("target snapshot not found")
	}

	baseFiles, err := m.getSnapshotFileMap(baseID)
	if err != nil {
		return nil, fmt.Errorf("getting base files: %w", err)
	}
	headFiles, err := m.getSnapshotFileMap(headID)
	if err != nil {
		return nil, fmt.Errorf("getting head files: %w", err)
	}
	targetFiles, err := m.getSnapshotFileMap(targetSnapshotID)
	if err != nil {
		return nil, fmt.Errorf("getting target files: %w", err)
	}

	csModified := make(map[string]bool)
	for path, headDigest := range headFiles {
		baseDigest, exists := baseFiles[path]
		if !exists || baseDigest != headDigest {
			csModified[path] = true
		}
	}
	for path := range baseFiles {
		if _, exists := headFiles[path]; !exists {
			csModified[path] = true
		}
	}

	targetModified := make(map[string]bool)
	for path, targetDigest := range targetFiles {
		baseDigest, exists := baseFiles[path]
		if !exists || baseDigest != targetDigest {
			targetModified[path] = true
		}
	}
	for path := range baseFiles {
		if _, exists := targetFiles[path]; !exists {
			targetModified[path] = true
		}
	}

	var conflicts []Conflict
	for path := range csModified {
		if targetModified[path] {
			conflicts = append(conflicts, Conflict{
				Path:        path,
				Description: "File modified in both changeset and target",
				BaseDigest:  baseFiles[path],
				HeadDigest:  headFiles[path],
				NewDigest:   targetFiles[path],
			})
		}
	}
	if len(conflicts) > 0 {
		return &CherryPickResult{Conflicts: conflicts}, nil
	}

	mergedFiles := make(map[string]string)
	for path, digest := range targetFiles {
		mergedFiles[path] = digest
	}

	for path, digest := range headFiles {
		if csModified[path] {
			mergedFiles[path] = digest
		}
	}
	for path := range baseFiles {
		if _, existsInHead := headFiles[path]; !existsInHead {
			delete(mergedFiles, path)
		}
	}

	tx, err := m.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	targetHex := util.BytesToHex(targetSnapshotID)
	mergedSnapPayload := map[string]interface{}{
		"sourceType":       "cherry-pick",
		"sourceRef":        fmt.Sprintf("cherry-pick:%s", util.BytesToHex(changeSetID)[:12]),
		"fileCount":        len(mergedFiles),
		"createdAt":        util.NowMs(),
		"targetSnapshot":   targetHex,
		"appliedChangeSet": util.BytesToHex(changeSetID),
	}

	mergedSnapID, err := m.db.InsertNode(tx, graph.KindSnapshot, mergedSnapPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting merged snapshot: %w", err)
	}

	headFileNodes, err := m.getSnapshotFileNodes(headID)
	if err != nil {
		return nil, err
	}
	targetFileNodes, err := m.getSnapshotFileNodes(targetSnapshotID)
	if err != nil {
		return nil, err
	}

	for path := range mergedFiles {
		var fileNode *graph.Node
		if csModified[path] {
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

	changeSetPayload := map[string]interface{}{
		"base":            targetHex,
		"head":            util.BytesToHex(mergedSnapID),
		"title":           "",
		"description":     fmt.Sprintf("cherry-pick %s", util.BytesToHex(changeSetID)[:12]),
		"intent":          "",
		"createdAt":       util.NowMs(),
		"sourceChangeSet": util.BytesToHex(changeSetID),
	}
	newChangeSetID, err := m.db.InsertNode(tx, graph.KindChangeSet, changeSetPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting changeset: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &CherryPickResult{
		ResultSnapshot:  mergedSnapID,
		ResultChangeSet: newChangeSetID,
		AppliedFiles:    len(csModified),
	}, nil
}
