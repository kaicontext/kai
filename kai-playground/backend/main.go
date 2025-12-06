// Package main provides the kai playground backend server.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sessionTimeout    = 30 * time.Minute
	maxSessionsPerIP  = 5
	cleanupInterval   = 5 * time.Minute
)

// Session represents a user's sandbox session.
type Session struct {
	ID        string
	Dir       string
	CreatedAt time.Time
	LastUsed  time.Time
	mu        sync.Mutex
}

// SessionManager manages sandbox sessions.
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	kaiBin   string
}

// NewSessionManager creates a new session manager.
func NewSessionManager(kaiBin string) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		kaiBin:   kaiBin,
	}
	go sm.cleanupLoop()
	return sm
}

// CreateSession creates a new sandbox session.
func (sm *SessionManager) CreateSession(example string) (*Session, error) {
	id := generateID()

	// Create temp directory
	dir, err := os.MkdirTemp("", "kai-playground-"+id+"-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	// Copy example project if specified
	if example != "" {
		if err := copyExampleProject(example, dir); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("copying example: %w", err)
		}
	}

	session := &Session{
		ID:        id,
		Dir:       dir,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
	}

	sm.mu.Lock()
	sm.sessions[id] = session
	sm.mu.Unlock()

	log.Printf("Created session %s in %s", id, dir)
	return session, nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(id string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[id]
}

// DeleteSession removes and cleans up a session.
func (sm *SessionManager) DeleteSession(id string) {
	sm.mu.Lock()
	session, ok := sm.sessions[id]
	if ok {
		delete(sm.sessions, id)
	}
	sm.mu.Unlock()

	if ok && session.Dir != "" {
		os.RemoveAll(session.Dir)
		log.Printf("Deleted session %s", id)
	}
}

// cleanupLoop periodically removes expired sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		var toDelete []string
		for id, session := range sm.sessions {
			if now.Sub(session.LastUsed) > sessionTimeout {
				toDelete = append(toDelete, id)
			}
		}
		sm.mu.Unlock()

		for _, id := range toDelete {
			sm.DeleteSession(id)
		}
	}
}

// parseCommand splits a command string respecting quoted strings.
func parseCommand(command string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range command {
		switch {
		case (r == '"' || r == '\'') && !inQuote:
			inQuote = true
			quoteChar = r
		case r == quoteChar && inQuote:
			inQuote = false
			quoteChar = 0
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// RunCommand executes a kai command in the session's sandbox.
func (sm *SessionManager) RunCommand(session *Session, command string) (string, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.LastUsed = time.Now()

	// Parse command respecting quotes
	parts := parseCommand(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	// Check if command uses shell operators that require sh -c
	needsShell := strings.ContainsAny(command, ">|&;<")

	// Only allow kai commands or safe shell commands
	var cmd *exec.Cmd
	switch parts[0] {
	case "kai":
		args := parts[1:]
		cmd = exec.Command(sm.kaiBin, args...)
	case "ls", "tree", "pwd":
		cmd = exec.Command(parts[0], parts[1:]...)
	case "mkdir":
		// Allow mkdir with -p flag
		cmd = exec.Command(parts[0], parts[1:]...)
	case "cat", "echo":
		if needsShell {
			// Use shell for redirections like echo 'text' >> file or cat <<EOF
			cmd = exec.Command("sh", "-c", command)
		} else {
			cmd = exec.Command(parts[0], parts[1:]...)
		}
	case "npm", "npx", "node":
		// Allow npm/node commands for running tests
		cmd = exec.Command(parts[0], parts[1:]...)
	case "cd":
		// Handle cd specially - it's a no-op in this context
		return fmt.Sprintf("(cd not supported in playground, working dir is always %s)\n", session.Dir), nil
	default:
		return "", fmt.Errorf("command not allowed: %s (only kai and basic shell commands)", parts[0])
	}

	cmd.Dir = session.Dir
	cmd.Env = append(os.Environ(), "HOME="+session.Dir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

// Server is the HTTP server for the playground.
type Server struct {
	sm       *SessionManager
	upgrader websocket.Upgrader
}

// NewServer creates a new playground server.
func NewServer(sm *SessionManager) *Server {
	return &Server{
		sm: sm,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
}

// handleCreateSession creates a new playground session.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Example string `json:"example"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to empty
		req.Example = "basic"
	}

	session, err := s.sm.CreateSession(req.Example)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"sessionId": session.ID,
	})
}

// handleCommand handles command execution via REST.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")
	session := s.sm.GetSession(sessionID)
	if session == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	output, err := s.sm.RunCommand(session, req.Command)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"output": output,
		"error":  err != nil,
	})
}

