//go:build integration

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/agent"
	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// withClaudeProbes checks a sandbox after init --agent claude-code.
const withClaudeProbes = agentProbes + `
version=$(as_agent "$claude" --version) || fail "claude --version as agent: $version"
echo "claude --version: $version" >&2
[ "$(stat -c %U "$claude")" = agent ] || fail "$claude is not owned by agent"
foreign=$(find /home/agent -xdev ! -user agent -print -quit)
[ -z "$foreign" ] || fail "$foreign under /home/agent is not owned by agent"
tunnel api.anthropic.com
tunnel claude.ai
tunnel github.com
refused example.org
[ "$failures" -eq 0 ]
`

// withoutAgentProbes checks the same sandbox after a later init without
// --agent: the hosts of Claude Code are refused, the client stays, and
// Orbit wrote no record of the agent.
const withoutAgentProbes = agentProbes + `
refused api.anthropic.com
refused claude.ai
tunnel github.com
[ -x "$claude" ] || fail "the client was removed"
[ "$(ls -A /etc/orbit)" = provisioned ] || fail "/etc/orbit holds $(ls -A /etc/orbit)"
[ "$failures" -eq 0 ]
`

// claudeRegistry returns the registry of cmd/orbit.
func claudeRegistry(t *testing.T) *agent.Registry {
	t.Helper()
	agents, err := agent.NewRegistry(claude.Definition)
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

// newSandboxDir returns a new project directory whose machine does not exist
// yet, and registers the deletion of that machine when it exists at the end
// of the test.
func newSandboxDir(ctx context.Context, t *testing.T, client *orbstack.Client) (string, string) {
	t.Helper()
	dir := projectDir(t)
	name := sandbox.MachineName(dir)
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Fatalf("Exists(%q) before init = %v, %v, want false", name, exists, err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		if exists, err := client.Exists(cleanupCtx, name); err != nil || !exists {
			return
		}
		if err := client.Delete(cleanupCtx, name); err != nil {
			t.Errorf("Delete(%q) = %v", name, err)
		}
	})
	return dir, name
}

func TestInitInstallsClaudeCodeAndDropsItsHostsWithoutAgent(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	first, err := handler.Handle(ctx, initialize.Input{Dir: dir, Agent: claude.Definition.Name})
	if err != nil || !first.Created {
		t.Fatalf("Handle with claude-code = %+v, %v, want a created sandbox; machines that this test did not create: %s", first, err, otherMachines(ctx, client, name))
	}
	if err := client.RunAsRoot(ctx, name, []byte(withClaudeProbes)); err != nil {
		t.Errorf("probes with Claude Code in %s:\n%v", name, err)
	}

	second, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || second.Created {
		t.Fatalf("Handle without agent = %+v, %v, want the existing sandbox provisioned again; machines that this test did not create: %s", second, err, otherMachines(ctx, client, name))
	}
	if err := client.RunAsRoot(ctx, name, []byte(withoutAgentProbes)); err != nil {
		t.Errorf("probes without agent in %s:\n%v", name, err)
	}
}

// TestInitDeletesTheNewMachineWhenTheDownloadIsRefused installs an agent
// whose installer lies on a host that the proxy refuses: curl fails, and
// bash, which reads no script, exits 0. Only the failure of the install
// passes: a refusal of init fails the test.
func TestInitDeletesTheNewMachineWhenTheDownloadIsRefused(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	refused := agent.Definition{Name: "refused-download", Install: "curl -fsSL https://example.org/install.sh | bash"}
	agents, err := agent.NewRegistry(refused)
	if err != nil {
		t.Fatal(err)
	}

	out, err := initHandler(client, agents).Handle(ctx, initialize.Input{Dir: dir, Agent: refused.Name})

	if err == nil || !strings.Contains(err.Error(), "install the agent in sandbox "+name) {
		t.Fatalf("Handle = %+v, %v, want the install to fail; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	t.Logf("Handle = %v", err)
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Errorf("Exists(%q) after the failed install = %v, %v, want false", name, exists, err)
	}
}
