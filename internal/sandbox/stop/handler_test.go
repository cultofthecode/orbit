package stop

import (
	"context"
	"errors"
	"io/fs"
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
	fake.Machines["orbit-other-0123456789abcdef"] = map[string]string{"/other": "/other"}
	return fake
}

func TestHandleStopsARunningSandbox(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

	if err != nil || got != (Output{Name: name, Dir: dir}) {
		t.Fatalf("Handle = %+v, %v, want %s stopped", got, err, name)
	}
	want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpStop, Name: name}, {Op: orbstacktest.OpState, Name: name}}
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
	if fake.States[name] != orbstacktest.StateStopped {
		t.Errorf("state = %q, want %q", fake.States[name], orbstacktest.StateStopped)
	}
	if state, _ := fake.State(context.Background(), "orbit-other-0123456789abcdef"); state != orbstacktest.StateRunning {
		t.Errorf("state of the other machine = %q, want it running", state)
	}
}

func TestHandleResolvesTheDirectory(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	link := filepath.Join(t.TempDir(), "link")
	symlinkTo(t, dir, link)

	got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: link})

	if err != nil || got.Name != name || got.Dir != dir {
		t.Errorf("Handle(%q) = %+v, %v, want %s for %s", link, got, err, name, dir)
	}
}

func TestHandleKeepsAStoppedSandbox(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateStopped)

	got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

	if err != nil || got != (Output{Name: name, Dir: dir, AlreadyStopped: true}) {
		t.Fatalf("Handle = %+v, %v, want %s already stopped", got, err, name)
	}
	if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: name}}; !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want only %v", fake.Calls, want)
	}
}

func TestHandleRefusesADirectoryWithoutASandbox(t *testing.T) {
	dir, name := project(t)
	fake := withMachine("orbit-other-1111111111111111", "/x", orbstacktest.StateRunning)

	got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

	if !errors.Is(err, sandbox.ErrNoSandbox) || got != (Output{}) {
		t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrNoSandbox)
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), `"orbit init"`) {
		t.Errorf("error = %q, want the directory and orbit init named", err)
	}
	if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: name}}; !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want only %v", fake.Calls, want)
	}
}

func TestHandleRefusesOtherStates(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "creating", "deleting", "unknown"} {
		t.Run(state, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, state)

			got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, sandbox.ErrUnexpectedState) || got != (Output{}) {
				t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrUnexpectedState)
			}
			if want := "sandbox " + name + " is " + state + "; wait and try again"; err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if want := []orbstacktest.Call{{Op: orbstacktest.OpState, Name: name}}; !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want only %v", fake.Calls, want)
			}
		})
	}
}

// keepRunning is a fake whose Stop succeeds and changes nothing.
type keepRunning struct {
	*orbstacktest.Fake
}

func (k keepRunning) Stop(context.Context, string) error {
	return nil
}

func TestHandleFailsWhenTheSandboxIsNotStoppedAfterTheStop(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	_, err := Handler{Machines: keepRunning{fake}}.Handle(context.Background(), Input{Dir: dir})

	if err == nil || !strings.Contains(err.Error(), "is running after the stop") {
		t.Errorf("Handle = %v, want an error that names the state after the stop", err)
	}
}

func TestHandleReportsOrbStackFailures(t *testing.T) {
	failure := errors.New("orb failed")
	tests := []struct {
		name string
		set  func(*orbstacktest.Fake)
	}{
		{name: "state", set: func(f *orbstacktest.Fake) { f.StateErr = failure }},
		{name: "stop", set: func(f *orbstacktest.Fake) { f.StopErr = failure }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, orbstacktest.StateRunning)
			test.set(fake)

			got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, failure) || !strings.Contains(err.Error(), name) {
				t.Errorf("Handle = %+v, %v, want %v naming %s", got, err, failure, name)
			}
		})
	}
}

func TestHandleRefusesAMissingDirectoryBeforeItAsksOrbStack(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)

	_, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: filepath.Join(t.TempDir(), "gone")})

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Handle = %v, want %v", err, fs.ErrNotExist)
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
	fake := withMachine("orbit-other-1111111111111111", "/x", orbstacktest.StateRunning)

	_, err = Handler{Machines: fake}.Handle(context.Background(), Input{Dir: dir})

	want := "no sandbox for " + strconv.Quote(dir) + `: run "orbit init" here, or run this command in the directory where you ran "orbit init"`
	if err == nil || err.Error() != want || strings.ContainsAny(err.Error(), "\x1b\r") || !errors.Is(err, sandbox.ErrNoSandbox) {
		t.Errorf("Handle = %v, want %v as %q", err, sandbox.ErrNoSandbox, want)
	}
}
