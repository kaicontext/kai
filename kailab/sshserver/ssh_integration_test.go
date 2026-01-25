//go:build integration

package sshserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"kailab/repo"
)

func TestSSHUploadPackClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	reg := repo.NewRegistry(repo.RegistryConfig{DataDir: tmpDir})
	defer reg.Close()

	handle, err := reg.Create(context.Background(), "test", "repo")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	reg.Acquire(handle)
	defer reg.Release(handle)

	if err := seedTestRepo(handle.DB); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := StartWithListener(listener, NewGitHandler(reg, nil, nil), nil)
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	defer Stop(context.Background(), srv)

	cloneDir := filepath.Join(tmpDir, "clone")
	port := listener.Addr().(*net.TCPAddr).Port
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p %d", port)
	cmd := exec.Command("git", "clone", "ssh://git@"+listener.Addr().String()+"/test/repo", cloneDir)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, string(out))
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}
