// Package sshserver provides SSH command parsing for Git protocol entrypoints.
package sshserver

import (
	"errors"
	"strings"
)

// GitCommandType represents the supported Git protocol commands.
type GitCommandType string

const (
	GitUploadPack  GitCommandType = "git-upload-pack"
	GitReceivePack GitCommandType = "git-receive-pack"
)

// GitCommand represents a parsed Git SSH command.
type GitCommand struct {
	Type GitCommandType
	Repo string
	Raw  string
}

// ParseGitCommand parses SSH command strings like:
//
//	git-upload-pack '/org/repo'
//	git-receive-pack "org/repo.git"
func ParseGitCommand(cmd string) (*GitCommand, error) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return nil, errors.New("empty command")
	}

	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) < 2 {
		return nil, errors.New("missing repo argument")
	}

	command := parts[0]
	repo := strings.TrimSpace(parts[1])
	if repo == "" {
		return nil, errors.New("empty repo argument")
	}

	repo = trimQuotes(repo)
	repo = strings.TrimPrefix(repo, "/")

	switch GitCommandType(command) {
	case GitUploadPack, GitReceivePack:
		if repo == "" {
			return nil, errors.New("empty repo after normalization")
		}
		return &GitCommand{Type: GitCommandType(command), Repo: repo, Raw: trimmed}, nil
	default:
		return nil, errors.New("unsupported command: " + command)
	}
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		first := s[0]
		last := s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// HandleCommand routes the parsed Git command to the handler.
func HandleCommand(command string, handler Handler, io GitIO) error {
	cmd, err := ParseGitCommand(command)
	if err != nil {
		return err
	}
	if handler == nil {
		return errors.New("missing handler")
	}

	switch cmd.Type {
	case GitUploadPack:
		return handler.UploadPack(cmd.Repo, io)
	case GitReceivePack:
		return handler.ReceivePack(cmd.Repo, io)
	default:
		return errors.New("unsupported command: " + string(cmd.Type))
	}
}
