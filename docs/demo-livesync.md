# Kai Live Sync — 4-Agent Demo

**Length:** ~2:30
**Audience:** anyone who's watched two AI agents edit the same codebase and wondered how the hell that could possibly work.
**The one thing they should leave with:** *Four agents can work in the same repo, at the same time, without anyone writing a merge resolver.*

---

## The argument in one paragraph

An AI agent is a great way to finish one task quickly. Four AI agents are, in theory, a great way to finish four tasks quickly — except the moment you run them against the same codebase, you get race conditions, clobbered files, and contradictory edits that someone still has to reconcile. The industry's current answer is "run them one at a time," which is another way of saying "don't use the parallelism that's right in front of you." Kai's answer is different: let them work simultaneously, semantically merge their edits in real time, and track who did what so you can actually review the result.

This demo shows four Claude Code agents building one small feature together — live, in the same repo, via kai's MCP live-sync channel.

---

## Setup

### Prerequisites

- `kai` ≥ 0.12.3 on PATH
- Four independent Claude Code sessions (four terminal windows, four tabs, or a tmux 2×2)
- The kai MCP server already configured in your Claude Code install
- A kaicontext account you're logged into (`kai auth status` should show your email)

### Layout — don't just show 4 claude prompts

The problem with a naive 2×2 grid of Claude sessions is that **sync is invisible**. When agent A saves a file and agent B's kai quietly writes that file to B's disk, nothing on B's screen changes until B's Claude decides to re-read the tree. The viewer never sees the magic happen. So this demo uses a slightly more involved layout.

Each of the four agent cells is split into **two stacked panes**:

```
┌─────────────────────────────┬─────────────────────────────┐
│ ┃A─ Claude Code (top, 55%)  │ ┃B─ Claude Code (top, 55%)  │
│ ┃                           │ ┃                           │
│ ├─ live filesystem (45%) ───┤ ├─ live filesystem (45%) ───┤
│ ┃ src/greet.js   mtime      │ ┃ src/greet.js   mtime      │
│ ┃ contents…                 │ ┃ contents…                 │
├─────────────────────────────┼─────────────────────────────┤
│ ┃C─ Claude Code              │ ┃D─ Claude Code              │
│ ┃                           │ ┃                           │
│ ├─ live filesystem ─────────┤ ├─ live filesystem ─────────┤
│ ┃ …                          │ ┃ …                          │
└─────────────────────────────┴─────────────────────────────┘
            │
            ▼  fifth pane below or to the side:
  ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
  ┃ SYNC FEED  (live SSE stream, all 4 agents)           ┃
  ┃ 13:21:04  [A → sync]  write src/greet.js (412 B)     ┃
  ┃ 13:21:05  [B ← sync]  recv  src/greet.js             ┃
  ┃ 13:21:05  [C ← sync]  recv  src/greet.js             ┃
  ┃ 13:21:05  [D ← sync]  recv  src/greet.js             ┃
  ┃ 13:21:12  [B → sync]  write tests/greet.test.js      ┃
  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
```

Three things this buys you on camera:

1. **The filesystem pane updates when sync lands**, not when the agent decides to read. Viewers see the file *arrive*.
2. **The sync feed** is a single narrative column that lists every send/receive across all four agents, timestamped. It's the running subtitle of the whole demo.
3. **Color-code the four agents** (A=red, B=blue, C=green, D=yellow) and carry those colors everywhere — pane borders, sync-feed rows, voiceover callouts. Without colors, a viewer staring at a 2×2 grid can't tell which agent wrote what.

### How to build it

Easiest path: tmux, one pre-made session. Save this as `demo-layout.sh` alongside the setup block:

