package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// diffContext is the number of unchanged lines emitted around each
// changed region, mirroring `diff -u`'s default. Big enough to
// orient the reader; small enough that a one-line edit doesn't
// flood the REPL with the whole file.
const diffContext = 3

// diffLine is one record in the flattened diff: marker is '+', '-',
// or ' '; text is the original line content (line-terminated).
// oldNum / newNum are 1-indexed positions in the source files (0
// means "doesn't exist on that side").
type diffLine struct {
	marker  byte
	text    string
	oldNum  int
	newNum  int
}

// unifiedDiff renders a hunked unified diff between old and new
// content. Only the changed lines plus `diffContext` lines of
// surrounding context appear; large unchanged regions between
// hunks are skipped and rendered as a "@@" separator the TUI shows
// as a visual break.
//
// File creation (oldContent == "") still emits every new line as
// "+", since by definition every line is new context.
func unifiedDiff(relPath, oldContent, newContent string) (patch string, added, removed int) {
	if oldContent == newContent {
		return "", 0, 0
	}
	dmp := diffmatchpatch.New()
	chrA, chrB, lines := dmp.DiffLinesToChars(oldContent, newContent)
	diffs := dmp.DiffMain(chrA, chrB, false)
	diffs = dmp.DiffCharsToLines(diffs, lines)

	all := make([]diffLine, 0, len(diffs))
	oldNum, newNum := 1, 1
	for _, d := range diffs {
		var marker byte
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			marker = '+'
		case diffmatchpatch.DiffDelete:
			marker = '-'
		default:
			marker = ' '
		}
		for _, line := range splitKeepNewlines(d.Text) {
			rec := diffLine{marker: marker, text: line}
			switch marker {
			case '+':
				rec.newNum = newNum
				newNum++
				added++
			case '-':
				rec.oldNum = oldNum
				oldNum++
				removed++
			default:
				rec.oldNum = oldNum
				rec.newNum = newNum
				oldNum++
				newNum++
			}
			all = append(all, rec)
		}
	}

	// Build hunk ranges: indices [start,end] inclusive that should
	// appear in the patch. Walk all lines; for each change, mark a
	// window of [change-context, change+context]. Merge overlapping
	// windows so adjacent edits don't duplicate context.
	type rng struct{ start, end int }
	var ranges []rng
	for i, ln := range all {
		if ln.marker == ' ' {
			continue
		}
		s := i - diffContext
		if s < 0 {
			s = 0
		}
		e := i + diffContext
		if e >= len(all) {
			e = len(all) - 1
		}
		if len(ranges) > 0 && s <= ranges[len(ranges)-1].end+1 {
			ranges[len(ranges)-1].end = e
		} else {
			ranges = append(ranges, rng{start: s, end: e})
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", relPath, relPath)
	for hi, r := range ranges {
		if hi > 0 {
			b.WriteString("@@\n")
		}
		for i := r.start; i <= r.end; i++ {
			// Format: "<lineNum>\x1f<marker><text>" — 0x1f
			// (Information Separator One) splits the metadata from
			// the content unambiguously, regardless of leading
			// whitespace in source lines. The renderer splits on
			// the first 0x1f. For deletes we show the old-file
			// line number (since the line is gone from the new
			// file); for adds and context, the new-file number.
			ln := all[i].newNum
			if all[i].marker == '-' {
				ln = all[i].oldNum
			}
			fmt.Fprintf(&b, "%d\x1f%c%s", ln, all[i].marker, all[i].text)
			if !strings.HasSuffix(all[i].text, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	return b.String(), added, removed
}

// splitKeepNewlines splits on "\n" but keeps the trailing newline on
// each segment so the unified-diff renderer doesn't double up.
func splitKeepNewlines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	// SplitAfter leaves an empty trailing element when s ends with
	// "\n"; strip it so we don't emit a phantom blank line.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// contentDigest returns the hex-encoded sha256 of the given content.
// Used by the live-sync broadcast hook so the receiver can dedupe
// quickly without rehashing.
func contentDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// encodeBase64 wraps content for the live-sync wire format.
// kailab's SyncPushFile expects standard base64 (not URL-safe).
func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// FileTools constructs the view/write/edit tools scoped to a single
// workspace directory. Two hooks fire after each successful write or
// edit:
//
//   - OnChange:    relPath + op ("created" / "modified" / "deleted")
//                  for visibility (TUI sync pane).
//   - OnBroadcast: relPath + digest + base64-content for live-sync
//                  forwarding to kailab. Optional — leave nil to skip.
//
// Both hooks are best-effort and fire synchronously; receivers must
// not block the agent's loop.
//
// Workspace must be an absolute path. All file operations resolve
// relative to it; absolute paths from the model are checked to be
// inside Workspace before anything is read or written.
type FileTools struct {
	Workspace   string
	OnChange    func(relPath, op string)
	OnBroadcast func(relPath, digest, contentBase64 string)
	// OnDiff fires after each successful write or edit with a unified
	// diff of the change. The TUI uses it to render an inline
	// "Update(path) — Added N lines" entry like Claude Code's. addedLines
	// / removedLines are pre-computed so consumers don't reparse.
	// Optional — leave nil to skip.
	OnDiff func(relPath, op, unifiedDiff string, addedLines, removedLines int)
	// ReadOnly omits write and edit from the tool set. Used by the
	// chat fallback in the REPL so a quick "what files are here?"
	// query can call view and bash without risking accidental
	// modifications to the user's actual repo.
	ReadOnly bool
}

// View returns the read-only file viewer.
func (f *FileTools) View() BaseTool { return &viewTool{ws: f.Workspace} }

// Write returns the file-create / overwrite tool.
func (f *FileTools) Write() BaseTool {
	return &writeTool{ws: f.Workspace, onChange: f.OnChange, onBroadcast: f.OnBroadcast, onDiff: f.OnDiff}
}

// Edit returns the patch-style editor.
func (f *FileTools) Edit() BaseTool {
	return &editTool{ws: f.Workspace, onChange: f.OnChange, onBroadcast: f.OnBroadcast, onDiff: f.OnDiff}
}

// All returns the available file tools. ReadOnly mode returns only
// the view tool — write and edit are intentionally absent so an
// agent in chat-fallback mode can't mutate files even if asked.
func (f *FileTools) All() []BaseTool {
	if f.ReadOnly {
		return []BaseTool{f.View()}
	}
	return []BaseTool{f.View(), f.Write(), f.Edit()}
}

// --- view ------------------------------------------------------------

type viewTool struct {
	ws string
}

type viewParams struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (v *viewTool) Info() ToolInfo {
	return ToolInfo{
		Name: "view",
		Description: "Read the contents of a file in the workspace. " +
			"Use offset/limit to page through large files. " +
			"Returns the file with line numbers prefixed (1: first line, 2: second, …).",
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path relative to the workspace root, or an absolute path inside the workspace.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Zero-indexed line offset to start from. Default 0.",
				"default":     0,
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max lines to return. Default 2000; cap 20000.",
				"default":     2000,
			},
		},
		Required: []string{"file_path"},
	}
}

func (v *viewTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p viewParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("view: invalid input json: " + err.Error()), nil
	}
	abs, err := resolveInWorkspace(v.ws, p.FilePath)
	if err != nil {
		return NewTextErrorResponse("view: " + err.Error()), nil
	}
	if p.Limit <= 0 {
		p.Limit = 2000
	}
	if p.Limit > 20000 {
		p.Limit = 20000
	}

	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return NewTextErrorResponse("view: file not found: " + p.FilePath), nil
		}
		return NewTextErrorResponse("view: open: " + err.Error()), nil
	}
	defer f.Close()

	// Bounded read: stop after the requested limit. Reading via a
	// scanner would be cleaner but stdlib's default token size caps at
	// 64KB per line, which trips on minified js. Read whole file then
	// slice — capped at a few MB which is fine for any source file.
	const maxBytes = 8 << 20 // 8 MiB
	body, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return NewTextErrorResponse("view: read: " + err.Error()), nil
	}
	if len(body) > maxBytes {
		return NewTextErrorResponse("view: file too large (>8MB)"), nil
	}

	lines := strings.Split(string(body), "\n")
	total := len(lines)
	start := p.Offset
	if start < 0 {
		start = 0
	}
	if start >= total {
		return NewTextResponse(fmt.Sprintf("(empty: offset %d past end of %d-line file)", start, total)), nil
	}
	end := start + p.Limit
	if end > total {
		end = total
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
	}
	if end < total {
		fmt.Fprintf(&b, "(truncated; %d more lines after line %d)\n", total-end, end)
	}
	return NewTextResponse(b.String()), nil
}

