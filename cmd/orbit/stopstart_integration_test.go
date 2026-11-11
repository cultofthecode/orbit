//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/start"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// stopStartTimeout bounds the stop and start test: two runs of init, one
// with the install of Claude Code, several starts with the boot check, and
// the probes.
const stopStartTimeout = 20 * time.Minute

// The file that the agent writes in its home directory before the stop, and
// its content, which must survive the stop and the start.
const (
	stateFile        = "/home/agent/orbit-it-state"
	stateFileContent = "written before the stop"
)

// writeStateFile writes stateFile as the agent.
const writeStateFile = `set -eu
printf '%s\n' '` + stateFileContent + `' > ` + stateFile + `
`

// afterStartProbes checks a started sandbox as root: the file of the agent
// survived, the agent reaches no public address directly and no host
// without an allow rule through the proxy, but reaches an allowed host, and
// every unit of the boundary entered active, in this boot, before the unit
// of every process with a uid from 1000 up started. It compares the
// timestamps of systemd in microseconds: the start time of a process in
// /proc counts clock ticks of 10 ms, too coarse to order it.
const afterStartProbes = agentProbes + `
content=$(as_agent cat ` + stateFile + `) || fail "` + stateFile + ` is gone"
[ "$content" = '` + stateFileContent + `' ] || fail "` + stateFile + ` holds $content"
if as_agent curl -sS --noproxy '*' -m 5 -o /dev/null -k https://1.1.1.1 2>/dev/null; then fail "agent reached 1.1.1.1 directly"; fi
refused example.org
tunnel github.com
tunnel api.anthropic.com
latest=0
for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do
  at=$(systemctl show -p ActiveEnterTimestampMonotonic --value "$unit")
  [ "${at:-0}" -gt 0 ] || fail "$unit did not enter active in this boot"
  [ "${at:-0}" -gt "$latest" ] && latest=$at
done
agents=0
for dir in /proc/[0-9]*; do
  uid=$(awk '/^Uid:/ {print $2}' "$dir/status" 2>/dev/null) || continue
  [ -n "$uid" ] && [ "$uid" -ge 1000 ] || continue
  unit=$(awk -F: '$1 == "0" {print $3}' "$dir/cgroup" 2>/dev/null | tr / '\n' | grep -m1 -E '[.](service|scope)$') || unit=""
  [ -n "$unit" ] || { fail "process ${dir#/proc/} with uid $uid is in no unit"; continue; }
  started=$(systemctl show -p InactiveExitTimestampMonotonic --value "$unit")
  agents=$((agents + 1))
  echo "process ${dir#/proc/} uid $uid ($(tr '\0' ' ' < "$dir/cmdline" | cut -c1-60)) in $unit, which started at $started us" >&2
  [ "${started:-0}" -gt "$latest" ] || fail "process ${dir#/proc/} with uid $uid runs in $unit, which started at $started us, before the boundary was active at $latest us"
done
echo "boundary active at $latest us; $agents processes with a uid from 1000 up, all started later" >&2
[ "$failures" -eq 0 ]
`

// maskProxy masks tinyproxy.service, so that it does not start at the next
// boot of the machine.
const maskProxy = "set -eu\nsystemctl mask tinyproxy.service >/dev/null 2>&1\n"

// outdateMarker writes a provisioning version into the marker that this
// orbit does not write.
const outdateMarker = "set -eu\nprintf '0\\n' > /etc/orbit/provisioned\n"

// initLock is a lock on initLockPath.
type initLock struct {
	file *os.File
}

// lockInit takes the lock how (syscall.LOCK_SH or syscall.LOCK_EX) on
// initLockPath, and waits while a test of another run holds a conflicting
// one. A shared lock lets init and start run while no machine of another
// run has a stored path through a symlink; the exclusive lock lets this run
// make one.
func lockInit(t *testing.T, how int) initLock {
	t.Helper()
	file, err := os.OpenFile(initLockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB); err == nil {
		return initLock{file: file}
	}
	t.Logf("waiting for the integration tests of other runs that hold the init lock (%s)", initLockPath)
	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		t.Fatalf("lock %s: %v", initLockPath, err)
	}
	return initLock{file: file}
}

// release releases the lock.
func (l initLock) release(t *testing.T) {
	t.Helper()
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock %s: %v", initLockPath, err)
	}
}