```bash
#!/bin/sh
tmux new-session -d -s livesync -x 240 -y 60
# Top-left: Agent A split vertically (Claude 55%, watcher 45%)
tmux send-keys -t livesync 'cd /tmp/demo-a && clear && echo "AGENT A — backend"' C-m
tmux split-window -v -p 45 -t livesync \
  "cd /tmp/demo-a && watch -n 0.3 -t -c 'printf \"\\033[31m┃ A \\033[0m\\n\"; ls -la --color=always src tests docs 2>/dev/null; echo ---; cat src/greet.js 2>/dev/null | head -20'"

# Top-right: Agent B
tmux split-window -h -t livesync:0.0
tmux send-keys -t livesync:0.2 'cd /tmp/demo-b && clear && echo "AGENT B — tests"' C-m
tmux split-window -v -p 45 -t livesync:0.2 \
  "cd /tmp/demo-b && watch -n 0.3 -t -c 'printf \"\\033[34m┃ B \\033[0m\\n\"; ls -la --color=always src tests docs 2>/dev/null; echo ---; cat tests/greet.test.js 2>/dev/null | head -20'"

# Bottom-left: Agent C
tmux split-window -v -p 50 -t livesync:0.0
tmux send-keys -t livesync:0.4 'cd /tmp/demo-c && clear && echo "AGENT C — frontend"' C-m
tmux split-window -v -p 45 -t livesync:0.4 \
  "cd /tmp/demo-c && watch -n 0.3 -t -c 'printf \"\\033[32m┃ C \\033[0m\\n\"; ls -la --color=always src tests docs 2>/dev/null; echo ---; cat src/App.jsx 2>/dev/null | head -20'"

# Bottom-right: Agent D
tmux split-window -v -p 50 -t livesync:0.2
tmux send-keys -t livesync:0.6 'cd /tmp/demo-d && clear && echo "AGENT D — docs"' C-m
tmux split-window -v -p 45 -t livesync:0.6 \
  "cd /tmp/demo-d && watch -n 0.3 -t -c 'printf \"\\033[33m┃ D \\033[0m\\n\"; ls -la --color=always src tests docs 2>/dev/null; echo ---; cat docs/greet.md 2>/dev/null | head -20'"

tmux attach -t livesync
```

The `watch -n 0.3` redraws each filesystem pane every 300 ms. When a file arrives from sync, its `mtime` in the `ls -la` line jumps to the current second — *that is the sync event, visible*.

### The sync feed (the fifth pane)

Kai writes every live-sync event to a JSONL log at `.kai/sync-log/YYYY-MM-DD.jsonl`. Each line is one event; the interesting fields are `event` (`push` / `recv` / `merge` / `conflict` / `skip`), `file`, `agent`, and `timestamp`. Tail all four agents at once:

```bash
# Paste into a fifth tmux pane or a dedicated terminal window.
# jq is the pretty-printer; if you don't have it, fall back to the
# raw `tail -F` block further down.
{
  for d in a b c d; do
    tail -F /tmp/demo-$d/.kai/sync-log/*.jsonl 2>/dev/null &
  done
  wait
} | jq -r --unbuffered '
  # Strip the long agent id down to a single letter based on the dir
  # (agents self-name; the filename path in the log tells us which
  # working dir they came from). Simpler and demo-safe: color by event.
  def color(e):
    if   e == "push"     then "\u001b[1;32m"   # bold green = outgoing
    elif e == "recv"     then "\u001b[2m"      # dim       = incoming
    elif e == "merge"    then "\u001b[1;33m"   # bold yellow
    elif e == "conflict" then "\u001b[1;31m"   # bold red
    else                       "\u001b[0m" end;
  "\(color(.event))\(.timestamp | (./1000) | strftime("%H:%M:%S"))  \(.event | ascii_upcase)  \(.file) \u001b[0m"
'
```

If you don't have `jq` on the demo machine, raw tail works fine and is still legible:

```bash
for d in a b c d; do
  tail -F /tmp/demo-$d/.kai/sync-log/*.jsonl 2>/dev/null | sed "s/^/[$d] /" &
done
wait
```

Poor-man's feed, but on screen it still reads `[a] {"event":"push","file":"src/greet.js",...}` followed a half-second later by three `[b/c/d] {"event":"recv",...}` lines — which is all the demo needs.

### Want a summary instead of per-event lines?

`kai live status` (no `--follow` flag yet) prints a compacted view of the sync_events log since the last capture. Wrap it with `watch` for a 500 ms-refreshing status panel:

