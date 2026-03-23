# Kai Architecture

## Repositories

All Kai components are open source under Apache 2.0, split across two repositories:

| Component | Repository | What it does | License |
|-----------|-----------|-------------|---------|
| `kai-core` | [kai](https://github.com/kaicontext/kai) | Tree-sitter parsing, semantic graph, diffing, merging, change detection, intent generation | Apache 2.0 |
| `kai-cli` | [kai](https://github.com/kaicontext/kai) | All CLI commands (`capture`, `diff`, `review`, `ci plan`, `workspace`), local SQLite graph store, Git integration | Apache 2.0 |
| `kailab` | [kai-server](https://github.com/kaicontext/kai-server) | Data plane server (Git protocol, object storage, SSH server) | Apache 2.0 |
| `kailab-control` | [kai-server](https://github.com/kaicontext/kai-server) | Control plane (auth, orgs, repos, CI runner, web UI) | Apache 2.0 |

### What you can do with Kai

- Build semantic snapshots from any Git repo
- Compute behavior-level diffs between branches
- Run impact analysis and selective test planning
- Create and manage code reviews locally
- Push/fetch snapshots to a self-hosted Kailab server
- Host your own data plane and control plane
- Use shadow mode in CI (GitHub Actions, GitLab CI)
- Extend the graph store via the `store.Store` interface

Everything needed for a single developer or team to get value from Kai runs locally, offline, with zero cloud dependency. The server components can be self-hosted for team collaboration.

## Kai Cloud

Kai Cloud is the hosted service at kaicontext.com. It runs the same open-source server code — you're paying for managed infrastructure, not proprietary features:

| Feature | Description |
|---------|------------|
| Hosted graph index (multi-branch, multi-repo) | Persistent server infrastructure with cross-repo data |
| Cross-branch artifact reuse / remote cache | Shared state across branches and users |
| Org-wide analytics and dashboards | Aggregated data across teams and repositories |
| Risk scoring and policy engine | Org-level historical data for ML-based scoring |
| Enterprise RBAC, SSO, and audit logs | Multi-tenant auth and compliance |
| CI runner orchestration (Kubernetes) | Managed compute for running CI jobs |

## Why two repositories

The split is architectural, not licensing:

- **kai** contains the core engine and CLI — pure local-first tools with no server dependencies
- **kai-server** contains the server infrastructure — depends on PostgreSQL, Kubernetes, and cloud services

We keep them separate so that:
1. Developers can inspect and verify what Kai does to their code
2. Teams can self-host the full stack if their security requirements demand it
3. The community can extend Kai for new languages, workflows, and server features
4. CI integration stays transparent and auditable

## Guarantees

- All Kai components will remain open source under Apache 2.0
- Local-only workflows will never require a Kai Cloud account
- The `store.Store` interface is a stable API — alternative storage backends will always be supported

## Architecture

```
┌──────────────────────────────────────────┐
│  kai-cli (Apache 2.0)                    │
│  Commands, Git integration, local DB     │
└──────────────┬───────────────────────────┘
               │ imports
               ▼
┌──────────────────────────────────────────┐
│  kai-core (Apache 2.0)                   │
│  Parsing, graph, diffing, detection      │
│  Pure library — no network, no I/O       │
└──────────────────────────────────────────┘

┌──────────────────────────────────────────┐
│  kai-server (Apache 2.0 — separate repo) │
│  Data plane, control plane, deploy       │
│  Optional — kai works without it         │
└──────────────────────────────────────────┘
```
