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
	"kailab-control/internal/email"
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
	email   *email.Client
}

// NewHandler creates a new API handler.
func NewHandler(database *db.DB, config *cfg.Config, tokens *auth.TokenService, shards *routing.ShardPicker) *Handler {
	var emailClient *email.Client
	if config.PostmarkToken != "" {
		emailClient = email.New(config.PostmarkToken, config.MagicLinkFrom)
	}

	return &Handler{
		db:     database,
		cfg:    config,
		tokens: tokens,
		shards: shards,
		email:  emailClient,
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

	// CLI install script
	mux.HandleFunc("GET /install.sh", h.InstallScript)

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

// InstallScript serves the CLI install script
func (h *Handler) InstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(installScript))
}

const installScript = `#!/bin/sh
# Kai CLI installer
# Usage: curl -fsSL https://kaiscm.com/install.sh | sh

set -e

INSTALL_DIR="${KAI_INSTALL_DIR:-/usr/local/bin}"
VERSION="${KAI_VERSION:-latest}"
BASE_URL="https://gitlab.com/api/v4/projects/preplan%2Fkai/packages/generic/kai-cli/${VERSION}"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux)  ;;
    darwin) ;;
    *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY="kai-${OS}-${ARCH}"
URL="${BASE_URL}/${BINARY}.gz"

echo "Installing Kai CLI..."
echo "  Version: $VERSION"
echo "  OS: $OS"
echo "  Arch: $ARCH"
echo ""

# Create temp directory
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

# Download and extract
if command -v curl > /dev/null; then
    curl -fsSL "$URL" -o "$TMP_DIR/kai.gz"
elif command -v wget > /dev/null; then
    wget -q "$URL" -O "$TMP_DIR/kai.gz"
else
    echo "Error: curl or wget required"
    exit 1
fi

gzip -d "$TMP_DIR/kai.gz"
chmod +x "$TMP_DIR/kai"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP_DIR/kai" "$INSTALL_DIR/kai"
else
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$TMP_DIR/kai" "$INSTALL_DIR/kai"
fi

echo ""
echo "Kai CLI installed successfully!"
echo ""
echo "Get started:"
echo "  kai init              # Initialize in a project"
echo "  kai --help            # See all commands"
echo ""
`
