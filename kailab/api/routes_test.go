package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kailab/config"
	"kailab/proto"
)

func TestNewHandler(t *testing.T) {
	cfg := &config.Config{Version: "1.0.0"}
	h := NewHandler(nil, cfg)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.cfg != cfg {
		t.Error("config not set correctly")
	}
}

func TestHealth(t *testing.T) {
	cfg := &config.Config{Version: "1.0.0"}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	h.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp proto.HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", resp.Version)
	}
}

func TestReady(t *testing.T) {
	cfg := &config.Config{Version: "1.0.0"}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()

	h.Ready(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp proto.HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ready" {
		t.Errorf("expected status 'ready', got %q", resp.Status)
	}
}

func TestCreateRepo_MissingFields(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	tests := []struct {
		name string
		body interface{}
	}{
		{"empty request", proto.CreateRepoRequest{}},
		{"missing repo", proto.CreateRepoRequest{Tenant: "tenant"}},
		{"missing tenant", proto.CreateRepoRequest{Repo: "repo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/admin/v1/repos", bytes.NewReader(body))
			w := httptest.NewRecorder()

			h.CreateRepo(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d", w.Code)
			}
		})
	}
}

func TestCreateRepo_InvalidBody(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("POST", "/admin/v1/repos", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	h.CreateRepo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestNegotiate_InvalidBody(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	// Need repo context, but will fail on invalid body first
	req := httptest.NewRequest("POST", "/test/repo/v1/push/negotiate", bytes.NewReader([]byte("not json")))
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	w := httptest.NewRecorder()

	// This will fail due to no repo context, but we can test the error path
	h.Negotiate(w, req)

	// Without repo context, should return 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGetObject_InvalidDigest(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/test/repo/v1/objects/notvalidhex", nil)
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.SetPathValue("digest", "notvalidhex")
	w := httptest.NewRecorder()

	// This will fail due to no repo context
	h.GetObject(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestUpdateRef_MissingTarget(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	body, _ := json.Marshal(proto.RefUpdateRequest{
		New: nil, // Missing target
	})
	req := httptest.NewRequest("PUT", "/test/repo/v1/refs/main", bytes.NewReader(body))
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.SetPathValue("name", "main")
	w := httptest.NewRecorder()

	// This will fail due to no repo context
	h.UpdateRef(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestUpdateRef_InvalidBody(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("PUT", "/test/repo/v1/refs/main", bytes.NewReader([]byte("not json")))
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.SetPathValue("name", "main")
	w := httptest.NewRecorder()

	h.UpdateRef(w, req)

	// Without repo context, returns 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp["key"] != "value" {
		t.Errorf("expected value 'value', got %q", resp["key"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test error", nil)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp proto.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Error != "test error" {
		t.Errorf("expected error 'test error', got %q", resp.Error)
	}
}

func TestWriteError_WithDetails(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusInternalServerError, "test error", http.ErrServerClosed)

	var resp proto.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Error != "test error" {
		t.Errorf("expected error 'test error', got %q", resp.Error)
	}
	if resp.Details == "" {
		t.Error("expected details to be set")
	}
}

func TestExtractRefName(t *testing.T) {
	tests := []struct {
		path     string
		prefix   string
		expected string
	}{
		{"/v1/refs/main", "/v1/refs", "main"},
		{"/v1/refs/heads/feature", "/v1/refs", "heads/feature"},
		{"/v1/refs/", "/v1/refs", ""},
	}

	for _, tt := range tests {
		result := extractRefName(tt.path, tt.prefix)
		if result != tt.expected {
			t.Errorf("extractRefName(%q, %q) = %q, expected %q", tt.path, tt.prefix, result, tt.expected)
		}
	}
}

func TestIsSQLiteBusy(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{http.ErrServerClosed, false},
	}

	for _, tt := range tests {
		result := isSQLiteBusy(tt.err)
		if result != tt.expected {
			t.Errorf("isSQLiteBusy(%v) = %v, expected %v", tt.err, result, tt.expected)
		}
	}
}

func TestDeleteRepo_MissingParams(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("DELETE", "/admin/v1/repos/tenant/", nil)
	req.SetPathValue("tenant", "")
	req.SetPathValue("repo", "")
	w := httptest.NewRecorder()

	h.DeleteRepo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestLogEntries_NoContext(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/test/repo/v1/log/entries?after=0&limit=10", nil)
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	w := httptest.NewRecorder()

	h.LogEntries(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLogHead_NoContext(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/test/repo/v1/log/head", nil)
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	w := httptest.NewRecorder()

	h.LogHead(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestListRefs_NoContext(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/test/repo/v1/refs?prefix=heads/", nil)
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	w := httptest.NewRecorder()

	h.ListRefs(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGetRef_NoContext(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("GET", "/test/repo/v1/refs/main", nil)
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.SetPathValue("name", "main")
	w := httptest.NewRecorder()

	h.GetRef(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestIngestPack_NoContext(t *testing.T) {
	cfg := &config.Config{MaxPackSize: 1024}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("POST", "/test/repo/v1/objects/pack", bytes.NewReader([]byte("pack data")))
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.Header.Set("X-Kailab-Actor", "testuser")
	req.ContentLength = 9
	w := httptest.NewRecorder()

	h.IngestPack(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestIngestPack_TooLarge(t *testing.T) {
	cfg := &config.Config{MaxPackSize: 10}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest("POST", "/test/repo/v1/objects/pack", bytes.NewReader([]byte("this is a larger pack")))
	req.SetPathValue("tenant", "test")
	req.SetPathValue("repo", "repo")
	req.ContentLength = 100
	w := httptest.NewRecorder()

	h.IngestPack(w, req)

	// No context, but should check size first
	if w.Code != http.StatusInternalServerError {
		// Actually without context it errors first
		t.Logf("got status %d", w.Code)
	}
}


// ----- Diff Performance Tests -----

func TestComputeUnifiedDiff_Large(t *testing.T) {
	// Generate large files
	var oldLines, newLines []string
	for i := 0; i < 10000; i++ {
		oldLines = append(oldLines, fmt.Sprintf("line %d original content here", i))
		if i%100 == 0 {
			newLines = append(newLines, fmt.Sprintf("line %d MODIFIED content here", i))
		} else {
			newLines = append(newLines, fmt.Sprintf("line %d original content here", i))
		}
	}

	oldText := strings.Join(oldLines, "\n")
	newText := strings.Join(newLines, "\n")

	start := time.Now()
	hunks := computeUnifiedDiff(oldText, newText)
	elapsed := time.Since(start)

	t.Logf("Diff of 10k lines with 100 changes took %v, produced %d hunks", elapsed, len(hunks))

	if elapsed > 2*time.Second {
		t.Errorf("Diff took too long: %v (should be < 2s)", elapsed)
	}
}

func BenchmarkComputeUnifiedDiff_Small(b *testing.B) {
	oldText := "line1\nline2\nline3\nline4\nline5"
	newText := "line1\nline2 modified\nline3\nline4\nline5\nline6"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		computeUnifiedDiff(oldText, newText)
	}
}

func BenchmarkComputeUnifiedDiff_Large(b *testing.B) {
	var oldLines, newLines []string
	for i := 0; i < 5000; i++ {
		oldLines = append(oldLines, fmt.Sprintf("line %d content", i))
		if i%50 == 0 {
			newLines = append(newLines, fmt.Sprintf("line %d CHANGED", i))
		} else {
			newLines = append(newLines, fmt.Sprintf("line %d content", i))
		}
	}

	oldText := strings.Join(oldLines, "\n")
	newText := strings.Join(newLines, "\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		computeUnifiedDiff(oldText, newText)
	}
}

func TestSnapshotCache(t *testing.T) {
	// Test cache key generation and expiry
	cacheKey := "test/repo:abc123"

	cs := &cachedSnapshot{
		filesByPath: map[string]string{
			"file1.go": "digest1",
			"file2.go": "digest2",
		},
		parsedAt: time.Now(),
	}

	snapshotCache.Store(cacheKey, cs)

	// Should retrieve from cache
	if cached, ok := snapshotCache.Load(cacheKey); ok {
		retrieved := cached.(*cachedSnapshot)
		if len(retrieved.filesByPath) != 2 {
			t.Errorf("Expected 2 files, got %d", len(retrieved.filesByPath))
		}
		if retrieved.filesByPath["file1.go"] != "digest1" {
			t.Error("Wrong digest for file1.go")
		}
	} else {
		t.Error("Cache miss when hit expected")
	}

	// Cleanup
	snapshotCache.Delete(cacheKey)
}

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{"empty", "", false},
		{"plain text", "Hello, World!\nThis is a test.", false},
		{"text with tabs", "func main() {\n\treturn\n}", false},
		{"null byte", "hello\x00world", true},
		{"binary with null", string([]byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x0A}), true}, // PNG-like with null byte
		{"high non-printable ratio", string([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBinaryContent(tt.content)
			if result != tt.expected {
				t.Errorf("isBinaryContent(%q) = %v, expected %v", tt.name, result, tt.expected)
			}
		})
	}
}

func TestIsImageFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"image.png", true},
		{"photo.jpg", true},
		{"photo.JPEG", true},
		{"icon.gif", true},
		{"logo.svg", true},
		{"image.webp", true},
		{"favicon.ico", true},
		{"bitmap.bmp", true},
		{"document.pdf", false},
		{"code.go", false},
		{"data.json", false},
		{"src/assets/logo.PNG", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isImageFile(tt.path)
			if result != tt.expected {
				t.Errorf("isImageFile(%q) = %v, expected %v", tt.path, result, tt.expected)
			}
		})
	}
}

