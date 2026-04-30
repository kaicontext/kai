# Phase 3: Planner + Agent Spawner — Implementation Plan

## Goal

The REPL stops shelling out to CLI commands and starts accepting natural language. A developer types "add rate limiting to the API" and Kai produces a work plan, launches agents in spawned workspaces, runs the safety gate on every integration, and reports back when everything's done.

## Critical context (from spawn read-through)

**Kai spawns workspaces, not processes.** `kai spawn` materializes a CoW directory, runs `kai init` + `kai capture` + `kai ws create`, registers the entry, and returns. It does not start an agent. The `--agent` flag is a label.

This shapes everything below: the **orchestrator owns subprocess lifecycle**. Spawn stays untouched. The orchestrator launches an agent command in each spawned dir, waits for exit, then uses push/pull to bring the work into the main repo for integration.

## Design principles

- **Spawn unchanged.** No edits to `cmd/kai/spawn.go` or `internal/spawn/`.
- **Subprocess exit = done signal.** No new completion semantics; rely on the agent process exiting when its task is complete (e.g. `claude -p "<prompt>"`).
- **Configurable agent command.** Template in `.kai/config.yaml` so users can plug in Claude / Cursor / Codex / future tools. Default to Claude Code non-interactive.
- **Integration via existing push/pull.** Agents push from spawn dirs; orchestrator pulls + integrates in main repo. Gate fires automatically. No new sync code.
- **No file-boundary enforcement at v1.** Pass `Files` and `DontTouch` only as prompt content. Build sandboxing later if agents actually wander.
- **All-parallel, no DependsOn.** Push/pull adds enough latency; ordering would feel slow. Live sync handles inter-agent visibility.
- **One-shot replanning.** Feedback gets one more LLM call. Not a conversation.

## Non-goals

- No model selection UI. Hardcode Claude Sonnet for v1.
- No cost estimation or token budgets. Ship, measure, optimize later.
- No automatic retries on agent failure.
- No agent-to-agent communication beyond live sync.
- No sandbox enforcement of file boundaries.
- No agent count cap UI; default 5, override via config.
- No editing of spawn or watcher; Phase 1 + 2 untouched.

## Architecture

```
REPL input (natural language)
   ↓
internal/planner/
   ├─ resolve symbols against graph (depth-1)
   ├─ build context (paths, dependents, protected globs)
   ├─ one LLM call → JSON WorkPlan
   └─ return WorkPlan (Summary, Agents[], RiskNotes[])
   ↓
REPL renders plan, waits for go / feedback / cancel
   ↓ (on "go")
internal/orchestrator/
   ├─ for each AgentTask:
   │    ├─ build prompt via internal/agentprompt/
   │    ├─ kai spawn → CoW dir
   │    ├─ exec.Cmd(agent_command, prompt)  ← subprocess
   │    └─ wait for exit
   ├─ in parallel: kai push from each spawn dir (via existing CLI)
   ├─ in main: kai pull + Manager.Integrate per agent (gate runs)
   ├─ optionally: kai despawn (cleanup)
   └─ report aggregate result back to REPL

Sync pane (Phase 2) shows agent activity in real time via watcher.
Gate pane (Phase 2) shows held integrations as they happen.
```

## Components

### 1. `internal/agentprompt/` (new, simplest)

Pure function. Takes an `AgentTask` and repo context, returns a prompt string.

```go
type Context struct {
    RepoRoot     string
    Language     string
    GraphContext string  // pre-rendered "callers/dependents" snippet for the agent's files
    Protected    []string
}

func Build(task planner.AgentTask, ctx Context) string
```

Prompt structure:
- One-line "you are agent X working on Y"
- "Files you may modify: ..." (from `task.Files`)
- "Files you must not modify: ..." (from `task.DontTouch` + protected globs)
- Graph context for those files
- "Use `kai_checkpoint` periodically; the human will review your work via the safety gate"

This package will iterate based on real usage. Easy to unit-test against fixtures.

### 2. `internal/planner/` (one LLM call)

