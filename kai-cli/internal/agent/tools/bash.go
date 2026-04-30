package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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

	c := exec.CommandContext(runCtx, "bash", "-c", cmd)
	c.Dir = b.Workspace
	// Combined output keeps stderr-stdout ordering close to what the
	// agent would see in a real terminal session.
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	start := time.Now()
	runErr := c.Run()
	elapsed := time.Since(start)

	maxBytes := b.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxOutputBytes
	}
	out, truncated := truncateOutput(buf.Bytes(), maxBytes)

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
