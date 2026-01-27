// Package db provides database operations for the control plane.
// Supports both SQLite (local/embedded) and PostgreSQL (production).
package db

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"kailab-control/internal/model"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

//go:embed schema/0001_init.sql
var sqliteSchema string

//go:embed schema/0001_init_pg.sql
var postgresSchema string

//go:embed schema/0002_ssh_keys.sql
var sqliteSSHKeysSchema string

//go:embed schema/0002_ssh_keys_pg.sql
var postgresSSHKeysSchema string

//go:embed schema/0003_webhooks.sql
var sqliteWebhooksSchema string

//go:embed schema/0003_webhooks_pg.sql
var postgresWebhooksSchema string

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidRole   = errors.New("invalid role")
)

// DriverType identifies the database driver.
type DriverType int

const (
	DriverSQLite DriverType = iota
	DriverPostgres
)

// DB wraps the database connection with driver-aware query handling.
type DB struct {
	*sql.DB
	driver DriverType
}

// newUUID generates a new UUID string.
func newUUID() string {
	return uuid.New().String()
}

// Open opens a database connection and runs migrations.
// DSN format:
//   - SQLite: file path or "file:path?mode=memory"
//   - PostgreSQL: "postgres://user:pass@host:port/dbname?sslmode=disable"
func Open(dsn string) (*DB, error) {
	driver, driverName := detectDriver(dsn)

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	wrapped := &DB{DB: db, driver: driver}

	// Driver-specific initialization
	if driver == DriverSQLite {
		if err := wrapped.initSQLite(); err != nil {
			db.Close()
			return nil, err
		}
	} else {
		if err := wrapped.initPostgres(); err != nil {
			db.Close()
			return nil, err
		}
	}

	return wrapped, nil
}

// detectDriver determines the driver type from the DSN.
func detectDriver(dsn string) (DriverType, string) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return DriverPostgres, "postgres"
	}
	return DriverSQLite, "sqlite"
}

// initSQLite runs SQLite-specific setup.
func (db *DB) initSQLite() error {
	// Enable WAL mode and foreign keys
	if _, err := db.DB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enabling WAL: %w", err)
	}
	if _, err := db.DB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}

	// Run SQLite schema
	if _, err := db.DB.Exec(sqliteSchema); err != nil {
		return fmt.Errorf("running SQLite migrations: %w", err)
	}
	// Run SSH keys schema
	if _, err := db.DB.Exec(sqliteSSHKeysSchema); err != nil {
		return fmt.Errorf("running SQLite SSH keys migrations: %w", err)
	}
	// Run webhooks schema
	if _, err := db.DB.Exec(sqliteWebhooksSchema); err != nil {
		return fmt.Errorf("running SQLite webhooks migrations: %w", err)
	}
	return nil
}

// initPostgres runs PostgreSQL-specific setup.
func (db *DB) initPostgres() error {
	// Run PostgreSQL schema
	if _, err := db.DB.Exec(postgresSchema); err != nil {
		return fmt.Errorf("running PostgreSQL migrations: %w", err)
	}
	// Run SSH keys schema
	if _, err := db.DB.Exec(postgresSSHKeysSchema); err != nil {
		return fmt.Errorf("running PostgreSQL SSH keys migrations: %w", err)
	}
	// Run webhooks schema
	if _, err := db.DB.Exec(postgresWebhooksSchema); err != nil {
		return fmt.Errorf("running PostgreSQL webhooks migrations: %w", err)
	}
	return nil
}

// Ping checks database connectivity.
func (db *DB) Ping() error {
	return db.DB.Ping()
}

// Driver returns the current driver type.
func (db *DB) Driver() DriverType {
	return db.driver
}

// ----- Query Helpers -----

// placeholderRegex matches SQLite ? placeholders
var placeholderRegex = regexp.MustCompile(`\?`)

// convertPlaceholders converts ? to $1, $2, etc. for PostgreSQL.
func convertPlaceholders(query string) string {
	counter := 0
	return placeholderRegex.ReplaceAllStringFunc(query, func(_ string) string {
		counter++
		return fmt.Sprintf("$%d", counter)
	})
}

