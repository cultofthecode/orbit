package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// readinessScript stands in for provision.ReadinessScript.
const readinessScript = "readiness check\n"

// otherMachine is a second machine on the host, which the handler must not
// touch.
const otherMachine = "orbit-other-0123456789abcdef"

// project returns a new canonical project directory and the name of its
// machine.
func project(t *testing.T) (string, string) {
	t.Helper()
	dir, err := realpath.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir, sandbox.MachineName(dir)
}

// withMachine returns a fake with the machine name of dir in state, and a
// second running machine.
func withMachine(name, dir, state string) *orbstacktest.Fake {
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{dir: dir}
	fake.States[name] = state
	fake.Machines[otherMachine] = map[string]string{"/other": "/other"}
	return fake
}

// handler returns the handler of the slice on machines.
func handler(machines Machines) Handler {
	return Handler{Machines: machines, ReadinessScript: func() []byte { return []byte(readinessScript) }}
}

// readyCalls are the calls of the handler up to the shell, in order.
func readyCalls(name string) []orbstacktest.Call {
	return []orbstacktest.Call{
		{Op: orbstacktest.OpState, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpActiveMounts, Name: name},
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: readinessScript},
	}
}

func TestHandleOpensTheShellAfterEveryCheck(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	got, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

	if err != nil || got != (Output{Name: name, Dir: dir}) {
		t.Fatalf("Handle = %+v, %v, want the shell of %s with status 0", got, err, name)
	}
	want := append(readyCalls(name), orbstacktest.Call{Op: orbstacktest.OpShell, Name: name, Dir: dir})
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

func TestHandlePassesTheStatusOfTheShellThrough(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.ShellStatus = 3

	got, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

	if err != nil || got != (Output{Name: name, Dir: dir, Status: 3}) {
		t.Errorf("Handle = %+v, %v, want status 3", got, err)
	}
}

func TestHandleOpensTheShellInTheCanonicalDirectory(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	link := filepath.Join(t.TempDir(), "link")
	symlinkTo(t, dir, link)

	got, err := handler(fake).Handle(context.Background(), Input{Dir: link})

	if err != nil || got.Name != name || got.Dir != dir {
		t.Fatalf("Handle(%q) = %+v, %v, want %s for %s", link, got, err, name, dir)
	}
	if last := fake.Calls[len(fake.Calls)-1]; last != (orbstacktest.Call{Op: orbstacktest.OpShell, Name: name, Dir: dir}) {
		t.Errorf("last call = %+v, want the shell in %s", last, dir)
	}
}

// TestHandleRefusesADirectoryWithoutASandbox covers a directory without a
// machine and a subdirectory of a project: the lookup is exact.
func TestHandleRefusesADirectoryWithoutASandbox(t *testing.T) {
	dir, name := project(t)
	sub := filepath.Join(dir, "sub")
	mkdir(t, sub)
	for _, test := range []struct{ name, dir string }{{name: "no machine", dir: t.TempDir()}, {name: "subdirectory", dir: sub}} {
		t.Run(test.name, func(t *testing.T) {
			fake := withMachine(name, dir, orbstacktest.StateRunning)

			_, err := handler(fake).Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, sandbox.ErrNoSandbox) || !strings.Contains(err.Error(), `run "orbit init" here, or run "orbit shell" in the directory where you ran "orbit init"`) {
				t.Errorf("Handle(%q) = %v, want %v naming orbit init", test.dir, err, sandbox.ErrNoSandbox)
			}
			if len(fake.Calls) != 1 || fake.Calls[0].Op != orbstacktest.OpState {
				t.Errorf("calls = %v, want only the state", fake.Calls)
			}
		})
	}
}

// TestHandleRefusesASandboxThatIsNotRunning checks that the handler runs no
// command after the state for any state but running, also a hostile one,
// and that a stopped machine stays stopped.
func TestHandleRefusesASandboxThatIsNotRunning(t *testing.T) {
	for _, state := range []string{orbstacktest.StateStopped, "starting", "stopping", "creating", "Running", "running ", "running\nrunning"} {
		t.Run(state, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, state)

			_, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

			want := "sandbox " + name + " is " + state + `: run "orbit start" in ` + dir
			if !errors.Is(err, ErrNotRunning) || err.Error() != want {
				t.Errorf("Handle in state %q = %v, want %q", state, err, want)
			}
			if !slices.Equal(fake.Calls, []orbstacktest.Call{{Op: orbstacktest.OpState, Name: name}}) {
				t.Errorf("calls = %v, want only the state", fake.Calls)
			}
			if fake.States[name] != state {
				t.Errorf("state after Handle = %q, want %q", fake.States[name], state)
			}
		})
	}
}

func TestHandleRefusesAMountMismatchBeforeTheReadinessCheck(t *testing.T) {
	dir, name := project(t)
	tests := []struct {
		name      string
		configure func(fake *orbstacktest.Fake)
		wantCalls int
	}{
		{name: "another directory configured", configure: func(fake *orbstacktest.Fake) { fake.Machines[name] = map[string]string{dir: "/Users/dev"} }, wantCalls: 2},
		{name: "a second mount configured", configure: func(fake *orbstacktest.Fake) { fake.Machines[name]["/Users/dev/.ssh"] = "/Users/dev/.ssh" }, wantCalls: 2},
		{name: "another directory in the kernel", configure: func(fake *orbstacktest.Fake) { fake.Active[name] = map[string]string{dir: "/Users/dev/other"} }, wantCalls: 3},
		{name: "no mount in the kernel", configure: func(fake *orbstacktest.Fake) { fake.Active[name] = map[string]string{} }, wantCalls: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := withMachine(name, dir, orbstacktest.StateRunning)
			test.configure(fake)

			_, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, sandbox.ErrMountMismatch) {
				t.Errorf("Handle = %v, want %v", err, sandbox.ErrMountMismatch)
			}
			if !slices.Equal(fake.Calls, readyCalls(name)[:test.wantCalls]) {
				t.Errorf("calls = %v, want %v", fake.Calls, readyCalls(name)[:test.wantCalls])
			}
		})
	}
}

