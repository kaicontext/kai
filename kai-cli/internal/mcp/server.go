// Package mcp provides an MCP (Model Context Protocol) server that exposes
// Kai's semantic graph to AI coding assistants like Claude Code and Kilo Code.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"lukechampine.com/blake3"

	"kai-core/merge"
	"kai-core/parse"
	"kai/internal/authorship"
	"kai/internal/dirio"
	"kai/internal/graph"
	"kai/internal/module"
	"kai/internal/ref"
	"kai/internal/remote"
	"kai/internal/snapshot"
	"kai/pkg/synclog"
	"kai/internal/watcher"
)

// --- Initialization State ---

// initPhase represents the current phase of background initialization.
type initPhase string

const (
	phaseDetecting  initPhase = "detecting_languages"
	phaseScanning   initPhase = "scanning_files"
	phaseCapturing  initPhase = "capturing_head"
	phaseBuilding   initPhase = "building_graph"
	phaseFinalizing initPhase = "finalizing"
)

// initState tracks the progress of a background initialization job.
type initState struct {
	phase     initPhase
	message   string
	fileCount int
	startedAt time.Time
	done      chan struct{} // closed when init completes
	err       error        // non-nil if init failed
}

// Server wraps the MCP server with access to the Kai graph database.
// Supports lazy initialization: db may be nil until the first data request
// triggers background indexing.
type Server struct {
	mu       sync.Mutex
	db       *graph.DB          // nil until initialized
	resolver *ref.Resolver      // nil until initialized
	snap     *snapshot.Creator   // nil until initialized
	parser   *parse.Parser       // lazy, created on first AST-filtered grep
	workDir  string             // project root (where .kai lives)
	kaiDir   string             // path to .kai directory
	version  string             // CLI version for MCP handshake
	initJob  *initState         // non-nil while background init is running
	// Authorship tracking
	sessionID    string                       // unique per MCP process lifetime
	agentName    string                       // detected from MCP client (e.g. "claude-code")
	clientMu     sync.RWMutex                 // guards clientName/clientVersion
	clientName   string                       // populated from initialize handshake
	clientVersion string                      // populated from initialize handshake
	cpWriter     *authorship.CheckpointWriter // checkpoint file writer
	// Live graph watcher
	fileWatcher  *watcher.Watcher
	// Overlap warnings and advisory locks from server
	warningsMu   sync.RWMutex
	warnings     []remote.OverlapWarning
	locksMu      sync.RWMutex
	locks        []remote.FileLock
	// Sync conflicts (surfaced in kai_activity)
	syncConflictsMu sync.RWMutex
	syncConflicts   []syncConflictInfo
	// Billing warning (surfaced in kai_activity)
	billingWarningMu sync.RWMutex
	billingWarning   string
	// Remote client for lock/unlock/sync (cached from watcher setup)
	remoteClient *remote.Client
	// Edge sync state
	lastEdgeSeq    int64
	syncChannelID  string   // live sync channel ID (empty if not subscribed)
	syncAgentName  string   // session-unique agent name for sync
	syncStopSSE    chan struct{} // signals SSE reader goroutine to stop
	// Files written by sync — watcher should skip these to avoid feedback loop
	syncWrittenMu  sync.Mutex
	syncWritten    map[string]time.Time // path -> time written
	// Last-synced file content for 3-way merge on receive
	syncBaseMu     sync.RWMutex
	syncBase       map[string][]byte // path -> content at last sync point
	// Sync audit log writer
	syncLogWriter  *synclog.SyncLogWriter
	// Content cache for auto-checkpoint line range detection
	preEditMu      sync.RWMutex
	preEditContent map[string][]byte // path -> content before last edit
	// Rate limiting: cap read-only tool calls to avoid context bloat
	readCallCount int32
}

// NewServer creates a new MCP server for the given project directory.
// If .kai already exists and contains a valid database, it opens immediately.
// Otherwise, initialization is deferred until the first data request.
func NewServer(workDir, version string) *Server {
	kaiDir := filepath.Join(workDir, ".kai")
	sessionID := fmt.Sprintf("mcp_%d_%d", os.Getpid(), time.Now().UnixMilli())
	s := &Server{
		workDir:        workDir,
		kaiDir:         kaiDir,
		version:        version,
		sessionID:      sessionID,
		cpWriter:       authorship.NewCheckpointWriter(kaiDir, sessionID),
		preEditContent: make(map[string][]byte),
		syncWritten:    make(map[string]time.Time),
		syncLogWriter:  synclog.NewSyncLogWriter(kaiDir),
	}

	// Fast path: if .kai exists, try to open the store immediately
	dbPath := filepath.Join(kaiDir, "db.sqlite")
	objPath := filepath.Join(kaiDir, "objects")
	if _, err := os.Stat(dbPath); err == nil {
		if db, err := graph.Open(dbPath, objPath); err == nil {
			s.db = db
			s.resolver = ref.NewResolver(db)
			s.snap = snapshot.NewCreator(db, nil)
			// Start file watcher if a snapshot exists
			go s.startWatcher(db)
		}
	}

	// Ensure AI coding tool context files have Kai MCP instructions
	ensureAIContextFiles(workDir)

	// If no database exists yet, start background initialization immediately
	// so the index is ready by the time the first tool call arrives.
	if s.db == nil {
		s.mu.Lock()
		s.startInitLocked()
		s.mu.Unlock()
	}

	return s
}

// NewServerWithDB creates a server with a pre-opened database (for backward compatibility).
func NewServerWithDB(db *graph.DB, workDir, version string) *Server {
	return &Server{
		db:       db,
		resolver: ref.NewResolver(db),
		snap:     snapshot.NewCreator(db, nil),
		workDir:  workDir,
		kaiDir:   filepath.Join(workDir, ".kai"),
		version:  version,
	}
}

// Serve starts the MCP server on stdio and blocks until the connection closes.
func (s *Server) Serve(ctx context.Context) error {
	version := s.version
	if version == "" {
		version = "0.0.0-dev"
	}

	// Register an after-initialize hook to capture the MCP client identity
	// (e.g. "claude-code 2.0.0", "cursor 0.42.1"). This is used by
	// authorship auto-attribution so checkpoints record the real agent
	// name instead of the generic "mcp-client" fallback.
	hooks := &server.Hooks{}
	hooks.AddAfterInitialize(func(ctx context.Context, id any, msg *mcp.InitializeRequest, res *mcp.InitializeResult) {
		if msg == nil {
			return
		}
		name := strings.TrimSpace(msg.Params.ClientInfo.Name)
		ver := strings.TrimSpace(msg.Params.ClientInfo.Version)
		if name == "" {
			return
		}
		s.clientMu.Lock()
		s.clientName = name
		s.clientVersion = ver
		s.clientMu.Unlock()
		// Rewrite the session file now that we know who's talking to us.
		s.writeSessionFile()
	})

	srv := server.NewMCPServer(
		"kai",
		version,
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)

	s.registerTools(srv)

	// Reconcile any stale or orphaned MCP sessions from prior runs before
	// we take over the workdir. If a sibling session is alive and healthy,
	// this returns an error and we refuse to start.
	if err := s.reconcileExistingSessions(); err != nil {
		return err
	}

	// Write MCP session file so kai capture can detect active AI sessions.
	// The initial write uses the "mcp-client" fallback; the after-initialize
	// hook rewrites it with the real client identity as soon as the
	// handshake completes.
	s.writeSessionFile()
	defer s.removeSessionFile()

	// Lock session base — the snapshot this session started from.
	// Persists as a named ref so kai review open can find it after the session exits.
	if s.db != nil && s.resolver != nil {
		kind := ref.KindSnapshot
		if result, err := s.resolver.Resolve("snap.latest", &kind); err == nil {
			refMgr := ref.NewRefManager(s.db)
			if err := refMgr.Set("session."+s.sessionID+".base", result.ID, ref.KindSnapshot); err != nil {
				fmt.Fprintf(os.Stderr, "[kai-mcp] warning: could not write session base ref: %v\n", err)
			}
		}
		// If snap.latest doesn't exist (fresh repo), skip silently — no meaningful base
	}

	return server.ServeStdio(srv)
}

