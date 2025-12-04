package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:7447", "tenant", "repo")
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.BaseURL != "http://localhost:7447" {
		t.Errorf("expected BaseURL 'http://localhost:7447', got %q", client.BaseURL)
	}
	if client.Tenant != "tenant" {
		t.Errorf("expected Tenant 'tenant', got %q", client.Tenant)
	}
	if client.Repo != "repo" {
		t.Errorf("expected Repo 'repo', got %q", client.Repo)
	}
	if client.HTTPClient == nil {
		t.Error("HTTPClient not initialized")
	}
}

func TestClient_RepoPath(t *testing.T) {
	client := NewClient("http://localhost", "myorg", "myrepo")
	path := client.repoPath()
	if path != "/myorg/myrepo" {
		t.Errorf("expected '/myorg/myrepo', got %q", path)
	}
}

func TestClient_Negotiate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test/repo/v1/push/negotiate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req NegotiateRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Return first digest as missing
		resp := NegotiateResponse{
			Missing: [][]byte{req.Digests[0]},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	digests := [][]byte{{1, 2, 3}, {4, 5, 6}}
	missing, err := client.Negotiate(digests)
	if err != nil {
		t.Fatalf("Negotiate failed: %v", err)
	}

	if len(missing) != 1 {
		t.Errorf("expected 1 missing, got %d", len(missing))
	}
}

func TestClient_GetRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test/repo/v1/refs/main" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := RefEntry{
			Name:      "main",
			Target:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
			UpdatedAt: 1234567890,
			Actor:     "user",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	ref, err := client.GetRef("main")
	if err != nil {
		t.Fatalf("GetRef failed: %v", err)
	}

	if ref == nil {
		t.Fatal("expected non-nil ref")
	}
	if ref.Name != "main" {
		t.Errorf("expected name 'main', got %q", ref.Name)
	}
	if ref.Actor != "user" {
		t.Errorf("expected actor 'user', got %q", ref.Actor)
	}
}

func TestClient_GetRef_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	ref, err := client.GetRef("nonexistent")
	if err != nil {
		t.Fatalf("GetRef failed: %v", err)
	}

	if ref != nil {
		t.Error("expected nil ref for not found")
	}
}

