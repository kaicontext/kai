// Package api provides the HTTP API for Kailab.
package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	mux.Handle("POST /{tenant}/{repo}/v1/refs/batch", withRepo(http.HandlerFunc(h.BatchUpdateRefs)))
	mux.Handle("PUT /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.UpdateRef)))
	mux.Handle("GET /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.GetRef)))

	// Log
	mux.Handle("GET /{tenant}/{repo}/v1/log/head", withRepo(http.HandlerFunc(h.LogHead)))
	mux.Handle("GET /{tenant}/{repo}/v1/log/entries", withRepo(http.HandlerFunc(h.LogEntries)))

	// Files - use {ref...} pattern since ref names contain dots (e.g., snap.latest)
	mux.Handle("GET /{tenant}/{repo}/v1/files/{ref...}", withRepo(http.HandlerFunc(h.ListSnapshotFiles)))
	mux.Handle("GET /{tenant}/{repo}/v1/content/{digest}", withRepo(http.HandlerFunc(h.GetFileContent)))

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

	ref, err := store.GetRef(rh.DB, name)
	resp := proto.RefUpdateResponse{
		OK:     true,
		PushID: pushID,
	}
	if err == nil && ref != nil {
		resp.UpdatedAt = ref.UpdatedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) BatchUpdateRefs(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	var req proto.BatchRefUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if len(req.Updates) == 0 {
		writeError(w, http.StatusBadRequest, "no updates provided", nil)
		return
	}

	actor := r.Header.Get("X-Kailab-Actor")
	if actor == "" {
		actor = "anonymous"
	}
	pushID := uuid.New().String()

	// Single transaction for all ref updates
	tx, err := store.BeginTx(rh.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	results := make([]proto.BatchRefResult, len(req.Updates))

	for i, upd := range req.Updates {
		if len(upd.New) == 0 {
			results[i] = proto.BatchRefResult{
				Name:  upd.Name,
				OK:    false,
				Error: "new target required",
			}
			continue
		}

		var err error
		if upd.Force {
			err = store.ForceSetRef(rh.DB, tx, upd.Name, upd.New, actor, pushID)
		} else {
			err = store.SetRefFF(rh.DB, tx, upd.Name, upd.Old, upd.New, actor, pushID)
		}

		if err != nil {
			errMsg := "failed to update ref"
			if err == store.ErrRefMismatch {
				errMsg = "ref mismatch (not fast-forward)"
			}
			results[i] = proto.BatchRefResult{
				Name:  upd.Name,
				OK:    false,
				Error: errMsg,
			}
			continue
		}

		ref, err := store.GetRef(rh.DB, upd.Name)
		result := proto.BatchRefResult{
			Name: upd.Name,
			OK:   true,
		}
		if err == nil && ref != nil {
			result.UpdatedAt = ref.UpdatedAt
		}
		results[i] = result
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	writeJSON(w, http.StatusOK, proto.BatchRefUpdateResponse{
		PushID:  pushID,
		Results: results,
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

// ----- Files -----

func (h *Handler) ListSnapshotFiles(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	refName := r.PathValue("ref")
	if refName == "" {
		writeError(w, http.StatusBadRequest, "ref name required", nil)
		return
	}

	// Optional: filter by path to get single file
	pathFilter := r.URL.Query().Get("path")

	// Get ref to find snapshot digest
	ref, err := store.GetRef(rh.DB, refName)
	if err != nil {
		if err == store.ErrRefNotFound {
			writeError(w, http.StatusNotFound, "ref not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get ref", err)
		return
	}

	// Fetch snapshot object
	snapshotContent, kind, err := pack.ExtractObjectFromDB(rh.DB, ref.Target)
	if err != nil {
		if err == store.ErrObjectNotFound {
			writeError(w, http.StatusNotFound, "snapshot object not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get snapshot", err)
		return
	}

	if kind != "Snapshot" {
		writeError(w, http.StatusBadRequest, "ref does not point to a snapshot", nil)
		return
	}

	// Parse snapshot payload (format: "Snapshot\n{json}")
	snapshotJSON := snapshotContent
	if idx := indexOf(snapshotContent, '\n'); idx >= 0 {
		snapshotJSON = snapshotContent[idx+1:]
	}

	var snapshotPayload struct {
		FileDigests []string `json:"fileDigests"`
		// New: inline file metadata for fast listing
		Files []struct {
			Path          string `json:"path"`
			Lang          string `json:"lang"`
			Digest        string `json:"digest"`
			ContentDigest string `json:"contentDigest"`
		} `json:"files"`
	}
	if err := json.Unmarshal(snapshotJSON, &snapshotPayload); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse snapshot", err)
		return
	}

	var files []*proto.FileEntry

	// Debug: log which path we're taking
	log.Printf("Snapshot has %d inline files, %d file digests", len(snapshotPayload.Files), len(snapshotPayload.FileDigests))

	// Fast path: use inline files metadata if available (new snapshots)
	if len(snapshotPayload.Files) > 0 {
		for _, f := range snapshotPayload.Files {
			// If filtering by path, check if this matches
			if pathFilter != "" {
				if f.Path == pathFilter {
					writeJSON(w, http.StatusOK, proto.FilesListResponse{
						SnapshotDigest: hex.EncodeToString(ref.Target),
						Files: []*proto.FileEntry{{
							Path:          f.Path,
							Digest:        f.Digest,
							ContentDigest: f.ContentDigest,
							Lang:          f.Lang,
						}},
					})
					return
				}
				continue
			}

			files = append(files, &proto.FileEntry{
				Path:          f.Path,
				Digest:        f.Digest,
				ContentDigest: f.ContentDigest,
				Lang:          f.Lang,
			})
		}

		if pathFilter != "" {
			writeError(w, http.StatusNotFound, "file not found in snapshot", nil)
			return
		}

		writeJSON(w, http.StatusOK, proto.FilesListResponse{
			SnapshotDigest: hex.EncodeToString(ref.Target),
			Files:          files,
		})
		return
	}

	// Slow path: fetch each file object (old snapshots without inline metadata)
	for _, fileDigestHex := range snapshotPayload.FileDigests {
		fileDigest, err := hex.DecodeString(fileDigestHex)
		if err != nil {
			continue
		}

		fileContent, fileKind, err := pack.ExtractObjectFromDB(rh.DB, fileDigest)
		if err != nil {
			continue
		}
		if fileKind != "File" {
			continue
		}

		// Parse file payload
		fileJSON := fileContent
		if idx := indexOf(fileContent, '\n'); idx >= 0 {
			fileJSON = fileContent[idx+1:]
		}

		var filePayload struct {
			Path   string `json:"path"`
			Digest string `json:"digest"`
			Lang   string `json:"lang"`
			Size   int64  `json:"size"`
		}
		if err := json.Unmarshal(fileJSON, &filePayload); err != nil {
			continue
		}

		// If filtering by path, check if this matches
		if pathFilter != "" {
			if filePayload.Path == pathFilter {
				// Return just this file
				writeJSON(w, http.StatusOK, proto.FilesListResponse{
					SnapshotDigest: hex.EncodeToString(ref.Target),
					Files: []*proto.FileEntry{{
						Path:          filePayload.Path,
						Digest:        fileDigestHex,
						ContentDigest: filePayload.Digest,
						Lang:          filePayload.Lang,
						Size:          filePayload.Size,
					}},
				})
				return
			}
			continue // Skip non-matching files when filtering
		}

		files = append(files, &proto.FileEntry{
			Path:          filePayload.Path,
			Digest:        fileDigestHex,
			ContentDigest: filePayload.Digest,
			Lang:          filePayload.Lang,
			Size:          filePayload.Size,
		})
	}

	// If filtering by path and we got here, file wasn't found
	if pathFilter != "" {
		writeError(w, http.StatusNotFound, "file not found in snapshot", nil)
		return
	}

	writeJSON(w, http.StatusOK, proto.FilesListResponse{
		SnapshotDigest: hex.EncodeToString(ref.Target),
		Files:          files,
	})
}

func (h *Handler) GetFileContent(w http.ResponseWriter, r *http.Request) {
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

	// Fetch the file node first to get the content digest
	fileContent, kind, err := pack.ExtractObjectFromDB(rh.DB, digest)
	if err != nil {
		if err == store.ErrObjectNotFound {
			writeError(w, http.StatusNotFound, "file not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get file", err)
		return
	}

	if kind != "File" {
		writeError(w, http.StatusBadRequest, "digest does not point to a file", nil)
		return
	}

	// Parse file payload
	fileJSON := fileContent
	if idx := indexOf(fileContent, '\n'); idx >= 0 {
		fileJSON = fileContent[idx+1:]
	}

	var filePayload struct {
		Path   string `json:"path"`
		Digest string `json:"digest"` // content digest
		Lang   string `json:"lang"`
	}
	if err := json.Unmarshal(fileJSON, &filePayload); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse file", err)
		return
	}

	// Fetch actual file content using the content digest
	contentDigest, err := hex.DecodeString(filePayload.Digest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid content digest", err)
		return
	}

	content, _, err := pack.ExtractObjectFromDB(rh.DB, contentDigest)
	if err != nil {
		if err == store.ErrObjectNotFound {
			writeError(w, http.StatusNotFound, "file content not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get content", err)
		return
	}

	// Return as base64 for binary safety
	writeJSON(w, http.StatusOK, proto.FileContentResponse{
		Path:    filePayload.Path,
		Digest:  filePayload.Digest,
		Content: base64.StdEncoding.EncodeToString(content),
		Lang:    filePayload.Lang,
	})
}

// indexOf returns the index of the first occurrence of b in data, or -1 if not found.
func indexOf(data []byte, b byte) int {
	for i, v := range data {
		if v == b {
			return i
		}
	}
	return -1
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
