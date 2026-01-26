package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"kailab-control/internal/db"
	"kailab-control/internal/model"
)

// ----- User-facing SSH Key endpoints -----

// SSHKeyResponse is the API response for an SSH key.
type SSHKeyResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
}

func sshKeyToResponse(k *model.SSHKey) SSHKeyResponse {
	resp := SSHKeyResponse{
		ID:          k.ID,
		Name:        k.Name,
		Fingerprint: k.Fingerprint,
		PublicKey:   k.PublicKey,
		CreatedAt:   k.CreatedAt.Format(time.RFC3339),
	}
	if !k.LastUsedAt.IsZero() {
		resp.LastUsedAt = k.LastUsedAt.Format(time.RFC3339)
	}
	return resp
}

// ListSSHKeys lists all SSH keys for the authenticated user.
func (h *Handler) ListSSHKeys(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	keys, err := h.db.ListUserSSHKeys(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list SSH keys", err)
		return
	}

	var resp []SSHKeyResponse
	for _, k := range keys {
		resp = append(resp, sshKeyToResponse(k))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"ssh_keys": resp})
}

// CreateSSHKeyRequest is the request body for adding an SSH key.
type CreateSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// CreateSSHKey adds a new SSH key for the authenticated user.
func (h *Handler) CreateSSHKey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	var req CreateSSHKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required", nil)
		return
	}
	if req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "public_key is required", nil)
		return
	}

	// Parse the public key to validate it and compute fingerprint
	pubKey, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid SSH public key", err)
		return
	}

	// Compute SHA256 fingerprint
	fingerprint := computeFingerprint(pubKey)

	// Use comment as name if not provided
	name := req.Name
	if name == "" && comment != "" {
		name = comment
	}

	// Normalize the public key (remove trailing whitespace, normalize format)
	normalizedKey := strings.TrimSpace(req.PublicKey)

	key, err := h.db.CreateSSHKey(user.ID, name, fingerprint, normalizedKey)
	if err != nil {
		// Check if duplicate fingerprint
		if strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "duplicate key") {
			writeError(w, http.StatusConflict, "SSH key already exists", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create SSH key", err)
		return
	}

	h.db.WriteAudit(nil, &user.ID, "ssh_key.create", "ssh_key", key.ID, map[string]string{
		"name":        key.Name,
		"fingerprint": key.Fingerprint,
	})

	writeJSON(w, http.StatusCreated, sshKeyToResponse(key))
}

// DeleteSSHKey deletes an SSH key.
func (h *Handler) DeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated", nil)
		return
	}

	keyID := r.PathValue("id")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "key id required", nil)
		return
	}

	// Verify the key belongs to the user
	key, err := h.db.GetSSHKeyByID(keyID)
	if err != nil {
		if err == db.ErrNotFound {
			writeError(w, http.StatusNotFound, "SSH key not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get SSH key", err)
		return
	}

	if key.UserID != user.ID {
		writeError(w, http.StatusForbidden, "not authorized to delete this key", nil)
		return
	}

	if err := h.db.DeleteSSHKey(keyID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete SSH key", err)
		return
	}

	h.db.WriteAudit(nil, &user.ID, "ssh_key.delete", "ssh_key", keyID, map[string]string{
		"name":        key.Name,
		"fingerprint": key.Fingerprint,
	})

	w.WriteHeader(http.StatusNoContent)
}

// ----- Internal SSH verification endpoint -----

// SSHVerifyRequest is the request for verifying SSH key access.
type SSHVerifyRequest struct {
	Fingerprint string `json:"fingerprint"`
	Repo        string `json:"repo"` // "org/repo" format
}

// SSHVerifyResponse is the response for SSH key verification.
type SSHVerifyResponse struct {
	Allowed    bool   `json:"allowed"`
	UserID     string `json:"user_id,omitempty"`
	UserEmail  string `json:"user_email,omitempty"`
	Permission string `json:"permission,omitempty"` // "read", "write", or "admin"
	Reason     string `json:"reason,omitempty"`
}

