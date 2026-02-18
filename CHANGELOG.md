# Changelog

All notable changes to Kai are documented here.

## Changelog since v0.3.0
_32 commits since 2026-02-11_

### Features
- Add diff-first CI fast path: skip full snapshots when coverage map exists (`bff10aec`)
- Add Ruby and Python support to change detection (`497605ab`)
- Add ideal customer profile for design partner outreach (`6f27d183`)
- Add roadmap link to README (`c86548b8`)
- Add contribution review policy with scope, determinism, and boundary rules (`d5aa775e`)
- Add weekly update template (`dee13172`)
- Add Slack community link to README and CONTRIBUTING (`5d27feda`)
- Add workflow discovery endpoint and show workflow definitions on CI page (`9c97e0fc`)
- Add copy button to markdown code blocks in README rendering (`ce1f8bc8`)
- Add light/dark mode with system preference detection and manual toggle (`ad669e37`)
- Add schedule triggers and reusable workflows for CI (`4deb404a`)

### Fixes
- Fix fast path: use native git diff and hook into runCIPlan (`4edf5fc3`)
- Fix matrix include-only expansion and runner job matching (`b695ba3a`)
- Fix job dependency resolution: map needs keys to display names (`6940b0fe`)
- Fix StringOrSlice JSON serialization to always use arrays (`9f2defaa`)
- Fix job label matching and resolve matrix expressions in job names (`8d5df206`)
- Fix nil pointer in workflow sync when workflow doesn't exist in DB (`08e9cc34`)
- Fix workflow sync to decode base64 content from data plane API (`d90befba`)
- Fix workflow discovery: use file object digest and add snap.latest fallback (`9919d44d`)
- Fix git source to capture all file types including images (`b5f31ce2`)
- Fix UTF-8 encoding in file content and add raw content endpoint for images (`d2d7c09a`)
- Fix code viewer horizontal overflow on long lines (`dc68d11c`)
- Fix repo page showing content for non-existent repos instead of error (`151a2265`)
- Fix idempotent migration for job outputs columns on PostgreSQL (`618c7181`)

### Other
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
