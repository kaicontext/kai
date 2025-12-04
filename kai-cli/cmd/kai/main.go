// Package main provides the kai CLI.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kai-core/merge"
	"kai/internal/classify"
	"kai/internal/dirio"
	"kai/internal/filesource"
	"kai/internal/gitio"
	"kai/internal/graph"
	"kai/internal/intent"
	"kai/internal/module"
	"kai/internal/ref"
	"kai/internal/remote"
	"kai/internal/snapshot"
	"kai/internal/status"
	"kai/internal/util"
	"kai/internal/workspace"
)

const (
	kaiDir      = ".kai"
	dbFile      = "db.sqlite"
	objectsDir  = "objects"
	schemaDir   = "schema"
	modulesFile = "kai.modules.yaml"
)

// Version is the current kai CLI version
var Version = "0.2.3"

var rootCmd = &cobra.Command{
	Use:     "kai",
	Short:   "Kai - semantic, intent-based version control",
	Long:    `Kai is a local CLI that creates semantic snapshots from Git refs, computes changesets, classifies change types, and generates intent sentences.`,
	Version: Version,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Kai in the current directory",
	RunE:  runInit,
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot [git-ref]",
	Short: "Create a snapshot from a Git ref or directory",
	Long: `Create a snapshot from a Git ref or directory.

Examples:
  kai snapshot main --repo .     # Snapshot from Git ref
  kai snapshot --dir ./src       # Snapshot from directory (no Git required)`,
	RunE: runSnapshot,
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

var changesetCmd = &cobra.Command{
	Use:   "changeset",
	Short: "ChangeSet commands",
}

var changesetCreateCmd = &cobra.Command{
	Use:   "create <base-snap> <head-snap>",
	Short: "Create a changeset between two snapshots",
	Args:  cobra.ExactArgs(2),
	RunE:  runChangesetCreate,
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
	Use:   "list",
	Short: "List resources",
}

var listSnapshotsCmd = &cobra.Command{
	Use:   "snapshots",
	Short: "List all snapshots",
	RunE:  runListSnapshots,
}

var listChangesetsCmd = &cobra.Command{
	Use:   "changesets",
	Short: "List all changesets",
	RunE:  runListChangesets,
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
	Use:   "diff <base-ref> [head-ref]",
	Short: "Show differences between two snapshots",
	Long: `Show file-level differences between two snapshots.

If head-ref is omitted, compares base-ref against the working directory.

Examples:
  kai diff @snap:prev @snap:last   # Compare two snapshots
  kai diff @snap:last              # Compare snapshot vs working directory`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runDiff,
}

// Workspace commands
var wsCmd = &cobra.Command{
	Use:   "ws",
	Short: "Workspace (branch) commands",
	Long:  `Workspaces are lightweight, isolated, mutable overlays on top of immutable snapshots.`,
}

var wsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new workspace",
	RunE:  runWsCreate,
}

var wsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE:  runWsList,
}

var wsStageCmd = &cobra.Command{
	Use:   "stage",
	Short: "Stage changes into a workspace",
	RunE:  runWsStage,
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
	Use:   "push [remote] [refs...]",
	Short: "Push snapshots and refs to a remote server",
	Long: `Push objects and refs to a remote Kailab server.

By default, pushes to the 'origin' remote. You can specify refs to push,
or push all local refs with --all.

Examples:
  kai push                        # Push snap.latest to origin
  kai push origin snap.main       # Push specific ref
  kai push --all                  # Push all refs
  kai push --force snap.main      # Force push (non-fast-forward)`,
	RunE: runPush,
}

var fetchCmd = &cobra.Command{
	Use:   "fetch [remote] [refs...]",
	Short: "Fetch refs and objects from a remote server",
	Long: `Fetch refs and objects from a remote Kailab server.

By default, fetches from the 'origin' remote.

Examples:
  kai fetch                       # Fetch all refs from origin
  kai fetch origin                # Fetch all refs
  kai fetch origin snap.main      # Fetch specific ref`,
	RunE: runFetch,
}

