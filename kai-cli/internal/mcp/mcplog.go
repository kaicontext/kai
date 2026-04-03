// Package mcp — tool call logging for SER/A-B measurement.
//
// Logs every MCP tool call to .kai/mcp-calls.jsonl with:
//   - tool name, parameters, duration
//   - files and symbols mentioned in the response (for SER computation)
//   - session ID (for grouping calls into sessions)
//
// Gated on KAI_MCP_LOG=1 environment variable. Off by default.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mcpLogMaxBytes caps the log file at 10 MB.
const mcpLogMaxBytes = 10 << 20

// ToolCallRecord is one logged MCP tool invocation.
type ToolCallRecord struct {
	SessionID string                 `json:"session_id"`
	Timestamp string                 `json:"ts"`
	Tool      string                 `json:"tool"`
	Params    map[string]interface{} `json:"params,omitempty"`
	DurMs     int64                  `json:"dur_ms"`
	IsError   bool                   `json:"is_error"`
	Files     []string               `json:"files,omitempty"`
	Symbols   []string               `json:"symbols,omitempty"`
	SeqNum    int                    `json:"seq"` // call order within session
}

// mcpLogger handles session-scoped, append-only JSONL logging.
type mcpLogger struct {
	mu        sync.Mutex
	sessionID string
	seq       int
	logPath   string
}

var globalLogger *mcpLogger

// initLogger sets up the logger for a given .kai directory.
// Called once from Server.registerTools if logging is enabled.
func initLogger(kaiDir string) {
	globalLogger = &mcpLogger{
		sessionID: uuid.New().String(),
		logPath:   filepath.Join(kaiDir, "mcp-calls.jsonl"),
	}
}

// mcpLogEnabled returns true if KAI_MCP_LOG=1.
func mcpLogEnabled() bool {
	return os.Getenv("KAI_MCP_LOG") == "1"
}

// record appends a ToolCallRecord to the JSONL log.
func (l *mcpLogger) record(rec ToolCallRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec.SessionID = l.sessionID
	l.seq++
	rec.SeqNum = l.seq

	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')

	f, err := os.OpenFile(l.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(line)
	f.Close()

	// Enforce size cap
	if info, err := os.Stat(l.logPath); err == nil && info.Size() > mcpLogMaxBytes {
		truncateLogHalf(l.logPath)
	}
}

func truncateLogHalf(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Find midpoint newline
	mid := len(data) / 2
	for mid < len(data) && data[mid] != '\n' {
		mid++
	}
	if mid < len(data) {
		os.WriteFile(path, data[mid+1:], 0o600)
	}
}

// withLogging wraps an MCP tool handler with call logging.
func withLogging(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if globalLogger == nil {
			return handler(ctx, req)
		}

		start := time.Now()
		result, err := handler(ctx, req)
		durMs := time.Since(start).Milliseconds()

		// Extract params from request
		params := make(map[string]interface{})
		if args, ok := req.Params.Arguments.(map[string]interface{}); ok {
			params = args
		}

		rec := ToolCallRecord{
			Timestamp: start.UTC().Format(time.RFC3339),
			Tool:      toolName,
			Params:    params,
			DurMs:     durMs,
			IsError:   err != nil || (result != nil && result.IsError),
		}

		// Extract files and symbols from the result for SER computation
		if result != nil && !rec.IsError {
			rec.Files, rec.Symbols = extractReferences(result)
		}

		globalLogger.record(rec)
		return result, err
	}
}

// pathPattern matches file paths in both JSON ("path": "x") and compact text (file.go:123:...) responses.
var pathPattern = regexp.MustCompile(`(?:"(?:path|file|source_file|test_file)"\s*:\s*"([^"]+)"|([\w][\w./\-]*\.\w+):\d+:)`)

// symbolPattern matches symbol name patterns in JSON responses.
var symbolPattern = regexp.MustCompile(`"(?:name|symbol|fqName|caller|callee)"\s*:\s*"([^"]+)"`)

// extractReferences pulls file paths and symbol names from a tool result.
// Uses simple regex over the JSON text — not a full parse, but good enough
// for co-occurrence tracking.
func extractReferences(result *mcpsdk.CallToolResult) (files, symbols []string) {
	if result == nil {
		return
	}

	var text string
	for _, c := range result.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if text == "" {
		return
	}

	fileSet := make(map[string]struct{})
	for _, m := range pathPattern.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 && m[1] != "" {
			fileSet[m[1]] = struct{}{}
		} else if len(m) > 2 && m[2] != "" {
			fileSet[m[2]] = struct{}{}
		}
	}
	for f := range fileSet {
		files = append(files, f)
	}

	symSet := make(map[string]struct{})
	for _, m := range symbolPattern.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			symSet[m[1]] = struct{}{}
		}
	}
	for s := range symSet {
		symbols = append(symbols, s)
	}

	return
}

// LogPath returns the path to the MCP call log, or "" if logging is disabled.
func LogPath(kaiDir string) string {
	if !mcpLogEnabled() {
		return ""
	}
	return filepath.Join(kaiDir, "mcp-calls.jsonl")
}

// SessionSummary aggregates a session's log records for analysis.
type SessionSummary struct {
	SessionID  string   `json:"session_id"`
	TotalCalls int      `json:"total_calls"`
	ToolCounts map[string]int `json:"tool_counts"`
	TotalDurMs int64    `json:"total_dur_ms"`
	AllFiles   []string `json:"all_files"`
	AllSymbols []string `json:"all_symbols"`
	Errors     int      `json:"errors"`
}

// SummarizeSession reads the log and returns a summary for the current session.
func SummarizeSession() (*SessionSummary, error) {
	if globalLogger == nil {
		return nil, fmt.Errorf("logging not enabled")
	}

	data, err := os.ReadFile(globalLogger.logPath)
	if err != nil {
		return nil, err
	}

	summary := &SessionSummary{
		SessionID:  globalLogger.sessionID,
		ToolCounts: make(map[string]int),
	}
	fileSet := make(map[string]struct{})
	symSet := make(map[string]struct{})

	for _, line := range splitLines(data) {
		var rec ToolCallRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.SessionID != globalLogger.sessionID {
			continue
		}
		summary.TotalCalls++
		summary.ToolCounts[rec.Tool]++
		summary.TotalDurMs += rec.DurMs
		if rec.IsError {
			summary.Errors++
		}
		for _, f := range rec.Files {
			fileSet[f] = struct{}{}
		}
		for _, s := range rec.Symbols {
			symSet[s] = struct{}{}
		}
	}

	for f := range fileSet {
		summary.AllFiles = append(summary.AllFiles, f)
	}
	for s := range symSet {
		summary.AllSymbols = append(summary.AllSymbols, s)
	}

	return summary, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
