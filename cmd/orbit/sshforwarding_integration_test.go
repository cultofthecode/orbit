//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// forwardedProbes checks, in a sandbox that forwards the SSH agent, the
// scenarios of the ssh-agent-forwarding capability that need no key:
// the socket and the group, the push path to GitHub, every other SSH
// destination, and the absence of key material. A value of 0 for
// AGENT_ON_THE_MAC means that SSH_AUTH_SOCK is unset on the Mac, so the
// check that the agent reaches the socket does not run.
const forwardedProbes = `
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
as_agent() { runuser -u agent -- "$@"; }
AGENT_ON_THE_MAC=%d
PROJECT_DIR='%s'

[ "$(id -nG agent)" = "agent orbstack" ] || fail "agent has groups: $(id -nG agent), want agent orbstack"
[ -S /opt/orbstack-guest/run/host-ssh-agent.sock ] || fail "the socket of the forwarded agent is missing"

if [ "$AGENT_ON_THE_MAC" -eq 1 ]; then
  out=$(as_agent ssh-add -l 2>&1); status=$?
  case "$status:$out" in
    0:*|1:*"The agent has no identities."*) ;;
    *) fail "ssh-add -l as agent: status $status: $out";;
  esac
fi

out=$(as_agent timeout 60 ssh -T git@github.com </dev/null 2>&1 || true)
case "$out" in
  *"Permission denied (publickey)"*|*"successfully authenticated"*) ;;
  *) fail "ssh -T git@github.com did not reach GitHub: $out";;
esac
case "$out" in
  *"Host key verification failed"*|*"authenticity of host"*|*"Are you sure"*) fail "ssh -T git@github.com asked about the host key: $out";;
esac
out=$(as_agent timeout 60 ssh -T git@ssh.github.com </dev/null 2>&1 || true)
case "$out" in
  *"Permission denied (publickey)"*|*"successfully authenticated"*) ;;
  *) fail "ssh -T git@ssh.github.com did not reach GitHub: $out";;
esac

not_reached() {
  local what=$1; shift
  local out status
  out=$(as_agent timeout 30 "$@" </dev/null 2>&1); status=$?
  [ "$status" -ne 0 ] || fail "$what succeeded: $out"
  case "$out" in
    *"Permission denied"*|*"Host key verification failed"*|*"successfully authenticated"*) fail "$what reached a server: $out";;
  esac
}
not_reached "ssh to gitlab.com" ssh -o ConnectTimeout=5 -o BatchMode=yes git@gitlab.com true
not_reached "ssh -p 22 to ssh.github.com" ssh -o ConnectTimeout=5 -o BatchMode=yes -p 22 git@ssh.github.com true
not_reached "ssh to altssh.gitlab.com:443 through the proxy" ssh -o ConnectTimeout=5 -o BatchMode=yes -o "ProxyCommand=/usr/bin/nc.openbsd -X connect -x 127.0.0.1:3128 %%h %%p" -p 443 git@altssh.gitlab.com true

if [ -d /home/agent/.ssh ]; then
  [ -z "$(ls -A /home/agent/.ssh)" ] || fail "/home/agent/.ssh is not empty"
fi
found=$(grep -rIl 'PRIVATE KEY-----' /home /root 2>/dev/null | grep -v -F "$PROJECT_DIR" || true)
[ -z "$found" ] || fail "private key content in: $found"
found=$(grep -rIl 'PRIVATE KEY-----' /etc/ssh 2>/dev/null | grep -v -E '^/etc/ssh/ssh_host_[a-z0-9_]+_key$' || true)
[ -z "$found" ] || fail "private key content in /etc/ssh: $found"

[ "$failures" -eq 0 ]
`

// sshAgentOnTheMac returns 1 when SSH_AUTH_SOCK on the Mac names an existing
// socket, and 0 otherwise.
func sshAgentOnTheMac(t *testing.T) int {
	t.Helper()
	socket := os.Getenv("SSH_AUTH_SOCK")
	if info, err := os.Stat(socket); socket != "" && err == nil && info.Mode()&os.ModeSocket != 0 {
		return 1
	}
	t.Log("SSH_AUTH_SOCK on the Mac names no running agent: the check that ssh-add -l reaches the socket does not run")
	return 0
}

