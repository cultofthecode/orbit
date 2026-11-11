package start

import (
	"bytes"
	"context"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
)

// execute runs the command of the slice on its own with handler and args and
// returns what it wrote to its output stream.
func execute(handler Handler, args ...string) (string, error) {
	var out bytes.Buffer
	command := NewCommand(handler)
	command.SetArgs(args)
	command.SetOut(&out)
	command.SetErr(&out)
	err := command.ExecuteContext(context.Background())
	return out.String(), err
}

func TestCommandPrintsTheResult(t *testing.T) {
	s := newSandboxHome(t)
	t.Chdir(s.dir)
	fake := s.fake(orbstacktest.StateStopped)

	for _, want := range []string{"Started sandbox " + s.name + " for " + s.dir + "\n", "Sandbox " + s.name + " is already running\n"} {
		stdout, err := execute(s.handler(fake))
		if err != nil || stdout != want {
			t.Errorf("command = %q, %v, want %q", stdout, err, want)
		}
	}
}

func TestCommandRefusesArguments(t *testing.T) {
	s := newSandboxHome(t)
	t.Chdir(s.dir)
	fake := s.fake(orbstacktest.StateStopped)

	if _, err := execute(s.handler(fake), otherMachine); err == nil {
		t.Error("command with an argument succeeded, want an error")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}
