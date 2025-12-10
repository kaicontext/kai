package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"kailab-control/internal/auth"
	"kailab-control/internal/db"
)

// ----- Magic Link -----

type SendMagicLinkRequest struct {
	Email string `json:"email"`
}

type SendMagicLinkResponse struct {
	Message string `json:"message"`
	// In dev mode, we include the token for easy testing
	DevToken string `json:"dev_token,omitempty"`
}

func (h *Handler) SendMagicLink(w http.ResponseWriter, r *http.Request) {
	var req SendMagicLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email required", nil)
		return
	}

	// Generate magic link token
	token, tokenHash, err := auth.GenerateMagicLink()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", err)
		return
	}

	// Store in DB
	expiresAt := time.Now().Add(h.cfg.MagicLinkTTL)
	if err := h.db.CreateMagicLink(req.Email, tokenHash, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create magic link", err)
		return
	}

	// Build login URL
	loginURL := h.cfg.BaseURL + "/auth/verify?token=" + token

	// In dev mode, log the token and include in response
	resp := SendMagicLinkResponse{
		Message: "Check your email for a login link",
	}
	if h.cfg.Debug {
		log.Printf("Magic link for %s: %s", req.Email, loginURL)
		resp.DevToken = token
	}

	// Send email in production (when Postmark is configured)
	if h.email != nil {
		if err := h.email.SendMagicLink(req.Email, loginURL); err != nil {
			log.Printf("Failed to send magic link email to %s: %v", req.Email, err)
			writeError(w, http.StatusInternalServerError, "failed to send email", err)
			return
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// ----- Exchange Token -----

type ExchangeTokenRequest struct {
	MagicToken string `json:"magic_token"`
}

type ExchangeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (h *Handler) ExchangeToken(w http.ResponseWriter, r *http.Request) {
	var req ExchangeTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.MagicToken == "" {
		writeError(w, http.StatusBadRequest, "magic_token required", nil)
		return
	}

	// Look up the magic link
	tokenHash := auth.HashToken(req.MagicToken)
	magicLink, err := h.db.GetMagicLink(tokenHash)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusUnauthorized, "invalid or expired token", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify token", err)
		return
	}

	// Check if expired
	if time.Now().After(magicLink.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "token expired", nil)
		return
	}

	// Check if already used
	if !magicLink.UsedAt.IsZero() {
		writeError(w, http.StatusUnauthorized, "token already used", nil)
		return
	}

	// Mark as used
	if err := h.db.UseMagicLink(magicLink.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to use token", err)
		return
	}

	// Get or create user
	user, created, err := h.db.GetOrCreateUser(magicLink.Email, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get/create user", err)
		return
	}
	if created {
		log.Printf("Created new user: %s", user.Email)
	}

	// Update last login
	h.db.UpdateLastLogin(user.ID)

	// Get user's orgs for token
	orgs, _ := h.db.ListUserOrgs(user.ID)
	var orgSlugs []string
	for _, o := range orgs {
		orgSlugs = append(orgSlugs, o.Slug)
	}

	// Generate access token
	accessToken, err := h.tokens.GenerateAccessToken(
		user.ID,
		user.Email,
		orgSlugs,
		[]string{"repo:read", "repo:write", "org:read", "org:write"},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", err)
		return
	}

	// Generate refresh token and create session
	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate refresh token", err)
		return
	}

	_, err = h.db.CreateSession(
		user.ID,
		refreshHash,
		r.UserAgent(),
		r.RemoteAddr,
		time.Now().Add(h.cfg.RefreshTokenTTL),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session", err)
		return
	}

	// Set HttpOnly cookies for browser security
	setAuthCookies(w, accessToken, refreshToken, h.cfg.AccessTokenTTL, h.cfg.RefreshTokenTTL, h.cfg.Debug)

	writeJSON(w, http.StatusOK, ExchangeTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(h.cfg.AccessTokenTTL.Seconds()),
		RefreshToken: refreshToken,
	})
}

// ----- Refresh Token -----

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token required", nil)
		return
	}

	// Look up session
	refreshHash := auth.HashToken(req.RefreshToken)
	session, err := h.db.GetSessionByRefreshHash(refreshHash)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusUnauthorized, "invalid refresh token", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify token", err)
		return
	}

	// Check if expired
	if time.Now().After(session.ExpiresAt) {
		h.db.DeleteSession(session.ID)
		writeError(w, http.StatusUnauthorized, "refresh token expired", nil)
		return
	}

	// Get user
	user, err := h.db.GetUserByID(session.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found", nil)
		return
	}

	// Get user's orgs
	orgs, _ := h.db.ListUserOrgs(user.ID)
	var orgSlugs []string
	for _, o := range orgs {
		orgSlugs = append(orgSlugs, o.Slug)
	}

	// Generate new access token
	accessToken, err := h.tokens.GenerateAccessToken(
		user.ID,
		user.Email,
		orgSlugs,
		[]string{"repo:read", "repo:write", "org:read", "org:write"},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", err)
		return
	}

	// Set HttpOnly cookie for browser security (access token only on refresh)
	setAccessTokenCookie(w, accessToken, h.cfg.AccessTokenTTL, h.cfg.Debug)

	writeJSON(w, http.StatusOK, ExchangeTokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(h.cfg.AccessTokenTTL.Seconds()),
	})
}

