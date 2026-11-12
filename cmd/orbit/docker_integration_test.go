//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/start"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// dockerTimeout bounds the Docker integration test: provisioning with the
// engine, pulls, three image builds, and four restarts of the machine.
const dockerTimeout = 60 * time.Minute

// bootFailureDropIn is the test drop-in that makes nftables.service fail.
const bootFailureDropIn = "/etc/systemd/system/nftables.service.d/zz-orbit-it-fail.conf"

// pullBuildRun is the scenario "Pull, build, and run" without the proxy log.
const pullBuildRun = agentHelpers + `
docker info --format '{{json .SecurityOptions}}' | grep -q rootless || fail "docker info does not report rootless"
allowed "pull alpine" docker pull alpine
allowed "pull ghcr.io/containerd/busybox" docker pull ghcr.io/containerd/busybox
build() {
  local tag=$1 content=$2 dir
  dir=$(mktemp -d)
  printf '%b' "$content" > "$dir/Dockerfile"
  docker build -q -t "$tag" "$dir" >/dev/null 2>"$dir/err" || fail "build $tag: $(tail -3 "$dir/err")"
}
build orbit-it-debian 'FROM debian:trixie\nRUN apt-get update && apt-get install -y curl\n'
build orbit-it-ubuntu 'FROM ubuntu:26.04\nRUN apt-get update && apt-get install -y curl\n'
build orbit-it-curl 'FROM alpine\nRUN apk add curl\n'
allowed "run alpine" docker run --rm alpine true
finish
`

// proxyLog checks the log of tinyproxy for the requests of pullBuildRun.
const proxyLog = `
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
log=/var/log/tinyproxy/tinyproxy.log
connected() { grep -Eq "Request \(file descriptor [0-9]+\): CONNECT $1:443 " "$log" || fail "no CONNECT $1:443 in the log"; }
plain() { grep -Eq "Request \(file descriptor [0-9]+\): GET http://$1/" "$log" || fail "no plain HTTP request to $1 in the log"; }
for host in registry-1.docker.io ghcr.io pkg-containers.githubusercontent.com dl-cdn.alpinelinux.org; do connected "$host"; done
plain deb.debian.org
grep -Eq "Request \(file descriptor [0-9]+\): GET http://(ports|archive).ubuntu.com/" "$log" || fail "no plain HTTP request to the Ubuntu mirror in the log"
grep 'CONNECT \(ghcr.io\|pkg-containers.githubusercontent.com\|dl-cdn.alpinelinux.org\):443' "$log" | head -3
[ "$failures" -eq 0 ]
`

// refusals is the scenarios "Package source outside the allowlist" and
// "Registry outside the allowlist", as the agent.
const refusals = agentHelpers + `
dir=$(mktemp -d)
printf '%b' "FROM debian:trixie\nRUN echo 'deb http://security.debian.org/debian-security trixie-security main' > /etc/apt/sources.list.d/x.list && apt-get update\n" > "$dir/Dockerfile"
blocked "build with a source outside the allowlist" docker build -q "$dir"
blocked "pull from quay.io" docker pull quay.io/prometheus/busybox
finish
`

// refusalLog checks that the proxy refused both hosts.
const refusalLog = `
log=/var/log/tinyproxy/tinyproxy.log
for host in security.debian.org quay.io; do
  grep -q "Proxying refused on filtered domain \"$host\"" "$log" || { echo "PROBE FAILED: the proxy did not refuse $host" >&2; exit 1; }
done
`

// composeScenario is the scenario "Docker Compose".
const composeScenario = agentHelpers + `
docker compose version | grep -Eq 'version v?2\.' || fail "docker compose is not v2"
dir=$(mktemp -d); cd "$dir"
printf 'services:\n  app:\n    image: alpine\n    command: ["true"]\n' > compose.yaml
docker compose -p orbitit up --abort-on-container-exit --exit-code-from app >/dev/null 2>&1 || fail "docker compose up failed or app exited non-zero"
docker network ls --format '{{.Name}}' | grep -qx orbitit_default || fail "up did not create the network of the project"
[ -n "$(docker compose -p orbitit ps -a -q)" ] || fail "up did not create the container"
docker compose -p orbitit down >/dev/null 2>&1 || fail "docker compose down failed"
[ -z "$(docker compose -p orbitit ps -a -q)" ] || fail "down left the container"
if docker network ls --format '{{.Name}}' | grep -qx orbitit_default; then fail "down left the network"; fi
finish
`

