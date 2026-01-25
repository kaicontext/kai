package sshserver

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"strings"

	"kailab/repo"
	"kailab/store"
)

// GitHandler routes Git protocol calls to repo-backed implementations.
type GitHandler struct {
	registry           *repo.Registry
	logger             *log.Logger
	mirror             *GitMirror
	readOnly           bool
	requireSigned      bool
	disableReceivePack bool
	capabilities       CapabilitiesConfig
	objectStore        ObjectStore
}

// GitHandlerOptions configure Git handler behavior.
type GitHandlerOptions struct {
	Mirror              *GitMirror
	ReadOnly            bool
	RequireSigned       bool
	DisableReceivePack  bool
	CapabilitiesExtra   []string
	CapabilitiesDisable []string
	Agent               string
	ObjectStore         ObjectStore
}

// CapabilitiesConfig controls advertised Git capabilities.
type CapabilitiesConfig struct {
	Agent   string
	Extra   []string
	Disable []string
}

// NewGitHandler creates a handler wired with the repo registry.
func NewGitHandler(registry *repo.Registry, logger *log.Logger, opts GitHandlerOptions) *GitHandler {
	if logger == nil {
		logger = log.Default()
	}
	return &GitHandler{
		registry:           registry,
		logger:             logger,
		mirror:             opts.Mirror,
		readOnly:           opts.ReadOnly,
		requireSigned:      opts.RequireSigned,
		disableReceivePack: opts.DisableReceivePack,
		capabilities: CapabilitiesConfig{
			Agent:   opts.Agent,
			Extra:   opts.CapabilitiesExtra,
			Disable: opts.CapabilitiesDisable,
		},
		objectStore: opts.ObjectStore,
	}
}

// UploadPack handles git-upload-pack (fetch/clone).
func (h *GitHandler) UploadPack(repoPath string, io GitIO) error {
	tenant, name, err := splitRepo(repoPath)
	if err != nil {
		_ = writeGitError(io.Stdout, "invalid repo path")
		_ = writeFlush(io.Stdout)
		return err
	}

	handle, err := h.registry.Get(context.Background(), tenant, name)
	if err != nil {
		_ = writeGitError(io.Stdout, "repo lookup failed")
		_ = writeFlush(io.Stdout)
		return err
	}
	h.registry.Acquire(handle)
	defer h.registry.Release(handle)

	if err := advertiseRefs(handle.DB, io.Stdout, h.capabilities); err != nil {
		h.logger.Printf("upload-pack advertise error: %v", err)
		_ = writeGitError(io.Stdout, "failed to advertise refs")
		_ = writeFlush(io.Stdout)
		return err
	}

	if err := handleUploadPack(handle.DB, h.objectStore, io.Stdin, io.Stdout); err != nil {
		h.logger.Printf("upload-pack negotiation error: %v", err)
		return err
	}

	return nil
}

// ReceivePack handles git-receive-pack (push).
func (h *GitHandler) ReceivePack(repoPath string, io GitIO) error {
	tenant, name, err := splitRepo(repoPath)
	if err != nil {
		_ = writeGitError(io.Stdout, "invalid repo path")
		_ = writeFlush(io.Stdout)
		return err
	}

	handle, err := h.registry.Get(context.Background(), tenant, name)
	if err != nil {
		_ = writeGitError(io.Stdout, "repo lookup failed")
		_ = writeFlush(io.Stdout)
		return err
	}
	h.registry.Acquire(handle)
	defer h.registry.Release(handle)

	if h.disableReceivePack || h.readOnly || h.requireSigned {
		err := fmt.Errorf("git receive-pack disabled (Kai-only)")
		_ = writeGitError(io.Stdout, err.Error())
		_ = writeFlush(io.Stdout)
		return err
	}

	updatedRefs, err := handleReceivePack(handle.DB, io.Stdin, io.Stdout)
	if err != nil {
		_ = writeGitError(io.Stdout, err.Error())
		_ = writeFlush(io.Stdout)
		return err
	}
	if h.mirror != nil && len(updatedRefs) > 0 {
		if err := h.mirror.SyncRefs(context.Background(), handle, updatedRefs); err != nil {
			h.logger.Printf("ssh mirror sync error: %v", err)
		}
	}
	return nil
}

func splitRepo(repoPath string) (tenant string, name string, err error) {
	trimmed := strings.TrimPrefix(repoPath, "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("repo path must be tenant/repo")
	}

	tenant = parts[0]
	name = strings.Join(parts[1:], "/")
	if tenant == "" || name == "" {
		return "", "", fmt.Errorf("repo path must be tenant/repo")
	}
	return tenant, name, nil
}

