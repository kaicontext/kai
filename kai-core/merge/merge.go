package merge

import (
	"bytes"
	"fmt"
	"sort"
)

// Merger performs AST-aware 3-way merges.
type Merger struct {
	extractor *Extractor
}

// NewMerger creates a new merger.
func NewMerger() *Merger {
	return &Merger{
		extractor: NewExtractor(),
	}
}

// MergeFiles performs a 3-way merge on file contents.
func (m *Merger) MergeFiles(base, left, right map[string][]byte, lang string) (*MergeResult, error) {
	result := &MergeResult{
		Success: true,
		Files:   make(map[string][]byte),
	}

	// Collect all file paths
	allPaths := make(map[string]bool)
	for p := range base {
		allPaths[p] = true
	}
	for p := range left {
		allPaths[p] = true
	}
	for p := range right {
		allPaths[p] = true
	}

	// Process each file
	for path := range allPaths {
		baseContent := base[path]
		leftContent := left[path]
		rightContent := right[path]

		merged, conflicts, err := m.mergeFile(path, baseContent, leftContent, rightContent, lang)
		if err != nil {
			return nil, fmt.Errorf("merging %s: %w", path, err)
		}

		if len(conflicts) > 0 {
			result.Success = false
			result.Conflicts = append(result.Conflicts, conflicts...)
		}

		if merged != nil {
			result.Files[path] = merged
			result.Stats.FilesModified++
		}
	}

	return result, nil
}

// mergeFile performs 3-way merge on a single file.
func (m *Merger) mergeFile(path string, base, left, right []byte, lang string) ([]byte, []Conflict, error) {
	// Handle file-level changes first
	if base == nil {
		// File created
		return m.mergeCreated(path, left, right, lang)
	}
	if left == nil && right == nil {
		// Both deleted
		return nil, nil, nil
	}
	if left == nil {
		// Left deleted
		if bytes.Equal(base, right) {
			return nil, nil, nil // Right unchanged, accept deletion
		}
		// Right modified, left deleted = conflict
		return nil, []Conflict{{
			Kind:    ConflictDeleteVsModify,
			UnitKey: UnitKey{File: path},
			Message: "File deleted on left but modified on right",
		}}, nil
	}
	if right == nil {
		// Right deleted
		if bytes.Equal(base, left) {
			return nil, nil, nil // Left unchanged, accept deletion
		}
		// Left modified, right deleted = conflict
		return nil, []Conflict{{
			Kind:    ConflictModifyVsDelete,
			UnitKey: UnitKey{File: path},
			Message: "File modified on left but deleted on right",
		}}, nil
	}

	// Both modified - do semantic merge
	if bytes.Equal(left, right) {
		// Same content, no conflict
		return left, nil, nil
	}
	if bytes.Equal(base, left) {
		// Only right changed
		return right, nil, nil
	}
	if bytes.Equal(base, right) {
		// Only left changed
		return left, nil, nil
	}

	// Both diverged - extract units and merge
	return m.mergeUnits(path, base, left, right, lang)
}

// mergeCreated handles files created in left and/or right.
func (m *Merger) mergeCreated(path string, left, right []byte, lang string) ([]byte, []Conflict, error) {
	if left != nil && right == nil {
		return left, nil, nil
	}
	if right != nil && left == nil {
		return right, nil, nil
	}
	if left != nil && right != nil {
		if bytes.Equal(left, right) {
			return left, nil, nil
		}
		// Both created differently - conflict
		return nil, []Conflict{{
			Kind:    ConflictConcurrentCreate,
			UnitKey: UnitKey{File: path},
			Message: "File created on both sides with different content",
		}}, nil
	}
	return nil, nil, nil
}