// stateOf returns the state of the machine name, which never starts it.
func stateOf(ctx context.Context, t *testing.T, client *orbstack.Client, name string) string {
	t.Helper()
	state, err := client.State(ctx, name)
	if err != nil {
		t.Fatalf("State(%q) = %v", name, err)
	}
	return state
}

// expectState fails t unless the machine name is in state want.
func expectState(ctx context.Context, t *testing.T, client *orbstack.Client, name, want, when string) {
	t.Helper()
	if got := stateOf(ctx, t, client, name); got != want {
		t.Errorf("state of %s %s = %q, want %q", name, when, got, want)
	}
}

// expectOrbStackRunning fails t unless orb status prints Running: no stop of
// a sandbox may stop OrbStack.
func expectOrbStackRunning(t *testing.T) {
	t.Helper()
	out, err := exec.Command("orb", "status").Output()
	if status := strings.TrimSpace(string(out)); err != nil || status != "Running" {
		t.Errorf("orb status = %q, %v, want Running", status, err)
	}
}

// sandboxes is the setup of TestStopAndStartTheSandbox: the sandbox a with
// Claude Code, which the test stops and starts, and the sandbox b without an
// agent, which also runs.
type sandboxes struct {
	client      *orbstack.Client
	stop        stop.Handler
	start       start.Handler
	dirA, nameA string
	dirB, nameB string
}

// newSandboxes creates and provisions the sandboxes a and b.
func newSandboxes(ctx context.Context, t *testing.T) sandboxes {
	t.Helper()
	client := orbstack.New(sandbox.NamePrefix)
	s := sandboxes{client: client, stop: stop.Handler{Machines: client}, start: startHandler(client)}
	setUp := initHandler(client, claudeRegistry(t))
	s.dirA, s.nameA = newSandboxDir(ctx, t, client)
	s.dirB, s.nameB = newSandboxDir(ctx, t, client)
	if _, err := setUp.Handle(ctx, initialize.Input{Dir: s.dirA, Agent: claude.Definition.Name}); err != nil {
		t.Fatalf("init --agent claude-code in %s = %v; machines that this test did not create: %s", s.dirA, err, otherMachines(ctx, client, s.nameA))
	}
	if _, err := setUp.Handle(ctx, initialize.Input{Dir: s.dirB}); err != nil {
		t.Fatalf("init in %s = %v; machines that this test did not create: %s", s.dirB, err, otherMachines(ctx, client, s.nameB))
	}
	return s
}

// TestStopAndStartTheSandbox stops and starts a provisioned sandbox next to a
// second one: both commands are idempotent, act only on the machine of the
// current directory, and keep the home of the agent; after the start the
// boundary is up. It then covers the refusals: no sandbox, a stored path of
// another sandbox through a symlink, a boundary unit that does not start
// (stopped again), and an outdated marker (left running). OrbStack keeps
// running throughout.
func TestStopAndStartTheSandbox(t *testing.T) {
	shared := lockInit(t, syscall.LOCK_SH)
	ctx, cancel := context.WithTimeout(context.Background(), stopStartTimeout)
	t.Cleanup(cancel)
	s := newSandboxes(ctx, t)
	if err := s.client.RunAsAgent(ctx, s.nameA, []byte(writeStateFile)); err != nil {
		t.Fatalf("write %s in %s: %v", stateFile, s.nameA, err)
	}

	checkIdempotentStopAndStart(ctx, t, s)
	if err := s.client.RunAsRoot(ctx, s.nameA, []byte(afterStartProbes)); err != nil {
		t.Errorf("probes after the start of %s:\n%v", s.nameA, err)
	} else {
		t.Logf("probes after the start of %s passed", s.nameA)
	}
	checkNoSandbox(ctx, t, s)
	checkBoundaryFailureStopsAgain(ctx, t, s)
	checkOutdatedMarkerStaysRunning(ctx, t, s)

	shared.release(t)
	exclusive := lockInit(t, syscall.LOCK_EX)
	checkLinkedStoredPathRefusesTheStart(ctx, t, s)
	exclusive.release(t)
	expectOrbStackRunning(t)
}

