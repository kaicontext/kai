// Package api provides the HTTP API for Kailab.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"
	"kai-core/cas"
	"kailab/background"
	"kailab/config"
	"kailab/metrics"
	"kailab/pack"
	"kailab/proto"
	"kailab/repo"
	"kailab/sshserver"
	"kailab/store"
)

// cachedSnapshot holds parsed snapshot data for fast lookups.
type cachedSnapshot struct {
	filesByPath map[string]string // path -> contentDigest
	parsedAt    time.Time
}

// snapshotCache is a simple in-memory cache for parsed snapshots.
// Key: "tenant/repo:snapshotHex", Value: *cachedSnapshot
var snapshotCache sync.Map

const snapshotCacheMaxAge = 5 * time.Minute

// Diff display limits - files exceeding these are not rendered
const (
	maxDiffFileSize  = 1 * 1024 * 1024 // 1 MB
	maxDiffLineCount = 10000           // 10k lines
)

// isBinaryContent checks if content appears to be binary (contains null bytes or high ratio of non-printable chars).
func isBinaryContent(content string) bool {
	if len(content) == 0 {
		return false
	}
	// Check first 8KB for binary indicators
	sample := content
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	// Null byte is a strong indicator of binary
	if strings.ContainsRune(sample, 0) {
		return true
	}
	// High ratio of non-printable characters suggests binary
	nonPrintable := 0
	for _, r := range sample {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(len(sample)) > 0.1
}

// isImageFile checks if a file path looks like an image.
func isImageFile(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".bmp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// Handler wraps the registry and config for HTTP handlers.
type Handler struct {
	reg             *repo.Registry
	cfg             *config.Config
	mirror          *sshserver.GitMirror
	webhookNotifier *sshserver.WebhookNotifier
}

func (h *Handler) mirrorRefs(ctx context.Context, rh *repo.Handle, refs []string) {
	if h.mirror == nil || rh == nil || len(refs) == 0 {
		return
	}
	if err := h.mirror.SyncRefs(ctx, rh, refs); err != nil {
		log.Printf("git mirror sync failed for %s/%s: %v", rh.Tenant, rh.Name, err)
	}
}

// notifyReviewCreated checks for new review refs and sends email notifications.
func (h *Handler) notifyReviewCreated(rh *repo.Handle, refs []string) {
	if h.webhookNotifier == nil || rh == nil || len(refs) == 0 {
		return
	}

	for _, refName := range refs {
		// Only process review refs (review.xyz format, not review.xyz.target etc.)
		if !strings.HasPrefix(refName, "review.") {
			continue
		}
		parts := strings.Split(refName, ".")
		if len(parts) != 2 {
			continue
		}
		reviewID := parts[1]

		// Fetch the review object to get metadata
		ref, err := store.GetRef(rh.DB, refName)
		if err != nil {
			continue
		}

		content, kind, err := pack.ExtractObjectFromDB(rh.DB, ref.Target)
		if err != nil || kind != "Review" {
			continue
		}

		// Parse review payload
		reviewJSON := content
		if idx := indexOf(content, '\n'); idx >= 0 {
			reviewJSON = content[idx+1:]
		}

		var payload struct {
			Title     string   `json:"title"`
			State     string   `json:"state"`
			Author    string   `json:"author"`
			Reviewers []string `json:"reviewers"`
		}
		if err := json.Unmarshal(reviewJSON, &payload); err != nil {
			continue
		}

		// Only notify for newly created reviews (state should be "draft" or "open")
		// Skip if already approved/merged/etc
		if payload.State != "draft" && payload.State != "open" {
			continue
		}

		// Send notification asynchronously
		go func(repo, reviewID, title, author string, reviewers []string, refDigest string) {
			if err := h.webhookNotifier.NotifyReviewCreated(repo, reviewID, title, author, reviewers); err != nil {
				log.Printf("notify: review created notification failed: %v", err)
			}
			// Trigger CI for review
			if err := h.webhookNotifier.NotifyCI(repo, "review_created", refName, refDigest, map[string]interface{}{
				"review_id": reviewID,
				"title":     title,
				"author":    author,
				"state":     payload.State,
			}); err != nil {
				log.Printf("notify: CI trigger for review failed: %v", err)
			}
		}(rh.Tenant+"/"+rh.Name, reviewID, payload.Title, payload.Author, payload.Reviewers, fmt.Sprintf("%x", ref.Target))
	}
}

// notifyPushCI triggers CI workflows for push events on branch-like refs.
func (h *Handler) notifyPushCI(rh *repo.Handle, refs []string) {
	if h.webhookNotifier == nil || rh == nil || len(refs) == 0 {
		return
	}

	repo := rh.Tenant + "/" + rh.Name

	for _, refName := range refs {
		// Trigger CI for snap.main, snap.master, or snap.<branch> refs
		// These represent branch pushes in kai's model
		if !strings.HasPrefix(refName, "snap.") {
			continue
		}

		// Get the SHA for this ref
		ref, err := store.GetRef(rh.DB, refName)
		if err != nil {
			continue
		}
		sha := fmt.Sprintf("%x", ref.Target)

		// Convert snap.main to refs/heads/main format for workflow matching
		branchName := strings.TrimPrefix(refName, "snap.")
		gitRef := "refs/heads/" + branchName

		go func(repo, gitRef, sha string, payload map[string]interface{}) {
			if err := h.webhookNotifier.NotifyCI(repo, "push", gitRef, sha, payload); err != nil {
				log.Printf("notify: CI trigger for push failed: %v", err)
			}
		}(repo, gitRef, sha, map[string]interface{}{
			"ref": refName,
		})
	}
}

func (h *Handler) ensureSignedChangeSet(db *sql.DB, target []byte) error {
	if h.cfg == nil || !h.cfg.RequireSignedChangeSets || len(target) == 0 {
		return nil
	}

	content, kind, err := pack.ExtractObjectFromDB(db, target)
	if err != nil {
		return fmt.Errorf("load target: %w", err)
	}
	if kind != "ChangeSet" {
		return nil
	}

	payload, err := background.ParseObjectPayload(content)
	if err != nil {
		return fmt.Errorf("parse changeset: %w", err)
	}
	if payload["signature"] == nil {
		return errors.New("changeset signature required")
	}
	ok, err := background.VerifyChangeSetSignature(payload)
	if err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	if !ok {
		return errors.New("changeset signature invalid")
	}
	return nil
}

// NewHandler creates a new API handler.
func NewHandler(reg *repo.Registry, cfg *config.Config) *Handler {
	mirror := sshserver.NewGitMirror(sshserver.MirrorConfig{
		Enabled:    cfg.GitMirrorEnabled,
		BaseDir:    cfg.GitMirrorDir,
		AllowRepos: cfg.GitMirrorAllowRepos,
		Rollback:   cfg.GitMirrorRollback,
		Logger:     log.Default(),
	})
	var notifier *sshserver.WebhookNotifier
	if cfg.ControlPlaneURL != "" {
		notifier = sshserver.NewWebhookNotifier(cfg.ControlPlaneURL)
	}
	return &Handler{reg: reg, cfg: cfg, mirror: mirror, webhookNotifier: notifier}
}

// NewRouter creates the HTTP router with all routes registered.
func NewRouter(reg *repo.Registry, cfg *config.Config) http.Handler {
	h := NewHandler(reg, cfg)
	mux := http.NewServeMux()
	withMetrics := withRouteMetrics()

	// Middleware for repo routes
	withRepo := WithRepo(reg)

	// Health (no repo needed)
	mux.Handle("GET /health", withMetrics("GET /health", http.HandlerFunc(h.Health)))
	mux.Handle("GET /healthz", withMetrics("GET /healthz", http.HandlerFunc(h.Health)))
	mux.Handle("GET /readyz", withMetrics("GET /readyz", http.HandlerFunc(h.Ready)))
	mux.Handle("GET /metrics", withMetrics("GET /metrics", expvar.Handler()))

	// Admin routes (no repo context needed)
	mux.Handle("POST /admin/v1/repos", withMetrics("POST /admin/v1/repos", http.HandlerFunc(h.CreateRepo)))
	mux.Handle("GET /admin/v1/repos", withMetrics("GET /admin/v1/repos", http.HandlerFunc(h.ListRepos)))
	mux.Handle("DELETE /admin/v1/repos/{tenant}/{repo}", withMetrics("DELETE /admin/v1/repos/{tenant}/{repo}", http.HandlerFunc(h.DeleteRepo)))

	// Repo-scoped routes: /{tenant}/{repo}/v1/...
	// Push negotiation
	mux.Handle("POST /{tenant}/{repo}/v1/push/negotiate", withMetrics("POST /{tenant}/{repo}/v1/push/negotiate", withRepo(http.HandlerFunc(h.Negotiate))))

	// Objects
	mux.Handle("POST /{tenant}/{repo}/v1/objects/pack", withMetrics("POST /{tenant}/{repo}/v1/objects/pack", withRepo(http.HandlerFunc(h.IngestPack))))
	mux.Handle("GET /{tenant}/{repo}/v1/objects/{digest}", withMetrics("GET /{tenant}/{repo}/v1/objects/{digest}", withRepo(http.HandlerFunc(h.GetObject))))

	// Refs
	mux.Handle("GET /{tenant}/{repo}/v1/refs", withMetrics("GET /{tenant}/{repo}/v1/refs", withRepo(http.HandlerFunc(h.ListRefs))))
	mux.Handle("POST /{tenant}/{repo}/v1/refs/batch", withMetrics("POST /{tenant}/{repo}/v1/refs/batch", withRepo(http.HandlerFunc(h.BatchUpdateRefs))))
	mux.Handle("PUT /{tenant}/{repo}/v1/refs/{name...}", withMetrics("PUT /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.UpdateRef))))
	mux.Handle("GET /{tenant}/{repo}/v1/refs/{name...}", withMetrics("GET /{tenant}/{repo}/v1/refs/{name...}", withRepo(http.HandlerFunc(h.GetRef))))

	// Log
	mux.Handle("GET /{tenant}/{repo}/v1/log/head", withMetrics("GET /{tenant}/{repo}/v1/log/head", withRepo(http.HandlerFunc(h.LogHead))))
	mux.Handle("GET /{tenant}/{repo}/v1/log/entries", withMetrics("GET /{tenant}/{repo}/v1/log/entries", withRepo(http.HandlerFunc(h.LogEntries))))

	// Files - use {ref...} pattern since ref names contain dots (e.g., snap.latest)
	mux.Handle("GET /{tenant}/{repo}/v1/files/{ref...}", withMetrics("GET /{tenant}/{repo}/v1/files/{ref...}", withRepo(http.HandlerFunc(h.ListSnapshotFiles))))
	mux.Handle("GET /{tenant}/{repo}/v1/content/{digest}", withMetrics("GET /{tenant}/{repo}/v1/content/{digest}", withRepo(http.HandlerFunc(h.GetFileContent))))

	// Diff
	mux.Handle("GET /{tenant}/{repo}/v1/diff/{base}/{head}", withMetrics("GET /{tenant}/{repo}/v1/diff/{base}/{head}", withRepo(http.HandlerFunc(h.GetFileDiff))))
	mux.Handle("GET /{tenant}/{repo}/v1/semantic-diff/{csId}", withMetrics("GET /{tenant}/{repo}/v1/semantic-diff/{csId}", withRepo(http.HandlerFunc(h.GetSemanticDiff))))

	// Reviews
	mux.Handle("GET /{tenant}/{repo}/v1/reviews", withMetrics("GET /{tenant}/{repo}/v1/reviews", withRepo(http.HandlerFunc(h.ListReviews))))
	mux.Handle("PATCH /{tenant}/{repo}/v1/reviews/{id}", withMetrics("PATCH /{tenant}/{repo}/v1/reviews/{id}", withRepo(http.HandlerFunc(h.UpdateReview))))
	mux.Handle("POST /{tenant}/{repo}/v1/reviews/{id}/state", withMetrics("POST /{tenant}/{repo}/v1/reviews/{id}/state", withRepo(http.HandlerFunc(h.UpdateReviewState))))
	mux.Handle("GET /{tenant}/{repo}/v1/reviews/{id}/comments", withMetrics("GET /{tenant}/{repo}/v1/reviews/{id}/comments", withRepo(http.HandlerFunc(h.ListReviewComments))))
	mux.Handle("POST /{tenant}/{repo}/v1/reviews/{id}/comments", withMetrics("POST /{tenant}/{repo}/v1/reviews/{id}/comments", withRepo(http.HandlerFunc(h.CreateReviewComment))))

	// Changesets
	mux.Handle("GET /{tenant}/{repo}/v1/changesets/{id}", withMetrics("GET /{tenant}/{repo}/v1/changesets/{id}", withRepo(http.HandlerFunc(h.GetChangeset))))
	mux.Handle("PATCH /{tenant}/{repo}/v1/changesets/{id}", withMetrics("PATCH /{tenant}/{repo}/v1/changesets/{id}", withRepo(http.HandlerFunc(h.UpdateChangeset))))
	mux.Handle("GET /{tenant}/{repo}/v1/changesets/{id}/affected-tests", withMetrics("GET /{tenant}/{repo}/v1/changesets/{id}/affected-tests", withRepo(http.HandlerFunc(h.GetAffectedTests))))

	// Edges
	mux.Handle("POST /{tenant}/{repo}/v1/edges", withMetrics("POST /{tenant}/{repo}/v1/edges", withRepo(http.HandlerFunc(h.IngestEdges))))

	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func withRouteMetrics() func(route string, handler http.Handler) http.Handler {
	return func(route string, handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			handler.ServeHTTP(rec, r)
			metrics.IncHTTP(r.Method, route, rec.status)
		})
	}
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

	if err := h.ensureSignedChangeSet(rh.DB, req.New); err != nil {
		writeError(w, http.StatusForbidden, err.Error(), nil)
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

	h.mirrorRefs(r.Context(), rh, []string{name})

	// Notify on new review refs (fire-and-forget)
	h.notifyReviewCreated(rh, []string{name})

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
		if err := h.ensureSignedChangeSet(rh.DB, upd.New); err != nil {
			results[i] = proto.BatchRefResult{
				Name:  upd.Name,
				OK:    false,
				Error: err.Error(),
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

	var syncedRefs []string
	for _, result := range results {
		if result.OK {
			syncedRefs = append(syncedRefs, result.Name)
		}
	}
	h.mirrorRefs(r.Context(), rh, syncedRefs)

	// Notify on new review refs (fire-and-forget)
	h.notifyReviewCreated(rh, syncedRefs)

	// Trigger CI for push events (fire-and-forget)
	h.notifyPushCI(rh, syncedRefs)

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

	// Track if we can cache (raw hex IDs are immutable, ref names are not)
	canCache := false

	// Check if refName looks like a raw hex digest (64 hex chars)
	if len(refName) == 64 && isHexString(refName) {
		canCache = true // Content-addressed = immutable
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

	// Set caching headers for immutable content-addressed snapshots
	if canCache {
		etag := `"` + hex.EncodeToString(target)[:16] + `"`
		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=3600") // 1 hour
	}

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

	// Generate ETag from base+head+path (content-addressable = immutable)
	etag := `"` + baseHex[:8] + headHex[:8] + `"`
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Fetch content from both snapshots (with caching)
	baseContent, err := h.getFileContentFromSnapshotWithCache(rh.DB, rh.Tenant, rh.Name, baseHex, filePath)
	if err != nil && err != store.ErrObjectNotFound {
		writeError(w, http.StatusInternalServerError, "failed to get base content", err)
		return
	}

	headContent, err := h.getFileContentFromSnapshotWithCache(rh.DB, rh.Tenant, rh.Name, headHex, filePath)
	if err != nil && err != store.ErrObjectNotFound {
		writeError(w, http.StatusInternalServerError, "failed to get head content", err)
		return
	}

	// Set caching headers - diffs are immutable (content-addressed)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=3600") // 1 hour

	// Check for binary content
	isImage := isImageFile(filePath)
	if isBinaryContent(baseContent) || isBinaryContent(headContent) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"path":    filePath,
			"binary":  true,
			"isImage": isImage,
			"hunks":   []DiffHunk{},
		})
		return
	}

	// Check for files that are too large
	maxSize := len(baseContent)
	if len(headContent) > maxSize {
		maxSize = len(headContent)
	}
	baseLines := strings.Count(baseContent, "\n")
	headLines := strings.Count(headContent, "\n")
	maxLines := baseLines
	if headLines > maxLines {
		maxLines = headLines
	}

	if maxSize > maxDiffFileSize || maxLines > maxDiffLineCount {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"path":     filePath,
			"tooLarge": true,
			"size":     maxSize,
			"lines":    maxLines,
			"hunks":    []DiffHunk{},
		})
		return
	}

	// Compute diff
	hunks := computeUnifiedDiff(baseContent, headContent)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":  filePath,
		"hunks": hunks,
	})
}

