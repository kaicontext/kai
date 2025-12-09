# Kai - AI Agent Guide

This project uses **Kai**, a semantic version control system. Unlike Git which tracks
line-by-line text changes, Kai understands code at a semantic level—identifying
functions, classes, variables, and how they relate to your project's architecture.

## What is Kai?

Kai is NOT a replacement for Git. It works alongside Git (or standalone) to provide:
- **Semantic snapshots** - Captures what your code means, not just what it looks like
- **Change classification** - Automatically detects if you added a function, changed a condition, etc.
- **Intent generation** - Creates human-readable summaries like "Update Auth login timeout"

Think of it as: Git tracks "line 42 changed from X to Y", Kai tracks "the login function's timeout was reduced from 1 hour to 30 minutes".

## 2-Minute Quick Start (Recommended)

Get Kai's value in 7 simple commands:

```bash
# 1. Initialize Kai
kai init

# 2. Scan your project (creates baseline snapshot)
kai scan

# 3. Make changes to your code...

# 4. See what changed semantically
kai diff

# 5. Scan again to capture your changes (updates working snapshot)
kai scan

# 6. Open a review (commits working snapshot, creates changeset)
kai review open --title "Fix bug"

# 7. Preview CI impact
kai ci plan --explain
```

That's it! You now have semantic diffs, change classification, and selective CI.

**Snapshot lifecycle:** First scan creates baseline (`@snap:last`). Subsequent scans update working (`@snap:working`). Review commits working as new baseline. Old working snapshots are GC'd by `kai prune`.

## The Core Commands

| Command | What it does |
|---------|-------------|
| `kai scan` | Snapshot + analyze in one step (recommended) |
| `kai diff` | Show semantic differences |
| `kai review open` | Create a code review |
| `kai ci plan` | Compute affected tests |

## Getting Started (Detailed)

If you want more control, here's the step-by-step approach:

### Step 1: Initialize Kai
```bash
kai init
```
This creates a `.kai/` directory with the database and object storage.

### Step 2: Create a Snapshot
```bash
# From directory (recommended)
kai snap .

# Or from Git branch/tag/commit
kai snapshot --git main
```

### Step 3: Make Changes and Diff
After modifying code:
```bash
kai scan                    # Re-scan with changes
kai diff                    # See semantic differences
```

### Step 4: Review and CI
```bash
kai review open --title "Fix login bug"
kai ci plan --explain       # See what tests to run
```

## Quick Reference

### Check Status
```bash
kai status                    # Show pending changes since last snapshot
```

### References (avoid typing long hashes)
- `@snap:last` - Most recent snapshot
- `@snap:prev` - Previous snapshot
- `@cs:last` - Most recent changeset
- `snap.main`, `cs.feature` - Named refs (create with `kai ref set snap.main @snap:last`)

### Remote Operations (sync with server)
```bash
kai clone http://server/org/repo           # Clone a repository (creates directory)
kai clone http://server/org/repo mydir     # Clone into specific directory
kai remote set origin https://kailab.example.com --tenant myorg --repo myproject
kai auth login                # Authenticate
kai push origin snap.latest   # Upload to server
kai fetch origin              # Download from server
```

## Key Concepts

| Concept | What it is | Analogy |
|---------|------------|---------|
| **Snapshot** | Semantic capture of codebase | Like a Git commit, but understands code structure |
| **ChangeSet** | Diff between two snapshots | Like `git diff`, but classifies change types |
| **Intent** | Human summary of changes | Like a commit message, but auto-generated |
| **Module** | Logical file grouping | Like folders, but by feature (Auth, Billing, etc.) |

## Change Types Kai Detects

| Type | What it means | Example |
|------|---------------|---------|
| `FUNCTION_ADDED` | New function created | Added `validateToken()` |
| `FUNCTION_REMOVED` | Function deleted | Removed `legacyAuth()` |
| `CONDITION_CHANGED` | If/comparison changed | `if (x > 100)` → `if (x > 50)` |
| `CONSTANT_UPDATED` | Literal value changed | `TIMEOUT = 3600` → `1800` |
| `API_SURFACE_CHANGED` | Function signature changed | Added parameter to function |
| `FILE_ADDED` | New file created | Added `auth/mfa.ts` |
| `FILE_DELETED` | File removed | Deleted `deprecated/old.ts` |

