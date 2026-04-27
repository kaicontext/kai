# Kai Weekly Update — Week 3

**2026-04-21 → 2026-04-27**

A week of two halves: finishing the kai↔git bridge, then quietly replacing half the demo infrastructure with two new primitives — `kai spawn` and `kai ui` — that turned out to be the right shape all along.

## What shipped

**Bridge → done.** The kai↔git bridge is end-to-end. Post-merge and post-checkout hooks now import commits that arrive via `git pull` (closed the gap the demo exposed). Milestone commits carry `Kai-Snapshot:` / `Kai-Assert:` trailers and skip-loop on import. Mixed teams can adopt kai one person at a time. *(v0.12.0–v0.12.5)*

**`kai spawn` / `kai despawn`.** New primitive that replaces ad-hoc `/tmp/demo-*` choreography. Spawn a uniquely-named workspace cloned from any kai repo, get a real working dir + sync channel + cleanup; the live-sync demo setup dropped from ~70 lines of bash to two commands. *(v0.13.0)*

**`kai ui`.** Local dashboard that surfaces sync events, checkpoint events, and spawned workspaces in a browser tab. Replaced the tmux sync-feed pane in the 4-agent demo entirely. *(v0.13.1, v0.13.2)*

**BSL 1.1 relicense.** Source-available now, Apache 2.0 in 2030 (Change Date 2030-04-21). The relicense + the curl install pill on the homepage shipped same day.

**Telemetry → PostHog.** Replaced the self-hosted ingest + admin dashboard with PostHog Go SDK. Default-on with a one-time first-run stderr notice naming exactly what's collected; `KAI_TELEMETRY=0` and `kai telemetry disable` honored. The legacy spool + control-plane endpoints + `telemetry_events` table are gone.

**Billing reframe.** Stopped metering `kai push`. Only MCP-driven `/v1/sync/push` events count now — the agent-chatter we actually want to shape, not user-initiated publishes. CLI usage messaging now says "agent sync events" instead of "commits."

**Admin tooling.** `DELETE /api/v1/orgs/{org}` (owner-only, requires `?confirm=<slug>`) + matching `kai org list` / `kai org delete` CLI. `POST /api/v1/admin/usage/reset` for resetting the meter mid-period.

**Demo scripts shipped.** `demo.md` (90s), `demo-30s.md` (tweet-embed with voiceover + `kai intent` as the third beat), `demo-livesync.md` (4-agent), and the matching setup/layout scripts. `kai diff` now renders signature/value changes as red/green pairs in a TTY — the visual contrast with `git diff` finally favors kai.

## What we're validating

- **`kai spawn` as the primitive every multi-agent workflow needs.** First user is the live-sync demo; should generalize to CI sandboxes and reviewer "scratch this in a clean workspace" flows.
- **Default-on telemetry adoption rate.** PostHog is now the source of truth for CLI usage. Watching for opt-out spikes after the first-run notice ships.
- **BSL 1.1 doesn't scare adopters.** Homepage now links the license line; tracking referrer drop-off.

## Metrics

- 6 CLI releases (v0.12.0 → v0.13.3) + matching kai-server deploys
- ~30 commits across both repos this week
- 4 new docs / scripts (`demo.md`, `demo-30s.md`, `demo-livesync.md`, `setup-livesync.sh`/`layout-livesync.sh`)
- Net: −643 / +1100 lines server-side (telemetry tear-out + org-delete + admin endpoints)

## Bugs found / fixed

- `kai clone <org>/<repo>` shorthand never worked — parser prepended `https://` before checking for hostname-shaped first segment. Fixed.
- `set -e` in pasted setup blocks killed user terminals. Setup scripts moved to standalone files, run via `bash …`.
- VitePress docs build broke on a dead link in `demo-30s.md` because `demo.md` was untracked. Committed.
- `.kailab/workflows/ci.yml` queued runs against a runner fleet that's offline. Removed (GitHub Actions does the real CI for kai/kai).
- `kai diff` looked monochrome next to colorized `git diff`. Fixed: red/green for adds/removes, yellow for modifies, bold filenames, dim arrows.

## Lessons learned

- **Setup scripts in pasted markdown blocks are a foot-cannon.** `set -e` makes a non-zero return kill the shell. Always: extract to a file, run via `bash`.
- **Manual tmux choreography is fragile demo infrastructure.** `kai ui` as a real dashboard is what we should have built first.
- **Adoption-friction matters more than feature completeness.** The bridge isn't a "feature" — it's the only reason a single developer would even try kai on a team that hasn't bought in.
- **A one-line first-run notice changes opt-out from "creepy" to "honest."** Worth the engineering.

## Next week

1. Record and ship the 30-second + bridge + 4-agent live-sync demo videos.
2. Soak-test `kai spawn` / `kai ui` on real repos; identify what breaks beyond the demo path.
3. Land contract diffing in the review UI — first user of structured trust assertions on the rendering side.