// startWatcher starts the file watcher for live graph updates.
// Runs in the background — file changes are automatically reflected in MCP queries.
func (s *Server) startWatcher(db *graph.DB) {
	w, err := watcher.New(s.workDir, db)
	if err != nil {
		return
	}

	w.OnError = func(err error) {
		fmt.Fprintf(os.Stderr, "[kai-watcher] error: %v\n", err)
	}

	// Wire activity heartbeats and edge deltas to the server (best-effort)
	if client, err := remote.NewClientForRemote("origin"); err == nil {
		s.remoteClient = client
		fmt.Fprintf(os.Stderr, "[kai-sync] remote client connected: %s\n", client.BaseURL)
		agentName := s.agentName
		if agentName == "" {
			agentName = "mcp-client"
		}
		s.syncAgentName = agentName + ":" + s.sessionID

		// Auto-resume live sync if it was on before this process started.
		if st, ok := s.loadSyncState(); ok && s.syncChannelID == "" {
			if _, _, err := s.doSubscribeSync(st.Files); err != nil {
				fmt.Fprintf(os.Stderr, "[kai-sync] auto-resume failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[kai-sync] auto-resumed from persisted state\n")
			}
		}
		w.OnActivity = func(entries []watcher.ActivityEntry) {
			var files []remote.ActivityFile
			var editedPaths []string
			for _, e := range entries {
				files = append(files, remote.ActivityFile{
					Path:      e.Path,
					Operation: e.Operation,
					Timestamp: e.Timestamp.UnixMilli(),
				})
				editedPaths = append(editedPaths, e.Path)
			}
			// Compute related files from local graph (1-hop)
			relatedFiles := w.GetRelatedFiles(editedPaths)
			// Fire-and-forget — don't block the watcher
			go func() {
				warnings, _ := client.PushActivity(agentName, files, relatedFiles)
				if len(warnings) > 0 {
					s.warningsMu.Lock()
					s.warnings = warnings
					s.warningsMu.Unlock()
				}
				// Auto-checkpoint: record AI authorship for edited files
				for _, path := range editedPaths {
					if s.isSyncWritten(path) {
						continue // don't attribute sync-received files to local agent
					}
					absPath := filepath.Join(s.workDir, path)
					content, err := os.ReadFile(absPath)
					if err != nil || len(content) > 512*1024 {
						continue
					}

					s.preEditMu.RLock()
					old := s.preEditContent[path]
					s.preEditMu.RUnlock()

					ranges := authorship.DiffLineRanges(old, content)
					for _, r := range ranges {
						s.cpWriter.Write(authorship.CheckpointRecord{
							File:       path,
							StartLine:  r.Start,
							EndLine:    r.End,
							Action:     "modify",
							AuthorType: "ai",
							Agent:      agentName,
							Timestamp:  time.Now().UnixMilli(),
						})
					}

					s.preEditMu.Lock()
					s.preEditContent[path] = content
					s.preEditMu.Unlock()
				}

				// Push file content if live sync is active
				if s.syncChannelID != "" {
					for _, path := range editedPaths {
						// Skip files written by sync to avoid feedback loop.
						// isSyncWritten has a 60s TTL — the second guard below
						// (syncBase equality) catches the longer-lived case
						// where a file stayed in the watcher's activity log
						// past the TTL but still has nothing new to push.
						if s.isSyncWritten(path) {
							s.syncLogWriter.Write(synclog.SyncLogEntry{
								Event:     synclog.EventSkip,
								File:      path,
								Agent:     s.syncAgentName,
								Channel:   s.syncChannelID,
								Timestamp: time.Now().UnixMilli(),
								Detail:    "feedback loop prevention",
							})
							continue
						}
						absPath := filepath.Join(s.workDir, path)
						content, err := os.ReadFile(absPath)
						if err != nil || len(content) > 512*1024 { // skip files > 512KB
							continue
						}
						// Skip if current content matches last-known sync state.
						// syncBase is updated by both push (after success) and
						// receive (after applySyncContent). If it equals the
						// current on-disk content, there is literally nothing
						// new to tell the server — suppress the push. This is
						// what breaks the watcher-activity-log / 60s-TTL race
						// that caused perpetual resonance between peers.
						s.syncBaseMu.RLock()
						base := s.syncBase[path]
						s.syncBaseMu.RUnlock()
						if base != nil && bytes.Equal(base, content) {
							s.syncLogWriter.Write(synclog.SyncLogEntry{
								Event:     synclog.EventSkip,
								File:      path,
								Agent:     s.syncAgentName,
								Channel:   s.syncChannelID,
								Timestamp: time.Now().UnixMilli(),
								Detail:    "no change since last sync",
							})
							continue
						}
						// Convert path to git-relative so all clones use the same paths
						syncPath := toGitRelativePath(s.workDir, path)
						encoded := base64.StdEncoding.EncodeToString(content)
						if err := client.SyncPushFile(s.syncAgentName, s.syncChannelID, syncPath, "", encoded); err != nil {
							if limErr, ok := err.(*remote.CommitLimitError); ok {
								fmt.Fprintf(os.Stderr, "[kai-sync] sync limit reached: %d/%d on %s plan\n", limErr.Used, limErr.Limit, limErr.Tier)
								if limErr.UpgradeURL != "" {
									fmt.Fprintf(os.Stderr, "[kai-sync] upgrade: %s\n", limErr.UpgradeURL)
								}
								s.billingWarningMu.Lock()
								s.billingWarning = fmt.Sprintf("Usage limit reached (%d/%d on %s plan). Live sync paused. Upgrade: %s", limErr.Used, limErr.Limit, limErr.Tier, limErr.UpgradeURL)
								s.billingWarningMu.Unlock()
								break // stop trying to sync more files
							}
							fmt.Fprintf(os.Stderr, "[kai-sync] push failed for %s: %v\n", syncPath, err)
						} else {
							fmt.Fprintf(os.Stderr, "[kai-sync] pushed %s (%d bytes)\n", syncPath, len(content))
							s.syncLogWriter.Write(synclog.SyncLogEntry{
								Event:     synclog.EventPush,
								File:      syncPath,
								Agent:     s.syncAgentName,
								Channel:   s.syncChannelID,
								Timestamp: time.Now().UnixMilli(),
							})
						}
						// Update base so we can 3-way merge incoming changes
						s.syncBaseMu.Lock()
						if s.syncBase != nil {
							s.syncBase[path] = content
						}
						s.syncBaseMu.Unlock()
					}
				}
			}()
		}
		w.OnEdgeDeltas = func(updates []watcher.EdgeUpdate) {
			var remoteUpdates []remote.IncrementalEdgeUpdate
			for _, u := range updates {
				ru := remote.IncrementalEdgeUpdate{File: u.File}
				for _, e := range u.AddedEdges {
					ru.AddedEdges = append(ru.AddedEdges, remote.EdgeDelta{
						Src: e.Src, Type: e.Type, Dst: e.Dst,
					})
				}
				for _, e := range u.RemovedEdges {
					ru.RemovedEdges = append(ru.RemovedEdges, remote.EdgeDelta{
						Src: e.Src, Type: e.Type, Dst: e.Dst,
					})
				}
				remoteUpdates = append(remoteUpdates, ru)
			}
			go client.PushEdgesIncremental(remoteUpdates)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[kai-sync] no remote configured (origin): %v\n", err)
	}

	if err := w.Start(); err != nil {
		return
	}

	s.mu.Lock()
	s.fileWatcher = w
	s.mu.Unlock()
}

// writeSessionFile records that an MCP session is active.
// kai capture reads this to auto-attribute changes to the AI agent.
// Uses PID in filename to avoid conflicts when multiple Claude windows are open.
//
// Called twice during a normal session: once at server start (with the
// generic "mcp-client" fallback) and again from the after-initialize hook
// once the MCP client has introduced itself.
func (s *Server) writeSessionFile() {
	agent := "mcp-client"
	var clientVersion string
	s.clientMu.RLock()
	if s.clientName != "" {
		agent = s.clientName
	}
	clientVersion = s.clientVersion
	s.clientMu.RUnlock()

	// Record session base snapshot for observability (ref is source of truth)
	var baseSnapshotHex string
	if s.resolver != nil {
		kind := ref.KindSnapshot
		if result, err := s.resolver.Resolve("snap.latest", &kind); err == nil {
			baseSnapshotHex = hex.EncodeToString(result.ID)
		}
	}

	sessionData := map[string]interface{}{
		"pid":              os.Getpid(),
		"sessionId":        s.sessionID,
		"startedAt":        time.Now().UnixMilli(),
		"updatedAt":        time.Now().UnixMilli(),
		"agent":            agent,
		"clientVersion":    clientVersion,
		"baseSnapshotID":   baseSnapshotHex,
	}
	data, _ := json.Marshal(sessionData)
	os.MkdirAll(s.kaiDir, 0755)
	os.WriteFile(s.sessionFilePath(), data, 0644)
}

func (s *Server) sessionFilePath() string {
	return filepath.Join(s.kaiDir, fmt.Sprintf("mcp-session-%d.json", os.Getpid()))
}

// touchSessionFile updates the timestamp to show the session is still active.
// Preserves the client identity if we learned it from an earlier handshake.
func (s *Server) touchSessionFile() {
	path := s.sessionFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var session map[string]interface{}
	if json.Unmarshal(data, &session) == nil {
		session["updatedAt"] = time.Now().UnixMilli()
		s.clientMu.RLock()
		if s.clientName != "" {
			session["agent"] = s.clientName
			session["clientVersion"] = s.clientVersion
		}
		s.clientMu.RUnlock()
		updated, _ := json.Marshal(session)
		os.WriteFile(path, updated, 0644)
	}
}

// removeSessionFile cleans up when the MCP server exits.
func (s *Server) removeSessionFile() {
	os.Remove(s.sessionFilePath())
}

// reconcileExistingSessions handles any other MCP session files present in
// s.kaiDir before we start watching files. Running multiple MCP servers in
// the same workdir causes every one of them to attach a separate fsnotify
// watcher and log its own push/skip for every event — the sync feed fills
// with noise.
//
// For each stale session file we find:
//
//   - If the PID is dead → leftover from a prior crash. Delete the file.
//   - If the PID is alive and its parent is dead → the parent Claude exited
//     without taking its MCP child with it. Safe to take over: SIGTERM the
//     orphan and delete its session file.
//   - If the PID is alive and its parent is also alive → a legitimate
//     concurrent Claude session is running here. Refuse to start so we
//     don't fight over fsnotify events (and so we don't enter a ping-pong
//     kill loop where each MCP kills the other on restart).
//
// The "parent alive" check is what makes this safe: once an MCP is
// orphaned we take over permanently; once a real sibling exists, we
// always refuse. There's no state that flips back and forth.
func (s *Server) reconcileExistingSessions() error {
	entries, err := os.ReadDir(s.kaiDir)
	if err != nil {
		return nil
	}
	myPid := os.Getpid()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "mcp-session-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		pidStr := strings.TrimSuffix(strings.TrimPrefix(name, "mcp-session-"), ".json")
		pid, convErr := strconv.Atoi(pidStr)
		if convErr != nil || pid == myPid {
			continue
		}
		path := filepath.Join(s.kaiDir, name)

		if !processAlive(pid) {
			os.Remove(path)
			continue
		}

		ppid, ok := parentPID(pid)
		if !ok || !processAlive(ppid) {
			// Orphan — parent Claude is gone. Take over.
			_ = syscall.Kill(pid, syscall.SIGTERM)
			os.Remove(path)
			continue
		}

		// Live sibling. Refuse to start; let the user pick which Claude to close.
		return fmt.Errorf("another MCP server is already running in %s (pid %d, parent pid %d). "+
			"Close one of the Claude sessions for this workdir, or kill pid %d, then retry",
			s.workDir, pid, ppid, pid)
	}
	return nil
}

// processAlive returns true if a signal-0 kill succeeds (process exists
// and we can signal it). A "permission denied" errno also means the
// process exists (just owned by someone else), which is treated as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}

// parentPID returns the PPID of pid via `ps`. Portable across macOS and
// Linux without pulling in platform-specific sysctl code.
func parentPID(pid int) (int, bool) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// Close cleans up resources.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileWatcher != nil {
		s.fileWatcher.Stop()
		s.fileWatcher = nil
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

const kaiMCPSection = `## Code Analysis

Use Kai MCP tools instead of reading files when you need to know callers, callees, dependencies, dependents, or test coverage for a file. One kai_context call returns this in ~500 tokens; reading the files yourself costs thousands. Do not read files just to discover call relationships or imports — use kai_context or kai_impact instead.

Do not delegate code exploration to subagents — they cannot access Kai MCP tools.
`

// ensureAIContextFiles checks for existing AI coding tool context files
// (CLAUDE.md, .cursorrules, etc.) and adds/updates Kai MCP instructions.
func ensureAIContextFiles(workDir string) {
	// Old text that should be replaced with the current version
	oldTexts := []string{
		"Use your native tools (grep, read, git diff) for search, file listing, and diffs",
		"Use the Kai MCP tools for call graph traversal, impact analysis, and code intelligence:",
	}

	files := []string{
		"CLAUDE.md",
		".github/copilot-instructions.md",
		".cursorrules",
		"CODEX.md",
		"AGENTS.md",
	}

	for _, name := range files {
		p := filepath.Join(workDir, name)
		existing, err := os.ReadFile(p)
		if err != nil {
			continue // file doesn't exist, skip
		}
		content := string(existing)

		// Check if old version needs replacing
		needsReplace := false
		for _, old := range oldTexts {
			if strings.Contains(content, old) {
				needsReplace = true
				break
			}
		}

		if needsReplace {
			// Remove old section and prepend new one
			// Find the old "## Code Analysis" section and remove it
			if idx := strings.Index(content, "## Code Analysis"); idx >= 0 {
				// Find the end of the section (next ## or end of content)
				end := idx + len("## Code Analysis")
				rest := content[end:]
				if nextSection := strings.Index(rest, "\n## "); nextSection >= 0 {
					content = content[:idx] + rest[nextSection+1:]
				} else {
					content = content[:idx]
				}
				content = strings.TrimSpace(content)
			}
			updated := kaiMCPSection + "\n" + content
			os.WriteFile(p, []byte(updated), 0644)
			continue
		}

		// No old version — add if missing entirely
		if !strings.Contains(content, "kai_context") {
			updated := kaiMCPSection + "\n" + content
			os.WriteFile(p, []byte(updated), 0644)
		}
	}
}

// isReady returns true if the database is open and ready for queries.
func (s *Server) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db != nil
}

// ensureReady checks if the store is ready. If not, it starts background
// initialization and returns a structured "initializing" response.
// Returns (nil, true) if ready, or (initResponse, false) if not ready.
func (s *Server) ensureReady() (*mcp.CallToolResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Fast path: already initialized
	if s.db != nil {
		// Touch session file to show activity (non-blocking)
		go s.touchSessionFile()
		return nil, true
	}

	// Check if init is already running
	if s.initJob != nil {
		// Return current progress
		result := s.initProgressLocked()
		return result, false
	}

	// Start background initialization
	s.startInitLocked()
	result := s.initProgressLocked()
	return result, false
}

