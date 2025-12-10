package api

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"kailab-control/internal/auth"
	"kailab-control/internal/cfg"
	"kailab-control/internal/db"
	"kailab-control/internal/routing"
)

//go:embed all:web
var webFS embed.FS

// getWebFS returns the web filesystem rooted at "web"
func getWebFS() http.FileSystem {
	sub, _ := fs.Sub(webFS, "web")
	return http.FS(sub)
}

// Handler wraps dependencies for HTTP handlers.
type Handler struct {
	db      *db.DB
	cfg     *cfg.Config
	tokens  *auth.TokenService
	shards  *routing.ShardPicker
}

// NewHandler creates a new API handler.
func NewHandler(database *db.DB, config *cfg.Config, tokens *auth.TokenService, shards *routing.ShardPicker) *Handler {
	return &Handler{
		db:     database,
		cfg:    config,
		tokens: tokens,
		shards: shards,
	}
}

// NewRouter creates the HTTP router with all routes registered.
func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()

	// Data plane proxy: /{org}/{repo}/v1/*
	// This handles all kailabd passthrough requests
	mux.Handle("/{org}/{repo}/v1/", h.ProxyHandler())

	// Health
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /healthz", h.Health)
	mux.HandleFunc("GET /readyz", h.Ready)

	// JWKS endpoint for kailabd to verify tokens
	mux.HandleFunc("GET /.well-known/jwks.json", h.JWKS)

	// Auth (public) - under /api/v1/ to avoid conflict with data plane proxy
	mux.HandleFunc("POST /api/v1/auth/magic-link", h.SendMagicLink)
	mux.HandleFunc("POST /api/v1/auth/token", h.ExchangeToken)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.RefreshToken)
	mux.Handle("POST /api/v1/auth/logout", h.WithAuth(http.HandlerFunc(h.Logout)))

	// User (authenticated)
	mux.Handle("GET /api/v1/me", h.WithAuth(http.HandlerFunc(h.GetMe)))

	// Orgs (authenticated)
	mux.Handle("POST /api/v1/orgs", h.WithAuth(http.HandlerFunc(h.CreateOrg)))
	mux.Handle("GET /api/v1/orgs", h.WithAuth(http.HandlerFunc(h.ListOrgs)))
	mux.Handle("GET /api/v1/orgs/{org}", Chain(
		http.HandlerFunc(h.GetOrg),
		h.WithAuth,
		h.WithOrg,
	))

	// Org members (authenticated + org)
	mux.Handle("GET /api/v1/orgs/{org}/members", Chain(
		http.HandlerFunc(h.ListMembers),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("reporter"),
	))
	mux.Handle("POST /api/v1/orgs/{org}/members", Chain(
		http.HandlerFunc(h.AddMember),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("admin"),
	))
	mux.Handle("DELETE /api/v1/orgs/{org}/members/{user_id}", Chain(
		http.HandlerFunc(h.RemoveMember),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("admin"),
	))

	// Repos (authenticated + org)
	mux.Handle("GET /api/v1/orgs/{org}/repos", Chain(
		http.HandlerFunc(h.ListRepos),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("reporter"),
	))
	mux.Handle("POST /api/v1/orgs/{org}/repos", Chain(
		http.HandlerFunc(h.CreateRepo),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("developer"),
	))
	mux.Handle("GET /api/v1/orgs/{org}/repos/{repo}", Chain(
		http.HandlerFunc(h.GetRepo),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("reporter"),
		h.WithRepo,
	))
	mux.Handle("DELETE /api/v1/orgs/{org}/repos/{repo}", Chain(
		http.HandlerFunc(h.DeleteRepo),
		h.WithAuth,
		h.WithOrg,
		h.RequireMembership("admin"),
		h.WithRepo,
	))

	// API Tokens (authenticated)
	mux.Handle("GET /api/v1/tokens", h.WithAuth(http.HandlerFunc(h.ListTokens)))
	mux.Handle("POST /api/v1/tokens", h.WithAuth(http.HandlerFunc(h.CreateToken)))
	mux.Handle("DELETE /api/v1/tokens/{id}", h.WithAuth(http.HandlerFunc(h.DeleteToken)))

	// Wrap mux with web console fallback
	return webConsoleFallback(mux)
}

// webConsoleFallback wraps a handler and serves the web console for unmatched GET requests
func webConsoleFallback(next http.Handler) http.Handler {
	webFileServer := http.FileServer(getWebFS())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a request for static assets or SPA routes
		if r.Method == http.MethodGet {
			path := r.URL.Path

			// Serve static assets directly
			if strings.HasPrefix(path, "/_app/") ||
				strings.HasPrefix(path, "/favicon") ||
				strings.HasSuffix(path, ".js") ||
				strings.HasSuffix(path, ".css") ||
				strings.HasSuffix(path, ".png") ||
				strings.HasSuffix(path, ".svg") ||
				strings.HasSuffix(path, ".ico") {
				webFileServer.ServeHTTP(w, r)
				return
			}

			// For root or SPA routes that don't match API/proxy patterns, serve index.html
			if path == "/" || (!strings.HasPrefix(path, "/api/") &&
				!strings.HasPrefix(path, "/health") &&
				!strings.HasPrefix(path, "/.well-known/") &&
				!strings.Contains(path, "/v1/")) {
				r.URL.Path = "/"
				webFileServer.ServeHTTP(w, r)
				return
			}
		}

		// Otherwise, pass to the main mux
		next.ServeHTTP(w, r)
	})
}

// ----- Health -----

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: h.cfg.Version,
	})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Check DB is accessible
	if err := h.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Status:  "not ready",
			Version: h.cfg.Version,
		})
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:  "ready",
		Version: h.cfg.Version,
	})
}

func (h *Handler) JWKS(w http.ResponseWriter, r *http.Request) {
	// For now, return an empty JWKS since we use symmetric signing
	// In production, you'd use asymmetric keys and publish the public key here
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"keys": []interface{}{},
	})
}

// ----- Helpers -----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string, err error) {
	resp := ErrorResponse{Error: msg}
	if err != nil {
		resp.Details = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
