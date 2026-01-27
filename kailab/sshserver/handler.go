package sshserver

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"strings"

	"kailab/metrics"
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
	webhookNotifier    *WebhookNotifier
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
	ControlPlaneURL     string
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
	var notifier *WebhookNotifier
	if opts.ControlPlaneURL != "" {
		notifier = NewWebhookNotifier(opts.ControlPlaneURL)
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
		objectStore:     opts.ObjectStore,
		webhookNotifier: notifier,
	}
}

// UploadPack handles git-upload-pack (fetch/clone).
func (h *GitHandler) UploadPack(repoPath string, io GitIO) error {
	metrics.IncSSHUploadPack()
	tenant, name, err := splitRepo(repoPath)
	if err != nil {
		_ = writeGitError(io.Stdout, "invalid repo path")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}

	handle, err := h.registry.Get(context.Background(), tenant, name)
	if err != nil {
		_ = writeGitError(io.Stdout, "repo lookup failed")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}
	h.registry.Acquire(handle)
	defer h.registry.Release(handle)

	if err := advertiseRefs(handle.DB, io.Stdout, h.capabilities); err != nil {
		h.logger.Printf("upload-pack advertise error: %v", err)
		_ = writeGitError(io.Stdout, "failed to advertise refs")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}

	if err := handleUploadPack(handle.DB, h.objectStore, io.Stdin, io.Stdout); err != nil {
		h.logger.Printf("upload-pack negotiation error: %v", err)
		metrics.IncSSHErrors()
		return err
	}

	return nil
}

// ReceivePack handles git-receive-pack (push).
func (h *GitHandler) ReceivePack(repoPath string, io GitIO) error {
	metrics.IncSSHReceivePack()
	tenant, name, err := splitRepo(repoPath)
	if err != nil {
		_ = writeGitError(io.Stdout, "invalid repo path")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}

	handle, err := h.registry.Get(context.Background(), tenant, name)
	if err != nil {
		_ = writeGitError(io.Stdout, "repo lookup failed")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}
	h.registry.Acquire(handle)
	defer h.registry.Release(handle)

	if h.disableReceivePack || h.readOnly || h.requireSigned {
		err := fmt.Errorf("git receive-pack disabled (Kai-only)")
		_ = writeGitError(io.Stdout, err.Error())
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}

	// Advertise refs before receiving pack data
	if err := advertiseReceivePackRefs(handle.DB, io.Stdout, h.capabilities); err != nil {
		h.logger.Printf("receive-pack advertise error: %v", err)
		_ = writeGitError(io.Stdout, "failed to advertise refs")
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}

	updatedRefs, err := handleReceivePack(handle.DB, io.Stdin, io.Stdout)
	if err != nil {
		_ = writeGitError(io.Stdout, err.Error())
		_ = writeFlush(io.Stdout)
		metrics.IncSSHErrors()
		return err
	}
	if h.mirror != nil && len(updatedRefs) > 0 {
		if err := h.mirror.SyncRefs(context.Background(), handle, updatedRefs); err != nil {
			h.logger.Printf("ssh mirror sync error: %v", err)
		}
	}
	// Trigger webhooks asynchronously
	if h.webhookNotifier != nil && len(updatedRefs) > 0 {
		go h.webhookNotifier.NotifyPush(tenant+"/"+name, updatedRefs)
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

	firstLine, flush, err := readPktLine(reader)
	if err != nil {
		return err
	}
	if flush {
		return writeEmptyPack(w)
	}
	if strings.TrimSpace(firstLine) == "version 2" {
		return handleUploadPackV2(db, store, reader, w)
	}

	req, err := readUploadPackRequestWithFirstLine(firstLine, reader)
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

	refAdapter := NewDBRefAdapter(db)
	refCommits, _, err := refAdapter.BuildRefCommits(context.Background())
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(refCommits))
	for oid := range refCommits {
		known[oid] = true
	}

	if len(req.Deepen) > 0 {
		if err := validateShallowRequest(req); err != nil {
			_ = writeGitError(w, err.Error())
			_ = writeFlush(w)
			return err
		}
		if err := writeShallowLines(w, req.Wants, true); err != nil {
			return err
		}
	}

	if err := writeAcknowledgements(w, req, known); err != nil {
		return err
	}

	packWriter := w
	if enabled, maxData := selectSideBand(req.Caps); enabled {
		packWriter = &sideBandWriter{w: w, maxData: maxData, channelID: 1}
	}

	builder := NewPackBuilder(refAdapter, store)
	if err := builder.BuildPack(context.Background(), PackRequest{
		Wants:    req.Wants,
		Haves:    req.Haves,
		Done:     req.Done,
		ThinPack: hasCapability(req.Caps, "thin-pack"),
		OFSDelta: hasCapability(req.Caps, "ofs-delta"),
		RefDelta: hasCapability(req.Caps, "ref-delta"),
	}, packWriter); err != nil {
		_ = writeGitError(w, err.Error())
		_ = writeFlush(w)
		return err
	}

	// Flush to signal end of pack data
	return writeFlush(w)
}

