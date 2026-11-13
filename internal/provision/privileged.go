package provision

import "fmt"

// Paths of the guard that refuses privileged containers. The guard replaces
// runc itself: the real binary moves to runtimeRealPath through a local
// diversion of dpkg, and a root-owned wrapper takes its place. Every runtime
// that the daemon knows, the stock ones (runc, io.containerd.runc.v2)
// included, runs runc by that name, so none of them can skip the wrapper.
const (
	runtimeWrapperPath = "/usr/sbin/runc"
	runtimeRealPath    = "/usr/sbin/runc.real"
)

// guardSetup returns the script lines that divert the real runc, for a
// profile that needs the guard. They run before the wrapper is written, and
// they are idempotent: dpkg-divert --truename prints the diverted name once
// the diversion exists. A runc that a later upgrade unpacks goes to
// runtimeRealPath, so the wrapper stays in place.
func (p profile) guardSetup() []string {
	if !p.refusePrivileged {
		return nil
	}
	return []string{
		fmt.Sprintf(`if [ "$(dpkg-divert --truename %s)" != %s ]; then dpkg-divert --local --rename --divert %s --add %s >/dev/null; fi`,
			runtimeWrapperPath, runtimeRealPath, runtimeRealPath, runtimeWrapperPath),
	}
}

// guardFiles returns the files that make the rootless engine of the agent
// refuse a privileged container, for a profile that needs them: the
// root-owned runc wrapper. Ubuntu needs none: OrbStack refuses --privileged
// there.
func (p profile) guardFiles() []File {
	if !p.refusePrivileged {
		return nil
	}
	return []File{{Path: runtimeWrapperPath, Mode: 0o755, Content: runtimeWrapper()}}
}

// guardCheck fails, for a profile that has the guard, unless the diversion
// and the wrapper are in place, and unless every runtime of the daemon of the
// agent resolves, with the PATH of the daemon, to the wrapper. A runtime with
// no path fails the check: it cannot be shown to go through the wrapper.
func (p profile) guardCheck() []string {
	if !p.refusePrivileged {
		return nil
	}
	return []string{
		fmt.Sprintf(`[ "$(dpkg-divert --truename %s)" = %s ] && [ -x %s ] || fail "runc is not diverted to %s"`,
			runtimeWrapperPath, runtimeRealPath, runtimeRealPath, runtimeRealPath),
		fmt.Sprintf(`[ "$(stat -c %%U:%%a %s)" = root:755 ] && grep -q 'privileged containers are refused' %s || fail "%s is not the root-owned guard"`,
			runtimeWrapperPath, runtimeWrapperPath, runtimeWrapperPath),
		fmt.Sprintf(`daemon_pid=$(%s) || fail "cannot read the main process of the Docker daemon of %s"`,
			asAgentUser("systemctl --user show -p MainPID --value "+unitDockerUser), AgentUser),
		`[ "${daemon_pid:-0}" -gt 0 ] || fail "the Docker daemon of ` + AgentUser + ` has no main process"`,
		`daemon_path=$(tr '\0' '\n' < "/proc/$daemon_pid/environ" | sed -n 's/^PATH=//p')`,
		fmt.Sprintf(`runtime_paths=$(%s)`,
			asAgentUser(`env DOCKER_HOST=unix:///run/user/$agent_uid/docker.sock docker info --format '{{range .Runtimes}}path={{.Path}}{{"\n"}}{{end}}'`)),
		`[ -n "$runtime_paths" ] || fail "the Docker daemon of ` + AgentUser + ` lists no runtime"`,
		`while IFS= read -r runtime_line; do`,
		`  runtime_path=${runtime_line#path=}`,
		`  [ -n "$runtime_path" ] || fail "a runtime of the Docker daemon of ` + AgentUser + ` has no path"`,
		`  resolved=$(PATH=$daemon_path command -v "$runtime_path" || true)`,
		fmt.Sprintf(`  [ -n "$resolved" ] && [ "$(readlink -f "$resolved")" = "$(readlink -f %s)" ] || fail "the runtime $runtime_path of `+AgentUser+` does not go through the guard"`,
			runtimeWrapperPath),
		`done < <(printf '%s\n' "$runtime_paths")`,
	}
}

