package provision

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cultofthecode/orbit/internal/proxy"
)

// Names of the rootless engine and its boundary gate.
const (
	unitDockerUser = "docker.service"
	unitGate       = "orbit-boundary-gate.service"
	unitUserMgr    = "user@.service"
)

// Paths of the rootless engine.
const (
	userUnitPath   = "/etc/systemd/user/" + unitDockerUser
	gateScriptPath = "/usr/local/libexec/orbit-boundary-gate"
	contribDir     = "/usr/share/docker.io/contrib"
	policyRCPath   = "/usr/sbin/policy-rc.d"
	newUIDMapPath  = "/usr/bin/newuidmap"
	newGIDMapPath  = "/usr/bin/newgidmap"
	subUIDPath     = "/etc/subuid"
	subGIDPath     = "/etc/subgid"
	helperMode     = "4755"
)

// ContextName is the name of the Docker CLI context that points the client
// of the agent at its rootless engine.
const ContextName = "orbit-rootless"

// dockerPackages are the packages of the rootless engine, all from the
// Ubuntu archive (Q1, Q9 of the rootless Docker spec).
var dockerPackages = []string{"docker.io", "docker-buildx", "docker-compose-v2", "rootlesskit", "uidmap", "slirp4netns", "dbus-user-session"}

// rootfulUnits are the units of the rootful Docker Engine. Provisioning
// masks them: the agent has no rootful daemon and no rootful socket.
var rootfulUnits = []string{"docker.service", "docker.socket", "containerd.service"}

// dockerUnits returns the files of the rootless engine: its user unit, the
// boundary gate with its script, and the drop-in that makes every user
// manager wait for the gate.
func dockerUnits() []File {
	return []File{
		{Path: userUnitPath, Mode: 0o644, Content: dockerUserUnit()},
		{Path: systemdDir + unitGate, Mode: 0o644, Content: gateUnit()},
		{Path: gateScriptPath, Mode: 0o755, Content: gateScript()},
		{Path: systemdDir + unitUserMgr + ".d/orbit.conf", Mode: 0o644, Content: userManagerDropIn()},
	}
}

// proxyURL is the address at which containers and the daemon reach the
// egress proxy: never loopback, because slirp4netns hides the loopback of
// the machine.
func proxyURL() string {
	return fmt.Sprintf("http://%s:%d", proxy.ContainerAddr, proxy.Port)
}

// dockerUserUnit is the user unit of the rootless daemon. Only the agent
// runs it, and it sends the pulls of the daemon through the proxy.
func dockerUserUnit() string {
	return lines(
		"[Unit]",
		"Description=Orbit: rootless Docker Engine of the agent",
		"ConditionUser="+AgentUser,
		"",
		"[Service]",
		"Type=notify",
		"NotifyAccess=all",
		"Environment=PATH="+contribDir+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"Environment=HTTP_PROXY="+proxyURL(),
		"Environment=HTTPS_PROXY="+proxyURL(),
		"Environment=NO_PROXY=localhost,127.0.0.1",
		"ExecStart="+contribDir+"/dockerd-rootless.sh",
		"ExecReload=/bin/kill -s HUP $MAINPID",
		"TimeoutSec=0",
		"RestartSec=2",
		"Restart=always",
		"LimitNOFILE=infinity",
		"LimitNPROC=infinity",
		"LimitCORE=infinity",
		"TasksMax=infinity",
		"Delegate=yes",
		"KillMode=mixed",
		"",
		"[Install]",
		"WantedBy=default.target",
	)
}

// gateUnit runs the boundary check once per boot, after the four boundary
// units. It does not require them: init restarts tinyproxy.service and
// orbit-harden.service, and a Requires= would pass each restart on to the
// user manager and stop every container of the agent.
func gateUnit() string {
	return lines(
		"[Unit]",
		"Description=Orbit: the boundary is up before the user manager of the agent starts",
		"After="+strings.Join(boundaryUnits(), " "),
		"StartLimitIntervalSec=0",
		"",
		"[Service]",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"ExecStart="+gateScriptPath,
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	)
}

