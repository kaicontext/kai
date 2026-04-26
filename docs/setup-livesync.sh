#!/usr/bin/env bash
# Sets up /tmp/demo-{a,b,c,d} for the 4-agent live-sync demo.
# See docs/demo-livesync.md for the full recording guide.
#
# Usage: bash docs/setup-livesync.sh

set -euo pipefail

kai version | grep -qE '0\.(1[3-9]|[2-9][0-9])' || {
  echo "need kai >= 0.13.1 (for kai spawn)"; exit 1
}

# Kill any MCP servers / tmux session left from a prior demo run. Otherwise
# their fsnotify watchers stay attached and flood the new sync log with
# skip-events (see docs/teardown-livesync.sh).
bash "$(dirname "$0")/teardown-livesync.sh"

rm -rf /tmp/demo-source /tmp/demo-a /tmp/demo-b /tmp/demo-c /tmp/demo-d

# ── Seed: scaffold a publishable repo at /tmp/demo-source. This is what
# kai spawn pulls files from. The 4 agent dirs become CoW clones of the
# first spawn workspace; sync between them flows through this repo's
# kaicontext origin. ──
mkdir -p /tmp/demo-source && cd /tmp/demo-source
git init -q -b main
git config user.email demo@demo
git config user.name Demo
git config commit.gpgsign false

mkdir -p src tests docs
cat > src/greet.js <<'JS'
// TODO: implement greet(name)
JS
cat > tests/greet.test.js <<'JS'
// TODO: tests for greet(name)
JS
cat > docs/greet.md <<'MD'
# greet(name)

TODO: describe
MD
cat > README.md <<'MD'
# live-sync demo
Four agents build greet(name) together.
MD

git add -A && git commit -q -m "scaffold"

# kai init is interactive: it'll prompt for an org the first time.
# Pick one, or press Enter to skip if you already have a default.
kai init
kai capture -m "scaffold"
kai push

# ── Spawn the 4 agent workspaces from the just-pushed snapshot.
# Workspace 1 (a) is materialized via kai checkout from /tmp/demo-source's
# object store; b/c/d are CoW clones of a (APFS clone on macOS, reflink
# on btrfs/xfs). All four inherit the seed repo's origin remote, so
# they're on the same live-sync channel automatically. ──
kai spawn /tmp/demo-a /tmp/demo-b /tmp/demo-c /tmp/demo-d \
  --agent claude --sync full

echo
echo "=== ready ==="
echo "  Open 4 Claude Code sessions, one in each of:"
for d in a b c d; do echo "    /tmp/demo-$d"; done
echo
echo "  Then in each Claude window, paste the first-prompt (join channel)"
echo "  from docs/demo-livesync.md."
echo
echo "  Optional: in a separate terminal, run 'kai ui' (in any of the"
echo "  spawned dirs, or in /tmp/demo-source) for the dashboard view."
