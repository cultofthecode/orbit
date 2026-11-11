package orbstacktest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestFakeCreatesAndFindsMachines(t *testing.T) {
	fake := New("orbit-")
	ctx := context.Background()

	before, err := fake.Exists(ctx, "orbit-api-1d9b22c9")
	if err != nil || before {
		t.Fatalf("Exists before Create = %v, %v, want false", before, err)
	}
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api"); err != nil {
		t.Fatalf("Create = %v", err)
	}
	after, err := fake.Exists(ctx, "orbit-api-1d9b22c9")
	if err != nil || !after {
		t.Fatalf("Exists after Create = %v, %v, want true", after, err)
	}

	wantCalls := []Call{
		{Op: OpExists, Name: "orbit-api-1d9b22c9"},
		{Op: OpCreate, Name: "orbit-api-1d9b22c9", Dir: "/work/api"},
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
	if err := fake.Create(ctx, name, "/work/api"); err != nil {
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
		{Op: OpCreate, Name: name, Dir: "/work/api"},
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
			err := fake.Create(context.Background(), test.machine, "/work/api")
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
		err := fake.Create(context.Background(), "orbit-api-1d9b22c9", dir)
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
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api"); !errors.Is(err, boom) {
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
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api"); err != nil {
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
		{Op: OpCreate, Name: "orbit-api-1d9b22c9", Dir: "/work/api"},
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
