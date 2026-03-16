# Kai Remote Protocol Specification

Version: 1.0-draft
Status: Draft

## Overview

The Kai Remote Protocol defines the HTTP API that a Kai-compatible remote server must implement. Any server implementing this protocol can be used as a remote for the `kai` CLI via `kai remote set origin <url>`.

The protocol has three layers:

1. **Core Protocol** — object storage, refs, snapshots (required)
2. **Management Protocol** — orgs, repos, auth, webhooks (required for multi-user)
3. **CI Protocol** — workflows, runs, jobs, secrets (optional)

A minimal implementation needs only the Core Protocol. The Management and CI protocols are optional extensions.

---

## 1. Core Protocol

The Core Protocol handles content-addressable object storage, ref management, and file access. This is the minimum required for `kai push` and `kai pull` to work.

### Base URL

All Core Protocol endpoints are scoped to a repository:

```
{server}/{tenant}/{repo}/v1/...
```

Where `{tenant}` is the organization/namespace and `{repo}` is the repository name.

### 1.1 Health

```
GET /health
```

Response:
```json
{
  "status": "ok",
  "version": "0.9.9"
}
```

### 1.2 Push Negotiate

Determine which objects the server is missing before pushing.

```
POST /{tenant}/{repo}/v1/push/negotiate
```

Request:
```json
{
  "digests": ["<hex>", ...]
}
```

Response:
```json
{
  "missing": ["<hex>", ...]
}
```

### 1.3 Object Pack Ingest

Push objects to the server as a compressed pack.

```
POST /{tenant}/{repo}/v1/objects/pack
Content-Type: application/octet-stream
X-Kailab-Actor: user@example.com
```

**Pack Format:**
1. 4-byte big-endian length prefix (header size)
2. JSON header describing objects
3. Raw object data (concatenated)
4. Entire payload zstd-compressed

Header:
```json
{
  "objects": [
    {
      "digest": "<32-byte hex>",
      "kind": "Snapshot|File|ChangeSet|Blob|...",
      "offset": 0,
      "length": 1234
    }
  ]
}
```

Response:
```json
{
  "segmentId": 42,
  "indexedCount": 15
}
```

### 1.4 Object Retrieval

```
GET /{tenant}/{repo}/v1/objects/{digest}
```

Response:
```json
{
  "kind": "Snapshot",
  "digest": "<hex>",
  "payload": { ... }
}
```

### 1.5 Refs

#### List Refs

```
GET /{tenant}/{repo}/v1/refs
GET /{tenant}/{repo}/v1/refs?prefix=snap.
```

Response:
```json
{
  "refs": [
    {
      "name": "snap.latest",
      "target": "<base64-encoded bytes>",
      "updatedAt": 1773500000000,
      "actor": "user@example.com"
    }
  ]
}
```

#### Get Ref

```
GET /{tenant}/{repo}/v1/refs/{name}
```

Response: same as single ref entry above.

#### Update Ref

```
PUT /{tenant}/{repo}/v1/refs/{name}
X-Kailab-Actor: user@example.com
```

Request:
```json
{
  "old": "<hex or null for create>",
  "new": "<hex>",
  "force": false
}
```

Response:
```json
{
  "ok": true,
  "updatedAt": 1773500000000,
  "pushId": "abc123",
  "error": null
}
```

On conflict (409):
```json
{
  "ok": false,
  "error": "ref mismatch (not fast-forward)"
}
```

#### Batch Update Refs

Atomic multi-ref update in a single transaction.

```
POST /{tenant}/{repo}/v1/refs/batch
X-Kailab-Actor: user@example.com
```

Request:
```json
{
  "updates": [
    {"name": "snap.latest", "old": "<hex>", "new": "<hex>", "force": false}
  ]
}
```

Response:
```json
{
  "pushId": "abc123",
  "results": [
    {"name": "snap.latest", "ok": true, "updatedAt": 1773500000000}
  ]
}
```

### 1.6 Files

#### List Snapshot Files

