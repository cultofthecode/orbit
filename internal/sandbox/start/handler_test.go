package start

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// relocatingVariables are the environment variables that move a protected
// location. TestMain unsets them, so that the environment of the developer
// does not change what a test protects.
var relocatingVariables = []string{"CLAUDE_CONFIG_DIR", "GH_CONFIG_DIR", "GNUPGHOME", "XDG_CONFIG_HOME", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"}

func TestMain(m *testing.M) {
	for _, variable := range relocatingVariables {
		if err := os.Unsetenv(variable); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

const (
	bootScript   = "boot check\n"
	markerScript = "marker check\n"
	otherMachine = "orbit-other-0123456789abcdef"
)

// sandboxHome is a home directory of the test with the project directory
// work/api in it, and the machine name of that directory.
type sandboxHome struct {
	home, dir, name string
}

// newSandboxHome makes a home directory with work/api, .ssh, and .claude in
// it and makes it the home directory of the environment of the test.
func newSandboxHome(t *testing.T) sandboxHome {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"work/api", ".ssh", ".claude/projects"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "work", "api")
	return sandboxHome{home: home, dir: dir, name: sandbox.MachineName(dir)}
}

// fake returns a fake with the machine of the project directory in state and
// a running machine of another directory.
func (s sandboxHome) fake(state string) *orbstacktest.Fake {
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[s.name] = map[string]string{s.dir: s.dir}
	fake.States[s.name] = state
	fake.Machines[otherMachine] = map[string]string{"/srv/other": filepath.Join(s.home, "other")}
	return fake
}

// handler returns a handler on machines for the user of the test with the
// home directory of s.
func (s sandboxHome) handler(machines Machines) Handler {
	entry := &user.User{Uid: strconv.Itoa(os.Getuid()), HomeDir: s.home}
	return Handler{
		Machines:          machines,
		CurrentUser:       func() (*user.User, error) { return entry, nil },
		BootCheckScript:   func() []byte { return []byte(bootScript) },
		MarkerCheckScript: func() []byte { return []byte(markerScript) },
	}
}

// startCalls returns the calls of a start of the machine name up to and
// including the operation last.
func startCalls(name, last string) []orbstacktest.Call {
	calls := []orbstacktest.Call{
		{Op: orbstacktest.OpState, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpStart, Name: name},
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: bootScript},
		{Op: orbstacktest.OpActiveMounts, Name: name},
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: markerScript},
	}
	for i, call := range calls {
		if call.Op == last || (last == bootScript && call.Script == bootScript) {
			return calls[:i+1]
		}
	}
	return calls
}

// stateOf returns the state of the machine name in fake.
func stateOf(fake *orbstacktest.Fake, name string) string {
	state, _ := fake.State(context.Background(), name)
	fake.Calls = fake.Calls[:len(fake.Calls)-1]
	return state
}

func TestHandleStartsAStoppedSandbox(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateStopped)

	got, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

	if err != nil || got != (Output{Name: s.name, Dir: s.dir}) {
		t.Fatalf("Handle = %+v, %v, want %s started", got, err, s.name)
	}
	if want := startCalls(s.name, ""); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls =\n%v, want\n%v", fake.Calls, want)
	}
	if state := stateOf(fake, s.name); state != orbstacktest.StateRunning {
		t.Errorf("state = %q, want %q", state, orbstacktest.StateRunning)
	}
}

func TestHandleKeepsARunningSandbox(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateRunning)

	got, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

	if err != nil || got != (Output{Name: s.name, Dir: s.dir, AlreadyRunning: true}) {
		t.Fatalf("Handle = %+v, %v, want %s already running", got, err, s.name)
	}
	if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: s.name}}; !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want only %v", fake.Calls, want)
	}
}

func TestHandleRefusesADirectoryWithoutASandbox(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateStopped)
	delete(fake.Machines, s.name)

	got, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

	if !errors.Is(err, sandbox.ErrNoSandbox) || got != (Output{}) {
		t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrNoSandbox)
	}
	if !strings.Contains(err.Error(), s.dir) || !strings.Contains(err.Error(), `"orbit init"`) {
		t.Errorf("error = %q, want the directory and orbit init named", err)
	}
	if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: s.name}}; !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want only %v", fake.Calls, want)
	}
}

func TestHandleRefusesOtherStates(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "creating", "deleting", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s := newSandboxHome(t)
			fake := s.fake(state)

			got, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

			if !errors.Is(err, sandbox.ErrUnexpectedState) || got != (Output{}) {
				t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrUnexpectedState)
			}
			if want := "sandbox " + s.name + " is " + state + "; wait and try again"; err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: s.name}}; !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want only %v", fake.Calls, want)
			}
		})
	}
}