// --- write -----------------------------------------------------------

type writeTool struct {
	ws          string
	onChange    func(relPath, op string)
	onBroadcast func(relPath, digest, contentBase64 string)
	onDiff      func(relPath, op, unifiedDiff string, addedLines, removedLines int)
}

type writeParams struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (w *writeTool) Info() ToolInfo {
	return ToolInfo{
		Name: "write",
		Description: "Create a new file or overwrite an existing one with the given content. " +
			"Parent directories are created as needed. " +
			"Use `edit` instead when you only need to change part of an existing file.",
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path relative to the workspace root.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Full contents of the file.",
			},
		},
		Required: []string{"file_path", "content"},
	}
}

func (w *writeTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p writeParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("write: invalid input json: " + err.Error()), nil
	}
	abs, err := resolveInWorkspace(w.ws, p.FilePath)
	if err != nil {
		return NewTextErrorResponse("write: " + err.Error()), nil
	}

	op := "modified"
	var prior string
	if existing, err := os.ReadFile(abs); err == nil {
		prior = string(existing)
	} else if os.IsNotExist(err) {
		op = "created"
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return NewTextErrorResponse("write: mkdir: " + err.Error()), nil
	}
	if err := os.WriteFile(abs, []byte(p.Content), 0o644); err != nil {
		return NewTextErrorResponse("write: " + err.Error()), nil
	}
	relForward := filepath.ToSlash(p.FilePath)
	if w.onChange != nil {
		w.onChange(relForward, op)
	}
	if w.onBroadcast != nil {
		w.onBroadcast(relForward, contentDigest(p.Content), encodeBase64(p.Content))
	}
	if w.onDiff != nil {
		patch, added, removed := unifiedDiff(relForward, prior, p.Content)
		w.onDiff(relForward, op, patch, added, removed)
	}
	return NewTextResponse(fmt.Sprintf("wrote %d bytes to %s (%s)", len(p.Content), p.FilePath, op)), nil
}

