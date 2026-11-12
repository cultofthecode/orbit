package destroy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

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

// recorder is a Confirm that records its targets and returns result.
type recorder struct {
	targets []Target
	result  error
	onCall  func()
}

func (r *recorder) confirm(_ context.Context, target Target) error {
	r.targets = append(r.targets, target)
	if r.onCall != nil {
		r.onCall()
	}
	return r.result
}

func calls(name string, ops ...string) []orbstacktest.Call {
	var want []orbstacktest.Call
	for _, op := range ops {
		want = append(want, orbstacktest.Call{Op: op, Name: name})
	}
	return want
}

const (
	opState  = orbstacktest.OpState
	opDelete = orbstacktest.OpDelete
)

func TestHandleDestroysARunningOrStoppedSandbox(t *testing.T) {
	for _, state := range []string{orbstacktest.StateRunning, orbstacktest.StateStopped} {
		t.Run(state, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, state)
			ask := &recorder{}

			got, err := Handler{Machines: fake, Confirm: ask.confirm}.Handle(context.Background(), Input{Dir: dir})

			if err != nil || got != (Output{Name: name, Dir: dir}) {
				t.Fatalf("Handle = %+v, %v, want %s destroyed", got, err, name)
			}
			if want := calls(name, opState, opState, opDelete, opState); !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want %v", fake.Calls, want)
			}
			if want := []Target{{Name: name, Dir: dir, Running: state == orbstacktest.StateRunning}}; !slices.Equal(ask.targets, want) {
				t.Errorf("confirmed %v, want %v", ask.targets, want)
			}
			if _, exists := fake.Machines[name]; exists {
				t.Error("machine still exists")
			}
			if _, exists := fake.Machines[otherMachine]; !exists {
				t.Error("the other machine was deleted")
			}
		})
	}
}

func TestHandleAsksBetweenTheTwoStateReads(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	var seen []orbstacktest.Call
	ask := &recorder{}
	ask.onCall = func() { seen = slices.Clone(fake.Calls) }

	if _, err := (Handler{Machines: fake, Confirm: ask.confirm}).Handle(context.Background(), Input{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if want := calls(name, opState); !slices.Equal(seen, want) {
		t.Errorf("calls before Confirm = %v, want %v", seen, want)
	}
}

func TestHandleResolvesTheDirectory(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateStopped)
	link := t.TempDir() + "/link"
	if err := osSymlink(dir, link); err != nil {
		t.Fatal(err)
	}

	got, err := Handler{Machines: fake, Confirm: (&recorder{}).confirm}.Handle(context.Background(), Input{Dir: link})

	if err != nil || got.Name != name || got.Dir != dir {
		t.Errorf("Handle(%q) = %+v, %v, want %s for %s", link, got, err, name, dir)
	}
}

func TestHandleRefusesADirectoryWithoutASandbox(t *testing.T) {
	dir, name := project(t)
	fake := withMachine("orbit-other-1111111111111111", "/x", orbstacktest.StateRunning)
	ask := &recorder{}

	got, err := Handler{Machines: fake, Confirm: ask.confirm}.Handle(context.Background(), Input{Dir: dir})

	if !errors.Is(err, sandbox.ErrNoSandbox) || got != (Output{}) {
		t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrNoSandbox)
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), `"orbit init"`) {
		t.Errorf("error = %q, want the directory and orbit init named", err)
	}
	if want := calls(name, opState); !slices.Equal(fake.Calls, want) || len(ask.targets) != 0 {
		t.Errorf("calls = %v, confirmed %v, want only %v", fake.Calls, ask.targets, want)
	}
}

func TestHandleRefusesOtherStates(t *testing.T) {
	for _, state := range []string{"starting", "stopping", "creating", "deleting", "unknown"} {
		t.Run(state, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, state)
			ask := &recorder{}

			got, err := Handler{Machines: fake, Confirm: ask.confirm}.Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, sandbox.ErrUnexpectedState) || got != (Output{}) {
				t.Fatalf("Handle = %+v, %v, want %v", got, err, sandbox.ErrUnexpectedState)
			}
			if want := "sandbox " + name + " is " + state + "; wait and try again"; err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if want := calls(name, opState); !slices.Equal(fake.Calls, want) || len(ask.targets) != 0 {
				t.Errorf("calls = %v, confirmed %v, want only %v", fake.Calls, ask.targets, want)
			}
		})
	}
}

func TestHandleDeletesNothingUnlessConfirmed(t *testing.T) {
	failure := errors.New("read failed")
	for _, answer := range []error{ErrNotConfirmed, failure} {
		dir, name := project(t)
		fake := withMachine(name, dir, orbstacktest.StateRunning)

		_, err := Handler{Machines: fake, Confirm: (&recorder{result: answer}).confirm}.Handle(context.Background(), Input{Dir: dir})

		if !errors.Is(err, answer) {
			t.Errorf("Handle = %v, want %v", err, answer)
		}
		if want := calls(name, opState); !slices.Equal(fake.Calls, want) {
			t.Errorf("calls = %v, want only %v", fake.Calls, want)
		}
		if _, exists := fake.Machines[name]; !exists {
			t.Error("machine was deleted without a confirmation")
		}
	}
}

