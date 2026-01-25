package sshserver

// ObjectType describes the git object type for pack encoding.
type ObjectType int

const (
	ObjectCommit ObjectType = 1
	ObjectTree   ObjectType = 2
	ObjectBlob   ObjectType = 3
)

// GitObject represents a git object with a precomputed OID.
type GitObject struct {
	Type ObjectType
	Data []byte
	OID  string
}

// GitRef is a git ref name and OID.
type GitRef struct {
	Name string
	OID  string
}

// PackRequest is the upload-pack negotiation request.
type PackRequest struct {
	Wants []string
	Haves []string
	Done  bool
}

// RefCommitInfo bundles a commit object with its dependent objects.
type RefCommitInfo struct {
	Commit  GitObject
	Objects []GitObject
}