// checkIdempotentStopAndStart stops sandbox a twice and starts it twice, and
// checks that sandbox b keeps running.
func checkIdempotentStopAndStart(ctx context.Context, t *testing.T, s sandboxes) {
	t.Helper()
	stopped, err := s.stop.Handle(ctx, stop.Input{Dir: s.dirA})
	if err != nil || stopped != (stop.Output{Name: s.nameA, Dir: s.dirA}) {
		t.Fatalf("first stop = %+v, %v, want %s stopped", stopped, err, s.nameA)
	}
	expectState(ctx, t, s.client, s.nameA, "stopped", "after the stop")
	expectState(ctx, t, s.client, s.nameB, "running", "after the stop of "+s.nameA)
	expectOrbStackRunning(t)
	again, err := s.stop.Handle(ctx, stop.Input{Dir: s.dirA})
	if err != nil || !again.AlreadyStopped {
		t.Errorf("second stop = %+v, %v, want already stopped", again, err)
	}
	expectState(ctx, t, s.client, s.nameA, "stopped", "after the second stop")

	started, err := s.start.Handle(ctx, start.Input{Dir: s.dirA})
	if err != nil || started != (start.Output{Name: s.nameA, Dir: s.dirA}) {
		t.Fatalf("first start = %+v, %v, want %s started; machines that this test did not create: %s", started, err, s.nameA, otherMachines(ctx, s.client, s.nameA))
	}
	expectState(ctx, t, s.client, s.nameA, "running", "after the start")
	runningAgain, err := s.start.Handle(ctx, start.Input{Dir: s.dirA})
	if err != nil || !runningAgain.AlreadyRunning {
		t.Errorf("second start = %+v, %v, want already running", runningAgain, err)
	}
	expectState(ctx, t, s.client, s.nameB, "running", "after the start of "+s.nameA)
}

// checkNoSandbox runs stop and start in a directory without a sandbox and in
// a subdirectory of a project, and checks that no machine changed state.
func checkNoSandbox(ctx context.Context, t *testing.T, s sandboxes) {
	t.Helper()
	sub := filepath.Join(s.dirA, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{projectDir(t), sub} {
		if _, err := s.stop.Handle(ctx, stop.Input{Dir: dir}); !errors.Is(err, sandbox.ErrNoSandbox) {
			t.Errorf("stop in %s = %v, want %v", dir, err, sandbox.ErrNoSandbox)
		}
		if _, err := s.start.Handle(ctx, start.Input{Dir: dir}); !errors.Is(err, sandbox.ErrNoSandbox) {
			t.Errorf("start in %s = %v, want %v", dir, err, sandbox.ErrNoSandbox)
		}
	}
	expectState(ctx, t, s.client, s.nameA, "running", "after the refusals")
	expectState(ctx, t, s.client, s.nameB, "running", "after the refusals")
}

// checkBoundaryFailureStopsAgain masks tinyproxy.service in sandbox b, stops
// it, and expects the start to fail with ErrBoundaryDown and to leave b
// stopped.
func checkBoundaryFailureStopsAgain(ctx context.Context, t *testing.T, s sandboxes) {
	t.Helper()
	if err := s.client.RunAsRoot(ctx, s.nameB, []byte(maskProxy)); err != nil {
		t.Fatalf("mask tinyproxy.service in %s: %v", s.nameB, err)
	}
	if _, err := s.stop.Handle(ctx, stop.Input{Dir: s.dirB}); err != nil {
		t.Fatalf("stop of %s = %v", s.nameB, err)
	}

	_, err := s.start.Handle(ctx, start.Input{Dir: s.dirB})

	if !errors.Is(err, start.ErrBoundaryDown) || !strings.Contains(err.Error(), "tinyproxy.service is not active") || !strings.Contains(err.Error(), "the sandbox was stopped again") {
		t.Errorf("start with a masked tinyproxy.service = %v, want %v naming the unit; machines that this test did not create: %s", err, start.ErrBoundaryDown, otherMachines(ctx, s.client, s.nameB))
	}
	t.Logf("start with a masked tinyproxy.service: %v", err)
	expectState(ctx, t, s.client, s.nameB, "stopped", "after the failed boot check")
	expectState(ctx, t, s.client, s.nameA, "running", "after the failed start of "+s.nameB)
}

// checkOutdatedMarkerStaysRunning writes another version into the marker of
// sandbox a, restarts it, and expects ErrNotProvisioned with a running
// sandbox.
func checkOutdatedMarkerStaysRunning(ctx context.Context, t *testing.T, s sandboxes) {
	t.Helper()
	if err := s.client.RunAsRoot(ctx, s.nameA, []byte(outdateMarker)); err != nil {
		t.Fatalf("outdate the marker of %s: %v", s.nameA, err)
	}
	if _, err := s.stop.Handle(ctx, stop.Input{Dir: s.dirA}); err != nil {
		t.Fatalf("stop of %s = %v", s.nameA, err)
	}

	_, err := s.start.Handle(ctx, start.Input{Dir: s.dirA})

	if !errors.Is(err, start.ErrNotProvisioned) || errors.Is(err, start.ErrBoundaryDown) || !strings.Contains(err.Error(), `run "orbit init" in `+s.dirA) {
		t.Errorf("start with an outdated marker = %v, want %v naming orbit init", err, start.ErrNotProvisioned)
	}
	t.Logf("start with an outdated marker: %v", err)
	expectState(ctx, t, s.client, s.nameA, "running", "after the start with an outdated marker")
}

// checkLinkedStoredPathRefusesTheStart stops sandbox a, replaces the stored
// path of sandbox b with a symlink to the moved directory, and expects the
// start of a to fail with ErrLinkedSandboxDir naming b, with a still
// stopped. It restores the path before it returns. Call it only under the
// exclusive init lock: the linked path makes every init and start refuse.
func checkLinkedStoredPathRefusesTheStart(ctx context.Context, t *testing.T, s sandboxes) {
	t.Helper()
	if _, err := s.stop.Handle(ctx, stop.Input{Dir: s.dirA}); err != nil {
		t.Fatalf("stop of %s = %v", s.nameA, err)
	}
	moved := s.dirB + "-moved"
	if err := os.Rename(s.dirB, moved); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if err := os.Remove(s.dirB); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove the symlink %s: %v", s.dirB, err)
		}
		if err := os.Rename(moved, s.dirB); err != nil {
			t.Errorf("restore %s: %v", s.dirB, err)
		}
	}
	defer restore()
	if err := os.Symlink(moved, s.dirB); err != nil {
		t.Fatal(err)
	}

	_, err := s.start.Handle(ctx, start.Input{Dir: s.dirA})

	if !errors.Is(err, sandbox.ErrLinkedSandboxDir) || !strings.Contains(err.Error(), s.nameB) {
		t.Errorf("start with the stored path of %s through a symlink = %v, want %v naming it", s.nameB, err, sandbox.ErrLinkedSandboxDir)
	}
	t.Logf("start with a linked stored path: %v", err)
	expectState(ctx, t, s.client, s.nameA, "stopped", "after the refused start")
}

