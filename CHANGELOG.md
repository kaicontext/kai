# Changelog

All notable changes to Kai are documented here.

## [0.9.11] — 2026-03-18

### CLI
- **`kai capture -m`** — attach a message to a snapshot, shown as the CI run headline on push
- **`kai fetch --review`** — syncs review comments from the server to the local CLI
- **Push sends git commit message** — `kai push` includes the latest git HEAD message via `X-Kailab-Message` header, used as CI trigger message. Falls back to changeset intent.
- **Review fetch handles duplicates** — re-fetching an existing review syncs comments without erroring

### Reviews (kailayer.com)
- **Comments fixed** — review comments now work end-to-end (SQLite→Postgres migration: repo_id scoping, placeholder syntax, NOT NULL edge constraint)
- **Review page UX** — relative timestamps ("2h ago"), singular/plural grammar fix ("1 file changed"), Merge/Abandon buttons separated with confirmation dialogs, clearer Semantic/Lines toggle active state
- **GetObject API fix** — returns raw content with `X-Kailab-Kind` header for CLI compatibility

### CI
- **Commit messages as run headlines** — CI runs show the git commit message or `kai capture -m` message instead of generic "CI"
- **30-minute default timeouts** — job and step timeouts reduced from 6 hours to 30 minutes (overridable via `timeout-minutes` in workflow YAML)
- **Checkout reliability** — HTTP status checks, 3x retry with backoff, concurrency reduced from 20 to 10 parallel downloads
- **SSE fixes** — fixed `/events` 500 (Flusher passthrough on response wrapper), EventSource cleanup on tab navigation
- **Auto-scroll logs** — log viewer scrolls to bottom on new output

### File View (kailayer.com)
- **File search** — fuzzy filter above the tree with auto-expand on matching directories
- **Type-specific icons** — Go, Markdown, YAML/JSON, Shell files get distinct icons
- **IDE layout** — fixed-height container with independent panel scrolling (tree + content)
- **Better indentation** — 20px per nesting level
- **Loading fix** — no more flash of "No files in this snapshot" while loading

### Header (kailayer.com)
- **Logo mark** — favicon icon next to "Kai" wordmark
- **Refined spacing** — smaller wordmark (18px), consistent 24px nav gaps, `text-sm` nav items
- **Soft shadow** — `box-shadow` instead of hard 1px border
- **Desaturated avatar** — muted gray tint instead of saturated blue

### Infrastructure
- **GCS blob storage** — segments stored inline in Postgres + GCS with range reads for fast file access. Always stores inline as safety net; GCS write is best-effort.
- **Postgres upgraded** — `db-custom-1-3840` (1 vCPU, 3.75GB RAM), max connections raised to 200
- **Connection pool fix** — `SetMaxOpenConns(10)` on both data plane and control plane to prevent pool exhaustion

### Other
- **README links** — SPA navigation for internal links in rendered markdown
- **`kai push --force`** — skips negotiate for data recovery (re-sends all objects)

## [0.9.10] — 2026-03-16

### CLI
- **`kai query` command group** — query the semantic graph directly from the terminal:
  - `kai query callers <symbol>` — find all call sites with file:line locations
  - `kai query dependents <file>` — find all files that import a given file
  - `kai query impact <file>` — transitive downstream impact analysis with hop distance, separating source files from tests
- **`kai analyze` summary output** — `kai analyze symbols` and `kai analyze calls` now print what they found (e.g., "Found 61 symbols across 11 files", "Found 36 imports, 50 calls, 16 test links")

## [0.9.9] — 2026-03-14

### MCP
- **`kai_files` MCP tool** — list files in a repo with language, module, and glob pattern filters
- **MCP call logging** — JSONL logging for measuring tool usage, gated on `KAI_MCP_LOG=1`. Captures tool name, params, duration, extracted file/symbol references per session
- **SER analysis script** — `scripts/analyze-mcp-log.py` computes Structured Exploration Ratio with A/B comparison mode

### Review System
- **`kai review edit`** — update title, description, and assignees after creation
- **`kai review comment`** — add comments with `--file` and `--line` anchoring
- **`kai review comments`** — list all comments on a review
- **Review model alignment** — CLI and server now share the same data model: assignees, comment threading (parentId), changesRequestedSummary/By, targetBranch
- **Review state validation** — state machine enforcement on both CLI and server (draft→open→approved/changes_requested→merged/abandoned)
- **Review summary persistence** — `kai review summary` stores structured summary in the review payload, accessible via web UI
- **Language-aware API surface detection** — Go (uppercase), Python (no `_` prefix), Ruby (all public), Rust (uppercase types), JS/TS (top-level functions/classes)
- **Module-based file categorization** — review summaries load modules from `.kai/rules/modules.yaml` for meaningful grouping
- **Unified diff in reviews** — `kai review view` shows proper unified diffs

### Capture & Push
- **Quiet output** — one-line summary by default (`Captured abc123 (191 files, 20 modified)`), inline progress counters, full detail with `-v`
- **Snapshot history** — each capture preserves the previous snapshot as `snap.YYYYMMDDTHHMMSS.mmm`, browsable in the web UI and CLI
- **`kai snapshot list`** — now shows ref names alongside IDs

### Snapshots & Refs
- **`@snap:` ref resolution** — `@snap:snap.20260314T090755.729` and `@snap:20260314T090755.729` both work
- **`kai diff` with historical snapshots** — `kai diff snap.20260314T085932 snap.latest --semantic`

