# Licensing FAQ

## What license does Kai use?

Kai is licensed under the [Apache License, Version 2.0](../LICENSE). This applies to all code in the public repository: `kai-core`, `kai-cli`, `kailab`, and `kailab-control`.

## Can I use Kai commercially?

Yes. Apache 2.0 explicitly allows commercial use. You can use Kai in proprietary projects, internal tools, and commercial products.

## Can I fork Kai?

Yes. You can fork, modify, and distribute Kai under the terms of Apache 2.0. You must retain copyright notices, include the license, and note any changes you made.

## Do I have to open-source my code if I use Kai?

No. Apache 2.0 is a permissive license. You can use Kai as a library or tool in proprietary software without open-sourcing your own code.

## What's the difference between self-hosting and Kai Cloud?

All Kai software is open source under Apache 2.0. Kai Cloud is a hosted service running the same code — you're paying for managed infrastructure, not proprietary features.

| | Self-hosted | Kai Cloud |
|--|------------|-----------|
| License | Apache 2.0 | Apache 2.0 |
| CLI + core engine | You run it | Included |
| Data plane + control plane | You host it | Managed |
| Multi-repo graph index | You host it | Managed |
| Analytics + dashboards | You host it | Managed |
| SSO/RBAC/audit | You configure it | Managed |
| Infrastructure + ops | Your responsibility | Managed |

See [architecture-boundary.md](architecture-boundary.md) for the full breakdown.

## Can I self-host Kai?

Yes. The Kailab data plane server can be self-hosted for team collaboration. The control plane can also be self-hosted with PostgreSQL. See the deployment documentation for details.

## Does Kai collect telemetry?

Kai CLI includes opt-in anonymous telemetry. It is:
- **Off by default** in CI environments
- Controllable via `KAI_TELEMETRY=0` (disable) or `KAI_TELEMETRY=1` (enable)
- Limited to usage statistics (no source code, no file contents)

## How do you handle security disclosures?

See [SECURITY.md](../SECURITY.md). Report vulnerabilities to security@kaicontext.com. We follow coordinated disclosure with a 48-hour acknowledgment SLA.

## What about patents?

Apache 2.0 includes an explicit patent grant. Contributors grant a patent license for their contributions to the project. This is standard for Apache 2.0 projects and is beneficial for enterprise adoption. See [patent-posture.md](patent-posture.md) for details.

## Do contributors need to sign a CLA?

We use the Developer Certificate of Origin (DCO) instead of a CLA. Contributors certify their right to submit code by adding a `Signed-off-by` line to commits. This is the same approach used by the Linux kernel, Kubernetes, and many other large OSS projects.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for details.