// TestHandleRefusesASandboxThatIsNotReady checks that ErrNotProvisioned
// wraps the error of the readiness check, so that errors.Is finds both,
// names orbit init, and opens no shell.
func TestHandleRefusesASandboxThatIsNotReady(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.RunAsRootErr = errors.New("tinyproxy.service is not active")

	_, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

	want := "sandbox " + name + " is not ready: tinyproxy.service is not active; run \"orbit init\" in " + dir + " to provision it again"
	if !errors.Is(err, ErrNotProvisioned) || !errors.Is(err, fake.RunAsRootErr) || err.Error() != want {
		t.Errorf("Handle = %v, want %q wrapping both errors", err, want)
	}
	if !slices.Equal(fake.Calls, readyCalls(name)) {
		t.Errorf("calls = %v, want no shell after %v", fake.Calls, readyCalls(name))
	}
}

func TestHandleReportsAnErrorOfEachStep(t *testing.T) {
	dir, name := project(t)
	failure := errors.New("orb failed")
	tests := []struct {
		name string
		fail func(fake *orbstacktest.Fake)
		want string
	}{
		{name: "state", fail: func(fake *orbstacktest.Fake) { fake.StateErr = failure }, want: "state of sandbox " + name},
		{name: "mounts", fail: func(fake *orbstacktest.Fake) { fake.MountsErr = failure }, want: "list sandboxes"},
		{name: "active mounts", fail: func(fake *orbstacktest.Fake) { fake.ActiveMountsErr = failure }, want: "read the mounts of sandbox " + name},
		{name: "shell", fail: func(fake *orbstacktest.Fake) { fake.ShellErr = failure }, want: "shell in sandbox " + name},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := withMachine(name, dir, orbstacktest.StateRunning)
			test.fail(fake)

			got, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, failure) || !strings.Contains(err.Error(), test.want) || got != (Output{}) {
				t.Errorf("Handle = %+v, %v, want %v naming %q", got, err, failure, test.want)
			}
			if test.name != "shell" && slices.ContainsFunc(fake.Calls, func(call orbstacktest.Call) bool { return call.Op == orbstacktest.OpShell }) {
				t.Errorf("calls = %v, want no shell", fake.Calls)
			}
		})
	}
}

func TestHandleRefusesAPathThatIsNotADirectory(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	if _, err := handler(fake).Handle(context.Background(), Input{Dir: filepath.Join(dir, "missing")}); err == nil {
		t.Error("Handle of a missing directory succeeded, want an error")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

// TestHandleQuotesAHostileDirectoryWithoutASandbox pins that the error for a
// directory without a sandbox cannot write an escape sequence to the terminal.
func TestHandleQuotesAHostileDirectoryWithoutASandbox(t *testing.T) {
	hostile := filepath.Join(t.TempDir(), "a\x1b[2K\rb")
	if err := os.Mkdir(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := realpath.Resolve(hostile)
	if err != nil {
		t.Fatal(err)
	}

	_, err = handler(withMachine("orbit-other-1111111111111111", "/x", orbstacktest.StateRunning)).Handle(context.Background(), Input{Dir: dir})

	want := "no sandbox for " + strconv.Quote(dir) + `: run "orbit init" here, or run "orbit shell" in the directory where you ran "orbit init"`
	if err == nil || err.Error() != want || strings.ContainsAny(err.Error(), "\x1b\r") || !errors.Is(err, sandbox.ErrNoSandbox) {
		t.Errorf("Handle = %v, want %v as %q", err, sandbox.ErrNoSandbox, want)
	}
}

// hostileProject returns the canonical project directory whose name holds an
// escape sequence and a line break, and the name of its machine.
func hostileProject(t *testing.T) (string, string) {
	t.Helper()
	hostile := filepath.Join(t.TempDir(), "a\x1b[2K\nb")
	if err := os.Mkdir(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := realpath.Resolve(hostile)
	if err != nil {
		t.Fatal(err)
	}
	return dir, sandbox.MachineName(dir)
}

// TestHandleQuotesAHostileDirectoryInTheNotRunningError pins that the error
// for a sandbox that does not run cannot write an escape sequence or a line
// break to the terminal (#151).
func TestHandleQuotesAHostileDirectoryInTheNotRunningError(t *testing.T) {
	dir, name := hostileProject(t)
	fake := withMachine(name, dir, orbstacktest.StateStopped)

	_, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

	want := "sandbox " + name + " is stopped: " + ErrNotRunning.Error() + " in " + strconv.Quote(dir)
	if !errors.Is(err, ErrNotRunning) || err.Error() != want || strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("Handle = %q, want %q", err, want)
	}
}

// TestHandleQuotesAHostileDirectoryInTheNotProvisionedError pins that the
// error for a sandbox that is not ready cannot write an escape sequence or a
// line break to the terminal (#151).
func TestHandleQuotesAHostileDirectoryInTheNotProvisionedError(t *testing.T) {
	dir, name := hostileProject(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.RunAsRootErr = errors.New("tinyproxy.service is not active")

	_, err := handler(fake).Handle(context.Background(), Input{Dir: dir})

	want := "sandbox " + name + " is not ready: tinyproxy.service is not active; run \"orbit init\" in " + strconv.Quote(dir) + " to provision it again"
	if !errors.Is(err, ErrNotProvisioned) || err.Error() != want || strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("Handle = %q, want %q", err, want)
	}
}
