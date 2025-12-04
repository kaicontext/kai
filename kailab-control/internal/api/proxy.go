package api

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"kailab-control/internal/auth"
	"kailab-control/internal/db"
	"kailab-control/internal/model"
)

// ProxyHandler returns an http.Handler that proxies requests to kailabd shards.
func (h *Handler) ProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse org and repo from path: /{org}/{repo}/v1/...
		orgSlug := r.PathValue("org")
		repoName := r.PathValue("repo")

		if orgSlug == "" || repoName == "" {
			writeError(w, http.StatusBadRequest, "org and repo required", nil)
			return
		}

		// Get org
		org, err := h.db.GetOrgBySlug(orgSlug)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "org not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get org", err)
			return
		}

		// Get repo
		repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "repo not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get repo", err)
			return
		}

		// Authenticate
		token := auth.ExtractBearerToken(r.Header.Get("Authorization"))

		var user *model.User
		var claims *auth.Claims

		if token != "" {
			if auth.IsPAT(token) {
				// Personal Access Token
				hash := auth.HashToken(token)
				apiToken, err := h.db.GetAPITokenByHash(hash)
				if err == nil {
					user, _ = h.db.GetUserByID(apiToken.UserID)
					h.db.UpdateAPITokenLastUsed(apiToken.ID)
					claims = &auth.Claims{
						UserID:  user.ID,
						Email:   user.Email,
						Scopes:  apiToken.Scopes,
						TokenID: apiToken.ID,
					}
				}
			} else {
				// JWT
				claims, _ = h.tokens.ValidateAccessToken(token)
				if claims != nil {
					user, _ = h.db.GetUserByID(claims.UserID)
				}
			}
		}

		// Authorization check
		isReadOnly := isReadOnlyRequest(r)
		isPublic := repo.Visibility == "public"

		if user == nil {
			// Unauthenticated
			if !isPublic || !isReadOnly {
				writeError(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}
			// Allow unauthenticated read of public repos
		} else {
			// Check membership
			membership, err := h.db.GetMembership(org.ID, user.ID)
			if err != nil {
				if err == db.ErrNotFound && !isPublic {
					writeError(w, http.StatusForbidden, "not a member of this org", nil)
					return
				}
				// Public repo - allow read
				if !isReadOnly && err == db.ErrNotFound {
					writeError(w, http.StatusForbidden, "not a member of this org", nil)
					return
				}
			}

			// Check permissions for non-public repos
			if membership != nil {
				if isReadOnly {
					if !model.HasAtLeastRole(membership.Role, model.RoleReporter) {
						writeError(w, http.StatusForbidden, "insufficient permissions", nil)
						return
					}
				} else {
					if !model.HasAtLeastRole(membership.Role, model.RoleDeveloper) {
						writeError(w, http.StatusForbidden, "insufficient permissions", nil)
						return
					}
				}
			}

			// Check scopes
			if claims != nil {
				if isReadOnly {
					if !model.HasScope(claims.Scopes, model.ScopeRepoRead) {
						writeError(w, http.StatusForbidden, "missing scope: repo:read", nil)
						return
					}
				} else {
					if !model.HasScope(claims.Scopes, model.ScopeRepoWrite) {
						writeError(w, http.StatusForbidden, "missing scope: repo:write", nil)
						return
					}
				}
			}
		}

		// Get shard URL
		shardURL := h.shards.GetShardURL(repo.ShardHint)
		if shardURL == "" {
			writeError(w, http.StatusInternalServerError, "shard not available", nil)
			return
		}

		// Generate downstream JWT for kailabd
		var downstreamToken string
		if user != nil {
			var err error
			scopes := []string{model.ScopeRepoRead}
			if !isReadOnly {
				scopes = append(scopes, model.ScopeRepoWrite)
			}
			downstreamToken, err = h.tokens.GenerateDownstreamToken(user.ID, user.Email, orgSlug, scopes)
			if err != nil {
				log.Printf("Failed to generate downstream token: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to generate downstream token", err)
				return
			}
		}

		// Build target URL
		// Original path: /{org}/{repo}/v1/...
		// Target path: /{org}/{repo}/v1/... (same structure)
		targetURL, err := url.Parse(shardURL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid shard URL", err)
			return
		}

		// Create reverse proxy
		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = targetURL.Scheme
				req.URL.Host = targetURL.Host
				req.Host = targetURL.Host

				// Keep the original path (/{org}/{repo}/v1/...)
				// Path is already set correctly

				// Remove original auth header
				req.Header.Del("Authorization")

				// Add downstream JWT if we have one
				if downstreamToken != "" {
					req.Header.Set("Authorization", "Bearer "+downstreamToken)
				}

				// Add actor header
				if user != nil {
					req.Header.Set("X-Kailab-Actor", user.Email)
				} else {
					req.Header.Set("X-Kailab-Actor", "anonymous")
				}

				// Add request ID for tracing
				req.Header.Set("X-Request-ID", generateRequestID())

				if h.cfg.Debug {
					log.Printf("Proxying %s %s -> %s%s", req.Method, r.URL.Path, shardURL, req.URL.Path)
				}
			},
			ModifyResponse: func(resp *http.Response) error {
				// Add CORS headers to response
				resp.Header.Set("Access-Control-Allow-Origin", "*")
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				log.Printf("Proxy error: %v", err)
				writeError(w, http.StatusBadGateway, "data plane unavailable", err)
			},
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}

		// Serve the proxy
		proxy.ServeHTTP(w, r)
	})
}

// StreamingProxy is a simpler streaming proxy implementation.
func (h *Handler) StreamingProxy(w http.ResponseWriter, r *http.Request, targetURL string, downstreamToken, actor string) {
	// Create the target URL
	target, err := url.Parse(targetURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid target URL", err)
		return
	}

	// Merge path
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery

	// Create the outgoing request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create request", err)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		// Skip hop-by-hop headers
		if isHopByHop(key) {
			continue
		}
		for _, value := range values {
			outReq.Header.Add(key, value)
		}
	}

	// Replace auth
	outReq.Header.Del("Authorization")
	if downstreamToken != "" {
		outReq.Header.Set("Authorization", "Bearer "+downstreamToken)
	}
	outReq.Header.Set("X-Kailab-Actor", actor)
	outReq.Header.Set("X-Request-ID", generateRequestID())

	// Make the request
	client := &http.Client{
		Timeout: 5 * time.Minute, // Long timeout for large uploads
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	resp, err := client.Do(outReq)
	if err != nil {
		log.Printf("Proxy error: %v", err)
		writeError(w, http.StatusBadGateway, "data plane unavailable", err)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Write status
	w.WriteHeader(resp.StatusCode)

	// Stream body
	io.Copy(w, resp.Body)
}

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Proxy-Connection":    true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func isHopByHop(header string) bool {
	return hopByHopHeaders[strings.Title(header)]
}

func generateRequestID() string {
	return time.Now().Format("20060102150405.000000")
}