// gateScript checks the table and the four boundary units. It does not
// check systemd-user-sessions.service and does not wait for the boot:
// user@.service waits for the gate, so a gate that waited for the boot would
// wait for itself.
func gateScript() string {
	return lines(slices.Concat(
		[]string{"#!/bin/bash"},
		checkPrelude(),
		boundaryUnitsCheck(),
	)...)
}

// userManagerDropIn makes every user manager require the gate and start
// after it, and nothing else.
func userManagerDropIn() string {
	return lines(
		"[Unit]",
		"Requires="+unitGate,
		"After="+unitGate,
	)
}

// installDocker installs the packages of the rootless engine behind a
// policy-rc.d that refuses every start, so that no package starts a rootful
// daemon. The guard is removed on every path.
func installDocker() []string {
	return []string{
		fmt.Sprintf("trap 'rm -f %s' EXIT", policyRCPath),
		fmt.Sprintf("printf '#!/bin/sh\\nexit 101\\n' > %s", policyRCPath),
		"chmod 755 " + policyRCPath,
		aptGet + " -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends " + strings.Join(dockerPackages, " ") + " </dev/null",
		"rm -f " + policyRCPath,
		"trap - EXIT",
	}
}

// maskRootfulDocker disables, stops, and masks the units of the rootful
// engine.
func maskRootfulDocker() []string {
	units := strings.Join(rootfulUnits, " ")
	return []string{
		"systemctl disable --now " + units + " 2>/dev/null || true",
		"systemctl mask " + units + " >/dev/null 2>&1",
	}
}

// helperPermissions gives the setuid helpers of uidmap the mode that the
// self-check requires.
func helperPermissions() []string {
	return []string{fmt.Sprintf("chmod %s %s %s", helperMode, newUIDMapPath, newGIDMapPath)}
}

// asAgentUser returns the command that runs args as the agent user, with
// its runtime directory and without a login shell, so that no shell file of
// the agent runs inside the root script.
func asAgentUser(args string) string {
	return fmt.Sprintf(`runuser -u %s -- env XDG_RUNTIME_DIR=/run/user/$agent_uid %s`, AgentUser, args)
}

// startEngine enables the user unit for every user, repairs the gate, the
// linger of the agent, and its user manager, in this order, and starts the
// daemon. start leaves a running daemon alone. The script never restarts or
// stops the gate: user@.service requires it, so that would stop every
// container of the agent.
func startEngine() []string {
	return []string{
		"systemctl --global enable " + unitDockerUser + " >/dev/null 2>&1",
		"agent_uid=$(id -u " + AgentUser + ")",
		"systemctl start " + unitGate,
		"loginctl enable-linger " + AgentUser,
		`systemctl start "user@$agent_uid.service"`,
		asAgentUser("systemctl --user daemon-reload"),
		asAgentUser("systemctl --user start " + unitDockerUser),
	}
}

// dockerCheck verifies, as root, the rootless engine: no rootful daemon, no
// rootful socket, linger, one sub-id range of the agent, the helpers of
// uidmap, the gate, the daemon of the agent, and the client plugins.
func dockerCheck() []string {
	return slices.Concat(
		rootfulCheck(),
		[]string{
			`[ ! -e /var/run/docker.sock ] && [ ! -e /run/docker.sock ] || fail "a rootful Docker socket exists"`,
			fmt.Sprintf(`[ -e /var/lib/systemd/linger/%s ] || fail "%s has no linger"`, AgentUser, AgentUser),
		},
		subIDCheck(subUIDPath),
		subIDCheck(subGIDPath),
		helperCheck(newUIDMapPath, 0),
		helperCheck(newGIDMapPath, 0),
		[]string{
			"systemctl is-active --quiet " + unitGate + ` || fail "` + unitGate + ` is not active"`,
			asAgentUser("systemctl --user is-active --quiet "+unitDockerUser) + ` || fail "` + unitDockerUser + ` of ` + AgentUser + ` is not active"`,
			`docker buildx version >/dev/null || fail "docker buildx is missing"`,
			`docker compose version >/dev/null || fail "docker compose is missing"`,
		},
	)
}

