// Package store provides SQLite-backed storage for Kailab.
package store

import (
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kai-core/cas"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed pragmas.sql
var pragmasSQL string

var (
	ErrRefNotFound      = errors.New("ref not found")
	ErrRefMismatch      = errors.New("ref target mismatch (not fast-forward)")
	ErrObjectNotFound   = errors.New("object not found")
	ErrSegmentNotFound  = errors.New("segment not found")
	ErrAmbiguousPrefix  = errors.New("ambiguous object prefix")
)

// DB wraps a SQLite connection for Kailab storage.
type DB struct {
	conn *sql.DB
	mu   sync.RWMutex
	path string
}

// OpenRepoDB opens or creates a per-repo database.
// root is the base data directory, tenant and repo identify the repository.
func OpenRepoDB(root, tenant, repo string) (*DB, error) {
	dir := filepath.Join(root, tenant, repo)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	dbPath := filepath.Join(dir, "kailab.db")
	return Open(dbPath)
}

// Open opens a database at the given path.
func Open(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	db := &DB{conn: conn, path: dbPath}

	// Apply pragmas
	for _, pragma := range strings.Split(pragmasSQL, "\n") {
		pragma = strings.TrimSpace(pragma)
		if pragma == "" || strings.HasPrefix(pragma, "--") {
			continue
		}
		if _, err := conn.Exec(pragma); err != nil {
			conn.Close()
			return nil, fmt.Errorf("applying pragma %q: %w", pragma, err)
		}
	}

	// Apply schema
	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// BeginTx starts a new transaction.
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.conn.Begin()
}

// ----- Segments -----

// InsertSegment stores a new segment blob.
func (db *DB) InsertSegment(tx *sql.Tx, checksum []byte, blob []byte) (int64, error) {
	ts := cas.NowMs()
	result, err := tx.Exec(
		`INSERT INTO segments (ts, checksum, size, blob) VALUES (?, ?, ?, ?)`,
		ts, checksum, len(blob), blob,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting segment: %w", err)
	}
	return result.LastInsertId()
}

// GetSegmentBlob retrieves a segment's blob by ID.
func (db *DB) GetSegmentBlob(segmentID int64) ([]byte, error) {
	var blob []byte
	err := db.conn.QueryRow(
		`SELECT blob FROM segments WHERE id = ?`, segmentID,
	).Scan(&blob)
	if err == sql.ErrNoRows {
		return nil, ErrSegmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying segment: %w", err)
	}
	return blob, nil
}

// ----- Objects -----

// ObjectInfo represents metadata about a stored object.
type ObjectInfo struct {
	Digest    []byte
	SegmentID int64
	Off       int64
	Len       int64
	Kind      string
	CreatedAt int64
}

// InsertObject records an object's location within a segment.
// Uses INSERT OR IGNORE for idempotence.
func (db *DB) InsertObject(tx *sql.Tx, digest []byte, segmentID, off, length int64, kind string) error {
	ts := cas.NowMs()
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO objects (digest, segment_id, off, len, kind, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		digest, segmentID, off, length, kind, ts,
	)
	if err != nil {
		return fmt.Errorf("inserting object: %w", err)
	}
	return nil
}

// GetObject retrieves object metadata by digest.
func (db *DB) GetObject(digest []byte) (*ObjectInfo, error) {
	var info ObjectInfo
	err := db.conn.QueryRow(
		`SELECT digest, segment_id, off, len, kind, created_at FROM objects WHERE digest = ?`,
		digest,
	).Scan(&info.Digest, &info.SegmentID, &info.Off, &info.Len, &info.Kind, &info.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying object: %w", err)
	}
	return &info, nil
}

// HasObject checks if an object exists by digest.
func (db *DB) HasObject(digest []byte) (bool, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM objects WHERE digest = ?`, digest,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking object: %w", err)
	}
	return count > 0, nil
}

// HasObjects checks which digests exist. Returns a set of existing digests (hex-encoded).
func (db *DB) HasObjects(digests [][]byte) (map[string]bool, error) {
	if len(digests) == 0 {
		return make(map[string]bool), nil
	}

	result := make(map[string]bool)

	// Process in batches to avoid query size limits
	batchSize := 500
	for i := 0; i < len(digests); i += batchSize {
		end := i + batchSize
		if end > len(digests) {
			end = len(digests)
		}
		batch := digests[i:end]

		placeholders := make([]string, len(batch))
		args := make([]interface{}, len(batch))
		for j, d := range batch {
			placeholders[j] = "?"
			args[j] = d
		}

		query := fmt.Sprintf(
			`SELECT digest FROM objects WHERE digest IN (%s)`,
			strings.Join(placeholders, ","),
		)

		rows, err := db.conn.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("querying objects: %w", err)
		}

		for rows.Next() {
			var digest []byte
			if err := rows.Scan(&digest); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning object: %w", err)
			}
			result[hex.EncodeToString(digest)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating objects: %w", err)
		}
	}

	return result, nil
}

// ReadObjectContent reads the actual content of an object by fetching from its segment.
func (db *DB) ReadObjectContent(digest []byte) ([]byte, error) {
	info, err := db.GetObject(digest)
	if err != nil {
		return nil, err
	}

	blob, err := db.GetSegmentBlob(info.SegmentID)
	if err != nil {
		return nil, err
	}

	if info.Off+info.Len > int64(len(blob)) {
		return nil, fmt.Errorf("object extends beyond segment bounds")
	}

	return blob[info.Off : info.Off+info.Len], nil
}

// ----- Refs -----

// Ref represents a named reference.
type Ref struct {
	Name      string
	Target    []byte
	UpdatedAt int64
	Actor     string
	PushID    string
}

// GetRef retrieves a ref by name.
func (db *DB) GetRef(name string) (*Ref, error) {
	var ref Ref
	err := db.conn.QueryRow(
		`SELECT name, target, updated_at, actor, push_id FROM refs WHERE name = ?`,
		name,
	).Scan(&ref.Name, &ref.Target, &ref.UpdatedAt, &ref.Actor, &ref.PushID)
	if err == sql.ErrNoRows {
		return nil, ErrRefNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying ref: %w", err)
	}
	return &ref, nil
}

// ListRefs returns all refs, optionally filtered by prefix.
func (db *DB) ListRefs(prefix string) ([]*Ref, error) {
	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = db.conn.Query(
			`SELECT name, target, updated_at, actor, push_id FROM refs ORDER BY name`,
		)
	} else {
		rows, err = db.conn.Query(
			`SELECT name, target, updated_at, actor, push_id FROM refs WHERE name LIKE ? ORDER BY name`,
			prefix+"%",
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying refs: %w", err)
	}
	defer rows.Close()

	var refs []*Ref
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.Name, &ref.Target, &ref.UpdatedAt, &ref.Actor, &ref.PushID); err != nil {
			return nil, fmt.Errorf("scanning ref: %w", err)
		}
		refs = append(refs, &ref)
	}
	return refs, rows.Err()
}

// SetRefFF updates a ref with fast-forward check.
// If old is nil, the ref must not exist.
// If old is non-nil, the current target must match old.
func (db *DB) SetRefFF(tx *sql.Tx, name string, old, new []byte, actor, pushID string) error {
	ts := cas.NowMs()

	// Get current value
	var currentTarget []byte
	err := tx.QueryRow(`SELECT target FROM refs WHERE name = ?`, name).Scan(&currentTarget)
	if err == sql.ErrNoRows {
		currentTarget = nil
	} else if err != nil {
		return fmt.Errorf("checking current ref: %w", err)
	}

	// Verify fast-forward
	if old == nil && currentTarget != nil {
		return ErrRefMismatch
	}
	if old != nil {
		if currentTarget == nil {
			return ErrRefMismatch
		}
		if !bytesEqual(old, currentTarget) {
			return ErrRefMismatch
		}
	}

	// Get the last ref_history entry for this ref to chain
	var parentID []byte
	err = tx.QueryRow(
		`SELECT id FROM ref_history WHERE ref = ? ORDER BY seq DESC LIMIT 1`,
		name,
	).Scan(&parentID)
	if err == sql.ErrNoRows {
		parentID = nil
	} else if err != nil {
		return fmt.Errorf("getting parent history: %w", err)
	}

	// Upsert ref
	_, err = tx.Exec(
		`INSERT INTO refs (name, target, updated_at, actor, push_id)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET target=excluded.target, updated_at=excluded.updated_at, actor=excluded.actor, push_id=excluded.push_id`,
		name, new, ts, actor, pushID,
	)
	if err != nil {
		return fmt.Errorf("upserting ref: %w", err)
	}

	// Append to ref_history
	historyEntry := map[string]interface{}{
		"time":   ts,
		"actor":  actor,
		"ref":    name,
		"old":    hex.EncodeToString(old),
		"new":    hex.EncodeToString(new),
		"pushId": pushID,
	}
	if parentID != nil {
		historyEntry["parent"] = hex.EncodeToString(parentID)
	}

	entryJSON, err := json.Marshal(historyEntry)
	if err != nil {
		return fmt.Errorf("marshaling history entry: %w", err)
	}
	entryID := cas.Blake3Hash(entryJSON)

	_, err = tx.Exec(
		`INSERT INTO ref_history (id, parent, time, actor, ref, old, new, meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entryID, parentID, ts, actor, name, old, new, string(entryJSON),
	)
	if err != nil {
		return fmt.Errorf("inserting ref history: %w", err)
	}

	return nil
}

// ForceSetRef updates a ref without fast-forward check.
func (db *DB) ForceSetRef(tx *sql.Tx, name string, new []byte, actor, pushID string) error {
	ts := cas.NowMs()

	// Get current target for history
	var currentTarget []byte
	err := tx.QueryRow(`SELECT target FROM refs WHERE name = ?`, name).Scan(&currentTarget)
	if err == sql.ErrNoRows {
		currentTarget = nil
	} else if err != nil {
		return fmt.Errorf("checking current ref: %w", err)
	}

	// Get the last ref_history entry to chain
	var parentID []byte
	err = tx.QueryRow(
		`SELECT id FROM ref_history WHERE ref = ? ORDER BY seq DESC LIMIT 1`,
		name,
	).Scan(&parentID)
	if err == sql.ErrNoRows {
		parentID = nil
	} else if err != nil {
		return fmt.Errorf("getting parent history: %w", err)
	}

	// Upsert ref
	_, err = tx.Exec(
		`INSERT INTO refs (name, target, updated_at, actor, push_id)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET target=excluded.target, updated_at=excluded.updated_at, actor=excluded.actor, push_id=excluded.push_id`,
		name, new, ts, actor, pushID,
	)
	if err != nil {
		return fmt.Errorf("upserting ref: %w", err)
	}

	// Append to ref_history (marked as force)
	historyEntry := map[string]interface{}{
		"time":   ts,
		"actor":  actor,
		"ref":    name,
		"old":    hex.EncodeToString(currentTarget),
		"new":    hex.EncodeToString(new),
		"pushId": pushID,
		"force":  true,
	}
	if parentID != nil {
		historyEntry["parent"] = hex.EncodeToString(parentID)
	}

	entryJSON, err := json.Marshal(historyEntry)
	if err != nil {
		return fmt.Errorf("marshaling history entry: %w", err)
	}
	entryID := cas.Blake3Hash(entryJSON)

	_, err = tx.Exec(
		`INSERT INTO ref_history (id, parent, time, actor, ref, old, new, meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entryID, parentID, ts, actor, name, currentTarget, new, string(entryJSON),
	)
	if err != nil {
		return fmt.Errorf("inserting ref history: %w", err)
	}

	return nil
}

// ----- Ref History -----

// RefHistoryEntry represents a single ref update event.
type RefHistoryEntry struct {
	Seq    int64
	ID     []byte
	Parent []byte
	Time   int64
	Actor  string
	Ref    string
	Old    []byte
	New    []byte
	Meta   string
}

// GetRefHistory retrieves ref history entries.
func (db *DB) GetRefHistory(refFilter string, afterSeq int64, limit int) ([]*RefHistoryEntry, error) {
	var rows *sql.Rows
	var err error

	if limit <= 0 {
		limit = 100
	}

	if refFilter == "" {
		rows, err = db.conn.Query(
			`SELECT seq, id, parent, time, actor, ref, old, new, meta
			 FROM ref_history WHERE seq > ? ORDER BY seq ASC LIMIT ?`,
			afterSeq, limit,
		)
	} else {
		rows, err = db.conn.Query(
			`SELECT seq, id, parent, time, actor, ref, old, new, meta
			 FROM ref_history WHERE ref = ? AND seq > ? ORDER BY seq ASC LIMIT ?`,
			refFilter, afterSeq, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying ref history: %w", err)
	}
	defer rows.Close()

	var entries []*RefHistoryEntry
	for rows.Next() {
		var e RefHistoryEntry
		if err := rows.Scan(&e.Seq, &e.ID, &e.Parent, &e.Time, &e.Actor, &e.Ref, &e.Old, &e.New, &e.Meta); err != nil {
			return nil, fmt.Errorf("scanning ref history: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// GetLogHead returns the ID of the most recent ref_history entry.
func (db *DB) GetLogHead() ([]byte, error) {
	var id []byte
	err := db.conn.QueryRow(
		`SELECT id FROM ref_history ORDER BY seq DESC LIMIT 1`,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying log head: %w", err)
	}
	return id, nil
}

// ----- Node Publish -----

// NodePublishEntry records a node announcement.
type NodePublishEntry struct {
	Seq    int64
	ID     []byte
	Parent []byte
	Time   int64
	Actor  string
	NodeID []byte
	Kind   string
}

// RecordNodePublish records a new node announcement.
func (db *DB) RecordNodePublish(tx *sql.Tx, nodeID []byte, kind, actor string) error {
	ts := cas.NowMs()

	// Get last entry for chaining
	var parentID []byte
	err := tx.QueryRow(
		`SELECT id FROM node_publish ORDER BY seq DESC LIMIT 1`,
	).Scan(&parentID)
	if err == sql.ErrNoRows {
		parentID = nil
	} else if err != nil {
		return fmt.Errorf("getting parent publish: %w", err)
	}

	entry := map[string]interface{}{
		"time":   ts,
		"actor":  actor,
		"nodeId": hex.EncodeToString(nodeID),
		"kind":   kind,
	}
	if parentID != nil {
		entry["parent"] = hex.EncodeToString(parentID)
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling publish entry: %w", err)
	}
	entryID := cas.Blake3Hash(entryJSON)

	_, err = tx.Exec(
		`INSERT OR IGNORE INTO node_publish (id, parent, time, actor, node_id, kind) VALUES (?, ?, ?, ?, ?, ?)`,
		entryID, parentID, ts, actor, nodeID, kind,
	)
	if err != nil {
		return fmt.Errorf("inserting node publish: %w", err)
	}

	return nil
}

// ----- Enrich Queue -----

// EnrichQueueItem represents an item waiting for enrichment.
type EnrichQueueItem struct {
	ID         int64
	NodeID     []byte
	Kind       string
	Status     string
	CreatedAt  int64
	StartedAt  *int64
	FinishedAt *int64
	Error      *string
}

// EnqueueForEnrichment adds a node to the enrichment queue.
func (db *DB) EnqueueForEnrichment(tx *sql.Tx, nodeID []byte, kind string) error {
	ts := cas.NowMs()
	_, err := tx.Exec(
		`INSERT INTO enrich_queue (node_id, kind, status, created_at) VALUES (?, ?, 'pending', ?)`,
		nodeID, kind, ts,
	)
	if err != nil {
		return fmt.Errorf("enqueueing for enrichment: %w", err)
	}
	return nil
}

// ClaimEnrichmentItem atomically claims the next pending item.
func (db *DB) ClaimEnrichmentItem() (*EnrichQueueItem, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var item EnrichQueueItem
	err = tx.QueryRow(
		`SELECT id, node_id, kind, status, created_at FROM enrich_queue WHERE status = 'pending' ORDER BY id ASC LIMIT 1`,
	).Scan(&item.ID, &item.NodeID, &item.Kind, &item.Status, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying queue: %w", err)
	}

	ts := cas.NowMs()
	_, err = tx.Exec(
		`UPDATE enrich_queue SET status = 'processing', started_at = ? WHERE id = ?`,
		ts, item.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("updating queue item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing claim: %w", err)
	}

	item.Status = "processing"
	item.StartedAt = &ts
	return &item, nil
}

// CompleteEnrichmentItem marks an item as done or failed.
func (db *DB) CompleteEnrichmentItem(id int64, errMsg string) error {
	ts := cas.NowMs()
	status := "done"
	var errPtr *string
	if errMsg != "" {
		status = "failed"
		errPtr = &errMsg
	}

	_, err := db.conn.Exec(
		`UPDATE enrich_queue SET status = ?, finished_at = ?, error = ? WHERE id = ?`,
		status, ts, errPtr, id,
	)
	if err != nil {
		return fmt.Errorf("completing enrichment: %w", err)
	}
	return nil
}

// ----- Utilities -----

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NowMs returns current Unix milliseconds.
func NowMs() int64 {
	return time.Now().UnixMilli()
}

// ============================================================================
// Standalone functions for multi-repo support
// These functions take *sql.DB as a parameter instead of being methods on DB.
// ============================================================================

// BeginTx starts a new transaction on the given database.
func BeginTx(db *sql.DB) (*sql.Tx, error) {
	return db.Begin()
}

// HasObjects checks which digests exist (standalone function).
func HasObjects(db *sql.DB, digests [][]byte) (map[string]bool, error) {
	if len(digests) == 0 {
		return make(map[string]bool), nil
	}

	result := make(map[string]bool)

	// Process in batches to avoid query size limits
	batchSize := 500
	for i := 0; i < len(digests); i += batchSize {
		end := i + batchSize
		if end > len(digests) {
			end = len(digests)
		}
		batch := digests[i:end]

		placeholders := make([]string, len(batch))
		args := make([]interface{}, len(batch))
		for j, d := range batch {
			placeholders[j] = "?"
			args[j] = d
		}

		query := fmt.Sprintf(
			`SELECT digest FROM objects WHERE digest IN (%s)`,
			strings.Join(placeholders, ","),
		)

		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("querying objects: %w", err)
		}

		for rows.Next() {
			var digest []byte
			if err := rows.Scan(&digest); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning object: %w", err)
			}
			result[hex.EncodeToString(digest)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating objects: %w", err)
		}
	}

	return result, nil
}

// InsertSegmentTx stores a new segment blob (standalone function).
func InsertSegmentTx(tx *sql.Tx, checksum []byte, blob []byte) (int64, error) {
	ts := cas.NowMs()
	result, err := tx.Exec(
		`INSERT INTO segments (ts, checksum, size, blob) VALUES (?, ?, ?, ?)`,
		ts, checksum, len(blob), blob,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting segment: %w", err)
	}
	return result.LastInsertId()
}

// InsertObjectTx records an object's location within a segment (standalone function).
func InsertObjectTx(tx *sql.Tx, digest []byte, segmentID, off, length int64, kind string) error {
	ts := cas.NowMs()
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO objects (digest, segment_id, off, len, kind, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		digest, segmentID, off, length, kind, ts,
	)
	if err != nil {
		return fmt.Errorf("inserting object: %w", err)
	}
	return nil
}

// GetObjectInfo retrieves object metadata by digest (standalone function).
func GetObjectInfo(db *sql.DB, digest []byte) (*ObjectInfo, error) {
	var info ObjectInfo
	err := db.QueryRow(
		`SELECT digest, segment_id, off, len, kind, created_at FROM objects WHERE digest = ?`,
		digest,
	).Scan(&info.Digest, &info.SegmentID, &info.Off, &info.Len, &info.Kind, &info.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying object: %w", err)
	}
	return &info, nil
}

// GetSegmentBlobByID retrieves a segment's blob by ID (standalone function).
func GetSegmentBlobByID(db *sql.DB, segmentID int64) ([]byte, error) {
	var blob []byte
	err := db.QueryRow(
		`SELECT blob FROM segments WHERE id = ?`, segmentID,
	).Scan(&blob)
	if err == sql.ErrNoRows {
		return nil, ErrSegmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying segment: %w", err)
	}
	return blob, nil
}

// ReadObjectContentDB reads the actual content of an object (standalone function).
func ReadObjectContentDB(db *sql.DB, digest []byte) ([]byte, error) {
	info, err := GetObjectInfo(db, digest)
	if err != nil {
		return nil, err
	}

	blob, err := GetSegmentBlobByID(db, info.SegmentID)
	if err != nil {
		return nil, err
	}

	if info.Off+info.Len > int64(len(blob)) {
		return nil, fmt.Errorf("object extends beyond segment bounds")
	}

	return blob[info.Off : info.Off+info.Len], nil
}

// ListRefs returns all refs, optionally filtered by prefix (standalone function).
func ListRefs(db *sql.DB, prefix string) ([]*Ref, error) {
	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = db.Query(
			`SELECT name, target, updated_at, actor, push_id FROM refs ORDER BY name`,
		)
	} else {
		rows, err = db.Query(
			`SELECT name, target, updated_at, actor, push_id FROM refs WHERE name LIKE ? ORDER BY name`,
			prefix+"%",
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying refs: %w", err)
	}
	defer rows.Close()

	var refs []*Ref
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.Name, &ref.Target, &ref.UpdatedAt, &ref.Actor, &ref.PushID); err != nil {
			return nil, fmt.Errorf("scanning ref: %w", err)
		}
		refs = append(refs, &ref)
	}
	return refs, rows.Err()
}

// GetRef retrieves a ref by name (standalone function).
func GetRef(db *sql.DB, name string) (*Ref, error) {
	var ref Ref
	err := db.QueryRow(
		`SELECT name, target, updated_at, actor, push_id FROM refs WHERE name = ?`,
		name,
	).Scan(&ref.Name, &ref.Target, &ref.UpdatedAt, &ref.Actor, &ref.PushID)
	if err == sql.ErrNoRows {
		return nil, ErrRefNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying ref: %w", err)
	}
	return &ref, nil
}

// SetRefFF updates a ref with fast-forward check (standalone function).
func SetRefFF(db *sql.DB, tx *sql.Tx, name string, old, new []byte, actor, pushID string) error {
	ts := cas.NowMs()

	// Get current value
	var currentTarget []byte
	err := tx.QueryRow(`SELECT target FROM refs WHERE name = ?`, name).Scan(&currentTarget)
	if err == sql.ErrNoRows {
		currentTarget = nil
	} else if err != nil {
		return fmt.Errorf("checking current ref: %w", err)
	}

	// Verify fast-forward
	if old == nil && currentTarget != nil {
		return ErrRefMismatch
	}
	if old != nil {
		if currentTarget == nil {
			return ErrRefMismatch
		}
		if !bytesEqual(old, currentTarget) {
			return ErrRefMismatch
		}
	}

	// Get the last ref_history entry for this ref to chain
	var parentID []byte
	err = tx.QueryRow(
		`SELECT id FROM ref_history WHERE ref = ? ORDER BY seq DESC LIMIT 1`,
		name,
	).Scan(&parentID)
	if err == sql.ErrNoRows {
		parentID = nil
	} else if err != nil {
		return fmt.Errorf("getting parent history: %w", err)
	}

	// Upsert ref
	_, err = tx.Exec(
		`INSERT INTO refs (name, target, updated_at, actor, push_id)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET target=excluded.target, updated_at=excluded.updated_at, actor=excluded.actor, push_id=excluded.push_id`,
		name, new, ts, actor, pushID,
	)
	if err != nil {
		return fmt.Errorf("upserting ref: %w", err)
	}

	// Append to ref_history
	historyEntry := map[string]interface{}{
		"time":   ts,
		"actor":  actor,
		"ref":    name,
		"old":    hex.EncodeToString(old),
		"new":    hex.EncodeToString(new),
		"pushId": pushID,
	}
	if parentID != nil {
		historyEntry["parent"] = hex.EncodeToString(parentID)
	}

	entryJSON, err := json.Marshal(historyEntry)
	if err != nil {
		return fmt.Errorf("marshaling history entry: %w", err)
	}
	entryID := cas.Blake3Hash(entryJSON)

	_, err = tx.Exec(
		`INSERT INTO ref_history (id, parent, time, actor, ref, old, new, meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entryID, parentID, ts, actor, name, old, new, string(entryJSON),
	)
	if err != nil {
		return fmt.Errorf("inserting ref history: %w", err)
	}

	return nil
}

// ForceSetRef updates a ref without fast-forward check (standalone function).
func ForceSetRef(db *sql.DB, tx *sql.Tx, name string, new []byte, actor, pushID string) error {
	ts := cas.NowMs()

	// Get current target for history
	var currentTarget []byte
	err := tx.QueryRow(`SELECT target FROM refs WHERE name = ?`, name).Scan(&currentTarget)
	if err == sql.ErrNoRows {
		currentTarget = nil
	} else if err != nil {
		return fmt.Errorf("checking current ref: %w", err)
	}

	// Get the last ref_history entry to chain
	var parentID []byte
	err = tx.QueryRow(
		`SELECT id FROM ref_history WHERE ref = ? ORDER BY seq DESC LIMIT 1`,
		name,
	).Scan(&parentID)
	if err == sql.ErrNoRows {
		parentID = nil
	} else if err != nil {
		return fmt.Errorf("getting parent history: %w", err)
	}

	// Upsert ref
	_, err = tx.Exec(
		`INSERT INTO refs (name, target, updated_at, actor, push_id)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET target=excluded.target, updated_at=excluded.updated_at, actor=excluded.actor, push_id=excluded.push_id`,
		name, new, ts, actor, pushID,
	)
	if err != nil {
		return fmt.Errorf("upserting ref: %w", err)
	}

	// Append to ref_history (marked as force)
	historyEntry := map[string]interface{}{
		"time":   ts,
		"actor":  actor,
		"ref":    name,
		"old":    hex.EncodeToString(currentTarget),
		"new":    hex.EncodeToString(new),
		"pushId": pushID,
		"force":  true,
	}
	if parentID != nil {
		historyEntry["parent"] = hex.EncodeToString(parentID)
	}

	entryJSON, err := json.Marshal(historyEntry)
	if err != nil {
		return fmt.Errorf("marshaling history entry: %w", err)
	}
	entryID := cas.Blake3Hash(entryJSON)

	_, err = tx.Exec(
		`INSERT INTO ref_history (id, parent, time, actor, ref, old, new, meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entryID, parentID, ts, actor, name, currentTarget, new, string(entryJSON),
	)
	if err != nil {
		return fmt.Errorf("inserting ref history: %w", err)
	}

	return nil
}

// GetRefHistory retrieves ref history entries (standalone function).
func GetRefHistory(db *sql.DB, refFilter string, afterSeq int64, limit int) ([]*RefHistoryEntry, error) {
	var rows *sql.Rows
	var err error

	if limit <= 0 {
		limit = 100
	}

	if refFilter == "" {
		rows, err = db.Query(
			`SELECT seq, id, parent, time, actor, ref, old, new, meta
			 FROM ref_history WHERE seq > ? ORDER BY seq ASC LIMIT ?`,
			afterSeq, limit,
		)
	} else {
		rows, err = db.Query(
			`SELECT seq, id, parent, time, actor, ref, old, new, meta
			 FROM ref_history WHERE ref = ? AND seq > ? ORDER BY seq ASC LIMIT ?`,
			refFilter, afterSeq, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("querying ref history: %w", err)
	}
	defer rows.Close()

	var entries []*RefHistoryEntry
	for rows.Next() {
		var e RefHistoryEntry
		if err := rows.Scan(&e.Seq, &e.ID, &e.Parent, &e.Time, &e.Actor, &e.Ref, &e.Old, &e.New, &e.Meta); err != nil {
			return nil, fmt.Errorf("scanning ref history: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// GetLogHead returns the ID of the most recent ref_history entry (standalone function).
func GetLogHead(db *sql.DB) ([]byte, error) {
	var id []byte
	err := db.QueryRow(
		`SELECT id FROM ref_history ORDER BY seq DESC LIMIT 1`,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying log head: %w", err)
	}
	return id, nil
}

// EnqueueForEnrichmentTx adds a node to the enrichment queue (standalone function).
func EnqueueForEnrichmentTx(tx *sql.Tx, nodeID []byte, kind string) error {
	ts := cas.NowMs()
	_, err := tx.Exec(
		`INSERT INTO enrich_queue (node_id, kind, status, created_at) VALUES (?, ?, 'pending', ?)`,
		nodeID, kind, ts,
	)
	if err != nil {
		return fmt.Errorf("enqueueing for enrichment: %w", err)
	}
	return nil
}
