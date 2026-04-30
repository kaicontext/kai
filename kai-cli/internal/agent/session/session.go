// Package session persists kai-agent conversations to the same
// SQLite file kai uses for the semantic graph (`<kaiDir>/db.sqlite`).
// One DB, one backup story; sessions naturally join with kai's other
// per-repo state.
//
// Tables, kept hand-rolled (no sqlc) because there are only a handful
// of queries:
//
//	agent_sessions(id, task_name, workspace, model, started_at,
//	               ended_at, status, total_tokens_in/out)
//	agent_messages(id, session_id, ordinal, role, parts_json,
//	               finished, tokens_in, tokens_out, created_at)
//
// Slice 5 contract: persistence happens automatically when the runner
// is given a Store + (optionally) a session id. Auto-resume into the
// TUI's REPL is a follow-up — Slice 5 just ensures the rows exist.
package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"kai/internal/agent/message"
)

// Store is the minimal SQLite handle the session layer needs. The
// methods match what `*kai/internal/graph.DB` already exposes, so
// graph.DB satisfies it directly — see EnsureSchema's caller in
// `cmd/kai/tui.go`.
type Store interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// Status values for agent_sessions.status. "active" rows are live
// sessions a future TUI feature can offer to resume.
const (
	StatusActive  = "active"
	StatusEnded   = "ended"
	StatusErrored = "errored"
)

// Session is a handle to one conversation. New writes go through it;
// History reads return the full transcript in ordinal order.
type Session struct {
	ID        string
	TaskName  string
	Workspace string
	Model     string
	StartedAt time.Time
	Status    string
	store     Store
}

// EnsureSchema creates the agent_sessions / agent_messages tables and
// indexes if they don't exist. Idempotent; safe to call on every TUI
// startup. Mirrors the convention in `internal/graph/graph.go` where
// migrations live next to the code that uses them.
func EnsureSchema(db Store) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_sessions (
			id               TEXT PRIMARY KEY,
			task_name        TEXT NOT NULL,
			workspace        TEXT NOT NULL,
			model            TEXT NOT NULL DEFAULT '',
			started_at       INTEGER NOT NULL,
			ended_at         INTEGER,
			status           TEXT NOT NULL DEFAULT 'active',
			total_tokens_in  INTEGER NOT NULL DEFAULT 0,
			total_tokens_out INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS agent_sessions_active
			ON agent_sessions(status, started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id           TEXT PRIMARY KEY,
			session_id   TEXT NOT NULL,
			ordinal      INTEGER NOT NULL,
			role         TEXT NOT NULL,
			parts_json   TEXT NOT NULL,
			finished     TEXT NOT NULL DEFAULT '',
			tokens_in    INTEGER NOT NULL DEFAULT 0,
			tokens_out   INTEGER NOT NULL DEFAULT 0,
			created_at   INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS agent_messages_session
			ON agent_messages(session_id, ordinal)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("session: ensuring schema: %w", err)
		}
	}
	return nil
}