// replacedDaemon is the scenario "Daemon replaced by the agent, in the
// network namespace of the machine". The script of Ubuntu refuses
// DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host, so the agent starts a copy of it
// without that check, as the agent can do with any flag.
const replacedDaemon = agentHelpers + leakProbe + `
systemctl --user stop docker.service
sed 's/\[ "\$net" = host \]/false/' /usr/share/docker.io/contrib/dockerd-rootless.sh > ~/dockerd-hostnet.sh && chmod +x ~/dockerd-hostnet.sh
systemd-run --user --quiet --unit=orbit-it-hostnet --setenv=DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host --setenv=DOCKERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=none --setenv=DOCKERD_ROOTLESS_ROOTLESSKIT_MTU=0 --setenv=PATH=/usr/share/docker.io/contrib:/usr/sbin:/usr/bin:/sbin:/bin ~/dockerd-hostnet.sh --bridge=none --iptables=false --ip6tables=false
wait_docker && leak_probe
systemctl --user stop orbit-it-hostnet.service; systemctl --user reset-failed 2>/dev/null
systemctl --user start docker.service
wait_docker
finish
`

// agentOwnedUnit writes an agent-owned docker.service that starts the daemon
// of replacedDaemon at every boot.
const agentOwnedUnit = agentHelpers + `
sed 's/\[ "\$net" = host \]/false/' /usr/share/docker.io/contrib/dockerd-rootless.sh > ~/dockerd-hostnet.sh && chmod +x ~/dockerd-hostnet.sh
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/docker.service <<'UNIT'
[Service]
Environment=DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host
Environment=DOCKERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=none
Environment=DOCKERD_ROOTLESS_ROOTLESSKIT_MTU=0
Environment=PATH=/usr/share/docker.io/contrib:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=%h/dockerd-hostnet.sh --bridge=none --iptables=false --ip6tables=false
Delegate=yes
Restart=always
[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload
systemctl --user enable docker.service >/dev/null 2>&1 || fail "enable the agent-owned unit"
finish
`

// agentOwnedUnitAtBoot checks, as root after a restart, that the agent-owned
// unit started after the gate.
const agentOwnedUnitAtBoot = `
set -u
uid=$(id -u agent)
as_agent() { runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$uid "$@"; }
for _ in $(seq 60); do [ "$(as_agent systemctl --user is-active docker.service)" = active ] && break; sleep 1; done
fragment=$(as_agent systemctl --user show -p FragmentPath --value docker.service)
[ "$fragment" = /home/agent/.config/systemd/user/docker.service ] || { echo "PROBE FAILED: docker.service comes from $fragment, not from the home of the agent" >&2; exit 1; }
gate=$(systemctl show -p ActiveEnterTimestampMonotonic --value orbit-boundary-gate.service)
unit=$(as_agent systemctl --user show -p ActiveEnterTimestampMonotonic --value docker.service)
echo "gate active at $gate us; the unit of the agent at $unit us"
[ "${gate:-0}" -gt 0 ] && [ "${unit:-0}" -gt "${gate:-0}" ] || { echo "PROBE FAILED: the unit of the agent did not start after the gate" >&2; exit 1; }
`

// removeAgentOwnedUnit puts the daemon of provisioning back.
const removeAgentOwnedUnit = agentHelpers + `
systemctl --user disable docker.service >/dev/null 2>&1
rm -f ~/.config/systemd/user/docker.service
systemctl --user daemon-reload
systemctl --user restart docker.service
wait_docker
finish
`

// leakAfterBoot runs leakProbe as the agent after a restart.
const leakAfterBoot = agentHelpers + leakProbe + `
wait_docker && leak_probe
finish
`

// noRootfulEngine is the scenario "No rootful engine".
const noRootfulEngine = `
set -u
for unit in docker.service docker.socket containerd.service; do
  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || { echo "PROBE FAILED: $unit is not masked" >&2; exit 1; }
  if systemctl is-active --quiet "$unit"; then echo "PROBE FAILED: $unit is active" >&2; exit 1; fi
done
for socket in /var/run/docker.sock /run/docker.sock; do
  [ ! -e "$socket" ] || { echo "PROBE FAILED: $socket exists" >&2; exit 1; }
done
`

// afterRestart is the scenario "After a restart", as the agent.
const afterRestart = agentHelpers + `
wait_docker
allowed "pull after the restart" docker pull busybox:1.37
[ "$(proxied 2>/dev/null)" = 200 ] || fail "a container did not reach an allowed host after the restart"
finish
`

// proxyDown is the scenario "Proxy down", as the agent.
const proxyDown = agentHelpers + `
blocked "pull while the proxy is down" docker pull hello-world
no_direct "a container while the proxy is down"
if docker run --rm $IMG curl -sS -m 5 -o /dev/null https://github.com >/dev/null 2>&1; then fail "a container reached a host while the proxy is down"; fi
finish
`

