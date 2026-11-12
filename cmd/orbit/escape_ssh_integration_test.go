//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// noSSHAgentTry is E15: no socket of an SSH agent is reachable. A socket
// answers when ssh-add -l exits 0 (an agent with identities) or 1 (an agent
// with none); exit 2 means no agent.
const noSSHAgentTry = `need ssh-add find timeout
[ ! -e /opt/orbstack-guest/run/host-ssh-agent.sock ] || fail "the socket of a forwarded agent exists"
[ -z "$(env | grep '^SSH_AUTH_SOCK=' || true)" ] || fail "SSH_AUTH_SOCK is set for the agent"
if id -nG | tr ' ' '\n' | grep -qx orbstack; then fail "the agent is in the group orbstack"; fi
for socket in $(find / \( -path /proc -o -path /sys \) -prune -o -type s -print 2>/dev/null); do
  status=0
  SSH_AUTH_SOCK="$socket" timeout 5 ssh-add -l >/dev/null 2>&1 || status=$?
  case "$status" in 0|1) fail "$socket answers ssh-add -l with exit $status";; esac
done
`

// sshAgentAnswersOnTheMac is the control of E15: an agent exists on the Mac,
// so a forwarding would have something to forward.
func sshAgentAnswersOnTheMac(ctx context.Context) error {
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return errors.New("SSH_AUTH_SOCK is unset on the Mac: run the suite from a shell that has an SSH agent")
	}
	err := exec.CommandContext(ctx, "ssh-add", "-l").Run()
	var exit *exec.ExitError
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil
	}
	return errors.New("ssh-add -l on the Mac does not answer with exit 0 or 1: " + err.Error())
}

// TestEscapeNoSSHForwarding runs E15 in a sandbox created with
// --no-ssh-forwarding.
func TestEscapeNoSSHForwarding(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	out, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir, NoSSHForwarding: true})
	if err != nil || !out.Created {
		t.Fatalf("init --no-ssh-forwarding = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	checkForwarding(ctx, t, client, name, false)
	rig := escapeRig{client: client, name: name, tally: &counts}
	rig.run(ctx, t, attempt{id: "E15-no-ssh-agent", rule: "R08 D25", as: agentSession, controlFn: sshAgentAnswersOnTheMac, try: noSSHAgentTry})
}
