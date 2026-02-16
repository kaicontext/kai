# Kai: Open Source vs Kai Cloud

## What's Open Source

The entire Kai CLI and core engine are open source under Apache 2.0. This includes:

| Component | What it does | License |
|-----------|-------------|---------|
| `kai-core` | Tree-sitter parsing, semantic graph, diffing, merging, change detection, intent generation | Apache 2.0 |
| `kai-cli` | All CLI commands (`capture`, `diff`, `review`, `ci plan`, `workspace`), local SQLite graph store, Git integration | Apache 2.0 |
| `kailab` | Data plane server (Git protocol, object storage, refs, pack files) | Apache 2.0 |

### What you can do with OSS Kai

- Build semantic snapshots from any Git repo
- Compute behavior-level diffs between branches
- Run impact analysis and selective test planning
- Create and manage code reviews locally
- Push/fetch snapshots to a self-hosted Kailab server
- Use shadow mode in CI (GitHub Actions, GitLab CI)
- Extend the graph store via the `store.Store` interface

Everything needed for a single developer or team to get value from Kai runs locally, offline, with zero cloud dependency.

## What's in Kai Cloud

Kai Cloud is the hosted service at kailayer.com. It adds capabilities that require multi-tenant infrastructure:

| Feature | Why it's not in OSS |
|---------|-------------------|
| Hosted graph index (multi-branch, multi-repo) | Requires persistent server infrastructure and cross-repo data |
| Cross-branch artifact reuse / remote cache | Depends on shared state across branches and users |
| Org-wide analytics and dashboards | Aggregates data across teams and repositories |
| Risk scoring and policy engine | Uses org-level historical data for ML-based scoring |
| Enterprise RBAC, SSO, and audit logs | Multi-tenant auth and compliance features |
| CI runner orchestration (Kubernetes) | Managed compute for running CI jobs |

## Why this split

**Rule of thumb:** If it must run locally to earn trust → it's OSS. If it depends on multi-tenant data, org state, or network effects → it's Kai Cloud.

We believe the core engine should be open so that:
1. Developers can inspect and verify what Kai does to their code
2. Teams can self-host if their security requirements demand it
3. The community can extend Kai for new languages and workflows
4. CI integration stays transparent and auditable

## Guarantees

- The CLI and core engine (`kai-cli`, `kai-core`) will always remain open source under Apache 2.0
- We will not move existing OSS features behind a paywall
- Local-only workflows will never require a Kai Cloud account
- The `store.Store` interface is a stable API — alternative storage backends will always be supported

## Self-hosting

You can run your own Kailab data plane server for team collaboration without Kai Cloud. The `kailab` server handles Git protocol, object storage, and refs. See the deployment docs for setup instructions.

The control plane (`kailab-control`) provides auth, org management, and CI orchestration. It can also be self-hosted but requires PostgreSQL and optionally Kubernetes for CI runner support.

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
│  kailab (Apache 2.0)                     │
│  Data plane: Git protocol, storage       │
│  Self-hostable                           │
└──────────────────────────────────────────┘

┌──────────────────────────────────────────┐
│  Kai Cloud (Proprietary)                 │
│  Hosted infra, analytics, enterprise     │
│  Optional — OSS works without it         │
└──────────────────────────────────────────┘
```
