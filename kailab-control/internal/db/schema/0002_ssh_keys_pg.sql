-- SSH Keys Schema
-- PostgreSQL version

-- ssh_keys
CREATE TABLE IF NOT EXISTS ssh_keys (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  fingerprint  TEXT NOT NULL UNIQUE,
  public_key   TEXT NOT NULL,
  created_at   BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  last_used_at BIGINT
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_ssh_keys_user_id ON ssh_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_ssh_keys_fingerprint ON ssh_keys(fingerprint);
