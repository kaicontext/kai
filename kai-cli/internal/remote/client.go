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

// Client communicates with a Kailab server.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Actor      string
}

// NewClient creates a new Kailab client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		Actor: os.Getenv("USER"),
	}
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

	resp, err := c.post("/v1/push/negotiate", body)
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

	req, err := http.NewRequest("POST", c.BaseURL+"/v1/objects/pack", bytes.NewReader(pack))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Kailab-Actor", c.Actor)

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

	httpReq, err := http.NewRequest("PUT", c.BaseURL+"/v1/refs/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Kailab-Actor", c.Actor)

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

// GetRef retrieves a single ref.
func (c *Client) GetRef(name string) (*RefEntry, error) {
	resp, err := c.get("/v1/refs/" + name)
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
	url := "/v1/refs"
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
	resp, err := c.get("/v1/objects/" + digestHex)
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

	return content, kind, nil
}

// GetLogHead returns the current log head.
func (c *Client) GetLogHead() ([]byte, error) {
	resp, err := c.get("/v1/log/head")
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
	url := fmt.Sprintf("/v1/log/entries?after=%d&limit=%d", afterSeq, limit)
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
	return c.HTTPClient.Get(c.BaseURL + path)
}

func (c *Client) post(path string, body []byte) (*http.Response, error) {
	return c.HTTPClient.Post(c.BaseURL+path, "application/json", bytes.NewReader(body))
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

// Config holds remote configuration.
type Config struct {
	Remotes map[string]string `json:"remotes"` // name -> URL
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
		return &Config{Remotes: make(map[string]string)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]string)
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

// GetRemoteURL gets the URL for a named remote.
func GetRemoteURL(name string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}

	url, ok := cfg.Remotes[name]
	if !ok {
		return "", fmt.Errorf("remote %q not configured", name)
	}
	return url, nil
}

// SetRemoteURL sets the URL for a named remote.
func SetRemoteURL(name, url string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	cfg.Remotes[name] = url
	return SaveConfig(cfg)
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
