package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newStateServer returns a minimal Server stub with kaiDir pointing at a
// temp directory so the sync-state file lives somewhere we can read back.
func newStateServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{kaiDir: dir}
}

func TestSyncState_SaveAndLoad(t *testing.T) {
	s := newStateServer(t)

	if _, ok := s.loadSyncState(); ok {
		t.Fatal("expected no state on a fresh kaiDir")
	}

	s.saveSyncState([]string{"a.go", "b.go"})

	st, ok := s.loadSyncState()
	if !ok {
		t.Fatal("expected state after save")
	}
	if !st.Enabled {
		t.Error("expected Enabled=true")
	}
	if len(st.Files) != 2 || st.Files[0] != "a.go" || st.Files[1] != "b.go" {
		t.Errorf("expected [a.go b.go], got %v", st.Files)
	}
	if st.LastSeq != 0 {
		t.Errorf("expected LastSeq=0 on first save, got %d", st.LastSeq)
	}
}

func TestSyncState_SaveSyncSeqAdvances(t *testing.T) {
	s := newStateServer(t)
	s.saveSyncState([]string{"a.go"})

	s.saveSyncSeq(42)

	st, ok := s.loadSyncState()
	if !ok {
		t.Fatal("expected state")
	}
	if st.LastSeq != 42 {
		t.Errorf("expected LastSeq=42, got %d", st.LastSeq)
	}
	// Files list must be preserved across a seq-only save.
	if len(st.Files) != 1 || st.Files[0] != "a.go" {
		t.Errorf("expected files preserved, got %v", st.Files)
	}
}

func TestSyncState_SaveSyncStatePreservesLastSeq(t *testing.T) {
	s := newStateServer(t)
	s.saveSyncState([]string{"a.go"})
	s.saveSyncSeq(99)

	// A subsequent saveSyncState (e.g. user toggles live_sync on with a new
	// files filter) must not zero the cursor.
	s.saveSyncState([]string{"c.go"})

	st, ok := s.loadSyncState()
	if !ok {
		t.Fatal("expected state")
	}
	if st.LastSeq != 99 {
		t.Errorf("expected LastSeq=99 to survive saveSyncState, got %d", st.LastSeq)
	}
	if len(st.Files) != 1 || st.Files[0] != "c.go" {
		t.Errorf("expected new files [c.go], got %v", st.Files)
	}
}

func TestSyncState_Clear(t *testing.T) {
	s := newStateServer(t)
	s.saveSyncState([]string{"a.go"})
	s.saveSyncSeq(7)

	s.clearSyncState()

	if _, ok := s.loadSyncState(); ok {
		t.Error("expected loadSyncState to fail after clear")
	}
	// File should not exist on disk.
	if _, err := os.Stat(s.syncStatePath()); !os.IsNotExist(err) {
		t.Errorf("expected sync-state.json to be gone, stat err=%v", err)
	}
}

func TestSyncState_FileFormat(t *testing.T) {
	// Lock the on-disk JSON shape so we don't accidentally break compatibility
	// with a sync-state.json written by an older binary on the same disk.
	s := newStateServer(t)
	s.saveSyncState([]string{"x.go"})
	s.saveSyncSeq(13)

	raw, err := os.ReadFile(filepath.Join(s.kaiDir, "sync-state.json"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", got["enabled"])
	}
	if got["last_seq"] != float64(13) {
		t.Errorf("expected last_seq=13, got %v", got["last_seq"])
	}
	files, _ := got["files"].([]interface{})
	if len(files) != 1 || files[0] != "x.go" {
		t.Errorf("expected files=[x.go], got %v", got["files"])
	}
}