// rootfulCheck fails unless every rootful unit is masked and inactive.
func rootfulCheck() []string {
	return []string{
		"for unit in " + strings.Join(rootfulUnits, " ") + "; do",
		`  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || fail "$unit is not masked"`,
		`  if systemctl is-active --quiet "$unit"; then fail "$unit is active"; fi`,
		"done",
	}
}

// subIDCheck fails unless path holds exactly one entry, the range of the
// agent, and that range starts at uidFloor or higher.
func subIDCheck(path string) []string {
	return []string{
		fmt.Sprintf(`[ "$(grep -c . %s || true)" = 1 ] || fail "%s does not hold exactly one entry"`, path, path),
		fmt.Sprintf(`IFS=: read -r sub_name sub_start _ < %s`, path),
		fmt.Sprintf(`[ "$sub_name" = %s ] && [ "$sub_start" -ge %d ] || fail "%s does not hold a range of %s from %d up"`, AgentUser, uidFloor, path, AgentUser, uidFloor),
	}
}

// helperCheck fails unless the helper at path is owned by ownerUID and has
// exactly the mode of helperMode: setuid, and writable by nobody but its
// owner.
func helperCheck(path string, ownerUID int) []string {
	return []string{
		fmt.Sprintf(`[ -n "$(find %s -maxdepth 0 -user %d -perm %s)" ] || fail "%s is not owned by root with mode %s"`, path, ownerUID, helperMode, path, helperMode),
	}
}

// proxyConfigFilter is the jq program that sets the proxy settings of
// containers and builds in config.json and keeps every other key.
const proxyConfigFilter = `.proxies.default = ((.proxies.default // {}) + {httpProxy: $url, httpsProxy: $url, noProxy: "localhost,127.0.0.1"})`

// DockerSetupScript returns the script that init runs as the agent after
// provisioning: it waits for the socket of the daemon, merges the proxy
// settings into the Docker client configuration, points the client at the
// rootless engine, and checks that the engine is rootless and that compose
// is v2. It pulls nothing. The bytes never change between calls.
func DockerSetupScript() []byte {
	body := slices.Concat(
		[]string{
			"set -euo pipefail",
			"unset DOCKER_HOST DOCKER_CONFIG DOCKER_CONTEXT",
			"socket=/run/user/$(id -u)/docker.sock",
		},
		waitForSocket(),
		mergeProxies(`"$HOME/.docker/config.json"`),
		useContext(),
		engineCheck(),
	)
	return []byte(lines(group(body)...))
}

// waitForSocket waits at most 60 s for the socket of the daemon.
func waitForSocket() []string {
	return []string{
		`for _ in $(seq 60); do [ -S "$socket" ] && break; sleep 1; done`,
		`[ -S "$socket" ] || { echo "the Docker daemon of the agent has no socket" >&2; exit 1; }`,
	}
}

// mergeProxies merges proxyConfigFilter into the file config, creates it
// with mode 0600 when it is missing, and writes through a symlink instead of
// replacing it.
func mergeProxies(config string) []string {
	return []string{
		"config=" + config,
		`mkdir -p "$(dirname "$config")"`,
		`[ -e "$config" ] || { echo '{}' > "$config"; chmod 600 "$config"; }`,
		`target=$(readlink -f "$config")`,
		`merged=$(jq --arg url ` + proxyURL() + ` '` + proxyConfigFilter + `' "$target")`,
		`printf '%s\n' "$merged" > "$target"`,
	}
}

// useContext creates or updates the context of the rootless engine and
// makes it the current one.
func useContext() []string {
	return []string{
		fmt.Sprintf(`if docker context inspect %s >/dev/null 2>&1; then docker context update %s --docker "host=unix://$socket" >/dev/null; else docker context create %s --docker "host=unix://$socket" >/dev/null; fi`, ContextName, ContextName, ContextName),
		"docker context use " + ContextName + " >/dev/null",
	}
}

// engineCheck fails unless the engine reports rootless and compose is v2.
func engineCheck() []string {
	return []string{
		`docker info --format '{{json .SecurityOptions}}' | grep -q rootless || { echo "the Docker engine of the agent is not rootless" >&2; exit 1; }`,
		`docker compose version --short | grep -Eq '^v?2\.' || { echo "docker compose is not v2" >&2; exit 1; }`,
	}
}
