PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS nodes (
  id BLOB PRIMARY KEY,         -- blake3(kind+payloadCanonicalJSON)
  kind TEXT NOT NULL,          -- File | Module | Symbol | Snapshot | ChangeSet | ChangeType
  payload TEXT NOT NULL,       -- canonical JSON
  created_at INTEGER NOT NULL  -- unix ms
);

CREATE TABLE IF NOT EXISTS edges (
  src BLOB NOT NULL,
  type TEXT NOT NULL,          -- CONTAINS | DEFINES_IN | HAS_FILE | MODIFIES | HAS | AFFECTS
  dst BLOB NOT NULL,
  at  BLOB,                    -- snapshot or changeset context
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst, at)
);

CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_type ON edges(type);
