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
kai init                      # One-shot setup: graph, MCP, account, remote
kai capture -m "Initial"      # Snapshot your code with a message
kai push                      # Push to kaicontext.com
kai diff                      # Semantic change impact
```

`kai init` runs a single, low-friction setup flow:

1. **Creates the semantic graph** in `.kai/` and builds the first capture.
2. **Imports git history** automatically (for repos with ≤1000 commits) — no prompt.
3. **Installs git hooks** for auto-capture on commit and auto-push on `git push`.
4. **Installs the Kai MCP server** for any detected AI coding tool (Claude Code,
   Codex). If the `kai` MCP is already registered, it's left alone; otherwise it's
   added automatically — no prompt.
5. **Signs you up for kaicontext.com** in-place: asks for your email, emails you
   a login link, you paste the token back, and Kai creates a personal org named
   after you plus a repo for the current directory. Your first push goes out at
   the end of init.

Every step runs by default. Press `Ctrl+C` to skip the account step if you'd
rather stay local; everything else is automatic.

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

## Language Support

Kai uses [tree-sitter](https://tree-sitter.github.io/) for parsing and builds semantic graphs for 9 languages.
Each language is validated with [end-to-end tests](kai-e2e/) against real open-source projects:

| Language | Symbols | Callers/Callees | Dependencies | Tests | Impact | E2E Fixtures |
|----------|---------|-----------------|--------------|-------|--------|-------------|
| **Go** | functions, methods, interfaces, structs | cross-package via import aliases | Go package resolution | same-dir `_test.go` + transitive | via dependency graph | [chi](https://github.com/go-chi/chi), [lo](https://github.com/samber/lo) |
| **Rust** | functions, structs, enums, traits, impl methods, macros | cross-file via exports | `crate::`, `super::`, `self::`, `mod`, wildcards | filename pattern matching for integration tests | via dependency graph | [just](https://github.com/casey/just), [miniserve](https://github.com/svenstaro/miniserve) |
| **TypeScript/JavaScript** | functions, classes, methods, variables | via imports | relative + workspace resolution | `*.test.ts`, `__tests__/` patterns | via dependency graph | — |
| **Python** | functions, classes, methods | via imports | dotted module resolution | `test_*.py`, `*_test.py` patterns | via dependency graph | — |
| **Ruby** | methods, classes, modules | via exports | `require`, `require_relative`, Zeitwerk autoload | `*_spec.rb`, `*_test.rb` patterns | via dependency graph | — |
| **SQL** | — | — | — | — | — | — |
| **PHP** | functions, classes, methods | — | — | — | — | — |
| **C#** | functions, classes, methods | — | — | — | — | — |

92 E2E tests across Go, Rust, and push completeness, all passing. Run them with:

```bash
cd kai-e2e && ./scripts/run-tests.sh
```

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
