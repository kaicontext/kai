# Kai in 30 Seconds

**Length:** :30 hard cap — this is the tweet-embed version.
**The one thing they should leave with:** *Git shows edits. Kai shows meaning — and intent.*

Three beats, one voiceover, one payoff card. Done.

---

## Setup (offscreen, before you hit record)

Two things matter here: a real caller so the demo has a caller to show, and a `kai.modules.yaml` defining the Auth module so the intent output reads `Update Auth authenticate` instead of the generic `Update General authenticate`.

```bash
set -e
kai version | grep -qE '0\.(1[2-9]|[2-9][0-9])' || { echo "need kai >= 0.12.3"; exit 1; }

rm -rf /tmp/kai-30 && mkdir /tmp/kai-30 && cd /tmp/kai-30
git init -q -b main
git config user.email you@demo && git config user.name You
git config commit.gpgsign false

cat > auth.js <<'EOF'
export function authenticate(user, password) {
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
# Module mapping so intent classifies this as an 'Auth' change, not 'General'.
cat > kai.modules.yaml <<'EOF'
modules:
  - name: Auth
    paths: ["auth.js"]
EOF

git add -A && git commit -q -m "initial"
kai init                                # interactive on first run — pick an org or Ctrl+C
kai capture -m "baseline"

# The change the demo is about to reveal: `authenticate` now requires a third argument.
cat > auth.js <<'EOF'
export function authenticate(user, password, mfaToken) {
  if (password !== "hunter2") return false;
  return mfaToken === "123456";
}
EOF
git add auth.js
kai capture -m "mfa argument"

# Pre-create the changeset so intent render doesn't need an ID lookup on camera.
CHANGESET=$(kai changeset create @snap:prev @snap:last -m "Require MFA for auth" | grep "^Created changeset:" | awk '{print $3}')
echo "CHANGESET=$CHANGESET"   # copy this — you'll paste it into the recording

echo "=== ready to record ==="
```

When `ready to record` prints, you have **three commands** to run on camera (all under a second each):

```bash
git diff --cached auth.js            # beat 1 — git
kai diff @snap:prev @snap:last       # beat 2 — semantic diff
kai intent render $CHANGESET         # beat 3 — intent
```

Widen your terminal so the longest line (`+ function authenticate(user, password, mfaToken)`) fits without wrapping.

---

## Storyboard

```
[0:00 – 0:04]  Cold open, text on black:
                "A function signature changed.
                 Now what?"

[0:04 – 0:12]  LEFT HALF of screen: output of
                  git diff --cached auth.js
               Subtitle (small, bottom): "git"

[0:12 – 0:22]  RIGHT HALF slides in: output of
                  kai diff @snap:prev @snap:last
               Subtitle: "kai"

               Viewer should visually catch the red/green pair:
                 - function authenticate(user, password)
                 + function authenticate(user, password, mfaToken)

[0:22 – 0:27]  RIGHT HALF adds the intent line below the diff:
                  kai intent render $CHANGESET
                  → Intent: Update Auth authenticate

[0:27 – 0:30]  Card, three short lines one at a time:
                  "Git shows edits."
                  "Kai shows meaning."
                  "Kai shows why."
               CTA: get.kaicontext.com
```

---

## Voiceover — 65 words, ~25 seconds at a natural pace

Read this once, clean, over the visuals. Don't improvise. The rhythm is built for 30 seconds — any longer and the video runs past the payoff card.

> "A function changed. One required new argument. **[beat — viewer reads git diff]**
>
> Git tells you which lines moved. **[beat]**
>
> Kai tells you the signature changed — old version gone, new version in, side by side. **[beat]**
>
> And Kai tells you what the change *is*: an update to `authenticate` in the Auth module. **[beat]**
>
> Git shows edits. Kai shows meaning."

Hit the `[beat]` pauses hard. Silence is what lets the viewer actually read the on-screen output — if the narration is continuous, the demo feels rushed and nothing lands.

---

## What the three commands prove (the argument beneath the voiceover)

1. **`git diff`** — git shows textual edits. It doesn't know the word "function," can't connect `authenticate` in `auth.js` to its caller in `app.js`, can't tell you what the change means in plain English.
2. **`kai diff`** — kai knows `authenticate` is a function, sees the signature changed, renders the delta the way you'd read it in a code review: removed line on top, added line below.
3. **`kai intent render`** — kai classifies the change semantically. Not "modified auth.js" — but "Update Auth authenticate." This is what lets an AI agent or a human reviewer reason about a stream of changes without reading every line.

Git stops at step 1. Kai goes two more steps. That's the whole pitch.

---

## Tweet copy (under 280 chars)

Attach the video. Post text:

> Same change. One function. One new required argument.
>
> `git diff` tells you lines moved.
> `kai diff` shows the signature change side-by-side.
> `kai intent` names what the change *is*.
>
> Git shows edits. Kai shows meaning.
>
> kai v0.12.3 — get.kaicontext.com

---

## Recording notes

- **The red/green signature pair in Scene 2 is the hero shot.** Do not rush it. The whole pitch depends on the viewer seeing:
  ```
  - function authenticate(user, password)
  + function authenticate(user, password, mfaToken)
  ```
  one above the other, and thinking *"oh — every caller that passes two arguments just broke."* If they don't have time to read it, you lose the demo.
- **`kai intent` output is one line.** Framing it on screen as a separate panel below the diff (with a slight highlight) makes it feel like a distinct beat, which is what the voiceover calls out. Don't let it blur into the diff output.
- **Record command outputs separately** and composite them into the split-screen. Live typing at demo speed looks frantic.
- **Dark terminal, high contrast, font ≥ 18pt.** The demo ships in a tweet; it needs to be legible on a phone at 720p.
- **Kai diff is colored in a TTY** (added green, removed red, modified yellow, bold file headers, dim arrow / hash). Git's diff is also colored. Verify your screen recorder captures ANSI — test a freeze-frame before the real take.
- **Don't include `kai capture` or `kai changeset create` in the recording.** Treat them like git staging — setup steps, not part of the story.

## Pushback answers (for the live Q&A after you post the clip)

- *"Can't I just read the code?"* → In a 500-file repo where three agents pushed today, you can't. The question isn't whether you *could* read it, it's which tools tell you which changes need reading.
- *"Won't my LSP / IDE catch the signature break?"* → Only when you open the affected file. Kai tells you the file exists and the intent of the change before you open anything.
- *"Is this a replacement for git?"* → No. Kai runs alongside git. The bridge (`kai init --git-bridge`) keeps both in sync for teammates who don't use kai at all.
- *"Is intent LLM-generated?"* → In this release it's template-based — deterministic, fast, and doesn't need API keys. LLM-generated intent is on the roadmap for richer explanations.

## Why the demo stops here

A 30-second video has room for one voiceover sentence per beat and one payoff card. That's what this is. If you have 60+ seconds and want to walk through impact analysis (`kai query callers`), test selection, or the kai↔git bridge for mixed teams — use [`demo.md`](./demo.md) or [`bridge-demo-script.md`](./bridge-demo-script.md). Those are different deliverables for different audiences.
