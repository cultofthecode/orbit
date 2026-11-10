package initialize

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// execute runs the command of the slice on its own with args and returns
// what it wrote to its output stream and to its error stream.
func execute(fake *orbstacktest.Fake, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	command := NewCommand(Handler{Machines: fake})
	command.SetArgs(args)
	command.SetOut(&out)
	command.SetErr(&errOut)
	err = command.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// printsResult reports whether stdout holds a result line of the command.
func printsResult(stdout string) bool {
	return strings.Contains(stdout, "Created sandbox") || strings.Contains(stdout, "already exists")
}

func TestCommandPrintsTheResult(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	t.Chdir(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	tests := []struct {
		name       string
		wantStdout string
		wantCalls  []orbstacktest.Call
	}{
		{
			name:       "created",
			wantStdout: "Created sandbox " + name + " for " + project + "\n",
			wantCalls: []orbstacktest.Call{
				{Op: orbstacktest.OpExists, Name: name},
				{Op: orbstacktest.OpCreate, Name: name, Dir: project},
			},
		},
		{
			name:       "already exists",
			wantStdout: "Sandbox " + name + " already exists for " + project + "\n",
			wantCalls:  []orbstacktest.Call{{Op: orbstacktest.OpExists, Name: name}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake.Calls = nil

			stdout, stderr, err := execute(fake)

			if err != nil {
				t.Fatalf("command = %v", err)
			}
			if stdout != test.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, test.wantStdout)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want it empty", stderr)
			}
			if !slices.Equal(fake.Calls, test.wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, test.wantCalls)
			}
		})
	}
}

func TestCommandRefusesArguments(t *testing.T) {
	home := newHome(t)
	t.Chdir(filepath.Join(home, "work", "api"))
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "directory", args: []string{"elsewhere"}, want: `unknown command "elsewhere" for "init"`},
		{name: "empty argument", args: []string{""}, want: `unknown command "" for "init"`},
		{name: "flag of a later task", args: []string{"--agent", "claude-code"}, want: "unknown flag: --agent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			stdout, _, err := execute(fake, test.args...)

			if err == nil || err.Error() != test.want {
				t.Errorf("command %q = %v, want %q", test.args, err, test.want)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
			if printsResult(stdout) {
				t.Errorf("stdout = %q, want no result", stdout)
			}
		})
	}
}

func TestCommandReturnsTheErrorOfTheHandler(t *testing.T) {
	boom := errors.New("orb: boom")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	failing := orbstacktest.New(sandbox.NamePrefix)
	failing.CreateErr = boom
	tests := []struct {
		name string
		dir  string
		fake *orbstacktest.Fake
		want error
	}{
		{name: "home directory", dir: home, fake: orbstacktest.New(sandbox.NamePrefix), want: sandbox.ErrHomeDir},
		{name: "OrbStack failure", dir: project, fake: failing, want: boom},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(test.dir)

			stdout, _, err := execute(test.fake)

			if !errors.Is(err, test.want) {
				t.Errorf("command in %s = %v, want %v", test.dir, err, test.want)
			}
			if printsResult(stdout) {
				t.Errorf("stdout = %q, want no result", stdout)
			}
		})
	}
}
