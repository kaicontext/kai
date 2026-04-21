# Kai in 90 Seconds

**Audience:** anyone who's ever looked at a `git diff` and thought *"that doesn't tell me what actually changed."*
**The one thing they should leave with:** *Git tracks lines. Kai tracks meaning. That difference matters more every month, because more of the code isn't being written by humans.*

---

## The argument in one paragraph

Git is 20 years old. It was designed for a world where humans wrote every line of code, where a commit meant a person had read the diff, and where "blame" meant "whose fault is this." That world is ending. Increasingly, the code you're reviewing was written by an agent you didn't supervise, in a file you didn't open, in patterns the human author wouldn't have chosen. Line-based diffs don't help you here. You need to know **what changed semantically, who calls what, what breaks, and whether it's trustworthy** — and git can't answer any of those questions. Kai can. That's the whole pitch.

This demo shows you one concrete instance of that gap.

---

## Setup (run once, offscreen)

A tiny JavaScript project with a function and a caller. Small enough that the screen stays uncluttered, real enough that the impact question is interesting.

Paste this whole block into one shell. `set -e` bails loudly on any failure so you don't start recording from a broken state.

```bash
set -e

# Must be on kai >= 0.12.1 for the exact commands below.
kai version | grep -qE '0\.(1[2-9]|[2-9][0-9])' || { echo "need kai >= 0.12.1; upgrade first"; exit 1; }

rm -rf /tmp/kai-demo
mkdir /tmp/kai-demo && cd /tmp/kai-demo
git init -q -b main
git config user.email you@demo
git config user.name You
git config commit.gpgsign false

cat > auth.js <<'EOF'
export function authenticate(user, password) {
  return checkPassword(user, password);
}

function checkPassword(user, password) {
  return password === "hunter2";
}
EOF

cat > app.js <<'EOF'
import { authenticate } from "./auth.js";

export function login(req) {
  if (authenticate(req.user, req.password)) return 200;
  return 401;
}
EOF

git add -A
git commit -q -m "initial"
echo "=== setup ok ==="
```

Then initialize kai and capture the baseline:

```bash
kai init
kai capture -m "baseline"
```

Note: `kai init` is interactive and will ask you to pick a kaicontext org on the first run. Pick one, or press Ctrl+C out cleanly if you want an offline demo (kai still works fully without a remote). After that, you're on `initial` in git and `snap.latest` in kai, both pointing at the same tree. Ready to record.

---

## Scene 1 — The change (0:00 – 0:20)

*Voiceover:*

> "I'm going to make a change that looks small in git and see what each tool tells me. It's the kind of change an AI agent might ship: the auth function now requires an MFA token. Small typed diff, big behavioral consequence."

```bash
cat > auth.js <<'EOF'
export function authenticate(user, password, mfaToken) {
  if (!checkPassword(user, password)) return false;
  return verifyMfa(user, mfaToken);
}

function checkPassword(user, password) {
  return password === "hunter2";
}

function verifyMfa(user, token) {
  return token === "123456";
}
EOF
```

---

## Scene 2 — What git shows you (0:20 – 0:50)

*Voiceover:*

> "Here's what git says changed."

```bash
git add auth.js
git diff --cached auth.js
```

*(On-screen: a handful of `-` / `+` lines inside `auth.js`, plus the additions for `verifyMfa`. Hold long enough for the viewer to absorb.)*

> "Okay. Git shows me lines. `authenticate` now takes a third argument. There's a new function called `verifyMfa`. That's true — and it's also almost useless. Look at what git is *not* telling me:
>
> - **It doesn't tell me the signature changed** in a way that breaks every caller.
> - **It doesn't tell me who calls this function.** `app.js` imports it, but git doesn't know or care.
> - **It doesn't tell me the blast radius.** If I merge this, I have no idea what else will break.
>
> Three questions a real reviewer needs answered before merging. Git answers zero. Let me ask kai."

---

## Scene 3 — What kai shows you (0:50 – 1:30)

```bash
kai capture -m "mfa argument"
```

*Voiceover:*

> "First, the semantic diff."

```bash
kai diff @snap:prev @snap:last
```

*(On-screen, verbatim:)*

```
Diff: a9d2447e4c0d → 3a6c7923dffa

~ auth.js
  + function verifyMfa(user, token)
  ~ function authenticate(user, password) -> function authenticate(user, password, mfaToken)

Summary: 1 files (0 added, 1 modified, 0 removed)
         2 units (1 added, 1 modified, 0 removed)
```

