package sshserver

import (
	"context"
	"time"

	sshlib "github.com/gliderlabs/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

// SessionAuthorizer enforces access control on git operations.
type SessionAuthorizer interface {
	Authorize(ctx context.Context, session sshlib.Session, cmd GitCommand) error
}

// SessionAuditor records access attempts for git operations.
type SessionAuditor interface {
	Audit(ctx context.Context, event AuditEvent)
}

// AuditEvent captures a single SSH git operation.
type AuditEvent struct {
	Time           time.Time
	Duration       time.Duration
	User           string
	RemoteAddr     string
	Command        GitCommandType
	Repo           string
	RawCommand     string
	Success        bool
	Error          string
	KeyFingerprint string
}

func buildAuditEvent(session sshlib.Session, cmd GitCommand, raw string, err error, start time.Time) AuditEvent {
	event := AuditEvent{
		Time:       start,
		Duration:   time.Since(start),
		User:       session.User(),
		RemoteAddr: session.RemoteAddr().String(),
		Command:    cmd.Type,
		Repo:       cmd.Repo,
		RawCommand: raw,
		Success:    err == nil,
	}
	if err != nil {
		event.Error = err.Error()
	}
	if key := session.PublicKey(); key != nil {
		event.KeyFingerprint = cryptossh.FingerprintSHA256(key)
	}
	return event
}
