// Package api provides the HTTP API for Kailab.
package api

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"kailab/config"
	"kailab/pack"
	"kailab/proto"
	"kailab/repo"
	"kailab/store"
)

// Handler wraps the registry and config for HTTP handlers.
type Handler struct {
	reg *repo.Registry
	cfg *config.Config
}

// NewHandler creates a new API handler.
func NewHandler(reg *repo.Registry, cfg *config.Config) *Handler {
	return &Handler{reg: reg, cfg: cfg}
}

// NewRouter creates the HTTP router with all routes registered.
func NewRouter(reg *repo.Registry, cfg *config.Config) http.Handler {
	h := NewHandler(reg, cfg)
	mux := http.NewServeMux()

	// Middleware for repo routes
	withRepo := WithRepo(reg)

	// Health (no repo needed)
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /healthz", h.Health)
	mux.HandleFunc("GET /readyz", h.Ready)

	// Admin routes (no repo context needed)
	mux.HandleFunc("POST /admin/v1/repos", h.CreateRepo)
	mux.HandleFunc("GET /admin/v1/repos", h.ListRepos)
	mux.HandleFunc("DELETE /admin/v1/repos/{tenant}/{repo}", h.DeleteRepo)

	// Repo-scoped routes: /{tenant}/{repo}/v1/...
	// Push negotiation
	mux.Handle("POST /{tenant}/{repo}/v1/push/negotiate", withRepo(http.HandlerFunc(h.Negotiate)))

	// Objects
	mux.Handle("POST /{tenant}/{repo}/v1/objects/pack", withRepo(http.HandlerFunc(h.IngestPack)))
	mux.Handle("GET /{tenant}/{repo}/v1/objects/{digest}", withRepo(http.HandlerFunc(h.GetObject)))

	// Refs
	mux.Handle("GET /{tenant}/{repo}/v1/refs", withRepo(http.HandlerFunc(h.ListRefs)))
	mux.Handle("PUT /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.UpdateRef)))
	mux.Handle("GET /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.GetRef)))

	// Log
	mux.Handle("GET /{tenant}/{repo}/v1/log/head", withRepo(http.HandlerFunc(h.LogHead)))
	mux.Handle("GET /{tenant}/{repo}/v1/log/entries", withRepo(http.HandlerFunc(h.LogEntries)))

	return mux
}

// ----- Health -----

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, proto.HealthResponse{
		Status:  "ok",
		Version: h.cfg.Version,
	})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Could check if we can open a sample repo
	writeJSON(w, http.StatusOK, proto.HealthResponse{
		Status:  "ready",
		Version: h.cfg.Version,
	})
}

// ----- Admin -----

