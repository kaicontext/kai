-- CI enhancements: runs_on labels on jobs + concurrency locks (PostgreSQL)

-- Add runs_on to jobs for runner label matching
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS runs_on TEXT NOT NULL DEFAULT '[]';

-- Concurrency locks for workflow/job-level concurrency controls
CREATE TABLE IF NOT EXISTS concurrency_locks (
  id              TEXT PRIMARY KEY,
  group_key       TEXT NOT NULL UNIQUE,
  workflow_run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  job_id          TEXT,
  repo_id         TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  created_at      BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);

CREATE INDEX IF NOT EXISTS idx_concurrency_locks_group ON concurrency_locks(group_key);
CREATE INDEX IF NOT EXISTS idx_concurrency_locks_run ON concurrency_locks(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_concurrency_locks_repo ON concurrency_locks(repo_id);
