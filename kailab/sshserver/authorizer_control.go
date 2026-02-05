package sshserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gliderlabs/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

// ControlPlaneAuthorizer verifies SSH keys against the control plane.
type ControlPlaneAuthorizer struct {
	baseURL    string
	httpClient *http.Client
}

// NewControlPlaneAuthorizer creates a new control plane authorizer.
func NewControlPlaneAuthorizer(baseURL string) *ControlPlaneAuthorizer {
	return &ControlPlaneAuthorizer{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// sshVerifyRequest is the request to the control plane.
type sshVerifyRequest struct {
	Fingerprint string `json:"fingerprint"`
	Repo        string `json:"repo"`
}

// sshVerifyResponse is the response from the control plane.
type sshVerifyResponse struct {
	Allowed    bool   `json:"allowed"`
	UserID     string `json:"user_id,omitempty"`
	UserEmail  string `json:"user_email,omitempty"`
	Permission string `json:"permission,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Authorize verifies the SSH key against the control plane.
func (a *ControlPlaneAuthorizer) Authorize(ctx context.Context, session ssh.Session, cmd GitCommand) error {
	key := session.PublicKey()
	if key == nil {
		return fmt.Errorf("access denied: SSH key required")
	}

	fingerprint := cryptossh.FingerprintSHA256(key)

	reqBody := sshVerifyRequest{
		Fingerprint: fingerprint,
		Repo:        cmd.Repo,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("access denied: internal error")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/-/ssh/verify", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("access denied: internal error")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("access denied: control plane unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("access denied: control plane error")
	}

	var verifyResp sshVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return fmt.Errorf("access denied: invalid response")
	}

	if !verifyResp.Allowed {
		reason := verifyResp.Reason
		if reason == "" {
			reason = "access denied"
		}
		return fmt.Errorf("access denied: %s", reason)
	}

	// Check permission for write operations
	if cmd.Type == GitReceivePack {
		if verifyResp.Permission == "read" {
			return fmt.Errorf("access denied: read-only access")
		}
	}

	return nil
}