```go
type WorkPlan struct {
    Summary   string
    Agents    []AgentTask
    RiskNotes []string
}

type AgentTask struct {
    Name      string
    Prompt    string  // semantic description; agentprompt.Build wraps it
    Files     []string
    DontTouch []string
}

type Config struct {
    Model     string  // default "claude-sonnet-4-6"
    MaxAgents int     // default 5
}

func Plan(ctx context.Context, request string, g *graph.DB, gateCfg safetygate.Config, cfg Config) (*WorkPlan, error)
func Replan(ctx context.Context, original string, feedback string, g *graph.DB, gateCfg safetygate.Config, cfg Config) (*WorkPlan, error)
```

Flow:
1. Resolve named symbols/files in the request via `graph.FindNodesByPayloadPath` + simple keyword match.
2. Build context payload: resolved files, depth-1 callers/dependents, protected globs from gateCfg, top-level dir tree.
3. Single Anthropic API call. System prompt instructs JSON output matching WorkPlan schema. Use `internal/ai/client.go` for the call.
4. Parse response. On parse error, return the raw response so REPL can show "I couldn't parse that — try rephrasing."
5. Refuse vague requests: if the LLM returns 0 agents or empty tasks, return an error like "request too vague to plan."

`Replan` appends the feedback to the original request and calls Plan once. No conversation loop.

### 3. `internal/orchestrator/` (subprocess lifecycle + integrate)

```go
type AgentRun struct {
    Task        planner.AgentTask
    SpawnDir    string
    AgentCmd    *exec.Cmd
    ExitErr     error
    Verdict     *workspace.IntegrationDecision
    AdvancedRefs []string  // populated if auto-promoted
}

type Result struct {
    Runs        []AgentRun
    AutoPromoted int
    Held         int
    Failed       int
}

type AgentCommandSpec struct {
    Argv []string  // e.g. ["claude", "-p", "{prompt}"]
}

type Config struct {
    AgentCommand   AgentCommandSpec  // from .kai/config.yaml
    Despawn        bool              // tear down spawn dirs after integrate
    PushRemote     string            // default "origin"
}

func Execute(ctx context.Context, plan *planner.WorkPlan, cfg Config, db *graph.DB, kaiDir, workDir string) (*Result, error)
```

Execute steps:
1. For each AgentTask: shell out `kai spawn --agent <name> --count 1 <path>` to provision (reuses existing CLI).
2. For each spawn: build prompt via agentprompt, write it to a temp file, exec `cfg.AgentCommand.Argv` with `{prompt}` substituted.
3. Wait for all subprocesses to exit (parallel; use errgroup or sync.WaitGroup).
4. For each spawn dir: shell out `kai push origin` (existing CLI).
5. In main repo: shell out `kai pull origin`, then in-process `Manager.Integrate(wsName, currentTarget)`. Gate runs.
6. Capture verdict per agent. Aggregate into Result.
7. If `Despawn`, shell out `kai despawn <path>` per spawn.