func TestClient_ListRefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("prefix") != "heads/" {
			// Allow empty prefix too
		}

		resp := RefsListResponse{
			Refs: []*RefEntry{
				{Name: "main", Target: []byte{1, 2, 3}},
				{Name: "feature", Target: []byte{4, 5, 6}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	refs, err := client.ListRefs("heads/")
	if err != nil {
		t.Fatalf("ListRefs failed: %v", err)
	}

	if len(refs) != 2 {
		t.Errorf("expected 2 refs, got %d", len(refs))
	}
}

func TestClient_Health(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	err := client.Health()
	if err != nil {
		t.Errorf("Health check failed: %v", err)
	}
}

func TestClient_Health_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	err := client.Health()
	if err == nil {
		t.Error("expected error for unhealthy server")
	}
}

func TestClient_GetObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Kailab-Kind", "file")
		// Content is stored as "kind\n{json}"
		w.Write([]byte("file\n{\"path\":\"test.js\"}"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	content, kind, err := client.GetObject([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	if kind != "file" {
		t.Errorf("expected kind 'file', got %q", kind)
	}
	if string(content) != `{"path":"test.js"}` {
		t.Errorf("unexpected content: %s", string(content))
	}
}

func TestClient_GetObject_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	content, kind, err := client.GetObject([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	if content != nil || kind != "" {
		t.Error("expected nil content and empty kind for not found")
	}
}

func TestClient_GetLogHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LogHeadResponse{
			Head: []byte{1, 2, 3, 4, 5, 6, 7, 8},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	head, err := client.GetLogHead()
	if err != nil {
		t.Fatalf("GetLogHead failed: %v", err)
	}

	if len(head) == 0 {
		t.Error("expected non-empty head")
	}
}

func TestClient_GetLogEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "0" {
			t.Errorf("expected after=0, got %s", r.URL.Query().Get("after"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit=10, got %s", r.URL.Query().Get("limit"))
		}

		resp := LogEntriesResponse{
			Entries: []*LogEntry{
				{Kind: "push", ID: []byte{1, 2, 3}, Time: 123456789},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	entries, err := client.GetLogEntries("", 0, 10)
	if err != nil {
		t.Fatalf("GetLogEntries failed: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestClient_UpdateRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.Header.Get("X-Kailab-Actor") == "" {
			t.Error("expected X-Kailab-Actor header")
		}

		resp := RefUpdateResponse{
			OK:        true,
			UpdatedAt: 123456789,
			PushID:    "push123",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	client.Actor = "testuser" // Ensure Actor is set for CI environments
	result, err := client.UpdateRef("main", []byte{1, 2, 3}, []byte{4, 5, 6}, false)
	if err != nil {
		t.Fatalf("UpdateRef failed: %v", err)
	}

	if !result.OK {
		t.Error("expected OK to be true")
	}
	if result.PushID != "push123" {
		t.Errorf("expected PushID 'push123', got %q", result.PushID)
	}
}

func TestClient_UpdateRef_Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		resp := RefUpdateResponse{
			OK:    false,
			Error: "ref has been updated",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	_, err := client.UpdateRef("main", []byte{1, 2, 3}, []byte{4, 5, 6}, false)
	if err == nil {
		t.Error("expected error for conflict")
	}
}

func TestClient_ParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "bad request",
			Details: "missing required field",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	_, err := client.Negotiate(nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "bad request: missing required field" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildPack(t *testing.T) {
	objects := []PackObject{
		{
			Digest:  []byte{1, 2, 3},
			Kind:    "file",
			Content: []byte(`{"path":"test.js"}`),
		},
		{
			Digest:  []byte{4, 5, 6},
			Kind:    "symbol",
			Content: []byte(`{"name":"foo"}`),
		},
	}

	pack, err := BuildPack(objects)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	if len(pack) == 0 {
		t.Error("expected non-empty pack")
	}

	// Pack should be compressed
	if pack[0] == '{' {
		t.Error("pack should be compressed, not raw JSON")
	}
}

func TestBuildPack_Empty(t *testing.T) {
	pack, err := BuildPack(nil)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	// Should still produce valid compressed output
	if len(pack) == 0 {
		t.Error("expected non-empty pack even for empty input")
	}
}

func TestRemoteConfig(t *testing.T) {
	// Create a temp directory for config
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Initially empty
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Remotes) != 0 {
		t.Errorf("expected empty remotes, got %d", len(cfg.Remotes))
	}

	// Set a remote
	err = SetRemote("origin", &RemoteEntry{
		URL:    "http://localhost:7447",
		Tenant: "myorg",
		Repo:   "myrepo",
	})
	if err != nil {
		t.Fatalf("SetRemote failed: %v", err)
	}

	// Load and verify
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(cfg.Remotes))
	}
	if cfg.Remotes["origin"].URL != "http://localhost:7447" {
		t.Errorf("unexpected URL: %s", cfg.Remotes["origin"].URL)
	}

	// Get remote
	entry, err := GetRemote("origin")
	if err != nil {
		t.Fatalf("GetRemote failed: %v", err)
	}
	if entry.Tenant != "myorg" {
		t.Errorf("expected tenant 'myorg', got %q", entry.Tenant)
	}

	// Get URL
	url, err := GetRemoteURL("origin")
	if err != nil {
		t.Fatalf("GetRemoteURL failed: %v", err)
	}
	if url != "http://localhost:7447" {
		t.Errorf("unexpected URL: %s", url)
	}

	// List remotes
	remotes, err := ListRemotes()
	if err != nil {
		t.Fatalf("ListRemotes failed: %v", err)
	}
	if len(remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(remotes))
	}

	// Delete remote
	err = DeleteRemote("origin")
	if err != nil {
		t.Fatalf("DeleteRemote failed: %v", err)
	}

	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Remotes) != 0 {
		t.Errorf("expected 0 remotes after delete, got %d", len(cfg.Remotes))
	}
}

func TestGetRemote_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	_, err := GetRemote("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent remote")
	}
}

func TestDeleteRemote_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	err := DeleteRemote("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent remote")
	}
}

func TestSetRemoteURL(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	err := SetRemoteURL("origin", "http://example.com")
	if err != nil {
		t.Fatalf("SetRemoteURL failed: %v", err)
	}

	entry, err := GetRemote("origin")
	if err != nil {
		t.Fatalf("GetRemote failed: %v", err)
	}

	if entry.URL != "http://example.com" {
		t.Errorf("expected URL 'http://example.com', got %q", entry.URL)
	}
	if entry.Tenant != "default" {
		t.Errorf("expected tenant 'default', got %q", entry.Tenant)
	}
	if entry.Repo != "main" {
		t.Errorf("expected repo 'main', got %q", entry.Repo)
	}
}

func TestNewClientForRemote(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	err := SetRemote("origin", &RemoteEntry{
		URL:    "http://localhost:7447",
		Tenant: "org",
		Repo:   "repo",
	})
	if err != nil {
		t.Fatalf("SetRemote failed: %v", err)
	}

	client, err := NewClientForRemote("origin")
	if err != nil {
		t.Fatalf("NewClientForRemote failed: %v", err)
	}

	if client.BaseURL != "http://localhost:7447" {
		t.Errorf("unexpected BaseURL: %s", client.BaseURL)
	}
	if client.Tenant != "org" {
		t.Errorf("unexpected Tenant: %s", client.Tenant)
	}
	if client.Repo != "repo" {
		t.Errorf("unexpected Repo: %s", client.Repo)
	}
}

func TestConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	path := ConfigPath()
	expected := filepath.Join(tmpDir, ".kai", "remotes.json")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestLoadConfig_MigrateOldFormat(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Create old format config
	configDir := filepath.Join(tmpDir, ".kai")
	os.MkdirAll(configDir, 0755)

	oldConfig := `{"remotes":{"origin":"http://old-url.com"}}`
	os.WriteFile(filepath.Join(configDir, "remotes.json"), []byte(oldConfig), 0644)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Remotes["origin"] == nil {
		t.Fatal("expected origin remote after migration")
	}
	if cfg.Remotes["origin"].URL != "http://old-url.com" {
		t.Errorf("expected URL 'http://old-url.com', got %q", cfg.Remotes["origin"].URL)
	}
	if cfg.Remotes["origin"].Tenant != "default" {
		t.Errorf("expected tenant 'default', got %q", cfg.Remotes["origin"].Tenant)
	}
}
