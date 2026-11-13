//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// sshLeakDetectors defines ssh_leaks, which prints one word for each sign
// that the SSH agent of the Mac reaches the agent user: "socket" for the
// socket of a forwarded agent, "env" for SSH_AUTH_SOCK, and "group" for the
// agent in the group orbstack. It never lists or uses a key of any agent
// (D08).
const sshLeakDetectors = `ssh_leaks() {
  if [ -e /opt/orbstack-guest/run/host-ssh-agent.sock ]; then echo socket; fi
  if env | grep -q '^SSH_AUTH_SOCK='; then echo env; fi
  if id -nG | tr ' ' '\n' | grep -qx orbstack; then echo group; fi
}
`

// noSSHAgentTry is E15: in a sandbox created with --no-ssh-forwarding no
// sign of a forwarded SSH agent reaches the agent user. The machine has an
// agent of its own at /run/user/1000/openssh_agent, which answers with no
// identities; it is not the agent of the Mac and is allowed (owner,
// 2026-11-12).
//
// E15 has no key check. On OrbStack 2.2.5 OrbStack forwards the agent that
// OrbStack itself is connected to, not the SSH_AUTH_SOCK of the process that
// calls orb, so a throwaway key in a test-owned agent never reaches a
// machine, even with forwarding on, and a check for it cannot fail (spec A4,
// #160). The evidence is the three detectors of sshLeakDetectors, and the
// control of E15 shows that each of them fires in a sandbox with forwarding.
const noSSHAgentTry = sshLeakDetectors + `leaks=$(ssh_leaks)
[ -z "$leaks" ] || fail "signs of a forwarded SSH agent for the agent user: $(echo $leaks)"
`

// forwardedDetectorsControl is the control of E15, run as the agent user in a
// sandbox created with forwarding: every detector of E15 fires there.
const forwardedDetectorsControl = sshLeakDetectors + `leaks=" $(echo $(ssh_leaks)) "
for detector in socket env group; do
  case "$leaks" in *" $detector "*) ;; *) fail "the detector $detector did not fire in a sandbox with forwarding";; esac
done
`

// testSSHAgent is an SSH agent on the Mac that the test owns, with no key.
type testSSHAgent struct {
	socket string
}

// startTestSSHAgent starts an SSH agent of the test on a socket of its own
// and points SSH_AUTH_SOCK of the test process at it, so that init and every
// orb call of the test carry this agent and never the agent of the user. The
// socket lies under /tmp, because macOS limits the path of a unix socket to
// 104 bytes. The agent stops and the socket is removed when the test ends.
func startTestSSHAgent(t *testing.T) (testSSHAgent, error) {
	t.Helper()
	socketDir, err := os.MkdirTemp("/tmp", "orbit-e15-")
	if err != nil {
		return testSSHAgent{}, err
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	agent := testSSHAgent{socket: filepath.Join(socketDir, "agent.sock")}
	env := append(os.Environ(), "SSH_AUTH_SOCK="+agent.socket)
	if err := startSSHAgentProcess(t, env, agent.socket); err != nil {
		return agent, err
	}
	t.Setenv("SSH_AUTH_SOCK", agent.socket)
	return agent, nil
}

// startSSHAgentProcess runs ssh-agent in the foreground on socket until the
// test ends, and waits until the socket exists.
func startSSHAgentProcess(t *testing.T, env []string, socket string) error {
	t.Helper()
	process := commandIn(env, "ssh-agent", "-D", "-a", socket)
	if err := process.Start(); err != nil {
		return fmt.Errorf("ssh-agent: %w", err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(socket); err == nil {
			return nil
		}
	}
	return fmt.Errorf("ssh-agent did not create %s", socket)
}

// commandIn returns the command name with args and the environment env.
func commandIn(env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	return cmd
}

// TestEscapeNoSSHForwarding runs E15 in a sandbox created with
// --no-ssh-forwarding. Its control is a second sandbox, created with
// forwarding, in which the detectors of E15 must fire. SSH_AUTH_SOCK of the
// test process names an agent of the test with no key, so that init never
// carries the agent of the user. The control sandbox still receives the agent
// that OrbStack 2.2.5 forwards on its own, the one of the Mac (spec A4); the
// test never queries it, and the sandbox is deleted at the end.
func TestEscapeNoSSHForwarding(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	if _, err := startTestSSHAgent(t); err != nil {
		t.Fatalf("CONTROL FAILED: E15-no-ssh-agent: no test agent on the Mac: %v", err)
	}
	dir, name := newSandboxDir(ctx, t, client)
	out, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t), NoSSHForwarding: true})
	if err != nil || !out.Created {
		t.Fatalf("init --no-ssh-forwarding = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	checkForwarding(ctx, t, client, name, false)
	forwardedDir, forwardedName := newSandboxDir(ctx, t, client)
	forwardedRig := escapeRig{client: client, name: forwardedName}
	var once sync.Once
	var controlErr error
	setUpControl := func(ctx context.Context) error {
		out, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: forwardedDir, Distro: integrationDistro(t)})
		if err != nil || !out.Created {
			return fmt.Errorf("init with forwarding = %+v, %v", out, err)
		}
		if forwarded, err := client.SSHAgentForwarded(ctx, forwardedName); err != nil || !forwarded {
			return fmt.Errorf("SSHAgentForwarded(%q) = %t, %v, want true", forwardedName, forwarded, err)
		}
		return forwardedRig.runIn(ctx, agentSession, forwardedDetectorsControl)
	}
	control := func(ctx context.Context) error {
		once.Do(func() { controlErr = setUpControl(ctx) })
		return controlErr
	}
	rig := escapeRig{client: client, name: name, tally: &counts}
	rig.run(ctx, t, attempt{id: "E15-no-ssh-agent", rule: "R08 D25", as: agentSession, controlFn: control, try: noSSHAgentTry})
	buildProbeImage(ctx, t, rig)
	if rig.privilegedGate(ctx, t, integrationDistro(t), "E21") {
		rig.runAll(ctx, t, privilegedSSHAttempts(control, rig))
	}
}