```bash
cd /tmp/demo-a
watch -n 0.5 -t kai live status
```

It won't show the hero "file just arrived" moment as cleanly as the tail-based feed — use one, not both.

### One more clever trick: the "diff heartbeat"

If you want the filesystem panes to do even more work, swap `cat` for a `diff` against the previous state:

```bash
# Replace the `cat src/greet.js` call in each watcher with:
diff -u .prev-greet src/greet.js 2>/dev/null | tail -15 || cat src/greet.js 2>/dev/null
cp src/greet.js .prev-greet 2>/dev/null
```

Now each filesystem pane shows **only the lines that just changed**, in git-diff style (red `-`, green `+`). A sync arrival isn't just "file got newer" — it's "these five lines appeared, this second, on my disk, without me doing anything."

It's a little more mise-en-place but it's the difference between the viewer *seeing* sync happen and the viewer *feeling* it.

### Post-production flash (optional, but makes it pop)

If you're editing the recording after the fact: detect the frame where each filesystem pane's `mtime` changes and add a 200 ms colored border pulse matching that agent's color. Even without a full VFX pass, a simple drop-shadow flash makes the "it appeared" moment land hard.

### Color assignments (use these everywhere)

| Agent | Role     | ANSI code   | On-screen cue            |
|-------|----------|-------------|--------------------------|
| A     | backend  | `\033[31m`  | red border, red in feed  |
| B     | tests    | `\033[34m`  | blue                     |
| C     | frontend | `\033[32m`  | green                    |
| D     | docs     | `\033[33m`  | yellow                   |

Stick to this palette. In the sync feed, in the pane borders, in the voiceover callouts. Consistency is what lets a viewer track four things at once for two minutes.

### One-shot setup (paste into a fifth, temporary shell)

```bash
set -e
kai version | grep -qE '0\.(1[2-9]|[2-9][0-9])' || { echo "need kai >= 0.12.3"; exit 1; }

# Bare repo that all four agents will sync through kaicontext.com
rm -rf /tmp/demo-seed /tmp/demo-a /tmp/demo-b /tmp/demo-c /tmp/demo-d
mkdir /tmp/demo-seed && cd /tmp/demo-seed
git init -q -b main
git config user.email demo@demo && git config user.name Demo
git config commit.gpgsign false

# Scaffolding small enough that every agent fits on one screen
mkdir -p src tests docs
cat > src/greet.js <<'EOF'
// TODO: implement greet(name)
EOF
cat > tests/greet.test.js <<'EOF'
// TODO: tests for greet(name)
EOF
cat > docs/greet.md <<'EOF'
# greet(name)

TODO: describe
EOF
cat > README.md <<'EOF'
# live-sync demo
Four agents build greet(name) together.
EOF

git add -A && git commit -q -m "scaffold"
kai init                   # first run: interactive org pick
kai capture -m "scaffold"
kai push

# Clone into four working dirs. Each will be one agent's workspace.
for d in a b c d; do
  cp -r /tmp/demo-seed /tmp/demo-$d
  # Independent kai databases per agent — they meet on the kaicontext server,
  # not on the filesystem. Reset local .kai so each agent initializes fresh.
  rm -rf /tmp/demo-$d/.kai /tmp/demo-$d/.git
  cd /tmp/demo-$d
  git init -q -b main && git config user.email demo@demo && git config user.name Demo
  git config commit.gpgsign false
  git add -A && git commit -q -m scaffold
  # Point origin at the kaicontext repo we just pushed
  KAIREPO=$(cd /tmp/demo-seed && grep -oE 'remote/origin/.*' .kai/db.sqlite 2>/dev/null | head -1 || echo "")
  kai remote set origin $(cd /tmp/demo-seed && kai remote get origin 2>/dev/null || echo "")
  kai fetch origin 2>&1 | tail -1
done

echo "=== ready: open 4 Claude Code sessions, one in each /tmp/demo-{a,b,c,d} ==="
```

(If the `kai remote set`/`fetch` dance is flaky at your install, just `kai clone` the same kaicontext repo four times into the four paths — the point is each agent ends up pointing at the same remote.)

