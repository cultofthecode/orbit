//go:build integration

package orbstack

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

// newMachine creates a machine of the integration tests with the mount dir,
// registers its deletion, and returns its client and its name.
func newMachine(ctx context.Context, t *testing.T, dir string) (*Client, string) {
	t.Helper()
	client := New(integrationPrefix)
	name := integrationPrefix + strings.ToLower(rand.Text()[:8])
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
	return client, name
}

// checkMounts checks the configured and the active mounts of the machine
// name.
func checkMounts(ctx context.Context, t *testing.T, client *Client, name string, configured, active map[string]string) {
	t.Helper()
	mounts, err := client.Mounts(ctx)
	if err != nil || !reflect.DeepEqual(mounts[name], configured) {
		t.Errorf("Mounts()[%q] = %q, %v, want %q", name, mounts[name], err, configured)
	}
	got, err := client.ActiveMounts(ctx, name)
	if err != nil || !reflect.DeepEqual(got, active) {
		t.Errorf("ActiveMounts(%q) = %q, %v, want %q", name, got, err, active)
	}
}

func TestCreateMountsOnlyTheDirectoryAtItsOwnPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)
	dir := hostDir(t)
	if err := os.WriteFile(filepath.Join(dir, "from-host.txt"), []byte("written on the host"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, name := newMachine(ctx, t, dir)
	if exists, err := client.Exists(ctx, name); err != nil || !exists {
		t.Errorf("Exists(%q) after Create = %v, %v, want true", name, exists, err)
	}
	checkMounts(ctx, t, client, name, map[string]string{dir: dir}, map[string]string{dir: dir})

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

// TestActiveMountsShowASwappedPath creates a machine for a path that an
// agent swapped for a relative symlink to another directory after the check.
// The configuration of OrbStack still names the checked path; only the
// active mounts show the directory that the machine really mounts.
func TestActiveMountsShowASwappedPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)
	base := hostDir(t)
	checked := filepath.Join(base, "project", "sub")
	secret := filepath.Join(base, "secret")
	for _, dir := range []string{filepath.Dir(checked), secret} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "secret"), checked); err != nil {
		t.Fatal(err)
	}

	client, name := newMachine(ctx, t, checked)

	checkMounts(ctx, t, client, name, map[string]string{checked: checked}, map[string]string{checked: secret})
}
