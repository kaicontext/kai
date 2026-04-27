# Kai Live Sync — 4-Agent Demo

**Length:** ~2:30
**Audience:** anyone who's watched two AI agents edit the same codebase and wondered how the hell that could possibly work.
**The one thing they should leave with:** *Four agents can work in the same repo, at the same time, without anyone writing a merge resolver.*

---

## TL;DR — run the whole thing

Two scripts, in order, from the kai repo root:

```bash
# 1. Build the four agent working dirs (interactive on first run:
#    kai init will ask you to pick an org).
bash docs/setup-livesync.sh

# 2. Open tmux with the 4-agent + sync-feed layout.
bash docs/layout-livesync.sh
```

You're now attached to a tmux session `livesync`. Inside:

1. In each of the four agent panes, run: `claude`
2. Paste the **"turn on live-sync"** prompt (section below) into all four Claude windows.
3. Paste the **four role prompts** (Agent A–D, also below) into their respective windows.
4. Watch the bottom sync-feed pane for `PUSH` / `RECV` events as the agents build `greet(name)` together.

Detach with `Ctrl-B d`, reattach with `tmux attach -t livesync`, kill with `tmux kill-session -t livesync`.

---

## The argument in one paragraph

An AI agent is a great way to finish one task quickly. Four AI agents are, in theory, a great way to finish four tasks quickly — except the moment you run them against the same codebase, you get race conditions, clobbered files, and contradictory edits that someone still has to reconcile. The industry's current answer is "run them one at a time," which is another way of saying "don't use the parallelism that's right in front of you." Kai's answer is different: let them work simultaneously, semantically merge their edits in real time, and track who did what so you can actually review the result.

This demo shows four Claude Code agents building one small feature together — live, in the same repo, via kai's MCP live-sync channel.

---

## Setup

### Prerequisites

- `kai` ≥ 0.13.1 on PATH (needs `kai spawn`)
- Four independent Claude Code sessions (four terminal windows, four tabs, or a tmux 2×2)
- The kai MCP server already configured in your Claude Code install
- A kaicontext account you're logged into (`kai auth status` should show your email)

### Layout

`docs/layout-livesync.sh` (invoked from the TL;DR above) builds this:

```
┌─────────────────────────────┬─────────────────────────────┐
│  AGENT A — backend          │  AGENT B — tests            │
│  (/tmp/demo-a)              │  (/tmp/demo-b)              │
│                             │                             │
├─────────────────────────────┼─────────────────────────────┤
│  AGENT C — frontend         │  AGENT D — docs             │
│  (/tmp/demo-c)              │  (/tmp/demo-d)              │
│                             │                             │
├─────────────────────────────┴─────────────────────────────┤
│  SYNC FEED — tails .kai/sync-log/*.jsonl across all four  │
│  13:21:04  [A] PUSH    src/greet.js                       │
│  13:21:05  [B] RECV    src/greet.js                       │
│  13:21:05  [C] RECV    src/greet.js                       │
│  13:21:05  [D] RECV    src/greet.js                       │
└───────────────────────────────────────────────────────────┘
```

Four agent panes (run `claude` in each) plus a bottom sync-feed strip that tails every agent's `.kai/sync-log/YYYY-MM-DD.jsonl` and pretty-prints with `jq` if available.

Why this layout: a naive 2×2 of Claude prompts makes sync invisible — when agent A saves a file and agent B's kai writes it to B's disk, nothing on B's screen changes until B's Claude decides to re-read. The feed strip is the running subtitle that makes every push/recv event visible as it happens.

Color code the four agents (A=red, B=blue, C=green, D=yellow) and carry those colors everywhere — pane labels, sync-feed rows, voiceover callouts. The layout script already does this for the labels and feed.

### The sync feed (how it's built)

Kai writes every live-sync event to a JSONL log at `.kai/sync-log/YYYY-MM-DD.jsonl`. Each line is one event; the interesting fields are `event` (`push` / `recv` / `merge` / `conflict` / `skip`), `file`, `agent`, and `timestamp`. The layout script's feed pane tails all four at once — roughly:

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

You don't need to run these by hand — `layout-livesync.sh` writes the same logic to `/tmp/demo-livesync-feed.sh` and wires it into the bottom pane. The snippets above are reference / troubleshooting material.

### Want a summary instead of per-event lines?

`kai live status` (no `--follow` flag yet) prints a compacted view of the sync_events log since the last capture. Wrap it with `watch` for a 500 ms-refreshing status panel:

```bash
cd /tmp/demo-a
watch -n 0.5 -t kai live status
```

