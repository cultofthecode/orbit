//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/shell"
)

// shellTimeout bounds the shell test when the test binary has no deadline:
// one run of init with the install of Claude Code, the shells, and one stop.
const shellTimeout = 20 * time.Minute

// shellStatus is the status with which the script of the shell exits when
// every probe passed.
const shellStatus = 3

// worktreeList is the file in the project directory into which the shell
// writes the worktree list as the sandbox sees it.
const worktreeList = ".orbit-it-worktrees"

// shellProbes is the script that the login shell of the agent reads from
// standard input. It checks that the shell is the unprivileged agent in the
// project directory, with Claude Code on PATH and no way to root, adds the
// worktree wt-a, and exits with shellStatus. Its first argument is the
// project directory, its second shellStatus.
const shellProbes = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
[ "$(id -un)" = agent ] || fail "user is $(id -un), want agent"
[ "$(id -u)" -ge 1000 ] || fail "uid is $(id -u), want at least 1000"
if sudo -n true </dev/null 2>/dev/null; then fail "sudo -n true succeeded"; fi
if /usr/bin/sudo.ws -n true </dev/null 2>/dev/null; then fail "/usr/bin/sudo.ws -n true succeeded"; fi
if /usr/lib/cargo/bin/sudo -n true </dev/null 2>/dev/null; then fail "/usr/lib/cargo/bin/sudo -n true succeeded"; fi
if su -c id root </dev/null 2>/dev/null; then fail "su -c id root succeeded"; fi
cap=$(awk '/^CapEff:/ {print $2}' /proc/self/status)
[ "$cap" = 0000000000000000 ] || fail "CapEff is $cap, want 0"
for group in sudo admin adm docker lxd disk shadow; do
  if id -nG | tr ' ' '\n' | grep -qx "$group"; then fail "agent is in the group $group"; fi
done
[ "$(pwd)" = '%[1]s' ] || fail "pwd is $(pwd), want %[1]s"
[ "$(command -v claude)" = /home/agent/.local/bin/claude ] || fail "claude is at '$(command -v claude)', want /home/agent/.local/bin/claude"
git worktree add -q wt-a </dev/null || fail "git worktree add wt-a failed"
git worktree list --porcelain > ` + worktreeList + ` </dev/null || fail "git worktree list failed"
echo "probes of the shell done with $failures failures" >&2
[ "$failures" -eq 0 ] || exit 1
exit %[2]d
`

// shellEnvironmentStatus is the status with which the script of
// shellEnvironmentProbes exits when no probe failed.
const shellEnvironmentStatus = 4

// hostSecret is a variable of the test process that ORBENV names, so that
// orb copies it into the machine unless the client removes ORBENV.
const hostSecret = "ORBIT_IT_SECRET"

// shellEnvironmentProbes is the script that checks that neither ORBENV nor
// hostSecret nor its value reached the shell of the agent. Its first argument
// is the value of hostSecret, its second shellEnvironmentStatus.
const shellEnvironmentProbes = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
if [ -n "${ORBENV+set}" ]; then fail "ORBENV reached the shell"; fi
if [ -n "${` + hostSecret + `+set}" ]; then fail "` + hostSecret + ` reached the shell"; fi
if env | grep -qF '%[1]s'; then fail "the value of ` + hostSecret + ` reached the shell"; fi
echo "probes of the environment done with $failures failures" >&2
[ "$failures" -eq 0 ] || exit 1
exit %[2]d
`

// removeMarker removes the provisioning marker.
const removeMarker = "set -eu\nrm -f /etc/orbit/provisioned\n"

// restoreMarker writes the provisioning marker as provisioning writes it.
var restoreMarker = fmt.Sprintf("set -eu\nprintf '%%s\\n' %d > /etc/orbit/provisioned\n", provision.Version)

// TestShellIsTheAgentInTheProjectDirectory runs the handler of orbit shell
// on a sandbox from init --agent claude-code, with a script on standard
// input instead of a terminal, so that orb gets the same arguments as for
// the interactive shell. The shell is the agent without a way to root, in
// the project directory, with claude on PATH; a worktree it adds has the
// same path on the host; its status passes through; ORBENV and a secret of
// the host do not reach it (R08, #76). Then the refusals: no
// sandbox, an outdated or missing marker, and a stopped sandbox, which
// stays stopped. The refusal with a failed boundary unit is E14 of the escape suite (TestEscapeRestart).
func TestShellIsTheAgentInTheProjectDirectory(t *testing.T) {
	waitToRunInit(t)
	ctx := boundedContext(t, shellTimeout)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	initRepository(t, dir)
	if _, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), Agent: claude.Definition.Name}); err != nil {
		t.Fatalf("init --agent claude-code in %s = %v; machines that this test did not create: %s", dir, err, otherMachines(ctx, client, name))
	}
	handler := shell.Handler{Machines: client, ReadinessScript: provision.ReadinessScript}

	checkShellProbes(ctx, t, handler, dir, name)
	checkShellGetsNoHostSecret(ctx, t, handler, dir, name)
	checkWorktreeOnTheHost(t, dir)
	checkShellWithoutASandbox(ctx, t, handler, dir)
	checkShellNotReady(ctx, t, client, handler, dir, name)
	checkShellInAStoppedSandbox(ctx, t, client, handler, dir, name)
	expectOrbStackRunning(t)
}

// initRepository makes dir a Git repository with one commit, on the host.
func initRepository(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=orbit-it", "-c", "user.email=orbit-it@example.invalid", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
		}
	}
}

// openShell runs the handler in dir with script on standard input and
// returns the output, what the shell wrote to its two streams, and the
// error.
func openShell(ctx context.Context, handler shell.Handler, dir, script string) (shell.Output, string, error) {
	var out bytes.Buffer
	got, err := handler.Handle(ctx, shell.Input{Dir: dir, In: strings.NewReader(script), Out: &out, Err: &out})
	return got, out.String(), err
}

