// Package model provides data models for the control plane.
package model

import (
	"encoding/json"
	"time"
)

// User represents a registered user.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name,omitempty"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty"`
}

// Session represents a user session.
type Session struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	RefreshHash string    `json:"-"`
	UserAgent   string    `json:"user_agent,omitempty"`
	IP          string    `json:"ip,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// MagicLink represents a passwordless login link.
type MagicLink struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	TokenHash string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedAt    time.Time `json:"used_at,omitempty"`
}

// Org represents an organization (namespace).
type Org struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

// Membership represents a user's membership in an org.
type Membership struct {
	UserID    string    `json:"user_id"`
	OrgID     string    `json:"org_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Role constants
const (
	RoleOwner      = "owner"
	RoleAdmin      = "admin"
	RoleMaintainer = "maintainer"
	RoleDeveloper  = "developer"
	RoleReporter   = "reporter"
	RoleGuest      = "guest"
)

// RoleHierarchy defines the permission hierarchy (higher index = more permissions)
var RoleHierarchy = map[string]int{
	RoleGuest:      0,
	RoleReporter:   1,
	RoleDeveloper:  2,
	RoleMaintainer: 3,
	RoleAdmin:      4,
	RoleOwner:      5,
}

// HasAtLeastRole checks if the given role has at least the required role level.
func HasAtLeastRole(role, required string) bool {
	return RoleHierarchy[role] >= RoleHierarchy[required]
}

// Repo represents a repository.
type Repo struct {
	ID         string    `json:"id"`
	OrgID      string    `json:"org_id"`
	Name       string    `json:"name"`
	Visibility string    `json:"visibility"`
	ShardHint  string    `json:"shard_hint"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// RepoWithOrg includes org info for display.
type RepoWithOrg struct {
	Repo
	OrgSlug string `json:"org_slug"`
}

// APIToken represents a personal access token.
type APIToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	OrgID      string    `json:"org_id,omitempty"`
	Name       string    `json:"name"`
	Hash       string    `json:"-"`
	Scopes     []string  `json:"scopes"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// ScopesJSON returns scopes as a JSON string.
func (t *APIToken) ScopesJSON() string {
	b, _ := json.Marshal(t.Scopes)
	return string(b)
}

// ParseScopes parses a JSON scopes string.
func ParseScopes(s string) []string {
	var scopes []string
	json.Unmarshal([]byte(s), &scopes)
	return scopes
}

// AuditEntry represents an audit log entry.
type AuditEntry struct {
	ID         string            `json:"id"`
	OrgID      string            `json:"org_id,omitempty"`
	ActorID    string            `json:"actor_id,omitempty"`
	Action     string            `json:"action"`
	TargetType string            `json:"target_type,omitempty"`
	TargetID   string            `json:"target_id,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
	Timestamp  time.Time         `json:"ts"`
}

// Common scopes
const (
	ScopeRepoRead  = "repo:read"
	ScopeRepoWrite = "repo:write"
	ScopeOrgRead   = "org:read"
	ScopeOrgWrite  = "org:write"
	ScopeUserRead  = "user:read"
)

// HasScope checks if scopes include the required scope.
func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required {
			return true
		}
	}
	return false
}
