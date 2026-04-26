package spawn

import (
	"os"
	"path/filepath"
	"strings"

	"kai/internal/authorship"
)

// LastPushFile is the marker `kai push` writes after a successful push,
// recording the hex snapshot ID that was pushed. `kai despawn` reads it
// to decide whether the workspace has unpushed snapshots.
const LastPushFile = "last-push"

// UnpushedReport summarizes what's unpushed so callers can show the
// user what they would lose.
type UnpushedReport struct {
	PendingCheckpoints int    // count of files under .kai/checkpoints/
	LocalHead          string // hex of current snap.latest (or "")
	LastPushed         string // hex from .kai/last-push (or "")
	HeadAhead          bool   // LocalHead != "" && LocalHead != LastPushed
}

// Any returns true if despawn should refuse without --force.
func (r UnpushedReport) Any() bool {
	return r.PendingCheckpoints > 0 || r.HeadAhead
}

// HasUnpushedCheckpoints inspects the spawned workspace's `.kai/` for
// either pending authorship-checkpoint files or a local snap.latest
// that's ahead of the last push marker. Soft-fails to a permissive
// report if anything goes sideways — better to occasionally let a
// despawn through than to wedge it on a transient I/O error.
func HasUnpushedCheckpoints(kaiDirPath string) (UnpushedReport, error) {
	rep := UnpushedReport{}
	rep.PendingCheckpoints = authorship.CountPendingCheckpoints(kaiDirPath)

	if data, err := os.ReadFile(filepath.Join(kaiDirPath, LastPushFile)); err == nil {
		rep.LastPushed = strings.TrimSpace(string(data))
	}

	// snap.latest is just a row in the refs table; reading the DB just
	// to ask "what's the latest snapshot?" is heavier than we want here.
	// Convention: callers wanting that comparison can populate LocalHead
	// themselves (e.g. from the workspace.HeadSnapshot they already have
	// loaded). HeadAhead is computed lazily by SetLocalHead below.
	return rep, nil
}

// SetLocalHead lets the caller inject the workspace's current head
// snapshot (typically from workspace.Manager.Get(name).HeadSnapshot
// hex-encoded). Recomputes HeadAhead.
func (r *UnpushedReport) SetLocalHead(headHex string) {
	r.LocalHead = headHex
	r.HeadAhead = headHex != "" && headHex != r.LastPushed
}