```
GET /{tenant}/{repo}/v1/files/{ref-or-digest}
GET /{tenant}/{repo}/v1/files/{ref-or-digest}?path=src/main.go
```

Response:
```json
{
  "snapshotDigest": "<hex>",
  "files": [
    {
      "path": "src/main.go",
      "digest": "<hex>",
      "contentDigest": "<hex>",
      "lang": "go"
    }
  ]
}
```

#### Get File Content

```
GET /{tenant}/{repo}/v1/content/{digest}
```

Response:
```json
{
  "path": "src/main.go",
  "digest": "<hex>",
  "content": "<base64-encoded>",
  "lang": "go"
}
```

#### Get Raw Content

```
GET /{tenant}/{repo}/v1/raw/{digest}
```

Response: raw binary file content with appropriate `Content-Type`.

### 1.7 Edges

Push semantic graph edges (imports, calls, tests).

```
POST /{tenant}/{repo}/v1/edges
```

Request:
```json
{
  "edges": [
    {"src": "<hex>", "type": "IMPORTS", "dst": "<hex>", "at": "<hex>"}
  ]
}
```

Response:
```json
{
  "inserted": 42
}
```

**Edge Types:**
- `IMPORTS` — file imports another file
- `CALLS` — symbol calls another symbol
- `TESTS` — test file covers a source file
- `HAS_FILE` — snapshot contains file
- `DEFINES_IN` — file defines symbol
- `REVIEW_OF` — review targets changeset
- `HAS_COMMENT` — review has comment
- `ANCHORS_TO` — comment anchors to file/symbol

### 1.8 Diff

```
GET /{tenant}/{repo}/v1/diff/{base}/{head}
GET /{tenant}/{repo}/v1/diff/{base}/{head}?path=src/main.go
```

Response:
```json
{
  "base": "<hex>",
  "head": "<hex>",
  "files": [
    {"path": "src/main.go", "status": "modified", "hunks": [...]}
  ]
}
```

### 1.9 Log

```
GET /{tenant}/{repo}/v1/log/head
GET /{tenant}/{repo}/v1/log/entries?ref=snap.latest&after=0&limit=50
```

Response:
```json
{
  "entries": [
    {
      "kind": "REF_UPDATE",
      "time": 1773500000000,
      "actor": "user@example.com",
      "ref": "snap.latest",
      "old": "<hex>",
      "new": "<hex>"
    }
  ]
}
```

### 1.10 Reviews

#### List Reviews
```
GET /{tenant}/{repo}/v1/reviews
```

#### Create Review
```
POST /{tenant}/{repo}/v1/reviews
```

Request:
```json
{
  "title": "My changes",
  "description": "Optional",
  "targetId": "<hex>",
  "targetKind": "ChangeSet",
  "reviewers": ["alice"],
  "assignees": ["bob"]
}
```

#### Update Review State
```
POST /{tenant}/{repo}/v1/reviews/{id}/state
```

Request:
```json
{
  "state": "approved|changes_requested|merged|abandoned",
  "actor": "alice",
  "summary": "Optional summary for changes_requested"
}
```

**Valid State Transitions:**
```
draft     → open, abandoned
open      → approved, changes_requested, merged, abandoned
approved  → merged, changes_requested, abandoned
changes_requested → open, approved, abandoned
merged    → (terminal)
abandoned → (terminal)
```

#### Comments
```
GET  /{tenant}/{repo}/v1/reviews/{id}/comments
POST /{tenant}/{repo}/v1/reviews/{id}/comments
```

Comment request:
```json
{
  "body": "Comment text",
  "filePath": "src/main.go",
  "line": 42,
  "parentId": "optional-for-replies"
}
```

### 1.11 Changesets

```
GET   /{tenant}/{repo}/v1/changesets/{id}
PATCH /{tenant}/{repo}/v1/changesets/{id}
GET   /{tenant}/{repo}/v1/changesets/{id}/affected-tests
```

---

## 2. Management Protocol

The Management Protocol handles authentication, organizations, repositories, and user management. Required for multi-user servers.

All Management Protocol endpoints use the `/api/v1/` prefix.