## Common Tasks

### "I want to see what changed in my code"
```bash
# Quick semantic diff (recommended)
kai diff @snap:last --semantic

# Or create a full changeset for detailed analysis
kai snapshot --dir .
kai analyze symbols @snap:last
kai changeset create @snap:prev @snap:last
kai dump @cs:last --json | jq '.nodes[] | select(.kind == "ChangeType")'
```

### "I want a semantic diff showing function/class changes"
```bash
kai diff @snap:prev @snap:last --semantic
# Output shows:
#   ~ auth/login.ts
#     ~ function login(user) -> login(user, token)
#     + function validateMFA(code)
#   Summary: 1 file, 2 units changed

# JSON output for programmatic use
kai diff @snap:prev @snap:last --json
```

### "I want to compare two Git branches"
```bash
kai snapshot main --repo .
kai snapshot feature-branch --repo .
kai analyze symbols @snap:prev
kai analyze symbols @snap:last
kai changeset create @snap:prev @snap:last
kai intent render @cs:last
```

### "I want to save a named reference"
```bash
kai ref set snap.main @snap:last      # Name the current snapshot
kai ref set snap.v1.0 abc123          # Name by ID
kai ref list                          # See all refs
```

## Accessing Raw Diffs (Ground Truth)

Kai provides semantic understanding, but raw Git diffs remain the authoritative source when you need to verify accuracy.

### When to Check the Raw Diff

| Scenario | Why Raw Diff Helps |
|----------|-------------------|
| **Ground truth** | If Kai miscategorizes a change (e.g., labels logic as "formatting"), the raw diff is authoritative |
| **Context visibility** | See actual code around changes, not just symbol names |
| **Uncategorized changes** | Catch formatting normalization, semicolon fixes—usually noise, but sometimes meaningful (e.g., ASI bugs in JS) |
| **Verification** | Confirm the structured summary matches what actually changed |

### Getting Both Views

```bash
# Kai's structured semantic analysis
kai dump @cs:last --json

# Raw Git diff for the same changes (ground truth)
git diff HEAD~1..HEAD

# Or use Kai's diff with --raw flag
kai diff @snap:prev @snap:last --raw
```

### Recommended Workflow

1. **Start with Kai** — Use structured data for speed and semantic understanding
2. **Verify when needed** — Check raw diff if something seems miscategorized
3. **Trust the diff** — If Kai and raw diff disagree, the diff is correct

## CI & Test Selection

Kai provides intelligent test selection for CI pipelines. Instead of running all tests on every change, analyze which tests are affected.

### Generate a Test Plan

```bash
# Generate test selection plan from a changeset
kai ci plan @cs:last --out plan.json

# Human-readable explanation
kai ci plan @cs:last --explain

# Force full suite (panic switch)
KAI_FORCE_FULL=1 kai ci plan @cs:last --out plan.json
```

### Safety Modes

| Mode | Description |
|------|-------------|
| `shadow` | Compute plan but run full suite. Compare predictions to learn. |
| `guarded` | Run selective with auto-fallback on risk. Default mode. |
| `strict` | Run selective only. Use panic switch for full suite. |

```bash
kai ci plan @cs:last --safety-mode=shadow   # Learning phase
kai ci plan @cs:last --safety-mode=guarded  # Safe default
kai ci plan @cs:last --safety-mode=strict   # High confidence
```

### Find Affected Tests

```bash
# Which tests are affected by changes between two snapshots?
kai test affected @snap:prev @snap:last

# Uses import graph tracing to find transitive dependencies
```

### Structural Risks Detected

| Risk | Severity | Meaning |
|------|----------|---------|
| `config_change` | High | package.json, tsconfig, etc. changed |
| `test_infra` | High | Fixtures, mocks, setup files changed |
| `dynamic_import` | High | Dynamic require/import detected |
| `no_test_mapping` | Medium | Changed files have no test coverage |
| `cross_module_change` | Medium | Changes span 3+ modules |

## Troubleshooting

| Error | Fix |
|-------|-----|
| "Kai not initialized" | Run `kai init` first |
| "No snapshots found" | Create one with `kai snapshot --dir .` |
| "ambiguous prefix" | Use more characters of the ID, or use `@snap:last` |

## More Information

Run `kai --help` or `kai <command> --help` for detailed usage.
