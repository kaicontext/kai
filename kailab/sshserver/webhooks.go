package sshserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// WebhookNotifier sends webhook trigger requests to the control plane.
type WebhookNotifier struct {
	baseURL    string
	httpClient *http.Client
}

// NewWebhookNotifier creates a new webhook notifier.
func NewWebhookNotifier(baseURL string) *WebhookNotifier {
	return &WebhookNotifier{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// webhookTriggerRequest is the request to trigger webhooks.
type webhookTriggerRequest struct {
	Repo    string                 `json:"repo"`
	Event   string                 `json:"event"`
	Payload map[string]interface{} `json:"payload"`
}

// NotifyPush notifies the control plane of a push event.
func (n *WebhookNotifier) NotifyPush(repo string, updatedRefs []string) error {
	// Determine event type from refs
	event := "push"
	payload := map[string]interface{}{
		"refs": updatedRefs,
	}

	// Check for branch/tag creates or deletes
	// For now, just send push events - can be enhanced later
	// to detect branch_create, branch_delete, tag_create, tag_delete

	return n.trigger(repo, event, payload)
}

// trigger sends a webhook trigger request to the control plane.
func (n *WebhookNotifier) trigger(repo, event string, payload map[string]interface{}) error {
	reqBody := webhookTriggerRequest{
		Repo:    repo,
		Event:   event,
		Payload: payload,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", n.baseURL+"/internal/webhooks/trigger", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// We don't care about the response - webhooks are fire-and-forget from kailab's perspective
	return nil
}
