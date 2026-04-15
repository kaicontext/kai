# Kai Demo — Full Recording Script

Full end-to-end demo of kai for screen recording. Setup, commands, narration
cues, expected outputs, and post-production notes. ~8 minutes at normal
pace; see "Scope-trimming options" at the bottom for shorter cuts.

## 0. Before you start recording

### Window setup
- Terminal: big font (≥18pt), dark or light — just pick one and commit
- Browser on a second workspace/monitor for the `kaicontext.com` close
- Claude Code open in a separate terminal tab (for Act V)
- Kill all noisy background processes — nothing should `Bell:` or ping mid-demo

### Verify before recording
```bash
kai --version              # should say 0.10.2 or later
kai doctor                 # should be green
claude --version           # if you'll do Act V
```

### Clean state
```bash
rm -rf /tmp/kai-demo && mkdir -p /tmp/kai-demo && cd /tmp/kai-demo
```

### One-shot fixture generator
Run this before the recording starts; paste into the demo dir:

```bash
cat > setup-fixture.sh <<'SETUP'
#!/usr/bin/env bash
set -e
mkdir -p src/auth src/db src/routes tests

cat > src/auth/validate.ts <<'TS'
const TOKEN_TTL_SECONDS = 3600;

export function validateToken(token: string): boolean {
  if (!token || token.length < 10) return false;
  const [, expiry] = token.split(".");
  return Number(expiry) > Date.now() / 1000;
}

export function tokenTTL(): number {
  return TOKEN_TTL_SECONDS;
}
TS

cat > src/auth/login.ts <<'TS'
import { validateToken } from "./validate";
import { getUserByEmail } from "../db/users";

export async function login(email: string, token: string) {
  if (!validateToken(token)) {
    throw new Error("invalid token");
  }
  const user = await getUserByEmail(email);
  if (!user) throw new Error("user not found");
  return { userId: user.id, email: user.email };
}
TS

cat > src/db/users.ts <<'TS'
export interface User {
  id: string;
  email: string;
}

const USERS: User[] = [
  { id: "1", email: "alice@example.com" },
  { id: "2", email: "bob@example.com" },
];

export async function getUserByEmail(email: string): Promise<User | undefined> {
  return USERS.find(u => u.email === email);
}
TS

cat > src/routes/api.ts <<'TS'
import { login } from "../auth/login";

export async function postLogin(req: { email: string; token: string }) {
  try {
    const session = await login(req.email, req.token);
    return { status: 200, body: session };
  } catch (e) {
    return { status: 401, body: { error: (e as Error).message } };
  }
}
TS

cat > tests/login.test.ts <<'TS'
import { login } from "../src/auth/login";

test("login rejects invalid token", async () => {
  await expect(login("alice@example.com", "")).rejects.toThrow("invalid token");
});

test("login rejects unknown user", async () => {
  const future = Math.floor(Date.now() / 1000) + 3600;
  await expect(
    login("ghost@example.com", `abc.${future}`),
  ).rejects.toThrow("user not found");
});
TS

cat > package.json <<'JSON'
{
  "name": "kai-demo-auth",
  "version": "0.1.0",
  "scripts": { "test": "jest" }
}
JSON

echo "Fixture ready: $(find . -type f | wc -l) files"
SETUP
chmod +x setup-fixture.sh
./setup-fixture.sh

git init -q
git add .
git -c user.email=demo@demo.test -c user.name=demo commit -qm "initial auth skeleton"
```

Dry run the whole demo once without the camera to catch any prompt mismatches
or slow commands.

---

## 1. Act I — Install & Init *(45 seconds)*

> **Narration**: "Kai is semantic version control for the AI coding era. It
> sits alongside git and gives your AI agents real understanding of your
> codebase. Let's install it and initialize a new project."

```bash
cd /tmp/kai-demo
ls
```

Expected: 5 files, 4 directories. The fixture.

```bash
kai init
```

When it prompts for email: type a real address you can receive mail at. In the
recording, cut from "Enter your email:" straight to "✓ You're all set!" — no
need to show the magic-link paste.

