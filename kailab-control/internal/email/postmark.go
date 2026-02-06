// Package email provides email sending via Postmark.
package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client sends emails via Postmark.
type Client struct {
	serverToken string
	from        string
	httpClient  *http.Client
}

// New creates a new Postmark email client.
func New(serverToken, from string) *Client {
	return &Client{
		serverToken: serverToken,
		from:        from,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// postmarkRequest is the Postmark API request body.
type postmarkRequest struct {
	From     string `json:"From"`
	To       string `json:"To"`
	Subject  string `json:"Subject"`
	HtmlBody string `json:"HtmlBody,omitempty"`
	TextBody string `json:"TextBody,omitempty"`
}

// postmarkResponse is the Postmark API response.
type postmarkResponse struct {
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
	MessageID string `json:"MessageID"`
}

// Send sends an email via Postmark.
func (c *Client) Send(to, subject, htmlBody, textBody string) error {
	if c.serverToken == "" {
		return fmt.Errorf("postmark server token not configured")
	}

	reqBody := postmarkRequest{
		From:     c.from,
		To:       to,
		Subject:  subject,
		HtmlBody: htmlBody,
		TextBody: textBody,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.postmarkapp.com/email", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", c.serverToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	var pmResp postmarkResponse
	if err := json.NewDecoder(resp.Body).Decode(&pmResp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if pmResp.ErrorCode != 0 {
		return fmt.Errorf("postmark error %d: %s", pmResp.ErrorCode, pmResp.Message)
	}

	return nil
}

// SendMagicLink sends a magic link login email.
func (c *Client) SendMagicLink(to, loginURL, token string) error {
	subject := "Sign in to Kailab"

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 24px; font-size: 24px; color: #111;">Sign in to Kailab</h1>
    <p style="margin: 0 0 24px; color: #555; line-height: 1.5;">
      Click the button below to sign in. This link expires in 15 minutes.
    </p>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      Sign in
    </a>
    <div style="margin: 32px 0 0; padding: 16px; background: #f9f9f9; border-radius: 6px;">
      <p style="margin: 0 0 8px; color: #555; font-size: 13px; font-weight: 500;">
        Using the CLI? Copy this token:
      </p>
      <code style="display: block; padding: 8px 12px; background: #fff; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; font-size: 12px; word-break: break-all; color: #333;">%s</code>
    </div>
    <p style="margin: 24px 0 0; color: #999; font-size: 13px; line-height: 1.5;">
      If you didn't request this email, you can safely ignore it.
    </p>
  </div>
</body>
</html>`, loginURL, token)

	textBody := fmt.Sprintf(`Sign in to Kailab

Click the link below to sign in. This link expires in 15 minutes.

%s

Using the CLI? Copy this token:
%s

If you didn't request this email, you can safely ignore it.`, loginURL, token)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendCommentNotification sends a notification about a new comment or reply.
func (c *Client) SendCommentNotification(to, commenterName, reviewTitle, commentBody, reviewURL string, isReply bool) error {
	var subject string
	var action string
	if isReply {
		subject = fmt.Sprintf("%s replied to your comment", commenterName)
		action = "replied to your comment on"
	} else {
		subject = fmt.Sprintf("%s commented on your review", commenterName)
		action = "commented on"
	}

	// Truncate comment body for preview
	preview := commentBody
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 560px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 16px; font-size: 20px; color: #111;">%s %s</h1>
    <p style="margin: 0 0 8px; color: #666; font-size: 14px;">Review: <strong>%s</strong></p>
    <div style="margin: 24px 0; padding: 16px; background: #f9f9f9; border-left: 3px solid #ddd; border-radius: 4px;">
      <p style="margin: 0; color: #333; line-height: 1.6; white-space: pre-wrap;">%s</p>
    </div>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Comment
    </a>
    <p style="margin: 24px 0 0; color: #999; font-size: 12px;">
      You're receiving this because you're involved in this review.
    </p>
  </div>
</body>
</html>`, commenterName, action, reviewTitle, preview, reviewURL)

	textBody := fmt.Sprintf(`%s %s

Review: %s

---
%s
---

View the comment: %s

You're receiving this because you're involved in this review.`, commenterName, action, reviewTitle, preview, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendOrgInvitation sends an email when someone is added to an organization.
func (c *Client) SendOrgInvitation(to, inviterName, orgName, role, orgURL string) error {
	subject := fmt.Sprintf("You've been added to %s on Kailab", orgName)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 24px; font-size: 24px; color: #111;">You've been added to %s</h1>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> has added you to the <strong>%s</strong> organization on Kailab as a <strong>%s</strong>.
    </p>
    <p style="margin: 0 0 24px; color: #555; line-height: 1.5;">
      You can now access the organization's repositories and collaborate with the team.
    </p>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Organization
    </a>
    <p style="margin: 24px 0 0; color: #999; font-size: 13px; line-height: 1.5;">
      If you don't recognize this organization, you can ignore this email or contact support.
    </p>
  </div>
</body>
</html>`, orgName, inviterName, orgName, role, orgURL)

	textBody := fmt.Sprintf(`You've been added to %s

%s has added you to the %s organization on Kailab as a %s.

You can now access the organization's repositories and collaborate with the team.

View the organization: %s

If you don't recognize this organization, you can ignore this email or contact support.`, orgName, inviterName, orgName, role, orgURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendReviewCreated sends an email when a new review is created.
func (c *Client) SendReviewCreated(to, authorName, reviewTitle, reviewURL, org, repo string) error {
	subject := fmt.Sprintf("[%s/%s] New review: %s", org, repo, reviewTitle)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 24px; font-size: 24px; color: #111;">New Review Created</h1>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> created a new review in <strong>%s/%s</strong>:
    </p>
    <div style="margin: 16px 0; padding: 16px; background: #f9f9f9; border-left: 3px solid #3b82f6; border-radius: 4px;">
      <p style="margin: 0; font-size: 16px; font-weight: 500; color: #111;">%s</p>
    </div>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
    <p style="margin: 24px 0 0; color: #999; font-size: 13px; line-height: 1.5;">
      You're receiving this because you're a reviewer on this review.
    </p>
  </div>
</body>
</html>`, authorName, org, repo, reviewTitle, reviewURL)

	textBody := fmt.Sprintf(`New Review Created

%s created a new review in %s/%s:

%s

View the review: %s

You're receiving this because you're a reviewer on this review.`, authorName, org, repo, reviewTitle, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendOrgRemoval sends an email when someone is removed from an organization.
func (c *Client) SendOrgRemoval(to, removerName, orgName string) error {
	subject := fmt.Sprintf("You've been removed from %s on Kailab", orgName)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 24px; font-size: 24px; color: #111;">You've been removed from %s</h1>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> has removed you from the <strong>%s</strong> organization on Kailab.
    </p>
    <p style="margin: 0 0 24px; color: #555; line-height: 1.5;">
      You no longer have access to the organization's repositories.
    </p>
    <p style="margin: 24px 0 0; color: #999; font-size: 13px; line-height: 1.5;">
      If you believe this was a mistake, please contact the organization administrator.
    </p>
  </div>
</body>
</html>`, orgName, removerName, orgName)

	textBody := fmt.Sprintf(`You've been removed from %s

%s has removed you from the %s organization on Kailab.

You no longer have access to the organization's repositories.

If you believe this was a mistake, please contact the organization administrator.`, orgName, removerName, orgName)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendPipelineResult sends a notification when a CI pipeline completes.
func (c *Client) SendPipelineResult(to, org, repo, workflowName, conclusion, runURL, triggerRef, triggerSHA string) error {
	var statusEmoji, statusText, statusColor string
	if conclusion == "success" {
		statusEmoji = "✓"
		statusText = "passed"
		statusColor = "#22c55e"
	} else {
		statusEmoji = "✕"
		statusText = "failed"
		statusColor = "#ef4444"
	}

	subject := fmt.Sprintf("[%s/%s] %s %s: %s", org, repo, workflowName, statusText, triggerRef)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 560px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: %s; color: #fff; font-size: 20px; font-weight: bold;">%s</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Pipeline %s</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <div style="margin: 24px 0; padding: 16px; background: #f9f9f9; border-radius: 8px;">
      <p style="margin: 0 0 8px; color: #666; font-size: 13px;">Workflow</p>
      <p style="margin: 0; font-weight: 500; color: #111;">%s</p>
    </div>
    <div style="display: flex; gap: 24px; margin: 16px 0;">
      <div>
        <p style="margin: 0 0 4px; color: #666; font-size: 13px;">Branch</p>
        <p style="margin: 0; font-weight: 500; color: #111;">%s</p>
      </div>
      <div>
        <p style="margin: 0 0 4px; color: #666; font-size: 13px;">Commit</p>
        <code style="font-family: monospace; font-size: 13px; color: #111;">%s</code>
      </div>
    </div>
    <a href="%s" style="display: inline-block; margin-top: 24px; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Pipeline
    </a>
  </div>
</body>
</html>`, statusColor, statusEmoji, statusText, org, repo, workflowName, triggerRef, triggerSHA[:7], runURL)

	textBody := fmt.Sprintf(`Pipeline %s: %s/%s

Workflow: %s
Branch: %s
Commit: %s

View pipeline: %s`, statusText, org, repo, workflowName, triggerRef, triggerSHA[:7], runURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// Comment represents a single code comment for email grouping.
type Comment struct {
	FilePath string
	Line     int
	Body     string
}

// SendRequestChanges sends a notification when a reviewer requests changes with grouped comments.
func (c *Client) SendRequestChanges(to, reviewerName, reviewTitle, reviewURL, org, repo string, comments []Comment) error {
	subject := fmt.Sprintf("[%s/%s] %s requested changes on: %s", org, repo, reviewerName, reviewTitle)

	// Build comments HTML
	var commentsHTML string
	for _, comment := range comments {
		commentsHTML += fmt.Sprintf(`
      <div style="margin: 16px 0; padding: 16px; background: #f9f9f9; border-left: 3px solid #f59e0b; border-radius: 4px;">
        <p style="margin: 0 0 8px; color: #666; font-size: 12px; font-family: monospace;">%s:%d</p>
        <p style="margin: 0; color: #333; line-height: 1.6; white-space: pre-wrap;">%s</p>
      </div>`, comment.FilePath, comment.Line, comment.Body)
	}

	// Build comments text
	var commentsText string
	for _, comment := range comments {
		commentsText += fmt.Sprintf("\n%s:%d\n%s\n---\n", comment.FilePath, comment.Line, comment.Body)
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 560px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: #f59e0b; color: #fff; font-size: 20px;">⟲</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Changes Requested</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> requested changes on <strong>%s</strong>
    </p>
    <div style="margin: 24px 0;">
      <h3 style="margin: 0 0 12px; font-size: 14px; color: #666; text-transform: uppercase; letter-spacing: 0.5px;">Comments (%d)</h3>
      %s
    </div>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
  </div>
</body>
</html>`, org, repo, reviewerName, reviewTitle, len(comments), commentsHTML, reviewURL)

	textBody := fmt.Sprintf(`Changes Requested

%s requested changes on %s in %s/%s

Comments (%d):
%s

View review: %s`, reviewerName, reviewTitle, org, repo, len(comments), commentsText, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendReviewMerged sends a notification when a review is merged.
func (c *Client) SendReviewMerged(to, mergerName, reviewTitle, reviewURL, org, repo, targetBranch string) error {
	subject := fmt.Sprintf("[%s/%s] Merged: %s", org, repo, reviewTitle)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: #8b5cf6; color: #fff; font-size: 20px;">⎇</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Review Merged</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> merged <strong>%s</strong> into <code style="background: #f3f4f6; padding: 2px 6px; border-radius: 4px;">%s</code>
    </p>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
  </div>
</body>
</html>`, org, repo, mergerName, reviewTitle, targetBranch, reviewURL)

	textBody := fmt.Sprintf(`Review Merged

%s merged %s into %s in %s/%s

View review: %s`, mergerName, reviewTitle, targetBranch, org, repo, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendReviewAbandoned sends a notification when a review is abandoned.
func (c *Client) SendReviewAbandoned(to, abandonerName, reviewTitle, reviewURL, org, repo, reason string) error {
	subject := fmt.Sprintf("[%s/%s] Abandoned: %s", org, repo, reviewTitle)

	reasonHTML := ""
	reasonText := ""
	if reason != "" {
		reasonHTML = fmt.Sprintf(`
    <div style="margin: 16px 0; padding: 16px; background: #f9f9f9; border-radius: 8px;">
      <p style="margin: 0; color: #666; font-style: italic;">"%s"</p>
    </div>`, reason)
		reasonText = fmt.Sprintf("\nReason: %s\n", reason)
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: #6b7280; color: #fff; font-size: 20px;">⊘</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Review Abandoned</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> abandoned <strong>%s</strong>
    </p>
    %s
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
  </div>
</body>
</html>`, org, repo, abandonerName, reviewTitle, reasonHTML, reviewURL)

	textBody := fmt.Sprintf(`Review Abandoned

%s abandoned %s in %s/%s
%s
View review: %s`, abandonerName, reviewTitle, org, repo, reasonText, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendReviewApproved sends a notification when a review is approved.
func (c *Client) SendReviewApproved(to, approverName, reviewTitle, reviewURL, org, repo string) error {
	subject := fmt.Sprintf("[%s/%s] Approved: %s", org, repo, reviewTitle)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: #22c55e; color: #fff; font-size: 20px;">✓</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Review Approved</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> approved <strong>%s</strong>
    </p>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
  </div>
</body>
</html>`, org, repo, approverName, reviewTitle, reviewURL)

	textBody := fmt.Sprintf(`Review Approved

%s approved %s in %s/%s

View review: %s`, approverName, reviewTitle, org, repo, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendReviewReady sends a notification when a review is marked ready for review.
func (c *Client) SendReviewReady(to, authorName, reviewTitle, reviewURL, org, repo string) error {
	subject := fmt.Sprintf("[%s/%s] Ready for review: %s", org, repo, reviewTitle)

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 480px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <div style="display: flex; align-items: center; margin-bottom: 24px;">
      <span style="display: inline-flex; align-items: center; justify-content: center; width: 40px; height: 40px; border-radius: 50%%; background: #3b82f6; color: #fff; font-size: 20px;">◉</span>
      <div style="margin-left: 16px;">
        <h1 style="margin: 0; font-size: 20px; color: #111;">Ready for Review</h1>
        <p style="margin: 4px 0 0; color: #666; font-size: 14px;">%s/%s</p>
      </div>
    </div>
    <p style="margin: 0 0 16px; color: #555; line-height: 1.5;">
      <strong>%s</strong> marked <strong>%s</strong> as ready for review
    </p>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Review
    </a>
    <p style="margin: 24px 0 0; color: #999; font-size: 13px;">
      You're receiving this because you're a reviewer on this review.
    </p>
  </div>
</body>
</html>`, org, repo, authorName, reviewTitle, reviewURL)

	textBody := fmt.Sprintf(`Ready for Review

%s marked %s as ready for review in %s/%s

View review: %s

You're receiving this because you're a reviewer on this review.`, authorName, reviewTitle, org, repo, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}

// SendMentionNotification sends a notification when someone is @mentioned.
func (c *Client) SendMentionNotification(to, commenterName, reviewTitle, commentBody, reviewURL string) error {
	subject := fmt.Sprintf("%s mentioned you in a comment", commenterName)

	// Truncate comment body for preview
	preview := commentBody
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; padding: 40px 20px; background: #f5f5f5;">
  <div style="max-width: 560px; margin: 0 auto; background: #fff; border-radius: 8px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
    <h1 style="margin: 0 0 16px; font-size: 20px; color: #111;">%s mentioned you</h1>
    <p style="margin: 0 0 8px; color: #666; font-size: 14px;">Review: <strong>%s</strong></p>
    <div style="margin: 24px 0; padding: 16px; background: #f9f9f9; border-left: 3px solid #3b82f6; border-radius: 4px;">
      <p style="margin: 0; color: #333; line-height: 1.6; white-space: pre-wrap;">%s</p>
    </div>
    <a href="%s" style="display: inline-block; background: #111; color: #fff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500;">
      View Comment
    </a>
    <p style="margin: 24px 0 0; color: #999; font-size: 12px;">
      You're receiving this because you were mentioned.
    </p>
  </div>
</body>
</html>`, commenterName, reviewTitle, preview, reviewURL)

	textBody := fmt.Sprintf(`%s mentioned you

Review: %s

---
%s
---

View the comment: %s

You're receiving this because you were mentioned.`, commenterName, reviewTitle, preview, reviewURL)

	return c.Send(to, subject, htmlBody, textBody)
}