### Enable live-sync in each Claude session

Open a Claude Code window in each directory, then give each one its *first* prompt (verbatim) so they all join the same sync channel:

```
Use kai_live_sync with channel="greet-demo" to join the sync channel,
then wait for your role prompt.
```

All four agents are now on the same channel. Any file they write gets pushed via `/v1/sync/push`, and the other three receive it in <1s.

---

## The four role prompts

Have these ready in your paste buffer — each one goes into its specific window after Claude acknowledges it's on the `greet-demo` channel.

### Agent A — backend

> You are the backend agent. Your job is to implement `src/greet.js` so the function `greet(name)` returns `` `hi, ${name}` `` when called. No frills. When it's working, call `kai_checkpoint_now` with `label="greet: implementation"` and `assert=tests-pass` (only include the assert if tests actually pass — you can check by reading `tests/greet.test.js` after agent B pushes it).

### Agent B — tests

> You are the tests agent. Your job is to fill in `tests/greet.test.js` with at least two test cases for `greet(name)`: one for a normal name, one for an empty string. Watch `src/greet.js` — agent A will update it shortly. Once the tests match the implementation, call `kai_checkpoint_now` with `label="greet: tests"`.

### Agent C — frontend

> You are the frontend agent. Read `src/greet.js` once agent A has updated it, then create `src/App.jsx` that imports `greet` and renders `<h1>{greet("world")}</h1>`. When done, call `kai_checkpoint_now` with `label="greet: frontend wired"`.

### Agent D — docs

> You are the docs agent. Watch `src/greet.js`, `tests/greet.test.js`, and `src/App.jsx`. As each lands, update `docs/greet.md` with a short description of the function's signature, its behavior, and one call-site example. When all three source files are in place and your docs reference all of them, call `kai_checkpoint_now` with `label="greet: docs"`.

None of these prompts mentions the other agents by name. They only know the files on disk. Sync makes the choreography work.

---

## Storyboard

```
[0:00 – 0:15]   Title card + one-paragraph framing (read voiceover).
                Cut to the 2×2 grid with all four empty Claude prompts.

[0:15 – 0:30]   All four agents get their "join channel" prompt. Each
                one calls kai_live_sync. Subtle hook highlight on the
                MCP tool call in each window.

[0:30 – 0:45]   Paste the four role prompts into their respective
                windows simultaneously. Each Claude starts "thinking."

[0:45 – 1:15]   HERO SHOT.
                Agent A writes src/greet.js.
                In the SYNC FEED: a red "[A → sync]" line appears.
                ~300 ms later: three dim lines appear in the feed
                  "[B ← sync] recv src/greet.js"
                  "[C ← sync] recv src/greet.js"
                  "[D ← sync] recv src/greet.js"
                IN THE SAME FRAME: the filesystem panes of B, C, D
                each flash their border (post-production), and
                src/greet.js appears in their ls -la with a fresh
                mtime.

                THIS IS THE MOMENT. The voiceover shuts up for ~2s.
                Let the viewer watch the file physically arrive on
                three other disks.

                Then agents B, C, D's Claude windows each start
                typing — not because we prompted them, but because
                they noticed the new file on their own tree.

[1:15 – 1:45]   Agents B, C, D finish their pieces. Each fires
                kai_checkpoint_now with a labeled milestone. The
                labels appear in each other's sync activity.

[1:45 – 2:15]   Back to setup shell, run:
                  kai activity
                  kai ref list | head
                Show the four milestone checkpoints + the four git.HEAD
                snapshots, all within the last two minutes, by four
                different agents.

[2:15 – 2:30]   Payoff card:
                  "Four agents. One repo. Zero merge conflicts."
                  CTA: get.kaicontext.com
```

---

## Voiceover — 80 words, ~30 seconds at a natural pace

> "One AI agent is fast. Four agents, in theory, should be four times faster. In practice, they fight — race conditions, clobbered files, contradictory edits. So most teams run them one at a time.
>
> Kai is a different answer. Four Claude Code agents, same repo, same sync channel. Each one picks up the others' work the second it lands. Semantic merge handles the overlaps. Milestone checkpoints mark who did what, so review is sane.
>
> Four agents. One repo. Zero merge conflicts."

