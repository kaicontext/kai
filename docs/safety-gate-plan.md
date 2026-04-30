# Safety Gate — Implementation Plan

## Goal

Add a single chokepoint that decides whether an agent's integrated changes become team-visible. Auto-promote safe changes, queue risky ones for human review, refuse load-bearing ones outright — without ever destroying the agent's work.

## Design principles

- **Refuse-to-promote, never roll back.** The agent's CoW workspace and the merged snapshot always survive. Only the `refMgr.Set` call (the publish step) is gated.
- **One chokepoint.** Every path that makes a snapshot team-visible flows through one helper. No caller can bypass the gate by forgetting to check.
- **Pure classifier.** The gate function is a pure read of graph + diff + config. No side effects, easy to test.
- **Configurable, not hard-coded.** Thresholds and protected paths live per-repo so teams can tune without code changes.
- **Inherit safety downstream.** `kai push` mirrors refs; once publish is gated, push is automatically gated too.

## Non-goals

- Not changing the watcher (`internal/watcher/`). File-system updates to the live graph are unrelated.
- Not changing the merge engine. Conflict detection stays exactly as it is.
- Not the TUI yet. Engine work first; TUI consumes the gate's output later.
- Not transitive blast radius. Depth-1 only; teams can opt into more later.
- Not touching `kai push` semantics. Push mirrors refs; that's already correct.

## Architecture

```
agent edits → workspace (private CoW)
   ↓
Manager.Integrate / IntegrateWithResolutions
   ├─ existing merge logic (unchanged)
   ├─ [NEW] safetygate.Classify(wsModified, graph, cfg) → Decision
   ├─ persists gateVerdict/gateReasons/gateBlastRadius on snapshot payload
   └─ returns IntegrateResult{ResultSnapshot, Decision}
   ↓
[NEW] Manager.Publish(result, targetRef, opts)
   ├─ if Decision.Verdict == Auto OR opts.SkipGate → refMgr.Set(targetRef, ResultSnapshot)
   ├─ else → leave ref unchanged, snapshot stays orphan in DB
   └─ always advance ws.<name>.head (local-only)

kai integrate / kai resolve → both call Publish (no inline refMgr.Set)
kai review                  → lists orphan snapshots; approve = Publish(SkipGate=true)
kai push                    → unchanged; ships whatever refs currently say
```

## Components

### 1. `kai-cli/internal/safetygate/` (new package)

```go
type Verdict string
const (
    Auto   Verdict = "auto"
    Review Verdict = "review"
    Block  Verdict = "block"
)

type Decision struct {
    Verdict     Verdict
    BlastRadius int
    Reasons     []string
    Touches     []string  // load-bearing symbols/paths hit
}

type Config struct {
    AutoThreshold  int       // blast ≤ this → Auto
    BlockThreshold int       // blast ≥ this → Block
    Protected      []string  // glob patterns; any match → Block
}

func Classify(ctx context.Context, wsModified []string, g *graph.DB, cfg Config) (Decision, error)
func LoadConfig(workDir string) (Config, error)
```

Implementation: depth-1 callers + dependents per modified path via existing `internal/graph` queries (same machinery `kai impact` uses). Sum unique node IDs touched = `BlastRadius`. Glob match against `Protected` first; if any hit, immediate `Block` regardless of radius.

### 2. `kai-cli/internal/workspace/integrate.go` (modify)

- Pull `wsModified` computation above the fast-forward check so both paths share it.
- Add `IntegrateOptions{SkipGate bool}` and a third entry point that takes it; existing `Integrate`/`IntegrateWithResolutions` pass `SkipGate=false`.
- Run `safetygate.Classify` after `wsModified` is known, before either return path commits.
- For the FF case, create a snapshot node anyway (small DB cost) so verdict metadata has somewhere to live.
- Persist `gateVerdict`, `gateReasons`, `gateBlastRadius` on the snapshot payload.
- Extend `IntegrateResult` with `Decision *safetygate.Decision`.

### 3. `kai-cli/internal/workspace/publish.go` (new file)

