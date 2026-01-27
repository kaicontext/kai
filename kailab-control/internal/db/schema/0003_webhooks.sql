-- Webhooks for repository events
CREATE TABLE IF NOT EXISTS webhooks (
  id           TEXT PRIMARY KEY,
  repo_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  url          TEXT NOT NULL,
  secret       TEXT,  -- for HMAC signature
  events       TEXT NOT NULL DEFAULT 'push',  -- comma-separated: push,branch_create,branch_delete,tag_create,tag_delete
  active       INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  updated_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_webhooks_repo_id ON webhooks(repo_id);
CREATE INDEX IF NOT EXISTS idx_webhooks_active ON webhooks(active);

-- Webhook delivery log
CREATE TABLE IF NOT EXISTS webhook_deliveries (
  id           TEXT PRIMARY KEY,
  webhook_id   TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  event        TEXT NOT NULL,
  payload      TEXT NOT NULL,  -- JSON payload
  status       TEXT NOT NULL DEFAULT 'pending',  -- pending, success, failed
  response_code INTEGER,
  response_body TEXT,
  attempts     INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  delivered_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook_id ON webhook_deliveries(webhook_id);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status ON webhook_deliveries(status);
