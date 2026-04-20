# Kai ↔ Git Bridge — Demo Script

**Length:** ~3:30
**Audience:** developers on teams, engineering managers, early adopters.
**The one thing they should leave with:** *I can use kai on Monday without asking permission from my whole team.*

---

## The problem this demo has to solve

Every new developer tool for teams dies the same death: *"I'd love to use it, but my team won't."* The tool needs everybody to switch at once, and nobody wants to be the one to propose it. So nothing changes. Git won because it didn't demand that — you can use it alone, and the rest of the team notices nothing. Kai needs the same property or it's never going anywhere.

**This demo is the proof that it has it.** One person turns kai on. Their teammate doesn't install anything, doesn't change anything, doesn't even know. They keep getting git commits. They keep sending git commits. And kai invisibly translates between the two.

Everything in this video should serve that claim. If a scene doesn't reinforce "adoption is now a personal decision, not a team decision," cut it.

---

## Setup (run once, offscreen)

Two terminals side-by-side (or tmux split).
- **Left** — labeled `alice (kai)`. Prompt: `alice $`
- **Right** — labeled `bob (git-only)`. Prompt: `bob $`

A shared bare repo at `/tmp/team.git` that both push to and pull from.

Paste this **whole block as-is** into one shell — the `set -e` makes it bail loudly if any step fails, which matters because a silent `git push` failure in Alice's block leaves Bob with an empty clone and Scene 4 fails mysteriously.

```bash
set -e
rm -rf /tmp/team.git /tmp/alice /tmp/bob

# Bare repo with an explicit default branch — don't leave it to git's defaults,
# which differ by version (master vs main) and can silently mismatch below.
git init --bare -b main /tmp/team.git
mkdir /tmp/alice /tmp/bob

# Alice: clone, commit, push. -u origin main makes the first push set upstream
# tracking so the bare repo actually receives the ref.
cd /tmp/alice
git clone /tmp/team.git .
git config user.email alice@demo
git config user.name Alice
git config commit.gpgsign false
git checkout -b main 2>/dev/null || git checkout main
cat > app.py <<'EOF'
def greet(name):
    return f"hi, {name}"
EOF
git add -A
git commit -q -m "initial"
git push -q -u origin main

# Bob: clones *after* Alice pushed, so he actually gets the initial commit.
cd /tmp/bob
git clone /tmp/team.git .
git config user.email bob@demo
git config user.name Bob
git config commit.gpgsign false

# Sanity check — fail loud if either side is in a bad state.
[ -f /tmp/alice/app.py ] || { echo "SETUP BROKEN: alice/app.py missing"; exit 1; }
[ -f /tmp/bob/app.py ]   || { echo "SETUP BROKEN: bob/app.py missing (Alice's push didn't land)"; exit 1; }
echo "setup ok — alice and bob both at initial commit"
```

If that final `setup ok` line doesn't print, **stop** and re-run the block in a fresh shell. Do not start recording until both sides are sitting on the same `initial` commit.

---

## Scene 1 — Set the stakes (0:00 – 0:25)

**Full screen. Plain type over black.**

> **You want to try a new dev tool.**
> **Your teammate doesn't.**
> **You both work in the same repo.**
>
> **What do you do?**

*(Hold 3 seconds. Then a beat of text replaces it:)*

> **Usually: nothing.**
> **You wait until everyone agrees.**
> **You never try the tool.**

*(Fade.)*

*Voiceover — write this down and deliver it clean, don't ad-lib:*

> "This is why most developer tools for teams die before anybody tries them. They require consensus. Consensus is slow. So the tool never gets adopted, and the team never gets better. Kai has a way out of this trap — watch."

---

## Scene 2 — Alice turns it on. One command. (0:25 – 1:05)

**Left terminal only. Screen-fill.**

*Voiceover:*

> "This is Alice. She's read about kai — semantic version control for a world where AI writes half the code. She wants to try it. Her team does not. So she turns on the bridge — one flag."

```bash
alice $ kai init --git-bridge
```

*Let the output play out. When `✓ Kai initialized` appears:*

> "That's it. Kai is live in her repo. It installed some git hooks, created a local semantic graph, and flagged the repo as bridged. Nothing she did touched the remote. Nothing her teammates can see yet."

```bash
alice $ kai bridge status
# kai↔git bridge: enabled

alice $ ls .git/hooks | grep -v sample
# post-checkout
# post-commit
# post-merge
# pre-commit
# pre-push
```

> "Five hooks, all best-effort, none of them can block git. If she deletes her kai directory tomorrow, git still works exactly like it did before."

---

## Scene 3 — Alice ships a feature. The git log stays sensible. (1:05 – 2:00)

**Left terminal. Alice writes code, then calls a milestone.**

*Voiceover:*

> "Alice builds a feature. Maybe she's working with an AI agent, maybe not — either way, when she's done she marks a milestone. This is what AI agents using kai's MCP tools already do automatically, so in the real workflow she doesn't even type this."

```bash
alice $ cat > app.py <<'EOF'
def greet(name):
    return f"hi, {name}"

def farewell(name):
    return f"goodbye, {name}"
EOF

alice $ kai bridge milestone --label "add farewell" --assert tests-pass
```

*Nothing visible happens. Pan to the git log.*

```bash
alice $ git log --oneline -3
# d41a98f add farewell
# 7c2bdc1 initial
```

> "That's a real git commit. Look closer."

```bash
alice $ git log -1 --format=fuller
```

*(The trailers should fill the screen. Pause on them. Do NOT rush this — this is the money shot.)*

