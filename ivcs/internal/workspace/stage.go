// Package workspace provides staging operations for workspaces.
package workspace

import (
	"fmt"

	"ivcs/internal/classify"
	"ivcs/internal/filesource"
	"ivcs/internal/graph"
	"ivcs/internal/module"
	"ivcs/internal/snapshot"
	"ivcs/internal/util"
)

// StageResult contains the result of staging changes.
type StageResult struct {
	ChangeSetID  []byte
	HeadSnapshot []byte
	ChangedFiles int
	ChangeTypes  int
	Conflicts    []Conflict
}

// Conflict represents a merge conflict.
type Conflict struct {
	Path        string
	Description string
	BaseDigest  string
	HeadDigest  string
	NewDigest   string
}

// Stage stages changes from a file source into a workspace.
func (m *Manager) Stage(nameOrID string, source filesource.FileSource, matcher *module.Matcher) (*StageResult, error) {
	ws, err := m.Get(nameOrID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace not found: %s", nameOrID)
	}
	if ws.Status != StatusActive {
		return nil, fmt.Errorf("workspace is %s, must be active to stage", ws.Status)
	}

	// Create a new snapshot from the source
	creator := snapshot.NewCreator(m.db, matcher)
	newSnapID, err := creator.CreateSnapshot(source)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot from source: %w", err)
	}

	// Get files from head snapshot and new snapshot
	headFiles, err := creator.GetSnapshotFiles(ws.HeadSnapshot)
	if err != nil {
		return nil, fmt.Errorf("getting head files: %w", err)
	}

	newFiles, err := creator.GetSnapshotFiles(newSnapID)
	if err != nil {
		return nil, fmt.Errorf("getting new files: %w", err)
	}

	// Build file maps
	headFileMap := make(map[string]*graph.Node)
	newFileMap := make(map[string]*graph.Node)

	for _, f := range headFiles {
		if path, ok := f.Payload["path"].(string); ok {
			headFileMap[path] = f
		}
	}

	for _, f := range newFiles {
		if path, ok := f.Payload["path"].(string); ok {
			newFileMap[path] = f
		}
	}

	// Find changed files
	var changedPaths []string
	var changedFileIDs [][]byte

	for path, newFile := range newFileMap {
		headFile, exists := headFileMap[path]
		if !exists {
			// Added file
			changedPaths = append(changedPaths, path)
			changedFileIDs = append(changedFileIDs, newFile.ID)
		} else {
			// Check if digest differs
			headDigest, _ := headFile.Payload["digest"].(string)
			newDigest, _ := newFile.Payload["digest"].(string)
			if headDigest != newDigest {
				changedPaths = append(changedPaths, path)
				changedFileIDs = append(changedFileIDs, newFile.ID)
			}
		}
	}

	// Check for deleted files
	for path := range headFileMap {
		if _, exists := newFileMap[path]; !exists {
			changedPaths = append(changedPaths, path)
		}
	}

	if len(changedPaths) == 0 {
		return &StageResult{
			ChangeSetID:  nil,
			HeadSnapshot: ws.HeadSnapshot,
			ChangedFiles: 0,
			ChangeTypes:  0,
		}, nil
	}

	// Start transaction
	tx, err := m.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Create changeset node
	changeSetPayload := map[string]interface{}{
		"base":        util.BytesToHex(ws.HeadSnapshot),
		"head":        util.BytesToHex(newSnapID),
		"title":       "",
		"description": "",
		"intent":      "",
		"workspaceId": util.BytesToHex(ws.ID),
		"createdAt":   util.NowMs(),
	}
	changeSetID, err := m.db.InsertNode(tx, graph.KindChangeSet, changeSetPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting changeset: %w", err)
	}

	// Detect change types
	detector := classify.NewDetector()
	var allChangeTypes []*classify.ChangeType
	var affectedModules []string
	affectedModulesSet := make(map[string]bool)

	for i, path := range changedPaths {
		newFile := newFileMap[path]
		headFile := headFileMap[path]

		var beforeContent, afterContent []byte

		// Read after content
		if newFile != nil {
			if digest, ok := newFile.Payload["digest"].(string); ok {
				afterContent, _ = m.db.ReadObject(digest)
			}
		}

		// Read before content
		if headFile != nil {
			if digest, ok := headFile.Payload["digest"].(string); ok {
				beforeContent, _ = m.db.ReadObject(digest)
			}
		}

		if len(beforeContent) > 0 && len(afterContent) > 0 && i < len(changedFileIDs) {
			changes, err := detector.DetectChanges(path, beforeContent, afterContent, util.BytesToHex(changedFileIDs[i]))
			if err == nil {
				allChangeTypes = append(allChangeTypes, changes...)
			}
		}

		// Create MODIFIES edge to file
		if i < len(changedFileIDs) {
			if err := m.db.InsertEdge(tx, changeSetID, graph.EdgeModifies, changedFileIDs[i], nil); err != nil {
				return nil, fmt.Errorf("inserting MODIFIES edge: %w", err)
			}
		}

		// Map to modules
		if matcher != nil {
			modules := matcher.MatchPath(path)
			for _, mod := range modules {
				if !affectedModulesSet[mod] {
					affectedModulesSet[mod] = true
					affectedModules = append(affectedModules, mod)
				}
			}
		}
	}

	// Create ChangeType nodes and HAS edges
	for _, ct := range allChangeTypes {
		payload := classify.GetCategoryPayload(ct)
		ctID, err := m.db.InsertNode(tx, graph.KindChangeType, payload)
		if err != nil {
			return nil, fmt.Errorf("inserting change type: %w", err)
		}
		if err := m.db.InsertEdge(tx, changeSetID, graph.EdgeHas, ctID, nil); err != nil {
			return nil, fmt.Errorf("inserting HAS edge: %w", err)
		}
	}

	// Create AFFECTS edges to modules
	if matcher != nil {
		for _, modName := range affectedModules {
			payload := matcher.GetModulePayload(modName)
			if payload != nil {
				modID, err := m.db.InsertNode(tx, graph.KindModule, payload)
				if err != nil {
					return nil, fmt.Errorf("inserting module: %w", err)
				}
				if err := m.db.InsertEdge(tx, changeSetID, graph.EdgeAffects, modID, nil); err != nil {
					return nil, fmt.Errorf("inserting AFFECTS edge: %w", err)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	// Update workspace head and add changeset
	if err := m.UpdateHead(ws.ID, newSnapID); err != nil {
		return nil, fmt.Errorf("updating workspace head: %w", err)
	}
	if err := m.AddChangeSet(ws.ID, changeSetID); err != nil {
		return nil, fmt.Errorf("adding changeset to workspace: %w", err)
	}

	return &StageResult{
		ChangeSetID:  changeSetID,
		HeadSnapshot: newSnapID,
		ChangedFiles: len(changedPaths),
		ChangeTypes:  len(allChangeTypes),
	}, nil
}
