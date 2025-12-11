// Package api provides the HTTP API for Kailab.
package api

import (
	"bytes"
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
	"kai-core/cas"
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

	// Diff
	mux.Handle("GET /{tenant}/{repo}/v1/diff/{base}/{head}", withRepo(http.HandlerFunc(h.GetFileDiff)))

	// Reviews
	mux.Handle("GET /{tenant}/{repo}/v1/reviews", withRepo(http.HandlerFunc(h.ListReviews)))
	mux.Handle("POST /{tenant}/{repo}/v1/reviews/{id}/state", withRepo(http.HandlerFunc(h.UpdateReviewState)))

	// CI / Affected Tests
	mux.Handle("GET /{tenant}/{repo}/v1/changesets/{id}/affected-tests", withRepo(http.HandlerFunc(h.GetAffectedTests)))

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

	// Return JSON with kind and payload
	// Content format is: Kind\n{json_payload}
	// Extract just the payload part after the first newline
	var payload json.RawMessage
	parts := bytes.SplitN(content, []byte("\n"), 2)
	if len(parts) == 2 {
		payload = parts[1]
	} else {
		payload = content
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":    kind,
		"digest":  digestHex,
		"payload": payload,
	})
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

	// Determine target - either from ref lookup or raw hex ID
	var target []byte

	// Check if refName looks like a raw hex digest (64 hex chars)
	if len(refName) == 64 && isHexString(refName) {
		// Use raw snapshot ID directly
		var err error
		target, err = hex.DecodeString(refName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid snapshot ID", err)
			return
		}
	} else {
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
		target = ref.Target
	}

	// Fetch snapshot object
	snapshotContent, kind, err := pack.ExtractObjectFromDB(rh.DB, target)
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
						SnapshotDigest: hex.EncodeToString(target),
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
			SnapshotDigest: hex.EncodeToString(target),
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
					SnapshotDigest: hex.EncodeToString(target),
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
		SnapshotDigest: hex.EncodeToString(target),
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

// isHexString checks if a string contains only hex characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ----- Diff -----

func (h *Handler) GetFileDiff(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	baseHex := r.PathValue("base")
	headHex := r.PathValue("head")
	filePath := r.URL.Query().Get("path")

	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path query parameter required", nil)
		return
	}

	// Fetch content from both snapshots
	baseContent, err := h.getFileContentFromSnapshot(rh.DB, baseHex, filePath)
	if err != nil && err != store.ErrObjectNotFound {
		writeError(w, http.StatusInternalServerError, "failed to get base content", err)
		return
	}

	headContent, err := h.getFileContentFromSnapshot(rh.DB, headHex, filePath)
	if err != nil && err != store.ErrObjectNotFound {
		writeError(w, http.StatusInternalServerError, "failed to get head content", err)
		return
	}

	// Compute diff
	hunks := computeUnifiedDiff(baseContent, headContent)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":  filePath,
		"hunks": hunks,
	})
}

func (h *Handler) getFileContentFromSnapshot(db *sql.DB, snapshotHex, filePath string) (string, error) {
	snapshotID, err := hex.DecodeString(snapshotHex)
	if err != nil {
		return "", err
	}

	// Get snapshot object
	content, kind, err := pack.ExtractObjectFromDB(db, snapshotID)
	if err != nil {
		return "", err
	}
	if kind != "Snapshot" {
		return "", fmt.Errorf("not a snapshot")
	}

	// Parse snapshot to get files
	snapshotJSON := content
	if idx := indexOf(content, '\n'); idx >= 0 {
		snapshotJSON = content[idx+1:]
	}

	var snapshot struct {
		Files []struct {
			Path          string `json:"path"`
			Digest        string `json:"digest"`
			ContentDigest string `json:"contentDigest"`
		} `json:"files"`
	}
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return "", err
	}

	// Find the file
	var contentDigestHex string
	for _, f := range snapshot.Files {
		if f.Path == filePath {
			contentDigestHex = f.ContentDigest
			if contentDigestHex == "" {
				contentDigestHex = f.Digest
			}
			break
		}
	}
	if contentDigestHex == "" {
		return "", store.ErrObjectNotFound
	}

	// Fetch content
	contentDigest, err := hex.DecodeString(contentDigestHex)
	if err != nil {
		return "", err
	}

	fileContent, _, err := pack.ExtractObjectFromDB(db, contentDigest)
	if err != nil {
		return "", err
	}

	return string(fileContent), nil
}

