package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"kai/internal/util"
	"kai/internal/workspace"
)

// kai resolve <workspace>
//
// Three-state lifecycle:
//
//  1. (default)            Materialize pending conflicts into .kai/conflicts/<ws>/
//                          as <path>.HEAD, <path>.TARGET, <path>.BASE files.
//                          The user edits the .HEAD files in place to be the
//                          resolved content.
//  2. --continue           Read each .HEAD file from .kai/conflicts/<ws>/ and
//                          call IntegrateWithResolutions. On success: clear
//                          conflict state and remove the conflict directory.
//  3. --abort              Clear conflict state and remove the conflict directory.
//                          The workspace returns to its pre-integrate state.

var (
	resolveContinue bool
	resolveAbort    bool
)

var resolveCmd = &cobra.Command{
	Use:   "resolve <workspace>",
	Short: "Resolve pending workspace integration conflicts",
	Long: `Materialize pending conflicts from a failed 'kai integrate' into files
that you can edit, then re-run the integration with your resolutions.

Workflow:

  $ kai integrate myws --target snap.main
  ✗ Integration produced 2 conflicts. Run 'kai resolve myws' to address them.

  $ kai resolve myws
  Wrote conflict files to .kai/conflicts/myws/:
    src/auth.go.HEAD     <- workspace version (edit this)
    src/auth.go.TARGET   <- target version (reference)
    src/auth.go.BASE     <- common ancestor (reference)
    src/db.go.HEAD       <- workspace version (edit this)
    ...
  Edit each .HEAD file to be the resolved content, then run:
    kai resolve myws --continue

  $ # ...edit src/auth.go.HEAD and src/db.go.HEAD in your editor...

  $ kai resolve myws --continue
  ✓ Integration successful (resolved 2 conflicts)`,
	Args: cobra.ExactArgs(1),
	RunE: runResolve,
}

