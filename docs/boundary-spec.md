# Boundary Spec: OSS vs SaaS Module Map

## Module Classification

### OSS (Apache 2.0)

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
- `internal/remote/` — HTTP client for Kailab servers
- `internal/review/` — Review creation and management
- `internal/signing/` — SSH signing
- `internal/snapshot/` — Snapshot creation
- `internal/status/` — Workspace status
- `internal/workspace/` — Workspace operations

**Constraints:** All core commands work offline. Remote features are opt-in.

#### kailab/ — Self-hostable data plane
- `api/` — HTTP API (push, fetch, refs, objects, diffs)
- `cmd/kailabd/` — Server binary
- `data/` — On-disk repository storage
- `pack/` — Pack file handling
- `repo/` — Repository management
- `sshserver/` — SSH Git protocol handler
- `store/` — Storage implementation

### SaaS (Proprietary — Kai Cloud)

These features live in separate closed-source infrastructure:

| Feature | Why closed |
|---------|-----------|
| Hosted multi-repo graph index | Multi-tenant persistent storage |
| Cross-branch artifact reuse | Shared cache across users/branches |
| Org-wide analytics dashboards | Aggregated org data |
| Risk scoring + policy engine | ML models trained on org history |
| Enterprise RBAC/SSO/audit | Multi-tenant auth compliance |

### Hybrid — kailab-control/

The control plane contains both OSS-compatible and cloud-specific code:

- **OSS-friendly:** Auth service, org/repo management, workflow parser, basic CI runner
- **Cloud-specific:** GCS storage backend, Kubernetes runner orchestration, shard routing

Future work: Extract cloud-specific backends behind interfaces so the control plane can run in a minimal self-hosted configuration without GCS or Kubernetes.

## Boundary Enforcement

### Current state (already clean)

1. **kai-core has zero network dependencies** — go.mod contains only tree-sitter, BLAKE3, doublestar, yaml
2. **kai-cli does not import kailab or kailab-control** — communicates via HTTP only
3. **Cloud URLs are configurable** — `KAI_SERVER` env var or `kai remote set`
4. **Telemetry is opt-in** — disabled by default in CI, controlled via `KAI_TELEMETRY`

### CI enforcement (planned)

- Lint check: forbid `net/http` imports in kai-core
- Lint check: forbid kailab/kailab-control imports in kai-cli Go packages
- Lint check: forbid hardcoded cloud URLs in kai-core
