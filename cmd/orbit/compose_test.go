package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
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
