package orbstacktest

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFakeCreatesAndFindsMachines(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()

	before, err := fake.Exists(ctx, "orbit-api-1d9b22c9")
	if err != nil || before {
		t.Fatalf("Exists before Create = %v, %v, want false", before, err)
	}
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	after, err := fake.Exists(ctx, "orbit-api-1d9b22c9")
	if err != nil || !after {
		t.Fatalf("Exists after Create = %v, %v, want true", after, err)
	}

	wantCalls := []Call{
		{Op: OpExists, Name: "orbit-api-1d9b22c9"},
		{Op: OpCreate, Name: "orbit-api-1d9b22c9", Dir: "/work/api", Forward: true, Distribution: "ubuntu"},
		{Op: OpExists, Name: "orbit-api-1d9b22c9"},
	}
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("Calls = %v, want %v", fake.Calls, wantCalls)
	}
	if mounts := fake.Machines["orbit-api-1d9b22c9"]; !reflect.DeepEqual(mounts, map[string]string{"/work/api": "/work/api"}) {
		t.Errorf("mounts = %v, want /work/api at its own path", mounts)
	}
}

func TestFakeReportsMountsAndDeletes(t *testing.T) {
	const name = "orbit-api-1d9b22c9"
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Create(ctx, name, "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	own := map[string]string{"/work/api": "/work/api"}

	mounts, err := fake.Mounts(ctx)
	if err != nil || !reflect.DeepEqual(mounts, map[string]map[string]string{name: own}) {
		t.Errorf("Mounts = %v, %v, want %v for %s", mounts, err, own, name)
	}
	mounts[name]["/work/api"] = "/changed"
	active, err := fake.ActiveMounts(ctx, name)
	if err != nil || !reflect.DeepEqual(active, own) {
		t.Errorf("ActiveMounts = %v, %v, want the configured mounts %v", active, err, own)
	}
	swapped := map[string]string{"/work/api": "/home/.ssh"}
	fake.Active[name] = swapped
	if active, err := fake.ActiveMounts(ctx, name); err != nil || !reflect.DeepEqual(active, swapped) {
		t.Errorf("ActiveMounts after a swap = %v, %v, want %v", active, err, swapped)
	}
	if err := fake.Delete(ctx, name); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if len(fake.Machines) != 0 || len(fake.Active) != 0 {
		t.Errorf("after Delete: Machines = %v, Active = %v, want none", fake.Machines, fake.Active)
	}
	wantCalls := []Call{
		{Op: OpCreate, Name: name, Dir: "/work/api", Forward: true, Distribution: "ubuntu"},
		{Op: OpMounts},
		{Op: OpActiveMounts, Name: name},
		{Op: OpActiveMounts, Name: name},
		{Op: OpDelete, Name: name},
	}
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("Calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestFakeRefusesMissingAndForeignMachines(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	fake.Machines["ubuntu"] = map[string]string{}
	tests := []struct {
		name    string
		machine string
		want    error
	}{
		{name: "missing machine", machine: "orbit-api-1d9b22c9", want: ErrNoMachine},
		{name: "foreign machine", machine: "ubuntu", want: ErrForeignMachine},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fake.ActiveMounts(ctx, test.machine); !errors.Is(err, test.want) {
				t.Errorf("ActiveMounts(%q) = %v, want %v", test.machine, err, test.want)
			}
			if err := fake.Delete(ctx, test.machine); !errors.Is(err, test.want) {
				t.Errorf("Delete(%q) = %v, want %v", test.machine, err, test.want)
			}
		})
	}
	if _, exists := fake.Machines["ubuntu"]; !exists {
		t.Errorf("Delete removed the foreign machine")
	}
}

func TestFakeRefusesForeignNames(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		machine string
	}{
		{name: "name of another machine", prefix: "orbit-", machine: "ubuntu"},
		{name: "only the prefix", prefix: "orbit-", machine: "orbit-"},
		{name: "empty name", prefix: "orbit-", machine: ""},
		{name: "empty prefix", prefix: "", machine: "orbit-api-1d9b22c9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := New(test.prefix)
			err := fake.Create(context.Background(), test.machine, "/work/api", "ubuntu", true)
			if !errors.Is(err, ErrForeignMachine) {
				t.Errorf("Create(%q) = %v, want %v", test.machine, err, ErrForeignMachine)
			}
			if len(fake.Machines) != 0 {
				t.Errorf("Machines = %v, want none", fake.Machines)
			}
		})
	}
}

