package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/agent"
	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/policy"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

func TestTheAdapterRunsInstallsAsTheAgentOfProvisioning(t *testing.T) {
	if orbstack.AgentUser != provision.AgentUser {
		t.Errorf("orbstack.AgentUser = %q, provision.AgentUser = %q, want the same user", orbstack.AgentUser, provision.AgentUser)
	}
}

func TestEveryDistributionHasAnImageAndAProfile(t *testing.T) {
	var keys []string
	for _, d := range sandbox.Distributions() {
		keys = append(keys, string(d))
	}
	if got := orbstack.Distributions(); !slices.Equal(got, keys) {
		t.Errorf("orbstack.Distributions() = %q, sandbox.Distributions() = %q, want the same list", got, keys)
	}
	if got := provision.Distributions(); !slices.Equal(got, keys) {
		t.Errorf("provision.Distributions() = %q, sandbox.Distributions() = %q, want the same list", got, keys)
	}
}

func TestOrbitRegistersInitWithClaudeCode(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), root, []string{"init", "--help"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("orbit init --help = %d, %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--agent string") || !strings.Contains(stdout.String(), "claude-code") {
		t.Errorf("orbit init --help = %q, want the flag --agent with claude-code", stdout.String())
	}
}

func TestOrbitRegistersVersion(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"--version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit --version = %d, %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "orbit version ") {
		t.Errorf("orbit --version = %q, want it to start with %q", stdout.String(), "orbit version ")
	}
	for _, command := range root.Commands() {
		if command.Name() == "version" {
			t.Error("orbit has a version command, want only the flag (D19)")
		}
	}
}

func TestOrbitDashVPrintsTheVersion(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"-v"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit -v = %d, %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "orbit version ") {
		t.Errorf("orbit -v = %q, want it to start with %q", stdout.String(), "orbit version ")
	}
}

func TestOrbitRegistersStop(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"stop", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit stop --help = %d, %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Stop the sandbox of the current directory") {
		t.Errorf("orbit stop --help = %q, want the command", stdout.String())
	}
}

func TestOrbitRegistersLs(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"ls", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit ls --help = %d, %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "List every sandbox with its state and project directory") {
		t.Errorf("orbit ls --help = %q, want the command", stdout.String())
	}
}

func TestLsFailsWithNothingOnStdoutWhenTheListCannotBeRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lister := stubLister{err: orbstack.ErrMachineList}

	code := run(context.Background(), newRoot(listCommand(listMachines{lister})), []string{"ls"}, &stdout, &stderr)

	want := "orbit: list sandboxes: " + orbstack.ErrMachineList.Error() + "\n"
	if code != exitFailure || stdout.String() != "" || stderr.String() != want {
		t.Errorf("orbit ls = %d, %q, %q, want %d, no stdout, %q", code, stdout.String(), stderr.String(), exitFailure, want)
	}
}

func TestOrbitRegistersStart(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"start", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit start --help = %d, %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Start the sandbox of the current directory") {
		t.Errorf("orbit start --help = %q, want the command", stdout.String())
	}
}

func TestStartRunsTheCheckScriptsOfProvisioning(t *testing.T) {
	project := projectInWorkingDir(t)
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.States[name] = orbstacktest.StateStopped
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(startCommand(fake)), []string{"start"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("orbit start = %d, %s", code, stderr.String())
	}
	var scripts []string
	for _, call := range fake.Calls {
		if call.Op == orbstacktest.OpRunAsRoot {
			scripts = append(scripts, call.Script)
		}
	}
	if want := []string{string(provision.BootCheckScript()), string(provision.MarkerCheckScript())}; !slices.Equal(scripts, want) {
		t.Errorf("root scripts = %q, want the boot check and the marker check of provisioning", scripts)
	}
}