> **Narration beat**: "That's it — kai init created a semantic graph of the
> codebase, installed defensive git hooks that will never block your workflow,
> signed me up for the free kaicontext.com sync, and ran my first capture.
> One command, maybe 10 seconds."

---

## 2. Act II — What kai sees *(30 seconds)*

`kai init` already ran a capture. If you've edited anything since, run
`kai capture` once before this act so the graph is current.

```bash
kai log
```

> **Narration**: "Kai has a snapshot of the project — a semantic graph of
> files, functions, classes, imports, and test relationships. Every capture
> is an immutable node in that history."

```bash
kai query dependents src/auth/validate.ts
```

Expected: `src/auth/login.ts` (and anything else that imports validate).

> **Narration**: "These aren't string matches — they're graph edges. Kai
> knows `login.ts` imports `validate.ts` because it parsed both files into
> the graph."

---

## 3. Act III — Semantic queries *(90 seconds)*

```bash
kai query callers login
```

Expected: `postLogin` calls `login`, test files reference `login`.

> **Narration**: "Who calls `login`? One API route, two tests. That answer
> came from a single query against the graph, not a grep across my codebase."

```bash
kai query impact src/auth/validate.ts
```

> **Narration**: "If I change `validate.ts`, what's affected? Kai walks the
> graph transitively: login.ts, api.ts, the login test. That's the blast
> radius — and the exact set of tests CI needs to run."

> **Narration beat**: "These same queries are exposed to AI agents as MCP
> tools — `kai_callers`, `kai_impact`, `kai_context`. That's the killer
> move, and we'll see it in Act V: the agent pulls exactly the code it
> needs to reason about a function instead of grepping the whole repo."

---

## 4. Act IV — Semantic diff *(60 seconds)*

Make a meaningful edit live on camera:

```bash
cat > src/auth/validate.ts <<'TS'
const TOKEN_TTL_SECONDS = 1800;

export function validateToken(token: string): boolean {
  if (!token || token.length < 10) return false;
  const [, expiry] = token.split(".");
  return Number(expiry) > Date.now() / 1000;
}

export function tokenTTL(): number {
  return TOKEN_TTL_SECONDS;
}

export function isExpired(expiry: number): boolean {
  return expiry <= Date.now() / 1000;
}
TS
```

```bash
kai status
kai diff
```

Expected: `~ TOKEN_TTL_SECONDS: 3600 -> 1800` and
`+ function isExpired(expiry: number)` on `src/auth/validate.ts`.

> **Narration**: "Git diff would tell me seven lines changed. Kai tells me
> a constant went from 3600 to 1800 and a new function called `isExpired`
> was added. That's the difference between text and meaning."

```bash
kai diff -p | head -20
```

> **Narration**: "And if I want the old-school line diff, it's still there."

---

## 5. Act V — MCP + Claude Code *(90 seconds — the headline beat)*

In your **second terminal tab**, Claude Code is already running against
`/tmp/kai-demo`. MCP server `kai` was auto-installed by `kai init`.

Ask Claude:

> "How does authentication work in this codebase? What would break if I
> changed the token TTL to 15 minutes?"

Claude will:
1. Call `kai_symbols` to find auth-related symbols
2. Call `kai_context login` to pull the function
3. Call `kai_impact src/auth/validate.ts` to answer the second half
4. Answer in ~3-5 MCP tool calls instead of ~15 Read/Grep calls

> **Narration**: "Watch Claude answer this. It's using kai MCP tools —
> `kai_context`, `kai_impact`, `kai_callers` — to get exactly the code it
> needs. No directory listing. No grep. No reading files it doesn't need. The
> answer shows up in seconds and the context window stays clean."

**Cut to a side-by-side** if you can: in a third pane, ask the same question
to a Claude instance **without** the kai MCP server. It will spend 10+
Read/Grep calls exploring the tree before answering. Same answer, way more
tokens.

