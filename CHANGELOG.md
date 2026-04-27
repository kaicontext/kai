# Changelog

All notable changes to Kai are documented here.

## [0.13.2] — 2026-04-27

### CLI — `kai ui` multi-repo display
- **Per-card source repo** — each agent card now shows its source repo (e.g. `claude-1 · kaicontext/kai`) so a dashboard with agents spawned from multiple repos is legible at a glance.
- **Smart header** — replaced the cwd-derived `repo · branch` label with a registry-summary: `N agents · <repo>` (single repo), `N agents across M repos` (multiple), or `no spawned agents` (empty). The dashboard is global across the machine; the cwd was misleading when agents came from different sources.
- **`/api/header`** schema change: returns `{agent_count, repo_count, repos, sole_repo}` instead of `{repo, branch}`. `/api/agents` adds a `source_repo` field per entry.
- **Surface checkpoints in the activity sparkline + event feed** (was 0.13.1 follow-up): the dashboard reflects local AI authorship even when no peer sync events have fired, fixing the "everything stays empty" UX in single-agent or sync-off demos.

## [0.13.1] — 2026-04-26

### CLI — `kai ui`
- **`kai ui`** opens a local dashboard in your default browser. Localhost-only HTTP server (`127.0.0.1`, random free port unless `--port` is set). Shows live status of every spawned workspace (agent name, last-checkpointed file, checkpoint count, uptime, 5-minute sync-event sparkline) and a real-time strip of recent sync events across all workspaces. Single-page vanilla-JS UI embedded in the binary; no Wails, no Electron, no extra install. Exit with Ctrl+C.

## [0.13.0] — 2026-04-26

### CLI — `kai spawn` / `kai despawn` / `kai spawn list`
- **`kai spawn`** stands up N disposable, sync-connected workspaces from a snapshot. Workspace 1 is materialized via `kai checkout` from the object store; workspaces 2..N are CoW-cloned (APFS clone on macOS, reflink on btrfs/xfs, fallback to `cp -R`) from workspace 1, with their cloned graph DBs rewritten in place to give each a fresh workspace ID, name, and agent name.
- **`kai despawn`** refuses workspaces with unpushed checkpoints unless `--force`; pushes first if a remote is configured. Removes the dir, drops the registry entry, optionally runs `kai prune`.
- **`kai spawn list`** reads the spawn registry at `~/.kai/spawned.json` (each spawned dir is its own independently-init'd repo, so there's no central `.kai/` to query).
- **`kai push` writes a `.kai/last-push` marker** the despawn safety gate reads to decide whether the workspace has unpushed snapshots.
- **Workspace metadata gains an `AgentName` field.** `kai checkpoint` falls back to it when neither `--agent` flag nor `KAI_CHECKPOINT_AGENT` env is set, so agents pre-registered at spawn time get correct attribution without threading `--agent` through every hook.
- spawn/despawn/spawn list CLI (`8c38f4e`)
- 0.13.0 release (`133989f`)

## [0.12.5] — 2026-04-21

### CLI
- **MCP single-instance guard** prevents two `kai mcp serve` processes from racing for the same `.kai/`.
- **`kai org list`** and **`kai org delete`** subcommands for managing organizations on the remote server (`bca2d59`).

### Docs
- **`docs/layout-livesync.sh`** plus a TL;DR run order for the 4-agent live-sync demo (`f58b7a6`).
- 0.12.5 release (`bdadb79`)

## [0.12.4] — 2026-04-21

### CLI
- **Fix `kai clone <org>/<repo>` shorthand** — parser now correctly resolves the shorthand against the default kaicontext server (`6bac4f7`, `45348b6`).

### Docs
- **`docs/setup-livesync.sh`** — extracted setup script that doesn't kill your terminal (`6183434`, `2c5c4fa`).
- **4-agent live-sync demo script** with visible-change staging (`3ba26bd`).
- **Sync-feed command fix** — replace nonexistent `kai activity --follow` with the actual JSONL tail (`c94ce6b`).
- **90-second demo script** committed to unbreak the docs-site build (`1c0f771`).
- **`push` usage messaging reframed** from "commits" to "agent sync events" (`2e2e6d2`).

