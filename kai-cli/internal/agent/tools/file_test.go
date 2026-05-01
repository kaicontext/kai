package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestView_ReadsFileWithLineNumbers(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := (&FileTools{Workspace: ws}).View()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "view",
		Input: `{"file_path":"hello.txt"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	for _, want := range []string{"1: alpha", "2: beta", "3: gamma"} {
		if !strings.Contains(resp.Content, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, resp.Content)
		}
	}
}

func TestView_FileNotFound(t *testing.T) {
	tool := (&FileTools{Workspace: t.TempDir()}).View()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "view",
		Input: `{"file_path":"missing.txt"}`,
	})
	if !resp.IsError || !strings.Contains(resp.Content, "not found") {
		t.Errorf("expected file-not-found error, got: %+v", resp)
	}
}

func TestWrite_CreatesFileAndFiresHook(t *testing.T) {
	ws := t.TempDir()
	var hookPath, hookOp string
	hookCalled := 0
	tool := (&FileTools{
		Workspace: ws,
		OnChange: func(p, op string) {
			hookCalled++
			hookPath, hookOp = p, op
		},
	}).Write()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "write",
		Input: `{"file_path":"sub/dir/new.txt","content":"hi"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	body, err := os.ReadFile(filepath.Join(ws, "sub", "dir", "new.txt"))
	if err != nil || string(body) != "hi" {
		t.Errorf("file not written correctly: err=%v body=%q", err, body)
	}
	if hookCalled != 1 || hookPath != "sub/dir/new.txt" || hookOp != "created" {
		t.Errorf("hook misfired: calls=%d path=%q op=%q", hookCalled, hookPath, hookOp)
	}
}

func TestWrite_OverwriteFiresModified(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var op string
	tool := (&FileTools{
		Workspace: ws,
		OnChange:  func(_, o string) { op = o },
	}).Write()
	tool.Run(context.Background(), ToolCall{
		Name:  "write",
		Input: `{"file_path":"x.txt","content":"new"}`,
	})
	if op != "modified" {
		t.Errorf("expected 'modified', got %q", op)
	}
}

func TestEdit_ReplaceUniqueMatch(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := (&FileTools{Workspace: ws}).Edit()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "edit",
		Input: `{"file_path":"x.txt","old_string":"world","new_string":"there"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	body, _ := os.ReadFile(filepath.Join(ws, "x.txt"))
	if string(body) != "hello there" {
		t.Errorf("edit failed: %q", string(body))
	}
}

func TestEdit_RefusesAmbiguousMatch(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("a a a"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := (&FileTools{Workspace: ws}).Edit()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "edit",
		Input: `{"file_path":"x.txt","old_string":"a","new_string":"b"}`,
	})
	if !resp.IsError || !strings.Contains(resp.Content, "appears 3 times") {
		t.Errorf("expected ambiguous-match error, got: %+v", resp)
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("a a a"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := (&FileTools{Workspace: ws}).Edit()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "edit",
		Input: `{"file_path":"x.txt","old_string":"a","new_string":"b","replace_all":true}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	body, _ := os.ReadFile(filepath.Join(ws, "x.txt"))
	if string(body) != "b b b" {
		t.Errorf("replace_all failed: %q", string(body))
	}
}

// TestFileTools_ReadOnlyOmitsWriteAndEdit: ReadOnly mode should
// register only the view tool. Used by the chat-fallback path so
// "what's in this dir" answers can run view+bash without the
// possibility of an accidental write or edit.
func TestFileTools_ReadOnlyOmitsWriteAndEdit(t *testing.T) {
	rw := (&FileTools{Workspace: t.TempDir(), ReadOnly: false}).All()
	if len(rw) != 3 {
		t.Errorf("read-write should expose 3 tools, got %d", len(rw))
	}

	ro := (&FileTools{Workspace: t.TempDir(), ReadOnly: true}).All()
	if len(ro) != 1 {
		t.Fatalf("read-only should expose 1 tool, got %d", len(ro))
	}
	if ro[0].Info().Name != "view" {
		t.Errorf("read-only should expose view, got %q", ro[0].Info().Name)
	}
}

func TestResolveInWorkspace_RejectsEscape(t *testing.T) {
	_, err := resolveInWorkspace("/tmp/work", "../../../etc/passwd")
	if err == nil {
		t.Error("expected escape error")
	}
}

func TestResolveInWorkspace_AbsoluteOutsideRefused(t *testing.T) {
	_, err := resolveInWorkspace("/tmp/work", "/etc/passwd")
	if err == nil {
		t.Error("expected escape error for absolute path outside workspace")
	}
}

// TestWrite_FiresBroadcast verifies the live-sync hook fires after a
// successful write with the right digest + base64 payload. The
// orchestrator wires this to remote.SyncPushFile in production.
func TestWrite_FiresBroadcast(t *testing.T) {
	ws := t.TempDir()
	var got struct {
		path, digest, b64 string
	}
	calls := 0
	tool := (&FileTools{
		Workspace: ws,
		OnBroadcast: func(p, d, b string) {
			calls++
			got.path, got.digest, got.b64 = p, d, b
		},
	}).Write()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "write",
		Input: `{"file_path":"x.txt","content":"hello"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	if calls != 1 {
		t.Errorf("expected 1 broadcast, got %d", calls)
	}
	if got.path != "x.txt" {
		t.Errorf("path: %q", got.path)
	}
	// Digest is hex sha256 of "hello"
	if got.digest != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("digest mismatch: %s", got.digest)
	}
	// Base64 of "hello" is aGVsbG8=
	if got.b64 != "aGVsbG8=" {
		t.Errorf("base64 mismatch: %q", got.b64)
	}
}

// TestEdit_FiresBroadcast: same shape, but for the edit path. The
// digest must reflect the post-edit content, not the original.
func TestEdit_FiresBroadcast(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotB64 string
	tool := (&FileTools{
		Workspace:   ws,
		OnBroadcast: func(_, _, b string) { gotB64 = b },
	}).Edit()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "edit",
		Input: `{"file_path":"x.txt","old_string":"world","new_string":"there"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	// Base64 of "hello there" is aGVsbG8gdGhlcmU=
	if gotB64 != "aGVsbG8gdGhlcmU=" {
		t.Errorf("post-edit base64 wrong: %q", gotB64)
	}
}

func TestResolveInWorkspace_RelativeInside(t *testing.T) {
	abs, err := resolveInWorkspace("/tmp/work", "subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(abs, "/tmp/work/subdir/file.txt") {
		t.Errorf("unexpected resolved path: %s", abs)
	}
}