Subprocess output is captured to disk (`<spawn-dir>/.kai/agent.log`) and surfaced to the sync pane via the existing watcher (it'll see file activity).

### 4. REPL integration (`internal/tui/views/repl.go`)

New code path: when input is **not** a recognized cobra subcommand, route to planner instead of shelling out.

Detection: lookup the first whitespace-split token in `rootCmd.Commands()`. If found, shell out as today. If not found, treat the entire input as a planner request.

Reserved keywords inside a plan-pending state:
- `go` → call `orchestrator.Execute` with the pending plan
- `cancel` → discard the pending plan
- anything else → treat as feedback, call `Replan`

Pending plan rendering: show Summary + Agents list + RiskNotes in scrollback, prompt with `[go / edit / cancel]>`.

### 5. `.kai/config.yaml` schema additions

```yaml
agent:
  command: ["claude", "-p", "{prompt}"]
  timeout: 600   # seconds; kill if agent runs too long

planner:
  model: claude-sonnet-4-6
  max_agents: 5
```

Loader in `internal/config/` (new tiny package) returning a `Config{Agent, Planner}`. Defaults if missing.

## Implementation order

1. **`internal/config/`** — yaml loader. Two structs, defaults. Pure code, easy tests.
2. **`internal/agentprompt/`** — pure function builder + golden-file tests.
3. **`internal/planner/`** — symbol resolution + LLM call + JSON parse. Tests with mocked `ai.Client`. One real-LLM smoke test gated on env var.
4. **`internal/orchestrator/`** — subprocess management + push/pull/integrate. Tests with a fake agent command (e.g. a script that writes a file and exits).
5. **REPL integration** — detect unrecognized commands, render plans, dispatch to orchestrator. Smoke test via teatest.
6. **End-to-end test** — local-loopback remote, fake agent command, verify spawn → exec → push → pull → integrate → gate → result aggregation.

Steps 1–2 sequential. Step 3 can run in parallel with 4 (different concerns). 5 needs 3+4. 6 needs 5.

## Risks / open questions

- **Agent log streaming to sync pane.** The watcher sees file activity in spawn dirs only if they're under `WorkDir`. They're not — spawns go to `/tmp/kai-*`. Either start a second watcher per spawn, or have the orchestrator emit synthetic events. Lean second-watcher; small code.
- **Cancel mid-execution.** If the user hits Ctrl+C during agent runs, kill subprocesses cleanly. Use `exec.CommandContext` so context cancellation propagates.
- **Push/pull failures.** If a remote isn't configured, the orchestrator must fail loudly. v1 surface: "configure a remote with `kai remote set origin <url>` before using the planner."
- **Concurrent integrates.** Phase 1 already considered this — gate runs on workspace diff vs. base, not vs. live target, so out-of-order arrivals are correct, just slightly stale on reasons.
- **Vague requests.** Planner refuses with "request too vague — name a file, package, or feature." REPL shows the message and waits for new input.
- **Token cost.** No budgets in v1. Document expected cost per plan (one call, ~5–20K tokens) so users aren't surprised.
- **What if `kai_checkpoint` isn't installed in the agent's MCP?** Agents may not have it. Prompt should mention it as preferred but not required; the watcher catches file changes anyway.

## Success criteria

- `internal/planner.Plan` returns a parseable WorkPlan for a real request against this repo. ✅
- `internal/orchestrator.Execute` runs a fake-agent script in a spawned workspace. ✅ (e2e test)
- REPL: typing `add a comment to README.md` produces a plan, `go` runs it, `kai gate list` shows the verdict. (manual)
- Existing `cmd/kai` test suite still passes — no regressions from the REPL detection logic. ✅
- E2E test green up to the push step. Full push/pull/integrate requires a running kailab server; covered by manual recipe below. ✅

## Important finding — kai's remote is HTTP-only

The original spec assumed a `file://` loopback remote for the e2e test. **Kai's remote is HTTP-only (kailab server).** A true e2e test of push/pull/integrate needs either:

- A running kailab server on a known port, or
- An httptest mock of kailab's API surface

Both are heavier than v1 warrants. The e2e test in `internal/orchestrator/e2e_test.go` runs spawn + agent for real (gated on `KAI_BIN`) and asserts that push fails predictably — proving the pipeline reaches the integrate phase before hitting infra limits. Full pipeline is verified manually:

### Manual e2e recipe

1. Start kailab server (per `kai-server/` repo's setup).
2. `kai init` in a fresh repo, `kai capture -m baseline`.
3. `kai remote set origin http://localhost:7447` (or wherever kailab runs).
4. Set `ANTHROPIC_API_KEY`, run `kai`.
5. Type a request (e.g. "add a one-line comment to README.md").
6. The plan appears in the REPL; type `go`.
7. Watch the sync pane for activity. Watch the gate pane for held integrations.
8. Type `kai gate list` (or `kai push`) to confirm.

A future Phase 3.x task is "build a kailab httptest mock for orchestrator e2e."

## What stays untouched

- `internal/spawn/`, `pkg/spawn/` — no edits.
- `internal/safetygate/` — no edits.
- `internal/workspace/` — no edits (Phase 1 already covers what we need).
- `internal/watcher/` — no edits.
- `internal/tui/` — only `views/repl.go` gains the planner path; gate and sync panes unchanged.
- All existing CLI subcommands keep working.
