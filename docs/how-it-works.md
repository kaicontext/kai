# How Kai Works

Kai is a semantic code intelligence engine. It builds a graph of your codebase — functions, classes, call relationships, imports — and exposes it to AI coding assistants via MCP.

## Snapshots

Everything starts with a **snapshot** — a frozen picture of your codebase at a point in time. When you run `kai capture`, it:

1. **Scans files** via `dirio` or `gitio` (respects `.gitignore` and `.kaiignore`)
2. **Creates a snapshot node** in the graph — a root node that owns everything below it
3. **Stores file contents** as content-addressed blobs (SHA-256 hashed, deduplicated) in `.kai/objects/`
4. **Creates File nodes** linked to the snapshot via `CONTAINS` edges

## Tree-sitter Parsing (`kai-core/parse/`)

After files are captured, `AnalyzeSymbols` and `AnalyzeCalls` run tree-sitter over every supported file:

- **Symbol extraction** — walks the AST and pulls out functions, classes, variables, methods, tables (SQL), etc. Each becomes a graph node with a `kind`, `name`, `signature`, and source `range`
- **Call extraction** — finds imports, call sites, and exports. These become edges between symbols

Supported languages: JavaScript/TypeScript, Python, Go, Ruby, Rust, SQL.

## The Semantic Graph (`kai-core/graph/`)

Everything lives in a **directed graph** stored in SQLite:

- **Nodes**: Snapshot, File, Function, Class, Variable, etc. — each with a `kind`, `payload` (JSON), and content-addressed `digest`
- **Edges**: `CONTAINS` (snapshot→file, file→symbol), `CALLS` (function→function), `IMPORTS` (file→file), `MODIFIES` (changeset→file)

This is what powers the MCP tools — `kai_callers` walks `CALLS` edges backward, `kai_callees` walks forward, `kai_dependents`/`kai_dependencies` walk `IMPORTS` edges.

## Semantic Diffing (`kai-core/diff/`)

When you have two snapshots, `diff` compares them at the **symbol level**, not line level:

1. Maps files by path between snapshots
2. For code files, parses both versions with tree-sitter
3. Compares symbols by name — detects added/removed/modified functions, classes, etc.
4. For modified symbols, does content comparison on the symbol's source range

The result is a **changeset** — a graph object that records which files and symbols changed between two snapshots.

## Change Detection (`kai-core/detect/`)

Builds on diffing to answer "what's affected by this change":

- Given a changeset, follows graph edges to find **impacted** symbols (callers of changed functions, dependents of changed modules)
- This is what `kai_impact` uses — it traces the blast radius of a change through the call graph

## Refs

Named pointers into the graph: `snap.latest`, `snap.main`, `cs.latest`, workspace refs. Same concept as git refs but for snapshots and changesets instead of commits.

## How MCP Tools Use This

When Claude calls `kai_callers("handleStatus")`:

1. MCP server finds the latest snapshot
2. Searches symbol nodes for one named `handleStatus`
3. Walks `CALLS` edges backward to find all symbols that call it
4. Returns the caller names, files, and line numbers

Everything is local, in SQLite, with no network calls. The graph is the index.
