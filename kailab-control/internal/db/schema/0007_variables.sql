-- Repository and org variables (like secrets but not encrypted, accessible via vars.* context)
CREATE TABLE IF NOT EXISTS workflow_variables (
  id         TEXT PRIMARY KEY,
  repo_id    TEXT REFERENCES repos(id) ON DELETE CASCADE,
  org_id     TEXT REFERENCES orgs(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  value      TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
  updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_workflow_variables_repo_id ON workflow_variables(repo_id);
CREATE INDEX IF NOT EXISTS idx_workflow_variables_org_id ON workflow_variables(org_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_variables_repo_name ON workflow_variables(repo_id, name) WHERE repo_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_variables_org_name ON workflow_variables(org_id, name) WHERE org_id IS NOT NULL AND repo_id IS NULL;