// VerifySSHKey verifies an SSH key fingerprint and returns user permissions.
// This is an internal endpoint for kailab to verify SSH authentication.
func (h *Handler) VerifySSHKey(w http.ResponseWriter, r *http.Request) {
	var req SSHVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, SSHVerifyResponse{
			Allowed: false,
			Reason:  "invalid request body",
		})
		return
	}

	if req.Fingerprint == "" {
		writeJSON(w, http.StatusOK, SSHVerifyResponse{
			Allowed: false,
			Reason:  "fingerprint required",
		})
		return
	}

	// Look up the SSH key by fingerprint
	key, err := h.db.GetSSHKeyByFingerprint(req.Fingerprint)
	if err != nil {
		if err == db.ErrNotFound {
			writeJSON(w, http.StatusOK, SSHVerifyResponse{
				Allowed: false,
				Reason:  "unknown key",
			})
			return
		}
		writeJSON(w, http.StatusOK, SSHVerifyResponse{
			Allowed: false,
			Reason:  "internal error",
		})
		return
	}

	// Get the user
	user, err := h.db.GetUserByID(key.UserID)
	if err != nil {
		writeJSON(w, http.StatusOK, SSHVerifyResponse{
			Allowed: false,
			Reason:  "user not found",
		})
		return
	}

	// Update last used timestamp
	h.db.UpdateSSHKeyLastUsed(key.ID)

	// If repo is specified, check permissions
	permission := "write" // Default permission for authenticated user
	if req.Repo != "" {
		// Strip .git suffix if present
		repoPath := strings.TrimSuffix(req.Repo, ".git")
		parts := strings.SplitN(repoPath, "/", 2)
		if len(parts) != 2 {
			writeJSON(w, http.StatusOK, SSHVerifyResponse{
				Allowed: false,
				Reason:  "invalid repo format",
			})
			return
		}

		orgSlug, repoName := parts[0], parts[1]

		// Get org
		org, err := h.db.GetOrgBySlug(orgSlug)
		if err != nil {
			if err == db.ErrNotFound {
				writeJSON(w, http.StatusOK, SSHVerifyResponse{
					Allowed: false,
					Reason:  "org not found",
				})
				return
			}
			writeJSON(w, http.StatusOK, SSHVerifyResponse{
				Allowed: false,
				Reason:  "internal error",
			})
			return
		}

		// Get repo
		repo, err := h.db.GetRepoByOrgAndName(org.ID, repoName)
		if err != nil {
			if err == db.ErrNotFound {
				writeJSON(w, http.StatusOK, SSHVerifyResponse{
					Allowed: false,
					Reason:  "repo not found",
				})
				return
			}
			writeJSON(w, http.StatusOK, SSHVerifyResponse{
				Allowed: false,
				Reason:  "internal error",
			})
			return
		}

		// Check membership
		membership, err := h.db.GetMembership(org.ID, user.ID)
		if err != nil {
			if err == db.ErrNotFound {
				// Check if repo is public (allow read access)
				if repo.Visibility == "public" {
					writeJSON(w, http.StatusOK, SSHVerifyResponse{
						Allowed:    true,
						UserID:     user.ID,
						UserEmail:  user.Email,
						Permission: "read",
					})
					return
				}
				writeJSON(w, http.StatusOK, SSHVerifyResponse{
					Allowed: false,
					Reason:  "not a member of this org",
				})
				return
			}
			writeJSON(w, http.StatusOK, SSHVerifyResponse{
				Allowed: false,
				Reason:  "internal error",
			})
			return
		}

		// Map role to permission
		permission = roleToPermission(membership.Role)
	}

	writeJSON(w, http.StatusOK, SSHVerifyResponse{
		Allowed:    true,
		UserID:     user.ID,
		UserEmail:  user.Email,
		Permission: permission,
	})
}

// computeFingerprint computes the SHA256 fingerprint of an SSH public key.
func computeFingerprint(pubKey ssh.PublicKey) string {
	hash := sha256.Sum256(pubKey.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
}

// roleToPermission maps an org role to a permission level.
func roleToPermission(role string) string {
	switch role {
	case model.RoleOwner, model.RoleAdmin:
		return "admin"
	case model.RoleMaintainer, model.RoleDeveloper:
		return "write"
	case model.RoleReporter, model.RoleGuest:
		return "read"
	default:
		return "read"
	}
}