var cloneCmd = &cobra.Command{
	Use:   "clone <url> [directory]",
	Short: "Clone a repository from a remote server",
	Long: `Clone a Kai repository from a remote Kailab server.

Creates a new directory, initializes Kai, sets up the remote, and fetches all refs.

The URL format is: http://server/tenant/repo
Or specify tenant and repo separately with flags.

Examples:
  kai clone http://localhost:8080/myorg/myrepo
  kai clone http://localhost:8080/myorg/myrepo myproject
  kai clone http://localhost:8080 --tenant myorg --repo myrepo`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runClone,
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

var (
	// Workspace flags
	wsName        string
	wsBase        string
	wsDescription string
	wsDir         string
	wsTarget      string
	statusDir      string
	statusAgainst  string
	statusNameOnly bool
	statusJSON     bool
	statusSemantic bool
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
	changesetMessage string
	wsStageMessage   string

	// Diff flags
	diffDir      string
	diffNameOnly bool

	// Snapshot flags
	snapshotMessage string

	// Push/fetch flags
	pushForce     bool
	pushAll       bool
	remoteLogRef  string
	remoteLogLimit int

	// Remote set flags
	remoteTenant  string
	remoteRepo    string

	// Clone flags
	cloneTenant string
	cloneRepo   string

	// Merge flags
	mergeLang   string
	mergeOutput string
	mergeJSON   bool
)

func init() {
	snapshotCmd.Flags().StringVar(&repoPath, "repo", ".", "Path to the Git repository")
	snapshotCmd.Flags().StringVar(&dirPath, "dir", "", "Path to directory (creates snapshot without Git)")
	snapshotCmd.Flags().StringVarP(&snapshotMessage, "message", "m", "", "Description for this snapshot")
	intentRenderCmd.Flags().StringVar(&editText, "edit", "", "Set the intent text directly")
	intentRenderCmd.Flags().BoolVar(&regenerateIntent, "regenerate", false, "Force regenerate intent (ignore saved)")
	dumpCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	logCmd.Flags().IntVarP(&logLimit, "limit", "n", 10, "Number of entries to show")
	statusCmd.Flags().StringVar(&statusDir, "dir", ".", "Directory to check for changes")
	statusCmd.Flags().StringVar(&statusAgainst, "against", "", "Baseline ref/selector to compare against (default: @snap:last)")
	statusCmd.Flags().BoolVar(&statusNameOnly, "name-only", false, "Output just paths with status prefixes (A/M/D)")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusSemantic, "semantic", false, "Include semantic change type analysis for modified files")

	// Changeset command flags
	changesetCreateCmd.Flags().StringVarP(&changesetMessage, "message", "m", "", "Changeset message describing the intent")

	// Diff command flags
	diffCmd.Flags().StringVar(&diffDir, "dir", ".", "Directory to compare against (when comparing snapshot vs working dir)")
	diffCmd.Flags().BoolVar(&diffNameOnly, "name-only", false, "Output just paths with status prefixes (A/M/D)")

	// Workspace command flags
	wsCreateCmd.Flags().StringVar(&wsName, "name", "", "Workspace name (required)")
	wsCreateCmd.Flags().StringVar(&wsBase, "base", "", "Base snapshot ID (required)")
	wsCreateCmd.Flags().StringVar(&wsDescription, "desc", "", "Workspace description")
	wsCreateCmd.MarkFlagRequired("name")
	wsCreateCmd.MarkFlagRequired("base")

	wsStageCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsStageCmd.Flags().StringVar(&wsDir, "dir", ".", "Directory to stage from")
	wsStageCmd.Flags().StringVarP(&wsStageMessage, "message", "m", "", "Message describing the staged changes")
	wsStageCmd.MarkFlagRequired("ws")

	wsLogCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsLogCmd.MarkFlagRequired("ws")

	wsShelveCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsShelveCmd.MarkFlagRequired("ws")

	wsUnshelveCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsUnshelveCmd.MarkFlagRequired("ws")

	wsCloseCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsCloseCmd.MarkFlagRequired("ws")

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
	pushCmd.Flags().BoolVar(&pushAll, "all", false, "Push all refs")
	remoteLogCmd.Flags().StringVar(&remoteLogRef, "ref", "", "Filter by ref name")
	remoteLogCmd.Flags().IntVarP(&remoteLogLimit, "limit", "n", 20, "Number of entries to show")

	// Remote set flags
	remoteSetCmd.Flags().StringVar(&remoteTenant, "tenant", "default", "Tenant/org name for the remote")
	remoteSetCmd.Flags().StringVar(&remoteRepo, "repo", "main", "Repository name for the remote")

	// Clone flags
	cloneCmd.Flags().StringVar(&cloneTenant, "tenant", "", "Tenant/org name (extracted from URL if not specified)")
	cloneCmd.Flags().StringVar(&cloneRepo, "repo", "", "Repository name (extracted from URL if not specified)")

	// Merge flags
	mergeCmd.Flags().StringVar(&mergeLang, "lang", "", "Language (js, ts, py) - auto-detected from extension if not specified")
	mergeCmd.Flags().StringVarP(&mergeOutput, "output", "o", "", "Output file path (defaults to stdout)")
	mergeCmd.Flags().BoolVar(&mergeJSON, "json", false, "Output result as JSON (includes conflicts)")

	// Add remote subcommands
	remoteCmd.AddCommand(remoteSetCmd)
	remoteCmd.AddCommand(remoteGetCmd)
	remoteCmd.AddCommand(remoteListCmd)
	remoteCmd.AddCommand(remoteDelCmd)

	// Add ref subcommands
	refCmd.AddCommand(refListCmd)
	refCmd.AddCommand(refSetCmd)
	refCmd.AddCommand(refDelCmd)

	// Set up dynamic completions for commands that accept IDs
	analyzeSymbolsCmd.ValidArgsFunction = completeSnapshotID
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

	analyzeCmd.AddCommand(analyzeSymbolsCmd)
	changesetCmd.AddCommand(changesetCreateCmd)
	intentCmd.AddCommand(intentRenderCmd)
	listCmd.AddCommand(listSnapshotsCmd)
	listCmd.AddCommand(listChangesetsCmd)

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(changesetCmd)
	rootCmd.AddCommand(intentCmd)
	rootCmd.AddCommand(dumpCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(wsCmd)
	rootCmd.AddCommand(integrateCmd)
	rootCmd.AddCommand(mergeCmd)
	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(refCmd)
	rootCmd.AddCommand(pickCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(remoteCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(fetchCmd)
	rootCmd.AddCommand(cloneCmd)
	rootCmd.AddCommand(remoteLogCmd)

	// Add auth subcommands
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authCmd)
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

func runInit(cmd *cobra.Command, args []string) error {
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

## Getting Started from Scratch

If this is a new project without Kai, follow these steps:

### Step 1: Initialize Kai
` + "```" + `bash
cd /path/to/your/project
kai init
` + "```" + `
This creates a ` + "`" + `.kai/` + "`" + ` directory with the database and object storage.

### Step 2: Create Your First Snapshot
` + "```" + `bash
# Option A: From a Git branch/tag/commit
kai snapshot main --repo .

# Option B: From a directory (no Git required)
kai snapshot --dir .
` + "```" + `
This captures the current state of your codebase. Output shows a snapshot ID like ` + "`" + `abc123...` + "`" + `

### Step 3: Analyze Symbols
` + "```" + `bash
kai analyze symbols @snap:last
` + "```" + `
This parses your code and extracts functions, classes, and variables.

### Step 4: Make Changes and Snapshot Again
After modifying code:
` + "```" + `bash
kai snapshot --dir .
kai analyze symbols @snap:last
` + "```" + `

### Step 5: Create a ChangeSet
` + "```" + `bash
kai changeset create @snap:prev @snap:last
` + "```" + `
This compares the two snapshots and classifies what changed.

### Step 6: Generate Intent
` + "```" + `bash
kai intent render @cs:last
` + "```" + `
Output: ` + "`" + `Intent: Update Auth login` + "`" + ` (auto-generated summary)

### Step 7: Export as JSON (Optional)
` + "```" + `bash
kai dump @cs:last --json
` + "```" + `
Get full structured data about the changeset.

## The Flow (Summary)

` + "```" + `
[Code Changes] → snapshot → analyze → changeset → intent
     ↓              ↓          ↓          ↓          ↓
  Your edits    Capture    Extract    Compare    Summarize
                 state     symbols    changes    in English
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
kai snapshot --dir .
kai analyze symbols @snap:last
kai changeset create @snap:prev @snap:last
kai dump @cs:last --json | jq '.nodes[] | select(.kind == "ChangeType")'
` + "```" + `

### "I want to compare two Git branches"
` + "```" + `bash
kai snapshot main --repo .
kai snapshot feature-branch --repo .
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

## Troubleshooting

| Error | Fix |
|-------|-----|
| "Kai not initialized" | Run ` + "`" + `kai init` + "`" + ` first |
| "No snapshots found" | Create one with ` + "`" + `kai snapshot --dir .` + "`" + ` |
| "ambiguous prefix" | Use more characters of the ID, or use ` + "`" + `@snap:last` + "`" + ` |

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

	fmt.Println("Initialized Kai in .kai/")
	return nil
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	var source filesource.FileSource

	if dirPath != "" {
		// Directory mode - no Git required
		source, err = dirio.OpenDirectory(dirPath)
		if err != nil {
			return fmt.Errorf("opening directory: %w", err)
		}
	} else {
		// Git mode - requires a git ref argument
		if len(args) < 1 {
			return fmt.Errorf("git ref required (use --dir for directory mode)")
		}
		gitRef := args[0]
		source, err = gitio.OpenSource(repoPath, gitRef)
		if err != nil {
			return fmt.Errorf("opening git source: %w", err)
		}
	}

	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		return fmt.Errorf("creating snapshot: %w", err)
	}

	// Auto-analyze symbols for better intent generation
	if err := creator.AnalyzeSymbols(snapshotID); err != nil {
		// Non-fatal - continue without symbols
		fmt.Fprintf(os.Stderr, "warning: symbol analysis failed: %v\n", err)
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
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnSnapshotCreated(snapshotID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	fmt.Printf("Created snapshot: %s\n", util.BytesToHex(snapshotID))
	fmt.Printf("Source: %s (%s)\n", source.Identifier(), source.SourceType())
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
	if err := creator.AnalyzeSymbols(snapshotID); err != nil {
		return fmt.Errorf("analyzing symbols: %w", err)
	}

	fmt.Println("Symbol analysis complete")
	return nil
}

func runChangesetCreate(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	baseSnapID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving base snapshot: %w", err)
	}

	headSnapID, err := resolveSnapshotID(db, args[1])
	if err != nil {
		return fmt.Errorf("resolving head snapshot: %w", err)
	}

	matcher, err := loadMatcher()
	if err != nil {
		return err
	}

	// Get files from both snapshots
	creator := snapshot.NewCreator(db, matcher)
	baseFiles, err := creator.GetSnapshotFiles(baseSnapID)
	if err != nil {
		return fmt.Errorf("getting base files: %w", err)
	}

	headFiles, err := creator.GetSnapshotFiles(headSnapID)
	if err != nil {
		return fmt.Errorf("getting head files: %w", err)
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
		return err
	}
	defer tx.Rollback()

	// Create changeset node
	changeSetPayload := map[string]interface{}{
		"base":        util.BytesToHex(baseSnapID),
		"head":        util.BytesToHex(headSnapID),
		"title":       "",
		"description": changesetMessage,
		"intent":      "",
		"createdAt":   util.NowMs(),
	}
	changeSetID, err := db.InsertNode(tx, graph.KindChangeSet, changeSetPayload)
	if err != nil {
		return fmt.Errorf("inserting changeset: %w", err)
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
			return fmt.Errorf("inserting MODIFIES edge: %w", err)
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
			return fmt.Errorf("inserting change type: %w", err)
		}
		if err := db.InsertEdge(tx, changeSetID, graph.EdgeHas, ctID, nil); err != nil {
			return fmt.Errorf("inserting HAS edge: %w", err)
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
				return fmt.Errorf("inserting module: %w", err)
			}
			if err := db.InsertEdge(tx, changeSetID, graph.EdgeAffects, modID, nil); err != nil {
				return fmt.Errorf("inserting AFFECTS edge: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// Update auto-refs
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnChangeSetCreated(changeSetID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	fmt.Printf("Created changeset: %s\n", util.BytesToHex(changeSetID))
	fmt.Printf("Changed files: %d\n", len(changedPaths))
	fmt.Printf("Change types detected: %d\n", len(allChangeTypes))
	fmt.Printf("Affected modules: %v\n", affectedModules)

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

func loadMatcher() (*module.Matcher, error) {
	return module.LoadRules(modulesFile)
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
		} else {
			fmt.Println("Create a new snapshot to capture these changes:")
		}
		fmt.Printf("  kai snapshot --dir %s\n", statusDir)
	}

	return nil
}

func runDiff(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Resolve base snapshot
	baseSnapID, err := resolveSnapshotID(db, args[0])
	if err != nil {
		return fmt.Errorf("resolving base snapshot: %w", err)
	}

	creator := snapshot.NewCreator(db, nil)

	// Get base snapshot files
	baseFiles, err := creator.GetSnapshotFiles(baseSnapID)
	if err != nil {
		return fmt.Errorf("getting base files: %w", err)
	}

	baseFileMap := make(map[string]string) // path -> digest
	for _, f := range baseFiles {
		path, _ := f.Payload["path"].(string)
		digest, _ := f.Payload["digest"].(string)
		baseFileMap[path] = digest
	}

	var headFileMap map[string]string

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
		for _, f := range headFiles {
			path, _ := f.Payload["path"].(string)
			digest, _ := f.Payload["digest"].(string)
			headFileMap[path] = digest
		}

		fmt.Printf("Diff: %s → %s\n\n", util.BytesToHex(baseSnapID)[:12], util.BytesToHex(headSnapID)[:12])
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
		for _, f := range currentFiles {
			headFileMap[f.Path] = util.Blake3HashHex(f.Content)
		}

		fmt.Printf("Diff: %s → working directory (%s)\n\n", util.BytesToHex(baseSnapID)[:12], diffDir)
	}

	// Compute differences
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

	// Output
	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		fmt.Println("No differences.")
		return nil
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
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	baseID, err := resolveSnapshotID(db, wsBase)
	if err != nil {
		return fmt.Errorf("resolving base snapshot: %w", err)
	}

	mgr := workspace.NewManager(db)
	ws, err := mgr.Create(wsName, baseID, wsDescription)
	if err != nil {
		return fmt.Errorf("creating workspace: %w", err)
	}

	// Update auto-refs
	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnWorkspaceCreated(wsName, baseID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update refs: %v\n", err)
	}

	fmt.Printf("Created workspace: %s\n", wsName)
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
	result, err := mgr.Stage(wsName, source, matcher, wsStageMessage)
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
	if err := autoRefMgr.OnWorkspaceHeadChanged(wsName, result.HeadSnapshot); err != nil {
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
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	// Determine remote name
	remoteName := "origin"
	refsToPush := []string{}

	if len(args) > 0 {
		// Check if first arg is a remote name
		if _, err := remote.GetRemote(args[0]); err == nil {
			remoteName = args[0]
			refsToPush = args[1:]
		} else {
			// First arg is a ref
			refsToPush = args
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
	var refsToSync []*ref.Ref

	if pushAll {
		refsToSync, err = refMgr.List(nil)
		if err != nil {
			return fmt.Errorf("listing refs: %w", err)
		}
	} else if len(refsToPush) > 0 {
		for _, name := range refsToPush {
			r, err := refMgr.Get(name)
			if err != nil {
				return fmt.Errorf("getting ref '%s': %w", name, err)
			}
			if r != nil {
				refsToSync = append(refsToSync, r)
			}
		}
	} else {
		// Default: push snap.latest if it exists
		r, _ := refMgr.Get("snap.latest")
		if r != nil {
			refsToSync = append(refsToSync, r)
		}
		// Also push cs.latest
		r, _ = refMgr.Get("cs.latest")
		if r != nil {
			refsToSync = append(refsToSync, r)
		}
	}

	if len(refsToSync) == 0 {
		fmt.Println("Nothing to push.")
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
		for _, digest := range missing {
			node, err := db.GetNode(digest)
			if err != nil {
				continue
			}
			if node == nil {
				continue
			}

			// Content must match how the digest was computed:
			// digest = blake3(kind + "\n" + canonicalJSON(payload))
			payloadJSON, err := util.CanonicalJSON(node.Payload)
			if err != nil {
				continue
			}
			content := append([]byte(string(node.Kind)+"\n"), payloadJSON...)

			packObjects = append(packObjects, remote.PackObject{
				Digest:  digest,
				Kind:    string(node.Kind),
				Content: content,
			})
		}

		if len(packObjects) > 0 {
			result, err := client.PushPack(packObjects)
			if err != nil {
				return fmt.Errorf("pushing pack: %w", err)
			}
			fmt.Printf("  Pushed segment %d (%d objects)\n", result.SegmentID, result.Indexed)
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

	fmt.Println("Push complete.")
	return nil
}

func runFetch(cmd *cobra.Command, args []string) error {
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