// DiffLine represents a single line in the diff
type DiffLine struct {
	Type    string `json:"type"`    // "context", "add", "delete"
	Content string `json:"content"` // line content without newline
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

// DiffHunk represents a section of changes
type DiffHunk struct {
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

func computeUnifiedDiff(oldText, newText string) []DiffHunk {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Use Myers diff algorithm (simplified LCS-based approach)
	lcs := longestCommonSubsequence(oldLines, newLines)

	var hunks []DiffHunk
	var currentHunk *DiffHunk

	oldIdx, newIdx, lcsIdx := 0, 0, 0
	contextLines := 3

	for oldIdx < len(oldLines) || newIdx < len(newLines) {
		// Check if current lines match LCS
		oldMatch := lcsIdx < len(lcs) && oldIdx < len(oldLines) && oldLines[oldIdx] == lcs[lcsIdx]
		newMatch := lcsIdx < len(lcs) && newIdx < len(newLines) && newLines[newIdx] == lcs[lcsIdx]

		if oldMatch && newMatch {
			// Context line
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "context",
					Content: oldLines[oldIdx],
					OldLine: oldIdx + 1,
					NewLine: newIdx + 1,
				})
				currentHunk.OldLines++
				currentHunk.NewLines++
			}
			oldIdx++
			newIdx++
			lcsIdx++
		} else if !oldMatch && oldIdx < len(oldLines) && (lcsIdx >= len(lcs) || oldLines[oldIdx] != lcs[lcsIdx]) {
			// Deletion
			if currentHunk == nil {
				currentHunk = &DiffHunk{
					OldStart: max(1, oldIdx+1-contextLines),
					NewStart: max(1, newIdx+1-contextLines),
				}
				// Add leading context
				for i := max(0, oldIdx-contextLines); i < oldIdx; i++ {
					if i < len(oldLines) {
						currentHunk.Lines = append(currentHunk.Lines, DiffLine{
							Type:    "context",
							Content: oldLines[i],
							OldLine: i + 1,
							NewLine: newIdx - (oldIdx - i) + 1,
						})
						currentHunk.OldLines++
						currentHunk.NewLines++
					}
				}
			}
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    "delete",
				Content: oldLines[oldIdx],
				OldLine: oldIdx + 1,
			})
			currentHunk.OldLines++
			oldIdx++
		} else if !newMatch && newIdx < len(newLines) {
			// Addition
			if currentHunk == nil {
				currentHunk = &DiffHunk{
					OldStart: max(1, oldIdx+1-contextLines),
					NewStart: max(1, newIdx+1-contextLines),
				}
				// Add leading context
				for i := max(0, newIdx-contextLines); i < newIdx; i++ {
					if i < len(newLines) && i-newIdx+oldIdx >= 0 && i-newIdx+oldIdx < len(oldLines) {
						currentHunk.Lines = append(currentHunk.Lines, DiffLine{
							Type:    "context",
							Content: oldLines[i-newIdx+oldIdx],
							OldLine: i - newIdx + oldIdx + 1,
							NewLine: i + 1,
						})
						currentHunk.OldLines++
						currentHunk.NewLines++
					}
				}
			}
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    "add",
				Content: newLines[newIdx],
				NewLine: newIdx + 1,
			})
			currentHunk.NewLines++
			newIdx++
		} else {
			break
		}
	}

	if currentHunk != nil && len(currentHunk.Lines) > 0 {
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

func longestCommonSubsequence(a, b []string) []string {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack to find LCS
	lcs := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return lcs
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

// ----- Reviews -----

func (h *Handler) ListReviews(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	// Get all refs with "review." prefix (excluding helper refs like review.xyz.target)
	refs, err := store.ListRefs(rh.DB, "review.")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list refs", err)
		return
	}

	var reviews []*proto.ReviewEntry

	for _, ref := range refs {
		// Skip helper refs (review.xyz.target, etc.)
		parts := strings.Split(ref.Name, ".")
		if len(parts) != 2 {
			continue
		}

		reviewID := parts[1] // Short hex ID

		// Fetch the review object to get its payload
		content, kind, err := pack.ExtractObjectFromDB(rh.DB, ref.Target)
		if err != nil {
			log.Printf("Failed to get review object %s: %v", hex.EncodeToString(ref.Target), err)
			continue
		}

		if kind != "Review" {
			continue
		}

		// Parse review payload (format: "Review\n{json}")
		reviewJSON := content
		if idx := indexOf(content, '\n'); idx >= 0 {
			reviewJSON = content[idx+1:]
		}

		var payload struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			State       string   `json:"state"`
			Author      string   `json:"author"`
			Reviewers   []string `json:"reviewers"`
			TargetID    string   `json:"targetId"`
			TargetKind  string   `json:"targetKind"`
			CreatedAt   float64  `json:"createdAt"`
			UpdatedAt   float64  `json:"updatedAt"`
		}

		if err := json.Unmarshal(reviewJSON, &payload); err != nil {
			log.Printf("Failed to parse review payload: %v", err)
			continue
		}

		reviews = append(reviews, &proto.ReviewEntry{
			ID:          reviewID,
			RefName:     ref.Name,
			Title:       payload.Title,
			Description: payload.Description,
			State:       payload.State,
			Author:      payload.Author,
			Reviewers:   payload.Reviewers,
			TargetID:    payload.TargetID,
			TargetKind:  payload.TargetKind,
			CreatedAt:   int64(payload.CreatedAt),
			UpdatedAt:   int64(payload.UpdatedAt),
		})
	}

	writeJSON(w, http.StatusOK, proto.ReviewsListResponse{Reviews: reviews})
}

