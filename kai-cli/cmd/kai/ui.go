package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	spawnpkg "kai/internal/spawn"
	"kai/internal/synclog"
)

// `kai ui` — v0 dashboard. Localhost-only, polls JSON endpoints,
// embeds a single-page vanilla-JS UI. Not a daemon, not a tray icon.
// Reads the spawn registry + each spawned dir's sync-log JSONL.

//go:embed ui/index.html
var uiHTML embed.FS

var (
	uiPort       int
	uiNoBrowser  bool
	uiOpenLater  bool
	uiPalette    = []string{"#ef4444", "#3b82f6", "#22c55e", "#a855f7", "#f97316", "#14b8a6", "#eab308", "#ec4899"}
	syncLogLimit = 200
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Open the local Kai dashboard in your browser",
	Long: `Starts a local HTTP server (127.0.0.1 only) and opens the dashboard
in your default browser. Shows live status of every spawned workspace
and a real-time feed of sync events.

The server runs in the foreground; Ctrl+C exits.`,
	RunE: runUI,
}

func init() {
	uiCmd.Flags().IntVar(&uiPort, "port", 0, "Port to listen on (0 = random free port)")
	uiCmd.Flags().BoolVar(&uiNoBrowser, "no-browser", false, "Don't auto-open a browser")
}

func runUI(cmd *cobra.Command, args []string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents", handleAgents)
	mux.HandleFunc("/api/events", handleEvents)
	mux.HandleFunc("/api/header", handleHeader)
	mux.HandleFunc("/", serveIndex)

	addr := fmt.Sprintf("127.0.0.1:%d", uiPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Printf("Kai dashboard → %s\n", url)
	fmt.Println("Ctrl+C to exit")

	if !uiNoBrowser {
		go func() {
			time.Sleep(200 * time.Millisecond)
			openBrowser(url)
		}()
	}
	return http.Serve(ln, mux)
}

// ---------------------------------------------------------------------------
// Handlers

type agentDTO struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Path        string `json:"path"`
	Workspace   string `json:"workspace"`
	SyncMode    string `json:"sync_mode"`
	Checkpoints int    `json:"checkpoints"`
	UptimeSec   int64  `json:"uptime_sec"`
	LastFile    string `json:"last_file,omitempty"`
	LastEventTs int64  `json:"last_event_ts,omitempty"`
	Sparkline   []int  `json:"sparkline"`
	TaskHint    string `json:"task_hint,omitempty"`
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	entries, err := spawnpkg.List()
	if err != nil {
		writeJSON(w, []agentDTO{})
		return
	}
	out := make([]agentDTO, 0, len(entries))
	colorIdx := 0
	for _, e := range entries {
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		kdPath := filepath.Join(e.Path, ".kai")
		dto := agentDTO{
			Name:      displayAgentName(e.Agent, e.WorkspaceName),
			Color:     uiPalette[colorIdx%len(uiPalette)],
			Path:      e.Path,
			Workspace: e.WorkspaceName,
			SyncMode:  e.SyncMode,
		}
		colorIdx++
		dto.Checkpoints = countCheckpointFiles(kdPath)
		if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
			dto.UptimeSec = int64(time.Since(t).Seconds())
		}
		lastFile, lastTs, sparks := summarizeSyncLog(kdPath, time.Now())
		dto.LastFile = lastFile
		dto.LastEventTs = lastTs
		dto.Sparkline = sparks
		// task_hint: no source today; left blank for v0. Wire to
		// changeset intent or workspace description in v1.
		out = append(out, dto)
	}
	writeJSON(w, out)
}

