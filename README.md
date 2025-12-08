<p align="center">
  <img src="kai.webp" alt="Kai Logo" width="400">
</p>

# Kai - Intent Version Control System

Kai is a semantic, intent-based version control system that understands *what* your code changes mean, not just *what* changed. It creates semantic snapshots from Git refs, computes changesets with classified change types, maps changes to logical modules, and generates human-readable intent sentences describing the purpose of changes.

Unlike traditional diff tools that show line-by-line text changes, Kai understands your code at a semantic level—identifying functions, classes, variables, and how they relate to your project's architecture.

## Design Principles

### Idempotency
Every command in Kai is idempotent—the same command always produces the same result, regardless of when or how many times you run it. There is no hidden state that changes behavior.

```bash
kai push origin --ws feature/auth   # Always pushes feature/auth
kai snapshot --dir ./src            # Same dir = same snapshot hash
kai changeset create snap.a snap.b  # Same inputs = same changeset
```

This is possible because everything in Kai is **content-addressed** and **immutable**. Objects are identified by their content hash, not by mutable pointers. Commands are explicit about what they operate on.

### Immutability
Once created, snapshots and changesets never change. They are permanent records identified by cryptographic hashes. This enables:
- Safe concurrent operations (no race conditions)
- Reliable caching and deduplication
- Trustworthy history that can't be rewritten

### Semantic Over Syntactic
Kai operates on meaning, not text. Instead of "line 47 changed," Kai tells you "function `validateToken` signature changed." This makes changes understandable to humans and machines alike.

### Explicit Over Implicit
Commands require explicit targets rather than relying on hidden state. There's no "current workspace" that changes behavior—you always specify what you're operating on.

### Speed as a Feature
Kai is designed to be fast enough that you never wait. Content-addressed storage enables O(1) lookups. Push/pull operations transfer only missing objects. Snapshots are computed in parallel. The goal is sub-second response times for all common operations.

## Table of Contents

