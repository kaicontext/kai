// Package api provides the HTTP API for the control plane.
package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"kailab-control/internal/auth"
	"kailab-control/internal/db"
	"kailab-control/internal/model"
)

type contextKey string

const (
	ctxUser   contextKey = "user"
	ctxClaims contextKey = "claims"
	ctxOrg    contextKey = "org"
	ctxRepo   contextKey = "repo"
)

// UserFromContext returns the user from context.
func UserFromContext(ctx context.Context) *model.User {
	if u, ok := ctx.Value(ctxUser).(*model.User); ok {
		return u
	}
	return nil
}

// ClaimsFromContext returns the claims from context.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	if c, ok := ctx.Value(ctxClaims).(*auth.Claims); ok {
		return c
	}
	return nil
}

// OrgFromContext returns the org from context.
func OrgFromContext(ctx context.Context) *model.Org {
	if o, ok := ctx.Value(ctxOrg).(*model.Org); ok {
		return o
	}
	return nil
}

// RepoFromContext returns the repo from context.
func RepoFromContext(ctx context.Context) *model.Repo {
	if r, ok := ctx.Value(ctxRepo).(*model.Repo); ok {
		return r
	}
	return nil
}

// WithAuth is middleware that authenticates requests.
func (h *Handler) WithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try Authorization header first, then cookie
		token := auth.ExtractBearerToken(r.Header.Get("Authorization"))
		if token == "" {
			if cookie, err := r.Cookie("kai_access_token"); err == nil {
				token = cookie.Value
			}
		}
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization", nil)
			return
		}

		var user *model.User
		var claims *auth.Claims

		if auth.IsPAT(token) {
			// Personal Access Token
			hash := auth.HashToken(token)
			apiToken, err := h.db.GetAPITokenByHash(hash)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token", nil)
				return
			}

			user, err = h.db.GetUserByID(apiToken.UserID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token", nil)
				return
			}

			// Update last used
			h.db.UpdateAPITokenLastUsed(apiToken.ID)

			claims = &auth.Claims{
				UserID:  user.ID,
				Email:   user.Email,
				Scopes:  apiToken.Scopes,
				TokenID: apiToken.ID,
			}
		} else {
			// JWT
			var err error
			claims, err = h.tokens.ValidateAccessToken(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token", nil)
				return
			}

			user, err = h.db.GetUserByID(claims.UserID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "user not found", nil)
				return
			}
		}

		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxClaims, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithOptionalAuth is middleware that authenticates if a token is present.
func (h *Handler) WithOptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try Authorization header first, then cookie
		token := auth.ExtractBearerToken(r.Header.Get("Authorization"))
		if token == "" {
			if cookie, err := r.Cookie("kai_access_token"); err == nil {
				token = cookie.Value
			}
		}
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		var user *model.User
		var claims *auth.Claims

		if auth.IsPAT(token) {
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
			claims, _ = h.tokens.ValidateAccessToken(token)
			if claims != nil {
				user, _ = h.db.GetUserByID(claims.UserID)
			}
		}

		ctx := r.Context()
		if user != nil {
			ctx = context.WithValue(ctx, ctxUser, user)
			ctx = context.WithValue(ctx, ctxClaims, claims)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithOrg is middleware that loads the org from the URL.
func (h *Handler) WithOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgSlug := r.PathValue("org")
		if orgSlug == "" {
			writeError(w, http.StatusBadRequest, "org required", nil)
			return
		}

		org, err := h.db.GetOrgBySlug(orgSlug)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "org not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get org", err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxOrg, org)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithRepo is middleware that loads the repo from the URL.
func (h *Handler) WithRepo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := OrgFromContext(r.Context())
		if org == nil {
			writeError(w, http.StatusInternalServerError, "org not in context", nil)
			return
		}

		repoName := r.PathValue("repo")
		if repoName == "" {
			writeError(w, http.StatusBadRequest, "repo required", nil)
			return
		}

		repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
		if err != nil {
			if err == db.ErrNotFound {
				writeError(w, http.StatusNotFound, "repo not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get repo", err)
			return
		}

		ctx := context.WithValue(r.Context(), ctxRepo, repo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireMembership is middleware that checks org membership.
func (h *Handler) RequireMembership(minRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			org := OrgFromContext(r.Context())

			if user == nil {
				writeError(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}

			if org == nil {
				writeError(w, http.StatusInternalServerError, "org not in context", nil)
				return
			}

			membership, err := h.db.GetMembership(org.ID, user.ID)
			if err != nil {
				if err == db.ErrNotFound {
					writeError(w, http.StatusForbidden, "not a member of this org", nil)
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to check membership", err)
				return
			}

			if !model.HasAtLeastRole(membership.Role, minRole) {
				writeError(w, http.StatusForbidden, "insufficient permissions", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireScope is middleware that checks for a required scope.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required", nil)
				return
			}

			if !model.HasScope(claims.Scopes, scope) {
				writeError(w, http.StatusForbidden, "missing required scope: "+scope, nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// WithDefaults adds default middleware to a handler.
func WithDefaults(h http.Handler, debug bool) http.Handler {
	return withLogging(withRecovery(withCORS(h)), debug)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Allow the requesting origin (for credentials support, can't use *)
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Kailab-Actor")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler, debug bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		if debug || wrapped.status >= 400 {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, wrapped.status, time.Since(start))
		}
	})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Chain combines multiple middleware.
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// isReadOnlyRequest checks if a request is read-only (safe for public access).
func isReadOnlyRequest(r *http.Request) bool {
	return r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS"
}

// isPublicRepoReadAllowed checks if unauthenticated access is allowed for a repo.
func isPublicRepoReadAllowed(repo *model.Repo) bool {
	return repo != nil && repo.Visibility == "public"
}

// ValidateSlug validates an org or repo slug.
func ValidateSlug(slug string) bool {
	if len(slug) < 1 || len(slug) > 63 {
		return false
	}
	for i, c := range slug {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '_' || c == '-' || c == '.' {
			// Can't start or end with special chars
			if i == 0 || i == len(slug)-1 {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// NormalizeSlug normalizes a slug to lowercase.
func NormalizeSlug(slug string) string {
	return strings.ToLower(slug)
}
