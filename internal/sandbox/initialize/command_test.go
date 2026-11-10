package initialize

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

func TestRunPrintsTheResult(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	t.Chdir(project)
	command := Command{Handler: Handler{Machines: orbstacktest.New(sandbox.NamePrefix)}}
	wantLines := []string{
		"Created sandbox " + name + " for " + project + "\n",
		"Sandbox " + name + " already exists for " + project + "\n",
	}
	for _, want := range wantLines {
		var stdout, stderr bytes.Buffer

		err := command.Run(context.Background(), nil, &stdout, &stderr)

		if err != nil {
			t.Fatalf("Run = %v", err)
		}
		if stdout.String() != want {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want nothing", stderr.String())
		}
	}
}

func TestRunRefusesArguments(t *testing.T) {
	home := newHome(t)
	t.Chdir(filepath.Join(home, "work", "api"))
	for _, args := range [][]string{{"elsewhere"}, {"--agent", "claude-code"}, {""}} {
		fake := orbstacktest.New(sandbox.NamePrefix)
		var stdout, stderr bytes.Buffer

		err := Command{Handler: Handler{Machines: fake}}.Run(context.Background(), args, &stdout, &stderr)

		if !errors.Is(err, ErrUnexpectedArgument) {
			t.Errorf("Run(%q) = %v, want %v", args, err, ErrUnexpectedArgument)
		}
		if len(fake.Calls) != 0 || stdout.Len() != 0 {
			t.Errorf("Run(%q) made the calls %v and printed %q, want nothing", args, fake.Calls, stdout.String())
		}
	}
}

func TestRunReturnsTheErrorOfTheHandler(t *testing.T) {
	home := newHome(t)
	t.Chdir(home)
	var stdout, stderr bytes.Buffer

	err := Command{Handler: Handler{Machines: orbstacktest.New(sandbox.NamePrefix)}}.Run(context.Background(), nil, &stdout, &stderr)

	if !errors.Is(err, sandbox.ErrHomeDir) {
		t.Errorf("Run in the home directory = %v, want %v", err, sandbox.ErrHomeDir)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
}
