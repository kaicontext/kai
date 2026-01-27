package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kailab-control/internal/db"
	"kailab-control/internal/model"
)

// ----- Webhook Management API -----

// WebhookResponse is the API response for a webhook.
type WebhookResponse struct {
	ID        string   `json:"id"`
	RepoID    string   `json:"repo_id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func webhookToResponse(w *model.Webhook) WebhookResponse {
	return WebhookResponse{
		ID:        w.ID,
		RepoID:    w.RepoID,
		URL:       w.URL,
		Events:    w.Events,
		Active:    w.Active,
		CreatedAt: w.CreatedAt.Format(time.RFC3339),
		UpdatedAt: w.UpdatedAt.Format(time.RFC3339),
	}
}

// ListWebhooks lists all webhooks for a repository.
func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgSlug := r.PathValue("org")
	repoName := r.PathValue("repo")

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

	// Check membership (need at least maintainer to view webhooks)
	membership, err := h.db.GetMembership(org.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this org", nil)
		return
	}
	if !model.HasAtLeastRole(membership.Role, model.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "insufficient permissions", nil)
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

	webhooks, err := h.db.ListRepoWebhooks(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list webhooks", err)
		return
	}

	var resp []WebhookResponse
	for _, wh := range webhooks {
		resp = append(resp, webhookToResponse(wh))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"webhooks": resp})
}

// CreateWebhookRequest is the request body for creating a webhook.
type CreateWebhookRequest struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret,omitempty"`
	Events []string `json:"events,omitempty"`
	Active *bool    `json:"active,omitempty"`
}

// CreateWebhook creates a new webhook for a repository.
func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgSlug := r.PathValue("org")
	repoName := r.PathValue("repo")

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

	// Check membership (need at least maintainer to create webhooks)
	membership, err := h.db.GetMembership(org.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this org", nil)
		return
	}
	if !model.HasAtLeastRole(membership.Role, model.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "insufficient permissions", nil)
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

	var req CreateWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required", nil)
		return
	}

	// Validate URL
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, http.StatusBadRequest, "url must start with http:// or https://", nil)
		return
	}

	// Default events to push
	events := req.Events
	if len(events) == 0 {
		events = []string{model.EventPush}
	}

	// Validate events
	for _, e := range events {
		valid := false
		for _, allowed := range model.AllWebhookEvents {
			if e == allowed {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid event: %s", e), nil)
			return
		}
	}

	webhook, err := h.db.CreateWebhook(repo.ID, req.URL, req.Secret, events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook", err)
		return
	}

	h.db.WriteAudit(&org.ID, &user.ID, "webhook.create", "webhook", webhook.ID, map[string]string{
		"url":    webhook.URL,
		"events": strings.Join(webhook.Events, ","),
	})

	writeJSON(w, http.StatusCreated, webhookToResponse(webhook))
}

// UpdateWebhookRequest is the request body for updating a webhook.
type UpdateWebhookRequest struct {
	URL    string   `json:"url,omitempty"`
	Secret string   `json:"secret,omitempty"`
	Events []string `json:"events,omitempty"`
	Active *bool    `json:"active,omitempty"`
}

// UpdateWebhook updates a webhook.
func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgSlug := r.PathValue("org")
	repoName := r.PathValue("repo")
	webhookID := r.PathValue("webhook_id")

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

	// Check membership
	membership, err := h.db.GetMembership(org.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this org", nil)
		return
	}
	if !model.HasAtLeastRole(membership.Role, model.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "insufficient permissions", nil)
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

	// Get existing webhook
	webhook, err := h.db.GetWebhookByID(webhookID)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusNotFound, "webhook not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get webhook", err)
		return
	}

	// Verify webhook belongs to this repo
	if webhook.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "webhook not found", nil)
		return
	}

	var req UpdateWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Apply updates
	url := webhook.URL
	if req.URL != "" {
		if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
			writeError(w, http.StatusBadRequest, "url must start with http:// or https://", nil)
			return
		}
		url = req.URL
	}

	secret := webhook.Secret
	if req.Secret != "" {
		secret = req.Secret
	}

	events := webhook.Events
	if len(req.Events) > 0 {
		// Validate events
		for _, e := range req.Events {
			valid := false
			for _, allowed := range model.AllWebhookEvents {
				if e == allowed {
					valid = true
					break
				}
			}
			if !valid {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid event: %s", e), nil)
				return
			}
		}
		events = req.Events
	}

	active := webhook.Active
	if req.Active != nil {
		active = *req.Active
	}

	if err := h.db.UpdateWebhook(webhookID, url, secret, events, active); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update webhook", err)
		return
	}

	h.db.WriteAudit(&org.ID, &user.ID, "webhook.update", "webhook", webhookID, nil)

	// Fetch updated webhook
	webhook, _ = h.db.GetWebhookByID(webhookID)
	writeJSON(w, http.StatusOK, webhookToResponse(webhook))
}

