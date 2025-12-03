// Package proto provides shared type definitions and wire schemas for Kai.
package proto

// SnapshotPayload represents the payload structure for Snapshot nodes.
type SnapshotPayload struct {
	SourceType  string `json:"sourceType"`  // "git", "directory", etc.
	SourceRef   string `json:"sourceRef"`   // Git ref or directory path
	FileCount   int    `json:"fileCount"`   // Number of files in snapshot
	Description string `json:"description"` // Optional user-provided description
	CreatedAt   int64  `json:"createdAt"`   // Unix milliseconds
}

// FilePayload represents the payload structure for File nodes.
type FilePayload struct {
	Path   string `json:"path"`   // Relative file path
	Digest string `json:"digest"` // BLAKE3 hash of content
	Size   int64  `json:"size"`   // File size in bytes
	Lang   string `json:"lang"`   // Detected language (ts, js, go, etc.)
}

// SymbolPayload represents the payload structure for Symbol nodes.
type SymbolPayload struct {
	FQName    string      `json:"fqName"`    // Fully qualified name
	Kind      string      `json:"kind"`      // "function", "class", "variable"
	Signature string      `json:"signature"` // Function signature or declaration
	Range     SymbolRange `json:"range"`     // Source location
}

// SymbolRange represents a source code range.
type SymbolRange struct {
	Start [2]int `json:"start"` // [line, column]
	End   [2]int `json:"end"`   // [line, column]
}

// ChangeSetPayload represents the payload structure for ChangeSet nodes.
type ChangeSetPayload struct {
	Base        string `json:"base"`        // Base snapshot ID (hex)
	Head        string `json:"head"`        // Head snapshot ID (hex)
	Title       string `json:"title"`       // Short title
	Description string `json:"description"` // User-provided message
	Intent      string `json:"intent"`      // Generated intent sentence
	CreatedAt   int64  `json:"createdAt"`   // Unix milliseconds
}

// ChangeTypePayload represents the payload structure for ChangeType nodes.
type ChangeTypePayload struct {
	Category string           `json:"category"` // Change category (e.g., FUNCTION_ADDED)
	Evidence ChangeTypeEvidence `json:"evidence"` // Evidence for the detection
}

// ChangeTypeEvidence contains evidence for a change type detection.
type ChangeTypeEvidence struct {
	FileRanges []FileRangeInfo `json:"fileRanges"`
	Symbols    []string        `json:"symbols"` // Symbol IDs or names
}

// FileRangeInfo represents a range within a file.
type FileRangeInfo struct {
	Path  string `json:"path"`
	Start [2]int `json:"start"`
	End   [2]int `json:"end"`
}

// ModulePayload represents the payload structure for Module nodes.
type ModulePayload struct {
	Name     string   `json:"name"`     // Module name
	Patterns []string `json:"patterns"` // Glob patterns
}

// WorkspacePayload represents the payload structure for Workspace nodes.
type WorkspacePayload struct {
	Name        string `json:"name"`        // Workspace name
	Status      string `json:"status"`      // "open", "shelved", "closed"
	Description string `json:"description"` // Optional description
	CreatedAt   int64  `json:"createdAt"`   // Unix milliseconds
}

// RefPayload represents a named reference.
type RefPayload struct {
	Name       string `json:"name"`
	TargetID   string `json:"targetId"`   // Hex-encoded target node ID
	TargetKind string `json:"targetKind"` // Node kind
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}