type uploadPackRequest struct {
	Wants   []string
	Haves   []string
	Shallow []string
	Deepen  []string
	Caps    []string
	Raw     []string
	Done    bool
}

func readUploadPackRequest(r *bufio.Reader) (*uploadPackRequest, error) {
	return readUploadPackRequestWithFirstLine("", r)
}

func readUploadPackRequestWithFirstLine(firstLine string, r *bufio.Reader) (*uploadPackRequest, error) {
	req := &uploadPackRequest{}
	capsParsed := false
	if firstLine != "" {
		line := strings.TrimSpace(firstLine)
		if line != "" {
			req.Raw = append(req.Raw, line)
			if err := parseUploadPackLine(req, line, &capsParsed); err != nil {
				return nil, err
			}
		}
	}
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
		if err := parseUploadPackLine(req, line, &capsParsed); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func parseUploadPackLine(req *uploadPackRequest, line string, capsParsed *bool) error {
	switch {
	case strings.HasPrefix(line, "want "):
		// Client capabilities are space-separated after the OID
		// Format: "want <sha> [cap1 cap2 cap3...]"
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			req.Wants = append(req.Wants, fields[1])
			if !*capsParsed && len(fields) > 2 {
				req.Caps = fields[2:]
				*capsParsed = true
			}
		}
	case line == "done":
		req.Done = true
	case strings.HasPrefix(line, "have "):
		req.Haves = append(req.Haves, strings.TrimSpace(strings.TrimPrefix(line, "have ")))
	case strings.HasPrefix(line, "shallow "):
		req.Shallow = append(req.Shallow, strings.TrimSpace(strings.TrimPrefix(line, "shallow ")))
	case strings.HasPrefix(line, "deepen "):
		req.Deepen = append(req.Deepen, strings.TrimSpace(strings.TrimPrefix(line, "deepen ")))
	case strings.HasPrefix(line, "deepen-since "):
		req.Deepen = append(req.Deepen, strings.TrimSpace(strings.TrimPrefix(line, "deepen-since ")))
	case strings.HasPrefix(line, "deepen-not "):
		req.Deepen = append(req.Deepen, strings.TrimSpace(strings.TrimPrefix(line, "deepen-not ")))
	}
	return nil
}

func handleUploadPackV2(db *sql.DB, store ObjectStore, r *bufio.Reader, w io.Writer) error {
	if db == nil {
		_ = writeGitError(w, "repository not available")
		_ = writeFlush(w)
		return fmt.Errorf("repository not available")
	}

	cmdLine, flush, err := readPktLine(r)
	if err != nil {
		return err
	}
	if flush {
		return nil
	}
	cmdLine = strings.TrimSpace(cmdLine)
	if !strings.HasPrefix(cmdLine, "command=") {
		return fmt.Errorf("invalid v2 command: %s", cmdLine)
	}
	command := strings.TrimPrefix(cmdLine, "command=")
	args := []string{}
	for {
		line, flush, err := readPktLine(r)
		if err != nil {
			return err
		}
		if flush {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		args = append(args, line)
	}

	switch command {
	case "ls-refs":
		return writeV2LsRefs(db, w)
	case "fetch":
		return handleUploadPackV2Fetch(db, store, args, w)
	default:
		return fmt.Errorf("unsupported v2 command: %s", command)
	}
}

func handleUploadPackV2Fetch(db *sql.DB, store ObjectStore, args []string, w io.Writer) error {
	req := parseV2FetchArgs(args)
	if len(req.Wants) == 0 {
		return writeEmptyPack(w)
	}

	if len(req.Deepen) > 0 {
		if err := validateShallowRequest(req); err != nil {
			_ = writeGitError(w, err.Error())
			_ = writeFlush(w)
			return err
		}
		if err := writeShallowLines(w, req.Wants, true); err != nil {
			return err
		}
	}

	if err := writePktLine(w, "packfile\n"); err != nil {
		return err
	}

	refAdapter := NewDBRefAdapter(db)
	builder := NewPackBuilder(refAdapter, store)
	if err := builder.BuildPack(context.Background(), PackRequest{
		Wants: req.Wants,
		Haves: req.Haves,
		Done:  req.Done,
	}, w); err != nil {
		return err
	}
	return writeFlush(w)
}

func parseV2FetchArgs(args []string) *uploadPackRequest {
	req := &uploadPackRequest{}
	for _, line := range args {
		switch {
		case strings.HasPrefix(line, "want "):
			req.Wants = append(req.Wants, strings.TrimSpace(strings.TrimPrefix(line, "want ")))
		case strings.HasPrefix(line, "have "):
			req.Haves = append(req.Haves, strings.TrimSpace(strings.TrimPrefix(line, "have ")))
		case line == "done":
			req.Done = true
		case strings.HasPrefix(line, "deepen "):
			req.Deepen = append(req.Deepen, strings.TrimSpace(strings.TrimPrefix(line, "deepen ")))
		case strings.HasPrefix(line, "shallow "):
			req.Shallow = append(req.Shallow, strings.TrimSpace(strings.TrimPrefix(line, "shallow ")))
		}
	}
	return req
}

func writeV2LsRefs(db *sql.DB, w io.Writer) error {
	refAdapter := NewDBRefAdapter(db)
	refs, _, err := refAdapter.ListRefs(context.Background())
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err := writePktLine(w, ref.OID+" "+ref.Name+"\n"); err != nil {
			return err
		}
	}
	return writeFlush(w)
}