func TestHandleRefusesAProjectDirectoryThatTheRulesRefuseInEveryState(t *testing.T) {
	tests := []struct {
		name   string
		layout func(t *testing.T, s sandboxHome) string
		want   error
	}{
		{name: "home directory", layout: func(_ *testing.T, s sandboxHome) string { return s.home }, want: sandbox.ErrHomeDir},
		{name: "protected directory", layout: func(_ *testing.T, s sandboxHome) string { return filepath.Join(s.home, ".ssh") }, want: sandbox.ErrProtectedDir},
		{name: "not a directory", layout: func(t *testing.T, s sandboxHome) string {
			file := filepath.Join(s.dir, "file")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return file
		}, want: sandbox.ErrNotDirectory},
		{name: "hard-linked protected file", layout: func(t *testing.T, s sandboxHome) string {
			file := filepath.Join(s.home, ".claude.json")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(file, filepath.Join(s.home, "claude.json.bak")); err != nil {
				t.Fatal(err)
			}
			return s.dir
		}, want: sandbox.ErrHardLinkedFile},
		{name: "relocated location that cannot be protected", layout: func(t *testing.T, s sandboxHome) string {
			t.Setenv("GNUPGHOME", "")
			return s.dir
		}, want: sandbox.ErrRelocatedPath},
	}
	for _, test := range tests {
		for _, state := range []string{orbstacktest.StateRunning, orbstacktest.StateStopped} {
			t.Run(test.name+", "+state, func(t *testing.T) {
				s := newSandboxHome(t)
				dir := test.layout(t, s)
				fake := s.fake(state)

				got, err := s.handler(fake).Handle(context.Background(), Input{Dir: dir})

				if !errors.Is(err, test.want) || got != (Output{}) {
					t.Errorf("Handle(%q) = %+v, %v, want %v", dir, got, err, test.want)
				}
				if len(fake.Calls) != 0 {
					t.Errorf("calls = %v, want none", fake.Calls)
				}
			})
		}
	}
}

func TestHandleRefusesAFailedHostCheckBeforeTheStart(t *testing.T) {
	tests := []struct {
		name   string
		layout func(t *testing.T, s sandboxHome, fake *orbstacktest.Fake)
		want   error
	}{
		{name: "configured mount of another directory", layout: func(_ *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			fake.Machines[s.name] = map[string]string{s.dir: filepath.Join(s.home, "work")}
		}, want: sandbox.ErrMountMismatch},
		{name: "second configured mount", layout: func(_ *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			fake.Machines[s.name] = map[string]string{s.dir: s.dir, "/mnt": s.home}
		}, want: sandbox.ErrMountMismatch},
		{name: "another sandbox contains the project", layout: func(_ *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			fake.Machines[otherMachine] = map[string]string{"/srv": filepath.Join(s.home, "work")}
		}, want: sandbox.ErrNestedProjectDir},
		{name: "project contains another sandbox", layout: func(_ *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			fake.Machines[otherMachine] = map[string]string{"/srv": filepath.Join(s.dir, "sub")}
		}, want: sandbox.ErrContainsProjectDir},
		{name: "stored path of another sandbox not clean", layout: func(_ *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			fake.Machines[otherMachine] = map[string]string{"/srv": s.home + "/other/../work/api"}
		}, want: sandbox.ErrPathNotClean},
		{name: "stored path of another sandbox through a symlink", layout: func(t *testing.T, s sandboxHome, fake *orbstacktest.Fake) {
			moved := filepath.Join(s.home, "w", "b")
			if err := os.MkdirAll(moved, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, filepath.Join(s.home, "b")); err != nil {
				t.Fatal(err)
			}
			fake.Machines[otherMachine] = map[string]string{"/srv": filepath.Join(s.home, "b")}
		}, want: sandbox.ErrLinkedSandboxDir},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newSandboxHome(t)
			fake := s.fake(orbstacktest.StateStopped)
			test.layout(t, s, fake)

			_, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

			if !errors.Is(err, test.want) {
				t.Errorf("Handle = %v, want %v", err, test.want)
			}
			if want := startCalls(s.name, orbstacktest.OpMounts); !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want %v", fake.Calls, want)
			}
			if state := stateOf(fake, s.name); state != orbstacktest.StateStopped {
				t.Errorf("state = %q, want it stopped", state)
			}
		})
	}
}

