// Package sshserver provides an SSH entrypoint for Git protocol commands.
package sshserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/gliderlabs/ssh"
)

// GitIO carries the stream handles for Git protocol communication.
type GitIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Handler routes git-upload-pack / git-receive-pack to implementations.
type Handler interface {
	UploadPack(repo string, io GitIO) error
	ReceivePack(repo string, io GitIO) error
}

// Start launches an SSH server that routes Git commands to the handler.
func Start(addr string, handler Handler, logger *log.Logger) (*ssh.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return StartWithListener(listener, handler, logger)
}

// StartWithListener launches an SSH server using a provided listener.
func StartWithListener(listener net.Listener, handler Handler, logger *log.Logger) (*ssh.Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("ssh handler is required")
	}
	if logger == nil {
		logger = log.Default()
	}

	srv := &ssh.Server{
		Addr: listener.Addr().String(),
		// Accept all public keys - actual authorization happens in the handler
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true
		},
		Handler: func(s ssh.Session) {
			start := time.Now()
			raw := s.RawCommand()
			parsed, err := ParseGitCommand(raw)
			cmd := GitCommand{}
			if parsed != nil {
				cmd = *parsed
			}
			if err != nil {
				if auditor, ok := handler.(SessionAuditor); ok {
					auditor.Audit(context.Background(), buildAuditEvent(s, cmd, raw, err, start))
				}
				fmt.Fprintln(s.Stderr(), err.Error())
				_ = s.Exit(1)
				return
			}
			if authorizer, ok := handler.(SessionAuthorizer); ok {
				if err := authorizer.Authorize(context.Background(), s, cmd); err != nil {
					if auditor, ok := handler.(SessionAuditor); ok {
						auditor.Audit(context.Background(), buildAuditEvent(s, cmd, raw, err, start))
					}
					fmt.Fprintln(s.Stderr(), err.Error())
					_ = s.Exit(1)
					return
				}
			}

			gitIO := GitIO{
				Stdin:  s,
				Stdout: s,
				Stderr: s.Stderr(),
			}
			err = HandleCommand(raw, handler, gitIO)
			if auditor, ok := handler.(SessionAuditor); ok {
				auditor.Audit(context.Background(), buildAuditEvent(s, cmd, raw, err, start))
			}
			if err != nil {
				fmt.Fprintln(s.Stderr(), err.Error())
				_ = s.Exit(1)
				return
			}

			_ = s.Exit(0)
		},
	}

	go func() {
		logger.Printf("ssh git server listening on %s", listener.Addr().String())
		if err := srv.Serve(listener); err != nil && err != ssh.ErrServerClosed {
			logger.Printf("ssh server error: %v", err)
		}
	}()

	return srv, nil
}

// Stop shuts down the SSH server.
func Stop(ctx context.Context, srv *ssh.Server) error {
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