func writeAcknowledgements(w io.Writer, req *uploadPackRequest, known map[string]bool) error {
	if len(req.Haves) == 0 || len(known) == 0 {
		return writePktLine(w, "NAK\n")
	}

	var ack string
	for _, have := range req.Haves {
		if known[have] {
			ack = have
		}
	}
	if ack == "" {
		return writePktLine(w, "NAK\n")
	}
	return writePktLine(w, "ACK "+ack+"\n")
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

func advertiseReceivePackRefs(db *sql.DB, w io.Writer, capsConfig CapabilitiesConfig) error {
	refAdapter := NewDBRefAdapter(db)
	refs, _, err := refAdapter.ListRefs(context.Background())
	if err != nil {
		return err
	}

	// receive-pack capabilities (subset of upload-pack caps)
	caps := buildReceivePackCapabilities(capsConfig)

	if len(refs) == 0 {
		// Empty repo: send zero-id with capabilities
		line := fmt.Sprintf("%s capabilities^{}\x00%s\n", zeroOID, caps)
		if err := writePktLine(w, line); err != nil {
			return err
		}
		return writeFlush(w)
	}

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

const zeroOID = "0000000000000000000000000000000000000000"

func buildReceivePackCapabilities(cfg CapabilitiesConfig) string {
	var caps []string
	agent := cfg.Agent
	if agent == "" {
		agent = "kai"
	}
	caps = append(caps, "report-status", "delete-refs", "agent="+agent)
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

// MapGitRefName maps a Git ref name to its Kai ref name.
func MapGitRefName(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "refs/heads/"):
		trimmed := strings.TrimPrefix(name, "refs/heads/")
		if trimmed == "" {
			return "", false
		}
		return "snap." + trimmed, true
	case strings.HasPrefix(name, "refs/tags/"):
		trimmed := strings.TrimPrefix(name, "refs/tags/")
		if trimmed == "" {
			return "", false
		}
		return "tag." + trimmed, true
	case strings.HasPrefix(name, "refs/kai/cs/"):
		trimmed := strings.TrimPrefix(name, "refs/kai/cs/")
		if trimmed == "" {
			return "", false
		}
		return "cs." + trimmed, true
	case strings.HasPrefix(name, "refs/kai/"):
		trimmed := strings.TrimPrefix(name, "refs/kai/")
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	default:
		return "", false
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
	caps = append(caps, "agent="+agent, "side-band-64k", "shallow")
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

func parseCaps(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
}

func writeShallowLines(w io.Writer, wants []string, flush bool) error {
	sent := false
	for _, want := range wants {
		if want == "" {
			continue
		}
		if err := writePktLine(w, "shallow "+want+"\n"); err != nil {
			return err
		}
		sent = true
	}
	if sent && flush {
		return writeFlush(w)
	}
	return nil
}

func selectSideBand(caps []string) (bool, int) {
	if hasCapability(caps, "side-band-64k") {
		return true, 65515
	}
	if hasCapability(caps, "side-band") {
		return true, 995
	}
	return false, 0
}

func hasCapability(caps []string, name string) bool {
	for _, cap := range caps {
		if cap == name {
			return true
		}
	}
	return false
}

func validateShallowRequest(req *uploadPackRequest) error {
	if len(req.Deepen) == 0 {
		return nil
	}
	if len(req.Deepen) > 1 {
		return fmt.Errorf("multiple deepen requests not supported")
	}
	if req.Deepen[0] != "1" {
		return fmt.Errorf("only deepen=1 supported")
	}
	return nil
}
