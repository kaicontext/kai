package api

import (
	"encoding/json"
	"net/http"
	"time"

	"kailab-control/internal/model"
)

// ----- Orgs -----

type CreateOrgRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type OrgResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	Plan      string `json:"plan"`
	CreatedAt string `json:"created_at"`
}

func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	var req CreateOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Normalize and validate slug
	req.Slug = NormalizeSlug(req.Slug)
	if !ValidateSlug(req.Slug) {
		writeError(w, http.StatusBadRequest, "invalid slug: must be 1-63 lowercase letters, numbers, hyphens, underscores", nil)
		return
	}

	if req.Name == "" {
		req.Name = req.Slug
	}

	// Create org
	org, err := h.db.CreateOrg(req.Slug, req.Name, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create org", err)
		return
	}

	// Audit
	h.db.WriteAudit(&org.ID, &user.ID, "org.create", "org", org.ID, map[string]string{
		"slug": org.Slug,
	})

	writeJSON(w, http.StatusCreated, OrgResponse{
		ID:        org.ID,
		Slug:      org.Slug,
		Name:      org.Name,
		OwnerID:   org.OwnerID,
		Plan:      org.Plan,
		CreatedAt: org.CreatedAt.Format(time.RFC3339),
	})
}

func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	orgs, err := h.db.ListUserOrgs(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orgs", err)
		return
	}

	var resp []OrgResponse
	for _, o := range orgs {
		resp = append(resp, OrgResponse{
			ID:        o.ID,
			Slug:      o.Slug,
			Name:      o.Name,
			OwnerID:   o.OwnerID,
			Plan:      o.Plan,
			CreatedAt: o.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"orgs": resp})
}

func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())
	if org == nil {
		writeError(w, http.StatusNotFound, "org not found", nil)
		return
	}

	writeJSON(w, http.StatusOK, OrgResponse{
		ID:        org.ID,
		Slug:      org.Slug,
		Name:      org.Name,
		OwnerID:   org.OwnerID,
		Plan:      org.Plan,
		CreatedAt: org.CreatedAt.Format(time.RFC3339),
	})
}

// ----- Members -----

type MemberResponse struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())
	if org == nil {
		writeError(w, http.StatusNotFound, "org not found", nil)
		return
	}

	members, err := h.db.ListOrgMembers(org.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members", err)
		return
	}

	var resp []MemberResponse
	for _, m := range members {
		user, err := h.db.GetUserByID(m.UserID)
		if err != nil {
			continue
		}
		resp = append(resp, MemberResponse{
			UserID:    m.UserID,
			Email:     user.Email,
			Name:      user.Name,
			Role:      m.Role,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"members": resp})
}

type AddMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	actor := UserFromContext(r.Context())
	org := OrgFromContext(r.Context())

	if actor == nil || org == nil {
		writeError(w, http.StatusInternalServerError, "missing context", nil)
		return
	}

	var req AddMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email required", nil)
		return
	}

	// Validate role
	if _, ok := model.RoleHierarchy[req.Role]; !ok {
		writeError(w, http.StatusBadRequest, "invalid role", nil)
		return
	}

	// Get or create user
	user, _, err := h.db.GetOrCreateUser(req.Email, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get/create user", err)
		return
	}

	// Add membership
	if err := h.db.AddMember(org.ID, user.ID, req.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add member", err)
		return
	}

	// Audit
	h.db.WriteAudit(&org.ID, &actor.ID, "member.add", "user", user.ID, map[string]string{
		"email": req.Email,
		"role":  req.Role,
	})

	writeJSON(w, http.StatusCreated, MemberResponse{
		UserID: user.ID,
		Email:  user.Email,
		Role:   req.Role,
	})
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	actor := UserFromContext(r.Context())
	org := OrgFromContext(r.Context())

	if actor == nil || org == nil {
		writeError(w, http.StatusInternalServerError, "missing context", nil)
		return
	}

	userID := r.PathValue("user_id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid user_id", nil)
		return
	}

	// Can't remove the owner
	if userID == org.OwnerID {
		writeError(w, http.StatusBadRequest, "cannot remove the owner", nil)
		return
	}

	// Remove membership
	if err := h.db.RemoveMember(org.ID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove member", err)
		return
	}

	// Audit
	h.db.WriteAudit(&org.ID, &actor.ID, "member.remove", "user", userID, nil)

	w.WriteHeader(http.StatusNoContent)
}