// checkShellProbes runs shellProbes in the shell and expects shellStatus.
func checkShellProbes(ctx context.Context, t *testing.T, handler shell.Handler, dir, name string) {
	t.Helper()
	got, streams, err := openShell(ctx, handler, dir, fmt.Sprintf(shellProbes, dir, shellStatus))
	t.Logf("shell in %s:\n%s", name, streams)
	if err != nil || got != (shell.Output{Name: name, Dir: dir, Status: shellStatus}) {
		t.Fatalf("shell = %+v, %v, want status %d of %s in %s", got, err, shellStatus, name, dir)
	}
}

// checkShellGetsNoHostSecret exports hostSecret with a random value and
// ORBENV naming it, runs shellEnvironmentProbes in the shell, and expects
// shellEnvironmentStatus. orb run directly with the same environment prints
// the value in the machine, so the check fails when the client stops
// removing ORBENV for the shell.
func checkShellGetsNoHostSecret(ctx context.Context, t *testing.T, handler shell.Handler, dir, name string) {
	t.Helper()
	secret := "orbit-it-secret-" + strings.ToLower(rand.Text()[:8])
	t.Setenv(hostSecret, secret)
	t.Setenv("ORBENV", hostSecret)
	got, streams, err := openShell(ctx, handler, dir, fmt.Sprintf(shellEnvironmentProbes, secret, shellEnvironmentStatus))
	t.Logf("shell with ORBENV=%s in %s:\n%s", hostSecret, name, streams)
	if err != nil || got != (shell.Output{Name: name, Dir: dir, Status: shellEnvironmentStatus}) {
		t.Errorf("shell with ORBENV=%s = %+v, %v, want status %d: ORBENV or the secret reached the shell", hostSecret, got, err, shellEnvironmentStatus)
	}
	direct, err := exec.CommandContext(ctx, "orb", "-m", name, "-u", "agent", "printenv", hostSecret).Output()
	if err != nil || strings.TrimSpace(string(direct)) != secret {
		t.Errorf("orb with ORBENV=%s printed %q, %v, want the secret: the check no longer proves the removal", hostSecret, direct, err)
	}
}

// checkWorktreeOnTheHost expects the worktree wt-a that the agent added in
// the project directory dir on the host, and the same worktree list on the
// host as in the sandbox (D18).
func checkWorktreeOnTheHost(t *testing.T, dir string) {
	t.Helper()
	if info, err := os.Stat(filepath.Join(dir, "wt-a")); err != nil || !info.IsDir() {
		t.Errorf("worktree %s on the host: %v", filepath.Join(dir, "wt-a"), err)
	}
	inSandbox, err := os.ReadFile(filepath.Join(dir, worktreeList))
	if err != nil {
		t.Fatalf("worktree list of the sandbox: %v", err)
	}
	onHost, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil || string(onHost) != string(inSandbox) {
		t.Errorf("worktree list on the host = %q, %v, want the list of the sandbox %q", onHost, err, inSandbox)
	}
}

// checkShellWithoutASandbox runs the shell in a subdirectory of the project
// and expects ErrNoSandbox.
func checkShellWithoutASandbox(ctx context.Context, t *testing.T, handler shell.Handler, dir string) {
	t.Helper()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, streams, err := openShell(ctx, handler, sub, "exit 0\n"); !errors.Is(err, sandbox.ErrNoSandbox) || streams != "" {
		t.Errorf("shell in %s = %v, %q, want %v and no shell", sub, err, streams, sandbox.ErrNoSandbox)
	}
}

// checkShellNotReady outdates and then removes the marker of the sandbox and
// expects ErrNotProvisioned without a shell each time. It restores the
// marker.
func checkShellNotReady(ctx context.Context, t *testing.T, client *orbstack.Client, handler shell.Handler, dir, name string) {
	t.Helper()
	for _, script := range []string{outdateMarker, removeMarker} {
		if err := client.RunAsRoot(ctx, name, []byte(script)); err != nil {
			t.Fatalf("change the marker of %s: %v", name, err)
		}
		_, streams, err := openShell(ctx, handler, dir, "echo opened\n")
		if !errors.Is(err, shell.ErrNotProvisioned) || !strings.Contains(err.Error(), "does not hold provisioning version") || !strings.Contains(err.Error(), `run "orbit init" in `+dir) || streams != "" {
			t.Errorf("shell with the marker changed by %q = %v, %q, want %v naming the marker and orbit init, and no shell", script, err, streams, shell.ErrNotProvisioned)
		}
		t.Logf("shell with a changed marker: %v", err)
	}
	if err := client.RunAsRoot(ctx, name, []byte(restoreMarker)); err != nil {
		t.Fatalf("restore the marker of %s: %v", name, err)
	}
}

// checkShellInAStoppedSandbox stops the sandbox and expects ErrNotRunning
// naming orbit start, with the sandbox still stopped.
func checkShellInAStoppedSandbox(ctx context.Context, t *testing.T, client *orbstack.Client, handler shell.Handler, dir, name string) {
	t.Helper()
	if err := client.Stop(ctx, name); err != nil {
		t.Fatalf("stop %s: %v", name, err)
	}
	_, streams, err := openShell(ctx, handler, dir, "echo opened\n")
	if !errors.Is(err, shell.ErrNotRunning) || !strings.Contains(err.Error(), `run "orbit start" in `+dir) || streams != "" {
		t.Errorf("shell in a stopped sandbox = %v, %q, want %v naming orbit start, and no shell", err, streams, shell.ErrNotRunning)
	}
	expectState(ctx, t, client, name, "stopped", "after the refused shell")
}
