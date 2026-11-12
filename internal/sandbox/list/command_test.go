package list

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// execute runs the command of the slice on its own with args and returns what
// it wrote to its output and error streams.
func execute(fake *orbstacktest.Fake, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	command := NewCommand(Handler{Machines: fakeMachines{fake}})
	command.SilenceUsage = true
	command.SetArgs(args)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	err := command.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestCommandPrintsNothingForNoSandbox(t *testing.T) {
	stdout, stderr, err := execute(orbstacktest.New(sandbox.NamePrefix))
	if err != nil || stdout != "" || stderr != "" {
		t.Errorf("command = %q, %q, %v, want no output", stdout, stderr, err)
	}
}

func TestCommandPrintsAnAlignedTableSortedByName(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	web := withSandbox(fake, "/Users/dev/my web", orbstacktest.StateStopped)
	api := withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)
	fake.Listed = []orbstacktest.Machine{{Name: "orbit-manual", State: "starting", Mounts: []orbstacktest.Mount{{Source: "/Users/dev/x", Destination: "/Users/dev/x"}}}}

	stdout, stderr, err := execute(fake)

	rows := map[string]string{
		"orbit-manual": "starting  ?",
		api:            "running   /Users/dev/api",
		web:            "stopped   /Users/dev/my web",
	}
	names := slices.Sorted(maps.Keys(rows))
	width := max(len(api), len(web)) + 2
	want := "NAME" + strings.Repeat(" ", width-len("NAME")) + "STATE     DIRECTORY\n"
	for _, name := range names {
		want += name + strings.Repeat(" ", width-len(name)) + rows[name] + "\n"
	}
	if err != nil || stdout != want {
		t.Errorf("stdout = %q, %v, want %q", stdout, err, want)
	}
	wantReason := "orbit: orbit-manual: project directory unknown: the name does not match \"/Users/dev/x\"\n"
	if stderr != wantReason {
		t.Errorf("stderr = %q, want %q", stderr, wantReason)
	}
}

func TestCommandPrintsBothReasonsOfARowWithNoStateAndNoDirectory(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Listed = []orbstacktest.Machine{{Name: "orbit-bare"}}

	stdout, stderr, err := execute(fake)

	if want := "NAME        STATE  DIRECTORY\norbit-bare  ?      ?\n"; err != nil || stdout != want {
		t.Errorf("stdout = %q, %v, want %q", stdout, err, want)
	}
	want := "orbit: orbit-bare: no state\norbit: orbit-bare: project directory unknown: no mount\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestCommandQuotesValuesWithNonPrintableCharacters(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	hostile := "/work/\x1b[2Jevil\nline\tcell"
	fake.Listed = []orbstacktest.Machine{
		{Name: "orbit-esc\x1b]0;title\a", State: "run\nning", Mounts: []orbstacktest.Mount{{Source: hostile, Destination: hostile}}},
		{Name: "orbit-bad\xff", State: "stopped "},
	}

	stdout, stderr, err := execute(fake)

	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []string{stdout, stderr} {
		for _, bad := range []string{"\x1b", "\a", "\xff", " ", "\t\t"} {
			if strings.Contains(stream, bad) {
				t.Errorf("output holds %q: %q", bad, stream)
			}
		}
		if strings.Contains(stream, "line\tcell") || strings.Contains(stream, "evil\nline") {
			t.Errorf("output holds a raw tab or line break from a value: %q", stream)
		}
	}
	if !strings.Contains(stdout, `"orbit-esc\x1b]0;title\a"`) || !strings.Contains(stdout, `"run\nning"`) {
		t.Errorf("stdout = %q, want the name and the state Go-quoted", stdout)
	}
	if !strings.Contains(stderr, `"orbit-esc\x1b]0;title\a"`) || !strings.Contains(stderr, `the name does not match "/work/\x1b[2Jevil\nline\tcell"`) {
		t.Errorf("stderr = %q, want the name and the path quoted", stderr)
	}
	if lines := strings.Count(stdout, "\n"); lines != 3 {
		t.Errorf("stdout has %d lines, want a header and two rows", lines)
	}
}

func TestCommandPrintsNothingOnStdoutWhenTheListCannotBeRead(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	withSandbox(fake, "/Users/dev/api", orbstacktest.StateRunning)
	fake.ListErr = errors.New("machine list cannot be read")

	stdout, _, err := execute(fake)

	if !errors.Is(err, fake.ListErr) || stdout != "" {
		t.Errorf("command = %q, %v, want the error and no stdout", stdout, err)
	}
}

func TestCommandRefusesArgumentsAndFlags(t *testing.T) {
	fake := orbstacktest.New(sandbox.NamePrefix)
	for _, args := range [][]string{{"orbit-a"}, {"--json"}, {"-q"}} {
		if _, _, err := execute(fake, args...); err == nil {
			t.Errorf("command %v succeeded, want an error", args)
		}
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestCommandHelpSaysTheDirectoryIsTheStoredMount(t *testing.T) {
	command := NewCommand(Handler{})
	if !strings.Contains(command.Long, "stores as the mount") {
		t.Errorf("help = %q, want it to say that OrbStack stores the directory", command.Long)
	}
}
