# Boundary Spec: OSS vs Proprietary Module Map

## Module Classification

### OSS (Apache 2.0) — This Repository

#### kai-core/ — Pure semantic engine
- `cas/` — Content-addressable storage (BLAKE3 hashing, canonical JSON)
- `detect/` — Change type detection (50+ semantic categories)
- `diff/` — Semantic diffing (unit-level diffs for code/JSON/YAML/SQL)
- `graph/` — Core graph types (Node, Edge, NodeKind, EdgeType)
- `intent/` — Intent generation (taxonomy, templates, clustering)
- `merge/` — AST-aware 3-way merging
- `modulematch/` — Module pattern matching
- `parse/` — Tree-sitter parsing (JS/TS, Python, Go, Ruby, Rust)
- `proto/` — Wire type definitions (SnapshotPayload, FilePayload, SymbolPayload)
- `store/` — Storage interface definitions (NodeStore, EdgeStore, ObjectStore)

**Constraints:** No HTTP clients. No cloud URLs. No authentication. No network I/O.

#### kai-cli/ — Local-first CLI
- `cmd/kai/` — CLI entry point (all commands)
- `internal/cache/` — File caching
- `internal/classify/` — Change classification
- `internal/diff/` — Changeset creation
- `internal/dirio/` — Directory I/O
- `internal/explain/` — Intent explanation
- `internal/filesource/` — File source abstraction (Git/filesystem)
- `internal/gitio/` — Git repository interaction
- `internal/graph/` — SQLite graph store (implements kai-core/store.Store)
- `internal/ignore/` — Path ignore patterns
- `internal/intent/` — Intent computation
- `internal/module/` — Module matching
- `internal/parse/` — Parser wrapper
- `internal/ref/` — Named reference management
- `internal/remote/` — HTTP client for remote servers
- `internal/review/` — Review creation and management
- `internal/signing/` — SSH signing
- `internal/snapshot/` — Snapshot creation
- `internal/status/` — Workspace status
- `internal/workspace/` — Workspace operations

**Constraints:** All core commands work offline. Remote features are opt-in.

### Proprietary — Separate Repository (kai-server)

All server infrastructure lives in a separate private repository:

- **kailab/** — Data plane (Git protocol, object storage, SSH server)
- **kailab-control/** — Control plane (auth, orgs, repos, CI runner, web UI)
- **deploy/** — Kubernetes, CloudBuild, Cloudflare configs

| Feature | Why it's separate |
|---------|------------------|
| Data plane server | Multi-tenant infrastructure |
| Control plane (auth, orgs) | SaaS-specific |
| CI runner (Kubernetes) | Managed compute |
| Hosted multi-repo graph index | Multi-tenant persistent storage |
| Org-wide analytics dashboards | Aggregated org data |
| Enterprise RBAC/SSO/audit | Multi-tenant auth compliance |

## Boundary Enforcement

### Structural enforcement

1. **Server code is not in this repository** — kailab, kailab-control, deploy are in a separate private repo
2. **kai-core has zero network dependencies** — go.mod contains only tree-sitter, BLAKE3, doublestar, yaml
3. **Cloud URLs are configurable** — `KAI_SERVER` env var or `kai remote set`
4. **Telemetry is opt-in** — disabled by default in CI, controlled via `KAI_TELEMETRY`

### CI enforcement

`scripts/check-core-purity.sh` runs in CI and fails if:
- Server/cloud directories exist in this repo
- `net/http` appears in kai-core imports
- Cloud SDK dependencies appear in kai-core/go.mod
- Cloud URLs are hardcoded in kai-core
- Proprietary concepts (tenant, org_id, sso, rbac, billing) appear in kai-core
