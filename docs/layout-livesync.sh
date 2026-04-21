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
tmux kill-session -t "$SESSION" 2>/dev/null || true

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

# Reserve the bottom ~18% for the sync feed.
tmux split-window -v -l 18% -t "$SESSION:0" -c /tmp/demo-a

# Top pane (now pane 0) becomes the 2×2. Pane 1 is the feed.
tmux split-window -h -l 50% -t "$SESSION:0.0" -c /tmp/demo-b
tmux split-window -v -l 50% -t "$SESSION:0.0" -c /tmp/demo-c
tmux split-window -v -l 50% -t "$SESSION:0.2" -c /tmp/demo-d

# Wire each agent pane: clear + show which agent + cd into its dir.
# Pane indices after the splits above:
#   0 = Agent A  (top-left)
#   2 = Agent B  (top-right)
#   3 = Agent C  (bottom-left)
#   4 = Agent D  (bottom-right)
#   1 = Sync feed (bottom strip)
tmux send-keys -t "$SESSION:0.0" 'clear; printf "\033[1;31mAGENT A — backend\033[0m  (/tmp/demo-a)\n"' C-m
tmux send-keys -t "$SESSION:0.2" 'clear; printf "\033[1;34mAGENT B — tests\033[0m    (/tmp/demo-b)\n"' C-m
tmux send-keys -t "$SESSION:0.3" 'clear; printf "\033[1;32mAGENT C — frontend\033[0m (/tmp/demo-c)\n"' C-m
tmux send-keys -t "$SESSION:0.4" 'clear; printf "\033[1;33mAGENT D — docs\033[0m     (/tmp/demo-d)\n"' C-m

# Sync-feed pane: tail every agent's sync log, prefixed with the agent
# letter. jq is used when available to pretty-print; otherwise raw JSONL.
FEED_CMD=$(cat <<'EOS'
clear
printf "\033[1mSYNC FEED\033[0m  (push/recv across all four agents)\n\n"
TODAY=$(date +%Y-%m-%d)
if command -v jq >/dev/null 2>&1; then
  {
    for d in a b c d; do
      tail -F /tmp/demo-$d/.kai/sync-log/$TODAY.jsonl 2>/dev/null | \
        sed "s/^/$d /" &
    done
    wait
  } | jq -r --unbuffered '
    def color(e):
      if   e == "push"     then "\u001b[1;32m"
      elif e == "recv"     then "\u001b[2m"
      elif e == "merge"    then "\u001b[1;33m"
      elif e == "conflict" then "\u001b[1;31m"
      else                       "\u001b[0m" end;
    def tag(l): if l=="a" then "\u001b[31mA" elif l=="b" then "\u001b[34mB"
                elif l=="c" then "\u001b[32mC" elif l=="d" then "\u001b[33mD"
                else l end;
    (input_line_number|tostring) as $ln |
    . as $line |
    ($line | split(" ") | .[0]) as $letter |
    ($line | .[2:] | fromjson? // {event:"(parse err)", file:$line}) as $ev |
    "\(tag($letter))\u001b[0m \(color($ev.event))\( ($ev.timestamp // 0) / 1000 | strftime("%H:%M:%S") )  \($ev.event | ascii_upcase)  \($ev.file // "")\u001b[0m"
  '
else
  for d in a b c d; do
    tail -F /tmp/demo-$d/.kai/sync-log/$TODAY.jsonl 2>/dev/null | sed "s/^/[$d] /" &
  done
  wait
fi
EOS
)
tmux send-keys -t "$SESSION:0.1" "$FEED_CMD" C-m

echo "Attaching to tmux session '$SESSION'…"
echo "  Ctrl-B q  = show pane numbers"
echo "  Ctrl-B d  = detach (session keeps running)"
echo "  In each agent pane, run:  claude"
tmux attach -t "$SESSION"