> **Closer for this act**: "Same answer. Roughly 10× fewer tokens. And every
> tool call is a query against a graph that already knows the structure, so
> it can't hallucinate a function name."

---

## 6. Act VI — Selective CI *(45 seconds)*

```bash
kai capture
kai ci plan --explain
```

Expected: kai shows only the tests affected by the validate.ts change.

> **Narration**: "Kai knows which tests touch the code I actually changed.
> It's telling me: run 1 of the 2 tests. On a real codebase with thousands of
> tests, this is the difference between a 30-second CI and a 30-minute CI.
> Same confidence, fraction of the cost."

```bash
kai ci plan @cs:last --safety-mode=guarded
```

> **Narration**: "And when it's not sure, it tells you — via safety modes.
> Guarded mode runs the selective plan with an automatic fall-through to the
> full suite on risk signals like dynamic imports or test infra changes.
> Shadow mode runs selective alongside full and compares. Strict mode trusts
> kai only."

---

## 7. Act VII — Workspaces & conflict resolution *(90 seconds)*

```bash
kai ws create feat/auth-refactor
```

Edit something in the workspace:
```bash
cat > src/auth/login.ts <<'TS'
import { validateToken } from "./validate";
import { getUserByEmail } from "../db/users";

export async function login(email: string, token: string) {
  if (!email || !validateToken(token)) {
    throw new Error("invalid credentials");
  }
  const user = await getUserByEmail(email);
  if (!user) throw new Error("user not found");
  return { userId: user.id, email: user.email, loggedInAt: Date.now() };
}
TS
kai capture
kai ws stage feat/auth-refactor
```

Now simulate main diverging with a different edit to the same function:
```bash
cat > src/auth/login.ts <<'TS'
import { validateToken } from "./validate";
import { getUserByEmail } from "../db/users";

export async function login(email: string, token: string) {
  if (!validateToken(token)) {
    throw new Error("invalid token");
  }
  const user = await getUserByEmail(email);
  if (!user) throw new Error("user not found");
  return { userId: user.id, email: user.email, sessionId: crypto.randomUUID() };
}
TS
kai capture
kai ref set snap.target @snap:last
```

Restore the workspace view in the working tree:
```bash
cat > src/auth/login.ts <<'TS'
import { validateToken } from "./validate";
import { getUserByEmail } from "../db/users";

export async function login(email: string, token: string) {
  if (!email || !validateToken(token)) {
    throw new Error("invalid credentials");
  }
  const user = await getUserByEmail(email);
  if (!user) throw new Error("user not found");
  return { userId: user.id, email: user.email, loggedInAt: Date.now() };
}
TS
```

```bash
kai integrate --ws feat/auth-refactor --into snap.target
```

Expected: kai reports a conflict and points at `kai resolve feat/auth-refactor`.

> **Narration**: "Two branches modified the same function in incompatible
> ways. Kai tells me there's a conflict and hands me a resolve workflow."

```bash
kai resolve feat/auth-refactor
ls .kai/conflicts/feat/auth-refactor/
cat .kai/conflicts/feat/auth-refactor/src__auth__login.ts.HEAD
```

> **Narration**: "Kai materialized the conflict into three files: my
> workspace version, the target version, and the common ancestor. I edit the
> HEAD file in place."

Simulate the resolution:
```bash
cat > .kai/conflicts/feat/auth-refactor/src__auth__login.ts.HEAD <<'TS'
import { validateToken } from "./validate";
import { getUserByEmail } from "../db/users";

export async function login(email: string, token: string) {
  if (!email || !validateToken(token)) {
    throw new Error("invalid credentials");
  }
  const user = await getUserByEmail(email);
  if (!user) throw new Error("user not found");
  return {
    userId: user.id,
    email: user.email,
    loggedInAt: Date.now(),
    sessionId: crypto.randomUUID(),
  };
}
TS
```

```bash
kai resolve feat/auth-refactor --continue
```

Expected: "✓ Integration successful (resolved 1 conflict(s))"

> **Narration**: "Done. Merged snapshot, cleaned-up conflict state, back to a
> green workspace."

---

