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

	"kailab/pack"
	"kailab/repo"
	"kailab/store"
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
	srv, err := StartWithListener(listener, NewGitHandler(reg, nil, GitHandlerOptions{}), nil)
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

func TestSSHUploadPackCloneDepth(t *testing.T) {
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
	srv, err := StartWithListener(listener, NewGitHandler(reg, nil, GitHandlerOptions{}), nil)
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	defer Stop(context.Background(), srv)

	cloneDir := filepath.Join(tmpDir, "clone-depth")
	port := listener.Addr().(*net.TCPAddr).Port
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p %d", port)
	cmd := exec.Command("git", "clone", "--depth", "1", "ssh://git@"+listener.Addr().String()+"/test/repo", cloneDir)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone --depth failed: %v\n%s", err, string(out))
	}

	if _, err := os.Stat(filepath.Join(cloneDir, ".git", "shallow")); err != nil {
		t.Fatalf("expected shallow file: %v", err)
	}
}

func TestSSHReceivePackPush(t *testing.T) {
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := StartWithListener(listener, NewGitHandler(reg, nil, GitHandlerOptions{}), nil)
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	defer Stop(context.Background(), srv)

	workDir := filepath.Join(tmpDir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := exec.Command("git", "-C", workDir, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", workDir, "config", "user.email", "test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config email: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", workDir, "config", "user.name", "Test User")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config name: %v\n%s", err, string(out))
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("push\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cmd = exec.Command("git", "-C", workDir, "add", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", workDir, "commit", "-m", "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, string(out))
	}

	port := listener.Addr().(*net.TCPAddr).Port
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p %d", port)
	cmd = exec.Command("git", "-C", workDir, "remote", "add", "origin", "ssh://git@"+listener.Addr().String()+"/test/repo")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", workDir, "push", "-u", "origin", "main")
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git push: %v\n%s", err, string(out))
	}

	ref, err := store.GetRef(handle.DB, "snap.main")
	if err != nil {
		t.Fatalf("get ref: %v", err)
	}
	_, kind, err := pack.ExtractObjectFromDB(handle.DB, ref.Target)
	if err != nil {
		t.Fatalf("extract ref target: %v", err)
	}
	if kind != "ChangeSet" {
		t.Fatalf("expected ChangeSet, got %s", kind)
	}
}
