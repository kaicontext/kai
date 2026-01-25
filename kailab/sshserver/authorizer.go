package sshserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/gliderlabs/ssh"
)

// AllowlistAuthorizer enforces a user/repo allowlist for git operations.
type AllowlistAuthorizer struct {
	users map[string]struct{}
	repos map[string]struct{}
}

// NewAllowlistAuthorizer builds an allowlist authorizer from user and repo lists.
func NewAllowlistAuthorizer(users, repos []string) *AllowlistAuthorizer {
	return &AllowlistAuthorizer{
		users: sliceToSet(users),
		repos: sliceToSet(repos),
	}
}

func (a *AllowlistAuthorizer) Authorize(ctx context.Context, session ssh.Session, cmd GitCommand) error {
	if len(a.users) > 0 {
		if _, ok := a.users[session.User()]; !ok {
			return fmt.Errorf("access denied: user %s", session.User())
		}
	}
	if len(a.repos) > 0 {
		if _, ok := a.repos[cmd.Repo]; !ok {
			return fmt.Errorf("access denied: repo %s", cmd.Repo)
		}
	}
	return nil
}

func sliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}