func handleUploadPack(db *sql.DB, store ObjectStore, r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)

	req, err := readUploadPackRequest(reader)
	if err != nil {
		return err
	}

	if len(req.Wants) == 0 {
		return writeEmptyPack(w)
	}

	if db == nil {
		_ = writeGitError(w, "repository not available")
		_ = writeFlush(w)
		return fmt.Errorf("repository not available")
	}

	if err := writeAcknowledgements(w, req); err != nil {
		return err
	}

	builder := NewPackBuilder(NewDBRefAdapter(db), store)
	if err := builder.BuildPack(context.Background(), PackRequest{
		Wants: req.Wants,
		Haves: req.Haves,
		Done:  req.Done,
	}, w); err != nil {
		_ = writeGitError(w, err.Error())
		_ = writeFlush(w)
		return err
	}

	return nil
}

type uploadPackRequest struct {
	Wants []string
	Haves []string
	Raw   []string
	Done  bool
}

func readUploadPackRequest(r *bufio.Reader) (*uploadPackRequest, error) {
	req := &uploadPackRequest{}
	for {
		line, flush, err := readPktLine(r)
		if err != nil {
			return nil, err
		}
		if flush {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		req.Raw = append(req.Raw, line)
		switch {
		case strings.HasPrefix(line, "want "):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				want := fields[1]
				if idx := strings.IndexByte(want, 0); idx >= 0 {
					want = want[:idx]
				}
				req.Wants = append(req.Wants, want)
			}
		case line == "done":
			req.Done = true
		case strings.HasPrefix(line, "have "):
			req.Haves = append(req.Haves, strings.TrimSpace(strings.TrimPrefix(line, "have ")))
		}
	}
	return req, nil
}

func writeAcknowledgements(w io.Writer, req *uploadPackRequest) error {
	if len(req.Haves) == 0 {
		return writePktLine(w, "NAK\n")
	}

	last := req.Haves[len(req.Haves)-1]
	return writePktLine(w, "ACK "+last+"\n")
}

func advertiseRefs(db *sql.DB, w io.Writer, capsConfig CapabilitiesConfig) error {
	refAdapter := NewDBRefAdapter(db)
	refs, headRef, err := refAdapter.ListRefs(context.Background())
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return writeFlush(w)
	}

	caps := buildCapabilities(headRef, capsConfig)

	for i, ref := range refs {
		line := fmt.Sprintf("%s %s", ref.OID, ref.Name)
		if i == 0 && caps != "" {
			line += "\x00" + caps
		}
		line += "\n"
		if err := writePktLine(w, line); err != nil {
			return err
		}
	}

	return writeFlush(w)
}

// MapRefName maps a Kai ref name to its Git ref name.
func MapRefName(name string) string {
	switch {
	case strings.HasPrefix(name, "snap."):
		return "refs/heads/" + strings.TrimPrefix(name, "snap.")
	case strings.HasPrefix(name, "ws."):
		return "refs/heads/" + strings.TrimPrefix(name, "ws.")
	case strings.HasPrefix(name, "cs."):
		return "refs/kai/cs/" + strings.TrimPrefix(name, "cs.")
	case strings.HasPrefix(name, "tag."):
		return "refs/tags/" + strings.TrimPrefix(name, "tag.")
	default:
		return "refs/kai/" + name
	}
}

func selectHeadRef(refs []*store.Ref) string {
	for _, ref := range refs {
		if strings.HasPrefix(ref.Name, "refs/heads/") {
			return ref.Name
		}
	}
	if len(refs) > 0 {
		return refs[0].Name
	}
	return ""
}

func buildCapabilities(headRef string, cfg CapabilitiesConfig) string {
	var caps []string
	if headRef != "" {
		caps = append(caps, "symref=HEAD:"+headRef)
	}
	agent := cfg.Agent
	if agent == "" {
		agent = "kai"
	}
	caps = append(caps,
		"agent="+agent,
		"side-band-64k",
		"report-status",
	)
	caps = append(caps, cfg.Extra...)
	if len(cfg.Disable) > 0 {
		disabled := make(map[string]bool, len(cfg.Disable))
		for _, name := range cfg.Disable {
			disabled[name] = true
		}
		filtered := caps[:0]
		for _, name := range caps {
			if !disabled[name] {
				filtered = append(filtered, name)
			}
		}
		caps = filtered
	}
	seen := make(map[string]bool, len(caps))
	out := make([]string, 0, len(caps))
	for _, name := range caps {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return strings.Join(out, " ")
}
