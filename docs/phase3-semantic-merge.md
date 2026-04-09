# Phase 3: Semantic Merge and Live Sync

## Overview

Two modes, layered:
- **Mode 1: Graph-based merge** (default, always on) — edge mutation log with sequence numbers, sync, validation, automated merge
- **Mode 2: Live sync** (opt-in) — SSE stream of real-time changes between agents

## Existing Infrastructure

These components already exist and should be extended, not rebuilt:

| Component | Location | Status |
|-----------|----------|--------|
| Semantic 3-way merge | `kai-core/merge/merge.go` | Complete — unit-level, 11 conflict types |
| Workspace/staging | `kai-cli/internal/workspace/` | Complete |
| Cherry-pick/rebase | `kai-cli/internal/workspace/` | Partial — file-level conflicts only |
| Integrate | `kai-cli/internal/workspace/integrate.go` | Stub — fast-forward only |
| Review system | `kai-cli/internal/review/` | Complete |
| Append-only ref log | `ref_history` table, `GET /v1/log/entries` | Complete |
| Semantic diff | `kai-core/diff/` | Complete |
| Activity/locks | `kai-server/kailab/api/activity.go` | Complete (in-memory, 5-min TTL) |
| Incremental edge push | `POST /v1/edges/incremental` | Complete |

### Key gap: kai-core/merge is not wired into workspace ops

The merge engine in `kai-core/merge/` does unit-level 3-way merge with conflict detection for functions, classes, imports, constants, etc. But `workspace/cherrypick.go` and `workspace/integrate.go` only do file-level conflict detection. Wiring these together is Step 0.

---

## Mode 1: Graph-Based Merge

### New server table: edge_log

Append-only log of all edge mutations. Modeled after existing `ref_history`.

```sql
CREATE TABLE edge_log (
    seq         BIGSERIAL PRIMARY KEY,
    tenant      TEXT NOT NULL,
    repo        TEXT NOT NULL,
    agent       TEXT NOT NULL,
    actor       TEXT NOT NULL,
    file        TEXT NOT NULL,
    action      TEXT NOT NULL,          -- "add" or "remove"
    src         BYTEA NOT NULL,
    edge_type   TEXT NOT NULL,          -- IMPORTS, CALLS, TESTS, DEFINES_IN
    dst         BYTEA NOT NULL,
    changeset_id TEXT,
    created_at  BIGINT NOT NULL DEFAULT extract(epoch from now()) * 1000
);

CREATE INDEX idx_edge_log_repo_seq ON edge_log(tenant, repo, seq);
CREATE INDEX idx_edge_log_agent ON edge_log(tenant, repo, agent);
```

### New endpoints

**POST /v1/edges/validate** — pre-flight conflict check

```
Request:  { agent, actor, updates: [{file, added_edges[], removed_edges[]}] }
Response: { ok: bool, conflicts: [{file, edge, reason, detail, other_agent}], warnings[] }
```

**GET /v1/edges/sync?since={seq}&agent={agent}** — fetch other agents' changes

```
Response: { updates: [{seq, agent, actor, time, file, added_edges[], removed_edges[]}], latest_seq, has_more }
```

**POST /v1/merge/validate** — can this changeset merge?

```
Request:  { agent, changeset_id, base_snapshot, files: [{path, digest, edges[]}] }
Response: { mergeable: bool, conflicts[], other_agents_files[], auto_merged_edges }
```

**POST /v1/merge/execute** — apply the merge

```
Request:  { agent, changeset_id, base_snapshot, message, files: [{path, digest, content_digest}] }
Response: { ok, merged_snapshot, merged_changeset, applied_files, auto_merged_edges, new_seq }
```

### Extend existing endpoint

**POST /v1/edges/incremental** — add optional `agent`, `changeset_id` fields and return `seq` in response. Old clients unaffected.

### Validation rules

Ordered cheapest to most expensive:

