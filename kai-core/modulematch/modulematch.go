// Package modulematch provides module mapping via path glob rules.
package modulematch

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

// GetModule returns a module by name.
func (m *Matcher) GetModule(name string) *ModuleRule {
	for i := range m.modules {
		if m.modules[i].Name == name {
			return &m.modules[i]
		}
	}
	return nil
}

// AddModule adds or updates a module.
func (m *Matcher) AddModule(name string, paths []string) {
	for i := range m.modules {
		if m.modules[i].Name == name {
			m.modules[i].Paths = paths
			return
		}
	}
	m.modules = append(m.modules, ModuleRule{Name: name, Paths: paths})
}

// RemoveModule removes a module by name.
func (m *Matcher) RemoveModule(name string) bool {
	for i := range m.modules {
		if m.modules[i].Name == name {
			m.modules = append(m.modules[:i], m.modules[i+1:]...)
			return true
		}
	}
	return false
}

// SaveRules saves module rules to a YAML file.
func (m *Matcher) SaveRules(path string) error {
	config := ModulesConfig{Modules: m.modules}
	data, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("marshaling modules: %w", err)
	}

	// Ensure directory exists
	dir := path[:len(path)-len("/modules.yaml")]
	if dir != path {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing modules file: %w", err)
	}
	return nil
}

// LoadRulesOrEmpty loads rules from file, or returns empty matcher if file doesn't exist.
func LoadRulesOrEmpty(path string) (*Matcher, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Matcher{modules: []ModuleRule{}}, nil
		}
		return nil, fmt.Errorf("reading modules file: %w", err)
	}

	var config ModulesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing modules file: %w", err)
	}

	return &Matcher{modules: config.Modules}, nil
}
