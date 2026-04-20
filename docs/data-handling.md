# How Kai Handles Your Code

Kai is a semantic code analysis tool. This document explains exactly what data Kai reads, extracts, stores, and transmits — so you can make an informed decision about using it in your codebase.

## What Kai Reads

Kai reads source files from your Git repository or working directory. Supported file types:

**Parsed for semantic structure** (tree-sitter):
- Go (`.go`)
- TypeScript / JavaScript (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs`)
- Python (`.py`)
- Ruby (`.rb`)
- Rust (`.rs`)

**Tracked but not parsed** (stored as content-addressed blobs):
- Config: `.json`, `.yaml`, `.yml`, `.toml`, `.xml`, `.ini`, `.env`
- Data: `.sql`, `.proto`, `.graphql`, `.html`, `.css`
- Other code: `.java`, `.c`, `.cpp`, `.cs`, `.php`, `.swift`, `.kt`
- Docs: `.md`, `.txt`
- Scripts: `.sh`, `.bash`, `.zsh`

**Skipped entirely:**
- Binary files (images, video, PDFs, compiled artifacts)
- Files larger than 500 KB
- Paths matched by `.gitignore` and `.ignore`

## What Kai Extracts

For parsed languages, Kai uses tree-sitter grammars to extract **structural metadata** — not source code text. Specifically:

| Extracted | Example | What's stored |
|-----------|---------|---------------|
| Function names | `calculateTax` | Name, kind, line range, signature |
| Class names | `UserService` | Name, kind, line range |
| Variable declarations | `const MAX_RETRIES` | Name, kind, line range |
| Call sites | `auth.validate()` | Callee name, object, location |
| Import statements | `import { hash } from "crypto"` | Source path, named imports, location |

Kai does **not** store function bodies, logic, literals, comments, or any raw source text in the graph. Only signatures and structural positions.

## What Kai Stores Locally

Kai stores data in `~/.kai/` (or `.kai/` in your project root):

### Graph database (`kai.db` — SQLite)

The graph contains **nodes** and **edges**:

- **File nodes**: path, language, BLAKE3 content hash (not file content)
- **Symbol nodes**: name, kind, file reference, line range, signature
- **Snapshot nodes**: Git ref, file count, list of file paths + hashes
- **ChangeSet nodes**: base/head snapshot references, title
- **Edges**: structural relationships (`CONTAINS`, `DEFINES_IN`, `CALLS`, `IMPORTS`, `TESTS`)

Every node is identified by `BLAKE3(kind + canonical_JSON_payload)` — content-addressed and deterministic.

### Object store (`~/.kai/objects/`)

File contents are stored locally as content-addressed blobs, named by their BLAKE3 hash. This is how Kai computes diffs between snapshots — it needs the file content to parse and compare.

**This is the only place raw file content exists in Kai's data model.** It lives on your machine, in your project directory.

## What Kai Sends to a Remote Server

Remote sync (`kai push` / `kai fetch`) is **opt-in**. If you never configure a remote, no data leaves your machine.

When you do push to a Kai server:

| Transmitted | Format | Purpose |
|-------------|--------|---------|
| Graph nodes | JSON payloads (metadata only) | Sync semantic graph |
| Graph edges | Source → type → destination | Sync relationships |
| Object pack | Zstd-compressed binary | Transfer file content for diffing |
| Named refs | Ref name → target ID | Track snapshot/changeset pointers |

**Protocol details:**
- All requests authenticated via `Authorization: Bearer <token>`
- Negotiation phase: client sends digests, server replies with what it's missing (no redundant transfer)
- Transport: HTTPS to configurable endpoint (`KAI_SERVER` env var)
- Pack format: binary header + concatenated objects, Zstd-compressed

## What Kai Sends as Telemetry

Telemetry is **on by default** on developer machines and **off by default in CI** (Kai auto-detects `CI=true`). It is **opt-out**: the first time Kai emits an event on a machine, it prints a one-line notice to stderr naming exactly what's collected and how to disable it.

Control at any time with:

- `kai telemetry disable` — persistent opt-out
- `kai telemetry enable` — re-enable (also works for first-time explicit enable)
- `KAI_TELEMETRY=0` — hard-off for a single invocation (or in env)
- `KAI_TELEMETRY=1` — hard-on, overrides everything including `CI=true`

When enabled, Kai reports:

| Collected | Example |
|-----------|---------|
| Command name | `capture`, `diff`, `ci plan` |
| Duration | `1200ms` |
| Phase timings | `parse: 400ms, diff: 300ms` |
| Aggregate counts | `files: 42, symbols: 180` |
| OS / architecture | `darwin / arm64` |
| Anonymous install ID | Random UUID per machine, stored in `~/.kai/telemetry.json` |
| Result | `ok` or error class (e.g. `network`, `auth`) |
| Kai version | `0.12.1` |

**Never collected:** file names, file paths, file contents, symbol names, repository URLs, Git refs, usernames, or any identifier tied to your codebase.

Events are delivered directly to [PostHog](https://posthog.com) (US cloud, `us.i.posthog.com`). Each CLI invocation flushes its events on exit — there is no persistent local spool. The install ID is the only identifier and is not tied to any Kai account or email.

## Summary

| Question | Answer |
|----------|--------|
| Does Kai read my source code? | Yes — to parse structure and compute diffs. |
| Does Kai store my source code? | Locally, yes (content-addressed blobs). Never in the graph database — only hashes and metadata. |
| Does Kai send my code to a server? | Only if you explicitly `kai push`. Never automatically. |
| Does Kai phone home? | Telemetry is on by default on dev machines (off in CI). A first-run notice tells you what's collected; `kai telemetry disable` turns it off forever. Telemetry contains zero code or file information. |
| Can I use Kai fully offline? | Yes. All commands work without network access. |
| What's in the graph database? | Names, signatures, line ranges, hashes, and structural relationships. Not source text. |
