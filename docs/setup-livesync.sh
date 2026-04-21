#!/usr/bin/env bash
# Sets up /tmp/demo-{a,b,c,d} for the 4-agent live-sync demo.
# See docs/demo-livesync.md for the full recording guide.
#
# Usage: bash docs/setup-livesync.sh

set -euo pipefail

kai version | grep -qE '0\.(1[2-9]|[2-9][0-9])' || {
  echo "need kai >= 0.12.3"; exit 1
}

# Kill any MCP servers / tmux session left from a prior demo run. Otherwise
# their fsnotify watchers stay attached and flood the new sync log with
# skip-events (see docs/teardown-livesync.sh).
bash "$(dirname "$0")/teardown-livesync.sh"

rm -rf /tmp/demo-a /tmp/demo-b /tmp/demo-c /tmp/demo-d

# ── Agent A's dir is the seed: init, push once, then clone for b/c/d. ──
mkdir -p /tmp/demo-a && cd /tmp/demo-a
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

# Read the full URL kai just set up (URL + tenant + repo).
# Build a full https:// form because 'kai clone <org>/<repo>' shorthand
# is currently broken in the parser — see the bug in cmd/kai/main.go:~15200.
eval "$(kai remote get origin | awk '
  /URL:/    {print "URL="$2}
  /Tenant:/ {print "TENANT="$2}
  /Repo:/   {print "REPO="$2}
')"
FULL_URL="${URL%/}/$TENANT/$REPO"
echo "seed published at: $FULL_URL"

# ── Clone the same kai repo into /tmp/demo-{b,c,d}, then init a local
# git repo in each so all four dirs are symmetric. We use --kai-only
# because the kaicontext server doesn't serve git refs (kai push
# uploads the snapshot, not a git remote). Each B/C/D gets an
# independent local git history rooted at the scaffold state — fine
# for the demo, since agents sync via kai, not git. ──
for d in b c d; do
  kai clone "$FULL_URL" "/tmp/demo-$d" --kai-only
  (
    cd "/tmp/demo-$d"
    git init -q -b main
    git config user.email demo@demo
    git config user.name Demo
    git config commit.gpgsign false
    git add -A && git commit -q -m "scaffold"
  )
done

echo
echo "=== ready ==="
echo "  Open 4 Claude Code sessions, one in each of:"
for d in a b c d; do echo "    /tmp/demo-$d"; done
echo
echo "  Then in each Claude window, paste the first-prompt (join channel)"
echo "  from docs/demo-livesync.md."