// startInitLocked starts a background initialization goroutine.
// Must be called with s.mu held.
func (s *Server) startInitLocked() {
	job := &initState{
		phase:     phaseDetecting,
		message:   "Initializing Kai semantic index...",
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
	s.initJob = job

	go s.runInit(job)
}

// runInit performs the full initialization sequence in the background.
func (s *Server) runInit(job *initState) {
	defer close(job.done)

	kaiDir := s.kaiDir
	dbPath := filepath.Join(kaiDir, "db.sqlite")
	objPath := filepath.Join(kaiDir, "objects")

	// Step 1: Create .kai directory if needed
	s.mu.Lock()
	job.phase = phaseDetecting
	job.message = "Creating Kai store..."
	s.mu.Unlock()

	if err := os.MkdirAll(kaiDir, 0755); err != nil {
		s.mu.Lock()
		job.err = fmt.Errorf("creating .kai directory: %w", err)
		s.mu.Unlock()
		return
	}
	if err := os.MkdirAll(objPath, 0755); err != nil {
		s.mu.Lock()
		job.err = fmt.Errorf("creating objects directory: %w", err)
		s.mu.Unlock()
		return
	}

	// Step 2: Open database
	db, err := graph.Open(dbPath, objPath)
	if err != nil {
		s.mu.Lock()
		job.err = fmt.Errorf("opening database: %w", err)
		s.mu.Unlock()
		return
	}

	// Fast path: if a snapshot already exists, skip the full scan + capture.
	// This handles the case where multiple MCP instances open the same DB —
	// only the first needs to init, the rest just read.
	refMgr := ref.NewRefManager(db)
	if existing, _ := refMgr.Get("snap.latest"); existing != nil {
		s.mu.Lock()
		s.db = db
		s.resolver = ref.NewResolver(db)
		s.snap = snapshot.NewCreator(db, nil)
		job.phase = phaseFinalizing
		job.message = "Ready (existing snapshot found)"
		s.mu.Unlock()

		// Auto-start file watcher for live graph updates
		s.startWatcher(db)
		return
	}

	// Step 3: Scan files
	s.mu.Lock()
	job.phase = phaseScanning
	job.message = fmt.Sprintf("Scanning files in %s...", filepath.Base(s.workDir))
	s.mu.Unlock()

	source, err := dirio.OpenDirectory(s.workDir)
	if err != nil {
		db.Close()
		s.mu.Lock()
		job.err = fmt.Errorf("opening directory: %w", err)
		s.mu.Unlock()
		return
	}

	files, err := source.GetFiles()
	if err != nil {
		db.Close()
		s.mu.Lock()
		job.err = fmt.Errorf("reading files: %w", err)
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	job.fileCount = len(files)
	job.phase = phaseCapturing
	job.message = fmt.Sprintf("Capturing %d files...", len(files))
	s.mu.Unlock()

	// Step 4: Load modules and create snapshot
	modulesPath := filepath.Join(kaiDir, "rules", "modules.yaml")
	matcher, _ := module.LoadRulesOrEmpty(modulesPath)
	if len(matcher.GetAllModules()) == 0 {
		legacyPath := filepath.Join(s.workDir, "kai.modules.yaml")
		matcher, _ = module.LoadRulesOrEmpty(legacyPath)
	}

	creator := snapshot.NewCreator(db, matcher)
	snapshotID, err := creator.CreateSnapshot(source)
	if err != nil {
		db.Close()
		s.mu.Lock()
		job.err = fmt.Errorf("creating snapshot: %w", err)
		s.mu.Unlock()
		return
	}

	// Step 5: Analyze symbols and calls
	s.mu.Lock()
	job.phase = phaseBuilding
	job.message = fmt.Sprintf("Building semantic graph for %d files...", len(files))
	s.mu.Unlock()

	// Non-fatal errors: continue even if some files fail to parse
	_ = creator.Analyze(snapshotID, nil)

	// Step 6: Update refs
	s.mu.Lock()
	job.phase = phaseFinalizing
	job.message = "Finalizing index..."
	s.mu.Unlock()

	autoRefMgr := ref.NewAutoRefManager(db)
	if err := autoRefMgr.OnSnapshotCreated(snapshotID); err != nil {
		db.Close()
		s.mu.Lock()
		job.err = fmt.Errorf("updating refs: %w", err)
		s.mu.Unlock()
		return
	}

	// Success: install the DB into the server
	s.mu.Lock()
	s.db = db
	s.resolver = ref.NewResolver(db)
	s.snap = snapshot.NewCreator(db, nil)
	job.message = fmt.Sprintf("Kai index ready (%d files)", len(files))
	s.mu.Unlock()

	// Auto-start file watcher for live graph updates
	s.startWatcher(db)
}

// initProgressLocked returns a structured "initializing" MCP response.
// Must be called with s.mu held.
func (s *Server) initProgressLocked() *mcp.CallToolResult {
	job := s.initJob
	if job == nil {
		result, _ := jsonResult(map[string]interface{}{
			"status":  "uninitialized",
			"message": "Kai has not been initialized for this repository.",
			"repo":    s.workDir,
		})
		return result
	}

	// Check for failure
	if job.err != nil {
		result, _ := jsonResult(map[string]interface{}{
			"status":           "init_failed",
			"message":          fmt.Sprintf("Kai initialization failed: %v", job.err),
			"reason":           job.err.Error(),
			"retryable":        true,
			"suggested_action": "Call kai_refresh to retry, or continue without Kai context.",
		})
		return result
	}

	elapsed := time.Since(job.startedAt)
	retryAfter := 5 // default poll interval
	if job.fileCount > 1000 {
		retryAfter = 15
	} else if job.fileCount > 100 {
		retryAfter = 10
	}

	result, _ := jsonResult(map[string]interface{}{
		"status":                     "initializing",
		"message":                    job.message,
		"repo":                       filepath.Base(s.workDir),
		"phase":                      string(job.phase),
		"file_count":                 job.fileCount,
		"elapsed_seconds":            int(elapsed.Seconds()),
		"retry_after":                retryAfter,
		"can_continue_without_kai":   true,
	})
	return result
}

// readOnly returns tool options marking the tool as read-only, non-destructive, and idempotent.
func readOnly() mcp.ToolOption {
	return mcp.WithToolAnnotation(mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(true),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(false),
	})
}

// toolEnabled checks whether a tool should be registered.
// If KAI_TOOLS is set, only tools in the comma-separated list are enabled.
// If KAI_TOOLS is unset, all tools are enabled.
func toolEnabled(name string) bool {
	env := os.Getenv("KAI_TOOLS")
	if env == "" {
		return true
	}
	for _, t := range strings.Split(env, ",") {
		if strings.TrimSpace(t) == name {
			return true
		}
	}
	return false
}

func (s *Server) registerTools(srv *server.MCPServer) {
	// Initialize MCP call logging if enabled (KAI_MCP_LOG=1)
	if mcpLogEnabled() {
		initLogger(s.kaiDir)
	}

	// log wraps a handler with call logging when enabled, otherwise passes through.
	log := func(name string, h server.ToolHandlerFunc) server.ToolHandlerFunc {
		if globalLogger != nil {
			return withLogging(name, h)
		}
		return h
	}

	// maxReadCalls caps how many read-only tool calls we serve per session.
	// Each call triggers a full API round-trip that re-reads the conversation context,
	// so excessive calls cost more than they save.
	const maxReadCalls = 3

	// add registers a tool only if it passes the KAI_TOOLS filter.
	// Read-only tools are wrapped with a call counter that returns a short
	// message after maxReadCalls, nudging the model to use results it already has.
	add := func(tool mcp.Tool, handler server.ToolHandlerFunc, readOnly bool) {
		if !toolEnabled(tool.Name) {
			return
		}
		if readOnly {
			inner := handler
			handler = func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				n := atomic.AddInt32(&s.readCallCount, 1)
				if int(n) > maxReadCalls {
					return mcp.NewToolResultText("Rate limit: you have already queried the semantic graph — use the results above instead of making more calls."), nil
				}
				return inner(ctx, req)
			}
		}
		// Wrap all tools to prepend billing warnings when the org has hit its limit
		{
			inner := handler
			handler = func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				result, err := inner(ctx, req)
				if err != nil {
					return result, err
				}
				s.billingWarningMu.RLock()
				warning := s.billingWarning
				s.billingWarningMu.RUnlock()
				if warning != "" && result != nil && len(result.Content) > 0 {
					if textContent, ok := result.Content[0].(mcp.TextContent); ok {
						textContent.Text = "[BILLING] " + warning + "\n\n" + textContent.Text
						result.Content[0] = textContent
					}
				}
				return result, err
			}
		}
		srv.AddTool(tool, handler)
	}

	// kai_symbols — list all symbols in a file
	add(
		mcp.NewTool("kai_symbols",
			readOnly(),
			mcp.WithDescription("List symbols defined in a file with names, kinds, and line numbers. Use 'kind' to filter (e.g. only functions). Use 'exported=true' for Go public symbols only."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root (e.g. src/auth.go)")),
			mcp.WithString("kind", mcp.Description("Filter by symbol kind: function, method, class, variable, interface, struct, type, constant")),
			mcp.WithBoolean("exported", mcp.Description("If true, only return exported/public symbols (Go: uppercase-first)")),
			mcp.WithBoolean("signatures", mcp.Description("If true, include full signatures in output (default: false to save tokens)")),
		),
		log("kai_symbols", s.handleSymbols), true,
	)

	// kai_context — bundled context for a location (the high-leverage tool)
	// Subsumes kai_callers, kai_callees, kai_dependents, kai_dependencies, kai_tests
	// into a single tool to reduce tool count and token overhead from definitions.
	add(
		mcp.NewTool("kai_context",
			readOnly(),
			mcp.WithDescription("Get callers, callees, tests, and dependencies for a specific file. Use only when you need to understand call relationships before editing — not for general exploration or architecture questions."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
			mcp.WithString("symbol", mcp.Description("Symbol name to focus on (optional, returns all symbols in file if omitted)")),
			mcp.WithNumber("depth", mcp.Description("How many hops to traverse in the graph (default: 1)")),
		),
		log("kai_context", s.handleContext), true,
	)

	// kai_impact — transitive downstream impact analysis
	add(
		mcp.NewTool("kai_impact",
			readOnly(),
			mcp.WithDescription("Find all files and tests affected by changing a file, with hop distance. Use before making edits to assess blast radius — not for read-only exploration."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path to analyze impact for")),
			mcp.WithNumber("max_depth", mcp.Description("Maximum graph traversal depth (default: 3)")),
		),
		log("kai_impact", s.handleImpact), true,
	)

	// --- Authorship / AI Attribution Tools ---

	// kai_checkpoint — record an AI edit event (not rate-limited)
	add(
		mcp.NewTool("kai_checkpoint",
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
			mcp.WithDescription("Record an AI code authorship checkpoint. Call this after editing files to track which code was AI-generated. Lightweight — writes a small JSON file, no DB needed."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
			mcp.WithNumber("start_line", mcp.Required(), mcp.Description("First line of the edit (1-based)")),
			mcp.WithNumber("end_line", mcp.Required(), mcp.Description("Last line of the edit (1-based)")),
			mcp.WithString("action", mcp.Description("Type of edit: insert, modify, delete (default: modify)")),
			mcp.WithString("agent", mcp.Description("Agent name (default: auto-detected from MCP session)")),
			mcp.WithString("model", mcp.Description("Model name (e.g. claude-opus-4-6)")),
		),
		log("kai_checkpoint", s.handleCheckpoint), false,
	)

	// kai_blame — show AI vs human authorship for a file
	add(
		mcp.NewTool("kai_blame",
			readOnly(),
			mcp.WithDescription("Show AI vs human authorship for a file. Returns per-line attribution or a summary showing which agent/model authored each section."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to repo root")),
			mcp.WithString("format", mcp.Description("Output format: 'lines' (per-line ranges) or 'summary' (percentages). Default: summary")),
		),
		log("kai_blame", s.handleBlame), true,
	)

	// --- Individual graph traversal tools ---
	// Not registered by default (subsumed by kai_context).
	// Enable via KAI_TOOLS=kai_callers,kai_callees,... for e2e tests or power users.

	add(
		mcp.NewTool("kai_callers",
			readOnly(),
			mcp.WithDescription("Find which files/functions call a given symbol."),
			mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name to find callers of")),
			mcp.WithString("file", mcp.Description("File path to narrow the search")),
		),
		log("kai_callers", s.handleCallers), true,
	)

	add(
		mcp.NewTool("kai_callees",
			readOnly(),
			mcp.WithDescription("Find which symbols/files are called from a given symbol."),
			mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol name to find callees of")),
			mcp.WithString("file", mcp.Description("File containing the symbol")),
		),
		log("kai_callees", s.handleCallees), true,
	)

	add(
		mcp.NewTool("kai_dependents",
			readOnly(),
			mcp.WithDescription("Find files that import/depend on a given file."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path to find dependents of")),
		),
		log("kai_dependents", s.handleDependents), true,
	)

	add(
		mcp.NewTool("kai_dependencies",
			readOnly(),
			mcp.WithDescription("Find files that a given file imports/depends on."),
			mcp.WithString("file", mcp.Required(), mcp.Description("File path to find dependencies of")),
		),
		log("kai_dependencies", s.handleDependencies), true,
	)

	add(
		mcp.NewTool("kai_tests",
			readOnly(),
			mcp.WithDescription("Find test files that cover a given source file."),
			mcp.WithString("file", mcp.Required(), mcp.Description("Source file to find tests for")),
		),
		log("kai_tests", s.handleTests), true,
	)

	add(
		mcp.NewTool("kai_activity",
			readOnly(),
			mcp.WithDescription("Show recent file changes detected by the live graph watcher."),
		),
		log("kai_activity", s.handleActivity), false,
	)

	add(
		mcp.NewTool("kai_stats",
			readOnly(),
			mcp.WithDescription("Return project-wide authorship statistics."),
		),
		log("kai_stats", s.handleStats), true,
	)

	add(
		mcp.NewTool("kai_lock",
			mcp.WithDescription("Acquire advisory locks on files. Other agents will see the lock but can still edit (soft lock). Locks auto-expire after 5 minutes of inactivity."),
			mcp.WithString("files", mcp.Required(), mcp.Description("Comma-separated file paths to lock (e.g. 'src/main.go,src/lib.go')")),
		),
		log("kai_lock", s.handleLock), false,
	)

	add(
		mcp.NewTool("kai_unlock",
			mcp.WithDescription("Release advisory locks on files."),
			mcp.WithString("files", mcp.Required(), mcp.Description("Comma-separated file paths to unlock")),
		),
		log("kai_unlock", s.handleUnlock), false,
	)

	add(
		mcp.NewTool("kai_sync",
			readOnly(),
			mcp.WithDescription("Fetch edge changes other agents have made since your last sync. Shows what files and relationships changed, who changed them, and when."),
		),
		log("kai_sync", s.handleSync), false,
	)

	add(
		mcp.NewTool("kai_merge_check",
			readOnly(),
			mcp.WithDescription("Check if your current changes can merge cleanly with other agents' work. Call before finalizing edits to catch conflicts early."),
			mcp.WithString("files", mcp.Required(), mcp.Description("Comma-separated file paths you've modified")),
		),
		log("kai_merge_check", s.handleMergeCheck), false,
	)

	add(
		mcp.NewTool("kai_live_sync",
			mcp.WithDescription("Enable or disable real-time sync with other agents. When on, you'll see other agents' changes as they happen via SSE."),
			mcp.WithString("action", mcp.Required(), mcp.Description("'on' to enable, 'off' to disable")),
			mcp.WithString("files", mcp.Description("Comma-separated file paths to watch (default: all)")),
		),
		log("kai_live_sync", s.handleLiveSync), false,
	)

	add(
		mcp.NewTool("kai_checkpoint_now",
			mcp.WithDescription("Mark the current moment as a semantic checkpoint in the live-sync log. Use this when you've finished a coherent piece of work (feature complete, tests passing, a good place to stop) so reviewers can use the label as a compaction boundary. Optionally attach a structured trust assertion (e.g. tests-pass) for the review trust badge. Does NOT run kai capture."),
			mcp.WithString("label", mcp.Required(), mcp.Description("Human-readable description of what this checkpoint represents, e.g. 'authentication refactor complete' or 'tests passing'")),
			mcp.WithString("assert", mcp.Description("Structured trust assertion: tests-pass, types-ok, lints-clean, manual-verified")),
			mcp.WithString("plan_hash", mcp.Description("Test plan hash for verification (optional, used with assert=tests-pass)")),
		),
		log("kai_checkpoint_now", s.handleCheckpointNow), false,
	)
}

// --- Snapshot Resolution ---

// latestSnapshotID resolves the most recent snapshot.
func (s *Server) latestSnapshotID() ([]byte, error) {
	kind := ref.KindSnapshot
	result, err := s.resolver.Resolve("@snap:last", &kind)
	if err != nil {
		return nil, fmt.Errorf("no snapshots found — run 'kai capture' first: %w", err)
	}
	return result.ID, nil
}

// resolveSnapshotRef resolves a snapshot reference string, defaulting to @snap:last.
func (s *Server) resolveSnapshotRef(input string) ([]byte, error) {
	if input == "" {
		return s.latestSnapshotID()
	}
	kind := ref.KindSnapshot
	result, err := s.resolver.Resolve(input, &kind)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve ref %q: %w", input, err)
	}
	return result.ID, nil
}

// findFileNodeByPath finds a file node by its path within a snapshot.
func (s *Server) findFileNodeByPath(snapshotID []byte, filePath string) (*graph.Node, error) {
	edges, err := s.db.GetEdges(snapshotID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Dst)
		if err != nil {
			return nil, err
		}
		if node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && path == filePath {
			return node, nil
		}
	}
	return nil, fmt.Errorf("file %q not found in latest snapshot", filePath)
}

// findSymbolByName finds a symbol node by name within a file.
func (s *Server) findSymbolByName(snapshotID, fileID []byte, symbolName string) (*graph.Node, error) {
	symbols, err := s.snap.GetSymbolsInFile(fileID, snapshotID)
	if err != nil {
		return nil, err
	}
	for _, sym := range symbols {
		if name, ok := sym.Payload["fqName"].(string); ok {
			// Match on full name or just the last segment
			parts := strings.Split(name, ".")
			if name == symbolName || (len(parts) > 0 && parts[len(parts)-1] == symbolName) {
				return sym, nil
			}
		}
	}
	return nil, fmt.Errorf("symbol %q not found in file", symbolName)
}

// --- Tool Handlers ---

func (s *Server) handleSymbols(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	kindFilter := optString(req, "kind")
	exportedOnly := optBool(req, "exported")
	includeSignatures := optBool(req, "signatures")

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	symbols, err := s.snap.GetSymbolsInFile(fileNode.ID, snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error getting symbols: %v", err)), nil
	}

	type symbolInfo struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Signature string `json:"signature,omitempty"`
		Line      int    `json:"line,omitempty"`
	}

	results := make([]symbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		name, _ := sym.Payload["fqName"].(string)
		kind, _ := sym.Payload["kind"].(string)

		// Apply kind filter
		if kindFilter != "" && !strings.EqualFold(kind, kindFilter) {
			continue
		}

		// Apply exported filter: check if the bare name starts with uppercase
		if exportedOnly {
			bareName := name
			if idx := strings.LastIndex(bareName, "."); idx >= 0 {
				bareName = bareName[idx+1:]
			}
			if bareName == "" || !unicode.IsUpper(rune(bareName[0])) {
				continue
			}
		}

		info := symbolInfo{
			Name: name,
			Kind: kind,
		}
		if includeSignatures {
			if v, ok := sym.Payload["signature"].(string); ok {
				info.Signature = v
			}
		}
		if r, ok := sym.Payload["range"].(map[string]interface{}); ok {
			if line, ok := r["startLine"].(float64); ok {
				info.Line = int(line)
			}
		}
		results = append(results, info)
	}

	var b strings.Builder
	b.Grow(len(results) * 50)
	fmt.Fprintf(&b, "%d symbols in %s\n", len(results), filePath)
	for _, sym := range results {
		if sym.Line > 0 {
			fmt.Fprintf(&b, "%d:", sym.Line)
		}
		b.WriteString(sym.Kind)
		b.WriteByte(' ')
		b.WriteString(sym.Name)
		if sym.Signature != "" {
			b.WriteByte(' ')
			b.WriteString(sym.Signature)
		}
		b.WriteByte('\n')
	}
	return textResult(b.String())
}

func (s *Server) handleCallers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	symbolName, err := req.RequireString("symbol")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'symbol'"), nil
	}
	filePath := optString(req, "file")

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callers, err := s.findCallersViaFileEdges(snapID, symbolName, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return textResult(formatCallInfos("callers", symbolName, callers))
}

func (s *Server) handleCallees(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	symbolName, err := req.RequireString("symbol")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'symbol'"), nil
	}
	filePath := optString(req, "file")

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	callees, err := s.findCalleesViaFileEdges(snapID, symbolName, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return textResult(formatCallInfos("callees", symbolName, callees))
}

func (s *Server) handleDependents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	// Use GetEdgesToByPath for IMPORTS edges — finds files that import this file
	edges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying dependents: %v", err)), nil
	}

	dependents := make([]string, 0, len(edges))
	seen := make(map[string]bool)
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			dependents = append(dependents, path)
			seen[path] = true
		}
	}
	sort.Strings(dependents)

	return textResult(formatPathList("dependents of "+filePath, dependents))
}

