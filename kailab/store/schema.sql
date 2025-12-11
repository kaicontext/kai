-- Segments: append-only zstd blobs that contain many objects (content-addressed)
CREATE TABLE IF NOT EXISTS segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  checksum BLOB NOT NULL,
  size INTEGER NOT NULL,
  blob BLOB NOT NULL
);

-- Objects: index into segments by digest
CREATE TABLE IF NOT EXISTS objects (
  digest BLOB PRIMARY KEY,     -- blake3
  segment_id INTEGER NOT NULL,
  off INTEGER NOT NULL,
  len INTEGER NOT NULL,
  kind TEXT NOT NULL,          -- hint: "file","snapshot","symbol","changeset", etc.
  created_at INTEGER NOT NULL,
  FOREIGN KEY(segment_id) REFERENCES segments(id)
);
CREATE INDEX IF NOT EXISTS objects_segment ON objects(segment_id);
CREATE INDEX IF NOT EXISTS objects_kind ON objects(kind);

-- Refs: moving pointers to Kai nodes (ONLY place that "moves")
CREATE TABLE IF NOT EXISTS refs (
  name TEXT PRIMARY KEY,       -- e.g., "snap.main", "cs.latest", "ws.auth.head"
  target BLOB NOT NULL,        -- blake3 of node
  updated_at INTEGER NOT NULL,
  actor TEXT NOT NULL,
  push_id TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS refs_target ON refs(target);

-- Append-only ref history (immutable)
CREATE TABLE IF NOT EXISTS ref_history (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id BLOB UNIQUE NOT NULL,            -- blake3(canonical_json(entry))
  parent BLOB,                        -- previous head id (hash-chain)
  time INTEGER NOT NULL,
  actor TEXT NOT NULL,
  ref TEXT NOT NULL,
  old BLOB,                           -- previous target
  new BLOB NOT NULL,                  -- new target
  meta TEXT                           -- JSON (client, host, etc.)
);
CREATE INDEX IF NOT EXISTS ref_history_ref_time ON ref_history(ref, time);
CREATE INDEX IF NOT EXISTS ref_history_parent ON ref_history(parent);

-- Record first-seen node announcements (also append-only)
CREATE TABLE IF NOT EXISTS node_publish (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id BLOB UNIQUE NOT NULL,            -- blake3(canonical_json(entry))
  parent BLOB,                        -- previous head id (hash-chain)
  time INTEGER NOT NULL,
  actor TEXT NOT NULL,
  node_id BLOB NOT NULL,
  kind TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS node_publish_node_id ON node_publish(node_id);
CREATE INDEX IF NOT EXISTS node_publish_parent ON node_publish(parent);
CREATE INDEX IF NOT EXISTS node_publish_kind ON node_publish(kind);

-- Enrichment queue for background processing
CREATE TABLE IF NOT EXISTS enrich_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id BLOB NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending, processing, done, failed
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER,
  error TEXT
);
CREATE INDEX IF NOT EXISTS enrich_queue_status ON enrich_queue(status);
CREATE INDEX IF NOT EXISTS enrich_queue_node_id ON enrich_queue(node_id);

-- Edges: relationships between nodes (IMPORTS, TESTS, CALLS, DEFINES_IN, etc.)
-- Pushed from CLI which computes them during symbol analysis
CREATE TABLE IF NOT EXISTS edges (
  src BLOB NOT NULL,           -- source node digest
  type TEXT NOT NULL,          -- edge type: IMPORTS, TESTS, CALLS, DEFINES_IN, HAS_FILE, etc.
  dst BLOB NOT NULL,           -- destination node digest
  at BLOB,                     -- snapshot context (optional, for scoped queries)
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst, at)
);
CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_type ON edges(type);
CREATE INDEX IF NOT EXISTS edges_at ON edges(at);
