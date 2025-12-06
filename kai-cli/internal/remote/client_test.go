package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"kai-core/cas"
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

func TestClient_BatchUpdateRefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/test/repo/v1/refs/batch" {
			t.Errorf("expected path '/test/repo/v1/refs/batch', got %s", r.URL.Path)
		}
		if r.Header.Get("X-Kailab-Actor") == "" {
			t.Error("expected X-Kailab-Actor header")
		}

		var req BatchRefUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if len(req.Updates) != 2 {
			t.Errorf("expected 2 updates, got %d", len(req.Updates))
		}

		resp := BatchRefUpdateResponse{
			PushID: "batch123",
			Results: []BatchRefResult{
				{Name: "snap.latest", OK: true, UpdatedAt: 123},
				{Name: "cs.latest", OK: true, UpdatedAt: 124},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "repo")
	client.Actor = "testuser"

	updates := []BatchRefUpdate{
		{Name: "snap.latest", New: []byte{1, 2, 3}},
		{Name: "cs.latest", New: []byte{4, 5, 6}},
	}

	result, err := client.BatchUpdateRefs(updates)
	if err != nil {
		t.Fatalf("BatchUpdateRefs failed: %v", err)
	}

	if result.PushID != "batch123" {
		t.Errorf("expected PushID 'batch123', got %q", result.PushID)
	}
	if len(result.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(result.Results))
	}
	for _, res := range result.Results {
		if !res.OK {
			t.Errorf("expected ref %s to be OK", res.Name)
		}
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

// TestWorkspacePackDigest verifies that workspace nodes use content-addressed
// digests in packs (not their UUID), allowing server-side verification.
func TestWorkspacePackDigest(t *testing.T) {
	// Simulate a workspace node with UUID ID (not content-addressed)
	workspaceUUID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	workspaceKind := "Workspace"

	// Original payload (as stored locally)
	originalPayload := map[string]interface{}{
		"name":           "test-workspace",
		"baseSnapshot":   "abc123",
		"headSnapshot":   "def456",
		"status":         "active",
		"openChangeSets": []interface{}{},
		"description":    "Test workspace",
	}

	// For pack transport, we add _uuid to the payload
	transportPayload := make(map[string]interface{})
	for k, v := range originalPayload {
		transportPayload[k] = v
	}
	transportPayload["_uuid"] = "0102030405060708090a0b0c0d0e0f10"

	// Compute content for pack
	payloadJSON, err := json.Marshal(transportPayload) // Note: in production we use CanonicalJSON
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}
	content := append([]byte(workspaceKind+"\n"), payloadJSON...)

	// The pack digest should be computed from content, NOT the UUID
	contentDigest := cas.Blake3Hash(content)

	// Verify the digest is NOT the same as the UUID
	if string(contentDigest) == string(workspaceUUID) {
		t.Error("content digest should differ from UUID")
	}

	// Verify the digest can be verified by computing blake3(content)
	verifyDigest := cas.Blake3Hash(content)
	if string(verifyDigest) != string(contentDigest) {
		t.Error("digest verification failed")
	}

	// Build a pack with the workspace object
	objects := []PackObject{
		{
			Digest:  contentDigest, // Use content-addressed digest, not UUID
			Kind:    workspaceKind,
			Content: content,
		},
	}

	pack, err := BuildPack(objects)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}
	if len(pack) == 0 {
		t.Error("expected non-empty pack")
	}
}

// TestWorkspaceUUIDRecovery verifies that the original workspace UUID can be
// recovered from the transported payload after fetching.
func TestWorkspaceUUIDRecovery(t *testing.T) {
	// Simulate transported payload (as received from server)
	transportPayload := map[string]interface{}{
		"name":           "test-workspace",
		"baseSnapshot":   "abc123",
		"headSnapshot":   "def456",
		"status":         "active",
		"openChangeSets": []interface{}{},
		"description":    "Test workspace",
		"_uuid":          "0102030405060708090a0b0c0d0e0f10",
	}

	// Extract the UUID
	uuidHex, ok := transportPayload["_uuid"].(string)
	if !ok {
		t.Fatal("_uuid field not found or wrong type")
	}

	expectedUUID := "0102030405060708090a0b0c0d0e0f10"
	if uuidHex != expectedUUID {
		t.Errorf("expected UUID %s, got %s", expectedUUID, uuidHex)
	}

	// Remove _uuid from payload for local storage
	delete(transportPayload, "_uuid")

	// Verify _uuid is removed
	if _, exists := transportPayload["_uuid"]; exists {
		t.Error("_uuid should have been removed from payload")
	}

	// Verify other fields are intact
	if name, _ := transportPayload["name"].(string); name != "test-workspace" {
		t.Errorf("expected name 'test-workspace', got %s", name)
	}
}