func (s *Server) handleDependencies(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Find edges FROM this file (what it imports)
	edges, err := s.db.GetEdges(fileNode.ID, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying dependencies: %v", err)), nil
	}

	deps := make([]string, 0, len(edges))
	seen := make(map[string]bool)
	for _, edge := range edges {
		node, err := s.db.GetNode(edge.Dst)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			deps = append(deps, path)
			seen[path] = true
		}
	}
	sort.Strings(deps)

	return textResult(formatPathList("dependencies of "+filePath, deps))
}

func (s *Server) handleTests(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	// Find test files via TESTS edges
	testEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeTests)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying tests: %v", err)), nil
	}

	// Also find test files that import this file
	importEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error querying import edges: %v", err)), nil
	}

	tests := make([]string, 0)
	seen := make(map[string]bool)

	// Direct TESTS edges
	for _, edge := range testEdges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] {
			tests = append(tests, path)
			seen[path] = true
		}
	}

	// Files that import this file and look like tests
	for _, edge := range importEdges {
		node, err := s.db.GetNode(edge.Src)
		if err != nil || node == nil {
			continue
		}
		if path, ok := node.Payload["path"].(string); ok && !seen[path] && isTestFile(path) {
			tests = append(tests, path)
			seen[path] = true
		}
	}

	sort.Strings(tests)

	return textResult(formatPathList("tests for "+filePath, tests))
}

func (s *Server) handleContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}
	symbolName := optString(req, "symbol")
	depth := int(optFloat(req, "depth", 1))
	_ = depth // reserved for future multi-hop traversal

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := map[string]interface{}{
		"file": filePath,
	}

	// Get all symbols in the file
	symbols, err := s.snap.GetSymbolsInFile(fileNode.ID, snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error getting symbols: %v", err)), nil
	}

	type symbolSummary struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Signature string `json:"signature,omitempty"`
		Line      int    `json:"line,omitempty"`
	}

	if symbolName != "" {
		// Focused mode: return only the requested symbol + its callers/callees
		result["focus_symbol"] = symbolName
		result["total_symbols"] = len(symbols)

		for _, sym := range symbols {
			name, _ := sym.Payload["fqName"].(string)
			parts := strings.Split(name, ".")
			bare := name
			if len(parts) > 0 {
				bare = parts[len(parts)-1]
			}
			if name == symbolName || bare == symbolName {
				info := symbolSummary{Name: name}
				if v, ok := sym.Payload["kind"].(string); ok {
					info.Kind = v
				}
				if v, ok := sym.Payload["signature"].(string); ok {
					info.Signature = v
				}
				if r, ok := sym.Payload["range"].(map[string]interface{}); ok {
					if line, ok := r["startLine"].(float64); ok {
						info.Line = int(line)
					}
				}
				result["symbol"] = info
				break
			}
		}

		symNode, err := s.findSymbolByName(snapID, fileNode.ID, symbolName)
		if err == nil {
			callerEdges, err := s.db.GetEdgesTo(symNode.ID, graph.EdgeCalls)
			if err == nil {
				callers, _ := s.edgesToSymbolLocations(callerEdges, true)
				result["callers"] = callers
			}
			calleeEdges, err := s.db.GetEdges(symNode.ID, graph.EdgeCalls)
			if err == nil {
				callees, _ := s.edgesToSymbolLocations(calleeEdges, false)
				result["callees"] = callees
			}
		}
	} else {
		// Summary mode: cap symbols
		const maxSymbols = 20
		shown := len(symbols)
		if shown > maxSymbols {
			shown = maxSymbols
		}
		symSummaries := make([]symbolSummary, 0, shown)
		for _, sym := range symbols[:shown] {
			info := symbolSummary{}
			if v, ok := sym.Payload["fqName"].(string); ok {
				info.Name = v
			}
			if v, ok := sym.Payload["kind"].(string); ok {
				info.Kind = v
			}
			symSummaries = append(symSummaries, info)
		}
		result["symbols"] = symSummaries
		if len(symbols) > maxSymbols {
			result["symbols_total"] = len(symbols)
		}
	}

	const maxContextItems = 15

	// Dependencies (what this file imports)
	importEdges, err := s.db.GetEdges(fileNode.ID, graph.EdgeImports)
	if err == nil {
		deps := make([]string, 0)
		for _, edge := range importEdges {
			node, err := s.db.GetNode(edge.Dst)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok {
				deps = append(deps, path)
			}
		}
		sort.Strings(deps)
		if len(deps) > maxContextItems {
			result["dependencies"] = deps[:maxContextItems]
			result["dependencies_total"] = len(deps)
		} else {
			result["dependencies"] = deps
		}
	}

	// Dependents (what imports this file)
	depEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeImports)
	if err == nil {
		dependents := make([]string, 0)
		seen := make(map[string]bool)
		for _, edge := range depEdges {
			node, err := s.db.GetNode(edge.Src)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok && !seen[path] {
				dependents = append(dependents, path)
				seen[path] = true
			}
		}
		sort.Strings(dependents)
		if len(dependents) > maxContextItems {
			result["dependents"] = dependents[:maxContextItems]
			result["dependents_total"] = len(dependents)
		} else {
			result["dependents"] = dependents
		}
	}

	// Tests
	testEdges, err := s.db.GetEdgesToByPath(filePath, graph.EdgeTests)
	if err == nil {
		tests := make([]string, 0)
		seen := make(map[string]bool)
		for _, edge := range testEdges {
			node, err := s.db.GetNode(edge.Src)
			if err != nil || node == nil {
				continue
			}
			if path, ok := node.Payload["path"].(string); ok && !seen[path] {
				tests = append(tests, path)
				seen[path] = true
			}
		}
		sort.Strings(tests)
		if len(tests) > maxContextItems {
			result["tests"] = tests[:maxContextItems]
			result["tests_total"] = len(tests)
		} else {
			result["tests"] = tests
		}
	}

	return jsonResult(result)
}

func (s *Server) handleImpact(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	filePath, err := req.RequireString("file")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}
	maxDepth := int(optFloat(req, "max_depth", 3))

	type impactEntry struct {
		Path   string `json:"path"`
		Hop    int    `json:"hop"`
		IsTest bool   `json:"is_test"`
	}

	visited := make(map[string]int) // path -> hop distance
	visited[filePath] = 0
	frontier := []string{filePath}
	var results []impactEntry

	for hop := 1; hop <= maxDepth && len(frontier) > 0; hop++ {
		// Batch query: find all files that import ANY file in the frontier
		importers, err := s.db.BatchGetImportersOf(frontier, graph.EdgeImports)
		if err != nil {
			importers = make(map[string]bool)
		}

		// Also batch query CALLS edges
		callers, err := s.db.BatchGetImportersOf(frontier, graph.EdgeCalls)
		if err != nil {
			callers = make(map[string]bool)
		}

		// Merge and dedupe
		var nextFrontier []string
		for path := range importers {
			if _, already := visited[path]; already {
				continue
			}
			visited[path] = hop
			results = append(results, impactEntry{Path: path, Hop: hop, IsTest: isTestFile(path)})
			nextFrontier = append(nextFrontier, path)
		}
		for path := range callers {
			if _, already := visited[path]; already {
				continue
			}
			visited[path] = hop
			results = append(results, impactEntry{Path: path, Hop: hop, IsTest: isTestFile(path)})
			nextFrontier = append(nextFrontier, path)
		}

		frontier = nextFrontier
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Hop != results[j].Hop {
			return results[i].Hop < results[j].Hop
		}
		return results[i].Path < results[j].Path
	})

	// Separate tests from source files
	var testFiles, sourceFiles []impactEntry
	for _, r := range results {
		if r.IsTest {
			testFiles = append(testFiles, r)
		} else {
			sourceFiles = append(sourceFiles, r)
		}
	}

	// Cap output to stay within MCP client token limits
	const maxItems = 20
	var b strings.Builder
	fmt.Fprintf(&b, "impact of %s (depth %d): %d affected\n", filePath, maxDepth, len(results))
	if len(sourceFiles) > 0 {
		shown := len(sourceFiles)
		if shown > maxItems {
			shown = maxItems
		}
		fmt.Fprintf(&b, "\naffected files (%d):\n", len(sourceFiles))
		for _, f := range sourceFiles[:shown] {
			fmt.Fprintf(&b, "  hop%d %s\n", f.Hop, f.Path)
		}
		if len(sourceFiles) > maxItems {
			fmt.Fprintf(&b, "  ... and %d more\n", len(sourceFiles)-maxItems)
		}
	}
	if len(testFiles) > 0 {
		shown := len(testFiles)
		if shown > maxItems {
			shown = maxItems
		}
		fmt.Fprintf(&b, "\naffected tests (%d):\n", len(testFiles))
		for _, f := range testFiles[:shown] {
			fmt.Fprintf(&b, "  hop%d %s\n", f.Hop, f.Path)
		}
		if len(testFiles) > maxItems {
			fmt.Fprintf(&b, "  ... and %d more\n", len(testFiles)-maxItems)
		}
	}
	return textResult(b.String())
}

// --- Call Graph Helpers ---

type callInfo struct {
	CallerFile string `json:"caller_file,omitempty"`
	CalleeFile string `json:"callee_file,omitempty"`
	CalleeName string `json:"callee_name"`
	Line       int    `json:"line,omitempty"`
}

// formatCallInfos renders caller/callee results as compact text.
func formatCallInfos(label, symbol string, infos []callInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s of %s\n", len(infos), label, symbol)
	for _, ci := range infos {
		file := ci.CallerFile
		if file == "" {
			file = ci.CalleeFile
		}
		if ci.Line > 0 {
			fmt.Fprintf(&b, "%s:%d:%s\n", file, ci.Line, ci.CalleeName)
		} else {
			fmt.Fprintf(&b, "%s:%s\n", file, ci.CalleeName)
		}
	}
	return b.String()
}