It won't show the hero "file just arrived" moment as cleanly as the tail-based feed — use one, not both.

### Post-production flash (optional, but makes it pop)

If you're editing the recording after the fact: when the sync feed shows a `RECV` line on an agent, add a 200 ms colored border pulse on that agent's pane matching the agent's color. Even without a full VFX pass, a simple drop-shadow flash makes the "it appeared" moment land hard.

### Color assignments (use these everywhere)

| Agent | Role     | ANSI code   | On-screen cue            |
|-------|----------|-------------|--------------------------|
| A     | backend  | `\033[31m`  | red border, red in feed  |
| B     | tests    | `\033[34m`  | blue                     |
| C     | frontend | `\033[32m`  | green                    |
| D     | docs     | `\033[33m`  | yellow                   |

Stick to this palette. In the sync feed, in the pane borders, in the voiceover callouts. Consistency is what lets a viewer track four things at once for two minutes.

### One-shot setup

Run it — **don't paste it into your interactive shell**; the `set -e` at the top will kill your terminal on any error (including Ctrl+C during the `kai init` org prompt):

```bash
bash docs/setup-livesync.sh
```

**What it does:**

1. Refuses to run on kai < 0.13.1 (needs `kai spawn`).
2. Wipes `/tmp/demo-source` and `/tmp/demo-{a,b,c,d}`.
3. In `/tmp/demo-source`: scaffolds `src/greet.js`, `tests/greet.test.js`, `docs/greet.md`, `README.md`, `git init`s, `kai init`s (interactive org prompt the first time), `kai capture`, `kai push`. This is the seed — the source repo whose snapshot the agent dirs spawn from.
4. Runs `kai spawn /tmp/demo-a /tmp/demo-b /tmp/demo-c /tmp/demo-d --agent claude --sync full`. Workspace `a` is materialized via `kai checkout` from the seed's object store; `b/c/d` are CoW clones of `a` (APFS clone on macOS, reflink on btrfs/xfs, fallback `cp -R`). Each agent dir gets its own kai workspace ID + agent name (`claude-1` through `claude-4`) and inherits the seed's `origin` remote — so all four are on the same sync channel automatically.

**Result:**

- `/tmp/demo-source` — the seed repo, kai-initialized, pushed to kaicontext. You generally don't open this in an agent window; it exists to anchor the spawn.
- `/tmp/demo-a` through `/tmp/demo-d` — four spawned workspaces. Each is its own independently-initialized kai repo with its own `.git`, registered in `~/.kai/spawned.json`. Same kai remote, same sync channel.

**Inspecting:** `kai spawn list` shows all four; `kai ui` (in any of the dirs or in the source) opens the local dashboard.

**Tearing down:** `kai despawn --all --force` (or just delete the dirs and let the registry self-clean on next `kai spawn list`).

**If `kai init` hangs or the org picker annoys you on every demo run:** create the repo once manually on kaicontext.com, then edit `docs/setup-livesync.sh` to replace the `kai init` line with a `kai remote set origin …` call — fully non-interactive after the one-time repo creation.

### Enable live-sync in each Claude session

All four dirs are clones of the same kai repo, so they're **already on the same sync channel** — there's no channel name to set. Each agent just needs to turn live-sync on. Open a Claude Code window in each directory and paste this as the first prompt:

```
Call kai_live_sync with action="on" to start receiving other agents'
changes in real time, then wait for your role prompt.
```

Once all four have confirmed sync is on, any file they write gets pushed via `/v1/sync/push` and the other three receive it in <1s.

---

## The four role prompts

Have these ready in your paste buffer — each one goes into its specific window after Claude acknowledges live-sync is on.

**Note on timing:** live-sync polls peers every ~30s, so a file another agent just wrote may not be on your disk yet. The prompts below tell each agent to **re-call `kai_live_sync` with `action="on"` right before reading a peer's file** — this forces a fresh pull instead of waiting for the next auto-poll. If the file still isn't there (or is the old scaffold content), wait ~20s and re-read.

**Note on checkpoints — two different tools, both needed:**

- `kai_checkpoint(file, start_line, end_line, action)` — records **per-edit authorship**. Call this after each file write so `kai blame` can attribute lines to the right agent. Without it, `kai blame` returns "no authorship data".
- `kai_checkpoint_now(label)` — drops a **named milestone** in the sync log. Purely a marker for reviewers. Both tools are used below.

### Agent A — backend

