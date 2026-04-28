// Kai Desktop — Wails wrapper around the same data sources as `kai ui`.
//
// SpawnEntry/registry I/O and sync-log types come from kai-cli's
// public pkg/* packages; CheckpointRecord is still inlined since the
// authorship package wasn't lifted out of internal/ (it has heavier
// graph-DB deps that kai-desktop doesn't need).

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	spawnpkg "kai/pkg/spawn"
	"kai/pkg/synclog"
)

var palette = []string{
	"#ef4444", "#3b82f6", "#22c55e", "#a855f7",
	"#f97316", "#14b8a6", "#eab308", "#ec4899",
}

const syncLogLimit = 200

// App is the Wails-bound struct. Every exported method becomes a JS-
// callable function under window.go.main.App in the frontend.
type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// ---------------------------------------------------------------------------
// Inline types
//
// SpawnEntry and SyncLogEntry are now imported from kai/pkg/spawn and
// kai/pkg/synclog. CheckpointRecord stays inlined here because the
// authorship package wasn't lifted to pkg/ — its consolidation logic
// pulls in internal/graph which kai-desktop can't transitively import.

// CheckpointRecord mirrors kai/internal/authorship.CheckpointRecord.
type CheckpointRecord struct {
	File      string `json:"file"`
	Agent     string `json:"agent"`
	Timestamp int64  `json:"ts"`
}

// ---------------------------------------------------------------------------
// DTOs (JSON shape sent to the frontend via Wails bindings)

type AgentDTO struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Path        string `json:"path"`
	Workspace   string `json:"workspace"`
	SyncMode    string `json:"sync_mode"`
	SourceRepo  string `json:"source_repo,omitempty"`
	Checkpoints int    `json:"checkpoints"`
	UptimeSec   int64  `json:"uptime_sec"`
	LastFile    string `json:"last_file,omitempty"`
	LastEventTs int64  `json:"last_event_ts,omitempty"`
	Sparkline   []int  `json:"sparkline"`
}

