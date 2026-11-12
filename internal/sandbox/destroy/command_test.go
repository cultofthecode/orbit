package destroy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// panicReader fails the test when something reads it.
type panicReader struct{ t *testing.T }

func (p panicReader) Read([]byte) (int, error) {
	p.t.Error("standard input was read")
	return 0, io.EOF
}

// execute runs the command on its own with the input and args and returns
// what it wrote to standard output and standard error.
func execute(fake *orbstacktest.Fake, in io.Reader, args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	command := NewCommand(Handler{Machines: fake})
	command.SetArgs(args)
	command.SetIn(in)
	command.SetOut(&out)
	command.SetErr(&errOut)
	err := command.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestCommandWithYesPrintsTheResultAndReadsNoInput(t *testing.T) {
	for _, flag := range []string{"--yes", "-y"} {
		dir, name := project(t)
		t.Chdir(dir)
		fake := withMachine(name, dir, orbstacktest.StateRunning)

		stdout, stderr, err := execute(fake, panicReader{t}, flag)

		if want := "Destroyed sandbox " + name + " for " + dir + "\n"; err != nil || stdout != want || stderr != "" {
			t.Errorf("%s = %q, %q, %v, want %q", flag, stdout, stderr, err, want)
		}
	}
}

func TestCommandWithoutATerminalRefusesAndDeletesNothing(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	_, _, err := execute(fake, strings.NewReader("y\n"))

	if !errors.Is(err, ErrNotConfirmed) || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), name) {
		t.Errorf("command = %v, want ErrNotConfirmed naming --yes and %s", err, name)
	}
	if _, exists := fake.Machines[name]; !exists {
		t.Error("machine was deleted")
	}
}

func TestCommandRefusesArguments(t *testing.T) {
	dir, name := project(t)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)

	if _, _, err := execute(fake, nil, "--yes", otherMachine); err == nil {
		t.Error("command with an argument succeeded, want an error")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestCommandReportsNoSandbox(t *testing.T) {
	dir, _ := project(t)
	t.Chdir(dir)

	if _, _, err := execute(orbstacktest.New(sandbox.NamePrefix), nil, "--yes"); !errors.Is(err, sandbox.ErrNoSandbox) {
		t.Errorf("command = %v, want %v", err, sandbox.ErrNoSandbox)
	}
}

func yesTerminal(io.Reader) bool { return true }

func TestConfirmerAnswers(t *testing.T) {
	target := Target{Name: "orbit-proj-0123456789abcdef", Dir: "/Users/dev/proj", Running: true}
	tests := []struct {
		input   string
		confirm bool
	}{
		{"y\n", true}, {"Y\n", true}, {"yes\n", true}, {"YES\n", true}, {" yes \n", true},
		{"n\n", false}, {"\n", false}, {"", false}, {"y", false}, {"yes please\n", false}, {"maybe\n", false},
	}
	for _, test := range tests {
		var errOut bytes.Buffer
		err := confirmer(false, strings.NewReader(test.input), &errOut, yesTerminal)(context.Background(), target)
		if (err == nil) != test.confirm || (err != nil && !errors.Is(err, ErrNotConfirmed)) {
			t.Errorf("answer %q = %v, want confirm=%v", test.input, err, test.confirm)
		}
		if !strings.Contains(errOut.String(), target.Name) || !strings.Contains(errOut.String(), target.Dir) {
			t.Errorf("prompt = %q, want the machine and the directory", errOut.String())
		}
	}
}

func TestConfirmerPromptMentionsOpenShellsOnlyWhenRunning(t *testing.T) {
	for _, running := range []bool{true, false} {
		var errOut bytes.Buffer
		_ = confirmer(false, strings.NewReader("n\n"), &errOut, yesTerminal)(context.Background(), Target{Name: "n", Dir: "/d", Running: running})
		got := errOut.String()
		if strings.Contains(got, "open shells end") != running || !strings.Contains(got, "the project files stay") || !strings.HasSuffix(got, "[y/N] ") {
			t.Errorf("prompt for running=%v = %q", running, got)
		}
	}
}

func TestConfirmerWithYesNeverAsksTheTerminal(t *testing.T) {
	notCalled := func(io.Reader) bool { t.Error("isTerminal was called"); return false }
	var errOut bytes.Buffer
	if err := confirmer(true, panicReader{t}, &errOut, notCalled)(context.Background(), Target{}); err != nil || errOut.Len() != 0 {
		t.Errorf("confirmer = %v, %q, want yes without a prompt", err, errOut.String())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read broke") }

func TestConfirmerReturnsTheErrorOfReading(t *testing.T) {
	err := confirmer(false, failingReader{}, io.Discard, yesTerminal)(context.Background(), Target{})
	if err == nil || errors.Is(err, ErrNotConfirmed) || !strings.Contains(err.Error(), "read broke") {
		t.Errorf("confirmer = %v, want the read error", err)
	}
}

func TestPromptAndResultQuoteAHostileDirectory(t *testing.T) {
	hostile := filepath.Join(t.TempDir(), "a\x1b[2K\rb")
	if err := os.Mkdir(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := realpath.Resolve(hostile)
	if err != nil {
		t.Fatal(err)
	}
	name := sandbox.MachineName(dir)
	t.Chdir(dir)
	fake := withMachine(name, dir, orbstacktest.StateRunning)
	quoted := strconv.Quote(dir)

	stdout, stderr, err := execute(fake, strings.NewReader("y\n"), "--yes")
	if want := "Destroyed sandbox " + name + " for " + quoted + "\n"; err != nil || stdout != want || stderr != "" {
		t.Errorf("destroy --yes = %q, %q, %v, want %q", stdout, stderr, err, want)
	}

	var errOut bytes.Buffer
	err = confirmer(false, strings.NewReader("n\n"), &errOut, yesTerminal)(context.Background(), Target{Name: name, Dir: dir})
	if !errors.Is(err, ErrNotConfirmed) {
		t.Errorf("confirmer after n = %v, want %v", err, ErrNotConfirmed)
	}
	if got := errOut.String(); strings.ContainsAny(got, "\x1b\r") || !strings.Contains(got, quoted) {
		t.Errorf("prompt = %q, want the directory quoted as %s and no control character", got, quoted)
	}
}
