// Package telemetry provides opt-in anonymous usage telemetry for the Kai CLI.
// Events are delivered to PostHog. Telemetry is off by default and collects no
// sensitive data.
package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

const (
	// posthogAPIKey is the PostHog project key. Public by design (PostHog project
	// keys are client-safe); compiled in so kai "just works" without setup.
	posthogAPIKey = "phc_CwMM26Bxk65ipkHertinsvDsNcdYgbZ3fvzF8h2gFvWh"

	// posthogEndpoint is the US PostHog cloud.
	posthogEndpoint = "https://us.i.posthog.com"

	// eventName is sent to PostHog for every CLI invocation.
	eventName = "cli_command"
)

// version is set by the main package at init time.
var version string

// SetVersion sets the CLI version string used in events.
func SetVersion(v string) { version = v }

// Config holds the telemetry configuration persisted to disk.
// LastUploadAt is retained for backwards compatibility with on-disk configs
// written by pre-PostHog builds; it is no longer read.
type Config struct {
	Enabled      bool   `json:"enabled"`
	InstallID    string `json:"install_id"`
	Level        string `json:"level"`
	CreatedAt    string `json:"created_at"`
	LastUploadAt string `json:"last_upload_at,omitempty"`
}

// Event represents a single telemetry event.
// The shape is API-compatible with the pre-PostHog client so existing call
// sites (main.go's NewEvent / SetPhase / Finish) need no changes.
type Event struct {
	EventName  string           `json:"event"`
	Timestamp  string           `json:"ts"`
	InstallID  string           `json:"install_id"`
	Version    string           `json:"version"`
	OS         string           `json:"os"`
	Arch       string           `json:"arch"`
	Command    string           `json:"command"`
	DurMs      int64            `json:"dur_ms"`
	PhasesMs   map[string]int64 `json:"phases_ms,omitempty"`
	Stats      map[string]int64 `json:"stats,omitempty"`
	Cache      map[string]int64 `json:"cache,omitempty"`
	Result     string           `json:"result"`
	ErrorClass string           `json:"error_class,omitempty"`

	start time.Time
	mu    sync.Mutex
}

// ConfigPath returns the path to the telemetry config file.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kai", "telemetry.json")
}

// legacySpoolPath is the path of the pre-PostHog on-disk spool. We delete it
// opportunistically so upgraded installs don't leave orphan state behind.
func legacySpoolPath() string {
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

// ─── PostHog client (singleton, lazy) ───────────────────────────────────────

// sink is the minimal subset of posthog.Client we depend on. Tests inject a
// fake implementation; production uses the real PostHog client.
type sink interface {
	Enqueue(posthog.Message) error
	Close() error
}

var (
	clientMu   sync.Mutex
	clientInst sink

	// newClient is swapped out in tests. Production path calls posthog.NewWithConfig.
	newClient = func() (sink, error) {
		return posthog.NewWithConfig(posthogAPIKey, posthog.Config{
			Endpoint: posthogEndpoint,
		})
	}
)

// getClient returns the singleton PostHog client, creating it on first use.
// Returns nil without error if telemetry is disabled.
func getClient() sink {
	if !IsEnabled() {
		return nil
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	if clientInst != nil {
		return clientInst
	}
	c, err := newClient()
	if err != nil {
		// Telemetry is best-effort; failures never block the caller.
		return nil
	}
	clientInst = c
	return clientInst
}

// Close flushes any pending events and shuts down the PostHog client.
// Safe to call multiple times; safe when telemetry is disabled.
// Main should defer this so events flush before the CLI exits.
func Close() {
	clientMu.Lock()
	c := clientInst
	clientInst = nil
	clientMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// ─── Event API (kept stable for main.go call sites) ─────────────────────────

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
	// Drop the pre-PostHog spool once we know telemetry is active — the
	// events are already stale and the new client never reads them.
	if _, err := os.Stat(legacySpoolPath()); err == nil {
		_ = os.Remove(legacySpoolPath())
	}
	return &Event{
		EventName: eventName,
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

// SetStat records a named integer statistic (file count, byte count, etc.).
func (e *Event) SetStat(name string, v int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Stats[name] = v
}

// SetCache records a named cache metric (hits, misses, etc.).
func (e *Event) SetCache(name string, v int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Cache[name] = v
}

// SetResult records the final outcome: "ok", "error", etc.
func (e *Event) SetResult(result string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Result = result
}

// SetErrorClass records a coarse-grained error taxonomy (e.g. "network",
// "auth", "parse"). Not the error message.
func (e *Event) SetErrorClass(class string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ErrorClass = class
}

// Finish computes the total duration and sends the event to PostHog.
func (e *Event) Finish() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.DurMs = time.Since(e.start).Milliseconds()
	e.mu.Unlock()

	client := getClient()
	if client == nil {
		return
	}

	props := posthog.NewProperties().
		Set("command", e.Command).
		Set("dur_ms", e.DurMs).
		Set("version", e.Version).
		Set("os", e.OS).
		Set("arch", e.Arch).
		Set("result", e.Result).
		Set("ts", e.Timestamp)

	if e.ErrorClass != "" {
		props.Set("error_class", e.ErrorClass)
	}
	for k, v := range e.PhasesMs {
		props.Set("phase_"+k+"_ms", v)
	}
	for k, v := range e.Stats {
		props.Set("stat_"+k, v)
	}
	for k, v := range e.Cache {
		props.Set("cache_"+k, v)
	}

	_ = client.Enqueue(posthog.Capture{
		DistinctId: e.InstallID,
		Event:      eventName,
		Properties: props,
	})
}

// ─── Opt-in management ──────────────────────────────────────────────────────

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

// FlushNow forces any buffered events to be delivered to PostHog immediately.
// Used by `kai telemetry flush`. The PostHog Go client flushes on Close, so
// this is equivalent to Close (and resets the singleton so a subsequent event
// in the same process would create a fresh client).
func FlushNow() error {
	if !IsEnabled() {
		return fmt.Errorf("telemetry disabled")
	}
	Close()
	return nil
}
