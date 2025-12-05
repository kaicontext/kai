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
	KindChangeType    NodeKind = "ChangeType"
	KindWorkspace     NodeKind = "Workspace"
	KindReview        NodeKind = "Review"
	KindReviewComment NodeKind = "ReviewComment"
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
)

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
