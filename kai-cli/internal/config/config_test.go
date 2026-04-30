package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
}

func TestLoad_FullOverride(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`
agent:
  timeout: 1200
  bash_allow: [npm, go]
planner:
  model: claude-opus-4-7
  max_agents: 8
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{
		Agent: AgentConfig{
			TimeoutSeconds: 1200,
			BashAllow:      []string{"npm", "go"},
		},
		Planner: PlannerConfig{
			Model:     "claude-opus-4-7",
			MaxAgents: 8,
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("unexpected config:\n got: %+v\nwant: %+v", cfg, want)
	}
}

// TestLoad_PartialOverrideKeepsDefaults: the user only specifies the
// model, so everything else should fall back to Default(). Critical
// for forward-compat — we add a new config field, existing yamls
// shouldn't break.
func TestLoad_PartialOverrideKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte("planner:\n  model: claude-haiku-4-5\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Planner.Model != "claude-haiku-4-5" {
		t.Errorf("model override lost: %s", cfg.Planner.Model)
	}
	if cfg.Planner.MaxAgents != Default().Planner.MaxAgents {
		t.Errorf("max_agents should default, got %d", cfg.Planner.MaxAgents)
	}
	if !reflect.DeepEqual(cfg.Agent, Default().Agent) {
		t.Errorf("agent block should default: %+v", cfg.Agent)
	}
}

// TestLoad_LegacyCommandFieldIgnored: pre-Slice 6 configs may have
// `agent.command: [...]` set. yaml.v3 silently ignores unknown fields,
// so existing configs load without error. The non-deprecated fields
// still parse normally.
func TestLoad_LegacyCommandFieldIgnored(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`agent:
  command: ["claude", "-p", "{prompt}"]
  timeout: 60
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent.TimeoutSeconds != 60 {
		t.Errorf("timeout override lost: %d", cfg.Agent.TimeoutSeconds)
	}
}

// TestLoad_BashAllowParses verifies the bash_allow allowlist round-trips
// from yaml so the in-process agent's bash tool can pick it up.
func TestLoad_BashAllowParses(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`agent:
  bash_allow: [npm, go, git, make]
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(cfg.Agent.BashAllow, []string{"npm", "go", "git", "make"}) {
		t.Errorf("bash_allow: %v", cfg.Agent.BashAllow)
	}
}

func TestLoad_MalformedYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("not: : valid:"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