func runResolve(cmd *cobra.Command, args []string) error {
	wsArg := args[0]

	if resolveContinue && resolveAbort {
		return fmt.Errorf("--continue and --abort are mutually exclusive")
	}

	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	mgr := workspace.NewManager(db)

	state, err := mgr.GetConflictState(wsArg)
	if err != nil {
		return fmt.Errorf("reading conflict state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("workspace %q has no pending conflicts", wsArg)
	}

	conflictDir := filepath.Join(kaiDir, "conflicts", wsArg)

	switch {
	case resolveAbort:
		return resolveAbortFlow(mgr, wsArg, conflictDir)
	case resolveContinue:
		return resolveContinueFlow(mgr, db, wsArg, state, conflictDir)
	default:
		return resolveMaterializeFlow(db, wsArg, state, conflictDir)
	}
}

// resolveMaterializeFlow writes the conflict files to disk so the user can edit them.
func resolveMaterializeFlow(db dbHandle, wsArg string, state *workspace.ConflictState, conflictDir string) error {
	if err := os.MkdirAll(conflictDir, 0755); err != nil {
		return fmt.Errorf("creating conflict directory: %w", err)
	}

	fmt.Printf("Workspace %q has %d pending conflict(s).\n\n", wsArg, len(state.Conflicts))
	fmt.Printf("Wrote conflict files to %s/:\n", conflictDir)

	for _, c := range state.Conflicts {
		safeName := pathToFilename(c.Path)

		if err := writeDigestFile(db, conflictDir, safeName+".HEAD", c.HeadDigest); err != nil {
			return err
		}
		if err := writeDigestFile(db, conflictDir, safeName+".TARGET", c.NewDigest); err != nil {
			return err
		}
		if err := writeDigestFile(db, conflictDir, safeName+".BASE", c.BaseDigest); err != nil {
			return err
		}

		fmt.Printf("  %-40s  <- workspace version (edit this)\n", safeName+".HEAD")
		fmt.Printf("  %-40s  <- target version (reference)\n", safeName+".TARGET")
		fmt.Printf("  %-40s  <- common ancestor (reference)\n", safeName+".BASE")
		if c.Description != "" {
			fmt.Printf("    reason: %s\n", c.Description)
		}
	}

	fmt.Println()
	fmt.Println("Edit each .HEAD file to be the resolved content, then run:")
	fmt.Printf("  kai resolve %s --continue\n", wsArg)
	fmt.Println()
	fmt.Println("To abort:")
	fmt.Printf("  kai resolve %s --abort\n", wsArg)

	return nil
}

// resolveContinueFlow reads the user's edited .HEAD files and re-runs the integration.
func resolveContinueFlow(mgr *workspace.Manager, db dbHandle, wsArg string, state *workspace.ConflictState, conflictDir string) error {
	if _, err := os.Stat(conflictDir); os.IsNotExist(err) {
		return fmt.Errorf("conflict directory %s does not exist — run 'kai resolve %s' first", conflictDir, wsArg)
	}

	resolutions := make(map[string][]byte, len(state.Conflicts))
	for _, c := range state.Conflicts {
		safeName := pathToFilename(c.Path)
		resolvedPath := filepath.Join(conflictDir, safeName+".HEAD")
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			return fmt.Errorf("reading resolution for %s: %w", c.Path, err)
		}
		resolutions[c.Path] = content
	}

	targetID, err := util.HexToBytes(state.TargetSnapshot)
	if err != nil {
		return fmt.Errorf("parsing target snapshot id: %w", err)
	}

	result, err := mgr.IntegrateWithResolutions(wsArg, targetID, resolutions)
	if err != nil {
		return fmt.Errorf("integrating with resolutions: %w", err)
	}

	if len(result.Conflicts) > 0 {
		fmt.Printf("Integration still has %d unresolved conflict(s):\n", len(result.Conflicts))
		for _, c := range result.Conflicts {
			fmt.Printf("  %s: %s\n", c.Path, c.Description)
		}
		return fmt.Errorf("resolve remaining conflicts before continuing")
	}

	if err := mgr.ClearConflictState(wsArg); err != nil {
		fmt.Printf("Warning: could not clear conflict state: %v\n", err)
	}
	if err := os.RemoveAll(conflictDir); err != nil {
		fmt.Printf("Warning: could not remove conflict directory %s: %v\n", conflictDir, err)
	}

	fmt.Printf("✓ Integration successful (resolved %d conflict(s))\n", len(state.Conflicts))
	fmt.Printf("  Result snapshot: %s\n", util.BytesToHex(result.ResultSnapshot))
	fmt.Printf("  Applied %d changeset(s)\n", len(result.AppliedChangeSets))
	if result.AutoResolved > 0 {
		fmt.Printf("  Auto-resolved: %d change(s)\n", result.AutoResolved)
	}
	return nil
}

// resolveAbortFlow clears conflict state and removes the conflict directory.
func resolveAbortFlow(mgr *workspace.Manager, wsArg, conflictDir string) error {
	if err := mgr.ClearConflictState(wsArg); err != nil {
		return fmt.Errorf("clearing conflict state: %w", err)
	}
	if err := os.RemoveAll(conflictDir); err != nil {
		fmt.Printf("Warning: could not remove conflict directory %s: %v\n", conflictDir, err)
	}
	fmt.Printf("Aborted. Workspace %q has no pending conflicts.\n", wsArg)
	return nil
}

// dbHandle is a narrow interface over the parts of the kai DB we need here.
// Defined locally so resolve.go doesn't pull in the full graph package surface.
type dbHandle interface {
	ReadObject(digest string) ([]byte, error)
}

// writeDigestFile reads an object by digest and writes it to <dir>/<name>.
// If digest is empty (file added/deleted), writes an empty file.
func writeDigestFile(db dbHandle, dir, name, digest string) error {
	dst := filepath.Join(dir, name)
	if digest == "" {
		return os.WriteFile(dst, nil, 0644)
	}
	content, err := db.ReadObject(digest)
	if err != nil {
		return fmt.Errorf("reading %s (digest %s): %w", name, digest, err)
	}
	return os.WriteFile(dst, content, 0644)
}

// pathToFilename converts a repo path to a flat filename safe for placement
// in the conflict directory. Slashes become __, leading dots are preserved.
func pathToFilename(p string) string {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || c == '\\' {
			out = append(out, '_', '_')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
