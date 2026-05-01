package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BashTool runs shell commands inside the agent's workspace and
// captures stdout/stderr. It is intentionally minimal — kai's safety
// gate is the chokepoint that decides whether the agent's overall
// changes promote, so we don't need OpenCode's per-command permission
// prompt here. The optional Allow allowlist is a defense-in-depth
// trip-wire for catastrophically wrong commands (rm -rf /, curl |
// sh, etc.) and is matched on the first whitespace-separated token.
//
// Output is bounded at MaxOutputBytes so a chatty command (npm
// install, go test ./... -v) doesn't blow the model's context. Both
// stdout and stderr are interleaved into one buffer the way the
// model sees it on a real terminal.
type BashTool struct {
	// Workspace is the absolute path the command runs in (cwd).
	// Same value as FileTools.Workspace.
	Workspace string

	// Allow is an optional allowlist of command-name prefixes. When
	// non-empty, the first token of the command must match one of
	// these (e.g. "npm", "go", "git"). Empty allows everything.
	Allow []string

	// DefaultTimeout caps how long a single command can run. 0 picks
	// 60s. The agent can override via the `timeout` parameter in
	// the call (subject to MaxTimeout).
	DefaultTimeout time.Duration

	// MaxOutputBytes caps the captured output length. 0 picks
	// DefaultMaxOutputBytes (30 KiB). Trimmed output gets a
	// "(truncated …)" tail.
	MaxOutputBytes int

	// OnOutput, when set, fires once per line as the command writes
	// to stdout/stderr. Lets the TUI stream progress (brew install
	// progress, npm test scrolling, etc.) inline instead of leaving
	// the user staring at a frozen pane until the command exits.
	// Must be safe for concurrent calls — output may interleave from
	// multiple goroutines reading the two pipes.
	OnOutput func(line string)

	// OnFilesChanged fires once after each command, with the
	// workspace-relative paths of files whose mtime changed during
	// the run. Used by the agent runner to gate bash-driven
	// mutations (cat heredoc → file, sed -i, npm install touching
	// node_modules) the same way write/edit calls are gated.
	// Detection is mtime-based against a pre-run snapshot; the
	// scan skips the usual noisy paths (.git, node_modules, .kai).
	OnFilesChanged func(paths []string)
}

const (
	defaultBashTimeout    = 60 * time.Second
	maxBashTimeout        = 10 * time.Minute
	defaultMaxOutputBytes = 30000
)

type bashParams struct {
	Command string `json:"command"`
	// Timeout is in seconds; clamped to [1, 600].
	Timeout int `json:"timeout"`
}

// Info returns the tool descriptor for the LLM. Description is
// careful to call out:
//   - allowed shell features (no interactive prompts; one-shot only)
//   - what's filtered (allowlist if configured)
//   - timeout semantics
//   - output truncation
//
// so the agent doesn't waste turns on unsupported usage.
func (b *BashTool) Info() ToolInfo {
	desc := "Run a shell command in the workspace and return stdout+stderr. " +
		"Commands run non-interactively under `bash -c`. " +
		"Output is capped at ~30 KB; long commands should redirect to a file " +
		"and view it with `view`."
	if len(b.Allow) > 0 {
		desc += " Only commands beginning with one of these names are permitted: " +
			strings.Join(b.Allow, ", ") + "."
	}
	return ToolInfo{
		Name:        "bash",
		Description: desc,
		Parameters: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute. Use `&&` / `||` / pipelines as normal.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in seconds (1–600). Default 60.",
				"default":     60,
			},
		},
		Required: []string{"command"},
	}
}