### CI
- **Removed `.kailab/workflows/ci.yml`** — GitHub Actions runs the real CI (`8559a9d`).

## [0.12.3] — 2026-04-20

### CLI — Semantic diff polish
- **Signature and value changes render as red/green pairs** in `kai diff`, matching the voiceover 30-second demo (`820348c`).
- **`docs/demo-30s.md`** adds `kai intent` as the third beat of the demo (`ff86358`).

## [0.12.2] — 2026-04-20

### CLI
- **Colorized `kai diff` output** in a TTY (`9eb3c80`, `0cf2406`).

### Docs
- **Demo script setup fix** — Scene 5 output now matches what actually runs; setup block lets Bob actually receive Alice's initial commit (`a81669f`, `b601b11`).

## [0.12.1] — 2026-04-20

### CLI — Telemetry overhaul
- **Telemetry default-on (opt-out)** with a one-time first-run notice (`d4b04e7`).
- **Events ship to PostHog** instead of a self-hosted endpoint (`a13f669`).
- **Bridge import on `git pull` / `git checkout`**, not just direct commits (`30ca8ec`).
- 0.12.1 release (`e4edf6c`)

## [0.12.0] — 2026-04-20

### CLI — kai↔git bridge end-to-end
- **`kai init --git-bridge`** installs a `post-commit` hook so git commits authored outside kai are imported as kai snapshots via `kai bridge import` (`e3665da`).
- **Milestone checkpoints become git commits** with `Kai-*` trailers via `kai bridge milestone` (`8822e46`).
- **End-to-end bridge wiring** (`12881a7`).
- **Smoke-init hook version assertions updated to v3** to keep CI green (`2126e41`).

## [0.11.7] — 2026-04-19

