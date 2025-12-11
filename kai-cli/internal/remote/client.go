// Package remote provides client functionality for communicating with Kailab servers.
package remote

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
	"kai-core/cas"
)

// DefaultServer is the production Kailab server URL.
// This is used when no explicit remote is configured.
// Can be overridden via KAI_SERVER environment variable.
const DefaultServer = "https://kailayer.com"

// Client communicates with a Kailab server.
type Client struct {
	BaseURL    string
	Tenant     string
	Repo       string
	HTTPClient *http.Client
	Actor      string
	AuthToken  string
}

// NewClient creates a new Kailab client.
// baseURL should be the server base (e.g., http://localhost:7447)
// tenant and repo specify the repository to operate on.
func NewClient(baseURL, tenant, repo string) *Client {
	// Try to load auth token
	token, _ := GetValidAccessToken()

	return &Client{
		BaseURL: baseURL,
		Tenant:  tenant,
		Repo:    repo,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		Actor:     os.Getenv("USER"),
		AuthToken: token,
	}
}

// repoPath returns the path prefix for repo-scoped endpoints.
func (c *Client) repoPath() string {
	return "/" + c.Tenant + "/" + c.Repo
}

// --- Wire types (matching kailab/proto/wire.go) ---

// NegotiateRequest is sent to negotiate which objects need pushing.
type NegotiateRequest struct {
	Digests [][]byte `json:"digests,omitempty"`
}

// NegotiateResponse tells the client which objects are missing.
type NegotiateResponse struct {
	Missing [][]byte `json:"missing"`
}

// PackIngestResponse is returned after ingesting a pack.
type PackIngestResponse struct {
	SegmentID int64 `json:"segmentId"`
	Indexed   int   `json:"indexedCount"`
}

// RefUpdateRequest updates a ref.
type RefUpdateRequest struct {
	Old   []byte `json:"old,omitempty"`
	New   []byte `json:"new"`
	Force bool   `json:"force,omitempty"`
}

// RefUpdateResponse is returned after updating a ref.
type RefUpdateResponse struct {
	OK        bool   `json:"ok"`
	UpdatedAt int64  `json:"updatedAt"`
	PushID    string `json:"pushId"`
	Error     string `json:"error,omitempty"`
}

// BatchRefUpdate represents a single ref update in a batch.
type BatchRefUpdate struct {
	Name  string `json:"name"`
	Old   []byte `json:"old,omitempty"`
	New   []byte `json:"new"`
	Force bool   `json:"force,omitempty"`
}

// BatchRefUpdateRequest updates multiple refs atomically.
type BatchRefUpdateRequest struct {
	Updates []BatchRefUpdate `json:"updates"`
}

// BatchRefResult is the result for a single ref in a batch update.
type BatchRefResult struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// BatchRefUpdateResponse is returned after updating multiple refs.
type BatchRefUpdateResponse struct {
	PushID  string           `json:"pushId"`
	Results []BatchRefResult `json:"results"`
}

// RefEntry represents a single ref.
type RefEntry struct {
	Name      string `json:"name"`
	Target    []byte `json:"target"`
	UpdatedAt int64  `json:"updatedAt"`
	Actor     string `json:"actor"`
}

// RefsListResponse contains refs.
type RefsListResponse struct {
	Refs []*RefEntry `json:"refs"`
}

// LogEntry represents a log entry.
type LogEntry struct {
	Kind     string `json:"kind"`
	ID       []byte `json:"id"`
	Parent   []byte `json:"parent,omitempty"`
	Time     int64  `json:"time"`
	Actor    string `json:"actor"`
	Ref      string `json:"ref,omitempty"`
	Old      []byte `json:"old,omitempty"`
	New      []byte `json:"new,omitempty"`
	NodeID   []byte `json:"nodeId,omitempty"`
	NodeKind string `json:"nodeKind,omitempty"`
}

// LogEntriesResponse contains log entries.
type LogEntriesResponse struct {
	Entries []*LogEntry `json:"entries"`
}

// LogHeadResponse returns the log head.
type LogHeadResponse struct {
	Head []byte `json:"head"`
}

// ErrorResponse is returned for API errors.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// --- API Methods ---