// GetSemanticDiff returns semantic diff (functions, classes changed) for a changeset.
func (h *Handler) GetSemanticDiff(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	csIdHex := r.PathValue("csId")
	filePath := r.URL.Query().Get("path") // Optional: filter to specific file

	csID, err := hex.DecodeString(csIdHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid changeset ID", err)
		return
	}

	// Get changeset data including all nodes and edges
	csData, err := h.getChangeSetDataWithNodes(rh.DB, csID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get changeset", err)
		return
	}

	// Extract semantic units from the changeset nodes
	units := h.extractSemanticUnits(csData, filePath)

	// If no units found from stored data, compute from file content
	if len(units) == 0 && filePath != "" {
		baseHex, _ := csData["base"].(string)
		headHex, _ := csData["head"].(string)
		if baseHex != "" && headHex != "" {
			units = h.computeSemanticDiffFromContent(rh.DB, baseHex, headHex, filePath)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changesetId": csIdHex,
		"path":        filePath,
		"units":       units,
	})
}

// SemanticUnit represents a semantic code unit (function, class, etc)
type SemanticUnit struct {
	Kind       string `json:"kind"`       // function, class, method, struct, etc
	Name       string `json:"name"`       // Symbol name
	FQName     string `json:"fqName"`     // Fully-qualified name
	Action     string `json:"action"`     // added, modified, removed
	File       string `json:"file"`       // File path
	BeforeSig  string `json:"beforeSig,omitempty"`
	AfterSig   string `json:"afterSig,omitempty"`
	ChangeType string `json:"changeType,omitempty"` // API_SURFACE_CHANGED, IMPLEMENTATION_CHANGED
}

