package provision

import "fmt"

// Paths and names of the guard that refuses privileged containers.
const (
	runtimeWrapperPath = "/usr/local/libexec/orbit-runc"
	runtimeRealPath    = "/usr/sbin/runc"
	runtimeName        = "orbit-runc"
	daemonConfigPath   = "/etc/orbit/docker-daemon.json"
	daemonDropInPath   = "/etc/systemd/user/" + unitDockerUser + ".d/orbit-privileged.conf"
)

// guardFiles returns the files that make the rootless engine of the agent
// refuse a privileged container, for a profile that needs them: a root-owned
// OCI runtime wrapper, a root-owned daemon configuration that makes the
// wrapper the default runtime, and a drop-in that points the daemon at that
// configuration. Ubuntu needs none: OrbStack refuses --privileged there.
func (p profile) guardFiles() []File {
	if !p.refusePrivileged {
		return nil
	}
	return []File{
		{Path: runtimeWrapperPath, Mode: 0o755, Content: runtimeWrapper()},
		{Path: daemonConfigPath, Mode: 0o644, Content: daemonConfig()},
		{Path: daemonDropInPath, Mode: 0o644, Content: daemonDropIn()},
	}
}

// guardCheck fails unless the daemon of the agent runs with the wrapper as
// its default runtime, for a profile that has the guard.
func (p profile) guardCheck() []string {
	if !p.refusePrivileged {
		return nil
	}
	return []string{
		fmt.Sprintf(`[ "$(%s)" = %s ] || fail "the Docker daemon of %s does not use the runtime %s"`,
			asAgentUser("env DOCKER_HOST=unix:///run/user/$agent_uid/docker.sock docker info --format '{{.DefaultRuntime}}'"),
			runtimeName, AgentUser, runtimeName),
	}
}

// daemonConfig makes the wrapper the default runtime of the daemon.
func daemonConfig() string {
	return fmt.Sprintf(`{"default-runtime": %q, "runtimes": {%q: {"path": %q}}}`, runtimeName, runtimeName, runtimeWrapperPath) + "\n"
}

// daemonDropIn replaces ExecStart= of the user unit with one that reads the
// root-owned configuration.
func daemonDropIn() string {
	return lines(
		"[Service]",
		"ExecStart=",
		fmt.Sprintf("ExecStart=%s/dockerd-rootless.sh --config-file=%s", contribDir, daemonConfigPath),
	)
}

// runtimeWrapper refuses to create a container whose OCI configuration has no
// masked paths, which is what --privileged (and systempaths=unconfined)
// produces, and runs the real runtime for everything else. It fails closed:
// a bundle that it cannot read is refused.
func runtimeWrapper() string {
	return lines(
		"#!/bin/bash",
		`args=("$@")`,
		"create=",
		"bundle=",
		`while [ $# -gt 0 ]; do`,
		`  case $1 in`,
		`    create|run) create=1;;`,
		`    --bundle|-b) bundle=${2:-}; shift;;`,
		`    --bundle=*) bundle=${1#--bundle=};;`,
		`  esac`,
		`  shift`,
		`done`,
		`if [ -n "$create" ]; then`,
		`  config=$bundle/config.json`,
		`  [ -n "$bundle" ] && [ -r "$config" ] || { echo "orbit: cannot read the container configuration; refused" >&2; exit 1; }`,
		`  config_text=$(tr -d ' \t\n' < "$config")`,
		`  case $config_text in *'"maskedPaths":["/'*) ;; *) echo "orbit: privileged containers are refused in this sandbox" >&2; exit 1;; esac`,
		`fi`,
		`exec `+runtimeRealPath+` "${args[@]}"`,
	)
}