### 2.1 Authentication

#### Magic Link Login
```
POST /api/v1/auth/magic-link
```

Request:
```json
{"email": "user@example.com"}
```

#### Token Exchange
```
POST /api/v1/auth/token
```

Request:
```json
{"code": "<magic-link-code>"}
```

Response:
```json
{
  "accessToken": "<jwt>",
  "refreshToken": "<jwt>",
  "expiresIn": 900
}
```

#### Token Refresh
```
POST /api/v1/auth/refresh
```

Request:
```json
{"refreshToken": "<jwt>"}
```

#### Current User
```
GET /api/v1/me
Authorization: Bearer <token>
```

### 2.2 Organizations

```
GET    /api/v1/orgs                         # list user's orgs
POST   /api/v1/orgs                         # create org
GET    /api/v1/orgs/{org}                   # get org info
GET    /api/v1/orgs/{org}/members           # list members
POST   /api/v1/orgs/{org}/members           # add member
DELETE /api/v1/orgs/{org}/members/{user_id} # remove member
```

**Roles:** `reporter`, `developer`, `maintainer`, `admin`

### 2.3 Repositories

```
GET    /api/v1/orgs/{org}/repos             # list repos
POST   /api/v1/orgs/{org}/repos             # create repo
GET    /api/v1/orgs/{org}/repos/{repo}      # get repo info
PATCH  /api/v1/orgs/{org}/repos/{repo}      # update repo
DELETE /api/v1/orgs/{org}/repos/{repo}      # delete repo
```

### 2.4 API Tokens

```
GET    /api/v1/tokens       # list tokens (masked)
POST   /api/v1/tokens       # create token
DELETE /api/v1/tokens/{id}  # delete token
```

### 2.5 SSH Keys

```
GET    /api/v1/me/ssh-keys       # list keys
POST   /api/v1/me/ssh-keys       # add key
DELETE /api/v1/me/ssh-keys/{id}  # remove key
```

### 2.6 Webhooks

```
GET    /api/v1/orgs/{org}/repos/{repo}/webhooks                           # list
POST   /api/v1/orgs/{org}/repos/{repo}/webhooks                           # create
PATCH  /api/v1/orgs/{org}/repos/{repo}/webhooks/{id}                      # update
DELETE /api/v1/orgs/{org}/repos/{repo}/webhooks/{id}                      # delete
GET    /api/v1/orgs/{org}/repos/{repo}/webhooks/{id}/deliveries           # delivery log
```

---

## 3. CI Protocol

The CI Protocol handles workflow execution, job management, and secrets. Optional — servers that don't support CI simply omit these endpoints.

### 3.1 Workflow Discovery

Workflows are YAML files stored at `.kailab/workflows/*.yml` in the repository snapshot. The server discovers and parses them on push.

**Workflow format** follows GitHub Actions syntax:

```yaml
name: CI
on:
  push:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    container:
      image: golang:1.24-alpine
    steps:
      - uses: actions/checkout@v4
      - name: Run tests
        run: go test ./...
```

### 3.2 Workflows

```
GET  /api/v1/orgs/{org}/repos/{repo}/workflows                              # list
POST /api/v1/orgs/{org}/repos/{repo}/workflows/sync                         # sync from snapshot
POST /api/v1/orgs/{org}/repos/{repo}/workflows/discover                     # discover + sync
POST /api/v1/orgs/{org}/repos/{repo}/workflows/{workflow_id}/dispatch       # manual trigger
```

### 3.3 Workflow Runs

```
GET  /api/v1/orgs/{org}/repos/{repo}/runs                    # list (paginated)
GET  /api/v1/orgs/{org}/repos/{repo}/runs?page=2&limit=20    # pagination
GET  /api/v1/orgs/{org}/repos/{repo}/runs/events              # SSE stream
GET  /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}           # get run
POST /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}/cancel    # cancel
POST /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}/rerun     # re-run
```

**Run Statuses:** `queued`, `in_progress`, `completed`
**Conclusions:** `success`, `failure`, `cancelled`

#### SSE Events