func TestOrbitRegistersShell(t *testing.T) {
	root, err := newOrbit()
	if err != nil {
		t.Fatalf("newOrbit = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), root, []string{"shell", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit shell --help = %d, %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Open a shell as the agent user in the sandbox of the current directory") {
		t.Errorf("orbit shell --help = %q, want the command", stdout.String())
	}
}

// runningSandbox returns a fake with the running sandbox of a new project
// directory, which is the working directory of the test.
func runningSandbox(t *testing.T) (*orbstacktest.Fake, string, string) {
	t.Helper()
	project := projectInWorkingDir(t)
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	return fake, project, name
}

func TestShellRunsTheReadinessScriptOfProvisioning(t *testing.T) {
	fake, project, name := runningSandbox(t)
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(shellCommand(fake)), []string{"shell"}, &stdout, &stderr)

	if code != exitOK || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("orbit shell = %d, %q, %q, want 0 and no output", code, stdout.String(), stderr.String())
	}
	want := []orbstacktest.Call{
		{Op: orbstacktest.OpState, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpActiveMounts, Name: name},
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: string(provision.ReadinessScript())},
		{Op: orbstacktest.OpShell, Name: name, Dir: project},
	}
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

func TestShellExitsWithTheStatusOfTheShellAndPrintsNothing(t *testing.T) {
	fake, _, _ := runningSandbox(t)
	fake.ShellStatus = 3
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(shellCommand(fake)), []string{"shell"}, &stdout, &stderr)

	if code != 3 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("orbit shell = %d, %q, %q, want 3 and no output", code, stdout.String(), stderr.String())
	}
}

func TestShellInAStoppedSandboxNamesStart(t *testing.T) {
	fake, project, name := runningSandbox(t)
	fake.States[name] = orbstacktest.StateStopped
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(shellCommand(fake)), []string{"shell"}, &stdout, &stderr)

	want := "orbit: sandbox " + name + ` is stopped: run "orbit start" in ` + project + "\n"
	if code != exitFailure || stderr.String() != want {
		t.Errorf("orbit shell = %d, %q, want %d, %q", code, stderr.String(), exitFailure, want)
	}
}

func TestShellWithoutASandboxNamesInit(t *testing.T) {
	project := projectInWorkingDir(t)
	fake := orbstacktest.New(sandbox.NamePrefix)
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(shellCommand(fake)), []string{"shell"}, &stdout, &stderr)

	want := "orbit: no sandbox for " + project + `: run "orbit init" here, or run "orbit shell" in the directory where you ran "orbit init"` + "\n"
	if code != exitFailure || stderr.String() != want {
		t.Errorf("orbit shell = %d, %q, want %d, %q", code, stderr.String(), exitFailure, want)
	}
}

func TestStopWithoutASandboxNamesInit(t *testing.T) {
	project := projectInWorkingDir(t)
	fake := orbstacktest.New(sandbox.NamePrefix)
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(stopCommand(fake)), []string{"stop"}, &stdout, &stderr)

	want := "orbit: no sandbox for " + project + `: run "orbit init" here, or run this command in the directory where you ran "orbit init"` + "\n"
	if code != exitFailure || stderr.String() != want {
		t.Errorf("orbit stop = %d, %q, want %d, %q", code, stderr.String(), exitFailure, want)
	}
}

func TestDestroyWithoutASandboxNamesInit(t *testing.T) {
	project := projectInWorkingDir(t)
	fake := orbstacktest.New(sandbox.NamePrefix)
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(destroyCommand(fake)), []string{"destroy", "--yes"}, &stdout, &stderr)

	want := "orbit: no sandbox for " + project + `: run "orbit init" here, or run this command in the directory where you ran "orbit init"` + "\n"
	if code != exitFailure || stderr.String() != want {
		t.Errorf("orbit destroy = %d, %q, want %d, %q", code, stderr.String(), exitFailure, want)
	}
}

func TestDestroyWithoutATerminalAndWithoutYesDeletesNothing(t *testing.T) {
	project := projectInWorkingDir(t)
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	root := newRoot(destroyCommand(fake))
	root.SetIn(strings.NewReader("y\n"))
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), root, []string{"destroy"}, &stdout, &stderr)

	if code != exitFailure || !strings.Contains(stderr.String(), `run "orbit destroy --yes"`) || stdout.Len() != 0 {
		t.Errorf("orbit destroy = %d, %q, %q, want a refusal that names --yes", code, stdout.String(), stderr.String())
	}
	if _, exists := fake.Machines[name]; !exists {
		t.Error("the machine was deleted")
	}
}

