package mcp

import (
	"testing"

	"kai/internal/authorship"
)

// peerCheckpointServer returns a Server stub with a real CheckpointWriter
// pointed at a temp kaiDir, so writePeerCheckpoint can be tested end-to-end
// against actual files on disk.
func peerCheckpointServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		kaiDir:   dir,
		cpWriter: authorship.NewCheckpointWriter(dir, "test-session"),
	}
}

func TestWritePeerCheckpoint_FreshFileWritesWholeRange(t *testing.T) {
	s := peerCheckpointServer(t)

	new := []byte("line one\nline two\nline three\n")
	s.writePeerCheckpoint("src/foo.go", nil, new, "claude-code", "modify")

	cps, err := authorship.ReadPendingCheckpoints(s.kaiDir)
	if err != nil {
		t.Fatalf("read checkpoints: %v", err)
	}
	if len(cps) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(cps))
	}
	cp := cps[0]
	if cp.Agent != "claude-code" {
		t.Errorf("expected agent=claude-code, got %q", cp.Agent)
	}
	if cp.AuthorType != "ai" {
		t.Errorf("expected author_type=ai, got %q", cp.AuthorType)
	}
	if !cp.PeerOrigin {
		t.Error("expected PeerOrigin=true")
	}
	if cp.Action != "modify" {
		t.Errorf("expected action=modify, got %q", cp.Action)
	}
	if cp.StartLine != 1 || cp.EndLine != 4 {
		t.Errorf("expected lines 1-4 (whole 3-line file + trailing newline), got %d-%d", cp.StartLine, cp.EndLine)
	}
}

func TestWritePeerCheckpoint_OnlyDiffRangeAttributed(t *testing.T) {
	s := peerCheckpointServer(t)

	old := []byte("a\nb\nc\nd\ne\n")
	new := []byte("a\nb\nX\nY\ne\n") // lines 3 and 4 changed
	s.writePeerCheckpoint("src/foo.go", old, new, "cursor", "modify")

	cps, _ := authorship.ReadPendingCheckpoints(s.kaiDir)
	if len(cps) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(cps))
	}
	cp := cps[0]
	if cp.StartLine != 3 || cp.EndLine != 4 {
		t.Errorf("expected lines 3-4 (only the diff), got %d-%d", cp.StartLine, cp.EndLine)
	}
	if cp.Agent != "cursor" {
		t.Errorf("expected agent=cursor, got %q", cp.Agent)
	}
}

func TestWritePeerCheckpoint_NoChangeWritesNothing(t *testing.T) {
	s := peerCheckpointServer(t)

	same := []byte("identical\nfile\n")
	s.writePeerCheckpoint("src/foo.go", same, same, "claude-code", "modify")

	cps, _ := authorship.ReadPendingCheckpoints(s.kaiDir)
	if len(cps) != 0 {
		t.Errorf("expected 0 checkpoints when content unchanged, got %d", len(cps))
	}
}

func TestWritePeerCheckpoint_EmptyAgentSkipped(t *testing.T) {
	s := peerCheckpointServer(t)

	s.writePeerCheckpoint("src/foo.go", nil, []byte("hi\n"), "", "modify")

	cps, _ := authorship.ReadPendingCheckpoints(s.kaiDir)
	if len(cps) != 0 {
		t.Errorf("expected no checkpoint when agent is empty, got %d", len(cps))
	}
}

func TestWritePeerCheckpoint_NilWriterSkipped(t *testing.T) {
	// A Server without a cpWriter must not panic — startup ordering can
	// produce this state if a sync receive races with writer init.
	s := &Server{kaiDir: t.TempDir(), cpWriter: nil}
	s.writePeerCheckpoint("src/foo.go", nil, []byte("hi\n"), "claude-code", "modify")
	// no panic = pass
}

func TestWritePeerCheckpoint_ConflictAction(t *testing.T) {
	s := peerCheckpointServer(t)

	// Conflict path: caller passes (local, incoming) so the recorded ranges
	// describe the lines the peer *would have changed* in our view.
	local := []byte("a\nb\nc\n")
	incoming := []byte("a\nZ\nc\n")
	s.writePeerCheckpoint("src/foo.go", local, incoming, "claude-code", "conflict")

	cps, _ := authorship.ReadPendingCheckpoints(s.kaiDir)
	if len(cps) != 1 {
		t.Fatalf("expected 1 conflict checkpoint, got %d", len(cps))
	}
	cp := cps[0]
	if cp.Action != "conflict" {
		t.Errorf("expected action=conflict, got %q", cp.Action)
	}
	if cp.StartLine != 2 || cp.EndLine != 2 {
		t.Errorf("expected line 2 only, got %d-%d", cp.StartLine, cp.EndLine)
	}
	if !cp.PeerOrigin {
		t.Error("expected PeerOrigin=true on conflict")
	}
}