func TestHandleRechecksTheStateAfterTheConfirmation(t *testing.T) {
	tests := []struct {
		name  string
		after func(*orbstacktest.Fake, string)
		want  error
	}{
		{name: "gone", after: func(f *orbstacktest.Fake, n string) { delete(f.Machines, n) }, want: sandbox.ErrNoSandbox},
		{name: "in transition", after: func(f *orbstacktest.Fake, n string) { f.States[n] = "stopping" }, want: sandbox.ErrUnexpectedState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, name := project(t)
			fake := withMachine(name, dir, orbstacktest.StateRunning)
			ask := &recorder{onCall: func() { test.after(fake, name) }}

			_, err := Handler{Machines: fake, Confirm: ask.confirm}.Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, test.want) {
				t.Fatalf("Handle = %v, want %v", err, test.want)
			}
			if want := calls(name, opState, opState); !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want %v", fake.Calls, want)
			}
		})
	}
}

func TestHandleDestroysAfterARunningToStoppedChange(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	ask := &recorder{onCall: func() { fake.States[name] = orbstacktest.StateStopped }}

	if _, err := (Handler{Machines: fake, Confirm: ask.confirm}).Handle(context.Background(), Input{Dir: dir}); err != nil {
		t.Fatalf("Handle = %v", err)
	}
}

// cancelledDelete is a fake whose Delete fails and deletes nothing.
type cancelledDelete struct {
	*orbstacktest.Fake
	errs []error
}

func (c *cancelledDelete) State(ctx context.Context, name string) (string, error) {
	c.errs = append(c.errs, ctx.Err())
	return c.Fake.State(ctx, name)
}

func TestHandleReadsOnceAfterAFailedDelete(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.DeleteErr = errors.New("orb failed")
	ctx, cancel := context.WithCancel(context.Background())
	ask := &recorder{onCall: cancel}
	wrapped := &cancelledDelete{Fake: fake}

	_, err := Handler{Machines: wrapped, Confirm: ask.confirm}.Handle(ctx, Input{Dir: dir})

	if !errors.Is(err, fake.DeleteErr) {
		t.Fatalf("Handle = %v, want %v", err, fake.DeleteErr)
	}
	for _, want := range []string{name, "partly deleted", `run "orbit destroy" again`, "is running"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if want := calls(name, opState, opState, opDelete, opState); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
	if last := wrapped.errs[len(wrapped.errs)-1]; last != nil {
		t.Errorf("the read after the delete got a cancelled context: %v", last)
	}
}

// failingRead is a fake whose State fails from the third call on.
type failingRead struct {
	*orbstacktest.Fake
	reads int
}

func (f *failingRead) State(ctx context.Context, name string) (string, error) {
	f.reads++
	if f.reads >= 3 {
		f.Calls = append(f.Calls, orbstacktest.Call{Op: opState, Name: name})
		return "", errors.New("list failed")
	}
	return f.Fake.State(ctx, name)
}

func TestHandleReportsBothErrorsWhenTheReadFailsToo(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateStopped)
	fake.DeleteErr = errors.New("orb failed")

	_, err := Handler{Machines: &failingRead{Fake: fake}, Confirm: (&recorder{}).confirm}.Handle(context.Background(), Input{Dir: dir})

	if err == nil || !strings.Contains(err.Error(), "orb failed") || !strings.Contains(err.Error(), "list failed") || !strings.Contains(err.Error(), "partly deleted") {
		t.Fatalf("Handle = %v, want both errors and the advice", err)
	}
	if want := calls(name, opState, opState, opDelete, opState); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want exactly one read after the delete", fake.Calls)
	}
}

func TestHandleReportsAFailedReadAfterASuccessfulDelete(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateStopped)

	_, err := Handler{Machines: &failingRead{Fake: fake}, Confirm: (&recorder{}).confirm}.Handle(context.Background(), Input{Dir: dir})

	if err == nil || !strings.Contains(err.Error(), "list failed") || !strings.Contains(err.Error(), "partly deleted") {
		t.Errorf("Handle = %v, want the read error and the advice", err)
	}
}

// keepMachine is a fake whose Delete succeeds and leaves the machine in
// state.
type keepMachine struct {
	*orbstacktest.Fake
	state string
}

func (k keepMachine) Delete(_ context.Context, name string) error {
	k.Calls = append(k.Calls, orbstacktest.Call{Op: opDelete, Name: name})
	k.States[name] = k.state
	return nil
}

func TestHandleFailsWhenTheSandboxIsNotGoneAfterTheDelete(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	wrapped := keepMachine{Fake: fake, state: "deleting"}

	_, err := Handler{Machines: wrapped, Confirm: (&recorder{}).confirm}.Handle(context.Background(), Input{Dir: dir})

	if err == nil || !strings.Contains(err.Error(), "is deleting after orb delete, not gone") || !strings.Contains(err.Error(), `run "orbit destroy" again`) {
		t.Errorf("Handle = %v, want an error that names the state", err)
	}
}

func TestHandleReportsStateFailures(t *testing.T) {
	dir, name := project(t)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.StateErr = errors.New("orb failed")
	ask := &recorder{}

	_, err := Handler{Machines: fake, Confirm: ask.confirm}.Handle(context.Background(), Input{Dir: dir})

	if !errors.Is(err, fake.StateErr) || !strings.Contains(err.Error(), name) || len(ask.targets) != 0 {
		t.Errorf("Handle = %v, want the error naming %s and no question", err, name)
	}
}
