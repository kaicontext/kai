-- CI/CD workflows and job execution (PostgreSQL)

-- Workflows: parsed workflow definitions from .kailab/workflows/*.yml
CREATE TABLE IF NOT EXISTS workflows (
  id           TEXT PRIMARY KEY,
  repo_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,  -- e.g., .kailab/workflows/ci.yml
  name         TEXT NOT NULL,
  content_hash TEXT NOT NULL,  -- SHA256 of file content for change detection
  parsed_json  TEXT NOT NULL,  -- Parsed workflow as JSON
  triggers     TEXT NOT NULL,  -- JSON array: ["push", "review", "workflow_dispatch"]
  active       BOOLEAN NOT NULL DEFAULT true,
  created_at   BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  updated_at   BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  UNIQUE (repo_id, path)
);

CREATE INDEX IF NOT EXISTS idx_workflows_repo_id ON workflows(repo_id);
CREATE INDEX IF NOT EXISTS idx_workflows_active ON workflows(active);

-- Workflow Runs: execution instances
CREATE TABLE IF NOT EXISTS workflow_runs (
  id              TEXT PRIMARY KEY,
  workflow_id     TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  repo_id         TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  run_number      INTEGER NOT NULL,  -- Sequential per workflow
  trigger_event   TEXT NOT NULL,     -- push, review_created, review_updated, workflow_dispatch
  trigger_ref     TEXT,              -- refs/heads/main
  trigger_sha     TEXT,              -- Commit SHA that triggered the run
  trigger_payload TEXT,              -- JSON with event-specific data
  status          TEXT NOT NULL DEFAULT 'queued',  -- queued, in_progress, completed
  conclusion      TEXT,              -- success, failure, cancelled, skipped
  started_at      BIGINT,
  completed_at    BIGINT,
  created_at      BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  created_by      TEXT               -- User ID for manual dispatch
);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_workflow_id ON workflow_runs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_repo_id ON workflow_runs(repo_id);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created_at ON workflow_runs(created_at);

-- Jobs: individual jobs within a run
CREATE TABLE IF NOT EXISTS jobs (
  id              TEXT PRIMARY KEY,
  workflow_run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  runner_id       TEXT,              -- Assigned runner
  status          TEXT NOT NULL DEFAULT 'queued',  -- queued, pending, in_progress, completed
  conclusion      TEXT,              -- success, failure, cancelled, skipped
  matrix_values   TEXT,              -- JSON object of matrix values for this job instance
  needs           TEXT,              -- JSON array of job names this depends on
  runner_name     TEXT,              -- Name of the runner that executed this job
  started_at      BIGINT,
  completed_at    BIGINT,
  created_at      BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);

CREATE INDEX IF NOT EXISTS idx_jobs_workflow_run_id ON jobs(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_runner_id ON jobs(runner_id);

-- Steps: individual steps within a job
CREATE TABLE IF NOT EXISTS steps (
  id           TEXT PRIMARY KEY,
  job_id       TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  number       INTEGER NOT NULL,
  name         TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',  -- pending, in_progress, completed
  conclusion   TEXT,              -- success, failure, cancelled, skipped
  started_at   BIGINT,
  completed_at BIGINT,
  UNIQUE (job_id, number)
);

CREATE INDEX IF NOT EXISTS idx_steps_job_id ON steps(job_id);

-- Job Logs: stored as chunks for streaming
CREATE TABLE IF NOT EXISTS job_logs (
  id         TEXT PRIMARY KEY,
  job_id     TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  step_id    TEXT REFERENCES steps(id) ON DELETE CASCADE,
  chunk_seq  INTEGER NOT NULL,
  content    TEXT NOT NULL,
  timestamp  BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);

CREATE INDEX IF NOT EXISTS idx_job_logs_job_id ON job_logs(job_id);
CREATE INDEX IF NOT EXISTS idx_job_logs_step_id ON job_logs(step_id);
CREATE INDEX IF NOT EXISTS idx_job_logs_chunk_seq ON job_logs(job_id, chunk_seq);

-- Artifacts: files produced by workflow runs
CREATE TABLE IF NOT EXISTS artifacts (
  id              TEXT PRIMARY KEY,
  workflow_run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  job_id          TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  path            TEXT NOT NULL,     -- Storage path
  size            BIGINT NOT NULL,
  expires_at      BIGINT,
  created_at      BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);

CREATE INDEX IF NOT EXISTS idx_artifacts_workflow_run_id ON artifacts(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_job_id ON artifacts(job_id);

-- Workflow Cache: cached directories for faster builds
CREATE TABLE IF NOT EXISTS workflow_cache (
  id         TEXT PRIMARY KEY,
  repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  cache_key  TEXT NOT NULL,
  path       TEXT NOT NULL,          -- Storage path
  size       BIGINT NOT NULL,
  created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  last_used  BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  UNIQUE (repo_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_workflow_cache_repo_id ON workflow_cache(repo_id);
CREATE INDEX IF NOT EXISTS idx_workflow_cache_last_used ON workflow_cache(last_used);

-- Workflow Secrets: encrypted secrets for workflows
CREATE TABLE IF NOT EXISTS workflow_secrets (
  id         TEXT PRIMARY KEY,
  repo_id    TEXT REFERENCES repos(id) ON DELETE CASCADE,
  org_id     TEXT REFERENCES orgs(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  encrypted  BYTEA NOT NULL,         -- Encrypted secret value
  created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
  created_by TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflow_secrets_repo_id ON workflow_secrets(repo_id);
CREATE INDEX IF NOT EXISTS idx_workflow_secrets_org_id ON workflow_secrets(org_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_secrets_repo_name ON workflow_secrets(repo_id, name) WHERE repo_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_secrets_org_name ON workflow_secrets(org_id, name) WHERE org_id IS NOT NULL AND repo_id IS NULL;

-- Runners: registered CI runners
CREATE TABLE IF NOT EXISTS runners (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  org_id       TEXT REFERENCES orgs(id) ON DELETE CASCADE,  -- NULL for global runners
  labels       TEXT NOT NULL DEFAULT '[]',  -- JSON array of labels
  status       TEXT NOT NULL DEFAULT 'offline',  -- online, offline, busy
  last_seen_at BIGINT,
  created_at   BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
);

CREATE INDEX IF NOT EXISTS idx_runners_org_id ON runners(org_id);
CREATE INDEX IF NOT EXISTS idx_runners_status ON runners(status);
