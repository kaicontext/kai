// Package review provides code review functionality for Kai changesets.
package review

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"kai/internal/graph"
	"kai/internal/util"
)

// State represents the state of a review.
type State string

const (
	StateDraft     State = "draft"
	StateOpen      State = "open"
	StateApproved  State = "approved"
	StateChanges   State = "changes_requested"
	StateMerged    State = "merged"
	StateAbandoned State = "abandoned"
)

// Review represents a code review for a changeset or workspace.
type Review struct {
	ID          []byte
	Title       string
	Description string
	State       State
	Author      string
	Reviewers   []string
	TargetID    []byte // ChangeSet or Workspace ID
	TargetKind  graph.NodeKind
	CreatedAt   int64
	UpdatedAt   int64
}

// Comment represents a review comment.
type Comment struct {
	ID        []byte
	ReviewID  []byte
	Author    string
	Body      string
	AnchorID  []byte // Symbol or File node ID (optional)
	FilePath  string // For file:line anchors
	Line      int    // For file:line anchors
	CreatedAt int64
}

// Manager handles review operations.
type Manager struct {
	db *graph.DB
}

// NewManager creates a new review manager.
func NewManager(db *graph.DB) *Manager {
	return &Manager{db: db}
}

// Open creates a new review for a changeset or workspace.
func (m *Manager) Open(targetID []byte, title, description, author string, reviewers []string) (*Review, error) {
	// Verify target exists and get its kind
	target, err := m.db.GetNode(targetID)
	if err != nil {
		return nil, fmt.Errorf("getting target: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("target not found")
	}

	// Validate target kind
	if target.Kind != graph.KindChangeSet && target.Kind != graph.KindWorkspace {
		return nil, fmt.Errorf("target must be a ChangeSet or Workspace, got %s", target.Kind)
	}

	// Generate UUID-like ID for review
	reviewID := make([]byte, 16)
	if _, err := rand.Read(reviewID); err != nil {
		return nil, fmt.Errorf("generating review ID: %w", err)
	}

	now := util.NowMs()
	payload := map[string]interface{}{
		"title":       title,
		"description": description,
		"state":       string(StateDraft),
		"author":      author,
		"reviewers":   reviewers,
		"targetId":    util.BytesToHex(targetID),
		"targetKind":  string(target.Kind),
		"createdAt":   now,
		"updatedAt":   now,
	}

	tx, err := m.db.BeginTx()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert review node
	if err := m.db.InsertReview(tx, reviewID, payload); err != nil {
		return nil, fmt.Errorf("inserting review: %w", err)
	}

	// Create REVIEW_OF edge
	if err := m.db.InsertEdge(tx, reviewID, graph.EdgeReviewOf, targetID, nil); err != nil {
		return nil, fmt.Errorf("inserting REVIEW_OF edge: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return &Review{
		ID:          reviewID,
		Title:       title,
		Description: description,
		State:       StateDraft,
		Author:      author,
		Reviewers:   reviewers,
		TargetID:    targetID,
		TargetKind:  target.Kind,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// Get retrieves a review by ID.
func (m *Manager) Get(reviewID []byte) (*Review, error) {
	node, err := m.db.GetNode(reviewID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, nil
	}
	if node.Kind != graph.KindReview {
		return nil, fmt.Errorf("not a review: %s", node.Kind)
	}

	return nodeToReview(node)
}

// GetByShortID retrieves a review by short hex prefix.
func (m *Manager) GetByShortID(prefix string) (*Review, error) {
	reviews, err := m.List()
	if err != nil {
		return nil, err
	}

	var matches []*Review
	for _, r := range reviews {
		idHex := hex.EncodeToString(r.ID)
		if len(prefix) <= len(idHex) && idHex[:len(prefix)] == prefix {
			matches = append(matches, r)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("review not found: %s", prefix)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous review prefix: %s (matches %d reviews)", prefix, len(matches))
	}

	return matches[0], nil
}

// List returns all reviews.
func (m *Manager) List() ([]*Review, error) {
	nodes, err := m.db.GetNodesByKind(graph.KindReview)
	if err != nil {
		return nil, err
	}

	reviews := make([]*Review, 0, len(nodes))
	for _, node := range nodes {
		r, err := nodeToReview(node)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}

	return reviews, nil
}

// UpdateState changes the state of a review.
func (m *Manager) UpdateState(reviewID []byte, newState State) error {
	node, err := m.db.GetNode(reviewID)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("review not found")
	}

	node.Payload["state"] = string(newState)
	node.Payload["updatedAt"] = util.NowMs()

	return m.db.UpdateNodePayload(reviewID, node.Payload)
}

// Close closes a review with a final state.
func (m *Manager) Close(reviewID []byte, state State) error {
	if state != StateMerged && state != StateAbandoned {
		return fmt.Errorf("close state must be 'merged' or 'abandoned'")
	}
	return m.UpdateState(reviewID, state)
}

// Approve marks a review as approved.
func (m *Manager) Approve(reviewID []byte) error {
	return m.UpdateState(reviewID, StateApproved)
}

// RequestChanges marks a review as needing changes.
func (m *Manager) RequestChanges(reviewID []byte) error {
	return m.UpdateState(reviewID, StateChanges)
}

// MarkReady transitions a review from draft to open.
func (m *Manager) MarkReady(reviewID []byte) error {
	review, err := m.Get(reviewID)
	if err != nil {
		return err
	}
	if review == nil {
		return fmt.Errorf("review not found")
	}
	if review.State != StateDraft {
		return fmt.Errorf("review is not in draft state")
	}
	return m.UpdateState(reviewID, StateOpen)
}

// GetTarget retrieves the target (ChangeSet or Workspace) of a review.
func (m *Manager) GetTarget(reviewID []byte) (*graph.Node, error) {
	edges, err := m.db.GetEdges(reviewID, graph.EdgeReviewOf)
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("review has no target")
	}

	return m.db.GetNode(edges[0].Dst)
}

// nodeToReview converts a graph node to a Review struct.
func nodeToReview(node *graph.Node) (*Review, error) {
	title, _ := node.Payload["title"].(string)
	description, _ := node.Payload["description"].(string)
	state, _ := node.Payload["state"].(string)
	author, _ := node.Payload["author"].(string)
	targetHex, _ := node.Payload["targetId"].(string)
	targetKind, _ := node.Payload["targetKind"].(string)
	createdAt, _ := node.Payload["createdAt"].(float64)
	updatedAt, _ := node.Payload["updatedAt"].(float64)

	targetID, _ := util.HexToBytes(targetHex)

	// Parse reviewers array
	var reviewers []string
	if arr, ok := node.Payload["reviewers"].([]interface{}); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				reviewers = append(reviewers, s)
			}
		}
	}

	return &Review{
		ID:          node.ID,
		Title:       title,
		Description: description,
		State:       State(state),
		Author:      author,
		Reviewers:   reviewers,
		TargetID:    targetID,
		TargetKind:  graph.NodeKind(targetKind),
		CreatedAt:   int64(createdAt),
		UpdatedAt:   int64(updatedAt),
	}, nil
}

// IDToHex converts review ID to hex string.
func IDToHex(id []byte) string {
	return hex.EncodeToString(id)
}
