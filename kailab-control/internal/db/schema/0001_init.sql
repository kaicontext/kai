-- Kailab Control Plane Schema
-- SQLite version with UUIDs as TEXT primary keys

-- users
CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  email         TEXT UNIQUE NOT NULL COLLATE NOCASE,
  name          TEXT,
  password_hash TEXT,
  created_at    INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  last_login_at INTEGER
);

-- sessions (for web)
CREATE TABLE IF NOT EXISTS sessions (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash  TEXT NOT NULL,
  user_agent    TEXT,
  ip            TEXT,
  created_at    INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  expires_at    INTEGER NOT NULL
);

-- magic_links (for passwordless login)
CREATE TABLE IF NOT EXISTS magic_links (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL COLLATE NOCASE,
  token_hash    TEXT NOT NULL UNIQUE,
  created_at    INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  expires_at    INTEGER NOT NULL,
  used_at       INTEGER
);

-- orgs (namespaces)
CREATE TABLE IF NOT EXISTS orgs (
  id         TEXT PRIMARY KEY,
  slug       TEXT UNIQUE NOT NULL,
  name       TEXT NOT NULL,
  owner_id   TEXT NOT NULL REFERENCES users(id),
  plan       TEXT NOT NULL DEFAULT 'free',
  created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- memberships
CREATE TABLE IF NOT EXISTS memberships (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  org_id     TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK (role IN ('owner','admin','maintainer','developer','reporter','guest')),
  created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  PRIMARY KEY (user_id, org_id)
);

-- repos (metadata only; code lives in kailabd)
CREATE TABLE IF NOT EXISTS repos (
  id          TEXT PRIMARY KEY,
  org_id      TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  visibility  TEXT NOT NULL CHECK (visibility IN ('private','public','internal')) DEFAULT 'private',
  shard_hint  TEXT NOT NULL,
  created_by  TEXT NOT NULL REFERENCES users(id),
  created_at  INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  UNIQUE (org_id, name)
);

-- PATs for CLI
CREATE TABLE IF NOT EXISTS api_tokens (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  org_id       TEXT,
  name         TEXT NOT NULL,
  hash         TEXT NOT NULL,
  scopes       TEXT NOT NULL DEFAULT '["repo:read","repo:write"]',
  created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  last_used_at INTEGER
);

-- audit
CREATE TABLE IF NOT EXISTS audit (
  id          TEXT PRIMARY KEY,
  org_id      TEXT,
  actor_id    TEXT,
  action      TEXT NOT NULL,
  target_type TEXT,
  target_id   TEXT,
  data        TEXT,
  ts          INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_repos_org_id ON repos(org_id);
CREATE INDEX IF NOT EXISTS idx_memberships_org_id ON memberships(org_id);
CREATE INDEX IF NOT EXISTS idx_audit_org_ts ON audit(org_id, ts);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_magic_links_email ON magic_links(email);
CREATE INDEX IF NOT EXISTS idx_magic_links_token_hash ON magic_links(token_hash);
