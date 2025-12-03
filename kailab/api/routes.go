// Package api provides the HTTP API for Kailab.
package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"kailab/config"
	"kailab/pack"
	"kailab/proto"
	"kailab/store"
)

// Handler wraps the database and config for HTTP handlers.
type Handler struct {
	db  *store.DB
	cfg *config.Config
}

// NewHandler creates a new API handler.
func NewHandler(db *store.DB, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// NewRouter creates the HTTP router with all routes registered.
func NewRouter(db *store.DB, cfg *config.Config) http.Handler {
	h := NewHandler(db, cfg)
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", h.Health)

	// Push negotiation
	mux.HandleFunc("POST /v1/push/negotiate", h.Negotiate)

	// Objects
	mux.HandleFunc("POST /v1/objects/pack", h.IngestPack)
	mux.HandleFunc("GET /v1/objects/{digest}", h.GetObject)

	// Refs
	mux.HandleFunc("GET /v1/refs", h.ListRefs)
	mux.HandleFunc("PUT /v1/refs/{name...}", h.UpdateRef)
	mux.HandleFunc("GET /v1/refs/{name...}", h.GetRef)

	// Log
	mux.HandleFunc("GET /v1/log/head", h.LogHead)
	mux.HandleFunc("GET /v1/log/entries", h.LogEntries)

	return mux
}

// ----- Health -----

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, proto.HealthResponse{
		Status:  "ok",
		Version: h.cfg.Version,
	})
}

// ----- Negotiate -----

func (h *Handler) Negotiate(w http.ResponseWriter, r *http.Request) {
	var req proto.NegotiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Check which digests exist
	existing, err := h.db.HasObjects(req.Digests)
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

	segmentID, indexed, err := pack.IngestSegment(h.db, limitReader, actor)
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
	digestHex := r.PathValue("digest")
	digest, err := hex.DecodeString(digestHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid digest", err)
		return
	}

	content, kind, err := pack.ExtractObject(h.db, digest)
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
	prefix := r.URL.Query().Get("prefix")

	refs, err := h.db.ListRefs(prefix)
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
	name := r.PathValue("name")

	ref, err := h.db.GetRef(name)
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

	tx, err := h.db.BeginTx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	if req.Force {
		err = h.db.ForceSetRef(tx, name, req.New, actor, pushID)
	} else {
		err = h.db.SetRefFF(tx, name, req.Old, req.New, actor, pushID)
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

	ref, _ := h.db.GetRef(name)

	writeJSON(w, http.StatusOK, proto.RefUpdateResponse{
		OK:        true,
		UpdatedAt: ref.UpdatedAt,
		PushID:    pushID,
	})
}

// ----- Log -----

func (h *Handler) LogHead(w http.ResponseWriter, r *http.Request) {
	head, err := h.db.GetLogHead()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get log head", err)
		return
	}

	writeJSON(w, http.StatusOK, proto.LogHeadResponse{Head: head})
}

func (h *Handler) LogEntries(w http.ResponseWriter, r *http.Request) {
	refFilter := r.URL.Query().Get("ref")
	afterSeq := int64(0)
	if after := r.URL.Query().Get("after"); after != "" {
		fmt.Sscanf(after, "%d", &afterSeq)
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	entries, err := h.db.GetRefHistory(refFilter, afterSeq, limit)
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
