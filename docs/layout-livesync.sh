#!/usr/bin/env bash
# Opens a tmux session "livesync" with the 4-agent + sync-feed layout
# described in docs/demo-livesync.md.
#
# Prereq: bash docs/setup-livesync.sh already populated
#         /tmp/demo-{a,b,c,d}.
#
# After this runs, you're attached to the tmux session. Each agent pane
# is in its working dir, ready for `claude`. A bottom strip tails the
# sync logs across all four agents.

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
# Final shape:
#
#   ┌──────────────┬──────────────┐
#   │  Agent A     │  Agent B     │   (top 2×2: one Claude per pane)
#   ├──────────────┼──────────────┤
#   │  Agent C     │  Agent D     │
#   ├──────────────┴──────────────┤
#   │  SYNC FEED  (tail -F)       │   (bottom strip: combined feed)
#   └─────────────────────────────┘

tmux new-session -d -s "$SESSION" -x 240 -y 60 -c /tmp/demo-a
tmux rename-window -t "$SESSION:0" livesync

# Capture pane IDs as we create them so we don't depend on numeric
# indices (which shift under `pane-base-index 1` and across tmux
# versions). Pane IDs look like "%17" and are stable.
A=$(tmux display-message -p -t "$SESSION:0" '#{pane_id}')

# Reserve the bottom ~35% for the sync feed (it's the narration track
# of the demo — it needs to be tall enough to actually read).
FEED=$(tmux split-window -v -l 35% -t "$A" -c /tmp/demo-a -P -F '#{pane_id}')

# Turn the top pane into a 2×2.
B=$(tmux split-window -h -l 50% -t "$A" -c /tmp/demo-b -P -F '#{pane_id}')
C=$(tmux split-window -v -l 50% -t "$A" -c /tmp/demo-c -P -F '#{pane_id}')
D=$(tmux split-window -v -l 50% -t "$B" -c /tmp/demo-d -P -F '#{pane_id}')

# Wire each agent pane: clear + show which agent + cd into its dir.
tmux send-keys -t "$A" 'clear; printf "\033[1;31mAGENT A — backend\033[0m  (/tmp/demo-a)\n"' C-m
tmux send-keys -t "$B" 'clear; printf "\033[1;34mAGENT B — tests\033[0m    (/tmp/demo-b)\n"' C-m
tmux send-keys -t "$C" 'clear; printf "\033[1;32mAGENT C — frontend\033[0m (/tmp/demo-c)\n"' C-m
tmux send-keys -t "$D" 'clear; printf "\033[1;33mAGENT D — docs\033[0m     (/tmp/demo-d)\n"' C-m

# Sync-feed pane: tail every agent's sync log, prefixed with the agent
# letter. jq is used when available to pretty-print; otherwise raw JSONL.
#
# We write the feed program to a file and exec it instead of piping the
# whole multi-line if/then/fi block through `send-keys` — typing that
# into an interactive shell is fragile (PS2 continuation hazards,
# quoting interactions). A script file is parsed as a single unit.
FEED_SCRIPT="/tmp/demo-livesync-feed.sh"
cat >"$FEED_SCRIPT" <<'EOS'
#!/usr/bin/env bash
clear
printf "\033[1mSYNC FEED\033[0m  (push/recv across all four agents)\n\n"
TODAY=$(date +%Y-%m-%d)
if command -v jq >/dev/null 2>&1; then
  {
    for d in a b c d; do
      tail -F "/tmp/demo-$d/.kai/sync-log/$TODAY.jsonl" 2>/dev/null \
        | awk -v p="$d " '{print p $0; fflush()}' &
    done
    wait
  } | jq -R -r --unbuffered '
    def color(e):
      if   e == "push"     then "[1;32m"
      elif e == "receive"  then "[2m"
      elif e == "merge"    then "[1;33m"
      elif e == "conflict" then "[1;31m"
      else                      "[0m" end;
    def tag(l):
      if   l == "a" then "[31mA"
      elif l == "b" then "[34mB"
      elif l == "c" then "[32mC"
      elif l == "d" then "[33mD"
      else l end;
    . as $line
    | ($line | split(" ") | .[0]) as $letter
    | ($line | .[2:] | fromjson? // {event:"(parse err)", file:$line}) as $ev
    | select($ev.event != "skip")
    | "\(tag($letter))[0m \(color($ev.event))\(($ev.timestamp // 0) / 1000 | strftime("%H:%M:%S"))  \($ev.event | ascii_upcase)  \($ev.file // "")[0m"
  '
else
  for d in a b c d; do
    tail -F "/tmp/demo-$d/.kai/sync-log/$TODAY.jsonl" 2>/dev/null \
      | awk -v p="[$d] " '{print p $0; fflush()}' &
  done
  wait
fi
EOS
chmod +x "$FEED_SCRIPT"
tmux send-keys -t "$FEED" "exec bash $FEED_SCRIPT" C-m

echo "Attaching to tmux session '$SESSION'…"
echo "  Ctrl-B q  = show pane numbers"
echo "  Ctrl-B d  = detach (session keeps running)"
echo "  In each agent pane, run:  claude"
tmux attach -t "$SESSION"