```
commit d41a98f...
Author:     Alice <alice@demo>
AuthorDate: ...

    add farewell

    Kai-Snapshot: 296cc854cf6fff77428677e2f0f6ce457e7c5d3e57bfeceb8f70bde621257bb2
    Kai-Assert: tests-pass
```

> "The label is the subject. The trailers carry structured kai evidence — which semantic snapshot this commit corresponds to, and the trust assertion the agent declared. **Bob will see this log** — but he doesn't need to know what Kai-Snapshot means. It's valid git. It parses cleanly. The trailers are just extra."

```bash
alice $ git push
```

> "Pushed. Now let's see what actually landed on Bob's side."

---

## Scene 4 — Bob pulls. He has never heard of kai. (2:00 – 2:25)

**Right terminal.**

*Voiceover:*

> "Bob is running vanilla git. No kai. No hooks. No knowledge. He pulls because he's about to start work."

```bash
bob $ git pull
bob $ git log --oneline -3
# d41a98f add farewell
# 7c2bdc1 initial
```

> "He sees Alice's commit. Readable subject line. If he opens it, he sees the trailers — and ignores them, because they look like normal git trailers. They are normal git trailers. His workflow did not change at all. *He didn't do anything to opt into this.*"

---

## Scene 5 — Bob ships a bug fix with plain git. Alice sees it. (2:25 – 3:10)

**Right terminal.**

*Voiceover:*

> "Now the reverse direction. Bob fixes a bug. He writes code the way he always has — `git commit`, `git push`. No kai commands."

```bash
bob $ sed -i '' 's/hi, {name}/hello, {name}/' app.py
bob $ git add app.py && git commit -m "use 'hello' instead of 'hi'"
bob $ git push
```

**Switch to left terminal.**

*Voiceover:*

> "Alice pulls."

```bash
alice $ git pull
```

> "Her post-merge hook sees Bob's new commit, recognizes it doesn't carry a Kai-Snapshot trailer — meaning it's genuinely new to kai — and imports it into her semantic graph, automatically. Let's see."

```bash
alice $ kai ref list | grep ^git\.
# git.28ebce9f...   Snapshot  f8cba03f...   ← Bob's bug fix, auto-imported
# git.HEAD          Snapshot  f8cba03f...   ← also points at Bob's commit
```

> "Two refs. Bob's new commit is in kai as a semantic snapshot, ready for Alice's AI agent to reason about — 'what did Bob just change,' 'does it affect my code,' 'should I rerun tests' — with full kai context. Notice there's no ref for Alice's own milestone commit here: that one already carries a Kai-Snapshot trailer, so the import hook correctly skipped it and no loop ever starts."

---

## Scene 6 — The claim, stated plainly (3:10 – 3:30)

**Full screen. Three lines of text, one at a time, ~3 seconds each.**

> **Alice ran one command.**
>
> **Bob ran zero.**
>
> **The team stayed together.**

*Voiceover:*

> "That's the whole story. You don't have to convince anyone. You don't have to rally the team. You don't have to ask permission. If you want to try kai, the bridge means you can — today — in the same repo your teammates are working in, without anyone noticing unless they want to. Adoption stops being a team decision and becomes a personal one. That changes everything about how a tool like this spreads."

*Final card:*

> **kai init --git-bridge**
> **docs.kaicontext.com/bridge**

---

## Recording notes

**What the video is actually doing emotionally:**
- Scene 1 names a problem every developer has felt (propose new tool → it dies).
- Scene 2 delivers the one-command fix.
- Scene 3 is the proof of elegance: a valid git commit *that also carries kai's evidence*.
- Scenes 4–5 are the reassurance: the teammate's experience is genuinely unchanged.
- Scene 6 restates the claim so it lodges.

**What to guard against:**
- **Don't get technical too fast.** The trailer format, the hook names, the snapshot IDs are supporting evidence, not the story. The story is "adoption is personal now."
- **Don't apologize or hedge.** Not "this is kind of like…", not "we're still working on…". Either the demo works or you don't ship the video.
- **Don't rush the trailer shot in Scene 3.** Viewers need to actually read `Kai-Assert: tests-pass` and think "huh, that's useful information a commit message normally can't carry." If they don't have time to read it, the scene fails.

**Cuts if over-length:**
- Scene 2's `ls .git/hooks` list can go (talking about hooks without showing the payoff is vamping).
- Scene 5's narration can compress — you can skip explaining the trailer-check mechanism and just say "auto-imports."

**Things to add if extending to 5 min:**
- After Scene 5, briefly show `kai review open` on Alice's side: a reviewable diff spanning her milestone plus Bob's change, with the trust assertion visible in the UI.
- Quick pivot: *"and when Alice's agent is reviewing a third teammate's change months from now, it still has all of this context."*

**Color/contrast:**
- Dark theme, high-contrast font. The commit trailers (`Kai-*`) must be legible on a phone screen.
- Do not use a typing animation for the trailer shot in Scene 3 — the viewer needs to pause and read, and typed-out text fights with that.

---

## Talking points you can weave in as fits the pace

- "Git won in the 2000s because it didn't make you ask permission from your team. Kai needs the same property."
- "Every semantic commit kai makes is a valid git commit. That's not a coincidence — it's the whole point."
- "Trailers are not metadata bolted on. They are the commit. Git-native, searchable, grep-able by people who never heard of kai."
- "The bridge is re-entrancy safe: a kai milestone commit carries a trailer that tells the import hook 'you made me, skip me'. No loops, ever."
- "This is what 'additive' looks like when you mean it."

---

## Post-demo CTA (at the end of the video or in the description)

```
try it: kai init --git-bridge
docs:   https://docs.kaicontext.com/bridge
source: https://github.com/kaicontext/kai
```