// Run executes the command, enforcing the allowlist + timeout, and
// returns truncated combined output as a text response.
func (b *BashTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p bashParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("bash: invalid input json: " + err.Error()), nil
	}
	cmd := strings.TrimSpace(p.Command)
	if cmd == "" {
		return NewTextErrorResponse("bash: command required"), nil
	}
	if b.Workspace == "" {
		return NewTextErrorResponse("bash: workspace not set"), nil
	}

	if reason := b.checkAllow(cmd); reason != "" {
		return NewTextErrorResponse("bash: " + reason), nil
	}

	timeout := b.DefaultTimeout
	if timeout <= 0 {
		timeout = defaultBashTimeout
	}
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Second
	}
	if timeout > maxBashTimeout {
		timeout = maxBashTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Pre-run mtime snapshot so we can identify files the command
	// touched. Cheap (stat only), bounded by the ignore filter to
	// the workspace's actually-interesting files — skips .git,
	// node_modules, .kai, vendor.
	preSnap := snapshotMtimes(b.Workspace)

	c := exec.CommandContext(runCtx, "bash", "-c", cmd)
	c.Dir = b.Workspace
	// Stdin from /dev/null: agents run non-interactively, so any
	// command that prompts for input (brew install confirmations,
	// `read`, ssh password prompts) would otherwise block until the
	// timeout fires. Closing stdin makes the prompting program see
	// EOF immediately and either fail loudly or proceed with its
	// non-interactive default — both better than a silent hang.
	if devnull, err := os.Open(os.DevNull); err == nil {
		c.Stdin = devnull
		defer devnull.Close()
	}
	// Tell common tools they're not on a TTY. Belt-and-suspenders:
	// brew honors NONINTERACTIVE; many CLIs check CI=true.
	c.Env = append(os.Environ(),
		"NONINTERACTIVE=1",
		"CI=1",
		"DEBIAN_FRONTEND=noninteractive",
		"HOMEBREW_NO_AUTO_UPDATE=1",
	)

	// Capture for the model's tool result AND tee each line to the
	// streaming hook so the user sees live progress in the TUI.
	var buf bytes.Buffer
	maxBytes := b.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputBytes
	}
	bufWriter := newCappedBuffer(&buf, maxBytes)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return NewTextErrorResponse("bash: stdout pipe: " + err.Error()), nil
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return NewTextErrorResponse("bash: stderr pipe: " + err.Error()), nil
	}
	start := time.Now()
	if err := c.Start(); err != nil {
		return NewTextErrorResponse("bash: start: " + err.Error()), nil
	}

	// Two scanners running concurrently, one per pipe. Each line is
	// written to the capped buffer (for the tool result) and to the
	// OnOutput hook (for live TUI display). A mutex protects the
	// shared buffer; the hook is invoked outside the lock so a slow
	// renderer doesn't serialize the readers.
	var mu sync.Mutex
	var wg sync.WaitGroup
	stream := func(r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64*1024), 1024*1024)
		for s.Scan() {
			line := s.Text()
			mu.Lock()
			bufWriter.WriteString(line)
			bufWriter.WriteByte('\n')
			mu.Unlock()
			if b.OnOutput != nil {
				b.OnOutput(line)
			}
		}
	}
	wg.Add(2)
	go stream(stdout)
	go stream(stderr)
	runErr := c.Wait()
	wg.Wait()
	elapsed := time.Since(start)

	// Post-run snapshot + diff. Fires the OnFilesChanged hook with
	// any path whose mtime advanced or that newly appeared. Skipped
	// when no hook is registered (saves the second walk).
	if b.OnFilesChanged != nil {
		if changed := diffMtimeSnapshots(preSnap, snapshotMtimes(b.Workspace)); len(changed) > 0 {
			b.OnFilesChanged(changed)
		}
	}

	out := buf.Bytes()
	truncated := bufWriter.truncated
	if truncated {
		out = append(out, []byte(fmt.Sprintf("\n…(truncated; output exceeded %d bytes)\n", maxBytes))...)
	}

	exitCode := 0
	if runErr != nil {
		// ExitError carries the real exit code; everything else
		// (timeout, missing binary, etc.) we surface as an error
		// response with code -1 so the model can react.
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	header := fmt.Sprintf("$ %s\n(exit=%d, %s",
		oneLine(cmd), exitCode, elapsed.Round(time.Millisecond))
	if truncated {
		header += ", output truncated"
	}
	header += ")\n"

	resp := NewTextResponse(header + string(out))
	if exitCode != 0 {
		resp.IsError = true
	}
	return resp, nil
}

// checkAllow returns "" when the command passes the allowlist (or the
// allowlist is empty). Otherwise returns a reason string.
func (b *BashTool) checkAllow(cmd string) string {
	if len(b.Allow) == 0 {
		return ""
	}
	first := firstCommandToken(cmd)
	for _, a := range b.Allow {
		if first == a {
			return ""
		}
	}
	return fmt.Sprintf("command %q not in allowlist (%s); ask the human to run it manually or extend `agent.bash_allow` in .kai/config.yaml",
		first, strings.Join(b.Allow, ", "))
}