```go
type PublishOptions struct {
    SkipGate bool  // used by kai review approval
}

func (m *Manager) Publish(result *IntegrateResult, targetRef string, opts PublishOptions) error
```

Wraps the `refMgr.Set` patterns currently inlined in `runIntegrate` (cmd/kai/main.go:12781-12799) and the resolve flow (cmd/kai/resolve.go:194-220). Honors verdict; advances `ws.<name>.head` regardless.

### 4. `kai-cli/cmd/kai/main.go` and `resolve.go` (modify)

- Replace the inlined `refMgr.Set` blocks with `mgr.Publish(result, wsTarget, PublishOptions{})`.
- On `Verdict ∈ {Review, Block}`, print: "Change held for review. Run `kai review` to inspect."
- On `Block`, also print the protected-path or radius reason.

### 5. `kai-cli/cmd/kai/review.go` (new or extend `internal/review/`)

- `kai review list` — orphan snapshots with non-Auto verdicts.
- `kai review show <id>` — semantic summary (reuse `internal/review/summary.go`) plus gate reasons.
- `kai review approve <id>` — calls `Publish` with `SkipGate=true`.
- `kai review reject <id>` — marks snapshot dismissed (payload flag); doesn't delete.

### 6. `.kai/gate.yaml` config schema

```yaml
auto_threshold: 0       # blast radius ≤ this → auto
block_threshold: 50     # blast radius ≥ this → block
protected:              # any match → block
  - pkg/auth/**
  - internal/billing/**
```

Defaults if file missing: `auto=0`, `block=999999`, `protected=[]`.

## Implementation order

1. **`internal/safetygate/`** — module + tests with synthetic graph fixtures.
2. **`Manager.Publish`** helper + refactor existing two callers (no behavior change yet — gate not wired in).
3. **Wire gate into `integrateInternal`** — verdict computed and persisted, `IntegrateResult.Decision` populated.
4. **`Publish` honors verdict** — flip the switch. Non-Auto integrations stop moving team refs.
5. **`kai review`** commands.
6. **End-to-end test**: spawn → edit → integrate auto-promotes; spawn → edit protected file → integrate held; `kai review approve` promotes.

Steps 1–4 are sequential. Step 5 can run in parallel with step 4 once `Publish` exists. Step 6 closes the loop.

## Risks / open questions

- **Snapshot bloat from FF case.** Creating a snapshot node on fast-forwards just to carry verdict metadata. Mild DB cost; revisit if measurable.
- **Default thresholds.** `auto=0` is strict; might send too much to review for early adoption. Could default `auto=5`. Try strict first, loosen by feel.
- **Concurrent integrates from parallel workspaces.** Gate runs on workspace diff vs. base, not vs. live target — so it's not wrong, but reasons might be slightly outdated after a race. Acceptable.
- **`kai review reject` semantics.** Just marks the snapshot dismissed; workspace keeps going. Later integrate creates a fresh snapshot with a fresh verdict.
- **Telemetry.** Add `gate.verdict` / `gate.blast_radius` events to `internal/telemetry/` after step 4.

## Success criteria

- `safetygate.Classify` has unit tests covering: zero-blast, threshold edges, protected-path match, missing config.
- Existing `kai integrate` and `kai resolve` tests pass after the `Publish` refactor (no behavior change with default config).
- E2E: agent editing a protected file produces a snapshot with `gateVerdict=block`, `snap.latest` doesn't move, `kai review list` shows it, `kai review approve` advances `snap.latest`.
- `kai push` against a protected change confirms it doesn't ship until reviewed.

## Phasing (broader context)

This plan is **Phase 1** of the kai-tui buildout:

- **Phase 1 (this plan):** Safety gate. Engine work in `kai-cli/`. ~1 week.
- **Phase 2:** TUI shell in `kai-tui/`. Bubble Tea, in-process imports. REPL + sync + review panes. ~1–2 weeks.
- **Phase 3:** Planner + spawner integration. The TUI becomes the product the spec describes. Open-ended.