// DeleteWebhook deletes a webhook.
func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgSlug := r.PathValue("org")
	repoName := r.PathValue("repo")
	webhookID := r.PathValue("webhook_id")

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

	// Check membership
	membership, err := h.db.GetMembership(org.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this org", nil)
		return
	}
	if !model.HasAtLeastRole(membership.Role, model.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "insufficient permissions", nil)
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

	// Get existing webhook to verify it belongs to this repo
	webhook, err := h.db.GetWebhookByID(webhookID)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusNotFound, "webhook not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get webhook", err)
		return
	}

	if webhook.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "webhook not found", nil)
		return
	}

	if err := h.db.DeleteWebhook(webhookID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete webhook", err)
		return
	}

	h.db.WriteAudit(&org.ID, &user.ID, "webhook.delete", "webhook", webhookID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// ListWebhookDeliveries lists recent deliveries for a webhook.
func (h *Handler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgSlug := r.PathValue("org")
	repoName := r.PathValue("repo")
	webhookID := r.PathValue("webhook_id")

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

	// Check membership
	membership, err := h.db.GetMembership(org.ID, user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this org", nil)
		return
	}
	if !model.HasAtLeastRole(membership.Role, model.RoleMaintainer) {
		writeError(w, http.StatusForbidden, "insufficient permissions", nil)
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

	// Get webhook to verify it belongs to this repo
	webhook, err := h.db.GetWebhookByID(webhookID)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusNotFound, "webhook not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get webhook", err)
		return
	}

	if webhook.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "webhook not found", nil)
		return
	}

	deliveries, err := h.db.ListWebhookDeliveries(webhookID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deliveries", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"deliveries": deliveries})
}

// ----- Internal Webhook Trigger Endpoint -----

// TriggerWebhookRequest is the request from kailab to trigger webhooks.
type TriggerWebhookRequest struct {
	Repo    string                 `json:"repo"`    // "org/repo" format
	Event   string                 `json:"event"`   // push, branch_create, etc.
	Payload map[string]interface{} `json:"payload"` // Event-specific payload
}

// TriggerWebhooks is an internal endpoint for kailab to trigger webhooks after git operations.
func (h *Handler) TriggerWebhooks(w http.ResponseWriter, r *http.Request) {
	var req TriggerWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "invalid request body",
		})
		return
	}

	if req.Repo == "" || req.Event == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "repo and event required",
		})
		return
	}

	// Parse org/repo
	parts := strings.SplitN(req.Repo, "/", 2)
	if len(parts) != 2 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "invalid repo format",
		})
		return
	}
	orgSlug, repoName := parts[0], parts[1]

	// Strip .git suffix if present
	repoName = strings.TrimSuffix(repoName, ".git")

	// Get org and repo
	org, err := h.db.GetOrgBySlug(orgSlug)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "org not found",
		})
		return
	}

	repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "repo not found",
		})
		return
	}

	// Find webhooks subscribed to this event
	webhooks, err := h.db.ListActiveWebhooksForEvent(repo.ID, req.Event)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
			"error":     "failed to list webhooks",
		})
		return
	}

	if len(webhooks) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"triggered": 0,
		})
		return
	}

	// Build full payload
	fullPayload := map[string]interface{}{
		"event":      req.Event,
		"repository": map[string]interface{}{
			"id":        repo.ID,
			"name":      repo.Name,
			"full_name": orgSlug + "/" + repoName,
		},
		"organization": map[string]interface{}{
			"id":   org.ID,
			"slug": org.Slug,
			"name": org.Name,
		},
	}
	// Merge event-specific payload
	for k, v := range req.Payload {
		fullPayload[k] = v
	}

	payloadBytes, _ := json.Marshal(fullPayload)
	payloadStr := string(payloadBytes)

	// Queue deliveries for each webhook
	triggered := 0
	for _, wh := range webhooks {
		delivery, err := h.db.CreateWebhookDelivery(wh.ID, req.Event, payloadStr)
		if err != nil {
			continue
		}

		// Deliver synchronously (in production, this would be async)
		go h.deliverWebhook(wh, delivery)
		triggered++
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"triggered": triggered,
	})
}

// deliverWebhook sends the webhook payload to the configured URL.
func (h *Handler) deliverWebhook(webhook *model.Webhook, delivery *model.WebhookDelivery) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("POST", webhook.URL, bytes.NewReader([]byte(delivery.Payload)))
	if err != nil {
		h.db.UpdateWebhookDelivery(delivery.ID, model.DeliveryFailed, 0, err.Error(), delivery.Attempts+1)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kailab-Webhook/1.0")
	req.Header.Set("X-Kailab-Event", delivery.Event)
	req.Header.Set("X-Kailab-Delivery", delivery.ID)

	// Add HMAC signature if secret is configured
	if webhook.Secret != "" {
		sig := computeHMAC([]byte(delivery.Payload), []byte(webhook.Secret))
		req.Header.Set("X-Kailab-Signature-256", "sha256="+sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		h.db.UpdateWebhookDelivery(delivery.ID, model.DeliveryFailed, 0, err.Error(), delivery.Attempts+1)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10000)) // Limit response body size

	status := model.DeliverySuccess
	if resp.StatusCode >= 400 {
		status = model.DeliveryFailed
	}

	h.db.UpdateWebhookDelivery(delivery.ID, status, resp.StatusCode, string(body), delivery.Attempts+1)
}

// computeHMAC computes the HMAC-SHA256 signature.
func computeHMAC(payload, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