// mergeUnits performs unit-level 3-way merge.
func (m *Merger) mergeUnits(path string, base, left, right []byte, lang string) ([]byte, []Conflict, error) {
	// Extract units from each version
	baseUnits, err := m.extractor.ExtractUnits(path, base, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("extracting base units: %w", err)
	}
	leftUnits, err := m.extractor.ExtractUnits(path, left, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("extracting left units: %w", err)
	}
	rightUnits, err := m.extractor.ExtractUnits(path, right, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("extracting right units: %w", err)
	}

	// Collect all unit keys
	allKeys := make(map[string]bool)
	for k := range baseUnits.Units {
		allKeys[k] = true
	}
	for k := range leftUnits.Units {
		allKeys[k] = true
	}
	for k := range rightUnits.Units {
		allKeys[k] = true
	}

	mergedUnits := make(map[string]*MergeUnit)
	var conflicts []Conflict

	// Process each unit
	for key := range allKeys {
		b := baseUnits.Units[key]
		l := leftUnits.Units[key]
		r := rightUnits.Units[key]

		merged, conflict := m.mergeUnit(b, l, r)
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		}
		if merged != nil {
			mergedUnits[key] = merged
		}
	}

	if len(conflicts) > 0 {
		return nil, conflicts, nil
	}

	// Reconstruct file from merged units
	result := m.reconstructFile(mergedUnits, leftUnits, rightUnits, lang)
	return result, nil, nil
}

// mergeUnit performs 3-way merge on a single unit.
func (m *Merger) mergeUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	// All same
	if EquivalentUnits(base, left) && EquivalentUnits(base, right) {
		return base, nil
	}

	// Unit created
	if base == nil {
		return m.mergeCreatedUnit(left, right)
	}

	// Left deleted
	if left == nil && right != nil {
		if EquivalentUnits(right, base) {
			return nil, nil // Delete accepted
		}
		// Right modified, left deleted
		return nil, &Conflict{
			Kind:    ConflictDeleteVsModify,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Unit %s deleted on left but modified on right", base.Name),
			Base:    base,
			Right:   right,
		}
	}

	// Right deleted
	if right == nil && left != nil {
		if EquivalentUnits(left, base) {
			return nil, nil // Delete accepted
		}
		// Left modified, right deleted
		return nil, &Conflict{
			Kind:    ConflictModifyVsDelete,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Unit %s modified on left but deleted on right", base.Name),
			Base:    base,
			Left:    left,
		}
	}

	// Both deleted
	if left == nil && right == nil {
		return nil, nil
	}

	// Only left changed
	if EquivalentUnits(base, right) && Changed(left, base) {
		return left, nil
	}

	// Only right changed
	if EquivalentUnits(base, left) && Changed(right, base) {
		return right, nil
	}

	// Both changed - need to merge based on unit kind
	return m.mergeChangedUnit(base, left, right)
}

// mergeCreatedUnit handles units created in left and/or right.
func (m *Merger) mergeCreatedUnit(left, right *MergeUnit) (*MergeUnit, *Conflict) {
	if left != nil && right == nil {
		return left, nil
	}
	if right != nil && left == nil {
		return right, nil
	}
	if left != nil && right != nil {
		if EquivalentUnits(left, right) {
			return left, nil
		}
		// Both created differently
		return nil, &Conflict{
			Kind:    ConflictConcurrentCreate,
			UnitKey: left.Key,
			Message: fmt.Sprintf("Unit %s created on both sides with different content", left.Name),
			Left:    left,
			Right:   right,
		}
	}
	return nil, nil
}

// mergeChangedUnit handles units that changed on both sides.
func (m *Merger) mergeChangedUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	// Check if changes are in different parts
	switch base.Kind {
	case UnitFunction, UnitMethod:
		return m.mergeFunctionUnit(base, left, right)
	case UnitClass:
		return m.mergeClassUnit(base, left, right)
	case UnitConst, UnitVariable:
		return m.mergeConstUnit(base, left, right)
	case UnitImport:
		return m.mergeImportUnit(base, left, right)
	default:
		// For unknown kinds, if bodies differ -> conflict
		if !EquivalentUnits(left, right) {
			return nil, &Conflict{
				Kind:    ConflictBodyDiverged,
				UnitKey: base.Key,
				Message: fmt.Sprintf("Unit %s modified on both sides", base.Name),
				Base:    base,
				Left:    left,
				Right:   right,
			}
		}
		return left, nil
	}
}