// runtimeWrapper refuses to create a container (create, run, restore) whose
// OCI configuration has no masked paths, which is what --privileged (and
// systempaths=unconfined) produces, and refuses a container that has every
// capability (the five that follow: CAP_SYS_ADMIN,
// CAP_SYS_MODULE, CAP_SYS_RAWIO, CAP_SYS_BOOT, CAP_MAC_ADMIN) and neither a
// seccomp filter nor an AppArmor profile (--cap-add ALL with seccomp=unconfined
// and apparmor=unconfined, which the owner treats like --privileged). Only a
// seccomp object whose default action blocks (ERRNO, KILL, KILL_PROCESS,
// KILL_THREAD, TRAP) counts as a filter: allow, log, and every other default
// action count as none. A text check cannot judge an allow-list profile that
// allows every call by rule. An AppArmor profile name other than unconfined
// counts as a profile, and the wrapper reads the field only in the part of the
// configuration before the root object, so a key of the user elsewhere does
// not count. It refuses an exec whose process spec holds a capability that the
// bounding set of the container lacks, which is what docker exec --privileged
// produces. It passes only the subcommands of an allowlist that neither
// create nor change a container's configuration (state, kill, delete, list,
// and so on); an unknown subcommand is refused. It fails closed: a bundle, a
// process file, or a state that it cannot read is refused. It looks at every
// argument, not at the first word, so a container ID that equals create, run,
// restore, or exec is refused or checked too, never skipped.
func runtimeWrapper() string {
	return lines(
		"#!/bin/bash",
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"real="+runtimeRealPath,
		`refuse() { echo "orbit: $1" >&2; exit 1; }`,
		`args=("$@")`,
		"create=",
		"sub=",
		"exec_at=",
		"bundle=",
		"process=",
		"n=0",
		`while [ $# -gt 0 ]; do`,
		`  case $1 in`,
		`    create|run|restore) create=1; [ -n "$sub" ] || sub=$1;;`,
		`    exec) [ -n "$exec_at" ] || exec_at=$n; [ -n "$sub" ] || sub=$1;;`,
		`    --root|--log|--log-format|--criu|--rootless) [ -n "$sub" ] || { shift; n=$((n + 1)); };;`,
		`    --bundle|-b) bundle=${2:-}; shift; n=$((n + 1));;`,
		`    --bundle=*) bundle=${1#--bundle=};;`,
		`    --process|-p) process=${2:-}; shift; n=$((n + 1));;`,
		`    --process=*) process=${1#--process=};;`,
		`    -*) ;;`,
		`    *) [ -n "$sub" ] || sub=$1;;`,
		`  esac`,
		`  shift`,
		`  n=$((n + 1))`,
		`done`,
		`case $sub in`,
		`  ""|create|run|restore|exec|state|kill|delete|list|ps|events|features|spec|start|pause|resume|checkpoint|update|help|version) ;;`,
		`  *) refuse "the runtime command $sub is refused in this sandbox";;`,
		`esac`,
		`if [ -n "$create" ]; then`,
		`  config=$bundle/config.json`,
		`  [ -n "$bundle" ] && [ -r "$config" ] || refuse "cannot read the container configuration; refused"`,
		`  config_text=$(tr -d ' \t\n' < "$config")`,
		`  case $config_text in *'"maskedPaths":["/'*) ;; *) refuse "privileged containers are refused in this sandbox";; esac`,
		`  unconfined=1`,
		`  case $config_text in *'"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"'*|*'"seccomp":{"defaultAction":"SCMP_ACT_KILL"'*|*'"seccomp":{"defaultAction":"SCMP_ACT_KILL_PROCESS"'*|*'"seccomp":{"defaultAction":"SCMP_ACT_KILL_THREAD"'*|*'"seccomp":{"defaultAction":"SCMP_ACT_TRAP"'*) unconfined=;; esac`,
		`  process_text=${config_text%%'"root":{'*}`,
		`  case $process_text in *'"apparmorProfile":"unconfined"'*) ;; *'"apparmorProfile":"'*) unconfined=;; esac`,
		`  if [ -n "$unconfined" ]; then`,
		`    caps=$(printf '%s' "$config_text" | sed -n 's/.*"bounding":\[\([^]]*\)\].*/\1/p')`,
		`    every=1`,
		`    for cap in CAP_SYS_ADMIN CAP_SYS_MODULE CAP_SYS_RAWIO CAP_SYS_BOOT CAP_MAC_ADMIN; do case $caps in *'"'$cap'"'*) ;; *) every=;; esac; done`,
		`    [ -z "$every" ] || refuse "containers with every capability and no seccomp or AppArmor profile are refused in this sandbox"`,
		`  fi`,
		`fi`,
		`if [ -n "$exec_at" ]; then`,
		`  id=${args[${#args[@]} - 1]}`,
		`  [ -n "$process" ] && [ -r "$process" ] || refuse "cannot read the process of the exec; refused"`,
		`  state=$("$real" "${args[@]:0:$exec_at}" state "$id" 2>/dev/null) || refuse "cannot read the state of the container; refused"`,
		`  dir=$(printf '%s' "$state" | tr -d ' \t\n' | sed -n 's/.*"bundle":"\([^"]*\)".*/\1/p')`,
		`  [ -n "$dir" ] && [ -r "$dir/config.json" ] || refuse "cannot read the container configuration; refused"`,
		`  bounding=" $(tr -d ' \t\n' < "$dir/config.json" | sed -n 's/.*"bounding":\[\([^]]*\)\].*/\1/p' | grep -o 'CAP_[A-Z_]*' | tr '\n' ' ') "`,
		`  for cap in $(tr -d ' \t\n' < "$process" | sed -n 's/.*"capabilities":{\([^}]*\)}.*/\1/p' | grep -o 'CAP_[A-Z_]*'); do`,
		`    case $bounding in *" $cap "*) ;; *) refuse "privileged exec is refused in this sandbox";; esac`,
		`  done`,
		`fi`,
		`exec "$real" "${args[@]}"`,
	)
}