// Negotiate sends object digests and returns which are missing on the server.
func (c *Client) Negotiate(digests [][]byte) ([][]byte, error) {
	req := NegotiateRequest{Digests: digests}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	resp, err := c.post(c.repoPath()+"/v1/push/negotiate", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result NegotiateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return result.Missing, nil
}

// PushPack sends a pack of objects to the server.
func (c *Client) PushPack(objects []PackObject) (*PackIngestResponse, error) {
	pack, err := BuildPack(objects)
	if err != nil {
		return nil, fmt.Errorf("building pack: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL+c.repoPath()+"/v1/objects/pack", bytes.NewReader(pack))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Kailab-Actor", c.Actor)
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result PackIngestResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}

// UpdateRef updates a ref on the server.
func (c *Client) UpdateRef(name string, old, new []byte, force bool) (*RefUpdateResponse, error) {
	req := RefUpdateRequest{Old: old, New: new, Force: force}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("PUT", c.BaseURL+c.repoPath()+"/v1/refs/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Kailab-Actor", c.Actor)
	if c.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	var result RefUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		return &result, fmt.Errorf("ref conflict: %s", result.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	return &result, nil
}

// BatchUpdateRefs updates multiple refs atomically in a single request.
func (c *Client) BatchUpdateRefs(updates []BatchRefUpdate) (*BatchRefUpdateResponse, error) {
	req := BatchRefUpdateRequest{Updates: updates}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.BaseURL+c.repoPath()+"/v1/refs/batch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Kailab-Actor", c.Actor)
	if c.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result BatchRefUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}

// GetRef retrieves a single ref.
func (c *Client) GetRef(name string) (*RefEntry, error) {
	resp, err := c.get(c.repoPath() + "/v1/refs/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result RefEntry
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}

// ListRefs lists refs, optionally filtered by prefix.
func (c *Client) ListRefs(prefix string) ([]*RefEntry, error) {
	url := c.repoPath() + "/v1/refs"
	if prefix != "" {
		url += "?prefix=" + prefix
	}

	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result RefsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return result.Refs, nil
}

// GetObject retrieves a single object by digest.
func (c *Client) GetObject(digest []byte) ([]byte, string, error) {
	digestHex := hex.EncodeToString(digest)
	resp, err := c.get(c.repoPath() + "/v1/objects/" + digestHex)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", c.parseError(resp)
	}

	kind := resp.Header.Get("X-Kailab-Kind")
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading body: %w", err)
	}

	// Content is stored as "Kind\n{json...}" for digest verification.
	// Strip the kind prefix to return just the JSON payload.
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		content = content[idx+1:]
	}

	return content, kind, nil
}

// GetLogHead returns the current log head.
func (c *Client) GetLogHead() ([]byte, error) {
	resp, err := c.get(c.repoPath() + "/v1/log/head")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result LogHeadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return result.Head, nil
}

// GetLogEntries retrieves log entries.
func (c *Client) GetLogEntries(refFilter string, afterSeq, limit int) ([]*LogEntry, error) {
	url := fmt.Sprintf(c.repoPath()+"/v1/log/entries?after=%d&limit=%d", afterSeq, limit)
	if refFilter != "" {
		url += "&ref=" + refFilter
	}

	resp, err := c.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result LogEntriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return result.Entries, nil
}

// Health checks if the server is healthy.
func (c *Client) Health() error {
	resp, err := c.get("/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// --- Helper methods ---

func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	return c.HTTPClient.Do(req)
}

func (c *Client) post(path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	return c.HTTPClient.Do(req)
}

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		if errResp.Details != "" {
			return fmt.Errorf("%s: %s", errResp.Error, errResp.Details)
		}
		return fmt.Errorf("%s", errResp.Error)
	}
	return fmt.Errorf("server error: %d %s", resp.StatusCode, string(body))
}

// --- Pack building ---

// PackObject represents an object to pack.
type PackObject struct {
	Digest  []byte
	Kind    string
	Content []byte
}

// PackHeader describes objects in a pack.
type PackHeader struct {
	Objects []PackObjectEntry `json:"objects"`
}

