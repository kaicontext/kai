// Package proto defines wire format DTOs for the Kailab HTTP API.
package proto

// NegotiateRequest is sent by the client to negotiate which objects need to be pushed.
type NegotiateRequest struct {
	// Bloom is a Bloom filter of client object digests (optional, for optimization).
	Bloom []byte `json:"bloom,omitempty"`
	// Digests is a list of object digests the client has.
	Digests [][]byte `json:"digests,omitempty"`
}

// NegotiateResponse tells the client which objects are missing on the server.
type NegotiateResponse struct {
	// Missing is the list of digests the server doesn't have.
	Missing [][]byte `json:"missing"`
}

// PackIngestResponse is returned after successfully ingesting a pack.
type PackIngestResponse struct {
	// SegmentID is the server-assigned segment ID.
	SegmentID int64 `json:"segmentId"`
	// Indexed is the count of objects indexed from the pack.
	Indexed int `json:"indexedCount"`
}

// RefUpdateRequest is sent to create or update a ref.
type RefUpdateRequest struct {
	// Old is the expected current target (nil for create, must match for update).
	Old []byte `json:"old,omitempty"`
	// New is the new target.
	New []byte `json:"new"`
	// Force allows non-fast-forward updates.
	Force bool `json:"force,omitempty"`
}

// RefUpdateResponse is returned after updating a ref.
type RefUpdateResponse struct {
	// OK indicates success.
	OK bool `json:"ok"`
	// UpdatedAt is the timestamp of the update.
	UpdatedAt int64 `json:"updatedAt"`
	// PushID is the unique push identifier.
	PushID string `json:"pushId"`
	// Error message if not OK.
	Error string `json:"error,omitempty"`
}

// BatchRefUpdate represents a single ref update in a batch.
type BatchRefUpdate struct {
	Name  string `json:"name"`
	Old   []byte `json:"old,omitempty"`
	New   []byte `json:"new"`
	Force bool   `json:"force,omitempty"`
}

// BatchRefUpdateRequest is sent to update multiple refs atomically.
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

// RefEntry represents a single ref in list responses.
type RefEntry struct {
	Name      string `json:"name"`
	Target    []byte `json:"target"`
	UpdatedAt int64  `json:"updatedAt"`
	Actor     string `json:"actor"`
}

// RefsListResponse contains a list of refs.
type RefsListResponse struct {
	Refs []*RefEntry `json:"refs"`
}

// LogEntry represents a single append-only log entry.
type LogEntry struct {
	// Kind is the entry type: "REF_UPDATE" or "NODE_PUBLISH".
	Kind string `json:"kind"`
	// ID is the content-addressed ID of this entry (blake3 of canonical JSON).
	ID []byte `json:"id"`
	// Parent is the previous entry's ID (hash chain).
	Parent []byte `json:"parent,omitempty"`
	// Time is Unix milliseconds.
	Time int64 `json:"time"`
	// Actor is who made the change.
	Actor string `json:"actor"`
	// Ref is the ref name (for REF_UPDATE).
	Ref string `json:"ref,omitempty"`
	// Old is the previous target (for REF_UPDATE).
	Old []byte `json:"old,omitempty"`
	// New is the new target (for REF_UPDATE).
	New []byte `json:"new,omitempty"`
	// NodeID is the published node ID (for NODE_PUBLISH).
	NodeID []byte `json:"nodeId,omitempty"`
	// NodeKind is the kind of node published (for NODE_PUBLISH).
	NodeKind string `json:"nodeKind,omitempty"`
	// Meta is additional JSON metadata.
	Meta map[string]string `json:"meta,omitempty"`
}

// LogHeadResponse returns the current log head.
type LogHeadResponse struct {
	Head []byte `json:"head"`
}

// LogEntriesResponse contains log entries.
type LogEntriesResponse struct {
	Entries []*LogEntry `json:"entries"`
}

// ObjectGetResponse wraps raw object content for JSON responses.
type ObjectGetResponse struct {
	Digest  []byte `json:"digest"`
	Kind    string `json:"kind"`
	Content []byte `json:"content"`
}

// PackHeader describes objects in a pack segment.
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

// ErrorResponse is returned for API errors.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// HealthResponse is returned by the health endpoint.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ----- Admin API -----

// CreateRepoRequest is sent to create a new repo.
type CreateRepoRequest struct {
	Tenant string `json:"tenant"`
	Repo   string `json:"repo"`
}

// CreateRepoResponse is returned after creating a repo.
type CreateRepoResponse struct {
	Tenant string `json:"tenant"`
	Repo   string `json:"repo"`
}

// RepoInfo describes a single repo.
type RepoInfo struct {
	Tenant string `json:"tenant"`
	Repo   string `json:"repo"`
}

// ListReposResponse contains a list of repos.
type ListReposResponse struct {
	Repos []RepoInfo `json:"repos"`
}
