//go:build integration

package orbstack

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// integrationPrefix starts the name of every machine of the integration
// tests, so that the client under test can touch no other machine.
const integrationPrefix = "orbit-it-"

// integrationTimeout bounds one integration test.
const integrationTimeout = 3 * time.Minute

// inMachine runs a command as root in the machine name and returns its
// output and its error.
func inMachine(ctx context.Context, name string, command ...string) (string, error) {
	args := append([]string{"-m", name, "-u", "root"}, command...)
	output, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// hostDir returns the canonical path of a new directory on the host.
func hostDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCreateMountsOnlyTheDirectoryAtItsOwnPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)
	client := New(integrationPrefix)
	name := integrationPrefix + strings.ToLower(rand.Text()[:8])
	dir := hostDir(t)
	if err := os.WriteFile(filepath.Join(dir, "from-host.txt"), []byte("written on the host"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Fatalf("Exists(%q) before Create = %v, %v, want false", name, exists, err)
	}
	t.Cleanup(func() {
		if err := client.Delete(context.WithoutCancel(ctx), name); err != nil {
			t.Errorf("Delete(%q) = %v", name, err)
		}
		if exists, err := client.Exists(context.WithoutCancel(ctx), name); err != nil || exists {
			t.Errorf("Exists(%q) after Delete = %v, %v, want false", name, exists, err)
		}
	})
	if err := client.Create(ctx, name, dir); err != nil {
		t.Fatalf("Create(%q, %q) = %v", name, dir, err)
	}
	t.Logf("created the machine %s with the mount %s", name, dir)
	if exists, err := client.Exists(ctx, name); err != nil || !exists {
		t.Errorf("Exists(%q) after Create = %v, %v, want true", name, exists, err)
	}

	if output, err := inMachine(ctx, name, "test", "-d", dir); err != nil {
		t.Errorf("%s is not a directory in the machine: %v: %s", dir, err, output)
	}
	content, err := inMachine(ctx, name, "cat", filepath.Join(dir, "from-host.txt"))
	if err != nil || content != "written on the host" {
		t.Errorf("file of the host in the machine = %q, %v, want %q", content, err, "written on the host")
	}
	users, err := inMachine(ctx, name, "ls", "-d", "/Users")
	if err == nil || !strings.Contains(users, "No such file or directory") {
		t.Errorf("ls -d /Users in the machine = %q, %v, want no such directory", users, err)
	}
	root, err := inMachine(ctx, name, "ls", "-d", "/")
	if err != nil || root != "/" {
		t.Errorf("ls -d / in the machine = %q, %v, want /", root, err)
	}
}

func TestRunAsRootRunsTheScriptAsRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)
	client := New(integrationPrefix)
	name := integrationPrefix + strings.ToLower(rand.Text()[:8])
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Fatalf("Exists(%q) before Create = %v, %v, want false", name, exists, err)
	}
	t.Cleanup(func() {
		if err := client.Delete(context.WithoutCancel(ctx), name); err != nil {
			t.Errorf("Delete(%q) = %v", name, err)
		}
	})
	if err := client.Create(ctx, name, hostDir(t)); err != nil {
		t.Fatalf("Create(%q) = %v", name, err)
	}
	if err := client.RunAsRoot(ctx, name, []byte("set -eu\n[ \"$(id -u)\" = 0 ]\necho done > /root/ran\n")); err != nil {
		t.Fatalf("RunAsRoot = %v", err)
	}
	if output, err := inMachine(ctx, name, "cat", "/root/ran"); err != nil || output != "done" {
		t.Errorf("the script did not run as root: %q, %v", output, err)
	}
	if err := client.RunAsRoot(ctx, name, []byte("exit 7\n")); err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Errorf("RunAsRoot of a failing script = %v, want exit status 7", err)
	}
}