- [Design Principles](#design-principles)
- [Key Concepts](#key-concepts)
- [Kai vs Git](#kai-vs-git)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Human-Friendly References](#human-friendly-references)
- [Workspace Workflow](#workspace-workflow)
- [Complete Workflow Tutorial](#complete-workflow-tutorial)
- [Command Reference](#command-reference)
- [CI & Test Selection](#ci--test-selection)
- [Configuration](#configuration)
- [Understanding the Output](#understanding-the-output)
- [Architecture Deep Dive](#architecture-deep-dive)
- [Use Cases](#use-cases)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [Kailab Server](#kailab-server)
- [Kailab Control Plane](#kailab-control-plane)
- [Northstar & Critical Risks](#northstar--critical-risks)
- [Roadmap](#roadmap)

---

## Key Concepts

### Snapshots
A **snapshot** is a semantic capture of your codebase at a specific Git ref (branch, tag, or commit). Unlike a Git commit which stores file diffs, a snapshot stores:
- All TypeScript/JavaScript files with their content hashes
- Parsed symbol information (functions, classes, variables)
- Module associations based on path patterns

### Symbols
**Symbols** are the semantic units extracted from your code:
- **Functions**: Named functions, arrow functions, methods
- **Classes**: Class declarations with their methods
- **Variables**: Constants and variable declarations

Each symbol includes its name, kind, source range, and signature.

### ChangeSets
A **changeset** represents the semantic difference between two snapshots:
- Which files were modified
- Which symbols were affected
- What types of changes occurred
- Which modules are impacted

### Change Types
Kai classifies changes into semantic categories:

**Code-Level Changes:**

| Change Type | Description | Example |
|-------------|-------------|---------|
| `FUNCTION_ADDED` | New function defined | Added `validateToken()` |
| `FUNCTION_REMOVED` | Function deleted | Removed `legacyAuth()` |
| `CONDITION_CHANGED` | Logic/comparison operators or boundaries changed | `if (x > 100)` → `if (x > 50)` |
| `CONSTANT_UPDATED` | Literal values (numbers, strings) changed | `const TIMEOUT = 3600` → `1800` |
| `API_SURFACE_CHANGED` | Function signatures or exports changed | `login(user)` → `login(user, token)` |

**File-Level Changes:**

| Change Type | Description | Example |
|-------------|-------------|---------|
| `FILE_ADDED` | New file created | Added `auth/mfa.ts` |
| `FILE_DELETED` | File removed | Removed `deprecated/old.ts` |
| `FILE_CONTENT_CHANGED` | Non-parseable file modified | Binary or unsupported file changed |

**JSON Changes:**

| Change Type | Description | Example |
|-------------|-------------|---------|
| `JSON_FIELD_ADDED` | New key added to JSON | Added `"timeout": 30` to config |
| `JSON_FIELD_REMOVED` | Key removed from JSON | Removed `"legacy"` field |
| `JSON_VALUE_CHANGED` | Value changed for existing key | `"version": "1.0"` → `"2.0"` |
| `JSON_ARRAY_CHANGED` | Array elements modified | Dependencies array changed |

**YAML Changes:**

| Change Type | Description | Example |
|-------------|-------------|---------|
| `YAML_KEY_ADDED` | New key added to YAML | Added `replicas: 3` |
| `YAML_KEY_REMOVED` | Key removed from YAML | Removed deprecated config |
| `YAML_VALUE_CHANGED` | Value changed for existing key | `port: 8080` → `port: 3000` |

### Modules
**Modules** are logical groupings of files defined by path patterns. They help organize changes by feature area (e.g., "Auth", "Billing", "Profile").

### Intent
An **intent** is a human-readable sentence summarizing what a changeset accomplishes, like "Update Auth login" or "Modify Billing invoice calculation".

### Workspaces
A **workspace** is a lightweight, mutable branch-like overlay on top of immutable snapshots. Workspaces allow you to:
- Accumulate multiple staged changes (as ChangeSets)
- Isolate work without affecting the main snapshot lineage
- Integrate (merge) changes back into a target snapshot

Workspaces have three states: `active` (can stage changes), `shelved` (frozen), and `closed` (permanent).

---

## Kai vs Git

| Feature | Kai | Git |
|---------|-----|-----|
| **Push / fetch latency** | < 100 ms globally | Depends on repo size / RTT |
| **Change understanding** | Semantic (functions, classes, intent) | Line-based diffs |
| **Storage model** | Content-addressed SQLite + zstd segments | Packfiles with delta compression |
| **Merge conflicts** | AST-aware semantic merge | Text-based 3-way merge |
| **History queries** | "What changed this function?" | `git log -p --follow` |
| **Repository size scaling** | O(1) push/fetch via negotiation | O(repo size) for clone |

Kai is designed to complement Git, not replace it. Use Git for your source of truth and Kai for semantic analysis, faster remote sync, and intent-based workflows.

---

## Installation

### Prerequisites

- **Go 1.22+** (uses Go 1.24 features)
- **Git** (optional - only needed for Git-based snapshots)

### Building from Source

```bash
# Clone or navigate to the kai directory
cd kai/kai-cli

# Build the CLI binary
make build

# Or build directly with Go
go build -o kai ./cmd/kai

# Verify installation
./kai --help
```

### Optional: Add to PATH

```bash
# Add to your shell profile for global access
export PATH="$PATH:/path/to/kai/kai-cli"

# Or move/symlink to a directory in your PATH
sudo ln -s /path/to/kai/kai-cli/kai /usr/local/bin/kai
```

---

## Quick Start

Here's the fastest way to get started with Kai:

```bash
# 1. Navigate to your Git repository
cd your-typescript-project

# 2. Initialize Kai
kai init

# 3. Create a snapshot of your main branch
kai snapshot main --repo .
# Output: Created snapshot: abc123...

# 4. Create a snapshot of your feature branch
kai snapshot feature-branch --repo .
# Output: Created snapshot: def456...

# 5. Analyze symbols in both snapshots
kai analyze symbols abc123...
kai analyze symbols def456...

# 6. Create a changeset between them
kai changeset create abc123... def456...
# Output: Created changeset: ghi789...

# 7. Generate an intent sentence
kai intent render ghi789...
# Output: Intent: Update Auth login

# 8. View the full changeset as JSON
kai dump ghi789... --json
```

---

## Human-Friendly References

You don't need to juggle 64-character BLAKE3 hashes! Kai provides multiple ways to reference snapshots and changesets using human-readable handles.

### Short IDs

Use the first 8-12 hex characters of any ID. Kai will resolve it if unambiguous:

```bash
# Instead of the full 64-char ID:
kai analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc

# Use a short prefix:
kai analyze symbols d9ec9902
```

If the prefix is ambiguous, Kai shows matching candidates:

```
ambiguous prefix '4a2556c0' matches:
  Snapshot 4a2556c086b1... created 2024-12-02 14:32Z
  ChangeSet 4a2556c0f31a... intent="Update Auth login"
provide more characters or use a ref
```

### Named References (Refs)

Create and use named pointers like Git does:

```bash
# Create refs
kai ref set snap.main d9ec9902
kai ref set snap.feature 4a2556c0
kai ref set cs.login_fix 90cd7264

# List all refs
kai ref list

# Use refs in any command
kai analyze symbols snap.main
kai changeset create snap.main snap.feature
kai intent render cs.login_fix
kai dump cs.login_fix --json

# Delete refs
kai ref del cs.login_fix
```

### Auto-Updated Refs

Kai automatically maintains helpful refs:

| Ref | Updated When | Points To |
|-----|--------------|-----------|
| `snap.latest` | Creating a snapshot | The most recent snapshot |
| `cs.latest` | Creating a changeset | The most recent changeset |
| `ws.<name>.base` | Creating a workspace | The workspace's base snapshot |
| `ws.<name>.head` | Staging changes | The workspace's head snapshot |

```bash
# Use auto-refs in commands
kai analyze symbols snap.latest
kai changeset create @snap:prev @snap:last
kai intent render cs.latest
```

### Selectors

Use selector syntax for dynamic references:

| Selector | Meaning |
|----------|---------|
| `@snap:last` | The most recent snapshot |
| `@snap:prev` | The second-most recent snapshot |
| `@cs:last` | The most recent changeset |
| `@cs:prev` | The second-most recent changeset |
| `@cs:last~2` | Two changesets back (relative navigation) |
| `@ws:name:head` | The head snapshot of workspace "name" |
| `@ws:name:base` | The base snapshot of workspace "name" |

```bash
# Common workflow using selectors
kai snapshot main --repo .
kai snapshot feature --repo .
kai analyze symbols @snap:last
kai changeset create @snap:prev @snap:last
kai intent render @cs:last
kai dump @cs:last --json
```

### Pick Command

Search and select nodes interactively:

```bash
# List recent snapshots
kai pick Snapshot

# Filter by substring
kai pick Snapshot --filter auth

# List changesets without interactive selection
kai pick ChangeSet --no-ui

# Output format shows ID, kind-specific info
#   %-4s  %-16s  %s
1     d9ec990243e5...  main (git)
2     4a2556c086b1...  feature (git)
```

### Shell Completion

Enable tab completion for refs, selectors, and short IDs:

```bash
# Bash
source <(kai completion bash)
# Add to ~/.bashrc for persistence

# Zsh
source <(kai completion zsh)
# Add to ~/.zshrc for persistence

# Fish
kai completion fish | source
# Or save to ~/.config/fish/completions/kai.fish

# PowerShell
kai completion powershell | Out-String | Invoke-Expression
```

With completion enabled, pressing Tab will suggest:
- Named refs (`snap.main`, `cs.latest`)
- Selectors (`@snap:last`, `@cs:prev`)
- Recent short IDs

### Complete Example Without Long IDs

```bash
# Initialize and create snapshots
kai init
kai snapshot main --repo .
kai snapshot feature --repo .

# Analyze using selectors
kai analyze symbols @snap:last
kai analyze symbols @snap:prev

# Create and inspect changeset
kai changeset create @snap:prev @snap:last
kai intent render @cs:last
kai dump @cs:last --json

# Name important refs
kai ref set snap.main @snap:prev
kai ref set snap.feature @snap:last
kai ref set cs.feature_changes @cs:last

# Use your named refs
kai changeset create snap.main snap.feature
kai checkout snap.main --dir ./restore
```

---

## Complete Workflow Tutorial

This tutorial walks through a real-world example using the included test repository.

### Step 1: Understanding the Test Repository

The `testdata/repo` directory contains a sample TypeScript project with two branches:

**main branch** (before):
```typescript
// auth/session.ts
export function isSessionExpired(createdAt: number): boolean {
  const age = (now - createdAt) / 1000;
  if (age > 3600) {  // 1 hour
    return true;
  }
  return false;
}

// auth/constants.ts
export const TIMEOUT = 3600;

// auth/login.ts
export function login(user: User, device: string): boolean {
  // ...
}
```

**feature branch** (after):
```typescript
// auth/session.ts - CONDITION_CHANGED
if (age > 1800) {  // Changed from 3600 to 1800 (30 minutes)

// auth/constants.ts - CONSTANT_UPDATED
export const TIMEOUT = 1800;  // Changed from 3600

// auth/login.ts - API_SURFACE_CHANGED
export function login(user: User, device: string, ip: string): boolean {
  // Added 'ip' parameter
}
```

### Step 2: Initialize Kai

```bash
cd testdata/repo
kai init
```

**What happens:**
- Creates `.kai/` directory
- Initializes SQLite database with schema
- Creates `objects/` directory for content storage
- Copies default rule files to `rules/`
- Creates `AGENTS.md` guide for AI assistants

**Output:**
```
Initialized Kai in .kai/
```

**Directory structure created:**
```
.kai/
├── AGENTS.md          # AI agent guide
├── db.sqlite          # SQLite database
├── objects/           # Content-addressed file storage
└── rules/
    ├── modules.yaml       # Module definitions
    └── changetypes.yaml   # Change type rules
```

### Step 3: Create Snapshots

Create a snapshot of the main branch:

```bash
kai snapshot main --repo .
```

**Output:**
```
Created snapshot: d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc
```

Create a snapshot of the feature branch:

```bash
kai snapshot feature --repo .
```

**Output:**
```
Created snapshot: 4a2556c086b1f664eaa5642e3bc0cddaa7423759d077701981e8e7e5ab0d39a3
```

**What happens during snapshot creation:**
1. Resolves the Git ref to a commit
2. Reads all TypeScript/JavaScript files from the commit tree
3. Computes BLAKE3 hash of each file's content
4. Stores file content in `objects/<hash>`
5. Creates File nodes in the database
6. Maps files to modules based on path patterns
7. Creates the Snapshot node with metadata

### Step 4: Analyze Symbols

Extract symbols from each snapshot:

```bash
# Analyze main branch snapshot
kai analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc

# Analyze feature branch snapshot
kai analyze symbols 4a2556c086b1f664eaa5642e3bc0cddaa7423759d077701981e8e7e5ab0d39a3
```

**Output:**
```
Symbol analysis complete
```

**What happens during symbol analysis:**
1. Retrieves all files in the snapshot
2. Parses each file using Tree-sitter (JavaScript grammar)
3. Extracts function declarations, class declarations, and variable declarations
4. Creates Symbol nodes with:
   - `fqName`: Fully qualified name (e.g., `User.greet`)
   - `kind`: function, class, or variable
   - `range`: Source code location (line/column)
   - `signature`: Function signature or declaration
5. Creates DEFINES_IN edges linking symbols to files

### Step 5: Create a ChangeSet

Compare the two snapshots:

```bash
kai changeset create \
  d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc \
  4a2556c086b1f664eaa5642e3bc0cddaa7423759d077701981e8e7e5ab0d39a3
```

**Output:**
```
Created changeset: 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203
Changed files: 3
Change types detected: 9
Affected modules: [Auth]
```

**What happens during changeset creation:**
1. Compares file digests between snapshots to find changed files
2. For each changed file:
   - Reads before/after content
   - Parses both versions with Tree-sitter
   - Runs change type detectors
3. Creates ChangeType nodes with evidence
4. Maps changed files to affected modules
5. Creates edges:
   - `MODIFIES`: ChangeSet → File, ChangeSet → Symbol
   - `HAS`: ChangeSet → ChangeType
   - `AFFECTS`: ChangeSet → Module

### Step 6: Render Intent

Generate a human-readable intent sentence:

```bash
kai intent render 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203
```

**Output:**
```
Intent: Update Auth TIMEOUT
```

**How intent is generated:**
1. Analyzes change types to determine verb:
   - `API_SURFACE_CHANGED` → "Update"
   - `CONDITION_CHANGED` → "Modify"
   - `CONSTANT_UPDATED` → "Update"
2. Identifies primary affected module (Auth)
3. Finds most prominent symbol or path area (TIMEOUT)
4. Combines: `<Verb> <Module> <Symbol>`

**Override with custom intent:**
```bash
kai intent render 90cd72... --edit "Reduce session timeout to 30 minutes"
```

### Step 7: Dump ChangeSet as JSON

View the complete changeset data:

```bash
kai dump 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203 --json
```

This outputs a structured JSON document containing:
- The changeset node with its payload
- All related nodes (files, symbols, change types, modules)
- All edges connecting them

---

## Command Reference

### `kai init`

Initialize Kai in the current directory.

```bash
kai init
```

**Creates:**
- `.kai/db.sqlite` - SQLite database with WAL mode
- `.kai/objects/` - Content-addressed storage directory
- `.kai/rules/modules.yaml` - Module definitions
- `.kai/rules/changetypes.yaml` - Change type rules
- `.kai/AGENTS.md` - AI agent guide for understanding Kai commands

**Notes:**
- Run this once per project
- Must be run from within a Git repository (or specify `--repo`)
- Safe to run multiple times (idempotent)

---

### `kai snapshot`

Create a semantic snapshot from a Git ref or directory.

```bash
kai snapshot [git-ref] [flags]
```

**Arguments:**
- `[git-ref]` - Branch name, tag, or commit hash (required for Git mode)

**Flags:**
- `--repo <path>` - Path to Git repository (default: current directory)
- `--dir <path>` - Path to directory (creates snapshot without Git)

**Examples:**
```bash
# Snapshot from Git ref
kai snapshot main --repo .

# Snapshot from directory (no Git required)
kai snapshot --dir ./src

# Snapshot a specific commit
kai snapshot abc123def456

# Snapshot a tag
kai snapshot v1.2.3 --repo /path/to/repo
```

**Output:**
```
Created snapshot: <64-character-hex-id>
```

**Supported file types:**
- `.ts`, `.tsx` - TypeScript
- `.js`, `.jsx` - JavaScript
- `.py` - Python
- `.json` - JSON
- `.yaml`, `.yml` - YAML
- `.sql` - SQL schemas

---

### `kai analyze symbols`

Extract symbols from all files in a snapshot.

```bash
kai analyze symbols <snapshot-id>
```

**Arguments:**
- `<snapshot-id>` - Hex ID of the snapshot to analyze

**Examples:**
```bash
kai analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc
```

**Output:**
```
Symbol analysis complete
```

**Extracted symbols:**
| Symbol Type | Examples |
|-------------|----------|
| Functions | `function foo()`, `const bar = () => {}`, `async function baz()` |
| Classes | `class User {}`, `class extends Component {}` |
| Methods | `greet() {}`, `static create() {}`, `async fetch() {}` |
| Variables | `const X = 1`, `let y = "str"`, `var z = []` |

**Symbol properties:**
- `fqName` - Fully qualified name (e.g., `ClassName.methodName`)
- `kind` - `function`, `class`, or `variable`
- `range` - Start and end positions `{start: [line, col], end: [line, col]}`
- `signature` - Declaration signature (e.g., `function login(user, device)`)

---

### `kai changeset create`

Create a changeset between two snapshots.

```bash
kai changeset create <base-snapshot-id> <head-snapshot-id>
```

**Arguments:**
- `<base-snapshot-id>` - The "before" snapshot (typically main branch)
- `<head-snapshot-id>` - The "after" snapshot (typically feature branch)

**Examples:**
```bash
kai changeset create abc123... def456...
```

**Output:**
```
Created changeset: <64-character-hex-id>
Changed files: 3
Change types detected: 5
Affected modules: [Auth, Billing]
```

**Change detection process:**
1. Files are compared by content hash (not line-by-line diff)
2. Modified files are parsed with Tree-sitter
3. AST nodes are compared to detect semantic changes
4. Changes are classified into types with evidence

---

### `kai intent render`

Generate or set an intent sentence for a changeset.

```bash
kai intent render <changeset-id> [flags]
```

**Arguments:**
- `<changeset-id>` - Hex ID of the changeset

**Flags:**
- `--edit "<text>"` - Manually set the intent text instead of generating

**Examples:**
```bash
# Auto-generate intent
kai intent render abc123...
# Output: Intent: Update Auth login

# Manually set intent
kai intent render abc123... --edit "Fix session expiration bug"
# Output: Intent: Fix session expiration bug
```

**Auto-generation rules:**
| Priority | Change Type | Verb |
|----------|-------------|------|
| 1 | `API_SURFACE_CHANGED` | "Update" |
| 2 | `CONDITION_CHANGED` | "Modify" |
| 3 | `CONSTANT_UPDATED` | "Update" |

---

### `kai dump`

Output changeset data as structured JSON.

```bash
kai dump <changeset-id> [flags]
```

**Arguments:**
- `<changeset-id>` - Hex ID of the changeset

**Flags:**
- `--json` - Output as JSON (currently the only format)

**Examples:**
```bash
# Output to terminal
kai dump abc123... --json

# Save to file
kai dump abc123... --json > changeset.json

# Pretty print with jq
kai dump abc123... --json | jq .
```

**Output structure:**
```json
{
  "changeset": {
    "id": "...",
    "kind": "ChangeSet",
    "payload": {
      "base": "<base-snapshot-id>",
      "head": "<head-snapshot-id>",
      "intent": "Update Auth login",
      "title": "",
      "description": ""
    }
  },
  "nodes": [
    { "id": "...", "kind": "File", "payload": {...} },
    { "id": "...", "kind": "Symbol", "payload": {...} },
    { "id": "...", "kind": "ChangeType", "payload": {...} },
    { "id": "...", "kind": "Module", "payload": {...} }
  ],
  "edges": [
    { "src": "...", "type": "MODIFIES", "dst": "..." },
    { "src": "...", "type": "HAS", "dst": "..." },
    { "src": "...", "type": "AFFECTS", "dst": "..." }
  ]
}
```

---

### `kai status`

Show Kai status and pending changes since last snapshot.

```bash
kai status [flags]
```

**Flags:**
- `--dir <path>` - Directory to check for changes (default: current directory)

**Example:**
```bash
kai status --dir ./src
```

**Output:**
```
Kai initialized

Snapshots:  3
Changesets: 1

Latest snapshot: a1b2c3d4e5f6
  Source: abc123... (directory)
  Date:   2024-12-02 14:30:45

Changes since last snapshot:

  Added (1):
    + auth/newfile.ts

  Modified (2):
    ~ auth/login.ts
    ~ billing/invoice.ts
```

---

### `kai log`

Show chronological log of snapshots and changesets.

```bash
kai log [flags]
```

**Flags:**
- `-n, --limit <count>` - Number of entries to show (default: 10)

**Example:**
```bash
kai log -n 5
```

---

### `kai ws create`

Create a new workspace (branch) based on a snapshot.

```bash
kai ws create --name <name> --base <snapshot-id> [flags]
```

**Flags:**
- `--name <name>` - Workspace name (required)
- `--base <snapshot-id>` - Base snapshot ID (required)
- `--desc <description>` - Optional description

**Example:**
```bash
kai ws create --name feature/auth --base abc123...
```

---

### `kai ws list`

List all workspaces.

```bash
kai ws list
```

**Output:**
```
NAME                  STATUS      BASE          HEAD          CHANGESETS
feature/auth          active      a1b2c3d4e5f6  d4e5f6a1b2c3  2
bugfix/login          shelved     a1b2c3d4e5f6  a1b2c3d4e5f6  0
```

---

### `kai ws stage`

Stage changes from a directory into a workspace.

```bash
kai ws stage --ws <name> [flags]
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--dir <path>` - Directory to stage from (default: current directory)

**Example:**
```bash
kai ws stage --ws feature/auth --dir ./src
```

**Output:**
```
Staged changes:
  Changeset: d4e5f6a1b2c3
  New head:  e5f6a1b2c3d4
  Files:     3 changed
  Changes:   2 change types detected
```

---

### `kai ws log`

Show the changelog for a workspace.

```bash
kai ws log --ws <name>
```

**Example:**
```bash
kai ws log --ws feature/auth
```

---

### `kai ws shelve`

Shelve a workspace (freeze staging).

```bash
kai ws shelve --ws <name>
```

---

### `kai ws unshelve`

Unshelve a workspace (resume staging).

```bash
kai ws unshelve --ws <name>
```

---

### `kai ws close`

Permanently close a workspace.

```bash
kai ws close --ws <name>
```

---

### `kai ws delete`

Delete a workspace permanently (metadata and refs).

```bash
kai ws delete --ws <name> [flags]
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--dry-run` - Show what would be deleted without actually deleting
- `--keep-refs` - Preserve workspace refs (rare)

**Examples:**
```bash
# Preview what would be deleted
kai ws delete --ws feature/experiment --dry-run

# Actually delete
kai ws delete --ws feature/experiment

# Delete but keep refs (rare)
kai ws delete --ws old-branch --keep-refs
```

**Note:** Content (snapshots, changesets, files) is NOT deleted - that's the GC's job. Run `kai prune` after deleting workspaces to reclaim storage.

---

### `kai ws checkout`

Checkout workspace head snapshot to filesystem.

```bash
kai ws checkout --ws <name> [flags]
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--dir <path>` - Target directory to write files to (default: current directory)
- `--clean` - Delete files not in snapshot

**Examples:**
```bash
# Checkout to current directory
kai ws checkout --ws feature/auth

# Checkout to specific directory
kai ws checkout --ws feature/auth --dir ./src

# Checkout with clean (removes extra files)
kai ws checkout --ws feature/auth --clean
```

---

### `kai integrate`

Integrate workspace changes into a target snapshot.

```bash
kai integrate --ws <name> --into <snapshot-id>
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--into <snapshot-id>` - Target snapshot ID (required)

**Example:**
```bash
kai integrate --ws feature/auth --into abc123...
```

**Output:**
```
Integration successful!
  Result snapshot: f6a1b2c3d4e5...
  Applied 2 changeset(s)
  Auto-resolved: 3 change(s)
```

If there are conflicts:
```
Integration conflicts (1):
  auth/login.ts: File modified in both workspace and target
```

---

### `kai diff`

Show semantic differences between two snapshots.

```bash
kai diff <base-ref> [head-ref] [flags]
```

**Arguments:**
- `<base-ref>` - Base snapshot (ref, selector, or short ID)
- `[head-ref]` - Head snapshot (optional; if omitted, compares against working directory)

**Flags:**
- `--semantic` - Show semantic diff (functions, classes, JSON keys, SQL tables)
- `--json` - Output diff as JSON (implies --semantic)
- `--name-only` - Output just paths with status prefixes (A/M/D)
- `--dir <path>` - Directory to compare against (default: current directory)

**Examples:**
```bash
# Compare two snapshots with semantic analysis
kai diff @snap:prev @snap:last --semantic

# Compare snapshot vs working directory
kai diff @snap:last --semantic

# Output as JSON for programmatic use
kai diff @snap:prev @snap:last --json

# Simple file-level diff
kai diff @snap:prev @snap:last --name-only
```

**Output example:**
```
Diff: a1b2c3d4e5f6 → working directory

~ auth/login.ts
  ~ function login(user) -> function login(user, token)
  + function validateMFA(code)

+ config.json
  + timeout
  + retries

~ schema.sql
  ~ users.email: VARCHAR(100) -> VARCHAR(255)
  + users.created_at: TIMESTAMP DEFAULT CURRENT_TIMESTAMP

Summary: 3 files (1 added, 2 modified, 0 removed)
         6 units (4 added, 2 modified, 0 removed)
```

**Diff granularity:**

| Type | Support | Description |
|------|---------|-------------|
| Function | ✓ | Detects added/removed/modified functions with signature changes |
| Class | ✓ | Detects class additions/removals |
| Method | ✓ | Detects method changes within classes |
| SQL Table | ✓ | Detects table additions/removals |
| SQL Column | ✓ | Detects column additions/modifications/removals |
| JSON Key | ✓ | Detects key additions/modifications/removals |
| YAML Key | ✓ | Detects key additions/modifications/removals |

---

### `kai merge`

Perform AST-aware 3-way merge at symbol granularity.

```bash
kai merge <base-file> <left-file> <right-file> [flags]
```

**Arguments:**
- `<base-file>` - Common ancestor file
- `<left-file>` - Left/ours version
- `<right-file>` - Right/theirs version

**Flags:**
- `--lang <lang>` - Language (js, ts, py) - auto-detected from extension if not specified
- `-o, --output <path>` - Output file path (defaults to stdout)
- `--json` - Output result as JSON (includes conflicts)

**Examples:**
```bash
# Merge JavaScript files - output to stdout
kai merge base.js left.js right.js

# Merge Python files with explicit language
kai merge base.py branch1.py branch2.py --lang py -o merged.py

# Get JSON output with conflict details
kai merge base.js left.js right.js --json
```

**What it does:**
- Parses files using Tree-sitter to extract semantic units (functions, classes, constants)
- Performs 3-way merge at symbol granularity, not line-by-line
- Auto-merges changes to different functions in the same file
- Detects semantic conflicts:

| Conflict Kind | Description |
|---------------|-------------|
| `API_SIGNATURE_DIVERGED` | Both sides changed function parameters differently |
| `CONST_VALUE_CONFLICT` | Both sides changed constant value differently |
| `DELETE_vs_MODIFY` | One side deleted, other modified |
| `CONCURRENT_CREATE` | Both sides created same-named unit |
| `BODY_DIVERGED` | Same function body modified on both sides |

**Example JSON output:**
```json
{
  "success": false,
  "conflicts": [
    {
      "kind": "BODY_DIVERGED",
      "unit": "file::foo",
      "message": "Function foo body modified on both sides"
    }
  ]
}
```

---

### `kai ref list`

List all named references.

```bash
kai ref list [flags]
```

**Flags:**
- `--kind <kind>` - Filter by kind (Snapshot, ChangeSet, Workspace)

**Example:**
```bash
kai ref list
kai ref list --kind Snapshot
```

**Output:**
```
NAME                            KIND          TARGET
snap.latest                     Snapshot      d9ec990243e5efea...
snap.main                       Snapshot      d9ec990243e5efea...
cs.latest                       ChangeSet     90cd726437a465b9...
ws.feature/auth.head            Snapshot      4a2556c086b1f664...
```

---

### `kai ref set`

Create or update a named reference.

```bash
kai ref set <name> <target>
```

**Arguments:**
- `<name>` - Reference name (e.g., `snap.main`, `cs.bugfix`)
- `<target>` - Target ID (full hex, short prefix, ref name, or selector)

**Examples:**
```bash
# Set using short ID
kai ref set snap.main d9ec9902

# Set using another ref
kai ref set snap.backup snap.main

# Set using selector
kai ref set snap.before_feature @snap:prev
kai ref set cs.last_change @cs:last
```

---

### `kai ref del`

Delete a named reference.

```bash
kai ref del <name>
```

**Arguments:**
- `<name>` - Reference name to delete

**Example:**
```bash
kai ref del snap.old_backup
```

---

### `kai pick`

Search and select nodes interactively.

```bash
kai pick <kind> [flags]
```

**Arguments:**
- `<kind>` - Node kind: `Snapshot`, `ChangeSet`, or `Workspace` (aliases: `snap`, `cs`, `ws`)

**Flags:**
- `--filter <substring>` - Filter by substring in ID or payload
- `--no-ui` - Output matches without interactive selection

**Examples:**
```bash
# List all snapshots
kai pick Snapshot

# Search snapshots containing "auth"
kai pick snap --filter auth

# List changesets non-interactively
kai pick cs --no-ui
```

---

### `kai completion`

Generate shell completion scripts.

```bash
kai completion [bash|zsh|fish|powershell]
```

**Arguments:**
- `bash` - Generate Bash completion script
- `zsh` - Generate Zsh completion script
- `fish` - Generate Fish completion script
- `powershell` - Generate PowerShell completion script

**Examples:**
```bash
# Bash - add to ~/.bashrc
source <(kai completion bash)

# Zsh - add to ~/.zshrc
source <(kai completion zsh)

# Fish - save to completions directory
kai completion fish > ~/.config/fish/completions/kai.fish

# PowerShell
kai completion powershell | Out-String | Invoke-Expression
```

---

### `kai prune`

Garbage collect unreferenced content (snapshots, changesets, files).

```bash
kai prune [flags]
```

**Flags:**
- `--dry-run` - Show what would be deleted without actually deleting
- `--since <days>` - Only delete content older than N days (0 = no limit)
- `--aggressive` - Also sweep orphaned Symbols and Modules

**Examples:**
```bash
# Preview what would be deleted
kai prune --dry-run

# Actually delete unreferenced content
kai prune

# Only delete content older than 30 days
kai prune --since 30

# Aggressive cleanup (includes symbols and modules)
kai prune --aggressive
```

**What happens:**
1. Collects all roots (refs targets, workspace nodes)
2. Marks all reachable nodes from roots (BFS traversal)
3. Deletes any nodes not marked as reachable
4. Deletes orphaned object files from `.kai/objects/`

**Note:** Run this after `kai ws delete` to reclaim storage.

---

## CI & Test Selection

Kai provides intelligent test selection for CI pipelines. Instead of running all tests on every change, Kai analyzes which tests are affected by your changes and generates a targeted test plan.

### Safe Skipping Philosophy

The key concern with selective testing is: "If selective CI ever skips a test that should've run, users will disable Kai the next day."

Kai addresses this with **progressive hardening** through three safety modes:

1. **Shadow Mode** - Learn and validate before trusting
2. **Guarded Mode** - Safe by construction with automatic fallback
3. **Strict Mode** - Full selective after building confidence

This allows teams to start safely, build confidence, and gradually increase selectivity.

---

### `kai ci plan`

Generate a CI test selection plan from a changeset.

```bash
kai ci plan <changeset|selector> [flags]
```

**Arguments:**
- `<changeset|selector>` - ChangeSet ID, workspace selector, or snapshot selector

**Flags:**
- `--strategy <strategy>` - Selection strategy: `auto`, `symbols`, `imports`, `coverage` (default: `auto`)
- `--risk-policy <policy>` - Risk policy: `expand`, `warn`, `fail` (default: `expand`)
- `--safety-mode <mode>` - Safety mode: `shadow`, `guarded`, `strict` (default: `guarded`)
- `--explain` - Output human-readable explanation table instead of JSON
- `--out <file>` - Output file for plan JSON

**Safety Modes:**

| Mode | Description | Use Case |
|------|-------------|----------|
| `shadow` | Compute selective plan but CI runs full suite. Logs predictions for comparison. | Learning phase, validating test selection |
| `guarded` | Run selective plan with automatic fallback. Auto-expands on structural risks. | Default production mode, safe by construction |
| `strict` | Run selective plan only. No auto-expansion. Use panic switch for full suite. | Mature setups with high confidence |

**Examples:**
```bash
# Generate plan with default guarded mode
kai ci plan @cs:last --strategy=auto --out plan.json

# Shadow mode: learn and compare
kai ci plan @cs:last --safety-mode=shadow --out plan.json

# Strict mode: selective only
kai ci plan @cs:last --safety-mode=strict --out plan.json

# Human-readable explanation table
kai ci plan @cs:last --explain

# Force full suite via panic switch (any mode)
KAI_FORCE_FULL=1 kai ci plan @cs:last --out plan.json
```

**Plan Output:**

The plan JSON includes:
- `mode` - Plan mode: `selective`, `expanded`, `shadow`, `full`, `skip`
- `safetyMode` - The safety mode used
- `confidence` - Top-level confidence score (0.0-1.0)
- `targets.run` - Test files to run
- `targets.full` - Full test suite (shadow mode only)
- `targets.fallback` - Whether fallback is enabled (guarded mode)
- `safety.confidence` - Confidence score (0.0-1.0)
- `safety.structuralRisks` - Detected risk patterns
- `safety.autoExpanded` - Whether plan was auto-expanded
- `uncertainty.score` - Uncertainty score (0-100)
- `uncertainty.sources` - What contributed to uncertainty
- `expansionLog` - Array of reasons why plan was expanded
- `provenance.changeset` - Changeset ID used
- `provenance.base` / `provenance.head` - Snapshot IDs
- `provenance.kaiVersion` - Kai CLI version
- `provenance.detectorVersion` - Dynamic import detector version (for cache invalidation)
- `provenance.generatedAt` - ISO8601 timestamp
- `provenance.analyzers` - Which analyzers ran (e.g., `symbols@1`, `imports@1`)
- `provenance.policyHash` - Hash of ci-policy.yaml if used
- `prediction` - Shadow mode prediction data

**Structural Risks:**

Kai detects patterns that indicate higher risk of missed tests:

| Risk Type | Severity | Description |
|-----------|----------|-------------|
| `config_change` | High | Config file changed (package.json, tsconfig, jest.config, etc.) |
| `test_infra` | High | Test infrastructure changed (fixtures, mocks, setup files) |
| `dynamic_import` | High | Dynamic require/import detected - static analysis unreliable |
| `no_test_mapping` | Medium | Changed files have no test coverage |
| `many_files_changed` | Medium | More than 20 files changed |
| `cross_module_change` | Medium | Changes span 3+ modules |

**Panic Switch:**

Set `KAI_FORCE_FULL=1` or `KAI_PANIC=1` to force full suite in any mode:
```bash
KAI_FORCE_FULL=1 kai ci plan @cs:last --safety-mode=strict --out plan.json
```

**CI Policy Configuration:**

Create a `.kai/rules/ci-policy.yaml` file in your repo to customize risk thresholds and behavior (legacy path `kai.ci-policy.yaml` is also supported):

```yaml
version: 1

thresholds:
  minConfidence: 0.40    # Expand if confidence below 40%
  maxUncertainty: 70     # Expand if uncertainty above 70
  maxFilesChanged: 50    # Expand if more than 50 files changed
  maxTestsSkipped: 0.90  # Expand if skipping >90% of tests

paranoia:
  alwaysFullPatterns:
    - "*.lock"
    - "go.mod"
    - "package.json"
    - ".github/workflows/*"
  expandOnPatterns:
    - "**/config/**"
    - "**/setup.*"
    - "**/__mocks__/**"
  riskMultipliers:
    "src/core/**": 1.5
    "lib/**": 1.3

behavior:
  onHighRisk: expand      # expand, warn, fail
  onLowConfidence: expand
  onNoTests: warn         # expand, warn, pass
  failOnExpansion: false  # Exit non-zero if expansion occurred

dynamicImports:
  expansion: nearest_module  # nearest_module, package, owners, full_suite
  ownersFallback: true       # Use union model: nearest_module → owners → full_suite
  maxFilesThreshold: 200     # If >N files in expansion, widen further
  boundedRiskThreshold: 100  # Bounded imports matching >N files are treated as risky
  allowlist:                 # Paths to ignore dynamic imports (glob patterns)
    - "src/vendor/**"
    - "**/*.generated.js"
  boundGlobs:                # Known-bounded dynamic imports by pattern
    "src/widgets/**": ["src/widgets/**/*.test.js"]
```

**Dynamic Import Expansion Strategies:**

| Strategy | Description |
|----------|-------------|
| `nearest_module` | Expand to tests in the same module as the dynamic import |
| `package` | Expand to tests in the same directory/package |
| `owners` | Expand to tests owned by the same team (via CODEOWNERS) |
| `full_suite` | Expand to all tests (most conservative) |

**Union Model (ownersFallback: true):**

When `ownersFallback` is enabled, Kai uses a cascading fallback strategy:

1. **nearest_module** → Try to find tests in the same module
2. **package** → Fall back to tests in the same directory
3. **owners** → Fall back to parent directory (team ownership proxy)
4. **full_suite** → Nuclear fallback if nothing else matches

This prevents edge cases where `modules.yaml` is incomplete from causing selection misses.

**Bounded-but-Risky Detection:**

Some dynamic imports are "bounded" by webpack/vite comments but have huge footprints:

```javascript
/* webpackInclude: /plugins/ */  // Might match 500+ files!
const plugin = await import(`./plugins/${name}`);
```

Set `boundedRiskThreshold` to treat bounded imports matching more than N files as risky (triggering expansion).

The policy hash is included in the plan's provenance for audit trail.

**Dynamic Import Detection:**

The plan includes detailed dynamic import analysis:

```json
{
  "dynamicImport": {
    "detected": true,
    "files": [
      {
        "path": "src/loader.js",
        "kind": "import(variable)",
        "line": 42,
        "bounded": false,
        "allowlisted": false,
        "confidence": 0.9
      }
    ],
    "policy": {
      "expansion": "nearest_module",
      "expandedTo": ["src/loader.test.js"],
      "ownersFallback": true
    },
    "telemetry": {
      "totalDetected": 3,
      "bounded": 1,
      "unbounded": 2,
      "allowlisted": 0,
      "widenedTests": 5,
      "cacheHits": 2,
      "cacheMisses": 1
    }
  }
}
```

**Bounding Dynamic Imports:**

Use webpack/vite magic comments to bound dynamic imports and prevent unnecessary test expansion:

```javascript
// Bounded - kai knows the scope
const widget = await import(
  /* webpackInclude: /\.widget\.js$/ */
  `./widgets/${name}.widget.js`
);

// Unbounded - triggers expansion
const mod = await import(modulePath);
```

**False Positive Reduction:**

Kai automatically filters out false positives:
- Constant-foldable cases: `require("foo/" + "bar")`
- Literal `require.resolve()`: `require.resolve("lodash")`
- Template literals with known paths: `` require(`./locales/${lang}.json`) `` with `webpackInclude`

---

### `kai ci print`

Print a human-readable summary of a CI plan.

```bash
kai ci print --plan <file> [flags]
```

**Flags:**
- `--plan <file>` - Plan JSON file to print (required)
- `--section <section>` - Section to print: `summary`, `targets`, `impact`, `causes`, `safety`
- `--json` - Output as JSON

**Sections:**

| Section | Description |
|---------|-------------|
| `summary` | Overview of plan (default) |
| `targets` | What to run/skip |
| `impact` | What changed |
| `causes` | Why each test was selected (root cause analysis) |
| `safety` | Safety analysis details |

**Examples:**
```bash
# Print summary
kai ci print --plan plan.json

# Print only targets
kai ci print --plan plan.json --section targets

# Print safety analysis
kai ci print --plan plan.json --section safety

# Print root cause analysis (why each test was selected)
kai ci print --plan plan.json --section causes
```

---

### `kai ci detect-runtime-risk`

Analyze test output for runtime signals that indicate a possible selection miss.

```bash
kai ci detect-runtime-risk --logs <file> [--plan <file>] [flags]
```

**Flags:**
- `--logs <file>` - Path to test output JSON (Jest, Mocha, pytest, Go)
- `--stderr <file>` - Path to stderr/text log file
- `--plan <file>` - Path to plan file (for cross-reference)
- `--format <fmt>` - Log format: auto, jest, mocha, pytest, go, text

**Detected Signals:**

| Signal | Severity | Description |
|--------|----------|-------------|
| `module_not_found` | Critical | Cannot find module/package errors (Node.js, Python, Go) |
| `import_error` | Critical | Import/require failures, missing exports |
| `type_error` | High | TypeScript/type checking errors |
| `setup_crash` | Critical | Test setup hook failures, fixture errors |
| `unexpected_failure` | Low | Other runtime errors |

**Languages Supported:**
- **Node.js/JavaScript**: `Cannot find module`, webpack errors, Jest failures
- **TypeScript**: TS2307, TS2305, TS2339, type error bursts
- **Python**: `ModuleNotFoundError`, `ImportError`, importlib errors, pytest fixtures
- **Go**: Package not found, plugin load failures, build failures

**Exit Codes:**
- `0` - No risks detected, selection was safe
- `1` - Error running the command
- `75` - TRIPWIRE: Rerun full suite recommended (with `--tripwire`)

**Examples:**
```bash
# Analyze Jest test output
kai ci detect-runtime-risk --logs jest-results.json --plan plan.json

# Analyze stderr from test run
kai ci detect-runtime-risk --stderr test.log

# Tripwire mode for CI (exit 75 if rerun needed)
kai ci detect-runtime-risk --stderr test.log --tripwire

# Treat any failure as tripwire trigger
kai ci detect-runtime-risk --stderr test.log --tripwire --rerun-on-fail
```

**Tripwire Mode (`--tripwire`):**

In tripwire mode, outputs only `RERUN` or `OK` and exits with code 75 or 0. This makes it easy to integrate into CI pipelines:

```bash
# Run selective tests, then check for runtime risks
npm run test:selective 2>&1 | tee test.log
kai ci detect-runtime-risk --stderr test.log --tripwire || npm run test:full
```

**GitHub Actions Integration:**

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Generate test plan
        run: kai ci plan @cs:last --out plan.json

      - name: Run selective tests
        id: selective
        continue-on-error: true
        run: |
          npm run test -- $(jq -r '.targets.run[]' plan.json) 2>&1 | tee test.log

      - name: Check for runtime tripwire
        id: tripwire
        run: kai ci detect-runtime-risk --stderr test.log --tripwire
        continue-on-error: true

      - name: Rerun full suite if tripwire triggered
        if: steps.tripwire.outcome == 'failure'
        run: |
          echo "Tripwire triggered - running full suite"
          npm run test:full
```

**GitLab CI Integration:**

```yaml
test:
  script:
    - kai ci plan @cs:last --out plan.json
    - npm run test:selective 2>&1 | tee test.log || true
    - |
      if ! kai ci detect-runtime-risk --stderr test.log --tripwire; then
        echo "Tripwire triggered - running full suite"
        npm run test:full
      fi
```

---

### `kai ci explain-dynamic-imports`

Analyze files for dynamic imports and explain how they would affect test selection.

```bash
kai ci explain-dynamic-imports [path] [--json]
```

**Arguments:**
- `[path]` - File or directory to scan (defaults to current directory)

**Flags:**
- `--json` - Output as JSON instead of human-readable format

**Examples:**
```bash
# Scan current directory
kai ci explain-dynamic-imports

# Scan specific directory
kai ci explain-dynamic-imports src/

# Scan single file
kai ci explain-dynamic-imports src/plugins/loader.js

# Output as JSON
kai ci explain-dynamic-imports src/ --json
```

**Output:**
```
Dynamic Import Analysis
============================================================
Files scanned: 42
Dynamic imports found: 5
Expansion strategy: nearest_module
Owners fallback: true
Bounded risk threshold: 100 files

⚠️  UNBOUNDED (2) - Will trigger expansion:
   src/plugins/index.ts:42
      Type: import(variable) (confidence: 90%)
      Action: Expand to nearest_module

✓  BOUNDED (2) - Safe, will not expand:
   src/i18n/load.js:5 → webpackInclude: /locales\/.*\.json$/

○  ALLOWLISTED (1) - Ignored by policy:
   src/vendor/legacy.js:12

Recommendations:
  • Add webpackInclude/webpackExclude comments to bound dynamic imports
  • Add paths to dynamicImports.allowlist in kai.ci-policy.yaml
  • Use explicit imports where possible
```

Use this command before committing to understand how dynamic imports affect CI test selection.

---

### `kai ci record-miss`

Record a test selection miss for shadow mode learning and analysis.

```bash
kai ci record-miss --plan <file> [--evidence <file> | --failed <tests>]
```

**Flags:**
- `--plan <file>` - Path to plan file (required)
- `--evidence <file>` - Path to test results JSON (Jest, pytest, Go test -json)
- `--failed <tests>` - Comma-separated list of failed test files

**Examples:**
```bash
# Record miss from test results JSON
kai ci record-miss --plan plan.json --evidence jest-results.json

# Record miss with explicit failed tests
kai ci record-miss --plan plan.json --failed "tests/auth.test.js,tests/api.test.js"
```

Miss records are appended to `.kai/ci-misses.jsonl` for aggregation and analysis.

---

### `kai ci ingest-coverage`

Ingest coverage reports to build file→test mappings for coverage-based test selection.

```bash
kai ci ingest-coverage --from <file> [flags]
```

**Flags:**
- `--from <file>` - Path to coverage report (required)
- `--format <fmt>` - Format: `auto`, `nyc`, `coveragepy`, `jacoco` (default: `auto`)
- `--branch <name>` - Branch name to associate with coverage data
- `--tag <name>` - Tag to associate with coverage data (e.g., commit hash)

**Supported Formats:**

| Format | Tool | Report File |
|--------|------|-------------|
| `nyc` | NYC/Istanbul | `coverage-final.json` |
| `coveragepy` | coverage.py | `coverage.json` (with `--format json`) |
| `jacoco` | JaCoCo | `jacoco.xml` |

**Examples:**
```bash
# Ingest NYC coverage (auto-detected)
kai ci ingest-coverage --from coverage/coverage-final.json

# Ingest Python coverage.py report
kai ci ingest-coverage --from coverage.json --format coveragepy

# Ingest JaCoCo XML report
kai ci ingest-coverage --from target/site/jacoco/jacoco.xml --format jacoco

# Associate with branch and commit
kai ci ingest-coverage --from coverage-final.json --branch main --tag abc123
```

**How Coverage Selection Works:**

1. **Ingest Phase**: After tests run, ingest coverage to build `file→test` mappings
2. **Planning Phase**: When `--strategy=coverage` or `--strategy=auto`, Kai looks up which tests covered the changed files
3. **Selection**: Tests that recently covered any changed file are included in the plan

Coverage data is stored in `.kai/coverage-map.json` and accumulates over time.

**Policy Configuration:**

```yaml
coverage:
  enabled: true        # Use coverage data in test selection
  lookbackDays: 30     # How far back to consider coverage data
  minHits: 1           # Minimum hit count to trust a mapping
  onNoCoverage: warn   # expand, warn, ignore - action for files without coverage
```

---

### `kai ci ingest-contracts`

Register contract schemas (OpenAPI, Protobuf, GraphQL) and their associated tests.

```bash
kai ci ingest-contracts --type <type> --path <path> --tests <tests> [flags]
```

**Flags:**
- `--type <type>` - Contract type: `openapi`, `protobuf`, `graphql` (required)
- `--path <path>` - Path to schema file (required)
- `--tests <tests>` - Comma-separated test files to run when schema changes (required)
- `--service <name>` - Service/module name this schema belongs to
- `--generated <paths>` - Comma-separated paths to generated files from this schema

**Examples:**
```bash
# Register OpenAPI schema
kai ci ingest-contracts --type openapi --path api/openapi.yaml \
  --tests "tests/api/users.test.js,tests/api/auth.test.js" \
  --service users

# Register Protobuf schema with generated files
kai ci ingest-contracts --type protobuf --path proto/user.proto \
  --tests "tests/grpc/user.test.go" \
  --service users \
  --generated "gen/user.pb.go,gen/user_grpc.pb.go"

# Register GraphQL schema
kai ci ingest-contracts --type graphql --path schema.graphql \
  --tests "tests/graphql/resolvers.test.ts" \
  --service api
```

**How Contract Detection Works:**

1. **Registration**: Register each contract schema with its associated tests
2. **Fingerprinting**: Kai computes a hash (digest) of each schema file
3. **Change Detection**: When planning, if a schema's digest changed, its registered tests are added to the plan
4. **Generated Files**: If generated files from a schema change, the schema's tests are also added

Contract registrations are stored in `.kai/contracts.json`.

**Policy Configuration:**

```yaml
contracts:
  enabled: true                    # Enable contract change detection
  onChange: add_tests              # add_tests, expand, warn
  types:                           # Which contract types to detect
    - openapi
    - protobuf
    - graphql
```

---

### `kai ci annotate-plan`

Annotate a plan with fallback/tripwire information for auditability.

```bash
kai ci annotate-plan <plan-file> [flags]
```

**Flags:**
- `--fallback-used` - Mark that fallback was triggered
- `--fallback-reason <reason>` - Reason for fallback: `runtime_tripwire`, `planner_over_threshold`, `panic_switch`
- `--fallback-trigger <text>` - The specific error/condition that triggered fallback
- `--fallback-exit-code <code>` - Exit code from tripwire (e.g., 75)

**Examples:**
```bash
# Annotate plan after tripwire triggered
kai ci annotate-plan plan.json \
  --fallback-used \
  --fallback-reason runtime_tripwire \
  --fallback-trigger "ModuleNotFoundError: No module named 'missing'" \
  --fallback-exit-code 75

# Annotate after planner expanded due to high uncertainty
kai ci annotate-plan plan.json \
  --fallback-used \
  --fallback-reason planner_over_threshold
```

**Plan Fallback Field:**

After annotation, the plan includes a `fallback` field:

```json
{
  "fallback": {
    "used": true,
    "reason": "runtime_tripwire",
    "trigger": "ModuleNotFoundError: No module named 'missing'",
    "exitCode": 75
  }
}
```

**Use in CI:**

```yaml
- name: Run selective tests
  id: selective
  continue-on-error: true
  run: npm run test:selective 2>&1 | tee test.log

- name: Check tripwire
  id: tripwire
  run: kai ci detect-runtime-risk --stderr test.log --tripwire
  continue-on-error: true

- name: Rerun and annotate if tripwire triggered
  if: steps.tripwire.outcome == 'failure'
  run: |
    npm run test:full
    kai ci annotate-plan plan.json \
      --fallback-used \
      --fallback-reason runtime_tripwire \
      --fallback-exit-code 75

- name: Upload plan for audit
  uses: actions/upload-artifact@v4
  with:
    name: test-plan
    path: plan.json
```

This creates an audit trail showing when and why fallback occurred.

---

### `kai ci validate-plan`

Validate that a plan.json file has all required fields with correct types.

```bash
kai ci validate-plan <plan-file> [--strict]
```

**Flags:**
- `--strict` - Also validate optional fields like policyHash and analyzers

**Validated Fields:**
- Required: `mode`, `risk`, `safetyMode`, `provenance.kaiVersion`, `provenance.detectorVersion`, `provenance.generatedAt`
- Strict mode: `provenance.policyHash`, `provenance.analyzers`
- Value validation: `mode` ∈ {selective, expanded, full, shadow, skip}, `risk` ∈ {low, medium, high}

**Exit Codes:**
- `0` - Plan is valid
- `1` - Plan is invalid or error reading file

**Examples:**
```bash
# Basic validation
kai ci validate-plan plan.json

# Strict validation (includes optional fields)
kai ci validate-plan plan.json --strict
```

---

### Nightly Shadow Validation Job

Use shadow mode with a nightly job to validate test selection accuracy before trusting it in production:

```yaml
# .github/workflows/nightly-validation.yml
name: Nightly Test Selection Validation

on:
  schedule:
    - cron: '0 3 * * *'  # 3 AM UTC daily

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # Full history for accurate comparison

      - name: Generate shadow plan
        run: |
          kai ci plan @snap:last --safety-mode=shadow --out plan.json
          kai ci validate-plan plan.json

      - name: Run full test suite
        id: full
        run: npm run test:full 2>&1 | tee test.log

      - name: Compare prediction to reality
        run: |
          # Extract which tests failed
          FAILED=$(jq -r '.failures[].file' jest-results.json 2>/dev/null || echo "")
          PREDICTED=$(jq -r '.targets.run[]' plan.json)

          # Check if any failures were in skipped tests
          for fail in $FAILED; do
            if ! echo "$PREDICTED" | grep -q "$fail"; then
              echo "MISS: $fail was not in predicted targets"
              kai ci record-miss --plan plan.json --failed "$fail"
            fi
          done

      - name: Annotate and upload plan
        run: |
          kai ci annotate-plan plan.json \
            --fallback.used=false \
            --fallback.reason=nightly_validation

      - name: Upload validation artifacts
        uses: actions/upload-artifact@v4
        with:
          name: nightly-validation-${{ github.run_number }}
          path: |
            plan.json
            test.log
            jest-results.json
```

This creates a feedback loop that:
1. Generates a shadow plan (what *would* run selectively)
2. Runs the full suite (ground truth)
3. Compares predictions to actual failures
4. Records any misses for analysis
5. Archives the plan for dashboarding

Over time, this builds confidence in test selection before enabling guarded or strict mode.

---

### `kai review`

Code review commands centered on changesets.

#### Code Diffs in Reviews (Semantic First, Text When Needed)

Kai reviews center on ChangeSets. Each ChangeSet renders reviewable code in three layers:

**1. Intent & Impact**
- Human sentence (intent)
- Affected modules/symbols
- Classified change types (e.g., `API_SURFACE_CHANGED`, `CONDITION_CHANGED`)

**2. Semantic Diff (default)**
- Symbol hunks: before/after of functions, methods, classes, constants
- Handles moves/renames/formatting automatically (no noise)
- Highlights signature changes, condition/constant updates, and JSON/YAML/SQL units
- Comments anchor to symbols (preferred) or file+range

**3. Raw Text Diff (fallback / verify)**
- Unified or side-by-side for any file (including unsupported language/binary-as-text)
- Useful for trust-but-verify and copy/paste

**CLI Examples:**
```bash
# Open a review for the latest ChangeSet
kai review open @cs:last --title "Reduce session timeout to 30 min"

# Show a ChangeSet with semantic hunks (default)
kai review view @cs:last

# Force raw text view (trust-but-verify)
kai review view @cs:last --view=text

# Mixed view: semantic first, then raw hunks for each touched file
kai review view @cs:last --view=mixed

# Plain semantic diff (outside the review flow)
kai diff @snap:prev @snap:last --semantic

# JSON for tooling / UI adapters
kai diff @snap:prev @snap:last --json
```

**Example (Semantic Hunk):**
```
auth/login.ts
  function login(user: User, device: string)
-   returns boolean
+   returns boolean  // unchanged
-  login(user, device)
+  login(user, device, ip: string)   // API_SURFACE_CHANGED: +ip

auth/constants.ts
- export const TIMEOUT = 3600
+ export const TIMEOUT = 1800         // CONSTANT_UPDATED
```

**JSON Shape (Hunks):**
```json
{
  "units": [
    {
      "kind": "function",
      "fqName": "auth.login",
      "file": "auth/login.ts",
      "change": "API_SURFACE_CHANGED",
      "before": {"signature": "login(user: User, device: string)"},
      "after":  {"signature": "login(user: User, device: string, ip: string)"},
      "ranges": {"before":[[4,0],[12,1]], "after":[[4,0],[14,1]]}
    }
  ],
  "files": [
    {
      "path": "auth/constants.ts",
      "change": "CONSTANT_UPDATED",
      "before": "export const TIMEOUT = 3600",
      "after":  "export const TIMEOUT = 1800"
    }
  ]
}
```

**Comment Anchoring:**
- Primary: `ReviewComment` → Symbol (fqName, signature, ranges)
- Fallback: `ReviewComment` → File + range
- Comments auto-carry forward when a ChangeSet is `SUPERSEDED`

**Unsupported/Non-Code Files:**
- Show file-level status + raw diff if textual
- If binary, show "changed" with size/hash deltas; attach preview if available

---

#### `kai review open`

Open a new review for a changeset or workspace.

```bash
kai review open <target-id> --title <title> [flags]
```

**Arguments:**
- `<target-id>` - ChangeSet or Workspace ID to review

**Flags:**
- `--title <title>` - Review title (required)
- `--desc <text>` - Review description
- `--reviewers <names>` - Reviewers (can be specified multiple times)

**Example:**
```bash
kai review open cs.latest --title "Fix authentication timeout" --reviewers alice --reviewers bob
```

---

#### `kai review list`

List all reviews.

```bash
kai review list
```

**Output:**
```
ID            STATE     TITLE                        AUTHOR    TARGET
a1b2c3d4...   open      Fix authentication timeout   alice     ChangeSet
d4e5f6a7...   approved  Add billing module           bob       Workspace
```

---

#### `kai review view`

View details of a review with semantic diff.

```bash
kai review view <review-id> [flags]
```

**Flags:**
- `--view <mode>` - View mode: `semantic` (default), `text`, or `mixed`
- `--json` - Output as JSON (includes semantic hunks)

**Examples:**
```bash
# Default semantic view
kai review view a1b2c3d4

# Raw text diff for verification
kai review view a1b2c3d4 --view=text

# Both semantic and raw hunks
kai review view a1b2c3d4 --view=mixed
```

---

#### `kai review status`

Change the status of a review.

```bash
kai review status <review-id> <new-state>
```

**States:** `draft`, `open`, `approved`, `changes_requested`, `merged`, `abandoned`

---

#### `kai review approve`

Approve a review.

```bash
kai review approve <review-id>
```

---

#### `kai review request-changes`

Request changes on a review.

```bash
kai review request-changes <review-id>
```

---

#### `kai review close`

Close a review with a final state.

```bash
kai review close <review-id> --state <merged|abandoned>
```

---

#### `kai review ready`

Mark a draft review as ready for review.

```bash
kai review ready <review-id>
```

---

#### `kai review export`

Export a review as markdown or HTML.

```bash
kai review export <review-id> [flags]
```

**Flags:**
- `--markdown` - Export as markdown
- `--html` - Export as HTML

---

## Configuration

### Module Definitions

Edit `.kai/rules/modules.yaml` to define your project's modules:

```yaml
modules:
  # Simple module with single pattern
  - name: Auth
    include: ["auth/**"]

  # Module with multiple patterns
  - name: API
    include:
      - "api/**"
      - "routes/**"
      - "controllers/**"

  # Module matching specific file types
  - name: Tests
    include:
      - "**/*.test.ts"
      - "**/*.spec.ts"
      - "__tests__/**"

  # Module with nested paths
  - name: Components
    include:
      - "src/components/**"
      - "src/ui/**"

  # Feature-based modules
  - name: Billing
    include: ["**/billing/**", "**/payments/**"]

  - name: Profile
    include: ["**/profile/**", "**/user/**"]
```

**Pattern syntax (doublestar):**
- `*` - Matches any characters except `/`
- `**` - Matches any characters including `/`
- `?` - Matches any single character
- `[abc]` - Matches any character in the set
- `{a,b}` - Matches either `a` or `b`

**Examples:**
| Pattern | Matches | Doesn't Match |
|---------|---------|---------------|
| `auth/**` | `auth/login.ts`, `auth/utils/hash.ts` | `authentication/login.ts` |
| `*.ts` | `index.ts` | `src/index.ts` |
| `**/*.test.ts` | `foo.test.ts`, `src/utils/foo.test.ts` | `foo.test.js` |
| `src/{api,lib}/**` | `src/api/index.ts`, `src/lib/utils.ts` | `src/app/index.ts` |

---

### Change Type Rules

The `.kai/rules/changetypes.yaml` file defines how changes are detected:

```yaml
rules:
  - id: CONDITION_CHANGED
    match:
      node_types: ["binary_expression", "logical_expression", "relational_expression"]
      detector: "operator_or_boundary_changed"

  - id: CONSTANT_UPDATED
    match:
      node_types: ["number", "string"]
      detector: "literal_value_changed"

  - id: API_SURFACE_CHANGED
    match:
      node_types: ["function_declaration", "method_definition", "export_statement"]
      detector: "params_or_exports_changed"
```

**Note:** In the MVP, these rules are informational. The detectors are implemented in Go code. Future versions may support custom detector definitions.

---

## Understanding the Output

### Node Types

| Kind | Description | Key Payload Fields |
|------|-------------|-------------------|
| `Snapshot` | Point-in-time capture of codebase | `gitRef`, `fileCount`, `createdAt` |
| `File` | Source code file | `path`, `lang`, `digest` |
| `Symbol` | Code symbol (function/class/variable) | `fqName`, `kind`, `range`, `signature` |
| `Module` | Logical grouping of files | `name`, `patterns` |
| `ChangeSet` | Diff between two snapshots | `base`, `head`, `intent` |
| `ChangeType` | Classified change with evidence | `category`, `evidence` |

### Edge Types

| Type | From | To | Meaning |
|------|------|-----|---------|
| `HAS_FILE` | Snapshot | File | Snapshot contains this file |
| `CONTAINS` | Module | File | Module includes this file |
| `DEFINES_IN` | Symbol | File | Symbol is defined in this file |
| `MODIFIES` | ChangeSet | File/Symbol | ChangeSet modifies this file/symbol |
| `HAS` | ChangeSet | ChangeType | ChangeSet includes this change type |
| `AFFECTS` | ChangeSet | Module | ChangeSet affects this module |

### Change Type Evidence

Each ChangeType node includes evidence of where the change occurred:

```json
{
  "category": "CONDITION_CHANGED",
  "evidence": {
    "fileRanges": [
      {
        "path": "auth/session.ts",
        "start": [7, 6],
        "end": [7, 16]
      }
    ],
    "symbols": ["abc123..."]
  }
}
```

- `fileRanges` - Source locations where the change was detected
- `symbols` - IDs of symbols containing/affected by the change

---

## Architecture Deep Dive

### Content-Addressed Storage

Kai uses content-addressed storage for both nodes and file content:

**Node IDs:**
```
NodeID = BLAKE3(kind + "\n" + canonicalJSON(payload))
```

This ensures:
- Same content always produces same ID (deterministic)
- Re-running commands doesn't create duplicates (idempotent)
- Easy to verify data integrity

**File Objects:**
```
.kai/objects/<blake3-hash-of-content>
```

Files are stored by their content hash, enabling:
- Automatic deduplication
- Efficient storage for similar files
- Integrity verification

### Database Schema

```sql
-- Nodes table: stores all entities
CREATE TABLE nodes (
  id BLOB PRIMARY KEY,         -- BLAKE3 hash
  kind TEXT NOT NULL,          -- Node type
  payload TEXT NOT NULL,       -- Canonical JSON
  created_at INTEGER NOT NULL  -- Unix milliseconds
);

-- Edges table: stores relationships
CREATE TABLE edges (
  src BLOB NOT NULL,           -- Source node ID
  type TEXT NOT NULL,          -- Edge type
  dst BLOB NOT NULL,           -- Destination node ID
  at BLOB,                     -- Context (snapshot/changeset ID)
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst, at)
);

-- Indexes for query performance
CREATE INDEX edges_src ON edges(src);
CREATE INDEX edges_dst ON edges(dst);
CREATE INDEX edges_type ON edges(type);
```

### Tree-sitter Integration

Kai uses Tree-sitter for parsing TypeScript/JavaScript:

- **Language**: JavaScript grammar (handles most TS syntax)
- **Node types parsed**:
  - `function_declaration`
  - `class_declaration`
  - `method_definition`
  - `variable_declaration`
  - `lexical_declaration`
  - `arrow_function`
  - `export_statement`
  - `binary_expression`
  - `number`, `string`

### Change Detection Algorithm

1. **File Comparison**
   ```
   changed_files = files where base.digest != head.digest
   ```

2. **AST Differencing**
   - Parse before/after with Tree-sitter
   - Find corresponding nodes by position proximity
   - Compare node content and structure

3. **Detector Execution**
   - `operator_or_boundary_changed`: Compare operators and numeric literals
   - `literal_value_changed`: Compare string/number values at same position
   - `params_or_exports_changed`: Compare function signatures and export lists

---

## Use Cases

### Code Review Enhancement

Use Kai to understand pull requests at a semantic level:

```bash
# Create snapshots of base and PR branches
kai snapshot main --repo .
kai snapshot pr-123 --repo .

# Analyze and compare
kai analyze symbols <main-snap>
kai analyze symbols <pr-snap>
kai changeset create <main-snap> <pr-snap>

# Get summary
kai intent render <changeset>
kai dump <changeset> --json > review.json
```

### Change Impact Analysis

Understand what modules and symbols are affected:

```bash
# View affected modules in changeset output
kai changeset create <before> <after>
# Output includes: Affected modules: [Auth, Billing]

# Get detailed impact from JSON
kai dump <changeset> --json | jq '.nodes[] | select(.kind == "Symbol")'
```

### Changelog Generation

Generate semantic changelogs:

```bash
# For each release tag pair
for version in v1.0 v1.1 v1.2; do
  kai snapshot $version --repo .
done

# Generate changesets between versions
kai changeset create <v1.0-snap> <v1.1-snap>
kai intent render <cs-1>

kai changeset create <v1.1-snap> <v1.2-snap>
kai intent render <cs-2>
```

### Auditing and Compliance

Track what types of changes were made:

```bash
# Export all change types
kai dump <changeset> --json | jq '.nodes[] | select(.kind == "ChangeType")'

# Find all API surface changes
kai dump <changeset> --json | jq '.nodes[] | select(.payload.category == "API_SURFACE_CHANGED")'
```

---

## Troubleshooting

### Common Issues

**"opening repository: repository does not exist"**
```
Error: opening repo: repository does not exist at '.'
```
Solution: Ensure you're in a Git repository or specify `--repo` path.

**"resolving ref: not a branch, tag, or commit hash"**
```
Error: resolving ref "main": not a branch, tag, or commit hash
```
Solution: Verify the Git ref exists (`git branch -a`, `git tag`).

**"invalid snapshot ID"**
```
Error: invalid snapshot ID: encoding/hex: invalid byte
```
Solution: Use the full 64-character hex ID from snapshot output.

**"changeset not found"**
```
Error: getting changeset data: changeset not found
```
Solution: Verify the changeset ID. Run `sqlite3 .kai/db.sqlite "SELECT hex(id) FROM nodes WHERE kind='ChangeSet'"`.

### Debugging

**View database contents:**
```bash
# List all nodes
sqlite3 .kai/db.sqlite "SELECT kind, COUNT(*) FROM nodes GROUP BY kind"

# List all snapshots
sqlite3 .kai/db.sqlite "SELECT hex(id), payload FROM nodes WHERE kind='Snapshot'"

# List all changesets
sqlite3 .kai/db.sqlite "SELECT hex(id), json_extract(payload, '$.intent') FROM nodes WHERE kind='ChangeSet'"
```

**Check object storage:**
```bash
# List stored objects
ls -la .kai/objects/

# View a file's content
cat .kai/objects/<hash>
```

**Reset Kai:**
```bash
# Remove all Kai data
rm -rf .kai/

# Reinitialize
kai init
```

---

## Development

### Building

```bash
# Build CLI
cd kai-cli && make build

# Build server
cd kailab && make build

# Run CLI tests
cd kai-cli && make test

# Run server tests
cd kailab && make test

# Run all tests
(cd kai-core && go test ./...) && \
(cd kai-cli && go test ./...) && \
(cd kailab && go test ./...)
```

### Running Tests

```bash
# All tests
go test ./...

# Specific package
go test ./internal/parse/...

# With verbose output
go test -v ./...

# With coverage
go test -cover ./...
```

### Project Structure

```
kai/
├── kai-cli/                     # CLI application
│   ├── cmd/kai/
│   │   └── main.go              # CLI entry point with Cobra commands
│   ├── internal/
│   │   ├── graph/               # SQLite node/edge storage (local)
│   │   ├── gitio/               # go-git repository operations
│   │   ├── snapshot/            # Snapshot creation and symbol analysis
│   │   ├── module/              # Path glob matching
│   │   ├── classify/            # Change type detection
│   │   └── remote/              # Kailab HTTP client
│   ├── schema/
│   │   └── 0001_init.sql        # Local database schema
│   ├── rules/
│   │   ├── modules.yaml         # Default module patterns
│   │   └── changetypes.yaml     # Change type definitions
│   ├── testdata/
│   │   └── repo/                # Sample Git repository
│   ├── go.mod
│   └── Makefile
│
├── kai-core/                    # Shared library
│   ├── cas/                     # Content-addressed storage (BLAKE3)
│   ├── util/                    # Canonical JSON utilities
│   ├── parse/                   # Tree-sitter parsing
│   ├── detect/                  # Change type detection
│   ├── diff/                    # Semantic diff computation
│   ├── intent/                  # Intent generation
│   ├── merge/                   # AST-aware 3-way merge
│   └── go.mod
│
├── kailab/                      # Data plane server
│   ├── cmd/kailabd/
│   │   └── main.go              # Server entry point
│   ├── api/                     # HTTP handlers and middleware
│   ├── repo/                    # Multi-repo registry with LRU caching
│   ├── store/                   # SQLite storage layer
│   ├── pack/                    # Pack format encoding/decoding
│   ├── proto/                   # Wire protocol DTOs
│   ├── background/              # Background enrichment workers
│   ├── config/                  # Environment configuration
│   └── go.mod
│
├── kailab-control/              # Control plane server
│   ├── cmd/kailab-control/
│   │   └── main.go              # Server entry point
│   ├── internal/
│   │   ├── api/                 # HTTP handlers, middleware, routes
│   │   ├── auth/                # JWT, magic links, PATs
│   │   ├── cfg/                 # Environment configuration
│   │   ├── db/                  # SQLite database layer
│   │   ├── model/               # Data models
│   │   └── routing/             # Shard picker
│   ├── frontend/                # Svelte + Tailwind web console
│   │   ├── src/
│   │   │   ├── lib/             # Stores, API client
│   │   │   └── routes/          # Page components
│   │   └── package.json
│   └── go.mod
│
└── README.md
```

### Adding a New Change Type

1. Define the rule in `rules/changetypes.yaml`
2. Add the category constant in `internal/classify/classify.go`
3. Implement the detector function
4. Add tests in `internal/classify/classify_test.go`

### Adding a New Language

1. Add Tree-sitter grammar dependency
2. Update `internal/parse/parse.go` with language detection
3. Add symbol extraction logic for new AST node types
4. Update `internal/gitio/gitio.go` file extension detection

---

## Kailab Server

Kailab is a fast, multi-tenant, DB-backed server for hosting Kai repositories remotely. It provides HTTP APIs for pushing and fetching snapshots, changesets, and other semantic objects. A single Kailab process can serve many repositories across multiple tenants.

### Architecture

- **Multi-repo**: One server process serves many repositories
- **Multi-tenant**: Repositories are organized by `/{tenant}/{repo}`
- **Per-repo isolation**: Each repo has its own SQLite database
- **LRU caching**: Open repo handles are cached with idle eviction

### Running the Server

```bash
# Build and run
cd kailab
go build -o kailabd ./cmd/kailabd
./kailabd --data ./data --listen :7447

# Or with environment variables
KAILAB_LISTEN=:7447 KAILAB_DATA=./data ./kailabd
```

**Output:**
```
kailabd starting...
  listen:       :7447
  data:         ./data
  max_open:     256
  idle_ttl:     10m0s
Multi-repo mode: routes are /{tenant}/{repo}/v1/...
Admin routes: POST /admin/v1/repos, GET /admin/v1/repos, DELETE /admin/v1/repos/{tenant}/{repo}
```

### Server Configuration

| Variable | Flag | Default | Description |
|----------|------|---------|-------------|
| `KAILAB_LISTEN` | `--listen` | `:7447` | HTTP listen address |
| `KAILAB_DATA` | `--data` | `./data` | Base directory for repo databases |
| `KAILAB_MAX_OPEN` | - | `256` | Max number of repos to keep open (LRU) |
| `KAILAB_IDLE_TTL` | - | `10m` | How long to keep idle repos open |
| `KAILAB_MAX_PACK_SIZE` | - | `256MB` | Maximum pack upload size |

### Filesystem Layout

```
data/
├── acme/                    # tenant
│   ├── webapp/              # repo
│   │   └── kai.db           # SQLite database (WAL mode)
│   └── api/
│       └── kai.db
└── other-org/
    └── main/
        └── kai.db
```

### Admin API

Create and manage repositories via the admin API:

```bash
# Create a repository
curl -X POST http://localhost:7447/admin/v1/repos \
  -H "Content-Type: application/json" \
  -d '{"tenant":"acme","repo":"webapp"}'

# List all repositories
curl http://localhost:7447/admin/v1/repos

# Delete a repository
curl -X DELETE http://localhost:7447/admin/v1/repos/acme/webapp
```

### Remote Commands

#### `kai remote set`

Configure a remote server with tenant and repository.

```bash
kai remote set <name> <url> [flags]
```

**Flags:**
- `--tenant <name>` - Tenant/org name (default: `default`)
- `--repo <name>` - Repository name (default: `main`)

**Examples:**
```bash
# Set remote with default tenant/repo
kai remote set origin http://localhost:7447

# Set remote with specific tenant/repo
kai remote set origin http://localhost:7447 --tenant acme --repo webapp

# Multiple remotes for different repos
kai remote set staging http://localhost:7447 --tenant acme --repo staging
kai remote set prod https://kailab.example.com --tenant acme --repo production
```

---

#### `kai remote get`

Get a remote's configuration.

```bash
kai remote get <name>
```

**Example:**
```bash
kai remote get origin
# Output:
# URL:    http://localhost:7447
# Tenant: acme
# Repo:   webapp
```

---

#### `kai remote list`

List all configured remotes.

```bash
kai remote list
```

**Output:**
```
NAME             TENANT        REPO          URL
origin           acme          webapp        http://localhost:7447
staging          acme          staging       http://localhost:7447
prod             acme          production    https://kailab.example.com
```

---

#### `kai remote del`

Delete a remote.

```bash
kai remote del <name>
```

---

#### `kai push`

Push workspaces, changesets, or snapshots to a remote server.

**In Kai, you primarily push workspaces** (the unit of collaboration). Changesets within the workspace are the meaningful units that collaborators review. Snapshots travel automatically as infrastructure.

```bash
kai push [remote] [--ws <workspace>] [target...]
```

**Arguments:**
- `[remote]` - Remote name (default: `origin`)
- `[--ws <workspace>]` - Workspace to push (default: current workspace if set)
- `[target...]` - Explicit targets with prefix: `cs:<ref>` or `snap:<ref>`

**Push hierarchy:**

| Level | Command | What gets pushed |
|-------|---------|------------------|
| **Primary** | `kai push origin` | Current workspace (all changesets + required snapshots) |
| **Secondary** | `kai push origin cs:login_fix` | Single changeset (+ its base/head snapshots) |
| **Tertiary** | `kai push origin snap:abc123` | Single snapshot (advanced/plumbing) |

**Examples:**
```bash
# Push your current workspace (the normal workflow)
kai push origin

# Push a specific workspace
kai push origin --ws feature/auth

# Push a single changeset for targeted review
kai push origin cs:reduce_timeout

# Push a snapshot (rarely needed, advanced)
kai push origin snap:4a2556c0
```

**What gets pushed for a workspace:**
1. Workspace node (metadata: name, status, description)
2. All changesets in the workspace stack (ordered)
3. All snapshots those changesets reference (base/head)
4. All file objects needed to reconstruct the snapshots
5. Refs created:
   - `ws.<name>` → workspace node (enables `fetch --ws`)
   - `ws.<name>.base` → base snapshot
   - `ws.<name>.head` → head snapshot
   - `ws.<name>.cs.<id>` → each changeset

**Flags:**
- `--dry-run` - Show what would be transferred without pushing
- `--force` - Force ref updates on name collisions (content is immutable)

**What happens:**
1. Client discovers which objects remote already has (negotiation)
2. Client computes minimal transfer set (missing changesets → snapshots → files)
3. Client builds zstd-compressed pack of missing objects
4. Pack is uploaded and ingested
5. Refs are atomically updated on server

**Note:** Because content is immutable and addressed by hash, pushes are idempotent and there are no "push conflicts." Conflicts only occur at integration time, where semantic merge handles them.

---

#### `kai fetch`

Fetch workspaces, changesets, or snapshots from a remote server.

```bash
kai fetch [remote] [--ws <workspace>] [target...]
```

**Arguments:**
- `[remote]` - Remote name (default: `origin`)
- `[target...]` - Explicit targets with prefix: `cs:<ref>` or `snap:<ref>`

**Flags:**
- `--ws <name>` - Fetch a specific workspace by name and recreate it locally

**Examples:**
```bash
# Fetch all remote refs (metadata only, lazy content)
kai fetch origin

# Fetch a specific workspace (downloads and recreates locally)
kai fetch origin --ws feature/auth

# Then checkout the workspace to filesystem
kai ws checkout --ws feature/auth --dir ./src

# Fetch a specific changeset
kai fetch origin cs:login_fix

# Fetch a specific snapshot
kai fetch origin snap:main
```

**What happens with `--ws`:**
1. Fetches the workspace ref (`ws.<name>`)
2. Downloads the workspace node and all related objects (snapshots, changesets)
3. Uses BFS to recursively fetch dependencies (parent snapshots, changeset before/after snapshots)
4. Recreates the workspace locally with proper edges (BASED_ON, HEAD_AT, HAS_CHANGESET)
5. Sets both local (`ws.<name>`) and remote tracking (`remote/origin/ws.<name>`) refs

**What happens without `--ws`:**
1. Fetches ref metadata from remote
2. For changesets: downloads the changeset + its base/head snapshots
3. For snapshots: downloads the snapshot + file objects

---

#### `kai remote-log`

Show the ref history from a remote server.

```bash
kai remote-log [remote]
```

**Arguments:**
- `[remote]` - Remote name (default: `origin`)

**Example:**
```bash
kai remote-log origin
```

**Output:**
```
snap.latest  abc123...  user@example.com  2024-12-02T15:30:00Z
snap.main    def456...  user@example.com  2024-12-02T14:00:00Z
```

---

#### `kai auth login`

Authenticate with a Kailab control plane server.

```bash
kai auth login [server-url]
```

**Arguments:**
- `[server-url]` - Server URL (optional, uses origin remote's URL if not provided)

**Examples:**
```bash
# Login to a specific server
kai auth login http://localhost:8080

# Login using the origin remote's URL
kai auth login
```

**What happens:**
1. Prompts for your email address
2. Sends a magic link to your email (in dev mode, token is returned directly)
3. You enter the token from your email
4. Tokens are exchanged and stored in `~/.kai/credentials.json`

---

#### `kai auth logout`

Clear stored credentials.

```bash
kai auth logout
```

---

#### `kai auth status`

Show current authentication status.

```bash
kai auth status
```

**Output:**
```
Logged in as: user@example.com
Server:       http://localhost:8080
Status:       Authenticated
```

### Server API

The Kailab server exposes these HTTP endpoints. All repo-scoped endpoints use the `/{tenant}/{repo}` prefix:

**Admin Routes:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/admin/v1/repos` | Create a new repository |
| `GET` | `/admin/v1/repos` | List all repositories |
| `DELETE` | `/admin/v1/repos/{tenant}/{repo}` | Delete a repository |

**Repo-Scoped Routes** (prefix: `/{tenant}/{repo}`):

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/{tenant}/{repo}/v1/push/negotiate` | Object negotiation |
| `POST` | `/{tenant}/{repo}/v1/objects/pack` | Ingest zstd-compressed pack |
| `GET` | `/{tenant}/{repo}/v1/objects/{digest}` | Get a single object |
| `PUT` | `/{tenant}/{repo}/v1/refs/{name}` | Update a ref |
| `GET` | `/{tenant}/{repo}/v1/refs` | List all refs |
| `GET` | `/{tenant}/{repo}/v1/log/head` | Get the latest log entry |
| `GET` | `/{tenant}/{repo}/v1/log/entries` | Get paginated ref history |

### Pack Format

Objects are transferred in zstd-compressed packs:

```
[4-byte header length][header JSON][object data...]
```

Header JSON structure:
```json
{
  "objects": [
    {"digest": "abc123...", "kind": "Snapshot", "offset": 0, "size": 1234},
    {"digest": "def456...", "kind": "File", "offset": 1234, "size": 567}
  ]
}
```

### Ref History

All ref updates are logged in an append-only history with hash chaining:

```
entry_hash = BLAKE3(prev_hash || ref_name || target || actor || timestamp)
```

This provides:
- Audit trail of all ref changes
- Tamper detection via hash chain
- Attribution to actors (users/systems)

---

## Kailab Control Plane

Kailab Control is a GitLab-like control plane service that provides user authentication, organization management, and repository metadata. It acts as a gateway to one or more Kailab data plane shards.

### Features

- **Magic Link Authentication** - Passwordless login via email
- **JWT Access Tokens** - Short-lived tokens with refresh capability
- **Personal Access Tokens (PATs)** - Long-lived tokens for CLI/API access
- **Organizations** - Namespaces for grouping repositories
- **Role-Based Access Control** - Owner, Admin, Maintainer, Developer, Reporter, Guest
- **Reverse Proxy** - Routes authenticated requests to kailabd shards
- **Web Console** - Svelte + Tailwind frontend for management

### Running the Control Plane

```bash
cd kailab-control

# Build
make build

# Run in development mode (includes debug logging, dev tokens)
make dev

# Run in production
./kailab-control
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `KLC_LISTEN` | `:8080` | HTTP listen address |
| `KLC_DB_URL` | `kailab-control.db` | SQLite database path |
| `KLC_JWT_KEY` | (required) | JWT signing key |
| `KLC_SHARDS` | `default=http://localhost:7447` | Comma-separated shard URLs |
| `KLC_DEBUG` | `false` | Enable debug mode |

### API Endpoints

**Authentication:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/auth/magic-link` | Request magic link email |
| `POST` | `/api/v1/auth/token` | Exchange magic token for access/refresh tokens |
| `POST` | `/api/v1/auth/refresh` | Refresh access token |
| `POST` | `/api/v1/auth/logout` | Logout (delete sessions) |
| `GET` | `/api/v1/me` | Get current user info |

**Organizations:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/orgs` | Create organization |
| `GET` | `/api/v1/orgs` | List user's organizations |
| `GET` | `/api/v1/orgs/{org}` | Get organization details |
| `GET` | `/api/v1/orgs/{org}/members` | List members |
| `POST` | `/api/v1/orgs/{org}/members` | Add member |
| `DELETE` | `/api/v1/orgs/{org}/members/{id}` | Remove member |

**Repositories:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/orgs/{org}/repos` | Create repository |
| `GET` | `/api/v1/orgs/{org}/repos` | List repositories |
| `GET` | `/api/v1/orgs/{org}/repos/{repo}` | Get repository |
| `DELETE` | `/api/v1/orgs/{org}/repos/{repo}` | Delete repository |

**API Tokens:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/tokens` | Create PAT |
| `GET` | `/api/v1/tokens` | List PATs |
| `DELETE` | `/api/v1/tokens/{id}` | Delete PAT |

**Data Plane Proxy:**

| Pattern | Description |
|---------|-------------|
| `/{org}/{repo}/v1/*` | Proxied to kailabd shard |

### Typical Workflow

```bash
# 1. Start the control plane
cd kailab-control && make dev

# 2. Start a kailabd shard
cd kailab && ./kailabd --data ./data

# 3. Configure CLI remote
kai remote set origin http://localhost:8080 --tenant myorg --repo myrepo

# 4. Login
kai auth login http://localhost:8080

# 5. Push/fetch as usual (now authenticated)
kai push origin snap.latest
kai fetch origin snap.main
```

### Web Console

The control plane includes a web console built with Svelte and Tailwind CSS.

**Development:**
```bash
cd kailab-control

# Run frontend dev server (hot reload)
make web-dev

# In another terminal, run the Go backend
make dev
```

**Production build:**
```bash
# Build frontend (outputs to internal/api/web/)
make web

# Build Go binary (embeds frontend)
make build
```

---

## Northstar & Critical Risks

This section is a ruthless, founder-grade teardown of Kai: what's brittle, what's risky, and what must change before we scale. It serves as the northstar for development priorities.

### One-Sentence Thesis

Kai's core idea—semantic changesets with selective CI—is compelling, but we're skating on thin ice across **correctness**, **performance at scale**, and **mental-model clarity**. If we don't nail those three, we'll get relegated to "neat dev tool" instead of "critical path infra."

---

### Product & Strategy Risks

* **Value depends on "safe skipping."** If selective CI ever skips a test that should've run, users will disable Kai the next day. Our bar is "better than running everything" **and** "never silently wrong." That's hard.
* **Overlap with incumbents.** Review UI + semantic diffs + test selection overlaps with GitHub (stacked PRs/Code View), Sourcegraph (code intel), Launchable/TestImpact (selection), Graphite (stacks). We must be **10× easier** or **provably safer**.
* **"Yet another system of record."** We're introducing a new graph of truth (snapshots/changesets/symbols). Enterprises already have Git, CI, test analytics, SAST/DAST, coverage DBs. Selling a *new* core store is uphill unless ROI is immediate and obvious.
* **Multi-language promise vs. reality.** If TS/JS is great but Python/Go/Rust lag, teams will conclude Kai isn't "ready." Partial language coverage kills perception.

---

### Core Model Risks

* **Snapshots are global, workspaces are overlays—good—but selectors are confusing.** Global selectors like `@snap:prev` mixed with workspace mental models cause user error. If users have to ask "prev relative to what?" you already lost.
* **ChangeSets are global "meaning units," but…** most teams still review & gate on *PRs*. If ChangeSets ≠ PRs, you need a crisp 1:1 mapping or people will ignore your model and stay in GitHub.
* **AST semantic diffs are brittle on dynamic languages.** Tree-sitter can parse structure but not resolve semantics (imports, types, aliasing) reliably at scale without a type system (TS OK, JS meh, Python worse). "Semantic rename/move" correctness is non-trivial.

---

### Selective CI: Correctness & Safety (Biggest Existential Risk)

* **Dynamic imports / reflection / DI / runtime plugin loading.** Our graph will miss edges; false negatives will slip. We must default to **over-selection** with explicit uncertainty reporting.
* **Mocks/stubs hide dependencies.** Test code might import an interface while real dependency changes elsewhere. Static analysis won't see the coupling; coverage can be flaky.
* **Cross-repo & contract tests.** If service A changes public API used by B, you need contract edges or you'll under-run. Single-repo analysis isn't enough in microservices.
* **Generated code / codegen steps.** If codegen artifacts are the real dependencies but aren't in the repo, plans are wrong unless you model the build graph.
* **Flaky tests & nondeterminism.** Even when your plan is right, flake will be blamed on "Kai skipped something." You need a flake strategy (quarantine, auto-rerun).

**Bluntly:** shipping selection without a **provable safety layer** and **transparent uncertainty** will burn trust.

---

### Parsing & Symbol Graph Limitations

* **Name resolution.** Without full TS program analysis (or language server integration), symbol "USES" edges will be lossy (aliasing, re-exports, barrel files).
* **Macros / transpilers.** Babel/TS transforms, decorators, tsc path mapping, module aliases—our static import walker must honor toolchain configs or miss edges.
* **Multi-language edges.** SQL embedded in strings, protobuf schemas, GraphQL—real systems cross language boundaries. If Kai ignores these, impact analysis is incomplete.

---

### Performance & Scale

* **SQLite as per-repo store:** great for local dev; risky for multi-tenant server scale. Concurrency (WAL), long-running writes (bulk ingest), and vacuum/GC will bite you. We'll need sharding & compaction sooner than expected.
* **Snapshot frequency explosion.** `ws stage` creates snapshots constantly. Graph growth can be O(N changes × files). We need aggressive dedup, segment compaction, and reachability indexes or lists will blow up.
* **Cold-start indexing.** First run on monorepos (100k+ files) must finish in minutes, not hours. Tree-sitter parallelization, incremental parsing, and caching are mandatory.
* **Pack protocol.** If we roll our own and it's not resumable, chunked, and content-address negotiation is naive, WAN pushes will be painful.

---

### Merge & Integration

* **AST-merge correctness.** Three-way symbol-merge is easy to demo, hard to make bulletproof (reordering, formatting, comments, trailing commas, import sorting). If conflicts surface as malformed code, we'll be blamed.
* **Hidden coupling across files.** Per-file merges won't catch invariant breaks (e.g., function signature + call sites across files). If we don't run type checks/tests post-merge, we ship broken code.

---

### UX / DevEx Friction

* **Too many new nouns.** Snapshot, ChangeSet, Workspace, Review, Selector types, Module, ChangeType, Symbol—this is a lot. If newcomers can't get value in 2–3 commands, you lose PLG.
* **Ambiguous commands.** Anything that can be interpreted as Git vs Dir mode must be banned or explicit (you already moved that way; keep going).
* **YAML fatigue.** "Define modules in YAML" is not delightful. Wizarding and preview are a must; otherwise users will skip modules and your impact analysis loses value.

---

### Security & Compliance

* **Pushing source to a remote graph.** Enterprises will balk unless you support self-hosting, SSO/SAML, audit trails, encryption at rest, tenant isolation, and strict data retention. Also: PII scanning in stored snapshots.
* **Provenance & tamper evidence.** If Kai claims to be a trusted history, you need a signed ref-log / hash chain (you mention it—make it default) and key management.

---

### Immediate Action Items

1. **Make selection provably safe.**
   * Always emit an **uncertainty budget**; if non-empty, expand selection until `risk: low`, or fail (configurable).
   * Add a **"paranoia mode"** flag per change type (e.g., `API_SURFACE_CHANGED` forces module-wide tests).

2. **Tighten selectors; remove ambiguity.**
   * Prefer `@ws:<name>:base|head` or `@ws:base` when inside a workspace.
   * Hard deprecate `@snap:prev` in tutorials for integrate.

3. **Language-accurate import resolution.**
   * For TS/JS, read tsconfig paths, package.json exports, Babel aliases.
   * Cache a project graph (like tsc) and reuse it.

4. **Ship a delightful modules wizard.**
   * `kai modules init --infer --write` + `modules preview` + TUI rename.

5. **Performance guardrails.**
   * Incremental parsing caches per file hash.
   * Parallelize symbol extraction aggressively.
   * Add "slow repo" telemetry with opt-in.

6. **Storage/GC defaults.**
   * Auto-prune unreachable older than N days by default on local (with prompt).
   * Surface storage footprint in `kai status`.

7. **Minimal CI integration path.**
   * One page: copy this step → it prints a plan → your CI uses it.
   * No runner bindings in CLI.

8. **Telemetry for trust.**
   * Emit post-run reconciliation: "Kai selected 132 tests; 0 failed due to missing dependency; uncertainty=0."
   * If any failure strongly suggests under-selection (e.g., import error), auto-mark `risk: high` next plan.

9. **Hard boundaries in UX.**
   * `kai snapshot` without args → error.
   * `kai snap` is the blessed local shortcut.
   * `kai ws stage` with no current workspace → error with fix hint.

10. **Kill-switch plan.**
    * If selection produces a failure pattern indicating possible miss, CI should retry with **full suite** automatically and flag Kai for inspection. Better to cost more than to lose trust.

---

### Experiments & Metrics (Prove It Works)

* **Primary metric:** P95 CI wall-time reduction per PR **with** stable failure rate (no increase in flake/rollback).
* **Secondary:** % of plans with `risk: low`; % plans that expanded; average tests selected vs. total.
* **Trust metric:** # of "fallback to full run" events per 100 plans; # of confirmed under-selections (must be ~0).
* **Adoption:** # repos with plan in CI; # devs using `ws stage` weekly.
* **Perf:** time to produce plan on repo sizes (10k, 50k, 200k files).

Run a **90-day bake-off** with 3–5 design partners; publish a case study ("70% CI time reduction, zero misses").

---

### Red-Flag Kill Criteria (Be Willing to Pivot)

* If under-selection (confirmed) > **0.1%** of plans after uncertainty expansion → selection engine not production-safe.
* If plan computation P95 > **10s** on 50k-file repo → not viable for inner loop.
* If < **30%** CI time reduction on average after 1 month across partners → wedge isn't sharp enough; focus on review semantics or analytics instead.

---

### Where Kai *Is* Uniquely Strong (Double Down)

* **Stacked semantic changesets** as first-class; superseding reviews with AST-aware diffs.
* **Content-addressed, immutable history** you can query (not just text diffs).
* **Orchestrator of *what to run*** across tests/builds—if it's **safe** and **fast**.

Make those three world-class; everything else is ornament.

---

## Roadmap

### Near-term

- [ ] Dependency graph analysis (imports/exports between files)
- [ ] Test mapping (which tests cover which symbols)
- [ ] CODEOWNERS integration
- [ ] Watch mode for continuous analysis

### Medium-term

- [ ] Web UI for changeset visualization
- [ ] VS Code extension
- [ ] GitHub Action for PR analysis
- [ ] Additional languages (Go, Python, Rust)

### Long-term

- [ ] ML-based change type classification
- [ ] Natural language intent generation
- [ ] Cross-repository analysis
- [ ] Time-series analysis (change patterns over time)

---

## License

GPLv3 License - See LICENSE file for details.

---

## Contributing

Contributions are welcome! Please read the contributing guidelines and submit pull requests to the main repository.
