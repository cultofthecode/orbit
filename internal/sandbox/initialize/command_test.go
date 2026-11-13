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

// execute runs the command of the slice on its own with args, for a user
// with the home directory home, and returns what it wrote to its output
// stream and to its error stream.
func execute(fake *orbstacktest.Fake, home string, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	command := NewCommand(newHandler(fake, account(home)), []string{testAgentName})
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
			wantCalls:  createCalls(name, project),
		},
		{
			name:       "already exists",
			wantStdout: "Sandbox " + name + " already exists for " + project + "\n",
			wantCalls:  keepCalls(name),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake.Calls = nil

			stdout, stderr, err := execute(fake, home)

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
		{name: "flag of another task", args: []string{"--no-ssh"}, want: "unknown flag: --no-ssh"},
		{name: "agent without a name", args: []string{"--agent"}, want: "flag needs an argument: --agent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			stdout, _, err := execute(fake, home, test.args...)

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

func TestCommandInstallsTheAgentOfTheFlag(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	t.Chdir(project)
	fake := orbstacktest.New(sandbox.NamePrefix)

	stdout, _, err := execute(fake, home, "--agent", testAgentName)

	if err != nil {
		t.Fatalf("command = %v", err)
	}
	if want := "Created sandbox " + name + " for " + project + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	wantCalls := append(checkedCreateCalls(name, project), agentCalls(name)...)
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestCommandRefusesAnUnknownAgent(t *testing.T) {
	home := newHome(t)
	t.Chdir(filepath.Join(home, "work", "api"))
	fake := orbstacktest.New(sandbox.NamePrefix)

	stdout, _, err := execute(fake, home, "--agent", "codex")

	if !errors.Is(err, errUnknownAgent) {
		t.Errorf("command = %v, want %v", err, errUnknownAgent)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
	if printsResult(stdout) {
		t.Errorf("stdout = %q, want no result", stdout)
	}
}

func TestCommandHelpNamesTheAgents(t *testing.T) {
	var out bytes.Buffer
	command := NewCommand(Handler{}, []string{"first-agent", "second-agent"})
	command.SetArgs([]string{"--help"})
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("help = %v", err)
	}
	if !strings.Contains(out.String(), "--agent string") || !strings.Contains(out.String(), "first-agent, second-agent") {
		t.Errorf("help = %q, want the flag --agent and the names of the agents", out.String())
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

			stdout, _, err := execute(test.fake, home)

			if !errors.Is(err, test.want) {
				t.Errorf("command in %s = %v, want %v", test.dir, err, test.want)
			}
			if printsResult(stdout) {
				t.Errorf("stdout = %q, want no result", stdout)
			}
		})
	}
}

func TestCommandFlagLeavesTheSSHAgentOut(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	t.Chdir(project)
	fake := orbstacktest.New(sandbox.NamePrefix)

	_, _, err := execute(fake, home, "--no-ssh-forwarding")

	if err != nil {
		t.Fatalf("command = %v", err)
	}
	if forwarded, _ := fake.SSHAgentForwarded(context.Background(), name); forwarded {
		t.Error("the machine forwards the SSH agent although --no-ssh-forwarding was passed")
	}
}

func TestCommandHelpNamesTheSSHFlag(t *testing.T) {
	var out bytes.Buffer
	command := NewCommand(Handler{}, nil)
	command.SetArgs([]string{"--help"})
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("help = %v", err)
	}
	if !strings.Contains(out.String(), "--no-ssh-forwarding") || !strings.Contains(out.String(), "do not forward the host SSH agent into the sandbox") || !strings.Contains(out.String(), "by default the sandbox can use every key in it") {
		t.Errorf("help = %q, want the flag --no-ssh-forwarding and its text", out.String())
	}
}

func TestCommandCreatesTheDistributionOfTheFlag(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	t.Chdir(project)
	tests := []struct {
		args []string
		want string
	}{
		{args: nil, want: "ubuntu"},
		{args: []string{"--distro", "ubuntu"}, want: "ubuntu"},
		{args: []string{"--distro=debian"}, want: "debian"},
		{args: []string{"--distro", ""}, want: "ubuntu"},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			if _, _, err := execute(fake, home, test.args...); err != nil {
				t.Fatalf("command %q = %v", test.args, err)
			}
			if got := fake.Calls[1]; got.Op != orbstacktest.OpCreate || got.Distribution != test.want {
				t.Errorf("create call = %+v, want the distribution %q", got, test.want)
			}
		})
	}
}

func TestCommandRefusesAnUnknownDistribution(t *testing.T) {
	home := newHome(t)
	t.Chdir(filepath.Join(home, "work", "api"))
	for _, distro := range []string{"alpine", "Debian", "debian:trixie", "ubuntu:26.04", "bookworm", "--mount", "-x", "a b", "a;b", `a"b`, "a'b", "a\nb"} {
		t.Run(distro, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			stdout, _, err := execute(fake, home, "--distro="+distro)

			if !errors.Is(err, sandbox.ErrUnknownDistribution) {
				t.Errorf("command = %v, want %v", err, sandbox.ErrUnknownDistribution)
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

func TestCommandHelpListsTheDistributions(t *testing.T) {
	command := NewCommand(Handler{}, nil)
	flag := command.Flags().Lookup("distro")
	if flag == nil {
		t.Fatal("no flag --distro")
	}
	if want := "distribution of a new sandbox: ubuntu, debian (default ubuntu)"; flag.Usage != want {
		t.Errorf("usage = %q, want %q", flag.Usage, want)
	}
	if strings.Contains(flag.Usage, "alpine") {
		t.Errorf("usage = %q, must not offer alpine in release 0.1", flag.Usage)
	}
}
