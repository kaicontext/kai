# Changelog

All notable changes to Kai are documented here.

## 2026-03-01  since v0.3.0
_43 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-28: 42 commits since v0.3.0) (`12d12e86`)
- Update CHANGELOG.md (2026-02-27: 41 commits since v0.3.0) (`01a428f6`)
- Update CHANGELOG.md (2026-02-26: 40 commits since v0.3.0) (`4bb24968`)
- Update CHANGELOG.md (2026-02-25: 39 commits since v0.3.0) (`67bb0fbd`)
- Update CHANGELOG.md (2026-02-24: 38 commits since v0.3.0) (`b48cdc1f`)
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!� refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-28  since v0.3.0
_42 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-27: 41 commits since v0.3.0) (`01a428f6`)
- Update CHANGELOG.md (2026-02-26: 40 commits since v0.3.0) (`4bb24968`)
- Update CHANGELOG.md (2026-02-25: 39 commits since v0.3.0) (`67bb0fbd`)
- Update CHANGELOG.md (2026-02-24: 38 commits since v0.3.0) (`b48cdc1f`)
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest ’ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-27  since v0.3.0
_41 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-26: 40 commits since v0.3.0) (`4bb24968`)
- Update CHANGELOG.md (2026-02-25: 39 commits since v0.3.0) (`67bb0fbd`)
- Update CHANGELOG.md (2026-02-24: 38 commits since v0.3.0) (`b48cdc1f`)
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!â€™ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-26  since v0.3.0
_40 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-25: 39 commits since v0.3.0) (`67bb0fbd`)
- Update CHANGELOG.md (2026-02-24: 38 commits since v0.3.0) (`b48cdc1f`)
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!Ã¢â‚¬â„¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-25  since v0.3.0
_39 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-24: 38 commits since v0.3.0) (`b48cdc1f`)
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-24  since v0.3.0
_38 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-23: 37 commits since v0.3.0) (`2f322663`)
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-23  since v0.3.0
_37 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-20: 36 commits since v0.3.0) (`cb425724`)
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¾Ãƒâ€šÃ‚Â¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-20  since v0.3.0
_36 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (2026-02-18: 35 commits since v0.3.0) (`a401c2d4`)
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¾ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## 2026-02-18  since v0.3.0
_35 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Update CHANGELOG.md (34 commits since v0.3.0) (`42120425`)
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¾ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)

## Changelog since v0.3.0
_34 commits since 2026-02-11_

### Features
- Add VitePress docs site and automated changelog pipeline (`e693fc94`)
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
- Remove duplicate VitePress setup (docs site lives in kai-server) (`dd794281`)
- Remove ICP doc from OSS repo (moved to private) (`0f4e3ce1`)
- Move detailed CLI reference to docs/cli-reference.md (`82143bec`)
- Simplify README to focus on what Kai does (`f5a8fe06`)
- Split repo: remove server code to separate kai-server repository (`b3fd983a`)
- Open-core architecture, licensing, benchmarks, CI, telemetry, and regression tests (`8d38b45f`)
- Both compile cleanly. The qualifyShardURL function is implemented and working. (`f83766b9`)
- actionCheckout  completely rewritten to use the Kai API instead of git clone: (`9078e36e`)
- Root cause: The Kai CLI pushes snap.latest (not snap.main). Both kai/kailab and howth/howth only have two refs: snap.latest and cs.latest. notifyPushCI was converting snap.latest!ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â¦ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â¦ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¾ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ refs/heads/latest, which never matches branches: [main] in any workflow. CI never worked for Kai CLI pushes  howth was the same. (`4d6475f7`)ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¿ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡ÃƒÆ’ÃƒÂÿ