// ----- Logout -----

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	// Delete all sessions for this user
	if err := h.db.DeleteUserSessions(user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to logout", err)
		return
	}

	// Clear auth cookies
	clearAuthCookies(w)

	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// ----- Me -----

type MeResponse struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Name      string     `json:"name,omitempty"`
	CreatedAt string     `json:"created_at"`
	Orgs      []OrgBrief `json:"orgs"`
}

type OrgBrief struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgs, _ := h.db.ListUserOrgs(user.ID)
	var orgBriefs []OrgBrief
	for _, o := range orgs {
		membership, _ := h.db.GetMembership(o.ID, user.ID)
		role := ""
		if membership != nil {
			role = membership.Role
		}
		orgBriefs = append(orgBriefs, OrgBrief{
			ID:   o.ID,
			Slug: o.Slug,
			Name: o.Name,
			Role: role,
		})
	}

	writeJSON(w, http.StatusOK, MeResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
		Orgs:      orgBriefs,
	})
}

// ----- API Tokens -----

type CreateTokenRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	OrgID  string   `json:"org_id,omitempty"`
}

type CreateTokenResponse struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Token     string   `json:"token"` // Only shown once!
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
}

func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required", nil)
		return
	}

	// Default scopes
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"repo:read", "repo:write"}
	}

	// Generate PAT
	token, hash, err := auth.GeneratePAT()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token", err)
		return
	}

	// Store in DB
	apiToken, err := h.db.CreateAPIToken(user.ID, req.OrgID, req.Name, hash, req.Scopes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token", err)
		return
	}

	writeJSON(w, http.StatusCreated, CreateTokenResponse{
		ID:        apiToken.ID,
		Name:      apiToken.Name,
		Token:     token, // Only returned once!
		Scopes:    apiToken.Scopes,
		CreatedAt: apiToken.CreatedAt.Format(time.RFC3339),
	})
}

type TokenInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
}

func (h *Handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	tokens, err := h.db.ListUserAPITokens(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens", err)
		return
	}

	var infos []TokenInfo
	for _, t := range tokens {
		info := TokenInfo{
			ID:        t.ID,
			Name:      t.Name,
			Scopes:    t.Scopes,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if !t.LastUsedAt.IsZero() {
			info.LastUsedAt = t.LastUsedAt.Format(time.RFC3339)
		}
		infos = append(infos, info)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tokens": infos})
}

func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	tokenID := r.PathValue("id")
	if tokenID == "" {
		writeError(w, http.StatusBadRequest, "invalid token id", nil)
		return
	}

	// Get token to verify ownership
	token, err := h.db.GetAPIToken(tokenID)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusNotFound, "token not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get token", err)
		return
	}

	if token.UserID != user.ID {
		writeError(w, http.StatusForbidden, "not your token", nil)
		return
	}

	if err := h.db.DeleteAPIToken(tokenID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete token", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----- Cookie Helpers -----

const (
	accessTokenCookie  = "kai_access_token"
	refreshTokenCookie = "kai_refresh_token"
)

// setAuthCookies sets both access and refresh token cookies.
func setAuthCookies(w http.ResponseWriter, accessToken, refreshToken string, accessTTL, refreshTTL time.Duration, debug bool) {
	setAccessTokenCookie(w, accessToken, accessTTL, debug)

	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    refreshToken,
		Path:     "/api/v1/auth", // Only sent to auth endpoints
		MaxAge:   int(refreshTTL.Seconds()),
		HttpOnly: true,
		Secure:   !debug, // Secure in production (HTTPS only)
		SameSite: http.SameSiteLaxMode,
	})
}

// setAccessTokenCookie sets the access token cookie.
func setAccessTokenCookie(w http.ResponseWriter, accessToken string, accessTTL time.Duration, debug bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessTokenCookie,
		Value:    accessToken,
		Path:     "/",
		MaxAge:   int(accessTTL.Seconds()),
		HttpOnly: true,
		Secure:   !debug, // Secure in production (HTTPS only)
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAuthCookies clears both auth cookies.
func clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessTokenCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
