// Package telemetry provides opt-in anonymous usage telemetry for the Kai CLI.
// Telemetry is off by default and collects no sensitive data.
package telemetry

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// UploadEndpoint is the telemetry batch upload URL.
	UploadEndpoint = "https://kaicontext.com/v1/telemetry/batch"

	// SpoolMaxBytes is the hard cap on spool file size (1 MB).
	SpoolMaxBytes = 1 << 20

	// uploadInterval is the minimum time between uploads.
	uploadInterval = 24 * time.Hour
)

// version is set by the main package at init time.
var version string

// SetVersion sets the CLI version string used in events.
func SetVersion(v string) { version = v }

// Config holds the telemetry configuration persisted to disk.
type Config struct {
	Enabled      bool   `json:"enabled"`
	InstallID    string `json:"install_id"`
	Level        string `json:"level"`
	CreatedAt    string `json:"created_at"`
	LastUploadAt string `json:"last_upload_at"`
}

// Event represents a single telemetry event.
type Event struct {
	EventName  string            `json:"event"`
	Timestamp  string            `json:"ts"`
	InstallID  string            `json:"install_id"`
	Version    string            `json:"version"`
	OS         string            `json:"os"`
	Arch       string            `json:"arch"`
	Command    string            `json:"command"`
	DurMs      int64             `json:"dur_ms"`
	PhasesMs   map[string]int64  `json:"phases_ms,omitempty"`
	Stats      map[string]int64  `json:"stats,omitempty"`
	Cache      map[string]int64  `json:"cache,omitempty"`
	Result     string            `json:"result"`
	ErrorClass string            `json:"error_class,omitempty"`

	start time.Time
	mu    sync.Mutex
}

// ConfigPath returns the path to the telemetry config file.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kai", "telemetry.json")
}

// SpoolPath returns the path to the telemetry spool file.
func SpoolPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kai", "telemetry.jsonl")
}

// LoadConfig reads the telemetry config from disk.
// Returns a disabled config if the file is missing.
func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if os.IsNotExist(err) {
		return &Config{Level: "basic"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading telemetry config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing telemetry config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the telemetry config to disk.
func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// IsEnabled returns whether telemetry is active, checking (in priority order):
// 1. KAI_TELEMETRY=0 → hard off
// 2. KAI_TELEMETRY=1 → on
// 3. CI=true → off (unless KAI_TELEMETRY=1)
// 4. Config file
func IsEnabled() bool {
	if v := os.Getenv("KAI_TELEMETRY"); v != "" {
		return v == "1"
	}
	if ci := os.Getenv("CI"); strings.EqualFold(ci, "true") || ci == "1" {
		return false
	}
	cfg, err := LoadConfig()
	if err != nil {
		return false
	}
	return cfg.Enabled
}

// NewEvent creates a new event for the given command, pre-filled with
// timestamp, install_id, version, os, and arch.
// Returns nil if telemetry is disabled.
func NewEvent(command string) *Event {
	if !IsEnabled() {
		return nil
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil
	}
	return &Event{
		EventName: "cli_command",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		InstallID: cfg.InstallID,
		Version:   version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Command:   command,
		PhasesMs:  make(map[string]int64),
		Stats:     make(map[string]int64),
		Cache:     make(map[string]int64),
		Result:    "ok",
		start:     time.Now(),
	}
}

// SetPhase records a named phase duration in milliseconds.
func (e *Event) SetPhase(name string, ms int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.PhasesMs[name] = ms
}

// Finish computes the total duration and records the event to the spool.
func (e *Event) Finish() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.DurMs = time.Since(e.start).Milliseconds()
	e.mu.Unlock()
	_ = Record(e)
}

// Record appends an event as a JSON line to the spool file,
// enforcing the 1 MB cap by dropping the oldest half when exceeded.
func Record(event *Event) error {
	if event == nil {
		return nil
	}
	path := SpoolPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	f.Close()

	// Enforce spool cap
	return enforceSpoolCap(path)
}

func enforceSpoolCap(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= SpoolMaxBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.Split(data, []byte("\n"))
	// Drop the oldest half
	half := len(lines) / 2
	remaining := bytes.Join(lines[half:], []byte("\n"))
	return os.WriteFile(path, remaining, 0o600)
}

// Enable turns on telemetry, generating an install_id if missing.
func Enable() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.Enabled = true
	if cfg.InstallID == "" {
		cfg.InstallID = uuid.New().String()
	}
	if cfg.CreatedAt == "" {
		cfg.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if cfg.Level == "" {
		cfg.Level = "basic"
	}
	return SaveConfig(cfg)
}

// Disable turns off telemetry.
func Disable() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.Enabled = false
	return SaveConfig(cfg)
}

// FlushIfNeeded reads the spool, gzip-POSTs to the upload endpoint,
// and clears the spool on success. Rate-limited to once per 24 hours.
func FlushIfNeeded() error {
	return flush(false)
}

// FlushNow uploads immediately, bypassing the 24-hour rate limit.
// Used by `kai telemetry flush` for manual recovery.
func FlushNow() error {
	return flush(true)
}

func flush(force bool) error {
	if !IsEnabled() {
		return fmt.Errorf("telemetry disabled")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	// Rate limit: at most once per 24 hours (unless forced)
	if !force && cfg.LastUploadAt != "" {
		last, err := time.Parse(time.RFC3339, cfg.LastUploadAt)
		if err == nil && time.Since(last) < uploadInterval {
			return nil
		}
	}

	path := SpoolPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) || len(data) == 0 {
		return nil
	}
	if err != nil {
		return err
	}

	// Parse spool lines into a batch.
	// Important: bufio.Scanner reuses its internal buffer, so we must COPY each
	// line before appending — otherwise all entries in `events` end up pointing
	// at the same memory and marshaling produces N copies of the last line.
	var events []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Allow large lines (default 64 KB is fine but be explicit).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		events = append(events, json.RawMessage(cp))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading spool: %w", err)
	}
	if len(events) == 0 {
		return nil
	}

	batch, err := json.Marshal(events)
	if err != nil {
		return err
	}

	// Gzip compress
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(batch); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	// POST
	req, err := http.NewRequest("POST", UploadEndpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Clear spool and update last upload time
		os.Remove(path)
		cfg.LastUploadAt = time.Now().UTC().Format(time.RFC3339)
		return SaveConfig(cfg)
	}
	return fmt.Errorf("telemetry upload: HTTP %d", resp.StatusCode)
}

// EventCount returns the number of events currently in the spool.
func EventCount() (int, error) {
	data, err := os.ReadFile(SpoolPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) > 0 {
			n++
		}
	}
	return n, nil
}

// SpoolSize returns the size of the spool file in bytes.
func SpoolSize() (int64, error) {
	info, err := os.Stat(SpoolPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