// --- edit ------------------------------------------------------------

type editTool struct {
	ws          string
	onChange    func(relPath, op string)
	onBroadcast func(relPath, digest, contentBase64 string)
	onDiff      func(relPath, op, unifiedDiff string, addedLines, removedLines int)
}

type editParams struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (e *editTool) Info() ToolInfo {
	return ToolInfo{
		Name: "edit",
		Description: "Replace one occurrence (or all, with replace_all=true) of `old_string` " +
			"with `new_string` in the named file. The match must be exact, including " +
			"whitespace and line endings — read the file first with `view` to copy the " +
			"exact text. To create a brand-new file, use `write` instead.",
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path relative to the workspace root.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "Exact substring to find. Must match exactly once unless replace_all is true.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "Replacement text. Empty string deletes the match.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "If true, replace every occurrence. Default false (require unique match).",
				"default":     false,
			},
		},
		Required: []string{"file_path", "old_string", "new_string"},
	}
}

func (e *editTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var p editParams
	if err := json.Unmarshal([]byte(call.Input), &p); err != nil {
		return NewTextErrorResponse("edit: invalid input json: " + err.Error()), nil
	}
	abs, err := resolveInWorkspace(e.ws, p.FilePath)
	if err != nil {
		return NewTextErrorResponse("edit: " + err.Error()), nil
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return NewTextErrorResponse("edit: file not found: " + p.FilePath + " (use `write` to create new files)"), nil
		}
		return NewTextErrorResponse("edit: read: " + err.Error()), nil
	}
	src := string(body)
	if !strings.Contains(src, p.OldString) {
		return NewTextErrorResponse("edit: old_string not found in " + p.FilePath), nil
	}

	var updated string
	if p.ReplaceAll {
		updated = strings.ReplaceAll(src, p.OldString, p.NewString)
	} else {
		// Enforce uniqueness for non-replace-all to catch ambiguous
		// matches early. The model can re-issue with replace_all=true
		// or a more specific old_string.
		count := strings.Count(src, p.OldString)
		if count > 1 {
			return NewTextErrorResponse(fmt.Sprintf(
				"edit: old_string appears %d times in %s; pass replace_all=true or expand the match to be unique",
				count, p.FilePath)), nil
		}
		updated = strings.Replace(src, p.OldString, p.NewString, 1)
	}

	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return NewTextErrorResponse("edit: write: " + err.Error()), nil
	}
	relForward := filepath.ToSlash(p.FilePath)
	if e.onChange != nil {
		e.onChange(relForward, "modified")
	}
	if e.onBroadcast != nil {
		e.onBroadcast(relForward, contentDigest(updated), encodeBase64(updated))
	}
	if e.onDiff != nil {
		patch, added, removed := unifiedDiff(relForward, src, updated)
		e.onDiff(relForward, "modified", patch, added, removed)
	}
	delta := len(updated) - len(src)
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	return NewTextResponse(fmt.Sprintf("edited %s (%s%d bytes)", p.FilePath, sign, delta)), nil
}

// --- shared ----------------------------------------------------------

// resolveInWorkspace turns the model-supplied path into an absolute
// path inside the workspace. Refuses absolute paths that escape the
// workspace and refuses traversal sequences (`..`) that would land
// outside. Symlinks are NOT followed at the path level — but the OS
// will follow them when reading; that's an acceptable v1 risk.
func resolveInWorkspace(workspace, p string) (string, error) {
	workspace = filepath.Clean(workspace)
	if workspace == "" {
		return "", fmt.Errorf("workspace not set")
	}
	if p == "" {
		return "", fmt.Errorf("file_path is required")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workspace, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(workspace, abs)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}
	if strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return abs, nil
}
