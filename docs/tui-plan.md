# Phase 2: TUI Shell — Implementation Plan

## Goal

A daily-driver terminal interface for Kai. `kai` with no subcommand drops the developer into a Bubble Tea app with three panes: REPL (input/output), sync (live agent activity), gate (held integrations). All existing `kai` subcommands keep working unchanged.

## Design principles

- **In-process.** TUI lives in the same Go module as `kai-cli`; imports `internal/*` directly. No IPC, no MCP boundary, no serialization overhead.
- **No new binary.** The TUI is the no-args mode of the existing `kai` binary. One install, one config, one set of permissions.
- **Engine-thin.** TUI views call existing engine APIs (`workspace.Manager`, `safetygate`, `watcher`, etc.). New engine code only when absolutely required.
- **Observe, don't gate.** The TUI watches the gate's verdicts; it doesn't make policy decisions. Approve/reject actions go through the same `Publish` chokepoint built in Phase 1.
- **Three views, one app.** REPL + sync + gate is the MVP. Plan/spawner views come in Phase 3.

## Non-goals (this phase)

- Not the planner. The REPL routes to existing cobra commands at first; LLM-driven planning is Phase 3.
- Not the spawner UI. `kai spawn` keeps working from the CLI; multi-agent orchestration view is Phase 3.
- Not editor integration. Use your editor alongside; TUI is a separate window.
- Not a desktop app. `kai-desktop/` exists separately; this is terminal-only.

## Architecture

```
kai (no args)
   ↓
internal/tui/app.go              ← Bubble Tea root model
   ├─ views/repl.go              ← input + streaming output
   ├─ views/sync.go              ← subscribes to watcher events
   ├─ views/gate.go              ← lists held snapshots, approve/reject
   ↓ in-process calls ↓
existing engine packages:
   internal/workspace/Publish    (gate approve action)
   internal/safetygate           (verdict types)
   internal/watcher              (file/agent activity stream)
   cobra command tree            (REPL routes here for v1)
```

## Components

### 1. Module setup
- Add `github.com/charmbracelet/bubbletea`, `lipgloss`, `bubbles/textinput`, `bubbles/viewport` to `kai-cli/go.mod`.
- Create `kai-cli/internal/tui/` package skeleton with `app.go` (root model) and a stub `Run()` entry point.

### 2. REPL view (`internal/tui/views/repl.go`)
- `bubbles/textinput` for the prompt line; history with arrow keys.
- `bubbles/viewport` for scrollable streaming output.
- Submitted text routes to a dispatcher: for v1, the dispatcher takes the input and runs the corresponding cobra command (`kai gate list` etc.) capturing stdout into the viewport.
- Status line shows current cwd + active workspace + held-count.

### 3. Sync pane (`internal/tui/views/sync.go`)
- Holds a `*watcher.Watcher`. Wires `OnUpdate`, `OnActivity`, `OnEdgeDeltas` callbacks to send Bubble Tea messages.
- Renders the last N file changes with timestamp, path, op (modified/created/deleted).
- Auto-scrolls; preserves history for the session.

### 4. Gate pane (`internal/tui/views/gate.go`)
- Calls a shared `ListHeld(*graph.DB) []*graph.Node` helper. **Extract from `cmd/kai/gate.go`** into `internal/safetygate/held.go` so both the cobra command and the TUI can call it.
- Renders held snapshots with verdict, blast, age. Up/down to select.
- Hotkeys: `a` approve (calls `workspace.PublishAtTarget(SkipGate=true)`), `r` reject (UpdateNodePayload `dismissed=true`), `enter` show details.

### 5. Layout + keybindings (`internal/tui/app.go`)
- Three-pane layout: gate top-left (1/3 width), sync top-right (2/3 width), REPL bottom (full width).
- `Ctrl+G` focus gate, `Ctrl+S` focus sync, `Ctrl+R` or `Esc` focus REPL.
- `Ctrl+C` exit (with confirmation if a long-running command is mid-flight).
- Resize-aware via `tea.WindowSizeMsg`.

### 6. No-args entry (`cmd/kai/tui.go`)
- Override `rootCmd.Run` so `kai` with no subcommand calls `tui.Run(ctx)`.
- Preserve all existing subcommands and their flags.
- Add a `--no-tui` escape hatch in case the user wants the bare cobra help instead.

## Implementation order

1. **Module setup** (deps + skeleton)
2. **No-args entry** that just prints "TUI placeholder" so we can verify wiring
3. **REPL view** (text input + viewport, dispatch to cobra)
4. **Extract `ListHeld` helper** to `internal/safetygate/held.go`
5. **Gate pane** (read-only first, then approve/reject hotkeys)
6. **Sync pane** (watcher subscription + render)
7. **Layout + keybindings** (assemble the three panes)

Steps 1–3 are sequential. Steps 4 and 5/6 can run in parallel once 3 lands.

## Risks / open questions

- **Watcher lifecycle.** The TUI needs to start a Watcher tied to the session. If one is already running (e.g. `kai live on` in another terminal), do we share or refuse? MVP: start fresh per TUI session; accept duplicate watchers as low-cost.
- **Cobra dispatch from REPL.** Easy path: shell out to `kai <args>`. Cleaner: invoke the cobra command tree in-process. Cleaner is faster and lets us capture errors better, but we have to redirect stdout/stderr. Start with shell-out, switch later if it's slow.
- **Long-running commands.** A `kai integrate` from the REPL takes seconds; the TUI must stream output, not block. Bubble Tea + a goroutine that emits messages fits this naturally.
- **Color and TTY detection.** Bubble Tea handles this for the most part, but we need to gracefully fall back to plain output when piped (e.g. `kai | grep`). Easy fix: only enter TUI mode when `isatty(stdin) && isatty(stdout)`.
- **Test coverage.** Bubble Tea apps are notoriously hard to unit-test. Plan: thin views that delegate to pure helpers (already extracted in step 4); test the helpers; smoke-test the views via Bubble Tea's `tea/teatest`.

## Success criteria

- `kai` with no args launches a working three-pane TUI on a real repo.
- REPL accepts `gate list`, `integrate`, `status` and shows their output.
- Sync pane updates within 1s of an external file edit.
- Gate pane lists held snapshots and approve/reject hotkeys mutate state correctly (verified by re-reading the DB after).
- Existing `kai integrate` / `kai gate list` / etc. still work identically when invoked with subcommands.
- TUI mode exits cleanly on `Ctrl+C`.
- No regressions in `cmd/kai` test suite.
