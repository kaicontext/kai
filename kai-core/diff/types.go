// Package diff provides unified semantic diff computation and formatting.
package diff

// Action represents the type of change to a unit.
type Action string

const (
	ActionAdded    Action = "added"
	ActionModified Action = "modified"
	ActionRemoved  Action = "removed"
)

// UnitKind represents the type of semantic unit.
type UnitKind string

const (
	KindFunction  UnitKind = "function"
	KindClass     UnitKind = "class"
	KindMethod    UnitKind = "method"
	KindConst     UnitKind = "const"
	KindVariable  UnitKind = "variable"
	KindJSONKey   UnitKind = "json_key"
	KindYAMLKey   UnitKind = "yaml_key"
	KindSQLTable  UnitKind = "sql_table"
	KindSQLColumn UnitKind = "sql_column"
	KindImport    UnitKind = "import"
	KindExport    UnitKind = "export"
)

// Range represents a source location.
type Range struct {
	StartLine int `json:"startLine"`
	StartCol  int `json:"startCol"`
	EndLine   int `json:"endLine"`
	EndCol    int `json:"endCol"`
}

// UnitDiff represents a change to a semantic unit.
type UnitDiff struct {
	Kind       UnitKind `json:"kind"`
	Name       string   `json:"name"`
	Path       string   `json:"path,omitempty"` // for nested: "users.email", "config.database.host"
	Action     Action   `json:"action"`
	Before     string   `json:"before,omitempty"`
	After      string   `json:"after,omitempty"`
	BeforeSig  string   `json:"beforeSig,omitempty"`  // signature before (for functions)
	AfterSig   string   `json:"afterSig,omitempty"`   // signature after
	Range      *Range   `json:"range,omitempty"`
	ChangeType string   `json:"changeType,omitempty"` // e.g., "API_SURFACE_CHANGED"
}

// FileDiff represents changes to a single file.
type FileDiff struct {
	Path    string     `json:"path"`
	Action  Action     `json:"action"` // added, modified, removed
	Lang    string     `json:"lang,omitempty"`
	Units   []UnitDiff `json:"units,omitempty"`
	Binary  bool       `json:"binary,omitempty"`
	OldSize int        `json:"oldSize,omitempty"`
	NewSize int        `json:"newSize,omitempty"`
}

// DiffSummary provides aggregate statistics.
type DiffSummary struct {
	FilesAdded    int `json:"filesAdded"`
	FilesModified int `json:"filesModified"`
	FilesRemoved  int `json:"filesRemoved"`
	UnitsAdded    int `json:"unitsAdded"`
	UnitsModified int `json:"unitsModified"`
	UnitsRemoved  int `json:"unitsRemoved"`
}

// SemanticDiff represents a complete semantic diff between two versions.
type SemanticDiff struct {
	Base    string      `json:"base,omitempty"`    // base snapshot/commit ID
	Head    string      `json:"head,omitempty"`    // head snapshot/commit ID
	Files   []FileDiff  `json:"files"`
	Summary DiffSummary `json:"summary"`
}

// ComputeSummary calculates the summary from files.
func (sd *SemanticDiff) ComputeSummary() {
	sd.Summary = DiffSummary{}
	for _, f := range sd.Files {
		switch f.Action {
		case ActionAdded:
			sd.Summary.FilesAdded++
		case ActionModified:
			sd.Summary.FilesModified++
		case ActionRemoved:
			sd.Summary.FilesRemoved++
		}
		for _, u := range f.Units {
			switch u.Action {
			case ActionAdded:
				sd.Summary.UnitsAdded++
			case ActionModified:
				sd.Summary.UnitsModified++
			case ActionRemoved:
				sd.Summary.UnitsRemoved++
			}
		}
	}
}