// query executes a query with driver-appropriate placeholders.
func (db *DB) query(q string, args ...interface{}) (*sql.Rows, error) {
	if db.driver == DriverPostgres {
		q = convertPlaceholders(q)
	}
	return db.DB.Query(q, args...)
}

// queryRow executes a query returning a single row.
func (db *DB) queryRow(q string, args ...interface{}) *sql.Row {
	if db.driver == DriverPostgres {
		q = convertPlaceholders(q)
	}
	return db.DB.QueryRow(q, args...)
}

// exec executes a query that doesn't return rows.
func (db *DB) exec(q string, args ...interface{}) (sql.Result, error) {
	if db.driver == DriverPostgres {
		q = convertPlaceholders(q)
	}
	return db.DB.Exec(q, args...)
}

// ----- Users -----

// CreateUser creates a new user.
func (db *DB) CreateUser(email, name string) (*model.User, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO users (id, email, name) VALUES (?, ?, ?)",
		id, email, name,
	)
	if err != nil {
		return nil, err
	}
	return db.GetUserByID(id)
}

// GetUserByID retrieves a user by ID.
func (db *DB) GetUserByID(id string) (*model.User, error) {
	var u model.User
	var createdAt int64
	var lastLoginNull sql.NullInt64
	err := db.queryRow(
		"SELECT id, email, name, COALESCE(password_hash, ''), created_at, last_login_at FROM users WHERE id = ?",
		id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &createdAt, &lastLoginNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastLoginNull.Valid {
		u.LastLoginAt = time.Unix(lastLoginNull.Int64, 0)
	}
	return &u, nil
}

// GetUserByEmail retrieves a user by email (case-insensitive).
func (db *DB) GetUserByEmail(email string) (*model.User, error) {
	var u model.User
	var createdAt int64
	var lastLoginNull sql.NullInt64

	// Use LOWER() for PostgreSQL, COLLATE NOCASE for SQLite
	var query string
	if db.driver == DriverPostgres {
		query = "SELECT id, email, name, COALESCE(password_hash, ''), created_at, last_login_at FROM users WHERE LOWER(email) = LOWER(?)"
	} else {
		query = "SELECT id, email, name, COALESCE(password_hash, ''), created_at, last_login_at FROM users WHERE email = ? COLLATE NOCASE"
	}

	err := db.queryRow(query, email).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &createdAt, &lastLoginNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	if lastLoginNull.Valid {
		u.LastLoginAt = time.Unix(lastLoginNull.Int64, 0)
	}
	return &u, nil
}

// GetOrCreateUser gets a user by email or creates one if not found.
func (db *DB) GetOrCreateUser(email, name string) (*model.User, bool, error) {
	u, err := db.GetUserByEmail(email)
	if err == nil {
		return u, false, nil
	}
	if err != ErrNotFound {
		return nil, false, err
	}
	u, err = db.CreateUser(email, name)
	return u, true, err
}

// UpdateLastLogin updates the user's last login time.
func (db *DB) UpdateLastLogin(userID string) error {
	_, err := db.exec("UPDATE users SET last_login_at = ? WHERE id = ?", time.Now().Unix(), userID)
	return err
}

// ----- Magic Links -----

// CreateMagicLink creates a magic link for passwordless login.
func (db *DB) CreateMagicLink(email, tokenHash string, expiresAt time.Time) error {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO magic_links (id, email, token_hash, expires_at) VALUES (?, ?, ?, ?)",
		id, email, tokenHash, expiresAt.Unix(),
	)
	return err
}

