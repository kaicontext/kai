package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"kai/internal/remote"
)

// liveSyncWiring is what setupLiveSync produces: a broadcast hook the
// orchestrator passes to the in-process agent, plus a stop function
// to release the kailab subscription on TUI exit.
type liveSyncWiring struct {
	// Broadcast forwards one file change to the kailab live-sync
	// channel. Best-effort — failures don't propagate; the TUI's
	// sync pane still shows the activity from the agent's local hook.
	Broadcast func(relPath, digest, contentBase64 string)
	// Stop releases the subscription. Safe to call multiple times.
	Stop func()
}

// setupLiveSync configures live-sync broadcasting for an in-process
// agent run. Behavior:
//
//   - If `<kaiDir>/sync-state.json` doesn't exist or has Enabled=false,
//     returns (nil, nil) — live sync is just disabled, not an error.
//     User runs `kai live on` to enable it.
//   - If a remote isn't configured or auth is missing, returns
//     (nil, err) so the caller surfaces a clear message about why
//     live sync isn't going to work.
//   - On success, returns a wiring with Broadcast + Stop.
//
// Subscription is one-shot per kai-code session: we register a channel
// when the TUI starts and tear it down on exit. Channel agent name is
// `kai-code:<pid>` so multiple kai-code sessions don't collide.
func setupLiveSync(kaiDir string) (*liveSyncWiring, error) {
	state, ok := readLiveSyncState(kaiDir)
	if !ok || !state.Enabled {
		return nil, nil
	}

	client, err := remote.NewClientForRemote("origin")
	if err != nil {
		return nil, fmt.Errorf("live sync: no `origin` remote configured (`kai remote set origin <url>`): %w", err)
	}
	if client.AuthToken == "" {
		return nil, fmt.Errorf("live sync: not logged in (`kai auth login`)")
	}

	agent := fmt.Sprintf("kai-code:%d", os.Getpid())
	resp, err := client.SubscribeSync(agent, client.Actor, state.Files)
	if err != nil {
		return nil, fmt.Errorf("live sync: subscribe failed: %w", err)
	}
	channelID := resp.ChannelID

	stopped := false
	return &liveSyncWiring{
		Broadcast: func(rel, digest, b64 string) {
			// Push errors are intentionally swallowed: a one-off
			// network blip shouldn't surface as a tool failure to
			// the agent. The local OnFileChange hook already gave
			// the user immediate visibility.
			_ = client.SyncPushFile(agent, channelID, rel, digest, b64)
		},
		Stop: func() {
			if stopped {
				return
			}
			stopped = true
			_ = client.UnsubscribeSync(channelID)
		},
	}, nil
}

// orchLiveSync converts a liveSyncWiring (or nil) into the
// `func(...)` shape orchestrator.Config.LiveSync expects. Returns
// nil when wiring is nil so the orchestrator's nil-check at the
// hook site routes the agent's file writes only to the local
// OnFileChange callback (no broadcast attempted).
func orchLiveSync(w *liveSyncWiring) func(string, string, string) {
	if w == nil {
		return nil
	}
	return w.Broadcast
}

// readLiveSyncState reads `<kaiDir>/sync-state.json`. Mirrors
// `liveSyncState` defined in main.go (which I can't reach from here
// without circular import gymnastics in cmd/kai). Returns ok=false on
// any read or parse error so callers can simply skip live sync
// without worrying about whether the file is missing vs malformed.
func readLiveSyncState(kaiDir string) (*liveSyncState, bool) {
	data, err := os.ReadFile(filepath.Join(kaiDir, "sync-state.json"))
	if err != nil {
		return nil, false
	}
	var st liveSyncState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, false
	}
	return &st, true
}