// checkForwarding expects that orb list reports forwarded for the machine
// name.
func checkForwarding(ctx context.Context, t *testing.T, client *orbstack.Client, name string, forwarded bool) {
	t.Helper()
	got, err := client.SSHAgentForwarded(ctx, name)
	if err != nil || got != forwarded {
		t.Errorf("SSHAgentForwarded(%q) = %t, %v, want %t", name, got, err, forwarded)
	}
}

// TestInitForwardsTheSSHAgentByDefaultAndRefusesTheOtherSetting creates a
// sandbox with the default init, probes the push path, the other SSH
// destinations, and the key material, and then runs init with
// --no-ssh-forwarding on the same sandbox, which must refuse before it
// touches the machine.
func TestInitForwardsTheSSHAgentByDefaultAndRefusesTheOtherSetting(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	out, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t)})
	if err != nil || !out.Created {
		t.Fatalf("Handle = %+v, %v, want a created sandbox; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	checkForwarding(ctx, t, client, name, true)
	script := fmt.Sprintf(forwardedProbes, sshAgentOnTheMac(t), dir)
	if err := client.RunAsRoot(ctx, name, []byte(script)); err != nil {
		t.Errorf("probes in %s:\n%v", name, err)
	}
	mounts, err := client.Mounts(ctx)
	if err != nil || len(mounts[name]) != 1 || mounts[name][dir] != dir {
		t.Errorf("Mounts()[%q] = %v, %v, want only the project directory %s", name, mounts[name], err, dir)
	}

	before := time.Now().Unix() + 1
	time.Sleep(2 * time.Second)
	refused, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), NoSSHForwarding: true})
	if !errors.Is(err, sandbox.ErrSSHForwardingMismatch) || refused != (initialize.Output{}) {
		t.Fatalf("Handle with NoSSHForwarding = %+v, %v, want %v", refused, err, sandbox.ErrSSHForwardingMismatch)
	}
	if !strings.Contains(err.Error(), "--no-ssh-forwarding") {
		t.Errorf("error %q does not name --no-ssh-forwarding", err)
	}
	untouched := fmt.Sprintf(`[ "$(stat -c %%Y /etc/orbit/provisioned)" -lt %d ] || { echo "the refused init provisioned the machine again" >&2; exit 1; }`, before)
	if err := client.RunAsRoot(ctx, name, []byte(untouched)); err != nil {
		t.Errorf("machine %s after the refused init:\n%v", name, err)
	}
	if state, err := client.State(ctx, name); err != nil || state != "running" {
		t.Errorf("State(%q) after the refused init = %q, %v, want running and untouched", name, state, err)
	}
	checkForwarding(ctx, t, client, name, true)
}

// TestInitLeavesTheSSHAgentOutOnRequest creates a sandbox with
// --no-ssh-forwarding and probes that no socket, variable, or group is
// there, and that init without the flag then refuses it.
func TestInitLeavesTheSSHAgentOutOnRequest(t *testing.T) {
	waitToRunInit(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	out, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), NoSSHForwarding: true})
	if err != nil || !out.Created {
		t.Fatalf("Handle = %+v, %v, want a created sandbox; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	checkForwarding(ctx, t, client, name, false)
	if err := client.RunAsRoot(ctx, name, []byte(unforwardedProbes)); err != nil {
		t.Errorf("probes in %s:\n%v", name, err)
	}
	again, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), NoSSHForwarding: true})
	if err != nil || again.Created {
		t.Errorf("second Handle with NoSSHForwarding = %+v, %v, want the existing sandbox provisioned again", again, err)
	}
	if _, err := handler.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t)}); !errors.Is(err, sandbox.ErrSSHForwardingMismatch) {
		t.Errorf("Handle without the flag on a sandbox without forwarding = %v, want %v", err, sandbox.ErrSSHForwardingMismatch)
	}
}
