package sshserver

import (
	"context"

	"github.com/gliderlabs/ssh"
)

// HandlerWithHooks decorates a Handler with optional authz/audit hooks.
type HandlerWithHooks struct {
	handler    Handler
	authorizer SessionAuthorizer
	auditor    SessionAuditor
}

// WrapHandler adds optional authz/audit hooks to a Handler.
func WrapHandler(handler Handler, authorizer SessionAuthorizer, auditor SessionAuditor) *HandlerWithHooks {
	return &HandlerWithHooks{
		handler:    handler,
		authorizer: authorizer,
		auditor:    auditor,
	}
}

func (h *HandlerWithHooks) UploadPack(repo string, io GitIO) error {
	return h.handler.UploadPack(repo, io)
}

func (h *HandlerWithHooks) ReceivePack(repo string, io GitIO) error {
	return h.handler.ReceivePack(repo, io)
}

func (h *HandlerWithHooks) Authorize(ctx context.Context, session ssh.Session, cmd GitCommand) error {
	if h.authorizer == nil {
		return nil
	}
	return h.authorizer.Authorize(ctx, session, cmd)
}

func (h *HandlerWithHooks) Audit(ctx context.Context, event AuditEvent) {
	if h.auditor == nil {
		return
	}
	h.auditor.Audit(ctx, event)
}
