// Package config loads kai's per-repo configuration from
// <kaiDir>/config.yaml. Currently covers the planner (LLM model, agent
// cap) and the agent runner (command template, timeout) — the bits
// Phase 3 needs. The safety gate has its own loader at
// internal/safetygate/config.go for the same reason: focused configs,
// minimal blast radius when one slice changes.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// configFileName lives next to db.sqlite inside the kai data directory.
const configFileName = "config.yaml"

// Config is the merged result of defaults + on-disk overrides.
type Config struct {
	Agent   AgentConfig   `yaml:"agent"`
	Planner PlannerConfig `yaml:"planner"`
}

// AgentConfig controls how the orchestrator launches an agent process
// inside a spawned workspace.
type AgentConfig struct {
	// Command is the argv template. The literal "{prompt}" token (if
	// present) is replaced with the path to a temp file containing
	// the agent's full prompt. Default invokes Claude Code in
	// non-interactive mode so the process exits when the task is done.
	Command []string `yaml:"command"`

	// TimeoutSeconds caps any single agent run. 0 means no timeout
	// (not recommended — agents can hang).
	TimeoutSeconds int `yaml:"timeout"`
}

// PlannerConfig controls the natural-language planner.
type PlannerConfig struct {
	// Model is the Anthropic model id (e.g. "claude-sonnet-4-6").
	Model string `yaml:"model"`

	// MaxAgents caps how many agents a single plan may spawn. The
	// planner's LLM is told this number so it doesn't propose more.
	MaxAgents int `yaml:"max_agents"`
}

// Default returns the config used when no file is present. Sensible
// for solo developers running Claude Code; teams override via the
// yaml file.
func Default() Config {
	return Config{
		Agent: AgentConfig{
			Command:        []string{"claude", "-p", "{prompt}"},
			TimeoutSeconds: 600, // 10 minutes
		},
		Planner: PlannerConfig{
			Model:     "claude-sonnet-4-6",
			MaxAgents: 5,
		},
	}
}

// Load reads <kaiDir>/config.yaml. Missing file → Default. Malformed
// file is an error: silent fallback would mask config drift the user
// expects to take effect.
//
// Partial yaml is tolerated — any field not specified gets the
// default value. We achieve this by unmarshaling onto a Default()
// copy rather than a zero value.
func Load(kaiDir string) (Config, error) {
	cfg := Default()
	p := filepath.Join(kaiDir, configFileName)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("reading %s: %w", p, err)
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	// Hand-fix obvious mistakes that would brick the orchestrator
	// silently — empty command means "run nothing." Restore the
	// default rather than failing hard, but log via the returned
	// config (caller can detect if Command is the default).
	if len(cfg.Agent.Command) == 0 {
		cfg.Agent.Command = Default().Agent.Command
	}
	if cfg.Planner.Model == "" {
		cfg.Planner.Model = Default().Planner.Model
	}
	if cfg.Planner.MaxAgents <= 0 {
		cfg.Planner.MaxAgents = Default().Planner.MaxAgents
	}
	return cfg, nil
}