func (h *Handler) UpdateReviewState(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	reviewID := r.PathValue("id")
	if reviewID == "" {
		writeError(w, http.StatusBadRequest, "review id required", nil)
		return
	}

	// Parse request body
	var req struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate state
	validStates := map[string]bool{
		"draft": true, "open": true, "approved": true,
		"changes_requested": true, "merged": true, "abandoned": true,
	}
	if !validStates[req.State] {
		writeError(w, http.StatusBadRequest, "invalid state", nil)
		return
	}

	// Find review ref
	refName := "review." + reviewID
	ref, err := store.GetRef(rh.DB, refName)
	if err != nil {
		writeError(w, http.StatusNotFound, "review not found", err)
		return
	}

	// Get review object
	content, kind, err := pack.ExtractObjectFromDB(rh.DB, ref.Target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get review", err)
		return
	}
	if kind != "Review" {
		writeError(w, http.StatusBadRequest, "not a review object", nil)
		return
	}

	// Parse existing payload
	reviewJSON := content
	if idx := indexOf(content, '\n'); idx >= 0 {
		reviewJSON = content[idx+1:]
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(reviewJSON, &payload); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse review", err)
		return
	}

	// Update state and timestamp
	payload["state"] = req.State
	payload["updatedAt"] = float64(time.Now().UnixMilli())

	// Create new review object content
	newPayloadJSON, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize review", err)
		return
	}
	newContent := append([]byte("Review\n"), newPayloadJSON...)

	// Compute object digest
	newDigest := computeBlake3(newContent)

	// Store raw content as a segment (not a pack - just the raw bytes)
	segmentChecksum := computeBlake3(newContent)

	// Store in transaction
	tx, err := rh.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	// Insert segment with raw content
	segmentID, err := store.InsertSegmentTx(tx, segmentChecksum, newContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store segment", err)
		return
	}

	// Insert object index at offset 0 with full length
	err = store.InsertObjectTx(tx, newDigest, segmentID, 0, int64(len(newContent)), "Review")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to index object", err)
		return
	}

	// Update review ref
	err = store.ForceSetRef(rh.DB, tx, refName, newDigest, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ref", err)
		return
	}

	// If merging, also update snap.main to the changeset's head snapshot
	if req.State == "merged" {
		targetID, ok := payload["targetId"].(string)
		if ok && targetID != "" {
			// Get the changeset object
			csDigest, err := hex.DecodeString(targetID)
			if err == nil {
				csContent, csKind, err := pack.ExtractObjectFromDB(rh.DB, csDigest)
				if err == nil && csKind == "ChangeSet" {
					// Parse changeset to get head snapshot
					csJSON := csContent
					if idx := indexOf(csContent, '\n'); idx >= 0 {
						csJSON = csContent[idx+1:]
					}
					var csPayload struct {
						Head string `json:"head"`
					}
					if err := json.Unmarshal(csJSON, &csPayload); err == nil && csPayload.Head != "" {
						// Update snap.main to point to the head snapshot
						headDigest, err := hex.DecodeString(csPayload.Head)
						if err == nil {
							store.ForceSetRef(rh.DB, tx, "snap.main", headDigest, "", "")
						}
					}
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"state":   req.State,
	})
}

// computeBlake3 computes blake3 hash of data
func computeBlake3(data []byte) []byte {
	h := cas.NewBlake3Hasher()
	h.Write(data)
	return h.Sum(nil)
}

// GetAffectedTests returns tests that might be affected by the changes in a changeset.
// This uses heuristics based on file naming conventions (e.g., foo.js -> foo.test.js).
func (h *Handler) GetAffectedTests(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	changesetID := r.PathValue("id")
	if changesetID == "" {
		writeError(w, http.StatusBadRequest, "missing changeset ID", nil)
		return
	}

	// Resolve changeset ID (could be short ID or full hex)
	var fullDigest []byte
	if len(changesetID) < 64 {
		// Short ID - find matching ref
		rows, err := rh.DB.Query(`SELECT target FROM refs WHERE name LIKE ? LIMIT 1`, "cs.%"+changesetID+"%")
		if err == nil {
			defer rows.Close()
			if rows.Next() {
				rows.Scan(&fullDigest)
			}
		}
		// If not found via ref, try objects table
		if fullDigest == nil {
			rows2, err := rh.DB.Query(`SELECT digest FROM objects WHERE kind = 'ChangeSet' AND hex(digest) LIKE ? LIMIT 1`, changesetID+"%")
			if err == nil {
				defer rows2.Close()
				if rows2.Next() {
					rows2.Scan(&fullDigest)
				}
			}
		}
	} else {
		fullDigest, _ = hex.DecodeString(changesetID)
	}

	if fullDigest == nil {
		writeError(w, http.StatusNotFound, "changeset not found", nil)
		return
	}

	// Get the changeset object
	csData, _, err := pack.ExtractObjectFromDB(rh.DB, fullDigest)
	if err != nil {
		writeError(w, http.StatusNotFound, "changeset not found", err)
		return
	}

	// Parse changeset payload
	var csPayload struct {
		Base string `json:"base"`
		Head string `json:"head"`
	}
	// Skip "ChangeSet\n" prefix
	if idx := bytes.Index(csData, []byte("\n")); idx > 0 {
		if err := json.Unmarshal(csData[idx+1:], &csPayload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to parse changeset", err)
			return
		}
	}

	baseDigest, _ := hex.DecodeString(csPayload.Base)
	headDigest, _ := hex.DecodeString(csPayload.Head)

	if baseDigest == nil || headDigest == nil {
		writeError(w, http.StatusInternalServerError, "invalid changeset base/head", nil)
		return
	}

	// Get files from both snapshots
	baseFiles, err := getSnapshotFilePaths(rh.DB, baseDigest)
	if err != nil {
		baseFiles = make(map[string]bool) // Empty if base doesn't exist
	}
	headFiles, err := getSnapshotFilePaths(rh.DB, headDigest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get head files", err)
		return
	}

	// Find changed files
	var changedFiles []string
	for path := range headFiles {
		if !baseFiles[path] {
			changedFiles = append(changedFiles, path) // Added
		}
	}
	for path := range baseFiles {
		if !headFiles[path] {
			changedFiles = append(changedFiles, path) // Removed
		}
	}
	// For modified, we'd need to compare content digests - skip for now
	// The heuristic will still work for added/removed files

	// Find affected tests using naming heuristics
	affectedTests := findAffectedTestsByHeuristic(changedFiles, headFiles)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changedFiles":  changedFiles,
		"affectedTests": affectedTests,
		"method":        "heuristic", // Indicates this is heuristic-based, not graph-based
	})
}

