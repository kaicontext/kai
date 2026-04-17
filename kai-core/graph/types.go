// Package graph provides core graph types for the Kai version control system.
package graph

// NodeKind represents the type of a node.
type NodeKind string

const (
	KindFile          NodeKind = "File"
	KindModule        NodeKind = "Module"
	KindSymbol        NodeKind = "Symbol"
	KindSnapshot      NodeKind = "Snapshot"
	KindChangeSet     NodeKind = "ChangeSet"
	KindChangeType     NodeKind = "ChangeType"
	KindClassification NodeKind = KindChangeType // Alias: user-facing name for semantic change classifications
	KindWorkspace     NodeKind = "Workspace"
	KindReview        NodeKind = "Review"
	KindReviewComment NodeKind = "ReviewComment"
	KindIntent        NodeKind = "Intent"
	KindAuthorshipLog NodeKind = "AuthorshipLog"
	KindSyncLog       NodeKind = "SyncLog"
)

// EdgeType represents the type of relationship between nodes.
type EdgeType string

const (
	EdgeContains     EdgeType = "CONTAINS"
	EdgeDefinesIn    EdgeType = "DEFINES_IN"
	EdgeHasFile      EdgeType = "HAS_FILE"
	EdgeModifies     EdgeType = "MODIFIES"
	EdgeHas          EdgeType = "HAS"
	EdgeAffects      EdgeType = "AFFECTS"
	EdgeBasedOn      EdgeType = "BASED_ON"      // Workspace -> base Snapshot
	EdgeHeadAt       EdgeType = "HEAD_AT"       // Workspace -> head Snapshot
	EdgeHasChangeSet EdgeType = "HAS_CHANGESET" // Workspace -> ChangeSet (ordered)
	EdgeReviewOf     EdgeType = "REVIEW_OF"     // Review -> ChangeSet or Workspace
	EdgeHasComment   EdgeType = "HAS_COMMENT"   // Review -> ReviewComment
	EdgeAnchorsTo    EdgeType = "ANCHORS_TO"    // ReviewComment -> Symbol/File
	EdgeSupersedes   EdgeType = "SUPERSEDES"    // ChangeSet -> ChangeSet (iteration)
	EdgeHasIntent    EdgeType = "HAS_INTENT"    // ChangeSet -> Intent
	EdgeCalls        EdgeType = "CALLS"         // Symbol -> Symbol (function call)
	EdgeImports      EdgeType = "IMPORTS"       // File -> File (import dependency)
	EdgeTests        EdgeType = "TESTS"         // File -> File (test file tests source file)
	EdgeAttributedIn EdgeType = "ATTRIBUTED_IN" // Snapshot -> AuthorshipLog
	EdgeHasSyncLog   EdgeType = "HAS_SYNC_LOG"  // Snapshot -> SyncLog
	EdgeHasCIRun     EdgeType = "HAS_CI_RUN"    // Snapshot -> CIRun
)

// ValidAssertions is the set of allowed values for the assert field on
// checkpoint_now milestones. Used by the MCP handler for validation and
// by the UI for badge rendering.
var ValidAssertions = []string{"tests-pass", "types-ok", "lints-clean", "manual-verified"}

// Node represents a node in the graph.
type Node struct {
	ID        []byte
	Kind      NodeKind
	Payload   map[string]interface{}
	CreatedAt int64
}

// Edge represents an edge in the graph.
type Edge struct {
	Src       []byte
	Type      EdgeType
	Dst       []byte
	At        []byte // context (snapshot or changeset ID), can be nil
	CreatedAt int64
}