// swapOnMounts is a fake that moves the parent of the project directory away
// and leaves a symlink in its place when the handler lists the mounts: a
// host-side move between the resolution of the current directory and the
// start.
type swapOnMounts struct {
	*orbstacktest.Fake
	home string
}

func (m swapOnMounts) Mounts(ctx context.Context) (map[string]map[string]string, error) {
	work := filepath.Join(m.home, "work")
	moved := filepath.Join(m.home, "moved")
	if err := os.Rename(work, moved); err != nil {
		return nil, err
	}
	if err := os.Symlink(moved, work); err != nil {
		return nil, err
	}
	return m.Fake.Mounts(ctx)
}

func TestHandleRefusesAnOwnStoredPathThroughASymlink(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateStopped)

	_, err := s.handler(swapOnMounts{Fake: fake, home: s.home}).Handle(context.Background(), Input{Dir: s.dir})

	if !errors.Is(err, sandbox.ErrLinkedProjectDir) {
		t.Fatalf("Handle = %v, want %v", err, sandbox.ErrLinkedProjectDir)
	}
	if want := startCalls(s.name, orbstacktest.OpMounts); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

// failingBoot is a fake whose boot check fails with err and whose Stop
// records whether its context was live.
type failingBoot struct {
	*orbstacktest.Fake
	bootErr     error
	cancel      context.CancelFunc
	cancelStart bool
	stopCtxErr  *error
}

func (f failingBoot) Start(ctx context.Context, name string) error {
	if f.cancelStart {
		f.cancel()
		f.Calls = append(f.Calls, orbstacktest.Call{Op: orbstacktest.OpStart, Name: name})
		f.States[name] = orbstacktest.StateRunning
		return ctx.Err()
	}
	return f.Fake.Start(ctx, name)
}

func (f failingBoot) RunAsRoot(ctx context.Context, name string, script []byte) error {
	err := f.Fake.RunAsRoot(ctx, name, script)
	if string(script) != bootScript || f.bootErr == nil {
		return err
	}
	if f.cancel != nil {
		f.cancel()
	}
	return f.bootErr
}

func (f failingBoot) Stop(ctx context.Context, name string) error {
	if f.stopCtxErr != nil {
		*f.stopCtxErr = ctx.Err()
	}
	return f.Fake.Stop(ctx, name)
}

func TestHandleStopsTheSandboxAgainWhenACheckAfterTheStartFails(t *testing.T) {
	unitDown := errors.New("orb -m: exit status 1: tinyproxy.service is not active")
	orbFailed := errors.New("orb start: exit status 1")
	tests := []struct {
		name     string
		machines func(*orbstacktest.Fake, sandboxHome) Machines
		last     string
		want     []error
		text     string
	}{
		{name: "start fails", machines: func(f *orbstacktest.Fake, _ sandboxHome) Machines {
			f.StartErr = orbFailed
			return f
		}, last: orbstacktest.OpStart, want: []error{orbFailed}},
		{name: "boot check fails", machines: func(f *orbstacktest.Fake, _ sandboxHome) Machines {
			return failingBoot{Fake: f, bootErr: unitDown}
		}, last: bootScript, want: []error{unitDown}, text: "tinyproxy.service is not active"},
		{name: "kernel mounts another directory", machines: func(f *orbstacktest.Fake, s sandboxHome) Machines {
			f.Active[s.name] = map[string]string{s.dir: filepath.Join(s.home, ".ssh")}
			return f
		}, last: orbstacktest.OpActiveMounts, want: []error{sandbox.ErrMountMismatch}},
		{name: "kernel mounts cannot be read", machines: func(f *orbstacktest.Fake, _ sandboxHome) Machines {
			f.ActiveMountsErr = orbFailed
			return f
		}, last: orbstacktest.OpActiveMounts, want: []error{orbFailed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newSandboxHome(t)
			fake := s.fake(orbstacktest.StateStopped)

			_, err := s.handler(test.machines(fake, s)).Handle(context.Background(), Input{Dir: s.dir})

			for _, want := range append(test.want, ErrBoundaryDown) {
				if !errors.Is(err, want) {
					t.Errorf("Handle = %v, want %v", err, want)
				}
			}
			if err == nil || !strings.HasSuffix(err.Error(), "; the sandbox was stopped again") || !strings.Contains(err.Error(), test.text) {
				t.Errorf("Handle = %v, want %q and that the sandbox was stopped again", err, test.text)
			}
			want := append(startCalls(s.name, test.last), orbstacktest.Call{Op: orbstacktest.OpStop, Name: s.name})
			if !slices.Equal(fake.Calls, want) {
				t.Errorf("calls =\n%v, want\n%v", fake.Calls, want)
			}
			if state := stateOf(fake, s.name); state != orbstacktest.StateStopped {
				t.Errorf("state = %q, want it stopped again", state)
			}
		})
	}
}