Hit the `[beat]` pauses between sentences. The hero shot in Scene 4 — file appearing on three other screens — is the one the voiceover should **not** compete with. Let the visual carry it.

---

## What's actually happening under the hood

1. **`kai_live_sync` (MCP tool)** — opens an SSE connection from the agent's kai session to `kaicontext.com/<org>/<repo>/v1/sync/live`. Every file the agent writes is pushed via `/v1/sync/push`; every file the channel receives is applied to the local working tree.
2. **Merge strategy** — when two agents change the same file, kai runs a semantic 3-way merge against the last common snapshot. For JS/TS/Python/Ruby/Rust/Go that's function-level. For JSON/YAML/Markdown it's a naive line-merge that applies cleanly when the edited regions don't overlap. A true conflict logs to `kai activity` and leaves the local edits preserved.
3. **`kai_checkpoint_now` (MCP tool)** — emits a labeled milestone on the sync channel. No file write, just a marker. Other agents' activity feeds show it and can use it as a coordination signal.
4. **No coordination protocol between agents.** There's no mutex, no lock, no chat. Each agent reads the filesystem, writes the filesystem, and kai ferries the bytes and merges when they collide. That's the whole thing.

---

## Recording notes

- **Pre-record the Claude responses if you need tight timing.** Live LLMs have variable latency; a 30-second response ruins the 2:30 pace. Recording once clean and stitching is fine — the sync itself is the demo, not Claude's speed.
- **Highlight the sync moments.** When agent A saves `src/greet.js` and it appears on B/C/D, add a brief pulse or border flash on the receiving windows. Viewers need to see that the file arrived *without an agent being told to pull*.
- **Watch for the `[kai-sync]` lines on stderr.** Kai prints `[kai-sync] merged foo.js (auto-resolved)` when a semantic merge fires. If your demo produces one of these organically, consider cutting to a close-up — it's the unsung money shot.
- **Don't try to demo a true conflict.** Live merging two competing signature changes is interesting but chaotic on camera. Save it for the 5-minute deep dive.
- **Dark terminal, ≥ 18pt, high contrast.** The 2×2 grid cuts per-window legibility in half. If the 30-second tweet demo needed 18pt, this one needs 22pt.

## What to say if someone pushes back

- *"Couldn't I just use git branches?"* → Four branches means four merges. Live sync means zero merges, because the merge happens continuously and never accumulates. Different shape of problem.
- *"This is basically live-share / Figma for code."* → Yes, and the interesting part is that the participants aren't humans reading each other's cursors — they're agents reading each other's *code*. The substrate is the same; the load on it is very different.
- *"Does it work with more than four agents?"* → Yes. The sync channel is fanout-ish; the limit is whatever the server-side `/v1/sync/live` SSE can handle. We haven't found the ceiling.
- *"What if an agent writes garbage?"* → Same failure mode as any tool handing code to an agent. Kai tracks authorship so you know which agent wrote what line — filter them out, revert their checkpoint, ban their MCP session. Authorship is the answer here, not prevention.

---

## Troubleshooting

- **Agent says `kai_live_sync` isn't available.** Your Claude Code `.mcp.json` is missing the kai MCP server entry. Run `kai mcp install` (if the command exists in your version) or manually add:
  ```json
  {
    "mcpServers": {
      "kai": { "command": "kai", "args": ["mcp", "serve"] }
    }
  }
  ```
- **Files don't appear on other agents' disks.** Each agent must be on the same channel name. Run `kai ref list | grep sync` to confirm. If channels differ, re-prompt each agent with the same `channel="greet-demo"`.
- **Merge conflict messages on console.** Expected — kai logs conflicts for visibility. Local edits are preserved; re-prompt the affected agent to reconcile against the on-disk state.
- **Usage: 140 / 150 agent sync events.** Each agent write is one sync event. Four agents editing actively will burn through free-tier budget in a long demo. Upgrade or set `KAI_TELEMETRY=0` for recording.
