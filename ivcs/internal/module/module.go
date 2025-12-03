// Package module provides module mapping via path glob rules.
package module

import (
	"fmt"
	"os"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// ModuleRule defines a module with its path patterns.
type ModuleRule struct {
	Name  string   `yaml:"name"`
	Paths []string `yaml:"paths"`
}

// ModulesConfig holds the modules configuration.
type ModulesConfig struct {
	Modules []ModuleRule `yaml:"modules"`
}

// Matcher matches file paths to modules.
type Matcher struct {
	modules []ModuleRule
}

// LoadRules loads module rules from a YAML file.
func LoadRules(path string) (*Matcher, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading modules file: %w", err)
	}

	var config ModulesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing modules file: %w", err)
	}

	return &Matcher{modules: config.Modules}, nil
}

// NewMatcher creates a matcher from a list of module rules.
func NewMatcher(modules []ModuleRule) *Matcher {
	return &Matcher{modules: modules}
}

// MatchPath returns the names of modules that match the given path.
func (m *Matcher) MatchPath(path string) []string {
	var matched []string

	for _, mod := range m.modules {
		for _, pattern := range mod.Paths {
			match, err := doublestar.Match(pattern, path)
			if err != nil {
				continue
			}
			if match {
				matched = append(matched, mod.Name)
				break // Only add each module once
			}
		}
	}

	return matched
}

// MatchPaths returns a map of module names to paths that match.
func (m *Matcher) MatchPaths(paths []string) map[string][]string {
	result := make(map[string][]string)

	for _, path := range paths {
		modules := m.MatchPath(path)
		for _, mod := range modules {
			result[mod] = append(result[mod], path)
		}
	}

	return result
}

// GetAllModules returns all module rules.
func (m *Matcher) GetAllModules() []ModuleRule {
	return m.modules
}

// GetModulePayload returns the payload for a module node.
func (m *Matcher) GetModulePayload(name string) map[string]interface{} {
	for _, mod := range m.modules {
		if mod.Name == name {
			patterns := make([]interface{}, len(mod.Paths))
			for i, p := range mod.Paths {
				patterns[i] = p
			}
			return map[string]interface{}{
				"name":     mod.Name,
				"patterns": patterns,
			}
		}
	}
	return nil
}
