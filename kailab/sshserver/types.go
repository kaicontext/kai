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
	Wants    []string
	Haves    []string
	Done     bool
	ThinPack bool
	OFSDelta bool
	RefDelta bool
	Depth    int // 0 = unlimited, 1 = shallow (just tip commit), etc.
}

// PackResult contains the pack data and metadata about shallow boundaries.
type PackResult struct {
	ShallowCommits []string // Commits that are shallow boundaries (parents not included)
}

// RefCommitInfo bundles a commit object with its dependent objects.
type RefCommitInfo struct {
	Commit  GitObject
	Objects []GitObject
}