// findCallersViaFileEdges finds files that call the given symbol by scanning
// CALLS edges and matching the call node's calleeName payload.
func (s *Server) findCallersViaFileEdges(snapID []byte, symbolName, filePath string) ([]callInfo, error) {
	// Normalize qualified names:
	// Go: *Resolver.Resolve → Resolve, Type.Method → Method
	if idx := strings.LastIndex(symbolName, "."); idx >= 0 {
		symbolName = symbolName[idx+1:]
	}
	// Rust: Analyzer::analyze → analyze, crate::foo::bar → bar
	if idx := strings.LastIndex(symbolName, "::"); idx >= 0 {
		symbolName = symbolName[idx+2:]
	}

	// If file specified, find the file node and get edges TO it
	// Then filter by calleeName matching symbolName
	var edges []*graph.Edge
	var err error

	if filePath != "" {
		edges, err = s.db.GetEdgesToByPath(filePath, graph.EdgeCalls)
	} else {
		// No file specified — scan all CALLS edges (expensive but correct)
		edges, err = s.db.GetEdgesOfType(graph.EdgeCalls)
	}
	if err != nil {
		return nil, err
	}

	var results []callInfo
	seen := make(map[string]bool)

	for _, edge := range edges {
		// The "at" field is a Call node with payload {calleeName, callerFile, calleeFile, line}
		if len(edge.At) == 0 {
			continue
		}
		callNode, err := s.db.GetNode(edge.At)
		if err != nil || callNode == nil {
			continue
		}
		calleeName, _ := callNode.Payload["calleeName"].(string)
		// Normalize stored callee name (may be scoped: Analyzer::analyze, auth::handle_auth)
		normalizedCallee := calleeName
		if idx := strings.LastIndex(normalizedCallee, "::"); idx >= 0 {
			normalizedCallee = normalizedCallee[idx+2:]
		}
		if normalizedCallee != symbolName {
			continue
		}

		callerFile, _ := callNode.Payload["callerFile"].(string)
		line := 0
		if l, ok := callNode.Payload["line"].(float64); ok {
			line = int(l)
		}

		key := fmt.Sprintf("%s:%d", callerFile, line)
		if seen[key] {
			continue
		}
		seen[key] = true

		results = append(results, callInfo{
			CallerFile: callerFile,
			CalleeName: calleeName,
			Line:       line,
		})
	}

	return results, nil
}

// findCalleesViaFileEdges finds symbols called from a file containing the given symbol.
func (s *Server) findCalleesViaFileEdges(snapID []byte, symbolName, filePath string) ([]callInfo, error) {
	if filePath == "" {
		// Need a file to find outgoing calls from.
		// Find the symbol, then follow its DEFINES_IN edge to get the file.
		node, err := s.findSymbolInGraph(snapID, symbolName, "")
		if err != nil {
			return nil, err
		}
		// Symbol nodes are linked to files via DEFINES_IN edges (symbol → file)
		defEdges, err := s.db.GetEdges(node.ID, graph.EdgeDefinesIn)
		if err == nil {
			for _, e := range defEdges {
				fileNode, err := s.db.GetNode(e.Dst)
				if err == nil && fileNode != nil {
					filePath, _ = fileNode.Payload["path"].(string)
					if filePath != "" {
						break
					}
				}
			}
		}
		if filePath == "" {
			return nil, fmt.Errorf("cannot determine file for symbol %q", symbolName)
		}
	}

	fileNode, err := s.findFileNodeByPath(snapID, filePath)
	if err != nil {
		return nil, err
	}

	edges, err := s.db.GetEdges(fileNode.ID, graph.EdgeCalls)
	if err != nil {
		return nil, err
	}

	var results []callInfo
	seen := make(map[string]bool)

	for _, edge := range edges {
		if len(edge.At) == 0 {
			continue
		}
		callNode, err := s.db.GetNode(edge.At)
		if err != nil || callNode == nil {
			continue
		}
		calleeName, _ := callNode.Payload["calleeName"].(string)
		calleeFile, _ := callNode.Payload["calleeFile"].(string)
		line := 0
		if l, ok := callNode.Payload["line"].(float64); ok {
			line = int(l)
		}

		key := fmt.Sprintf("%s:%s", calleeFile, calleeName)
		if seen[key] {
			continue
		}
		seen[key] = true

		results = append(results, callInfo{
			CalleeFile: calleeFile,
			CalleeName: calleeName,
			Line:       line,
		})
	}

	return results, nil
}

// --- Helpers ---

// findSymbolInGraph finds a symbol by name, optionally scoped to a file.
func (s *Server) findSymbolInGraph(snapID []byte, symbolName, filePath string) (*graph.Node, error) {
	if filePath != "" {
		fileNode, err := s.findFileNodeByPath(snapID, filePath)
		if err != nil {
			return nil, err
		}
		return s.findSymbolByName(snapID, fileNode.ID, symbolName)
	}

	// No file specified — scan all files in snapshot for the symbol
	edges, err := s.db.GetEdges(snapID, graph.EdgeHasFile)
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		sym, err := s.findSymbolByName(snapID, edge.Dst, symbolName)
		if err == nil {
			return sym, nil
		}
	}
	return nil, fmt.Errorf("symbol %q not found in any file", symbolName)
}

type symbolLocation struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// edgesToSymbolLocations resolves edge src/dst nodes to symbol locations.
// If useSrc is true, resolves edge.Src (for callers); otherwise edge.Dst (for callees).
func (s *Server) edgesToSymbolLocations(edges []*graph.Edge, useSrc bool) ([]symbolLocation, error) {
	results := make([]symbolLocation, 0, len(edges))
	seen := make(map[string]bool)

	for _, edge := range edges {
		nodeID := edge.Dst
		if useSrc {
			nodeID = edge.Src
		}
		idHex := hex.EncodeToString(nodeID)
		if seen[idHex] {
			continue
		}
		seen[idHex] = true

		node, err := s.db.GetNode(nodeID)
		if err != nil || node == nil {
			continue
		}

		loc := symbolLocation{}
		if v, ok := node.Payload["fqName"].(string); ok {
			loc.Name = v
		}
		if v, ok := node.Payload["kind"].(string); ok {
			loc.Kind = v
		}
		if v, ok := node.Payload["signature"].(string); ok {
			loc.Signature = v
		}

		// Resolve file path from the symbol's fileId
		if fileIDStr, ok := node.Payload["fileId"].(string); ok {
			fileID, err := hex.DecodeString(fileIDStr)
			if err == nil {
				fileNode, err := s.db.GetNode(fileID)
				if err == nil && fileNode != nil {
					if path, ok := fileNode.Payload["path"].(string); ok {
						loc.File = path
					}
				}
			}
		}

		if r, ok := node.Payload["range"].(map[string]interface{}); ok {
			if line, ok := r["startLine"].(float64); ok {
				loc.Line = int(line)
			}
		}

		results = append(results, loc)
	}

	return results, nil
}

// gitOutput runs a git command and returns trimmed stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isTestFile returns true if the file path looks like a test file.
func isTestFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "_test.") ||
		strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.") ||
		strings.Contains(lower, "test_") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "test/") ||
		strings.Contains(lower, "__tests__/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/test/")
}

// jsonResult marshals data to a JSON text result.
// maxResultBytes is the soft cap on MCP tool response size.
// Responses larger than this are truncated to save tokens.
const maxResultBytes = 2048

func jsonResult(data interface{}) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error marshaling result: %v", err)), nil
	}
	s := string(b)
	if len(s) > maxResultBytes {
		s = s[:maxResultBytes] + "\n... truncated — use focused queries (symbol filter, kai_callers, kai_tests) for full details"
	}
	return mcp.NewToolResultText(s), nil
}

// textResult returns a plain-text MCP result (no JSON overhead).
func textResult(text string) (*mcp.CallToolResult, error) {
	if len(text) > maxResultBytes {
		text = text[:maxResultBytes] + "\n... truncated — use focused queries for full details"
	}
	return mcp.NewToolResultText(text), nil
}

// formatPathList renders a labeled list of file paths as compact text.
// Caps output at 50 items to stay within MCP client token limits.
func formatPathList(label string, paths []string) string {
	const maxItems = 50
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s\n", len(paths), label)
	shown := len(paths)
	if shown > maxItems {
		shown = maxItems
	}
	for _, p := range paths[:shown] {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	if len(paths) > maxItems {
		fmt.Fprintf(&b, "... and %d more\n", len(paths)-maxItems)
	}
	return b.String()
}

// optString returns an optional string argument, or "" if not present.
func optString(req mcp.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

// optBool returns an optional boolean argument, or false if not present.
func optBool(req mcp.CallToolRequest, key string) bool {
	return req.GetBool(key, false)
}

// optFloat returns an optional float argument, or the default if not present.
func optFloat(req mcp.CallToolRequest, key string, def float64) float64 {
	return req.GetFloat(key, def)
}

// --- Authorship Handlers ---

func (s *Server) handleCheckpoint(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// kai_checkpoint does NOT require ensureReady — it's filesystem-only
	file := optString(req, "file")
	if file == "" {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	startLine := int(optFloat(req, "start_line", 0))
	endLine := int(optFloat(req, "end_line", 0))
	if startLine <= 0 || endLine <= 0 {
		return mcp.NewToolResultError("start_line and end_line must be positive integers"), nil
	}
	if endLine < startLine {
		endLine = startLine
	}

	action := optString(req, "action")
	if action == "" {
		action = "modify"
	}
	agent := optString(req, "agent")
	if agent == "" {
		agent = "mcp-agent" // generic default
	}
	model := optString(req, "model")

	// Resolve the file to an absolute path to detect cross-project edits
	absFile := file
	if !filepath.IsAbs(file) {
		absFile = filepath.Join(s.workDir, file)
	}
	absFile = filepath.Clean(absFile)

	// Determine which project owns this file and pick the right writer
	writer := s.cpWriter
	relFile := file
	targetProject := s.workDir

	if !strings.HasPrefix(absFile, s.workDir+string(filepath.Separator)) {
		// File is outside our project root — find its .kai/ directory
		if foreignKaiDir := findKaiDir(absFile); foreignKaiDir != "" {
			foreignProject := filepath.Dir(foreignKaiDir)
			writer = authorship.NewCheckpointWriter(foreignKaiDir, s.sessionID)
			targetProject = foreignProject
			// Make the file path relative to the foreign project
			if r, err := filepath.Rel(foreignProject, absFile); err == nil {
				relFile = r
			}
		}
	}

	cp := authorship.CheckpointRecord{
		File:       relFile,
		StartLine:  startLine,
		EndLine:    endLine,
		Action:     action,
		AuthorType: "ai",
		Agent:      agent,
		Model:      model,
		Timestamp:  time.Now().UnixMilli(),
	}

	seq, err := writer.Write(cp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("writing checkpoint: %v", err)), nil
	}

	result := map[string]interface{}{
		"status":     "recorded",
		"session_id": s.sessionID,
		"seq":        seq,
		"file":       relFile,
		"lines":      fmt.Sprintf("%d-%d", startLine, endLine),
	}
	if targetProject != s.workDir {
		result["project"] = targetProject
	}
	return jsonResult(result)
}

// findKaiDir walks up from a file path looking for a .kai/ directory.
// Returns the .kai path if found, empty string otherwise.
func findKaiDir(absFilePath string) string {
	dir := filepath.Dir(absFilePath)
	for {
		candidate := filepath.Join(dir, ".kai")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			// Verify it's a real Kai project (has db.sqlite)
			if _, err := os.Stat(filepath.Join(candidate, "db.sqlite")); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

func (s *Server) handleBlame(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	file := optString(req, "file")
	if file == "" {
		return mcp.NewToolResultError("missing required parameter 'file'"), nil
	}

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	format := optString(req, "format")
	if format == "" {
		format = "summary"
	}

	if format == "summary" {
		summary, err := authorship.BlameFileSummary(s.db, snapID, file)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error computing blame: %v", err)), nil
		}
		if summary.TotalLines == 0 {
			return jsonResult(map[string]interface{}{
				"file":    file,
				"status":  "no_attribution",
				"message": "No authorship data found. Run kai capture after making edits with kai_checkpoint enabled.",
			})
		}
		return jsonResult(summary)
	}

	// "lines" format — return raw ranges
	ranges, err := authorship.Blame(s.db, snapID, file)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error computing blame: %v", err)), nil
	}
	if len(ranges) == 0 {
		return jsonResult(map[string]interface{}{
			"file":    file,
			"status":  "no_attribution",
			"message": "No authorship data found.",
		})
	}

	type rangeOut struct {
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		AuthorType string `json:"author_type"`
		Agent      string `json:"agent,omitempty"`
		Model      string `json:"model,omitempty"`
	}
	out := make([]rangeOut, len(ranges))
	for i, r := range ranges {
		out[i] = rangeOut{
			StartLine:  r.StartLine,
			EndLine:    r.EndLine,
			AuthorType: r.AuthorType,
			Agent:      r.Agent,
			Model:      r.Model,
		}
	}
	return jsonResult(map[string]interface{}{
		"file":   file,
		"ranges": out,
	})
}

func (s *Server) handleStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if result, ok := s.ensureReady(); !ok {
		return result, nil
	}

	snapID, err := s.latestSnapshotID()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	stats, err := authorship.ProjectStats(s.db, snapID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error computing stats: %v", err)), nil
	}

	if stats.TotalLines == 0 {
		return jsonResult(map[string]interface{}{
			"status":  "no_attribution",
			"message": "No authorship data found. Use kai_checkpoint to record AI edits, then run kai capture.",
		})
	}

	return jsonResult(stats)
}

func (s *Server) handleActivity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	w := s.fileWatcher
	s.mu.Unlock()

	if w == nil {
		return jsonResult(map[string]interface{}{
			"status":  "inactive",
			"message": "File watcher is not running. The graph updates on kai capture.",
		})
	}

	entries := w.GetActivity()
	if len(entries) == 0 {
		return jsonResult(map[string]interface{}{
			"status":     "watching",
			"message":    "Watcher active, no recent file changes.",
			"file_count": 0,
		})
	}

	// Group by file, show latest op
	type fileActivity struct {
		Path string `json:"path"`
		Op   string `json:"op"`
		Ago  string `json:"ago"`
	}

	seen := make(map[string]bool)
	var files []fileActivity
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		ago := time.Since(e.Timestamp).Round(time.Second).String()
		files = append(files, fileActivity{Path: e.Path, Op: e.Operation, Ago: ago})
	}

	result := map[string]interface{}{
		"status":     "active",
		"file_count": len(files),
		"files":      files,
	}

	// Include overlap warnings from the server
	s.warningsMu.RLock()
	warnings := s.warnings
	s.warningsMu.RUnlock()
	if len(warnings) > 0 {
		result["warnings"] = warnings
		result["warning_count"] = len(warnings)
	}

	// Include advisory locks from the server
	s.locksMu.RLock()
	locks := s.locks
	s.locksMu.RUnlock()
	if len(locks) > 0 {
		result["locks"] = locks
		result["lock_count"] = len(locks)
	}

	// Include sync conflicts
	s.syncConflictsMu.RLock()
	conflicts := s.syncConflicts
	s.syncConflictsMu.RUnlock()
	if len(conflicts) > 0 {
		result["sync_conflicts"] = conflicts
		result["sync_conflict_count"] = len(conflicts)
	}

	// Include billing warning
	s.billingWarningMu.RLock()
	billingWarning := s.billingWarning
	s.billingWarningMu.RUnlock()
	if billingWarning != "" {
		result["billing_warning"] = billingWarning
	}

	return jsonResult(result)
}

