package sshserver

import (
	"bytes"
	"crypto/ed25519"
	"io"
	"net"
	"testing"

	sshlib "github.com/gliderlabs/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

type fakeChannel struct {
	buf    bytes.Buffer
	stderr bytes.Buffer
}

func (c *fakeChannel) Read(p []byte) (int, error)  { return c.buf.Read(p) }
func (c *fakeChannel) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *fakeChannel) Close() error                { return nil }
func (c *fakeChannel) CloseWrite() error           { return nil }
func (c *fakeChannel) SendRequest(string, bool, []byte) (bool, error) {
	return true, nil
}
func (c *fakeChannel) Stderr() io.ReadWriter { return &c.stderr }

type fakeSession struct {
	*fakeChannel
	user      string
	remote    net.Addr
	local     net.Addr
	rawCmd    string
	command   []string
	subsystem string
	pubKey    sshlib.PublicKey
}

func (s *fakeSession) User() string                    { return s.user }
func (s *fakeSession) RemoteAddr() net.Addr            { return s.remote }
func (s *fakeSession) LocalAddr() net.Addr             { return s.local }
func (s *fakeSession) Environ() []string               { return nil }
func (s *fakeSession) Exit(int) error                  { return nil }
func (s *fakeSession) Command() []string               { return s.command }
func (s *fakeSession) RawCommand() string              { return s.rawCmd }
func (s *fakeSession) Subsystem() string               { return s.subsystem }
func (s *fakeSession) PublicKey() sshlib.PublicKey     { return s.pubKey }
func (s *fakeSession) Context() sshlib.Context         { return nil }
func (s *fakeSession) Permissions() sshlib.Permissions { return sshlib.Permissions{} }
func (s *fakeSession) Pty() (sshlib.Pty, <-chan sshlib.Window, bool) {
	return sshlib.Pty{}, nil, false
}
func (s *fakeSession) Signals(chan<- sshlib.Signal) {}
func (s *fakeSession) Break(chan<- bool)            {}

func TestAllowlistAuthorizer(t *testing.T) {
	session := &fakeSession{
		fakeChannel: &fakeChannel{},
		user:        "alice",
		remote:      &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222},
		local:       &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22},
	}
	cmd := GitCommand{Repo: "org/repo"}

	authorizer := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/repo"}, nil)
	if err := authorizer.Authorize(nil, session, cmd); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}

	denyUser := NewAllowlistAuthorizer([]string{"bob"}, []string{"org/repo"}, nil)
	if err := denyUser.Authorize(nil, session, cmd); err == nil {
		t.Fatalf("expected user deny")
	}

	denyRepo := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/other"}, nil)
	if err := denyRepo.Authorize(nil, session, cmd); err == nil {
		t.Fatalf("expected repo deny")
	}
}

func TestAllowlistAuthorizerKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshKey, err := cryptossh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatalf("ssh key: %v", err)
	}
	fingerprint := cryptossh.FingerprintSHA256(sshKey)

	session := &fakeSession{
		fakeChannel: &fakeChannel{},
		user:        "alice",
		remote:      &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222},
		local:       &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22},
		pubKey:      sshKey,
	}
	cmd := GitCommand{Repo: "org/repo"}

	allow := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/repo"}, []string{fingerprint})
	if err := allow.Authorize(nil, session, cmd); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}

	deny := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/repo"}, []string{"SHA256:missing"})
	if err := deny.Authorize(nil, session, cmd); err == nil {
		t.Fatalf("expected key deny")
	}
}