// GetMagicLink retrieves and validates a magic link by token hash.
func (db *DB) GetMagicLink(tokenHash string) (*model.MagicLink, error) {
	var ml model.MagicLink
	var createdAt, expiresAt int64
	var usedAtNull sql.NullInt64
	err := db.queryRow(
		"SELECT id, email, token_hash, created_at, expires_at, used_at FROM magic_links WHERE token_hash = ?",
		tokenHash,
	).Scan(&ml.ID, &ml.Email, &ml.TokenHash, &createdAt, &expiresAt, &usedAtNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ml.CreatedAt = time.Unix(createdAt, 0)
	ml.ExpiresAt = time.Unix(expiresAt, 0)
	if usedAtNull.Valid {
		ml.UsedAt = time.Unix(usedAtNull.Int64, 0)
	}
	return &ml, nil
}

// UseMagicLink marks a magic link as used.
func (db *DB) UseMagicLink(id string) error {
	_, err := db.exec("UPDATE magic_links SET used_at = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

// CleanupExpiredMagicLinks removes expired magic links.
func (db *DB) CleanupExpiredMagicLinks() error {
	_, err := db.exec("DELETE FROM magic_links WHERE expires_at < ?", time.Now().Unix())
	return err
}

// ----- Sessions -----

// CreateSession creates a new session.
func (db *DB) CreateSession(userID string, refreshHash, userAgent, ip string, expiresAt time.Time) (*model.Session, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO sessions (id, user_id, refresh_hash, user_agent, ip, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, userID, refreshHash, userAgent, ip, expiresAt.Unix(),
	)
	if err != nil {
		return nil, err
	}
	return db.GetSession(id)
}

// GetSession retrieves a session by ID.
func (db *DB) GetSession(id string) (*model.Session, error) {
	var s model.Session
	var createdAt, expiresAt int64
	err := db.queryRow(
		"SELECT id, user_id, refresh_hash, user_agent, ip, created_at, expires_at FROM sessions WHERE id = ?",
		id,
	).Scan(&s.ID, &s.UserID, &s.RefreshHash, &s.UserAgent, &s.IP, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt = time.Unix(createdAt, 0)
	s.ExpiresAt = time.Unix(expiresAt, 0)
	return &s, nil
}

// GetSessionByRefreshHash retrieves a session by refresh token hash.
func (db *DB) GetSessionByRefreshHash(hash string) (*model.Session, error) {
	var s model.Session
	var createdAt, expiresAt int64
	err := db.queryRow(
		"SELECT id, user_id, refresh_hash, user_agent, ip, created_at, expires_at FROM sessions WHERE refresh_hash = ?",
		hash,
	).Scan(&s.ID, &s.UserID, &s.RefreshHash, &s.UserAgent, &s.IP, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt = time.Unix(createdAt, 0)
	s.ExpiresAt = time.Unix(expiresAt, 0)
	return &s, nil
}

// DeleteSession deletes a session.
func (db *DB) DeleteSession(id string) error {
	_, err := db.exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

// DeleteUserSessions deletes all sessions for a user.
func (db *DB) DeleteUserSessions(userID string) error {
	_, err := db.exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// ----- Orgs -----

// CreateOrg creates a new organization.
func (db *DB) CreateOrg(slug, name string, ownerID string) (*model.Org, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO orgs (id, slug, name, owner_id) VALUES (?, ?, ?, ?)",
		id, slug, name, ownerID,
	)
	if err != nil {
		return nil, err
	}

	// Add owner as owner member
	if _, err := db.exec(
		"INSERT INTO memberships (user_id, org_id, role) VALUES (?, ?, ?)",
		ownerID, id, model.RoleOwner,
	); err != nil {
		return nil, err
	}

	return db.GetOrgByID(id)
}

// GetOrgByID retrieves an org by ID.
func (db *DB) GetOrgByID(id string) (*model.Org, error) {
	var o model.Org
	var createdAt int64
	err := db.queryRow(
		"SELECT id, slug, name, owner_id, plan, created_at FROM orgs WHERE id = ?",
		id,
	).Scan(&o.ID, &o.Slug, &o.Name, &o.OwnerID, &o.Plan, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(createdAt, 0)
	return &o, nil
}

// GetOrgBySlug retrieves an org by slug.
func (db *DB) GetOrgBySlug(slug string) (*model.Org, error) {
	var o model.Org
	var createdAt int64
	err := db.queryRow(
		"SELECT id, slug, name, owner_id, plan, created_at FROM orgs WHERE slug = ?",
		slug,
	).Scan(&o.ID, &o.Slug, &o.Name, &o.OwnerID, &o.Plan, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(createdAt, 0)
	return &o, nil
}

// ListUserOrgs lists all orgs a user belongs to.
func (db *DB) ListUserOrgs(userID string) ([]*model.Org, error) {
	rows, err := db.query(`
		SELECT o.id, o.slug, o.name, o.owner_id, o.plan, o.created_at
		FROM orgs o
		JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id = ?
		ORDER BY o.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []*model.Org
	for rows.Next() {
		var o model.Org
		var createdAt int64
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.OwnerID, &o.Plan, &createdAt); err != nil {
			return nil, err
		}
		o.CreatedAt = time.Unix(createdAt, 0)
		orgs = append(orgs, &o)
	}
	return orgs, rows.Err()
}

// ----- Memberships -----

// AddMember adds a user to an org (upserts if already exists).
func (db *DB) AddMember(orgID, userID string, role string) error {
	if _, ok := model.RoleHierarchy[role]; !ok {
		return ErrInvalidRole
	}

	// Use driver-specific upsert syntax
	var query string
	if db.driver == DriverPostgres {
		query = `INSERT INTO memberships (user_id, org_id, role) VALUES (?, ?, ?)
				 ON CONFLICT (user_id, org_id) DO UPDATE SET role = EXCLUDED.role`
	} else {
		query = "INSERT OR REPLACE INTO memberships (user_id, org_id, role) VALUES (?, ?, ?)"
	}

	_, err := db.exec(query, userID, orgID, role)
	return err
}

// RemoveMember removes a user from an org.
func (db *DB) RemoveMember(orgID, userID string) error {
	_, err := db.exec("DELETE FROM memberships WHERE org_id = ? AND user_id = ?", orgID, userID)
	return err
}

// GetMembership gets a user's membership in an org.
func (db *DB) GetMembership(orgID, userID string) (*model.Membership, error) {
	var m model.Membership
	var createdAt int64
	err := db.queryRow(
		"SELECT user_id, org_id, role, created_at FROM memberships WHERE org_id = ? AND user_id = ?",
		orgID, userID,
	).Scan(&m.UserID, &m.OrgID, &m.Role, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	return &m, nil
}

// ListOrgMembers lists all members of an org.
func (db *DB) ListOrgMembers(orgID string) ([]*model.Membership, error) {
	rows, err := db.query(
		"SELECT user_id, org_id, role, created_at FROM memberships WHERE org_id = ?",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*model.Membership
	for rows.Next() {
		var m model.Membership
		var createdAt int64
		if err := rows.Scan(&m.UserID, &m.OrgID, &m.Role, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		members = append(members, &m)
	}
	return members, rows.Err()
}

// ----- Repos -----

// CreateRepo creates a new repository.
func (db *DB) CreateRepo(orgID string, name, visibility, shardHint string, createdBy string) (*model.Repo, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO repos (id, org_id, name, visibility, shard_hint, created_by) VALUES (?, ?, ?, ?, ?, ?)",
		id, orgID, name, visibility, shardHint, createdBy,
	)
	if err != nil {
		return nil, err
	}
	return db.GetRepoByID(id)
}

// GetRepoByID retrieves a repo by ID.
func (db *DB) GetRepoByID(id string) (*model.Repo, error) {
	var r model.Repo
	var createdAt int64
	err := db.queryRow(
		"SELECT id, org_id, name, visibility, shard_hint, created_by, created_at FROM repos WHERE id = ?",
		id,
	).Scan(&r.ID, &r.OrgID, &r.Name, &r.Visibility, &r.ShardHint, &r.CreatedBy, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	return &r, nil
}

// GetRepoByOrgAndName retrieves a repo by org ID and name.
func (db *DB) GetRepoByOrgAndName(orgID string, name string) (*model.Repo, error) {
	var r model.Repo
	var createdAt int64
	err := db.queryRow(
		"SELECT id, org_id, name, visibility, shard_hint, created_by, created_at FROM repos WHERE org_id = ? AND name = ?",
		orgID, name,
	).Scan(&r.ID, &r.OrgID, &r.Name, &r.Visibility, &r.ShardHint, &r.CreatedBy, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	return &r, nil
}

// ListOrgRepos lists all repos in an org.
func (db *DB) ListOrgRepos(orgID string) ([]*model.Repo, error) {
	rows, err := db.query(
		"SELECT id, org_id, name, visibility, shard_hint, created_by, created_at FROM repos WHERE org_id = ? ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []*model.Repo
	for rows.Next() {
		var r model.Repo
		var createdAt int64
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Name, &r.Visibility, &r.ShardHint, &r.CreatedBy, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(createdAt, 0)
		repos = append(repos, &r)
	}
	return repos, rows.Err()
}

// DeleteRepo deletes a repo.
func (db *DB) DeleteRepo(id string) error {
	_, err := db.exec("DELETE FROM repos WHERE id = ?", id)
	return err
}

// ----- API Tokens -----

// CreateAPIToken creates a new API token.
func (db *DB) CreateAPIToken(userID string, orgID string, name, hash string, scopes []string) (*model.APIToken, error) {
	id := newUUID()
	scopesJSON, _ := json.Marshal(scopes)
	var orgIDPtr interface{}
	if orgID != "" {
		orgIDPtr = orgID
	}
	_, err := db.exec(
		"INSERT INTO api_tokens (id, user_id, org_id, name, hash, scopes) VALUES (?, ?, ?, ?, ?, ?)",
		id, userID, orgIDPtr, name, hash, string(scopesJSON),
	)
	if err != nil {
		return nil, err
	}
	return db.GetAPIToken(id)
}

// GetAPIToken retrieves an API token by ID.
func (db *DB) GetAPIToken(id string) (*model.APIToken, error) {
	var t model.APIToken
	var createdAt int64
	var lastUsedNull sql.NullInt64
	var orgIDNull sql.NullString
	var scopesJSON string
	err := db.queryRow(
		"SELECT id, user_id, org_id, name, hash, scopes, created_at, last_used_at FROM api_tokens WHERE id = ?",
		id,
	).Scan(&t.ID, &t.UserID, &orgIDNull, &t.Name, &t.Hash, &scopesJSON, &createdAt, &lastUsedNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	if lastUsedNull.Valid {
		t.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
	}
	if orgIDNull.Valid {
		t.OrgID = orgIDNull.String
	}
	json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	return &t, nil
}

// GetAPITokenByHash retrieves an API token by hash.
func (db *DB) GetAPITokenByHash(hash string) (*model.APIToken, error) {
	var t model.APIToken
	var createdAt int64
	var lastUsedNull sql.NullInt64
	var orgIDNull sql.NullString
	var scopesJSON string
	err := db.queryRow(
		"SELECT id, user_id, org_id, name, hash, scopes, created_at, last_used_at FROM api_tokens WHERE hash = ?",
		hash,
	).Scan(&t.ID, &t.UserID, &orgIDNull, &t.Name, &t.Hash, &scopesJSON, &createdAt, &lastUsedNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	if lastUsedNull.Valid {
		t.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
	}
	if orgIDNull.Valid {
		t.OrgID = orgIDNull.String
	}
	json.Unmarshal([]byte(scopesJSON), &t.Scopes)
	return &t, nil
}

// ListUserAPITokens lists all API tokens for a user.
func (db *DB) ListUserAPITokens(userID string) ([]*model.APIToken, error) {
	rows, err := db.query(
		"SELECT id, user_id, org_id, name, hash, scopes, created_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*model.APIToken
	for rows.Next() {
		var t model.APIToken
		var createdAt int64
		var lastUsedNull sql.NullInt64
		var orgIDNull sql.NullString
		var scopesJSON string
		if err := rows.Scan(&t.ID, &t.UserID, &orgIDNull, &t.Name, &t.Hash, &scopesJSON, &createdAt, &lastUsedNull); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		if lastUsedNull.Valid {
			t.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
		}
		if orgIDNull.Valid {
			t.OrgID = orgIDNull.String
		}
		json.Unmarshal([]byte(scopesJSON), &t.Scopes)
		tokens = append(tokens, &t)
	}
	return tokens, rows.Err()
}

// DeleteAPIToken deletes an API token.
func (db *DB) DeleteAPIToken(id string) error {
	_, err := db.exec("DELETE FROM api_tokens WHERE id = ?", id)
	return err
}

// UpdateAPITokenLastUsed updates the last used time for a token.
func (db *DB) UpdateAPITokenLastUsed(id string) error {
	_, err := db.exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

// ----- Audit -----

// WriteAudit writes an audit log entry.
func (db *DB) WriteAudit(orgID *string, actorID *string, action, targetType, targetID string, data map[string]string) error {
	id := newUUID()
	dataJSON, _ := json.Marshal(data)
	_, err := db.exec(
		"INSERT INTO audit (id, org_id, actor_id, action, target_type, target_id, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, orgID, actorID, action, targetType, targetID, string(dataJSON),
	)
	return err
}

// ListOrgAudit lists audit entries for an org.
func (db *DB) ListOrgAudit(orgID string, limit int) ([]*model.AuditEntry, error) {
	rows, err := db.query(
		"SELECT id, org_id, actor_id, action, target_type, target_id, data, ts FROM audit WHERE org_id = ? ORDER BY ts DESC LIMIT ?",
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var ts int64
		var orgIDNull, actorIDNull sql.NullString
		var targetType, targetID, dataJSON sql.NullString
		if err := rows.Scan(&e.ID, &orgIDNull, &actorIDNull, &e.Action, &targetType, &targetID, &dataJSON, &ts); err != nil {
			return nil, err
		}
		e.Timestamp = time.Unix(ts, 0)
		if orgIDNull.Valid {
			e.OrgID = orgIDNull.String
		}
		if actorIDNull.Valid {
			e.ActorID = actorIDNull.String
		}
		e.TargetType = targetType.String
		e.TargetID = targetID.String
		if dataJSON.Valid {
			json.Unmarshal([]byte(dataJSON.String), &e.Data)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// ----- SSH Keys -----

// CreateSSHKey creates a new SSH key for a user.
func (db *DB) CreateSSHKey(userID, name, fingerprint, publicKey string) (*model.SSHKey, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO ssh_keys (id, user_id, name, fingerprint, public_key) VALUES (?, ?, ?, ?, ?)",
		id, userID, name, fingerprint, publicKey,
	)
	if err != nil {
		return nil, err
	}
	return db.GetSSHKeyByID(id)
}

// GetSSHKeyByID retrieves an SSH key by ID.
func (db *DB) GetSSHKeyByID(id string) (*model.SSHKey, error) {
	var k model.SSHKey
	var createdAt int64
	var lastUsedNull sql.NullInt64
	err := db.queryRow(
		"SELECT id, user_id, name, fingerprint, public_key, created_at, last_used_at FROM ssh_keys WHERE id = ?",
		id,
	).Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &createdAt, &lastUsedNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt = time.Unix(createdAt, 0)
	if lastUsedNull.Valid {
		k.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
	}
	return &k, nil
}

// GetSSHKeyByFingerprint retrieves an SSH key by fingerprint.
func (db *DB) GetSSHKeyByFingerprint(fingerprint string) (*model.SSHKey, error) {
	var k model.SSHKey
	var createdAt int64
	var lastUsedNull sql.NullInt64
	err := db.queryRow(
		"SELECT id, user_id, name, fingerprint, public_key, created_at, last_used_at FROM ssh_keys WHERE fingerprint = ?",
		fingerprint,
	).Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &createdAt, &lastUsedNull)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt = time.Unix(createdAt, 0)
	if lastUsedNull.Valid {
		k.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
	}
	return &k, nil
}

// ListUserSSHKeys lists all SSH keys for a user.
func (db *DB) ListUserSSHKeys(userID string) ([]*model.SSHKey, error) {
	rows, err := db.query(
		"SELECT id, user_id, name, fingerprint, public_key, created_at, last_used_at FROM ssh_keys WHERE user_id = ? ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*model.SSHKey
	for rows.Next() {
		var k model.SSHKey
		var createdAt int64
		var lastUsedNull sql.NullInt64
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &createdAt, &lastUsedNull); err != nil {
			return nil, err
		}
		k.CreatedAt = time.Unix(createdAt, 0)
		if lastUsedNull.Valid {
			k.LastUsedAt = time.Unix(lastUsedNull.Int64, 0)
		}
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

// DeleteSSHKey deletes an SSH key.
func (db *DB) DeleteSSHKey(id string) error {
	_, err := db.exec("DELETE FROM ssh_keys WHERE id = ?", id)
	return err
}

// UpdateSSHKeyLastUsed updates the last used time for an SSH key.
func (db *DB) UpdateSSHKeyLastUsed(id string) error {
	_, err := db.exec("UPDATE ssh_keys SET last_used_at = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

// ----- Webhook Methods -----

// CreateWebhook creates a new webhook for a repository.
func (db *DB) CreateWebhook(repoID, url, secret string, events []string) (*model.Webhook, error) {
	id := newUUID()
	now := time.Now().Unix()
	eventsStr := strings.Join(events, ",")

	_, err := db.exec(
		"INSERT INTO webhooks (id, repo_id, url, secret, events, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, repoID, url, secret, eventsStr, 1, now, now,
	)
	if err != nil {
		return nil, err
	}

	return &model.Webhook{
		ID:        id,
		RepoID:    repoID,
		URL:       url,
		Secret:    secret,
		Events:    events,
		Active:    true,
		CreatedAt: time.Unix(now, 0),
		UpdatedAt: time.Unix(now, 0),
	}, nil
}

// GetWebhookByID retrieves a webhook by ID.
func (db *DB) GetWebhookByID(id string) (*model.Webhook, error) {
	var w model.Webhook
	var eventsStr string
	var active int
	var createdAt, updatedAt int64

	err := db.queryRow(
		"SELECT id, repo_id, url, secret, events, active, created_at, updated_at FROM webhooks WHERE id = ?",
		id,
	).Scan(&w.ID, &w.RepoID, &w.URL, &w.Secret, &eventsStr, &active, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	w.Events = strings.Split(eventsStr, ",")
	w.Active = active == 1
	w.CreatedAt = time.Unix(createdAt, 0)
	w.UpdatedAt = time.Unix(updatedAt, 0)
	return &w, nil
}

// ListRepoWebhooks lists all webhooks for a repository.
func (db *DB) ListRepoWebhooks(repoID string) ([]*model.Webhook, error) {
	rows, err := db.query(
		"SELECT id, repo_id, url, secret, events, active, created_at, updated_at FROM webhooks WHERE repo_id = ? ORDER BY created_at DESC",
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []*model.Webhook
	for rows.Next() {
		var w model.Webhook
		var eventsStr string
		var active int
		var createdAt, updatedAt int64

		if err := rows.Scan(&w.ID, &w.RepoID, &w.URL, &w.Secret, &eventsStr, &active, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		w.Events = strings.Split(eventsStr, ",")
		w.Active = active == 1
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		webhooks = append(webhooks, &w)
	}
	return webhooks, rows.Err()
}

// ListActiveWebhooksForEvent lists all active webhooks for a repo that subscribe to a given event.
func (db *DB) ListActiveWebhooksForEvent(repoID, event string) ([]*model.Webhook, error) {
	// We need to check if the event is in the comma-separated events field
	// SQLite and Postgres handle this differently, but we can use LIKE for both
	rows, err := db.query(
		"SELECT id, repo_id, url, secret, events, active, created_at, updated_at FROM webhooks WHERE repo_id = ? AND active = 1 AND (events LIKE ? OR events LIKE ? OR events LIKE ? OR events = ?)",
		repoID, event+",%", "%,"+event+",%", "%,"+event, event,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []*model.Webhook
	for rows.Next() {
		var w model.Webhook
		var eventsStr string
		var active int
		var createdAt, updatedAt int64

		if err := rows.Scan(&w.ID, &w.RepoID, &w.URL, &w.Secret, &eventsStr, &active, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		w.Events = strings.Split(eventsStr, ",")
		w.Active = active == 1
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		webhooks = append(webhooks, &w)
	}
	return webhooks, rows.Err()
}

// UpdateWebhook updates a webhook.
func (db *DB) UpdateWebhook(id, url, secret string, events []string, active bool) error {
	activeInt := 0
	if active {
		activeInt = 1
	}
	eventsStr := strings.Join(events, ",")
	now := time.Now().Unix()

	_, err := db.exec(
		"UPDATE webhooks SET url = ?, secret = ?, events = ?, active = ?, updated_at = ? WHERE id = ?",
		url, secret, eventsStr, activeInt, now, id,
	)
	return err
}

// DeleteWebhook deletes a webhook.
func (db *DB) DeleteWebhook(id string) error {
	_, err := db.exec("DELETE FROM webhooks WHERE id = ?", id)
	return err
}

// ----- Webhook Delivery Methods -----

// CreateWebhookDelivery creates a new webhook delivery record.
func (db *DB) CreateWebhookDelivery(webhookID, event, payload string) (*model.WebhookDelivery, error) {
	id := newUUID()
	now := time.Now().Unix()

	_, err := db.exec(
		"INSERT INTO webhook_deliveries (id, webhook_id, event, payload, status, attempts, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, webhookID, event, payload, model.DeliveryPending, 0, now,
	)
	if err != nil {
		return nil, err
	}

	return &model.WebhookDelivery{
		ID:        id,
		WebhookID: webhookID,
		Event:     event,
		Payload:   payload,
		Status:    model.DeliveryPending,
		Attempts:  0,
		CreatedAt: time.Unix(now, 0),
	}, nil
}

// UpdateWebhookDelivery updates a delivery record after an attempt.
func (db *DB) UpdateWebhookDelivery(id, status string, responseCode int, responseBody string, attempts int) error {
	now := time.Now().Unix()
	_, err := db.exec(
		"UPDATE webhook_deliveries SET status = ?, response_code = ?, response_body = ?, attempts = ?, delivered_at = ? WHERE id = ?",
		status, responseCode, responseBody, attempts, now, id,
	)
	return err
}

// ListWebhookDeliveries lists recent deliveries for a webhook.
func (db *DB) ListWebhookDeliveries(webhookID string, limit int) ([]*model.WebhookDelivery, error) {
	rows, err := db.query(
		"SELECT id, webhook_id, event, payload, status, response_code, response_body, attempts, created_at, delivered_at FROM webhook_deliveries WHERE webhook_id = ? ORDER BY created_at DESC LIMIT ?",
		webhookID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*model.WebhookDelivery
	for rows.Next() {
		var d model.WebhookDelivery
		var responseCode sql.NullInt64
		var responseBody sql.NullString
		var createdAt int64
		var deliveredAt sql.NullInt64

		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Payload, &d.Status, &responseCode, &responseBody, &d.Attempts, &createdAt, &deliveredAt); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(createdAt, 0)
		if responseCode.Valid {
			d.ResponseCode = int(responseCode.Int64)
		}
		if responseBody.Valid {
			d.ResponseBody = responseBody.String
		}
		if deliveredAt.Valid {
			d.DeliveredAt = time.Unix(deliveredAt.Int64, 0)
		}
		deliveries = append(deliveries, &d)
	}
	return deliveries, rows.Err()
}

// GetPendingDeliveries gets deliveries that need to be retried.
func (db *DB) GetPendingDeliveries(limit int) ([]*model.WebhookDelivery, error) {
	rows, err := db.query(
		"SELECT id, webhook_id, event, payload, status, response_code, response_body, attempts, created_at, delivered_at FROM webhook_deliveries WHERE status = ? AND attempts < 3 ORDER BY created_at ASC LIMIT ?",
		model.DeliveryPending, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*model.WebhookDelivery
	for rows.Next() {
		var d model.WebhookDelivery
		var responseCode sql.NullInt64
		var responseBody sql.NullString
		var createdAt int64
		var deliveredAt sql.NullInt64

		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Payload, &d.Status, &responseCode, &responseBody, &d.Attempts, &createdAt, &deliveredAt); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(createdAt, 0)
		if responseCode.Valid {
			d.ResponseCode = int(responseCode.Int64)
		}
		if responseBody.Valid {
			d.ResponseBody = responseBody.String
		}
		if deliveredAt.Valid {
			d.DeliveredAt = time.Unix(deliveredAt.Int64, 0)
		}
		deliveries = append(deliveries, &d)
	}
	return deliveries, rows.Err()
}
