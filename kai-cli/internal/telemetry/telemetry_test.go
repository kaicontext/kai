package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHome sets HOME to a temp dir for the duration of the test,
// so config/spool files don't pollute the real home directory.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Ensure .kai dir exists
	os.MkdirAll(filepath.Join(tmp, ".kai"), 0o700)
	return tmp
}

func TestLoadConfig_Missing(t *testing.T) {
	withTempHome(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("expected disabled config when file missing")
	}
	if cfg.Level != "basic" {
		t.Errorf("expected level=basic, got %q", cfg.Level)
	}
}

func TestLoadConfig_Exists(t *testing.T) {
	tmp := withTempHome(t)
	data := `{"enabled":true,"install_id":"test-uuid","level":"basic","created_at":"2026-01-01T00:00:00Z","last_upload_at":""}`
	os.WriteFile(filepath.Join(tmp, ".kai", "telemetry.json"), []byte(data), 0o600)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.InstallID != "test-uuid" {
		t.Errorf("expected install_id=test-uuid, got %q", cfg.InstallID)
	}
}

func TestSaveAndLoad(t *testing.T) {
	withTempHome(t)
	cfg := &Config{
		Enabled:   true,
		InstallID: "round-trip-id",
		Level:     "basic",
		CreatedAt: "2026-02-15T00:00:00Z",
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InstallID != "round-trip-id" {
		t.Errorf("expected round-trip-id, got %q", loaded.InstallID)
	}
	if !loaded.Enabled {
		t.Error("expected enabled=true after round-trip")
	}
}

func TestIsEnabled_Default(t *testing.T) {
	withTempHome(t)
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "")
	if IsEnabled() {
		t.Error("expected disabled by default")
	}
}

func TestIsEnabled_EnvOverrides(t *testing.T) {
	withTempHome(t)
	t.Setenv("CI", "")

	// KAI_TELEMETRY=1 enables even without config
	t.Setenv("KAI_TELEMETRY", "1")
	if !IsEnabled() {
		t.Error("expected KAI_TELEMETRY=1 to enable")
	}

	// KAI_TELEMETRY=0 disables even with config enabled
	Enable()
	t.Setenv("KAI_TELEMETRY", "0")
	if IsEnabled() {
		t.Error("expected KAI_TELEMETRY=0 to hard-disable")
	}
}

func TestIsEnabled_CIAutoDisable(t *testing.T) {
	withTempHome(t)
	Enable()

	// CI=true disables
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "true")
	if IsEnabled() {
		t.Error("expected CI=true to auto-disable")
	}

	// KAI_TELEMETRY=1 overrides CI
	t.Setenv("KAI_TELEMETRY", "1")
	if !IsEnabled() {
		t.Error("expected KAI_TELEMETRY=1 to override CI=true")
	}
}

func TestNewEvent(t *testing.T) {
	withTempHome(t)
	Enable()
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "")
	SetVersion("0.9.4-test")

	e := NewEvent("capture")
	if e == nil {
		t.Fatal("expected non-nil event when enabled")
	}
	if e.Command != "capture" {
		t.Errorf("expected command=capture, got %q", e.Command)
	}
	if e.Version != "0.9.4-test" {
		t.Errorf("expected version=0.9.4-test, got %q", e.Version)
	}
	if e.OS == "" || e.Arch == "" {
		t.Error("expected OS and Arch to be populated")
	}
	if e.Result != "ok" {
		t.Errorf("expected result=ok, got %q", e.Result)
	}
}

func TestNewEvent_Disabled(t *testing.T) {
	withTempHome(t)
	t.Setenv("KAI_TELEMETRY", "0")
	e := NewEvent("capture")
	if e != nil {
		t.Error("expected nil event when disabled")
	}
	// nil-safe methods should not panic
	e.SetPhase("test", 100)
	e.Finish()
}

func TestRecord_AppendsJSONL(t *testing.T) {
	withTempHome(t)
	Enable()
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "")

	e1 := NewEvent("capture")
	e1.DurMs = 100
	if err := Record(e1); err != nil {
		t.Fatal(err)
	}
	e2 := NewEvent("diff")
	e2.DurMs = 50
	if err := Record(e2); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(SpoolPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestRecord_SpoolCap(t *testing.T) {
	withTempHome(t)
	Enable()
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "")

	// Write >1MB of data to the spool
	path := SpoolPath()
	bigLine := strings.Repeat("x", 500) // ~500 bytes per line
	var builder strings.Builder
	for builder.Len() < SpoolMaxBytes+1000 {
		line, _ := json.Marshal(&Event{
			EventName: "cli_command",
			Command:   bigLine,
			Result:    "ok",
		})
		builder.Write(line)
		builder.WriteByte('\n')
	}
	os.WriteFile(path, []byte(builder.String()), 0o600)

	// Record one more event to trigger cap enforcement
	e := NewEvent("status")
	if err := Record(e); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > SpoolMaxBytes {
		t.Errorf("spool size %d exceeds cap %d", info.Size(), SpoolMaxBytes)
	}
}

func TestEnableDisable(t *testing.T) {
	withTempHome(t)
	t.Setenv("KAI_TELEMETRY", "")
	t.Setenv("CI", "")

	if err := Enable(); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfig()
	if !cfg.Enabled {
		t.Error("expected enabled after Enable()")
	}
	if cfg.InstallID == "" {
		t.Error("expected install_id to be generated")
	}

	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadConfig()
	if cfg.Enabled {
		t.Error("expected disabled after Disable()")
	}
	// install_id should be preserved
	if cfg.InstallID == "" {
		t.Error("expected install_id to be preserved after Disable()")
	}
}
