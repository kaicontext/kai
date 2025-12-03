// Package main provides the kai CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kai/internal/classify"
	"kai/internal/dirio"
	"kai/internal/filesource"
	"kai/internal/gitio"
	"kai/internal/graph"
	"kai/internal/intent"
	"kai/internal/module"
	"kai/internal/ref"
	"kai/internal/snapshot"
	"kai/internal/status"
	"kai/internal/util"
	"kai/internal/workspace"
)

const (
	kaiDir     = ".kai"
	dbFile     = "db.sqlite"
	objectsDir = "objects"
	rulesDir   = "rules"
	schemaDir  = "schema"
)

var rootCmd = &cobra.Command{
	Use:   "kai",
	Short: "Kai - semantic, intent-based version control",
	Long:  `Kai is a local CLI that creates semantic snapshots from Git refs, computes changesets, classifies change types, and generates intent sentences.`,
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
	editText      string
	jsonFlag      bool
	checkoutDir   string
	checkoutClean bool

	// Ref/pick flags
	refKindFilter string
	pickFilter    string
	pickNoUI      bool
)

func init() {
	snapshotCmd.Flags().StringVar(&repoPath, "repo", ".", "Path to the Git repository")
	snapshotCmd.Flags().StringVar(&dirPath, "dir", "", "Path to directory (creates snapshot without Git)")
	intentRenderCmd.Flags().StringVar(&editText, "edit", "", "Set the intent text directly")
	dumpCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	logCmd.Flags().IntVarP(&logLimit, "limit", "n", 10, "Number of entries to show")
	statusCmd.Flags().StringVar(&statusDir, "dir", ".", "Directory to check for changes")
	statusCmd.Flags().StringVar(&statusAgainst, "against", "", "Baseline ref/selector to compare against (default: @snap:last)")
	statusCmd.Flags().BoolVar(&statusNameOnly, "name-only", false, "Output just paths with status prefixes (A/M/D)")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVar(&statusSemantic, "semantic", false, "Include semantic change type analysis for modified files")

	// Workspace command flags
	wsCreateCmd.Flags().StringVar(&wsName, "name", "", "Workspace name (required)")
	wsCreateCmd.Flags().StringVar(&wsBase, "base", "", "Base snapshot ID (required)")
	wsCreateCmd.Flags().StringVar(&wsDescription, "desc", "", "Workspace description")
	wsCreateCmd.MarkFlagRequired("name")
	wsCreateCmd.MarkFlagRequired("base")

	wsStageCmd.Flags().StringVar(&wsName, "ws", "", "Workspace name or ID (required)")
	wsStageCmd.Flags().StringVar(&wsDir, "dir", ".", "Directory to stage from")
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
	rootCmd.AddCommand(wsCmd)
	rootCmd.AddCommand(integrateCmd)
	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(refCmd)
	rootCmd.AddCommand(pickCmd)
	rootCmd.AddCommand(completionCmd)
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

	// Create rules directory and copy default rules
	rulesPath := filepath.Join(kaiDir, rulesDir)
	if err := os.MkdirAll(rulesPath, 0755); err != nil {
		return fmt.Errorf("creating rules directory: %w", err)
	}

	// Write default modules.yaml only if it doesn't exist
	modulesPath := filepath.Join(rulesPath, "modules.yaml")
	if _, err := os.Stat(modulesPath); os.IsNotExist(err) {
		modulesContent := `modules:
  - name: Auth
    include: ["auth/**"]
  - name: Billing
    include: ["billing/**"]
  - name: Profile
    include: ["profile/**"]
`
		if err := os.WriteFile(modulesPath, []byte(modulesContent), 0644); err != nil {
			return fmt.Errorf("writing modules.yaml: %w", err)
		}
	}

	// Write default changetypes.yaml only if it doesn't exist
	changetypesPath := filepath.Join(rulesPath, "changetypes.yaml")
	if _, err := os.Stat(changetypesPath); os.IsNotExist(err) {
		changetypesContent := `rules:
  - id: CONDITION_CHANGED
    match:
      node_types: ["binary_expression","logical_expression","relational_expression"]
      detector: "operator_or_boundary_changed"
  - id: CONSTANT_UPDATED
    match:
      node_types: ["number","string"]
      detector: "literal_value_changed"
  - id: API_SURFACE_CHANGED
    match:
      node_types: ["function_declaration","method_definition","export_statement"]
      detector: "params_or_exports_changed"
`
		if err := os.WriteFile(changetypesPath, []byte(changetypesContent), 0644); err != nil {
			return fmt.Errorf("writing changetypes.yaml: %w", err)
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
		"description": "",
		"intent":      "",
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

		if len(beforeContent) > 0 && len(afterContent) > 0 {
			changes, err := detector.DetectChanges(path, beforeContent, afterContent, util.BytesToHex(changedFileIDs[i]))
			if err == nil {
				allChangeTypes = append(allChangeTypes, changes...)
			}
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
	intentText, err := gen.RenderIntent(changeSetID, editText)
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
	dbPath := filepath.Join(kaiDir, dbFile)
	objPath := filepath.Join(kaiDir, objectsDir)
	return graph.Open(dbPath, objPath)
}

func loadMatcher() (*module.Matcher, error) {
	rulesPath := filepath.Join(kaiDir, rulesDir, "modules.yaml")
	return module.LoadRules(rulesPath)
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

		entries = append(entries, logEntry{
			ID:        util.BytesToHex(node.ID),
			Kind:      "snapshot",
			CreatedAt: int64(createdAt),
			Summary:   fmt.Sprintf("%s (%s)", sourceRef, sourceType),
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
		intentText, _ := node.Payload["intent"].(string)
		if intentText == "" {
			intentText = "(no intent)"
		}

		base, _ := node.Payload["base"].(string)
		head, _ := node.Payload["head"].(string)

		entries = append(entries, logEntry{
			ID:        util.BytesToHex(node.ID),
			Kind:      "changeset",
			CreatedAt: int64(createdAt),
			Summary:   intentText,
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

		if len(snapshots) == 0 {
			fmt.Println("No snapshots yet. Create one with:")
			fmt.Println("  kai snapshot --dir ./src")
			fmt.Println("  kai snapshot <git-ref> --repo .")
			return nil
		}

		fmt.Printf("Checking for changes in: %s\n", statusDir)
		fmt.Println()
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
		fmt.Println("Create a new snapshot to capture these changes:")
		fmt.Printf("  kai snapshot --dir %s\n", statusDir)
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
	result, err := mgr.Stage(wsName, source, matcher)
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
		intent, _ := cs.Payload["intent"].(string)
		if intent == "" {
			intent = "(no intent)"
		}
		createdAt, _ := cs.Payload["createdAt"].(float64)
		t := time.UnixMilli(int64(createdAt))

		fmt.Printf("\n  [%d] %s\n", i+1, util.BytesToHex(cs.ID)[:12])
		fmt.Printf("      Date:   %s\n", t.Format("2006-01-02 15:04:05"))
		fmt.Printf("      Intent: %s\n", intent)
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

	// Verify target directory exists
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return fmt.Errorf("target directory does not exist: %s", targetDir)
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