func (h *Handler) getChangeSetDataWithNodes(db *sql.DB, csID []byte) (map[string]interface{}, error) {
	// Get changeset object
	csData, kind, err := pack.ExtractObjectFromDB(db, csID)
	if err != nil {
		return nil, err
	}
	if kind != "ChangeSet" {
		return nil, fmt.Errorf("not a changeset")
	}

	// Parse changeset payload
	csJSON := csData
	if idx := indexOf(csData, '\n'); idx >= 0 {
		csJSON = csData[idx+1:]
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(csJSON, &payload); err != nil {
		return nil, err
	}

	// Query for related nodes (symbols, files) via edges
	nodes, err := h.getChangeSetNodes(db, csID)
	if err == nil {
		payload["nodes"] = nodes
	}

	return payload, nil
}

func (h *Handler) getChangeSetNodes(db *sql.DB, csID []byte) ([]map[string]interface{}, error) {
	// Query edges from this changeset
	rows, err := db.Query(`
		SELECT o.data, o.kind FROM objects o
		JOIN edges e ON o.digest = e.dst
		WHERE e.src = ? AND e.type IN ('MODIFIES', 'CONTAINS')
	`, csID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []map[string]interface{}
	for rows.Next() {
		var data []byte
		var kind string
		if err := rows.Scan(&data, &kind); err != nil {
			continue
		}

		// Parse node payload
		nodeJSON := data
		if idx := indexOf(data, '\n'); idx >= 0 {
			nodeJSON = data[idx+1:]
		}

		var nodePayload map[string]interface{}
		if err := json.Unmarshal(nodeJSON, &nodePayload); err != nil {
			continue
		}
		nodePayload["kind"] = kind
		nodes = append(nodes, nodePayload)
	}

	return nodes, nil
}

func (h *Handler) extractSemanticUnits(csData map[string]interface{}, filterPath string) []SemanticUnit {
	var units []SemanticUnit

	nodes, _ := csData["nodes"].([]map[string]interface{})
	for _, node := range nodes {
		kind, _ := node["kind"].(string)
		if kind != "Symbol" {
			continue
		}

		filePath, _ := node["file"].(string)
		if filterPath != "" && filePath != filterPath {
			continue
		}

		fqName, _ := node["fqName"].(string)
		symKind, _ := node["symKind"].(string)
		if symKind == "" {
			symKind, _ = node["kind"].(string)
		}
		sig, _ := node["signature"].(string)
		beforeSig, _ := node["beforeSignature"].(string)
		changeType, _ := node["changeType"].(string)

		// Determine action
		action := "modified"
		if changeType == "ADDED" || (beforeSig == "" && sig != "") {
			action = "added"
		} else if changeType == "REMOVED" || (beforeSig != "" && sig == "") {
			action = "removed"
		}

		units = append(units, SemanticUnit{
			Kind:       symKind,
			Name:       extractSymbolName(fqName),
			FQName:     fqName,
			Action:     action,
			File:       filePath,
			BeforeSig:  beforeSig,
			AfterSig:   sig,
			ChangeType: changeType,
		})
	}

	return units
}

func extractSymbolName(fqName string) string {
	if fqName == "" {
		return ""
	}
	for i := len(fqName) - 1; i >= 0; i-- {
		if fqName[i] == '.' {
			return fqName[i+1:]
		}
	}
	return fqName
}

// computeSemanticDiffFromContent extracts symbols from file content and compares them
func (h *Handler) computeSemanticDiffFromContent(db *sql.DB, baseHex, headHex, filePath string) []SemanticUnit {
	baseContent, _ := h.getFileContentFromSnapshot(db, baseHex, filePath)
	headContent, _ := h.getFileContentFromSnapshot(db, headHex, filePath)

	baseSymbols := extractSymbolsFromContent(baseContent, filePath)
	headSymbols := extractSymbolsFromContent(headContent, filePath)

	var units []SemanticUnit

	// Build map of base symbols by name
	baseMap := make(map[string]extractedSymbol)
	for _, sym := range baseSymbols {
		baseMap[sym.Name] = sym
	}

	// Find added and modified symbols
	for _, headSym := range headSymbols {
		if baseSym, exists := baseMap[headSym.Name]; exists {
			// Symbol exists in both - check if modified
			if headSym.Signature != baseSym.Signature || headSym.Body != baseSym.Body {
				changeType := "IMPLEMENTATION_CHANGED"
				if headSym.Signature != baseSym.Signature {
					changeType = "API_SURFACE_CHANGED"
				}
				units = append(units, SemanticUnit{
					Kind:       headSym.Kind,
					Name:       headSym.Name,
					FQName:     headSym.Name,
					Action:     "modified",
					File:       filePath,
					BeforeSig:  baseSym.Signature,
					AfterSig:   headSym.Signature,
					ChangeType: changeType,
				})
			}
			delete(baseMap, headSym.Name)
		} else {
			// Symbol only in head - added
			units = append(units, SemanticUnit{
				Kind:     headSym.Kind,
				Name:     headSym.Name,
				FQName:   headSym.Name,
				Action:   "added",
				File:     filePath,
				AfterSig: headSym.Signature,
			})
		}
	}

	// Remaining symbols in baseMap are removed
	for _, baseSym := range baseMap {
		units = append(units, SemanticUnit{
			Kind:      baseSym.Kind,
			Name:      baseSym.Name,
			FQName:    baseSym.Name,
			Action:    "removed",
			File:      filePath,
			BeforeSig: baseSym.Signature,
		})
	}

	return units
}

type extractedSymbol struct {
	Kind      string
	Name      string
	Signature string
	Body      string
}

// extractSymbolsFromContent extracts functions/classes from file content using regex
func extractSymbolsFromContent(content, filePath string) []extractedSymbol {
	var symbols []extractedSymbol
	if content == "" {
		return symbols
	}

	ext := getFileExtension(filePath)

	switch ext {
	case ".js", ".ts", ".jsx", ".tsx", ".mjs":
		symbols = extractJSSymbols(content)
	case ".go":
		symbols = extractGoSymbols(content)
	case ".py":
		symbols = extractPythonSymbols(content)
	}

	return symbols
}

func getFileExtension(path string) string {
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

// extractJSSymbols extracts functions and classes from JavaScript/TypeScript
func extractJSSymbols(content string) []extractedSymbol {
	var symbols []extractedSymbol

	// Match function declarations: function name(...) or async function name(...)
	funcRegex := regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(([^)]*)\)`)
	for _, match := range funcRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      "function",
			Name:      match[1],
			Signature: "function " + match[1] + "(" + match[2] + ")",
		})
	}

	// Match arrow functions: const name = (...) => or const name = async (...) =>
	arrowRegex := regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>`)
	for _, match := range arrowRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      "function",
			Name:      match[1],
			Signature: "const " + match[1] + " = () =>",
		})
	}

	// Match class declarations
	classRegex := regexp.MustCompile(`(?m)^[\t ]*(?:export\s+)?class\s+(\w+)(?:\s+extends\s+(\w+))?`)
	for _, match := range classRegex.FindAllStringSubmatch(content, -1) {
		sig := "class " + match[1]
		if match[2] != "" {
			sig += " extends " + match[2]
		}
		symbols = append(symbols, extractedSymbol{
			Kind:      "class",
			Name:      match[1],
			Signature: sig,
		})
	}

	// Match method definitions inside classes: name(...) { or async name(...) {
	methodRegex := regexp.MustCompile(`(?m)^[\t ]+(?:async\s+)?(\w+)\s*\(([^)]*)\)\s*\{`)
	for _, match := range methodRegex.FindAllStringSubmatch(content, -1) {
		if match[1] != "if" && match[1] != "for" && match[1] != "while" && match[1] != "switch" && match[1] != "function" {
			symbols = append(symbols, extractedSymbol{
				Kind:      "method",
				Name:      match[1],
				Signature: match[1] + "(" + match[2] + ")",
			})
		}
	}

	return symbols
}

