//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/start"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// signInTimeout bounds the sign-in test when the test binary has no
// deadline: two runs of init, one with the install of Claude Code, one stop,
// one start, and the probes.
const signInTimeout = 20 * time.Minute

// credentialVariables are the variables of the provider that the client
// reads before or instead of /login. The test sets each to a marker, never
// a real credential, and names it in ORBENV and WSLENV.
var credentialVariables = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"CLAUDE_CONFIG_DIR",
}

// Files of the agent that the test writes. None is a credential: the test
// never reads or writes .credentials.json.
const (
	sentinelFile = "/home/agent/.claude/orbit-it-sentinel"
	sentinelText = "written before init, stop, and start"
	accountSum   = "/home/agent/orbit-it-claude-json.sha256"
)

// agentEnvironmentProbes runs as the agent. Its first argument is the
// marker. It checks that no variable of the provider, no ORBENV, WSLENV, or
// marker value reached the environment, that the client reports that it is
// not logged in and names /login, and then records the checksum of
// ~/.claude.json, which the client creates and which holds no token. It
// never prints the file.
const agentEnvironmentProbes = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
for name in %[1]s ORBENV WSLENV; do
  if [ -n "$(printenv "$name" || true)" ]; then fail "$name reached the agent"; fi
done
if env | grep -qF '%[2]s'; then fail "a marker value reached the agent"; fi
set -a; . /etc/environment; set +a
out=$(timeout 120 /home/agent/.local/bin/claude -p hi </dev/null 2>&1 || true)
case "$out" in
  *"Not logged in"*"/login"*) ;;
  *) fail "claude -p hi did not report that it is not logged in and name /login: $out" ;;
esac
[ -f /home/agent/.claude.json ] || fail "/home/agent/.claude.json does not exist after claude -p hi"
sha256sum /home/agent/.claude.json > ` + accountSum + `
mkdir -p /home/agent/.claude
printf '%%s\n' '` + sentinelText + `' > ` + sentinelFile + `
[ "$failures" -eq 0 ]
`

// noHostClaudeProbes runs as root. Its first argument is the home directory
// of the user on the host. Neither ~/.claude nor ~/.claude.json of the host
// exists in the machine.
const noHostClaudeProbes = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
for path in '%[1]s/.claude' '%[1]s/.claude.json'; do
  if [ -e "$path" ] || [ -L "$path" ]; then fail "$path exists in the machine"; fi
done
[ "$failures" -eq 0 ]
`

// homeKeptProbes runs as the agent: the sentinel is intact and the checksum
// of ~/.claude.json is the recorded one.
const homeKeptProbes = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
[ "$(cat ` + sentinelFile + `)" = '` + sentinelText + `' ] || fail "` + sentinelFile + ` changed or is gone"
sha256sum -c --quiet ` + accountSum + ` || fail "the checksum of /home/agent/.claude.json changed"
[ "$failures" -eq 0 ]
`

// TestSignInKeepsTheLoginAndTakesNoHostCredential covers the guards of #7
// without a sign-in: host variables of the provider, named by ORBENV and
// WSLENV, do not reach the agent, and the client reports that it is not
// logged in (no key is needed or set); the host ~/.claude and
// ~/.claude.json are not in the machine, which mounts only the project
// directory; and the home of the agent, with its ~/.claude and
// ~/.claude.json, survives a second init --agent claude-code, orbit stop,
// and orbit start. It never reads or writes .credentials.json.
func TestSignInKeepsTheLoginAndTakesNoHostCredential(t *testing.T) {
	lockInit(t, syscall.LOCK_SH)
	ctx := boundedContext(t, signInTimeout)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	marker := setCredentialVariablesOnTheHost(t)
	setUp := initHandler(client, claudeRegistry(t))
	if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Agent: claude.Definition.Name}); err != nil {
		t.Fatalf("init --agent claude-code in %s = %v; machines that this test did not create: %s", dir, err, otherMachines(ctx, client, name))
	}

	checkAgentEnvironment(ctx, t, client, name, marker)
	checkNoHostClaude(ctx, t, client, name, dir)
	checkHomeKept(ctx, t, client, name, "after the first init")

	if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Agent: claude.Definition.Name}); err != nil {
		t.Fatalf("second init --agent claude-code in %s = %v", dir, err)
	}
	checkHomeKept(ctx, t, client, name, "after the second init")

	if _, err := (stop.Handler{Machines: client}).Handle(ctx, stop.Input{Dir: dir}); err != nil {
		t.Fatalf("stop in %s = %v", dir, err)
	}
	if _, err := startHandler(client).Handle(ctx, start.Input{Dir: dir}); err != nil {
		t.Fatalf("start in %s = %v", dir, err)
	}
	checkHomeKept(ctx, t, client, name, "after stop and start")
	expectOrbStackRunning(t)
}

// setCredentialVariablesOnTheHost sets every credentialVariables entry to a
// marker value (CLAUDE_CONFIG_DIR to an absolute path that ends in it, which
// the project directory check requires), and ORBENV and WSLENV to their
// names, in the environment of the test. It returns the marker.
func setCredentialVariablesOnTheHost(t *testing.T) string {
	t.Helper()
	marker := "orbit-it-marker-" + strings.ToLower(rand.Text()[:8])
	for _, variable := range credentialVariables {
		t.Setenv(variable, marker)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), marker))
	t.Setenv("ORBENV", strings.Join(credentialVariables, ":"))
	t.Setenv("WSLENV", strings.Join(credentialVariables, ":"))
	return marker
}

// checkAgentEnvironment runs agentEnvironmentProbes as the agent.
func checkAgentEnvironment(ctx context.Context, t *testing.T, client *orbstack.Client, name, marker string) {
	t.Helper()
	script := fmt.Sprintf(agentEnvironmentProbes, strings.Join(credentialVariables, " "), marker)
	if err := client.RunAsAgent(ctx, name, []byte(script)); err != nil {
		t.Errorf("environment probes as agent in %s:\n%v", name, err)
	}
}

// checkNoHostClaude runs noHostClaudeProbes as root and expects the project
// directory as the only mount.
func checkNoHostClaude(ctx context.Context, t *testing.T, client *orbstack.Client, name, dir string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RunAsRoot(ctx, name, []byte(fmt.Sprintf(noHostClaudeProbes, home))); err != nil {
		t.Errorf("host ~/.claude probes as root in %s:\n%v", name, err)
	}
	mounts, err := client.ActiveMounts(ctx, name)
	if want := map[string]string{dir: dir}; err != nil || !maps.Equal(mounts, want) {
		t.Errorf("ActiveMounts(%s) = %v, %v, want exactly %v", name, mounts, err, want)
	}
}

// checkHomeKept runs homeKeptProbes as the agent.
func checkHomeKept(ctx context.Context, t *testing.T, client *orbstack.Client, name, when string) {
	t.Helper()
	if err := client.RunAsAgent(ctx, name, []byte(homeKeptProbes)); err != nil {
		t.Errorf("home of the agent in %s %s:\n%v", name, when, err)
	}
}