// handleTerminal handles WebSocket terminal connections.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	session := s.sm.GetSession(sessionID)
	if session == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Send welcome message (use \r\n for proper terminal line breaks)
	welcome := fmt.Sprintf("Welcome to Kai Playground!\r\nSession: %s\r\nWorking directory: %s\r\n\r\nType 'kai --help' to get started.\r\n\r\n$ ", session.ID[:8], session.Dir)
	conn.WriteMessage(websocket.TextMessage, []byte(welcome))

	// Handle commands
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		command := strings.TrimSpace(string(message))
		if command == "" {
			conn.WriteMessage(websocket.TextMessage, []byte("$ "))
			continue
		}

		output, cmdErr := s.sm.RunCommand(session, command)
		// Convert \n to \r\n for proper terminal display
		response := strings.ReplaceAll(output, "\n", "\r\n")
		if cmdErr != nil && !strings.Contains(output, "Error") {
			response += fmt.Sprintf("\r\nError: %v", cmdErr)
		}
		response += "\r\n$ "

		conn.WriteMessage(websocket.TextMessage, []byte(response))
	}
}

// handleGraph returns the current graph state for visualization.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	session := s.sm.GetSession(sessionID)
	if session == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Run kai dump to get graph data
	// For now, just return snapshots and refs
	output, _ := s.sm.RunCommand(session, "kai list snapshots --json")

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(output))
}

// handleTutorial returns tutorial content.
func (s *Server) handleTutorial(w http.ResponseWriter, r *http.Request) {
	tutorialID := r.URL.Query().Get("id")

	tutorials := getTutorials()
	if tutorial, ok := tutorials[tutorialID]; ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tutorial)
	} else {
		// Return list of tutorials
		var list []map[string]string
		for id, t := range tutorials {
			list = append(list, map[string]string{
				"id":          id,
				"title":       t.Title,
				"description": t.Description,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	}
}

func main() {
	// Find kai binary
	kaiBin := os.Getenv("KAI_BIN")
	if kaiBin == "" {
		// Try to find in PATH or relative location
		if path, err := exec.LookPath("kai"); err == nil {
			kaiBin = path
		} else {
			kaiBin = "../kai-cli/kai" // Development default
		}
	}

	// Verify kai binary exists
	if _, err := os.Stat(kaiBin); os.IsNotExist(err) {
		log.Printf("Warning: kai binary not found at %s", kaiBin)
		log.Printf("Set KAI_BIN environment variable or build kai first")
	}

	sm := NewSessionManager(kaiBin)
	server := NewServer(sm)

	// Setup routes
	http.HandleFunc("/api/session", server.handleCreateSession)
	http.HandleFunc("/api/command", server.handleCommand)
	http.HandleFunc("/api/terminal", server.handleTerminal)
	http.HandleFunc("/api/graph", server.handleGraph)
	http.HandleFunc("/api/tutorial", server.handleTutorial)

	// Serve static files
	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "../frontend/dist" // Development default
	}
	http.Handle("/", http.FileServer(http.Dir(staticDir)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	log.Printf("Kai Playground server starting on :%s", port)
	log.Printf("Using kai binary: %s", kaiBin)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func copyExampleProject(example, destDir string) error {
	exampleDir := filepath.Join("examples", example)
	if _, err := os.Stat(exampleDir); os.IsNotExist(err) {
		// Create a basic example inline
		return createBasicExample(destDir)
	}
	return copyDir(exampleDir, destDir)
}

func createBasicExample(dir string) error {
	// Create a simple JS project structure
	files := map[string]string{
		"src/utils/math.js": `// Math utilities
function add(a, b) {
    return a + b;
}

function multiply(a, b) {
    return a * b;
}

module.exports = { add, multiply };
`,
		"src/utils/format.js": `// Formatting utilities
const { multiply } = require('./math');

function formatCurrency(amount) {
    return '$' + (multiply(amount, 100) / 100).toFixed(2);
}

module.exports = { formatCurrency };
`,
		"src/app.js": `// Main application
const { formatCurrency } = require('./utils/format');

function calculateTotal(items) {
    const sum = items.reduce((acc, item) => acc + item.price, 0);
    return formatCurrency(sum);
}

module.exports = { calculateTotal };
`,
		"tests/app.test.js": `// Tests
const { calculateTotal } = require('../src/app');

describe('calculateTotal', () => {
    it('calculates total price', () => {
        const items = [{ price: 10 }, { price: 20 }];
        expect(calculateTotal(items)).toBe('$30.00');
    });
});
`,
		"tests/math.test.js": `// Math tests
const { add, multiply } = require('../src/utils/math');

describe('math', () => {
    it('adds numbers', () => {
        expect(add(2, 3)).toBe(5);
    });

    it('multiplies numbers', () => {
        expect(multiply(2, 3)).toBe(6);
    });
});
`,
		"package.json": `{
  "name": "kai-playground-example",
  "version": "1.0.0",
  "scripts": {
    "test": "jest"
  }
}
`,
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}
