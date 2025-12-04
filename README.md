<p align="center">
  <img src="kai.webp" alt="Kai Logo" width="400">
</p>

# Kai - Intent Version Control System

Kai is a semantic, intent-based version control system that understands *what* your code changes mean, not just *what* changed. It creates semantic snapshots from Git refs, computes changesets with classified change types, maps changes to logical modules, and generates human-readable intent sentences describing the purpose of changes.

Unlike traditional diff tools that show line-by-line text changes, Kai understands your code at a semantic level—identifying functions, classes, variables, and how they relate to your project's architecture.

## Table of Contents

- [Key Concepts](#key-concepts)
- [Kai vs Git](#kai-vs-git)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Human-Friendly References](#human-friendly-references)
- [Workspace Workflow](#workspace-workflow)
- [Complete Workflow Tutorial](#complete-workflow-tutorial)
- [Command Reference](#command-reference)
- [Configuration](#configuration)
- [Understanding the Output](#understanding-the-output)
- [Architecture Deep Dive](#architecture-deep-dive)
- [Use Cases](#use-cases)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [Kailab Server](#kailab-server)
- [Kailab Control Plane](#kailab-control-plane)
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
../../kai init
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
../../kai snapshot main --repo .
```

**Output:**
```
Created snapshot: d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc
```

Create a snapshot of the feature branch:

```bash
../../kai snapshot feature --repo .
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
../../kai analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc

# Analyze feature branch snapshot
../../kai analyze symbols 4a2556c086b1f664eaa5642e3bc0cddaa7423759d077701981e8e7e5ab0d39a3
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
../../kai changeset create \
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
../../kai intent render 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203
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
../../kai intent render 90cd72... --edit "Reduce session timeout to 30 minutes"
```

### Step 7: Dump ChangeSet as JSON

View the complete changeset data:

```bash
../../kai dump 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203 --json
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
- `.ts` - TypeScript
- `.tsx` - TypeScript JSX
- `.js` - JavaScript
- `.jsx` - JavaScript JSX

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
│   ├── intent/                  # Intent generation
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

Push snapshots and changesets to a remote server.

```bash
kai push [remote] [refs...]
```

**Arguments:**
- `[remote]` - Remote name (default: `origin`)
- `[refs...]` - Refs to push (default: `snap.latest`)

**Examples:**
```bash
# Push latest snapshot to origin
kai push

# Push specific refs
kai push origin snap.main cs.latest

# Push to a different remote
kai push production snap.release
```

**What happens:**
1. Client sends list of object digests to server's negotiate endpoint
2. Server responds with which objects it needs
3. Client builds a zstd-compressed pack of missing objects
4. Pack is uploaded and ingested
5. Refs are updated on the server

---

#### `kai fetch`

Fetch refs and objects from a remote server.

```bash
kai fetch [remote] [refs...]
```

**Arguments:**
- `[remote]` - Remote name (default: `origin`)
- `[refs...]` - Refs to fetch (default: `snap.latest`)

**Examples:**
```bash
# Fetch latest from origin
kai fetch

# Fetch specific refs
kai fetch origin snap.main snap.feature

# Fetch from production
kai fetch production snap.release
```

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
