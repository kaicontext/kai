<p align="center">
  <img src="kai.webp" alt="Kai Logo" width="400">
</p>

# Kai

**Semantic infrastructure for code change.**

Kai understands what code *means* — functions, dependencies, behavior impact —
not just which lines changed. This semantic graph powers precise CI,
context-aware IDEs, and verifiable AI coding agents.

[kaicontext.com](https://kaicontext.com) · [Docs](https://docs.kaicontext.com) · [Slack](https://join.slack.com/t/kailayer/shared_invite/zt-3q8ulczwl-vkZ05GQH~kwudonmH53hGg)

---

## Install

```bash
# curl
curl -sSL https://get.kaicontext.com | sh

# Homebrew
brew install kaicontext/kai/kai
```

## Quick Start

```bash
kai init                      # Detects git, offers history import + auto-sync
kai capture -m "Initial"      # Snapshot your code with a message
kai push                      # Push to kaicontext.com
kai diff                      # Semantic change impact
```

In a git repo, `kai init` will:
1. Offer to **import git history** as semantic snapshots
2. Install a **post-commit hook** for automatic capture on each commit
3. Generate a **GitHub Actions / GitLab CI** workflow to keep Kai in sync

For full command reference, see [docs/cli-reference.md](docs/cli-reference.md).

---

## MCP Server

Kai ships an [MCP](https://modelcontextprotocol.io) server that gives AI coding assistants
access to call graphs, dependency maps, impact analysis, and test coverage.

```bash
# Claude Code
claude mcp add kai -- kai mcp serve

# Or without installing kai (npx downloads it automatically)
claude mcp add kai -- npx -y kai-mcp
```

No setup required — the server lazily initializes the semantic graph on first use.

12 tools: `kai_status`, `kai_symbols`, `kai_files`, `kai_diff`, `kai_impact`, `kai_callers`, `kai_callees`, `kai_context`, `kai_dependencies`, `kai_dependents`, `kai_tests`, `kai_refresh`.

See [docs/mcp.md](docs/mcp.md) for Cursor setup, tool reference, and troubleshooting.

---

## Code Reviews

Kai reviews are anchored to semantic changesets, not line diffs.

```bash
kai review open --title "Add auth middleware"   # Create a review
kai push                                         # Push to kaicontext.com
kai fetch --review abc123                        # Sync comments from web
kai review comments abc123                       # View inline comments locally
```

On the web, reviews show semantic diffs (what functions changed, not just which lines), inline commenting, and one-click merge that updates `snap.main`.

---

## CI Integration

Kai CI runs workflows defined in `.kailab/workflows/` with semantic checkout, parallel jobs, and 30-minute default timeouts.

```bash
kai ci runs                   # List CI runs
kai ci logs 42                # View logs for run #42
kai ci cancel 42              # Cancel a run
kai capture -m "Fix bug"      # Message shows as CI run headline
```

Email notifications on pipeline completion are sent to the snapshot author via Postmark.

---

## What Kai Builds

For every capture, Kai constructs a queryable semantic model:

| Layer | What It Captures |
|-------|----------------|
| **Functions & methods** | Signatures, bodies, call graphs |
| **Dependencies** | Module relationships, imports, data flow |
| **Behavior changes** | What actually changed in meaning, not just text |
| **Test coverage** | Which tests cover which source files (static + transitive) |

This graph is immutable, content-addressed, and designed for machine reasoning.

---

## Use Cases

### Selective CI
Kai determines which tests actually need to run based on behavioral impact,
not file diffs. Result: 80% CI time reduction for early users.

### AI Code Context
12 MCP tools give AI assistants structured access to your codebase's
dependency graph, call graph, impact analysis, and test coverage.

### Code Reviews
Semantic diff shows *what changed* (function added, condition modified, API changed)
instead of raw line diffs. Inline comments anchored to symbols, not lines.

### Verified AI Agents
Agent proposes edit → Kai validates impact →
agent executes with proof, not generation with hope.

---

## Architecture

Kai is fully open source under Apache 2.0: core engine, CLI, and server.
Kai Cloud is the hosted version — same code, managed infrastructure.

```
kai capture → local semantic graph (SQLite)
kai push    → kaicontext.com (Postgres + GCS)
                ├── File viewer with search, language breakdown
                ├── CI with SSE live updates
                ├── Code reviews with semantic diffs
                └── Email notifications (Postmark)
```

See [What's Open Source vs. Kai Cloud](docs/architecture-boundary.md).

---

## License

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

See [LICENSE](LICENSE).

---

## Community & Contributing

- [Slack](https://join.slack.com/t/kailayer/shared_invite/zt-3q8ulczwl-vkZ05GQH~kwudonmH53hGg)
- [Contributing guidelines](CONTRIBUTING.md)