func (h *Handler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	var req proto.CreateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Tenant == "" || req.Repo == "" {
		writeError(w, http.StatusBadRequest, "tenant and repo required", nil)
		return
	}

	_, err := h.reg.Create(r.Context(), req.Tenant, req.Repo)
	if err != nil {
		if err == repo.ErrRepoExists {
			writeError(w, http.StatusConflict, "repo already exists", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create repo", err)
		return
	}

	writeJSON(w, http.StatusCreated, proto.CreateRepoResponse{
		Tenant: req.Tenant,
		Repo:   req.Repo,
	})
}

func (h *Handler) ListRepos(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")

	var result []proto.RepoInfo

	if tenant != "" {
		// List repos for specific tenant
		repos, err := h.reg.List(r.Context(), tenant)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list repos", err)
			return
		}
		for _, name := range repos {
			result = append(result, proto.RepoInfo{Tenant: tenant, Repo: name})
		}
	} else {
		// List all tenants and repos
		tenants, err := h.reg.ListTenants(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tenants", err)
			return
		}
		for _, t := range tenants {
			repos, err := h.reg.List(r.Context(), t)
			if err != nil {
				continue
			}
			for _, name := range repos {
				result = append(result, proto.RepoInfo{Tenant: t, Repo: name})
			}
		}
	}

	writeJSON(w, http.StatusOK, proto.ListReposResponse{Repos: result})
}

func (h *Handler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	repoName := r.PathValue("repo")

	if tenant == "" || repoName == "" {
		writeError(w, http.StatusBadRequest, "tenant and repo required", nil)
		return
	}

	if err := h.reg.Delete(r.Context(), tenant, repoName); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete repo", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----- Negotiate -----

func (h *Handler) Negotiate(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	var req proto.NegotiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Check which digests exist
	existing, err := store.HasObjects(rh.DB, req.Digests)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check objects", err)
		return
	}

	// Return missing digests
	var missing [][]byte
	for _, d := range req.Digests {
		hexDigest := hex.EncodeToString(d)
		if !existing[hexDigest] {
			missing = append(missing, d)
		}
	}

	writeJSON(w, http.StatusOK, proto.NegotiateResponse{Missing: missing})
}

// ----- Objects -----

func (h *Handler) IngestPack(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	// Check content length
	if r.ContentLength > h.cfg.MaxPackSize {
		writeError(w, http.StatusRequestEntityTooLarge, "pack too large", nil)
		return
	}

	// Limit reader as extra protection
	limitReader := io.LimitReader(r.Body, h.cfg.MaxPackSize)

	// Get actor from header or default
	actor := r.Header.Get("X-Kailab-Actor")
	if actor == "" {
		actor = "anonymous"
	}

	segmentID, indexed, err := pack.IngestSegmentToDB(rh.DB, limitReader, actor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to ingest pack", err)
		return
	}

	writeJSON(w, http.StatusOK, proto.PackIngestResponse{
		SegmentID: segmentID,
		Indexed:   indexed,
	})
}

func (h *Handler) GetObject(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	digestHex := r.PathValue("digest")
	digest, err := hex.DecodeString(digestHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid digest", err)
		return
	}

	content, kind, err := pack.ExtractObjectFromDB(rh.DB, digest)
	if err != nil {
		if err == store.ErrObjectNotFound {
			writeError(w, http.StatusNotFound, "object not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get object", err)
		return
	}

	// Return raw bytes with metadata headers
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Kailab-Kind", kind)
	w.Header().Set("X-Kailab-Digest", digestHex)
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

// ----- Refs -----

func (h *Handler) ListRefs(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	prefix := r.URL.Query().Get("prefix")

	refs, err := store.ListRefs(rh.DB, prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list refs", err)
		return
	}

	var entries []*proto.RefEntry
	for _, ref := range refs {
		entries = append(entries, &proto.RefEntry{
			Name:      ref.Name,
			Target:    ref.Target,
			UpdatedAt: ref.UpdatedAt,
			Actor:     ref.Actor,
		})
	}

	writeJSON(w, http.StatusOK, proto.RefsListResponse{Refs: entries})
}

func (h *Handler) GetRef(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	name := r.PathValue("name")

	ref, err := store.GetRef(rh.DB, name)
	if err != nil {
		if err == store.ErrRefNotFound {
			writeError(w, http.StatusNotFound, "ref not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get ref", err)
		return
	}

	writeJSON(w, http.StatusOK, proto.RefEntry{
		Name:      ref.Name,
		Target:    ref.Target,
		UpdatedAt: ref.UpdatedAt,
		Actor:     ref.Actor,
	})
}

func (h *Handler) UpdateRef(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	name := r.PathValue("name")

	var req proto.RefUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if len(req.New) == 0 {
		writeError(w, http.StatusBadRequest, "new target required", nil)
		return
	}

	actor := r.Header.Get("X-Kailab-Actor")
	if actor == "" {
		actor = "anonymous"
	}
	pushID := uuid.New().String()

	tx, err := store.BeginTx(rh.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	if req.Force {
		err = store.ForceSetRef(rh.DB, tx, name, req.New, actor, pushID)
	} else {
		err = store.SetRefFF(rh.DB, tx, name, req.Old, req.New, actor, pushID)
	}

	if err != nil {
		if err == store.ErrRefMismatch {
			writeJSON(w, http.StatusConflict, proto.RefUpdateResponse{
				OK:    false,
				Error: "ref mismatch (not fast-forward)",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update ref", err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	ref, _ := store.GetRef(rh.DB, name)

	writeJSON(w, http.StatusOK, proto.RefUpdateResponse{
		OK:        true,
		UpdatedAt: ref.UpdatedAt,
		PushID:    pushID,
	})
}

// ----- Log -----

func (h *Handler) LogHead(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	head, err := store.GetLogHead(rh.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get log head", err)
		return
	}

	writeJSON(w, http.StatusOK, proto.LogHeadResponse{Head: head})
}

func (h *Handler) LogEntries(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	refFilter := r.URL.Query().Get("ref")
	afterSeq := int64(0)
	if after := r.URL.Query().Get("after"); after != "" {
		fmt.Sscanf(after, "%d", &afterSeq)
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	entries, err := store.GetRefHistory(rh.DB, refFilter, afterSeq, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get log entries", err)
		return
	}

	var logEntries []*proto.LogEntry
	for _, e := range entries {
		logEntries = append(logEntries, &proto.LogEntry{
			Kind:   "REF_UPDATE",
			ID:     e.ID,
			Parent: e.Parent,
			Time:   e.Time,
			Actor:  e.Actor,
			Ref:    e.Ref,
			Old:    e.Old,
			New:    e.New,
		})
	}

	writeJSON(w, http.StatusOK, proto.LogEntriesResponse{Entries: logEntries})
}

// ----- Helpers -----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string, err error) {
	resp := proto.ErrorResponse{Error: msg}
	if err != nil {
		resp.Details = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// extractRefName extracts ref name from path, handling slashes.
func extractRefName(path, prefix string) string {
	name := strings.TrimPrefix(path, prefix)
	return strings.TrimPrefix(name, "/")
}

// Retry helpers for SQLite busy handling
const maxRetries = 5
const baseDelay = 50 * time.Millisecond

func withRetry(fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) {
			return err
		}
		// Exponential backoff with jitter
		delay := baseDelay * time.Duration(1<<i)
		time.Sleep(delay)
	}
	return err
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLITE_BUSY") ||
		strings.Contains(err.Error(), "database is locked")
}

// Helper type for unused import
var _ = sql.ErrNoRows