func TestFakeRefusesMountPathsLikeTheAdapter(t *testing.T) {
	for _, dir := range []string{"", "work/api", "/work/a:b", "/work/.ssh:", "/work/a,b", "/work/a:/b"} {
		fake := New("orbit-")
		err := fake.Create(context.Background(), "orbit-api-1d9b22c9", dir, "ubuntu", true)
		if !errors.Is(err, ErrMountPath) {
			t.Errorf("Create with the directory %q = %v, want %v", dir, err, ErrMountPath)
		}
		if len(fake.Machines) != 0 {
			t.Errorf("Machines = %v, want none", fake.Machines)
		}
	}
}

func TestFakeFailsWhenTold(t *testing.T) {
	boom := errors.New("boom")
	fake := New("orbit-")
	fake.ExistsErr = boom
	fake.CreateErr = boom
	fake.MountsErr = boom
	fake.ActiveMountsErr = boom
	fake.DeleteErr = boom
	fake.RunAsRootErr = boom
	fake.RunAsAgentErr = boom
	ctx := context.Background()

	if _, err := fake.Exists(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("Exists = %v, want %v", err, boom)
	}
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); !errors.Is(err, boom) {
		t.Errorf("Create = %v, want %v", err, boom)
	}
	if err := fake.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true")); !errors.Is(err, boom) {
		t.Errorf("RunAsRoot = %v, want %v", err, boom)
	}
	if err := fake.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true")); !errors.Is(err, boom) {
		t.Errorf("RunAsAgent = %v, want %v", err, boom)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("Machines = %v, want none", fake.Machines)
	}
	if _, err := fake.Mounts(ctx); !errors.Is(err, boom) {
		t.Errorf("Mounts = %v, want %v", err, boom)
	}
	fake.Machines["orbit-api-1d9b22c9"] = map[string]string{"/work/api": "/work/api"}
	if _, err := fake.ActiveMounts(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("ActiveMounts = %v, want %v", err, boom)
	}
	if err := fake.Delete(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("Delete = %v, want %v", err, boom)
	}
	if len(fake.Machines) != 1 {
		t.Errorf("Machines = %v, want the machine kept after a failed Delete", fake.Machines)
	}
}

func TestFakeDeletesAndRunsScripts(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	if err := fake.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("echo hi\n")); err != nil {
		t.Fatalf("RunAsRoot = %v", err)
	}
	if err := fake.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("id -un\n")); err != nil {
		t.Fatalf("RunAsAgent = %v", err)
	}
	if err := fake.Delete(ctx, "orbit-api-1d9b22c9"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("Machines = %v, want none", fake.Machines)
	}
	wantCalls := []Call{
		{Op: OpCreate, Name: "orbit-api-1d9b22c9", Dir: "/work/api", Forward: true, Distribution: "ubuntu"},
		{Op: OpRunAsRoot, Name: "orbit-api-1d9b22c9", Script: "echo hi\n"},
		{Op: OpRunAsAgent, Name: "orbit-api-1d9b22c9", Script: "id -un\n"},
		{Op: OpDelete, Name: "orbit-api-1d9b22c9"},
	}
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("Calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestFakeRefusesForeignNamesForEveryOperation(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Delete(ctx, "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("Delete = %v, want %v", err, ErrForeignMachine)
	}
	if err := fake.RunAsRoot(ctx, "ubuntu", []byte("true")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsRoot = %v, want %v", err, ErrForeignMachine)
	}
	if err := fake.RunAsAgent(ctx, "ubuntu", []byte("true")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsAgent = %v, want %v", err, ErrForeignMachine)
	}
	if err := fake.RunAsAgent(ctx, "orbit-", []byte("true")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsAgent in a machine named only by the prefix = %v, want %v", err, ErrForeignMachine)
	}
}

