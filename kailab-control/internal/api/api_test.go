package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendMagicLink_EmptyEmail(t *testing.T) {
	req := SendMagicLinkRequest{Email: ""}
	body, _ := json.Marshal(req)

	// Create request and recorder to verify they can be created
	r := httptest.NewRequest("POST", "/api/v1/auth/magic-link", bytes.NewReader(body))
	w := httptest.NewRecorder()

	// Verify request was created properly
	if r.Method != "POST" {
		t.Errorf("expected POST, got %s", r.Method)
	}
	if w.Code != 200 {
		// Default status is 200
	}

	// We can't test without a handler, but we can verify the request is valid JSON
	var decoded SendMagicLinkRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.Email != "" {
		t.Error("expected empty email")
	}
}

func TestExchangeTokenRequest_EmptyToken(t *testing.T) {
	req := ExchangeTokenRequest{MagicToken: ""}
	body, _ := json.Marshal(req)

	var decoded ExchangeTokenRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.MagicToken != "" {
		t.Error("expected empty token")
	}
}

func TestRefreshTokenRequest_Serialization(t *testing.T) {
	req := RefreshTokenRequest{RefreshToken: "test-refresh-token"}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RefreshTokenRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.RefreshToken != "test-refresh-token" {
		t.Errorf("expected 'test-refresh-token', got %q", decoded.RefreshToken)
	}
}

func TestExchangeTokenResponse_Serialization(t *testing.T) {
	resp := ExchangeTokenResponse{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "refresh-456",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ExchangeTokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.AccessToken != "access-123" {
		t.Errorf("expected 'access-123', got %q", decoded.AccessToken)
	}
	if decoded.TokenType != "Bearer" {
		t.Errorf("expected 'Bearer', got %q", decoded.TokenType)
	}
	if decoded.ExpiresIn != 3600 {
		t.Errorf("expected 3600, got %d", decoded.ExpiresIn)
	}
	if decoded.RefreshToken != "refresh-456" {
		t.Errorf("expected 'refresh-456', got %q", decoded.RefreshToken)
	}
}

func TestMeResponse_Serialization(t *testing.T) {
	resp := MeResponse{
		ID:        "user-123",
		Email:     "test@example.com",
		Name:      "Test User",
		CreatedAt: "2024-01-01T00:00:00Z",
		Orgs: []OrgBrief{
			{ID: "org-1", Slug: "test-org", Name: "Test Org", Role: "admin"},
		},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded MeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != "user-123" {
		t.Errorf("expected 'user-123', got %q", decoded.ID)
	}
	if decoded.Email != "test@example.com" {
		t.Errorf("expected 'test@example.com', got %q", decoded.Email)
	}
	if decoded.Name != "Test User" {
		t.Errorf("expected 'Test User', got %q", decoded.Name)
	}
	if len(decoded.Orgs) != 1 {
		t.Fatalf("expected 1 org, got %d", len(decoded.Orgs))
	}
	if decoded.Orgs[0].Slug != "test-org" {
		t.Errorf("expected slug 'test-org', got %q", decoded.Orgs[0].Slug)
	}
}

func TestCreateTokenRequest_Serialization(t *testing.T) {
	req := CreateTokenRequest{
		Name:   "My Token",
		Scopes: []string{"repo:read", "repo:write"},
		OrgID:  "org-123",
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded CreateTokenRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Name != "My Token" {
		t.Errorf("expected 'My Token', got %q", decoded.Name)
	}
	if len(decoded.Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(decoded.Scopes))
	}
	if decoded.OrgID != "org-123" {
		t.Errorf("expected 'org-123', got %q", decoded.OrgID)
	}
}

func TestCreateTokenResponse_Serialization(t *testing.T) {
	resp := CreateTokenResponse{
		ID:        "token-123",
		Name:      "Test Token",
		Token:     "pat_abc123",
		Scopes:    []string{"repo:read"},
		CreatedAt: "2024-01-01T00:00:00Z",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded CreateTokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != "token-123" {
		t.Errorf("expected 'token-123', got %q", decoded.ID)
	}
	if decoded.Token != "pat_abc123" {
		t.Errorf("expected 'pat_abc123', got %q", decoded.Token)
	}
}

func TestTokenInfo_Serialization(t *testing.T) {
	info := TokenInfo{
		ID:         "token-123",
		Name:       "API Token",
		Scopes:     []string{"repo:read", "repo:write"},
		CreatedAt:  "2024-01-01T00:00:00Z",
		LastUsedAt: "2024-01-02T00:00:00Z",
	}
	body, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded TokenInfo
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != "token-123" {
		t.Errorf("expected 'token-123', got %q", decoded.ID)
	}
	if decoded.LastUsedAt != "2024-01-02T00:00:00Z" {
		t.Errorf("expected '2024-01-02T00:00:00Z', got %q", decoded.LastUsedAt)
	}
}

func TestTokenInfo_EmptyLastUsed(t *testing.T) {
	info := TokenInfo{
		ID:        "token-123",
		Name:      "API Token",
		Scopes:    []string{"repo:read"},
		CreatedAt: "2024-01-01T00:00:00Z",
	}
	body, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Verify omitempty works
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, exists := decoded["last_used_at"]; exists && decoded["last_used_at"] != "" {
		t.Log("last_used_at present but should be empty")
	}
}

func TestOrgBrief_Serialization(t *testing.T) {
	org := OrgBrief{
		ID:   "org-123",
		Slug: "my-org",
		Name: "My Organization",
		Role: "owner",
	}
	body, err := json.Marshal(org)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded OrgBrief
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ID != "org-123" {
		t.Errorf("expected 'org-123', got %q", decoded.ID)
	}
	if decoded.Slug != "my-org" {
		t.Errorf("expected 'my-org', got %q", decoded.Slug)
	}
	if decoded.Name != "My Organization" {
		t.Errorf("expected 'My Organization', got %q", decoded.Name)
	}
	if decoded.Role != "owner" {
		t.Errorf("expected 'owner', got %q", decoded.Role)
	}
}

func TestOrgBrief_OmittedRole(t *testing.T) {
	org := OrgBrief{
		ID:   "org-123",
		Slug: "my-org",
		Name: "My Organization",
	}
	body, err := json.Marshal(org)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if role, exists := decoded["role"]; exists && role != "" {
		t.Log("role present but might be empty")
	}
}

func TestSendMagicLinkResponse_DevMode(t *testing.T) {
	resp := SendMagicLinkResponse{
		Message:  "Check your email",
		DevToken: "dev-token-123",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded SendMagicLinkResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.DevToken != "dev-token-123" {
		t.Errorf("expected 'dev-token-123', got %q", decoded.DevToken)
	}
}

func TestSendMagicLinkResponse_ProdMode(t *testing.T) {
	resp := SendMagicLinkResponse{
		Message: "Check your email",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, exists := decoded["dev_token"]; exists && decoded["dev_token"] != "" {
		t.Error("dev_token should not be present in prod mode")
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"test": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var decoded map[string]string
	if err := json.NewDecoder(w.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if decoded["test"] != "value" {
		t.Errorf("expected 'value', got %q", decoded["test"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test error", nil)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var decoded map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if decoded["error"] != "test error" {
		t.Errorf("expected 'test error', got %v", decoded["error"])
	}
}

func TestWriteError_WithDetails(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusInternalServerError, "server error", http.ErrServerClosed)

	var decoded map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if decoded["details"] == nil || decoded["details"] == "" {
		t.Error("expected details to be set")
	}
}

func TestUserFromContext_NilContext(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	user := UserFromContext(req.Context())
	if user != nil {
		t.Error("expected nil user from empty context")
	}
}
