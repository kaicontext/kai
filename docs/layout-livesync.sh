#!/usr/bin/env bash
# Opens a tmux session "livesync" with the 4-agent layout described in
# docs/demo-livesync.md.
#
# Prereq: bash docs/setup-livesync.sh already populated
#         /tmp/demo-{a,b,c,d}.
#
# After this runs, you're attached to the tmux session. Each agent pane
# is in its working dir, ready for `claude`. Run `kai ui` in a separate
# terminal to see push/recv/merge/conflict + checkpoint activity across
# all four agents.

set -eu

command -v tmux >/dev/null 2>&1 || { echo "tmux not installed"; exit 1; }
for d in a b c d; do
  [ -d "/tmp/demo-$d" ] || {
    echo "missing /tmp/demo-$d — run docs/setup-livesync.sh first"; exit 1
  }
done

SESSION="livesync"

# Kill any MCP servers and the tmux session from a prior demo run. Skipping
# this leaves stale fsnotify watchers attached to /tmp/demo-* that flood the
# sync feed with skip events.
bash "$(dirname "$0")/teardown-livesync.sh"

# ── Build the layout ─────────────────────────────────────────────────
# Final shape (sync feed lives in `kai ui` now, not a tmux pane):
#
#   ┌──────────────┬──────────────┐
#   │  Agent A     │  Agent B     │   (one Claude per pane)
#   ├──────────────┼──────────────┤
#   │  Agent C     │  Agent D     │
#   └──────────────┴──────────────┘

tmux new-session -d -s "$SESSION" -x 240 -y 60 -c /tmp/demo-a
tmux rename-window -t "$SESSION:0" livesync

# Capture pane IDs as we create them so we don't depend on numeric
# indices (which shift under `pane-base-index 1` and across tmux
# versions). Pane IDs look like "%17" and are stable.
A=$(tmux display-message -p -t "$SESSION:0" '#{pane_id}')

# Build the 2×2.
B=$(tmux split-window -h -l 50% -t "$A" -c /tmp/demo-b -P -F '#{pane_id}')
C=$(tmux split-window -v -l 50% -t "$A" -c /tmp/demo-c -P -F '#{pane_id}')
D=$(tmux split-window -v -l 50% -t "$B" -c /tmp/demo-d -P -F '#{pane_id}')

# Wire each agent pane: clear + show which agent + cd into its dir.
tmux send-keys -t "$A" 'clear; printf "\033[1;31mAGENT A — backend\033[0m  (/tmp/demo-a)\n"' C-m
tmux send-keys -t "$B" 'clear; printf "\033[1;34mAGENT B — tests\033[0m    (/tmp/demo-b)\n"' C-m
tmux send-keys -t "$C" 'clear; printf "\033[1;32mAGENT C — frontend\033[0m (/tmp/demo-c)\n"' C-m
tmux send-keys -t "$D" 'clear; printf "\033[1;33mAGENT D — docs\033[0m     (/tmp/demo-d)\n"' C-m

echo "Attaching to tmux session '$SESSION'…"
echo "  Ctrl-B q  = show pane numbers"
echo "  Ctrl-B d  = detach (session keeps running)"
echo "  In each agent pane, run:  claude"
echo
echo "  For the live activity feed, run 'kai ui' in a separate terminal."
tmux attach -t "$SESSION"
