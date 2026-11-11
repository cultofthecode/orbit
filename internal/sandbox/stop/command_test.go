package stop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// execute runs the command of the slice on its own with args and returns
// what it wrote to its output stream.
func execute(fake *orbstacktest.Fake, args ...string) (string, error) {
	var out bytes.Buffer
	command := NewCommand(Handler{Machines: fake})
	command.SetArgs(args)
	command.SetOut(&out)
	command.SetErr(&out)
	err := command.ExecuteContext(context.Background())
	return out.String(), err
}

// symlinkTo makes link a symlink to target.
func symlinkTo(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestCommandPrintsTheResult(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	for _, want := range []string{"Stopped sandbox " + name + " for " + dir + "\n", "Sandbox " + name + " is already stopped\n"} {
		stdout, err := execute(fake)
		if err != nil || stdout != want {
			t.Errorf("command = %q, %v, want %q", stdout, err, want)
		}
	}
}

func TestCommandRefusesArguments(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	if _, err := execute(fake, "orbit-other-0123456789abcdef"); err == nil {
		t.Error("command with an argument succeeded, want an error")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestCommandReportsNoSandbox(t *testing.T) {
	dir, _ := project(t)
	t.Chdir(dir)

	if _, err := execute(orbstacktest.New(sandbox.NamePrefix)); !errors.Is(err, sandbox.ErrNoSandbox) {
		t.Errorf("command = %v, want %v", err, sandbox.ErrNoSandbox)
	}
}
