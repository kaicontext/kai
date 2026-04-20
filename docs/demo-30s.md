# Kai in 30 Seconds

**Length:** :30 hard cap — this is the tweet-embed version.
**The one thing they should leave with:** *Git shows lines. Kai shows meaning.*

One problem. One solution. One number that matters. Done.

---

## Setup (offscreen, before you hit record)

Two things to make the demo land: a small repo with a real caller, and both snapshots already captured so you can run the reveal commands with no lag.

```bash
set -e
kai version | grep -qE '0\.(1[2-9]|[2-9][0-9])' || { echo "need kai >= 0.12.1"; exit 1; }

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

echo "=== ready to record ==="
```

When `ready to record` prints, you can run these three commands in under two seconds total:

```bash
git diff --cached auth.js            # what git sees
kai diff @snap:prev @snap:last       # what kai sees
kai query callers authenticate       # what git can't see
```

Widen your terminal so the longest line (`+ function authenticate(user, password, mfaToken)`) fits without wrapping.

---

## Storyboard

```
[0:00 – 0:04]  Cold open. Just type on screen:
                "A function signature changed.
                 Who's going to break?"

[0:04 – 0:12]  LEFT HALF of screen: the output of
                git diff --cached auth.js
               Subtitle (small, bottom): "git"

[0:12 – 0:22]  RIGHT HALF slides in: the output of
                kai diff @snap:prev @snap:last
                kai query callers authenticate
               Subtitle: "kai"

                On-screen highlight the viewer should
                visually catch:
                  - function authenticate(user, password)
                  + function authenticate(user, password, mfaToken)
                  1 callers: app.js:2

[0:22 – 0:28]  Card, three short lines one at a time:
                  "Git shows lines."
                  "Kai shows meaning."
                  "That's the whole product."

[0:28 – 0:30]  CTA: get.kaicontext.com
```

---

## Voiceover — 80 words, ~27 seconds at normal pace

Read this once, clean, over the visuals. Don't improvise. The line count is deliberate — any longer and the pace breaks.

> "A function changed. One required new argument. **[beat]**
>
> Git tells you which lines moved. **[beat]**
>
> It doesn't tell you anything's broken. It doesn't tell you who calls this function. It doesn't tell you what's about to fail at runtime. **[beat]**
>
> Kai does. It shows you the signature change — and it tells you the one line of code, in a different file, that's about to break. **[beat]**
>
> Git was built for humans writing every line. That's ending. Kai is built for what comes next."

Hit `[beat]` hard — silence is what makes the viewer actually read the on-screen text.

---

## Tweet copy (under 280 chars)

Attach the video. Post text:

> Same small change. One function. One new required argument.
>
> `git diff` tells you lines moved.
> `kai diff` tells you `app.js:2` is about to break.
>
> Every AI-shipped change should come with a blast radius. Now it can.
>
> kai v0.12.2 — get.kaicontext.com

---

## Recording notes

- **The signature-change line is the hero shot.** Do not rush it. The whole pitch depends on the viewer reading `+ function authenticate(user, password, mfaToken)` and thinking *"oh — that breaks everywhere that only passes two arguments."* If they don't have time to read it, you lose the demo.
- **Record the three command outputs separately** and composite them into the split-screen. Live typing at demo speed looks frantic.
- **Dark terminal, high contrast, font ≥ 18pt.** The demo ships in a tweet; it needs to be legible on a phone at 720p.
- **Do not include `kai capture` in the recording.** Treat it like git staging — a setup step, not part of the story.
- **Kai diff is colored in a TTY** (added lines green, removed lines red, modified yellow). Git's diff is also colored. Make sure the screen recording captures the colors — some recording tools mangle ANSI. Test a freeze-frame before the real take.

## Pushback answers (for the live Q&A after you post the clip)

- *"Can't I just read the code?"* → In a 500-file repo where three agents pushed today, you can't. The question isn't whether you *could* read it, it's which tools tell you which changes need reading.
- *"Won't my LSP / IDE catch this?"* → Only when you open the affected file. Kai tells you the file exists before you open anything.
- *"Is this a replacement for git?"* → No. Kai runs alongside git. The bridge (`kai init --git-bridge`) keeps both in sync for teammates who don't use kai at all.

## Why the demo stops here

Longer demos need voiceover to earn their length, need a proper narrative arc, need setup context. This one doesn't. It's a single visual comparison with one sentence of framing. If you have 60+ seconds, use [`demo.md`](./demo.md) instead — that's a different deliverable for a different audience.
