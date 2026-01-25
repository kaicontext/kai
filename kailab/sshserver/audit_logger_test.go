package sshserver

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestLoggerAuditor(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	auditor := NewLoggerAuditor(logger)

	event := AuditEvent{
		Time:       time.Now(),
		Duration:   250 * time.Millisecond,
		User:       "alice",
		RemoteAddr: "127.0.0.1:2222",
		Command:    GitUploadPack,
		Repo:       "org/repo",
		RawCommand: "git-upload-pack 'org/repo'",
		Success:    true,
	}
	auditor.Audit(nil, event)

	out := buf.String()
	if !strings.Contains(out, "ssh_audit") {
		t.Fatalf("expected audit prefix, got %q", out)
	}
	if !strings.Contains(out, "user=alice") || !strings.Contains(out, "repo=org/repo") {
		t.Fatalf("expected audit fields, got %q", out)
	}
}
