package detect

import (
	"testing"
)

func TestDetectYAMLChanges_KeyAdded(t *testing.T) {
	before := []byte(`
name: myapp
version: "1.0"
`)
	after := []byte(`
name: myapp
version: "1.0"
description: A new field
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLKeyAdded {
			found = true
			if len(c.Evidence.Symbols) == 0 || c.Evidence.Symbols[0] != "description" {
				t.Errorf("expected symbol 'description', got %v", c.Evidence.Symbols)
			}
		}
	}
	if !found {
		t.Error("expected YAML_KEY_ADDED change")
	}
}

func TestDetectYAMLChanges_KeyRemoved(t *testing.T) {
	before := []byte(`
name: myapp
version: "1.0"
deprecated: true
`)
	after := []byte(`
name: myapp
version: "1.0"
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLKeyRemoved {
			found = true
			if len(c.Evidence.Symbols) == 0 || c.Evidence.Symbols[0] != "deprecated" {
				t.Errorf("expected symbol 'deprecated', got %v", c.Evidence.Symbols)
			}
		}
	}
	if !found {
		t.Error("expected YAML_KEY_REMOVED change")
	}
}

func TestDetectYAMLChanges_ValueChanged(t *testing.T) {
	before := []byte(`
name: myapp
version: "1.0"
`)
	after := []byte(`
name: myapp
version: "2.0"
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged {
			found = true
			if len(c.Evidence.Symbols) == 0 || c.Evidence.Symbols[0] != "version" {
				t.Errorf("expected symbol 'version', got %v", c.Evidence.Symbols)
			}
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED change")
	}
}

func TestDetectYAMLChanges_NestedKeyAdded(t *testing.T) {
	before := []byte(`
database:
  host: localhost
  port: 5432
`)
	after := []byte(`
database:
  host: localhost
  port: 5432
  username: admin
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLKeyAdded && len(c.Evidence.Symbols) > 0 {
			if c.Evidence.Symbols[0] == "database.username" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected YAML_KEY_ADDED for 'database.username'")
	}
}

func TestDetectYAMLChanges_NestedValueChanged(t *testing.T) {
	before := []byte(`
server:
  host: localhost
  port: 8080
`)
	after := []byte(`
server:
  host: localhost
  port: 3000
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged && len(c.Evidence.Symbols) > 0 {
			if c.Evidence.Symbols[0] == "server.port" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED for 'server.port'")
	}
}

func TestDetectYAMLChanges_NoChanges(t *testing.T) {
	content := []byte(`
name: myapp
version: "1.0"
`)

	changes, err := DetectYAMLChanges("config.yaml", content, content)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestDetectYAMLChanges_ArrayChanged(t *testing.T) {
	before := []byte(`
ports:
  - 80
  - 443
`)
	after := []byte(`
ports:
  - 80
  - 443
  - 8080
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged {
			found = true
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED for array modification")
	}
}

func TestDetectYAMLChanges_BooleanChanged(t *testing.T) {
	before := []byte(`
enabled: true
`)
	after := []byte(`
enabled: false
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged && len(c.Evidence.Symbols) > 0 {
			if c.Evidence.Symbols[0] == "enabled" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED for 'enabled'")
	}
}

func TestDetectYAMLChanges_TypeChanged(t *testing.T) {
	before := []byte(`
value: "100"
`)
	after := []byte(`
value: 100
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged {
			found = true
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED for type change")
	}
}

func TestDetectYAMLChanges_DockerCompose(t *testing.T) {
	before := []byte(`
version: "3.8"
services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
`)
	after := []byte(`
version: "3.8"
services:
  web:
    image: nginx:1.21
    ports:
      - "80:80"
      - "443:443"
`)

	changes, err := DetectYAMLChanges("docker-compose.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	if len(changes) == 0 {
		t.Error("expected changes for docker-compose modification")
	}

	// Should detect image change and ports change
	foundImage := false
	foundPorts := false
	for _, c := range changes {
		for _, sym := range c.Evidence.Symbols {
			if sym == "services.web.image" {
				foundImage = true
			}
			if sym == "services.web.ports" {
				foundPorts = true
			}
		}
	}
	if !foundImage {
		t.Error("expected change for services.web.image")
	}
	if !foundPorts {
		t.Error("expected change for services.web.ports")
	}
}

func TestDetectYAMLChanges_KubernetesDeployment(t *testing.T) {
	before := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  replicas: 2
`)
	after := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  replicas: 5
`)

	changes, err := DetectYAMLChanges("deployment.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Category == YAMLValueChanged && len(c.Evidence.Symbols) > 0 {
			if c.Evidence.Symbols[0] == "spec.replicas" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected YAML_VALUE_CHANGED for 'spec.replicas'")
	}
}

func TestExtractYAMLSymbols(t *testing.T) {
	content := []byte(`
name: myapp
database:
  host: localhost
  port: 5432
`)

	symbols, err := ExtractYAMLSymbols(content, 3)
	if err != nil {
		t.Fatalf("ExtractYAMLSymbols failed: %v", err)
	}

	expected := map[string]bool{
		"name":          false,
		"database":      false,
		"database.host": false,
		"database.port": false,
	}

	for _, sym := range symbols {
		if _, ok := expected[sym.Path]; ok {
			expected[sym.Path] = true
		}
	}

	for path, found := range expected {
		if !found {
			t.Errorf("expected to find symbol '%s'", path)
		}
	}
}

func TestExtractYAMLSymbols_MaxDepth(t *testing.T) {
	content := []byte(`
level1:
  level2:
    level3:
      level4: value
`)

	// With maxDepth=2, should not see level3.level4
	symbols, err := ExtractYAMLSymbols(content, 2)
	if err != nil {
		t.Fatalf("ExtractYAMLSymbols failed: %v", err)
	}

	foundLevel3 := false
	foundLevel4 := false
	for _, sym := range symbols {
		if sym.Path == "level1.level2.level3" {
			foundLevel3 = true
		}
		if sym.Path == "level1.level2.level3.level4" {
			foundLevel4 = true
		}
	}

	if !foundLevel3 {
		t.Error("expected to find level1.level2.level3 at depth 2")
	}
	if foundLevel4 {
		t.Error("should not find level1.level2.level3.level4 with maxDepth=2")
	}
}

func TestFormatYAMLPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "(root)"},
		{"name", "name"},
		{"database.host", "database.host"},
	}

	for _, tt := range tests {
		result := FormatYAMLPath(tt.input)
		if result != tt.expected {
			t.Errorf("FormatYAMLPath(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestDetectYAMLChanges_InvalidYAML(t *testing.T) {
	before := []byte(`valid: yaml`)
	after := []byte(`invalid: yaml: extra: colon`)

	_, err := DetectYAMLChanges("config.yaml", before, after)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestDetectYAMLChanges_MultipleChanges(t *testing.T) {
	before := []byte(`
name: oldname
version: "1.0"
enabled: true
`)
	after := []byte(`
name: newname
version: "2.0"
debug: true
`)

	changes, err := DetectYAMLChanges("config.yaml", before, after)
	if err != nil {
		t.Fatalf("DetectYAMLChanges failed: %v", err)
	}

	// Should have: name changed, version changed, enabled removed, debug added
	if len(changes) < 3 {
		t.Errorf("expected at least 3 changes, got %d", len(changes))
	}

	categories := make(map[ChangeCategory]int)
	for _, c := range changes {
		categories[c.Category]++
	}

	if categories[YAMLValueChanged] < 2 {
		t.Error("expected at least 2 YAML_VALUE_CHANGED")
	}
	if categories[YAMLKeyRemoved] < 1 {
		t.Error("expected at least 1 YAML_KEY_REMOVED")
	}
	if categories[YAMLKeyAdded] < 1 {
		t.Error("expected at least 1 YAML_KEY_ADDED")
	}
}
