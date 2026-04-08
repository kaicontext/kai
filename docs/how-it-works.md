# How Kai Works

Kai is a semantic code intelligence engine. It builds a graph of your codebase — functions, classes, call relationships, imports — and exposes it to AI coding assistants via MCP.

## Snapshots

Everything starts with a **snapshot** — a frozen picture of your codebase at a point in time. When you run `kai capture`, it:

1. **Scans files** via a custom directory walker that skips unchanged subtrees using a memory-mapped binary stat cache (zero allocation on read, binary search on sorted entries)
2. **Creates a snapshot node** in the graph — a root node that owns everything below it
3. **Stores file contents** as content-addressed blobs (BLAKE3 hashed, deduplicated) in `.kai/objects/` for parseable files; non-parseable files compute digest only
4. **Creates File nodes** linked to the snapshot via `HAS_FILE` edges
5. **Records git metadata** (commit SHA, message, author, branch) on the snapshot ref

## Tree-sitter Parsing (`kai-core/parse/`)

After files are captured, `AnalyzeSymbols` and `AnalyzeCalls` run tree-sitter over every supported file:

- **Symbol extraction** — walks the AST and pulls out functions, classes, variables, methods, tables (SQL), etc. Each becomes a graph node with a `kind`, `name`, `signature`, and source `range`
- **Call extraction** — finds imports, call sites, and exports. These become edges between symbols

Supported languages: Go, Rust, TypeScript, JavaScript, Python, Ruby, SQL, PHP, C#.

Each language has its own import resolver:
- **Go**: package path matching against directory suffixes
- **Rust**: `crate::`, `super::`, `self::`, `mod` declarations, wildcards
- **Python**: dotted module path resolution
- **Ruby**: `require`, `require_relative`, Zeitwerk autoloading
- **TypeScript/JavaScript**: relative imports, workspace package resolution

## The Semantic Graph (`kai-core/graph/`)

Everything lives in a **directed graph** stored in SQLite:

- **Nodes**: Snapshot, File, Function, Class, Variable, etc. — each with a `kind`, `payload` (JSON), and content-addressed `digest`
- **Edges**: `HAS_FILE` (snapshot→file), `DEFINES_IN` (symbol→file), `CALLS` (file→file via call sites), `IMPORTS` (file→file), `TESTS` (test file→source file)

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