// projectInWorkingDir returns the canonical path of a new project directory
// that the mount rules accept, and makes it the working directory of the
// test.
func projectInWorkingDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestInitRefusesAnUnknownAgentBeforeItAsksOrbStack(t *testing.T) {
	projectInWorkingDir(t)
	agents, err := agent.NewRegistry(claude.Definition)
	if err != nil {
		t.Fatal(err)
	}
	fake := orbstacktest.New(sandbox.NamePrefix)
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), newRoot(initCommand(fake, agents)), []string{"init", "--agent", "codex"}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d", code, exitFailure)
	}
	if want := "orbit: agent: unknown agent \"codex\" (known agents: claude-code)\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestInitRefusesAHostileDomainOfADefinitionBeforeItAsksOrbStack(t *testing.T) {
	project := projectInWorkingDir(t)
	for _, domain := range []string{"*.anthropic.com", "1.2.3.4", "localhost", "Claude.AI", "example.com:22", "claude.ai\n^.*$"} {
		t.Run(domain, func(t *testing.T) {
			agents, err := agent.NewRegistry(agent.Definition{Name: "hostile", Install: "true", EgressDomains: []string{"claude.ai", domain}})
			if err != nil {
				t.Fatal(err)
			}
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err = initHandler(fake, agents).Handle(context.Background(), initialize.Input{Dir: project, Agent: "hostile"})

			if !errors.Is(err, policy.ErrInvalidHost) {
				t.Errorf("Handle = %v, want %v", err, policy.ErrInvalidHost)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

func TestInitInstallsClaudeCodeWithItsDomains(t *testing.T) {
	project := projectInWorkingDir(t)
	agents, err := agent.NewRegistry(claude.Definition)
	if err != nil {
		t.Fatal(err)
	}
	fake := orbstacktest.New(sandbox.NamePrefix)

	if _, err := initHandler(fake, agents).Handle(context.Background(), initialize.Input{Dir: project, Agent: "claude-code"}); err != nil {
		t.Fatalf("Handle = %v", err)
	}

	var rootScript, installScript string
	for _, call := range fake.Calls {
		switch call.Op {
		case orbstacktest.OpRunAsRoot:
			rootScript = call.Script
		case orbstacktest.OpRunAsAgent:
			installScript = call.Script
		}
	}
	if !strings.Contains(rootScript, `^api\.anthropic\.com$`) || !strings.Contains(rootScript, `^github\.com$`) {
		t.Error("the root script lacks the allow rules of Claude Code or of the base list")
	}
	if !strings.Contains(installScript, "\n"+claude.Definition.Install+"\n}\n") {
		t.Errorf("install script = %q, want the install of Claude Code in the group", installScript)
	}
}

// TestOrbitInitRefusesAStoppedOrbStack runs orbit init on the real adapter
// with a fake orb that reports a stopped OrbStack, and checks the composition:
// init fails with exit code 1 and an "orbit: " error that carries the orb
// status, and runs no orb command other than orb status, because orb starts a
// stopped OrbStack by itself. The advice text depends on the home of the
// account and is pinned in internal/orbstack, so this test does not pin it.
func TestOrbitInitRefusesAStoppedOrbStack(t *testing.T) {
	projectInWorkingDir(t)
	calls := filepath.Join(t.TempDir(), "calls")
	fakeOrb := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> '" + calls + "'\n" +
		"if [ \"$1\" = status ]; then echo Stopped; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(fakeOrb, "orb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeOrb)
	root, err := newOrbit()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), root, []string{"init", "--agent", "claude-code"}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.HasPrefix(stderr.String(), "orbit: ") || !strings.Contains(stderr.String(), "(orb status: Stopped)") {
		t.Errorf("stderr = %q, want an orbit: error that carries (orb status: Stopped)", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want it empty", stdout.String())
	}
	if received, err := os.ReadFile(calls); err != nil || string(received) != "status\n" {
		t.Errorf("orb ran %q, %v, want only orb status", received, err)
	}
}