// bootFailure is the scenario "Boundary unit failed at boot", as root after
// the restart with the failing drop-in.
const bootFailure = `
set -u
timeout 120 systemctl is-system-running --wait >/dev/null 2>&1 || true
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
if systemctl is-active --quiet nftables.service; then fail "nftables.service is active"; fi
if systemctl is-active --quiet orbit-boundary-gate.service; then fail "the gate is active"; fi
if systemctl is-active --quiet "user@$(id -u agent).service"; then fail "the user manager of the agent is active"; fi
for dir in /proc/[0-9]*; do
  uid=$(awk '/^Uid:/ {print $2}' "$dir/status" 2>/dev/null) || continue
  [ -n "$uid" ] && [ "$uid" -ge 1000 ] && fail "process ${dir#/proc/} has uid $uid"
done
echo "gate: $(systemctl is-active orbit-boundary-gate.service); user manager: $(systemctl is-active "user@$(id -u agent).service")"
[ "$failures" -eq 0 ]
`

// failNftables makes nftables.service fail at the next boot, and removeFail
// removes the drop-in.
const (
	failNftables = "set -eu\nmkdir -p /etc/systemd/system/nftables.service.d\nprintf '[Service]\\nExecStartPre=/bin/false\\n' > " + bootFailureDropIn + "\nsystemctl daemon-reload\n"
	removeFail   = "set -eu\nrm -f " + bootFailureDropIn + "\nsystemctl daemon-reload\n"
)

// repaired is the scenario "Repair after a failed boot", as root.
const repaired = `
set -u
uid=$(id -u agent)
for unit in orbit-boundary-gate.service "user@$uid.service"; do
  systemctl is-active --quiet "$unit" || { echo "PROBE FAILED: $unit is not active after init" >&2; exit 1; }
done
runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$uid systemctl --user is-active --quiet docker.service || { echo "PROBE FAILED: docker.service of the agent is not active after init" >&2; exit 1; }
`

// hostDockerVariables are the variables of the Docker client on the host
// that init must not pass into the sandbox.
var hostDockerVariables = map[string]string{
	"DOCKER_HOST":        "unix:///var/run/orbit-it-host.sock",
	"DOCKER_CONFIG":      "/tmp/orbit-it-host-docker-config",
	"DOCKER_AUTH_CONFIG": `{"auths":{"example.invalid":{"auth":"b3JiaXQ6aXQ="}}}`,
}

// noHostDockerVariables is the scenario "Host Docker variables", as the
// agent in a script and in a login shell.
const noHostDockerVariables = `
set -u
[ -z "$(env | grep '^DOCKER_' || true)" ] || { echo "PROBE FAILED: a script of the agent has $(env | grep '^DOCKER_')" >&2; exit 1; }
[ -z "$(bash -lc "env | grep '^DOCKER_'" || true)" ] || { echo "PROBE FAILED: a login shell of the agent has a DOCKER_ variable" >&2; exit 1; }
`

// orbRoot runs script as root in the machine name and returns its output.
func orbRoot(ctx context.Context, name, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "orb", "-m", name, "-u", "root", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// expectRoot runs script as root and fails t with the output when it fails.
func expectRoot(ctx context.Context, t *testing.T, name, step, script string) {
	t.Helper()
	out, err := orbRoot(ctx, name, script)
	t.Logf("%s (root):\n%s", step, out)
	if err != nil {
		t.Errorf("%s: %v", step, err)
	}
}

// expectAgent runs script as the agent in a login shell in dir and fails t
// with the output when it exits non-zero.
func expectAgent(ctx context.Context, t *testing.T, client *orbstack.Client, name, dir, step, script string) {
	t.Helper()
	var out bytes.Buffer
	status, err := client.Shell(ctx, name, dir, strings.NewReader(script), &out, &out)
	t.Logf("%s (agent):\n%s", step, out.String())
	if err != nil || status != 0 {
		t.Errorf("%s: exit status %d, %v", step, status, err)
	}
}

// orbMachine runs "orb <verb> <name>" and fails t when it fails.
func orbMachine(ctx context.Context, t *testing.T, verb, name string) {
	t.Helper()
	if out, err := exec.CommandContext(ctx, "orb", verb, name).CombinedOutput(); err != nil {
		t.Fatalf("orb %s %s: %v\n%s", verb, name, err, out)
	}
}

// restart stops and starts the sandbox of dir with orbit stop and orbit
// start.
func restart(ctx context.Context, t *testing.T, client *orbstack.Client, dir string) {
	t.Helper()
	if _, err := (stop.Handler{Machines: client}).Handle(ctx, stop.Input{Dir: dir}); err != nil {
		t.Fatalf("orbit stop: %v", err)
	}
	if _, err := startHandler(client).Handle(ctx, start.Input{Dir: dir}); err != nil {
		t.Fatalf("orbit start: %v", err)
	}
}

// initWithHostVariables runs init with the Docker variables of the host set
// in this process, as the host of a user would have them, and unsets them
// afterwards.
func initWithHostVariables(ctx context.Context, t *testing.T, client *orbstack.Client, dir string) {
	t.Helper()
	for name, value := range hostDockerVariables {
		if err := os.Setenv(name, value); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for name := range hostDockerVariables {
			_ = os.Unsetenv(name)
		}
	}()
	if _, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir}); err != nil {
		t.Fatalf("init: %v; machines that this test did not create: %s", err, otherMachines(ctx, client, sandbox.MachineName(dir)))
	}
	expectAgentVariables(ctx, t, client, dir)
}