// extractGoSymbols extracts functions and types from Go code
func extractGoSymbols(content string) []extractedSymbol {
	var symbols []extractedSymbol

	// Match function declarations: func name(...) or func (r Recv) name(...)
	funcRegex := regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?(\w+)\s*\(([^)]*)\)`)
	for _, match := range funcRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      "function",
			Name:      match[1],
			Signature: "func " + match[1] + "(" + match[2] + ")",
		})
	}

	// Match type declarations: type Name struct/interface
	typeRegex := regexp.MustCompile(`(?m)^type\s+(\w+)\s+(struct|interface)`)
	for _, match := range typeRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      match[2],
			Name:      match[1],
			Signature: "type " + match[1] + " " + match[2],
		})
	}

	return symbols
}

// extractPythonSymbols extracts functions and classes from Python code
func extractPythonSymbols(content string) []extractedSymbol {
	var symbols []extractedSymbol

	// Match function definitions: def name(...):
	funcRegex := regexp.MustCompile(`(?m)^[\t ]*def\s+(\w+)\s*\(([^)]*)\)`)
	for _, match := range funcRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      "function",
			Name:      match[1],
			Signature: "def " + match[1] + "(" + match[2] + ")",
		})
	}

	// Match class definitions: class Name:
	classRegex := regexp.MustCompile(`(?m)^class\s+(\w+)(?:\([^)]*\))?:`)
	for _, match := range classRegex.FindAllStringSubmatch(content, -1) {
		symbols = append(symbols, extractedSymbol{
			Kind:      "class",
			Name:      match[1],
			Signature: "class " + match[1],
		})
	}

	return symbols
}

// getCachedSnapshot returns a cached snapshot or parses and caches it.
func (h *Handler) getCachedSnapshot(db *sql.DB, tenant, repoName, snapshotHex string) (*cachedSnapshot, error) {
	cacheKey := tenant + "/" + repoName + ":" + snapshotHex

	// Check cache
	if cached, ok := snapshotCache.Load(cacheKey); ok {
		cs := cached.(*cachedSnapshot)
		if time.Since(cs.parsedAt) < snapshotCacheMaxAge {
			return cs, nil
		}
		// Expired, remove it
		snapshotCache.Delete(cacheKey)
	}

	// Parse snapshot
	snapshotID, err := hex.DecodeString(snapshotHex)
	if err != nil {
		return nil, err
	}

	content, kind, err := pack.ExtractObjectFromDB(db, snapshotID)
	if err != nil {
		return nil, err
	}
	if kind != "Snapshot" {
		return nil, fmt.Errorf("not a snapshot")
	}

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
		return nil, err
	}

	// Build path index
	cs := &cachedSnapshot{
		filesByPath: make(map[string]string, len(snapshot.Files)),
		parsedAt:    time.Now(),
	}
	for _, f := range snapshot.Files {
		digest := f.ContentDigest
		if digest == "" {
			digest = f.Digest
		}
		cs.filesByPath[f.Path] = digest
	}

	// Cache it
	snapshotCache.Store(cacheKey, cs)

	return cs, nil
}

func (h *Handler) getFileContentFromSnapshot(db *sql.DB, snapshotHex, filePath string) (string, error) {
	return h.getFileContentFromSnapshotWithCache(db, "", "", snapshotHex, filePath)
}

func (h *Handler) getFileContentFromSnapshotWithCache(db *sql.DB, tenant, repoName, snapshotHex, filePath string) (string, error) {
	// Use cache if tenant/repo provided
	if tenant != "" && repoName != "" {
		cs, err := h.getCachedSnapshot(db, tenant, repoName, snapshotHex)
		if err != nil {
			return "", err
		}

		contentDigestHex, ok := cs.filesByPath[filePath]
		if !ok {
			return "", store.ErrObjectNotFound
		}

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

	// Fallback: no caching (backward compat)
	snapshotID, err := hex.DecodeString(snapshotHex)
	if err != nil {
		return "", err
	}

	content, kind, err := pack.ExtractObjectFromDB(db, snapshotID)
	if err != nil {
		return "", err
	}
	if kind != "Snapshot" {
		return "", fmt.Errorf("not a snapshot")
	}

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
	contextLines := 3
	matcher := difflib.NewMatcher(oldLines, newLines)
	groups := matcher.GetGroupedOpCodes(contextLines)
	hunks := make([]DiffHunk, 0, len(groups))

	for _, group := range groups {
		if len(group) == 0 {
			continue
		}

		first := group[0]
		hunk := DiffHunk{
			OldStart: max(1, first.I1+1),
			NewStart: max(1, first.J1+1),
		}

		for _, op := range group {
			switch op.Tag {
			case 'e': // "equal"
				for i := 0; i < op.I2-op.I1; i++ {
					oldIdx := op.I1 + i
					newIdx := op.J1 + i
					hunk.Lines = append(hunk.Lines, DiffLine{
						Type:    "context",
						Content: oldLines[oldIdx],
						OldLine: oldIdx + 1,
						NewLine: newIdx + 1,
					})
					hunk.OldLines++
					hunk.NewLines++
				}
			case 'd': // "delete"
				for i := op.I1; i < op.I2; i++ {
					hunk.Lines = append(hunk.Lines, DiffLine{
						Type:    "delete",
						Content: oldLines[i],
						OldLine: i + 1,
					})
					hunk.OldLines++
				}
			case 'i': // "insert"
				for j := op.J1; j < op.J2; j++ {
					hunk.Lines = append(hunk.Lines, DiffLine{
						Type:    "add",
						Content: newLines[j],
						NewLine: j + 1,
					})
					hunk.NewLines++
				}
			case 'r': // "replace"
				for i := op.I1; i < op.I2; i++ {
					hunk.Lines = append(hunk.Lines, DiffLine{
						Type:    "delete",
						Content: oldLines[i],
						OldLine: i + 1,
					})
					hunk.OldLines++
				}
				for j := op.J1; j < op.J2; j++ {
					hunk.Lines = append(hunk.Lines, DiffLine{
						Type:    "add",
						Content: newLines[j],
						NewLine: j + 1,
					})
					hunk.NewLines++
				}
			}
		}

		if len(hunk.Lines) > 0 {
			hunks = append(hunks, hunk)
		}
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
		State   string `json:"state"`
		Summary string `json:"summary,omitempty"` // Optional summary for changes_requested
		Actor   string `json:"actor,omitempty"`   // Who made this state change
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

	// Store summary and actor for changes_requested
	if req.State == "changes_requested" {
		if req.Summary != "" {
			payload["changesRequestedSummary"] = req.Summary
		}
		if req.Actor != "" {
			payload["changesRequestedBy"] = req.Actor
		}
	} else {
		// Clear changes requested fields when moving to other states
		delete(payload, "changesRequestedSummary")
		delete(payload, "changesRequestedBy")
	}

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

// UpdateReview updates review metadata (assignees, title, description).
func (h *Handler) UpdateReview(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		Assignees   []string `json:"assignees,omitempty"`
		Title       string   `json:"title,omitempty"`
		Description string   `json:"description,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
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

	// Update fields if provided
	if req.Assignees != nil {
		payload["assignees"] = req.Assignees
	}
	if req.Title != "" {
		payload["title"] = req.Title
	}
	if req.Description != "" {
		payload["description"] = req.Description
	}
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
	segmentChecksum := computeBlake3(newContent)

	// Store in transaction
	tx, err := rh.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	segmentID, err := store.InsertSegmentTx(tx, segmentChecksum, newContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store segment", err)
		return
	}

	err = store.InsertObjectTx(tx, newDigest, segmentID, 0, int64(len(newContent)), "Review")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to index object", err)
		return
	}

	err = store.ForceSetRef(rh.DB, tx, refName, newDigest, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ref", err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"assignees": req.Assignees,
	})
}

