package list

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// fakeMachines wraps the fake of OrbStack as the Machines of the slice, as
// cmd/orbit wraps the client of the adapter.
type fakeMachines struct {
	fake *orbstacktest.Fake
}

func (f fakeMachines) List(ctx context.Context) ([]Machine, error) {
	listed, err := f.fake.List(ctx)
	if err != nil {
		return nil, err
	}
	machines := make([]Machine, 0, len(listed))
	for _, l := range listed {
		machine := Machine{Name: l.Name, State: l.State}
		for _, m := range l.Mounts {
			machine.Mounts = append(machine.Mounts, sandbox.Mount{Source: m.Source, Destination: m.Destination})
		}
		machines = append(machines, machine)
	}
	return machines, nil
}

// withSandbox adds the sandbox of dir to fake in state.
func withSandbox(fake *orbstacktest.Fake, dir, state string) string {
	name := sandbox.MachineName(dir)
	fake.Machines[name] = map[string]string{dir: dir}
	fake.States[name] = state
	return name
}

func TestHandleListsNothingForNoMachine(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)

	got, err := Handler{Machines: fakeMachines{fake}}.Handle(context.Background(), Input{})

	if err != nil || len(got.Sandboxes) != 0 {
		t.Errorf("Handle = %+v, %v, want no sandbox", got, err)
	}
}

func TestHandleListsRunningAndStoppedSandboxesSortedByName(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	web := withSandbox(fake, "/Users/dev/web", orbstacktest.StateStopped)
	api := withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)

	got, err := Handler{Machines: fakeMachines{fake}}.Handle(context.Background(), Input{})

	want := []Sandbox{{Name: api, State: "running", Dir: "/Users/dev/api"}, {Name: web, State: "stopped", Dir: "/Users/dev/web"}}
	if !slices.Equal(got.Sandboxes, want) || err != nil {
		t.Errorf("Handle = %+v, %v, want %+v", got, err, want)
	}
}

func TestHandleRecordsOnlyListAndChangesNoState(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	web := withSandbox(fake, "/Users/dev/web", orbstacktest.StateStopped)

	if _, err := (Handler{Machines: fakeMachines{fake}}).Handle(context.Background(), Input{}); err != nil {
		t.Fatal(err)
	}

	if want := []orbstacktest.Call{{Op: orbstacktest.OpList}}; !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
	if fake.States[web] != orbstacktest.StateStopped {
		t.Errorf("state = %q, want %q", fake.States[web], orbstacktest.StateStopped)
	}
}

func TestHandleDropsForeignNames(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	api := withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)
	for _, name := range []string{"ubuntu", "xorbit-a", "Orbit-b", "orbit", "orbit-", ""} {
		fake.Listed = append(fake.Listed, orbstacktest.Machine{Name: name, State: "running"})
	}

	got, err := Handler{Machines: fakeMachines{fake}}.Handle(context.Background(), Input{})

	want := []Sandbox{{Name: api, State: "running", Dir: "/Users/dev/api"}}
	if !slices.Equal(got.Sandboxes, want) || err != nil {
		t.Errorf("Handle = %+v, %v, want %+v", got, err, want)
	}
}

func TestHandleKeepsUnreadableMachinesWithTheirReason(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	api := withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)
	fake.Listed = []orbstacktest.Machine{
		{Name: "orbit-manual", State: "stopped", Mounts: []orbstacktest.Mount{{Source: "/Users/dev/x", Destination: "/Users/dev/x"}}},
		{Name: "orbit-two", State: "running", Mounts: []orbstacktest.Mount{{Source: "/a", Destination: "/a"}, {Source: "/b", Destination: "/b"}}},
		{Name: "orbit-none", State: "running"},
		{Name: "orbit-nostate"},
	}

	got, err := Handler{Machines: fakeMachines{fake}}.Handle(context.Background(), Input{})

	if err != nil || len(got.Sandboxes) != 5 {
		t.Fatalf("Handle = %+v, %v, want 5 sandboxes", got, err)
	}
	if got.Sandboxes[0] != (Sandbox{Name: api, State: "running", Dir: "/Users/dev/api"}) {
		t.Errorf("first = %+v, want the readable sandbox with its directory", got.Sandboxes[0])
	}
	problems := map[string]string{
		"orbit-manual":  `project directory unknown: the name does not match "/Users/dev/x"`,
		"orbit-two":     "project directory unknown: 2 mounts",
		"orbit-none":    "project directory unknown: no mount",
		"orbit-nostate": "project directory unknown: no mount",
	}
	for _, s := range got.Sandboxes[1:] {
		if s.Dir != "" || s.Problem != problems[s.Name] {
			t.Errorf("%s = %+v, want no directory and problem %q", s.Name, s, problems[s.Name])
		}
	}
}

func TestHandleFailsWithoutAPartialListWhenTheListCannotBeRead(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)
	fake.ListErr = errors.New("machine list cannot be read")

	got, err := Handler{Machines: fakeMachines{fake}}.Handle(context.Background(), Input{})

	if !errors.Is(err, fake.ListErr) || err.Error() != "list sandboxes: machine list cannot be read" || len(got.Sandboxes) != 0 {
		t.Errorf("Handle = %+v, %v, want the wrapped error and no sandbox", got, err)
	}
}