// firstCommandToken returns the first whitespace-separated word of
// the command, after stripping any leading env-var assignments
// (`FOO=bar npm test` → `npm`). Crude — doesn't handle pipelines or
// `&&` chains specially; the allowlist is a first-token guard, not a
// full sandbox.
func firstCommandToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for _, tok := range strings.Fields(cmd) {
		if strings.Contains(tok, "=") {
			continue // env-var assignment, skip
		}
		// Strip leading `./` so `./scripts/build` allowlists as `scripts/build`
		// (fine for our purposes — the allowlist is best-effort).
		return strings.TrimPrefix(tok, "./")
	}
	return ""
}

// bashIgnoreDirs is the set of directory names the mtime walker
// skips when snapshotting the workspace. These are the heavyweight
// areas where a single bash command can touch tens of thousands of
// files (npm install in node_modules, git operations) — walking
// them on every bash call would be prohibitive. Source files at
// arbitrary depth above these still get tracked.
var bashIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".kai":         true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
	"target":       true,
	"dist":         true,
	"build":        true,
}

// snapshotMtimes walks the workspace returning a map of relative
// path → modification timestamp (UnixNano). Returns nil on error so
// the caller's diff sees an empty pre-snapshot and reports every
// file as "changed" — degraded but not broken.
//
// The walker bails on any single entry's error (Lstat failure on a
// permission-denied file, etc.) rather than failing the whole
// snapshot; one un-stat-able file shouldn't poison the rest.
func snapshotMtimes(workspace string) map[string]int64 {
	if workspace == "" {
		return nil
	}
	out := make(map[string]int64, 256)
	_ = filepath.WalkDir(workspace, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip this entry, keep walking
		}
		name := d.Name()
		if d.IsDir() {
			if bashIgnoreDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip hidden files and symlinks — both add noise; mtime
		// doesn't always reflect symlink target changes anyway.
		if strings.HasPrefix(name, ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(workspace, p)
		if err != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = info.ModTime().UnixNano()
		return nil
	})
	return out
}

// diffMtimeSnapshots returns the workspace-relative paths whose
// mtime advanced (or that appeared anew) between pre and post.
// Sorted so output ordering is stable across runs — callers that
// log the list will see deterministic output even when the
// underlying map iteration is randomized.
func diffMtimeSnapshots(pre, post map[string]int64) []string {
	if len(post) == 0 {
		return nil
	}
	var changed []string
	for path, t := range post {
		if priorT, ok := pre[path]; !ok || t > priorT {
			changed = append(changed, path)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// Stable order — paths sort lexicographically for readable
	// downstream output.
	sortStrings(changed)
	return changed
}

// sortStrings is a tiny adapter so we don't pull "sort" into a
// file that doesn't otherwise need it. Stable-enough for our
// purposes (paths are unique, so stability is moot).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// cappedBuffer wraps a *bytes.Buffer and stops writing once it has
// reached the cap. Used by the streaming bash runner so the buffer
// fed back to the model can't grow without bound on a chatty
// command (npm install in a fresh repo can dump megabytes); the
// streaming hook still sees every line.
type cappedBuffer struct {
	buf       *bytes.Buffer
	cap       int
	truncated bool
}

func newCappedBuffer(buf *bytes.Buffer, cap int) *cappedBuffer {
	return &cappedBuffer{buf: buf, cap: cap}
}

func (c *cappedBuffer) WriteString(s string) {
	if c.buf.Len() >= c.cap {
		c.truncated = true
		return
	}
	remaining := c.cap - c.buf.Len()
	if len(s) > remaining {
		s = s[:remaining]
		c.truncated = true
	}
	c.buf.WriteString(s)
}

func (c *cappedBuffer) WriteByte(b byte) {
	if c.buf.Len() >= c.cap {
		c.truncated = true
		return
	}
	c.buf.WriteByte(b)
}

// truncateOutput keeps the first maxBytes of output and appends a
// truncation marker if anything was dropped. Returning the head
// rather than the tail matches what `head -c` would do — most
// command failures surface near the start (configuration errors,
// missing dependencies); long tails are usually noise.
func truncateOutput(b []byte, maxBytes int) ([]byte, bool) {
	if len(b) <= maxBytes {
		return b, false
	}
	out := make([]byte, 0, maxBytes+64)
	out = append(out, b[:maxBytes]...)
	out = append(out, []byte(fmt.Sprintf("\n…(truncated; output exceeded %d bytes)\n", maxBytes))...)
	return out, true
}

// oneLine collapses multi-line commands into a single line for the
// echo-back header. Keeps the response readable when the agent
// pastes a heredoc or backslash-continued command.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}
