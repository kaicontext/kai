# Extension Points

This document identifies stable internal APIs and extension points where server features (in [kai-server](https://github.com/kaicontext/kai-server)) can be attached without refactoring the core engine.

## 1. Storage Backends

### Graph Store — `kai-core/store.Store`

The core graph storage interface is already abstract:

```go
type Store interface {
    NodeStore      // InsertNode, GetNode, GetNodesByKind, etc.
    EdgeStore      // InsertEdge, GetEdges, GetEdgesTo, etc.
    ObjectStore    // WriteObject, ReadObject (content-addressable)
    TransactionStore
    QueryStore
    Close() error
    ApplySchema(schemaPath string) error
}
```

**Current implementation:** SQLite (in kai-cli/internal/graph/)

**Server extension:** Remote graph store backed by a hosted database. The interface is stable — a remote implementation would wrap HTTP calls to a graph API service.

### File Source — `kai-cli/internal/filesource.FileSource`

```go
type FileSource interface {
    GetFiles() ([]*FileInfo, error)
    GetFile(path string) (*FileInfo, error)
    Identifier() string
    SourceType() string
}
```

**Current implementations:** Git repository, local directory

**Future extension:** Remote file source for hosted repos or artifact stores.

## 2. Telemetry and Event Stream

### Current: `kai-cli/internal/telemetry/`

Telemetry already captures run events with structured data:
- Command invoked
- Duration
- Snapshot/changeset stats
- Error information

**Extension point:** The telemetry collector can be extended to emit richer events:

```
KaiRunCompleted {
    command, duration, repo_id,
    files_analyzed, symbols_found, changes_detected,
    tests_selected, tests_total, reduction_pct
}
```

These events feed org-wide analytics dashboards. The CLI telemetry path stays opt-in and anonymous.

## 3. CI Plan Output

### Current output: JSON plan

The `kai ci plan` command outputs a deterministic JSON plan:

```json
{
    "strategy": "symbols",
    "safety_mode": "shadow",
    "selected_tests": [...],
    "skipped_tests": [...],
    "risk_level": "low",
    "confidence": 0.95
}
```

**Extension points:**
- **Schema versioning:** Add a `version` field to the plan output so cloud analytics can track plan format changes
- **Plan upload hook:** After plan generation, optionally POST the plan to a cloud endpoint for historical tracking
- **Risk scoring override:** Server can provide org-level risk scores that augment local analysis

## 4. Authentication and Identity

### Current: `kai-cli/internal/remote/`

Auth is handled via magic link → JWT flow, stored in `~/.kai/credentials.json`.

**Extension points:**
- `AuthProvider` interface for SSO/SAML/OIDC (enterprise)
- Org/repo identity model (currently only used for remote push/fetch)
- Token refresh can be extended for different auth backends

## 5. Module and Rule Configuration

### Current: `kai.modules.yaml` + `.kai/rules/`

Module rules and CI policies are file-based.

**Extension points:**
- **Remote policy fetch:** Pull org-level policies from a Kai server
- **Policy composition:** Merge local rules with org defaults
- **Rule versioning:** Track which policy version produced each CI plan

## 6. Review Workflow

### Current: Local reviews with optional AI summary

**Extension points:**
- **Review sync:** Push/pull reviews to hosted service
- **Review analytics:** Aggregate review quality metrics across org
- **Custom review rules:** Org-defined review checklists

## Stable Internal APIs

These APIs should remain backward-compatible to support server extension without breaking the core:

| API | Location | Stability |
|-----|----------|-----------|
| `store.Store` | kai-core/store/ | Stable |
| `FileSource` | kai-cli/internal/filesource/ | Stable |
| `graph.Node`, `graph.Edge` | kai-core/graph/ | Stable |
| `proto.SnapshotPayload` | kai-core/proto/ | Stable |
| CI plan JSON schema | kai-cli cmd output | Versioned |
| Telemetry event format | kai-cli/internal/telemetry/ | Versioned |

## Design Principle

Server features attach to these extension points via:
1. **Alternative implementations** of existing interfaces (e.g., remote Store)
2. **Post-hooks** on existing commands (e.g., upload plan after generation)
3. **Configuration overlays** (e.g., org policies merged with local)

The core engine never imports server code. Server code imports and wraps core interfaces.
