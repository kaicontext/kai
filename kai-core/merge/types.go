// Package merge provides AST-aware 3-way merge for semantic version control.
package merge

import (
	"kai-core/parse"
)

// UnitKind represents the type of merge unit.
type UnitKind string

const (
	UnitFunction UnitKind = "function"
	UnitClass    UnitKind = "class"
	UnitMethod   UnitKind = "method"
	UnitConst    UnitKind = "const"
	UnitVariable UnitKind = "variable"
	UnitImport   UnitKind = "import"
	UnitExport   UnitKind = "export"
	UnitType     UnitKind = "type"
)

// UnitKey uniquely identifies a merge unit within a file.
type UnitKey struct {
	File       string   // file path
	SymbolPath []string // e.g., ["MyClass", "myMethod"] for nested symbols
	Kind       UnitKind
}

// String returns a human-readable key representation.
func (k UnitKey) String() string {
	path := k.File + "::"
	for i, p := range k.SymbolPath {
		if i > 0 {
			path += "."
		}
		path += p
	}
	return path
}

// MergeUnit represents a semantic unit that can be merged.
type MergeUnit struct {
	Key       UnitKey
	Kind      UnitKind
	Name      string
	Signature string       // for functions: params + return type
	BodyHash  []byte       // content hash for quick comparison
	Range     parse.Range  // source location
	Modifiers []string     // public, private, async, etc.
	Children  []*MergeUnit // for classes: methods; for blocks: statements
	RawNode   interface{}  // underlying AST node (language-specific)
	Content   []byte       // source content for this unit
}

// ConflictKind classifies the type of merge conflict.
type ConflictKind string

const (
	// API conflicts
	ConflictAPIParamRenameDiverged ConflictKind = "API_PARAM_RENAME_DIVERGED"
	ConflictAPIParamAddedBoth      ConflictKind = "API_PARAM_ADDED_BOTH"
	ConflictAPIReturnChangedBoth   ConflictKind = "API_RETURN_CHANGED_BOTH"
	ConflictAPISignatureDiverged   ConflictKind = "API_SIGNATURE_DIVERGED"

	// Logic conflicts
	ConflictCondBoundaryConflict ConflictKind = "COND_BOUNDARY_CONFLICT"
	ConflictConstValueConflict   ConflictKind = "CONST_VALUE_CONFLICT"

	// Structural conflicts
	ConflictDeleteVsModify   ConflictKind = "DELETE_vs_MODIFY"
	ConflictModifyVsDelete   ConflictKind = "MODIFY_vs_DELETE"
	ConflictConcurrentCreate ConflictKind = "CONCURRENT_CREATE"
	ConflictReorderVsEdit    ConflictKind = "REORDER_vs_EDIT"
	ConflictImportAlias      ConflictKind = "IMPORT_ALIAS_CONFLICT"

	// Body conflicts
	ConflictBodyDiverged ConflictKind = "BODY_DIVERGED"
)

// Conflict represents a semantic merge conflict.
type Conflict struct {
	Kind      ConflictKind
	UnitKey   UnitKey
	Message   string
	Base      *MergeUnit // nil if created in both
	Left      *MergeUnit // nil if deleted
	Right     *MergeUnit // nil if deleted
	LeftDiff  string     // human-readable change description
	RightDiff string
	// Suggested resolutions
	Resolutions []Resolution
}

// Resolution represents a possible way to resolve a conflict.
type Resolution struct {
	Label       string // e.g., "Keep left", "Keep right", "Union"
	Description string
	AutoApply   bool   // can be applied automatically
	Result      []byte // resulting content if applied
}

// MergeResult contains the outcome of a 3-way merge.
type MergeResult struct {
	Success   bool
	Files     map[string][]byte // merged file contents
	Conflicts []Conflict
	Stats     MergeStats
}

// MergeStats contains statistics about the merge.
type MergeStats struct {
	UnitsTotal      int
	UnitsAutoMerged int
	UnitsConflicted int
	FilesModified   int
}

// FileUnits contains indexed merge units for a single file.
type FileUnits struct {
	Path    string
	Lang    string
	Units   map[string]*MergeUnit // key -> unit
	Content []byte
}

// MergeInput represents the three sides of a merge.
type MergeInput struct {
	Base  map[string]*FileUnits // file path -> units
	Left  map[string]*FileUnits
	Right map[string]*FileUnits
}