### CLI
- **`kai clone --kai-only`** clones from kai only, skipping git; materializes files from the latest snapshot on the remote.
- **`kai doctor --fix`** now installs missing kai-managed hooks in addition to upgrading stale ones.
- **Live-sync line-merge fallback** for `json` / `yaml` / `md` (file types where AST merge isn't available) (`4bbc329`).

## [0.11.6] — 2026-04-19

### CLI
- **`kai telemetry flush`** force-uploads spooled telemetry events, bypassing the 24-hour rate limit.
- **Fix `bufio.Scanner` aliasing bug** in the telemetry spooler (`5cbf90c`).

## [0.11.5] — 2026-04-18

### CLI
- **Fully exclude framework-generated code** from snapshots (`.svelte-kit/generated`, `.next/cache`, etc.) so Next/Sveltekit/etc. projects don't capture build artifacts (`56afa9b`).

## [0.11.4] — 2026-04-18

### CLI
- **`kai push` sends ref metadata** (git info, file counts) to the server so the kaicontext history page can render it (`b2d912b`).

## [0.11.3] — 2026-04-18

### CLI — Cross-project authorship continued
- **PostToolUse hook now writes checkpoints into foreign `.kai/` projects** when an agent edits a file outside its session's project root (`edc1686`).

## [0.11.2] — 2026-04-17

### CLI — Cross-project authorship
- **Checkpoints route to a foreign `.kai/`** when the edited file is in another kai-init'd project, so AI authorship is captured even on cross-repo edits (`9427ac0`).

## [0.11.1] — 2026-04-17

### CI
- **Smoke-test contract fix** — `kai init` now prints `Created repo:` so the smoke assertion passes (`ada9bfd`).

## [0.11.0] — 2026-04-17

### Spec — v3
- **v3 spec:** session base, trust assertions, CI evidence, quiet init (`7c5942c`). Foundation for the upcoming trust-level model (`unverified` / `agent-claimed` / `CI-verified`).

## [0.10.5] — 2026-04-16

### CLI
- **`kai purge <path-or-glob> --yes`** — escape hatch from immutability: remove a file from every snapshot in history. Supports glob patterns (`**/*.pem`, etc.). Snapshot nodes remain valid for navigation; purged file content is gone (`909bd30`).

## [0.10.4] — 2026-04-15

### CLI
- **Semantic diff reports const value changes** in addition to symbol structural changes (`db88883`).

## [0.10.3] — 2026-04-15

### CLI — `kai resolve`
- **Workspace conflict resolution flow** (`e7b4ee2`) — when `kai integrate` produces conflicts, `kai resolve <ws>` materializes them into editable `.HEAD` / `.TARGET` / `.BASE` files; `kai resolve --continue` re-runs the integration with your resolutions.
- **Fall back to working tree + marker on missing blob** in resolve (`ca0104e`).
- **Snapshot stores blob content for all file types**, not just text-recognized ones (`fc5969c`).
- **Analyze on first capture after import** so freshly-imported git history gets symbols and call edges immediately (`9d873a4`).

### CI
- **Smoke test for `kai init` against staging kai-server** (`b632a64`).
- **Smoke self-heal test** uses `kai doctor` instead of `kai --version` (`c3a9406`).
- **Release-kai-cli gated on smoke** (`a2eba48`).

## [0.10.2] — 2026-04-14

### CLI — Git hooks can no longer block git
- **Hooks are now best-effort and never block git.** The previous `pre-commit` / `pre-push` scripts ended with `kai capture` / `kai push` as the last command, so any failure (missing kai binary, deleted `.kai` directory, capture error) propagated as the hook's exit code and could block `git commit` / `git push`. The new `v2` hook scripts check for kai-on-PATH and `.kai/`, run capture/push silently, swallow any failure with `|| true`, and unconditionally `exit 0`. There is no execution path that returns nonzero.
- **Self-heal on every kai invocation.** `PersistentPreRun` now calls `selfHealHooks()` which silently rewrites any kai-managed (`# kai-managed-hook`) hook that isn't at the current `v2` version. Users with the old dangerous hook get healed the moment they run any `kai` command — no manual upgrade step required.
- **`kai hook install` always upgrades kai-managed hooks in place.** Previously bailed with "already installed". Foreign (non-kai) hooks are still left untouched, but with a warning instead of a hard error — init no longer aborts in repos with husky/lefthook setups.

### CLI — New `kai doctor` command
- **`kai doctor`** audits local Kai state: kai binary on PATH, `.kai/` present, git hooks (kai-managed vs foreign, current vs stale), kaicontext.com auth, configured remote.
- **`kai doctor --fix`** applies automatic repairs — currently upgrades any stale kai-managed git hook to the current safe version.

## [0.10.1] — 2026-04-14

### CLI — `kai init` is now one-shot and low-friction
- **Git history import is automatic** — previously gated behind a `[y/N]` prompt and a `≤1000` commit limit. Now runs unconditionally; `runGitImport` already caps at `importMaxCommits` (default 50), so large repos get their most recent 50 commits silently.
- **Git hooks install without prompting** — post-commit + pre-push hooks are set up automatically in any git repo.
- **MCP server install is auto-detected** — if Claude Code or Codex is on `PATH`, init runs `<tool> mcp list`; if `kai` isn't already registered, it's installed automatically. If it is, init says so and moves on. No prompt either way.
- **kaicontext.com signup is the default path** — the "Would you like to set that up?" gate is gone. Init proceeds straight to asking for an email, sends the magic link, exchanges the token, and signs you in.
- **Already-logged-in users skip signup entirely** — `GetValidAccessToken` is checked first; if valid, the signup copy and email prompt are suppressed.
- **Personal org + repo + first push are fully automatic** — after login, init uses the server-auto-created personal org (or derives a slug from the email local-part as a safety net), calls `DetectProjectName()` for the repo name, creates the repo, wires up `origin`, and pushes. The previous `Repository name [...]:` and `Push your semantic graph now? [Y/n]:` prompts are removed.
- **`kai bench` offer removed from init** — run `kai bench` manually anytime.

### Docs
- **README** — `Quick Start` section rewritten to describe the new one-shot init flow (graph → history → hooks → MCP → account + org + repo + push).

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
