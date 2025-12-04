package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// ----- Repos -----

type CreateRepoRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

type RepoResponse struct {
	ID         string `json:"id"`
	OrgID      string `json:"org_id"`
	OrgSlug    string `json:"org_slug"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	ShardHint  string `json:"shard_hint"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	CloneURL   string `json:"clone_url"`
}

func (h *Handler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	org := OrgFromContext(r.Context())

	if user == nil || org == nil {
		writeError(w, http.StatusInternalServerError, "missing context", nil)
		return
	}

	var req CreateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate name
	req.Name = NormalizeSlug(req.Name)
	if !ValidateSlug(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid repo name: must be 1-63 lowercase letters, numbers, hyphens, underscores, dots", nil)
		return
	}

	// Default visibility
	if req.Visibility == "" {
		req.Visibility = "private"
	}
	if req.Visibility != "private" && req.Visibility != "public" && req.Visibility != "internal" {
		writeError(w, http.StatusBadRequest, "invalid visibility: must be private, public, or internal", nil)
		return
	}

	// Pick shard
	shardHint := h.shards.PickShardByHash(org.ID)

	// Provision on shard (kailabd)
	shardURL := h.shards.GetShardURL(shardHint)
	if shardURL == "" {
		writeError(w, http.StatusInternalServerError, "no shard available", nil)
		return
	}

	// Call kailabd admin API to create repo
	provisionReq := map[string]string{
		"tenant": org.Slug,
		"repo":   req.Name,
	}
	body, _ := json.Marshal(provisionReq)

	resp, err := http.Post(shardURL+"/admin/v1/repos", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("Failed to provision repo on shard %s: %v", shardHint, err)
		writeError(w, http.StatusInternalServerError, "failed to provision repo on data plane", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("Shard returned %d: %s", resp.StatusCode, string(respBody))
		writeError(w, http.StatusInternalServerError, "failed to provision repo on data plane", nil)
		return
	}

	// Create in control plane DB
	repo, err := h.db.CreateRepo(org.ID, req.Name, req.Visibility, shardHint, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create repo", err)
		return
	}

	// Audit
	h.db.WriteAudit(&org.ID, &user.ID, "repo.create", "repo", repo.ID, map[string]string{
		"name":       repo.Name,
		"visibility": repo.Visibility,
		"shard":      shardHint,
	})

	cloneURL := fmt.Sprintf("%s/%s/%s", h.cfg.BaseURL, org.Slug, repo.Name)

	writeJSON(w, http.StatusCreated, RepoResponse{
		ID:         repo.ID,
		OrgID:      repo.OrgID,
		OrgSlug:    org.Slug,
		Name:       repo.Name,
		Visibility: repo.Visibility,
		ShardHint:  repo.ShardHint,
		CreatedBy:  repo.CreatedBy,
		CreatedAt:  repo.CreatedAt.Format(time.RFC3339),
		CloneURL:   cloneURL,
	})
}

func (h *Handler) ListRepos(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())
	if org == nil {
		writeError(w, http.StatusNotFound, "org not found", nil)
		return
	}

	repos, err := h.db.ListOrgRepos(org.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list repos", err)
		return
	}

	var resp []RepoResponse
	for _, repo := range repos {
		cloneURL := fmt.Sprintf("%s/%s/%s", h.cfg.BaseURL, org.Slug, repo.Name)
		resp = append(resp, RepoResponse{
			ID:         repo.ID,
			OrgID:      repo.OrgID,
			OrgSlug:    org.Slug,
			Name:       repo.Name,
			Visibility: repo.Visibility,
			ShardHint:  repo.ShardHint,
			CreatedBy:  repo.CreatedBy,
			CreatedAt:  repo.CreatedAt.Format(time.RFC3339),
			CloneURL:   cloneURL,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"repos": resp})
}

func (h *Handler) GetRepo(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())
	repo := RepoFromContext(r.Context())

	if org == nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found", nil)
		return
	}

	cloneURL := fmt.Sprintf("%s/%s/%s", h.cfg.BaseURL, org.Slug, repo.Name)

	writeJSON(w, http.StatusOK, RepoResponse{
		ID:         repo.ID,
		OrgID:      repo.OrgID,
		OrgSlug:    org.Slug,
		Name:       repo.Name,
		Visibility: repo.Visibility,
		ShardHint:  repo.ShardHint,
		CreatedBy:  repo.CreatedBy,
		CreatedAt:  repo.CreatedAt.Format(time.RFC3339),
		CloneURL:   cloneURL,
	})
}

func (h *Handler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	org := OrgFromContext(r.Context())
	repo := RepoFromContext(r.Context())

	if user == nil || org == nil || repo == nil {
		writeError(w, http.StatusInternalServerError, "missing context", nil)
		return
	}

	// Delete on shard (kailabd)
	shardURL := h.shards.GetShardURL(repo.ShardHint)
	if shardURL != "" {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/admin/v1/repos/%s/%s", shardURL, org.Slug, repo.Name), nil)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to delete repo on shard: %v", err)
		} else {
			resp.Body.Close()
		}
	}

	// Delete in control plane
	if err := h.db.DeleteRepo(repo.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete repo", err)
		return
	}

	// Audit
	h.db.WriteAudit(&org.ID, &user.ID, "repo.delete", "repo", repo.ID, map[string]string{
		"name": repo.Name,
	})

	w.WriteHeader(http.StatusNoContent)
}