### kailayer.com
- **Web review creation** — "New Review" button on Reviews tab with changeset selector, title, and description fields
- **Raw endpoint fix** — serves `text/plain` with `nosniff` header so HTML source is displayed, not rendered
- **Skeleton loaders** — all loading states show animated skeleton placeholders matching the content shape
- **File-first loading** — file content renders immediately while the file tree loads in the background
- **Consistent page padding** — all repo pages now use matching `px-5 py-8`
- **kai-core auto-sync** — CI pulls latest kai-core from OSS repo before every build, no more drift
- **State transition validation** — server enforces same state machine as CLI

### Other
- Removed dead kailab/kailab-control build jobs from OSS CI
- MCP registry token files gitignored
- Updated README and site for MCP registry launch

## [0.9.6] — 2026-03-09

### Features
- Add `mcpName` field (`io.github.kailayerhq/kai`) for MCP registry discovery (`3b0a92a`)

### Fixes
- Skip flaky `TestRunCompletion` in CI — was timing out after 10m (`a27caca`)
- Remove kailab/kailab-control test jobs from OSS CI (server code moved to private repo) (`428837a`)

## [0.9.5] — 2026-03-08

### Features
- **MCP registry readiness** — npm package (`kai-mcp`), postinstall binary download, `server.json` schema, CI publish-on-tag pipeline (`33ac01c`)
- **Per-project remote config** — remote URLs stored per `.kai/` directory to prevent cross-repo pushes (`882ff7b`)
- **`kai_status` and `kai_refresh` MCP tools** — check graph freshness (via git, not file hashing) and re-capture from within an AI assistant (`c84c01c`)
- **Lazy MCP initialization** — semantic graph only built on first tool call, not on server startup (`cf47473`)
- **Token-efficient MCP responses** — optimized output format across all tools to reduce context window usage (`684156e`)
- **Go and Python import resolution** — dependency graph edges now resolve actual imports, not just file co-occurrence (`7c0f1b4`)
- **`kai pull`** — fetch snapshots and content from a remote Kailab server (`7cf314b`)
- **MCP server** — expose Kai's semantic graph (symbols, callers, callees, dependencies, tests, impact, diff, context) to AI coding assistants via Model Context Protocol (`e46fa1c`)

### Fixes
- Fix Go `CALLS` edges and same-package `TESTS` edge resolution in MCP callers query (`73a20e5`)

### Other
- Rewrite README with infrastructure-first framing and add install script (`d5ae0cc`)
- Update GitLab CI example and changelog script (`f1fcc88`)

## [0.4.0] — 2026-03-06

### Features
- **Open-core split** — server code (`kailab/`, `kailab-control/`, `deploy/`) moved to private `kai-server` repo. This repo is now pure OSS (Apache 2.0): `kai-core/` + `kai-cli/` + `bench/` + `docs/` (`b3fd983`)
- **Open-core architecture** — licensing, benchmarks, CI, telemetry, and regression test infrastructure (`8d38b45`)
- **Diff-first CI fast path** — skip full snapshot when coverage map exists, use native git diff (`bff10ae`, `4edf5fc`)
- **Ruby and Python change detection** — detect layer now covers Ruby and Python in addition to Go, JS/TS, and Rust (`497605a`)
- **VitePress docs site** with automated changelog pipeline (`e693fc9`)

### Other
- Contribution review policy with scope, determinism, and boundary rules (`d5aa775`)
- Move CLI reference to `docs/cli-reference.md` (`82143be`)
- Simplify README to focus on what Kai does (`f5a8fe0`)

## [0.3.0] — 2026-02-11

### Features
- **CI system** — GitHub Actions-compatible workflow engine with matrix expansion, job dependencies, schedule triggers, and reusable workflows (`4deb404`, `9c97e0f`)
- **Workflow discovery** — automatic detection of workflow files in snapshots (`9919d44`)
- **Light/dark mode** — system preference detection with manual toggle (`ad669e3`)
- **Markdown code copy** — copy button on code blocks in README rendering (`ce1f8bc`)

### Fixes
- Fix CI push notification: map `snap.latest` → `refs/heads/main` so workflows actually trigger (`4d6475f`)
- Fix matrix include-only expansion and runner job matching (`b695ba3`)
- Fix job dependency resolution: map `needs` keys to display names (`6940b0f`)
- Fix `StringOrSlice` JSON serialization to always use arrays (`9f2defa`)
- Fix job label matching and resolve matrix expressions in job names (`8d5df20`)
- Fix nil pointer in workflow sync when workflow doesn't exist in DB (`08e9cc3`)
- Fix workflow sync to decode base64 content from data plane API (`d90befb`)
- Fix workflow discovery: use file object digest and add `snap.latest` fallback (`9919d44`)
- Fix git source to capture all file types including images (`b5f31ce`)
- Fix UTF-8 encoding in file content and add raw content endpoint for images (`d2d7c09`)
- Fix code viewer horizontal overflow on long lines (`dc68d11`)
- Fix repo page showing content for non-existent repos instead of error (`151a226`)
- Fix idempotent migration for job outputs columns on PostgreSQL (`618c718`)
- Rewrite `actionCheckout` to use Kai API instead of git clone (`9078e36`)