// TestStopAndStartRefuseAMachineInAnotherState creates a sandbox machine and,
// while OrbStack reports it in a state other than running or stopped (it is
// "creating" for several seconds on OrbStack 2.2.5), runs stop and start in
// its directory. Both refuse with ErrUnexpectedState, and the machine ends
// running, as the create leaves it.
func TestStopAndStartRefuseAMachineInAnotherState(t *testing.T) {
	lockInit(t, syscall.LOCK_SH)
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	t.Cleanup(cancel)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	created := make(chan error, 1)
	go func() { created <- client.Create(ctx, name, dir) }()

	state := waitForTransientState(ctx, t, client, name)
	_, stopErr := stop.Handler{Machines: client}.Handle(ctx, stop.Input{Dir: dir})
	_, startErr := startHandler(client).Handle(ctx, start.Input{Dir: dir})

	if err := <-created; err != nil {
		t.Fatalf("Create(%q) = %v", name, err)
	}
	t.Logf("state during the create: %s; stop: %v; start: %v", state, stopErr, startErr)
	for command, err := range map[string]error{"stop": stopErr, "start": startErr} {
		if !errors.Is(err, sandbox.ErrUnexpectedState) {
			t.Errorf("%s while %s = %v, want %v", command, name, err, sandbox.ErrUnexpectedState)
		}
	}
	expectState(ctx, t, client, name, "running", "after the create")
	expectOrbStackRunning(t)
}

// waitForTransientState waits until OrbStack lists the machine name in a
// state other than running or stopped and returns that state.
func waitForTransientState(ctx context.Context, t *testing.T, client *orbstack.Client, name string) string {
	t.Helper()
	for {
		switch state := stateOf(ctx, t, client, name); state {
		case "running", "stopped":
			t.Fatalf("machine %s is %s before a transient state was seen", name, state)
		case "":
		default:
			return state
		}
		select {
		case <-ctx.Done():
			t.Fatalf("machine %s did not appear: %v", name, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
