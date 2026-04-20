# Kai ↔ Git Bridge — Demo Script

**Length:** ~2:30
**Goal:** show a mixed team working in one repo, half on kai, half on plain git, neither changing their workflow.

## Setup (before recording)

Two terminals side-by-side (or tmux split).
- **Left** — labeled `alice (kai)`. Prompt: `alice $`
- **Right** — labeled `bob (git-only)`. Prompt: `bob $`

Both start in empty directories. A bare repo exists at `/tmp/team-repo.git` that both will push to.

```bash
# Run this once, offscreen:
rm -rf /tmp/team-repo.git /tmp/alice /tmp/bob
git init --bare /tmp/team-repo.git
mkdir /tmp/alice /tmp/bob
cd /tmp/alice && git clone /tmp/team-repo.git . && git config user.email alice@demo && git config user.name Alice
cd /tmp/bob  && git clone /tmp/team-repo.git . && git config user.email bob@demo   && git config user.name Bob
echo 'def hello(): return "hi"' > /tmp/alice/app.py
cd /tmp/alice && git add -A && git commit -m "initial" -q && git push -q origin main
cd /tmp/bob && git pull -q
```

---

## Scene 1 — The pitch (0:00–0:15)

**Camera on full screen with title card:**

> **Two teammates. One repo. Different tools.**
> Alice wants kai. Bob's happy with git.
> Neither has to change.

---

## Scene 2 — Alice turns on the bridge (0:15–0:45)

**Left terminal only.**

*Say:* "Alice wants kai, so she enables the bridge. That's one command."

```bash
alice $ kai init --git-bridge
```

*Wait for output. When `✓ Kai initialized` appears, say:*

"Done. Three git hooks, a kai snapshot graph, and a sentinel that says 'this repo is bridged'."

```bash
alice $ kai bridge status
# kai↔git bridge: enabled

alice $ ls .git/hooks | grep -v sample
# post-commit
# pre-commit
# pre-push
```

---

## Scene 3 — Alice works in kai. Bob sees a clean git commit. (0:45–1:30)

**Left terminal.**

*Say:* "Alice builds a feature. Her AI agent calls `kai_checkpoint_now` when it's done. Watch what happens on Bob's side."

```bash
alice $ cat > app.py <<'EOF'
def hello(): return "hi"

def goodbye(name):
    return f"bye, {name}"
EOF

alice $ kai bridge milestone --label "add goodbye" --assert tests-pass
# (silent — milestone becomes a git commit)

alice $ git log --oneline -3
# abc1234 add goodbye
# def5678 initial

alice $ git push
```

**Switch to right terminal.** *Say:* "Bob pulls. What does he see?"

```bash
bob $ git pull
bob $ git log -1 --format=fuller
```

*Pause on the commit message. The trailers are visible:*

```
    add goodbye

    Kai-Snapshot: 296cc854cf6fff77428677e2f0f6ce457e7c5d3e57bfeceb8f70bde621257bb2
    Kai-Assert: tests-pass
```

*Say:* "A meaningful commit. A readable log. Trailers he can ignore — or use to ask kai what changed."

---

## Scene 4 — Bob works in plain git. Alice sees it in kai. (1:30–2:10)

**Right terminal.**

*Say:* "Bob doesn't know kai exists. He just writes code."

```bash
bob $ cat >> app.py <<'EOF'

def thanks(name):
    return f"thanks, {name}"
EOF

bob $ git add app.py && git commit -m "add thanks"
bob $ git push
```

**Switch to left terminal.** *Say:* "Alice pulls."

```bash
alice $ git pull
```

*Say:* "Her post-commit hook sees Bob's commit, imports it into kai. Watch."

```bash
alice $ kai ref list | grep git.
# git.HEAD        Snapshot  9d8e...
# git.a1b2c3d4    Snapshot  8f7e...  ← Alice's milestone
# git.f5e6d7c8    Snapshot  9d8e...  ← Bob's commit, auto-imported

alice $ kai activity
# (shows Bob's change as a kai snapshot)
```

*Say:* "Alice's agent can now review, reason about, and build on Bob's work — even though Bob never touched kai."

---

## Scene 5 — The punch line (2:10–2:30)

**Full screen, text overlay:**

> **Alice ran one command.**
> **Bob didn't run any.**
> **The team is together in one repo.**

*Say:* "That's the bridge. Adoption stops being a team decision."

---

## Notes for recording

- **Pace:** don't rush the trailer reveal — it's the "aha" moment. Let the viewer read `Kai-Assert: tests-pass` themselves.
- **What to cut if short on time:** Scene 4's `kai activity` call. The git-ref list alone tells the story.
- **What to add if extending:** show `kai review open` on Alice's side pulling in Bob's change as a reviewable unit with proper trust context. That's the future-sell.
- **Fallback if milestone subcommand output is noisy:** redirect to `/dev/null` and cut to `git log` directly.
- **Color/contrast:** if you have a dark theme, the commit trailers (`Kai-*`) should be legible at YouTube 1080p — test a freeze-frame before publishing.

## Talking points to weave in (optional)

- "This is the problem every new-substrate tool has: how do you onboard when your teammate doesn't want to?"
- "Git is canonical. Kai is additive. The bridge makes that explicit instead of a marketing claim."
- "Re-entrancy-safe: the milestone commit carries a trailer that tells the import hook 'skip me'. No loops."