> You are the backend agent. Your job is to implement `src/greet.js` so the function `greet(name)` returns `` `hi, ${name}` `` when called. No frills. After writing the file, call `kai_checkpoint` with `file="src/greet.js"`, `start_line=1`, `end_line=N` (where N is the last line of the file you just wrote), `action="modify"` so your authorship is recorded for `kai blame`. Then call `kai_checkpoint_now` with `label="greet: implementation"`. Finally, to verify tests pass, call `kai_live_sync` with `action="on"` again, wait ~15s, read `tests/greet.test.js`, and if it contains real test cases (not just the TODO scaffold), run them mentally against your implementation and report pass/fail.

### Agent B — tests

> You are the tests agent. Your job is to fill in `tests/greet.test.js` with at least two test cases for `greet(name)`: one for a normal name, one for an empty string. Before writing, call `kai_live_sync` with `action="on"` to force a sync, then read `src/greet.js` — agent A should have written the implementation. If `src/greet.js` is still the TODO scaffold, wait ~20s and re-read once; if it's still a TODO, assume the expected return is `` `hi, ${name}` `` and write tests for that. After writing your tests, call `kai_checkpoint` with `file="tests/greet.test.js"`, `start_line=1`, `end_line=N`, `action="modify"` so authorship is recorded, then call `kai_checkpoint_now` with `label="greet: tests"`.

### Agent C — frontend

> You are the frontend agent. Before writing anything, call `kai_live_sync` with `action="on"` to force a sync, then read `src/greet.js`. If it's still the TODO scaffold, wait ~20s and try once more. Then create `src/App.jsx` that imports `greet` from `./greet` and renders an `h1` with `greet("world")` as its text. After writing, call `kai_checkpoint` with `file="src/App.jsx"`, `start_line=1`, `end_line=N`, `action="insert"` (it's a new file), then call `kai_checkpoint_now` with `label="greet: frontend wired"`.

### Agent D — docs

> You are the docs agent. Your job is to update `docs/greet.md` based on what the other agents produce. Work in a loop: (1) call `kai_live_sync` with `action="on"` to force a sync, (2) read `src/greet.js`, `tests/greet.test.js`, and `src/App.jsx`, (3) update `docs/greet.md` with whatever you have — signature, behavior, one call-site example — noting any files that are still TODO scaffolds. After each write to `docs/greet.md`, call `kai_checkpoint` with `file="docs/greet.md"`, `start_line=1`, `end_line=N`, `action="modify"`. Repeat the loop once after ~30s so you catch late arrivals. When all three source files have real content and your docs reference all of them, call `kai_checkpoint_now` with `label="greet: docs"`.

None of these prompts mentions the other agents by name. They only know the files on disk. Sync + explicit re-polling makes the choreography work.

### After the demo: fold checkpoints into the snapshot

`kai_checkpoint` writes lightweight JSON records; `kai blame` reads them directly but some views (and future compaction) need them folded into a kai snapshot. Once the four agents are done, run:

```bash
cd /tmp/demo-a && kai capture -m "greet feature complete"
kai blame src/greet.js       # should now show Agent A's attribution
kai blame tests/greet.test.js  # Agent B
kai blame src/App.jsx          # Agent C
kai blame docs/greet.md        # Agent D
```

If `kai blame` still returns "no authorship data", the agent skipped the `kai_checkpoint` call — re-prompt it with "call `kai_checkpoint` for the file you just wrote, with line range 1 through N".

---

## Storyboard

```
[0:00 – 0:15]   Title card + one-paragraph framing (read voiceover).
                Cut to the 2×2 grid with all four empty Claude prompts.

[0:15 – 0:30]   All four agents get their "turn on live-sync" prompt.
                Each one calls kai_live_sync with action="on". Subtle
                hook highlight on the MCP tool call in each window.

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

1. **`kai_live_sync` (MCP tool)** — opens an SSE connection from the agent's kai session to `kaicontext.com/{org}/{repo}/v1/sync/live`. Every file the agent writes is pushed via `/v1/sync/push`; every file the channel receives is applied to the local working tree.
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
- **Files don't appear on other agents' disks.** The channel is determined by the kai remote — all four dirs must be clones of the same kaicontext repo, and each agent must have run `kai_live_sync` with `action="on"`. Re-check by asking each agent "is live-sync on?" — they should each report a channel id like `ch_74f2…` and it should be the same across all four.
- **Merge conflict messages on console.** Expected — kai logs conflicts for visibility. Local edits are preserved; re-prompt the affected agent to reconcile against the on-disk state.
- **Usage: 140 / 150 agent sync events.** Each agent write is one sync event. Four agents editing actively will burn through free-tier budget in a long demo. Upgrade or set `KAI_TELEMETRY=0` for recording.
