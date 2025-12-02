// Package main provides the ivcs CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"ivcs/internal/classify"
	"ivcs/internal/graph"
	"ivcs/internal/intent"
	"ivcs/internal/module"
	"ivcs/internal/snapshot"
	"ivcs/internal/util"
)

const (
	ivcsDir    = ".ivcs"
	dbFile     = "db.sqlite"
	objectsDir = "objects"
	rulesDir   = "rules"
	schemaDir  = "schema"
)

var rootCmd = &cobra.Command{
	Use:   "ivcs",
	Short: "Intent Version Control System - semantic, intent-based version control",
	Long:  `IVCS is a local CLI that creates semantic snapshots from Git refs, computes changesets, classifies change types, and generates intent sentences.`,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize IVCS in the current directory",
	RunE:  runInit,
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <git-ref>",
	Short: "Create a snapshot from a Git ref",
	Args:  cobra.ExactArgs(1),
	RunE:  runSnapshot,
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

var (
	repoPath string
	editText string
	jsonFlag bool
)

func init() {
	snapshotCmd.Flags().StringVar(&repoPath, "repo", ".", "Path to the Git repository")
	intentRenderCmd.Flags().StringVar(&editText, "edit", "", "Set the intent text directly")
	dumpCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")

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
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	// Create .ivcs directory
	if err := os.MkdirAll(ivcsDir, 0755); err != nil {
		return fmt.Errorf("creating .ivcs directory: %w", err)
	}

	// Create objects directory
	objPath := filepath.Join(ivcsDir, objectsDir)
	if err := os.MkdirAll(objPath, 0755); err != nil {
		return fmt.Errorf("creating objects directory: %w", err)
	}

	// Create rules directory and copy default rules
	rulesPath := filepath.Join(ivcsDir, rulesDir)
	if err := os.MkdirAll(rulesPath, 0755); err != nil {
		return fmt.Errorf("creating rules directory: %w", err)
	}

	// Copy default modules.yaml
	modulesContent := `modules:
  - name: Auth
    include: ["auth/**"]
  - name: Billing
    include: ["billing/**"]
  - name: Profile
    include: ["profile/**"]
`
	if err := os.WriteFile(filepath.Join(rulesPath, "modules.yaml"), []byte(modulesContent), 0644); err != nil {
		return fmt.Errorf("writing modules.yaml: %w", err)
	}

	// Copy default changetypes.yaml
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
	if err := os.WriteFile(filepath.Join(rulesPath, "changetypes.yaml"), []byte(changetypesContent), 0644); err != nil {
		return fmt.Errorf("writing changetypes.yaml: %w", err)
	}

	// Open database and apply schema
	dbPath := filepath.Join(ivcsDir, dbFile)
	db, err := graph.Open(dbPath, objPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Apply schema inline (since we may not have the schema file available)
	schema := `
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS nodes (
  id BLOB PRIMARY KEY,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

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
`
	if _, err := db.BeginTx(); err != nil {
		return err
	}
	// Apply schema directly via a transaction
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

	fmt.Println("Initialized IVCS in .ivcs/")
	return nil
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	gitRef := args[0]

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
	snapshotID, err := creator.CreateSnapshot(repoPath, gitRef)
	if err != nil {
		return fmt.Errorf("creating snapshot: %w", err)
	}

	fmt.Printf("Created snapshot: %s\n", util.BytesToHex(snapshotID))
	return nil
}

func runAnalyzeSymbols(cmd *cobra.Command, args []string) error {
	snapshotIDHex := args[0]
	snapshotID, err := util.HexToBytes(snapshotIDHex)
	if err != nil {
		return fmt.Errorf("invalid snapshot ID: %w", err)
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

	creator := snapshot.NewCreator(db, matcher)
	if err := creator.AnalyzeSymbols(snapshotID); err != nil {
		return fmt.Errorf("analyzing symbols: %w", err)
	}

	fmt.Println("Symbol analysis complete")
	return nil
}

func runChangesetCreate(cmd *cobra.Command, args []string) error {
	baseSnapHex := args[0]
	headSnapHex := args[1]

	baseSnapID, err := util.HexToBytes(baseSnapHex)
	if err != nil {
		return fmt.Errorf("invalid base snapshot ID: %w", err)
	}

	headSnapID, err := util.HexToBytes(headSnapHex)
	if err != nil {
		return fmt.Errorf("invalid head snapshot ID: %w", err)
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

	fmt.Printf("Created changeset: %s\n", util.BytesToHex(changeSetID))
	fmt.Printf("Changed files: %d\n", len(changedPaths))
	fmt.Printf("Change types detected: %d\n", len(allChangeTypes))
	fmt.Printf("Affected modules: %v\n", affectedModules)

	return nil
}

func runIntentRender(cmd *cobra.Command, args []string) error {
	changeSetIDHex := args[0]
	changeSetID, err := util.HexToBytes(changeSetIDHex)
	if err != nil {
		return fmt.Errorf("invalid changeset ID: %w", err)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	gen := intent.NewGenerator(db)
	intentText, err := gen.RenderIntent(changeSetID, editText)
	if err != nil {
		return fmt.Errorf("rendering intent: %w", err)
	}

	fmt.Printf("Intent: %s\n", intentText)
	return nil
}

func runDump(cmd *cobra.Command, args []string) error {
	changeSetIDHex := args[0]
	changeSetID, err := util.HexToBytes(changeSetIDHex)
	if err != nil {
		return fmt.Errorf("invalid changeset ID: %w", err)
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

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
	dbPath := filepath.Join(ivcsDir, dbFile)
	objPath := filepath.Join(ivcsDir, objectsDir)
	return graph.Open(dbPath, objPath)
}

func loadMatcher() (*module.Matcher, error) {
	rulesPath := filepath.Join(ivcsDir, rulesDir, "modules.yaml")
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

	fmt.Printf("%-64s  %-20s  %s\n", "ID", "GIT REF", "FILES")
	fmt.Println(string(make([]byte, 100)))
	for _, node := range nodes {
		gitRef, _ := node.Payload["gitRef"].(string)
		fileCount := ""
		if fc, ok := node.Payload["fileCount"].(float64); ok {
			fileCount = fmt.Sprintf("%.0f", fc)
		}
		fmt.Printf("%-64s  %-20s  %s\n", util.BytesToHex(node.ID), gitRef, fileCount)
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
	fmt.Println(string(make([]byte, 80)))
	for _, node := range nodes {
		intent, _ := node.Payload["intent"].(string)
		if intent == "" {
			intent = "(no intent)"
		}
		fmt.Printf("%-64s  %s\n", util.BytesToHex(node.ID), intent)
	}

	return nil
}