// New begins a session and inserts an "active" row. The returned
// Session is ready for AppendMessage / History calls.
func New(db Store, taskName, workspace, model string) (*Session, error) {
	if db == nil {
		return nil, errors.New("session.New: nil store")
	}
	s := &Session{
		ID:        uuid.NewString(),
		TaskName:  taskName,
		Workspace: workspace,
		Model:     model,
		StartedAt: time.Now().UTC(),
		Status:    StatusActive,
		store:     db,
	}
	_, err := db.Exec(
		`INSERT INTO agent_sessions
			(id, task_name, workspace, model, started_at, status)
			VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.TaskName, s.Workspace, s.Model, s.StartedAt.UnixMilli(), s.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("session.New: insert: %w", err)
	}
	return s, nil
}

// Resume loads an existing session by id. Returns ErrNotFound if the
// id has no row, so callers can fall back to New cleanly.
var ErrNotFound = errors.New("session: not found")

func Resume(db Store, id string) (*Session, error) {
	if db == nil || id == "" {
		return nil, errors.New("session.Resume: nil store or empty id")
	}
	row := db.QueryRow(
		`SELECT task_name, workspace, model, started_at, status
		 FROM agent_sessions WHERE id = ?`, id)
	s := &Session{ID: id, store: db}
	var startedAt int64
	if err := row.Scan(&s.TaskName, &s.Workspace, &s.Model, &startedAt, &s.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("session.Resume: %w", err)
	}
	s.StartedAt = time.UnixMilli(startedAt).UTC()
	return s, nil
}

// AppendMessage stores one message at the next ordinal. tokensIn /
// tokensOut accumulate on the session row so a quick "how much did
// this run cost" query doesn't have to sum agent_messages.
func (s *Session) AppendMessage(m message.Message, tokensIn, tokensOut int) error {
	if s == nil || s.store == nil {
		return errors.New("session.AppendMessage: nil session or store")
	}
	parts, err := encodeParts(m.Parts)
	if err != nil {
		return err
	}
	// Atomically claim the next ordinal. SQLite's COALESCE+MAX is
	// race-free under a single writer, which is the only model we
	// support today (kai's DB is single-process).
	res, err := s.store.Exec(
		`INSERT INTO agent_messages
			(id, session_id, ordinal, role, parts_json, finished,
			 tokens_in, tokens_out, created_at)
			SELECT ?, ?, COALESCE(MAX(ordinal), -1) + 1, ?, ?, ?, ?, ?, ?
			FROM agent_messages WHERE session_id = ?`,
		uuid.NewString(), s.ID, string(m.Role), parts, string(m.Finished),
		tokensIn, tokensOut, time.Now().UnixMilli(), s.ID,
	)
	if err != nil {
		return fmt.Errorf("session.AppendMessage: insert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("session.AppendMessage: no rows inserted")
	}

	if tokensIn > 0 || tokensOut > 0 {
		if _, err := s.store.Exec(
			`UPDATE agent_sessions
			 SET total_tokens_in = total_tokens_in + ?,
			     total_tokens_out = total_tokens_out + ?
			 WHERE id = ?`,
			tokensIn, tokensOut, s.ID,
		); err != nil {
			return fmt.Errorf("session.AppendMessage: token totals: %w", err)
		}
	}
	return nil
}

// History returns every message for the session in ordinal order.
// The runner calls this on Resume to seed the model with prior turns.
func (s *Session) History() ([]message.Message, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("session.History: nil session or store")
	}
	rows, err := s.store.Query(
		`SELECT role, parts_json, finished, tokens_in, tokens_out, created_at
		 FROM agent_messages WHERE session_id = ?
		 ORDER BY ordinal`, s.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session.History: %w", err)
	}
	defer rows.Close()
	var out []message.Message
	for rows.Next() {
		var role, partsJSON, finished string
		var tIn, tOut, createdAt int64
		if err := rows.Scan(&role, &partsJSON, &finished, &tIn, &tOut, &createdAt); err != nil {
			return nil, fmt.Errorf("session.History: scan: %w", err)
		}
		parts, err := decodeParts(partsJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, message.Message{
			Role:     message.Role(role),
			Parts:    parts,
			Finished: message.FinishReason(finished),
			Time:     time.UnixMilli(createdAt).UTC(),
		})
	}
	return out, rows.Err()
}

// End marks the session terminal. The TUI calls this when the agent
// loop exits cleanly (status="ended") or aborts (status="errored").
// Idempotent — calling twice doesn't break anything.
func (s *Session) End(status string) error {
	if s == nil || s.store == nil {
		return errors.New("session.End: nil session or store")
	}
	if status == "" {
		status = StatusEnded
	}
	_, err := s.store.Exec(
		`UPDATE agent_sessions
		 SET status = ?, ended_at = ?
		 WHERE id = ?`,
		status, time.Now().UnixMilli(), s.ID,
	)
	if err != nil {
		return fmt.Errorf("session.End: %w", err)
	}
	s.Status = status
	return nil
}

// --- ContentPart JSON encoding ---------------------------------------
//
// message.ContentPart is an interface; standard json.Marshal on a
// slice of interfaces produces inert empty objects unless we
// serialize each variant with a `type` discriminator. Done by hand
// here so the package stays free of reflection magic.

type partEnvelope struct {
	Type     string          `json:"type"`
	Raw      json.RawMessage `json:"data"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
}

const (
	partTypeText      = "text"
	partTypeReasoning = "reasoning"
	partTypeToolCall  = "tool_call"
	partTypeToolResult = "tool_result"
)

func encodeParts(parts []message.ContentPart) (string, error) {
	out := make([]partEnvelope, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case message.TextContent:
			b, _ := json.Marshal(v)
			out = append(out, partEnvelope{Type: partTypeText, Raw: b})
		case message.ReasoningContent:
			b, _ := json.Marshal(v)
			out = append(out, partEnvelope{Type: partTypeReasoning, Raw: b})
		case message.ToolCall:
			b, _ := json.Marshal(v)
			out = append(out, partEnvelope{Type: partTypeToolCall, Raw: b})
		case message.ToolResult:
			b, _ := json.Marshal(v)
			out = append(out, partEnvelope{Type: partTypeToolResult, Raw: b})
		default:
			// Forward-compat: unknown variants get serialized as
			// empty text so we don't drop ordering. Logging would
			// be ideal but session is a leaf package.
			b, _ := json.Marshal(message.TextContent{Text: ""})
			out = append(out, partEnvelope{Type: partTypeText, Raw: b})
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("session: encoding parts: %w", err)
	}
	return string(body), nil
}

func decodeParts(s string) ([]message.ContentPart, error) {
	var envs []partEnvelope
	if s == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(s), &envs); err != nil {
		return nil, fmt.Errorf("session: decoding parts: %w", err)
	}
	out := make([]message.ContentPart, 0, len(envs))
	for _, env := range envs {
		switch env.Type {
		case partTypeText:
			var v message.TextContent
			_ = json.Unmarshal(env.Raw, &v)
			out = append(out, v)
		case partTypeReasoning:
			var v message.ReasoningContent
			_ = json.Unmarshal(env.Raw, &v)
			out = append(out, v)
		case partTypeToolCall:
			var v message.ToolCall
			_ = json.Unmarshal(env.Raw, &v)
			out = append(out, v)
		case partTypeToolResult:
			var v message.ToolResult
			_ = json.Unmarshal(env.Raw, &v)
			out = append(out, v)
		}
	}
	return out, nil
}