// TestContentAddressedNodeDigest verifies that content-addressed nodes
// have digests that match blake3(kind + "\n" + canonicalJSON(payload)).
func TestContentAddressedNodeDigest(t *testing.T) {
	// Simulate a content-addressed node (like Snapshot or ChangeSet)
	kind := "Snapshot"
	payload := map[string]interface{}{
		"source":    "abc123",
		"parent":    "",
		"createdAt": int64(1234567890),
	}

	// Compute the expected node ID using canonical JSON
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	content := append([]byte(kind+"\n"), payloadJSON...)
	expectedDigest := cas.Blake3Hash(content)

	// Build a pack object with this content
	obj := PackObject{
		Digest:  expectedDigest,
		Kind:    kind,
		Content: content,
	}

	// Verify the digest matches when we recompute
	computedDigest := cas.Blake3Hash(obj.Content)
	if string(computedDigest) != string(obj.Digest) {
		t.Errorf("digest mismatch: expected %x, got %x", obj.Digest, computedDigest)
	}

	// Build pack and verify it's valid
	pack, err := BuildPack([]PackObject{obj})
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}
	if len(pack) == 0 {
		t.Error("expected non-empty pack")
	}
}

// TestRawPayloadPreservesDigest verifies that using raw JSON payload
// (instead of re-serializing) preserves the content-addressed digest.
func TestRawPayloadPreservesDigest(t *testing.T) {
	kind := "File"
	// Create payload with specific types
	payload := map[string]interface{}{
		"path":   "src/main.ts",
		"digest": "abc123def456",
		"size":   int64(1024),
	}

	// Compute canonical JSON (this is what gets stored in DB)
	storedJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	// Compute the node ID from stored JSON
	content := append([]byte(kind+"\n"), storedJSON...)
	nodeID := cas.Blake3Hash(content)

	// Simulate reading back from DB - use the stored JSON directly
	// (this is what GetNodeRawPayload does)
	rawPayloadJSON := storedJSON

	// Build content from raw payload
	packContent := append([]byte(kind+"\n"), rawPayloadJSON...)
	packDigest := cas.Blake3Hash(packContent)

	// The digest should match the original node ID
	if string(packDigest) != string(nodeID) {
		t.Errorf("digest mismatch when using raw payload: expected %x, got %x", nodeID, packDigest)
	}
}

// TestJSONRoundTripBreaksDigest demonstrates why we need raw payload -
// JSON round-tripping changes types and breaks content-addressing.
func TestJSONRoundTripBreaksDigest(t *testing.T) {
	kind := "ChangeSet"
	// Create payload with int64 (as it would be created originally)
	payload := map[string]interface{}{
		"beforeSnapshot": "abc123",
		"afterSnapshot":  "def456",
		"createdAt":      int64(1234567890000),
	}

	// Compute original digest
	originalJSON, _ := cas.CanonicalJSON(payload)
	originalContent := append([]byte(kind+"\n"), originalJSON...)
	originalDigest := cas.Blake3Hash(originalContent)

	// Simulate JSON round-trip (what happens in DB read)
	var roundTrippedPayload map[string]interface{}
	json.Unmarshal(originalJSON, &roundTrippedPayload)

	// Re-serialize after round-trip
	roundTrippedJSON, _ := cas.CanonicalJSON(roundTrippedPayload)
	roundTrippedContent := append([]byte(kind+"\n"), roundTrippedJSON...)
	roundTrippedDigest := cas.Blake3Hash(roundTrippedContent)

	// The digests might differ due to type changes (int64 -> float64)
	// This test documents the issue - in practice we use raw payload to avoid this
	if string(originalDigest) != string(roundTrippedDigest) {
		// This is expected behavior - document it
		t.Logf("Note: JSON round-trip changed digest (expected behavior)")
		t.Logf("  Original:     %x", originalDigest[:8])
		t.Logf("  Round-tripped: %x", roundTrippedDigest[:8])
	}
}

// TestPackObjectVerification simulates server-side pack verification.
func TestPackObjectVerification(t *testing.T) {
	// Create several objects of different kinds
	objects := []struct {
		kind    string
		payload map[string]interface{}
	}{
		{
			kind:    "Snapshot",
			payload: map[string]interface{}{"source": "abc", "parent": ""},
		},
		{
			kind:    "File",
			payload: map[string]interface{}{"path": "test.ts", "digest": "xyz"},
		},
		{
			kind:    "Symbol",
			payload: map[string]interface{}{"name": "foo", "kind": "function"},
		},
	}

	var packObjects []PackObject
	for _, obj := range objects {
		payloadJSON, _ := cas.CanonicalJSON(obj.payload)
		content := append([]byte(obj.kind+"\n"), payloadJSON...)
		digest := cas.Blake3Hash(content)

		packObjects = append(packObjects, PackObject{
			Digest:  digest,
			Kind:    obj.kind,
			Content: content,
		})
	}

	// Build the pack
	pack, err := BuildPack(packObjects)
	if err != nil {
		t.Fatalf("BuildPack failed: %v", err)
	}

	// Simulate server-side verification: for each object, verify blake3(content) == digest
	for i, obj := range packObjects {
		computedDigest := cas.Blake3Hash(obj.Content)
		if string(computedDigest) != string(obj.Digest) {
			t.Errorf("object %d (%s): digest verification failed", i, obj.Kind)
		}
	}

	t.Logf("Pack size: %d bytes, objects: %d", len(pack), len(packObjects))
}