// ListReviewComments returns all comments for a review
func (h *Handler) ListReviewComments(w http.ResponseWriter, r *http.Request) {
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

	// Find review ref to get the review object
	refName := "review." + reviewID
	ref, err := store.GetRef(rh.DB, refName)
	if err != nil {
		writeError(w, http.StatusNotFound, "review not found", err)
		return
	}

	// Get comment digests via HAS_COMMENT edges from this review
	rows, err := rh.DB.Query(`
		SELECT e.dst FROM edges e
		WHERE e.src = ? AND e.type = 'HAS_COMMENT'
		ORDER BY e.created_at
	`, ref.Target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query comments", err)
		return
	}
	defer rows.Close()

	var commentDigests [][]byte
	for rows.Next() {
		var digest []byte
		if err := rows.Scan(&digest); err != nil {
			continue
		}
		commentDigests = append(commentDigests, digest)
	}

	var comments []map[string]interface{}
	for _, digest := range commentDigests {
		data, kind, err := pack.ExtractObjectFromDB(rh.DB, digest)
		if err != nil || kind != "ReviewComment" {
			continue
		}

		// Parse comment payload
		commentJSON := data
		if idx := indexOf(data, '\n'); idx >= 0 {
			commentJSON = data[idx+1:]
		}

		var comment map[string]interface{}
		if err := json.Unmarshal(commentJSON, &comment); err != nil {
			continue
		}
		comments = append(comments, comment)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"comments": comments,
	})
}