func TestFakeStopsAndStartsMachines(t *testing.T) {
	const name = "orbit-api-1d9b22c9"
	fake := New("orbit-")
	ctx := context.Background()
	if state, err := fake.State(ctx, name); err != nil || state != "" {
		t.Fatalf("State before Create = %q, %v, want no machine", state, err)
	}
	if err := fake.Create(ctx, name, "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	steps := []struct {
		change func(context.Context, string) error
		want   string
	}{
		{change: fake.Stop, want: StateStopped},
		{change: fake.Stop, want: StateStopped},
		{change: fake.Start, want: StateRunning},
		{change: fake.Start, want: StateRunning},
	}
	for i, step := range steps {
		if err := step.change(ctx, name); err != nil {
			t.Fatalf("step %d = %v", i, err)
		}
		if state, err := fake.State(ctx, name); err != nil || state != step.want {
			t.Errorf("State after step %d = %q, %v, want %q", i, state, err, step.want)
		}
	}
}

func TestFakeRefusesCommandsInAStoppedMachine(t *testing.T) {
	const name = "orbit-api-1d9b22c9"
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Create(ctx, name, "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	fake.States[name] = "starting"
	if _, err := fake.ActiveMounts(ctx, name); !errors.Is(err, ErrStopped) {
		t.Errorf("ActiveMounts of a starting machine = %v, want %v", err, ErrStopped)
	}
	if err := fake.Stop(ctx, name); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if _, err := fake.ActiveMounts(ctx, name); !errors.Is(err, ErrStopped) {
		t.Errorf("ActiveMounts of a stopped machine = %v, want %v", err, ErrStopped)
	}
	if err := fake.RunAsRoot(ctx, name, []byte("true\n")); !errors.Is(err, ErrStopped) {
		t.Errorf("RunAsRoot in a stopped machine = %v, want %v", err, ErrStopped)
	}
	if err := fake.RunAsAgent(ctx, name, []byte("true\n")); !errors.Is(err, ErrStopped) {
		t.Errorf("RunAsAgent in a stopped machine = %v, want %v", err, ErrStopped)
	}
}

func TestFakeRefusesToStartOrStopWhatTheAdapterRefuses(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	fake.Machines["orbit-x --all"] = map[string]string{}
	tests := []struct {
		machine string
		want    error
	}{
		{machine: "", want: ErrForeignMachine},
		{machine: "orbit-", want: ErrForeignMachine},
		{machine: "--all", want: ErrForeignMachine},
		{machine: "ubuntu", want: ErrForeignMachine},
		{machine: "orbit-x --all", want: ErrMachineName},
		{machine: "orbit-gone-22222222", want: ErrNoMachine},
	}
	for _, test := range tests {
		if err := fake.Start(ctx, test.machine); !errors.Is(err, test.want) {
			t.Errorf("Start(%q) = %v, want %v", test.machine, err, test.want)
		}
		if err := fake.Stop(ctx, test.machine); !errors.Is(err, test.want) {
			t.Errorf("Stop(%q) = %v, want %v", test.machine, err, test.want)
		}
		if _, err := fake.State(ctx, test.machine); test.want == ErrForeignMachine && !errors.Is(err, test.want) {
			t.Errorf("State(%q) = %v, want %v", test.machine, err, test.want)
		}
	}
}

func TestFakeStartAndStopFailWhenTold(t *testing.T) {
	const name = "orbit-api-1d9b22c9"
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Create(ctx, name, "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	failure := errors.New("orb failed")
	fake.StopErr, fake.StartErr, fake.StateErr = failure, failure, failure
	if err := fake.Stop(ctx, name); !errors.Is(err, failure) {
		t.Errorf("Stop = %v, want %v", err, failure)
	}
	if err := fake.Start(ctx, name); !errors.Is(err, failure) {
		t.Errorf("Start = %v, want %v", err, failure)
	}
	if _, err := fake.State(ctx, name); !errors.Is(err, failure) {
		t.Errorf("State = %v, want %v", err, failure)
	}
	if fake.States[name] != StateRunning {
		t.Errorf("state after failed calls = %q, want %q", fake.States[name], StateRunning)
	}
}

func TestFakeShellRecordsAndReturnsTheStatus(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	const name = "orbit-api-11111111"
	if err := fake.Create(ctx, name, "/Users/dev/api", "ubuntu", true); err != nil {
		t.Fatal(err)
	}
	fake.ShellStatus = 3

	status, err := fake.Shell(ctx, name, "/Users/dev/api", strings.NewReader(""), io.Discard, io.Discard)

	if err != nil || status != 3 {
		t.Errorf("Shell = %d, %v, want 3, nil", status, err)
	}
	if want := (Call{Op: OpShell, Name: name, Dir: "/Users/dev/api"}); fake.Calls[len(fake.Calls)-1] != want {
		t.Errorf("last call = %+v, want %+v", fake.Calls[len(fake.Calls)-1], want)
	}
}

func TestFakeShellRefusesWhatTheAdapterRefuses(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	const name = "orbit-api-11111111"
	if err := fake.Create(ctx, name, "/Users/dev/api", "ubuntu", true); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		machine string
		dir     string
		want    error
	}{
		{machine: "ubuntu", dir: "/Users/dev/api", want: ErrForeignMachine},
		{machine: "orbit-", dir: "/Users/dev/api", want: ErrForeignMachine},
		{machine: name, dir: "api", want: ErrMountPath},
		{machine: name, dir: "-uroot", want: ErrMountPath},
		{machine: name, dir: "/Users/dev/a:b", want: ErrMountPath},
		{machine: name, dir: "/Users/dev/a,b", want: ErrMountPath},
	}
	for _, test := range tests {
		if _, err := fake.Shell(ctx, test.machine, test.dir, strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, test.want) {
			t.Errorf("Shell(%q, %q) = %v, want %v", test.machine, test.dir, err, test.want)
		}
	}
	if err := fake.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Shell(ctx, name, "/Users/dev/api", strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, ErrStopped) {
		t.Errorf("Shell in a stopped machine = %v, want %v", err, ErrStopped)
	}
	fake.ShellErr = errors.New("orb failed")
	if err := fake.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Shell(ctx, name, "/Users/dev/api", strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, fake.ShellErr) {
		t.Errorf("Shell with ShellErr = %v, want %v", err, fake.ShellErr)
	}
}

func TestFakeKeepsTheSSHAgentForwardingOfACreatedMachine(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	for name, forward := range map[string]bool{"orbit-on-1d9b22c9": true, "orbit-off-00db83c9": false} {
		if err := fake.Create(ctx, name, "/work/"+name, "ubuntu", forward); err != nil {
			t.Fatalf("Create(%q) = %v", name, err)
		}
		if got, err := fake.SSHAgentForwarded(ctx, name); err != nil || got != forward {
			t.Errorf("SSHAgentForwarded(%q) = %t, %v, want %t", name, got, err, forward)
		}
	}
	if err := fake.Delete(ctx, "orbit-on-1d9b22c9"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if _, err := fake.SSHAgentForwarded(ctx, "orbit-on-1d9b22c9"); !errors.Is(err, ErrNoMachine) {
		t.Errorf("SSHAgentForwarded after Delete = %v, want %v", err, ErrNoMachine)
	}
}

func TestFakeReportsAnotherForwardingThanRequestedWhenToldTo(t *testing.T) {
	fake := New("orbit-")
	other := false
	fake.CreatedForwarded = &other
	if err := fake.Create(context.Background(), "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); err != nil {
		t.Fatalf("Create = %v", err)
	}
	if got, err := fake.SSHAgentForwarded(context.Background(), "orbit-api-1d9b22c9"); err != nil || got {
		t.Errorf("SSHAgentForwarded = %t, %v, want false", got, err)
	}
}

func TestFakeSSHAgentForwardedRefusesWhatTheAdapterRefuses(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	if _, err := fake.SSHAgentForwarded(ctx, "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("SSHAgentForwarded of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if _, err := fake.SSHAgentForwarded(ctx, "orbit-gone-22222222"); !errors.Is(err, ErrNoMachine) {
		t.Errorf("SSHAgentForwarded of a missing machine = %v, want %v", err, ErrNoMachine)
	}
	boom := errors.New("boom")
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); err != nil {
		t.Fatal(err)
	}
	fake.SSHAgentForwardedErr = boom
	if _, err := fake.SSHAgentForwarded(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("SSHAgentForwarded = %v, want %v", err, boom)
	}
}

func TestFakeDeleteRefusesHostileNames(t *testing.T) {
	for _, machine := range []string{"orbit-x --all", "orbit-a orbit-b", "orbit-x\t"} {
		fake := New("orbit-")
		fake.Machines["orbit-x"] = map[string]string{}
		if err := fake.Delete(context.Background(), machine); !errors.Is(err, ErrMachineName) {
			t.Errorf("Delete(%q) = %v, want %v", machine, err, ErrMachineName)
		}
		if _, exists := fake.Machines["orbit-x"]; !exists {
			t.Errorf("Delete(%q) removed a machine", machine)
		}
	}
	for _, machine := range []string{"", "orbit-", "--all", "-a"} {
		if err := New("orbit-").Delete(context.Background(), machine); !errors.Is(err, ErrForeignMachine) {
			t.Errorf("Delete(%q) = %v, want %v", machine, err, ErrForeignMachine)
		}
	}
}

func TestFakeListsMachinesAndRecordsOnlyList(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()
	if err := fake.Create(ctx, "orbit-web-00db83c9", "/work/web", "ubuntu", true); err != nil {
		t.Fatal(err)
	}
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api", "ubuntu", true); err != nil {
		t.Fatal(err)
	}
	if err := fake.Stop(ctx, "orbit-web-00db83c9"); err != nil {
		t.Fatal(err)
	}
	fake.Listed = []Machine{{Name: "ubuntu", State: "running"}}
	fake.Calls = nil

	got, err := fake.List(ctx)
	want := []Machine{
		{Name: "orbit-api-1d9b22c9", State: StateRunning, Mounts: []Mount{{Source: "/work/api", Destination: "/work/api"}}},
		{Name: "orbit-web-00db83c9", State: StateStopped, Mounts: []Mount{{Source: "/work/web", Destination: "/work/web"}}},
		{Name: "ubuntu", State: "running"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, %v, want %+v", got, err, want)
	}
	if !slices.Equal(fake.Calls, []Call{{Op: OpList}}) {
		t.Errorf("Calls = %v, want only %q", fake.Calls, OpList)
	}
	if state, _ := fake.State(ctx, "orbit-web-00db83c9"); state != StateStopped {
		t.Errorf("state after List = %q, want %q", state, StateStopped)
	}
}

func TestFakeListReturnsItsError(t *testing.T) {
	fake := New("orbit-")
	fake.ListErr = errors.New("boom")
	if got, err := fake.List(context.Background()); !errors.Is(err, fake.ListErr) || got != nil {
		t.Errorf("List = %+v, %v, want %v", got, err, fake.ListErr)
	}
}

func TestFakeCreateRecordsTheDistributionAndRefusesOthers(t *testing.T) {
	for _, key := range []string{"ubuntu", "debian"} {
		fake := New("orbit-")
		if err := fake.Create(context.Background(), "orbit-api-1d9b22c9", "/work/api", key, true); err != nil {
			t.Fatalf("Create(%q) = %v", key, err)
		}
		if got := fake.Calls[0].Distribution; got != key {
			t.Errorf("recorded distribution = %q, want %q", got, key)
		}
	}
	for _, key := range []string{"", "alpine", "Ubuntu", "ubuntu:26.04", "--debian"} {
		fake := New("orbit-")
		err := fake.Create(context.Background(), "orbit-api-1d9b22c9", "/work/api", key, true)
		if !errors.Is(err, ErrDistribution) {
			t.Errorf("Create(%q) = %v, want %v", key, err, ErrDistribution)
		}
		if _, exists := fake.Machines["orbit-api-1d9b22c9"]; exists {
			t.Errorf("Create(%q) added a machine", key)
		}
	}
}