> "That's a different diff. Kai doesn't show me lines — it shows me **meaning**. A function was added: `verifyMfa`. A function's signature was modified, and kai prints the full before-and-after signatures in one line. I can read this in a second and understand exactly what changed. Now the blast-radius question:"

```bash
kai query callers authenticate
```

*(On-screen:)*

```
1 callers of authenticate:
  app.js:2
```

> "One caller. `app.js` at line 2. That's the line of code that's going to break when this PR merges, because `app.js` only passes two arguments and `authenticate` now requires three. Kai told me in one command. Git can't tell me this at all."

---

## Scene 4 — The punchline (1:30 – 2:00)

*Full screen. Three lines, one at a time (~3 seconds each):*

> **Git shows you lines.**
>
> **Kai shows you meaning.**
>
> **When most code is written by machines, meaning is the only thing that matters.**

*Voiceover (deliver slowly):*

> "Git was designed in 2005 for humans writing code. That world is ending. Kai is designed for the one you're already in — where you're reviewing code you didn't write, from authors you can't interview, in quantities you can't read line-by-line. Line diffs can't answer the questions that matter in that world. Semantic diffs can. That's the argument. Try it."

*Final card:*

```
install: https://get.kaicontext.com
docs:    https://docs.kaicontext.com
source:  https://github.com/kaicontext/kai
```

---

## Recording notes

- **Total length:** ~2 minutes. Don't stretch it.
- **Pace check:** the git diff in Scene 2 should stay on screen long enough that the viewer can *feel* how little information it carries. Don't cut immediately to kai — if anything, slow down here.
- **Scene 3 is the hero.** Pause on the `authenticate(user, password) -> authenticate(user, password, mfaToken)` line. That's the exact moment the viewer should think *"wait, why doesn't git do that?"* Let them get there.
- **Screen:** dark theme, large font (≥ 18pt). Terminal output should be legible at 720p so the demo works embedded in tweets.
- **Don't type the heredocs live.** Pre-load the shell history or keep the setup offscreen and just show the runtime commands.
- **The `/* MFA */` scenario is dramatic on purpose.** A signature change with a required new argument guarantees visible breakage in callers. Softening it to "add optional argument" kills the punch.

## Gotchas

- **`kai init` is interactive** on first run if you're logged into kaicontext. It'll ask you to pick an org. Either pre-select on camera (it takes 2 seconds) or hit Ctrl+C and use `kai init --help` to find an offline path — kai works fully without a remote.
- **Don't call `kai diff` with no args** and expect to see the change — the default compares `snap.latest` against the working directory, so running it *after* `kai capture` will say "No differences" (both sides are the new state). Use `kai diff @snap:prev @snap:last` as the script does.
- **`kai query callers` works for JavaScript, TypeScript, Go, Java, and Ruby.** Python call-graph extraction is in progress as of v0.12.1. If you port this demo to Python, the callers command will return "No callers found." Stick with JS until that ships.

## What to say if someone pushes back mid-demo

- *"Git has extensions for this."* → They don't, not really. Every tool that adds "semantic diff" on top of git (Gerrit, GitHub's expand-diff, IDE features) still hands you lines first and hopes you derive meaning from them. Kai starts from meaning.
- *"Why not just read the code?"* → In a 500-file repo where three agents pushed today, you can't. The question isn't whether you *could* read it, it's what tools help you decide which parts deserve reading.
- *"Doesn't this slow me down?"* → No. Kai's hooks are best-effort and non-blocking; if kai breaks, git still works. There's no downside to having it installed.
- *"How's this different from linters / static analysis?"* → Linters run per-file against style rules. Kai runs against the semantic history of the whole codebase and answers questions about change, not about style.

## Talking points to weave in as fits

- "Most of the cost of a bad commit isn't in the commit itself. It's in the six people downstream who have to figure out what you did."
- "A diff should answer 'what changed and what breaks.' Git answers the first one and shrugs at the second."
- "Kai isn't replacing git. Kai is the layer you've always wanted on top of it."
- "The gap between 'lines' and 'meaning' used to be annoying. With agents shipping code, it's now the whole job."

---

## If you're just reading this, try the demo yourself

Three copy-paste steps, ~90 seconds:

```bash
# 1. setup: copy the block at the top
# 2. make the change: overwrite auth.js with the Scene 1 contents
# 3. compare what each tool tells you:

git diff --cached auth.js            # what git shows
kai capture -m "mfa argument"
kai diff @snap:prev @snap:last       # what kai shows
kai query callers authenticate       # the question git can't answer
```

If the second pair of commands tell you something the first one couldn't — that's the demo.