type eventDTO struct {
	Type      string `json:"type"`     // checkpoint | push | recv | merge | conflict | skip
	Agent     string `json:"agent"`
	AgentName string `json:"agent_name"`
	Color     string `json:"color"`
	File      string `json:"file,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Detail    string `json:"detail,omitempty"`
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	entries, err := spawnpkg.List()
	if err != nil {
		writeJSON(w, []eventDTO{})
		return
	}
	all := make([]eventDTO, 0, 64)
	colorIdx := 0
	for _, e := range entries {
		color := uiPalette[colorIdx%len(uiPalette)]
		colorIdx++
		if _, err := os.Stat(e.Path); err != nil {
			continue
		}
		kdPath := filepath.Join(e.Path, ".kai")
		evs := readRecentSyncLog(kdPath, 50)
		for _, ev := range evs {
			all = append(all, eventDTO{
				Type:      ev.Event,
				Agent:     ev.Agent,
				AgentName: displayAgentName(e.Agent, e.WorkspaceName),
				Color:     color,
				File:      ev.File,
				Timestamp: ev.Timestamp,
				Detail:    ev.Detail,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Timestamp > all[j].Timestamp })
	if len(all) > syncLogLimit {
		all = all[:syncLogLimit]
	}
	writeJSON(w, all)
}

type headerDTO struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

func handleHeader(w http.ResponseWriter, r *http.Request) {
	cwd, _ := os.Getwd()
	dto := headerDTO{Repo: filepath.Base(cwd), Branch: ""}
	if data, err := os.ReadFile(filepath.Join(cwd, ".git", "HEAD")); err == nil {
		s := strings.TrimSpace(string(data))
		if strings.HasPrefix(s, "ref: refs/heads/") {
			dto.Branch = strings.TrimPrefix(s, "ref: refs/heads/")
		}
	}
	// Prefer remote tenant/repo if available (matches the mockup's
	// "kaicontext/kai" form). Falls back to dir basename above.
	if rem, err := remoteOriginEntry(); err == nil && rem != "" {
		dto.Repo = rem
	}
	writeJSON(w, dto)
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := uiHTML.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// ---------------------------------------------------------------------------
// Helpers

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

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

// readRecentSyncLog returns the most recent N entries from today's
// sync-log file, newest first.
func readRecentSyncLog(kdPath string, max int) []synclog.SyncLogEntry {
	logDir := filepath.Join(kdPath, "sync-log")
	entries := []synclog.SyncLogEntry{}
	dir, err := os.ReadDir(logDir)
	if err != nil {
		return entries
	}
	// Sort filenames descending to read newest first.
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
		// Walk newest-first within a file.
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

// summarizeSyncLog returns the most recent file edited, its timestamp,
// and a 20-bucket sparkline of event counts over the last 5 minutes.
func summarizeSyncLog(kdPath string, now time.Time) (string, int64, []int) {
	const buckets = 20
	const windowSec = 300
	bucketSec := int64(windowSec / buckets)
	hist := make([]int, buckets)
	cutoff := now.Add(-time.Duration(windowSec) * time.Second).UnixMilli()
	recent := readRecentSyncLog(kdPath, 500)
	var lastFile string
	var lastTs int64
	for _, e := range recent {
		if e.Timestamp > lastTs {
			lastTs = e.Timestamp
			if e.File != "" {
				lastFile = e.File
			}
		}
		if e.Timestamp < cutoff {
			continue
		}
		idx := int((now.UnixMilli()-e.Timestamp)/(bucketSec*1000)) % buckets
		if idx < 0 || idx >= buckets {
			continue
		}
		hist[buckets-1-idx]++
	}
	return lastFile, lastTs, hist
}

func remoteOriginEntry() (string, error) {
	// Cheapest: read .kai/remotes.json directly.
	data, err := os.ReadFile(filepath.Join(".kai", "remotes.json"))
	if err != nil {
		return "", err
	}
	var cfg struct {
		Remotes map[string]struct {
			Tenant string `json:"tenant"`
			Repo   string `json:"repo"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if r, ok := cfg.Remotes["origin"]; ok && r.Tenant != "" && r.Repo != "" {
		return r.Tenant + "/" + r.Repo, nil
	}
	return "", nil
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("cmd", "/c", "start", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}
