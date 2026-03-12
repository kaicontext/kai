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

## What Kai Builds

For every commit, Kai constructs a queryable semantic model:

| Layer | What It Captures |
|-------|----------------|
| **Functions & methods** | Signatures, bodies, call graphs |
| **Dependencies** | Module relationships, data flow |
| **Behavior changes** | What actually changed in meaning, not just text |

This graph is immutable, verifiable, and designed for machine reasoning.

---

## Current Applications

### Phase 1: Selective CI (Shipping)
Kai determines which tests actually need to run based on behavioral impact,
not file diffs. Result: 80% CI time reduction for early users.

### Phase 2: IDE Integration (In Development)
Semantic context where developers work — precise jump-to-definition,
impact analysis, and change validation inside VS Code.

### Phase 3: Verified AI Agents (Building)
Close the loop: agent proposes edit → Kai validates impact →
agent executes with proof, not generation with hope.

---

## Why This Matters Now

AI generates more code than humans review. Current tools:

- Guess at dependencies from text
- Run tests blindly or skip them dangerously
- Produce changes without verifiable impact analysis

Kai provides the semantic substrate AI coding tools need to operate
safely and precisely at scale.

---

## Install

```bash
curl -sSL https://get.kailayer.com | sh
```

## Quick Start

```bash
kai init
kai snapshot create --git main --repo .
kai snapshot create --git feat/my-branch --repo .
kai diff                    # semantic change impact
kai ci plan                 # minimal test selection
```

For full command reference and workflow examples, see [docs/cli-reference.md](docs/cli-reference.md).

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
| IDE extension | **Building** | VS Code integration, 60-day target |
| Context retrieval for LLMs | **Architecture** | Design partners defining requirements |
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
