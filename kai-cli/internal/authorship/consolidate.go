package authorship

import (
	"fmt"
	"time"

	"kai/internal/graph"

	"kai-core/cas"
)

// Consolidate reads pending checkpoints and writes authorship ranges to the DB.
// Called as Step 4 of kai capture.
func Consolidate(db *graph.DB, snapshotID []byte, kaiDir string) error {
	checkpoints, err := ReadPendingCheckpoints(kaiDir)
	if err != nil {
		return fmt.Errorf("reading checkpoints: %w", err)
	}
	if len(checkpoints) == 0 {
		return nil // nothing to consolidate
	}

	tx, err := db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	now := cas.NowMs()

	// Group checkpoints by file and merge overlapping ranges
	byFile := make(map[string][]CheckpointRecord)
	for _, cp := range checkpoints {
		byFile[cp.File] = append(byFile[cp.File], cp)
	}

	for filePath, cps := range byFile {
		// Merge overlapping/adjacent ranges from the same agent
		merged := mergeRanges(cps)
		for _, r := range merged {
			ar := graph.AuthorshipRange{
				FilePath:   filePath,
				StartLine:  r.StartLine,
				EndLine:    r.EndLine,
				AuthorType: r.AuthorType,
				Agent:      r.Agent,
				Model:      r.Model,
				SessionID:  r.SessionID,
				CreatedAt:  now,
			}
			if err := db.InsertAuthorshipRange(tx, snapshotID, ar); err != nil {
				return fmt.Errorf("inserting authorship range: %w", err)
			}
		}
	}

	// Create an AuthorshipLog node linked to the snapshot
	totalRanges := 0
	for _, cps := range byFile {
		totalRanges += len(mergeRanges(cps))
	}

	agents := collectAgents(checkpoints)
	logPayload := map[string]interface{}{
		"snapshotId":    fmt.Sprintf("%x", snapshotID),
		"checkpoints":   len(checkpoints),
		"files":         len(byFile),
		"ranges":        totalRanges,
		"agents":        agents,
		"consolidatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	logID, err := db.InsertNode(tx, graph.KindAuthorshipLog, logPayload)
	if err != nil {
		return fmt.Errorf("inserting authorship log node: %w", err)
	}

	// Link: Snapshot -> AuthorshipLog
	if err := db.InsertEdge(tx, snapshotID, graph.EdgeAttributedIn, logID, nil); err != nil {
		return fmt.Errorf("inserting ATTRIBUTED_IN edge: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing authorship data: %w", err)
	}

	// Clean up processed checkpoints
	if err := ClearProcessedCheckpoints(kaiDir); err != nil {
		return fmt.Errorf("clearing checkpoints: %w", err)
	}

	return nil
}

// mergeRanges merges overlapping or adjacent checkpoint ranges for the same file.
// Later checkpoints (by timestamp) win when ranges overlap.
func mergeRanges(cps []CheckpointRecord) []CheckpointRecord {
	if len(cps) == 0 {
		return nil
	}
	if len(cps) == 1 {
		return cps
	}

	// Build a line-level map: line -> latest checkpoint info
	lineMap := make(map[int]*CheckpointRecord)
	for i := range cps {
		cp := &cps[i]
		for line := cp.StartLine; line <= cp.EndLine; line++ {
			existing, ok := lineMap[line]
			if !ok || cp.Timestamp >= existing.Timestamp {
				lineMap[line] = cp
			}
		}
	}

	// Collect all lines and sort them
	lines := make([]int, 0, len(lineMap))
	for line := range lineMap {
		lines = append(lines, line)
	}
	sortInts(lines)

	// Merge consecutive lines with the same attribution into ranges
	var merged []CheckpointRecord
	i := 0
	for i < len(lines) {
		start := lines[i]
		cp := lineMap[start]
		end := start

		// Extend range while consecutive lines have same attribution
		for i+1 < len(lines) && lines[i+1] == lines[i]+1 && sameAttribution(lineMap[lines[i+1]], cp) {
			i++
			end = lines[i]
		}

		merged = append(merged, CheckpointRecord{
			File:       cp.File,
			StartLine:  start,
			EndLine:    end,
			AuthorType: cp.AuthorType,
			Agent:      cp.Agent,
			Model:      cp.Model,
			SessionID:  cp.SessionID,
			Timestamp:  cp.Timestamp,
		})
		i++
	}

	return merged
}

func sameAttribution(a, b *CheckpointRecord) bool {
	return a.AuthorType == b.AuthorType && a.Agent == b.Agent && a.Model == b.Model
}

func collectAgents(cps []CheckpointRecord) []string {
	seen := make(map[string]bool)
	var agents []string
	for _, cp := range cps {
		if cp.Agent != "" && !seen[cp.Agent] {
			seen[cp.Agent] = true
			agents = append(agents, cp.Agent)
		}
	}
	return agents
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
