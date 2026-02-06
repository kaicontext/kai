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

// NotifyReviewCreated notifies the control plane of a new review.
func (n *WebhookNotifier) NotifyReviewCreated(repo, reviewID, title, author string, reviewers []string) error {
	// Parse org/repo from repo string
	parts := splitOrgRepo(repo)
	if len(parts) != 2 {
		return nil
	}

	reqBody := struct {
		Org          string   `json:"org"`
		Repo         string   `json:"repo"`
		ReviewID     string   `json:"reviewId"`
		ReviewTitle  string   `json:"reviewTitle"`
		ReviewAuthor string   `json:"reviewAuthor"`
		Reviewers    []string `json:"reviewers"`
	}{
		Org:          parts[0],
		Repo:         parts[1],
		ReviewID:     reviewID,
		ReviewTitle:  title,
		ReviewAuthor: author,
		Reviewers:    reviewers,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", n.baseURL+"/-/notify/review", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// splitOrgRepo splits "org/repo" into parts.
func splitOrgRepo(repo string) []string {
	for i := 0; i < len(repo); i++ {
		if repo[i] == '/' {
			return []string{repo[:i], repo[i+1:]}
		}
	}
	return []string{repo}
}

// ciTriggerRequest is the request to trigger CI workflows.
type ciTriggerRequest struct {
	Repo    string                 `json:"repo"`
	Event   string                 `json:"event"`
	Ref     string                 `json:"ref"`
	SHA     string                 `json:"sha"`
	Payload map[string]interface{} `json:"payload"`
}

// NotifyCI notifies the control plane to trigger CI workflows.
func (n *WebhookNotifier) NotifyCI(repo, event, ref, sha string, payload map[string]interface{}) error {
	reqBody := ciTriggerRequest{
		Repo:    repo,
		Event:   event,
		Ref:     ref,
		SHA:     sha,
		Payload: payload,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", n.baseURL+"/-/ci/trigger", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// CI triggers are fire-and-forget from kailab's perspective
	return nil
}

// NotifyReviewState notifies the control plane of a review state change.
func (n *WebhookNotifier) NotifyReviewState(repo, reviewID, title, author, actorName, state, targetBranch, reason string, reviewers []string) error {
	parts := splitOrgRepo(repo)
	if len(parts) != 2 {
		return nil
	}

	reqBody := struct {
		Org          string   `json:"org"`
		Repo         string   `json:"repo"`
		ReviewID     string   `json:"reviewId"`
		ReviewTitle  string   `json:"reviewTitle"`
		ReviewAuthor string   `json:"reviewAuthor"`
		ActorName    string   `json:"actorName"`
		State        string   `json:"state"`
		TargetBranch string   `json:"targetBranch,omitempty"`
		Reason       string   `json:"reason,omitempty"`
		Reviewers    []string `json:"reviewers,omitempty"`
	}{
		Org:          parts[0],
		Repo:         parts[1],
		ReviewID:     reviewID,
		ReviewTitle:  title,
		ReviewAuthor: author,
		ActorName:    actorName,
		State:        state,
		TargetBranch: targetBranch,
		Reason:       reason,
		Reviewers:    reviewers,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", n.baseURL+"/-/notify/review-state", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// NotifyRequestChanges notifies the control plane of a request changes action.
func (n *WebhookNotifier) NotifyRequestChanges(repo, reviewID, title, author, reviewerName string, comments []struct {
	FilePath string
	Line     int
	Body     string
}) error {
	parts := splitOrgRepo(repo)
	if len(parts) != 2 {
		return nil
	}

	reqBody := struct {
		Org          string `json:"org"`
		Repo         string `json:"repo"`
		ReviewID     string `json:"reviewId"`
		ReviewTitle  string `json:"reviewTitle"`
		ReviewAuthor string `json:"reviewAuthor"`
		ReviewerName string `json:"reviewerName"`
		Comments     []struct {
			FilePath string `json:"filePath"`
			Line     int    `json:"line"`
			Body     string `json:"body"`
		} `json:"comments"`
	}{
		Org:          parts[0],
		Repo:         parts[1],
		ReviewID:     reviewID,
		ReviewTitle:  title,
		ReviewAuthor: author,
		ReviewerName: reviewerName,
		Comments: func() []struct {
			FilePath string `json:"filePath"`
			Line     int    `json:"line"`
			Body     string `json:"body"`
		} {
			result := make([]struct {
				FilePath string `json:"filePath"`
				Line     int    `json:"line"`
				Body     string `json:"body"`
			}, len(comments))
			for i, c := range comments {
				result[i] = struct {
					FilePath string `json:"filePath"`
					Line     int    `json:"line"`
					Body     string `json:"body"`
				}{
					FilePath: c.FilePath,
					Line:     c.Line,
					Body:     c.Body,
				}
			}
			return result
		}(),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", n.baseURL+"/-/notify/request-changes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
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

	req, err := http.NewRequest("POST", n.baseURL+"/-/webhooks/trigger", bytes.NewReader(body))
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
