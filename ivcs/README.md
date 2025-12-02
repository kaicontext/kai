# IVCS - Intent Version Control System

IVCS is a semantic, intent-based version control system that understands *what* your code changes mean, not just *what* changed. It creates semantic snapshots from Git refs, computes changesets with classified change types, maps changes to logical modules, and generates human-readable intent sentences describing the purpose of changes.

Unlike traditional diff tools that show line-by-line text changes, IVCS understands your code at a semantic level—identifying functions, classes, variables, and how they relate to your project's architecture.

## Table of Contents

- [Key Concepts](#key-concepts)
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
IVCS classifies changes into semantic categories:

| Change Type | Description | Example |
|-------------|-------------|---------|
| `CONDITION_CHANGED` | Logic/comparison operators or boundaries changed | `if (x > 100)` → `if (x > 50)` |
| `CONSTANT_UPDATED` | Literal values (numbers, strings) changed | `const TIMEOUT = 3600` → `1800` |
| `API_SURFACE_CHANGED` | Function signatures or exports changed | `login(user)` → `login(user, token)` |

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

## Installation

### Prerequisites

- **Go 1.22+** (uses Go 1.24 features)
- **Git** (optional - only needed for Git-based snapshots)

### Building from Source

```bash
# Clone or navigate to the ivcs directory
cd ivcs

# Build the CLI binary
make build

# Or build directly with Go
go build -o ivcs ./cmd/ivcs

# Verify installation
./ivcs --help
```

### Optional: Add to PATH

```bash
# Add to your shell profile for global access
export PATH="$PATH:/path/to/ivcs"

# Or move/symlink to a directory in your PATH
sudo ln -s /path/to/ivcs/ivcs /usr/local/bin/ivcs
```

---

## Quick Start

Here's the fastest way to get started with IVCS:

```bash
# 1. Navigate to your Git repository
cd your-typescript-project

# 2. Initialize IVCS
ivcs init

# 3. Create a snapshot of your main branch
ivcs snapshot main --repo .
# Output: Created snapshot: abc123...

# 4. Create a snapshot of your feature branch
ivcs snapshot feature-branch --repo .
# Output: Created snapshot: def456...

# 5. Analyze symbols in both snapshots
ivcs analyze symbols abc123...
ivcs analyze symbols def456...

# 6. Create a changeset between them
ivcs changeset create abc123... def456...
# Output: Created changeset: ghi789...

# 7. Generate an intent sentence
ivcs intent render ghi789...
# Output: Intent: Update Auth login

# 8. View the full changeset as JSON
ivcs dump ghi789... --json
```

---

## Human-Friendly References

You don't need to juggle 64-character BLAKE3 hashes! IVCS provides multiple ways to reference snapshots and changesets using human-readable handles.

### Short IDs

Use the first 8-12 hex characters of any ID. IVCS will resolve it if unambiguous:

```bash
# Instead of the full 64-char ID:
ivcs analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc

# Use a short prefix:
ivcs analyze symbols d9ec9902
```

If the prefix is ambiguous, IVCS shows matching candidates:

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
ivcs ref set snap.main d9ec9902
ivcs ref set snap.feature 4a2556c0
ivcs ref set cs.login_fix 90cd7264

# List all refs
ivcs ref list

# Use refs in any command
ivcs analyze symbols snap.main
ivcs changeset create snap.main snap.feature
ivcs intent render cs.login_fix
ivcs dump cs.login_fix --json

# Delete refs
ivcs ref del cs.login_fix
```

### Auto-Updated Refs

IVCS automatically maintains helpful refs:

| Ref | Updated When | Points To |
|-----|--------------|-----------|
| `snap.latest` | Creating a snapshot | The most recent snapshot |
| `cs.latest` | Creating a changeset | The most recent changeset |
| `ws.<name>.base` | Creating a workspace | The workspace's base snapshot |
| `ws.<name>.head` | Staging changes | The workspace's head snapshot |

```bash
# Use auto-refs in commands
ivcs analyze symbols snap.latest
ivcs changeset create @snap:prev @snap:last
ivcs intent render cs.latest
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
ivcs snapshot main --repo .
ivcs snapshot feature --repo .
ivcs analyze symbols @snap:last
ivcs changeset create @snap:prev @snap:last
ivcs intent render @cs:last
ivcs dump @cs:last --json
```

### Pick Command

Search and select nodes interactively:

```bash
# List recent snapshots
ivcs pick Snapshot

# Filter by substring
ivcs pick Snapshot --filter auth

# List changesets without interactive selection
ivcs pick ChangeSet --no-ui

# Output format shows ID, kind-specific info
#   %-4s  %-16s  %s
1     d9ec990243e5...  main (git)
2     4a2556c086b1...  feature (git)
```

### Shell Completion

Enable tab completion for refs, selectors, and short IDs:

```bash
# Bash
source <(ivcs completion bash)
# Add to ~/.bashrc for persistence

# Zsh
source <(ivcs completion zsh)
# Add to ~/.zshrc for persistence

# Fish
ivcs completion fish | source
# Or save to ~/.config/fish/completions/ivcs.fish

# PowerShell
ivcs completion powershell | Out-String | Invoke-Expression
```

With completion enabled, pressing Tab will suggest:
- Named refs (`snap.main`, `cs.latest`)
- Selectors (`@snap:last`, `@cs:prev`)
- Recent short IDs

### Complete Example Without Long IDs

```bash
# Initialize and create snapshots
ivcs init
ivcs snapshot main --repo .
ivcs snapshot feature --repo .

# Analyze using selectors
ivcs analyze symbols @snap:last
ivcs analyze symbols @snap:prev

# Create and inspect changeset
ivcs changeset create @snap:prev @snap:last
ivcs intent render @cs:last
ivcs dump @cs:last --json

# Name important refs
ivcs ref set snap.main @snap:prev
ivcs ref set snap.feature @snap:last
ivcs ref set cs.feature_changes @cs:last

# Use your named refs
ivcs changeset create snap.main snap.feature
ivcs checkout snap.main --dir ./restore
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

### Step 2: Initialize IVCS

```bash
cd testdata/repo
../../ivcs init
```

**What happens:**
- Creates `.ivcs/` directory
- Initializes SQLite database with schema
- Creates `objects/` directory for content storage
- Copies default rule files to `rules/`

**Output:**
```
Initialized IVCS in .ivcs/
```

**Directory structure created:**
```
.ivcs/
├── db.sqlite          # SQLite database
├── objects/           # Content-addressed file storage
└── rules/
    ├── modules.yaml       # Module definitions
    └── changetypes.yaml   # Change type rules
```

### Step 3: Create Snapshots

Create a snapshot of the main branch:

```bash
../../ivcs snapshot main --repo .
```

**Output:**
```
Created snapshot: d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc
```

Create a snapshot of the feature branch:

```bash
../../ivcs snapshot feature --repo .
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
../../ivcs analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc

# Analyze feature branch snapshot
../../ivcs analyze symbols 4a2556c086b1f664eaa5642e3bc0cddaa7423759d077701981e8e7e5ab0d39a3
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
../../ivcs changeset create \
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
../../ivcs intent render 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203
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
../../ivcs intent render 90cd72... --edit "Reduce session timeout to 30 minutes"
```

### Step 7: Dump ChangeSet as JSON

View the complete changeset data:

```bash
../../ivcs dump 90cd726437a465b9602cfd7abc0bba7e1150726486013b3951539b04b72de203 --json
```

This outputs a structured JSON document containing:
- The changeset node with its payload
- All related nodes (files, symbols, change types, modules)
- All edges connecting them

---

## Command Reference

### `ivcs init`

Initialize IVCS in the current directory.

```bash
ivcs init
```

**Creates:**
- `.ivcs/db.sqlite` - SQLite database with WAL mode
- `.ivcs/objects/` - Content-addressed storage directory
- `.ivcs/rules/modules.yaml` - Module definitions
- `.ivcs/rules/changetypes.yaml` - Change type rules

**Notes:**
- Run this once per project
- Must be run from within a Git repository (or specify `--repo`)
- Safe to run multiple times (idempotent)

---

### `ivcs snapshot`

Create a semantic snapshot from a Git ref or directory.

```bash
ivcs snapshot [git-ref] [flags]
```

**Arguments:**
- `[git-ref]` - Branch name, tag, or commit hash (required for Git mode)

**Flags:**
- `--repo <path>` - Path to Git repository (default: current directory)
- `--dir <path>` - Path to directory (creates snapshot without Git)

**Examples:**
```bash
# Snapshot from Git ref
ivcs snapshot main --repo .

# Snapshot from directory (no Git required)
ivcs snapshot --dir ./src

# Snapshot a specific commit
ivcs snapshot abc123def456

# Snapshot a tag
ivcs snapshot v1.2.3 --repo /path/to/repo
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

### `ivcs analyze symbols`

Extract symbols from all files in a snapshot.

```bash
ivcs analyze symbols <snapshot-id>
```

**Arguments:**
- `<snapshot-id>` - Hex ID of the snapshot to analyze

**Examples:**
```bash
ivcs analyze symbols d9ec990243e5efea78878ffa8314a7fcdb3a69a4c89306c6e909950a4bfa00fc
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

### `ivcs changeset create`

Create a changeset between two snapshots.

```bash
ivcs changeset create <base-snapshot-id> <head-snapshot-id>
```

**Arguments:**
- `<base-snapshot-id>` - The "before" snapshot (typically main branch)
- `<head-snapshot-id>` - The "after" snapshot (typically feature branch)

**Examples:**
```bash
ivcs changeset create abc123... def456...
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

### `ivcs intent render`

Generate or set an intent sentence for a changeset.

```bash
ivcs intent render <changeset-id> [flags]
```

**Arguments:**
- `<changeset-id>` - Hex ID of the changeset

**Flags:**
- `--edit "<text>"` - Manually set the intent text instead of generating

**Examples:**
```bash
# Auto-generate intent
ivcs intent render abc123...
# Output: Intent: Update Auth login

# Manually set intent
ivcs intent render abc123... --edit "Fix session expiration bug"
# Output: Intent: Fix session expiration bug
```

**Auto-generation rules:**
| Priority | Change Type | Verb |
|----------|-------------|------|
| 1 | `API_SURFACE_CHANGED` | "Update" |
| 2 | `CONDITION_CHANGED` | "Modify" |
| 3 | `CONSTANT_UPDATED` | "Update" |

---

### `ivcs dump`

Output changeset data as structured JSON.

```bash
ivcs dump <changeset-id> [flags]
```

**Arguments:**
- `<changeset-id>` - Hex ID of the changeset

**Flags:**
- `--json` - Output as JSON (currently the only format)

**Examples:**
```bash
# Output to terminal
ivcs dump abc123... --json

# Save to file
ivcs dump abc123... --json > changeset.json

# Pretty print with jq
ivcs dump abc123... --json | jq .
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

### `ivcs status`

Show IVCS status and pending changes since last snapshot.

```bash
ivcs status [flags]
```

**Flags:**
- `--dir <path>` - Directory to check for changes (default: current directory)

**Example:**
```bash
ivcs status --dir ./src
```

**Output:**
```
IVCS initialized

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

### `ivcs log`

Show chronological log of snapshots and changesets.

```bash
ivcs log [flags]
```

**Flags:**
- `-n, --limit <count>` - Number of entries to show (default: 10)

**Example:**
```bash
ivcs log -n 5
```

---

### `ivcs ws create`

Create a new workspace (branch) based on a snapshot.

```bash
ivcs ws create --name <name> --base <snapshot-id> [flags]
```

**Flags:**
- `--name <name>` - Workspace name (required)
- `--base <snapshot-id>` - Base snapshot ID (required)
- `--desc <description>` - Optional description

**Example:**
```bash
ivcs ws create --name feature/auth --base abc123...
```

---

### `ivcs ws list`

List all workspaces.

```bash
ivcs ws list
```

**Output:**
```
NAME                  STATUS      BASE          HEAD          CHANGESETS
feature/auth          active      a1b2c3d4e5f6  d4e5f6a1b2c3  2
bugfix/login          shelved     a1b2c3d4e5f6  a1b2c3d4e5f6  0
```

---

### `ivcs ws stage`

Stage changes from a directory into a workspace.

```bash
ivcs ws stage --ws <name> [flags]
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--dir <path>` - Directory to stage from (default: current directory)

**Example:**
```bash
ivcs ws stage --ws feature/auth --dir ./src
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

### `ivcs ws log`

Show the changelog for a workspace.

```bash
ivcs ws log --ws <name>
```

**Example:**
```bash
ivcs ws log --ws feature/auth
```

---

### `ivcs ws shelve`

Shelve a workspace (freeze staging).

```bash
ivcs ws shelve --ws <name>
```

---

### `ivcs ws unshelve`

Unshelve a workspace (resume staging).

```bash
ivcs ws unshelve --ws <name>
```

---

### `ivcs ws close`

Permanently close a workspace.

```bash
ivcs ws close --ws <name>
```

---

### `ivcs integrate`

Integrate workspace changes into a target snapshot.

```bash
ivcs integrate --ws <name> --into <snapshot-id>
```

**Flags:**
- `--ws <name>` - Workspace name or ID (required)
- `--into <snapshot-id>` - Target snapshot ID (required)

**Example:**
```bash
ivcs integrate --ws feature/auth --into abc123...
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

### `ivcs ref list`

List all named references.

```bash
ivcs ref list [flags]
```

**Flags:**
- `--kind <kind>` - Filter by kind (Snapshot, ChangeSet, Workspace)

**Example:**
```bash
ivcs ref list
ivcs ref list --kind Snapshot
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

### `ivcs ref set`

Create or update a named reference.

```bash
ivcs ref set <name> <target>
```

**Arguments:**
- `<name>` - Reference name (e.g., `snap.main`, `cs.bugfix`)
- `<target>` - Target ID (full hex, short prefix, ref name, or selector)

**Examples:**
```bash
# Set using short ID
ivcs ref set snap.main d9ec9902

# Set using another ref
ivcs ref set snap.backup snap.main

# Set using selector
ivcs ref set snap.before_feature @snap:prev
ivcs ref set cs.last_change @cs:last
```

---

### `ivcs ref del`

Delete a named reference.

```bash
ivcs ref del <name>
```

**Arguments:**
- `<name>` - Reference name to delete

**Example:**
```bash
ivcs ref del snap.old_backup
```

---

### `ivcs pick`

Search and select nodes interactively.

```bash
ivcs pick <kind> [flags]
```

**Arguments:**
- `<kind>` - Node kind: `Snapshot`, `ChangeSet`, or `Workspace` (aliases: `snap`, `cs`, `ws`)

**Flags:**
- `--filter <substring>` - Filter by substring in ID or payload
- `--no-ui` - Output matches without interactive selection

**Examples:**
```bash
# List all snapshots
ivcs pick Snapshot

# Search snapshots containing "auth"
ivcs pick snap --filter auth

# List changesets non-interactively
ivcs pick cs --no-ui
```

---

### `ivcs completion`

Generate shell completion scripts.

```bash
ivcs completion [bash|zsh|fish|powershell]
```

**Arguments:**
- `bash` - Generate Bash completion script
- `zsh` - Generate Zsh completion script
- `fish` - Generate Fish completion script
- `powershell` - Generate PowerShell completion script

**Examples:**
```bash
# Bash - add to ~/.bashrc
source <(ivcs completion bash)

# Zsh - add to ~/.zshrc
source <(ivcs completion zsh)

# Fish - save to completions directory
ivcs completion fish > ~/.config/fish/completions/ivcs.fish

# PowerShell
ivcs completion powershell | Out-String | Invoke-Expression
```

---

## Configuration

### Module Definitions

Edit `.ivcs/rules/modules.yaml` to define your project's modules:

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

The `.ivcs/rules/changetypes.yaml` file defines how changes are detected:

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

IVCS uses content-addressed storage for both nodes and file content:

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
.ivcs/objects/<blake3-hash-of-content>
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

IVCS uses Tree-sitter for parsing TypeScript/JavaScript:

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

Use IVCS to understand pull requests at a semantic level:

```bash
# Create snapshots of base and PR branches
ivcs snapshot main --repo .
ivcs snapshot pr-123 --repo .

# Analyze and compare
ivcs analyze symbols <main-snap>
ivcs analyze symbols <pr-snap>
ivcs changeset create <main-snap> <pr-snap>

# Get summary
ivcs intent render <changeset>
ivcs dump <changeset> --json > review.json
```

### Change Impact Analysis

Understand what modules and symbols are affected:

```bash
# View affected modules in changeset output
ivcs changeset create <before> <after>
# Output includes: Affected modules: [Auth, Billing]

# Get detailed impact from JSON
ivcs dump <changeset> --json | jq '.nodes[] | select(.kind == "Symbol")'
```

### Changelog Generation

Generate semantic changelogs:

```bash
# For each release tag pair
for version in v1.0 v1.1 v1.2; do
  ivcs snapshot $version --repo .
done

# Generate changesets between versions
ivcs changeset create <v1.0-snap> <v1.1-snap>
ivcs intent render <cs-1>

ivcs changeset create <v1.1-snap> <v1.2-snap>
ivcs intent render <cs-2>
```

### Auditing and Compliance

Track what types of changes were made:

```bash
# Export all change types
ivcs dump <changeset> --json | jq '.nodes[] | select(.kind == "ChangeType")'

# Find all API surface changes
ivcs dump <changeset> --json | jq '.nodes[] | select(.payload.category == "API_SURFACE_CHANGED")'
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
Solution: Verify the changeset ID. Run `sqlite3 .ivcs/db.sqlite "SELECT hex(id) FROM nodes WHERE kind='ChangeSet'"`.

### Debugging

**View database contents:**
```bash
# List all nodes
sqlite3 .ivcs/db.sqlite "SELECT kind, COUNT(*) FROM nodes GROUP BY kind"

# List all snapshots
sqlite3 .ivcs/db.sqlite "SELECT hex(id), payload FROM nodes WHERE kind='Snapshot'"

# List all changesets
sqlite3 .ivcs/db.sqlite "SELECT hex(id), json_extract(payload, '$.intent') FROM nodes WHERE kind='ChangeSet'"
```

**Check object storage:**
```bash
# List stored objects
ls -la .ivcs/objects/

# View a file's content
cat .ivcs/objects/<hash>
```

**Reset IVCS:**
```bash
# Remove all IVCS data
rm -rf .ivcs/

# Reinitialize
ivcs init
```

---

## Development

### Building

```bash
# Build binary
make build

# Run tests
make test

# Format code
make fmt

# Clean artifacts
make clean
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
ivcs/
├── cmd/ivcs/
│   └── main.go              # CLI entry point with Cobra commands
├── internal/
│   ├── util/                # Canonical JSON, BLAKE3 hashing
│   ├── graph/               # SQLite node/edge storage
│   ├── gitio/               # go-git repository operations
│   ├── snapshot/            # Snapshot creation and symbol analysis
│   ├── parse/               # Tree-sitter parsing
│   ├── module/              # Path glob matching
│   ├── classify/            # Change type detection
│   └── intent/              # Intent generation
├── schema/
│   └── 0001_init.sql        # Database schema
├── rules/
│   ├── modules.yaml         # Default module patterns
│   └── changetypes.yaml     # Change type definitions
├── testdata/
│   ├── repo/                # Sample Git repository
│   └── expected/            # Golden test output
├── go.mod
├── go.sum
├── Makefile
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

MIT License - See LICENSE file for details.

---

## Contributing

Contributions are welcome! Please read the contributing guidelines and submit pull requests to the main repository.
