package orbstacktest

import (
	"context"
	"errors"
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
	if dir := fake.Machines["orbit-api-1d9b22c9"]; dir != "/work/api" {
		t.Errorf("mount = %q, want /work/api", dir)
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
	fake.DeleteErr = boom
	fake.RunAsRootErr = boom
	ctx := context.Background()

	if _, err := fake.Exists(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("Exists = %v, want %v", err, boom)
	}
	if err := fake.Create(ctx, "orbit-api-1d9b22c9", "/work/api"); !errors.Is(err, boom) {
		t.Errorf("Create = %v, want %v", err, boom)
	}
	if err := fake.Delete(ctx, "orbit-api-1d9b22c9"); !errors.Is(err, boom) {
		t.Errorf("Delete = %v, want %v", err, boom)
	}
	if err := fake.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true")); !errors.Is(err, boom) {
		t.Errorf("RunAsRoot = %v, want %v", err, boom)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("Machines = %v, want none", fake.Machines)
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
	if err := fake.Delete(ctx, "orbit-api-1d9b22c9"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("Machines = %v, want none", fake.Machines)
	}
	wantCalls := []Call{
		{Op: OpCreate, Name: "orbit-api-1d9b22c9", Dir: "/work/api"},
		{Op: OpRunAsRoot, Name: "orbit-api-1d9b22c9", Script: "echo hi\n"},
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
}