func (s *Server) handleLock(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesStr, err := req.RequireString("files")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'files'"), nil
	}

	if s.remoteClient == nil {
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	var files []string
	for _, f := range strings.Split(filesStr, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			files = append(files, f)
		}
	}

	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}

	acquired, denied, err := s.remoteClient.AcquireLocks(agentName, files)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("lock request failed: %v", err)), nil
	}

	result := map[string]interface{}{
		"acquired": acquired,
	}
	if len(denied) > 0 {
		result["denied"] = denied
	}
	return jsonResult(result)
}

func (s *Server) handleUnlock(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesStr, err := req.RequireString("files")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'files'"), nil
	}

	if s.remoteClient == nil {
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	var files []string
	for _, f := range strings.Split(filesStr, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			files = append(files, f)
		}
	}

	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}

	err = s.remoteClient.ReleaseLocks(agentName, files)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("unlock failed: %v", err)), nil
	}

	return jsonResult(map[string]interface{}{
		"released": files,
	})
}

func (s *Server) handleSync(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.remoteClient == nil {
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}

	resp, err := s.remoteClient.SyncEdges(s.lastEdgeSeq, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
	}

	// Update our sync position
	if resp.LatestSeq > s.lastEdgeSeq {
		s.lastEdgeSeq = resp.LatestSeq
	}

	if len(resp.Entries) == 0 {
		return jsonResult(map[string]interface{}{
			"status":     "up_to_date",
			"latest_seq": resp.LatestSeq,
			"message":    "No changes from other agents since last sync.",
		})
	}

	// Group by agent and file for readable output
	type fileChange struct {
		File    string `json:"file"`
		Added   int    `json:"added,omitempty"`
		Removed int    `json:"removed,omitempty"`
	}
	type agentChanges struct {
		Agent   string       `json:"agent"`
		Actor   string       `json:"actor"`
		Files   []fileChange `json:"files"`
	}

	agentMap := make(map[string]*agentChanges)
	fileMap := make(map[string]map[string]*fileChange) // agent -> file -> change

	for _, e := range resp.Entries {
		ac, ok := agentMap[e.Agent]
		if !ok {
			ac = &agentChanges{Agent: e.Agent, Actor: e.Actor}
			agentMap[e.Agent] = ac
			fileMap[e.Agent] = make(map[string]*fileChange)
		}
		fc, ok := fileMap[e.Agent][e.File]
		if !ok {
			fc = &fileChange{File: e.File}
			fileMap[e.Agent][e.File] = fc
		}
		if e.Action == "add" {
			fc.Added++
		} else {
			fc.Removed++
		}
	}

	var agents []agentChanges
	for _, ac := range agentMap {
		for _, fc := range fileMap[ac.Agent] {
			ac.Files = append(ac.Files, *fc)
		}
		agents = append(agents, *ac)
	}

	return jsonResult(map[string]interface{}{
		"status":      "changes_found",
		"entry_count": len(resp.Entries),
		"latest_seq":  resp.LatestSeq,
		"has_more":    resp.HasMore,
		"agents":      agents,
	})
}

func (s *Server) handleMergeCheck(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesStr, err := req.RequireString("files")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'files'"), nil
	}

	if s.remoteClient == nil {
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	var files []string
	for _, f := range strings.Split(filesStr, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			files = append(files, f)
		}
	}

	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}

	// Sync to get latest state
	syncResp, err := s.remoteClient.SyncEdges(s.lastEdgeSeq, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
	}
	if syncResp.LatestSeq > s.lastEdgeSeq {
		s.lastEdgeSeq = syncResp.LatestSeq
	}

	// Check: did any other agent modify the same files?
	myFiles := make(map[string]bool, len(files))
	for _, f := range files {
		myFiles[f] = true
	}

	type conflictInfo struct {
		File  string `json:"file"`
		Agent string `json:"agent"`
		Actor string `json:"actor"`
		Edges int    `json:"edges_changed"`
	}

	var conflicts []conflictInfo
	var warnings []conflictInfo
	otherFiles := make(map[string]string) // file -> agent

	for _, e := range syncResp.Entries {
		if myFiles[e.File] {
			// Another agent changed the same file
			conflicts = append(conflicts, conflictInfo{
				File: e.File, Agent: e.Agent, Actor: e.Actor, Edges: 1,
			})
		} else {
			otherFiles[e.File] = e.Agent
		}
	}

	// Check 1-hop: did other agents change files that our files depend on?
	s.mu.Lock()
	w := s.fileWatcher
	s.mu.Unlock()
	if w != nil {
		related := w.GetRelatedFiles(files)
		for _, r := range related {
			if agent, ok := otherFiles[r]; ok {
				warnings = append(warnings, conflictInfo{
					File: r, Agent: agent, Edges: 1,
				})
			}
		}
	}

	mergeable := len(conflicts) == 0
	result := map[string]interface{}{
		"mergeable": mergeable,
	}
	if mergeable {
		result["message"] = "No conflicts detected. Safe to merge."
	} else {
		result["message"] = "Conflicts detected — other agents modified the same files."
		result["conflicts"] = conflicts
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
		result["warning_message"] = "Other agents changed files related to yours (dependencies/dependents)."
	}
	return jsonResult(result)
}

func (s *Server) handleLiveSync(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'action' (use 'on' or 'off')"), nil
	}

	if s.remoteClient == nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] live_sync(%s) called but no remote client configured\n", action)
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}

	switch action {
	case "on":
		// Already subscribed?
		if s.syncChannelID != "" {
			return jsonResult(map[string]interface{}{
				"status":  "already_subscribed",
				"channel": s.syncChannelID,
			})
		}

		var files []string
		if filesStr := optString(req, "files"); filesStr != "" {
			for _, f := range strings.Split(filesStr, ",") {
				f = strings.TrimSpace(f)
				if f != "" {
					files = append(files, f)
				}
			}
		}

		channelID, synced, err := s.doSubscribeSync(files)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[kai-sync] subscribe failed: %v\n", err)
			return mcp.NewToolResultError(fmt.Sprintf("subscribe failed: %v", err)), nil
		}
		s.saveSyncState(files)

		result := map[string]interface{}{
			"status":  "subscribed",
			"channel": channelID,
			"message": "Live sync active. File changes from other agents will be applied automatically.",
		}
		if synced > 0 {
			result["initial_sync"] = synced
			result["message"] = fmt.Sprintf("Live sync active. Applied %d file(s) from server.", synced)
		}
		if len(files) > 0 {
			result["watching"] = files
		} else {
			result["watching"] = "all files"
		}
		return jsonResult(result)

	case "status":
		if s.syncChannelID == "" {
			status := map[string]interface{}{"status": "off"}
			if s.remoteClient != nil {
				status["remote"] = s.remoteClient.BaseURL
			} else {
				status["remote"] = nil
			}
			return jsonResult(status)
		}
		s.syncBaseMu.RLock()
		baseCount := len(s.syncBase)
		s.syncBaseMu.RUnlock()
		s.syncConflictsMu.RLock()
		conflictCount := len(s.syncConflicts)
		s.syncConflictsMu.RUnlock()
		return jsonResult(map[string]interface{}{
			"status":         "on",
			"channel":        s.syncChannelID,
			"remote":         s.remoteClient.BaseURL,
			"agent":          s.syncAgentName,
			"polling":        s.syncStopSSE != nil,
			"last_poll_time": syncLastPollTime,
			"tracked_files":  baseCount,
			"conflicts":      conflictCount,
		})

	case "off":
		if s.syncChannelID == "" {
			return jsonResult(map[string]interface{}{
				"status":  "not_subscribed",
				"message": "Live sync is not active.",
			})
		}

		if s.syncStopSSE != nil {
			close(s.syncStopSSE)
			s.syncStopSSE = nil
		}
		s.remoteClient.UnsubscribeSync(s.syncChannelID)
		s.syncChannelID = ""
		s.clearSyncState()

		// Auto-capture to seal pending sync log and checkpoints
		if synclog.CountPendingSyncLogs(s.kaiDir) > 0 || authorship.CountPendingCheckpoints(s.kaiDir) > 0 {
			go func() {
				cmd := exec.Command("kai", "capture")
				cmd.Dir = s.workDir
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "[kai-sync] auto-capture on sync-off failed: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "[kai-sync] auto-capture on sync-off succeeded\n")
				}
			}()
		}

		return jsonResult(map[string]interface{}{
			"status":  "unsubscribed",
			"message": "Live sync disabled.",
		})

	default:
		return mcp.NewToolResultError("action must be 'on', 'off', or 'status'"), nil
	}
}

// syncStatePath returns the path to the persisted live-sync state file.
// handleCheckpointNow writes an agent-milestone marker into sync_events so
// the live-sync log has a semantic boundary the reviewer / compactor can
// key off of. Does not run kai capture — that's intentional. The marker
// travels alongside normal sync pushes (same endpoint, same channel) but
// with reason="agent_milestone" and a caller-provided label.
//
// Phase 2c: lays the foundation for kai review --live (phase 2d) and
// Compact(from_seq, to_seq) (phase 2e) to use named checkpoints as
// natural boundaries.
func (s *Server) handleCheckpointNow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	label, err := req.RequireString("label")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter 'label'"), nil
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return mcp.NewToolResultError("label must not be empty"), nil
	}

	// Optional structured trust assertion
	assertVal := optString(req, "assert")
	planHash := optString(req, "plan_hash")

	if assertVal != "" {
		valid := false
		for _, a := range graph.ValidAssertions {
			if a == assertVal {
				valid = true
				break
			}
		}
		if !valid {
			return mcp.NewToolResultError(fmt.Sprintf(
				"invalid assert value %q: must be one of %s",
				assertVal, strings.Join(graph.ValidAssertions, ", "),
			)), nil
		}
	}
	if planHash != "" && assertVal == "" {
		return mcp.NewToolResultError("plan_hash requires assert field"), nil
	}
	if planHash != "" && assertVal != "tests-pass" {
		return mcp.NewToolResultError("plan_hash only valid with assert=tests-pass"), nil
	}

	if s.remoteClient == nil {
		return mcp.NewToolResultError("no remote server configured (need git remote 'origin')"), nil
	}

	agentName := s.syncAgentName
	if agentName == "" {
		agentName = s.agentName
		if agentName == "" {
			agentName = "mcp-client"
		}
	}

	// Push milestone via dedicated method that supports assert/plan_hash fields
	if err := s.remoteClient.SyncPushMilestone(agentName, s.syncChannelID, label, assertVal, planHash); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("checkpoint push failed: %v", err)), nil
	}

	fmt.Fprintf(os.Stderr, "[kai-sync] checkpoint: agent=%s label=%q assert=%s\n", agentName, label, assertVal)

	// kai↔git bridge: if enabled for this repo, translate the milestone into
	// a git commit so git-only teammates see meaningful history. Best-effort:
	// no git repo, no bridge, or kai-binary-not-on-PATH all result in silent
	// skip. The bridge subcommand sets KAI_BRIDGE_INPROGRESS=1 internally so
	// we don't re-enter pre-commit / pre-push here.
	bridgeSentinel := filepath.Join(s.workDir, ".kai", "bridge-enabled")
	gitDir := filepath.Join(s.workDir, ".git")
	if _, err := os.Stat(bridgeSentinel); err == nil {
		if _, err := os.Stat(gitDir); err == nil {
			bridgeArgs := []string{"bridge", "milestone", "--label", label}
			if assertVal != "" {
				bridgeArgs = append(bridgeArgs, "--assert", assertVal)
			}
			if planHash != "" {
				bridgeArgs = append(bridgeArgs, "--plan-hash", planHash)
			}
			cmd := exec.Command("kai", bridgeArgs...)
			cmd.Dir = s.workDir
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "[kai-bridge] milestone→commit failed (non-fatal): %v\n%s\n", err, out)
			} else {
				fmt.Fprintf(os.Stderr, "[kai-bridge] milestone→commit: %q\n", label)
			}
		}
	}

	result := map[string]interface{}{
		"status": "marked",
		"reason": "agent_milestone",
		"label":  label,
		"agent":  agentName,
	}
	if assertVal != "" {
		result["assert"] = assertVal
	}
	if planHash != "" {
		result["plan_hash"] = planHash
	}
	return jsonResult(result)
}

func (s *Server) syncStatePath() string {
	return filepath.Join(s.kaiDir, "sync-state.json")
}

type persistedSyncState struct {
	Enabled bool     `json:"enabled"`
	Files   []string `json:"files,omitempty"`
	LastSeq int64    `json:"last_seq,omitempty"` // tip of sync_events the client has caught up through
}

