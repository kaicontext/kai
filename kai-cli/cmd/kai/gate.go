package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kai/internal/graph"
	"kai/internal/safetygate"
	"kai/internal/util"
	"kai/internal/workspace"
)

// `kai gate` is the human-facing surface for the safety gate's hold
// queue. When `kai integrate` produces a Review or Block verdict, the
// merged snapshot is committed to the DB but the team-visible refs
// (snap.latest, etc.) are not advanced. `kai gate list` shows what's
// held; `kai gate approve` advances the refs after human review.
//
// This is intentionally separate from `kai review`, which is the
// formal PR-style review system (Review nodes, comments, reviewers).
// The gate is a lighter-weight "did this earn auto-publish?" check.

var gateCmd = &cobra.Command{
	Use:   "gate",
	Short: "Inspect and resolve safety-gate-held integrations",
	Long: `The safety gate decides whether an agent's integration auto-promotes,
needs human review, or is blocked. Held integrations stay in the
database as orphan snapshots until you approve or reject them.

Examples:
  kai gate list                    # snapshots held by the gate
  kai gate show <id>               # verdict reasons + affected files
  kai gate approve <id>            # advance the team-visible refs
  kai gate reject <id>             # mark the held snapshot dismissed`,
}

var gateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List integrations held by the safety gate",
	RunE:  runGateList,
}

var gateShowCmd = &cobra.Command{
	Use:   "show <snapshot-id>",
	Short: "Show the gate verdict for a held integration",
	Args:  cobra.ExactArgs(1),
	RunE:  runGateShow,
}

var gateApproveCmd = &cobra.Command{
	Use:   "approve <snapshot-id>",
	Short: "Approve a held integration; advance the team-visible refs",
	Args:  cobra.ExactArgs(1),
	RunE:  runGateApprove,
}

var gateRejectCmd = &cobra.Command{
	Use:   "reject <snapshot-id>",
	Short: "Mark a held integration as dismissed (snapshot is kept for audit)",
	Args:  cobra.ExactArgs(1),
	RunE:  runGateReject,
}

func runGateList(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	held, err := safetygate.ListHeld(db)
	if err != nil {
		return fmt.Errorf("listing held snapshots: %w", err)
	}
	if len(held) == 0 {
		fmt.Println("No integrations are held by the safety gate.")
		return nil
	}
	fmt.Printf("%d integration(s) held:\n\n", len(held))
	for _, n := range held {
		v, _ := n.Payload["gateVerdict"].(string)
		blast, _ := n.Payload["gateBlastRadius"].(float64)
		from, _ := n.Payload["integratedFrom"].(string)
		createdMs, _ := n.Payload["createdAt"].(float64)
		fromShort := from
		if len(fromShort) > 12 {
			fromShort = fromShort[:12]
		}
		ts := ""
		if createdMs > 0 {
			ts = time.UnixMilli(int64(createdMs)).Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  %s  %-6s  blast=%-4d  ws=%s  %s\n",
			util.BytesToHex(n.ID)[:12], strings.ToUpper(v), int(blast), fromShort, ts)
	}
	fmt.Println("\nRun `kai gate show <id>` to inspect, `kai gate approve <id>` to publish.")
	return nil
}

// resolveSnapshotByPrefix accepts a hex prefix and returns the unique
// matching snapshot node, or an error if zero or multiple match. This
// lets the user paste the truncated id printed by `kai gate list`.
func resolveSnapshotByPrefix(db *graph.DB, prefix string) (*graph.Node, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if _, err := hex.DecodeString(strings.TrimRight(prefix, " ")); err != nil {
		// Allow odd-length prefixes (e.g. 12 chars from the listing).
		if _, err := hex.DecodeString(prefix + "0"); err != nil {
			return nil, fmt.Errorf("invalid hex id %q: %w", prefix, err)
		}
	}
	all, err := db.GetNodesByKind(graph.KindSnapshot)
	if err != nil {
		return nil, err
	}
	var matches []*graph.Node
	for _, n := range all {
		if strings.HasPrefix(util.BytesToHex(n.ID), prefix) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no snapshot matches prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("ambiguous prefix %q matches %d snapshots", prefix, len(matches))
	}
}

func runGateShow(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snap, err := resolveSnapshotByPrefix(db, args[0])
	if err != nil {
		return err
	}
	v, _ := snap.Payload["gateVerdict"].(string)
	if v == "" {
		return fmt.Errorf("snapshot %s has no gate verdict (was it produced by `kai integrate`?)",
			util.BytesToHex(snap.ID)[:12])
	}
	blast, _ := snap.Payload["gateBlastRadius"].(float64)
	from, _ := snap.Payload["integratedFrom"].(string)
	target, _ := snap.Payload["targetSnapshot"].(string)
	createdMs, _ := snap.Payload["createdAt"].(float64)

	fmt.Printf("Snapshot %s\n", util.BytesToHex(snap.ID))
	fmt.Printf("  Verdict:      %s\n", strings.ToUpper(v))
	fmt.Printf("  Blast radius: %d (depth-1 callers + importers)\n", int(blast))
	if from != "" {
		fmt.Printf("  From workspace: %s\n", from)
	}
	if target != "" {
		fmt.Printf("  Original target: %s\n", target)
	}
	if createdMs > 0 {
		fmt.Printf("  Created:      %s\n", time.UnixMilli(int64(createdMs)).Format("2006-01-02 15:04:05"))
	}
	if dismissed, _ := snap.Payload["dismissed"].(bool); dismissed {
		fmt.Println("  Status:       DISMISSED")
	}

	if reasons := stringList(snap.Payload["gateReasons"]); len(reasons) > 0 {
		fmt.Println("\nReasons:")
		for _, r := range reasons {
			fmt.Printf("  · %s\n", r)
		}
	}
	if touches := stringList(snap.Payload["gateTouches"]); len(touches) > 0 {
		fmt.Printf("\nAffected files (%d):\n", len(touches))
		for _, t := range touches {
			fmt.Printf("  %s\n", t)
		}
	}
	return nil
}

// runGateApprove delegates to workspace.Manager.ApproveHeld and prints
// the result. The "is the target ref still here?" check and the actual
// ref advance live in the workspace package so the TUI can reuse them.
func runGateApprove(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snap, err := resolveSnapshotByPrefix(db, args[0])
	if err != nil {
		return err
	}
	mgr := workspace.NewManager(db)
	advanced, err := mgr.ApproveHeld(snap.ID)
	if err != nil {
		return err
	}
	if len(advanced) == 0 {
		fmt.Println("No refs were advanced.")
		return nil
	}
	fmt.Printf("Approved snapshot %s. Advanced:\n", util.BytesToHex(snap.ID)[:12])
	for _, n := range advanced {
		fmt.Printf("  %s -> %s\n", n, util.BytesToHex(snap.ID)[:12])
	}
	return nil
}

// runGateReject delegates to workspace.Manager.RejectHeld.
func runGateReject(cmd *cobra.Command, args []string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	snap, err := resolveSnapshotByPrefix(db, args[0])
	if err != nil {
		return err
	}
	mgr := workspace.NewManager(db)
	if err := mgr.RejectHeld(snap.ID); err != nil {
		return err
	}
	fmt.Printf("Dismissed snapshot %s.\n", util.BytesToHex(snap.ID)[:12])
	return nil
}

// stringList coerces a payload value to []string. JSON-decoded payloads
// produce []interface{} rather than []string, so we normalize here.
func stringList(v interface{}) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []interface{}:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