// getSnapshotFilePaths returns a map of file paths in a snapshot
func getSnapshotFilePaths(db *sql.DB, snapshotDigest []byte) (map[string]bool, error) {
	snapData, _, err := pack.ExtractObjectFromDB(db, snapshotDigest)
	if err != nil {
		return nil, err
	}

	// Parse snapshot payload
	var snapPayload struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if idx := bytes.Index(snapData, []byte("\n")); idx > 0 {
		if err := json.Unmarshal(snapData[idx+1:], &snapPayload); err != nil {
			return nil, err
		}
	}

	paths := make(map[string]bool)
	for _, f := range snapPayload.Files {
		paths[f.Path] = true
	}
	return paths, nil
}

// findAffectedTestsByHeuristic finds test files that might be affected by changed files.
// Uses common naming patterns:
// - foo.js -> foo.test.js, foo.spec.js, test/foo.js, __tests__/foo.js
// - src/foo.js -> test/foo.test.js, tests/foo.test.js
func findAffectedTestsByHeuristic(changedFiles []string, allFiles map[string]bool) []string {
	testPatterns := make(map[string]bool)

	for _, path := range changedFiles {
		// Skip if the changed file is already a test file
		if isTestFile(path) {
			testPatterns[path] = true
			continue
		}

		// Generate possible test file names
		base := strings.TrimSuffix(path, getExtension(path))
		ext := getExtension(path)

		// Common patterns
		candidates := []string{
			base + ".test" + ext,
			base + ".spec" + ext,
			base + "_test" + ext,
			strings.Replace(path, "src/", "test/", 1),
			strings.Replace(path, "src/", "tests/", 1),
			strings.Replace(path, "lib/", "test/", 1),
			"test/" + path,
			"tests/" + path,
			"__tests__/" + strings.TrimPrefix(path, "src/"),
		}

		// Also try test file with same name in test directory
		fileName := getFileName(path)
		baseName := strings.TrimSuffix(fileName, ext)
		candidates = append(candidates,
			"test/"+baseName+".test"+ext,
			"tests/"+baseName+".test"+ext,
			"test/"+baseName+"_test"+ext,
			"__tests__/"+baseName+".test"+ext,
		)

		for _, candidate := range candidates {
			if allFiles[candidate] {
				testPatterns[candidate] = true
			}
		}
	}

	// Convert to slice
	var result []string
	for path := range testPatterns {
		result = append(result, path)
	}
	return result
}

// isTestFile checks if a file path looks like a test file
func isTestFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.") ||
		strings.Contains(lower, "_test.") ||
		strings.HasPrefix(lower, "test/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.Contains(lower, "__tests__/")
}

// getExtension returns the file extension including the dot
func getExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' {
			return ""
		}
	}
	return ""
}

// getFileName returns just the file name from a path
func getFileName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// Helper type for unused import
var _ = sql.ErrNoRows
