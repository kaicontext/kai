package sshserver

import (
	"context"
	"log"
)

// LoggerAuditor logs audit events with a standard logger.
type LoggerAuditor struct {
	logger *log.Logger
}

// NewLoggerAuditor creates a logger-backed auditor.
func NewLoggerAuditor(logger *log.Logger) *LoggerAuditor {
	if logger == nil {
		logger = log.Default()
	}
	return &LoggerAuditor{logger: logger}
}

func (a *LoggerAuditor) Audit(ctx context.Context, event AuditEvent) {
	a.logger.Printf(
		"ssh_audit user=%s addr=%s repo=%s cmd=%s ok=%t err=%s key=%s dur=%s",
		event.User,
		event.RemoteAddr,
		event.Repo,
		event.Command,
		event.Success,
		event.Error,
		event.KeyFingerprint,
		event.Duration,
	)
}
