package sshserver

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

type stubHandler struct {
	lastRepo string
	lastType GitCommandType
}

func (h *stubHandler) UploadPack(repo string, io GitIO) error {
	h.lastRepo = repo
	h.lastType = GitUploadPack
	_, _ = io.Stdout.Write([]byte("ok"))
	return nil
}

func (h *stubHandler) ReceivePack(repo string, io GitIO) error {
	h.lastRepo = repo
	h.lastType = GitReceivePack
	return errors.New("not implemented")
}

func TestParseGitCommand_UploadPack(t *testing.T) {
	cmd, err := ParseGitCommand("git-upload-pack '/org/repo'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != GitUploadPack {
		t.Fatalf("expected upload-pack, got %s", cmd.Type)
	}
	if cmd.Repo != "org/repo" {
		t.Fatalf("expected repo org/repo, got %q", cmd.Repo)
	}
}

func TestParseGitCommand_ReceivePack(t *testing.T) {
	cmd, err := ParseGitCommand(`git-receive-pack "org/repo.git"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != GitReceivePack {
		t.Fatalf("expected receive-pack, got %s", cmd.Type)
	}
	if cmd.Repo != "org/repo.git" {
		t.Fatalf("expected repo org/repo.git, got %q", cmd.Repo)
	}
}

func TestParseGitCommand_Invalid(t *testing.T) {
	_, err := ParseGitCommand("git-unknown-pack /org/repo")
	if err == nil {
		t.Fatal("expected error for unsupported command")
	}
}

func TestHandleCommand_RoutesToHandler(t *testing.T) {
	handler := &stubHandler{}
	out := &bytes.Buffer{}
	err := HandleCommand("git-upload-pack '/org/repo'", handler, GitIO{Stdout: out})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handler.lastRepo != "org/repo" || handler.lastType != GitUploadPack {
		t.Fatalf("expected upload-pack to org/repo, got %s %q", handler.lastType, handler.lastRepo)
	}
	if out.String() != "ok" {
		t.Fatalf("expected stdout to be 'ok', got %q", out.String())
	}
}

func TestSplitRepo(t *testing.T) {
	tenant, name, err := splitRepo("org/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "org" || name != "repo" {
		t.Fatalf("expected org/repo, got %s/%s", tenant, name)
	}
}

func TestWriteGitError(t *testing.T) {
	out := &bytes.Buffer{}
	if err := writeGitError(out, "boom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	expected := "000cERR boom"
	if got != expected {
		t.Fatalf("unexpected pkt-line: got %q want %q", got, expected)
	}
}

func TestReadUploadPackRequestCapsShallow(t *testing.T) {
	var buf bytes.Buffer
	// Git protocol uses space-separated capabilities after the OID
	if err := writePktLine(&buf, "want deadbeefdeadbeefdeadbeefdeadbeefdeadbeef symref=HEAD:refs/heads/main side-band-64k\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writePktLine(&buf, "shallow feedfacefeedfacefeedfacefeedfacefeedface\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writeFlush(&buf); err != nil {
		t.Fatalf("write flush: %v", err)
	}
	reader := bufio.NewReader(&buf)

	req, err := readUploadPackRequest(reader)
	if err != nil {
		t.Fatalf("read upload-pack: %v", err)
	}
	if len(req.Wants) != 1 {
		t.Fatalf("expected 1 want, got %d", len(req.Wants))
	}
	if len(req.Caps) != 2 {
		t.Fatalf("expected 2 caps, got %d: %v", len(req.Caps), req.Caps)
	}
	if len(req.Shallow) != 1 {
		t.Fatalf("expected 1 shallow, got %d", len(req.Shallow))
	}
}

func TestMapRefName(t *testing.T) {
	if got := MapRefName("snap.main"); got != "refs/heads/main" {
		t.Fatalf("unexpected snap mapping: %s", got)
	}
	if got := MapRefName("cs.latest"); got != "refs/kai/cs/latest" {
		t.Fatalf("unexpected cs mapping: %s", got)
	}
	if got := MapRefName("tag.v1.0.0"); got != "refs/tags/v1.0.0" {
		t.Fatalf("unexpected tag mapping: %s", got)
	}
	if got := MapRefName("other.ref"); got != "refs/kai/other.ref" {
		t.Fatalf("unexpected default mapping: %s", got)
	}
}

func TestMapGitRefName(t *testing.T) {
	tests := []struct {
		input   string
		expect  string
		allowed bool
	}{
		{"refs/heads/main", "snap.main", true},
		{"refs/tags/v1.2.3", "tag.v1.2.3", true},
		{"refs/kai/cs/latest", "cs.latest", true},
		{"refs/kai/other.ref", "other.ref", true},
		{"refs/notes/commits", "", false},
		{"refs/heads/", "", false},
	}

	for _, tt := range tests {
		got, ok := MapGitRefName(tt.input)
		if ok != tt.allowed {
			t.Fatalf("expected allowed=%v for %s, got %v", tt.allowed, tt.input, ok)
		}
		if tt.allowed && got != tt.expect {
			t.Fatalf("expected %s, got %s for %s", tt.expect, got, tt.input)
		}
	}
}

func TestBuildCommitObject(t *testing.T) {
	commit := buildCommitObject("refs/heads/main", "deadbeef", emptyTreeOID)
	if len(commit.OID) != 40 {
		t.Fatalf("expected 40-hex oid, got %q", commit.OID)
	}
	if commit.Type != ObjectCommit {
		t.Fatalf("expected commit type")
	}
	if !bytes.Contains(commit.Data, []byte("tree "+emptyTreeOID)) {
		t.Fatalf("expected empty tree reference in commit")
	}
}

func TestBuildCapabilities(t *testing.T) {
	cfg := CapabilitiesConfig{
		Agent:   "kai-test",
		Extra:   []string{"thin-pack"},
		Disable: []string{"report-status"},
	}
	got := buildCapabilities("refs/heads/main", cfg)
	if !strings.Contains(got, "symref=HEAD:refs/heads/main") {
		t.Fatalf("missing symref: %s", got)
	}
	if !strings.Contains(got, "agent=kai-test") {
		t.Fatalf("missing agent: %s", got)
	}
	if !strings.Contains(got, "side-band-64k") {
		t.Fatalf("missing side-band-64k: %s", got)
	}
	if !strings.Contains(got, "shallow") {
		t.Fatalf("missing shallow: %s", got)
	}
	if !strings.Contains(got, "thin-pack") {
		t.Fatalf("missing extra capability: %s", got)
	}
	if strings.Contains(got, "report-status") {
		t.Fatalf("expected report-status to be disabled: %s", got)
	}
}

func TestWriteShallowLines(t *testing.T) {
	var buf bytes.Buffer
	if err := writeShallowLines(&buf, []string{"deadbeef"}, true); err != nil {
		t.Fatalf("write shallow: %v", err)
	}
	if got := buf.String(); got != "0015shallow deadbeef\n0000" {
		t.Fatalf("unexpected shallow pkt: %q", got)
	}
}

func TestValidateShallowRequest(t *testing.T) {
	req := &uploadPackRequest{Deepen: []string{"2"}}
	if err := validateShallowRequest(req); err == nil {
		t.Fatalf("expected error for deepen=2")
	}
	req = &uploadPackRequest{Deepen: []string{"1"}}
	if err := validateShallowRequest(req); err != nil {
		t.Fatalf("unexpected error for deepen=1: %v", err)
	}
}

func TestWriteAcknowledgements(t *testing.T) {
	req := &uploadPackRequest{
		Haves: []string{"aaaa", "bbbb"},
	}
	known := map[string]bool{
		"bbbb": true,
	}
	var buf bytes.Buffer
	if err := writeAcknowledgements(&buf, req, known); err != nil {
		t.Fatalf("write ack: %v", err)
	}
	if got := buf.String(); got != "000dACK bbbb\n" {
		t.Fatalf("unexpected ack pkt: %q", got)
	}

	buf.Reset()
	if err := writeAcknowledgements(&buf, req, map[string]bool{}); err != nil {
		t.Fatalf("write nak: %v", err)
	}
	if got := buf.String(); got != "0008NAK\n" {
		t.Fatalf("unexpected nak pkt: %q", got)
	}
}

func TestReadPktLine(t *testing.T) {
	buf := bytes.NewBufferString("0009hello0000")
	r := bufio.NewReader(buf)

	line, flush, err := readPktLine(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flush || line != "hello" {
		t.Fatalf("unexpected line: %q flush=%v", line, flush)
	}

	_, flush, err = readPktLine(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flush {
		t.Fatalf("expected flush pkt-line")
	}
}

func TestReadPktLineDelimiter(t *testing.T) {
	buf := bytes.NewBufferString("0001")
	r := bufio.NewReader(buf)
	_, flush, err := readPktLine(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flush {
		t.Fatalf("expected delimiter to be treated as flush")
	}
}

func TestWriteEmptyPack(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := writeEmptyPack(buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() != 12+20 {
		t.Fatalf("expected 32 bytes, got %d", buf.Len())
	}
	if string(buf.Bytes()[:4]) != "PACK" {
		t.Fatalf("expected PACK header")
	}
}

func TestReadUploadPackRequest(t *testing.T) {
	// Git protocol uses space-separated capabilities after the OID
	// pkt-line length includes the 4-byte length prefix
	// "want " (5) + sha (40) + " agent=git/2.0\n" (15) = 60 + 4 = 64 = 0x40
	buf := bytes.NewBufferString("0040want 0123456789012345678901234567890123456789 agent=git/2.0\n" +
		"000fhave abcdef" +
		"0008done" +
		"0000")
	reader := bufio.NewReader(buf)

	req, err := readUploadPackRequest(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Wants) != 1 || req.Wants[0] != "0123456789012345678901234567890123456789" {
		t.Fatalf("unexpected wants: %v", req.Wants)
	}
	if len(req.Haves) != 1 || req.Haves[0] != "abcdef" {
		t.Fatalf("unexpected haves: %v", req.Haves)
	}
	if len(req.Raw) != 3 {
		t.Fatalf("unexpected raw count: %d", len(req.Raw))
	}
	if !req.Done {
		t.Fatalf("expected done flag to be set")
	}
}

func TestHandleUploadPack_RejectsWants(t *testing.T) {
	buf := bytes.NewBufferString("0032want 0123456789012345678901234567890123456789\n0000")
	out := &bytes.Buffer{}

	if err := handleUploadPack(nil, nil, buf, out); err == nil {
		t.Fatal("expected error for unimplemented pack")
	}
	if out.Len() == 0 {
		t.Fatal("expected error response output")
	}
}

func TestParseGitProtocolVersion(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		expect  int
	}{
		{
			name:    "no protocol",
			environ: []string{},
			expect:  0,
		},
		{
			name:    "version 2",
			environ: []string{"GIT_PROTOCOL=version=2"},
			expect:  2,
		},
		{
			name:    "version 2 with other vars",
			environ: []string{"HOME=/home/user", "GIT_PROTOCOL=version=2", "SHELL=/bin/bash"},
			expect:  2,
		},
		{
			name:    "version 2 colon-separated",
			environ: []string{"GIT_PROTOCOL=version=2:shallow"},
			expect:  2,
		},
		{
			name:    "other version",
			environ: []string{"GIT_PROTOCOL=version=1"},
			expect:  0,
		},
		{
			name:    "partial match not version 2",
			environ: []string{"GIT_PROTOCOL=foo:bar"},
			expect:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitProtocolVersion(tt.environ)
			if got != tt.expect {
				t.Errorf("parseGitProtocolVersion(%v) = %d, want %d", tt.environ, got, tt.expect)
			}
		})
	}
}

func TestAdvertiseV2Capabilities(t *testing.T) {
	var buf bytes.Buffer
	cfg := CapabilitiesConfig{Agent: "kai-test"}
	if err := advertiseV2Capabilities(&buf, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "version 2") {
		t.Errorf("missing version 2 in output: %s", output)
	}
	if !strings.Contains(output, "agent=kai-test") {
		t.Errorf("missing agent in output: %s", output)
	}
	if !strings.Contains(output, "ls-refs") {
		t.Errorf("missing ls-refs capability: %s", output)
	}
	if !strings.Contains(output, "fetch=shallow") {
		t.Errorf("missing fetch capability: %s", output)
	}
}
