<p align="center">
  <img src="kai.webp" alt="Kai Logo" width="400">
</p>

# Kai

**Semantic infrastructure for code change.**

Kai understands what code *means* — functions, dependencies, behavior impact —
not just which lines changed. This semantic graph powers precise CI,
context-aware IDEs, and verifiable AI coding agents.

[kailayer.com](https://kailayer.com)

---

## Install

```bash
# curl
curl -sSL https://get.kailayer.com | sh

# Homebrew
brew install kailayerhq/kai/kai
```

## Quick Start

```bash
kai init
kai capture
kai diff                    # semantic change impact
kai ci plan                 # minimal test selection
```

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

Also available on the [MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.kailayerhq/kai`.

See [docs/mcp.md](docs/mcp.md) for Cursor setup, tool reference, and troubleshooting.

---

## What Kai Builds

For every commit, Kai constructs a queryable semantic model:

| Layer | What It Captures |
|-------|----------------|
| **Functions & methods** | Signatures, bodies, call graphs |
| **Dependencies** | Module relationships, data flow |
| **Behavior changes** | What actually changed in meaning, not just text |

This graph is immutable, verifiable, and designed for machine reasoning.

---

## Use Cases

### Selective CI
Kai determines which tests actually need to run based on behavioral impact,
not file diffs. Result: 80% CI time reduction for early users.

### AI Code Context
11 MCP tools give AI assistants structured access to your codebase's
dependency graph, call graph, impact analysis, and test coverage.

### Verified AI Agents
Agent proposes edit → Kai validates impact →
agent executes with proof, not generation with hope.

---

## Architecture

Kai is open-core: CLI and semantic engine are Apache 2.0.
Kai Cloud adds hosted infrastructure and team features.

See [What's Open Source vs. Kai Cloud](docs/architecture-boundary.md).

---

## Roadmap

| Phase | Status | Milestone |
|-------|--------|-----------|
| Semantic graph (5 languages, 71 change types) | **Production** | 2.8M commits analyzed |
| CI optimization | **Shipping** | 3 design partners, daily usage |
| MCP server for AI assistants | **Shipped** | 11 tools, npm + brew + registry |
| IDE extension | **Building** | VS Code integration |
| Verified agent loop | **Planned** | Autonomous editing with proof |

[Full roadmap →](https://github.com/orgs/kailayerhq/projects/1)

---

## License

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

See [LICENSE](LICENSE).

---

## Community & Contributing

- [Slack](https://join.slack.com/t/kailayer/shared_invite/zt-3q8ulczwl-vkZ05GQH~kwudonmH53hGg)
- [Contributing guidelines](CONTRIBUTING.md)