1. **File disjointness** — different files, no shared edges -> always safe (fast path)
2. **Edge removal safety** — can't remove an edge another agent's pending changeset depends on
3. **Concurrent file edit** — same file by two agents -> conflict (unit of merge is the file, unless kai-core/merge resolves it at unit level)
4. **Cycle detection** — adding IMPORTS can't create import cycle
5. **Test coverage preservation** — removing TESTS edge for a file being edited by another agent -> warning (advisory)

### MCP tools

- `kai_sync` — fetch what other agents changed since last sync
- `kai_merge_check` — "can I land this?" pre-flight validation
- `kai_merge` — execute the merge (replaces PR for agent-to-agent work)

---

## Mode 2: Live Sync (opt-in)

### New server table: sync_channels

```sql
CREATE TABLE sync_channels (
    channel_id  TEXT PRIMARY KEY,
    tenant      TEXT NOT NULL,
    repo        TEXT NOT NULL,
    agent       TEXT NOT NULL,
    actor       TEXT NOT NULL,
    filter      JSONB,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL,
    last_seq    BIGINT NOT NULL DEFAULT 0
);
```

### New endpoints

**POST /v1/sync/subscribe** — register for live sync

```
Request:  { agent, actor, filter: {files[], modules[], all: bool} }
Response: { channel_id, expires_at, current_seq }
```

**GET /v1/sync/events?channel={id}** — SSE stream

```
Events: edge_change, file_change, lock_acquired, lock_released, merge_completed, heartbeat
```

**POST /v1/sync/push** — push change, server validates then broadcasts

```
Request:  { agent, channel, file, digest, units_changed[], edge_deltas: {added[], removed[]} }
Response: { ok, seq, broadcast: bool, conflicts[] }
```

**DELETE /v1/sync/subscribe/{channel_id}** — unsubscribe

### MCP tool

- `kai_live_sync` — toggle live sync on/off, optionally filter to specific files

---

## Implementation Sequence

### Step 0: Wire kai-core/merge into workspace ops
- Connect `kai-core/merge/merge.go` to `workspace/cherrypick.go`
- Replace file-level conflict detection with unit-level semantic merge
- Implement non-fast-forward path in `workspace/integrate.go`
- **Files:** `kai-cli/internal/workspace/cherrypick.go`, `integrate.go`

### Step 1: edge_log table + seq field
- Add `edge_log` table to server schema
- Update `POST /v1/edges/incremental` handler to write to edge_log and return seq
- Old clients ignore the new field
- **Files:** server schema migration, `kai-server/kailab/api/routes.go`

### Step 2: /v1/edges/sync endpoint
- Read-only endpoint, no client changes needed yet
- **Files:** `kai-server/kailab/api/routes.go` (new handler)

### Step 3: Client sync + kai_sync tool
- Client stores `lastEdgeSeq`, periodically calls `/v1/edges/sync`
- Add `kai_sync` MCP tool
- **Files:** `kai-cli/internal/remote/client.go`, `mcp/server.go`, `watcher/watcher.go`

### Step 4: /v1/edges/validate endpoint
- Watcher calls validate before pushing edge deltas
- Conflicts surface in kai_activity
- **Files:** server handler + client watcher

### Step 5: Merge endpoints + MCP tools
- `POST /v1/merge/validate`, `POST /v1/merge/execute`
- `kai_merge_check` and `kai_merge` MCP tools
- `pending_changesets` table
- **Files:** server handlers, client methods, MCP tools

### Step 6: Live sync (Mode 2)
- SSE infrastructure, `sync_channels` table
- `kai_live_sync` MCP tool
- **Files:** server SSE broadcaster, client SSE reader, MCP tool

Each step is independently deployable. Old clients keep working at every step.

---

## How this replaces/supplements PRs

**Human path (unchanged):**
```
workspace create -> edit -> stage -> review open -> review approve -> integrate
```

**Agent path (Phase 3):**
```
edit -> watcher auto-pushes edges -> kai_merge_check -> kai_merge
```

- No explicit workspace creation (watcher tracks changes automatically)
- No explicit staging (edge deltas flow continuously)
- No human review (graph validation replaces it for agent-to-agent merges)
- Review still available for human oversight when needed
- `kai push` remains as escape hatch (full snapshot push)