```
GET /api/v1/orgs/{org}/repos/{repo}/runs/events
Accept: text/event-stream
```

Events:
```
event: runs
data: {"runs": [...]}
```

Server pushes updates when run state changes. Connection kept alive with 2-second internal poll.

### 3.4 Jobs

```
GET /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}/jobs                    # list jobs
GET /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}/jobs/{job_id}/logs      # get logs
```

Job response includes steps:
```json
{
  "id": "...",
  "name": "test",
  "status": "completed",
  "conclusion": "success",
  "exit_code": 0,
  "steps": [
    {
      "name": "actions/checkout@v4",
      "status": "completed",
      "conclusion": "success",
      "exit_code": 0
    }
  ]
}
```

### 3.5 Artifacts

```
GET /api/v1/orgs/{org}/repos/{repo}/runs/{run_id}/artifacts    # list artifacts
```

### 3.6 Secrets

Secrets are encrypted at rest and injected as environment variables in CI job pods.

```
GET    /api/v1/orgs/{org}/repos/{repo}/secrets                 # list (names only)
PUT    /api/v1/orgs/{org}/repos/{repo}/secrets/{name}          # set
DELETE /api/v1/orgs/{org}/repos/{repo}/secrets/{name}          # delete
```

### 3.7 Variables

Variables are plaintext configuration values available in CI jobs.

```
GET    /api/v1/orgs/{org}/repos/{repo}/variables               # list
PUT    /api/v1/orgs/{org}/repos/{repo}/variables/{name}        # set
DELETE /api/v1/orgs/{org}/repos/{repo}/variables/{name}        # delete
```

---

## Content-Addressable Storage Model

All objects in Kai are content-addressed using BLAKE3:

```
nodeID = blake3(kind + "\n" + canonicalJSON(payload))
```

### Node Kinds

| Kind | Description |
|---|---|
| `Snapshot` | Immutable point-in-time capture of a directory |
| `File` | File metadata (path, content digest, language) |
| `Symbol` | Code symbol (function, class, method) |
| `ChangeSet` | Diff between two snapshots |
| `Review` | Code review (mutable, UUID-based) |
| `ReviewComment` | Review comment (mutable, UUID-based) |
| `Workspace` | Mutable branch-like overlay (UUID-based) |
| `Module` | Logical grouping of files |
| `Blob` | Raw file content |

### Ref Naming Convention

| Pattern | Purpose |
|---|---|
| `snap.latest` | Most recent snapshot |
| `snap.main` | Main branch snapshot |
| `snap.YYYYMMDDTHHMMSS.mmm` | Historical timestamped snapshot |
| `cs.latest` | Most recent changeset |
| `ws.<name>` | Workspace |
| `review.<id>` | Review |

---

## Authentication

Requests to Core Protocol endpoints use Bearer token authentication:

```
Authorization: Bearer <token>
```

The token is obtained via the Management Protocol's auth endpoints. Servers may also support API tokens created via `/api/v1/tokens`.

The `X-Kailab-Actor` header identifies the user performing write operations (push, ref updates) for audit trail purposes.

---

## Error Responses

All errors follow a standard format:

```json
{
  "error": "human-readable message",
  "code": "OPTIONAL_ERROR_CODE"
}
```

Standard HTTP status codes:
- `400` — bad request
- `401` — authentication required
- `403` — forbidden (insufficient permissions)
- `404` — not found
- `409` — conflict (ref mismatch)
- `500` — internal server error

---

## Implementation Notes

### Minimum Viable Implementation

A minimal Kai remote needs:
1. Object storage (pack ingest + retrieval)
2. Ref management (list, get, update)
3. File listing and content retrieval
4. Health endpoint

This is sufficient for `kai push`, `kai pull`, `kai clone`, and `kai diff` to work.

### Optional Capabilities

Servers can advertise capabilities via the health endpoint:

```json
{
  "status": "ok",
  "version": "1.0.0",
  "capabilities": ["core", "management", "ci", "reviews", "webhooks"]
}
```

The CLI uses this to determine which commands are available for a given remote.
