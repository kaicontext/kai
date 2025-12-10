// Package remote provides auth functionality for Kailab servers.
package remote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials stores authentication tokens.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Email        string `json:"email,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	ServerURL    string `json:"server_url,omitempty"`
}

// CredentialsPath returns the path to the credentials file.
func CredentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kai", "credentials.json")
}

// LoadCredentials loads stored credentials.
func LoadCredentials() (*Credentials, error) {
	path := CredentialsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	return &creds, nil
}

// SaveCredentials saves credentials.
func SaveCredentials(creds *Credentials) error {
	path := CredentialsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	// Restrict permissions to owner only
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	return nil
}

// ClearCredentials removes stored credentials.
func ClearCredentials() error {
	path := CredentialsPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing credentials: %w", err)
	}
	return nil
}

// AuthClient handles authentication with kailab-control.
type AuthClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewAuthClient creates a new auth client.
func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MagicLinkResponse is the response from sending a magic link.
type MagicLinkResponse struct {
	Message  string `json:"message"`
	DevToken string `json:"dev_token,omitempty"`
}

// TokenResponse is the response from exchanging a token.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// MeResponse is the response from /api/v1/me.
type MeResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	CreatedAt string `json:"created_at"`
}

// SendMagicLink requests a magic link email.
func (c *AuthClient) SendMagicLink(email string) (*MagicLinkResponse, error) {
	body, _ := json.Marshal(map[string]string{"email": email})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/v1/auth/magic-link", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result MagicLinkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// ExchangeToken exchanges a magic link token for access/refresh tokens.
func (c *AuthClient) ExchangeToken(magicToken string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"magic_token": magicToken})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/v1/auth/token", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// RefreshAccessToken refreshes the access token using the refresh token.
func (c *AuthClient) RefreshAccessToken(refreshToken string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/v1/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// GetMe retrieves the current user info.
func (c *AuthClient) GetMe(accessToken string) (*MeResponse, error) {
	req, _ := http.NewRequest("GET", c.BaseURL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result MeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

func (c *AuthClient) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Error   string `json:"error"`
		Details string `json:"details,omitempty"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		return fmt.Errorf("%s", errResp.Error)
	}
	return fmt.Errorf("server error: %d %s", resp.StatusCode, string(body))
}

// Login performs an interactive login flow.
func Login(serverURL string) error {
	client := NewAuthClient(serverURL)

	// Prompt for email
	fmt.Print("Email: ")
	reader := bufio.NewReader(os.Stdin)
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	if email == "" {
		return fmt.Errorf("email required")
	}

	// Send magic link
	fmt.Printf("Sending login link to %s...\n", email)
	result, err := client.SendMagicLink(email)
	if err != nil {
		return fmt.Errorf("sending magic link: %w", err)
	}

	var token string

	// In dev mode, the token might be returned directly
	if result.DevToken != "" {
		fmt.Println("Dev mode: Token received directly")
		token = result.DevToken
	} else {
		fmt.Println("Check your email for a login link (from notifications@1medium.ai).")
		fmt.Print("Copy the token from the email and paste it here: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Handle if user pasted a URL
		if strings.Contains(input, "token=") {
			parts := strings.Split(input, "token=")
			if len(parts) > 1 {
				token = strings.Split(parts[1], "&")[0]
			}
		} else {
			token = input
		}
	}

	if token == "" {
		return fmt.Errorf("token required")
	}

	// Exchange token
	fmt.Println("Logging in...")
	tokens, err := client.ExchangeToken(token)
	if err != nil {
		return fmt.Errorf("exchanging token: %w", err)
	}

	// Save credentials
	creds := &Credentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Email:        email,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix(),
		ServerURL:    serverURL,
	}
	if err := SaveCredentials(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Printf("Logged in as %s\n", email)
	return nil
}

// Logout clears stored credentials.
func Logout() error {
	return ClearCredentials()
}

// GetValidAccessToken returns a valid access token, refreshing if needed.
func GetValidAccessToken() (string, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", fmt.Errorf("not logged in (run 'kai auth login')")
	}

	// Check if token is expired or about to expire (within 60 seconds)
	if creds.ExpiresAt > 0 && time.Now().Unix() > creds.ExpiresAt-60 {
		// Try to refresh
		if creds.RefreshToken != "" && creds.ServerURL != "" {
			client := NewAuthClient(creds.ServerURL)
			tokens, err := client.RefreshAccessToken(creds.RefreshToken)
			if err == nil {
				creds.AccessToken = tokens.AccessToken
				if tokens.RefreshToken != "" {
					creds.RefreshToken = tokens.RefreshToken
				}
				creds.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix()
				SaveCredentials(creds)
			}
		}
	}

	return creds.AccessToken, nil
}

// GetAuthStatus returns the current auth status.
func GetAuthStatus() (email string, serverURL string, loggedIn bool) {
	creds, err := LoadCredentials()
	if err != nil || creds == nil {
		return "", "", false
	}
	return creds.Email, creds.ServerURL, true
}
