//go:build integration

package orbstack

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// integrationPrefix starts the name of every machine of the integration
// tests, so that the client under test can touch no other machine.
const integrationPrefix = "orbit-it-"

// createTimeout bounds the creation of one machine. On a cold OrbStack,
// orb create alone can take most of three minutes.
const createTimeout = 5 * time.Minute

// integrationTimeout bounds the steps of one integration test after its
// machine exists.
const integrationTimeout = 3 * time.Minute

// initLockPath is the file that serializes the integration runs on one
// host. A test whose machine makes init refuse every directory holds an
// exclusive lock on it; every test in cmd/orbit that runs init holds a
// shared lock on the same path.
const initLockPath = "/tmp/orbit-integration-init.lock"

// blockInit holds the exclusive lock on initLockPath until the end of t, so
// that no init test of any run on the host runs while the machine of t
// exists. Call it before newMachine: the lock is released after the
// machine is deleted.
func blockInit(t *testing.T) {
	t.Helper()
	file, err := os.OpenFile(initLockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		return
	}
	t.Logf("waiting for the integration tests of other runs that hold the init lock (%s)", initLockPath)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("lock %s: %v", initLockPath, err)
	}
}

// testContext returns a context that ends after integrationTimeout.
func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)
	return ctx
}

// inMachine runs a command as root in the machine name and returns its
// output and its error.
func inMachine(ctx context.Context, name string, command ...string) (string, error) {
	args := append([]string{"-m", name, "-u", "root"}, command...)
	output, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// newMachine creates a machine of the integration tests with the mount dir
// under its own deadline, createTimeout, registers its deletion, and returns
// its client and its name.
func newMachine(t *testing.T, dir string) (*Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), createTimeout)
	defer cancel()
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
	dir := hostDir(t)
	if err := os.WriteFile(filepath.Join(dir, "from-host.txt"), []byte("written on the host"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, name := newMachine(t, dir)
	ctx := testContext(t)
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
// agent swapped for a relative symlink to another directory after the check
// and before the mount. The configuration of OrbStack still names the
// checked path; the active mounts show the target of the symlink. A path
// swapped between two starts does not show there. While the machine exists,
// init refuses every directory, so the test blocks the init tests of every
// run on the host.
func TestActiveMountsShowASwappedPath(t *testing.T) {
	blockInit(t)
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

	client, name := newMachine(t, checked)
	ctx := testContext(t)

	checkMounts(ctx, t, client, name, map[string]string{checked: checked}, map[string]string{checked: secret})
}

func TestRunAsRootRunsTheScriptAsRoot(t *testing.T) {
	client, name := newMachine(t, hostDir(t))
	ctx := testContext(t)
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

// TestRunAsAgentRunsTheScriptAsTheAgent creates the agent user with
// RunAsRoot first: the machine of this test is not provisioned.
func TestRunAsAgentRunsTheScriptAsTheAgent(t *testing.T) {
	client, name := newMachine(t, hostDir(t))
	ctx := testContext(t)
	if err := client.RunAsRoot(ctx, name, []byte("set -eu\nuseradd --create-home --shell /bin/bash --user-group "+AgentUser+"\n")); err != nil {
		t.Fatalf("create the agent user: %v", err)
	}
	if err := client.RunAsAgent(ctx, name, []byte("set -eu\n[ \"$(id -un)\" = agent ]\necho done > \"$HOME/ran\"\n")); err != nil {
		t.Fatalf("RunAsAgent = %v", err)
	}
	if output, err := inMachine(ctx, name, "stat", "-c", "%U %n", "/home/agent/ran"); err != nil || output != "agent /home/agent/ran" {
		t.Errorf("the script did not run as the agent: %q, %v", output, err)
	}
	if err := client.RunAsAgent(ctx, name, []byte("exit 7\n")); err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Errorf("RunAsAgent of a failing script = %v, want exit status 7", err)
	}
}

// TestAnotherHomeNamesHomeWhileOrbStackRuns runs the client with HOME set to
// an empty directory while OrbStack runs. orb then cannot reach OrbStack and
// prints Stopped; the client names HOME as the cause and runs no other
// command. With the account home, the same call works, so OrbStack still
// runs. The test creates no machine and starts or stops nothing.
func TestAnotherHomeNamesHomeWhileOrbStackRuns(t *testing.T) {
	ctx := testContext(t)
	name := integrationPrefix + strings.ToLower(rand.Text()[:8])
	client := New(integrationPrefix)
	if _, err := client.Exists(ctx, name); err != nil {
		t.Fatalf("Exists with the account home = %v, want OrbStack running", err)
	}

	t.Run("empty HOME", func(t *testing.T) {
		t.Setenv("HOME", hostDir(t))
		_, err := client.Exists(ctx, name)
		if !errors.Is(err, ErrHome) || errors.Is(err, ErrNotRunning) {
			t.Fatalf("Exists with an empty HOME = %v, want %v and not %v", err, ErrHome, ErrNotRunning)
		}
		t.Logf("error: %v", err)
	})

	if _, err := client.Exists(ctx, name); err != nil {
		t.Errorf("Exists with the account home again = %v, want OrbStack still running", err)
	}
}

// forwardedVariables are host variables that are not allowed and that
// OrbStack 2.2.5 copies into a machine. ORBENV and WSLENV also copy every host
// variable that they name.
var forwardedVariables = []string{
	"ORBENV", "WSLENV",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "FTP_PROXY", "http_proxy", "https_proxy", "all_proxy", "ftp_proxy",
	"DOCKER_HOST", "DOCKER_AUTH_CONFIG", "DOCKER_CONFIG", "DOCKER_X", "ELECTRON_RUN_AS_NODE",
	"LC_X", "TERM_PROGRAM", "COMMAND_MODE",
}

// TestOnlyAllowedVariablesReachTheMachine sets variables that orb forwards
// but that are not allowed: ORBENV and WSLENV name a variable of the test
// process, and each other one holds a proxy URL with a user name and a
// marker as the password. A script of RunAsRoot and one of RunAsAgent find no
// variable that holds the marker in the machine, and find the allowed TERM
// and LC_ALL. orb run directly with the same environment does forward each
// of the forbidden variables, so the test fails when the client stops
// removing one.
func TestOnlyAllowedVariablesReachTheMachine(t *testing.T) {
	const variable = "ORBIT_IT_FORWARDED"
	namingVariables := []string{"ORBENV", "WSLENV"}
	marker := "marker-" + strings.ToLower(rand.Text()[:8])
	value := "http://orbit:" + marker + "@proxy.invalid:3128"
	allowed := []string{"TERM=xterm-256color", "LC_ALL=C.UTF-8"}
	client, name := newMachine(t, hostDir(t))
	ctx := testContext(t)
	if err := client.RunAsRoot(ctx, name, []byte("set -eu\nuseradd --create-home --shell /bin/bash --user-group "+AgentUser+"\n")); err != nil {
		t.Fatalf("create the agent user: %v", err)
	}
	t.Setenv(variable, marker)
	for _, forwarded := range forwardedVariables {
		t.Setenv(forwarded, value)
	}
	for _, naming := range namingVariables {
		t.Setenv(naming, variable)
	}
	for _, entry := range allowed {
		key, setting, _ := strings.Cut(entry, "=")
		t.Setenv(key, setting)
	}
	script := []byte("env > \"$HOME/environment\"\n")

	if err := client.RunAsRoot(ctx, name, script); err != nil {
		t.Fatalf("RunAsRoot = %v", err)
	}
	if err := client.RunAsAgent(ctx, name, script); err != nil {
		t.Fatalf("RunAsAgent = %v", err)
	}
	for _, file := range []string{"/root/environment", "/home/" + AgentUser + "/environment"} {
		output, err := inMachine(ctx, name, "cat", file)
		if err != nil || !strings.Contains(output, "HOME=") || strings.Contains(output, marker) {
			t.Errorf("%s in the machine = %q, %v, want an environment without %q", file, output, err, marker)
		}
		for _, entry := range allowed {
			if !slices.Contains(strings.Split(output, "\n"), entry) {
				t.Errorf("%s in the machine lacks the allowed %q", file, entry)
			}
		}
	}

	output, err := inMachine(ctx, name, "env")
	if err != nil {
		t.Fatalf("orb env = %v", err)
	}
	direct := strings.Split(output, "\n")
	for _, forwarded := range forwardedVariables {
		t.Run("orb forwards with "+forwarded, func(t *testing.T) {
			if !slices.Contains(namingVariables, forwarded) {
				if !slices.Contains(direct, forwarded+"="+value) {
					t.Errorf("orb with %s=%s did not forward it: the test no longer proves the removal", forwarded, value)
				}
				return
			}
			for _, naming := range namingVariables {
				t.Setenv(naming, "")
			}
			t.Setenv(forwarded, variable)
			if output, err := inMachine(ctx, name, "printenv", variable); err != nil || output != marker {
				t.Errorf("orb with %s=%s printed %q, %v, want %q: the test no longer proves the removal", forwarded, variable, output, err, marker)
			}
		})
	}
}
