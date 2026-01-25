package sshserver

import (
	"bufio"
	"bytes"
	"errors"
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
	buf := bytes.NewBufferString("003fwant 0123456789012345678901234567890123456789\x00agent=git/2.0" +
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

	if err := handleUploadPack(nil, buf, out); err == nil {
		t.Fatal("expected error for unimplemented pack")
	}
	if out.Len() == 0 {
		t.Fatal("expected error response output")
	}
}

func TestWriteAcknowledgements(t *testing.T) {
	out := &bytes.Buffer{}
	req := &uploadPackRequest{
		Haves: []string{"a1", "b2"},
	}
	if err := writeAcknowledgements(out, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.String() != "000bACK b2\n" {
		t.Fatalf("unexpected ack: %q", out.String())
	}
}
