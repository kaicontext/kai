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