func (s *Server) saveSyncState(files []string) {
	// Preserve LastSeq if already persisted — callers of saveSyncState
	// only know about enable/disable, not replay progress.
	var lastSeq int64
	if prev, ok := s.loadSyncState(); ok {
		lastSeq = prev.LastSeq
	}
	data, err := json.Marshal(persistedSyncState{Enabled: true, Files: files, LastSeq: lastSeq})
	if err != nil {
		return
	}
	_ = os.WriteFile(s.syncStatePath(), data, 0644)
}

// saveSyncSeq advances the persisted last_seq after the client has applied
// replay events up to that point. Kept separate from saveSyncState so the
// replay phase doesn't have to reconstruct files list.
func (s *Server) saveSyncSeq(seq int64) {
	prev, _ := s.loadSyncState()
	state := persistedSyncState{Enabled: true, LastSeq: seq}
	if prev != nil {
		state.Files = prev.Files
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.syncStatePath(), data, 0644)
}

func (s *Server) clearSyncState() {
	_ = os.Remove(s.syncStatePath())
}

func (s *Server) loadSyncState() (*persistedSyncState, bool) {
	data, err := os.ReadFile(s.syncStatePath())
	if err != nil {
		return nil, false
	}
	var st persistedSyncState
	if json.Unmarshal(data, &st) != nil || !st.Enabled {
		return nil, false
	}
	return &st, true
}

// doSubscribeSync performs the subscribe + initial-pull + SSE-start flow.
// Called from handleLiveSync("on") and from auto-resume in startWatcher.
// Assumes s.remoteClient is non-nil and s.syncChannelID is empty.
func (s *Server) doSubscribeSync(files []string) (channelID string, synced int, err error) {
	agentName := s.agentName
	if agentName == "" {
		agentName = "mcp-client"
	}
	resp, err := s.remoteClient.SubscribeSync(agentName, s.remoteClient.Actor, files)
	if err != nil {
		return "", 0, err
	}
	fmt.Fprintf(os.Stderr, "[kai-sync] subscribed: channel=%s agent=%s\n", resp.ChannelID, agentName)

	s.syncChannelID = resp.ChannelID
	s.syncStopSSE = make(chan struct{})
	s.syncBaseMu.Lock()
	s.syncBase = make(map[string][]byte)
	s.syncBaseMu.Unlock()

	synced = s.syncInitialPull()

	// Replay durable sync events we missed while offline. Reads sinceSeq
	// from persisted state (0 for first-time subscribers), applies events
	// with a seq > sinceSeq via applySyncContent, and persists the new tip.
	// Best-effort: if the server doesn't have the replay endpoint yet or
	// the call fails, fall back to SSE-only behavior.
	var replaySeq int64
	if prev, ok := s.loadSyncState(); ok {
		replaySeq = prev.LastSeq
	}
	if replayResp, err := s.remoteClient.SyncReplaySince(replaySeq, s.syncAgentName, 500); err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] replay skipped (since=%d): %v\n", replaySeq, err)
	} else if replayResp != nil {
		applied := 0
		for _, ev := range replayResp.Events {
			if ev.File == "" || ev.Content == "" {
				continue
			}
			raw, decErr := base64.StdEncoding.DecodeString(ev.Content)
			if decErr != nil || len(raw) == 0 {
				continue
			}
			localPath := fromGitRelativePath(s.workDir, ev.File)
			absPath := filepath.Join(s.workDir, localPath)
			if !strings.HasPrefix(absPath, s.workDir) {
				continue
			}
			s.applySyncContent(localPath, absPath, raw, ev.Agent)
			applied++
		}
		if applied > 0 {
			fmt.Fprintf(os.Stderr, "[kai-sync] replay applied %d event(s), tip=%d\n", applied, replayResp.LatestSeq)
		}
		if replayResp.LatestSeq > replaySeq {
			s.saveSyncSeq(replayResp.LatestSeq)
		}
	}

	go s.readSSEEvents(resp.ChannelID)
	return resp.ChannelID, synced, nil
}

// readSSEEvents connects to the SSE stream and applies incoming file changes to disk.
func (s *Server) readSSEEvents(channelID string) {
	if s.remoteClient == nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] polling goroutine aborted: no remote client\n")
		return
	}

	fmt.Fprintf(os.Stderr, "[kai-sync] SSE goroutine started: channel=%s\n", channelID)
	defer fmt.Fprintf(os.Stderr, "[kai-sync] SSE goroutine stopped: channel=%s\n", channelID)

	for {
		select {
		case <-s.syncStopSSE:
			return
		default:
		}

		url := fmt.Sprintf("%s%s/v1/sync/events?channel=%s",
			s.remoteClient.BaseURL, s.remoteClient.RepoPath(), s.syncChannelID)
		s.connectSSE(url, s.syncChannelID)

		// If we get here, the connection dropped. Retry after a delay.
		select {
		case <-s.syncStopSSE:
			return
		case <-time.After(5 * time.Second):
			fmt.Fprintf(os.Stderr, "[kai-sync] SSE reconnecting...\n")
		}
	}
}

// syncLastPollTime tracks when we last polled for sync changes.
var syncLastPollTime int64

// syncPollCount tracks how many polls have been made (for periodic alive logging).
var syncPollCount int64