## 8. Act VIII — AI authorship *(30 seconds)*

```bash
kai blame src/auth/login.ts
```

> **Narration**: "Kai tracks which lines were written by an AI agent versus a
> human. It knows this because the MCP server captures every edit with
> attribution. If you need to prove to a compliance team exactly what the AI
> touched, it's here. Same shape as git blame, different axis."

---

## 9. Act IX — Push to cloud *(30 seconds)*

```bash
kai push
```

**Cut to the browser**: open `https://kaicontext.com/<your-slug>/kai-demo` on
your other workspace.

> **Narration**: "And it's synced. Semantic diffs, reviews, CI history,
> selective test plans — all in the web UI. Your team can see the same graph,
> comment on symbols instead of lines, and the review is anchored to what
> actually changed semantically."

---

## 10. Close *(15 seconds)*

```bash
kai doctor
```

> **Narration**: "One command to install. One command to check health. One
> graph your AI agents talk to through MCP. One place your team can review
> semantic changes. **This is Kai.**"

Show the URL: `kaicontext.com` and `docs.kaicontext.com`. End card.

---

## Short version — 90-second elevator pitch

If you need a social-media-length cut:

1. **0:00–0:10** — `kai init` running in a fresh repo. One command, no prompts.
2. **0:10–0:25** — edit a function, `kai diff` showing `CONSTANT_UPDATED` and
   `FUNCTION_ADDED`. Narration: "Git sees lines. Kai sees meaning."
3. **0:25–0:45** — switch to Claude Code, ask "how does auth work". Claude
   uses kai MCP tools, answers in 3-5 calls. Narration: "This is what AI
   agents should see."
4. **0:45–1:00** — `kai ci plan --explain` on the same change. Narration:
   "One function changed. One test affected. Not thirty minutes of CI."
5. **1:00–1:20** — `kai integrate` → conflict → `kai resolve --continue`.
   Narration: "Workspaces and merges understood as code, not text."
6. **1:20–1:30** — `kai push`, cut to web UI. Tag: "**kaicontext.com**"

---

## Post-production notes

- **Cut the magic-link paste** in Act I. Start the shot on "Enter your email"
  and end on the success banner.
- **Slow down on the diff output in Act IV** — give the viewer 2 full seconds
  to read `CONSTANT_UPDATED` and `FUNCTION_ADDED`.
- **The MCP beat in Act V is the hero shot.** Don't rush it. If Claude takes
  8 seconds to answer, let it sit — the viewer needs to see the tool-call
  trail.
- **Overlay text** for commands if you have time in editing: show `kai
  impact` and `kai context` as captions the first time they appear. Viewers
  unfamiliar with kai will miss what these words mean without a helper.
- **Background music**: none during Claude's tool calls. Bring music back up
  during "Push to cloud" for the emotional payoff.
- **End card**: `kaicontext.com`, `docs.kaicontext.com`, `get.kaicontext.com`.

---

## If something goes wrong mid-recording

**`kai init` hangs on email**: type the email, open the email, paste the
token. Cut in post.

**MCP server not responding**: run `claude mcp list` — if `kai` isn't there,
`claude mcp add kai -- kai mcp serve`.

**Staging/prod auth blip during kai push**: switch to offline mode, show `kai
push` failing gracefully with the "not configured" message instead. Save the
cloud beat for a retake.

**A semantic query returns empty**: likely `kai capture` didn't run. Run it,
then retry. Cut in post.

---

## Scope-trimming options

If 8 minutes is too long, drop in this order:
1. **Act VIII (blame)** — 30s savings, lowest viewer impact
2. **Act VII conflict resolution** — 90s savings, powerful but niche
3. **Act VI (selective CI)** — 45s savings, strong technical message
4. **Act IV (semantic diff)** — 60s savings, core message — **do not cut
   unless desperate**
5. **Act V (MCP + Claude)** — **never cut this. It's the reason to record the
   demo at all.**

Keeping acts I, III, IV, V, and IX gives you a ~4-minute video that hits
every important beat.
