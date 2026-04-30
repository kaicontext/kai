package workspace

import (
	"fmt"
	"strings"

	"kai/internal/ref"
	"kai/internal/util"
)

// ApproveHeld advances every non-ws.* ref currently pointing at the
// held snapshot's original target, effectively publishing a previously-
// gated integration after human review.
//
// Refuses if the original target ref has moved past the integrate
// point — re-running `kai integrate` against the current target is the
// safer fix in that case (otherwise we'd regress newer auto-promoted
// changes).
//
// Returns the names of refs that were advanced.
//
// This is the engine-level operation behind both `kai gate approve`
// and the TUI's gate pane "approve" hotkey.
func (m *Manager) ApproveHeld(snapID []byte) ([]string, error) {
	snap, err := m.db.GetNode(snapID)
	if err != nil {
		return nil, fmt.Errorf("getting snapshot: %w", err)
	}
	if snap == nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	if dismissed, _ := snap.Payload["dismissed"].(bool); dismissed {
		return nil, fmt.Errorf("snapshot is dismissed; cannot approve")
	}

	targetHex, _ := snap.Payload["targetSnapshot"].(string)
	if targetHex == "" {
		return nil, fmt.Errorf("snapshot has no targetSnapshot in payload")
	}
	targetID, err := util.HexToBytes(targetHex)
	if err != nil {
		return nil, fmt.Errorf("decoding target hex: %w", err)
	}

	wsHex, _ := snap.Payload["integratedFrom"].(string)
	if wsHex == "" {
		return nil, fmt.Errorf("snapshot has no integratedFrom in payload")
	}
	ws, err := m.Get(wsHex)
	if err != nil || ws == nil {
		return nil, fmt.Errorf("workspace %s not found: %v", wsHex[:12], err)
	}

	// Confirm at least one user-named ref still points at the original
	// target. If none, the world has moved on — advancing now would
	// leak old changes back in front of newer ones.
	refMgr := ref.NewRefManager(m.db)
	refs, err := refMgr.List(nil)
	if err != nil {
		return nil, fmt.Errorf("listing refs: %w", err)
	}
	stillAtTarget := false
	for _, r := range refs {
		if strings.HasPrefix(r.Name, "ws.") {
			continue
		}
		if util.BytesToHex(r.TargetID) == targetHex {
			stillAtTarget = true
			break
		}
	}
	if !stillAtTarget {
		return nil, fmt.Errorf("no team-visible ref still points at the original target %s; "+
			"re-run `kai integrate` to refresh the merge against the current target", targetHex[:12])
	}

	report, err := m.PublishAtTarget(
		ws,
		&IntegrateResult{ResultSnapshot: snap.ID},
		targetID,
		PublishOptions{SkipGate: true},
	)
	if err != nil {
		return nil, err
	}
	return report.AdvancedRefs, nil
}

// RejectHeld marks a held snapshot as dismissed. The snapshot is not
// deleted — keeping it preserves the audit trail and lets the agent's
// later work supersede it organically (a fresh integrate produces a
// new snapshot with a fresh verdict).
func (m *Manager) RejectHeld(snapID []byte) error {
	snap, err := m.db.GetNode(snapID)
	if err != nil {
		return fmt.Errorf("getting snapshot: %w", err)
	}
	if snap == nil {
		return fmt.Errorf("snapshot not found")
	}
	if dismissed, _ := snap.Payload["dismissed"].(bool); dismissed {
		return nil // idempotent — already dismissed
	}
	if snap.Payload == nil {
		snap.Payload = map[string]interface{}{}
	}
	snap.Payload["dismissed"] = true
	snap.Payload["dismissedAt"] = util.NowMs()
	if err := m.db.UpdateNodePayload(snap.ID, snap.Payload); err != nil {
		return fmt.Errorf("marking dismissed: %w", err)
	}
	return nil
}

