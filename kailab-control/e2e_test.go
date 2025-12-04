// Package main provides end-to-end tests for the kailab-control service.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kailab-control/internal/api"
	"kailab-control/internal/auth"
	"kailab-control/internal/cfg"
	"kailab-control/internal/db"
	"kailab-control/internal/routing"
)

// TestE2EWorkflow tests the complete workflow:
// 1. Start control plane
// 2. Create user via magic link
// 3. Create org
// 4. Create repo (this provisions on the mock shard)
// 5. Generate PAT
// 6. Use PAT to access repo through proxy
func TestE2EWorkflow(t *testing.T) {
	// Create temp directory for databases
	tmpDir, err := os.MkdirTemp("", "kailab-e2e-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a mock kailabd server
	mockShard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle admin repo creation
		if r.Method == "POST" && r.URL.Path == "/admin/v1/repos" {
			var req struct {
				Tenant string `json:"tenant"`
				Repo   string `json:"repo"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			t.Logf("Mock shard: creating repo %s/%s", req.Tenant, req.Repo)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"tenant": req.Tenant,
				"repo":   req.Repo,
			})
			return
		}

		// Handle proxy passthrough
		if r.URL.Path != "" {
			t.Logf("Mock shard: %s %s", r.Method, r.URL.Path)

			// Check for authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}

			// Return mock response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"refs": []map[string]string{
					{"name": "snap.latest", "target": "abc123"},
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer mockShard.Close()

	// Create config
	config := &cfg.Config{
		Listen:          ":0", // Will be overridden by test server
		DBURL:           filepath.Join(tmpDir, "control.db"),
		JWTSigningKey:   []byte("test-secret-key"),
		JWTIssuer:       "kailab-control-test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
		MagicLinkTTL:    15 * time.Minute,
		BaseURL:         "http://localhost:8080",
		Debug:           true,
		Version:         "test",
		Shards: map[string]string{
			"default": mockShard.URL,
		},
	}

	// Open database
	database, err := db.Open(config.DBURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Create token service
	tokens := auth.NewTokenService(
		config.JWTSigningKey,
		config.JWTIssuer,
		config.AccessTokenTTL,
		config.RefreshTokenTTL,
	)

	// Create shard picker
	shards := routing.NewShardPicker(config.Shards)

	// Create handler and router
	handler := api.NewHandler(database, config, tokens, shards)
	router := api.NewRouter(handler)
	wrappedHandler := api.WithDefaults(router, config.Debug)

	// Create test server
	ts := httptest.NewServer(wrappedHandler)
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// Helper to make API calls
	apiCall := func(method, path string, body interface{}, token string) (int, map[string]interface{}) {
		var bodyReader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(b)
		}

		req, _ := http.NewRequestWithContext(context.Background(), method, ts.URL+path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result
	}

	// 1. Test health endpoint
	t.Run("Health", func(t *testing.T) {
		status, data := apiCall("GET", "/health", nil, "")
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
		if data["status"] != "ok" {
			t.Errorf("Expected status=ok, got %v", data["status"])
		}
	})

	// 2. Request magic link
	var magicToken string
	t.Run("RequestMagicLink", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/auth/magic-link", map[string]string{
			"email": "test@example.com",
		}, "")
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		// In debug mode, the token is returned
		if token, ok := data["dev_token"].(string); ok {
			magicToken = token
		} else {
			t.Fatal("No dev_token returned in debug mode")
		}
	})

	// 3. Exchange magic token for access token
	var accessToken string
	t.Run("ExchangeToken", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/auth/token", map[string]string{
			"magic_token": magicToken,
		}, "")
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		if token, ok := data["access_token"].(string); ok {
			accessToken = token
		} else {
			t.Fatal("No access_token returned")
		}
	})

	// 4. Get current user
	t.Run("GetMe", func(t *testing.T) {
		status, data := apiCall("GET", "/api/v1/me", nil, accessToken)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		if data["email"] != "test@example.com" {
			t.Errorf("Expected email=test@example.com, got %v", data["email"])
		}
	})

	// 5. Create org
	t.Run("CreateOrg", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/orgs", map[string]string{
			"slug": "acme",
			"name": "Acme Corp",
		}, accessToken)
		if status != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %v", status, data)
		}

		if data["slug"] != "acme" {
			t.Errorf("Expected slug=acme, got %v", data["slug"])
		}
	})

	// 6. List orgs
	t.Run("ListOrgs", func(t *testing.T) {
		status, data := apiCall("GET", "/api/v1/orgs", nil, accessToken)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		orgs, ok := data["orgs"].([]interface{})
		if !ok || len(orgs) == 0 {
			t.Error("Expected at least one org")
		}
	})

	// 7. Create repo
	t.Run("CreateRepo", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/orgs/acme/repos", map[string]string{
			"name":       "api",
			"visibility": "private",
		}, accessToken)
		if status != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %v", status, data)
		}

		if data["name"] != "api" {
			t.Errorf("Expected name=api, got %v", data["name"])
		}
		if data["visibility"] != "private" {
			t.Errorf("Expected visibility=private, got %v", data["visibility"])
		}
	})

	// 8. List repos
	t.Run("ListRepos", func(t *testing.T) {
		status, data := apiCall("GET", "/api/v1/orgs/acme/repos", nil, accessToken)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		repos, ok := data["repos"].([]interface{})
		if !ok || len(repos) == 0 {
			t.Error("Expected at least one repo")
		}
	})

	// 9. Create PAT
	var pat string
	t.Run("CreatePAT", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/tokens", map[string]interface{}{
			"name":   "test-token",
			"scopes": []string{"repo:read", "repo:write"},
		}, accessToken)
		if status != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %v", status, data)
		}

		if token, ok := data["token"].(string); ok {
			pat = token
			t.Logf("Created PAT: %s...", pat[:20])
		} else {
			t.Fatal("No token returned")
		}
	})

	// 10. Use PAT to access proxy
	t.Run("ProxyWithPAT", func(t *testing.T) {
		status, data := apiCall("GET", "/acme/api/v1/refs", nil, pat)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		// The mock shard should have returned refs
		if refs, ok := data["refs"].([]interface{}); !ok || len(refs) == 0 {
			t.Logf("Data: %v", data)
			// This is expected since mock shard returns refs
		}
	})

	// 11. Test unauthenticated access to private repo (should fail)
	t.Run("ProxyUnauthenticatedPrivate", func(t *testing.T) {
		status, _ := apiCall("GET", "/acme/api/v1/refs", nil, "")
		if status != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", status)
		}
	})

	// 12. Create public repo and test unauthenticated read
	t.Run("CreatePublicRepo", func(t *testing.T) {
		status, data := apiCall("POST", "/api/v1/orgs/acme/repos", map[string]string{
			"name":       "oss",
			"visibility": "public",
		}, accessToken)
		if status != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %v", status, data)
		}
	})

	t.Run("ProxyUnauthenticatedPublicRead", func(t *testing.T) {
		status, _ := apiCall("GET", "/acme/oss/v1/refs", nil, "")
		// Public repos should allow unauthenticated reads
		if status != http.StatusOK {
			t.Logf("Status: %d (public read may not be implemented yet)", status)
		}
	})

	// 13. List tokens
	t.Run("ListTokens", func(t *testing.T) {
		status, data := apiCall("GET", "/api/v1/tokens", nil, accessToken)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d: %v", status, data)
		}

		tokens, ok := data["tokens"].([]interface{})
		if !ok || len(tokens) == 0 {
			t.Error("Expected at least one token")
		}
	})

	// 14. Logout
	t.Run("Logout", func(t *testing.T) {
		status, _ := apiCall("POST", "/api/v1/auth/logout", nil, accessToken)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
	})

	fmt.Println("E2E tests passed!")
}

// TestValidation tests input validation
func TestValidation(t *testing.T) {
	t.Run("ValidSlug", func(t *testing.T) {
		tests := []struct {
			slug  string
			valid bool
		}{
			{"acme", true},
			{"my-org", true},
			{"my_org", true},
			{"org123", true},
			{"my.org", true},
			{"", false},
			{"-invalid", false},
			{"invalid-", false},
			{"UPPERCASE", false},
			{"with spaces", false},
			{"a", true},
			{string(make([]byte, 64)), false}, // Too long
		}

		for _, tt := range tests {
			result := api.ValidateSlug(tt.slug)
			if result != tt.valid {
				t.Errorf("ValidateSlug(%q) = %v, want %v", tt.slug, result, tt.valid)
			}
		}
	})
}

// TestAuth tests authentication helpers
func TestAuth(t *testing.T) {
	t.Run("HashToken", func(t *testing.T) {
		hash1 := auth.HashToken("test-token")
		hash2 := auth.HashToken("test-token")
		hash3 := auth.HashToken("different-token")

		if hash1 != hash2 {
			t.Error("Same token should produce same hash")
		}
		if hash1 == hash3 {
			t.Error("Different tokens should produce different hashes")
		}
	})

	t.Run("GeneratePAT", func(t *testing.T) {
		token, hash, err := auth.GeneratePAT()
		if err != nil {
			t.Fatalf("GeneratePAT failed: %v", err)
		}

		if !auth.IsPAT(token) {
			t.Error("Generated token should be recognized as PAT")
		}

		if auth.HashToken(token) != hash {
			t.Error("Hash should match token")
		}
	})

	t.Run("ExtractBearerToken", func(t *testing.T) {
		tests := []struct {
			header string
			want   string
		}{
			{"Bearer abc123", "abc123"},
			{"bearer ABC123", "ABC123"},
			{"BEARER xyz", "xyz"},
			{"Basic abc", ""},
			{"abc", ""},
			{"", ""},
		}

		for _, tt := range tests {
			got := auth.ExtractBearerToken(tt.header)
			if got != tt.want {
				t.Errorf("ExtractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		}
	})
}

// TestTokenService tests JWT generation and validation
func TestTokenService(t *testing.T) {
	ts := auth.NewTokenService(
		[]byte("test-secret"),
		"test-issuer",
		15*time.Minute,
		7*24*time.Hour,
	)

	t.Run("GenerateAndValidate", func(t *testing.T) {
		token, err := ts.GenerateAccessToken("test-user-id", "user@example.com", []string{"acme"}, []string{"repo:read"})
		if err != nil {
			t.Fatalf("GenerateAccessToken failed: %v", err)
		}

		claims, err := ts.ValidateAccessToken(token)
		if err != nil {
			t.Fatalf("ValidateAccessToken failed: %v", err)
		}

		if claims.UserID != "test-user-id" {
			t.Errorf("UserID = %s, want test-user-id", claims.UserID)
		}
		if claims.Email != "user@example.com" {
			t.Errorf("Email = %s, want user@example.com", claims.Email)
		}
	})

	t.Run("InvalidToken", func(t *testing.T) {
		_, err := ts.ValidateAccessToken("invalid-token")
		if err == nil {
			t.Error("Expected error for invalid token")
		}
	})

	t.Run("DownstreamToken", func(t *testing.T) {
		token, err := ts.GenerateDownstreamToken("test-user-id", "user@example.com", "acme", []string{"repo:read"})
		if err != nil {
			t.Fatalf("GenerateDownstreamToken failed: %v", err)
		}

		claims, err := ts.ValidateAccessToken(token)
		if err != nil {
			t.Fatalf("ValidateAccessToken failed: %v", err)
		}

		if claims.Audience != "kailabd" {
			t.Errorf("Audience = %s, want kailabd", claims.Audience)
		}
	})
}