// pollSyncChanges fetches file content pushed by other agents.
// Polls GET /v1/sync/files which works through proxies (unlike SSE).
func (s *Server) pollSyncChanges(agentName string) {
	syncPollCount++
	url := fmt.Sprintf("%s%s/v1/sync/files?since=%d&agent=%s",
		s.remoteClient.BaseURL, s.remoteClient.RepoPath(), syncLastPollTime, s.syncAgentName)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: failed to create request: %v\n", err)
		return
	}
	if s.remoteClient.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.remoteClient.AuthToken)
	}

	resp, err := s.remoteClient.HTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: request failed: %v (url=%s)\n", err, url)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: HTTP %d from %s\n", resp.StatusCode, url)
		return
	}

	var result struct {
		Files []struct {
			Path    string `json:"path"`
			Agent   string `json:"agent"`
			Content string `json:"content"`
			Time    int64  `json:"time"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: JSON decode error: %v\n", err)
		return
	}

	// Log alive heartbeat every 12 polls (~1 minute)
	if syncPollCount%12 == 0 {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: alive (poll #%d, since=%d)\n", syncPollCount, syncLastPollTime)
	}

	if len(result.Files) > 0 {
		fmt.Fprintf(os.Stderr, "[kai-sync] poll: %d file(s) from server\n", len(result.Files))
	}

	for _, f := range result.Files {
		if f.Time > syncLastPollTime {
			syncLastPollTime = f.Time
		}

		// Decode and apply
		content, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[kai-sync] poll: base64 decode failed for %s: %v\n", f.Path, err)
			continue
		}
		if len(content) == 0 {
			fmt.Fprintf(os.Stderr, "[kai-sync] poll: empty content for %s, skipping\n", f.Path)
			continue
		}

		// Convert git-relative path to local workDir-relative path
		localPath := fromGitRelativePath(s.workDir, f.Path)
		absPath := filepath.Join(s.workDir, localPath)
		if !strings.HasPrefix(absPath, s.workDir) {
			continue
		}

		s.applySyncContent(localPath, absPath, content, f.Agent)
	}
}

// applySyncContent is the single path by which peer-originated file bytes
// land on local disk. Called from:
//   - the SSE real-time handler (handleSyncFileChange)
//   - the /v1/sync/replay catch-up loop in doSubscribeSync
//   - the legacy polling fallback (pollSyncChanges)
//
// All three must share this path so the guards (feedback-loop suppression,
// peer-attribution checkpoints, synclog audit entries) can't diverge.
func (s *Server) applySyncContent(relPath, absPath string, incoming []byte, agent string) {
	local, localErr := os.ReadFile(absPath)

	if localErr == nil && bytes.Equal(local, incoming) {
		return // identical — nothing to do and nothing to record
	}

	s.syncBaseMu.RLock()
	base := s.syncBase[relPath]
	s.syncBaseMu.RUnlock()

	var toWrite []byte

	if localErr != nil || base == nil {
		toWrite = incoming
	} else if bytes.Equal(local, base) {
		toWrite = incoming
	} else {
		lang := detectSyncLang(relPath)
		if lang != "" {
			mergeResult, mergeErr := merge.Merge3Way(base, local, incoming, lang)
			if mergeErr == nil && mergeResult.Success {
				if merged, ok := mergeResult.Files["file"]; ok {
					toWrite = merged
					fmt.Fprintf(os.Stderr, "[kai-sync] merged %s (auto-resolved)\n", relPath)
					s.syncLogWriter.Write(synclog.SyncLogEntry{
						Event:     synclog.EventMerge,
						File:      relPath,
						Agent:     s.syncAgentName,
						PeerAgent: agent,
						Channel:   s.syncChannelID,
						Timestamp: time.Now().UnixMilli(),
						Detail:    "3-way merge auto-resolved",
					})
				}
			}
		} else {
			// No semantic merger for this extension (json, yaml, md, sh, etc.).
			// Try a naive line-based 3-way merge; if the peer and the local
			// edited disjoint line ranges we can still apply cleanly. This
			// covers the common case of two agents editing different sections
			// of a package.json or config file.
			if merged, ok := naiveLineMerge3(base, local, incoming); ok {
				toWrite = merged
				fmt.Fprintf(os.Stderr, "[kai-sync] line-merged %s (no semantic merger for ext)\n", relPath)
				s.syncLogWriter.Write(synclog.SyncLogEntry{
					Event:     synclog.EventMerge,
					File:      relPath,
					Agent:     s.syncAgentName,
					PeerAgent: agent,
					Channel:   s.syncChannelID,
					Timestamp: time.Now().UnixMilli(),
					Detail:    "line-based 3-way merge",
				})
			}
		}
		if toWrite == nil {
			fmt.Fprintf(os.Stderr, "[kai-sync] conflict on %s from %s — local edits preserved\n", relPath, agent)
			s.syncConflictsMu.Lock()
			s.syncConflicts = append(s.syncConflicts, syncConflictInfo{
				File:    relPath,
				Agent:   agent,
				Time:    time.Now().Format(time.RFC3339),
				Message: "Both you and " + agent + " edited the same function. Your local edits were preserved.",
			})
			// Keep only last 10 conflicts
			if len(s.syncConflicts) > 10 {
				s.syncConflicts = s.syncConflicts[len(s.syncConflicts)-10:]
			}
			s.syncConflictsMu.Unlock()
			s.syncLogWriter.Write(synclog.SyncLogEntry{
				Event:     synclog.EventConflict,
				File:      relPath,
				Agent:     s.syncAgentName,
				PeerAgent: agent,
				Channel:   s.syncChannelID,
				Timestamp: time.Now().UnixMilli(),
				Detail:    "local edits preserved",
			})
			// Record a conflict-style peer checkpoint: the lines the peer
			// would have changed from our local view. Surfaces in
			// kai blame --conflicts without actually landing the peer's edit.
			s.writePeerCheckpoint(relPath, local, incoming, agent, "conflict")
			s.syncBaseMu.Lock()
			if s.syncBase != nil {
				s.syncBase[relPath] = incoming
			}
			s.syncBaseMu.Unlock()
			return
		}
	}

	os.MkdirAll(filepath.Dir(absPath), 0755)
	if err := os.WriteFile(absPath, toWrite, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] failed to write %s: %v\n", relPath, err)
		return
	}

	// Mark as sync-written so the watcher doesn't push it back (feedback
	// loop prevention). Without this the watcher's next tick picks up the
	// write and pushes it straight back to the server.
	s.markSyncWritten(relPath)

	// Emit peer-attribution checkpoints so kai blame reflects who actually
	// wrote each line *now*, without waiting for kai capture on this side.
	// Use local→toWrite: the lines that changed on disk, which are the lines
	// the peer contributed (either whole-file apply or the peer's portion of
	// an auto-merge).
	s.writePeerCheckpoint(relPath, local, toWrite, agent, "modify")

	s.syncBaseMu.Lock()
	if s.syncBase != nil {
		s.syncBase[relPath] = incoming
	}
	s.syncBaseMu.Unlock()
	s.syncLogWriter.Write(synclog.SyncLogEntry{
		Event:     synclog.EventReceive,
		File:      relPath,
		Agent:     s.syncAgentName,
		PeerAgent: agent,
		Channel:   s.syncChannelID,
		Timestamp: time.Now().UnixMilli(),
	})
	fmt.Fprintf(os.Stderr, "[kai-sync] applied %s from %s\n", relPath, agent)
}

// writePeerCheckpoint records an authorship checkpoint attributing the line
// ranges that changed from old→new to the given peer agent. Used by the
// live-sync receive path so peer-originated edits appear in kai blame
// immediately, without waiting for kai capture on the receiving side.
func (s *Server) writePeerCheckpoint(relPath string, old, new []byte, agent, action string) {
	if s.cpWriter == nil || agent == "" {
		return
	}
	ranges := authorship.DiffLineRanges(old, new)
	if len(ranges) == 0 {
		return
	}
	ts := time.Now().UnixMilli()
	for _, r := range ranges {
		s.cpWriter.Write(authorship.CheckpointRecord{
			File:       relPath,
			StartLine:  r.Start,
			EndLine:    r.End,
			Action:     action,
			AuthorType: "ai",
			Agent:      agent,
			Timestamp:  ts,
			PeerOrigin: true,
		})
	}
}

func (s *Server) connectSSE(url, channelID string) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Accept-Encoding", "identity") // Disable gzip for streaming
	if s.remoteClient.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.remoteClient.AuthToken)
	}

	fmt.Fprintf(os.Stderr, "[kai-sync] connecting SSE to %s\n", url)
	sseClient := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}
	resp, err := sseClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] SSE connect failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		// Channel expired or server restarted — re-subscribe
		fmt.Fprintf(os.Stderr, "[kai-sync] channel expired, re-subscribing...\n")
		resp.Body.Close()
		agentName := s.agentName
		if agentName == "" {
			agentName = "mcp-client"
		}
		newResp, err := s.remoteClient.SubscribeSync(agentName, s.remoteClient.Actor, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[kai-sync] re-subscribe failed: %v\n", err)
			return
		}
		s.syncChannelID = newResp.ChannelID
		fmt.Fprintf(os.Stderr, "[kai-sync] re-subscribed on channel %s\n", newResp.ChannelID)
		return // will reconnect with new channel on next retry
	}
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "[kai-sync] SSE status: %d\n", resp.StatusCode)
		return
	}
	fmt.Fprintf(os.Stderr, "[kai-sync] SSE connected\n")

	scanner := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for large file content
	scanner.Buffer(make([]byte, 0), 10*1024*1024) // 10MB max
	var eventType, eventData string

	for {
		select {
		case <-s.syncStopSSE:
			return
		default:
		}

		if !scanner.Scan() {
			fmt.Fprintf(os.Stderr, "[kai-sync] SSE connection closed\n")
			return
		}
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		} else if line == "" && eventData != "" {
			// End of event — process it
			if eventType == "file_change" {
				s.handleSyncFileChange(eventData)
			}
			eventType = ""
			eventData = ""
		}
	}
}

// handleSyncFileChange applies a received file change to disk.
// handleSyncFileChange is the SSE real-time receive handler. Decodes the
// event payload and delegates to applySyncContent — the single receive
// path shared with replay and polling. Historically this contained its
// own duplicated 3-way-merge implementation which drifted out of sync
// with applySyncContent and notably was missing markSyncWritten, causing
// a feedback loop where the watcher would re-push every received file
// on every 30s tick.
func (s *Server) handleSyncFileChange(data string) {
	var event struct {
		Agent   string `json:"agent"`
		File    string `json:"file"`
		Content string `json:"content"` // base64
		Time    int64  `json:"time"`    // unix ms
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}
	if event.Content == "" || event.File == "" {
		return
	}

	incoming, err := base64.StdEncoding.DecodeString(event.Content)
	if err != nil {
		return
	}

	// Convert git-relative path from the event to local workDir-relative.
	localPath := fromGitRelativePath(s.workDir, event.File)
	absPath := filepath.Join(s.workDir, localPath)

	// Safety: don't write outside workDir.
	if !strings.HasPrefix(absPath, s.workDir) {
		return
	}

	s.applySyncContent(localPath, absPath, incoming, event.Agent)
}

// syncInitialPull compares the remote snapshot against local files
// and writes any files that differ. Uses the local graph DB which
// is populated by kai pull / kai push.
func (s *Server) syncInitialPull() int {
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		return 0
	}

	// Step 1: Snapshot the LOCAL state before pulling.
	// This is the "base" for 3-way merge — the common ancestor.
	localDigests := make(map[string]string) // path -> digest before pull
	localSnapID, _ := s.latestSnapshotID()
	if localSnapID != nil {
		edges, _ := db.GetEdges(localSnapID, graph.EdgeHasFile)
		for _, edge := range edges {
			node, _ := db.GetNode(edge.Dst)
			if node == nil {
				continue
			}
			path, _ := node.Payload["path"].(string)
			digest, _ := node.Payload["digest"].(string)
			if path != "" && digest != "" {
				localDigests[path] = digest
			}
		}
	}

	// Step 2: Pull the latest snapshot from the server.
	if s.remoteClient != nil {
		fmt.Fprintf(os.Stderr, "[kai-sync] pulling latest snapshot from server...\n")
		cmd := exec.Command("kai", "pull", "--force")
		cmd.Dir = s.workDir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[kai-sync] pull failed (continuing with local snapshot): %v\n", err)
		}
		// Reopen DB to pick up pulled data
		// (kai pull updates refs but the DB handle is already open, so refs are visible)
	}

	// Step 3: Get the remote snapshot (now snap.latest after pull)
	remoteSnapID, err := s.latestSnapshotID()
	if err != nil {
		return 0
	}

	edges, err := db.GetEdges(remoteSnapID, graph.EdgeHasFile)
	if err != nil {
		return 0
	}

	synced := 0
	for _, edge := range edges {
		node, err := db.GetNode(edge.Dst)
		if err != nil || node == nil {
			continue
		}

		path, _ := node.Payload["path"].(string)
		remoteDigest, _ := node.Payload["digest"].(string)
		if path == "" || remoteDigest == "" {
			continue
		}

		absPath := filepath.Join(s.workDir, path)
		localContent, readErr := os.ReadFile(absPath)

		if readErr != nil {
			// File doesn't exist locally — extract from object store
			content, err := db.ReadObject(remoteDigest)
			if err != nil || len(content) == 0 {
				continue
			}
			os.MkdirAll(filepath.Dir(absPath), 0755)
			if err := os.WriteFile(absPath, content, 0644); err == nil {
				s.markSyncWritten(path)
				fmt.Fprintf(os.Stderr, "[kai-sync] initial: wrote %s (new file)\n", path)
				synced++
			}
			continue
		}

		// File exists — check if remote content matches local
		localFileDigest := fmt.Sprintf("%x", blake3Sum(localContent))
		if localFileDigest == remoteDigest {
			continue // same content
		}

		// Content differs. Read remote content.
		remoteContent, err := db.ReadObject(remoteDigest)
		if err != nil || len(remoteContent) == 0 {
			continue
		}

		// Check if local file was modified vs the pre-pull snapshot (the base).
		baseDigest := localDigests[path]
		if baseDigest == localFileDigest {
			// Local file matches the base (user B didn't edit this file).
			// Safe to overwrite with remote content.
			os.WriteFile(absPath, remoteContent, 0644)
			s.markSyncWritten(path)
			fmt.Fprintf(os.Stderr, "[kai-sync] initial: updated %s\n", path)
			synced++
			continue
		}

		// Local file was modified by user B AND remote is different.
		// Need 3-way merge: base (pre-pull snapshot) vs local vs remote.
		var baseContent []byte
		if baseDigest != "" {
			baseContent, _ = db.ReadObject(baseDigest)
		}

		if baseContent != nil {
			// 3-way merge
			lang := detectSyncLang(path)
			if lang != "" {
				mergeResult, mergeErr := merge.Merge3Way(baseContent, localContent, remoteContent, lang)
				if mergeErr == nil && mergeResult.Success {
					if merged, ok := mergeResult.Files["file"]; ok {
						os.WriteFile(absPath, merged, 0644)
						s.markSyncWritten(path)
						fmt.Fprintf(os.Stderr, "[kai-sync] initial: merged %s (auto-resolved)\n", path)
						s.syncLogWriter.Write(synclog.SyncLogEntry{
							Event:     synclog.EventMerge,
							File:      path,
							Agent:     s.syncAgentName,
							PeerAgent: "server",
							Channel:   s.syncChannelID,
							Timestamp: time.Now().UnixMilli(),
							Detail:    "initial sync 3-way merge",
						})
						synced++
						continue
					}
				}
			}
			// Merge failed — preserve local
			fmt.Fprintf(os.Stderr, "[kai-sync] initial: conflict on %s — local edits preserved\n", path)
			s.syncConflictsMu.Lock()
			s.syncConflicts = append(s.syncConflicts, syncConflictInfo{
				File:    path,
				Agent:   "server",
				Time:    time.Now().Format(time.RFC3339),
				Message: "Conflict during initial sync. Your local edits were preserved.",
			})
			s.syncConflictsMu.Unlock()
		} else {
			// No base available — preserve local, don't clobber
			fmt.Fprintf(os.Stderr, "[kai-sync] initial: conflict on %s (no base) — local edits preserved\n", path)
			s.syncConflictsMu.Lock()
			s.syncConflicts = append(s.syncConflicts, syncConflictInfo{
				File:    path,
				Agent:   "server",
				Time:    time.Now().Format(time.RFC3339),
				Message: "File differs from server but no common base found. Your local edits were preserved.",
			})
			s.syncConflictsMu.Unlock()
		}
	}

	return synced
}

// blake3Sum computes a blake3 hash.
func blake3Sum(data []byte) []byte {
	h := blake3.Sum256(data)
	return h[:]
}

// markSyncWritten records that a file was written by the sync system.
// The push code checks this to avoid pushing sync-received files back to the server.
func (s *Server) markSyncWritten(path string) {
	s.syncWrittenMu.Lock()
	if s.syncWritten == nil {
		s.syncWritten = make(map[string]time.Time)
	}
	s.syncWritten[path] = time.Now()
	s.syncWrittenMu.Unlock()
}

// isSyncWritten returns true if the file was written by sync and hasn't been
// modified by the user since. Compares sync write time against file mtime.
func (s *Server) isSyncWritten(path string) bool {
	s.syncWrittenMu.Lock()
	defer s.syncWrittenMu.Unlock()
	if s.syncWritten == nil {
		return false
	}
	syncTime, ok := s.syncWritten[path]
	if !ok {
		return false
	}
	// Expire after 60 seconds regardless
	if time.Since(syncTime) > 60*time.Second {
		delete(s.syncWritten, path)
		return false
	}
	// Check if the file was modified AFTER the sync write — if so, it's a real edit
	absPath := filepath.Join(s.workDir, path)
	info, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	// If file mtime is more than 1 second after sync write, user edited it
	if info.ModTime().After(syncTime.Add(time.Second)) {
		delete(s.syncWritten, path)
		return false
	}
	return true
}

type syncConflictInfo struct {
	File    string `json:"file"`
	Agent   string `json:"agent"`
	Time    string `json:"time"`
	Message string `json:"message"`
}

// toGitRelativePath converts a workDir-relative path to a git-root-relative path.
// This ensures all clones of the same repo use the same file paths in sync.
func toGitRelativePath(workDir, relPath string) string {
	absPath := filepath.Join(workDir, relPath)

	// Walk up from absPath to find .git
	dir := filepath.Dir(absPath)
	for dir != "/" && dir != "." {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			// Found git root
			gitRel, err := filepath.Rel(dir, absPath)
			if err == nil {
				return filepath.ToSlash(gitRel)
			}
			break
		}
		dir = filepath.Dir(dir)
	}

	// Fallback: return as-is
	return relPath
}

// fromGitRelativePath converts a git-root-relative path to a workDir-relative path.
func fromGitRelativePath(workDir, gitRelPath string) string {
	// Find git root from workDir
	dir := workDir
	for dir != "/" && dir != "." {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			absPath := filepath.Join(dir, gitRelPath)
			rel, err := filepath.Rel(workDir, absPath)
			if err == nil {
				return filepath.ToSlash(rel)
			}
			break
		}
		dir = filepath.Dir(dir)
	}
	return gitRelPath
}

// detectSyncLang maps file path to a language the merge engine supports.
func detectSyncLang(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs":
		return "js"
	case ".ts", ".tsx":
		return "ts"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	}
	return ""
}

// naiveLineMerge3 performs a line-based 3-way merge. It returns the merged
// bytes and true when local and incoming edited disjoint hunks relative to
// base. If either side modified the same base line range, it returns ok=false
// so the caller can fall through to conflict handling.
//
// Algorithm: diff base→local and base→incoming. If the changed line-ranges
// don't overlap, apply both sets of changes to base. Not a full LCS; we use
// a simple common-prefix / common-suffix trim to isolate the changed region
// on each side — sufficient for the common case (two agents editing different
// sections of a package.json / yaml / markdown file).
func naiveLineMerge3(base, local, incoming []byte) ([]byte, bool) {
	// Fast path: one side unchanged.
	if bytes.Equal(base, local) {
		return incoming, true
	}
	if bytes.Equal(base, incoming) {
		return local, true
	}
	if bytes.Equal(local, incoming) {
		return local, true
	}

	bLines := splitLinesKeepNL(base)
	lLines := splitLinesKeepNL(local)
	iLines := splitLinesKeepNL(incoming)

	lStart, lEnd, lNew := diffRange(bLines, lLines)
	iStart, iEnd, iNew := diffRange(bLines, iLines)

	// Disjoint hunks: local's changed base-range ends before incoming's starts,
	// or vice versa. (Using half-open [start, end).)
	if lEnd <= iStart {
		// local first, then incoming
		out := make([][]byte, 0, len(bLines)+len(lNew)+len(iNew))
		out = append(out, bLines[:lStart]...)
		out = append(out, lNew...)
		out = append(out, bLines[lEnd:iStart]...)
		out = append(out, iNew...)
		out = append(out, bLines[iEnd:]...)
		return bytes.Join(out, nil), true
	}
	if iEnd <= lStart {
		out := make([][]byte, 0, len(bLines)+len(lNew)+len(iNew))
		out = append(out, bLines[:iStart]...)
		out = append(out, iNew...)
		out = append(out, bLines[iEnd:lStart]...)
		out = append(out, lNew...)
		out = append(out, bLines[lEnd:]...)
		return bytes.Join(out, nil), true
	}
	return nil, false
}

// splitLinesKeepNL splits bytes into lines, keeping the trailing newline on
// each line. A final line without newline is returned as its own element.
func splitLinesKeepNL(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// diffRange returns the half-open range [start, end) of base lines that were
// changed to produce other, plus the replacement line slice. Computed by
// trimming the common prefix and suffix.
func diffRange(base, other [][]byte) (int, int, [][]byte) {
	n, m := len(base), len(other)
	prefix := 0
	for prefix < n && prefix < m && bytes.Equal(base[prefix], other[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < n-prefix && suffix < m-prefix && bytes.Equal(base[n-1-suffix], other[m-1-suffix]) {
		suffix++
	}
	return prefix, n - suffix, other[prefix : m-suffix]
}