// PackObjectEntry describes a single object in a pack.
type PackObjectEntry struct {
	Digest []byte `json:"digest"`
	Kind   string `json:"kind"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

const headerLengthSize = 4

// BuildPack creates a zstd-compressed pack.
func BuildPack(objects []PackObject) ([]byte, error) {
	var header PackHeader
	var data bytes.Buffer

	for _, obj := range objects {
		entry := PackObjectEntry{
			Digest: obj.Digest,
			Kind:   obj.Kind,
			Offset: int64(data.Len()),
			Length: int64(len(obj.Content)),
		}
		header.Objects = append(header.Objects, entry)
		data.Write(obj.Content)
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshaling header: %w", err)
	}

	var pack bytes.Buffer
	headerLen := make([]byte, headerLengthSize)
	binary.BigEndian.PutUint32(headerLen, uint32(len(headerJSON)))
	pack.Write(headerLen)
	pack.Write(headerJSON)
	pack.Write(data.Bytes())

	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		return nil, fmt.Errorf("creating encoder: %w", err)
	}
	if _, err := encoder.Write(pack.Bytes()); err != nil {
		encoder.Close()
		return nil, fmt.Errorf("compressing: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("closing encoder: %w", err)
	}

	return compressed.Bytes(), nil
}

// --- Config ---

// RemoteEntry holds configuration for a single remote.
type RemoteEntry struct {
	URL    string `json:"url"`
	Tenant string `json:"tenant"`
	Repo   string `json:"repo"`
}

// Config holds remote configuration.
type Config struct {
	Remotes map[string]*RemoteEntry `json:"remotes"` // name -> entry
}

// ConfigPath returns the path to the remote config file.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kai", "remotes.json")
}

// LoadConfig loads the remote configuration.
func LoadConfig() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{Remotes: make(map[string]*RemoteEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Try to migrate from old format (remotes were strings)
		var oldCfg struct {
			Remotes map[string]string `json:"remotes"`
		}
		if err2 := json.Unmarshal(data, &oldCfg); err2 == nil && len(oldCfg.Remotes) > 0 {
			cfg.Remotes = make(map[string]*RemoteEntry)
			for name, url := range oldCfg.Remotes {
				cfg.Remotes[name] = &RemoteEntry{
					URL:    url,
					Tenant: "default",
					Repo:   "main",
				}
			}
			// Save migrated config
			SaveConfig(&cfg)
			return &cfg, nil
		}
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]*RemoteEntry)
	}
	return &cfg, nil
}

// SaveConfig saves the remote configuration.
func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// GetRemote gets the entry for a named remote.
// If the remote is not configured and the name is "origin", it falls back to:
// 1. KAI_SERVER environment variable
// 2. DefaultServer constant (production server)
func GetRemote(name string) (*RemoteEntry, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	entry, ok := cfg.Remotes[name]
	if !ok {
		// For "origin", fall back to default server
		if name == "origin" {
			serverURL := os.Getenv("KAI_SERVER")
			if serverURL == "" {
				serverURL = DefaultServer
			}
			return &RemoteEntry{
				URL:    serverURL,
				Tenant: "default",
				Repo:   "main",
			}, nil
		}
		return nil, fmt.Errorf("remote %q not configured", name)
	}
	return entry, nil
}

// GetRemoteURL gets the URL for a named remote (backwards compatible).
func GetRemoteURL(name string) (string, error) {
	entry, err := GetRemote(name)
	if err != nil {
		return "", err
	}
	return entry.URL, nil
}

// SetRemote sets the entry for a named remote.
func SetRemote(name string, entry *RemoteEntry) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	cfg.Remotes[name] = entry
	return SaveConfig(cfg)
}

// SetRemoteURL sets the URL for a named remote with default tenant/repo.
func SetRemoteURL(name, url string) error {
	return SetRemote(name, &RemoteEntry{
		URL:    url,
		Tenant: "default",
		Repo:   "main",
	})
}

// NewClientForRemote creates a new client for a named remote.
func NewClientForRemote(name string) (*Client, error) {
	entry, err := GetRemote(name)
	if err != nil {
		return nil, err
	}
	return NewClient(entry.URL, entry.Tenant, entry.Repo), nil
}

// DeleteRemote deletes a named remote.
func DeleteRemote(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	if _, ok := cfg.Remotes[name]; !ok {
		return fmt.Errorf("remote %q not found", name)
	}

	delete(cfg.Remotes, name)
	return SaveConfig(cfg)
}

// ListRemotes returns all configured remotes.
func ListRemotes() (map[string]*RemoteEntry, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Remotes, nil
}

// CollectObjects collects all objects reachable from a set of node IDs.
// This is a helper for building packs - it traverses the graph to find all related objects.
func CollectObjects(db interface {
	GetNode([]byte) (interface{ GetPayload() map[string]interface{} }, error)
	GetEdges([]byte, string) ([]interface{ GetDstID() []byte }, error)
	ReadObject(string) ([]byte, error)
}, nodeIDs [][]byte) ([]PackObject, error) {
	visited := make(map[string]bool)
	var objects []PackObject

	var collect func([]byte) error
	collect = func(id []byte) error {
		idHex := hex.EncodeToString(id)
		if visited[idHex] {
			return nil
		}
		visited[idHex] = true

		// Get the node
		node, err := db.GetNode(id)
		if err != nil {
			return err
		}
		if node == nil {
			return nil
		}

		// Serialize the node as JSON
		payload := node.GetPayload()
		content, err := cas.CanonicalJSON(payload)
		if err != nil {
			return err
		}

		// Determine kind from the node
		kind := "node" // Default kind

		objects = append(objects, PackObject{
			Digest:  id,
			Kind:    kind,
			Content: content,
		})

		return nil
	}

	for _, id := range nodeIDs {
		if err := collect(id); err != nil {
			return nil, err
		}
	}

	return objects, nil
}

// EdgeData represents an edge to push to the server.
type EdgeData struct {
	Src  string `json:"src"`  // hex digest
	Type string `json:"type"` // IMPORTS, TESTS, etc.
	Dst  string `json:"dst"`  // hex digest
	At   string `json:"at"`   // hex digest (optional)
}

// PushEdgesResponse is the response from POST /edges.
type PushEdgesResponse struct {
	Inserted int `json:"inserted"`
}

// PushEdges sends edges to the server.
func (c *Client) PushEdges(edges []EdgeData) (*PushEdgesResponse, error) {
	if len(edges) == 0 {
		return &PushEdgesResponse{Inserted: 0}, nil
	}

	req := struct {
		Edges []EdgeData `json:"edges"`
	}{Edges: edges}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.BaseURL+c.repoPath()+"/v1/edges", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Kailab-Actor", c.Actor)
	if c.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result PushEdgesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}
