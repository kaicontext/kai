package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBash_RunsSuccessfulCommand: minimal happy path — `echo hi` runs,
// captures stdout, exit 0.
func TestBash_RunsSuccessfulCommand(t *testing.T) {
	requireBash(t)
	ws := t.TempDir()
	tool := &BashTool{Workspace: ws}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"echo hi"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "hi") {
		t.Errorf("output missing 'hi': %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "exit=0") {
		t.Errorf("expected exit=0 in header: %q", resp.Content)
	}
}

// TestBash_NonzeroExitMarksError: bash returns 2; tool reports
// IsError so the agent loop can react.
func TestBash_NonzeroExitMarksError(t *testing.T) {
	requireBash(t)
	tool := &BashTool{Workspace: t.TempDir()}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"exit 2"}`,
	})
	if !resp.IsError {
		t.Errorf("expected IsError for exit 2, got: %+v", resp)
	}
	if !strings.Contains(resp.Content, "exit=2") {
		t.Errorf("header should show exit=2: %q", resp.Content)
	}
}

// TestBash_RunsInWorkspace: pwd should print the workspace dir, not
// the test process's cwd.
func TestBash_RunsInWorkspace(t *testing.T) {
	requireBash(t)
	ws := t.TempDir()
	tool := &BashTool{Workspace: ws}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"pwd"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	// macOS sometimes returns /private prefix on tempdirs.
	if !strings.Contains(resp.Content, ws) && !strings.Contains(resp.Content, "/private"+ws) {
		t.Errorf("pwd didn't run in workspace: %q (ws=%s)", resp.Content, ws)
	}
}

func TestBash_EmptyCommandRejected(t *testing.T) {
	tool := &BashTool{Workspace: t.TempDir()}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"  "}`,
	})
	if !resp.IsError || !strings.Contains(resp.Content, "command required") {
		t.Errorf("expected 'command required' error, got: %+v", resp)
	}
}

func TestBash_AllowlistRejectsUnlisted(t *testing.T) {
	tool := &BashTool{Workspace: t.TempDir(), Allow: []string{"echo", "ls"}}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"rm -rf /"}`,
	})
	if !resp.IsError || !strings.Contains(resp.Content, "not in allowlist") {
		t.Errorf("expected allowlist rejection, got: %+v", resp)
	}
}

func TestBash_AllowlistAcceptsListed(t *testing.T) {
	requireBash(t)
	tool := &BashTool{Workspace: t.TempDir(), Allow: []string{"echo"}}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"echo ok"}`,
	})
	if resp.IsError {
		t.Errorf("expected success for listed command, got: %s", resp.Content)
	}
}

func TestBash_AllowlistSkipsEnvAssignments(t *testing.T) {
	requireBash(t)
	tool := &BashTool{Workspace: t.TempDir(), Allow: []string{"echo"}}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"FOO=bar echo ok"}`,
	})
	if resp.IsError {
		t.Errorf("env-assignment prefix should be skipped for allowlist: %s", resp.Content)
	}
}

// TestBash_OutputTruncated: write more bytes than MaxOutputBytes;
// expect a "(truncated…)" tail and the tail bytes dropped.
func TestBash_OutputTruncated(t *testing.T) {
	requireBash(t)
	ws := t.TempDir()
	tool := &BashTool{Workspace: ws, MaxOutputBytes: 100}
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"yes hello | head -c 5000"}`,
	})
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "truncated") {
		t.Errorf("output should be truncated: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "output truncated") {
		t.Errorf("header should note truncation: %q", resp.Content)
	}
}

// TestBash_TimeoutFromParam: the agent supplies a 1s timeout, sleep
// 5 should be killed.
func TestBash_TimeoutFromParam(t *testing.T) {
	requireBash(t)
	tool := &BashTool{Workspace: t.TempDir()}
	start := time.Now()
	resp, _ := tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"sleep 5","timeout":1}`,
	})
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("timeout didn't fire — took %v", elapsed)
	}
	if !resp.IsError {
		t.Errorf("expected error response on timeout: %+v", resp)
	}
}

// TestBash_FilesEditedDuringRun: a bash command that writes a file
// produces the file in the workspace. Confirms cwd plumbing.
func TestBash_FilesEditedDuringRun(t *testing.T) {
	requireBash(t)
	ws := t.TempDir()
	tool := &BashTool{Workspace: ws}
	_, _ = tool.Run(context.Background(), ToolCall{
		Name:  "bash",
		Input: `{"command":"echo content > out.txt"}`,
	})
	body, err := os.ReadFile(filepath.Join(ws, "out.txt"))
	if err != nil {
		t.Fatalf("expected out.txt in workspace: %v", err)
	}
	if !strings.HasPrefix(string(body), "content") {
		t.Errorf("unexpected body: %q", string(body))
	}
}

func TestFirstCommandToken(t *testing.T) {
	cases := map[string]string{
		"npm test":              "npm",
		"  go build ./...":      "go",
		"FOO=bar npm install":   "npm",
		"./scripts/build":       "scripts/build",
		"":                      "",
	}
	for in, want := range cases {
		if got := firstCommandToken(in); got != want {
			t.Errorf("firstCommandToken(%q) = %q, want %q", in, got, want)
		}
	}
}

// requireBash skips the test if /bin/bash isn't available (Windows
// CI). All bash tests use real bash for fidelity; mocking would
// defeat the purpose.
func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash tool tests skip on Windows")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("/bin/bash unavailable: %v", err)
	}
}
