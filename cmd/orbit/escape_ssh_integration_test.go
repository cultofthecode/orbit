//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// throwawayComment names the key that E15 loads into its own SSH agent.
const throwawayComment = "orbit-escape-throwaway"

// noSSHAgentTry is E15: the throwaway key of the test agent on the Mac is
// not visible in the machine, through any socket that lists keys. The
// machine has an agent of its own at /run/user/1000/openssh_agent, which
// answers with no identities; a socket that answers is allowed, a socket
// that lists the throwaway key is the escape (owner, 2026-11-12).
//
// On OrbStack 2.2.5 the key check cannot fail: OrbStack forwards the agent
// that OrbStack itself is connected to, not the SSH_AUTH_SOCK of the process
// that calls orb, so the throwaway key never reaches a machine, even with
// forwarding on. The evidence of E15 is the other checks: no forwarded
// socket, no SSH_AUTH_SOCK, and the agent not in orbstack. The owner accepted
// this limit on 2026-11-12 (spec A4). #160 tracks a stronger check.
const noSSHAgentTry = `need ssh-add find timeout
KEY='@KEY@'
[ ! -e /opt/orbstack-guest/run/host-ssh-agent.sock ] || fail "the socket of a forwarded agent exists"
[ -z "$(env | grep '^SSH_AUTH_SOCK=' || true)" ] || fail "SSH_AUTH_SOCK is set for the agent"
if id -nG | tr ' ' '\n' | grep -qx orbstack; then fail "the agent is in the group orbstack"; fi
for socket in $(find / \( -path /proc -o -path /sys \) -prune -o -type s -print 2>/dev/null); do
  keys=$(SSH_AUTH_SOCK="$socket" timeout 5 ssh-add -L 2>/dev/null || true)
  case "$keys" in *"$KEY"*) fail "$socket lists the throwaway key of the test agent on the Mac";; esac
done
`

// testSSHAgent is an SSH agent on the Mac that the test owns, with one
// throwaway key. It never touches the agent or the keys of the user.
type testSSHAgent struct {
	socket, publicKey string
}

// startTestSSHAgent generates a throwaway key in a fake HOME, starts an SSH
// agent of the test on a socket of its own, loads the key into it, and
// points SSH_AUTH_SOCK of the test process at it, so that init and every
// orb call of the test carry this agent and never the agent of the user.
// The socket lies under /tmp, because macOS limits the path of a unix socket
// to 104 bytes. The agent stops and the files are removed when the test
// ends.
func startTestSSHAgent(t *testing.T) (testSSHAgent, error) {
	t.Helper()
	home := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "orbit-e15-")
	if err != nil {
		return testSSHAgent{}, err
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	agent := testSSHAgent{socket: filepath.Join(socketDir, "agent.sock")}
	key := filepath.Join(home, "throwaway")
	env := append(os.Environ(), "HOME="+home, "SSH_AUTH_SOCK="+agent.socket)
	if out, err := commandIn(env, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", throwawayComment, "-f", key).CombinedOutput(); err != nil {
		return agent, fmt.Errorf("ssh-keygen: %w: %s", err, out)
	}
	if err := startSSHAgentProcess(t, env, agent.socket); err != nil {
		return agent, err
	}
	if out, err := commandIn(env, "ssh-add", key).CombinedOutput(); err != nil {
		return agent, fmt.Errorf("ssh-add of the throwaway key: %w: %s", err, out)
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		return agent, err
	}
	fields := strings.Fields(string(public))
	if len(fields) < 2 {
		return agent, fmt.Errorf("malformed public key %q", public)
	}
	agent.publicKey = fields[1]
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

// listsTheThrowawayKey is the control of E15: the test agent on the Mac
// lists the throwaway key, so a forwarding of this agent would show it.
func (a testSSHAgent) listsTheThrowawayKey(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "ssh-add", "-L").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-add -L on the test agent %s: %w: %s", a.socket, err, out)
	}
	if !strings.Contains(string(out), a.publicKey) {
		return errors.New("the test agent on the Mac does not list the throwaway key")
	}
	return nil
}

// TestEscapeNoSSHForwarding runs E15 in a sandbox created with
// --no-ssh-forwarding, while the test agent on the Mac holds a throwaway
// key.
func TestEscapeNoSSHForwarding(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	agent, err := startTestSSHAgent(t)
	if err != nil {
		t.Fatalf("CONTROL FAILED: E15-no-ssh-agent: no test agent with a throwaway key on the Mac: %v", err)
	}
	dir, name := newSandboxDir(ctx, t, client)
	out, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir, NoSSHForwarding: true})
	if err != nil || !out.Created {
		t.Fatalf("init --no-ssh-forwarding = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	checkForwarding(ctx, t, client, name, false)
	rig := escapeRig{client: client, name: name, tally: &counts}
	try := strings.ReplaceAll(noSSHAgentTry, "@KEY@", agent.publicKey)
	rig.run(ctx, t, attempt{id: "E15-no-ssh-agent", rule: "R08 D25", as: agentSession, controlFn: agent.listsTheThrowawayKey, try: try})
}