func TestHandleStopsTheSandboxAgainAfterCancellation(t *testing.T) {
	for _, cancelStart := range []bool{true, false} {
		t.Run("cancelled in start "+strconv.FormatBool(cancelStart), func(t *testing.T) {
			s := newSandboxHome(t)
			fake := s.fake(orbstacktest.StateStopped)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopCtxErr := errors.New("Stop was not called")
			machines := failingBoot{Fake: fake, cancel: cancel, cancelStart: cancelStart, stopCtxErr: &stopCtxErr}
			if !cancelStart {
				machines.bootErr = context.Canceled
			}

			_, err := s.handler(machines).Handle(ctx, Input{Dir: s.dir})

			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrBoundaryDown) {
				t.Errorf("Handle = %v, want %v and %v", err, context.Canceled, ErrBoundaryDown)
			}
			if stopCtxErr != nil {
				t.Errorf("context of Stop = %v, want a live context", stopCtxErr)
			}
			if state := stateOf(fake, s.name); state != orbstacktest.StateStopped {
				t.Errorf("state = %q, want it stopped again", state)
			}
		})
	}
}

func TestHandleReportsBothErrorsWhenTheStopAgainFails(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateStopped)
	unitDown := errors.New("tinyproxy.service is not active")
	stopFailed := errors.New("orb stop: exit status 1")
	fake.StopErr = stopFailed

	_, err := s.handler(failingBoot{Fake: fake, bootErr: unitDown}).Handle(context.Background(), Input{Dir: s.dir})

	for _, want := range []error{ErrBoundaryDown, unitDown, stopFailed} {
		if !errors.Is(err, want) {
			t.Errorf("Handle = %v, want %v", err, want)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "may still run") || !strings.Contains(err.Error(), `"orbit stop" in `+s.dir) {
		t.Errorf("Handle = %v, want that the sandbox may still run and orbit stop named", err)
	}
}

func TestHandleLeavesTheSandboxRunningWhenOnlyTheMarkerFails(t *testing.T) {
	s := newSandboxHome(t)
	fake := s.fake(orbstacktest.StateStopped)
	markerFailed := errors.New("/etc/orbit/provisioned does not hold provisioning version 1")

	_, err := s.handler(failingMarker{Fake: fake, err: markerFailed}).Handle(context.Background(), Input{Dir: s.dir})

	if !errors.Is(err, ErrNotProvisioned) || !errors.Is(err, markerFailed) || errors.Is(err, ErrBoundaryDown) {
		t.Fatalf("Handle = %v, want %v and not %v", err, ErrNotProvisioned, ErrBoundaryDown)
	}
	if want := `run "orbit init" in ` + s.dir + " to provision it again"; !strings.Contains(err.Error(), want) {
		t.Errorf("Handle = %v, want %q", err, want)
	}
	if want := startCalls(s.name, ""); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls =\n%v, want\n%v", fake.Calls, want)
	}
	if state := stateOf(fake, s.name); state != orbstacktest.StateRunning {
		t.Errorf("state = %q, want it running", state)
	}
}

// failingMarker is a fake whose marker check fails with err.
type failingMarker struct {
	*orbstacktest.Fake
	err error
}

func (f failingMarker) RunAsRoot(ctx context.Context, name string, script []byte) error {
	if err := f.Fake.RunAsRoot(ctx, name, script); err != nil || string(script) != markerScript {
		return err
	}
	return f.err
}

func TestHandleReportsOrbStackFailuresBeforeTheStart(t *testing.T) {
	failure := errors.New("orb failed")
	tests := []struct {
		name string
		set  func(*orbstacktest.Fake)
		last string
	}{
		{name: "state", set: func(f *orbstacktest.Fake) { f.StateErr = failure }, last: orbstacktest.OpState},
		{name: "mounts", set: func(f *orbstacktest.Fake) { f.MountsErr = failure }, last: orbstacktest.OpMounts},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newSandboxHome(t)
			fake := s.fake(orbstacktest.StateStopped)
			test.set(fake)

			_, err := s.handler(fake).Handle(context.Background(), Input{Dir: s.dir})

			if !errors.Is(err, failure) {
				t.Errorf("Handle = %v, want %v", err, failure)
			}
			if want := startCalls(s.name, test.last); !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want %v", fake.Calls, want)
			}
		})
	}
}
