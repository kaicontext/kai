# Patent Posture

## Overview

Kai uses the Apache License, Version 2.0 for all open-source components. Apache 2.0 includes an explicit patent grant (Section 3) that provides legal clarity for users and contributors.

## OSS Components — Apache 2.0 Patent Grant

All code in the public repository is covered by the Apache 2.0 patent grant:

- **kai-core** — Parsing, graph construction, semantic diffing, change detection, intent generation
- **kai-cli** — CLI commands, local graph store, Git integration, CI planning
- **kailab** — Data plane server, Git protocol, object storage

Contributors grant a perpetual, worldwide, royalty-free patent license for their contributions. This license terminates only if a user initiates patent litigation against the project.

**This is good for enterprise adoption.** Companies can use Kai without patent risk for any functionality in the OSS codebase.

## Proprietary Components — Rights Retained

Patent rights for Kai Cloud features are retained by the company:

- Hosted multi-repo graph indexing
- Cross-branch artifact reuse algorithms
- Org-wide risk scoring and ML models
- Enterprise policy engine

These features are not included in the OSS release and are not covered by the Apache 2.0 patent grant.

## Contributor Patent Grant

By contributing to Kai under Apache 2.0, contributors grant a patent license that covers their specific contributions. This is implicit in the license — no additional patent assignment is required.

The DCO (Developer Certificate of Origin) confirms that contributors have the right to submit their code, which includes the right to grant the Apache 2.0 patent license.

## Summary

| Scope | Patent position |
|-------|----------------|
| OSS code (kai-core, kai-cli, kailab) | Apache 2.0 patent grant to all users |
| Kai Cloud features | Patent rights retained by the company |
| Contributor code | Apache 2.0 patent grant via contribution |