type EventDTO struct {
	Type      string `json:"type"`
	Agent     string `json:"agent"`
	AgentName string `json:"agent_name"`
	Color     string `json:"color"`
	File      string `json:"file,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Detail    string `json:"detail,omitempty"`
}

type HeaderDTO struct {
	AgentCount int      `json:"agent_count"`
	RepoCount  int      `json:"repo_count"`
	Repos      []string `json:"repos"`
	SoleRepo   string   `json:"sole_repo,omitempty"`
}

// ---------------------------------------------------------------------------
// Bound methods (called from JS via window.go.main.App.*)

func (a *App) Agents() []AgentDTO {
	entries, _ := spawnpkg.List()
	out := make([]AgentDTO, 0, len(entries))
	for i, e := range entries {
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		kdPath := filepath.Join(e.Path, ".kai")
		dto := AgentDTO{
			Name:       displayAgentName(e.Agent, e.WorkspaceName),
			Color:      palette[i%len(palette)],
			Path:       e.Path,
			Workspace:  e.WorkspaceName,
			SyncMode:   e.SyncMode,
			SourceRepo: e.RepoChannel,
		}
		dto.Checkpoints = countCheckpointFiles(kdPath)
		if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
			dto.UptimeSec = int64(time.Since(t).Seconds())
		}
		lastFile, lastTs, sparks := summarizeActivity(kdPath, time.Now())
		dto.LastFile = lastFile
		dto.LastEventTs = lastTs
		dto.Sparkline = sparks
		out = append(out, dto)
	}
	return out
}

func (a *App) Events() []EventDTO {
	entries, _ := spawnpkg.List()
	all := make([]EventDTO, 0, 64)
	for i, e := range entries {
		color := palette[i%len(palette)]
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		kdPath := filepath.Join(e.Path, ".kai")
		displayName := displayAgentName(e.Agent, e.WorkspaceName)
		for _, ev := range readRecentSyncLog(kdPath, 50) {
			all = append(all, EventDTO{
				Type:      ev.Event,
				Agent:     ev.Agent,
				AgentName: displayName,
				Color:     color,
				File:      ev.File,
				Timestamp: ev.Timestamp,
				Detail:    ev.Detail,
			})
		}
		for _, cp := range readPendingCheckpoints(kdPath) {
			all = append(all, EventDTO{
				Type:      "checkpoint",
				Agent:     cp.Agent,
				AgentName: displayName,
				Color:     color,
				File:      cp.File,
				Timestamp: cp.Timestamp,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Timestamp > all[j].Timestamp })
	if len(all) > syncLogLimit {
		all = all[:syncLogLimit]
	}
	return all
}

func (a *App) Header() HeaderDTO {
	entries, _ := spawnpkg.List()
	seen := map[string]bool{}
	repos := []string{}
	live := 0
	for _, e := range entries {
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		live++
		if e.RepoChannel != "" && !seen[e.RepoChannel] {
			seen[e.RepoChannel] = true
			repos = append(repos, e.RepoChannel)
		}
	}
	sort.Strings(repos)
	dto := HeaderDTO{AgentCount: live, RepoCount: len(repos), Repos: repos}
	if len(repos) == 1 {
		dto.SoleRepo = repos[0]
	}
	return dto
}

// ---------------------------------------------------------------------------
// On-disk readers (sync-log + checkpoint JSONL parsing)

func displayAgentName(agent, ws string) string {
	if agent != "" {
		return agent
	}
	return ws
}

func countCheckpointFiles(kdPath string) int {
	root := filepath.Join(kdPath, "checkpoints")
	count := 0
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func readRecentSyncLog(kdPath string, max int) []synclog.SyncLogEntry {
	logDir := filepath.Join(kdPath, "sync-log")
	entries := []synclog.SyncLogEntry{}
	dir, err := os.ReadDir(logDir)
	if err != nil {
		return entries
	}
	files := make([]string, 0, len(dir))
	for _, d := range dir {
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, d.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, fname := range files {
		data, err := os.ReadFile(filepath.Join(logDir, fname))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i] == "" {
				continue
			}
			var e synclog.SyncLogEntry
			if json.Unmarshal([]byte(lines[i]), &e) != nil {
				continue
			}
			entries = append(entries, e)
			if len(entries) >= max {
				return entries
			}
		}
	}
	return entries
}

// readPendingCheckpoints walks .kai/checkpoints/<session>/*.jsonl and
// parses one CheckpointRecord per line. Best-effort: any read or
// parse failure is silently skipped.
func readPendingCheckpoints(kdPath string) []CheckpointRecord {
	root := filepath.Join(kdPath, "checkpoints")
	out := []CheckpointRecord{}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var cp CheckpointRecord
			if json.Unmarshal([]byte(line), &cp) == nil {
				out = append(out, cp)
			}
		}
		return nil
	})
	return out
}

func summarizeActivity(kdPath string, now time.Time) (string, int64, []int) {
	const buckets = 20
	const windowSec = 300
	bucketSec := int64(windowSec / buckets)
	hist := make([]int, buckets)
	cutoff := now.Add(-time.Duration(windowSec) * time.Second).UnixMilli()

	type tsFile struct {
		ts   int64
		file string
	}
	all := []tsFile{}

	for _, e := range readRecentSyncLog(kdPath, 500) {
		all = append(all, tsFile{ts: e.Timestamp, file: e.File})
	}
	for _, cp := range readPendingCheckpoints(kdPath) {
		all = append(all, tsFile{ts: cp.Timestamp, file: cp.File})
	}

	var lastFile string
	var lastTs int64
	for _, x := range all {
		if x.ts > lastTs {
			lastTs = x.ts
			if x.file != "" {
				lastFile = x.file
			}
		}
		if x.ts < cutoff {
			continue
		}
		idx := int((now.UnixMilli()-x.ts)/(bucketSec*1000)) % buckets
		if idx < 0 || idx >= buckets {
			continue
		}
		hist[buckets-1-idx]++
	}
	return lastFile, lastTs, hist
}
