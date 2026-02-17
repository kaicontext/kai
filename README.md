<p align="center">
  <img src="kai.webp" alt="Kai Logo" width="400">
</p>

# Kai

Kai makes code changes safer and CI dramatically faster by understanding what actually changed.

Modern CI systems run everything because they only see line-level diffs.
Kai understands behavior-level impact.

Instead of asking:

> "What files changed?"

Kai answers:

> "What parts of the system changed in meaning — and what tests actually need to run?"

---

## What Kai Does

For every pull request, Kai:

* Builds a semantic model of your codebase
* Determines which modules are impacted by a change
* Identifies the minimal set of tests required
* Produces a deterministic execution plan
* Runs only what matters — safely

The result:

* Shorter CI times
* Fewer redundant test executions
* Faster developer feedback loops
* No compromise on correctness

---

## Why This Matters

As teams grow and AI generates more code:

* PR volume increases
* CI costs scale linearly
* Review noise increases
* Build times slow development velocity

Kai turns CI from a static script into an intelligent execution plan.

It reduces wasted computation while preserving safety.

---

## What Kai Is Not

Kai does not replace your test runner.
Kai does not rewrite your build system.
Kai does not require changing your workflow.

Kai sits on top of your existing CI and makes it smarter.

---

## The Vision

Software changes should be evaluated based on meaning and impact, not just modified lines.

Kai is building the semantic control plane for software change.

---

## Quick Start

```bash
kai init
kai snapshot create --git main --repo .
kai snapshot create --git feat/my-branch --repo .
kai diff
kai ci plan
```

For the full command reference, concepts, and workflow examples, see [docs/cli-reference.md](docs/cli-reference.md).

---

## License

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

---

## Architecture

Kai is open-core: the CLI and core engine are Apache 2.0, while Kai Cloud adds hosted infrastructure features. See [What's Open Source vs What's in Kai Cloud](docs/architecture-boundary.md) for the full breakdown.

## Community

Join the conversation:

* [Slack](https://join.slack.com/t/kailayer/shared_invite/zt-3q8ulczwl-vkZ05GQH~kwudonmH53hGg)

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and how to submit changes.
