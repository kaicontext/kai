# Session Status

Last updated: 2026-03-18 (v0.9.11)

## What's Working

### Infrastructure
- **Postgres data plane** — migrated from SQLite+PVC to shared Postgres (`db-custom-1-3840`, 200 max connections)
- **GCS blob storage** — segments stored inline in Postgres + GCS (`preplanai-kailab-blobs`), range reads for fast file access. Inline is safety net; GCS is best-effort.
- **Zero-downtime deploys** — 2 replicas with RollingUpdate (maxUnavailable=0), health probes
- **GitLab CI/CD** — push to `gitlab.com:rite-day/kaylayerhq/kai-server` triggers test → build → deploy pipeline
- **Cache headers** — `no-cache` on HTML (prevents stale JS chunk 404s after deploy), `immutable` on hashed assets

### CLI (v0.9.11)
- `kai capture -m "message"` — attach message to snapshot, shown as CI run headline
- `kai push` — sends git HEAD commit message via `X-Kailab-Message` header
- `kai push --force` — skips negotiate, re-sends all objects (data recovery)
- `kai review open/approve/close/comment` — full review lifecycle
- `kai fetch --review <id>` — syncs review + comments from server to local
- `kai ci runs/run/logs/cancel/rerun` — remote CI management
- `kai ws create/checkout/list` — workspace management
- `kai mcp serve` — MCP server for AI coding assistants (12 tools)

### Web UI (kailayer.com)
- **File view** — IDE-style split panel (tree + content), file search/filter, type-specific icons, language breakdown bar, keyboard navigation, breadcrumb, doc tabs (README/CONTRIBUTING/LICENSE/SECURITY)
- **CI runs** — commit messages as headlines, SSE live updates, auto-scroll logs, 30min default timeouts, pod GC for stale jobs
- **Reviews** — create/view/approve/merge/abandon, inline line commenting, semantic + line diff toggle, threaded comments, relative timestamps
- **Header** — logo mark, refined spacing, soft shadow, desaturated avatar
- **History** — batched changeset fetches (5 concurrent, max 20)
- **README links** — SPA navigation for internal links

### Email Notifications (Postmark)
- CI pipeline results → snapshot author
- Review comments → review author
- Review state changes (approved, merged, abandoned, changes requested)
- @mention notifications
- Org invitation/removal

### Review System (end-to-end)
- CLI: `kai review open` → creates review targeting changeset
- CLI: `kai push` → sends review + changeset to server
- Web: view review, see semantic diff, add inline comments
- CLI: `kai fetch --review` → syncs comments back to local
- Web: approve/merge → updates `snap.main` to changeset head
- Email: notifications on all state changes

## Known Issues

### Edge Accumulation (partially fixed)
- **Root cause**: SQLite treats NULL as unique in PRIMARY KEY, so `INSERT OR IGNORE` on `(src, type, dst, at)` with `at=NULL` never ignored duplicates
- **Fix applied**: Changed PK to `(src, type, dst)`, auto-migration deduplicates on first run, `DISTINCT` on queries
- **Status**: HAS_FILE edges stable at 197. DEFINES_IN/IMPORTS edges still accumulate on re-capture because `AnalyzeSymbols` creates new edges each time. The `skipAnalysis` optimization helps when snapshot ID is unchanged.
- **Files**: `kai-cli/internal/graph/graph.go`, `kai-cli/schema/0001_init.sql`, `kai-cli/internal/snapshot/snapshot.go`

### Capture Performance
- Full capture (197 files): ~4 seconds (first run), ~7 seconds (subsequent due to symbol edge accumulation)
- `skipAnalysis` triggers when snapshot ID is byte-identical to `snap.latest` (no file changes)
- Incremental per-file analysis not yet implemented — all files re-parsed even when only one changed

### Push Performance
- Sends ALL edges every push (~20K), not just new ones
- Edge negotiate/dedup not implemented on the push protocol
- Segments accumulate in Postgres (408MB for kai repo across 28 segments)

### CI
- Stale job pods can exhaust namespace quota — mitigated by GC (deletes pods >30min old every 5min)
- Runner image built separately from main pipeline (Dockerfile.runner)

## Architecture

```
kai-cli (local)
  ├── .kai/db.sqlite     — local graph (nodes, edges, refs)
  ├── .kai/objects/       — content-addressed file blobs
  └── .kai/message        — capture message (consumed by push)

kai push → kailab (data plane)
  ├── Postgres            — segments, objects, refs, edges, ref_history
  ├── GCS                 — segment blobs (range reads)
  └── notifyPushCI →

kailab-control (control plane)
  ├── Postgres (shared)   — users, orgs, repos, workflows, CI runs
  ├── SvelteKit frontend  — embedded in Go binary
  ├── Postmark            — email notifications
  └── kailab-runner (CI)  — K8s pods per job, 3 runner replicas
```

## Deployment

### kai-server (GitLab)
```bash
git push  # triggers: test → build → deploy
```

### kai CLI (GitHub)
```bash
# Build and install locally
cd kai-cli && go build -o kai-darwin-arm64 ./cmd/kai
cp kai-darwin-arm64 ~/go/bin/kai

# Push to kai (captures + pushes semantic graph)
kai capture -m "message" && kai push
```

### Manual operations
```bash
# Check CI pods
kubectl get pods -n kailab-ci

# Clean stale CI pods
kubectl delete pod -n kailab-ci -l app=kailab-ci --field-selector=status.phase!=Running

# Check Postgres connections
kubectl exec -n kailab deployment/kailab -- sh -c 'apk add --no-cache postgresql-client > /dev/null 2>&1; PGPASSWORD="..." psql "postgres://..." -c "SELECT count(*) FROM pg_stat_activity;"'

# GCS bucket
gsutil ls -lh gs://preplanai-kailab-blobs/
```
