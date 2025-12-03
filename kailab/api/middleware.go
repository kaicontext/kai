// Package api provides HTTP middleware for Kailab.
package api

import (
	"compress/gzip"
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"kailab/repo"
)

// WithDefaults wraps a handler with standard middleware.
func WithDefaults(h http.Handler) http.Handler {
	return LoggingMiddleware(
		TimeoutMiddleware(
			GzipMiddleware(h),
			30*time.Second,
		),
	)
}

// LoggingMiddleware logs all requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, lw.status, time.Since(start))
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (lw *loggingResponseWriter) WriteHeader(status int) {
	lw.status = status
	lw.ResponseWriter.WriteHeader(status)
}

// TimeoutMiddleware adds a timeout to requests.
func TimeoutMiddleware(next http.Handler, timeout time.Duration) http.Handler {
	return http.TimeoutHandler(next, timeout, "request timeout")
}

// GzipMiddleware decompresses gzip request bodies and compresses responses.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decompress request if gzipped
		if r.Header.Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "invalid gzip body", http.StatusBadRequest)
				return
			}
			defer gr.Close()
			r.Body = io.NopCloser(gr)
		}

		// Check if client accepts gzip
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			w = &gzipResponseWriter{ResponseWriter: w, Writer: gz}
		}

		next.ServeHTTP(w, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	io.Writer
}

func (grw *gzipResponseWriter) Write(p []byte) (int, error) {
	return grw.Writer.Write(p)
}

// Context keys for request-scoped values.
type ctxKey int

const (
	repoKey ctxKey = iota
	tenantKey
	repoNameKey
)

// WithRepo is middleware that extracts tenant/repo from URL and injects RepoHandle.
func WithRepo(reg *repo.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := r.PathValue("tenant")
			repoName := r.PathValue("repo")

			if tenant == "" || repoName == "" {
				http.Error(w, "tenant and repo required", http.StatusBadRequest)
				return
			}

			rh, err := reg.Get(r.Context(), tenant, repoName)
			if err != nil {
				if err == repo.ErrRepoNotFound {
					http.Error(w, "repo not found", http.StatusNotFound)
					return
				}
				log.Printf("error getting repo %s/%s: %v", tenant, repoName, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			// Mark as in-use
			reg.Acquire(rh)
			defer reg.Release(rh)

			// Inject into context
			ctx := context.WithValue(r.Context(), repoKey, rh)
			ctx = context.WithValue(ctx, tenantKey, tenant)
			ctx = context.WithValue(ctx, repoNameKey, repoName)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RepoFrom returns the RepoHandle from request context.
func RepoFrom(ctx context.Context) *repo.Handle {
	if v := ctx.Value(repoKey); v != nil {
		return v.(*repo.Handle)
	}
	return nil
}

// TenantFrom returns the tenant from request context.
func TenantFrom(ctx context.Context) string {
	if v := ctx.Value(tenantKey); v != nil {
		return v.(string)
	}
	return ""
}

// RepoNameFrom returns the repo name from request context.
func RepoNameFrom(ctx context.Context) string {
	if v := ctx.Value(repoNameKey); v != nil {
		return v.(string)
	}
	return ""
}
