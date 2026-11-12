package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// streamRecorder is a fake that records the streams of the shell.
type streamRecorder struct {
	*orbstacktest.Fake
	in       io.Reader
	out, err io.Writer
}

// Shell records the streams and runs the shell of the fake.
func (r *streamRecorder) Shell(ctx context.Context, name, dir string, in io.Reader, out, errOut io.Writer) (int, error) {
	r.in, r.out, r.err = in, out, errOut
	return r.Fake.Shell(ctx, name, dir, in, out, errOut)
}

// symlinkTo makes link a symlink to target.
func symlinkTo(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// mkdir makes the directory dir.
func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// execute runs the command of the slice on its own with machines, the
// streams, and args.
func execute(machines Machines, in io.Reader, out, errOut io.Writer, args ...string) error {
	command := NewCommand(handler(machines))
	command.SetArgs(args)
	command.SetIn(in)
	command.SetOut(out)
	command.SetErr(errOut)
	return command.ExecuteContext(context.Background())
}

func TestCommandPassesItsStreamsAndPrintsNothing(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	recorder := &streamRecorder{Fake: withMachine(name, dir, orbstacktest.StateRunning)}
	in := strings.NewReader("")
	var out, errOut bytes.Buffer

	if err := execute(recorder, in, &out, &errOut); err != nil {
		t.Fatalf("command = %v", err)
	}
	if recorder.in != in || recorder.out != &out || recorder.err != &errOut {
		t.Error("the shell did not get the streams of the command")
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("command printed %q and %q, want nothing", out.String(), errOut.String())
	}
}

func TestCommandReturnsANonZeroStatusAsExitStatus(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	fake.ShellStatus = 3

	err := execute(fake, strings.NewReader(""), io.Discard, io.Discard)

	var status sandbox.ExitStatus
	if !errors.As(err, &status) || status.Code != 3 {
		t.Errorf("command = %v, want %+v", err, sandbox.ExitStatus{Code: 3})
	}
}

func TestCommandReturnsTheRefusalOfTheHandler(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateStopped)

	err := execute(fake, strings.NewReader(""), io.Discard, io.Discard)

	var status sandbox.ExitStatus
	if !errors.Is(err, ErrNotRunning) || errors.As(err, &status) {
		t.Errorf("command = %v, want %v", err, ErrNotRunning)
	}
}

func TestCommandRefusesArguments(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	if err := execute(fake, strings.NewReader(""), io.Discard, io.Discard, "claude"); err == nil {
		t.Error("command with an argument succeeded, want an error")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}