// mergeFunctionUnit merges function/method units.
func (m *Merger) mergeFunctionUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	// Check signature changes
	sigLeftChanged := left.Signature != base.Signature
	sigRightChanged := right.Signature != base.Signature

	if sigLeftChanged && sigRightChanged && left.Signature != right.Signature {
		return nil, &Conflict{
			Kind:    ConflictAPISignatureDiverged,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Function %s signature changed on both sides", base.Name),
			Base:    base,
			Left:    left,
			Right:   right,
			LeftDiff:  fmt.Sprintf("%s -> %s", base.Signature, left.Signature),
			RightDiff: fmt.Sprintf("%s -> %s", base.Signature, right.Signature),
		}
	}

	// If only one signature changed, or both changed the same way
	if sigLeftChanged && !sigRightChanged {
		// Left changed signature - if body same as right or base, use left
		return left, nil
	}
	if sigRightChanged && !sigLeftChanged {
		return right, nil
	}

	// Signatures are same, check body
	if !bytes.Equal(left.BodyHash, right.BodyHash) {
		return nil, &Conflict{
			Kind:    ConflictBodyDiverged,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Function %s body modified on both sides", base.Name),
			Base:    base,
			Left:    left,
			Right:   right,
		}
	}

	return left, nil
}

// mergeClassUnit merges class units.
func (m *Merger) mergeClassUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	// For classes, we rely on child method merging
	// If the overall class body differs, it's a conflict
	if !bytes.Equal(left.BodyHash, right.BodyHash) {
		return nil, &Conflict{
			Kind:    ConflictBodyDiverged,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Class %s modified on both sides", base.Name),
			Base:    base,
			Left:    left,
			Right:   right,
		}
	}
	return left, nil
}

// mergeConstUnit merges constant/variable units.
func (m *Merger) mergeConstUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	if !bytes.Equal(left.BodyHash, right.BodyHash) {
		return nil, &Conflict{
			Kind:    ConflictConstValueConflict,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Constant %s value changed on both sides", base.Name),
			Base:    base,
			Left:    left,
			Right:   right,
		}
	}
	return left, nil
}

// mergeImportUnit merges import units (set-like merge).
func (m *Merger) mergeImportUnit(base, left, right *MergeUnit) (*MergeUnit, *Conflict) {
	// Imports are typically set-like - if they differ, prefer union or conflict
	if !bytes.Equal(left.BodyHash, right.BodyHash) {
		return nil, &Conflict{
			Kind:    ConflictImportAlias,
			UnitKey: base.Key,
			Message: fmt.Sprintf("Import %s changed on both sides", base.Name),
			Base:    base,
			Left:    left,
			Right:   right,
		}
	}
	return left, nil
}

// reconstructFile rebuilds the file from merged units.
func (m *Merger) reconstructFile(merged map[string]*MergeUnit, left, right *FileUnits, lang string) []byte {
	// Collect all units and sort by original position
	var units []*MergeUnit
	for _, u := range merged {
		if u != nil {
			units = append(units, u)
		}
	}

	// Sort by original position (use left as reference, fall back to right)
	sort.Slice(units, func(i, j int) bool {
		ui, uj := units[i], units[j]
		return ui.Range.Start[0] < uj.Range.Start[0] ||
			(ui.Range.Start[0] == uj.Range.Start[0] && ui.Range.Start[1] < uj.Range.Start[1])
	})

	// Rebuild content
	var result bytes.Buffer
	for i, u := range units {
		if i > 0 {
			result.WriteString("\n\n")
		}
		result.Write(u.Content)
	}
	result.WriteString("\n")

	return result.Bytes()
}

// Merge3Way is a convenience function for 3-way merge of single files.
func Merge3Way(base, left, right []byte, lang string) (*MergeResult, error) {
	m := NewMerger()
	return m.MergeFiles(
		map[string][]byte{"file": base},
		map[string][]byte{"file": left},
		map[string][]byte{"file": right},
		lang,
	)
}