// CreateReviewComment creates a new comment on a review
func (h *Handler) CreateReviewComment(w http.ResponseWriter, r *http.Request) {
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

	// Parse request
	var req struct {
		Body     string `json:"body"`
		Author   string `json:"author"`
		FilePath string `json:"filePath,omitempty"` // Optional: for inline comments
		Line     int    `json:"line,omitempty"`     // Optional: line number
		ParentID string `json:"parentId,omitempty"` // Optional: for replies
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "comment body required", nil)
		return
	}

	// Find review ref
	refName := "review." + reviewID
	ref, err := store.GetRef(rh.DB, refName)
	if err != nil {
		writeError(w, http.StatusNotFound, "review not found", err)
		return
	}

	// Get review details for notification
	var reviewTitle, reviewAuthor string
	reviewContent, reviewKind, err := pack.ExtractObjectFromDB(rh.DB, ref.Target)
	if err == nil && reviewKind == "Review" {
		reviewJSON := reviewContent
		if idx := indexOf(reviewContent, '\n'); idx >= 0 {
			reviewJSON = reviewContent[idx+1:]
		}
		var reviewPayload struct {
			Title  string `json:"title"`
			Author string `json:"author"`
		}
		if json.Unmarshal(reviewJSON, &reviewPayload) == nil {
			reviewTitle = reviewPayload.Title
			reviewAuthor = reviewPayload.Author
		}
	}

	// Create comment payload
	commentID := uuid.New().String()[:8]
	now := time.Now().UnixMilli()

	comment := map[string]interface{}{
		"id":        commentID,
		"body":      req.Body,
		"author":    req.Author,
		"createdAt": float64(now),
	}
	if req.FilePath != "" {
		comment["filePath"] = req.FilePath
	}
	if req.Line > 0 {
		comment["line"] = req.Line
	}
	if req.ParentID != "" {
		comment["parentId"] = req.ParentID
	}

	// Create ReviewComment object
	commentJSON, err := json.Marshal(comment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize comment", err)
		return
	}
	commentContent := append([]byte("ReviewComment\n"), commentJSON...)
	commentDigest := computeBlake3(commentContent)

	// Store in transaction
	tx, err := rh.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	// Store comment object
	segmentChecksum := computeBlake3(commentContent)
	segmentID, err := store.InsertSegmentTx(tx, segmentChecksum, commentContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store segment", err)
		return
	}

	err = store.InsertObjectTx(tx, commentDigest, segmentID, 0, int64(len(commentContent)), "ReviewComment")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to index object", err)
		return
	}

	// Create HAS_COMMENT edge from review to comment
	ts := time.Now().UnixMilli()
	_, err = tx.Exec(`INSERT OR IGNORE INTO edges (src, type, dst, at, created_at) VALUES (?, 'HAS_COMMENT', ?, NULL, ?)`,
		ref.Target, commentDigest, ts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create edge", err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	// Send notification to control plane (async, don't block response)
	if h.cfg.ControlPlaneURL != "" {
		go func() {
			// If this is a reply, find parent comment author
			var parentAuthor string
			if req.ParentID != "" {
				parentAuthor = h.findCommentAuthor(rh.DB, ref.Target, req.ParentID)
			}

			h.notifyComment(rh.Tenant, rh.Name, reviewID, reviewTitle, reviewAuthor, req.Author, req.Body, parentAuthor)
		}()
	}

	comment["id"] = commentID
	writeJSON(w, http.StatusCreated, comment)
}

