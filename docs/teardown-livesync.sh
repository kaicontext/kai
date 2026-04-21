#!/usr/bin/env bash
# Kills MCP servers and tmux session left over from a previous livesync demo.
# Scoped to /tmp/demo-* so it can't touch real MCP servers you use for work.
#
# Called from setup-livesync.sh and layout-livesync.sh before they rebuild
# state. Safe to run standalone: `bash docs/teardown-livesync.sh`.

set -u

tmux kill-session -t livesync 2>/dev/null || true

# Find `kai mcp serve` processes whose cwd is inside /tmp/demo-*. We resolve
# cwd via lsof because ps on macOS doesn't expose it. Anything outside the
# demo dirs is left alone — a developer may have a real MCP server open.
pids=$(pgrep -f "kai mcp serve" 2>/dev/null || true)
for pid in $pids; do
  cwd=$(lsof -p "$pid" -d cwd -Fn 2>/dev/null | awk '/^n/ {print substr($0,2); exit}')
  case "$cwd" in
    /tmp/demo-a|/tmp/demo-b|/tmp/demo-c|/tmp/demo-d|/tmp/demo-a/*|/tmp/demo-b/*|/tmp/demo-c/*|/tmp/demo-d/*)
      kill "$pid" 2>/dev/null || true
      ;;
  esac
done
