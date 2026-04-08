# Kai MCP Server

Kai exposes its semantic graph to AI coding assistants via the [Model Context Protocol (MCP)](https://modelcontextprotocol.io). This gives tools like Claude Code and Cursor access to call graphs, dependency maps, impact analysis, and test coverage.

## Install

### One-liner (Claude Code + npx)

```bash
claude mcp add kai -- npx -y kai-mcp
```

### With Kai binary installed

```bash
# Install kai
curl -sSL https://get.kaicontext.com | sh

# Register with Claude Code
claude mcp add kai -- kai mcp serve
```

### Homebrew

```bash
brew install kaicontext/kai/kai
claude mcp add kai -- kai mcp serve
```

## Editor Setup

### Claude Code

```bash
claude mcp add kai -- kai mcp serve
```

Or with npx (no binary install needed):

```bash
claude mcp add kai -- npx -y kai-mcp
```

### Cursor

Add to `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "kai": {
      "command": "kai",
      "args": ["mcp", "serve"]
    }
  }
}
```

Or with npx:

```json
{
  "mcpServers": {
    "kai": {
      "command": "npx",
      "args": ["-y", "kai-mcp"]
    }
  }
}
```

### Generic stdio

Any MCP client that supports stdio transport:

```bash
kai mcp serve
```

## Tools

### Semantic Graph Tools

| Tool | Description |
|------|-------------|
| `kai_symbols` | List symbols in a file (functions, classes, structs, traits, methods, macros). Filter by `kind` or `exported`. |
| `kai_callers` | Find all callers of a symbol. Walks CALLS edges — more accurate than grep. |
| `kai_callees` | Find all symbols called by a symbol. |
| `kai_dependents` | Find files that import/depend on a file. "What breaks if I change this?" |
| `kai_dependencies` | Find files a file imports. "What does this file need?" |
| `kai_tests` | Find test files covering a source file. Uses import graph + filename patterns. |
| `kai_context` | Bundled context for a file/symbol: callers, callees, tests, dependencies in one call. When a symbol is specified, returns only that symbol's info (not all symbols in the file). |
| `kai_impact` | Transitive downstream impact analysis with hop distance. Uses batch SQL queries for performance on large repos. |

### AI Authorship Tools

| Tool | Description |
|------|-------------|
| `kai_checkpoint` | Record an AI edit event (file, line range, agent, model). Usually auto-detected — see below. |
| `kai_blame` | Show AI vs human authorship for a file. Per-line ranges or summary percentages. |
| `kai_stats` | Project-wide AI vs human authorship statistics with per-agent breakdowns. |

### Auto AI Authorship Detection

When an MCP session is active and `kai capture` runs (via the pre-commit hook), Kai automatically attributes changed files to the AI agent. No `kai_checkpoint` calls needed. The MCP server writes a session file that `kai capture` checks — if the session was active within the last 5 minutes, changed files are auto-attributed.

### Output Limits

All list outputs are capped at 50 items by default to stay within MCP client token limits. Total counts are always shown (e.g., `"dependents_total": 725`). `kai_context` in focused mode (with a `symbol` parameter) returns only the focused symbol's info instead of all symbols in the file.

## Lazy Initialization

No setup required before using the MCP server. When `kai mcp serve` starts:

1. If `.kai/` exists with a valid database, it opens instantly.
2. If `.kai/` doesn't exist, the first data request (e.g., `kai_symbols`) triggers background initialization.
3. While initializing, `kai_status` reports progress. Other tools return a message indicating init is in progress.
4. Once complete, all tools work normally.

This means you can register the MCP server and start using it immediately — even in a fresh clone.

## CLI Equivalents

The most common MCP queries are also available as CLI commands for debugging and scripting:

```bash
kai query callers getUser                 # same as kai_callers
kai query dependents services/user.ts     # same as kai_dependents
kai query impact shared/types/user.ts     # same as kai_impact
```

See [cli-reference.md](cli-reference.md) for full documentation.

## Troubleshooting

### "kai: command not found"

Install kai:
```bash
curl -sSL https://get.kaicontext.com | sh
```

Or use the npx wrapper which bundles the binary:
```bash
claude mcp add kai -- npx -y kai-mcp
```

### Graph is stale

The AI assistant can call `kai_status` to check freshness and `kai_refresh` to update. You can also run `kai capture` manually.

### Initialization is slow

First-time indexing parses all source files to build the semantic graph. Subsequent starts are instant. For large repos, expect 10-30 seconds on first use.

### Server doesn't start

Check that kai is working:
```bash
kai version
kai mcp serve
```

The server communicates over stdio (stdin/stdout). It will appear to hang when run directly — that's normal. It's waiting for JSON-RPC messages from the MCP client.
