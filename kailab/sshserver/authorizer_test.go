package sshserver

import (
	"bytes"
	"io"
	"net"
	"testing"

	sshlib "github.com/gliderlabs/ssh"
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
}

func (s *fakeSession) User() string                    { return s.user }
func (s *fakeSession) RemoteAddr() net.Addr            { return s.remote }
func (s *fakeSession) LocalAddr() net.Addr             { return s.local }
func (s *fakeSession) Environ() []string               { return nil }
func (s *fakeSession) Exit(int) error                  { return nil }
func (s *fakeSession) Command() []string               { return s.command }
func (s *fakeSession) RawCommand() string              { return s.rawCmd }
func (s *fakeSession) Subsystem() string               { return s.subsystem }
func (s *fakeSession) PublicKey() sshlib.PublicKey     { return nil }
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

	authorizer := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/repo"})
	if err := authorizer.Authorize(nil, session, cmd); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}

	denyUser := NewAllowlistAuthorizer([]string{"bob"}, []string{"org/repo"})
	if err := denyUser.Authorize(nil, session, cmd); err == nil {
		t.Fatalf("expected user deny")
	}

	denyRepo := NewAllowlistAuthorizer([]string{"alice"}, []string{"org/other"})
	if err := denyRepo.Authorize(nil, session, cmd); err == nil {
		t.Fatalf("expected repo deny")
	}
}
