# Kai repository summary (brutally honest)

## What this repo is
- Kai is a semantic analysis layer built **on top of Git**, not a Git replacement. It builds snapshots of a codebase, extracts symbols (functions/classes/vars), and computes semantic diffs (“ChangeSets”).
- It aims to turn text diffs into meaning-level changes (e.g., “timeout reduced from 3600 to 1800”), generate intent summaries, map dependencies/impacts, and produce selective CI plans.
- The repo contains:
  - a Go CLI (`kai-cli`) that captures snapshots, computes diffs, and renders summaries
  - a shared Go core library (`kai-core`) for parsing, diffing, intent, and merging
  - a data-plane server (`kailab`) for remote storage and collaboration
  - a control-plane server (`kailab-control`) for auth/orgs/web UI
  - a playground and docs

## How it compares to Git (straight talk)
- **Not a source of truth:** Git still owns history, branching, and collaboration. Kai adds a parallel semantic model; it doesn’t replace Git’s commit graph or ecosystem.
- **Value proposition:** Git shows line diffs; Kai tries to show *semantic* change, which can be more useful for reviews, impact analysis, and test selection.
- **Scope limits:** The documented semantic parsing is focused on JavaScript/TypeScript with Tree-sitter. Other languages aren’t clearly first-class here.
- **Operational overhead:** You run extra tooling (CLI + optional servers) and maintain another database (SQLite + content-addressed objects). That’s more moving parts than Git alone.
- **Maturity risk:** Git is battle-tested for decades; Kai is comparatively new and has a much smaller user base. Expect rough edges.
- **Licensing impact:** GPLv3 may be a non-starter for some organizations depending on how they deploy or integrate the tooling.

## Why people might use it anyway
- **Better change understanding:** Semantic diffs and intent summaries can reduce review time and ambiguity.
- **Selective CI potential:** If their CI is slow/expensive, Kai’s test selection could be attractive (assuming it’s accurate enough for their risk tolerance).
- **AI tooling:** Semantic snapshots and stable symbol identity are useful for LLM-based tools that struggle with raw text diffs.
- **Large monorepos:** Impact maps and module-aware change grouping can help scale reviews and testing.

## Reasons to stick with plain Git
- **Ecosystem and stability:** Git is ubiquitous, supported everywhere, and extremely reliable.
- **Simplicity:** Git alone is simpler to operate and explain to teams.
- **Language coverage:** If your repo is not TS/JS-heavy or you need strong multi-language semantics, Kai’s current value is unclear.
- **Risk tolerance:** If missing tests or a wrong impact map is unacceptable, you may prefer full test suites and traditional diff review.

## Bottom line
Kai is a promising semantic layer that can make diffs, reviews, and CI smarter, but it’s not a replacement for Git and comes with operational and maturity trade-offs. It’s best for teams willing to experiment with semantic tooling in exchange for potentially faster reviews and cheaper CI—less ideal for teams that need maximum stability or broad language coverage.