// expectAgentVariables runs noHostDockerVariables while the variables are
// still set on the host, so that a variable that orb passes is found.
func expectAgentVariables(ctx context.Context, t *testing.T, client *orbstack.Client, dir string) {
	t.Helper()
	expectAgent(ctx, t, client, sandbox.MachineName(dir), dir, "Host Docker variables", noHostDockerVariables)
}

// TestInitGivesTheAgentRootlessDocker runs every scenario of the
// sandbox-containers capability, and the scenarios of the proxy allowlist and
// of the firewall that the engine needs, on one sandbox. The steps depend on
// each other, so they run in order.
func TestInitGivesTheAgentRootlessDocker(t *testing.T) {
	waitToRunInit(t)
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	t.Cleanup(cancel)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	t.Logf("machine %s", name)
	initWithHostVariables(ctx, t, client, dir)

	expectAgent(ctx, t, client, name, dir, "Pull, build, and run", pullBuildRun)
	expectRoot(ctx, t, name, "the requests of the builds in the log of the proxy", proxyLog)
	expectAgent(ctx, t, client, name, dir, "Package source and registry outside the allowlist", refusals)
	expectRoot(ctx, t, name, "the refusals in the log of the proxy", refusalLog)
	expectAgent(ctx, t, client, name, dir, "Docker Compose", composeScenario)
	hostContainer := hostContainerReachableByRoot(ctx, t, client, name)
	expectAgent(ctx, t, client, name, dir, "Container traffic and the boundary", strings.Replace(containerTraffic, "%s", hostContainer, 1))
	expectAgent(ctx, t, client, name, dir, "Daemon replaced by the agent", replacedDaemon)

	restart(ctx, t, client, dir)
	expectRoot(ctx, t, name, "No rootful engine after the restart", noRootfulEngine)
	expectAgent(ctx, t, client, name, dir, "After a restart", afterRestart)

	expectRoot(ctx, t, name, "stop tinyproxy.service", "systemctl stop tinyproxy.service\n")
	expectAgent(ctx, t, client, name, dir, "Proxy down", proxyDown)
	expectRoot(ctx, t, name, "start tinyproxy.service", "set -e\nsystemctl start tinyproxy.service systemd-user-sessions.service\n")

	expectAgent(ctx, t, client, name, dir, "Agent-owned unit", agentOwnedUnit)
	restart(ctx, t, client, dir)
	expectRoot(ctx, t, name, "Agent-owned unit at boot", agentOwnedUnitAtBoot)
	expectAgent(ctx, t, client, name, dir, "Agent-owned unit: container", leakAfterBoot)
	expectAgent(ctx, t, client, name, dir, "Agent-owned unit removed", removeAgentOwnedUnit)

	expectRoot(ctx, t, name, "a failing nftables.service", failNftables)
	orbMachine(ctx, t, "stop", name)
	orbMachine(ctx, t, "start", name)
	expectRoot(ctx, t, name, "Boundary unit failed at boot", bootFailure)
	expectRoot(ctx, t, name, "remove the failing drop-in", removeFail)
	if _, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir}); err != nil {
		t.Fatalf("init to repair the failed boot: %v", err)
	}
	expectRoot(ctx, t, name, "Repair after a failed boot", repaired)
	expectAgent(ctx, t, client, name, dir, "Docker after the repair", agentHelpers+"wait_docker\nallowed \"run alpine\" docker run --rm alpine true\nfinish\n")
	expectRoot(ctx, t, name, "No rootful engine at the end", noRootfulEngine)
}