// findCommentAuthor looks up a comment by ID and returns its author
func (h *Handler) findCommentAuthor(db *sql.DB, reviewTarget []byte, commentID string) string {
	// Query all comments on this review
	rows, err := db.Query(`SELECT dst FROM edges WHERE src = ? AND type = 'HAS_COMMENT'`, reviewTarget)
	if err != nil {
		return ""
	}
	defer rows.Close()

	for rows.Next() {
		var commentDigest []byte
		if err := rows.Scan(&commentDigest); err != nil {
			continue
		}

		// Load comment
		content, kind, err := pack.ExtractObjectFromDB(db, commentDigest)
		if err != nil || kind != "ReviewComment" {
			continue
		}

		// Parse comment
		commentJSON := content
		if idx := indexOf(content, '\n'); idx >= 0 {
			commentJSON = content[idx+1:]
		}

		var comment struct {
			ID     string `json:"id"`
			Author string `json:"author"`
		}
		if json.Unmarshal(commentJSON, &comment) == nil && comment.ID == commentID {
			return comment.Author
		}
	}
	return ""
}

// extractMentions finds @username patterns in text
func extractMentions(text string) []string {
	mentionRegex := regexp.MustCompile(`@([a-zA-Z0-9_-]+)`)
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var mentions []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			mentions = append(mentions, m[1])
		}
	}
	return mentions
}

// notifyComment sends a comment notification to the control plane
func (h *Handler) notifyComment(org, repo, reviewID, reviewTitle, reviewAuthor, commentAuthor, commentBody, parentAuthor string) {
	if h.cfg.ControlPlaneURL == "" {
		return
	}

	// Extract @mentions from comment body
	mentions := extractMentions(commentBody)

	payload := map[string]interface{}{
		"org":           org,
		"repo":          repo,
		"reviewId":      reviewID,
		"reviewTitle":   reviewTitle,
		"reviewAuthor":  reviewAuthor,
		"commentAuthor": commentAuthor,
		"commentBody":   commentBody,
	}
	if parentAuthor != "" {
		payload["parentCommentAuthor"] = parentAuthor
	}
	if len(mentions) > 0 {
		payload["mentions"] = mentions
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		log.Printf("notify: failed to marshal payload: %v", err)
		return
	}

	url := h.cfg.ControlPlaneURL + "/-/notify/comment"
	resp, err := http.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		log.Printf("notify: failed to call control plane: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("notify: control plane returned %d", resp.StatusCode)
	}
}

// computeBlake3 computes blake3 hash of data
func computeBlake3(data []byte) []byte {
	h := cas.NewBlake3Hasher()
	h.Write(data)
	return h.Sum(nil)
}

// GetChangeset returns details about a changeset including files and symbols changed.
func (h *Handler) GetChangeset(w http.ResponseWriter, r *http.Request) {
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

	// Parse changeset payload (format: "ChangeSet\n{json}")
	csJSON := csData
	if idx := indexOf(csData, '\n'); idx >= 0 {
		csJSON = csData[idx+1:]
	}

	var csPayload struct {
		Base   string `json:"base"`
		Head   string `json:"head"`
		Intent string `json:"intent"`
	}
	json.Unmarshal(csJSON, &csPayload)

	// Build response with file and symbol info
	response := map[string]interface{}{
		"id":     hex.EncodeToString(fullDigest),
		"base":   csPayload.Base,
		"head":   csPayload.Head,
		"intent": csPayload.Intent,
		"files":  []interface{}{},
	}

	// Get files from the changeset by looking at MODIFIES edges or scanning nodes
	var files []map[string]interface{}
	rows, err := rh.DB.Query(`
		SELECT n.payload FROM nodes n
		JOIN edges e ON n.id = e.dst
		WHERE e.src = ? AND e.kind = 'MODIFIES' AND n.kind = 'File'
	`, fullDigest)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var payloadJSON []byte
			if err := rows.Scan(&payloadJSON); err == nil {
				var payload map[string]interface{}
				if json.Unmarshal(payloadJSON, &payload) == nil {
					files = append(files, map[string]interface{}{
						"path":   payload["path"],
						"action": "modified",
					})
				}
			}
		}
	}

	// If no files from edges, compute diff between base and head snapshots
	if len(files) == 0 && csPayload.Base != "" && csPayload.Head != "" {
		files = h.computeSnapshotDiff(rh.DB, csPayload.Base, csPayload.Head)
	}

	response["files"] = files
	writeJSON(w, http.StatusOK, response)
}

