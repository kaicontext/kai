// Package main provides the kai CLI.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"kai-core/diff"
	"kai-core/merge"
	"kai/internal/classify"
	"kai/internal/dirio"
	"kai/internal/explain"
	"kai/internal/filesource"
	"kai/internal/gitio"
	"kai/internal/graph"
	"kai/internal/intent"
	"kai/internal/module"
	"kai/internal/parse"
	"kai/internal/ref"
	"kai/internal/remote"
	"kai/internal/review"
	"kai/internal/snapshot"
	"kai/internal/status"
	"kai/internal/util"
	"kai/internal/workspace"
)

const (
	kaiDir         = ".kai"
	dbFile         = "db.sqlite"
	objectsDir     = "objects"
	schemaDir            = "schema"
	modulesFile          = "kai.modules.yaml"
	ciPolicyFile         = ".kai/rules/ci-policy.yaml" // Primary location
	ciPolicyFileFallback = "kai.ci-policy.yaml"        // Legacy location for backwards compat
	workspaceFile        = "workspace"                 // stores current workspace name
)

// Version is the current kai CLI version
var Version = "0.9.4"

var rootCmd = &cobra.Command{
	Use:     "kai",
	Short:   "Kai - semantic, intent-based version control",
	Long:    `Kai is a local CLI that creates semantic snapshots from Git refs, computes changesets, classifies change types, and generates intent sentences.`,
	Version: Version,
}

// Command groups for organized help output
const (
	groupStart    = "start"
	groupDiff     = "diff"
	groupCI       = "ci"
	groupRemote   = "remote"
	groupAdvanced = "advanced"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Kai in the current directory",
	RunE:  runInit,
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Snapshot commands",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a snapshot from a Git ref or directory",
	Long: `Create a snapshot from a Git ref or directory.

IMPORTANT: You must be explicit about the source using --git or --dir.

Git Snapshot:
  kai snapshot create --git main           # Snapshot from Git branch
  kai snapshot create --git feature/login  # Snapshot from branch
  kai snapshot create --git abc123def      # Snapshot from commit hash

Directory Snapshot:
  kai snapshot create --dir .              # Snapshot current directory
  kai snapshot create --dir ./src          # Snapshot specific path

For a quick directory snapshot, use 'kai snap' instead.
For the full workflow (snapshot + analyze), use 'kai capture'.`,
	RunE: runSnapshot,
}

var snapCmd = &cobra.Command{
	Use:   "snap [path]",
	Short: "Quick directory snapshot (no Git)",
	Long: `Create a snapshot from a directory, ignoring Git entirely.

This is the recommended shortcut for the common case of snapshotting
your current working directory.

Examples:
  kai snap                # Snapshot current directory
  kai snap src/           # Snapshot specific path
  kai snap ./build        # Snapshot build output

Equivalent to 'kai snapshot create --dir <path>'.

This command:
  - Never reads Git
  - Includes uncommitted changes
  - Works without a Git repository
  - Is ideal for workspaces, CI, and local development`,
	RunE: runSnap,
}

var captureCmd = &cobra.Command{
	Use:   "capture [path]",
	Short: "Capture your project (snapshot + analyze) in one step",
	Long: `Captures your codebase in one simple command.

This is the recommended way to get started with Kai. It performs:
  1. Creates a snapshot of your project
  2. Analyzes symbols (functions, classes, variables)
  3. Builds the call graph (imports, dependencies)
  4. Updates module mappings

Examples:
  kai capture              # Capture current directory
  kai capture src/         # Capture specific path
  kai capture --explain    # Show what's happening

This is equivalent to running:
  kai snap . && kai analyze symbols @snap:last && kai analyze calls @snap:last

The capture command is the first step in the "2-minute value path":
  kai capture → kai diff → kai review open → kai ci plan`,
	RunE: runCapture,
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analysis commands",
}

var analyzeSymbolsCmd = &cobra.Command{
	Use:   "symbols <snapshot-id>",
	Short: "Extract symbols from a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE:  runAnalyzeSymbols,
}

var analyzeCallsCmd = &cobra.Command{
	Use:   "calls <snapshot-id>",
	Short: "Extract function calls and imports from a snapshot (JS/TS)",
	Long: `Analyzes JavaScript and TypeScript files to build a call graph.

Creates the following relationships:
  - File --IMPORTS--> File (import dependencies)
  - File --CALLS--> File (function call relationships)
  - File --TESTS--> File (test file to source file mapping)

This enables features like:
  - Finding all callers of a function
  - Determining which tests cover a file
  - Running only affected tests after changes`,
	Args: cobra.ExactArgs(1),
	RunE: runAnalyzeCalls,
}

var analyzeDepsCmd = &cobra.Command{
	Use:   "deps <snapshot-id>",
	Short: "Build the import/dependency graph for a snapshot (alias for 'calls')",
	Long: `Analyzes JavaScript and TypeScript files to build the import dependency graph.

This is an alias for 'kai analyze calls'. It creates the following relationships:
  - File --IMPORTS--> File (import dependencies)
  - File --CALLS--> File (function call relationships)
  - File --TESTS--> File (test file to source file mapping)

Use this to enable selective CI testing based on which files changed.`,
	Args: cobra.ExactArgs(1),
	RunE: runAnalyzeCalls,
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test-related commands",
}

var testAffectedCmd = &cobra.Command{
	Use:   "affected <base-snap> <head-snap>",
	Short: "List test files affected by changes between two snapshots",
	Long: `Analyzes the call graph to find test files that should be run based on changes.

Compares two snapshots and identifies which test files are affected by the changes.
This command requires running 'kai analyze calls' first to build the call graph.

Example:
  # Find affected tests between two snapshots
  kai test affected @snap:prev @snap:last

  # Using explicit snapshot IDs
  kai test affected abc123 def456`,
	Args: cobra.ExactArgs(2),
	RunE: runTestAffected,
}

// CI commands - runner-agnostic selective CI
var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "CI/CD commands for selective test/build execution",
	Long: `Runner-agnostic CI commands that produce deterministic plans.

Kai CI computes what targets (tests, builds, etc.) are affected by changes,
outputting a neutral JSON plan that any CI system can consume.

The CLI never runs tests or builds - it just determines what to run.`,
}

var ciPlanCmd = &cobra.Command{
	Use:   "plan [changeset|selector]",
	Short: "Compute a selection plan for affected targets",
	Long: `Analyzes changes and computes which targets should run.

Produces a deterministic JSON plan listing affected paths/globs that
any CI system can consume. The CLI is tool-neutral - it never runs
tests or builds directly.

Strategies:
  symbols   - Use symbol-level dependency graph (most precise)
  imports   - Use file-level import graph
  coverage  - Use learned test↔file mappings
  auto      - Try symbols → imports → coverage (default)

Risk policies:
  expand    - Widen selection when uncertain (safe default)
  warn      - Keep minimal plan but mark risk higher
  fail      - Exit non-zero on uncertainty

Examples:
  kai ci plan                  # Uses @cs:last by default
  kai ci plan @cs:last --out plan.json
  kai ci plan @cs:last --strategy=imports --risk-policy=expand
  kai ci plan @ws:feature/auth --out plan.json --json

Git-based CI (no .kai/ database required):
  kai ci plan --git-range main..feature --out plan.json
  kai ci plan --git-range $CI_MERGE_REQUEST_DIFF_BASE_SHA..$CI_COMMIT_SHA`,
	Args: cobra.RangeArgs(0, 1),
	RunE: runCIPlan,
}

var ciPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print a selection plan for humans or CI logs",
	Long: `Displays the contents of a plan file in a human-readable format.

Use --section to show specific parts:
  targets   - What to run/skip
  impact    - What changed
  causes    - Why each test was selected (root cause analysis)
  safety    - Safety analysis details
  summary   - Overview (default)

Examples:
  kai ci print --plan plan.json
  kai ci print --plan plan.json --section targets
  kai ci print --plan plan.json --section causes
  kai ci print --plan plan.json --json`,
	RunE: runCIPrint,
}

var ciDetectRuntimeRiskCmd = &cobra.Command{
	Use:   "detect-runtime-risk",
	Short: "Analyze test logs for runtime risk signals (tripwire)",
	Long: `Analyzes test output/logs to detect runtime signals that indicate
the selective test plan may have missed dependencies. This is the RUNTIME
SAFETY NET that catches selection misses after tests run.

Detects:
  - Cannot find module / Module not found (Node.js)
  - ImportError / ModuleNotFoundError (Python)
  - Go plugin load failures
  - TypeScript type error bursts
  - Jest/Mocha/pytest setup/fixture failures
  - importlib errors (Python dynamic imports)
  - Any dependency resolution errors

Exit Codes:
  0   - No risks detected, selection was safe
  1   - Error running the command
  75  - TRIPWIRE: Rerun full suite recommended (--tripwire mode)

Tripwire Mode (--tripwire):
  In tripwire mode, outputs only RERUN or OK and exits with code 75 or 0.
  Use this in CI to conditionally trigger a full suite rerun:

    kai ci detect-runtime-risk --stderr test.log --tripwire || npm run test:full

Examples:
  # Analyze Jest output
  kai ci detect-runtime-risk --logs ./jest-results.json

  # With plan cross-reference
  kai ci detect-runtime-risk --logs ./jest-results.json --plan plan.json

  # Tripwire mode for CI
  kai ci detect-runtime-risk --stderr ./test.log --tripwire

  # Treat any failure as tripwire
  kai ci detect-runtime-risk --stderr ./test.log --tripwire --rerun-on-fail`,
	RunE: runCIDetectRuntimeRisk,
}

var ciRecordMissCmd = &cobra.Command{
	Use:   "record-miss",
	Short: "Record a test selection miss for shadow mode learning",
	Long: `Records information about tests that failed but were not selected,
allowing Kai to learn and improve its selection accuracy over time.

Used in shadow mode to compare what was predicted vs what actually failed.
This data is used to identify missing dependency edges and improve the
test selection algorithm.

Examples:
  kai ci record-miss --plan plan.json --evidence ./test-results.json
  kai ci record-miss --plan plan.json --failed "tests/auth.test.js,tests/api.test.js"`,
	RunE: runCIRecordMiss,
}

var ciExplainDynamicImportsCmd = &cobra.Command{
	Use:   "explain-dynamic-imports [path]",
	Short: "Analyze and explain dynamic imports in a file or directory",
	Long: `Scans files for dynamic imports and shows how they would affect test selection.

This helps developers understand before committing:
- What dynamic imports exist in their code
- Whether they are bounded or unbounded
- What expansion strategy would be used
- What tests would be affected

Examples:
  kai ci explain-dynamic-imports src/
  kai ci explain-dynamic-imports src/plugins/loader.js
  kai ci explain-dynamic-imports . --json`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCIExplainDynamicImports,
}

var ciIngestCoverageCmd = &cobra.Command{
	Use:   "ingest-coverage",
	Short: "Ingest coverage reports to build file→test mappings",
	Long: `Ingests test coverage reports to build a mapping of which tests
exercise which source files. This data is used during plan generation
to select tests that recently covered changed files.

Supported formats:
  - NYC/Istanbul JSON (coverage-final.json)
  - coverage.py JSON
  - JaCoCo XML

The coverage map is stored in .kai/coverage-map.json and used during
plan generation when coverage.enabled=true in ci-policy.yaml.

Examples:
  # Ingest NYC/Istanbul coverage
  kai ci ingest-coverage --from coverage/coverage-final.json --format nyc

  # Ingest Python coverage.py
  kai ci ingest-coverage --from .coverage.json --format coveragepy

  # Ingest JaCoCo XML
  kai ci ingest-coverage --from build/reports/jacoco.xml --format jacoco

  # Tag with branch and run ID
  kai ci ingest-coverage --from coverage.json --branch main --tag nightly-2025-12-06`,
	RunE: runCIIngestCoverage,
}

var ciIngestContractsCmd = &cobra.Command{
	Use:   "ingest-contracts",
	Short: "Register contract schemas and their associated tests",
	Long: `Registers API contracts/schemas and links them to their contract tests.
When a registered schema changes, the linked tests are automatically selected.

Supported schema types:
  - OpenAPI (YAML/JSON)
  - Protobuf (.proto)
  - GraphQL (.graphql/SDL)

The contract registry is stored in .kai/contracts.json.

Examples:
  # Register an OpenAPI schema
  kai ci ingest-contracts --type openapi --path api/openapi.yaml \
    --service billing --tests "tests/contract/billing/**"

  # Register a protobuf schema
  kai ci ingest-contracts --type protobuf --path proto/user.proto \
    --service users --tests "tests/contract/users/**"

  # Register with generated file tracking
  kai ci ingest-contracts --type openapi --path api/openapi.yaml \
    --service billing --tests "tests/contract/**" \
    --generated "src/clients/billing/**"`,
	RunE: runCIIngestContracts,
}

var ciAnnotatePlanCmd = &cobra.Command{
	Use:   "annotate-plan <plan-file>",
	Short: "Annotate a plan with fallback/tripwire information",
	Long: `Updates a plan.json file with fallback status after a CI run.
Used to record when tripwire fallback was triggered for auditability.

This creates an audit trail showing why full suite was run:
- runtime_tripwire: Tests failed with import/module errors
- planner_over_threshold: Confidence too low at planning time
- panic_switch: Manual override via KAI_FORCE_FULL

Examples:
  # Record tripwire fallback
  kai ci annotate-plan plan.json \
    --fallback.used=true \
    --fallback.reason=runtime_tripwire \
    --fallback.trigger="Cannot find module" \
    --fallback.exitCode=75

  # Record panic switch
  kai ci annotate-plan plan.json \
    --fallback.used=true \
    --fallback.reason=panic_switch`,
	Args: cobra.ExactArgs(1),
	RunE: runCIAnnotatePlan,
}

var ciValidatePlanCmd = &cobra.Command{
	Use:   "validate-plan <plan-file>",
	Short: "Validate plan JSON schema and required fields",
	Long: `Validates that a plan.json file has all required fields with correct types.

Checks:
- Required fields: mode, risk, confidence, uncertainty.score, fallback.used
- Provenance fields: kaiVersion, detectorVersion, generatedAt
- Type validation for all fields

Exit codes:
  0 - Plan is valid
  1 - Plan is invalid or error reading file

Examples:
  kai ci validate-plan plan.json
  kai ci validate-plan plan.json --strict  # Also validate optional fields`,
	Args: cobra.ExactArgs(1),
	RunE: runCIValidatePlan,
}

// CI command flags
var (
	ciStrategy   string
	ciRiskPolicy string
	ciOutFile    string
	ciSafetyMode string // "shadow", "guarded", "strict"
	ciExplain    bool   // Output human-readable explanation
	ciGitRange   string // BASE..HEAD format for git-based CI plan
	ciGitRepo    string // Git repo path for --git-range
	ciPlanFile   string
	ciSection    string
	// detect-runtime-risk flags
	ciLogsFile    string
	ciStderrFile  string
	ciLogFormat   string
	ciTripwire    bool // Just output tripwire status and exit code
	ciRerunOnFail bool // Recommend rerun on any failure
	// record-miss flags
	ciEvidenceFile string
	ciFailedTests  string
	// ingest-coverage flags
	ciCoverageFrom   string
	ciCoverageFormat string
	ciCoverageBranch string
	ciCoverageTag    string
	// ingest-contracts flags
	ciContractType      string
	ciContractPath      string
	ciContractService   string
	ciContractTests     string
	ciContractGenerated string
	// annotate-plan flags
	ciFallbackUsed     bool
	ciFallbackReason   string
	ciFallbackTrigger  string
	ciFallbackExitCode int
	// validate-plan flags
	ciValidateStrict bool
)

var changesetCmd = &cobra.Command{
	Use:   "changeset",
	Short: "ChangeSet commands",
}

var changesetCreateCmd = &cobra.Command{
	Use:   "create [base-snap] [head-snap]",
	Short: "Create a changeset between two snapshots",
	Long: `Create a changeset between two snapshots.

You can specify snapshots by ID/ref, or create them on-the-fly from Git refs:

  kai changeset create snap.main snap.feature      # Using snapshot IDs/refs
  kai changeset create --git-base main --git-head feature  # From Git refs

The --git-base and --git-head flags create ephemeral snapshots from Git refs,
useful for CI pipelines where you don't have a persistent .kai database.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runChangesetCreate,
}

var intentCmd = &cobra.Command{
	Use:   "intent",
	Short: "Intent commands",
}

var intentRenderCmd = &cobra.Command{
	Use:   "render <changeset-id>",
	Short: "Render or edit the intent for a changeset",
	Args:  cobra.ExactArgs(1),
	RunE:  runIntentRender,
}

var dumpCmd = &cobra.Command{
	Use:   "dump <changeset-id>",
	Short: "Dump a changeset as JSON",
	Args:  cobra.ExactArgs(1),
	RunE:  runDump,
}

var listCmd = &cobra.Command{
	Use:        "list",
	Short:      "List resources (deprecated: use 'kai snapshot list' or 'kai changeset list')",
	Deprecated: "use 'kai snapshot list' or 'kai changeset list' instead",
}

var listSnapshotsCmd = &cobra.Command{
	Use:   "snapshots",
	Short: "List all snapshots (deprecated: use 'kai snapshot list')",
	RunE:  runListSnapshots,
}

var listChangesetsCmd = &cobra.Command{
	Use:   "changesets",
	Short: "List all changesets (deprecated: use 'kai changeset list')",
	RunE:  runListChangesets,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all snapshots",
	RunE:  runListSnapshots,
}

var changesetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all changesets",
	RunE:  runListChangesets,
}

var listSymbolsCmd = &cobra.Command{
	Use:   "symbols <snapshot-id>",
	Short: "List symbols extracted from a snapshot",
	Long: `List all symbols (functions, classes, methods, etc.) extracted from a snapshot.

Symbols are extracted by 'kai analyze symbols'. This command shows what was found,
grouped by file.

Example:
  kai list symbols @snap:last`,
	Args: cobra.ExactArgs(1),
	RunE: runListSymbols,
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show chronological log of snapshots and changesets",
	RunE:  runLog,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Kai status and pending changes",
	RunE:  runStatus,
}

var diffCmd = &cobra.Command{
	Use:   "diff [base-ref] [head-ref]",
	Short: "Show semantic differences between snapshots",
	Long: `Show semantic differences between snapshots.

With no arguments, compares the last snapshot against the working directory.
This is the recommended way to see what changed after 'kai capture'.

By default shows semantic diff (functions, classes, JSON keys changed).
Use -p/--patch for git-style line-level diff.

Examples:
  kai diff                         # Semantic diff: @snap:last vs working directory
  kai diff -p                      # Line-level diff like git
  kai diff @snap:prev @snap:last   # Compare two snapshots
  kai diff --name-only             # Just file paths`,
	Args: cobra.RangeArgs(0, 2),
	RunE: runDiff,
}

// Workspace commands
var wsCmd = &cobra.Command{
	Use:   "ws",
	Short: "Workspace (branch) commands",
	Long:  `Workspaces are lightweight, isolated, mutable overlays on top of immutable snapshots.`,
}

var wsCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new workspace",
	Long: `Create a new workspace for parallel development.

Base snapshot options (must choose one or let it default):
  --from-dir <path>    Create base from directory snapshot
  --from-git <ref>     Create base from Git commit/branch/tag
  --base <selector>    Use existing snapshot as base

If no base is specified, Kai automatically snapshots the current directory.

Examples:
  kai ws create feat/demo                    # Auto-snapshot current dir as base
  kai ws create feat/demo --from-dir .       # Explicit directory snapshot
  kai ws create feat/demo --from-git main    # From Git branch
  kai ws create feat/demo --base @snap:last  # From existing snapshot

The workspace name can be provided as a positional argument or via --name.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWsCreate,
}

var wsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE:  runWsList,
}

var wsStageCmd = &cobra.Command{
	Use:   "stage [workspace]",
	Short: "Stage changes into a workspace",
	Long: `Stage changes from the current directory into a workspace.

The workspace can be specified as:
  1. Positional argument: kai ws stage feat/demo
  2. Flag: kai ws stage --ws feat/demo
  3. Implicit (if checked out): kai ws stage

If no workspace is specified and you're checked out on a workspace,
that workspace is used automatically.

Examples:
  kai ws stage feat/demo     # Stage into feat/demo
  kai ws stage               # Stage into current workspace (if checked out)
  kai ws stage --ws feat/demo --dir src/  # Stage specific directory`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWsStage,
}

var wsLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show workspace changelog",
	RunE:  runWsLog,
}

var wsShelveCmd = &cobra.Command{
	Use:   "shelve",
	Short: "Shelve a workspace (freeze staging)",
	RunE:  runWsShelve,
}

var wsUnshelveCmd = &cobra.Command{
	Use:   "unshelve",
	Short: "Unshelve a workspace (resume staging)",
	RunE:  runWsUnshelve,
}

var wsCloseCmd = &cobra.Command{
	Use:   "close",
	Short: "Close a workspace (permanent)",
	RunE:  runWsClose,
}

var wsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a workspace (metadata and refs; run `kai prune` to reclaim storage)",
	Long: `Delete a workspace permanently, removing the workspace node, edges, and refs.

Content (snapshots, changesets, files) is NOT deleted - that's the GC's job.
Run 'kai prune' after deleting workspaces to reclaim storage.

Examples:
  kai ws delete --ws feature/experiment --dry-run  # Preview what would be deleted
  kai ws delete --ws feature/experiment            # Actually delete
  kai ws delete --ws old-branch --keep-refs        # Keep refs (rare)`,
	RunE: runWsDelete,
}

var wsCheckoutCmd = &cobra.Command{
	Use:   "checkout [workspace]",
	Short: "Checkout workspace and set as current",
	Long: `Checkout a workspace's head snapshot and set it as the current workspace.

This writes files from the workspace's current state to a directory and
sets .kai/workspace so subsequent commands use this workspace by default.

Examples:
  kai ws checkout feat/demo              # Checkout and set as current
  kai ws checkout feat/demo --dir ./src  # Checkout to specific directory
  kai ws checkout feat/demo --clean      # Remove files not in snapshot`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWsCheckout,
}

var wsCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show current workspace",
	Long: `Show the currently checked-out workspace.

This reads from .kai/workspace which is set by 'kai ws checkout'.`,
	RunE: runWsCurrent,
}

var integrateCmd = &cobra.Command{
	Use:   "integrate",
	Short: "Integrate workspace changes into a target snapshot",
	RunE:  runIntegrate,
}

var mergeCmd = &cobra.Command{
	Use:   "merge <base-file> <left-file> <right-file>",
	Short: "Perform AST-aware 3-way merge",
	Long: `Perform an AST-aware 3-way merge at symbol granularity.

Unlike line-based merge, this understands code structure and can:
- Auto-merge changes to different functions in the same file
- Detect API signature conflicts when both sides change function params
- Classify conflicts semantically (DELETE_vs_MODIFY, CONCURRENT_CREATE, etc.)

Examples:
  kai merge base.js left.js right.js --lang js
  kai merge base.py branch1.py branch2.py --lang py --output merged.py`,
	Args: cobra.ExactArgs(3),
	RunE: runMerge,
}

var checkoutCmd = &cobra.Command{
	Use:   "checkout <snapshot-id>",
	Short: "Restore filesystem to match a snapshot",
	Long: `Restore the filesystem to match a snapshot's state.

This writes all files from the snapshot to the target directory.
Use --clean to also delete files not in the snapshot.

Examples:
  kai checkout abc123... --dir ./src
  kai checkout abc123... --dir ./src --clean`,
	Args: cobra.ExactArgs(1),
	RunE: runCheckout,
}

// Reference commands
var refCmd = &cobra.Command{
	Use:   "ref",
	Short: "Manage named references",
	Long: `Create and manage named references (aliases) for snapshots and changesets.

References allow you to use human-readable names instead of 64-character hex IDs.

Examples:
  kai ref set snap.main @snap:last
  kai ref set cs.login_fix 90cd7264
  kai ref list
  kai ref del cs.login_fix`,
}

var refListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all refs",
	RunE:  runRefList,
}

var refSetCmd = &cobra.Command{
	Use:   "set <name> <id|short|selector>",
	Short: "Create or update a ref",
	Long: `Create or update a named reference pointing to a node.

The target can be:
  - A full 64-char hex ID
  - A short hex prefix (8+ chars)
  - Another ref name
  - A selector (@snap:last, @cs:prev, etc.)

Examples:
  kai ref set snap.main d9ec9902
  kai ref set cs.bugfix @cs:last
  kai ref set snap.release @snap:prev`,
	Args: cobra.ExactArgs(2),
	RunE: runRefSet,
}

var refDelCmd = &cobra.Command{
	Use:   "del <name>",
	Short: "Delete a ref",
	Args:  cobra.ExactArgs(1),
	RunE:  runRefDel,
}

// Modules commands
var modulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "Manage module definitions",
	Long: `Define and manage modules for your codebase.

Modules group related files together, enabling:
- Semantic diffs at module level
- Targeted test selection based on module changes
- Import graph analysis between modules

Examples:
  kai modules init --infer --write   # Auto-detect and save modules
  kai modules add App src/app.js     # Add a module
  kai modules list                   # Show all modules
  kai modules preview                # Preview file-to-module mapping`,
}

var modulesInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize module configuration",
	Long: `Initialize module configuration by auto-detecting modules from your codebase.

With --infer, Kai scans your source directories and creates sensible module definitions.
With --write, the configuration is saved to .kai/rules/modules.yaml.

Examples:
  kai modules init --infer                    # Preview inferred modules
  kai modules init --infer --write            # Save inferred modules
  kai modules init --infer --by dirs          # Group by top-level directories
  kai modules init --infer --tests "tests/**" # Also detect test modules`,
	RunE: runModulesInit,
}

var modulesAddCmd = &cobra.Command{
	Use:   "add <name> <glob> [glob...]",
	Short: "Add or update a module",
	Long: `Add a new module or update an existing module's patterns.

Examples:
  kai modules add App src/app.js
  kai modules add Utils "src/utils/**"
  kai modules add Auth "src/auth/**" "src/middleware/auth*"`,
	Args: cobra.MinimumNArgs(2),
	RunE: runModulesAdd,
}

var modulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all modules",
	RunE:  runModulesList,
}

var modulesPreviewCmd = &cobra.Command{
	Use:   "preview [module]",
	Short: "Preview which files match each module",
	Long: `Show which files are matched by module patterns.

Without arguments, shows all modules and their matched files.
With a module name, shows only that module's matches.

Examples:
  kai modules preview
  kai modules preview Utils`,
	Args: cobra.MaximumNArgs(1),
	RunE: runModulesPreview,
}

var modulesShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show a module's configuration",
	Args:  cobra.ExactArgs(1),
	RunE:  runModulesShow,
}

var modulesRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a module",
	Args:  cobra.ExactArgs(1),
	RunE:  runModulesRm,
}

var pickCmd = &cobra.Command{
	Use:   "pick <Snapshot|ChangeSet|Workspace>",
	Short: "Search and select a node interactively",
	Long: `Search for nodes and display matches for selection.

Use --filter to search by substring in ID, slug, or payload.
Use --no-ui to output matches without interactive selection.

Examples:
  kai pick Snapshot --filter auth
  kai pick ChangeSet --no-ui`,
	Args: cobra.ExactArgs(1),
	RunE: runPick,
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for kai.

To load completions:

Bash:
  $ source <(kai completion bash)
  # To load completions for each session, add to your ~/.bashrc:
  # source <(kai completion bash)

Zsh:
  $ source <(kai completion zsh)
  # To load completions for each session, add to your ~/.zshrc:
  # source <(kai completion zsh)

Fish:
  $ kai completion fish | source
  # To load completions for each session:
  # kai completion fish > ~/.config/fish/completions/kai.fish

PowerShell:
  PS> kai completion powershell | Out-String | Invoke-Expression
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE:                  runCompletion,
}

// Remote commands
var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage remote servers",
	Long: `Configure remote Kailab servers for pushing and fetching.

Examples:
  kai remote set origin http://localhost:7447
  kai remote get origin
  kai remote list`,
}

var remoteSetCmd = &cobra.Command{
	Use:   "set <name> <url>",
	Short: "Set a remote URL",
	Long: `Set a remote Kailab server URL with optional tenant and repo.

Examples:
  kai remote set origin http://localhost:7447
  kai remote set origin http://localhost:7447 --tenant myorg --repo main`,
	Args:  cobra.ExactArgs(2),
	RunE:  runRemoteSet,
}

var remoteGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get a remote URL",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteGet,
}

var remoteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all remotes",
	RunE:  runRemoteList,
}

var remoteDelCmd = &cobra.Command{
	Use:   "del <name>",
	Short: "Delete a remote",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteDel,
}

var pushCmd = &cobra.Command{
	Use:   "push [remote] [target...]",
	Short: "Push workspaces, changesets, reviews, or snapshots to a remote server",
	Long: `Push workspaces, changesets, reviews, or snapshots to a remote Kailab server.

In Kai, you primarily push workspaces (the unit of collaboration). Changesets
within the workspace are the meaningful units that collaborators review.
Reviews can be pushed to share code review state with collaborators.
Snapshots travel automatically as infrastructure.

Targets can use prefixes:
  cs:<ref>      Push a changeset (+ its base/head snapshots)
  review:<id>   Push a review (+ its target changeset)
  snap:<ref>    Push a snapshot (advanced/plumbing)
  <ref>         Legacy: push a ref directly

Examples:
  kai push                         # Push current workspace to origin
  kai push origin --ws feature/auth # Push specific workspace
  kai push origin cs:login_fix     # Push single changeset for review
  kai push origin review:abc123    # Push a code review
  kai push origin snap:abc123      # Push snapshot (rarely needed)
  kai push --all                   # Push all refs (legacy)`,
	RunE: runPush,
}

var fetchCmd = &cobra.Command{
	Use:   "fetch [remote] [refs...]",
	Short: "Fetch refs and objects from a remote server",
	Long: `Fetch refs and objects from a remote Kailab server.

By default, fetches from the 'origin' remote.

Use --ws to fetch a specific workspace and recreate it locally.
Use --review to fetch a specific review and recreate it locally.

Examples:
  kai fetch                       # Fetch all refs from origin
  kai fetch origin                # Fetch all refs
  kai fetch origin snap.main      # Fetch specific ref
  kai fetch --ws feature/auth     # Fetch and recreate workspace
  kai fetch --review abc123       # Fetch and recreate review`,
	RunE: runFetch,
}

var cloneCmd = &cobra.Command{
	Use:   "clone <org/repo | url> [directory]",
	Short: "Clone a repository from a remote server",
	Long: `Clone a Kai repository from a remote Kailab server.

Creates a new directory, initializes Kai, sets up the remote, and fetches all refs.

URL formats:
  org/repo                         Shorthand (uses default server: kaiscm.com)
  http://server/tenant/repo        Full URL with server

The default server can be overridden with the KAI_SERVER environment variable.

Examples:
  kai clone 1m/myrepo                                   # Clone from kaiscm.com
  kai clone 1m/myrepo myproject                         # Clone into 'myproject' directory
  kai clone https://kaiscm.com/myorg/myrepo             # Full URL
  kai clone http://localhost:8080/myorg/myrepo          # Local development
  kai clone http://localhost:8080 --tenant myorg --repo myrepo`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runClone,
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Garbage-collect unreachable snapshots/changesets/files",
	Long: `Garbage-collect unreachable content using mark-and-sweep.

Roots (kept):
  - All ref targets
  - All workspace nodes (and their base/head/changesets)

Everything not reachable from roots is swept.

Examples:
  kai prune --dry-run           # Preview what would be deleted
  kai prune                     # Actually delete unreachable content
  kai prune --since 7           # Only delete content older than 7 days
  kai prune --aggressive        # Also sweep orphaned Symbols/Modules`,
	RunE: runPrune,
}

var remoteLogCmd = &cobra.Command{
	Use:   "remote-log [remote]",
	Short: "Show remote ref history log",
	Long: `Display the append-only ref history from a remote Kailab server.

Examples:
  kai remote-log                  # Show log from origin
  kai remote-log origin -n 20    # Show 20 entries
  kai remote-log --ref snap.main # Filter by ref`,
	RunE: runRemoteLog,
}

// Auth commands
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
	Long: `Manage authentication with Kailab servers.

Examples:
  kai auth login                  # Interactive login
  kai auth logout                 # Clear credentials
  kai auth status                 # Show auth status`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login [server-url]",
	Short: "Login to a Kailab server",
	Long: `Authenticate with a Kailab control plane server.

If no server URL is provided, uses the origin remote's URL.

Examples:
  kai auth login                              # Login using origin remote
  kai auth login http://localhost:8080        # Login to specific server`,
	RunE: runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout and clear credentials",
	RunE:  runAuthLogout,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE:  runAuthStatus,
}

// Review commands
var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Manage code reviews for changesets",
	Long: `Create and manage code reviews for changesets or workspaces.

Reviews are anchored to semantic entities (changesets, symbols) not lines.

Examples:
  kai review open @cs:last --title "Add auth"    # Open a review
  kai review list                                 # List all reviews
  kai review view <id>                            # View a review
  kai review approve <id>                         # Approve a review
  kai review close <id> --state merged            # Close as merged`,
}

var reviewOpenCmd = &cobra.Command{
	Use:   "open [changeset|workspace]",
	Short: "Open a new review",
	Long: `Open a new code review for a changeset or workspace.

With no arguments, automatically creates a changeset from your last two snapshots
(@snap:prev → @snap:last) and opens a review for it.

The title is auto-generated from semantic analysis if not provided (like git commit).

Examples:
  kai review open                                      # Auto-title from changes
  kai review open -m "Fix login bug"                   # Explicit title
  kai review open @cs:last --title "Reduce timeout"    # Explicit changeset`,
	Args: cobra.RangeArgs(0, 1),
	RunE: runReviewOpen,
}

var reviewListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all reviews",
	RunE:  runReviewList,
}

var reviewViewCmd = &cobra.Command{
	Use:   "view <review-id>",
	Short: "View a review",
	Long: `View details of a review including its changeset, status, and comments.

Examples:
  kai review view abc123
  kai review view abc123 --json`,
	Args: cobra.ExactArgs(1),
	RunE: runReviewView,
}

var reviewStatusCmd = &cobra.Command{
	Use:   "status <review-id>",
	Short: "Show review status",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewStatus,
}

var reviewApproveCmd = &cobra.Command{
	Use:   "approve <review-id>",
	Short: "Approve a review",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewApprove,
}

var reviewRequestChangesCmd = &cobra.Command{
	Use:   "request-changes <review-id>",
	Short: "Request changes on a review",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewRequestChanges,
}

var reviewCloseCmd = &cobra.Command{
	Use:   "close <review-id>",
	Short: "Close a review",
	Long: `Close a review with a final state.

Examples:
  kai review close abc123 --state merged
  kai review close abc123 --state abandoned`,
	Args: cobra.ExactArgs(1),
	RunE: runReviewClose,
}

var reviewReadyCmd = &cobra.Command{
	Use:   "ready <review-id>",
	Short: "Mark a draft review as ready for review",
	Args:  cobra.ExactArgs(1),
	RunE:  runReviewReady,
}

var reviewExportCmd = &cobra.Command{
	Use:   "export <review-id>",
	Short: "Export review as markdown or HTML",
	Long: `Export a review summary for posting to GitHub/GitLab PRs.

Examples:
  kai review export abc123 --markdown > review.md
  kai review export abc123 --html > review.html`,
	Args: cobra.ExactArgs(1),
	RunE: runReviewExport,
}

var (
	// Workspace flags
	wsName           string
	wsBase           string
	wsFromDir        string
	wsFromGit        string
	wsDescription    string
	wsDir            string
	wsTarget         string
	wsDeleteKeepRefs bool
	wsDeleteDryRun   bool
	wsCheckoutClean  bool
	pruneDryRun      bool
	pruneSinceDays   int
	pruneAggressive  bool
	pruneYes         bool
	pruneKeep        []string

	// Review flags
	reviewTitle       string
	reviewDesc        string
	reviewReviewers   []string
	reviewCloseState  string
	reviewExportMD    bool
	reviewExportHTML bool
	reviewJSON       bool
	reviewViewMode   string
	reviewExplain    bool
	reviewBase       string

	statusDir      string
	statusAgainst  string
	statusNameOnly bool
	statusJSON     bool
	statusSemantic bool
	statusExplain  bool
	logLimit       int
	repoPath      string
	dirPath       string
	editText       string
	regenerateIntent bool
	jsonFlag      bool
	checkoutDir   string
	checkoutClean bool

	// Ref/pick flags
	refKindFilter string
	pickFilter    string
	pickNoUI      bool

	// Changeset flags
	changesetMessage  string
	changesetGitBase  string // git ref for base snapshot
	changesetGitHead  string // git ref for head snapshot
	changesetGitRepo  string // git repo path
	wsStageMessage    string

	// Diff flags
	diffDir      string
	diffNameOnly bool
	diffSemantic bool
	diffJSON     bool
	diffExplain  bool
	diffPatch    bool // git-style line-level diff
	diffForce    bool // skip stale baseline warning

	// Snapshot flags
	snapshotMessage string
	snapshotGitRef  string // explicit git ref for disambiguation

	// Capture flags
	captureExplain bool

	// Global explain flag
	explainFlag bool

	// Push/fetch flags
	pushForce     bool
	pushAll       bool
	pushWorkspace string
	pushDryRun    bool
	pushExplain   bool
	remoteLogRef  string
	remoteLogLimit int

	// Remote set flags
	remoteTenant  string
	remoteRepo    string

	// Clone flags
	cloneTenant string
	cloneRepo   string

	// Fetch flags
	fetchWorkspace string
	fetchReview    string
	fetchExplain   bool

	// Merge flags
	mergeLang   string
	mergeOutput string
	mergeJSON   bool

	// Modules flags
	modulesInfer     bool
	modulesWrite     bool
	modulesBy        string
	modulesTestsGlob string
	modulesDryRun    bool
)

func init() {
	snapshotCreateCmd.Flags().StringVar(&repoPath, "repo", ".", "Path to the Git repository")
	snapshotCreateCmd.Flags().StringVar(&dirPath, "dir", "", "Path to directory (creates snapshot without Git)")
	snapshotCreateCmd.Flags().StringVar(&snapshotGitRef, "git", "", "Git ref to snapshot (explicit mode)")
	snapshotCreateCmd.Flags().StringVarP(&snapshotMessage, "message", "m", "", "Description for this snapshot")
	snapshotCreateCmd.Flags().BoolVar(&explainFlag, "explain", false, "Show detailed explanation of what this command does")

	// Capture command flags
	captureCmd.Flags().BoolVar(&captureExplain, "explain", false, "Show detailed explanation of what this command does")
	intentRenderCmd.Flags().StringVar(&editText, "edit", "", "Set the intent text directly")
	intentRenderCmd.Flags().BoolVar(&regenerateIntent, "regenerate", false, "Force regenerate intent (ignore saved)")
	dumpCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	logCmd.Flags().IntVarP(&logLimit, "limit", "n", 10, "Number of entries to show")
	statusCmd.Flags().StringVar(&statusDir, "dir", ".", "Directory to check for changes")
	statusCmd.Flags().StringVar(&statusAgainst, "against", "", "Baseline ref/selector to compare against (default: @snap:last)")
	statusCmd.Flags().BoolVar(&statusNameOnly, "name-only", false, "Output just paths with status prefixes (A/M/D)")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusSemantic, "semantic", false, "Include semantic change type analysis for modified files")
	statusCmd.Flags().BoolVar(&statusExplain, "explain", false, "Show detailed explanation of what this command does")

	// Changeset command flags
	changesetCreateCmd.Flags().StringVarP(&changesetMessage, "message", "m", "", "Changeset message describing the intent")
	changesetCreateCmd.Flags().StringVar(&changesetGitBase, "git-base", "", "Git ref for base snapshot (instead of snapshot ID)")
	changesetCreateCmd.Flags().StringVar(&changesetGitHead, "git-head", "", "Git ref for head snapshot (instead of snapshot ID)")
	changesetCreateCmd.Flags().StringVar(&changesetGitRepo, "repo", ".", "Path to Git repository (used with --git-base/--git-head)")

	// Diff command flags
	diffCmd.Flags().StringVar(&diffDir, "dir", ".", "Directory to compare against (when comparing snapshot vs working dir)")
	diffCmd.Flags().BoolVar(&diffNameOnly, "name-only", false, "Output just paths with status prefixes (A/M/D)")
	diffCmd.Flags().BoolVar(&diffSemantic, "semantic", false, "Show semantic diff (default, use --name-only to disable)")
	diffCmd.Flags().BoolVar(&diffJSON, "json", false, "Output diff as JSON (implies --semantic)")
	diffCmd.Flags().BoolVar(&diffExplain, "explain", false, "Show detailed explanation of what this command does")
	diffCmd.Flags().BoolVarP(&diffPatch, "patch", "p", false, "Show line-level diff (like git diff)")
	diffCmd.Flags().BoolVar(&diffForce, "force", false, "Skip stale baseline warning")

	// Workspace command flags
	wsCreateCmd.Flags().StringVar(&wsName, "name", "", "Workspace name (or pass as positional arg)")
	wsCreateCmd.Flags().StringVar(&wsBase, "base", "", "Base snapshot selector (e.g., @snap:last)")
	wsCreateCmd.Flags().StringVar(&wsFromDir, "from-dir", "", "Create base from directory snapshot")
	wsCreateCmd.Flags().StringVar(&wsFromGit, "from-git", "", "Create base from Git commit/branch/tag")
	wsCreateCmd.Flags().StringVar(&wsDescription, "desc", "", "Workspace description")

	wsStageCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name (or pass as positional arg)")
	wsStageCmd.Flags().StringVar(&wsDir, "dir", ".", "Directory to stage from")
	wsStageCmd.Flags().StringVarP(&wsStageMessage, "message", "m", "", "Message describing the staged changes")

	wsLogCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsLogCmd.MarkFlagRequired("ws")

	wsShelveCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsShelveCmd.MarkFlagRequired("ws")

	wsUnshelveCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsUnshelveCmd.MarkFlagRequired("ws")

	wsCloseCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsCloseCmd.MarkFlagRequired("ws")

	wsDeleteCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsDeleteCmd.Flags().BoolVar(&wsDeleteKeepRefs, "keep-refs", false, "Preserve workspace refs (rare)")
	wsDeleteCmd.Flags().BoolVar(&wsDeleteDryRun, "dry-run", false, "Show what would be deleted without actually deleting")
	wsDeleteCmd.MarkFlagRequired("ws")

	wsCheckoutCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name (or pass as positional arg)")
	wsCheckoutCmd.Flags().StringVar(&wsDir, "dir", ".", "Target directory to write files to")
	wsCheckoutCmd.Flags().BoolVar(&wsCheckoutClean, "clean", false, "Delete files not in snapshot")

	integrateCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	integrateCmd.Flags().StringVar(&wsTarget, "into", "", "Target snapshot ID (required)")
	integrateCmd.MarkFlagRequired("ws")
	integrateCmd.MarkFlagRequired("into")

	// Checkout command flags
	checkoutCmd.Flags().StringVar(&checkoutDir, "dir", ".", "Target directory to write files to")
	checkoutCmd.Flags().BoolVar(&checkoutClean, "clean", false, "Delete files not in snapshot")

	// Ref command flags
	refListCmd.Flags().StringVar(&refKindFilter, "kind", "", "Filter by kind (Snapshot, ChangeSet, Workspace)")

	// Pick command flags
	pickCmd.Flags().StringVar(&pickFilter, "filter", "", "Filter by substring")
	pickCmd.Flags().BoolVar(&pickNoUI, "no-ui", false, "Output matches without interactive selection")

	// Push/fetch command flags
	pushCmd.Flags().BoolVarP(&pushForce, "force", "f", false, "Force push (allow non-fast-forward)")
	pushCmd.Flags().BoolVar(&pushAll, "all", false, "Push all refs (legacy)")
	pushCmd.Flags().StringVar(&pushWorkspace, "ws", "", "Workspace to push")
	pushCmd.Flags().BoolVar(&pushDryRun, "dry-run", false, "Show what would be transferred without pushing")
	pushCmd.Flags().BoolVar(&pushExplain, "explain", false, "Show detailed explanation of what this command does")
	remoteLogCmd.Flags().StringVar(&remoteLogRef, "ref", "", "Filter by ref name")
	remoteLogCmd.Flags().IntVarP(&remoteLogLimit, "limit", "n", 20, "Number of entries to show")

	// Remote set flags
	remoteSetCmd.Flags().StringVar(&remoteTenant, "tenant", "default", "Tenant/org name for the remote")
	remoteSetCmd.Flags().StringVar(&remoteRepo, "repo", "main", "Repository name for the remote")

	// Clone flags
	cloneCmd.Flags().StringVar(&cloneTenant, "tenant", "", "Tenant/org name (extracted from URL if not specified)")
	cloneCmd.Flags().StringVar(&cloneRepo, "repo", "", "Repository name (extracted from URL if not specified)")

	// Fetch flags
	fetchCmd.Flags().StringVar(&fetchWorkspace, "ws", "", "Fetch a specific workspace by name and recreate it locally")
	fetchCmd.Flags().StringVar(&fetchReview, "review", "", "Fetch a specific review by ID and recreate it locally")
	fetchCmd.Flags().BoolVar(&fetchExplain, "explain", false, "Show detailed explanation of what this command does")

	// Prune flags
	pruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "Show what would be deleted without actually deleting (default behavior)")
	pruneCmd.Flags().IntVar(&pruneSinceDays, "since", 0, "Only delete content older than N days (0 = no limit)")
	pruneCmd.Flags().BoolVar(&pruneAggressive, "aggressive", false, "Also sweep orphaned Symbols and Modules")
	pruneCmd.Flags().BoolVar(&pruneYes, "yes", false, "Actually perform the deletion (required for non-dry-run)")
	pruneCmd.Flags().StringArrayVar(&pruneKeep, "keep", nil, "Glob patterns for paths to keep (can be repeated)")

	// Review flags
	reviewOpenCmd.Flags().StringVarP(&reviewTitle, "title", "m", "", "Review title (auto-generated from changes if not provided)")
	reviewOpenCmd.Flags().StringVar(&reviewDesc, "desc", "", "Review description")
	reviewOpenCmd.Flags().StringVar(&reviewBase, "base", "", "Base ref for changeset (default: @snap:prev)")
	reviewOpenCmd.Flags().StringArrayVar(&reviewReviewers, "reviewers", nil, "Reviewers (can be specified multiple times)")
	reviewOpenCmd.Flags().BoolVar(&reviewExplain, "explain", false, "Show detailed explanation of what this command does")

	reviewViewCmd.Flags().BoolVar(&reviewJSON, "json", false, "Output as JSON")
	reviewViewCmd.Flags().StringVar(&reviewViewMode, "view", "semantic", "View mode: semantic, text, or mixed")

	reviewCloseCmd.Flags().StringVar(&reviewCloseState, "state", "", "Close state: merged or abandoned (required)")
	reviewCloseCmd.MarkFlagRequired("state")

	reviewExportCmd.Flags().BoolVar(&reviewExportMD, "markdown", false, "Export as markdown")
	reviewExportCmd.Flags().BoolVar(&reviewExportHTML, "html", false, "Export as HTML")

	// Merge flags
	mergeCmd.Flags().StringVar(&mergeLang, "lang", "", "Language (js, ts, py) - auto-detected from extension if not specified")
	mergeCmd.Flags().StringVarP(&mergeOutput, "output", "o", "", "Output file path (defaults to stdout)")
	mergeCmd.Flags().BoolVar(&mergeJSON, "json", false, "Output result as JSON (includes conflicts)")

	// Modules init flags
	modulesInitCmd.Flags().BoolVar(&modulesInfer, "infer", false, "Auto-detect modules from source structure")
	modulesInitCmd.Flags().BoolVar(&modulesWrite, "write", false, "Write configuration to .kai/rules/modules.yaml")
	modulesInitCmd.Flags().StringVar(&modulesBy, "by", "dirs", "Grouping strategy: dirs (directories) or globs")
	modulesInitCmd.Flags().StringVar(&modulesTestsGlob, "tests", "", "Glob pattern for test files (e.g., \"tests/**\")")
	modulesInitCmd.Flags().BoolVar(&modulesDryRun, "dry-run", false, "Preview changes without writing")

	// Add remote subcommands
	remoteCmd.AddCommand(remoteSetCmd)
	remoteCmd.AddCommand(remoteGetCmd)
	remoteCmd.AddCommand(remoteListCmd)
	remoteCmd.AddCommand(remoteDelCmd)

	// Add ref subcommands
	refCmd.AddCommand(refListCmd)
	refCmd.AddCommand(refSetCmd)
	refCmd.AddCommand(refDelCmd)

	// Add modules subcommands
	modulesCmd.AddCommand(modulesInitCmd)
	modulesCmd.AddCommand(modulesAddCmd)
	modulesCmd.AddCommand(modulesListCmd)
	modulesCmd.AddCommand(modulesPreviewCmd)
	modulesCmd.AddCommand(modulesShowCmd)
	modulesCmd.AddCommand(modulesRmCmd)

	// Set up dynamic completions for commands that accept IDs
	analyzeSymbolsCmd.ValidArgsFunction = completeSnapshotID
	analyzeCallsCmd.ValidArgsFunction = completeSnapshotID
	listSymbolsCmd.ValidArgsFunction = completeSnapshotID
	changesetCreateCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Both args are snapshot IDs
		return completeSnapshotID(cmd, args, toComplete)
	}
	intentRenderCmd.ValidArgsFunction = completeChangeSetID
	dumpCmd.ValidArgsFunction = completeChangeSetID
	checkoutCmd.ValidArgsFunction = completeSnapshotID
	refDelCmd.ValidArgsFunction = completeRefName

	// Add workspace subcommands
	wsCmd.AddCommand(wsCreateCmd)
	wsCmd.AddCommand(wsListCmd)
	wsCmd.AddCommand(wsStageCmd)
	wsCmd.AddCommand(wsLogCmd)
	wsCmd.AddCommand(wsShelveCmd)
	wsCmd.AddCommand(wsUnshelveCmd)
	wsCmd.AddCommand(wsCloseCmd)
	wsCmd.AddCommand(wsDeleteCmd)
	wsCmd.AddCommand(wsCheckoutCmd)
	wsCmd.AddCommand(wsCurrentCmd)

	analyzeCmd.AddCommand(analyzeSymbolsCmd)
	analyzeCmd.AddCommand(analyzeCallsCmd)
	analyzeCmd.AddCommand(analyzeDepsCmd)

	// Snapshot subcommands
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)

	// Changeset subcommands
	changesetCmd.AddCommand(changesetCreateCmd)
	changesetCmd.AddCommand(changesetListCmd)

	intentCmd.AddCommand(intentRenderCmd)

	// Deprecated list commands (kept for backwards compatibility)
	listCmd.AddCommand(listSnapshotsCmd)
	listCmd.AddCommand(listChangesetsCmd)
	listCmd.AddCommand(listSymbolsCmd)

	testCmd.AddCommand(testAffectedCmd)

	// CI commands
	ciCmd.AddCommand(ciPlanCmd)
	ciCmd.AddCommand(ciPrintCmd)
	ciCmd.AddCommand(ciDetectRuntimeRiskCmd)
	ciCmd.AddCommand(ciRecordMissCmd)
	ciCmd.AddCommand(ciExplainDynamicImportsCmd)
	ciCmd.AddCommand(ciIngestCoverageCmd)
	ciCmd.AddCommand(ciIngestContractsCmd)
	ciCmd.AddCommand(ciAnnotatePlanCmd)
	ciCmd.AddCommand(ciValidatePlanCmd)
	ciValidatePlanCmd.Flags().BoolVar(&ciValidateStrict, "strict", false, "Validate optional fields as well")
	ciPlanCmd.Flags().StringVar(&ciStrategy, "strategy", "auto", "Selection strategy: auto, symbols, imports, coverage")
	ciPlanCmd.Flags().StringVar(&ciRiskPolicy, "risk-policy", "expand", "Risk policy: expand, warn, fail")
	ciPlanCmd.Flags().StringVar(&ciOutFile, "out", "", "Output file for plan JSON")
	ciPlanCmd.Flags().StringVar(&ciSafetyMode, "safety-mode", "guarded", "Safety mode: shadow (learn-only), guarded (safe fallback), strict (no fallback)")
	ciPlanCmd.Flags().BoolVar(&ciExplain, "explain", false, "Output human-readable explanation table instead of JSON")
	ciPlanCmd.Flags().StringVar(&ciGitRange, "git-range", "", "Git range BASE..HEAD to create changeset from (e.g., main..feature)")
	ciPlanCmd.Flags().StringVar(&ciGitRepo, "repo", ".", "Path to Git repository (used with --git-range)")
	ciPrintCmd.Flags().StringVar(&ciPlanFile, "plan", "plan.json", "Path to plan file")
	ciPrintCmd.Flags().StringVar(&ciSection, "section", "summary", "Section to display: targets, impact, summary")
	// detect-runtime-risk flags
	ciDetectRuntimeRiskCmd.Flags().StringVar(&ciLogsFile, "logs", "", "Path to test output JSON (Jest, Mocha, pytest, etc.)")
	ciDetectRuntimeRiskCmd.Flags().StringVar(&ciStderrFile, "stderr", "", "Path to stderr/text log file")
	ciDetectRuntimeRiskCmd.Flags().StringVar(&ciLogFormat, "format", "auto", "Log format: auto, jest, mocha, pytest, go, text")
	ciDetectRuntimeRiskCmd.Flags().StringVar(&ciPlanFile, "plan", "", "Path to plan file (for cross-reference)")
	ciDetectRuntimeRiskCmd.Flags().BoolVar(&ciTripwire, "tripwire", false, "Tripwire mode: exit 75 if rerun needed, 0 otherwise")
	ciDetectRuntimeRiskCmd.Flags().BoolVar(&ciRerunOnFail, "rerun-on-fail", false, "Treat any test failure as a tripwire trigger")
	// record-miss flags
	ciRecordMissCmd.Flags().StringVar(&ciPlanFile, "plan", "", "Path to plan file (required)")
	ciRecordMissCmd.Flags().StringVar(&ciEvidenceFile, "evidence", "", "Path to test results JSON")
	ciRecordMissCmd.Flags().StringVar(&ciFailedTests, "failed", "", "Comma-separated list of failed test files")
	// ingest-coverage flags
	ciIngestCoverageCmd.Flags().StringVar(&ciCoverageFrom, "from", "", "Path to coverage report file(s)")
	ciIngestCoverageCmd.Flags().StringVar(&ciCoverageFormat, "format", "auto", "Coverage format: auto, nyc, coveragepy, jacoco")
	ciIngestCoverageCmd.Flags().StringVar(&ciCoverageBranch, "branch", "", "Branch name for tagging")
	ciIngestCoverageCmd.Flags().StringVar(&ciCoverageTag, "tag", "", "Tag/identifier for this coverage run")
	ciIngestCoverageCmd.MarkFlagRequired("from")
	// ingest-contracts flags
	ciIngestContractsCmd.Flags().StringVar(&ciContractType, "type", "", "Contract type: openapi, protobuf, graphql")
	ciIngestContractsCmd.Flags().StringVar(&ciContractPath, "path", "", "Path to schema file")
	ciIngestContractsCmd.Flags().StringVar(&ciContractService, "service", "", "Service/module name")
	ciIngestContractsCmd.Flags().StringVar(&ciContractTests, "tests", "", "Glob pattern for contract tests")
	ciIngestContractsCmd.Flags().StringVar(&ciContractGenerated, "generated", "", "Glob pattern for generated files")
	ciIngestContractsCmd.MarkFlagRequired("type")
	ciIngestContractsCmd.MarkFlagRequired("path")
	ciIngestContractsCmd.MarkFlagRequired("tests")
	// annotate-plan flags
	ciAnnotatePlanCmd.Flags().BoolVar(&ciFallbackUsed, "fallback.used", false, "Whether fallback was triggered")
	ciAnnotatePlanCmd.Flags().StringVar(&ciFallbackReason, "fallback.reason", "", "Reason: runtime_tripwire, planner_over_threshold, panic_switch")
	ciAnnotatePlanCmd.Flags().StringVar(&ciFallbackTrigger, "fallback.trigger", "", "What triggered fallback (e.g., 'Cannot find module')")
	ciAnnotatePlanCmd.Flags().IntVar(&ciFallbackExitCode, "fallback.exitCode", 0, "Exit code that triggered fallback")

	// Define command groups for organized help output
	// Note: Workspaces are intentionally in Advanced to reduce cognitive load for new users
	rootCmd.AddGroup(
		&cobra.Group{ID: groupStart, Title: "Getting Started:"},
		&cobra.Group{ID: groupDiff, Title: "Diff & Review:"},
		&cobra.Group{ID: groupCI, Title: "CI & Testing:"},
		&cobra.Group{ID: groupRemote, Title: "Remote & Sync:"},
		&cobra.Group{ID: groupAdvanced, Title: "Advanced:"},
	)

	// Getting Started
	initCmd.GroupID = groupStart
	captureCmd.GroupID = groupStart
	initCmd.Flags().BoolVar(&initExplain, "explain", false, "Show detailed explanation of what this command does")
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(captureCmd)

	// Diff & Review
	diffCmd.GroupID = groupDiff
	statusCmd.GroupID = groupDiff
	reviewCmd.GroupID = groupDiff
	changesetCmd.GroupID = groupDiff
	intentCmd.GroupID = groupDiff
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(changesetCmd)
	rootCmd.AddCommand(intentCmd)

	// Workspaces (in Advanced group to reduce PLG cognitive load)
	wsCmd.GroupID = groupAdvanced
	integrateCmd.GroupID = groupAdvanced
	mergeCmd.GroupID = groupAdvanced
	checkoutCmd.GroupID = groupAdvanced
	rootCmd.AddCommand(wsCmd)
	rootCmd.AddCommand(integrateCmd)
	rootCmd.AddCommand(mergeCmd)
	rootCmd.AddCommand(checkoutCmd)

	// CI & Testing
	ciCmd.GroupID = groupCI
	testCmd.GroupID = groupCI
	rootCmd.AddCommand(ciCmd)
	rootCmd.AddCommand(testCmd)

	// Remote & Sync
	remoteCmd.GroupID = groupRemote
	pushCmd.GroupID = groupRemote
	fetchCmd.GroupID = groupRemote
	cloneCmd.GroupID = groupRemote
	authCmd.GroupID = groupRemote
	rootCmd.AddCommand(remoteCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(fetchCmd)
	rootCmd.AddCommand(cloneCmd)

	// Advanced (low-level/plumbing commands)
	snapshotCmd.GroupID = groupAdvanced
	snapCmd.GroupID = groupAdvanced
	analyzeCmd.GroupID = groupAdvanced
	dumpCmd.GroupID = groupAdvanced
	listCmd.GroupID = groupAdvanced
	logCmd.GroupID = groupAdvanced
	refCmd.GroupID = groupAdvanced
	modulesCmd.GroupID = groupAdvanced
	pickCmd.GroupID = groupAdvanced
	pruneCmd.GroupID = groupAdvanced
	completionCmd.GroupID = groupAdvanced
	remoteLogCmd.GroupID = groupAdvanced
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(snapCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(dumpCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(refCmd)
	rootCmd.AddCommand(modulesCmd)
	rootCmd.AddCommand(pickCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(remoteLogCmd)

	// Add auth subcommands
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authCmd)

	// Add review subcommands
	reviewCmd.AddCommand(reviewOpenCmd)
	reviewCmd.AddCommand(reviewListCmd)
	reviewCmd.AddCommand(reviewViewCmd)
	reviewCmd.AddCommand(reviewStatusCmd)
	reviewCmd.AddCommand(reviewApproveCmd)
	reviewCmd.AddCommand(reviewRequestChangesCmd)
	reviewCmd.AddCommand(reviewCloseCmd)
	reviewCmd.AddCommand(reviewReadyCmd)
	reviewCmd.AddCommand(reviewExportCmd)
	rootCmd.AddCommand(reviewCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// shortID safely truncates an ID string to 12 characters.
func shortID(s string) string {
	if len(s) >= 12 {
		return s[:12]
	}
	return s
}

// skipModulesFile is set by clone to skip creating kai.modules.yaml
var skipModulesFile bool
var initExplain bool

func runInit(cmd *cobra.Command, args []string) error {
	// Show explain if requested
	if initExplain {
		cwd, _ := os.Getwd()
		ctx := explain.ExplainInit(cwd)
		ctx.Print(os.Stdout)
	}

	// Create .kai directory
	if err := os.MkdirAll(kaiDir, 0755); err != nil {
		return fmt.Errorf("creating .kai directory: %w", err)
	}

	// Create objects directory
	objPath := filepath.Join(kaiDir, objectsDir)
	if err := os.MkdirAll(objPath, 0755); err != nil {
		return fmt.Errorf("creating objects directory: %w", err)
	}

	// Write default kai.modules.yaml in project root (not in .kai) only if it doesn't exist
	// This file is meant to be committed to version control and shared with the team
	// Skip during clone since the remote repo may have its own modules file
	if !skipModulesFile {
		if _, err := os.Stat(modulesFile); os.IsNotExist(err) {
			modulesContent := `# Kai module definitions
# This file maps file paths to logical modules for better intent generation.
# Commit this file to version control to share with your team.
#
# Example:
#   modules:
#     - name: Auth
#       paths:
#         - src/auth/**
#         - lib/session.js
#     - name: API
#       paths:
#         - src/routes/**
#         - src/controllers/**

modules: []
`
			if err := os.WriteFile(modulesFile, []byte(modulesContent), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", modulesFile, err)
			}
		}
	}

	// Write AI agent guide in .kai directory
	agentGuideFile := filepath.Join(kaiDir, "AGENTS.md")
	if _, err := os.Stat(agentGuideFile); os.IsNotExist(err) {
		agentGuide := `# Kai - AI Agent Guide

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

Get Kai's value in 6 simple commands:

` + "```" + `bash
# 1. Initialize Kai
kai init

# 2. Scan your project (snapshot + analyze in one step)
kai capture

# 3. Make changes to your code...

# 4. See what changed semantically
kai diff

# 5. Open a review
kai review open --title "Fix bug"

# 6. Preview CI impact
kai ci plan --explain

# 7. Complete the review
kai review view <id>        # View review details
kai review approve <id>     # Approve the review
kai review close <id> --state merged
` + "```" + `

That's it! You now have semantic diffs, change classification, and selective CI.

## The Core Commands

| Command | What it does |
|---------|-------------|
| ` + "`" + `kai capture` + "`" + ` | Snapshot + analyze in one step (recommended) |
| ` + "`" + `kai diff` + "`" + ` | Show semantic differences |
| ` + "`" + `kai review open` + "`" + ` | Create a code review |
| ` + "`" + `kai review view <id>` + "`" + ` | View review details |
| ` + "`" + `kai review approve <id>` + "`" + ` | Approve a review |
| ` + "`" + `kai review close <id>` + "`" + ` | Close review (--state merged\|abandoned) |
| ` + "`" + `kai ci plan` + "`" + ` | Compute affected tests |

## Getting Started (Detailed)

If you want more control, here's the step-by-step approach:

### Step 1: Initialize Kai
` + "```" + `bash
kai init
` + "```" + `
This creates a ` + "`" + `.kai/` + "`" + ` directory with the database and object storage.

### Step 2: Create a Snapshot
` + "```" + `bash
# From directory (recommended)
kai snap .

# Or from Git branch/tag/commit
kai snapshot create --git main
` + "```" + `

### Step 3: Make Changes and Diff
After modifying code:
` + "```" + `bash
kai capture                    # Re-capture with changes
kai diff                    # See semantic differences
` + "```" + `

### Step 4: Review and CI
` + "```" + `bash
kai review open --title "Fix login bug"
kai ci plan --explain       # See what tests to run
` + "```" + `

### Step 5: Complete the Review
` + "```" + `bash
kai review view <id>        # View the review details
kai review approve <id>     # Approve the review
kai review close <id> --state merged  # Close as merged
` + "```" + `

## Quick Reference

### Check Status
` + "```" + `bash
kai status                    # Show pending changes since last snapshot
` + "```" + `

### References (avoid typing long hashes)
- ` + "`" + `@snap:last` + "`" + ` - Most recent snapshot
- ` + "`" + `@snap:prev` + "`" + ` - Previous snapshot
- ` + "`" + `@cs:last` + "`" + ` - Most recent changeset
- ` + "`" + `snap.main` + "`" + `, ` + "`" + `cs.feature` + "`" + ` - Named refs (create with ` + "`" + `kai ref set snap.main @snap:last` + "`" + `)

### Remote Operations (sync with server)
` + "```" + `bash
kai clone http://server/org/repo           # Clone a repository (creates directory)
kai clone http://server/org/repo mydir     # Clone into specific directory
kai remote set origin https://kailab.example.com --tenant myorg --repo myproject
kai auth login                # Authenticate
kai push origin snap.latest   # Upload to server
kai fetch origin              # Download from server
` + "```" + `

## Key Concepts

| Concept | What it is | Analogy |
|---------|------------|---------|
| **Snapshot** | Semantic capture of codebase | Like a Git commit, but understands code structure |
| **ChangeSet** | Diff between two snapshots | Like ` + "`" + `git diff` + "`" + `, but classifies change types |
| **Intent** | Human summary of changes | Like a commit message, but auto-generated |
| **Module** | Logical file grouping | Like folders, but by feature (Auth, Billing, etc.) |

## Change Types Kai Detects

| Type | What it means | Example |
|------|---------------|---------|
| ` + "`" + `FUNCTION_ADDED` + "`" + ` | New function created | Added ` + "`" + `validateToken()` + "`" + ` |
| ` + "`" + `FUNCTION_REMOVED` + "`" + ` | Function deleted | Removed ` + "`" + `legacyAuth()` + "`" + ` |
| ` + "`" + `CONDITION_CHANGED` + "`" + ` | If/comparison changed | ` + "`" + `if (x > 100)` + "`" + ` → ` + "`" + `if (x > 50)` + "`" + ` |
| ` + "`" + `CONSTANT_UPDATED` + "`" + ` | Literal value changed | ` + "`" + `TIMEOUT = 3600` + "`" + ` → ` + "`" + `1800` + "`" + ` |
| ` + "`" + `API_SURFACE_CHANGED` + "`" + ` | Function signature changed | Added parameter to function |
| ` + "`" + `FILE_ADDED` + "`" + ` | New file created | Added ` + "`" + `auth/mfa.ts` + "`" + ` |
| ` + "`" + `FILE_DELETED` + "`" + ` | File removed | Deleted ` + "`" + `deprecated/old.ts` + "`" + ` |

## Common Tasks

### "I want to see what changed in my code"
` + "```" + `bash
# Semantic diff (default) - shows function/class/variable changes
kai diff
# Output shows:
#   ~ auth/login.ts
#     ~ function login(user) -> login(user, token)
#     + function validateMFA(code)
#   Summary: 1 file, 2 units changed

# Line-level diff like git (with colors)
kai diff -p

# Just file paths
kai diff --name-only

# JSON output for programmatic use
kai diff --json
` + "```" + `

### "I want a git-style line diff"
` + "```" + `bash
kai diff -p
# Output shows:
#   diff --kai a/src/auth.ts b/src/auth.ts
#   --- a/src/auth.ts
#   +++ b/src/auth.ts
#   @@ -42 +42 @@
#   -  const timeout = 3600;
#   +  const timeout = 1800;
` + "```" + `

### "I want to compare two Git branches"
` + "```" + `bash
# Must use explicit --git flag (no positional args)
kai snapshot create --git main
kai snapshot create --git feature-branch
kai analyze symbols @snap:prev
kai analyze symbols @snap:last
kai changeset create @snap:prev @snap:last
kai intent render @cs:last
` + "```" + `

### "I want to save a named reference"
` + "```" + `bash
kai ref set snap.main @snap:last      # Name the current snapshot
kai ref set snap.v1.0 abc123          # Name by ID
kai ref list                          # See all refs
` + "```" + `

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

` + "```" + `bash
# Kai's structured semantic analysis
kai dump @cs:last --json

# Raw Git diff for the same changes (ground truth)
git diff HEAD~1..HEAD

# Or use Kai's diff with --raw flag
kai diff @snap:prev @snap:last --raw
` + "```" + `

### Recommended Workflow

1. **Start with Kai** — Use structured data for speed and semantic understanding
2. **Verify when needed** — Check raw diff if something seems miscategorized
3. **Trust the diff** — If Kai and raw diff disagree, the diff is correct

## CI & Test Selection

Kai provides intelligent test selection for CI pipelines. Instead of running all tests on every change, analyze which tests are affected.

### Generate a Test Plan

` + "```" + `bash
# Generate test selection plan from a changeset
kai ci plan @cs:last --out plan.json

# Human-readable explanation
kai ci plan @cs:last --explain

# Force full suite (panic switch)
KAI_FORCE_FULL=1 kai ci plan @cs:last --out plan.json
` + "```" + `

### Safety Modes

| Mode | Description |
|------|-------------|
| ` + "`" + `shadow` + "`" + ` | Compute plan but run full suite. Compare predictions to learn. |
| ` + "`" + `guarded` + "`" + ` | Run selective with auto-fallback on risk. Default mode. |
| ` + "`" + `strict` + "`" + ` | Run selective only. Use panic switch for full suite. |

` + "```" + `bash
kai ci plan @cs:last --safety-mode=shadow   # Learning phase
kai ci plan @cs:last --safety-mode=guarded  # Safe default
kai ci plan @cs:last --safety-mode=strict   # High confidence
` + "```" + `

### Find Affected Tests

` + "```" + `bash
# Which tests are affected by changes between two snapshots?
kai test affected @snap:prev @snap:last

# Uses import graph tracing to find transitive dependencies
` + "```" + `

### Structural Risks Detected

| Risk | Severity | Meaning |
|------|----------|---------|
| ` + "`" + `config_change` + "`" + ` | High | package.json, tsconfig, etc. changed |
| ` + "`" + `test_infra` + "`" + ` | High | Fixtures, mocks, setup files changed |
| ` + "`" + `dynamic_import` + "`" + ` | High | Dynamic require/import detected |
| ` + "`" + `no_test_mapping` + "`" + ` | Medium | Changed files have no test coverage |
| ` + "`" + `cross_module_change` + "`" + ` | Medium | Changes span 3+ modules |

## Working Snapshot Model

Kai uses a two-tier snapshot model to prevent database bloat:

| Ref | Purpose | GC Root? |
|-----|---------|----------|
| ` + "`" + `@snap:working` + "`" + ` | Current working directory state | No (ephemeral) |
| ` + "`" + `@snap:last` + "`" + ` | Last committed baseline | No (ephemeral) |
| ` + "`" + `snap.main` + "`" + `, etc. | Named refs you create | Yes (permanent) |

### How it works:

1. **` + "`" + `kai capture` + "`" + `** creates a snapshot and updates ` + "`" + `snap.latest` + "`" + `
2. **` + "`" + `kai status` + "`" + `** compares working directory to ` + "`" + `snap.latest` + "`" + `
3. **` + "`" + `kai review open` + "`" + `** creates a changeset for review
4. **` + "`" + `kai prune` + "`" + `** cleans up unreferenced snapshots

### Garbage Collection

` + "```" + `bash
kai prune              # Dry-run (shows what would be deleted)
kai prune --yes        # Actually delete unreachable content
kai prune --since 7    # Only delete content older than 7 days
kai prune --keep "src/**"  # Preserve files matching pattern
` + "```" + `

**What stays alive:**
- Snapshots referenced by workspaces
- Snapshots referenced by reviews
- Snapshots with named refs (` + "`" + `snap.main` + "`" + `, etc.)

**What gets cleaned up:**
- Old ` + "`" + `@snap:working` + "`" + ` snapshots (replaced by newer ones)
- Orphaned changesets with no review

## Troubleshooting

| Error | Fix |
|-------|-----|
| "Kai not initialized" | Run ` + "`" + `kai init` + "`" + ` first |
| "No snapshots found" | Create one with ` + "`" + `kai snapshot create --dir .` + "`" + ` |
| "ambiguous prefix" | Use more characters of the ID, or use ` + "`" + `@snap:last` + "`" + ` |
| "Last capture was X minutes ago" | Run ` + "`" + `kai capture` + "`" + ` or use ` + "`" + `--force` + "`" + ` |

## More Information

Run ` + "`" + `kai --help` + "`" + ` or ` + "`" + `kai <command> --help` + "`" + ` for detailed usage.
`
		if err := os.WriteFile(agentGuideFile, []byte(agentGuide), 0644); err != nil {
			return fmt.Errorf("writing AGENTS.md: %w", err)
		}
	}

	// Open database and apply schema
	dbPath := filepath.Join(kaiDir, dbFile)
	db, err := graph.Open(dbPath, objPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Set WAL mode outside transaction (SQLite requirement)
	db.Exec("PRAGMA journal_mode=WAL")

	// Apply schema inline (since we may not have the schema file available)
	schema := `
CREATE TABLE IF NOT EXISTS nodes (
  id BLOB PRIMARY KEY,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS nodes_kind ON nodes(kind);

CREATE TABLE IF NOT EXISTS edges (
  src BLOB NOT NULL,
  type TEXT NOT NULL,
  dst BLOB NOT NULL,
  at  BLOB,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst, at)
);

CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_type ON edges(type);
CREATE INDEX IF NOT EXISTS edges_at ON edges(at);

-- Named references (aliases)
CREATE TABLE IF NOT EXISTS refs (
  name TEXT PRIMARY KEY,
  target_id BLOB NOT NULL,
  target_kind TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS refs_kind ON refs(target_kind);

-- Human-readable slugs
CREATE TABLE IF NOT EXISTS slugs (
  target_id BLOB PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL
);

-- Sequence log for navigation
CREATE TABLE IF NOT EXISTS logs (
  kind TEXT NOT NULL,
  seq INTEGER NOT NULL,
  id BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (kind, seq)
);
CREATE INDEX IF NOT EXISTS logs_id ON logs(id);

-- Ref change log for auditability (append-only)
CREATE TABLE IF NOT EXISTS ref_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  old_target BLOB,
  new_target BLOB NOT NULL,
  actor TEXT,
  moved_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ref_log_name ON ref_log(name);
CREATE INDEX IF NOT EXISTS ref_log_moved_at ON ref_log(moved_at);

-- Index for prune --since filtering
CREATE INDEX IF NOT EXISTS nodes_created_at ON nodes(created_at);
`
	// Apply schema in a transaction
	tx, err := db.BeginTx()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(schema); err != nil {
		tx.Rollback()
		return fmt.Errorf("applying schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing schema: %w", err)
	}

	fmt.Println()
	fmt.Println("╭─────────────────────────────────────────────")
	fmt.Println("│  ✓ Initialized Kai in .kai/")
	fmt.Println("│")
	fmt.Println("│  Quickstart:")
	fmt.Println("│    kai capture        # Snapshot your code")
	fmt.Println("│    kai diff           # See what changed")
	fmt.Println("│    kai review open    # Commit & review")
	fmt.Println("│    kai ci plan        # Get selective test plan")
	fmt.Println("│")
	fmt.Println("│  That's it. Semantic commits + safe selective CI.")
	fmt.Println("│")
	fmt.Println("│  Use --explain on any command to learn more.")
	fmt.Println("╰─────────────────────────────────────────────")
	return nil
}

func runSnap(cmd *cobra.Command, args []string) error {
	// Set dirPath from argument or default to current directory
	if len(args) > 0 {
		dirPath = args[0]
	} else {
		dirPath = "."
	}
	// Delegate to runSnapshot which handles dirPath mode
	return runSnapshot(cmd, args)
}

// runCapture is the "2-minute value" macro command that performs:
// 1. Snapshot the directory
// 2. Analyze symbols
// 3. Analyze calls (build call graph)
// This is the recommended starting point for new users.
func runCapture(cmd *cobra.Command, args []string) error {
	// Determine path to capture
	capturePath := "."
	if len(args) > 0 {
		capturePath = args[0]
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Load modules
	fmt.Print("Loading module configuration... ")
	matcher, err := loadMatcher()
	if err != nil {
		fmt.Println("failed")
		return err
	}
	moduleCount := len(matcher.GetAllModules())
	fmt.Printf("found %d modules\n", moduleCount)

	// Show explanation if requested
	if captureExplain {
		ctx := explain.ExplainCapture(capturePath, moduleCount)
		ctx.Print(os.Stdout)
	}

	// Step 1: Create snapshot
	fmt.Println()
	fmt.Println("Step 1/3: Creating snapshot...")
	fmt.Printf("Capturing directory: %s\n", capturePath)
	source, err := dirio.OpenDirectory(capturePath)
	if err != nil {
		return fmt.Errorf("opening directory: %w", err)
	}

	fmt.Print("Reading files... ")
	files, err := source.GetFiles()
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("getting files: %w", err)
	}
	fmt.Printf("found %d files\n", len(files))

	fmt.Print("Creating snapshot... ")
	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("creating snapshot: %w", err)
	}
	fmt.Println("done")

	// Step 2: Analyze symbols
	fmt.Println()
	fmt.Println("Step 2/3: Analyzing symbols...")
	progress := func(current, total int, filename string) {
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rAnalyzing... %d/%d %s", current, total, display)
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeSymbols(snapshotID, progress); err != nil {
		fmt.Print("\rAnalyzing symbols... ")
		fmt.Println("warning: some files failed")
		fmt.Fprintf(os.Stderr, "  %v\n", err)
	} else {
		fmt.Print("\rAnalyzing symbols... done")
		fmt.Print("\033[K")
		fmt.Println()
	}

	// Step 3: Analyze calls (build call graph)
	fmt.Println()
	fmt.Println("Step 3/3: Building call graph...")
	callProgress := func(current, total int, filename string) {
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rBuilding graph... %d/%d %s", current, total, display)
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeCalls(snapshotID, callProgress); err != nil {
		fmt.Print("\rBuilding call graph... ")
		fmt.Println("warning: some files failed")
		fmt.Fprintf(os.Stderr, "  %v\n", err)
	} else {
		fmt.Print("\rBuilding call graph... done")
		fmt.Print("\033[K")
		fmt.Println()
	}

	// Update refs - like git commit, capture always updates snap.latest
	fmt.Print("Updating refs... ")
	autoRefMgr := ref.NewAutoRefManager(db)

	// Check if this is the first scan (no snap.latest ref exists)
	refMgr := ref.NewRefManager(db)
	existingLatest, _ := refMgr.Get("snap.latest")
	isFirstScan := existingLatest == nil

	// Always update snap.latest (like git commit updates HEAD)
	if err := autoRefMgr.OnSnapshotCreated(snapshotID); err != nil {
		fmt.Println("failed")
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	} else {
		fmt.Println("done")
	}

	// Compute change summary if not first scan
	var changeSummary *captureSummary
	if !isFirstScan {
		fmt.Println()
		fmt.Print("Computing changes... ")
		changeSummary = computeCaptureSummary(db, snapshotID, matcher)
		fmt.Println("done")
	}

	// Summary
	fmt.Println()
	fmt.Println("╭─────────────────────────────────────────────")
	fmt.Println("│  ✓ Scan complete!")
	fmt.Println("│")
	fmt.Printf("│  Snapshot: %s\n", util.BytesToHex(snapshotID)[:12])
	fmt.Printf("│  Files: %d\n", len(files))
	fmt.Printf("│  Modules: %d\n", moduleCount)

	// Show change summary if available
	if changeSummary != nil && changeSummary.hasChanges {
		fmt.Println("│")
		fmt.Println("│  Changes detected:")
		fmt.Printf("│    %d file(s) modified\n", changeSummary.filesChanged)
		if len(changeSummary.changeTypes) > 0 {
			// Group into 3 buckets for cleaner display
			buckets := bucketChangeTypes(changeSummary.changeTypes)

			// Display buckets in consistent order: Structural, Behavioral, API/Contract
			bucketOrder := []ChangeBucket{BucketStructural, BucketBehavioral, BucketAPIContract}
			for _, bucket := range bucketOrder {
				paths := buckets[bucket]
				if len(paths) == 0 {
					continue
				}
				if len(paths) == 1 {
					fmt.Printf("│      %s: %s\n", bucket, paths[0])
				} else if len(paths) <= 3 {
					fmt.Printf("│      %s: %s\n", bucket, strings.Join(paths, ", "))
				} else {
					fmt.Printf("│      %s: %d files\n", bucket, len(paths))
				}
			}
		}
		if len(changeSummary.modules) > 0 {
			fmt.Printf("│    Modules: %s\n", strings.Join(changeSummary.modules, ", "))
		}
	} else if changeSummary != nil {
		fmt.Println("│")
		fmt.Println("│  No changes since baseline.")
	}

	fmt.Println("│")
	if isFirstScan {
		fmt.Println("│  Snapshot created (snap.latest).")
		fmt.Println("│")
		fmt.Println("│  Next: make changes, then:")
		fmt.Println("│    kai status         # See what changed")
		fmt.Println("│    kai capture        # Capture changes")
		fmt.Println("│    kai review open    # Create a review")
	} else if changeSummary != nil && changeSummary.hasChanges {
		fmt.Println("│  Snapshot updated (snap.latest).")
		fmt.Println("│")
		fmt.Println("│  Next:")
		fmt.Println("│    kai review open    # Create a review")
		fmt.Println("│    kai ci plan        # Get selective test plan")
		fmt.Println("│    kai push           # Push to remote")
	} else {
		fmt.Println("│  Snapshot updated (snap.latest).")
		fmt.Println("│")
		fmt.Println("│  No changes from previous snapshot.")
	}
	fmt.Println("╰─────────────────────────────────────────────")

	return nil
}

// captureSummary holds a summary of changes detected during capture
type captureSummary struct {
	hasChanges   bool
	filesChanged int
	changeTypes  map[string][]string // category -> list of paths
	modules      []string
}

// ChangeBucket represents a high-level category of changes for simpler UX
type ChangeBucket string

const (
	BucketStructural  ChangeBucket = "Structural"  // Things added/removed/moved
	BucketBehavioral  ChangeBucket = "Behavioral"  // Logic/values changed
	BucketAPIContract ChangeBucket = "API/Contract" // Interface/contract changed
)

// categorizeToBucket maps a raw change category to one of 3 user-friendly buckets
func categorizeToBucket(category string) ChangeBucket {
	switch category {
	// Structural: things were added/removed/moved
	case "FILE_ADDED", "FILE_DELETED", "FUNCTION_ADDED", "FUNCTION_REMOVED",
		"JSON_FIELD_ADDED", "JSON_FIELD_REMOVED", "YAML_KEY_ADDED", "YAML_KEY_REMOVED":
		return BucketStructural
	// Behavioral: logic/values changed
	case "CONDITION_CHANGED", "CONSTANT_UPDATED", "JSON_VALUE_CHANGED",
		"JSON_ARRAY_CHANGED", "YAML_VALUE_CHANGED", "FILE_CONTENT_CHANGED":
		return BucketBehavioral
	// API/Contract: interface/contract changed
	case "API_SURFACE_CHANGED":
		return BucketAPIContract
	default:
		// Unknown categories go to Behavioral as fallback
		return BucketBehavioral
	}
}

// bucketChangeTypes groups raw change types into 3 buckets for simpler display
func bucketChangeTypes(changeTypes map[string][]string) map[ChangeBucket][]string {
	buckets := make(map[ChangeBucket][]string)
	seen := make(map[ChangeBucket]map[string]bool)

	for category, paths := range changeTypes {
		bucket := categorizeToBucket(category)
		if seen[bucket] == nil {
			seen[bucket] = make(map[string]bool)
		}
		for _, path := range paths {
			if !seen[bucket][path] {
				seen[bucket][path] = true
				buckets[bucket] = append(buckets[bucket], path)
			}
		}
	}

	return buckets
}

// computeCaptureSummary computes a quick summary of changes between @snap:last and new snapshot
func computeCaptureSummary(db *graph.DB, newSnapshotID []byte, matcher *module.Matcher) *captureSummary {
	summary := &captureSummary{
		changeTypes: make(map[string][]string),
	}

	// Get baseline snapshot
	baseSnapID, err := resolveSnapshotID(db, "@snap:last")
	if err != nil {
		return summary
	}

	creator := snapshot.NewCreator(db, matcher)

	// Get files from both snapshots
	baseFiles, err := creator.GetSnapshotFiles(baseSnapID)
	if err != nil {
		return summary
	}
	newFiles, err := creator.GetSnapshotFiles(newSnapshotID)
	if err != nil {
		return summary
	}

	// Build maps
	baseFileMap := make(map[string]*graph.Node)
	for _, f := range baseFiles {
		path, _ := f.Payload["path"].(string)
		baseFileMap[path] = f
	}
	newFileMap := make(map[string]*graph.Node)
	for _, f := range newFiles {
		path, _ := f.Payload["path"].(string)
		newFileMap[path] = f
	}

	// Find changed files
	var changedPaths []string
	modulesSet := make(map[string]bool)

	// Check for modified and added files
	for path, newFile := range newFileMap {
		baseFile, exists := baseFileMap[path]
		newDigest, _ := newFile.Payload["digest"].(string)

		if !exists {
			// Added file
			changedPaths = append(changedPaths, path)
			summary.changeTypes["FILE_ADDED"] = append(summary.changeTypes["FILE_ADDED"], path)
		} else {
			baseDigest, _ := baseFile.Payload["digest"].(string)
			if newDigest != baseDigest {
				// Modified file
				changedPaths = append(changedPaths, path)

				// Try to detect semantic changes
				lang, _ := newFile.Payload["lang"].(string)
				beforeContent, _ := db.ReadObject(baseDigest)
				afterContent, _ := db.ReadObject(newDigest)

				if len(beforeContent) > 0 && len(afterContent) > 0 {
					detector := classify.NewDetector()
					var changes []*classify.ChangeType

					switch lang {
					case "json":
						changes, _ = classify.DetectJSONChanges(path, beforeContent, afterContent)
					case "ts", "js", "tsx", "jsx", "go", "py":
						changes, _ = detector.DetectChanges(path, beforeContent, afterContent, "")
					default:
						changes = []*classify.ChangeType{classify.NewFileChange(classify.FileContentChanged, path)}
					}

					for _, ct := range changes {
						category := string(ct.Category)
						summary.changeTypes[category] = append(summary.changeTypes[category], path)
					}
				}
			}
		}

		// Track modules
		if matcher != nil {
			for _, mod := range matcher.MatchPath(path) {
				modulesSet[mod] = true
			}
		}
	}

	// Check for deleted files
	for path := range baseFileMap {
		if _, exists := newFileMap[path]; !exists {
			changedPaths = append(changedPaths, path)
			summary.changeTypes["FILE_DELETED"] = append(summary.changeTypes["FILE_DELETED"], path)
		}
	}

	summary.filesChanged = len(changedPaths)
	summary.hasChanges = len(changedPaths) > 0

	for mod := range modulesSet {
		summary.modules = append(summary.modules, mod)
	}

	return summary
}

// createSnapshotFromDir creates a snapshot from the given directory and returns its ID.
// This is a helper used by commands that need to auto-snapshot (e.g., ws create).
func createSnapshotFromDir(db *graph.DB, dir string) ([]byte, error) {
	fmt.Print("Loading module configuration... ")
	matcher, err := loadMatcher()
	if err != nil {
		fmt.Println("failed")
		return nil, err
	}
	fmt.Printf("found %d modules\n", len(matcher.GetAllModules()))

	fmt.Printf("Scanning directory: %s\n", dir)
	source, err := dirio.OpenDirectory(dir)
	if err != nil {
		return nil, fmt.Errorf("opening directory: %w", err)
	}

	fmt.Print("Reading files... ")
	files, err := source.GetFiles()
	if err != nil {
		fmt.Println("failed")
		return nil, fmt.Errorf("getting files: %w", err)
	}
	fmt.Printf("found %d files\n", len(files))

	fmt.Print("Creating snapshot... ")
	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		fmt.Println("failed")
		return nil, fmt.Errorf("creating snapshot: %w", err)
	}
	fmt.Println("done")

	// Auto-analyze symbols
	fmt.Printf("Analyzing symbols... ")
	progress := func(current, total int, filename string) {
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rAnalyzing symbols... %d/%d %s", current, total, display)
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeSymbols(snapshotID, progress); err != nil {
		fmt.Println(" failed")
		fmt.Fprintf(os.Stderr, "warning: symbol analysis failed: %v\n", err)
	} else {
		fmt.Print("\rAnalyzing symbols... done")
		fmt.Print("\033[K")
		fmt.Println()
	}

	// Update auto-refs
	fmt.Print("Updating refs... ")
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnSnapshotCreated(snapshotID); err != nil {
		fmt.Println("failed")
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	} else {
		fmt.Println("done")
	}

	fmt.Printf("Created snapshot: %s\n", util.BytesToHex(snapshotID))
	return snapshotID, nil
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Print("Loading module configuration... ")
	matcher, err := loadMatcher()
	if err != nil {
		fmt.Println("failed")
		return err
	}
	fmt.Printf("found %d modules\n", len(matcher.GetAllModules()))

	var source filesource.FileSource

	// Require explicit --git or --dir (no positional args allowed)
	hasExplicitGit := snapshotGitRef != ""
	hasExplicitDir := dirPath != ""
	hasPositionalArg := len(args) > 0

	// Reject positional arguments - must be explicit
	if hasPositionalArg {
		arg := args[0]
		fmt.Println()
		fmt.Println("╭─ Positional arguments not allowed")
		fmt.Println("│")
		fmt.Printf("│  'kai snapshot create %s' is ambiguous.\n", arg)
		fmt.Println("│")
		fmt.Println("│  Please be explicit about the source:")
		fmt.Println("│")
		fmt.Printf("│    kai snapshot create --git %s    # Snapshot from Git commit/branch\n", arg)
		fmt.Printf("│    kai snapshot create --dir %s    # Snapshot from directory\n", arg)
		fmt.Println("│")
		fmt.Println("│  Or use 'kai snap' for quick directory snapshots:")
		fmt.Println("│")
		fmt.Printf("│    kai snap %s\n", arg)
		fmt.Println("│")
		fmt.Println("╰────────────────────────────────────────────")
		return fmt.Errorf("positional arguments not allowed: use --git or --dir")
	}

	// Handle the explicit modes
	if hasExplicitDir {
		// Directory mode - no Git required
		path := dirPath
		if path == "" {
			path = "."
		}
		fmt.Printf("Scanning directory: %s\n", path)
		source, err = dirio.OpenDirectory(path)
		if err != nil {
			return fmt.Errorf("opening directory: %w", err)
		}
	} else if hasExplicitGit {
		// Explicit Git mode
		fmt.Printf("Opening git ref: %s\n", snapshotGitRef)
		source, err = gitio.OpenSource(repoPath, snapshotGitRef)
		if err != nil {
			return fmt.Errorf("opening git source: %w", err)
		}
	} else {
		// No source specified - show helpful usage
		fmt.Println()
		fmt.Println("╭─ No snapshot source specified")
		fmt.Println("│")
		fmt.Println("│  Choose one of:")
		fmt.Println("│")
		fmt.Println("│    kai snapshot create --git main        # From Git commit/branch/tag")
		fmt.Println("│    kai snapshot create --dir .           # From directory")
		fmt.Println("│")
		fmt.Println("│  Or use 'kai snap' for quick directory snapshots:")
		fmt.Println("│")
		fmt.Println("│    kai snap                       # Snapshot current directory")
		fmt.Println("│    kai snap src/                  # Snapshot specific path")
		fmt.Println("│")
		fmt.Println("│  For the full workflow, use 'kai capture':")
		fmt.Println("│")
		fmt.Println("│    kai capture                       # Snapshot + analyze in one step")
		fmt.Println("│")
		fmt.Println("╰────────────────────────────────────────────")
		return fmt.Errorf("snapshot source required: use --git <ref> or --dir <path>")
	}

	fmt.Print("Reading files... ")
	files, err := source.GetFiles()
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("getting files: %w", err)
	}
	fmt.Printf("found %d files\n", len(files))

	fmt.Print("Creating snapshot... ")
	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("creating snapshot: %w", err)
	}
	fmt.Println("done")

	// Auto-analyze symbols for better intent generation
	fmt.Printf("Analyzing symbols... ")
	progress := func(current, total int, filename string) {
		// Truncate filename if too long
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rAnalyzing symbols... %d/%d %s", current, total, display)
		// Clear rest of line in case previous filename was longer
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeSymbols(snapshotID, progress); err != nil {
		// Non-fatal - continue without symbols
		fmt.Println(" failed")
		fmt.Fprintf(os.Stderr, "warning: symbol analysis failed: %v\n", err)
	} else {
		fmt.Print("\rAnalyzing symbols... done")
		fmt.Print("\033[K")
		fmt.Println()
	}

	// Add description if provided
	if snapshotMessage != "" {
		node, err := db.GetNode(snapshotID)
		if err == nil && node != nil {
			node.Payload["description"] = snapshotMessage
			if err := db.UpdateNodePayload(snapshotID, node.Payload); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save description: %v\n", err)
			}
		}
	}

	// Update auto-refs
	fmt.Print("Updating refs... ")
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnSnapshotCreated(snapshotID); err != nil {
		fmt.Println("failed")
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	} else {
		fmt.Println("done")
	}

	fmt.Println()
	fmt.Printf("Created snapshot: %s\n", util.BytesToHex(snapshotID))
	fmt.Printf("Source: %s (%s)\n", source.Identifier(), source.SourceType())
	fmt.Printf("Files: %d\n", len(files))
	return nil
}

func runAnalyzeSymbols(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snapshotID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving snapshot ID: %w", err)
	}

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	creator := snapshot.NewCreator(db, matcher)
	fmt.Printf("Analyzing symbols... ")
	progress := func(current, total int, filename string) {
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rAnalyzing symbols... %d/%d %s", current, total, display)
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeSymbols(snapshotID, progress); err != nil {
		return fmt.Errorf("analyzing symbols: %w", err)
	}

	fmt.Print("\rAnalyzing symbols... done")
	fmt.Print("\033[K")
	fmt.Println()
	return nil
}

func runAnalyzeCalls(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snapshotID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving snapshot ID: %w", err)
	}

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	creator := snapshot.NewCreator(db, matcher)
	fmt.Printf("Analyzing calls... ")
	progress := func(current, total int, filename string) {
		display := filename
		if len(display) > 40 {
			display = "..." + display[len(display)-37:]
		}
		fmt.Printf("\rAnalyzing calls... %d/%d %s", current, total, display)
		fmt.Print("\033[K")
	}
	if err := creator.AnalyzeCalls(snapshotID, progress); err != nil {
		return fmt.Errorf("analyzing calls: %w", err)
	}

	fmt.Print("\rAnalyzing calls... done")
	fmt.Print("\033[K")
	fmt.Println()
	return nil
}

func runTestAffected(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	creator := snapshot.NewCreator(db, matcher)

	// Resolve both snapshot IDs
	baseSnapshotID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving base snapshot ID: %w", err)
	}

	headSnapshotID, err := resolveSnapshotID(db, args[1])
	if err != nil {
		return fmt.Errorf("resolving head snapshot ID: %w", err)
	}

	// Get files that changed
	changedFiles, err := getChangedFiles(db, creator, baseSnapshotID, headSnapshotID)
	if err != nil {
		return fmt.Errorf("getting changed files: %w", err)
	}

	if len(changedFiles) == 0 {
		fmt.Println("No changed files found")
		return nil
	}

	// Build a set of changed file paths
	changedPaths := make(map[string]bool)
	for _, f := range changedFiles {
		changedPaths[f] = true
	}

	// Get all files in the snapshot to build the graph context
	files, err := creator.GetSnapshotFiles(headSnapshotID)
	if err != nil {
		return fmt.Errorf("getting snapshot files: %w", err)
	}

	// Build file ID -> path map
	filePathByID := make(map[string]string)
	fileIDByPath := make(map[string][]byte)
	for _, f := range files {
		path, _ := f.Payload["path"].(string)
		idHex := util.BytesToHex(f.ID)
		filePathByID[idHex] = path
		fileIDByPath[path] = f.ID
	}

	// Find all files that depend on the changed files (reverse call graph)
	// and all test files that test the changed files
	affectedTests := make(map[string]bool)

	// First: direct test files for changed files
	for changedPath := range changedPaths {
		// Find TESTS edges pointing to files with this path
		// Uses path-based lookup to handle content-addressed ID changes
		testsEdges, err := db.GetEdgesToByPath(changedPath, graph.EdgeTests)
		if err != nil {
			continue
		}
		for _, e := range testsEdges {
			srcHex := util.BytesToHex(e.Src)
			if path, ok := filePathByID[srcHex]; ok {
				affectedTests[path] = true
			} else {
				// Query node directly if not in current snapshot
				if srcNode, _ := db.GetNode(e.Src); srcNode != nil {
					if srcPath, ok := srcNode.Payload["path"].(string); ok {
						affectedTests[srcPath] = true
					}
				}
			}
		}

		// Also find files that import/call into the changed file
		importsEdges, err := db.GetEdgesToByPath(changedPath, graph.EdgeImports)
		if err != nil {
			continue
		}
		for _, e := range importsEdges {
			srcHex := util.BytesToHex(e.Src)
			if path, ok := filePathByID[srcHex]; ok {
				// If this importer is a test file, add it
				if parse.IsTestFile(path) {
					affectedTests[path] = true
				}
			} else {
				// Query node directly
				if srcNode, _ := db.GetNode(e.Src); srcNode != nil {
					if srcPath, ok := srcNode.Payload["path"].(string); ok {
						if parse.IsTestFile(srcPath) {
							affectedTests[srcPath] = true
						}
					}
				}
			}
		}

		callsEdges, err := db.GetEdgesToByPath(changedPath, graph.EdgeCalls)
		if err != nil {
			continue
		}
		for _, e := range callsEdges {
			srcHex := util.BytesToHex(e.Src)
			if path, ok := filePathByID[srcHex]; ok {
				if parse.IsTestFile(path) {
					affectedTests[path] = true
				}
			} else {
				// Query node directly
				if srcNode, _ := db.GetNode(e.Src); srcNode != nil {
					if srcPath, ok := srcNode.Payload["path"].(string); ok {
						if parse.IsTestFile(srcPath) {
							affectedTests[srcPath] = true
						}
					}
				}
			}
		}
	}

	// Output results
	if len(affectedTests) == 0 {
		fmt.Println("No affected test files found")
		fmt.Println("(Make sure you've run 'kai analyze calls @snap:last' to build the call graph)")
		return nil
	}

	// Sort for consistent output
	var tests []string
	for t := range affectedTests {
		tests = append(tests, t)
	}
	sort.Strings(tests)

	fmt.Printf("Affected test files (%d):\n", len(tests))
	for _, t := range tests {
		fmt.Println(t)
	}

	return nil
}

// CIPlan represents a runner-agnostic selection plan
type CIPlan struct {
	Version       int                `json:"version"`
	Mode          string             `json:"mode"`                          // "selective", "expanded", "shadow", "full", "skip"
	Risk          string             `json:"risk"`                          // "low", "medium", "high"
	SafetyMode    string             `json:"safetyMode"`                    // "shadow", "guarded", "strict"
	Confidence    float64            `json:"confidence"`                    // 0.0-1.0 confidence score
	Targets       CITargets          `json:"targets"`
	Impact        CIImpact           `json:"impact"`
	Policy        CIPolicy           `json:"policy"`
	Safety        CISafety           `json:"safety"`
	Uncertainty   CIUncertainty      `json:"uncertainty"`                   // Structured uncertainty info
	ExpansionLog  []string           `json:"expansionLog,omitempty"`        // Why expansions happened
	DynamicImport *DynamicImportInfo `json:"dynamicImport,omitempty"`       // Dynamic import analysis
	Coverage      *CoverageInfo      `json:"coverage,omitempty"`            // Coverage-based selection info
	Contracts     *ContractInfo      `json:"contracts,omitempty"`           // Contract/schema change info
	Fallback      CIFallback         `json:"fallback"`                      // Fallback/tripwire status
	Provenance    CIProvenance       `json:"provenance"`                    // Audit trail
	Prediction    CIPrediction       `json:"prediction,omitempty"`          // For shadow mode comparison
}

// DynamicImportInfo captures dynamic import detection details
type DynamicImportInfo struct {
	Detected   bool                    `json:"detected"`
	Files      []DynamicImportFile     `json:"files,omitempty"`
	Policy     DynamicImportPolicyUsed `json:"policy"`
	Telemetry  DynamicImportTelemetry  `json:"telemetry"`
}

// DynamicImportFile represents a detected dynamic import in a file
type DynamicImportFile struct {
	Path          string  `json:"path"`
	Kind          string  `json:"kind"`                    // e.g., "import(variable)", "require(variable)", "__import__()"
	Line          int     `json:"line"`                    // Line number if available
	Bounded       bool    `json:"bounded"`                 // True if bounded by webpackInclude or similar
	BoundedBy     string  `json:"boundedBy,omitempty"`     // What bounded it (e.g., "webpackInclude: /\\.widget\\.js$/")
	BoundedRisky  bool    `json:"boundedRisky,omitempty"`  // True if bounded but footprint exceeds threshold
	Allowlisted   bool    `json:"allowlisted"`             // True if in allowlist
	Confidence    float64 `json:"confidence"`              // 0.0-1.0 confidence this is truly dynamic
	ExpandedTo    string  `json:"expandedTo,omitempty"`    // What module/package it expanded to
}

// DynamicImportPolicyUsed shows what policy was applied
type DynamicImportPolicyUsed struct {
	Expansion      string   `json:"expansion"`      // nearest_module, package, owners, full_suite
	ExpandedTo     []string `json:"expandedTo,omitempty"` // What modules/tests were added
	OwnersFallback bool     `json:"ownersFallback"`
}

// DynamicImportTelemetry provides counters for visibility
type DynamicImportTelemetry struct {
	TotalDetected int    `json:"totalDetected"`
	Bounded       int    `json:"bounded"`
	BoundedRisky  int    `json:"boundedRisky,omitempty"` // Bounded but exceeds footprint threshold
	Unbounded     int    `json:"unbounded"`
	Allowlisted   int    `json:"allowlisted"`
	WidenedTests  int    `json:"widenedTests"`           // How many tests were added due to dynamic imports
	StrategyUsed  string `json:"strategyUsed,omitempty"` // Actual strategy that was applied
	CacheHits     int    `json:"cacheHits,omitempty"`    // Files served from cache
	CacheMisses   int    `json:"cacheMisses,omitempty"`  // Files that needed scanning
}

// DynamicImportCache caches detection results by file digest
type DynamicImportCache struct {
	mu      sync.RWMutex
	entries map[string]DynamicImportCacheEntry
}

// DynamicImportCacheEntry is a cached detection result
type DynamicImportCacheEntry struct {
	DetectorVersion string
	Imports         []DynamicImportFile
}

// Global cache instance
var dynamicImportCache = &DynamicImportCache{
	entries: make(map[string]DynamicImportCacheEntry),
}

// Get retrieves cached results for a file digest
func (c *DynamicImportCache) Get(digest string) ([]DynamicImportFile, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[digest]
	if !ok || entry.DetectorVersion != DetectorVersion {
		return nil, false
	}
	return entry.Imports, true
}

// Set stores detection results for a file digest
func (c *DynamicImportCache) Set(digest string, imports []DynamicImportFile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[digest] = DynamicImportCacheEntry{
		DetectorVersion: DetectorVersion,
		Imports:         imports,
	}
}

// CIUncertainty captures structured uncertainty information
type CIUncertainty struct {
	Score   int                    `json:"score"`             // 0-100 uncertainty score (higher = more uncertain)
	Sources []string               `json:"sources"`           // What contributed to uncertainty
	Details *CIUncertaintyDetails  `json:"details,omitempty"` // Detailed breakdown
}

// CIUncertaintyDetails provides granular uncertainty info
type CIUncertaintyDetails struct {
	Coverage      *CoverageUncertainty      `json:"coverage,omitempty"`
	DynamicImport *DynamicImportUncertainty `json:"dynamicImport,omitempty"`
}

// CoverageUncertainty captures coverage-related uncertainty
type CoverageUncertainty struct {
	FilesWithoutCoverage int `json:"filesWithoutCoverage"`
	LookbackDays         int `json:"lookbackDays"`
}

// DynamicImportUncertainty captures dynamic import uncertainty
type DynamicImportUncertainty struct {
	Detected  bool `json:"detected"`
	Bounded   int  `json:"bounded"`
	Unbounded int  `json:"unbounded"`
}

// CIFallback captures fallback/tripwire status for auditability
type CIFallback struct {
	Used     bool   `json:"used"`               // Whether fallback was triggered
	Reason   string `json:"reason,omitempty"`   // Why: runtime_tripwire, planner_over_threshold, panic_switch
	Trigger  string `json:"trigger,omitempty"`  // What triggered it: "Cannot find module", "low_confidence", etc.
	ExitCode int    `json:"exitCode,omitempty"` // Exit code that triggered fallback (e.g., 75)
}

// CoverageInfo captures coverage-based test selection info
type CoverageInfo struct {
	Enabled              bool     `json:"enabled"`
	LookbackDays         int      `json:"lookbackDays"`
	FilesWithCoverage    int      `json:"filesWithCoverage"`
	FilesWithoutCoverage int      `json:"filesWithoutCoverage"`
	TestsFromCoverage    []string `json:"testsFromCoverage,omitempty"` // Tests selected via coverage
	CoverageMapAge       string   `json:"coverageMapAge,omitempty"`    // When coverage was last ingested
}

// ContractInfo captures contract/schema change detection
type ContractInfo struct {
	Changed          bool                `json:"changed"`
	SchemasChanged   []ContractChange    `json:"schemasChanged,omitempty"`
	TestsFromSchema  []string            `json:"testsFromSchema,omitempty"`  // Tests selected due to schema changes
	GeneratedChanged []string            `json:"generatedChanged,omitempty"` // Generated files that changed
}

// ContractChange represents a changed contract/schema
type ContractChange struct {
	Path         string   `json:"path"`
	Type         string   `json:"type"`                   // openapi, protobuf, graphql
	Service      string   `json:"service,omitempty"`      // Service/module this schema belongs to
	DigestBefore string   `json:"digestBefore,omitempty"` // Hash before change
	DigestAfter  string   `json:"digestAfter,omitempty"`  // Hash after change
	Tests        []string `json:"tests,omitempty"`        // Tests registered for this schema
}

// ========== Coverage Parsing Types ==========

// CoverageMap stores file -> test mappings from coverage reports
type CoverageMap struct {
	Version    int                        `json:"version"`
	RepoID     string                     `json:"repoId,omitempty"`
	Branch     string                     `json:"branch,omitempty"`
	Tag        string                     `json:"tag,omitempty"`
	IngestedAt string                     `json:"ingestedAt"`
	Entries    map[string][]CoverageEntry `json:"entries"` // file_path -> test entries
}

// CoverageEntry represents a single file -> test coverage record
type CoverageEntry struct {
	TestID      string `json:"testId"`      // Test file/name that covers this file
	LastSeenAt  string `json:"lastSeenAt"`  // ISO8601 timestamp
	HitCount    int    `json:"hitCount"`    // How many times this test hit this file
	LinesCovered []int `json:"linesCovered,omitempty"` // Specific lines if available
}

// ========== Contract Registry Types ==========

// ContractRegistry stores registered contracts/schemas
type ContractRegistry struct {
	Version   int                `json:"version"`
	Contracts []ContractBinding  `json:"contracts"`
}

// ContractBinding links a schema to its tests
type ContractBinding struct {
	Type       string   `json:"type"`                 // openapi, protobuf, graphql
	Path       string   `json:"path"`                 // Path to schema file
	Service    string   `json:"service,omitempty"`    // Service/module name
	Tests      []string `json:"tests"`                // Glob patterns for contract tests
	Digest     string   `json:"digest,omitempty"`     // Current fingerprint
	Generated  []string `json:"generated,omitempty"`  // Generated output paths
}

// CIProvenance captures audit information for the plan
type CIProvenance struct {
	Changeset       string   `json:"changeset,omitempty"`       // Changeset ID used
	Base            string   `json:"base,omitempty"`            // Base snapshot ID
	Head            string   `json:"head,omitempty"`            // Head snapshot ID
	KaiVersion      string   `json:"kaiVersion"`                // Kai CLI version
	DetectorVersion string   `json:"detectorVersion"`           // Dynamic import detector version
	GeneratedAt     string   `json:"generatedAt"`               // ISO8601 timestamp
	Analyzers       []string `json:"analyzers"`                 // Which analyzers ran
	PolicyHash      string   `json:"policyHash,omitempty"`      // Hash of ci-policy.yaml if used
}

// DetectorVersion is the current version of the dynamic import detector
const DetectorVersion = "1.0.0"

type CITargets struct {
	Run      []string            `json:"run"`
	Skip     []string            `json:"skip"`
	Full     []string            `json:"full,omitempty"` // All tests (for shadow mode comparison)
	Tags     map[string][]string `json:"tags,omitempty"`
	Fallback bool                `json:"fallback"` // If true, runner should fallback to full on failure
}

type CIImpact struct {
	FilesChanged    []string         `json:"filesChanged"`
	SymbolsChanged  []CISymbolChange `json:"symbolsChanged,omitempty"`
	ModulesAffected []string         `json:"modulesAffected,omitempty"`
	Uncertainty     []string         `json:"uncertainty,omitempty"`
}

type CISymbolChange struct {
	FQ     string `json:"fq"`
	Change string `json:"change"`
}

type CIPolicy struct {
	Strategy     string `json:"strategy"`
	Expanded     bool   `json:"expanded"`
	FallbackUsed string `json:"fallbackUsed,omitempty"`
}

// CISafety contains safety-related flags and risk signals
type CISafety struct {
	StructuralRisks  []StructuralRisk `json:"structuralRisks,omitempty"`
	Confidence       float64          `json:"confidence"`       // 0.0-1.0 confidence in selection
	RecommendFull    bool             `json:"recommendFull"`    // True if full run recommended
	RecommendReason  string           `json:"recommendReason,omitempty"`
	PanicSwitch      bool             `json:"panicSwitch"`      // Force full run (env/label override)
	AutoExpanded     bool             `json:"autoExpanded"`     // Was selection auto-expanded due to risk?
	ExpansionReasons []string         `json:"expansionReasons,omitempty"`
}

// StructuralRisk represents a detected risk pattern
type StructuralRisk struct {
	Type        string `json:"type"`        // Risk type identifier
	Description string `json:"description"` // Human-readable description
	Severity    string `json:"severity"`    // "low", "medium", "high", "critical"
	FilePath    string `json:"filePath,omitempty"`
	Triggered   bool   `json:"triggered"`   // Did this risk trigger expansion?
}

// CIPrediction contains shadow mode prediction data for comparison
type CIPrediction struct {
	SelectiveTests   int     `json:"selectiveTests"`   // Number of tests in selective plan
	FullTests        int     `json:"fullTests"`        // Number of tests in full suite
	PredictedSavings float64 `json:"predictedSavings"` // Percentage of tests saved
	// After running full suite, these get populated for comparison
	MissedFailures []string `json:"missedFailures,omitempty"` // Tests that failed but weren't selected
	FalsePositives []string `json:"falsePositives,omitempty"` // Tests selected but didn't need to run
}

// CIPolicyConfig defines risk thresholds and behavior for CI test selection
// Loaded from kai.ci-policy.yaml in repo root
type CIPolicyConfig struct {
	// Version for schema evolution
	Version int `yaml:"version" json:"version"`

	// Thresholds control when to expand or fail
	Thresholds CIPolicyThresholds `yaml:"thresholds" json:"thresholds"`

	// Paranoia rules define additional patterns that trigger expansion
	Paranoia CIPolicyParanoia `yaml:"paranoia" json:"paranoia"`

	// Behavior defines how to handle different risk levels
	Behavior CIPolicyBehavior `yaml:"behavior" json:"behavior"`

	// DynamicImports configures how to handle dynamic import detection
	DynamicImports CIPolicyDynamicImports `yaml:"dynamicImports" json:"dynamicImports"`

	// Coverage configures coverage-based test selection
	Coverage CIPolicyCoverage `yaml:"coverage" json:"coverage"`

	// Contracts configures contract/schema change detection
	Contracts CIPolicyContracts `yaml:"contracts" json:"contracts"`
}

// CIPolicyDynamicImports configures dynamic import handling
type CIPolicyDynamicImports struct {
	// Expansion strategy: nearest_module, package, owners, full_suite
	Expansion string `yaml:"expansion" json:"expansion"`
	// OwnersFallback: if module unknown, widen to code owners' suites
	OwnersFallback bool `yaml:"ownersFallback" json:"ownersFallback"`
	// MaxFilesThreshold: if >N files in module, widen by owners instead
	MaxFilesThreshold int `yaml:"maxFilesThreshold" json:"maxFilesThreshold"`
	// BoundedRiskThreshold: bounded imports matching >N files are treated as risky
	BoundedRiskThreshold int `yaml:"boundedRiskThreshold" json:"boundedRiskThreshold"`
	// Allowlist: paths where dynamic imports are known-safe (won't trigger expansion)
	Allowlist []string `yaml:"allowlist" json:"allowlist"`
	// BoundGlobs: map pattern -> test globs for bounded expansion
	BoundGlobs map[string][]string `yaml:"boundGlobs" json:"boundGlobs"`
}

// CIPolicyThresholds defines numeric thresholds for risk handling
type CIPolicyThresholds struct {
	// MinConfidence: expand to full if confidence below this (0.0-1.0)
	MinConfidence float64 `yaml:"minConfidence" json:"minConfidence"`
	// MaxUncertainty: expand if uncertainty score exceeds this (0-100)
	MaxUncertainty int `yaml:"maxUncertainty" json:"maxUncertainty"`
	// MaxFilesChanged: expand if more than N files changed
	MaxFilesChanged int `yaml:"maxFilesChanged" json:"maxFilesChanged"`
	// MaxTestsSkipped: expand if more than N% of tests would be skipped
	MaxTestsSkipped float64 `yaml:"maxTestsSkipped" json:"maxTestsSkipped"`
}

// CIPolicyParanoia defines patterns that trigger extra caution
type CIPolicyParanoia struct {
	// AlwaysFullPatterns: globs that trigger full run when matched
	AlwaysFullPatterns []string `yaml:"alwaysFullPatterns" json:"alwaysFullPatterns"`
	// ExpandOnPatterns: globs that trigger expansion (but not full)
	ExpandOnPatterns []string `yaml:"expandOnPatterns" json:"expandOnPatterns"`
	// RiskMultipliers: boost risk score for certain paths
	RiskMultipliers map[string]float64 `yaml:"riskMultipliers" json:"riskMultipliers"`
}

// CIPolicyBehavior defines how to respond to different conditions
type CIPolicyBehavior struct {
	// OnHighRisk: "expand", "warn", "fail"
	OnHighRisk string `yaml:"onHighRisk" json:"onHighRisk"`
	// OnLowConfidence: "expand", "warn", "fail"
	OnLowConfidence string `yaml:"onLowConfidence" json:"onLowConfidence"`
	// OnNoTests: "expand", "warn", "pass" - what to do when no tests selected
	OnNoTests string `yaml:"onNoTests" json:"onNoTests"`
	// FailOnExpansion: if true, exit non-zero when expansion happens
	FailOnExpansion bool `yaml:"failOnExpansion" json:"failOnExpansion"`
}

// CIPolicyCoverage configures coverage-based test selection
type CIPolicyCoverage struct {
	// Enabled: whether to use coverage data for test selection
	Enabled bool `yaml:"enabled" json:"enabled"`
	// LookbackDays: how far back to look for coverage data (default 30)
	LookbackDays int `yaml:"lookbackDays" json:"lookbackDays"`
	// MinHits: minimum hit count to trust a file→test mapping
	MinHits int `yaml:"minHits" json:"minHits"`
	// OnNoCoverage: "expand", "warn", "ignore" - what to do for files without coverage
	OnNoCoverage string `yaml:"onNoCoverage" json:"onNoCoverage"`
	// RetentionDays: prune coverage entries older than this (default 90)
	RetentionDays int `yaml:"retentionDays" json:"retentionDays"`
}

// CIPolicyContracts configures contract/schema change detection
type CIPolicyContracts struct {
	// Enabled: whether to detect contract/schema changes
	Enabled bool `yaml:"enabled" json:"enabled"`
	// OnChange: "add_tests", "expand", "warn" - action when contract changes
	OnChange string `yaml:"onChange" json:"onChange"`
	// Types: which contract types to detect (openapi, protobuf, graphql)
	Types []string `yaml:"types" json:"types"`
	// RetentionRevisions: keep last N revisions of each contract (default 50)
	RetentionRevisions int `yaml:"retentionRevisions" json:"retentionRevisions"`
	// Generated: map of schema input→output globs for generated file tracking
	Generated []CIPolicyGeneratedMapping `yaml:"generated" json:"generated"`
}

// CIPolicyGeneratedMapping maps a schema to its generated outputs
type CIPolicyGeneratedMapping struct {
	Input   string   `yaml:"input" json:"input"`     // Schema file path
	Outputs []string `yaml:"outputs" json:"outputs"` // Generated file globs
}

// DefaultCIPolicy returns a sensible default policy
func DefaultCIPolicy() CIPolicyConfig {
	return CIPolicyConfig{
		Version: 1,
		Thresholds: CIPolicyThresholds{
			MinConfidence:   0.40, // Below 40% = low confidence
			MaxUncertainty:  70,   // Above 70 = high uncertainty
			MaxFilesChanged: 50,   // More than 50 files is suspicious
			MaxTestsSkipped: 0.90, // Skipping >90% of tests is suspicious
		},
		Paranoia: CIPolicyParanoia{
			AlwaysFullPatterns: []string{
				"*.lock",
				"go.mod",
				"go.sum",
				"package.json",
				"Dockerfile",
				".github/workflows/*",
			},
			ExpandOnPatterns: []string{
				"**/config/**",
				"**/setup.*",
				"**/__mocks__/**",
			},
			RiskMultipliers: map[string]float64{
				"src/core/**": 1.5,
				"lib/**":      1.3,
			},
		},
		Behavior: CIPolicyBehavior{
			OnHighRisk:      "expand",
			OnLowConfidence: "expand",
			OnNoTests:       "warn",
			FailOnExpansion: false,
		},
		DynamicImports: CIPolicyDynamicImports{
			Expansion:            "nearest_module", // nearest_module, package, owners, full_suite
			OwnersFallback:       true,
			MaxFilesThreshold:    200,
			BoundedRiskThreshold: 100, // Bounded imports matching >100 files are treated as risky
			Allowlist:            []string{},
			BoundGlobs:           map[string][]string{},
		},
		Coverage: CIPolicyCoverage{
			Enabled:       true,   // Coverage-based selection enabled by default
			LookbackDays:  30,     // Use coverage from last 30 days
			MinHits:       1,      // Trust mappings with at least 1 hit
			OnNoCoverage:  "warn", // Warn but don't expand for files without coverage
			RetentionDays: 90,     // Prune entries older than 90 days
		},
		Contracts: CIPolicyContracts{
			Enabled:            true,                                       // Contract detection enabled by default
			OnChange:           "add_tests",                                // Add registered tests when contracts change
			Types:              []string{"openapi", "protobuf", "graphql"}, // All supported types
			RetentionRevisions: 50,                                         // Keep last 50 revisions per contract
			Generated:          []CIPolicyGeneratedMapping{},               // User-defined schema→outputs
		},
	}
}

// loadCIPolicy loads the CI policy from .kai/rules/ci-policy.yaml or returns defaults
// Falls back to kai.ci-policy.yaml for backwards compatibility
func loadCIPolicy() (CIPolicyConfig, string, error) {
	policy := DefaultCIPolicy()

	// Try primary location first, then fallback
	var data []byte
	var err error
	var usedPath string

	data, err = os.ReadFile(ciPolicyFile)
	if err == nil {
		usedPath = ciPolicyFile
	} else if os.IsNotExist(err) {
		// Try fallback location
		data, err = os.ReadFile(ciPolicyFileFallback)
		if err == nil {
			usedPath = ciPolicyFileFallback
		} else if os.IsNotExist(err) {
			return policy, "", nil // Use defaults, no hash
		}
	}

	if err != nil {
		return policy, "", fmt.Errorf("reading CI policy: %w", err)
	}

	if err := yaml.Unmarshal(data, &policy); err != nil {
		return policy, "", fmt.Errorf("parsing %s: %w", usedPath, err)
	}

	// Compute policy hash for provenance
	hash := sha256.Sum256(data)
	policyHash := hex.EncodeToString(hash[:8]) // First 8 bytes as hex

	return policy, policyHash, nil
}

// Structural risk type constants
const (
	RiskConfigChange      = "config_change"       // package.json, tsconfig, etc changed
	RiskBuildFileChange   = "build_file_change"   // webpack, vite, build configs
	RiskGlobalChange      = "global_change"       // Global state, env vars, shared constants
	RiskDynamicImport     = "dynamic_import"      // Dynamic require/import detected
	RiskReflection        = "reflection"          // Reflection or metaprogramming
	RiskTestInfra         = "test_infra"          // Test helpers, fixtures, mocks changed
	RiskNoTestMapping     = "no_test_mapping"     // Changed files have no test coverage
	RiskCircularDep       = "circular_dependency" // Circular import detected
	RiskNewFile           = "new_file"            // New file with no test coverage
	RiskDeletedFile       = "deleted_file"        // File was deleted
	RiskManyFilesChanged  = "many_files_changed"  // Too many files changed (>threshold)
	RiskCrossModuleChange = "cross_module_change" // Changes span multiple modules
)

// Config file patterns that affect all tests
var configFilePatterns = []string{
	"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"tsconfig.json", "tsconfig.*.json", "jsconfig.json",
	".babelrc", "babel.config.js", "babel.config.json",
	".eslintrc", ".eslintrc.js", ".eslintrc.json",
	"jest.config.js", "jest.config.ts", "jest.config.json",
	"vitest.config.js", "vitest.config.ts",
	"webpack.config.js", "vite.config.js", "vite.config.ts",
	"rollup.config.js", ".env", ".env.*",
	"go.mod", "go.sum", "Cargo.toml", "Cargo.lock",
	"requirements.txt", "setup.py", "pyproject.toml",
	"Makefile", "Dockerfile", "docker-compose.yml",
}

// Test infrastructure patterns
var testInfraPatterns = []string{
	"**/fixtures/**", "**/mocks/**", "**/__mocks__/**",
	"**/testutils/**", "**/test-utils/**", "**/helpers/**",
	"**/setup.*", "**/teardown.*", "**/globalSetup.*",
	"conftest.py", "pytest.ini",
}

// DynamicImportPattern represents a pattern for detecting dynamic imports
type DynamicImportPattern struct {
	pattern    string
	kind       string  // Human-readable kind
	confidence float64 // Base confidence (can be adjusted by context)
	language   string  // js, ts, py, go, or "" for any
}

// Dynamic import patterns that indicate runtime-dependent imports
// These make static analysis unreliable
var dynamicImportPatterns = []DynamicImportPattern{
	// JavaScript/TypeScript - High confidence dynamic imports
	{`require\s*\(\s*[^"'\x60\s]`, "require(variable)", 0.9, "js"},
	{`import\s*\(\s*[^"'\x60\s]`, "import(variable)", 0.9, "js"},
	{`__non_webpack_require__\s*\(`, "webpack bypass", 1.0, "js"},

	// require.resolve - often static but can be dynamic
	{`require\.resolve\s*\(\s*[^"'\x60]`, "require.resolve(variable)", 0.8, "js"},

	// Python - dynamic imports
	{`__import__\s*\(\s*[^"']`, "__import__(variable)", 0.9, "py"},
	{`importlib\.import_module\s*\(\s*[^"']`, "importlib.import_module(variable)", 0.9, "py"},
	{`exec\s*\([^)]*import\s`, "exec(import)", 1.0, "py"},

	// Go - plugin loading
	{`plugin\.Open\s*\(\s*[^"]`, "plugin.Open(variable)", 0.9, "go"},

	// Generic dangerous patterns
	{`eval\s*\([^)]*require`, "eval(require)", 1.0, ""},
	{`eval\s*\([^)]*import`, "eval(import)", 1.0, ""},
}

// False positive patterns - if these match, reduce confidence
var dynamicImportFalsePositives = []struct {
	pattern     string
	description string
	reduction   float64 // Reduce confidence by this amount
}{
	// Constant string concatenation (e.g., require("foo/" + "bar"))
	{`require\s*\(\s*["'\x60][^"'\x60]*["'\x60]\s*\+\s*["'\x60]`, "constant concatenation", 0.8},
	// require.resolve with string literal
	{`require\.resolve\s*\(\s*["'\x60][^"'\x60]+["'\x60]\s*\)`, "require.resolve(literal)", 0.9},
	// path.join with __dirname (commonly static)
	{`require\s*\(\s*path\.join\s*\(\s*__dirname`, "path.join(__dirname)", 0.6},
	// Template literal with only static parts
	{`require\s*\(\s*\x60[^\$\x60]+\x60\s*\)`, "static template literal", 0.9},
}

// Bounding patterns - if these are near a dynamic import, it's bounded
var dynamicImportBounders = []struct {
	pattern     string
	description string
}{
	{`/\*\s*webpackInclude:\s*([^*]+)\*/`, "webpackInclude"},
	{`/\*\s*webpackExclude:\s*([^*]+)\*/`, "webpackExclude"},
	{`/\*\s*webpackChunkName:\s*["']([^"']+)["']\s*\*/`, "webpackChunkName"},
	{`/\*\s*@vite-ignore\s*\*/`, "vite-ignore"},
}

// FileContentReader is a function that reads file content by path
type FileContentReader func(path string) ([]byte, error)

// detectDynamicImports checks if a file contains dynamic import patterns
// Returns simple bool/string for backward compatibility
func detectDynamicImports(content []byte, filePath string) (bool, string) {
	files := detectDynamicImportsDetailed(content, filePath, nil)
	if len(files) > 0 {
		return true, files[0].Kind
	}
	return false, ""
}

// detectDynamicImportsDetailed provides detailed dynamic import detection
// with false positive reduction and bounding detection
func detectDynamicImportsDetailed(content []byte, filePath string, policy *CIPolicyDynamicImports) []DynamicImportFile {
	var results []DynamicImportFile
	contentStr := string(content)
	lines := strings.Split(contentStr, "\n")

	// Determine file language from extension
	ext := strings.ToLower(filepath.Ext(filePath))
	var lang string
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		lang = "js"
	case ".py":
		lang = "py"
	case ".go":
		lang = "go"
	}

	// Check each pattern
	for _, p := range dynamicImportPatterns {
		// Skip patterns for other languages
		if p.language != "" && p.language != lang {
			continue
		}

		re, err := regexp.Compile(p.pattern)
		if err != nil {
			continue
		}

		matches := re.FindAllStringIndex(contentStr, -1)
		for _, match := range matches {
			// Find line number
			lineNum := 1
			for i := range lines {
				if match[0] <= len(strings.Join(lines[:i+1], "\n")) {
					lineNum = i + 1
					break
				}
			}

			// Start with base confidence
			confidence := p.confidence

			// Check for false positive patterns (reduce confidence)
			for _, fp := range dynamicImportFalsePositives {
				fpRe, err := regexp.Compile(fp.pattern)
				if err != nil {
					continue
				}
				// Check in a window around the match
				start := match[0]
				if start > 50 {
					start = match[0] - 50
				}
				end := match[1] + 50
				if end > len(contentStr) {
					end = len(contentStr)
				}
				window := contentStr[start:end]
				if fpRe.MatchString(window) {
					confidence -= fp.reduction
				}
			}

			// Check for bounding patterns BEFORE confidence skip
			// This is important: we only skip low-confidence imports if bounded
			bounded := false
			boundedBy := ""
			for _, b := range dynamicImportBounders {
				bRe, err := regexp.Compile(b.pattern)
				if err != nil {
					continue
				}
				// Check in a window before the match (comments come before)
				start := match[0] - 200
				if start < 0 {
					start = 0
				}
				window := contentStr[start:match[0]]
				if bMatch := bRe.FindStringSubmatch(window); bMatch != nil {
					bounded = true
					if len(bMatch) > 1 {
						boundedBy = fmt.Sprintf("%s: %s", b.description, strings.TrimSpace(bMatch[1]))
					} else {
						boundedBy = b.description
					}
					break
				}
			}

			// Skip ONLY if confidence is very low AND the import is bounded
			// Unbounded imports with low confidence are still risky - include them
			if confidence <= 0.1 && bounded {
				continue
			}

			// Check allowlist
			allowlisted := false
			if policy != nil {
				for _, allow := range policy.Allowlist {
					matched, _ := doublestar.Match(allow, filePath)
					if matched {
						allowlisted = true
						break
					}
				}
			}

			results = append(results, DynamicImportFile{
				Path:        filePath,
				Kind:        p.kind,
				Line:        lineNum,
				Bounded:     bounded,
				BoundedBy:   boundedBy,
				Allowlisted: allowlisted,
				Confidence:  confidence,
			})
		}
	}

	return results
}

// expandForDynamicImports performs scoped expansion based on dynamic import detection
// Uses union model: nearest_module → owners → full_suite (with fallback)
// Returns the list of additional test files to add and expansion info
func expandForDynamicImports(
	detectedImports []DynamicImportFile,
	policy *CIPolicyDynamicImports,
	allTestFiles []string,
	changedFiles []string,
	moduleMappings []ModulePathMapping,
	filesByModule map[string][]string, // map[module name] -> test files in that module
) ([]string, *DynamicImportInfo) {
	info := &DynamicImportInfo{
		Detected: len(detectedImports) > 0,
		Files:    make([]DynamicImportFile, len(detectedImports)),
		Policy: DynamicImportPolicyUsed{
			Expansion:      policy.Expansion,
			OwnersFallback: policy.OwnersFallback,
		},
		Telemetry: DynamicImportTelemetry{
			TotalDetected: len(detectedImports),
		},
	}
	copy(info.Files, detectedImports)

	if len(detectedImports) == 0 {
		return nil, info
	}

	// Classify imports: allowlisted, bounded (safe), bounded-risky, unbounded
	var importsToExpand []DynamicImportFile
	for i, imp := range info.Files {
		if imp.Allowlisted {
			info.Telemetry.Allowlisted++
			continue
		}

		if imp.Bounded {
			// Check if bounded import has a huge footprint (bounded-but-risky)
			if policy.BoundedRiskThreshold > 0 && imp.BoundedBy != "" {
				// Estimate footprint by checking how many files match the bound pattern
				footprint := estimateBoundedFootprint(imp.BoundedBy, allTestFiles)
				if footprint > policy.BoundedRiskThreshold {
					info.Files[i].BoundedRisky = true
					info.Telemetry.BoundedRisky++
					importsToExpand = append(importsToExpand, info.Files[i])
					continue
				}
			}
			info.Telemetry.Bounded++
			continue
		}

		// Unbounded - needs expansion
		info.Telemetry.Unbounded++
		importsToExpand = append(importsToExpand, imp)
	}

	// Nothing to expand
	if len(importsToExpand) == 0 {
		info.Telemetry.StrategyUsed = "none (all safe)"
		return nil, info
	}

	var expandedTests []string
	expandedTestSet := make(map[string]bool)
	strategyUsed := policy.Expansion

	// Union model: try nearest_module first, fall back to owners, then full_suite
	for _, imp := range importsToExpand {
		impTests, impStrategy := expandSingleImport(imp, policy, allTestFiles, moduleMappings, filesByModule)

		// Track what this import expanded to
		for j := range info.Files {
			if info.Files[j].Path == imp.Path && info.Files[j].Line == imp.Line {
				if len(impTests) > 0 {
					info.Files[j].ExpandedTo = impStrategy
				}
				break
			}
		}

		// Add tests to set
		for _, t := range impTests {
			if !expandedTestSet[t] {
				expandedTestSet[t] = true
				expandedTests = append(expandedTests, t)
			}
		}

		// Track if we had to escalate
		if impStrategy == "full_suite" {
			strategyUsed = "full_suite"
		} else if impStrategy == "owners" && strategyUsed != "full_suite" {
			strategyUsed = "owners"
		}
	}

	// Apply MaxFilesThreshold - if expansion exceeds threshold, widen to full suite
	if policy.MaxFilesThreshold > 0 && len(expandedTests) > policy.MaxFilesThreshold {
		expandedTests = allTestFiles
		expandedTestSet = make(map[string]bool)
		for _, t := range allTestFiles {
			expandedTestSet[t] = true
		}
		strategyUsed = "full_suite (threshold exceeded)"
	}

	info.Policy.Expansion = strategyUsed
	info.Policy.ExpandedTo = expandedTests
	info.Telemetry.WidenedTests = len(expandedTests)
	info.Telemetry.StrategyUsed = strategyUsed

	return expandedTests, info
}

// expandSingleImport expands a single dynamic import using the union model
// Returns tests to add and the strategy that was actually used
func expandSingleImport(
	imp DynamicImportFile,
	policy *CIPolicyDynamicImports,
	allTestFiles []string,
	moduleMappings []ModulePathMapping,
	filesByModule map[string][]string,
) ([]string, string) {
	var tests []string
	fileDir := filepath.Dir(imp.Path)

	// Strategy 1: nearest_module
	if policy.Expansion == "nearest_module" || policy.Expansion == "package" || policy.OwnersFallback {
		// Find which module this file belongs to using path prefixes
		var foundModule string
		for _, mod := range moduleMappings {
			for _, prefix := range mod.PathPrefixes {
				if strings.HasPrefix(fileDir, prefix) || strings.HasPrefix(imp.Path, prefix) {
					foundModule = mod.Name
					if modTests, ok := filesByModule[mod.Name]; ok {
						tests = append(tests, modTests...)
					}
					break
				}
			}
			if foundModule != "" {
				break
			}
		}

		if len(tests) > 0 {
			return tests, "nearest_module: " + foundModule
		}
	}

	// Strategy 2: package (same directory)
	if policy.Expansion == "package" || policy.OwnersFallback {
		for _, t := range allTestFiles {
			testDir := filepath.Dir(t)
			if strings.HasPrefix(testDir, fileDir) || testDir == fileDir {
				tests = append(tests, t)
			}
		}

		if len(tests) > 0 {
			return tests, "package: " + fileDir
		}
	}

	// Strategy 3: owners (would parse CODEOWNERS - for now use parent directories)
	if policy.Expansion == "owners" || policy.OwnersFallback {
		// Expand to parent directory tests as a proxy for "team ownership"
		parentDir := filepath.Dir(fileDir)
		for _, t := range allTestFiles {
			testDir := filepath.Dir(t)
			if strings.HasPrefix(testDir, parentDir) {
				tests = append(tests, t)
			}
		}

		if len(tests) > 0 {
			return tests, "owners: " + parentDir
		}
	}

	// Strategy 4: full_suite (nuclear fallback)
	return allTestFiles, "full_suite"
}

// estimateBoundedFootprint estimates how many files a bounded pattern matches
func estimateBoundedFootprint(boundedBy string, allFiles []string) int {
	// Extract the pattern from the boundedBy string (e.g., "webpackInclude: /\.widget\.js$/")
	// For now, do a simple heuristic based on common patterns

	// Check for very broad patterns
	broadPatterns := []string{"**/*", "/**", ".*", ".+"}
	for _, broad := range broadPatterns {
		if strings.Contains(boundedBy, broad) {
			return len(allFiles) // Assume matches everything
		}
	}

	// Check for directory-specific patterns
	if strings.Contains(boundedBy, "/plugins/") || strings.Contains(boundedBy, "/components/") {
		// These are typically large directories
		count := 0
		for _, f := range allFiles {
			if strings.Contains(f, "/plugins/") || strings.Contains(f, "/components/") {
				count++
			}
		}
		return count
	}

	// Default: assume reasonable footprint
	return 10
}

// ModulePathMapping maps module names to their path prefixes for matching
type ModulePathMapping struct {
	Name         string   // Module name (e.g., "App")
	PathPrefixes []string // Extracted path prefixes (e.g., ["src/app", "lib/app"])
}

// extractPathPrefix converts a glob pattern to a path prefix for matching.
// For example: "src/app/**" -> "src/app", "lib/*.js" -> "lib"
func extractPathPrefix(pattern string) string {
	// Remove trailing glob patterns
	prefix := pattern

	// Remove common glob suffixes
	suffixes := []string{"/**", "/*", "**/*", "**"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(prefix, suffix) {
			prefix = strings.TrimSuffix(prefix, suffix)
			break
		}
	}

	// If pattern contains wildcards in the middle, take up to the first wildcard
	if idx := strings.IndexAny(prefix, "*?["); idx != -1 {
		prefix = prefix[:idx]
		// Trim trailing slash if present
		prefix = strings.TrimSuffix(prefix, "/")
	}

	// If we ended up with empty string, use the directory of the pattern
	if prefix == "" {
		prefix = filepath.Dir(pattern)
		if prefix == "." {
			prefix = ""
		}
	}

	return prefix
}

// buildModulePathMappings builds path mappings for modules using their configured glob patterns
func buildModulePathMappings(matcher *module.Matcher, moduleNames []string) []ModulePathMapping {
	var mappings []ModulePathMapping

	for _, name := range moduleNames {
		mod := matcher.GetModule(name)
		if mod == nil {
			continue
		}

		var prefixes []string
		seen := make(map[string]bool)
		for _, pattern := range mod.Paths {
			prefix := extractPathPrefix(pattern)
			if prefix != "" && !seen[prefix] {
				prefixes = append(prefixes, prefix)
				seen[prefix] = true
			}
		}

		if len(prefixes) > 0 {
			mappings = append(mappings, ModulePathMapping{
				Name:         name,
				PathPrefixes: prefixes,
			})
		}
	}

	return mappings
}

// buildModuleTestMap builds a map of module name -> test files in that module
// Uses path prefixes from module configurations for accurate matching
func buildModuleTestMap(testFiles []string, moduleMappings []ModulePathMapping) map[string][]string {
	result := make(map[string][]string)
	for _, mod := range moduleMappings {
		result[mod.Name] = []string{}
	}

	for _, t := range testFiles {
		testDir := filepath.Dir(t)
		for _, mod := range moduleMappings {
			for _, prefix := range mod.PathPrefixes {
				if strings.HasPrefix(testDir, prefix) || strings.HasPrefix(t, prefix) {
					result[mod.Name] = append(result[mod.Name], t)
					break // Only add once per module
				}
			}
		}
	}

	return result
}

// detectStructuralRisks analyzes changed files for patterns that indicate
// higher risk and should trigger expansion in Guarded mode.
func detectStructuralRisks(changedFiles []string, affectedTests map[string]bool, allTestFiles []string, modules []string) []StructuralRisk {
	return detectStructuralRisksWithContent(changedFiles, affectedTests, allTestFiles, modules, nil)
}

// detectStructuralRisksWithContent is like detectStructuralRisks but also checks file content
func detectStructuralRisksWithContent(changedFiles []string, affectedTests map[string]bool, allTestFiles []string, modules []string, readContent FileContentReader) []StructuralRisk {
	var risks []StructuralRisk

	// Check each changed file for risk patterns
	for _, file := range changedFiles {
		basename := filepath.Base(file)

		// Check for config file changes
		for _, pattern := range configFilePatterns {
			matched, _ := filepath.Match(pattern, basename)
			if matched {
				risks = append(risks, StructuralRisk{
					Type:        RiskConfigChange,
					Description: fmt.Sprintf("Config file changed: %s - may affect all tests", file),
					Severity:    "high",
					FilePath:    file,
					Triggered:   true,
				})
				break
			}
		}

		// Check for test infrastructure changes
		for _, pattern := range testInfraPatterns {
			matched, _ := doublestar.Match(pattern, file)
			if matched {
				risks = append(risks, StructuralRisk{
					Type:        RiskTestInfra,
					Description: fmt.Sprintf("Test infrastructure changed: %s - may affect many tests", file),
					Severity:    "high",
					FilePath:    file,
					Triggered:   true,
				})
				break
			}
		}

		// Check if changed file has no test coverage
		if !parse.IsTestFile(file) && !affectedTests[file] {
			hasMapping := false
			for testPath := range affectedTests {
				// Check if any test was found for this file
				if testPath != "" {
					hasMapping = true
					break
				}
			}
			if !hasMapping && len(affectedTests) == 0 {
				risks = append(risks, StructuralRisk{
					Type:        RiskNoTestMapping,
					Description: fmt.Sprintf("No test mapping found for: %s", file),
					Severity:    "medium",
					FilePath:    file,
					Triggered:   false, // Don't auto-expand for individual files
				})
			}
		}

		// Check for dynamic imports if we have a content reader
		if readContent != nil {
			// Only check source files that might have dynamic imports
			ext := strings.ToLower(filepath.Ext(file))
			if ext == ".js" || ext == ".ts" || ext == ".tsx" || ext == ".jsx" ||
				ext == ".mjs" || ext == ".cjs" || ext == ".py" || ext == ".go" {
				content, err := readContent(file)
				if err == nil && len(content) > 0 {
					if hasDynamic, desc := detectDynamicImports(content, file); hasDynamic {
						risks = append(risks, StructuralRisk{
							Type:        RiskDynamicImport,
							Description: fmt.Sprintf("Dynamic import in %s: %s - static analysis may be incomplete", file, desc),
							Severity:    "high",
							FilePath:    file,
							Triggered:   true, // Auto-expand because we can't trust the dependency graph
						})
					}
				}
			}
		}
	}

	// Check for too many files changed
	const manyFilesThreshold = 20
	if len(changedFiles) > manyFilesThreshold {
		risks = append(risks, StructuralRisk{
			Type:        RiskManyFilesChanged,
			Description: fmt.Sprintf("Many files changed (%d) - consider running full suite", len(changedFiles)),
			Severity:    "medium",
			FilePath:    "",
			Triggered:   false, // Warning only, don't auto-expand
		})
	}

	// Check for cross-module changes
	if len(modules) > 2 {
		risks = append(risks, StructuralRisk{
			Type:        RiskCrossModuleChange,
			Description: fmt.Sprintf("Changes span %d modules - increased risk of missed dependencies", len(modules)),
			Severity:    "medium",
			FilePath:    "",
			Triggered:   false, // Warning only
		})
	}

	return risks
}

// calculateConfidence returns a confidence score (0.0-1.0) based on the risk signals
func calculateConfidence(risks []StructuralRisk, testsFound int, changedFiles int) float64 {
	if changedFiles == 0 {
		return 1.0 // No changes = max confidence
	}

	// Start with base confidence
	confidence := 0.8

	// Reduce confidence for each high-severity risk
	for _, risk := range risks {
		switch risk.Severity {
		case "critical":
			confidence -= 0.3
		case "high":
			confidence -= 0.2
		case "medium":
			confidence -= 0.1
		case "low":
			confidence -= 0.05
		}
	}

	// Reduce confidence if no tests found
	if testsFound == 0 {
		confidence -= 0.3
	}

	// Reduce confidence for many changes
	if changedFiles > 10 {
		confidence -= 0.1
	}
	if changedFiles > 20 {
		confidence -= 0.1
	}

	// Clamp to valid range
	if confidence < 0.0 {
		confidence = 0.0
	}
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// shouldExpandForSafety determines if the selection should be expanded based on safety mode and risks
func shouldExpandForSafety(safetyMode string, risks []StructuralRisk, confidence float64, policy CIPolicyConfig) (bool, []string) {
	var reasons []string

	switch safetyMode {
	case "shadow":
		// Shadow mode: never expand, just observe
		return false, nil

	case "strict":
		// Strict mode: never auto-expand (but panic switch can still force full)
		return false, nil

	case "guarded":
		// Use policy thresholds for expansion decisions
		minConfidence := policy.Thresholds.MinConfidence
		if minConfidence == 0 {
			minConfidence = 0.3 // Default fallback
		}

		// Guarded mode: expand if any high-severity triggered risk
		for _, risk := range risks {
			if risk.Triggered && (risk.Severity == "high" || risk.Severity == "critical") {
				reasons = append(reasons, risk.Description)
			}
		}

		// Expand if confidence is below policy threshold
		if confidence < minConfidence {
			reasons = append(reasons, fmt.Sprintf("Low confidence score: %.0f%% (threshold: %.0f%%)", confidence*100, minConfidence*100))
		}

		return len(reasons) > 0, reasons
	}

	return false, nil
}

// checkPanicSwitch checks environment variables and returns true if full run is forced
func checkPanicSwitch() bool {
	// Check for panic switch environment variables
	if os.Getenv("KAI_FORCE_FULL") == "1" || os.Getenv("KAI_FORCE_FULL") == "true" {
		return true
	}
	if os.Getenv("KAI_PANIC") == "1" || os.Getenv("KAI_PANIC") == "true" {
		return true
	}
	return false
}

// getAllTestFiles returns all test files from the file list
func getAllTestFiles(files []*graph.Node) []string {
	var tests []string
	for _, f := range files {
		path, _ := f.Payload["path"].(string)
		if parse.IsTestFile(path) {
			tests = append(tests, path)
		}
	}
	sort.Strings(tests)
	return tests
}

func runCIPlan(cmd *cobra.Command, args []string) error {
	var db *graph.DB
	var err error
	var baseSnapshotID, headSnapshotID []byte
	var changesetID []byte // Track for provenance
	var changedFiles []string
	var matcher *module.Matcher
	var creator *snapshot.Creator
	var cleanupFunc func() // For temp dir cleanup

	// Handle --git-range mode: create ephemeral DB and snapshots from git
	if ciGitRange != "" {
		// Parse BASE..HEAD format
		parts := strings.Split(ciGitRange, "..")
		if len(parts) != 2 {
			return fmt.Errorf("invalid --git-range format: expected BASE..HEAD (e.g., main..feature)")
		}
		gitBase, gitHead := parts[0], parts[1]

		// Create temp database
		tmpDir, err := os.MkdirTemp("", "kai-ci-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		cleanupFunc = func() { os.RemoveAll(tmpDir) }
		defer cleanupFunc()

		dbPath := filepath.Join(tmpDir, "db.sqlite")
		objDir := filepath.Join(tmpDir, "objects")
		os.MkdirAll(objDir, 0755)
		db, err = graph.Open(dbPath, objDir)
		if err != nil {
			return fmt.Errorf("creating temp database: %w", err)
		}
		defer db.Close()

		// Apply schema to temp database
		if err := applyDBSchema(db); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Creating snapshot from git ref: %s\n", gitBase)
		baseSnapshotID, err = createSnapshotFromGitRef(db, ciGitRepo, gitBase)
		if err != nil {
			return fmt.Errorf("creating base snapshot: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Creating snapshot from git ref: %s\n", gitHead)
		headSnapshotID, err = createSnapshotFromGitRef(db, ciGitRepo, gitHead)
		if err != nil {
			return fmt.Errorf("creating head snapshot: %w", err)
		}

		// Create changeset
		fmt.Fprintf(os.Stderr, "Creating changeset...\n")
		changesetID, err = createChangesetFromSnapshots(db, baseSnapshotID, headSnapshotID, "")
		if err != nil {
			return fmt.Errorf("creating changeset: %w", err)
		}

		// Set up matcher and creator for the rest of the function
		matcher, _ = loadMatcher()
		if matcher == nil {
			matcher = module.NewMatcher(nil)
		}
		creator = snapshot.NewCreator(db, matcher)

	} else {
		// Normal mode: use existing .kai database
		db, err = openDB()
		if err != nil {
			return err
		}
		defer db.Close()

		matcher, err = loadMatcher()
		if err != nil {
			return err
		}

		creator = snapshot.NewCreator(db, matcher)

		// Resolve the selector - could be changeset, workspace, or snapshot pair
		// Default to @cs:last if no argument given
		selector := "@cs:last"
		if len(args) > 0 {
			selector = args[0]
		}

		// Try to resolve as changeset first
		// Accept both @cs: prefix and raw hex IDs
		isChangesetSelector := strings.HasPrefix(selector, "@cs:")
		if !isChangesetSelector && len(selector) >= 8 && !strings.HasPrefix(selector, "@") {
			// Try to resolve as raw changeset ID
			if csID, err := util.HexToBytes(selector); err == nil {
				if node, _ := db.GetNode(csID); node != nil && node.Kind == graph.KindChangeSet {
					isChangesetSelector = true
					selector = "@cs:" + selector // Normalize for resolver
				}
			}
		}
		if isChangesetSelector {
			csID, err := resolveChangeSetID(db, selector)
			changesetID = csID // Save for provenance
			if err != nil {
				return fmt.Errorf("resolving changeset: %w", err)
			}

			// Get the changeset's base and head snapshots from payload
			cs, err := db.GetNode(csID)
			if err != nil {
				return fmt.Errorf("getting changeset: %w", err)
			}

			// Get base and head from changeset payload
			// Note: EdgeHas edges point to ChangeType nodes, not snapshots
			if headHex, ok := cs.Payload["head"].(string); ok {
				headSnapshotID, _ = util.HexToBytes(headHex)
			}
			if baseHex, ok := cs.Payload["base"].(string); ok {
				baseSnapshotID, _ = util.HexToBytes(baseHex)
			}
		}

		// If we couldn't resolve snapshots from changeset, try workspace
		if headSnapshotID == nil && strings.HasPrefix(selector, "@ws:") {
			wsName := strings.TrimPrefix(selector, "@ws:")
			mgr := workspace.NewManager(db)
			ws, err := mgr.Get(wsName)
			if err != nil {
				return fmt.Errorf("resolving workspace: %w", err)
			}
			baseSnapshotID = ws.BaseSnapshot
			headSnapshotID = ws.HeadSnapshot
		}

		// Fallback: try as snapshot selector (use with @snap:prev)
		if headSnapshotID == nil {
			headSnapshotID, err = resolveSnapshotID(db, selector)
			if err != nil {
				return fmt.Errorf("could not resolve selector '%s' as changeset, workspace, or snapshot", selector)
			}
			// Try to get previous snapshot as base
			baseSnapshotID, _ = resolveSnapshotID(db, "@snap:prev")
		}

		if headSnapshotID == nil {
			return fmt.Errorf("could not determine head snapshot from selector")
		}
	} // End of else block (normal mode)

	// Get changed files
	changedFiles, err = getChangedFiles(db, creator, baseSnapshotID, headSnapshotID)
	if err != nil {
		return fmt.Errorf("getting changed files: %w", err)
	}

	// Check panic switch first
	panicSwitch := checkPanicSwitch()

	// Load CI policy configuration
	ciPolicy, policyHash, err := loadCIPolicy()
	if err != nil {
		return fmt.Errorf("loading CI policy: %w", err)
	}

	// Build provenance info
	var changesetHex, baseHex, headHex string
	if changesetID != nil {
		changesetHex = util.BytesToHex(changesetID)
	}
	if baseSnapshotID != nil {
		baseHex = util.BytesToHex(baseSnapshotID)
	}
	if headSnapshotID != nil {
		headHex = util.BytesToHex(headSnapshotID)
	}

	// Track which analyzers we use
	analyzersUsed := []string{}

	// Build the plan
	plan := CIPlan{
		Version:    1,
		Mode:       "selective",
		Risk:       "low",
		SafetyMode: ciSafetyMode,
		Confidence: 1.0,
		Targets: CITargets{
			Run:      []string{},
			Skip:     []string{},
			Full:     []string{}, // Will be populated for shadow mode
			Tags:     make(map[string][]string),
			Fallback: ciSafetyMode == "guarded", // Enable fallback in guarded mode
		},
		Impact: CIImpact{
			FilesChanged:    changedFiles,
			SymbolsChanged:  []CISymbolChange{},
			ModulesAffected: []string{},
			Uncertainty:     []string{},
		},
		Policy: CIPolicy{
			Strategy:     ciStrategy,
			Expanded:     false,
			FallbackUsed: "",
		},
		Safety: CISafety{
			StructuralRisks:  []StructuralRisk{},
			Confidence:       1.0,
			RecommendFull:    false,
			PanicSwitch:      panicSwitch,
			AutoExpanded:     false,
			ExpansionReasons: []string{},
		},
		Uncertainty: CIUncertainty{
			Score:   0,
			Sources: []string{},
		},
		ExpansionLog: []string{},
		Provenance: CIProvenance{
			Changeset:       changesetHex,
			Base:            baseHex,
			Head:            headHex,
			KaiVersion:      Version,
			DetectorVersion: DetectorVersion,
			GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
			Analyzers:       analyzersUsed, // Will be populated as we analyze
			PolicyHash:      policyHash,
		},
		Prediction: CIPrediction{},
	}

	// Get all files in the snapshot for graph context (need this for all paths)
	files, err := creator.GetSnapshotFiles(headSnapshotID)
	if err != nil {
		return fmt.Errorf("getting snapshot files: %w", err)
	}

	// Get all test files for full suite reference
	allTestFiles := getAllTestFiles(files)

	// Handle panic switch - forces full run regardless of mode
	if panicSwitch {
		plan.Mode = "full"
		plan.Risk = "low"
		plan.Targets.Run = allTestFiles
		plan.Safety.RecommendFull = true
		plan.Safety.RecommendReason = "Panic switch activated (KAI_FORCE_FULL or KAI_PANIC env var)"

		// For shadow mode, still track prediction data
		if ciSafetyMode == "shadow" {
			plan.Prediction.SelectiveTests = 0 // Would have been 0 before panic
			plan.Prediction.FullTests = len(allTestFiles)
			plan.Prediction.PredictedSavings = 0.0
		}
	} else if len(changedFiles) == 0 {
		// No changes = nothing to do
		plan.Mode = "skip"
		plan.Risk = "low"
		plan.Safety.Confidence = 1.0
	} else {
		// Find affected targets based on strategy
		affectedTargets := make(map[string]bool)
		fallbackUsed := ""

		filePathByID := make(map[string]string)
		fileIDByPath := make(map[string][]byte)
		for _, f := range files {
			path, _ := f.Payload["path"].(string)
			idHex := util.BytesToHex(f.ID)
			filePathByID[idHex] = path
			fileIDByPath[path] = f.ID
		}

		// Try strategies in order: symbols -> imports -> coverage
		strategies := []string{ciStrategy}
		if ciStrategy == "auto" {
			strategies = []string{"symbols", "imports", "coverage"}
		}

		for _, strat := range strategies {
			switch strat {
			case "symbols":
				// Try symbol-level analysis
				analyzersUsed = append(analyzersUsed, "symbols@1")
				// For now, fall through to imports
				continue

			case "imports":
				analyzersUsed = append(analyzersUsed, "imports@1")
				// Use file-level import graph
				for _, changedPath := range changedFiles {
					// Find test files that test this file BY PATH
					// This handles content-addressed ID changes when files are modified
					testsEdges, _ := db.GetEdgesToByPath(changedPath, graph.EdgeTests)
					for _, e := range testsEdges {
						srcHex := util.BytesToHex(e.Src)
						if path, ok := filePathByID[srcHex]; ok {
							affectedTargets[path] = true
						} else {
							// Source file might not be in current snapshot's filePathByID
							// Query the node directly to get its path
							if srcNode, err := db.GetNode(e.Src); err == nil && srcNode != nil {
								if srcPath, ok := srcNode.Payload["path"].(string); ok {
									affectedTargets[srcPath] = true
								}
							}
						}
					}

					// Find files that import this file and are tests (also by path)
					importsEdges, _ := db.GetEdgesToByPath(changedPath, graph.EdgeImports)
					for _, e := range importsEdges {
						srcHex := util.BytesToHex(e.Src)
						if path, ok := filePathByID[srcHex]; ok {
							if parse.IsTestFile(path) {
								affectedTargets[path] = true
							}
						} else {
							// Query node directly
							if srcNode, err := db.GetNode(e.Src); err == nil && srcNode != nil {
								if srcPath, ok := srcNode.Payload["path"].(string); ok {
									if parse.IsTestFile(srcPath) {
										affectedTargets[srcPath] = true
									}
								}
							}
						}
					}
				}

				if len(affectedTargets) > 0 {
					fallbackUsed = "imports"
					break
				}

			case "coverage":
				// Skip if coverage is disabled in policy
				if !ciPolicy.Coverage.Enabled {
					continue
				}

				analyzersUsed = append(analyzersUsed, "coverage@1")
				// Use coverage map to find tests that cover changed files
				coverageMap := loadOrCreateCoverageMap()

				// Check if coverage data is too old based on policy
				lookbackDays := ciPolicy.Coverage.LookbackDays
				if lookbackDays == 0 {
					lookbackDays = 30 // Default
				}

				// Track coverage stats
				filesWithCoverage := 0
				filesWithoutCoverage := 0
				testsFromCoverage := make(map[string]bool)

				for _, changedPath := range changedFiles {
					// Skip test files themselves
					if parse.IsTestFile(changedPath) {
						continue
					}

					// Check if we have coverage data for this file
					entries, hasCoverage := coverageMap.Entries[changedPath]
					if !hasCoverage {
						// Also try relative path variants
						for mapPath, mapEntries := range coverageMap.Entries {
							if strings.HasSuffix(mapPath, changedPath) || strings.HasSuffix(changedPath, mapPath) {
								entries = mapEntries
								hasCoverage = true
								break
							}
						}
					}

					if hasCoverage && len(entries) > 0 {
						filesWithCoverage++
						for _, entry := range entries {
							// Filter by MinHits policy
							if entry.HitCount >= ciPolicy.Coverage.MinHits && entry.TestID != "aggregate" && entry.TestID != "" {
								testsFromCoverage[entry.TestID] = true
							}
						}
					} else {
						filesWithoutCoverage++
					}
				}

				// Add tests from coverage to affected targets
				for testPath := range testsFromCoverage {
					affectedTargets[testPath] = true
				}

				// Build coverage info for the plan
				var coverageAge string
				if coverageMap.IngestedAt != "" {
					coverageAge = coverageMap.IngestedAt
				}

				plan.Coverage = &CoverageInfo{
					Enabled:              true,
					LookbackDays:         lookbackDays,
					FilesWithCoverage:    filesWithCoverage,
					FilesWithoutCoverage: filesWithoutCoverage,
					TestsFromCoverage:    mapKeysToSortedSlice(testsFromCoverage),
					CoverageMapAge:       coverageAge,
				}

				// If files without coverage, increase uncertainty
				if filesWithoutCoverage > 0 {
					plan.Uncertainty.Score += 10 * filesWithoutCoverage
					plan.Uncertainty.Sources = append(plan.Uncertainty.Sources,
						fmt.Sprintf("no_coverage_data:%d_files", filesWithoutCoverage))
				}

				if len(affectedTargets) > 0 {
					fallbackUsed = "coverage"
					break
				}
			}

			if len(affectedTargets) > 0 {
				break
			}
		}

		// === CONTRACT DETECTION ===
		// Check if any changed files are registered contract schemas
		if ciPolicy.Contracts.Enabled {
			contractRegistry := loadOrCreateContractRegistry()
			if len(contractRegistry.Contracts) > 0 {
				var schemasChanged []ContractChange
				testsFromContracts := make(map[string]bool)
				generatedChanged := make(map[string]bool)

				// Build a set of changed file paths for quick lookup
				changedSet := make(map[string]bool)
				for _, p := range changedFiles {
					changedSet[p] = true
				}

				for _, contract := range contractRegistry.Contracts {
					// Check if this contract schema was changed
					if changedSet[contract.Path] {
						// Read current schema and compute digest
						currentDigest := ""
						if data, err := os.ReadFile(contract.Path); err == nil {
							currentDigest = util.Blake3HashHex(data)
						}

						// If digest changed from registered, this is a schema change
						if currentDigest != "" && currentDigest != contract.Digest {
							change := ContractChange{
								Path:         contract.Path,
								Type:         contract.Type,
								Service:      contract.Service,
								DigestBefore: contract.Digest,
								DigestAfter:  currentDigest,
								Tests:        contract.Tests,
							}
							schemasChanged = append(schemasChanged, change)

							// Add registered tests for this contract
							for _, testPath := range contract.Tests {
								testsFromContracts[testPath] = true
								affectedTargets[testPath] = true
							}
						}
					}

					// Check if any generated files from this contract changed
					for _, genPath := range contract.Generated {
						if changedSet[genPath] {
							generatedChanged[genPath] = true
							// Also add the contract tests when generated files change
							for _, testPath := range contract.Tests {
								testsFromContracts[testPath] = true
								affectedTargets[testPath] = true
							}
						}
					}
				}

				// Build contract info for the plan
				if len(schemasChanged) > 0 || len(generatedChanged) > 0 {
					plan.Contracts = &ContractInfo{
						Changed:          len(schemasChanged) > 0,
						SchemasChanged:   schemasChanged,
						TestsFromSchema:  mapKeysToSortedSlice(testsFromContracts),
						GeneratedChanged: mapKeysToSortedSlice(generatedChanged),
					}

					// Contract changes increase risk
					if len(schemasChanged) > 0 {
						plan.Uncertainty.Score += 20
						plan.Uncertainty.Sources = append(plan.Uncertainty.Sources,
							fmt.Sprintf("contract_change:%d_schemas", len(schemasChanged)))
						analyzersUsed = append(analyzersUsed, "contracts@1")
					}
				}
			}
		}

		// Update provenance with analyzers used
		plan.Provenance.Analyzers = analyzersUsed

		plan.Policy.FallbackUsed = fallbackUsed

		// Convert affected targets to sorted list
		for t := range affectedTargets {
			plan.Targets.Run = append(plan.Targets.Run, t)
		}
		sort.Strings(plan.Targets.Run)

		// If no targets found but there are changes, we have uncertainty
		if len(plan.Targets.Run) == 0 && len(changedFiles) > 0 {
			plan.Risk = "medium"
			plan.Impact.Uncertainty = append(plan.Impact.Uncertainty,
				"No test files found for changed files - dependency graph may be incomplete")
			plan.Uncertainty.Score += 30
			plan.Uncertainty.Sources = append(plan.Uncertainty.Sources, "no_test_mapping:present")

			// Apply risk policy (original behavior still applies)
			switch ciRiskPolicy {
			case "expand":
				// Add all test files as targets
				for _, f := range files {
					path, _ := f.Payload["path"].(string)
					if parse.IsTestFile(path) {
						plan.Targets.Run = append(plan.Targets.Run, path)
					}
				}
				sort.Strings(plan.Targets.Run)
				plan.Policy.Expanded = true
				plan.Risk = "low" // Expanded to be safe
				plan.ExpansionLog = append(plan.ExpansionLog,
					fmt.Sprintf("no_test_mapping → expanded to full suite (%d tests)", len(plan.Targets.Run)))

			case "warn":
				plan.Risk = "high"

			case "fail":
				return fmt.Errorf("uncertainty detected and risk-policy is 'fail': no test mappings found for changed files")
			}
		}

		// === SAFETY MODE LOGIC ===

		// Get modules affected for cross-module risk detection
		var modulesAffected []string
		var moduleMappings []ModulePathMapping
		if matcher != nil {
			moduleSet := make(map[string]bool)
			for _, f := range changedFiles {
				modules := matcher.MatchPath(f)
				for _, m := range modules {
					moduleSet[m] = true
				}
			}
			for m := range moduleSet {
				modulesAffected = append(modulesAffected, m)
			}
			sort.Strings(modulesAffected)
			plan.Impact.ModulesAffected = modulesAffected

			// Build path mappings for accurate test matching
			moduleMappings = buildModulePathMappings(matcher, modulesAffected)
		}

		// Create a content reader for dynamic import detection
		// Map path -> digest for quick lookup
		fileDigestByPath := make(map[string]string)
		for _, f := range files {
			if path, ok := f.Payload["path"].(string); ok {
				if digest, ok := f.Payload["digest"].(string); ok {
					fileDigestByPath[path] = digest
				}
			}
		}
		contentReader := func(path string) ([]byte, error) {
			digest, ok := fileDigestByPath[path]
			if !ok {
				return nil, fmt.Errorf("file not found: %s", path)
			}
			return db.ReadObject(digest)
		}

		// Detect structural risks (with content analysis for dynamic imports)
		risks := detectStructuralRisksWithContent(changedFiles, affectedTargets, allTestFiles, modulesAffected, contentReader)
		plan.Safety.StructuralRisks = risks

		// Detect dynamic imports in detail and perform scoped expansion
		// Uses content-addressable caching by file digest for performance
		var allDynamicImports []DynamicImportFile
		var cacheHits, cacheMisses int
		for _, changedPath := range changedFiles {
			digest, hasDigest := fileDigestByPath[changedPath]

			// Try cache first if we have a digest
			if hasDigest {
				if cached, ok := dynamicImportCache.Get(digest); ok {
					cacheHits++
					allDynamicImports = append(allDynamicImports, cached...)
					continue
				}
			}

			// Cache miss - perform detection
			cacheMisses++
			content, err := contentReader(changedPath)
			if err != nil {
				continue
			}
			imports := detectDynamicImportsDetailed(content, changedPath, &ciPolicy.DynamicImports)
			allDynamicImports = append(allDynamicImports, imports...)

			// Store in cache if we have a digest
			if hasDigest {
				dynamicImportCache.Set(digest, imports)
			}
		}

		// Build module -> test map for scoped expansion
		moduleTestMap := buildModuleTestMap(allTestFiles, moduleMappings)

		// Perform scoped expansion based on policy
		dynamicExpandedTests, dynamicImportInfo := expandForDynamicImports(
			allDynamicImports,
			&ciPolicy.DynamicImports,
			allTestFiles,
			changedFiles,
			moduleMappings,
			moduleTestMap,
		)

		// Add dynamically expanded tests to targets
		if len(dynamicExpandedTests) > 0 {
			for _, t := range dynamicExpandedTests {
				if !affectedTargets[t] {
					affectedTargets[t] = true
					plan.Targets.Run = append(plan.Targets.Run, t)
				}
			}
			sort.Strings(plan.Targets.Run)

			// Log expansion
			plan.ExpansionLog = append(plan.ExpansionLog,
				fmt.Sprintf("dynamic_imports (%s) → expanded by %d tests",
					ciPolicy.DynamicImports.Expansion, len(dynamicExpandedTests)))
		}

		// Attach dynamic import info to plan
		plan.DynamicImport = dynamicImportInfo

		// Add cache stats to telemetry
		if plan.DynamicImport != nil {
			plan.DynamicImport.Telemetry.CacheHits = cacheHits
			plan.DynamicImport.Telemetry.CacheMisses = cacheMisses
		}

		// Add uncertainty sources from structural risks
		for _, r := range risks {
			source := fmt.Sprintf("%s:%s", r.Type, r.Severity)
			plan.Uncertainty.Sources = append(plan.Uncertainty.Sources, source)
			switch r.Severity {
			case "critical":
				plan.Uncertainty.Score += 40
			case "high":
				plan.Uncertainty.Score += 25
			case "medium":
				plan.Uncertainty.Score += 10
			case "low":
				plan.Uncertainty.Score += 5
			}
		}
		// Cap uncertainty at 100
		if plan.Uncertainty.Score > 100 {
			plan.Uncertainty.Score = 100
		}

		// Calculate confidence
		confidence := calculateConfidence(risks, len(affectedTargets), len(changedFiles))
		plan.Safety.Confidence = confidence
		plan.Confidence = confidence // Also set top-level confidence

		// Check if we should expand for safety
		shouldExpand, expansionReasons := shouldExpandForSafety(ciSafetyMode, risks, confidence, ciPolicy)

		// Apply safety mode behavior
		switch ciSafetyMode {
		case "shadow":
			// Shadow mode: compute selective plan but include full test list
			// CI should run the full suite and compare results
			plan.Mode = "shadow"
			plan.Targets.Full = allTestFiles

			// Populate prediction data for comparison
			selectiveCount := len(plan.Targets.Run)
			fullCount := len(allTestFiles)
			var savings float64
			if fullCount > 0 {
				savings = float64(fullCount-selectiveCount) / float64(fullCount) * 100
			}
			plan.Prediction = CIPrediction{
				SelectiveTests:   selectiveCount,
				FullTests:        fullCount,
				PredictedSavings: savings,
			}

			// Log what would have been selected
			if len(risks) > 0 {
				plan.Impact.Uncertainty = append(plan.Impact.Uncertainty,
					fmt.Sprintf("Shadow mode: detected %d structural risks - would have expanded in guarded mode", len(risks)))
			}

		case "guarded":
			// Guarded mode: expand if structural risks triggered
			if shouldExpand {
				// Expand to full suite
				plan.Mode = "expanded"
				plan.Targets.Run = allTestFiles
				plan.Safety.AutoExpanded = true
				plan.Safety.ExpansionReasons = expansionReasons
				plan.Safety.RecommendFull = true
				plan.Safety.RecommendReason = strings.Join(expansionReasons, "; ")
				plan.Risk = "low" // Safe because we expanded
				plan.Policy.Expanded = true

				// Add expansion log entries
				for _, reason := range expansionReasons {
					plan.ExpansionLog = append(plan.ExpansionLog,
						fmt.Sprintf("%s → expanded to full suite", reason))
				}
			} else {
				plan.Mode = "selective"
				// Still recommend full if confidence is low but not critical
				if confidence < 0.5 {
					plan.Safety.RecommendFull = true
					plan.Safety.RecommendReason = fmt.Sprintf("Low confidence (%.0f%%) - consider running full suite", confidence*100)
				}
			}

		case "strict":
			// Strict mode: never auto-expand, just warn
			plan.Mode = "selective"
			plan.Targets.Fallback = false // Disable automatic fallback

			// Still populate warnings
			if shouldExpand {
				plan.Safety.RecommendFull = true
				plan.Safety.RecommendReason = fmt.Sprintf("Strict mode: %d triggered risks detected but not expanding (use KAI_FORCE_FULL=1 for full suite)", len(expansionReasons))
				for _, reason := range expansionReasons {
					plan.Impact.Uncertainty = append(plan.Impact.Uncertainty,
						fmt.Sprintf("Risk detected (not expanding in strict mode): %s", reason))
				}
			}
		}

		// Adjust risk level based on safety analysis
		if len(risks) > 0 && plan.Risk == "low" {
			hasHighRisk := false
			for _, r := range risks {
				if r.Severity == "high" || r.Severity == "critical" {
					hasHighRisk = true
					break
				}
			}
			if hasHighRisk && !plan.Safety.AutoExpanded {
				plan.Risk = "medium"
			}
		}
	}

	// Output the plan
	planJSON, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling plan: %w", err)
	}

	// Write to file if specified
	if ciOutFile != "" {
		if err := os.WriteFile(ciOutFile, planJSON, 0644); err != nil {
			return fmt.Errorf("writing plan file: %w", err)
		}
		fmt.Printf("Plan written to %s\n", ciOutFile)
	}

	// Handle --explain flag for human-readable output
	if ciExplain {
		// First show concept explanations
		ctx := explain.ExplainCIPlan(
			args[0],
			plan.Policy.Strategy,
			len(plan.Impact.FilesChanged),
			len(plan.Targets.Run),
		)
		ctx.Print(os.Stdout)
		// Then show the detailed table
		printExplainTable(plan)
		return nil
	}

	// Also print if --json flag or no output file
	if jsonFlag || ciOutFile == "" {
		fmt.Println(string(planJSON))
	} else {
		// Print summary
		fmt.Printf("\nCI Plan Summary:\n")
		fmt.Printf("  Mode: %s\n", plan.Mode)
		fmt.Printf("  Safety Mode: %s\n", plan.SafetyMode)
		fmt.Printf("  Risk: %s\n", plan.Risk)
		fmt.Printf("  Confidence: %.0f%%\n", plan.Safety.Confidence*100)
		fmt.Printf("  Strategy: %s", plan.Policy.Strategy)
		if plan.Policy.FallbackUsed != "" {
			fmt.Printf(" (used: %s)", plan.Policy.FallbackUsed)
		}
		fmt.Println()
		fmt.Printf("  Files changed: %d\n", len(plan.Impact.FilesChanged))
		fmt.Printf("  Targets to run: %d\n", len(plan.Targets.Run))
		if len(plan.Targets.Full) > 0 {
			fmt.Printf("  Full suite size: %d\n", len(plan.Targets.Full))
		}
		if plan.Policy.Expanded || plan.Safety.AutoExpanded {
			fmt.Printf("  (Expanded for safety)\n")
		}
		if plan.Safety.PanicSwitch {
			fmt.Printf("  PANIC SWITCH: Full suite forced via env var\n")
		}
		if len(plan.Safety.StructuralRisks) > 0 {
			fmt.Printf("  Structural risks: %d\n", len(plan.Safety.StructuralRisks))
		}
		// Dynamic import telemetry - detailed output
		if plan.DynamicImport != nil && plan.DynamicImport.Detected {
			fmt.Printf("\n  Dynamic Imports:\n")
			fmt.Printf("    Detected: %d total\n", plan.DynamicImport.Telemetry.TotalDetected)
			if plan.DynamicImport.Telemetry.Bounded > 0 {
				fmt.Printf("    Bounded (safe): %d\n", plan.DynamicImport.Telemetry.Bounded)
			}
			if plan.DynamicImport.Telemetry.BoundedRisky > 0 {
				fmt.Printf("    Bounded (risky footprint): %d\n", plan.DynamicImport.Telemetry.BoundedRisky)
			}
			if plan.DynamicImport.Telemetry.Unbounded > 0 {
				fmt.Printf("    Unbounded: %d\n", plan.DynamicImport.Telemetry.Unbounded)
			}
			if plan.DynamicImport.Telemetry.Allowlisted > 0 {
				fmt.Printf("    Allowlisted: %d\n", plan.DynamicImport.Telemetry.Allowlisted)
			}

			// Show per-file details for unbounded/risky imports
			for _, imp := range plan.DynamicImport.Files {
				if !imp.Bounded || imp.BoundedRisky {
					if imp.Allowlisted {
						continue
					}
					status := "unbounded"
					if imp.BoundedRisky {
						status = "bounded-risky"
					}
					fmt.Printf("    - %s:%d (%s) [%s]", imp.Path, imp.Line, imp.Kind, status)
					if imp.ExpandedTo != "" {
						fmt.Printf(" → %s", imp.ExpandedTo)
					}
					fmt.Println()
				}
			}

			if plan.DynamicImport.Telemetry.WidenedTests > 0 {
				fmt.Printf("    Tests widened: %d (strategy: %s)\n",
					plan.DynamicImport.Telemetry.WidenedTests, plan.DynamicImport.Telemetry.StrategyUsed)
			}
			// Show cache performance stats if any caching occurred
			if plan.DynamicImport.Telemetry.CacheHits > 0 || plan.DynamicImport.Telemetry.CacheMisses > 0 {
				total := plan.DynamicImport.Telemetry.CacheHits + plan.DynamicImport.Telemetry.CacheMisses
				hitRate := float64(plan.DynamicImport.Telemetry.CacheHits) / float64(total) * 100
				fmt.Printf("    Cache: %d/%d hits (%.0f%%)\n",
					plan.DynamicImport.Telemetry.CacheHits, total, hitRate)
			}
		}
		if plan.Safety.RecommendFull && !plan.Safety.AutoExpanded {
			fmt.Printf("  WARNING: %s\n", plan.Safety.RecommendReason)
		}
		// Shadow mode specific output
		if plan.Mode == "shadow" {
			fmt.Printf("\n  Shadow Mode Analysis:\n")
			fmt.Printf("    Selective would run: %d tests\n", plan.Prediction.SelectiveTests)
			fmt.Printf("    Full suite: %d tests\n", plan.Prediction.FullTests)
			fmt.Printf("    Predicted savings: %.1f%%\n", plan.Prediction.PredictedSavings)
		}
	}

	// Fail-closed: exit non-zero if no tests selected and risk is not low
	// This prevents silently passing CI when test selection fails
	if len(plan.Targets.Run) == 0 && plan.Risk != "low" {
		return fmt.Errorf("fail-closed: no tests selected but risk level is '%s' (uncertainty: %d%%)", plan.Risk, plan.Uncertainty.Score)
	}

	// Also fail if policy says to fail on expansion
	if ciPolicy.Behavior.FailOnExpansion && plan.Safety.AutoExpanded {
		return fmt.Errorf("fail-closed: expansion occurred and policy.behavior.failOnExpansion is true")
	}

	return nil
}

// printExplainTable outputs a human-readable why-in/why-out table
func printExplainTable(plan CIPlan) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                           CI TEST SELECTION PLAN                             ║")
	fmt.Printf("╠══════════════════════════════════════════════════════════════════════════════╣\n")
	fmt.Printf("║ Mode: %-12s  Safety: %-10s  Risk: %-8s  Confidence: %3.0f%% ║\n",
		plan.Mode, plan.SafetyMode, plan.Risk, plan.Confidence*100)
	fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")

	// Changes section
	fmt.Println("║ CHANGES                                                                      ║")
	fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
	for _, f := range plan.Impact.FilesChanged {
		if len(f) > 74 {
			f = "..." + f[len(f)-71:]
		}
		fmt.Printf("║   %-75s║\n", f)
	}
	if len(plan.Impact.FilesChanged) == 0 {
		fmt.Println("║   (no changes)                                                               ║")
	}

	// Tests to run section
	fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ TESTS TO RUN (%d)                                                             ║\n", len(plan.Targets.Run))
	fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
	for i, t := range plan.Targets.Run {
		if i >= 10 {
			fmt.Printf("║   ... and %d more                                                            ║\n", len(plan.Targets.Run)-10)
			break
		}
		if len(t) > 74 {
			t = "..." + t[len(t)-71:]
		}
		fmt.Printf("║   %-75s║\n", t)
	}
	if len(plan.Targets.Run) == 0 {
		fmt.Println("║   (none selected)                                                            ║")
	}

	// Tests skipped section
	if len(plan.Targets.Skip) > 0 {
		fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
		fmt.Printf("║ TESTS SKIPPED (%d)                                                            ║\n", len(plan.Targets.Skip))
		fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
		for i, t := range plan.Targets.Skip {
			if i >= 5 {
				fmt.Printf("║   ... and %d more                                                            ║\n", len(plan.Targets.Skip)-5)
				break
			}
			if len(t) > 74 {
				t = "..." + t[len(t)-71:]
			}
			fmt.Printf("║   %-75s║\n", t)
		}
	}

	// Risks section
	if len(plan.Safety.StructuralRisks) > 0 {
		fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
		fmt.Println("║ STRUCTURAL RISKS                                                             ║")
		fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
		for _, r := range plan.Safety.StructuralRisks {
			triggered := " "
			if r.Triggered {
				triggered = "!"
			}
			severity := fmt.Sprintf("[%s]", r.Severity)
			desc := r.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			fmt.Printf("║ %s %-8s %-64s║\n", triggered, severity, desc)
		}
	}

	// Dynamic imports section
	if plan.DynamicImport != nil && plan.DynamicImport.Detected {
		fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
		fmt.Printf("║ DYNAMIC IMPORTS (detected: %d, bounded: %d, unbounded: %d)                    ║\n",
			plan.DynamicImport.Telemetry.TotalDetected,
			plan.DynamicImport.Telemetry.Bounded,
			plan.DynamicImport.Telemetry.Unbounded)
		fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
		for i, imp := range plan.DynamicImport.Files {
			if i >= 5 {
				fmt.Printf("║   ... and %d more                                                            ║\n", len(plan.DynamicImport.Files)-5)
				break
			}
			status := "⚠"
			if imp.Bounded {
				status = "✓"
			} else if imp.Allowlisted {
				status = "○"
			}
			desc := fmt.Sprintf("%s %s:%d (%s)", status, imp.Path, imp.Line, imp.Kind)
			if len(desc) > 74 {
				desc = desc[:71] + "..."
			}
			fmt.Printf("║   %-75s║\n", desc)
		}
		fmt.Printf("║   Strategy: %-65s║\n", plan.DynamicImport.Policy.Expansion)
		if plan.DynamicImport.Telemetry.WidenedTests > 0 {
			fmt.Printf("║   Widened tests: %-60d║\n", plan.DynamicImport.Telemetry.WidenedTests)
		}
	}

	// Expansion log section
	if len(plan.ExpansionLog) > 0 {
		fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
		fmt.Println("║ EXPANSION LOG                                                                ║")
		fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
		for _, entry := range plan.ExpansionLog {
			if len(entry) > 74 {
				entry = entry[:71] + "..."
			}
			fmt.Printf("║   %-75s║\n", entry)
		}
	}

	// Uncertainty section
	if plan.Uncertainty.Score > 0 {
		fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
		fmt.Printf("║ UNCERTAINTY SCORE: %d/100                                                     ║\n", plan.Uncertainty.Score)
		fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
		for _, s := range plan.Uncertainty.Sources {
			if len(s) > 74 {
				s = s[:71] + "..."
			}
			fmt.Printf("║   %-75s║\n", s)
		}
	}

	// Provenance section
	fmt.Println("╠══════════════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║ PROVENANCE                                                                   ║")
	fmt.Println("╟──────────────────────────────────────────────────────────────────────────────╢")
	fmt.Printf("║   Generated: %-63s║\n", plan.Provenance.GeneratedAt)
	fmt.Printf("║   Kai Version: %-61s║\n", plan.Provenance.KaiVersion)
	if plan.Provenance.Changeset != "" {
		cs := plan.Provenance.Changeset
		if len(cs) > 16 {
			cs = cs[:16]
		}
		fmt.Printf("║   Changeset: %-63s║\n", cs)
	}
	if plan.Provenance.PolicyHash != "" {
		fmt.Printf("║   Policy Hash: %-61s║\n", plan.Provenance.PolicyHash)
	}
	if len(plan.Provenance.Analyzers) > 0 {
		fmt.Printf("║   Analyzers: %-63s║\n", strings.Join(plan.Provenance.Analyzers, ", "))
	}

	fmt.Println("╚══════════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func runCIPrint(cmd *cobra.Command, args []string) error {
	// Read plan file
	data, err := os.ReadFile(ciPlanFile)
	if err != nil {
		return fmt.Errorf("reading plan file: %w", err)
	}

	var plan CIPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return fmt.Errorf("parsing plan file: %w", err)
	}

	if jsonFlag {
		// Output as JSON
		output, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(output))
		return nil
	}

	switch ciSection {
	case "targets":
		fmt.Println("Targets to run:")
		if len(plan.Targets.Run) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, t := range plan.Targets.Run {
				fmt.Printf("  %s\n", t)
			}
		}
		if len(plan.Targets.Skip) > 0 {
			fmt.Println("\nTargets to skip:")
			for _, t := range plan.Targets.Skip {
				fmt.Printf("  %s\n", t)
			}
		}

	case "impact":
		fmt.Println("Impact:")
		fmt.Printf("  Files changed: %d\n", len(plan.Impact.FilesChanged))
		for _, f := range plan.Impact.FilesChanged {
			fmt.Printf("    %s\n", f)
		}
		if len(plan.Impact.SymbolsChanged) > 0 {
			fmt.Printf("\n  Symbols changed: %d\n", len(plan.Impact.SymbolsChanged))
			for _, s := range plan.Impact.SymbolsChanged {
				fmt.Printf("    %s (%s)\n", s.FQ, s.Change)
			}
		}
		if len(plan.Impact.Uncertainty) > 0 {
			fmt.Println("\n  Uncertainty:")
			for _, u := range plan.Impact.Uncertainty {
				fmt.Printf("    - %s\n", u)
			}
		}

	case "safety":
		fmt.Println("Safety Analysis")
		fmt.Println(strings.Repeat("-", 40))
		fmt.Printf("Safety Mode:  %s\n", plan.SafetyMode)
		fmt.Printf("Confidence:   %.0f%%\n", plan.Safety.Confidence*100)
		fmt.Printf("Panic Switch: %v\n", plan.Safety.PanicSwitch)
		fmt.Printf("Auto Expanded: %v\n", plan.Safety.AutoExpanded)
		if plan.Safety.RecommendFull {
			fmt.Printf("Recommend Full: yes\n")
			fmt.Printf("Reason: %s\n", plan.Safety.RecommendReason)
		}
		if len(plan.Safety.StructuralRisks) > 0 {
			fmt.Printf("\nStructural Risks: %d\n", len(plan.Safety.StructuralRisks))
			for _, r := range plan.Safety.StructuralRisks {
				triggered := ""
				if r.Triggered {
					triggered = " [TRIGGERED]"
				}
				fmt.Printf("  [%s] %s%s\n", r.Severity, r.Description, triggered)
			}
		}
		if len(plan.Safety.ExpansionReasons) > 0 {
			fmt.Println("\nExpansion Reasons:")
			for _, reason := range plan.Safety.ExpansionReasons {
				fmt.Printf("  - %s\n", reason)
			}
		}
		// Shadow mode prediction
		if plan.Mode == "shadow" {
			fmt.Println("\nPrediction (Shadow Mode):")
			fmt.Printf("  Selective would run: %d tests\n", plan.Prediction.SelectiveTests)
			fmt.Printf("  Full suite: %d tests\n", plan.Prediction.FullTests)
			fmt.Printf("  Predicted savings: %.1f%%\n", plan.Prediction.PredictedSavings)
		}

	case "causes":
		fmt.Println("Test Selection Root Causes")
		fmt.Println(strings.Repeat("-", 40))

		// Build cause map: test -> reasons
		causeMap := make(map[string][]string)

		// 1. Direct changes - tests that are themselves in the changed files
		for _, f := range plan.Impact.FilesChanged {
			for _, t := range plan.Targets.Run {
				if t == f || strings.HasSuffix(f, t) || strings.HasSuffix(t, f) {
					causeMap[t] = append(causeMap[t], fmt.Sprintf("directly changed: %s", f))
				}
			}
		}

		// 2. Symbol-level impact
		for _, sym := range plan.Impact.SymbolsChanged {
			for _, t := range plan.Targets.Run {
				// Check if test imports/depends on this symbol
				if strings.Contains(sym.FQ, filepath.Dir(t)) {
					causeMap[t] = append(causeMap[t], fmt.Sprintf("symbol changed: %s (%s)", sym.FQ, sym.Change))
				}
			}
		}

		// 3. Expansion log entries
		for _, log := range plan.ExpansionLog {
			// Parse expansion log: "reason → tests..."
			parts := strings.SplitN(log, " → ", 2)
			if len(parts) == 2 {
				reason := parts[0]
				// This expansion reason applies to added tests
				for _, t := range plan.Targets.Run {
					if len(causeMap[t]) == 0 {
						causeMap[t] = append(causeMap[t], fmt.Sprintf("expansion: %s", reason))
					}
				}
			}
		}

		// 4. Dynamic import causes
		if plan.DynamicImport != nil && plan.DynamicImport.Detected {
			for _, imp := range plan.DynamicImport.Files {
				if imp.ExpandedTo != "" {
					// ExpandedTo might be a module or test pattern
					for _, t := range plan.Targets.Run {
						if strings.Contains(t, imp.ExpandedTo) || strings.HasPrefix(t, imp.ExpandedTo) {
							status := "unbounded"
							if imp.BoundedRisky {
								status = "bounded-risky"
							}
							causeMap[t] = append(causeMap[t],
								fmt.Sprintf("dynamic import in %s:%d (%s) [%s]",
									imp.Path, imp.Line, imp.Kind, status))
						}
					}
				}
			}
		}

		// 5. Structural risks that triggered expansion
		for _, r := range plan.Safety.StructuralRisks {
			if r.Triggered {
				for _, t := range plan.Targets.Run {
					if len(causeMap[t]) == 0 {
						causeMap[t] = append(causeMap[t],
							fmt.Sprintf("structural risk: %s (%s)", r.Type, r.Severity))
					}
				}
			}
		}

		// 6. Auto-expansion reasons
		if plan.Safety.AutoExpanded {
			for _, reason := range plan.Safety.ExpansionReasons {
				for _, t := range plan.Targets.Run {
					if len(causeMap[t]) == 0 {
						causeMap[t] = append(causeMap[t], fmt.Sprintf("safety expansion: %s", reason))
					}
				}
			}
		}

		// Output organized by test
		if len(plan.Targets.Run) == 0 {
			fmt.Println("  No tests selected.")
		} else {
			for _, t := range plan.Targets.Run {
				fmt.Printf("\n  %s\n", t)
				causes := causeMap[t]
				if len(causes) == 0 {
					fmt.Println("    → dependency graph traversal (inferred)")
				} else {
					// Deduplicate causes
					seen := make(map[string]bool)
					for _, c := range causes {
						if !seen[c] {
							seen[c] = true
							fmt.Printf("    → %s\n", c)
						}
					}
				}
			}
		}

		// Show if full suite was triggered
		if plan.Safety.PanicSwitch {
			fmt.Println("\n  FULL SUITE TRIGGERED (panic switch)")
		}

	case "summary":
		fallthrough
	default:
		// One-line summary for quick reading
		coverageTests := 0
		contractTests := 0
		if plan.Coverage != nil {
			coverageTests = len(plan.Coverage.TestsFromCoverage)
		}
		if plan.Contracts != nil {
			contractTests = len(plan.Contracts.TestsFromSchema)
		}
		fallbackStatus := "used=false"
		if plan.Fallback.Used {
			fallbackStatus = fmt.Sprintf("used=true (%s)", plan.Fallback.Reason)
		}
		fmt.Printf("Mode: %s | Risk: %s (score %d) | Fallback: %s | Coverage: %d tests | Contracts: %d tests\n\n",
			plan.SafetyMode, plan.Risk, plan.Uncertainty.Score, fallbackStatus, coverageTests, contractTests)

		fmt.Println("CI Plan Summary")
		fmt.Println(strings.Repeat("-", 40))
		fmt.Printf("Mode:       %s\n", plan.Mode)
		fmt.Printf("Safety:     %s\n", plan.SafetyMode)
		fmt.Printf("Risk:       %s\n", plan.Risk)
		fmt.Printf("Confidence: %.0f%%\n", plan.Safety.Confidence*100)
		fmt.Printf("Strategy:   %s\n", plan.Policy.Strategy)
		if plan.Policy.FallbackUsed != "" {
			fmt.Printf("Used:       %s\n", plan.Policy.FallbackUsed)
		}
		if plan.Policy.Expanded || plan.Safety.AutoExpanded {
			fmt.Printf("Expanded:   yes (for safety)\n")
		}
		if plan.Safety.PanicSwitch {
			fmt.Printf("Panic:      FULL SUITE FORCED\n")
		}
		fmt.Println()
		fmt.Printf("Changed:    %d files\n", len(plan.Impact.FilesChanged))
		fmt.Printf("Run:        %d targets\n", len(plan.Targets.Run))
		fmt.Printf("Skip:       %d targets\n", len(plan.Targets.Skip))
		if len(plan.Targets.Full) > 0 {
			fmt.Printf("Full Suite: %d targets\n", len(plan.Targets.Full))
		}

		if len(plan.Safety.StructuralRisks) > 0 {
			fmt.Printf("\nRisks:      %d detected\n", len(plan.Safety.StructuralRisks))
		}

		if plan.Safety.RecommendFull && !plan.Safety.AutoExpanded {
			fmt.Printf("\nWARNING: %s\n", plan.Safety.RecommendReason)
		}

		if len(plan.Impact.Uncertainty) > 0 {
			fmt.Printf("\nWarnings: %d\n", len(plan.Impact.Uncertainty))
			for _, u := range plan.Impact.Uncertainty {
				fmt.Printf("  - %s\n", u)
			}
		}

		// Shadow mode prediction summary
		if plan.Mode == "shadow" {
			fmt.Println("\nShadow Mode:")
			fmt.Printf("  Would save: %.1f%% (%d of %d tests)\n",
				plan.Prediction.PredictedSavings,
				plan.Prediction.FullTests-plan.Prediction.SelectiveTests,
				plan.Prediction.FullTests)
		}

		// Coverage info
		if plan.Coverage != nil && plan.Coverage.Enabled {
			fmt.Println("\nCoverage:")
			fmt.Printf("  Files with coverage:    %d\n", plan.Coverage.FilesWithCoverage)
			fmt.Printf("  Files without coverage: %d\n", plan.Coverage.FilesWithoutCoverage)
			if len(plan.Coverage.TestsFromCoverage) > 0 {
				fmt.Printf("  Tests from coverage:    %d\n", len(plan.Coverage.TestsFromCoverage))
			}
		}

		// Contracts info
		if plan.Contracts != nil && plan.Contracts.Changed {
			fmt.Println("\nContracts:")
			fmt.Printf("  Schemas changed: %d\n", len(plan.Contracts.SchemasChanged))
			for _, sc := range plan.Contracts.SchemasChanged {
				fmt.Printf("    - %s (%s)\n", sc.Path, sc.Type)
			}
			if len(plan.Contracts.TestsFromSchema) > 0 {
				fmt.Printf("  Tests from contracts: %d\n", len(plan.Contracts.TestsFromSchema))
			}
		}

		// Fallback status
		if plan.Fallback.Used {
			fmt.Println("\nFallback:")
			fmt.Printf("  Used:   true\n")
			fmt.Printf("  Reason: %s\n", plan.Fallback.Reason)
			if plan.Fallback.Trigger != "" {
				fmt.Printf("  Trigger: %s\n", plan.Fallback.Trigger)
			}
			if plan.Fallback.ExitCode != 0 {
				fmt.Printf("  Exit Code: %d\n", plan.Fallback.ExitCode)
			}
		}
	}

	return nil
}

// RuntimeRiskReport represents the output of detect-runtime-risk
type RuntimeRiskReport struct {
	RisksDetected     bool                `json:"risksDetected"`
	TotalRisks        int                 `json:"totalRisks"`
	TripwireTriggered bool                `json:"tripwireTriggered"`
	Risks             []RuntimeRiskSignal `json:"risks"`
	Recommendation    string              `json:"recommendation"`
}

// RuntimeRiskSignal represents a single detected runtime risk
type RuntimeRiskSignal struct {
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
}

// Runtime risk signal types
const (
	RuntimeRiskModuleNotFound = "module_not_found"
	RuntimeRiskImportError    = "import_error"
	RuntimeRiskTypeError      = "type_error"
	RuntimeRiskSetupCrash     = "setup_crash"
	RuntimeRiskCoverageAnomly = "coverage_anomaly"
	RuntimeRiskUnexpectedFail = "unexpected_failure"
)

// Patterns to detect runtime risks in test output
var runtimeRiskPatterns = []struct {
	pattern     string
	riskType    string
	severity    string
	description string
}{
	// ===== Node.js / JavaScript =====
	{`Cannot find module ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Module not found - likely selection miss"},
	{`Error: Cannot find module`, RuntimeRiskModuleNotFound, "critical", "Module not found - likely selection miss"},
	{`Module not found: Error: Can't resolve ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Webpack module resolution failed"},
	{`Error: Cannot resolve module ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Module resolution failed"},
	{`SyntaxError: Cannot use import statement`, RuntimeRiskImportError, "high", "ES module import error"},
	{`ReferenceError: (\w+) is not defined`, RuntimeRiskImportError, "high", "Reference error - possible missing import"},
	{`TypeError: (\w+) is not a function`, RuntimeRiskImportError, "high", "Type error - possible missing export"},
	{`TypeError: Cannot read propert(?:y|ies) of undefined`, RuntimeRiskImportError, "high", "Undefined access - possible missing dependency"},

	// ===== TypeScript =====
	{`error TS2307:.*Cannot find module`, RuntimeRiskModuleNotFound, "critical", "TypeScript module not found"},
	{`error TS2305:.*has no exported member`, RuntimeRiskImportError, "critical", "TypeScript export missing"},
	{`error TS2339:.*does not exist on type`, RuntimeRiskTypeError, "high", "TypeScript property missing"},
	{`error TS\d+:`, RuntimeRiskTypeError, "high", "TypeScript compilation error"},
	{`Cannot find name ['"](\w+)['"]`, RuntimeRiskTypeError, "medium", "TypeScript name resolution failed"},
	// Type error burst detection (3+ errors = critical)
	{`Found \d+ errors?`, RuntimeRiskTypeError, "high", "TypeScript found errors"},

	// ===== Python =====
	{`ModuleNotFoundError: No module named ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Python module not found"},
	{`ImportError: cannot import name ['"]([^'"]+)['"]`, RuntimeRiskImportError, "critical", "Python import name error"},
	{`ImportError: No module named ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Python import error"},
	{`AttributeError: module ['"]([^'"]+)['"] has no attribute`, RuntimeRiskImportError, "high", "Python attribute missing"},
	// Python importlib errors
	{`importlib\..*Error`, RuntimeRiskImportError, "critical", "Python importlib error"},
	{`ModuleSpec.*not found`, RuntimeRiskModuleNotFound, "critical", "Python module spec not found"},
	{`spec_from_file_location.*failed`, RuntimeRiskImportError, "critical", "Python dynamic import failed"},
	{`__import__.*failed`, RuntimeRiskImportError, "critical", "Python __import__ failed"},
	// Python fixture/setup errors
	{`fixture ['"](\w+)['"] not found`, RuntimeRiskSetupCrash, "critical", "pytest fixture not found"},
	{`E\s+ModuleNotFoundError`, RuntimeRiskModuleNotFound, "critical", "pytest module not found"},
	{`ERRORS.*collection`, RuntimeRiskSetupCrash, "critical", "pytest collection errors"},

	// ===== Go =====
	{`undefined: (\w+)`, RuntimeRiskImportError, "high", "Go undefined symbol"},
	{`cannot find package ['"]([^'"]+)['"]`, RuntimeRiskModuleNotFound, "critical", "Go package not found"},
	{`no required module provides package`, RuntimeRiskModuleNotFound, "critical", "Go module missing"},
	// Go plugin load failures
	{`plugin\.Open.*failed`, RuntimeRiskImportError, "critical", "Go plugin load failed"},
	{`plugin: symbol .* not found`, RuntimeRiskImportError, "critical", "Go plugin symbol not found"},
	{`plugin was built with a different version`, RuntimeRiskImportError, "critical", "Go plugin version mismatch"},
	// Go test failures
	{`panic: .*nil pointer`, RuntimeRiskUnexpectedFail, "high", "Go nil pointer panic"},
	{`FAIL\s+[\w/.]+\s+\[build failed\]`, RuntimeRiskSetupCrash, "critical", "Go build failed"},

	// ===== Jest / JavaScript Test Runners =====
	{`beforeAll.*failed|beforeEach.*failed`, RuntimeRiskSetupCrash, "critical", "Test setup hook failed"},
	{`afterAll.*failed|afterEach.*failed`, RuntimeRiskSetupCrash, "high", "Test teardown hook failed"},
	{`Test suite failed to run`, RuntimeRiskSetupCrash, "critical", "Test suite failed to initialize"},
	{`Jest encountered an unexpected token`, RuntimeRiskSetupCrash, "critical", "Jest parse error - config issue"},
	{`Cannot find module.*from.*\.test\.[jt]sx?`, RuntimeRiskModuleNotFound, "critical", "Test file import failed"},
	{`Your test suite must contain at least one test`, RuntimeRiskSetupCrash, "high", "Empty test suite - possible selection miss"},
	{`RUNS.*0 passed`, RuntimeRiskUnexpectedFail, "medium", "All tests failed"},
	// Jest environment errors
	{`Test environment.*not found`, RuntimeRiskSetupCrash, "critical", "Jest environment not found"},
	{`jest-environment-.*not installed`, RuntimeRiskSetupCrash, "critical", "Jest environment missing"},
	{`Could not locate module.*mapped as`, RuntimeRiskModuleNotFound, "critical", "Jest module mapping failed"},

	// ===== Mocha =====
	{`Error: Cannot find module.*mocha`, RuntimeRiskSetupCrash, "critical", "Mocha module error"},
	{`Error \[ERR_MODULE_NOT_FOUND\]`, RuntimeRiskModuleNotFound, "critical", "ESM module not found"},

	// ===== Generic / Cross-platform =====
	{`ENOENT.*no such file or directory`, RuntimeRiskModuleNotFound, "high", "File not found at runtime"},
	{`ENOENT:.*\.js`, RuntimeRiskModuleNotFound, "critical", "JavaScript file missing"},
	{`Error: connect ECONNREFUSED`, RuntimeRiskUnexpectedFail, "low", "Connection refused - service may be down"},
	{`SIGTERM|SIGKILL|SIGSEGV`, RuntimeRiskSetupCrash, "critical", "Process killed - resource issue"},
	{`out of memory|OOM|heap out of memory`, RuntimeRiskSetupCrash, "critical", "Out of memory"},
	{`Maximum call stack size exceeded`, RuntimeRiskUnexpectedFail, "high", "Stack overflow"},

	// ===== Selection Miss Indicators =====
	// These patterns specifically indicate a test selection miss
	{`no tests found`, RuntimeRiskSetupCrash, "high", "No tests found - possible selection miss"},
	{`0 passing`, RuntimeRiskUnexpectedFail, "medium", "Zero passing tests"},
	{`nothing to test`, RuntimeRiskSetupCrash, "high", "Nothing to test - possible selection miss"},
}

func runCIDetectRuntimeRisk(cmd *cobra.Command, args []string) error {
	report := RuntimeRiskReport{
		RisksDetected: false,
		Risks:         []RuntimeRiskSignal{},
	}

	// Determine input source
	var content []byte
	var err error

	if ciLogsFile != "" {
		content, err = os.ReadFile(ciLogsFile)
		if err != nil {
			return fmt.Errorf("reading logs file: %w", err)
		}
	} else if ciStderrFile != "" {
		content, err = os.ReadFile(ciStderrFile)
		if err != nil {
			return fmt.Errorf("reading stderr file: %w", err)
		}
	} else {
		return fmt.Errorf("either --logs or --stderr is required")
	}

	// Load plan if provided (for cross-referencing)
	var plan *CIPlan
	if ciPlanFile != "" {
		planData, err := os.ReadFile(ciPlanFile)
		if err == nil {
			var p CIPlan
			if json.Unmarshal(planData, &p) == nil {
				plan = &p
			}
		}
	}

	// Convert content to string for pattern matching
	contentStr := string(content)

	// Check each pattern
	for _, p := range runtimeRiskPatterns {
		re, err := regexp.Compile(p.pattern)
		if err != nil {
			continue
		}

		matches := re.FindAllStringSubmatch(contentStr, -1)
		for _, match := range matches {
			risk := RuntimeRiskSignal{
				Type:        p.riskType,
				Severity:    p.severity,
				Description: p.description,
			}

			// Extract additional context if available
			if len(match) > 1 {
				risk.Evidence = match[1]
			} else {
				risk.Evidence = match[0]
			}

			// Check if the risk is related to files outside the plan selection
			if plan != nil && risk.File != "" {
				inPlan := false
				for _, t := range plan.Targets.Run {
					if strings.Contains(t, risk.File) || strings.Contains(risk.File, t) {
						inPlan = true
						break
					}
				}
				if inPlan {
					// Risk is in selected files, lower severity
					risk.Description += " (in selected files)"
				} else {
					risk.Description += " (NOT in selected files - possible miss)"
				}
			}

			report.Risks = append(report.Risks, risk)
		}
	}

	// Determine overall risk status
	report.TotalRisks = len(report.Risks)
	report.RisksDetected = report.TotalRisks > 0

	// Count by severity
	criticalCount := 0
	highCount := 0
	for _, r := range report.Risks {
		switch r.Severity {
		case "critical":
			criticalCount++
		case "high":
			highCount++
		}
	}

	// Determine if tripwire should trigger
	tripwireTriggered := criticalCount > 0 || highCount > 0
	if ciRerunOnFail && report.RisksDetected {
		tripwireTriggered = true
	}

	// Generate recommendation
	if criticalCount > 0 {
		report.Recommendation = "RERUN: Critical runtime errors detected. Run full test suite."
	} else if highCount > 0 {
		report.Recommendation = "RERUN: High severity runtime errors detected. Run full test suite."
	} else if report.RisksDetected && ciRerunOnFail {
		report.Recommendation = "RERUN: Failures detected with --rerun-on-fail. Run full test suite."
	} else if report.RisksDetected {
		report.Recommendation = "WARNING: Minor runtime issues detected. Monitor for patterns."
	} else {
		report.Recommendation = "OK: No runtime risk signals detected."
	}

	// Tripwire mode: simple output for CI scripting
	if ciTripwire {
		if tripwireTriggered {
			fmt.Println("RERUN")
			// Exit with code 75 (custom code for "rerun needed")
			// We use a custom error type to signal this
			os.Exit(75)
		}
		fmt.Println("OK")
		return nil
	}

	// Output report
	if jsonFlag {
		report.TripwireTriggered = tripwireTriggered
		output, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(output))
	} else {
		fmt.Println("Runtime Risk Analysis")
		fmt.Println(strings.Repeat("=", 50))
		fmt.Printf("Risks Detected: %v\n", report.RisksDetected)
		fmt.Printf("Total Signals:  %d\n", report.TotalRisks)
		if tripwireTriggered {
			fmt.Printf("Tripwire:       TRIGGERED (rerun recommended)\n")
		}
		fmt.Printf("Recommendation: %s\n", report.Recommendation)

		if len(report.Risks) > 0 {
			fmt.Println("\nDetected Risks:")
			for i, r := range report.Risks {
				if i >= 10 {
					fmt.Printf("  ... and %d more\n", len(report.Risks)-10)
					break
				}
				fmt.Printf("  [%s] %s: %s\n", r.Severity, r.Type, r.Description)
				if r.Evidence != "" {
					evidence := r.Evidence
					if len(evidence) > 60 {
						evidence = evidence[:57] + "..."
					}
					fmt.Printf("         Evidence: %s\n", evidence)
				}
			}
		}

		// CI integration hint
		if tripwireTriggered {
			fmt.Println("\nCI Integration:")
			fmt.Println("  Use --tripwire flag for exit code 75 to trigger rerun:")
			fmt.Println("  kai ci detect-runtime-risk --stderr test.log --tripwire || npm run test:full")
		}
	}

	// Exit non-zero if risks detected that warrant fallback
	if tripwireTriggered {
		return fmt.Errorf("runtime risks detected: %d critical, %d high - rerun full suite", criticalCount, highCount)
	}

	return nil
}

// MissRecord represents a recorded test selection miss
type MissRecord struct {
	Timestamp     string   `json:"timestamp"`
	PlanFile      string   `json:"planFile,omitempty"`
	PlanProvenance CIProvenance `json:"planProvenance,omitempty"`
	FailedTests   []string `json:"failedTests"`
	SelectedTests []string `json:"selectedTests"`
	MissedTests   []string `json:"missedTests"` // Failed but not selected
	FalsePositives []string `json:"falsePositives,omitempty"` // Selected but didn't fail
}

func runCIRecordMiss(cmd *cobra.Command, args []string) error {
	if ciPlanFile == "" {
		return fmt.Errorf("--plan is required")
	}

	// Read plan
	planData, err := os.ReadFile(ciPlanFile)
	if err != nil {
		return fmt.Errorf("reading plan file: %w", err)
	}

	var plan CIPlan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return fmt.Errorf("parsing plan file: %w", err)
	}

	// Get failed tests
	var failedTests []string
	if ciFailedTests != "" {
		failedTests = strings.Split(ciFailedTests, ",")
		for i := range failedTests {
			failedTests[i] = strings.TrimSpace(failedTests[i])
		}
	} else if ciEvidenceFile != "" {
		// Try to parse evidence file (Jest/Mocha JSON format)
		evidenceData, err := os.ReadFile(ciEvidenceFile)
		if err != nil {
			return fmt.Errorf("reading evidence file: %w", err)
		}
		failedTests = extractFailedTestsFromEvidence(evidenceData)
	} else {
		return fmt.Errorf("either --failed or --evidence is required")
	}

	// Build sets for comparison
	selectedSet := make(map[string]bool)
	for _, t := range plan.Targets.Run {
		selectedSet[t] = true
	}

	failedSet := make(map[string]bool)
	for _, t := range failedTests {
		failedSet[t] = true
	}

	// Find misses (failed but not selected)
	var missedTests []string
	for _, t := range failedTests {
		if !selectedSet[t] {
			missedTests = append(missedTests, t)
		}
	}

	// Find false positives (selected but didn't fail)
	// Note: This is tricky because we don't know which selected tests passed
	// We can only record this if we have full test results
	var falsePositives []string
	// For now, we'll leave this empty unless we have evidence

	// Create miss record
	record := MissRecord{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		PlanFile:       ciPlanFile,
		PlanProvenance: plan.Provenance,
		FailedTests:    failedTests,
		SelectedTests:  plan.Targets.Run,
		MissedTests:    missedTests,
		FalsePositives: falsePositives,
	}

	// Output
	if jsonFlag {
		output, _ := json.MarshalIndent(record, "", "  ")
		fmt.Println(string(output))
	} else {
		fmt.Println("Test Selection Miss Report")
		fmt.Println(strings.Repeat("=", 50))
		fmt.Printf("Timestamp:     %s\n", record.Timestamp)
		fmt.Printf("Selected:      %d tests\n", len(record.SelectedTests))
		fmt.Printf("Failed:        %d tests\n", len(record.FailedTests))
		fmt.Printf("Missed:        %d tests\n", len(record.MissedTests))

		if len(missedTests) > 0 {
			fmt.Println("\nMissed Tests (failed but not selected):")
			for _, t := range missedTests {
				fmt.Printf("  - %s\n", t)
			}
			fmt.Println("\nThese tests failed but were not in the selection plan.")
			fmt.Println("Consider investigating missing dependency edges.")
		} else {
			fmt.Println("\nNo misses detected! All failing tests were selected.")
		}
	}

	// Store record for aggregation (append to .kai/ci-misses.jsonl)
	if err := appendMissRecord(record); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to persist miss record: %v\n", err)
	}

	// Exit non-zero if there were misses
	if len(missedTests) > 0 {
		return fmt.Errorf("recorded %d missed tests", len(missedTests))
	}

	return nil
}

// extractFailedTestsFromEvidence parses test results to find failed tests
func extractFailedTestsFromEvidence(data []byte) []string {
	var failedTests []string

	// Try Jest format
	var jestResult struct {
		TestResults []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"testResults"`
	}
	if json.Unmarshal(data, &jestResult) == nil && len(jestResult.TestResults) > 0 {
		for _, tr := range jestResult.TestResults {
			if tr.Status == "failed" {
				failedTests = append(failedTests, tr.Name)
			}
		}
		return failedTests
	}

	// Try pytest format
	var pytestResult struct {
		Tests []struct {
			NodeID  string `json:"nodeid"`
			Outcome string `json:"outcome"`
		} `json:"tests"`
	}
	if json.Unmarshal(data, &pytestResult) == nil && len(pytestResult.Tests) > 0 {
		for _, t := range pytestResult.Tests {
			if t.Outcome == "failed" {
				failedTests = append(failedTests, t.NodeID)
			}
		}
		return failedTests
	}

	// Try Go test JSON format
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		var goResult struct {
			Action  string `json:"Action"`
			Package string `json:"Package"`
			Test    string `json:"Test"`
		}
		if json.Unmarshal([]byte(line), &goResult) == nil {
			if goResult.Action == "fail" && goResult.Test != "" {
				failedTests = append(failedTests, goResult.Package+"/"+goResult.Test)
			}
		}
	}

	return failedTests
}

// appendMissRecord appends a miss record to the CI misses log
func appendMissRecord(record MissRecord) error {
	// Find .kai directory
	kaiPath := filepath.Join(".", kaiDir)
	if _, err := os.Stat(kaiPath); os.IsNotExist(err) {
		return nil // Not in a kai repo, skip
	}

	missesFile := filepath.Join(kaiPath, "ci-misses.jsonl")

	f, err := os.OpenFile(missesFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	_, err = f.WriteString(string(data) + "\n")
	return err
}

// runCIExplainDynamicImports scans files for dynamic imports and explains their impact
func runCIExplainDynamicImports(cmd *cobra.Command, args []string) error {
	// Default to current directory
	targetPath := "."
	if len(args) > 0 {
		targetPath = args[0]
	}

	// Load CI policy
	ciPolicy, _, err := loadCIPolicy()
	if err != nil {
		return fmt.Errorf("loading CI policy: %w", err)
	}

	// Find all files to scan
	var filesToScan []string
	stat, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("accessing path: %w", err)
	}

	if stat.IsDir() {
		// Walk directory
		err = filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if info.IsDir() {
				// Skip common non-source directories
				base := filepath.Base(path)
				if base == "node_modules" || base == ".git" || base == "vendor" || base == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			// Only check source files
			ext := strings.ToLower(filepath.Ext(path))
			switch ext {
			case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".py", ".go":
				filesToScan = append(filesToScan, path)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("walking directory: %w", err)
		}
	} else {
		filesToScan = append(filesToScan, targetPath)
	}

	// Scan each file for dynamic imports
	var allImports []DynamicImportFile
	for _, filePath := range filesToScan {
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		imports := detectDynamicImportsDetailed(content, filePath, &ciPolicy.DynamicImports)
		allImports = append(allImports, imports...)
	}

	// Output
	if jsonFlag {
		output := struct {
			TotalFilesScanned int                  `json:"totalFilesScanned"`
			TotalDetected     int                  `json:"totalDetected"`
			Policy            CIPolicyDynamicImports `json:"policy"`
			Imports           []DynamicImportFile  `json:"imports"`
		}{
			TotalFilesScanned: len(filesToScan),
			TotalDetected:     len(allImports),
			Policy:            ciPolicy.DynamicImports,
			Imports:           allImports,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Human-readable output
	fmt.Println()
	fmt.Println("Dynamic Import Analysis")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Files scanned: %d\n", len(filesToScan))
	fmt.Printf("Dynamic imports found: %d\n", len(allImports))
	fmt.Printf("Expansion strategy: %s\n", ciPolicy.DynamicImports.Expansion)
	fmt.Printf("Owners fallback: %v\n", ciPolicy.DynamicImports.OwnersFallback)
	fmt.Printf("Bounded risk threshold: %d files\n", ciPolicy.DynamicImports.BoundedRiskThreshold)
	fmt.Println()

	if len(allImports) == 0 {
		fmt.Println("No dynamic imports detected.")
		return nil
	}

	// Group by status
	var bounded, boundedRisky, unbounded, allowlisted []DynamicImportFile
	for _, imp := range allImports {
		if imp.Allowlisted {
			allowlisted = append(allowlisted, imp)
		} else if imp.Bounded {
			bounded = append(bounded, imp)
		} else {
			unbounded = append(unbounded, imp)
		}
	}

	// Show unbounded first (most important)
	if len(unbounded) > 0 {
		fmt.Printf("⚠️  UNBOUNDED (%d) - Will trigger expansion:\n", len(unbounded))
		for _, imp := range unbounded {
			fmt.Printf("   %s:%d\n", imp.Path, imp.Line)
			fmt.Printf("      Type: %s (confidence: %.0f%%)\n", imp.Kind, imp.Confidence*100)
			fmt.Printf("      Action: Expand to %s\n", ciPolicy.DynamicImports.Expansion)
		}
		fmt.Println()
	}

	if len(boundedRisky) > 0 {
		fmt.Printf("⚡ BOUNDED-RISKY (%d) - Bounded but large footprint:\n", len(boundedRisky))
		for _, imp := range boundedRisky {
			fmt.Printf("   %s:%d\n", imp.Path, imp.Line)
			fmt.Printf("      Type: %s\n", imp.Kind)
			fmt.Printf("      Bound: %s\n", imp.BoundedBy)
			fmt.Printf("      Action: Treat as unbounded (footprint > %d)\n", ciPolicy.DynamicImports.BoundedRiskThreshold)
		}
		fmt.Println()
	}

	if len(bounded) > 0 {
		fmt.Printf("✓  BOUNDED (%d) - Safe, will not expand:\n", len(bounded))
		for _, imp := range bounded {
			fmt.Printf("   %s:%d → %s\n", imp.Path, imp.Line, imp.BoundedBy)
		}
		fmt.Println()
	}

	if len(allowlisted) > 0 {
		fmt.Printf("○  ALLOWLISTED (%d) - Ignored by policy:\n", len(allowlisted))
		for _, imp := range allowlisted {
			fmt.Printf("   %s:%d\n", imp.Path, imp.Line)
		}
		fmt.Println()
	}

	// Recommendations
	if len(unbounded) > 0 {
		fmt.Println("Recommendations:")
		fmt.Println("  • Add webpackInclude/webpackExclude comments to bound dynamic imports")
		fmt.Println("  • Add paths to dynamicImports.allowlist in .kai/rules/ci-policy.yaml")
		fmt.Println("  • Use explicit imports where possible")
	}

	return nil
}

// getChangedFiles returns paths of files that changed between two snapshots.
func getChangedFiles(db *graph.DB, creator *snapshot.Creator, baseID, headID []byte) ([]string, error) {
	// Get head files
	headFiles, err := creator.GetSnapshotFiles(headID)
	if err != nil {
		return nil, err
	}

	headMap := make(map[string]string) // path -> digest
	for _, f := range headFiles {
		path, _ := f.Payload["path"].(string)
		digest, _ := f.Payload["digest"].(string)
		headMap[path] = digest
	}

	// If no base, all head files are "changed"
	if baseID == nil {
		var paths []string
		for p := range headMap {
			paths = append(paths, p)
		}
		return paths, nil
	}

	// Get base files
	baseFiles, err := creator.GetSnapshotFiles(baseID)
	if err != nil {
		return nil, err
	}

	baseMap := make(map[string]string) // path -> digest
	for _, f := range baseFiles {
		path, _ := f.Payload["path"].(string)
		digest, _ := f.Payload["digest"].(string)
		baseMap[path] = digest
	}

	// Find changed files
	var changed []string

	// New or modified files
	for path, headDigest := range headMap {
		baseDigest, exists := baseMap[path]
		if !exists || baseDigest != headDigest {
			changed = append(changed, path)
		}
	}

	// Deleted files (these could affect tests too)
	for path := range baseMap {
		if _, exists := headMap[path]; !exists {
			changed = append(changed, path)
		}
	}

	return changed, nil
}

// createChangesetFromSnapshots creates a changeset between two snapshots and returns its ID
func createChangesetFromSnapshots(db *graph.DB, baseSnapID, headSnapID []byte, message string) ([]byte, error) {
	matcher, err := loadMatcher()
	if err != nil {
		return nil, err
	}

	// Get files from both snapshots
	creator := snapshot.NewCreator(db, matcher)
	baseFiles, err := creator.GetSnapshotFiles(baseSnapID)
	if err != nil {
		return nil, fmt.Errorf("getting base files: %w", err)
	}

	headFiles, err := creator.GetSnapshotFiles(headSnapID)
	if err != nil {
		return nil, fmt.Errorf("getting head files: %w", err)
	}

	// Build file maps
	baseFileMap := make(map[string]*graph.Node)
	headFileMap := make(map[string]*graph.Node)

	for _, f := range baseFiles {
		if path, ok := f.Payload["path"].(string); ok {
			baseFileMap[path] = f
		}
	}

	for _, f := range headFiles {
		if path, ok := f.Payload["path"].(string); ok {
			headFileMap[path] = f
		}
	}

	// Find changed files (by digest comparison)
	var changedPaths []string
	var changedFileIDs [][]byte

	for path, headFile := range headFileMap {
		baseFile, exists := baseFileMap[path]
		if !exists {
			// Added file
			changedPaths = append(changedPaths, path)
			changedFileIDs = append(changedFileIDs, headFile.ID)
		} else {
			// Check if digest differs
			baseDigest, _ := baseFile.Payload["digest"].(string)
			headDigest, _ := headFile.Payload["digest"].(string)
			if baseDigest != headDigest {
				changedPaths = append(changedPaths, path)
				changedFileIDs = append(changedFileIDs, headFile.ID)
			}
		}
	}

	// Start transaction
	tx, err := db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Create changeset node
	changeSetPayload := map[string]interface{}{
		"base":        util.BytesToHex(baseSnapID),
		"head":        util.BytesToHex(headSnapID),
		"title":       "",
		"description": message,
		"intent":      "",
		"createdAt":   util.NowMs(),
	}
	changeSetID, err := db.InsertNode(tx, graph.KindChangeSet, changeSetPayload)
	if err != nil {
		return nil, fmt.Errorf("inserting changeset: %w", err)
	}

	// Detect change types
	detector := classify.NewDetector()

	// Load symbols for each changed file
	for i := range changedPaths {
		fileID := changedFileIDs[i]
		symbols, err := creator.GetSymbolsInFile(fileID, headSnapID)
		if err == nil && len(symbols) > 0 {
			detector.SetSymbols(util.BytesToHex(fileID), symbols)
		}
	}

	var allChangeTypes []*classify.ChangeType
	var affectedModules []string
	affectedModulesSet := make(map[string]bool)

	for i, path := range changedPaths {
		headFile := headFileMap[path]
		baseFile := baseFileMap[path]

		var beforeContent, afterContent []byte

		// Read after content
		if digest, ok := headFile.Payload["digest"].(string); ok {
			afterContent, _ = db.ReadObject(digest)
		}

		// Read before content (if exists)
		if baseFile != nil {
			if digest, ok := baseFile.Payload["digest"].(string); ok {
				beforeContent, _ = db.ReadObject(digest)
			}
		}

		// Get the file's language
		lang, _ := headFile.Payload["lang"].(string)

		if len(beforeContent) > 0 && len(afterContent) > 0 {
			var changes []*classify.ChangeType
			var err error

			switch lang {
			case "json":
				// Use JSON-specific detection
				changes, err = classify.DetectJSONChanges(path, beforeContent, afterContent)
			case "ts", "js":
				// Use tree-sitter based detection
				changes, err = detector.DetectChanges(path, beforeContent, afterContent, util.BytesToHex(changedFileIDs[i]))
			default:
				// Non-parseable files get FILE_CONTENT_CHANGED
				changes = []*classify.ChangeType{classify.NewFileChange(classify.FileContentChanged, path)}
			}

			if err == nil && len(changes) > 0 {
				allChangeTypes = append(allChangeTypes, changes...)
			}
		} else if baseFile == nil && len(afterContent) > 0 {
			// New file added
			allChangeTypes = append(allChangeTypes, classify.NewFileChange(classify.FileAdded, path))
		}

		// Create MODIFIES edge to file
		if err := db.InsertEdge(tx, changeSetID, graph.EdgeModifies, changedFileIDs[i], nil); err != nil {
			return nil, fmt.Errorf("inserting MODIFIES edge: %w", err)
		}

		// Map to modules
		modules := matcher.MatchPath(path)
		for _, mod := range modules {
			if !affectedModulesSet[mod] {
				affectedModulesSet[mod] = true
				affectedModules = append(affectedModules, mod)
			}
		}
	}

	// Create ChangeType nodes and HAS edges
	for _, ct := range allChangeTypes {
		payload := classify.GetCategoryPayload(ct)
		ctID, err := db.InsertNode(tx, graph.KindChangeType, payload)
		if err != nil {
			return nil, fmt.Errorf("inserting change type: %w", err)
		}
		if err := db.InsertEdge(tx, changeSetID, graph.EdgeHas, ctID, nil); err != nil {
			return nil, fmt.Errorf("inserting HAS edge: %w", err)
		}

		// Create MODIFIES edges to symbols
		for _, symIDHex := range ct.Evidence.Symbols {
			symID, err := util.HexToBytes(symIDHex)
			if err == nil {
				if err := db.InsertEdge(tx, changeSetID, graph.EdgeModifies, symID, nil); err != nil {
					// Ignore if symbol doesn't exist
				}
			}
		}
	}

	// Create AFFECTS edges to modules
	for _, modName := range affectedModules {
		payload := matcher.GetModulePayload(modName)
		if payload != nil {
			modID, err := db.InsertNode(tx, graph.KindModule, payload)
			if err != nil {
				return nil, fmt.Errorf("inserting module: %w", err)
			}
			if err := db.InsertEdge(tx, changeSetID, graph.EdgeAffects, modID, nil); err != nil {
				return nil, fmt.Errorf("inserting AFFECTS edge: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	// Update auto-refs
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnChangeSetCreated(changeSetID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	return changeSetID, nil
}

// createSnapshotFromGitRef creates a snapshot from a git ref, including symbol analysis
func createSnapshotFromGitRef(db *graph.DB, repoPath, gitRef string) ([]byte, error) {
	// Load module matcher
	matcher, err := loadMatcher()
	if err != nil {
		// Use empty matcher if no config
		matcher = module.NewMatcher(nil)
	}

	// Open git source
	source, err := gitio.OpenSource(repoPath, gitRef)
	if err != nil {
		return nil, fmt.Errorf("opening git ref %s: %w", gitRef, err)
	}

	// Create snapshot
	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot from %s: %w", gitRef, err)
	}

	// Analyze symbols (needed for semantic diff and CI plan)
	if err := analyzeSnapshotSymbols(db, snapshotID); err != nil {
		// Non-fatal - continue without symbols
		fmt.Fprintf(os.Stderr, "warning: symbol analysis failed for %s: %v\n", gitRef, err)
	}

	return snapshotID, nil
}

// analyzeSnapshotSymbols extracts symbols from all files in a snapshot
func analyzeSnapshotSymbols(db *graph.DB, snapshotID []byte) error {
	matcher, err := loadMatcher()
	if err != nil {
		matcher = module.NewMatcher(nil)
	}

	creator := snapshot.NewCreator(db, matcher)
	// Silent progress for internal use
	progress := func(current, total int, filename string) {}
	return creator.AnalyzeSymbols(snapshotID, progress)
}

func runChangesetCreate(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var baseSnapID, headSnapID []byte

	// Check if using git refs or snapshot IDs
	useGitRefs := changesetGitBase != "" || changesetGitHead != ""

	if useGitRefs {
		// Both --git-base and --git-head required together
		if changesetGitBase == "" || changesetGitHead == "" {
			return fmt.Errorf("both --git-base and --git-head are required when using git refs")
		}

		fmt.Printf("Creating snapshot from git ref: %s\n", changesetGitBase)
		baseSnapID, err = createSnapshotFromGitRef(db, changesetGitRepo, changesetGitBase)
		if err != nil {
			return fmt.Errorf("creating base snapshot: %w", err)
		}

		fmt.Printf("Creating snapshot from git ref: %s\n", changesetGitHead)
		headSnapID, err = createSnapshotFromGitRef(db, changesetGitRepo, changesetGitHead)
		if err != nil {
			return fmt.Errorf("creating head snapshot: %w", err)
		}
	} else {
		// Traditional mode: use positional args as snapshot IDs
		if len(args) != 2 {
			return fmt.Errorf("either provide two snapshot IDs or use --git-base and --git-head")
		}

		baseSnapID, err = resolveSnapshotID(db, args[0])
		if err != nil {
			return fmt.Errorf("resolving base snapshot: %w", err)
		}

		headSnapID, err = resolveSnapshotID(db, args[1])
		if err != nil {
			return fmt.Errorf("resolving head snapshot: %w", err)
		}
	}

	changeSetID, err := createChangesetFromSnapshots(db, baseSnapID, headSnapID, changesetMessage)
	if err != nil {
		return err
	}

	// Get stats for output by querying edges
	modifiedFiles, _ := db.GetEdges(changeSetID, graph.EdgeModifies)
	changeTypes, _ := db.GetEdges(changeSetID, graph.EdgeHas)
	affectedModulesEdges, _ := db.GetEdges(changeSetID, graph.EdgeAffects)

	// Count unique files (filter out symbols from MODIFIES)
	fileCount := 0
	for _, edge := range modifiedFiles {
		node, err := db.GetNode(edge.Dst)
		if err == nil && node.Kind == graph.KindFile {
			fileCount++
		}
	}

	// Get module names
	var moduleNames []string
	for _, edge := range affectedModulesEdges {
		node, err := db.GetNode(edge.Dst)
		if err == nil && node.Kind == graph.KindModule {
			if name, ok := node.Payload["name"].(string); ok {
				moduleNames = append(moduleNames, name)
			}
		}
	}

	fmt.Printf("Created changeset: %s\n", util.BytesToHex(changeSetID))
	fmt.Printf("Changed files: %d\n", fileCount)
	fmt.Printf("Change types detected: %d\n", len(changeTypes))
	fmt.Printf("Affected modules: %v\n", moduleNames)

	return nil
}

func runIntentRender(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	changeSetID, err := resolveChangeSetID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving changeset ID: %w", err)
	}

	gen := intent.NewGenerator(db)
	intentText, err := gen.RenderIntent(changeSetID, editText, regenerateIntent)
	if err != nil {
		return fmt.Errorf("rendering intent: %w", err)
	}

	fmt.Printf("Intent: %s\n", intentText)
	return nil
}

func runDump(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	changeSetID, err := resolveChangeSetID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving changeset ID: %w", err)
	}

	result, err := db.GetAllNodesAndEdgesForChangeSet(changeSetID)
	if err != nil {
		return fmt.Errorf("getting changeset data: %w", err)
	}

	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}

	fmt.Println(string(output))
	return nil
}

func openDB() (*graph.DB, error) {
	// Check if .kai directory exists
	if _, err := os.Stat(kaiDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("Kai not initialized. Run 'kai init' first")
	}
	dbPath := filepath.Join(kaiDir, dbFile)
	objPath := filepath.Join(kaiDir, objectsDir)
	return graph.Open(dbPath, objPath)
}

// applyDBSchema applies the database schema to a fresh database.
// Used for ephemeral databases in --git-range mode.
func applyDBSchema(db *graph.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS nodes (
  id BLOB PRIMARY KEY,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS nodes_kind ON nodes(kind);

CREATE TABLE IF NOT EXISTS edges (
  src BLOB NOT NULL,
  type TEXT NOT NULL,
  dst BLOB NOT NULL,
  at  BLOB,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (src, type, dst, at)
);

CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_type ON edges(type);
CREATE INDEX IF NOT EXISTS edges_at ON edges(at);

CREATE TABLE IF NOT EXISTS refs (
  name TEXT PRIMARY KEY,
  target_id BLOB NOT NULL,
  target_kind TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS refs_kind ON refs(target_kind);

CREATE TABLE IF NOT EXISTS slugs (
  target_id BLOB PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS logs (
  kind TEXT NOT NULL,
  seq INTEGER NOT NULL,
  id BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (kind, seq)
);
CREATE INDEX IF NOT EXISTS logs_id ON logs(id);

CREATE TABLE IF NOT EXISTS ref_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  old_target BLOB,
  new_target BLOB NOT NULL,
  actor TEXT,
  moved_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ref_log_name ON ref_log(name);
CREATE INDEX IF NOT EXISTS ref_log_moved_at ON ref_log(moved_at);

CREATE INDEX IF NOT EXISTS nodes_created_at ON nodes(created_at);
`
	tx, err := db.BeginTx()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(schema); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func loadMatcher() (*module.Matcher, error) {
	// Try the new location first (.kai/rules/modules.yaml)
	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return nil, err
	}
	if len(matcher.GetAllModules()) > 0 {
		return matcher, nil
	}

	// Fall back to legacy location (kai.modules.yaml in project root)
	return module.LoadRulesOrEmpty(modulesFile)
}

// getCurrentWorkspace reads the current workspace name from .kai/workspace
func getCurrentWorkspace() (string, error) {
	path := filepath.Join(kaiDir, workspaceFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // No current workspace
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// setCurrentWorkspace writes the current workspace name to .kai/workspace
func setCurrentWorkspace(name string) error {
	path := filepath.Join(kaiDir, workspaceFile)
	if name == "" {
		// Clear current workspace
		os.Remove(path)
		return nil
	}
	return os.WriteFile(path, []byte(name+"\n"), 0644)
}

func runListSnapshots(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	nodes, err := db.GetNodesByKind(graph.KindSnapshot)
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Println("No snapshots found.")
		return nil
	}

	fmt.Printf("%-64s  %-10s  %-20s  %s\n", "ID", "TYPE", "SOURCE REF", "FILES")
	fmt.Println(strings.Repeat("-", 110))
	for _, node := range nodes {
		// Support both old (gitRef) and new (sourceRef/sourceType) formats
		sourceType, _ := node.Payload["sourceType"].(string)
		sourceRef, _ := node.Payload["sourceRef"].(string)

		// Backward compatibility: check old gitRef field
		if sourceRef == "" {
			if gitRef, ok := node.Payload["gitRef"].(string); ok {
				sourceRef = gitRef
				sourceType = "git"
			}
		}

		// Truncate long sourceRef for display
		displayRef := sourceRef
		if len(displayRef) > 20 {
			displayRef = displayRef[:17] + "..."
		}

		fileCount := ""
		if fc, ok := node.Payload["fileCount"].(float64); ok {
			fileCount = fmt.Sprintf("%.0f", fc)
		}
		fmt.Printf("%-64s  %-10s  %-20s  %s\n", util.BytesToHex(node.ID), sourceType, displayRef, fileCount)
	}

	return nil
}

func runListChangesets(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	nodes, err := db.GetNodesByKind(graph.KindChangeSet)
	if err != nil {
		return fmt.Errorf("listing changesets: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Println("No changesets found.")
		return nil
	}

	fmt.Printf("%-64s  %s\n", "ID", "INTENT")
	fmt.Println(strings.Repeat("-", 80))
	for _, node := range nodes {
		intent, _ := node.Payload["intent"].(string)
		if intent == "" {
			intent = "(no intent)"
		}
		fmt.Printf("%-64s  %s\n", util.BytesToHex(node.ID), intent)
	}

	return nil
}

func runListSymbols(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snapshotID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving snapshot ID: %w", err)
	}

	// Get all files in the snapshot
	edges, err := db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return fmt.Errorf("getting snapshot files: %w", err)
	}

	if len(edges) == 0 {
		fmt.Println("No files in snapshot.")
		return nil
	}

	// Build a map of file ID -> path
	fileIDToPath := make(map[string]string)
	for _, edge := range edges {
		node, err := db.GetNode(edge.Dst)
		if err != nil {
			continue
		}
		if node != nil {
			if path, ok := node.Payload["path"].(string); ok {
				fileIDToPath[util.BytesToHex(node.ID)] = path
			}
		}
	}

	// Get symbols for each file, grouped by file path
	type symbolInfo struct {
		Kind      string
		Name      string
		Signature string
	}
	fileSymbols := make(map[string][]symbolInfo)
	totalSymbols := 0

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}
	creator := snapshot.NewCreator(db, matcher)

	for _, edge := range edges {
		symbols, err := creator.GetSymbolsInFile(edge.Dst, snapshotID)
		if err != nil {
			continue
		}
		if len(symbols) == 0 {
			continue
		}

		fileID := util.BytesToHex(edge.Dst)
		path := fileIDToPath[fileID]
		if path == "" {
			path = fileID[:16] + "..."
		}

		for _, sym := range symbols {
			kind, _ := sym.Payload["kind"].(string)
			fqName, _ := sym.Payload["fqName"].(string)
			signature, _ := sym.Payload["signature"].(string)

			fileSymbols[path] = append(fileSymbols[path], symbolInfo{
				Kind:      kind,
				Name:      fqName,
				Signature: signature,
			})
			totalSymbols++
		}
	}

	if totalSymbols == 0 {
		fmt.Println("No symbols found. Run 'kai analyze symbols <snapshot-id>' first.")
		return nil
	}

	// Sort file paths for consistent output
	var paths []string
	for path := range fileSymbols {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	// Print symbols grouped by file
	for _, path := range paths {
		fmt.Printf("\n%s\n", path)
		for _, sym := range fileSymbols[path] {
			if sym.Kind == "function" && sym.Signature != "" {
				// For functions, signature already includes "function" keyword
				fmt.Printf("  %s\n", sym.Signature)
			} else if sym.Signature != "" {
				fmt.Printf("  %s %s\n", sym.Kind, sym.Signature)
			} else {
				fmt.Printf("  %s %s\n", sym.Kind, sym.Name)
			}
		}
	}

	fmt.Printf("\n%d symbols in %d files\n", totalSymbols, len(paths))
	return nil
}

// logEntry represents an entry in the log timeline
type logEntry struct {
	ID        string
	Kind      string
	CreatedAt int64
	Summary   string
	Details   map[string]string
}

func runLog(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var entries []logEntry

	// Get all snapshots
	snapshots, err := db.GetNodesByKind(graph.KindSnapshot)
	if err != nil {
		return fmt.Errorf("getting snapshots: %w", err)
	}

	for _, node := range snapshots {
		createdAt, _ := node.Payload["createdAt"].(float64)
		description, _ := node.Payload["description"].(string)

		// Get source info (support both old and new formats)
		sourceType, _ := node.Payload["sourceType"].(string)
		sourceRef, _ := node.Payload["sourceRef"].(string)
		if sourceRef == "" {
			if gitRef, ok := node.Payload["gitRef"].(string); ok {
				sourceRef = gitRef
				sourceType = "git"
			}
		}

		fileCount := ""
		if fc, ok := node.Payload["fileCount"].(float64); ok {
			fileCount = fmt.Sprintf("%.0f files", fc)
		}

		// Use description as summary if provided, otherwise show source info
		summary := description
		if summary == "" {
			summary = fmt.Sprintf("%s (%s)", sourceRef, sourceType)
		}

		entries = append(entries, logEntry{
			ID:        util.BytesToHex(node.ID),
			Kind:      "snapshot",
			CreatedAt: int64(createdAt),
			Summary:   summary,
			Details: map[string]string{
				"files": fileCount,
			},
		})
	}

	// Get all changesets
	changesets, err := db.GetNodesByKind(graph.KindChangeSet)
	if err != nil {
		return fmt.Errorf("getting changesets: %w", err)
	}

	for _, node := range changesets {
		createdAt, _ := node.Payload["createdAt"].(float64)
		description, _ := node.Payload["description"].(string)
		intentText, _ := node.Payload["intent"].(string)

		// Use description (user message) as summary, fall back to intent
		summary := description
		if summary == "" {
			summary = intentText
		}
		if summary == "" {
			summary = "(no message)"
		}

		base, _ := node.Payload["base"].(string)
		head, _ := node.Payload["head"].(string)

		entries = append(entries, logEntry{
			ID:        util.BytesToHex(node.ID),
			Kind:      "changeset",
			CreatedAt: int64(createdAt),
			Summary:   summary,
			Details: map[string]string{
				"base": base,
				"head": head,
			},
		})
	}

	if len(entries) == 0 {
		fmt.Println("No entries found.")
		return nil
	}

	// Sort by createdAt descending (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt > entries[j].CreatedAt
	})

	// Limit entries
	if logLimit > 0 && len(entries) > logLimit {
		entries = entries[:logLimit]
	}

	// Display entries
	for i, entry := range entries {
		if i > 0 {
			fmt.Println()
		}

		// Format timestamp
		timestamp := "unknown"
		if entry.CreatedAt > 0 {
			t := time.UnixMilli(entry.CreatedAt)
			timestamp = t.Format("2006-01-02 15:04:05")
		}

		// Color-like formatting using markers
		kindMarker := "snapshot"
		if entry.Kind == "changeset" {
			kindMarker = "changeset"
		}

		fmt.Printf("[%s] %s\n", kindMarker, shortID(entry.ID))
		fmt.Printf("Date:    %s\n", timestamp)
		fmt.Printf("Summary: %s\n", entry.Summary)

		if entry.Kind == "changeset" {
			if base, ok := entry.Details["base"]; ok && base != "" {
				fmt.Printf("Base:    %s\n", shortID(base))
			}
			if head, ok := entry.Details["head"]; ok && head != "" {
				fmt.Printf("Head:    %s\n", shortID(head))
			}
		} else {
			if files, ok := entry.Details["files"]; ok && files != "" {
				fmt.Printf("Files:   %s\n", files)
			}
		}
	}

	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Check if Kai is initialized
	if _, err := os.Stat(kaiDir); os.IsNotExist(err) {
		fmt.Println("Not a Kai repository (run 'kai init' to initialize)")
		return nil
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// For JSON output, skip the header info
	if !statusJSON && !statusNameOnly {
		fmt.Println("Kai initialized")
		fmt.Println()

		// Count snapshots and changesets
		snapshots, err := db.GetNodesByKind(graph.KindSnapshot)
		if err != nil {
			return err
		}
		changesets, err := db.GetNodesByKind(graph.KindChangeSet)
		if err != nil {
			return err
		}

		fmt.Printf("Snapshots:  %d\n", len(snapshots))
		fmt.Printf("Changesets: %d\n", len(changesets))
		fmt.Println()

		if len(snapshots) > 0 {
			fmt.Printf("Checking for changes in: %s\n", statusDir)
			fmt.Println()
		}
	}

	// Compute status using the new status package
	result, err := status.Compute(db, status.Options{
		Dir:      statusDir,
		Against:  statusAgainst,
		UseCache: true,
		CacheDir: ".", // Cache in repo root's .kai/cache, not in scanned dir
	})
	if err != nil {
		return err
	}

	// Run semantic analysis if requested
	var semantic *status.SemanticResult
	if statusSemantic && len(result.Modified) > 0 {
		semantic, err = status.AnalyzeSemantic(db, result, status.SemanticOptions{
			Dir: statusDir,
		})
		if err != nil {
			// Non-fatal - continue without semantic info
			fmt.Fprintf(os.Stderr, "warning: semantic analysis failed: %v\n", err)
		}
	}

	// Show explain if requested
	if statusExplain {
		hasBaseline := !result.NoBaseline
		ctx := explain.ExplainStatus(hasBaseline, len(result.Modified), len(result.Added), len(result.Deleted))
		ctx.Print(os.Stdout)
	}

	// Determine output format
	var format status.OutputFormat
	if statusJSON {
		format = status.FormatJSON
	} else if statusNameOnly {
		format = status.FormatNameOnly
	} else {
		format = status.FormatDefault
	}

	// Write output
	if err := status.WriteOutputWithSemantic(os.Stdout, result, semantic, format); err != nil {
		return err
	}

	// For default format, add helpful suggestion if there are changes
	if format == status.FormatDefault && result.HasChanges() {
		fmt.Println()
		if result.NoBaseline {
			fmt.Println("Create your first snapshot with:")
			fmt.Println("  kai capture")
		} else {
			fmt.Println("To see semantic differences:")
			fmt.Println("  kai diff")
			fmt.Println()
			fmt.Println("To capture these changes:")
			fmt.Println("  kai capture")
		}
	}

	return nil
}

func runDiff(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Semantic is the default unless --name-only or --patch is specified
	// JSON also implies semantic
	if !diffNameOnly && !diffPatch {
		diffSemantic = true
	}
	if diffJSON {
		diffSemantic = true
	}

	// Default to @snap:last if no args provided (simple mode friendly)
	baseRef := "@snap:last"
	if len(args) >= 1 {
		baseRef = args[0]
	}

	// Check for stale baseline warning (unless --force or comparing two explicit snapshots)
	if !diffForce && len(args) < 2 {
		refMgr := ref.NewRefManager(db)
		workingRef, _ := refMgr.Get("snap.working")
		if workingRef != nil {
			staleThreshold := int64(10 * 60 * 1000) // 10 minutes in ms
			age := util.NowMs() - workingRef.UpdatedAt
			if age > staleThreshold {
				ageMinutes := age / 60000
				fmt.Fprintf(os.Stderr, "Warning: Last capture was %d minutes ago.\n", ageMinutes)
				fmt.Fprintf(os.Stderr, "  Your working directory may have changed since then.\n")
				fmt.Fprintf(os.Stderr, "  Run 'kai capture' for an accurate diff, or use --force to continue.\n\n")
			}
		}
	}

	// Resolve base snapshot
	baseSnapID, err := resolveSnapshotID(db, baseRef)
	if err != nil {
		// Friendly error for simple mode users
		if baseRef == "@snap:last" {
			fmt.Println()
			fmt.Println("No snapshots found. Create one first:")
			fmt.Println()
			fmt.Println("  kai capture    # Recommended: capture and analyze in one step")
			fmt.Println()
			return fmt.Errorf("no snapshots available")
		}
		return fmt.Errorf("resolving base snapshot: %w", err)
	}

	creator := snapshot.NewCreator(db, nil)

	// Get base snapshot files
	baseFiles, err := creator.GetSnapshotFiles(baseSnapID)
	if err != nil {
		return fmt.Errorf("getting base files: %w", err)
	}

	// Load content if needed for semantic or patch mode
	needContent := diffSemantic || diffPatch

	baseFileMap := make(map[string]string)   // path -> digest
	baseContent := make(map[string][]byte)   // path -> content (for semantic/patch diff)
	for _, f := range baseFiles {
		path, _ := f.Payload["path"].(string)
		digest, _ := f.Payload["digest"].(string)
		baseFileMap[path] = digest
		if needContent {
			content, _ := creator.GetFileContent(digest)
			baseContent[path] = content
		}
	}

	var headFileMap map[string]string
	var headContent map[string][]byte
	var headLabel string

	if len(args) == 2 {
		// Compare two snapshots
		headSnapID, err := resolveSnapshotID(db, args[1])
		if err != nil {
			return fmt.Errorf("resolving head snapshot: %w", err)
		}

		headFiles, err := creator.GetSnapshotFiles(headSnapID)
		if err != nil {
			return fmt.Errorf("getting head files: %w", err)
		}

		headFileMap = make(map[string]string)
		headContent = make(map[string][]byte)
		for _, f := range headFiles {
			path, _ := f.Payload["path"].(string)
			digest, _ := f.Payload["digest"].(string)
			headFileMap[path] = digest
			if needContent {
				content, _ := creator.GetFileContent(digest)
				headContent[path] = content
			}
		}

		headLabel = util.BytesToHex(headSnapID)[:12]
	} else {
		// Compare snapshot vs working directory
		source, err := dirio.OpenDirectory(diffDir)
		if err != nil {
			return fmt.Errorf("opening directory: %w", err)
		}

		currentFiles, err := source.GetFiles()
		if err != nil {
			return fmt.Errorf("getting current files: %w", err)
		}

		headFileMap = make(map[string]string)
		headContent = make(map[string][]byte)
		for _, f := range currentFiles {
			headFileMap[f.Path] = util.Blake3HashHex(f.Content)
			if needContent {
				headContent[f.Path] = f.Content
			}
		}

		headLabel = "working directory"
	}

	// Compute file-level differences
	var added, modified, deleted []string

	for path, headDigest := range headFileMap {
		if baseDigest, exists := baseFileMap[path]; !exists {
			added = append(added, path)
		} else if headDigest != baseDigest {
			modified = append(modified, path)
		}
	}

	for path := range baseFileMap {
		if _, exists := headFileMap[path]; !exists {
			deleted = append(deleted, path)
		}
	}

	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)

	// Show explain if requested (for both semantic and simple mode)
	fileCount := len(added) + len(modified) + len(deleted)
	if diffExplain {
		var changeTypes []string
		if len(added) > 0 {
			changeTypes = append(changeTypes, fmt.Sprintf("%d file(s) added", len(added)))
		}
		if len(modified) > 0 {
			changeTypes = append(changeTypes, fmt.Sprintf("%d file(s) modified", len(modified)))
		}
		if len(deleted) > 0 {
			changeTypes = append(changeTypes, fmt.Sprintf("%d file(s) deleted", len(deleted)))
		}
		// Get affected modules
		var modules []string
		if matcher, err := loadMatcher(); err == nil {
			modulesSet := make(map[string]bool)
			allPaths := append(append(added, modified...), deleted...)
			for _, path := range allPaths {
				for _, mod := range matcher.MatchPath(path) {
					modulesSet[mod] = true
				}
			}
			for mod := range modulesSet {
				modules = append(modules, mod)
			}
		}
		ctx := explain.ExplainDiffFull(baseRef, headLabel, fileCount, changeTypes, modules)
		ctx.Print(os.Stdout)
	}

	// No differences
	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		if diffJSON {
			fmt.Println(`{"files":[],"summary":{"filesAdded":0,"filesModified":0,"filesRemoved":0,"unitsAdded":0,"unitsModified":0,"unitsRemoved":0}}`)
		} else {
			fmt.Println("No differences.")
		}
		return nil
	}

	// Semantic diff mode
	if diffSemantic {
		differ := diff.NewDiffer()
		sd := &diff.SemanticDiff{
			Base: util.BytesToHex(baseSnapID)[:12],
			Head: headLabel,
		}

		// Process added files
		for _, path := range added {
			fd, _ := differ.DiffFile(path, nil, headContent[path])
			if fd != nil {
				sd.Files = append(sd.Files, *fd)
			}
		}

		// Process modified files
		for _, path := range modified {
			fd, _ := differ.DiffFile(path, baseContent[path], headContent[path])
			if fd != nil {
				sd.Files = append(sd.Files, *fd)
			}
		}

		// Process deleted files
		for _, path := range deleted {
			fd, _ := differ.DiffFile(path, baseContent[path], nil)
			if fd != nil {
				sd.Files = append(sd.Files, *fd)
			}
		}

		sd.ComputeSummary()

		if diffJSON {
			jsonOut, err := sd.FormatJSON()
			if err != nil {
				return fmt.Errorf("formatting JSON: %w", err)
			}
			fmt.Println(string(jsonOut))
		} else {
			fmt.Printf("Diff: %s → %s\n\n", sd.Base, sd.Head)
			fmt.Print(sd.FormatText())
		}

		return nil
	}

	// Patch mode - line-level diff like git
	if diffPatch {
		fmt.Printf("Diff: %s → %s\n\n", util.BytesToHex(baseSnapID)[:12], headLabel)

		// Show added files
		for _, path := range added {
			fmt.Printf("\033[1mdiff --kai a/%s b/%s\033[0m\n", path, path)
			fmt.Println("--- /dev/null")
			fmt.Printf("+++ b/%s\n", path)
			content := headContent[path]
			if content != nil {
				lines := strings.Split(string(content), "\n")
				fmt.Printf("@@ -0,0 +1,%d @@\n", len(lines))
				for _, line := range lines {
					fmt.Printf("\033[32m+%s\033[0m\n", line)
				}
			}
			fmt.Println()
		}

		// Show modified files
		for _, path := range modified {
			fmt.Printf("\033[1mdiff --kai a/%s b/%s\033[0m\n", path, path)
			fmt.Printf("--- a/%s\n", path)
			fmt.Printf("+++ b/%s\n", path)
			before := baseContent[path]
			after := headContent[path]
			if before != nil && after != nil {
				showUnifiedDiff(string(before), string(after))
			}
			fmt.Println()
		}

		// Show deleted files
		for _, path := range deleted {
			fmt.Printf("\033[1mdiff --kai a/%s b/%s\033[0m\n", path, path)
			fmt.Printf("--- a/%s\n", path)
			fmt.Println("+++ /dev/null")
			content := baseContent[path]
			if content != nil {
				lines := strings.Split(string(content), "\n")
				fmt.Printf("@@ -1,%d +0,0 @@\n", len(lines))
				for _, line := range lines {
					fmt.Printf("\033[31m-%s\033[0m\n", line)
				}
			}
			fmt.Println()
		}

		return nil
	}

	// Simple file-level output
	if !diffJSON {
		fmt.Printf("Diff: %s → %s\n\n", util.BytesToHex(baseSnapID)[:12], headLabel)
	}

	if diffNameOnly {
		for _, path := range added {
			fmt.Printf("A %s\n", path)
		}
		for _, path := range modified {
			fmt.Printf("M %s\n", path)
		}
		for _, path := range deleted {
			fmt.Printf("D %s\n", path)
		}
	} else {
		if len(added) > 0 {
			fmt.Printf("Added (%d):\n", len(added))
			for _, path := range added {
				fmt.Printf("  + %s\n", path)
			}
			fmt.Println()
		}

		if len(modified) > 0 {
			fmt.Printf("Modified (%d):\n", len(modified))
			for _, path := range modified {
				fmt.Printf("  ~ %s\n", path)
			}
			fmt.Println()
		}

		if len(deleted) > 0 {
			fmt.Printf("Deleted (%d):\n", len(deleted))
			for _, path := range deleted {
				fmt.Printf("  - %s\n", path)
			}
		}
	}

	return nil
}

// Workspace command implementations

func runWsCreate(cmd *cobra.Command, args []string) error {
	// Get workspace name from positional arg or --name flag
	name := wsName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("workspace name required (pass as argument or use --name)")
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var baseID []byte

	// Count how many base sources are specified
	sourceCount := 0
	if wsBase != "" {
		sourceCount++
	}
	if wsFromDir != "" {
		sourceCount++
	}
	if wsFromGit != "" {
		sourceCount++
	}

	// Check for conflicting options
	if sourceCount > 1 {
		fmt.Println()
		fmt.Println("╭─ Conflicting base options")
		fmt.Println("│")
		fmt.Println("│  You specified multiple base sources. Use only one of:")
		fmt.Println("│")
		fmt.Println("│    --from-dir <path>    # From directory snapshot")
		fmt.Println("│    --from-git <ref>     # From Git commit/branch/tag")
		fmt.Println("│    --base <selector>    # From existing snapshot")
		fmt.Println("│")
		fmt.Println("╰────────────────────────────────────────────")
		return fmt.Errorf("conflicting base options: use only one of --from-dir, --from-git, or --base")
	}

	if wsFromDir != "" {
		// Create base from directory
		fmt.Printf("Creating base snapshot from directory: %s\n", wsFromDir)
		fmt.Println()
		baseID, err = createSnapshotFromDir(db, wsFromDir)
		if err != nil {
			return fmt.Errorf("creating directory snapshot: %w", err)
		}
		fmt.Println()
	} else if wsFromGit != "" {
		// Create base from Git ref
		fmt.Printf("Creating base snapshot from Git ref: %s\n", wsFromGit)
		matcher, err := loadMatcher()
		if err != nil {
			return err
		}
		source, err := gitio.OpenSource(".", wsFromGit)
		if err != nil {
			return fmt.Errorf("opening git ref: %w", err)
		}
		creator := snapshot.NewCreator(db, matcher)
		baseID, err = creator.CreateSnapshot(source)
		if err != nil {
			return fmt.Errorf("creating git snapshot: %w", err)
		}
		// Analyze symbols
		progress := func(current, total int, filename string) {}
		_ = creator.AnalyzeSymbols(baseID, progress)

		// Update refs
		autoRefMgr := ref.NewAutoRefManager(db)
		_ = autoRefMgr.OnSnapshotCreated(baseID)
		fmt.Printf("Created base snapshot: %s\n", util.BytesToHex(baseID)[:12])
		fmt.Println()
	} else if wsBase != "" {
		// Explicit base snapshot provided
		baseID, err = resolveSnapshotID(db, wsBase)
		if err != nil {
			return fmt.Errorf("resolving base snapshot: %w", err)
		}
	} else {
		// Auto-snapshot current directory (default behavior)
		fmt.Println("No base specified, auto-snapshotting current directory...")
		fmt.Println()
		baseID, err = createSnapshotFromDir(db, ".")
		if err != nil {
			return fmt.Errorf("auto-snapshot failed: %w", err)
		}
		fmt.Println()
	}

	mgr := workspace.NewManager(db)
	ws, err := mgr.Create(name, baseID, wsDescription)
	if err != nil {
		return fmt.Errorf("creating workspace: %w", err)
	}

	// Update auto-refs
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnWorkspaceCreated(name, baseID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	fmt.Printf("Created workspace: %s\n", name)
	fmt.Printf("ID: %s\n", util.BytesToHex(ws.ID))
	fmt.Printf("Base snapshot: %s\n", util.BytesToHex(ws.BaseSnapshot)[:12])
	return nil
}

func runWsList(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)
	workspaces, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}

	fmt.Printf("%-20s  %-10s  %-12s  %-12s  %s\n", "NAME", "STATUS", "BASE", "HEAD", "CHANGESETS")
	fmt.Println(strings.Repeat("-", 80))
	for _, ws := range workspaces {
		baseStr := ""
		headStr := ""
		if len(ws.BaseSnapshot) > 0 {
			baseStr = util.BytesToHex(ws.BaseSnapshot)[:12]
		}
		if len(ws.HeadSnapshot) > 0 {
			headStr = util.BytesToHex(ws.HeadSnapshot)[:12]
		}
		fmt.Printf("%-20s  %-10s  %-12s  %-12s  %d\n",
			ws.Name, ws.Status, baseStr, headStr, len(ws.OpenChangeSets))
	}

	return nil
}

func runWsStage(cmd *cobra.Command, args []string) error {
	// Resolve workspace name: positional arg > --ws flag > current workspace
	name := wsName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		// Try current workspace
		current, err := getCurrentWorkspace()
		if err != nil {
			return fmt.Errorf("reading current workspace: %w", err)
		}
		if current == "" {
			return fmt.Errorf("no workspace specified. Use 'kai ws stage <name>' or 'kai ws checkout <name>' first")
		}
		name = current
		fmt.Printf("Using current workspace: %s\n", name)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	// Open directory source
	source, err := dirio.OpenDirectory(wsDir)
	if err != nil {
		return fmt.Errorf("opening directory: %w", err)
	}

	mgr := workspace.NewManager(db)
	result, err := mgr.Stage(name, source, matcher, wsStageMessage)
	if err != nil {
		return fmt.Errorf("staging changes: %w", err)
	}

	if result.ChangedFiles == 0 {
		fmt.Println("No changes to stage.")
		return nil
	}

	if len(result.Conflicts) > 0 {
		fmt.Printf("Conflicts detected (%d):\n", len(result.Conflicts))
		for _, c := range result.Conflicts {
			fmt.Printf("  %s: %s\n", c.Path, c.Description)
		}
		return fmt.Errorf("resolve conflicts before staging")
	}

	// Update auto-refs
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnWorkspaceHeadChanged(name, result.HeadSnapshot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}
	if err := autoRefMgr.OnChangeSetCreated(result.ChangeSetID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	fmt.Printf("Staged changes:\n")
	fmt.Printf("  Changeset: %s\n", util.BytesToHex(result.ChangeSetID)[:12])
	fmt.Printf("  New head:  %s\n", util.BytesToHex(result.HeadSnapshot)[:12])
	fmt.Printf("  Files:     %d changed\n", result.ChangedFiles)
	fmt.Printf("  Changes:   %d change types detected\n", result.ChangeTypes)

	return nil
}

func runWsLog(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)

	ws, err := mgr.Get(wsName)
	if err != nil {
		return err
	}
	if ws == nil {
		return fmt.Errorf("workspace not found: %s", wsName)
	}

	changesets, err := mgr.GetLog(wsName)
	if err != nil {
		return fmt.Errorf("getting workspace log: %w", err)
	}

	fmt.Printf("Workspace: %s\n", ws.Name)
	fmt.Printf("Status:    %s\n", ws.Status)
	fmt.Printf("Base:      %s\n", util.BytesToHex(ws.BaseSnapshot)[:12])
	fmt.Printf("Head:      %s\n", util.BytesToHex(ws.HeadSnapshot)[:12])
	fmt.Println()

	if len(changesets) == 0 {
		fmt.Println("No changesets yet.")
		return nil
	}

	fmt.Printf("Changesets (%d):\n", len(changesets))
	for i, cs := range changesets {
		description, _ := cs.Payload["description"].(string)
		intent, _ := cs.Payload["intent"].(string)
		createdAt, _ := cs.Payload["createdAt"].(float64)
		t := time.UnixMilli(int64(createdAt))

		fmt.Printf("\n  [%d] %s\n", i+1, util.BytesToHex(cs.ID)[:12])
		fmt.Printf("      Date:   %s\n", t.Format("2006-01-02 15:04:05"))
		if description != "" {
			fmt.Printf("      Message: %s\n", description)
		}
		if intent != "" {
			fmt.Printf("      Intent: %s\n", intent)
		}
	}

	return nil
}

func runWsShelve(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)
	if err := mgr.Shelve(wsName); err != nil {
		return fmt.Errorf("shelving workspace: %w", err)
	}

	fmt.Printf("Workspace %q shelved.\n", wsName)
	return nil
}

func runWsUnshelve(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)
	if err := mgr.Unshelve(wsName); err != nil {
		return fmt.Errorf("unshelving workspace: %w", err)
	}

	fmt.Printf("Workspace %q unshelved.\n", wsName)
	return nil
}

func runWsClose(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)
	if err := mgr.Close(wsName); err != nil {
		return fmt.Errorf("closing workspace: %w", err)
	}

	fmt.Printf("Workspace %q closed.\n", wsName)
	return nil
}

func runWsDelete(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)

	// Dry-run: show plan
	if wsDeleteDryRun {
		plan, err := mgr.PlanDelete(wsName)
		if err != nil {
			return fmt.Errorf("planning delete: %w", err)
		}

		fmt.Println("Plan:")
		fmt.Printf("  Remove Workspace node: %s\n", util.BytesToHex(plan.WorkspaceID)[:12])
		fmt.Printf("  Remove edges: %d\n", plan.EdgesRemoved)
		fmt.Printf("  Remove refs: %s\n", strings.Join(plan.RefsRemoved, ", "))
		if plan.OrphanedCSCount > 0 {
			fmt.Printf("Note: %d ChangeSet(s) will become unreferenced and eligible for GC.\n", plan.OrphanedCSCount)
		}
		fmt.Println("Run without --dry-run to apply.")
		return nil
	}

	// Actually delete
	if err := mgr.Delete(wsName, wsDeleteKeepRefs); err != nil {
		return fmt.Errorf("deleting workspace: %w", err)
	}

	fmt.Printf("Workspace %q deleted.\n", wsName)
	fmt.Println("Run `kai prune` to reclaim storage.")
	return nil
}

func runWsCheckout(cmd *cobra.Command, args []string) error {
	// Get workspace name from positional arg or --ws flag
	name := wsName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("workspace name required (pass as argument or use --ws)")
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Get the workspace
	mgr := workspace.NewManager(db)
	ws, err := mgr.Get(name)
	if err != nil {
		return fmt.Errorf("getting workspace: %w", err)
	}
	if ws == nil {
		return fmt.Errorf("workspace %q not found", name)
	}

	// Resolve target directory to absolute path
	targetDir, err := filepath.Abs(wsDir)
	if err != nil {
		return fmt.Errorf("resolving target directory: %w", err)
	}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}

	// Checkout the head snapshot
	creator := snapshot.NewCreator(db, nil)
	result, err := creator.Checkout(ws.HeadSnapshot, targetDir, wsCheckoutClean)
	if err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}

	// Set as current workspace
	if err := setCurrentWorkspace(name); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to set current workspace: %v\n", err)
	}

	fmt.Printf("Checkout complete!\n")
	fmt.Printf("  Workspace: %s\n", ws.Name)
	fmt.Printf("  Snapshot:  %s\n", util.BytesToHex(ws.HeadSnapshot)[:12])
	fmt.Printf("  Target:    %s\n", result.TargetDir)
	fmt.Printf("  Written:   %d file(s)\n", result.FilesWritten)
	if result.FilesDeleted > 0 {
		fmt.Printf("  Deleted:   %d file(s)\n", result.FilesDeleted)
	}
	if result.FilesSkipped > 0 {
		fmt.Printf("  Skipped:   %d file(s)\n", result.FilesSkipped)
	}
	fmt.Printf("\nNow on workspace: %s\n", name)

	return nil
}

func runWsCurrent(cmd *cobra.Command, args []string) error {
	current, err := getCurrentWorkspace()
	if err != nil {
		return fmt.Errorf("reading current workspace: %w", err)
	}
	if current == "" {
		fmt.Println("No workspace checked out.")
		fmt.Println("Use 'kai ws checkout <name>' to switch to a workspace.")
		return nil
	}
	fmt.Println(current)
	return nil
}

func runIntegrate(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	targetID, err := resolveSnapshotID(db, wsTarget)
	if err != nil {
		return fmt.Errorf("resolving target snapshot: %w", err)
	}

	mgr := workspace.NewManager(db)
	result, err := mgr.Integrate(wsName, targetID)
	if err != nil {
		return fmt.Errorf("integrating workspace: %w", err)
	}

	if len(result.Conflicts) > 0 {
		fmt.Printf("Integration conflicts (%d):\n", len(result.Conflicts))
		for _, c := range result.Conflicts {
			fmt.Printf("  %s: %s\n", c.Path, c.Description)
		}
		return fmt.Errorf("resolve conflicts before integration")
	}

	fmt.Printf("Integration successful!\n")
	fmt.Printf("  Result snapshot: %s\n", util.BytesToHex(result.ResultSnapshot))
	fmt.Printf("  Applied %d changeset(s)\n", len(result.AppliedChangeSets))
	if result.AutoResolved > 0 {
		fmt.Printf("  Auto-resolved: %d change(s)\n", result.AutoResolved)
	}

	return nil
}

func runMerge(cmd *cobra.Command, args []string) error {
	baseFile := args[0]
	leftFile := args[1]
	rightFile := args[2]

	// Read file contents
	baseContent, err := os.ReadFile(baseFile)
	if err != nil {
		return fmt.Errorf("reading base file: %w", err)
	}
	leftContent, err := os.ReadFile(leftFile)
	if err != nil {
		return fmt.Errorf("reading left file: %w", err)
	}
	rightContent, err := os.ReadFile(rightFile)
	if err != nil {
		return fmt.Errorf("reading right file: %w", err)
	}

	// Detect language from extension if not specified
	lang := mergeLang
	if lang == "" {
		ext := strings.ToLower(filepath.Ext(baseFile))
		switch ext {
		case ".js":
			lang = "js"
		case ".ts", ".tsx":
			lang = "ts"
		case ".py":
			lang = "py"
		case ".go":
			lang = "go"
		default:
			lang = "js" // fallback
		}
	}

	// Perform merge
	result, err := merge.Merge3Way(baseContent, leftContent, rightContent, lang)
	if err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	// Output as JSON if requested
	if mergeJSON {
		type jsonConflict struct {
			Kind      string `json:"kind"`
			Unit      string `json:"unit"`
			Message   string `json:"message"`
			LeftDiff  string `json:"leftDiff,omitempty"`
			RightDiff string `json:"rightDiff,omitempty"`
		}
		type jsonResult struct {
			Success   bool           `json:"success"`
			Conflicts []jsonConflict `json:"conflicts,omitempty"`
			Merged    string         `json:"merged,omitempty"`
		}

		jr := jsonResult{
			Success: result.Success,
		}
		for _, c := range result.Conflicts {
			jr.Conflicts = append(jr.Conflicts, jsonConflict{
				Kind:      string(c.Kind),
				Unit:      c.UnitKey.String(),
				Message:   c.Message,
				LeftDiff:  c.LeftDiff,
				RightDiff: c.RightDiff,
			})
		}
		if merged := result.Files["file"]; merged != nil {
			jr.Merged = string(merged)
		}

		out, err := json.MarshalIndent(jr, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling result: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}

	// Text output
	if !result.Success {
		fmt.Fprintf(os.Stderr, "Merge conflicts detected (%d):\n\n", len(result.Conflicts))
		for _, c := range result.Conflicts {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", c.Kind, c.Message)
			fmt.Fprintf(os.Stderr, "    Unit: %s\n", c.UnitKey.String())
			if c.LeftDiff != "" {
				fmt.Fprintf(os.Stderr, "    Left:  %s\n", c.LeftDiff)
			}
			if c.RightDiff != "" {
				fmt.Fprintf(os.Stderr, "    Right: %s\n", c.RightDiff)
			}
			fmt.Fprintln(os.Stderr)
		}
		return fmt.Errorf("merge has conflicts")
	}

	// Success - output merged content
	merged := result.Files["file"]
	if merged == nil {
		return fmt.Errorf("no merged content produced")
	}

	if mergeOutput != "" {
		if err := os.WriteFile(mergeOutput, merged, 0644); err != nil {
			return fmt.Errorf("writing output file: %w", err)
		}
		fmt.Printf("Merged successfully -> %s\n", mergeOutput)
	} else {
		fmt.Print(string(merged))
	}

	return nil
}

func runCheckout(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snapshotID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving snapshot ID: %w", err)
	}

	// Resolve target directory to absolute path
	targetDir, err := filepath.Abs(checkoutDir)
	if err != nil {
		return fmt.Errorf("resolving target directory: %w", err)
	}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}

	creator := snapshot.NewCreator(db, nil)
	result, err := creator.Checkout(snapshotID, targetDir, checkoutClean)
	if err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}

	fmt.Printf("Checkout complete!\n")
	fmt.Printf("  Target:  %s\n", result.TargetDir)
	fmt.Printf("  Written: %d file(s)\n", result.FilesWritten)
	if result.FilesDeleted > 0 {
		fmt.Printf("  Deleted: %d file(s)\n", result.FilesDeleted)
	}
	if result.FilesSkipped > 0 {
		fmt.Printf("  Skipped: %d file(s)\n", result.FilesSkipped)
	}

	return nil
}

// Ref command implementations

func runRefList(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	refMgr := ref.NewRefManager(db)

	var filterKind *ref.Kind
	if refKindFilter != "" {
		k := ref.Kind(refKindFilter)
		filterKind = &k
	}

	refs, err := refMgr.List(filterKind)
	if err != nil {
		return fmt.Errorf("listing refs: %w", err)
	}

	if len(refs) == 0 {
		fmt.Println("No refs found.")
		return nil
	}

	fmt.Printf("%-30s  %-12s  %s\n", "NAME", "KIND", "TARGET")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range refs {
		fmt.Printf("%-30s  %-12s  %s\n", r.Name, r.TargetKind, util.BytesToHex(r.TargetID)[:16]+"...")
	}

	return nil
}

func runRefSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	target := args[1]

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	resolver := ref.NewResolver(db)
	refMgr := ref.NewRefManager(db)

	// Resolve the target
	result, err := resolver.Resolve(target, nil)
	if err != nil {
		return fmt.Errorf("resolving target: %w", err)
	}

	// Set the ref
	if err := refMgr.Set(name, result.ID, result.Kind); err != nil {
		return fmt.Errorf("setting ref: %w", err)
	}

	fmt.Printf("Set ref '%s' -> %s (%s)\n", name, util.BytesToHex(result.ID)[:16]+"...", result.Kind)
	return nil
}

func runRefDel(cmd *cobra.Command, args []string) error {
	name := args[0]

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	refMgr := ref.NewRefManager(db)
	if err := refMgr.Delete(name); err != nil {
		return fmt.Errorf("deleting ref: %w", err)
	}

	fmt.Printf("Deleted ref '%s'\n", name)
	return nil
}

func runPick(cmd *cobra.Command, args []string) error {
	kindArg := args[0]

	// Normalize kind
	var kind ref.Kind
	switch strings.ToLower(kindArg) {
	case "snapshot", "snap":
		kind = ref.KindSnapshot
	case "changeset", "cs":
		kind = ref.KindChangeSet
	case "workspace", "ws":
		kind = ref.KindWorkspace
	default:
		return fmt.Errorf("unknown kind: %s (use Snapshot, ChangeSet, or Workspace)", kindArg)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	nodes, err := db.GetNodesByKind(graph.NodeKind(kind))
	if err != nil {
		return fmt.Errorf("getting nodes: %w", err)
	}

	// Filter if requested
	var filtered []*graph.Node
	for _, node := range nodes {
		if pickFilter == "" {
			filtered = append(filtered, node)
			continue
		}

		// Check if filter matches ID or payload
		idHex := util.BytesToHex(node.ID)
		if strings.Contains(idHex, pickFilter) {
			filtered = append(filtered, node)
			continue
		}

		// Check payload fields
		for _, v := range node.Payload {
			if str, ok := v.(string); ok && strings.Contains(str, pickFilter) {
				filtered = append(filtered, node)
				break
			}
		}
	}

	if len(filtered) == 0 {
		fmt.Println("No matches found.")
		return nil
	}

	// Sort by created_at descending
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt > filtered[j].CreatedAt
	})

	// Output matches
	fmt.Printf("%-4s  %-16s  %s\n", "#", "ID", "INFO")
	fmt.Println(strings.Repeat("-", 60))
	for i, node := range filtered {
		idHex := util.BytesToHex(node.ID)[:16]

		var info string
		switch kind {
		case ref.KindSnapshot:
			sourceRef, _ := node.Payload["sourceRef"].(string)
			sourceType, _ := node.Payload["sourceType"].(string)
			if sourceRef == "" {
				sourceRef, _ = node.Payload["gitRef"].(string)
				sourceType = "git"
			}
			info = fmt.Sprintf("%s (%s)", sourceRef, sourceType)
		case ref.KindChangeSet:
			intent, _ := node.Payload["intent"].(string)
			if intent == "" {
				intent = "(no intent)"
			}
			info = intent
		case ref.KindWorkspace:
			name, _ := node.Payload["name"].(string)
			status, _ := node.Payload["status"].(string)
			info = fmt.Sprintf("%s [%s]", name, status)
		}

		fmt.Printf("%-4d  %s...  %s\n", i+1, idHex, info)
	}

	if pickNoUI {
		return nil
	}

	// Output the first match's full ID for scripting
	fmt.Printf("\nFirst match: %s\n", util.BytesToHex(filtered[0].ID))
	return nil
}

// resolveID resolves a user-provided ID string to a full ID bytes.
// It supports full hex IDs, short prefixes, refs, selectors, and slugs.
func resolveID(db *graph.DB, input string, wantKind *ref.Kind) ([]byte, error) {
	resolver := ref.NewResolver(db)
	result, err := resolver.Resolve(input, wantKind)
	if err != nil {
		return nil, err
	}
	return result.ID, nil
}

// resolveSnapshotID is a convenience wrapper for resolving snapshot IDs.
func resolveSnapshotID(db *graph.DB, input string) ([]byte, error) {
	kind := ref.KindSnapshot
	return resolveID(db, input, &kind)
}

// resolveChangeSetID is a convenience wrapper for resolving changeset IDs.
func resolveChangeSetID(db *graph.DB, input string) ([]byte, error) {
	kind := ref.KindChangeSet
	return resolveID(db, input, &kind)
}

func runCompletion(cmd *cobra.Command, args []string) error {
	switch args[0] {
	case "bash":
		return cmd.Root().GenBashCompletion(os.Stdout)
	case "zsh":
		return cmd.Root().GenZshCompletion(os.Stdout)
	case "fish":
		return cmd.Root().GenFishCompletion(os.Stdout, true)
	case "powershell":
		return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
	default:
		return fmt.Errorf("unknown shell: %s", args[0])
	}
}

// completeSnapshotID provides shell completion for snapshot IDs.
func completeSnapshotID(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeNodeID(ref.KindSnapshot, toComplete)
}

// completeChangeSetID provides shell completion for changeset IDs.
func completeChangeSetID(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeNodeID(ref.KindChangeSet, toComplete)
}

// completeNodeID provides shell completion for node IDs of a specific kind.
func completeNodeID(kind ref.Kind, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Always include selectors
	selectors := []string{}
	switch kind {
	case ref.KindSnapshot:
		selectors = []string{"@snap:last", "@snap:prev"}
	case ref.KindChangeSet:
		selectors = []string{"@cs:last", "@cs:prev"}
	}

	// Try to open DB for refs
	db, err := openDB()
	if err != nil {
		return selectors, cobra.ShellCompDirectiveNoFileComp
	}
	defer db.Close()

	var completions []string
	completions = append(completions, selectors...)

	// Add matching refs
	refMgr := ref.NewRefManager(db)
	refs, err := refMgr.List(&kind)
	if err == nil {
		for _, r := range refs {
			if strings.HasPrefix(r.Name, toComplete) || toComplete == "" {
				completions = append(completions, r.Name)
			}
		}
	}

	// Add matching short IDs (up to 10)
	if len(toComplete) >= 3 {
		nodes, err := db.GetNodesByKind(graph.NodeKind(kind))
		if err == nil {
			count := 0
			for _, n := range nodes {
				if count >= 10 {
					break
				}
				idHex := util.BytesToHex(n.ID)
				if strings.HasPrefix(idHex, toComplete) {
					// Show short ID with description
					completions = append(completions, idHex[:12])
					count++
				}
			}
		}
	}

	// Add recent nodes if no specific prefix
	if toComplete == "" {
		nodes, err := db.GetNodesByKind(graph.NodeKind(kind))
		if err == nil {
			sort.Slice(nodes, func(i, j int) bool {
				return nodes[i].CreatedAt > nodes[j].CreatedAt
			})
			for i, n := range nodes {
				if i >= 5 {
					break
				}
				idHex := util.BytesToHex(n.ID)[:12]
				completions = append(completions, idHex)
			}
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// completeRefName provides shell completion for ref names.
func completeRefName(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	db, err := openDB()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer db.Close()

	refMgr := ref.NewRefManager(db)
	refs, err := refMgr.List(nil)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var completions []string
	for _, r := range refs {
		if strings.HasPrefix(r.Name, toComplete) || toComplete == "" {
			completions = append(completions, r.Name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// Remote command implementations

func runRemoteSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	rawURL := args[1]

	tenant := remoteTenant
	repo := remoteRepo

	// Parse URL to extract tenant/repo from path if not specified via flags
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Extract tenant/repo from path if flags are at default values
	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) >= 2 && tenant == "default" && repo == "main" {
		tenant = pathParts[0]
		repo = pathParts[1]
		// Rebuild base URL without tenant/repo path
		parsedURL.Path = ""
		rawURL = parsedURL.String()
	}

	entry := &remote.RemoteEntry{
		URL:    rawURL,
		Tenant: tenant,
		Repo:   repo,
	}

	if err := remote.SetRemote(name, entry); err != nil {
		return fmt.Errorf("setting remote: %w", err)
	}

	fmt.Printf("Remote '%s' set to: %s (tenant=%s, repo=%s)\n", name, rawURL, tenant, repo)
	return nil
}

func runRemoteGet(cmd *cobra.Command, args []string) error {
	name := args[0]

	entry, err := remote.GetRemote(name)
	if err != nil {
		return err
	}

	fmt.Printf("URL:    %s\n", entry.URL)
	fmt.Printf("Tenant: %s\n", entry.Tenant)
	fmt.Printf("Repo:   %s\n", entry.Repo)
	return nil
}

func runRemoteList(cmd *cobra.Command, args []string) error {
	remotes, err := remote.ListRemotes()
	if err != nil {
		return fmt.Errorf("loading remotes: %w", err)
	}

	if len(remotes) == 0 {
		fmt.Println("No remotes configured.")
		fmt.Println("Use 'kai remote set <name> <url>' to add a remote.")
		return nil
	}

	fmt.Printf("%-15s  %-12s  %-12s  %s\n", "NAME", "TENANT", "REPO", "URL")
	fmt.Println(strings.Repeat("-", 80))
	for name, entry := range remotes {
		fmt.Printf("%-15s  %-12s  %-12s  %s\n", name, entry.Tenant, entry.Repo, entry.URL)
	}

	return nil
}

func runRemoteDel(cmd *cobra.Command, args []string) error {
	name := args[0]

	if err := remote.DeleteRemote(name); err != nil {
		return fmt.Errorf("deleting remote: %w", err)
	}

	fmt.Printf("Deleted remote '%s'\n", name)
	return nil
}

func runPush(cmd *cobra.Command, args []string) error {
	// Show explain if requested
	if pushExplain {
		remoteName := "origin"
		refSpec := "*"
		if len(args) > 0 {
			remoteName = args[0]
		}
		if len(args) > 1 {
			refSpec = args[1]
		}
		ctx := explain.ExplainPush(remoteName, refSpec, 0) // 0 refs - count determined later
		ctx.Print(os.Stdout)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Determine remote name and targets
	remoteName := "origin"
	targets := []string{}

	if len(args) > 0 {
		// Check if first arg is a remote name
		if _, err := remote.GetRemote(args[0]); err == nil {
			remoteName = args[0]
			targets = args[1:]
		} else {
			// First arg is a target
			targets = args
		}
	}

	// Create client for remote
	client, err := remote.NewClientForRemote(remoteName)
	if err != nil {
		return fmt.Errorf("remote '%s' not configured (use 'kai remote set %s <url>')", remoteName, remoteName)
	}

	// Check server health
	if err := client.Health(); err != nil {
		return fmt.Errorf("cannot connect to %s: %w", client.BaseURL, err)
	}

	// Get refs to push
	refMgr := ref.NewRefManager(db)
	wsMgr := workspace.NewManager(db)
	reviewMgr := review.NewManager(db)
	var refsToSync []*ref.Ref
	var workspaceToPush *workspace.Workspace
	var reviewToPush *review.Review

	if pushAll {
		// Legacy: push all refs
		refsToSync, err = refMgr.List(nil)
		if err != nil {
			return fmt.Errorf("listing refs: %w", err)
		}
	} else if pushWorkspace != "" {
		// Push specific workspace
		workspaceToPush, err = wsMgr.Get(pushWorkspace)
		if err != nil {
			return fmt.Errorf("getting workspace: %w", err)
		}
		if workspaceToPush == nil {
			return fmt.Errorf("workspace not found: %s", pushWorkspace)
		}
	} else if len(targets) > 0 {
		// Parse targets with prefixes
		for _, target := range targets {
			if strings.HasPrefix(target, "cs:") {
				// Changeset target
				csRef := strings.TrimPrefix(target, "cs:")
				r, err := refMgr.Get("cs." + csRef)
				if err != nil {
					return fmt.Errorf("getting changeset ref 'cs.%s': %w", csRef, err)
				}
				if r == nil {
					// Try without prefix
					r, err = refMgr.Get(csRef)
					if err != nil {
						return fmt.Errorf("getting changeset ref '%s': %w", csRef, err)
					}
				}
				if r != nil {
					refsToSync = append(refsToSync, r)
				}
			} else if strings.HasPrefix(target, "snap:") {
				// Snapshot target
				snapRef := strings.TrimPrefix(target, "snap:")
				r, err := refMgr.Get("snap." + snapRef)
				if err != nil {
					return fmt.Errorf("getting snapshot ref 'snap.%s': %w", snapRef, err)
				}
				if r == nil {
					// Try without prefix
					r, err = refMgr.Get(snapRef)
					if err != nil {
						return fmt.Errorf("getting snapshot ref '%s': %w", snapRef, err)
					}
				}
				if r != nil {
					refsToSync = append(refsToSync, r)
				}
			} else if strings.HasPrefix(target, "review:") {
				// Review target
				reviewRef := strings.TrimPrefix(target, "review:")
				rev, err := reviewMgr.GetByShortID(reviewRef)
				if err != nil {
					return fmt.Errorf("getting review '%s': %w", reviewRef, err)
				}
				reviewToPush = rev
			} else {
				// Legacy: direct ref name
				r, err := refMgr.Get(target)
				if err != nil {
					return fmt.Errorf("getting ref '%s': %w", target, err)
				}
				if r != nil {
					refsToSync = append(refsToSync, r)
				}
			}
		}
	} else {
		// No workspace specified, no targets - push snap.latest and cs.latest
		r, _ := refMgr.Get("snap.latest")
		if r != nil {
			refsToSync = append(refsToSync, r)
		}
		r, _ = refMgr.Get("cs.latest")
		if r != nil {
			refsToSync = append(refsToSync, r)
		}
	}

	// Track pre-computed pack objects for UUID-based nodes (workspace, review)
	// Key: content-addressed digest hex, Value: pre-built PackObject
	precomputedPackObjects := make(map[string]remote.PackObject)

	// If pushing a workspace, collect its changesets and snapshots
	if workspaceToPush != nil {
		fmt.Printf("Pushing workspace '%s'...\n", workspaceToPush.Name)

		// Get workspace node to compute content-addressed digest
		wsNode, err := db.GetNode(workspaceToPush.ID)
		if err != nil || wsNode == nil {
			return fmt.Errorf("getting workspace node: %w", err)
		}

		// Compute content-addressed digest for workspace
		// Include UUID in payload so it can be reconstructed on fetch
		wsPayload := make(map[string]interface{})
		for k, v := range wsNode.Payload {
			wsPayload[k] = v
		}
		wsPayload["_uuid"] = hex.EncodeToString(workspaceToPush.ID)
		wsPayloadJSON, err := util.CanonicalJSON(wsPayload)
		if err != nil {
			return fmt.Errorf("serializing workspace payload: %w", err)
		}
		wsContent := append([]byte(string(graph.KindWorkspace)+"\n"), wsPayloadJSON...)
		wsContentDigest := util.Blake3Hash(wsContent)

		// Store pre-computed pack object for workspace
		precomputedPackObjects[hex.EncodeToString(wsContentDigest)] = remote.PackObject{
			Digest:  wsContentDigest,
			Kind:    string(graph.KindWorkspace),
			Content: wsContent,
		}

		// Add workspace node ref (for fetch --ws to work)
		// Uses content-addressed digest so server can verify
		wsNodeRef := &ref.Ref{
			Name:       fmt.Sprintf("ws.%s", workspaceToPush.Name),
			TargetID:   wsContentDigest,
			TargetKind: ref.KindWorkspace,
		}
		// Add workspace snapshot refs
		wsBaseRef := &ref.Ref{
			Name:       fmt.Sprintf("ws.%s.base", workspaceToPush.Name),
			TargetID:   workspaceToPush.BaseSnapshot,
			TargetKind: ref.KindSnapshot,
		}
		wsHeadRef := &ref.Ref{
			Name:       fmt.Sprintf("ws.%s.head", workspaceToPush.Name),
			TargetID:   workspaceToPush.HeadSnapshot,
			TargetKind: ref.KindSnapshot,
		}
		refsToSync = append(refsToSync, wsNodeRef, wsBaseRef, wsHeadRef)

		// Add all changesets in the workspace
		for _, csID := range workspaceToPush.OpenChangeSets {
			// Get the changeset node to find its base/head snapshots
			csNode, err := db.GetNode(csID)
			if err != nil || csNode == nil {
				continue
			}

			// Create a ref for this changeset
			csRefName := fmt.Sprintf("ws.%s.cs.%s", workspaceToPush.Name, hex.EncodeToString(csID)[:8])
			csRef := &ref.Ref{
				Name:     csRefName,
				TargetID: csID,
			}
			refsToSync = append(refsToSync, csRef)
		}

		fmt.Printf("  Base: %s\n", hex.EncodeToString(workspaceToPush.BaseSnapshot)[:12])
		fmt.Printf("  Head: %s\n", hex.EncodeToString(workspaceToPush.HeadSnapshot)[:12])
		fmt.Printf("  Changesets: %d\n", len(workspaceToPush.OpenChangeSets))
	}

	// If pushing a review, collect it and its target changeset
	if reviewToPush != nil {
		fmt.Printf("Pushing review '%s'...\n", reviewToPush.Title)

		// Get review node to compute content-addressed digest
		reviewNode, err := db.GetNode(reviewToPush.ID)
		if err != nil || reviewNode == nil {
			return fmt.Errorf("getting review node: %w", err)
		}

		// Compute content-addressed digest for review
		// Include UUID in payload so it can be reconstructed on fetch
		reviewPayload := make(map[string]interface{})
		for k, v := range reviewNode.Payload {
			reviewPayload[k] = v
		}
		reviewPayload["_uuid"] = hex.EncodeToString(reviewToPush.ID)
		reviewPayloadJSON, err := util.CanonicalJSON(reviewPayload)
		if err != nil {
			return fmt.Errorf("serializing review payload: %w", err)
		}
		reviewContent := append([]byte(string(graph.KindReview)+"\n"), reviewPayloadJSON...)
		reviewContentDigest := util.Blake3Hash(reviewContent)

		// Store pre-computed pack object for review
		precomputedPackObjects[hex.EncodeToString(reviewContentDigest)] = remote.PackObject{
			Digest:  reviewContentDigest,
			Kind:    string(graph.KindReview),
			Content: reviewContent,
		}

		// Add review ref
		reviewShortID := hex.EncodeToString(reviewToPush.ID)[:12]
		reviewNodeRef := &ref.Ref{
			Name:       fmt.Sprintf("review.%s", reviewShortID),
			TargetID:   reviewContentDigest,
			TargetKind: ref.KindReview,
		}
		refsToSync = append(refsToSync, reviewNodeRef)

		// Also push the target changeset (if it's a changeset)
		if reviewToPush.TargetKind == graph.KindChangeSet {
			targetRef := &ref.Ref{
				Name:       fmt.Sprintf("review.%s.target", reviewShortID),
				TargetID:   reviewToPush.TargetID,
				TargetKind: ref.KindChangeSet,
			}
			refsToSync = append(refsToSync, targetRef)

			// Also create a top-level cs ref so it shows up in changesets list
			csShortID := hex.EncodeToString(reviewToPush.TargetID)[:12]
			csRef := &ref.Ref{
				Name:       fmt.Sprintf("cs.%s", csShortID),
				TargetID:   reviewToPush.TargetID,
				TargetKind: ref.KindChangeSet,
			}
			refsToSync = append(refsToSync, csRef)

			// Also push the base and head snapshots for the changeset
			// These are stored in the changeset payload as hex strings
			csNode, err := db.GetNode(reviewToPush.TargetID)
			if err == nil && csNode != nil {
				if baseHex, ok := csNode.Payload["base"].(string); ok {
					if baseID, err := hex.DecodeString(baseHex); err == nil {
						snapShortID := baseHex[:12]
						snapRef := &ref.Ref{
							Name:       fmt.Sprintf("snap.%s", snapShortID),
							TargetID:   baseID,
							TargetKind: ref.KindSnapshot,
						}
						refsToSync = append(refsToSync, snapRef)
					}
				}
				if headHex, ok := csNode.Payload["head"].(string); ok {
					if headID, err := hex.DecodeString(headHex); err == nil {
						snapShortID := headHex[:12]
						snapRef := &ref.Ref{
							Name:       fmt.Sprintf("snap.%s", snapShortID),
							TargetID:   headID,
							TargetKind: ref.KindSnapshot,
						}
						refsToSync = append(refsToSync, snapRef)
					}
				}
			}
		}

		fmt.Printf("  Review ID: %s\n", reviewShortID)
		fmt.Printf("  State: %s\n", reviewToPush.State)
		fmt.Printf("  Target: %s (%s)\n", hex.EncodeToString(reviewToPush.TargetID)[:12], reviewToPush.TargetKind)
	}

	if len(refsToSync) == 0 {
		fmt.Println("Nothing to push.")
		return nil
	}

	// Dry run - just show what would be pushed
	if pushDryRun {
		fmt.Printf("\nDry run - would push to %s:\n", remoteName)
		fmt.Printf("  Refs: %d\n", len(refsToSync))
		for _, r := range refsToSync {
			fmt.Printf("    %s -> %s\n", r.Name, hex.EncodeToString(r.TargetID)[:12])
		}
		return nil
	}

	// Collect all objects to push (including related objects via edges)
	var allDigests [][]byte
	digestSet := make(map[string]bool)

	// Helper to add a digest if not already seen
	addDigest := func(d []byte) {
		key := hex.EncodeToString(d)
		if !digestSet[key] {
			digestSet[key] = true
			allDigests = append(allDigests, d)
		}
	}

	// Collect all objects reachable from each ref target
	for _, r := range refsToSync {
		addDigest(r.TargetID)

		// Get all edges from this node to find related objects
		for _, edgeType := range []graph.EdgeType{
			graph.EdgeHasFile,
			graph.EdgeDefinesIn,
			graph.EdgeModifies,
			graph.EdgeHas,
			graph.EdgeAffects,
			graph.EdgeContains,
			graph.EdgeBasedOn,
			graph.EdgeHeadAt,
			graph.EdgeHasChangeSet,
			graph.EdgeHasIntent,
		} {
			edges, err := db.GetEdges(r.TargetID, edgeType)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				addDigest(edge.Dst)
			}
		}

		// Also get edges by context (for edges created with 'at' = this node)
		for _, edgeType := range []graph.EdgeType{
			graph.EdgeHasFile,
			graph.EdgeDefinesIn,
		} {
			edges, err := db.GetEdgesByContext(r.TargetID, edgeType)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				addDigest(edge.Src)
				addDigest(edge.Dst)
			}
		}
	}

	fmt.Printf("Pushing to %s (%s)...\n", remoteName, client.BaseURL)

	// Skip negotiate for small pushes (< 100 objects) - just send everything
	// Server will dedupe on ingest. This saves a round-trip.
	const negotiateThreshold = 100
	var missing [][]byte

	if len(allDigests) >= negotiateThreshold {
		// Negotiate for larger pushes
		var err error
		missing, err = client.Negotiate(allDigests)
		if err != nil {
			return fmt.Errorf("negotiating: %w", err)
		}
	} else {
		// For small pushes, send all objects (server dedupes)
		missing = allDigests
	}

	if len(missing) > 0 {
		fmt.Printf("  Objects to push: %d\n", len(missing))

		// Build pack from missing objects
		var packObjects []remote.PackObject
		contentDigestSet := make(map[string]bool)

		for _, digest := range missing {
			digestHex := hex.EncodeToString(digest)

			// Check if we have a precomputed pack object (for UUID-based nodes like Workspace)
			if precomputed, ok := precomputedPackObjects[digestHex]; ok {
				packObjects = append(packObjects, precomputed)
				continue
			}

			// Get the raw payload JSON to avoid serialization differences
			// (JSON round-tripping can change types like int64 to float64)
			nodeKind, rawPayloadJSON, err := db.GetNodeRawPayload(digest)
			if err != nil {
				continue
			}
			if rawPayloadJSON == nil {
				continue
			}

			// Content = kind + "\n" + rawPayloadJSON
			// For content-addressed nodes, digest = blake3(content) = nodeID
			content := append([]byte(string(nodeKind)+"\n"), rawPayloadJSON...)
			packDigest := digest

			// UUID-based nodes (Workspace, Review) should have been precomputed
			// but handle them here as a fallback
			if nodeKind == graph.KindWorkspace || nodeKind == graph.KindReview {
				// Need to add _uuid to payload and recompute content
				node, err := db.GetNode(digest)
				if err != nil || node == nil {
					continue
				}
				payload := make(map[string]interface{})
				for k, v := range node.Payload {
					payload[k] = v
				}
				payload["_uuid"] = hex.EncodeToString(node.ID)
				payloadJSON, err := util.CanonicalJSON(payload)
				if err != nil {
					continue
				}
				content = append([]byte(string(nodeKind)+"\n"), payloadJSON...)
				packDigest = util.Blake3Hash(content)
			}

			packObjects = append(packObjects, remote.PackObject{
				Digest:  packDigest,
				Kind:    string(nodeKind),
				Content: content,
			})

			// For File nodes, also collect the content blob digest
			if nodeKind == graph.KindFile {
				// Parse the raw payload to get the digest field
				var filePayload map[string]interface{}
				if err := json.Unmarshal(rawPayloadJSON, &filePayload); err == nil {
					if contentDigest, ok := filePayload["digest"].(string); ok {
						contentDigestSet[contentDigest] = true
					}
				}
			}
		}

		// Push content blobs for File nodes
		// Content blobs are stored with digest = blake3(rawContent), no kind prefix
		for contentDigestHex := range contentDigestSet {
			contentBytes, err := db.ReadObject(contentDigestHex)
			if err != nil {
				continue
			}
			contentDigest, err := hex.DecodeString(contentDigestHex)
			if err != nil {
				continue
			}
			packObjects = append(packObjects, remote.PackObject{
				Digest:  contentDigest,
				Kind:    "Blob",
				Content: contentBytes, // Raw content, no prefix
			})
		}

		fmt.Printf("  Including %d content blobs\n", len(contentDigestSet))

		if len(packObjects) > 0 {
			// Batch packs to stay under server size limit (target 50MB per batch)
			const maxBatchSize = 50 * 1024 * 1024 // 50MB
			var batch []remote.PackObject
			var batchSize int64
			batchNum := 1

			// Count total batches accurately by simulating the batching
			totalBatches := 0
			var simBatchSize int64
			for _, obj := range packObjects {
				objSize := int64(len(obj.Content))
				if simBatchSize+objSize > maxBatchSize && simBatchSize > 0 {
					totalBatches++
					simBatchSize = 0
				}
				simBatchSize += objSize
			}
			if simBatchSize > 0 {
				totalBatches++
			}

			for _, obj := range packObjects {
				objSize := int64(len(obj.Content))

				// If adding this object would exceed limit, push current batch
				if batchSize+objSize > maxBatchSize && len(batch) > 0 {
					fmt.Printf("\r  Pushing batch %d/%d (%d objects)...", batchNum, totalBatches, len(batch))
					result, err := client.PushPack(batch)
					if err != nil {
						return fmt.Errorf("pushing pack batch %d: %w", batchNum, err)
					}
					fmt.Printf(" segment %d\n", result.SegmentID)
					batch = nil
					batchSize = 0
					batchNum++
				}

				batch = append(batch, obj)
				batchSize += objSize
			}

			// Push remaining batch
			if len(batch) > 0 {
				if totalBatches > 1 {
					fmt.Printf("\r  Pushing batch %d/%d (%d objects)...", batchNum, totalBatches, len(batch))
				} else {
					fmt.Printf("  Pushing %d objects...", len(batch))
				}
				result, err := client.PushPack(batch)
				if err != nil {
					return fmt.Errorf("pushing pack: %w", err)
				}
				fmt.Printf(" segment %d\n", result.SegmentID)
			}
		}
	} else {
		fmt.Println("  All objects already on server.")
	}

	// Batch update refs (single round-trip instead of N)
	// Falls back to individual updates if server doesn't support batch endpoint
	var batchUpdates []remote.BatchRefUpdate
	for _, r := range refsToSync {
		// Get old value from remote
		remoteRef, _ := client.GetRef(r.Name)
		var oldTarget []byte
		if remoteRef != nil {
			oldTarget = remoteRef.Target
		}
		batchUpdates = append(batchUpdates, remote.BatchRefUpdate{
			Name:  r.Name,
			Old:   oldTarget,
			New:   r.TargetID,
			Force: pushForce,
		})
	}

	if len(batchUpdates) > 0 {
		result, err := client.BatchUpdateRefs(batchUpdates)
		if err != nil {
			// Fallback to individual updates if batch not supported (405 or other error)
			if strings.Contains(err.Error(), "405") || strings.Contains(err.Error(), "Method Not Allowed") {
				for _, upd := range batchUpdates {
					res, err := client.UpdateRef(upd.Name, upd.Old, upd.New, upd.Force)
					if err != nil {
						fmt.Printf("  Failed to update ref %s: %v\n", upd.Name, err)
						continue
					}
					if res.OK {
						fmt.Printf("  %s -> %s (push %s)\n", upd.Name, hex.EncodeToString(upd.New)[:12], res.PushID[:8])
					} else {
						fmt.Printf("  %s: %s\n", upd.Name, res.Error)
					}
				}
			} else {
				return fmt.Errorf("updating refs: %w", err)
			}
		} else {
			for _, res := range result.Results {
				if res.OK {
					fmt.Printf("  %s -> updated (push %s)\n", res.Name, result.PushID[:8])
				} else {
					fmt.Printf("  %s: %s\n", res.Name, res.Error)
				}
			}
		}
	}

	// Push edges for pushed snapshots
	// Collect edges for all snapshot refs we just pushed
	var edgesToPush []remote.EdgeData
	for _, r := range refsToSync {
		// Only push edges for snapshots (where import/test analysis is scoped)
		if r.TargetKind != ref.KindSnapshot {
			continue
		}

		// Get edges scoped to this snapshot (IMPORTS, TESTS, CALLS, etc.)
		for _, edgeType := range []graph.EdgeType{
			graph.EdgeImports,
			graph.EdgeTests,
			graph.EdgeCalls,
		} {
			edges, err := db.GetEdgesByContext(r.TargetID, edgeType)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				edgesToPush = append(edgesToPush, remote.EdgeData{
					Src:  hex.EncodeToString(edge.Src),
					Type: string(edge.Type),
					Dst:  hex.EncodeToString(edge.Dst),
					At:   hex.EncodeToString(edge.At),
				})
			}
		}
	}

	if len(edgesToPush) > 0 {
		fmt.Printf("  Pushing %d edges...", len(edgesToPush))
		result, err := client.PushEdges(edgesToPush)
		if err != nil {
			// Don't fail the push if edge push fails - edges are supplementary
			fmt.Printf(" warning: %v\n", err)
		} else {
			fmt.Printf(" %d inserted\n", result.Inserted)
		}
	}

	fmt.Println("Push complete.")
	return nil
}

func runFetch(cmd *cobra.Command, args []string) error {
	// Show explain if requested
	if fetchExplain {
		remoteName := "origin"
		if len(args) > 0 {
			remoteName = args[0]
		}
		ctx := explain.ExplainFetch(remoteName, 0) // 0 refs - count determined later
		ctx.Print(os.Stdout)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Determine remote name
	remoteName := "origin"
	refsToFetch := []string{}

	if len(args) > 0 {
		// Check if first arg is a remote name
		if _, err := remote.GetRemote(args[0]); err == nil {
			remoteName = args[0]
			refsToFetch = args[1:]
		} else {
			// First arg is a ref
			refsToFetch = args
		}
	}

	// Create client for remote
	client, err := remote.NewClientForRemote(remoteName)
	if err != nil {
		return fmt.Errorf("remote '%s' not configured (use 'kai remote set %s <url>')", remoteName, remoteName)
	}

	// Check server health
	if err := client.Health(); err != nil {
		return fmt.Errorf("cannot connect to %s: %w", client.BaseURL, err)
	}

	fmt.Printf("Fetching from %s (%s)...\n", remoteName, client.BaseURL)

	// Handle workspace fetch if --ws flag is set
	if fetchWorkspace != "" {
		return fetchWorkspaceFromRemote(db, client, remoteName, fetchWorkspace)
	}

	// Handle review fetch if --review flag is set
	if fetchReview != "" {
		return fetchReviewFromRemote(db, client, remoteName, fetchReview)
	}

	// Get refs from remote
	var remoteRefs []*remote.RefEntry
	if len(refsToFetch) > 0 {
		for _, name := range refsToFetch {
			r, err := client.GetRef(name)
			if err != nil {
				fmt.Printf("  Warning: failed to get ref %s: %v\n", name, err)
				continue
			}
			if r != nil {
				remoteRefs = append(remoteRefs, r)
			}
		}
	} else {
		// Fetch all refs
		remoteRefs, err = client.ListRefs("")
		if err != nil {
			return fmt.Errorf("listing refs: %w", err)
		}
	}

	if len(remoteRefs) == 0 {
		fmt.Println("No refs to fetch.")
		return nil
	}

	fmt.Printf("  Found %d ref(s)\n", len(remoteRefs))

	// Collect objects to fetch
	var objectsToFetch [][]byte
	for _, r := range remoteRefs {
		exists, _ := db.HasNode(r.Target)
		if !exists {
			objectsToFetch = append(objectsToFetch, r.Target)
		}
	}

	if len(objectsToFetch) > 0 {
		fmt.Printf("  Objects to fetch: %d\n", len(objectsToFetch))

		for _, digest := range objectsToFetch {
			content, kind, err := client.GetObject(digest)
			if err != nil {
				fmt.Printf("  Warning: failed to get object %s: %v\n", hex.EncodeToString(digest)[:12], err)
				continue
			}

			if content != nil {
				// Parse and store the object
				var payload map[string]interface{}
				if err := json.Unmarshal(content, &payload); err != nil {
					fmt.Printf("  Warning: failed to parse object %s: %v\n", hex.EncodeToString(digest)[:12], err)
					continue
				}

				// Insert node directly
				tx, err := db.BeginTx()
				if err != nil {
					continue
				}
				_, err = db.InsertNode(tx, graph.NodeKind(kind), payload)
				if err != nil {
					tx.Rollback()
					continue
				}
				tx.Commit()
			}
		}
	}

	// Update local refs (prefixed with remote name)
	refMgr := ref.NewRefManager(db)
	for _, r := range remoteRefs {
		// Store as remote/origin/snap.main style
		localName := fmt.Sprintf("remote/%s/%s", remoteName, r.Name)
		kind := ref.KindSnapshot // Default
		if strings.HasPrefix(r.Name, "cs.") {
			kind = ref.KindChangeSet
		} else if strings.HasPrefix(r.Name, "ws.") {
			kind = ref.KindWorkspace
		}

		if err := refMgr.Set(localName, r.Target, kind); err != nil {
			fmt.Printf("  Warning: failed to set ref %s: %v\n", localName, err)
			continue
		}
		fmt.Printf("  %s -> %s\n", localName, hex.EncodeToString(r.Target)[:12])
	}

	fmt.Println("Fetch complete.")
	return nil
}

func runClone(cmd *cobra.Command, args []string) error {
	rawURL := args[0]

	// Parse the URL to extract tenant and repo if not provided via flags
	tenant := cloneTenant
	repo := cloneRepo

	// Check if input is shorthand format: org/repo (no scheme)
	if !strings.Contains(rawURL, "://") && strings.Count(rawURL, "/") == 1 {
		// Shorthand format: org/repo - use default server
		parts := strings.Split(rawURL, "/")
		tenant = parts[0]
		repo = parts[1]
		// Use default server (env var or constant)
		serverURL := os.Getenv("KAI_SERVER")
		if serverURL == "" {
			serverURL = remote.DefaultServer
		}
		rawURL = serverURL
	} else {
		// Parse URL: http://server/tenant/repo
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("invalid URL: %w", err)
		}

		// Extract tenant/repo from path if not specified via flags
		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) >= 2 && tenant == "" && repo == "" {
			tenant = pathParts[0]
			repo = pathParts[1]
			// Rebuild base URL without tenant/repo path
			parsedURL.Path = ""
			rawURL = parsedURL.String()
		}
	}

	// Validate we have tenant and repo
	if tenant == "" {
		return fmt.Errorf("tenant not specified (use --tenant or include in URL path)")
	}
	if repo == "" {
		return fmt.Errorf("repo not specified (use --repo or include in URL path)")
	}

	// Determine directory name
	dirName := repo
	if len(args) > 1 {
		dirName = args[1]
	}

	// Check if directory already exists
	if _, err := os.Stat(dirName); err == nil {
		return fmt.Errorf("directory '%s' already exists", dirName)
	}

	fmt.Printf("Cloning into '%s'...\n", dirName)

	// Create the directory
	if err := os.MkdirAll(dirName, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	// Change to the new directory
	origDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	if err := os.Chdir(dirName); err != nil {
		return fmt.Errorf("changing to directory: %w", err)
	}
	defer os.Chdir(origDir)

	// Initialize Kai (skip creating kai.modules.yaml - the remote repo may have one)
	fmt.Println("Initializing Kai...")
	skipModulesFile = true
	if err := runInit(cmd, nil); err != nil {
		skipModulesFile = false
		// Clean up on failure
		os.Chdir(origDir)
		os.RemoveAll(dirName)
		return fmt.Errorf("initializing: %w", err)
	}
	skipModulesFile = false

	// Set up the remote
	fmt.Printf("Setting remote 'origin' to %s (tenant=%s, repo=%s)...\n", rawURL, tenant, repo)
	entry := &remote.RemoteEntry{
		URL:    rawURL,
		Tenant: tenant,
		Repo:   repo,
	}
	if err := remote.SetRemote("origin", entry); err != nil {
		os.Chdir(origDir)
		os.RemoveAll(dirName)
		return fmt.Errorf("setting remote: %w", err)
	}

	// Open database for fetch
	db, err := openDB()
	if err != nil {
		os.Chdir(origDir)
		os.RemoveAll(dirName)
		return err
	}
	defer db.Close()

	// Create client for remote
	client, err := remote.NewClientForRemote("origin")
	if err != nil {
		os.Chdir(origDir)
		os.RemoveAll(dirName)
		return fmt.Errorf("creating client: %w", err)
	}

	// Check server health
	if err := client.Health(); err != nil {
		os.Chdir(origDir)
		os.RemoveAll(dirName)
		return fmt.Errorf("cannot connect to %s: %w", client.BaseURL, err)
	}

	fmt.Println("Fetching refs...")

	// Fetch all refs from remote
	remoteRefs, err := client.ListRefs("")
	if err != nil {
		fmt.Printf("Warning: could not list refs: %v\n", err)
		fmt.Println("Clone complete (empty repository).")
		return nil
	}

	if len(remoteRefs) == 0 {
		fmt.Println("Clone complete (empty repository).")
		return nil
	}

	fmt.Printf("  Found %d ref(s)\n", len(remoteRefs))

	// Collect objects to fetch
	var objectsToFetch [][]byte
	for _, r := range remoteRefs {
		exists, _ := db.HasNode(r.Target)
		if !exists {
			objectsToFetch = append(objectsToFetch, r.Target)
		}
	}

	if len(objectsToFetch) > 0 {
		fmt.Printf("  Fetching %d object(s)...\n", len(objectsToFetch))

		for _, digest := range objectsToFetch {
			content, kind, err := client.GetObject(digest)
			if err != nil {
				fmt.Printf("  Warning: failed to get object %s: %v\n", hex.EncodeToString(digest)[:12], err)
				continue
			}

			if content == nil {
				fmt.Printf("  Warning: object %s not found on server\n", hex.EncodeToString(digest)[:12])
				continue
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(content, &payload); err != nil {
				fmt.Printf("  Warning: failed to parse object %s: %v\n", hex.EncodeToString(digest)[:12], err)
				continue
			}

			tx, err := db.BeginTx()
			if err != nil {
				fmt.Printf("  Warning: failed to start transaction: %v\n", err)
				continue
			}
			_, err = db.InsertNode(tx, graph.NodeKind(kind), payload)
			if err != nil {
				tx.Rollback()
				fmt.Printf("  Warning: failed to insert object %s: %v\n", hex.EncodeToString(digest)[:12], err)
				continue
			}
			tx.Commit()
		}
	}

	// Update local refs
	refMgr := ref.NewRefManager(db)
	for _, r := range remoteRefs {
		localName := fmt.Sprintf("remote/origin/%s", r.Name)
		kind := ref.KindSnapshot
		if strings.HasPrefix(r.Name, "cs.") {
			kind = ref.KindChangeSet
		} else if strings.HasPrefix(r.Name, "ws.") {
			kind = ref.KindWorkspace
		}

		if err := refMgr.Set(localName, r.Target, kind); err != nil {
			continue
		}
		fmt.Printf("  %s -> %s\n", localName, hex.EncodeToString(r.Target)[:12])
	}

	fmt.Printf("\nClone complete. Repository cloned into '%s'\n", dirName)
	return nil
}

func runPrune(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Dry-run is the default unless --yes is specified
	// --dry-run can also be explicitly set
	isDryRun := !pruneYes || pruneDryRun

	opts := graph.GCOptions{
		SinceDays:  pruneSinceDays,
		Aggressive: pruneAggressive,
		DryRun:     isDryRun,
		Keep:       pruneKeep,
	}

	plan, err := db.BuildGCPlan(opts)
	if err != nil {
		return fmt.Errorf("building GC plan: %w", err)
	}

	// Show summary
	totalNodes := len(plan.NodesToDelete)
	if totalNodes == 0 && len(plan.ObjectsToDelete) == 0 {
		fmt.Println("Nothing to prune. All content is reachable.")
		return nil
	}

	if isDryRun {
		fmt.Println("Would sweep:")
	} else {
		fmt.Println("Sweeping:")
	}

	if plan.SnapshotCount > 0 {
		fmt.Printf("  Snapshots:  %d\n", plan.SnapshotCount)
	}
	if plan.ChangeSetCount > 0 {
		fmt.Printf("  ChangeSets: %d\n", plan.ChangeSetCount)
	}
	if plan.FileCount > 0 {
		fmt.Printf("  Files:      %d\n", plan.FileCount)
	}
	if plan.SymbolCount > 0 {
		fmt.Printf("  Symbols:    %d\n", plan.SymbolCount)
	}
	if plan.ModuleCount > 0 {
		fmt.Printf("  Modules:    %d\n", plan.ModuleCount)
	}

	if len(plan.ObjectsToDelete) > 0 {
		fmt.Printf("  Objects:    %d (~%.2f MiB)\n", len(plan.ObjectsToDelete), float64(plan.BytesReclaimed)/(1024*1024))
	}

	if isDryRun {
		fmt.Println("\nRun `kai prune --yes` to proceed.")
		return nil
	}

	// Actually execute
	if err := db.ExecuteGC(plan); err != nil {
		return fmt.Errorf("executing GC: %w", err)
	}

	fmt.Printf("\nPrune complete. Reclaimed %.2f MiB.\n", float64(plan.BytesReclaimed)/(1024*1024))
	return nil
}

func runRemoteLog(cmd *cobra.Command, args []string) error {
	// Determine remote name
	remoteName := "origin"
	if len(args) > 0 {
		remoteName = args[0]
	}

	// Create client for remote
	client, err := remote.NewClientForRemote(remoteName)
	if err != nil {
		return fmt.Errorf("remote '%s' not configured (use 'kai remote set %s <url>')", remoteName, remoteName)
	}

	// Check server health
	if err := client.Health(); err != nil {
		return fmt.Errorf("cannot connect to %s: %w", client.BaseURL, err)
	}

	// Get log entries
	entries, err := client.GetLogEntries(remoteLogRef, 0, remoteLogLimit)
	if err != nil {
		return fmt.Errorf("getting log: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No log entries found.")
		return nil
	}

	// Get current head
	head, _ := client.GetLogHead()

	fmt.Printf("Remote log from %s (%s)\n", remoteName, client.BaseURL)
	if head != nil {
		fmt.Printf("Head: %s\n", hex.EncodeToString(head)[:16])
	}
	fmt.Println()

	for _, e := range entries {
		timestamp := time.UnixMilli(e.Time).Format("2006-01-02 15:04:05")

		switch e.Kind {
		case "REF_UPDATE":
			oldStr := "(new)"
			if len(e.Old) > 0 {
				oldStr = hex.EncodeToString(e.Old)[:12]
			}
			newStr := hex.EncodeToString(e.New)[:12]
			fmt.Printf("%s  %-10s  %-20s  %s -> %s\n",
				timestamp, e.Actor, e.Ref, oldStr, newStr)
		case "NODE_PUBLISH":
			fmt.Printf("%s  %-10s  published %s (%s)\n",
				timestamp, e.Actor, hex.EncodeToString(e.NodeID)[:12], e.NodeKind)
		default:
			fmt.Printf("%s  %-10s  %s\n", timestamp, e.Actor, e.Kind)
		}
	}

	return nil
}

// Auth command implementations

func runAuthLogin(cmd *cobra.Command, args []string) error {
	var serverURL string

	if len(args) > 0 {
		serverURL = args[0]
	} else {
		// Try to get URL from origin remote
		entry, err := remote.GetRemote("origin")
		if err != nil {
			return fmt.Errorf("no server URL provided and no 'origin' remote configured\n\nUsage: kai auth login <server-url>\nExample: kai auth login http://localhost:8080")
		}
		serverURL = entry.URL
	}

	return remote.Login(serverURL)
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	if err := remote.Logout(); err != nil {
		return fmt.Errorf("logout failed: %w", err)
	}
	fmt.Println("Logged out successfully.")
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	email, serverURL, loggedIn := remote.GetAuthStatus()

	if !loggedIn {
		fmt.Println("Not logged in.")
		fmt.Println("\nUse 'kai auth login' to authenticate.")
		return nil
	}

	fmt.Printf("Logged in as: %s\n", email)
	fmt.Printf("Server:       %s\n", serverURL)

	// Try to validate the token
	token, err := remote.GetValidAccessToken()
	if err != nil {
		fmt.Println("Status:       Token invalid or expired")
		fmt.Println("\nUse 'kai auth login' to re-authenticate.")
		return nil
	}

	if token != "" {
		fmt.Println("Status:       Authenticated")
	}

	return nil
}

// Review command implementations

func runReviewOpen(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var targetID []byte
	var targetKind string

	if len(args) == 0 {
		// Check if there's a current workspace
		currentWsName, err := getCurrentWorkspace()
		if err != nil {
			return fmt.Errorf("checking current workspace: %w", err)
		}

		if currentWsName != "" {
			// Use the current workspace
			wsMgr := workspace.NewManager(db)
			ws, err := wsMgr.Get(currentWsName)
			if err != nil {
				return fmt.Errorf("getting workspace %q: %w", currentWsName, err)
			}
			if ws == nil {
				return fmt.Errorf("workspace %q not found (clear with 'kai ws switch')", currentWsName)
			}

			// Create changeset from workspace's head to working snapshot
			headSnapID, err := resolveSnapshotID(db, "@snap:working")
			if err != nil {
				return fmt.Errorf("resolving @snap:working: %w", err)
			}

			// Check if there are changes
			if string(ws.HeadSnapshot) == string(headSnapID) {
				return fmt.Errorf("no changes to review in workspace %q\n  Make changes and run 'kai capture' to capture them", currentWsName)
			}

			fmt.Printf("Using workspace: %s\n", currentWsName)
			fmt.Printf("Creating changeset from workspace head to @snap:working...\n")

			changeSetID, err := createChangesetFromSnapshots(db, ws.HeadSnapshot, headSnapID, reviewTitle)
			if err != nil {
				return fmt.Errorf("creating changeset: %w", err)
			}
			fmt.Printf("Changeset created: %s\n", util.BytesToHex(changeSetID)[:12])

			// Add changeset to workspace
			if err := wsMgr.AddChangeSet(ws.ID, changeSetID); err != nil {
				return fmt.Errorf("adding changeset to workspace: %w", err)
			}

			// Update workspace head
			if err := wsMgr.UpdateHead(ws.ID, headSnapID); err != nil {
				return fmt.Errorf("updating workspace head: %w", err)
			}

			// Review targets the workspace (which contains the changeset stack)
			targetID = ws.ID
			targetKind = string(ref.KindWorkspace)
			fmt.Printf("Changeset added to workspace stack\n\n")
		} else {
			// No workspace - auto-create standalone changeset from base to snap.latest
			// Determine base: --base flag > @snap:prev (previous snapshot)
			var baseSnapID []byte
			var baseLabel string

			if reviewBase != "" {
				// User specified --base
				baseSnapID, err = resolveSnapshotID(db, reviewBase)
				if err != nil {
					return fmt.Errorf("resolving --base %q: %w", reviewBase, err)
				}
				baseLabel = reviewBase
			} else {
				// Use previous snapshot as base (like git diff HEAD~1)
				baseSnapID, err = resolveSnapshotID(db, "@snap:prev")
				if err != nil {
					return fmt.Errorf("resolving @snap:prev: %w (need at least 2 snapshots, run 'kai capture' twice)", err)
				}
				baseLabel = "@snap:prev"
			}

			// Use snap.latest as head (updated by kai capture)
			headSnapID, err := resolveSnapshotID(db, "snap.latest")
			if err != nil {
				return fmt.Errorf("resolving snap.latest: %w (run 'kai capture' to capture your changes)", err)
			}

			// Check if baseline and head are the same (no changes)
			if string(baseSnapID) == string(headSnapID) {
				return fmt.Errorf("no changes to review: %s and snap.latest are the same\n  Make changes and run 'kai capture' to capture them", baseLabel)
			}

			fmt.Printf("Creating changeset from %s to snap.latest...\n", baseLabel)
			changeSetID, err := createChangesetFromSnapshots(db, baseSnapID, headSnapID, reviewTitle)
			if err != nil {
				return fmt.Errorf("creating changeset: %w", err)
			}

			targetID = changeSetID
			targetKind = string(ref.KindChangeSet)
			fmt.Printf("Changeset created: %s\n", util.BytesToHex(changeSetID)[:12])
			fmt.Println()
		}
	} else {
		// Resolve the target (changeset or workspace)
		resolver := ref.NewResolver(db)
		result, err := resolver.Resolve(args[0], nil)
		if err != nil {
			return fmt.Errorf("resolving target: %w", err)
		}

		// Validate target kind
		if result.Kind != ref.KindChangeSet && result.Kind != ref.KindWorkspace {
			return fmt.Errorf("target must be a changeset or workspace, got %s", result.Kind)
		}

		targetID = result.ID
		targetKind = string(result.Kind)
	}

	// Get author (use system user for now)
	author := os.Getenv("USER")
	if author == "" {
		author = "unknown"
	}

	// Auto-generate title from intent if not provided
	autoTitle := reviewTitle == ""
	if autoTitle {
		gen := intent.NewGenerator(db)
		generatedTitle, err := gen.RenderIntent(targetID, "", false)
		if err != nil {
			// Fall back to generic title if intent generation fails
			generatedTitle = "Review of changes"
		}
		reviewTitle = generatedTitle
		fmt.Printf("Auto-generated title: %s\n", reviewTitle)
	}

	// Show explain if requested
	if reviewExplain {
		targetRef := "@cs:last"
		if len(args) > 0 {
			targetRef = args[0]
		}
		// Check if we have a workspace
		currentWsName, _ := getCurrentWorkspace()
		hasWorkspace := currentWsName != "" && len(args) == 0
		ctx := explain.ExplainReviewOpenFull(targetRef, reviewTitle, hasWorkspace, currentWsName, autoTitle)
		ctx.Print(os.Stdout)
	}

	mgr := review.NewManager(db)
	rev, err := mgr.Open(targetID, reviewTitle, reviewDesc, author, reviewReviewers)
	if err != nil {
		return fmt.Errorf("opening review: %w", err)
	}

	reviewID := review.IDToHex(rev.ID)[:12]
	fmt.Printf("Review opened: %s\n", reviewID)
	fmt.Printf("Title:         %s\n", rev.Title)
	fmt.Printf("State:         %s\n", rev.State)
	fmt.Printf("Target:        %s (%s)\n", util.BytesToHex(rev.TargetID)[:12], targetKind)
	if len(rev.Reviewers) > 0 {
		fmt.Printf("Reviewers:     %s\n", strings.Join(rev.Reviewers, ", "))
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  kai review view %s       # View the review\n", reviewID)
	fmt.Printf("  kai review approve %s    # Approve the review\n", reviewID)
	fmt.Printf("  kai review close %s      # Close (--state merged|abandoned)\n", reviewID)
	fmt.Println()
	fmt.Println("Other commands:")
	fmt.Println("  kai ci plan              # See which tests to run")
	fmt.Println("  kai review export <id>   # Export as markdown/HTML")

	return nil
}

func runReviewList(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	reviews, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing reviews: %w", err)
	}

	if len(reviews) == 0 {
		fmt.Println("No reviews found.")
		return nil
	}

	fmt.Printf("%-12s  %-10s  %-30s  %s\n", "ID", "STATE", "TITLE", "TARGET")
	fmt.Println(strings.Repeat("-", 80))

	for _, r := range reviews {
		title := r.Title
		if len(title) > 30 {
			title = title[:27] + "..."
		}
		fmt.Printf("%-12s  %-10s  %-30s  %s\n",
			review.IDToHex(r.ID)[:12],
			r.State,
			title,
			util.BytesToHex(r.TargetID)[:12])
	}

	return nil
}

func runReviewView(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	// Get changeset data if target is a changeset
	var csNode *graph.Node
	var baseSnapID, headSnapID []byte
	var fileChanges []fileChangeInfo
	var symbolChanges []symbolChangeInfo
	var changeTypes []string

	if rev.TargetKind == graph.KindChangeSet {
		csNode, _ = db.GetNode(rev.TargetID)
		if csNode != nil {
			// Extract base and head snapshot IDs
			if baseHex, ok := csNode.Payload["base"].(string); ok {
				baseSnapID, _ = util.HexToBytes(baseHex)
			}
			if headHex, ok := csNode.Payload["head"].(string); ok {
				headSnapID, _ = util.HexToBytes(headHex)
			}

			// Collect file and symbol changes
			csData, err := db.GetAllNodesAndEdgesForChangeSet(rev.TargetID)
			if err == nil {
				if nodes, ok := csData["nodes"].([]map[string]interface{}); ok {
					for _, node := range nodes {
						kind, _ := node["kind"].(string)
						payload, _ := node["payload"].(map[string]interface{})
						nodeID, _ := node["id"].(string)

						switch kind {
						case "File":
							// Deduplicate by node ID
							isDuplicateFile := false
							for _, existing := range fileChanges {
								if existing.ID == nodeID {
									isDuplicateFile = true
									break
								}
							}
							if !isDuplicateFile {
								path, _ := payload["path"].(string)
								digest, _ := payload["digest"].(string)
								fileChanges = append(fileChanges, fileChangeInfo{
									ID:     nodeID,
									Path:   path,
									Digest: digest,
								})
							}
						case "Symbol":
							// Deduplicate by node ID
							isDuplicate := false
							for _, existing := range symbolChanges {
								if existing.ID == nodeID {
									isDuplicate = true
									break
								}
							}
							if !isDuplicate {
								fqName, _ := payload["fqName"].(string)
								symKind, _ := payload["kind"].(string)
								sig, _ := payload["signature"].(string)
								symbolChanges = append(symbolChanges, symbolChangeInfo{
									ID:        nodeID,
									FQName:    fqName,
									Kind:      symKind,
									Signature: sig,
								})
							}
						case "ChangeType":
							if category, ok := payload["category"].(string); ok {
								changeTypes = append(changeTypes, category)
							}
						}
					}
				}
			}
		}
	}

	// JSON output
	if reviewJSON {
		data := map[string]interface{}{
			"id":          review.IDToHex(rev.ID),
			"title":       rev.Title,
			"description": rev.Description,
			"state":       rev.State,
			"author":      rev.Author,
			"reviewers":   rev.Reviewers,
			"targetId":    util.BytesToHex(rev.TargetID),
			"targetKind":  rev.TargetKind,
			"createdAt":   rev.CreatedAt,
			"updatedAt":   rev.UpdatedAt,
		}

		if csNode != nil {
			// Build semantic hunks for JSON
			var units []map[string]interface{}
			for _, sym := range symbolChanges {
				units = append(units, map[string]interface{}{
					"kind":   sym.Kind,
					"fqName": sym.FQName,
					"after":  map[string]interface{}{"signature": sym.Signature},
				})
			}

			var files []map[string]interface{}
			for _, f := range fileChanges {
				// Get file content if available
				var afterContent string
				if f.Digest != "" {
					if content, err := db.ReadObject(f.Digest); err == nil {
						afterContent = string(content)
					}
				}
				files = append(files, map[string]interface{}{
					"path":  f.Path,
					"after": afterContent,
				})
			}

			data["units"] = units
			data["files"] = files
			data["changeTypes"] = changeTypes
			if csNode.Payload["intent"] != nil {
				data["intent"] = csNode.Payload["intent"]
			}
		}

		output, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(output))
		return nil
	}

	// Text output - header
	fmt.Printf("Review: %s\n", review.IDToHex(rev.ID)[:12])
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Title:       %s\n", rev.Title)
	if rev.Description != "" {
		fmt.Printf("Description: %s\n", rev.Description)
	}
	fmt.Printf("State:       %s\n", rev.State)
	fmt.Printf("Author:      %s\n", rev.Author)
	if len(rev.Reviewers) > 0 {
		fmt.Printf("Reviewers:   %s\n", strings.Join(rev.Reviewers, ", "))
	}
	fmt.Printf("Target:      %s (%s)\n", util.BytesToHex(rev.TargetID)[:12], rev.TargetKind)
	fmt.Printf("Created:     %s\n", time.UnixMilli(rev.CreatedAt).Format(time.RFC3339))
	fmt.Printf("Updated:     %s\n", time.UnixMilli(rev.UpdatedAt).Format(time.RFC3339))

	// Show intent
	if csNode != nil {
		if intentStr, ok := csNode.Payload["intent"].(string); ok && intentStr != "" {
			fmt.Println()
			fmt.Println("Intent:")
			fmt.Printf("  %s\n", intentStr)
		}
	}

	// Show change types grouped into 3 buckets
	if len(changeTypes) > 0 {
		fmt.Println()
		fmt.Println("Changes:")

		// Group raw categories into buckets
		bucketCounts := make(map[ChangeBucket]int)
		for _, ct := range changeTypes {
			bucket := categorizeToBucket(ct)
			bucketCounts[bucket]++
		}

		// Display in consistent order
		bucketOrder := []ChangeBucket{BucketStructural, BucketBehavioral, BucketAPIContract}
		for _, bucket := range bucketOrder {
			count := bucketCounts[bucket]
			if count > 0 {
				fmt.Printf("  • %s: %d\n", bucket, count)
			}
		}
	}

	// Show diffs based on view mode
	showSemantic := reviewViewMode == "semantic" || reviewViewMode == "mixed"
	showText := reviewViewMode == "text" || reviewViewMode == "mixed"

	if showSemantic && len(symbolChanges) > 0 {
		fmt.Println()
		fmt.Println("Semantic Diff:")
		fmt.Println(strings.Repeat("-", 60))

		// Group symbols by file path (approximate from fqName)
		for _, sym := range symbolChanges {
			fmt.Printf("\n  %s %s\n", sym.Kind, sym.FQName)
			if sym.Signature != "" {
				fmt.Printf("    + %s\n", sym.Signature)
			}
		}
	}

	if showText && len(fileChanges) > 0 {
		fmt.Println()
		fmt.Println("File Contents:")
		fmt.Println(strings.Repeat("-", 60))

		snapshotCreator := snapshot.NewCreator(db, nil)

		for _, f := range fileChanges {
			fmt.Printf("\n--- %s\n", f.Path)

			// Get before content from base snapshot
			var beforeContent, afterContent string

			if baseSnapID != nil {
				beforeContent = getFileContentFromSnapshot(db, snapshotCreator, baseSnapID, f.Path)
			}
			if headSnapID != nil {
				afterContent = getFileContentFromSnapshot(db, snapshotCreator, headSnapID, f.Path)
			}

			// Show unified diff
			if beforeContent == "" && afterContent != "" {
				// New file
				fmt.Println("+++ (new file)")
				lines := strings.Split(afterContent, "\n")
				for i, line := range lines {
					if i < 20 { // Limit preview
						fmt.Printf("+ %s\n", line)
					}
				}
				if len(lines) > 20 {
					fmt.Printf("  ... (%d more lines)\n", len(lines)-20)
				}
			} else if beforeContent != "" && afterContent == "" {
				// Deleted file
				fmt.Println("--- (deleted)")
			} else if beforeContent != afterContent {
				// Modified - show simple diff
				fmt.Println("+++ (modified)")
				showSimpleDiff(beforeContent, afterContent)
			} else {
				fmt.Println("  (unchanged)")
			}
		}
	}

	return nil
}

// Helper types for review view
type fileChangeInfo struct {
	ID     string
	Path   string
	Digest string
}

type symbolChangeInfo struct {
	ID        string
	FQName    string
	Kind      string
	Signature string
}

// getFileContentFromSnapshot retrieves file content from a snapshot by path
func getFileContentFromSnapshot(db *graph.DB, sc *snapshot.Creator, snapID []byte, path string) string {
	files, err := sc.GetSnapshotFiles(snapID)
	if err != nil {
		return ""
	}

	for _, f := range files {
		if fPath, ok := f.Payload["path"].(string); ok && fPath == path {
			if digest, ok := f.Payload["digest"].(string); ok {
				content, err := db.ReadObject(digest)
				if err == nil {
					return string(content)
				}
			}
		}
	}
	return ""
}

// showSimpleDiff displays a simple line-by-line diff
func showSimpleDiff(before, after string) {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Simple diff: show first few changed lines
	maxLines := 30
	shown := 0

	// Find differences
	maxLen := len(beforeLines)
	if len(afterLines) > maxLen {
		maxLen = len(afterLines)
	}

	for i := 0; i < maxLen && shown < maxLines; i++ {
		var beforeLine, afterLine string
		if i < len(beforeLines) {
			beforeLine = beforeLines[i]
		}
		if i < len(afterLines) {
			afterLine = afterLines[i]
		}

		if beforeLine != afterLine {
			if beforeLine != "" {
				fmt.Printf("- %s\n", beforeLine)
				shown++
			}
			if afterLine != "" {
				fmt.Printf("+ %s\n", afterLine)
				shown++
			}
		}
	}

	if shown >= maxLines {
		fmt.Println("  ... (diff truncated)")
	}
}

// showUnifiedDiff displays a unified diff using pure Go (no system dependency)
func showUnifiedDiff(before, after string) {
	dmp := diffmatchpatch.New()

	// Convert to line-based diff for better unified output
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Use line mode for cleaner diffs
	chars1, chars2, lineArray := dmp.DiffLinesToChars(before, after)
	diffs := dmp.DiffMain(chars1, chars2, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	diffs = dmp.DiffCleanupSemantic(diffs)

	// ANSI color codes
	const (
		colorReset  = "\033[0m"
		colorRed    = "\033[31m"
		colorGreen  = "\033[32m"
		colorCyan   = "\033[36m"
	)

	// Track line numbers for hunk headers
	oldLine := 1
	newLine := 1

	// Collect hunks
	type hunk struct {
		oldStart, oldCount int
		newStart, newCount int
		lines              []string
	}
	var hunks []hunk
	var currentHunk *hunk

	for _, d := range diffs {
		lines := strings.Split(strings.TrimSuffix(d.Text, "\n"), "\n")
		if d.Text == "" {
			continue
		}

		switch d.Type {
		case diffmatchpatch.DiffEqual:
			// Context lines - start new hunk if needed, include up to 3 lines
			contextLines := lines
			if len(contextLines) > 6 && currentHunk != nil {
				// End current hunk with 3 trailing context lines
				for i := 0; i < 3 && i < len(contextLines); i++ {
					currentHunk.lines = append(currentHunk.lines, " "+contextLines[i])
					currentHunk.oldCount++
					currentHunk.newCount++
				}
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
				// Skip middle, advance line counters
				oldLine += len(contextLines) - 3
				newLine += len(contextLines) - 3
				contextLines = contextLines[len(contextLines)-3:]
			}
			if currentHunk != nil {
				for _, line := range contextLines {
					currentHunk.lines = append(currentHunk.lines, " "+line)
					currentHunk.oldCount++
					currentHunk.newCount++
				}
			}
			oldLine += len(lines)
			newLine += len(lines)

		case diffmatchpatch.DiffDelete:
			if currentHunk == nil {
				// Start new hunk with up to 3 lines of previous context
				currentHunk = &hunk{oldStart: oldLine, newStart: newLine}
			}
			for _, line := range lines {
				currentHunk.lines = append(currentHunk.lines, colorRed+"-"+line+colorReset)
				currentHunk.oldCount++
			}
			oldLine += len(lines)

		case diffmatchpatch.DiffInsert:
			if currentHunk == nil {
				currentHunk = &hunk{oldStart: oldLine, newStart: newLine}
			}
			for _, line := range lines {
				currentHunk.lines = append(currentHunk.lines, colorGreen+"+"+line+colorReset)
				currentHunk.newCount++
			}
			newLine += len(lines)
		}
	}

	// Flush last hunk
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	// Print hunks
	for _, h := range hunks {
		// Print hunk header
		fmt.Printf("%s@@ -%d,%d +%d,%d @@%s\n", colorCyan, h.oldStart, h.oldCount, h.newStart, h.newCount, colorReset)
		for _, line := range h.lines {
			fmt.Println(line)
		}
	}

	// Handle edge case: no differences
	if len(hunks) == 0 && len(beforeLines) != len(afterLines) {
		// Fallback for edge cases
		fmt.Printf("%s@@ -1,%d +1,%d @@%s\n", colorCyan, len(beforeLines), len(afterLines), colorReset)
	}
}

func runReviewStatus(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("Review %s: %s\n", review.IDToHex(rev.ID)[:12], rev.State)
	return nil
}

func runReviewApprove(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	if err := mgr.Approve(rev.ID); err != nil {
		return fmt.Errorf("approving review: %w", err)
	}

	fmt.Printf("Review %s approved.\n", review.IDToHex(rev.ID)[:12])
	return nil
}

func runReviewRequestChanges(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	if err := mgr.RequestChanges(rev.ID); err != nil {
		return fmt.Errorf("requesting changes: %w", err)
	}

	fmt.Printf("Review %s: changes requested.\n", review.IDToHex(rev.ID)[:12])
	return nil
}

func runReviewClose(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	state := review.State(reviewCloseState)
	if state != review.StateMerged && state != review.StateAbandoned {
		return fmt.Errorf("--state must be 'merged' or 'abandoned'")
	}

	if err := mgr.Close(rev.ID, state); err != nil {
		return fmt.Errorf("closing review: %w", err)
	}

	// If merging, update snap.main to the changeset's head snapshot
	if state == review.StateMerged && rev.TargetKind == graph.KindChangeSet {
		target, err := mgr.GetTarget(rev.ID)
		if err == nil && target != nil {
			if headHex, ok := target.Payload["head"].(string); ok && headHex != "" {
				headID, err := util.HexToBytes(headHex)
				if err == nil {
					refMgr := ref.NewRefManager(db)
					if err := refMgr.Set("snap.main", headID, ref.KindSnapshot); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not update snap.main: %v\n", err)
					} else {
						fmt.Printf("Updated snap.main to merged head.\n")
					}
				}
			}
		}
	}

	fmt.Printf("Review %s closed as %s.\n", review.IDToHex(rev.ID)[:12], state)
	return nil
}

func runReviewReady(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	if err := mgr.MarkReady(rev.ID); err != nil {
		return fmt.Errorf("marking ready: %w", err)
	}

	fmt.Printf("Review %s is now open for review.\n", review.IDToHex(rev.ID)[:12])
	return nil
}

func runReviewExport(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := review.NewManager(db)
	rev, err := mgr.GetByShortID(args[0])
	if err != nil {
		return err
	}

	// Get target for more context
	target, _ := mgr.GetTarget(rev.ID)

	if reviewExportMD || (!reviewExportMD && !reviewExportHTML) {
		// Default to markdown
		fmt.Printf("# %s\n\n", rev.Title)
		if rev.Description != "" {
			fmt.Printf("%s\n\n", rev.Description)
		}
		fmt.Printf("**State:** %s  \n", rev.State)
		fmt.Printf("**Author:** %s  \n", rev.Author)
		if len(rev.Reviewers) > 0 {
			fmt.Printf("**Reviewers:** %s  \n", strings.Join(rev.Reviewers, ", "))
		}
		fmt.Printf("**Target:** `%s` (%s)  \n\n", util.BytesToHex(rev.TargetID)[:12], rev.TargetKind)

		// If target is a changeset, show details
		if target != nil && target.Kind == graph.KindChangeSet {
			if intentStr, ok := target.Payload["intent"].(string); ok && intentStr != "" {
				fmt.Printf("## Intent\n\n%s\n\n", intentStr)
			}

			// Show affected symbols if available
			edges, _ := db.GetEdges(target.ID, graph.EdgeAffects)
			if len(edges) > 0 {
				fmt.Println("## Affected Symbols")
				fmt.Println()
				for _, edge := range edges {
					sym, _ := db.GetNode(edge.Dst)
					if sym != nil {
						name, _ := sym.Payload["name"].(string)
						kind, _ := sym.Payload["kind"].(string)
						fmt.Printf("- `%s` (%s)\n", name, kind)
					}
				}
				fmt.Println()
			}
		}

		fmt.Println("---")
		fmt.Println("*Generated by [Kai](https://github.com/rite-day/ivcs)*")
		return nil
	}

	if reviewExportHTML {
		fmt.Println("<!DOCTYPE html>")
		fmt.Println("<html><head><title>Review: " + rev.Title + "</title>")
		fmt.Println("<style>body{font-family:sans-serif;max-width:800px;margin:0 auto;padding:20px}</style>")
		fmt.Println("</head><body>")
		fmt.Printf("<h1>%s</h1>\n", rev.Title)
		if rev.Description != "" {
			fmt.Printf("<p>%s</p>\n", rev.Description)
		}
		fmt.Printf("<p><strong>State:</strong> %s</p>\n", rev.State)
		fmt.Printf("<p><strong>Author:</strong> %s</p>\n", rev.Author)
		fmt.Printf("<p><strong>Target:</strong> <code>%s</code> (%s)</p>\n", util.BytesToHex(rev.TargetID)[:12], rev.TargetKind)
		fmt.Println("</body></html>")
	}

	return nil
}

// fetchWorkspaceFromRemote fetches a workspace and all its dependencies from a remote.
func fetchWorkspaceFromRemote(db *graph.DB, client *remote.Client, remoteName, wsName string) error {
	// Construct the workspace ref name
	refName := "ws." + wsName

	// Fetch the workspace ref
	wsRef, err := client.GetRef(refName)
	if err != nil {
		return fmt.Errorf("getting workspace ref: %w", err)
	}
	if wsRef == nil {
		return fmt.Errorf("workspace '%s' not found on remote", wsName)
	}

	fmt.Printf("  Found workspace ref: %s -> %s\n", refName, hex.EncodeToString(wsRef.Target)[:12])

	// Check if workspace already exists locally
	wsMgr := workspace.NewManager(db)
	existingWs, err := wsMgr.Get(wsName)
	if err != nil {
		return fmt.Errorf("checking existing workspace: %w", err)
	}
	if existingWs != nil {
		return fmt.Errorf("workspace '%s' already exists locally (delete it first with 'kai ws delete --ws %s')", wsName, wsName)
	}

	// Fetch the workspace node
	wsContent, wsKind, err := client.GetObject(wsRef.Target)
	if err != nil {
		return fmt.Errorf("fetching workspace object: %w", err)
	}
	if wsContent == nil {
		return fmt.Errorf("workspace object not found on remote")
	}
	if wsKind != "Workspace" {
		return fmt.Errorf("expected Workspace, got %s", wsKind)
	}

	var wsPayload map[string]interface{}
	if err := json.Unmarshal(wsContent, &wsPayload); err != nil {
		return fmt.Errorf("parsing workspace payload: %w", err)
	}

	fmt.Printf("  Fetching workspace: %s\n", wsName)

	// Extract references to fetch
	var objectsToFetch [][]byte
	seenObjects := make(map[string]bool)

	// Add base snapshot
	if baseHex, ok := wsPayload["baseSnapshot"].(string); ok && baseHex != "" {
		if baseID, err := util.HexToBytes(baseHex); err == nil {
			objectsToFetch = append(objectsToFetch, baseID)
			seenObjects[string(baseID)] = true
		}
	}

	// Add head snapshot
	if headHex, ok := wsPayload["headSnapshot"].(string); ok && headHex != "" {
		if headID, err := util.HexToBytes(headHex); err == nil {
			if !seenObjects[string(headID)] {
				objectsToFetch = append(objectsToFetch, headID)
				seenObjects[string(headID)] = true
			}
		}
	}

	// Add open changesets
	if csArr, ok := wsPayload["openChangeSets"].([]interface{}); ok {
		for _, csHex := range csArr {
			if hexStr, ok := csHex.(string); ok {
				if csID, err := util.HexToBytes(hexStr); err == nil {
					if !seenObjects[string(csID)] {
						objectsToFetch = append(objectsToFetch, csID)
						seenObjects[string(csID)] = true
					}
				}
			}
		}
	}

	// Fetch all related objects (BFS to get dependencies)
	fetchedCount := 0
	for len(objectsToFetch) > 0 {
		objID := objectsToFetch[0]
		objectsToFetch = objectsToFetch[1:]

		// Skip if already exists locally
		exists, _ := db.HasNode(objID)
		if exists {
			continue
		}

		content, kind, err := client.GetObject(objID)
		if err != nil {
			fmt.Printf("  Warning: failed to fetch object %s: %v\n", hex.EncodeToString(objID)[:12], err)
			continue
		}
		if content == nil {
			fmt.Printf("  Warning: object %s not found on remote\n", hex.EncodeToString(objID)[:12])
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(content, &payload); err != nil {
			fmt.Printf("  Warning: failed to parse object %s: %v\n", hex.EncodeToString(objID)[:12], err)
			continue
		}

		// Insert the node
		tx, err := db.BeginTx()
		if err != nil {
			continue
		}
		_, err = db.InsertNode(tx, graph.NodeKind(kind), payload)
		if err != nil {
			tx.Rollback()
			fmt.Printf("  Warning: failed to insert object %s: %v\n", hex.EncodeToString(objID)[:12], err)
			continue
		}
		tx.Commit()
		fetchedCount++

		// For Snapshot nodes, queue their parent if present
		if kind == "Snapshot" {
			if parentHex, ok := payload["parent"].(string); ok && parentHex != "" {
				if parentID, err := util.HexToBytes(parentHex); err == nil {
					if !seenObjects[string(parentID)] {
						objectsToFetch = append(objectsToFetch, parentID)
						seenObjects[string(parentID)] = true
					}
				}
			}
		}

		// For ChangeSet nodes, queue their before/after snapshots
		if kind == "ChangeSet" {
			if beforeHex, ok := payload["beforeSnapshot"].(string); ok && beforeHex != "" {
				if beforeID, err := util.HexToBytes(beforeHex); err == nil {
					if !seenObjects[string(beforeID)] {
						objectsToFetch = append(objectsToFetch, beforeID)
						seenObjects[string(beforeID)] = true
					}
				}
			}
			if afterHex, ok := payload["afterSnapshot"].(string); ok && afterHex != "" {
				if afterID, err := util.HexToBytes(afterHex); err == nil {
					if !seenObjects[string(afterID)] {
						objectsToFetch = append(objectsToFetch, afterID)
						seenObjects[string(afterID)] = true
					}
				}
			}
		}
	}

	if fetchedCount > 0 {
		fmt.Printf("  Fetched %d object(s)\n", fetchedCount)
	}

	// Now create the workspace locally
	tx, err := db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Extract the original UUID from the payload (added during push for transport)
	// If not present (legacy), fall back to using the content-addressed ref target
	var wsID []byte
	if uuidHex, ok := wsPayload["_uuid"].(string); ok && uuidHex != "" {
		wsID, err = util.HexToBytes(uuidHex)
		if err != nil {
			return fmt.Errorf("parsing workspace UUID: %w", err)
		}
		// Remove _uuid from payload - it was only for transport
		delete(wsPayload, "_uuid")
	} else {
		// Legacy fallback: use content-addressed ref target
		wsID = wsRef.Target
	}

	// Insert workspace node with the original UUID
	if err := db.InsertWorkspace(tx, wsID, wsPayload); err != nil {
		return fmt.Errorf("inserting workspace: %w", err)
	}

	// Create BASED_ON edge
	if baseHex, ok := wsPayload["baseSnapshot"].(string); ok && baseHex != "" {
		if baseID, err := util.HexToBytes(baseHex); err == nil {
			if err := db.InsertEdge(tx, wsID, graph.EdgeBasedOn, baseID, nil); err != nil {
				return fmt.Errorf("inserting BASED_ON edge: %w", err)
			}
		}
	}

	// Create HEAD_AT edge
	if headHex, ok := wsPayload["headSnapshot"].(string); ok && headHex != "" {
		if headID, err := util.HexToBytes(headHex); err == nil {
			if err := db.InsertEdge(tx, wsID, graph.EdgeHeadAt, headID, nil); err != nil {
				return fmt.Errorf("inserting HEAD_AT edge: %w", err)
			}
		}
	}

	// Create HAS_CHANGESET edges
	if csArr, ok := wsPayload["openChangeSets"].([]interface{}); ok {
		for _, csHex := range csArr {
			if hexStr, ok := csHex.(string); ok {
				if csID, err := util.HexToBytes(hexStr); err == nil {
					if err := db.InsertEdge(tx, wsID, graph.EdgeHasChangeSet, csID, nil); err != nil {
						fmt.Printf("  Warning: failed to insert HAS_CHANGESET edge: %v\n", err)
					}
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// Create local workspace ref
	refMgr := ref.NewRefManager(db)
	if err := refMgr.Set(refName, wsRef.Target, ref.KindWorkspace); err != nil {
		return fmt.Errorf("setting local ref: %w", err)
	}

	// Also store remote tracking ref
	remoteRefName := fmt.Sprintf("remote/%s/%s", remoteName, refName)
	if err := refMgr.Set(remoteRefName, wsRef.Target, ref.KindWorkspace); err != nil {
		fmt.Printf("  Warning: failed to set remote tracking ref: %v\n", err)
	}

	fmt.Printf("  Created workspace: %s\n", wsName)
	fmt.Printf("  %s -> %s\n", refName, hex.EncodeToString(wsRef.Target)[:12])
	fmt.Println("Fetch complete.")
	return nil
}

func fetchReviewFromRemote(db *graph.DB, client *remote.Client, remoteName, reviewID string) error {
	// Construct the review ref name
	refName := "review." + reviewID

	// Fetch the review ref
	reviewRef, err := client.GetRef(refName)
	if err != nil {
		return fmt.Errorf("getting review ref: %w", err)
	}
	if reviewRef == nil {
		return fmt.Errorf("review '%s' not found on remote", reviewID)
	}

	fmt.Printf("  Found review ref: %s -> %s\n", refName, hex.EncodeToString(reviewRef.Target)[:12])

	// Fetch the review object
	reviewContent, reviewKind, err := client.GetObject(reviewRef.Target)
	if err != nil {
		return fmt.Errorf("fetching review object: %w", err)
	}
	if reviewContent == nil {
		return fmt.Errorf("review object not found on remote")
	}
	if reviewKind != "Review" {
		return fmt.Errorf("expected Review, got %s", reviewKind)
	}

	var reviewPayload map[string]interface{}
	if err := json.Unmarshal(reviewContent, &reviewPayload); err != nil {
		return fmt.Errorf("parsing review payload: %w", err)
	}

	fmt.Printf("  Fetching review: %s\n", reviewPayload["title"])

	// Extract target changeset to fetch
	var objectsToFetch [][]byte
	seenObjects := make(map[string]bool)

	// Add target (changeset or workspace)
	if targetHex, ok := reviewPayload["targetId"].(string); ok && targetHex != "" {
		if targetID, err := util.HexToBytes(targetHex); err == nil {
			objectsToFetch = append(objectsToFetch, targetID)
			seenObjects[string(targetID)] = true
		}
	}

	// Fetch the target and its dependencies (BFS)
	fetchedCount := 0
	for len(objectsToFetch) > 0 {
		objID := objectsToFetch[0]
		objectsToFetch = objectsToFetch[1:]

		// Skip if already exists locally
		exists, _ := db.HasNode(objID)
		if exists {
			continue
		}

		content, kind, err := client.GetObject(objID)
		if err != nil {
			fmt.Printf("  Warning: failed to fetch object %s: %v\n", hex.EncodeToString(objID)[:12], err)
			continue
		}
		if content == nil {
			fmt.Printf("  Warning: object %s not found on remote\n", hex.EncodeToString(objID)[:12])
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(content, &payload); err != nil {
			fmt.Printf("  Warning: failed to parse object %s: %v\n", hex.EncodeToString(objID)[:12], err)
			continue
		}

		// Insert the node
		tx, err := db.BeginTx()
		if err != nil {
			continue
		}
		_, err = db.InsertNode(tx, graph.NodeKind(kind), payload)
		if err != nil {
			tx.Rollback()
			continue
		}
		tx.Commit()
		fetchedCount++

		// If this is a ChangeSet, queue its base/head snapshots
		if kind == "ChangeSet" {
			if baseHex, ok := payload["base"].(string); ok && baseHex != "" {
				if baseID, err := util.HexToBytes(baseHex); err == nil {
					if !seenObjects[string(baseID)] {
						objectsToFetch = append(objectsToFetch, baseID)
						seenObjects[string(baseID)] = true
					}
				}
			}
			if headHex, ok := payload["head"].(string); ok && headHex != "" {
				if headID, err := util.HexToBytes(headHex); err == nil {
					if !seenObjects[string(headID)] {
						objectsToFetch = append(objectsToFetch, headID)
						seenObjects[string(headID)] = true
					}
				}
			}
		}

		// If this is a Snapshot, queue its files (via HAS edges on server, or payload)
		// Snapshots store file references that may need to be fetched
	}

	if fetchedCount > 0 {
		fmt.Printf("  Fetched %d object(s)\n", fetchedCount)
	}

	// Create the review locally
	tx, err := db.BeginTx()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	// Extract the original UUID from the payload
	var revID []byte
	if uuidHex, ok := reviewPayload["_uuid"].(string); ok && uuidHex != "" {
		revID, err = util.HexToBytes(uuidHex)
		if err != nil {
			return fmt.Errorf("parsing review UUID: %w", err)
		}
		// Remove _uuid from payload - it was only for transport
		delete(reviewPayload, "_uuid")
	} else {
		// Legacy fallback: generate new UUID
		revID = make([]byte, 16)
		if _, err := rand.Read(revID); err != nil {
			return fmt.Errorf("generating review ID: %w", err)
		}
	}

	// Insert review node
	if err := db.InsertReview(tx, revID, reviewPayload); err != nil {
		return fmt.Errorf("inserting review: %w", err)
	}

	// Create REVIEW_OF edge to target
	if targetHex, ok := reviewPayload["targetId"].(string); ok && targetHex != "" {
		if targetID, err := util.HexToBytes(targetHex); err == nil {
			if err := db.InsertEdge(tx, revID, graph.EdgeReviewOf, targetID, nil); err != nil {
				fmt.Printf("  Warning: failed to insert REVIEW_OF edge: %v\n", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// Create local review ref
	refMgr := ref.NewRefManager(db)
	if err := refMgr.Set(refName, reviewRef.Target, ref.KindReview); err != nil {
		return fmt.Errorf("setting local ref: %w", err)
	}

	// Also store remote tracking ref
	remoteRefName := fmt.Sprintf("remote/%s/%s", remoteName, refName)
	if err := refMgr.Set(remoteRefName, reviewRef.Target, ref.KindReview); err != nil {
		fmt.Printf("  Warning: failed to set remote tracking ref: %v\n", err)
	}

	title, _ := reviewPayload["title"].(string)
	fmt.Printf("  Created review: %s (%s)\n", hex.EncodeToString(revID)[:12], title)
	fmt.Printf("  %s -> %s\n", refName, hex.EncodeToString(reviewRef.Target)[:12])
	fmt.Println("Fetch complete.")
	return nil
}

// Modules command implementations

const modulesRulesPath = ".kai/rules/modules.yaml"

func runModulesInit(cmd *cobra.Command, args []string) error {
	if !modulesInfer {
		// Just show current config or prompt
		matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
		if err != nil {
			return err
		}
		modules := matcher.GetAllModules()
		if len(modules) == 0 {
			fmt.Println("No modules configured.")
			fmt.Println()
			fmt.Println("To auto-detect modules from your codebase:")
			fmt.Println("  kai modules init --infer --write")
			fmt.Println()
			fmt.Println("To add modules manually:")
			fmt.Println("  kai modules add App src/app.js")
			fmt.Println("  kai modules add Utils \"src/utils/**\"")
			return nil
		}
		fmt.Printf("Found %d module(s) in %s\n", len(modules), modulesRulesPath)
		for _, m := range modules {
			fmt.Printf("  %s: %v\n", m.Name, m.Paths)
		}
		return nil
	}

	// Infer modules from directory structure
	fmt.Println("Scanning for modules...")

	var modules []module.ModuleRule

	// Look for common source root directories
	sourceRoots := []string{"src", "lib", "pkg", "internal", "app", "core"}
	testRoots := []string{"tests", "test", "__tests__", "spec"}

	// Find which source root exists
	var sourceRoot string
	for _, dir := range sourceRoots {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			sourceRoot = dir
			break
		}
	}

	if sourceRoot != "" {
		// Look inside the source root for subdirectories (these become modules)
		entries, err := os.ReadDir(sourceRoot)
		if err != nil {
			return err
		}

		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				// Capitalize first letter for module name
				name := strings.ToUpper(e.Name()[:1]) + e.Name()[1:]
				modules = append(modules, module.ModuleRule{
					Name:  name,
					Paths: []string{filepath.Join(sourceRoot, e.Name()) + "/**"},
				})
			}
		}

		// Also check for top-level files in source root (e.g., src/app.js)
		for _, e := range entries {
			if !e.IsDir() {
				ext := filepath.Ext(e.Name())
				if ext == ".js" || ext == ".ts" || ext == ".jsx" || ext == ".tsx" || ext == ".go" || ext == ".py" {
					baseName := strings.TrimSuffix(e.Name(), ext)
					name := strings.ToUpper(baseName[:1]) + baseName[1:]
					// Check if module already exists
					exists := false
					for _, m := range modules {
						if m.Name == name {
							exists = true
							break
						}
					}
					if !exists {
						modules = append(modules, module.ModuleRule{
							Name:  name,
							Paths: []string{filepath.Join(sourceRoot, e.Name())},
						})
					}
				}
			}
		}
	} else {
		// No standard source root, look for top-level directories
		entries, err := os.ReadDir(".")
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") &&
				e.Name() != "node_modules" && e.Name() != "vendor" &&
				!contains(testRoots, e.Name()) {
				name := strings.ToUpper(e.Name()[:1]) + e.Name()[1:]
				modules = append(modules, module.ModuleRule{
					Name:  name,
					Paths: []string{e.Name() + "/**"},
				})
			}
		}
	}

	// Add tests module if specified or auto-detect
	if modulesTestsGlob != "" {
		modules = append(modules, module.ModuleRule{
			Name:  "Tests",
			Paths: []string{modulesTestsGlob},
		})
	} else {
		for _, dir := range testRoots {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				modules = append(modules, module.ModuleRule{
					Name:  "Tests",
					Paths: []string{dir + "/**"},
				})
				break
			}
		}
	}

	if len(modules) == 0 {
		fmt.Println("No modules detected. Add modules manually:")
		fmt.Println("  kai modules add App src/app.js")
		return nil
	}

	// Print inferred modules
	fmt.Printf("\nInferred %d module(s):\n", len(modules))
	for _, m := range modules {
		fmt.Printf("  %s:\n", m.Name)
		for _, p := range m.Paths {
			fmt.Printf("    - %s\n", p)
		}
	}

	if modulesDryRun || !modulesWrite {
		fmt.Println()
		if !modulesWrite {
			fmt.Println("Run with --write to save to", modulesRulesPath)
		}
		return nil
	}

	// Save modules
	matcher := module.NewMatcher(modules)
	if err := matcher.SaveRules(modulesRulesPath); err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Saved to %s\n", modulesRulesPath)
	return nil
}

func runModulesAdd(cmd *cobra.Command, args []string) error {
	name := args[0]
	paths := args[1:]

	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return err
	}

	existing := matcher.GetModule(name)
	if existing != nil {
		// Update existing module
		matcher.AddModule(name, paths)
		fmt.Printf("Updated module: %s\n", name)
	} else {
		matcher.AddModule(name, paths)
		fmt.Printf("Added module: %s\n", name)
	}

	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}

	if err := matcher.SaveRules(modulesRulesPath); err != nil {
		return err
	}
	fmt.Printf("Saved to %s\n", modulesRulesPath)
	return nil
}

func runModulesList(cmd *cobra.Command, args []string) error {
	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return err
	}

	modules := matcher.GetAllModules()
	if len(modules) == 0 {
		fmt.Println("No modules configured.")
		fmt.Println("Run 'kai modules init --infer --write' or 'kai modules add <name> <glob>'")
		return nil
	}

	fmt.Printf("Modules (%d):\n", len(modules))
	for _, m := range modules {
		fmt.Printf("  %s\n", m.Name)
		for _, p := range m.Paths {
			fmt.Printf("    - %s\n", p)
		}
	}
	return nil
}

func runModulesPreview(cmd *cobra.Command, args []string) error {
	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return err
	}

	modules := matcher.GetAllModules()
	if len(modules) == 0 {
		fmt.Println("No modules configured.")
		return nil
	}

	// Get all files in the current directory
	var allFiles []string
	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			// Skip hidden directories (but not "." itself), node_modules, and vendor
			if (strings.HasPrefix(name, ".") && name != ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		allFiles = append(allFiles, path)
		return nil
	})
	if err != nil {
		return err
	}

	// Filter by module name if specified
	var targetModules []module.ModuleRule
	if len(args) > 0 {
		moduleName := args[0]
		for _, m := range modules {
			if m.Name == moduleName {
				targetModules = append(targetModules, m)
				break
			}
		}
		if len(targetModules) == 0 {
			return fmt.Errorf("module %q not found", moduleName)
		}
	} else {
		targetModules = modules
	}

	// Match files to modules
	matchedFiles := matcher.MatchPaths(allFiles)

	for _, m := range targetModules {
		files := matchedFiles[m.Name]
		fmt.Printf("%s (%d files):\n", m.Name, len(files))
		if len(files) == 0 {
			fmt.Println("  (no files matched)")
		} else {
			for _, f := range files {
				fmt.Printf("  %s\n", f)
			}
		}
		fmt.Println()
	}

	// Show unmatched files
	if len(args) == 0 {
		var unmatched []string
		for _, f := range allFiles {
			mods := matcher.MatchPath(f)
			if len(mods) == 0 {
				unmatched = append(unmatched, f)
			}
		}
		if len(unmatched) > 0 {
			fmt.Printf("Unmatched (%d files):\n", len(unmatched))
			for _, f := range unmatched {
				fmt.Printf("  %s\n", f)
			}
		}
	}

	return nil
}

func runModulesShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return err
	}

	mod := matcher.GetModule(name)
	if mod == nil {
		return fmt.Errorf("module %q not found", name)
	}

	fmt.Printf("Module: %s\n", mod.Name)
	fmt.Println("Patterns:")
	for _, p := range mod.Paths {
		fmt.Printf("  - %s\n", p)
	}
	return nil
}

func runModulesRm(cmd *cobra.Command, args []string) error {
	name := args[0]

	matcher, err := module.LoadRulesOrEmpty(modulesRulesPath)
	if err != nil {
		return err
	}

	if !matcher.RemoveModule(name) {
		return fmt.Errorf("module %q not found", name)
	}

	if err := matcher.SaveRules(modulesRulesPath); err != nil {
		return err
	}

	fmt.Printf("Removed module: %s\n", name)
	return nil
}

// contains checks if a string slice contains a value
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// ========== Coverage Ingestion ==========

const coverageMapFile = ".kai/coverage-map.json"

// runCIIngestCoverage ingests coverage reports to build file→test mappings
func runCIIngestCoverage(cmd *cobra.Command, args []string) error {
	// Read coverage file
	data, err := os.ReadFile(ciCoverageFrom)
	if err != nil {
		return fmt.Errorf("reading coverage file: %w", err)
	}

	// Detect format if auto
	format := ciCoverageFormat
	if format == "auto" {
		format = detectCoverageFormat(ciCoverageFrom, data)
	}

	// Parse coverage based on format
	var entries map[string][]CoverageEntry
	switch format {
	case "nyc":
		entries, err = parseNYCCoverage(data)
	case "coveragepy":
		entries, err = parseCoveragePyCoverage(data)
	case "jacoco":
		entries, err = parseJaCoCoCoverage(data)
	default:
		return fmt.Errorf("unknown coverage format: %s (use --format nyc|coveragepy|jacoco)", format)
	}

	if err != nil {
		return fmt.Errorf("parsing coverage (%s): %w", format, err)
	}

	// Normalize paths to repo-relative POSIX format
	normalizedEntries := make(map[string][]CoverageEntry)
	for filePath, testEntries := range entries {
		normPath := normalizePath(filePath)
		normalizedEntries[normPath] = testEntries
	}
	entries = normalizedEntries

	// Load or create coverage map
	coverageMap := loadOrCreateCoverageMap()

	// Load policy for retention settings
	ciPolicy, _, _ := loadCIPolicy()
	retentionDays := ciPolicy.Coverage.RetentionDays
	if retentionDays == 0 {
		retentionDays = 90 // Default
	}

	// Merge new entries
	timestamp := time.Now().UTC().Format(time.RFC3339)
	for filePath, testEntries := range entries {
		for i := range testEntries {
			testEntries[i].LastSeenAt = timestamp
		}
		// Merge with existing entries
		coverageMap.Entries[filePath] = mergeTestEntries(coverageMap.Entries[filePath], testEntries)
	}

	// Prune old entries based on retention policy
	pruneCount := pruneCoverageMap(coverageMap, retentionDays)

	// Update metadata
	coverageMap.IngestedAt = timestamp
	if ciCoverageBranch != "" {
		coverageMap.Branch = ciCoverageBranch
	}
	if ciCoverageTag != "" {
		coverageMap.Tag = ciCoverageTag
	}

	// Save coverage map
	if err := saveCoverageMap(coverageMap); err != nil {
		return fmt.Errorf("saving coverage map: %w", err)
	}

	// Output summary
	fmt.Println("Coverage Ingestion Complete")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Format:      %s\n", format)
	fmt.Printf("Files:       %d\n", len(entries))
	fmt.Printf("Total pairs: %d\n", countTotalPairs(coverageMap.Entries))
	if pruneCount > 0 {
		fmt.Printf("Pruned:      %d old entries (>%d days)\n", pruneCount, retentionDays)
	}
	if ciCoverageBranch != "" {
		fmt.Printf("Branch:      %s\n", ciCoverageBranch)
	}
	if ciCoverageTag != "" {
		fmt.Printf("Tag:         %s\n", ciCoverageTag)
	}
	fmt.Printf("Saved to:    %s\n", coverageMapFile)

	return nil
}

// normalizePath converts a file path to repo-relative POSIX format
func normalizePath(path string) string {
	// Convert Windows backslashes to forward slashes
	path = strings.ReplaceAll(path, "\\", "/")

	// Remove common absolute path prefixes
	if idx := strings.Index(path, "/src/"); idx >= 0 {
		path = path[idx+1:]
	} else if idx := strings.Index(path, "/app/"); idx >= 0 {
		path = path[idx+1:]
	}

	// Strip leading ./
	path = strings.TrimPrefix(path, "./")

	return path
}

// pruneCoverageMap removes entries older than retentionDays
func pruneCoverageMap(cm *CoverageMap, retentionDays int) int {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	pruneCount := 0

	for filePath, entries := range cm.Entries {
		var kept []CoverageEntry
		for _, e := range entries {
			if e.LastSeenAt != "" {
				lastSeen, err := time.Parse(time.RFC3339, e.LastSeenAt)
				if err == nil && lastSeen.Before(cutoff) {
					pruneCount++
					continue
				}
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(cm.Entries, filePath)
		} else {
			cm.Entries[filePath] = kept
		}
	}

	return pruneCount
}

// detectCoverageFormat auto-detects coverage format from filename and content
func detectCoverageFormat(path string, data []byte) string {
	name := filepath.Base(path)

	if strings.Contains(name, "coverage-final") || strings.Contains(name, "nyc") {
		return "nyc"
	}
	if strings.HasSuffix(name, ".xml") {
		return "jacoco"
	}

	content := string(data)
	if strings.Contains(content, "statementMap") {
		return "nyc"
	}
	if strings.Contains(content, "<jacoco") || strings.Contains(content, "<report") {
		return "jacoco"
	}
	if strings.Contains(content, "executed_lines") || strings.Contains(content, "missing_lines") {
		return "coveragepy"
	}

	return "nyc"
}

// parseNYCCoverage parses NYC/Istanbul coverage-final.json
func parseNYCCoverage(data []byte) (map[string][]CoverageEntry, error) {
	var nycData map[string]struct {
		Path         string         `json:"path"`
		StatementMap map[string]struct {
			Start struct{ Line int } `json:"start"`
			End   struct{ Line int } `json:"end"`
		} `json:"statementMap"`
		S map[string]int `json:"s"`
	}

	if err := json.Unmarshal(data, &nycData); err != nil {
		return nil, err
	}

	entries := make(map[string][]CoverageEntry)
	for filePath, coverage := range nycData {
		var coveredLines []int
		for stmtID, hits := range coverage.S {
			if hits > 0 {
				if stmt, ok := coverage.StatementMap[stmtID]; ok {
					coveredLines = append(coveredLines, stmt.Start.Line)
				}
			}
		}

		if len(coveredLines) > 0 {
			entries[filePath] = []CoverageEntry{
				{TestID: "aggregate", HitCount: 1, LinesCovered: coveredLines},
			}
		}
	}

	return entries, nil
}

// parseCoveragePyCoverage parses coverage.py JSON output
func parseCoveragePyCoverage(data []byte) (map[string][]CoverageEntry, error) {
	var coverageData struct {
		Files map[string]struct {
			ExecutedLines []int `json:"executed_lines"`
		} `json:"files"`
	}

	if err := json.Unmarshal(data, &coverageData); err != nil {
		return nil, err
	}

	entries := make(map[string][]CoverageEntry)
	for filePath, coverage := range coverageData.Files {
		if len(coverage.ExecutedLines) > 0 {
			entries[filePath] = []CoverageEntry{
				{TestID: "aggregate", HitCount: 1, LinesCovered: coverage.ExecutedLines},
			}
		}
	}

	return entries, nil
}

// parseJaCoCoCoverage parses JaCoCo XML format
func parseJaCoCoCoverage(data []byte) (map[string][]CoverageEntry, error) {
	entries := make(map[string][]CoverageEntry)

	sourceFileRe := regexp.MustCompile(`<sourcefile[^>]*name="([^"]+)"`)
	lineRe := regexp.MustCompile(`<line[^>]*nr="(\d+)"[^>]*ci="(\d+)"`)

	content := string(data)
	sourceMatches := sourceFileRe.FindAllStringSubmatchIndex(content, -1)

	for i, match := range sourceMatches {
		fileName := content[match[2]:match[3]]

		endIdx := len(content)
		if i+1 < len(sourceMatches) {
			endIdx = sourceMatches[i+1][0]
		}

		section := content[match[0]:endIdx]
		lineMatches := lineRe.FindAllStringSubmatch(section, -1)

		var coveredLines []int
		for _, lm := range lineMatches {
			if len(lm) >= 3 {
				lineNum := 0
				ci := 0
				fmt.Sscanf(lm[1], "%d", &lineNum)
				fmt.Sscanf(lm[2], "%d", &ci)
				if ci > 0 {
					coveredLines = append(coveredLines, lineNum)
				}
			}
		}

		if len(coveredLines) > 0 {
			entries[fileName] = []CoverageEntry{
				{TestID: "aggregate", HitCount: 1, LinesCovered: coveredLines},
			}
		}
	}

	return entries, nil
}

func loadOrCreateCoverageMap() *CoverageMap {
	data, err := os.ReadFile(coverageMapFile)
	if err != nil {
		return &CoverageMap{Version: 1, Entries: make(map[string][]CoverageEntry)}
	}

	var cm CoverageMap
	if json.Unmarshal(data, &cm) != nil {
		return &CoverageMap{Version: 1, Entries: make(map[string][]CoverageEntry)}
	}

	if cm.Entries == nil {
		cm.Entries = make(map[string][]CoverageEntry)
	}
	return &cm
}

func saveCoverageMap(cm *CoverageMap) error {
	os.MkdirAll(filepath.Dir(coverageMapFile), 0755)
	data, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(coverageMapFile, data, 0644)
}

func mergeTestEntries(existing, new []CoverageEntry) []CoverageEntry {
	entryMap := make(map[string]*CoverageEntry)
	for i := range existing {
		entryMap[existing[i].TestID] = &existing[i]
	}
	for _, e := range new {
		if ex, ok := entryMap[e.TestID]; ok {
			ex.HitCount += e.HitCount
			ex.LastSeenAt = e.LastSeenAt
		} else {
			entryCopy := e
			entryMap[e.TestID] = &entryCopy
		}
	}

	result := make([]CoverageEntry, 0, len(entryMap))
	for _, e := range entryMap {
		result = append(result, *e)
	}
	return result
}

func countTotalPairs(entries map[string][]CoverageEntry) int {
	count := 0
	for _, tests := range entries {
		count += len(tests)
	}
	return count
}

// mapKeysToSortedSlice converts a map[string]bool to a sorted slice of keys
func mapKeysToSortedSlice(m map[string]bool) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// ========== Contract Ingestion ==========

const contractsFile = ".kai/contracts.json"

// runCIIngestContracts registers contract schemas and their tests
func runCIIngestContracts(cmd *cobra.Command, args []string) error {
	validTypes := map[string]bool{"openapi": true, "protobuf": true, "graphql": true}
	if !validTypes[ciContractType] {
		return fmt.Errorf("invalid contract type: %s (use openapi, protobuf, or graphql)", ciContractType)
	}

	if _, err := os.Stat(ciContractPath); os.IsNotExist(err) {
		return fmt.Errorf("schema file not found: %s", ciContractPath)
	}

	schemaData, err := os.ReadFile(ciContractPath)
	if err != nil {
		return fmt.Errorf("reading schema: %w", err)
	}

	// Canonicalize schema before hashing to avoid noisy re-runs on non-semantic edits
	canonicalData := canonicalizeSchema(schemaData, ciContractType)
	digest := util.Blake3HashHex(canonicalData)

	registry := loadOrCreateContractRegistry()

	found := false
	for i, c := range registry.Contracts {
		if c.Path == ciContractPath {
			registry.Contracts[i].Type = ciContractType
			registry.Contracts[i].Service = ciContractService
			registry.Contracts[i].Tests = strings.Split(ciContractTests, ",")
			registry.Contracts[i].Digest = digest
			if ciContractGenerated != "" {
				registry.Contracts[i].Generated = strings.Split(ciContractGenerated, ",")
			}
			found = true
			break
		}
	}

	if !found {
		binding := ContractBinding{
			Type:    ciContractType,
			Path:    ciContractPath,
			Service: ciContractService,
			Tests:   strings.Split(ciContractTests, ","),
			Digest:  digest,
		}
		if ciContractGenerated != "" {
			binding.Generated = strings.Split(ciContractGenerated, ",")
		}
		registry.Contracts = append(registry.Contracts, binding)
	}

	if err := saveContractRegistry(registry); err != nil {
		return fmt.Errorf("saving contract registry: %w", err)
	}

	fmt.Println("Contract Registration Complete")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Type:     %s\n", ciContractType)
	fmt.Printf("Path:     %s\n", ciContractPath)
	fmt.Printf("Service:  %s\n", ciContractService)
	fmt.Printf("Tests:    %s\n", ciContractTests)
	fmt.Printf("Digest:   %s\n", digest[:16]+"...")
	if ciContractGenerated != "" {
		fmt.Printf("Generated: %s\n", ciContractGenerated)
	}
	fmt.Printf("Saved to: %s\n", contractsFile)

	return nil
}

func loadOrCreateContractRegistry() *ContractRegistry {
	data, err := os.ReadFile(contractsFile)
	if err != nil {
		return &ContractRegistry{Version: 1, Contracts: []ContractBinding{}}
	}

	var cr ContractRegistry
	if json.Unmarshal(data, &cr) != nil {
		return &ContractRegistry{Version: 1, Contracts: []ContractBinding{}}
	}
	return &cr
}

func saveContractRegistry(cr *ContractRegistry) error {
	os.MkdirAll(filepath.Dir(contractsFile), 0755)
	data, err := json.MarshalIndent(cr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(contractsFile, data, 0644)
}

// canonicalizeSchema normalizes schema content before hashing to avoid noisy re-runs
// on non-semantic changes (comments, whitespace, key ordering)
func canonicalizeSchema(data []byte, schemaType string) []byte {
	content := string(data)

	switch schemaType {
	case "openapi":
		// For YAML/JSON OpenAPI: try to parse and re-serialize with sorted keys
		// Fallback to basic normalization if parsing fails
		return canonicalizeYAMLorJSON(data)

	case "graphql":
		// For GraphQL: strip comments and normalize whitespace
		return canonicalizeGraphQL(content)

	case "protobuf":
		// For Protobuf: strip comments and normalize whitespace
		return canonicalizeProtobuf(content)
	}

	return data
}

// canonicalizeYAMLorJSON attempts to parse and re-serialize with sorted keys
func canonicalizeYAMLorJSON(data []byte) []byte {
	// Try JSON first
	var jsonObj interface{}
	if err := json.Unmarshal(data, &jsonObj); err == nil {
		if canonical, err := json.Marshal(jsonObj); err == nil {
			return canonical
		}
	}

	// Try YAML
	var yamlObj interface{}
	if err := yaml.Unmarshal(data, &yamlObj); err == nil {
		if canonical, err := yaml.Marshal(yamlObj); err == nil {
			return canonical
		}
	}

	// Fallback: basic whitespace normalization
	return normalizeWhitespace(data)
}

// canonicalizeGraphQL strips GraphQL comments and normalizes whitespace
func canonicalizeGraphQL(content string) []byte {
	// Strip single-line comments (# ...)
	lines := strings.Split(content, "\n")
	var cleaned []string
	for _, line := range lines {
		// Remove comment portion
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return []byte(strings.Join(cleaned, "\n"))
}

// canonicalizeProtobuf strips Protobuf comments and normalizes whitespace
func canonicalizeProtobuf(content string) []byte {
	// Strip // comments
	var result strings.Builder
	lines := strings.Split(content, "\n")
	inBlockComment := false

	for _, line := range lines {
		// Handle block comments /* ... */
		for {
			if inBlockComment {
				if idx := strings.Index(line, "*/"); idx >= 0 {
					line = line[idx+2:]
					inBlockComment = false
				} else {
					line = ""
					break
				}
			} else {
				if idx := strings.Index(line, "/*"); idx >= 0 {
					prefix := line[:idx]
					rest := line[idx+2:]
					if endIdx := strings.Index(rest, "*/"); endIdx >= 0 {
						line = prefix + rest[endIdx+2:]
					} else {
						line = prefix
						inBlockComment = true
					}
				} else {
					break
				}
			}
		}

		// Strip // comments
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}

		line = strings.TrimSpace(line)
		if line != "" {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	return []byte(result.String())
}

// normalizeWhitespace collapses multiple whitespace to single space
func normalizeWhitespace(data []byte) []byte {
	// Replace all whitespace sequences with single space
	re := regexp.MustCompile(`\s+`)
	return re.ReplaceAll(data, []byte(" "))
}

// ========== Plan Annotation ==========

// runCIAnnotatePlan annotates a plan with fallback information
func runCIAnnotatePlan(cmd *cobra.Command, args []string) error {
	planFile := args[0]

	data, err := os.ReadFile(planFile)
	if err != nil {
		return fmt.Errorf("reading plan: %w", err)
	}

	var plan CIPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return fmt.Errorf("parsing plan: %w", err)
	}

	if ciFallbackUsed {
		plan.Fallback.Used = true
	}
	if ciFallbackReason != "" {
		plan.Fallback.Reason = ciFallbackReason
	}
	if ciFallbackTrigger != "" {
		plan.Fallback.Trigger = ciFallbackTrigger
	}
	if ciFallbackExitCode != 0 {
		plan.Fallback.ExitCode = ciFallbackExitCode
	}

	output, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling plan: %w", err)
	}

	if err := os.WriteFile(planFile, output, 0644); err != nil {
		return fmt.Errorf("writing plan: %w", err)
	}

	fmt.Printf("Plan annotated: %s\n", planFile)
	if plan.Fallback.Used {
		fmt.Printf("  Fallback: used=true reason=%s\n", plan.Fallback.Reason)
		if plan.Fallback.Trigger != "" {
			fmt.Printf("  Trigger: %s\n", plan.Fallback.Trigger)
		}
		if plan.Fallback.ExitCode != 0 {
			fmt.Printf("  Exit code: %d\n", plan.Fallback.ExitCode)
		}
	}

	return nil
}

// runCIValidatePlan validates plan JSON schema and required fields
func runCIValidatePlan(cmd *cobra.Command, args []string) error {
	planFile := args[0]

	data, err := os.ReadFile(planFile)
	if err != nil {
		return fmt.Errorf("reading plan: %w", err)
	}

	var plan CIPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return fmt.Errorf("parsing plan: %w", err)
	}

	var errors []string

	// Required fields
	if plan.Mode == "" {
		errors = append(errors, "missing required field: mode")
	}
	if plan.Risk == "" {
		errors = append(errors, "missing required field: risk")
	}
	if plan.SafetyMode == "" {
		errors = append(errors, "missing required field: safetyMode")
	}

	// Provenance fields
	if plan.Provenance.KaiVersion == "" {
		errors = append(errors, "missing provenance.kaiVersion")
	}
	if plan.Provenance.DetectorVersion == "" {
		errors = append(errors, "missing provenance.detectorVersion")
	}
	if plan.Provenance.GeneratedAt == "" {
		errors = append(errors, "missing provenance.generatedAt")
	}

	// Strict mode: validate optional fields
	if ciValidateStrict {
		if plan.Provenance.PolicyHash == "" && plan.Mode != "skip" {
			errors = append(errors, "strict: missing provenance.policyHash")
		}
		if len(plan.Provenance.Analyzers) == 0 && plan.Mode != "skip" {
			errors = append(errors, "strict: missing provenance.analyzers")
		}
	}

	// Validate mode values
	validModes := map[string]bool{"selective": true, "expanded": true, "full": true, "shadow": true, "skip": true}
	if plan.Mode != "" && !validModes[plan.Mode] {
		errors = append(errors, fmt.Sprintf("invalid mode value: %s", plan.Mode))
	}

	// Validate risk values
	validRisks := map[string]bool{"low": true, "medium": true, "high": true}
	if plan.Risk != "" && !validRisks[plan.Risk] {
		errors = append(errors, fmt.Sprintf("invalid risk value: %s", plan.Risk))
	}

	// Validate safety mode values
	validSafetyModes := map[string]bool{"shadow": true, "guarded": true, "strict": true}
	if plan.SafetyMode != "" && !validSafetyModes[plan.SafetyMode] {
		errors = append(errors, fmt.Sprintf("invalid safetyMode value: %s", plan.SafetyMode))
	}

	if len(errors) > 0 {
		fmt.Println("Plan validation FAILED")
		fmt.Println(strings.Repeat("-", 40))
		for _, e := range errors {
			fmt.Printf("  - %s\n", e)
		}
		return fmt.Errorf("validation failed with %d errors", len(errors))
	}

	fmt.Println("Plan validation PASSED")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Mode:       %s\n", plan.Mode)
	fmt.Printf("Risk:       %s\n", plan.Risk)
	fmt.Printf("Safety:     %s\n", plan.SafetyMode)
	fmt.Printf("Confidence: %.0f%%\n", plan.Safety.Confidence*100)
	fmt.Printf("Targets:    %d run, %d skip\n", len(plan.Targets.Run), len(plan.Targets.Skip))
	fmt.Printf("Kai:        %s\n", plan.Provenance.KaiVersion)
	fmt.Printf("Detector:   %s\n", plan.Provenance.DetectorVersion)

	return nil
}
