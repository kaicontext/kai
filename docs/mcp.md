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

| Tool | Description |
|------|-------------|
| `kai_symbols` | List symbols in a file (functions, classes, methods). Filter by kind or exported status. |
| `kai_callers` | Find all callers of a symbol. Walks CALLS edges — more accurate than grep. |
| `kai_callees` | Find all symbols called by a symbol. |
| `kai_dependents` | Find files that import/depend on a file. "What breaks if I change this?" |
| `kai_dependencies` | Find files a file imports. "What does this file need?" |
| `kai_tests` | Find test files covering a source file. Uses static analysis and coverage data. |
| `kai_diff` | Semantic diff between two snapshots/refs. Symbol-level changes, not line diffs. |
| `kai_context` | Bundled context for a file/symbol: callers, callees, tests, dependencies in one call. |
| `kai_impact` | Transitive downstream impact analysis with hop distance. |
| `kai_status` | Check graph freshness: last capture time, branch, stale files. |
| `kai_refresh` | Re-capture the semantic graph. Supports full or staged-only scope. |

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