// UpdateChangeset updates changeset metadata (like intent).
func (h *Handler) UpdateChangeset(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		Intent string `json:"intent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Resolve changeset ID
	fullDigest, _ := hex.DecodeString(changesetID)
	if len(fullDigest) != 32 {
		// Try to find by short ID
		rows, err := rh.DB.Query(`SELECT digest FROM objects WHERE kind = 'ChangeSet' AND hex(digest) LIKE ? LIMIT 1`, changesetID+"%")
		if err == nil {
			defer rows.Close()
			if rows.Next() {
				rows.Scan(&fullDigest)
			}
		}
	}

	if fullDigest == nil {
		writeError(w, http.StatusNotFound, "changeset not found", nil)
		return
	}

	// Get existing changeset
	csData, _, err := pack.ExtractObjectFromDB(rh.DB, fullDigest)
	if err != nil {
		writeError(w, http.StatusNotFound, "changeset not found", err)
		return
	}

	// Parse existing payload
	csJSON := csData
	if idx := indexOf(csData, '\n'); idx >= 0 {
		csJSON = csData[idx+1:]
	}

	var csPayload map[string]interface{}
	json.Unmarshal(csJSON, &csPayload)

	// Update intent
	csPayload["intent"] = req.Intent

	// Re-encode and update the object
	newJSON, _ := json.Marshal(csPayload)
	newData := append([]byte("ChangeSet\n"), newJSON...)

	// Update the object data in the database
	_, err = rh.DB.Exec(`UPDATE objects SET data = ? WHERE digest = ?`, newData, fullDigest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update changeset", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     hex.EncodeToString(fullDigest),
		"intent": req.Intent,
	})
}

// computeSnapshotDiff compares two snapshots and returns file changes
func (h *Handler) computeSnapshotDiff(db *sql.DB, baseHex, headHex string) []map[string]interface{} {
	baseDigest, err := hex.DecodeString(baseHex)
	if err != nil {
		return nil
	}
	headDigest, err := hex.DecodeString(headHex)
	if err != nil {
		return nil
	}

	// Get files from both snapshots
	baseFiles := h.getSnapshotFiles(db, baseDigest)
	headFiles := h.getSnapshotFiles(db, headDigest)

	var files []map[string]interface{}

	// Find added and modified files
	for path, headFile := range headFiles {
		if baseFile, exists := baseFiles[path]; exists {
			// File exists in both - check if modified
			if headFile.contentDigest != baseFile.contentDigest {
				files = append(files, map[string]interface{}{
					"path":   path,
					"action": "modified",
				})
			}
		} else {
			// File only in head - added
			files = append(files, map[string]interface{}{
				"path":   path,
				"action": "added",
			})
		}
	}

	// Find removed files
	for path := range baseFiles {
		if _, exists := headFiles[path]; !exists {
			files = append(files, map[string]interface{}{
				"path":   path,
				"action": "removed",
			})
		}
	}

	return files
}

type snapshotFile struct {
	path          string
	contentDigest string
}

// getSnapshotFiles returns a map of path -> file info for a snapshot
func (h *Handler) getSnapshotFiles(db *sql.DB, snapshotDigest []byte) map[string]snapshotFile {
	result := make(map[string]snapshotFile)

	snapData, kind, err := pack.ExtractObjectFromDB(db, snapshotDigest)
	if err != nil || kind != "Snapshot" {
		return result
	}

	// Parse snapshot payload
	snapJSON := snapData
	if idx := indexOf(snapData, '\n'); idx >= 0 {
		snapJSON = snapData[idx+1:]
	}

	var snapPayload struct {
		Files []struct {
			Path          string `json:"path"`
			Digest        string `json:"digest"`
			ContentDigest string `json:"contentDigest"`
		} `json:"files"`
	}
	if err := json.Unmarshal(snapJSON, &snapPayload); err != nil {
		return result
	}

	for _, f := range snapPayload.Files {
		contentDigest := f.ContentDigest
		if contentDigest == "" {
			contentDigest = f.Digest
		}
		result[f.Path] = snapshotFile{
			path:          f.Path,
			contentDigest: contentDigest,
		}
	}

	return result
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

	// Try to find affected tests using real edges first
	affectedTests, method := findAffectedTestsFromEdges(rh.DB, headDigest, changedFiles)

	// Fall back to heuristics if no edges found
	if len(affectedTests) == 0 {
		affectedTests = findAffectedTestsByHeuristic(changedFiles, headFiles)
		method = "heuristic"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changedFiles":  changedFiles,
		"affectedTests": affectedTests,
		"method":        method,
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

// findAffectedTestsFromEdges finds affected tests using real edges from the database.
// Returns affected test paths and the method used ("edges" or empty if none found).
func findAffectedTestsFromEdges(db *sql.DB, snapshotDigest []byte, changedFiles []string) ([]string, string) {
	// Get all TESTS edges scoped to this snapshot
	edges, err := store.GetEdgesBySnapshotDB(db, snapshotDigest, "TESTS")
	if err != nil || len(edges) == 0 {
		return nil, ""
	}

	// Build map of file digests to their test edges
	// Edge format: src=test_file_digest, type=TESTS, dst=source_file_digest
	// We need to find tests where dst matches any changed file

	// First get the snapshot to map paths to digests
	snapData, _, err := pack.ExtractObjectFromDB(db, snapshotDigest)
	if err != nil {
		return nil, ""
	}

	// Parse snapshot to get file paths and their digests
	var snapPayload struct {
		Files []struct {
			Path   string `json:"path"`
			Digest string `json:"digest"` // content digest
		} `json:"files"`
	}
	if idx := bytes.Index(snapData, []byte("\n")); idx > 0 {
		if err := json.Unmarshal(snapData[idx+1:], &snapPayload); err != nil {
			return nil, ""
		}
	}

	// Map paths to content digests, and vice versa
	pathToDigest := make(map[string][]byte)
	digestToPath := make(map[string]string)
	for _, f := range snapPayload.Files {
		if d, err := hex.DecodeString(f.Digest); err == nil {
			pathToDigest[f.Path] = d
			digestToPath[f.Digest] = f.Path
		}
	}

	// Find digests of changed files
	changedDigests := make(map[string]bool)
	for _, path := range changedFiles {
		if d, ok := pathToDigest[path]; ok {
			changedDigests[hex.EncodeToString(d)] = true
		}
	}

	// Find tests that test any changed file
	affectedTests := make(map[string]bool)
	for _, edge := range edges {
		dstHex := hex.EncodeToString(edge.Dst)
		if changedDigests[dstHex] {
			// This test targets a changed file
			srcHex := hex.EncodeToString(edge.Src)
			if testPath, ok := digestToPath[srcHex]; ok {
				affectedTests[testPath] = true
			}
		}
	}

	if len(affectedTests) == 0 {
		return nil, ""
	}

	// Convert to slice
	var result []string
	for path := range affectedTests {
		result = append(result, path)
	}
	return result, "edges"
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

// ----- Edges -----

// IngestEdges receives and stores edges from CLI.
func (h *Handler) IngestEdges(w http.ResponseWriter, r *http.Request) {
	rh := RepoFrom(r.Context())
	if rh == nil {
		writeError(w, http.StatusInternalServerError, "repo not in context", nil)
		return
	}

	// Parse request body
	var req struct {
		Edges []struct {
			Src  string `json:"src"`  // hex digest
			Type string `json:"type"` // IMPORTS, TESTS, etc.
			Dst  string `json:"dst"`  // hex digest
			At   string `json:"at"`   // hex digest (optional)
		} `json:"edges"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if len(req.Edges) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"inserted": 0,
		})
		return
	}

	// Convert to store.Edge format
	edges := make([]store.Edge, 0, len(req.Edges))
	for _, e := range req.Edges {
		src, err := hex.DecodeString(e.Src)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid src digest", err)
			return
		}
		dst, err := hex.DecodeString(e.Dst)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid dst digest", err)
			return
		}
		var at []byte
		if e.At != "" {
			at, err = hex.DecodeString(e.At)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid at digest", err)
				return
			}
		}
		edges = append(edges, store.Edge{
			Src:  src,
			Type: e.Type,
			Dst:  dst,
			At:   at,
		})
	}

	// Insert in a transaction
	tx, err := store.BeginTx(rh.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	if err := store.InsertEdgesTx(tx, edges); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to insert edges", err)
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"inserted": len(edges),
	})
}

// Helper type for unused import
var _ = sql.ErrNoRows
