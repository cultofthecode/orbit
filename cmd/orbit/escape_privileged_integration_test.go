//go:build integration

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/proxy"
)

// targetSentinel is the file that root writes into the target machine of
// E11, which E19 must not find from a privileged container.
const targetSentinel = "orbit-e19-sentinel"

// privilegedHelpers holds the helpers of E18 to E22. P runs a command in a
// --privileged container with the flags of one variant. blocked_priv expects
// the command to fail. refused_errno expects it to fail with one of the
// errors that the line lists, and prints unexpectedErrnoMarker for any other
// failure, so that a line never passes for the wrong reason. na reports a
// line whose target does not exist in the machine.
const privilegedHelpers = `P() { local flags=$1; shift; docker run --rm --privileged $flags $IMG "$@"; }
escape() { echo "` + escapeMarker + `: $*" >&2; failures=$((failures + 1)); }
blocked_priv() { local what=$1 flags=$2; shift 2; if P "$flags" "$@" >/dev/null 2>&1; then escape "$what succeeded"; fi; }
refused_errno() { local what=$1 flags=$2 pattern=$3 out; shift 3
  if out=$(P "$flags" "$@" 2>&1); then escape "$what succeeded: $out"; return; fi
  out=$(printf '%s' "$out" | tr '\n' ' ')
  printf '%s\n' "$out" | grep -qE "$pattern" || { echo "` + unexpectedErrnoMarker + `: $what: $out" >&2; failures=$((failures + 1)); }
}
write_refused() { local flags=$1 file=$2
  P "$flags" test -e "$file" >/dev/null 2>&1 || { na "$file ($flags)"; return; }
  refused_errno "open $file for writing ($flags)" "$flags" 'Operation not permitted|Permission denied' sh -c ': >> "$1"' sh "$file"
}
na() { echo "N/A: $*" >&2; }
priv_control() { local flags=$1 out cap map
  out=$(P "$flags" sh -c 'awk "/^CapEff:/ {print \$2}" /proc/self/status; head -n1 /proc/self/uid_map' 2>&1) || { fail "the privileged container ('$flags') did not start: $out"; return; }
  cap=$(printf '%s\n' "$out" | sed -n 1p); map=$(printf '%s\n' "$out" | sed -n 2p)
  [ "$cap" = 000001ffffffffff ] || fail "the privileged container ('$flags') has CapEff $cap, want 000001ffffffffff"
  set -- $map
  { [ "${1:-}" = 0 ] && [ "${2:-}" = "$(id -u)" ]; } || fail "the uid_map of the privileged container ('$flags') starts with '$map', want 0 mapped to $(id -u)"
}
`

// perVariant renders line once for each set of flags.
func perVariant(variants []string, line func(flags string) string) string {
	var b strings.Builder
	for _, flags := range variants {
		b.WriteString(line(flags))
	}
	return b.String()
}

// sharedPrivilegedControl is the shared control of E18 to E22: the
// privileged container starts in every variant, has every capability, and
// maps root to the uid of the agent.
func sharedPrivilegedControl(variants []string) string {
	return privilegedHelpers + perVariant(variants, func(flags string) string {
		return fmt.Sprintf("priv_control '%s'\n", flags)
	})
}

// chain runs the steps in order and stops at the first that fails.
func chain(steps ...func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		for _, step := range steps {
			if err := step(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// privilegedOutcome reports, for a distribution, whether the attempts E18 to
// E22 and M02 run. It compares the observation of --privileged with the
// table in both directions (topic 10): refused and refused counts the ids as
// n/a, and any other mismatch fails t.
func (r escapeRig) privilegedGate(ctx context.Context, t *testing.T, distro string, ids ...string) bool {
	t.Helper()
	started := r.privilegedStarts(ctx, t)
	expected, ok := privilegedTable[distro]
	t.Logf("--privileged starts on %s: %t (table: %s)", distro, started, tableCell(expected, ok))
	if !ok {
		t.Fatalf("CONTROL FAILED: no row of the privileged table for %s; --privileged started: %t; set the row from this observation", distro, started)
	}
	observation := privilegedObservation{distro: distro, expected: expected, started: started}
	run := true
	for _, id := range ids {
		got, message := attemptVerdict(attemptResult{id: id, privileged: &observation})
		if got == outcomePass {
			continue
		}
		run = false
		r.tally.add(got)
		t.Run(id, func(t *testing.T) {
			if got == outcomeNotApplicable {
				t.Skip(message)
			}
			t.Error(message)
		})
	}
	return run
}

// tableCell names the table entry of a distribution for the log.
func tableCell(expected privilegedExpectation, ok bool) string {
	switch {
	case !ok:
		return "no row"
	case expected == privilegedAllowed:
		return "allowed"
	}
	return "refused"
}

// privilegedStarts reports whether docker run --privileged starts a
// container in the machine of r. A container of the same image without the
// flag runs first: when that one does not start, a refusal of --privileged
// would prove nothing, and the test fails.
func (r escapeRig) privilegedStarts(ctx context.Context, t *testing.T) bool {
	t.Helper()
	if err := r.runIn(ctx, agentContainer, "docker run --rm $IMG true || fail \"docker run failed\"\n"); err != nil {
		t.Fatalf("CONTROL FAILED: a container of the probe image without --privileged does not start in %s, so a refusal of --privileged would prove nothing: %v", r.name, err)
	}
	return r.runIn(ctx, agentContainer, "docker run --rm --privileged $IMG true || fail \"docker run --privileged failed\"\n") == nil
}

// kernelVariants and the other variant lists are the binding Containers
// column of each row of the Delta.
var (
	kernelVariants  = []string{"", "--pid host --network host -v /:/h"}
	sandboxVariants = []string{"", "--network host -v /:/h"}
	macVariants     = []string{"-v /:/h", "--network host"}
	sshVariants     = []string{"-v /:/h", "--network host"}
	egressVariants  = []string{"", "--network host", "--pid host", "-v /:/h"}
)

// kernelWriteFiles are the kernel settings that E18 tries to write.
const kernelWriteFiles = "/proc/sys/kernel/core_pattern /proc/sys/kernel/modprobe /proc/sysrq-trigger /sys/kernel/uevent_helper"

// kernelSettingsSnapshot prints the value of each kernel setting of E18 that
// root can read, one line each. A file that has no read operation, such as
// /proc/sysrq-trigger, has no value to compare and is left out.
const kernelSettingsSnapshot = `for file in ` + kernelWriteFiles + `; do if value=$(cat "$file" 2>/dev/null); then printf '%s=%s\n' "$file" "$value"; fi; done`

// kernelSettingsBefore is the file in which root keeps the values before E18.
const kernelSettingsBefore = "/run/orbit-e18.before"

// pidNamespaceFile is where the container of E18 leaves the pid namespace
// that it reads, for root to look up.
const pidNamespaceFile = "/tmp/orbit-e18-pidns"

// privilegedKernelAttempts is E18. Each line names the error that refuses
// it. An open that is refused for lack of a capability is EPERM or EACCES;
// an error of the file system, such as EROFS from a read-only mount of the
// container, is not a refusal by the kernel and fails the line. The lists
// are the spec's, and the first Debian run tightens each to the error it
// observes. Root reads each kernel setting back after the attempt, and
// looks up the pid namespace that the container read.
func (e escapeEnv) privilegedKernelAttempts() []attempt {
	try := privilegedHelpers + `need awk
` + perVariant(kernelVariants, func(f string) string {
		machineFiles := ""
		if strings.Contains(f, "-v /:/h") {
			machineFiles = "for file in " + kernelWriteFiles + "; do write_refused '%[1]s' \"/h$file\"; done\n"
		}
		return fmt.Sprintf(`refused_errno "insmod (%[1]s)" '%[1]s' 'Operation not permitted' insmod /dev/null
for file in %[2]s; do write_refused '%[1]s' "$file"; done
`+machineFiles+`refused_errno "mount a cgroup v1 hierarchy (%[1]s)" '%[1]s' 'Operation not permitted' sh -c 'mkdir -p /tmp/cg && mount -t cgroup -o none,name=orbit cgroup /tmp/cg'
for dev in mem kmem port; do
  if ! P '%[1]s' test -e "/dev/$dev"; then na "/dev/$dev"; continue; fi
  refused_errno "read /dev/$dev (%[1]s)" '%[1]s' 'Operation not permitted|Permission denied' dd if="/dev/$dev" of=/dev/null bs=1 count=1
done
while read -r major minor name; do
  if ! P '%[1]s' test -e "/dev/$name"; then na "/dev/$name"; else
    refused_errno "open the block device $name (%[1]s)" '%[1]s' 'Operation not permitted|Permission denied' sh -c ': < "$1"' sh "/dev/$name"
  fi
  refused_errno "mknod of the block device $name (%[1]s)" '%[1]s' 'Operation not permitted' mknod /tmp/blk b "$major" "$minor"
done < <(awk 'NR > 2 {print $1, $2, $4}' /proc/partitions)
`, f, kernelWriteFiles)
	}) + `if P '--pid host --network host -v /:/h' sh -c 'test -d /h/lib/modules/$(uname -r)' >/dev/null 2>&1; then
  refused_errno "modprobe (--pid host)" '--pid host --network host -v /:/h' 'Operation not permitted' modprobe -d /h dummy
else
  na "no module tree for modprobe"
fi
refused_errno "nsenter into pid 1 (--pid host)" '--pid host --network host -v /:/h' 'Operation not permitted|Permission denied' nsenter -t 1 -m -n -p true
if ns=$(P '--pid host --network host -v /:/h' sh -c 'readlink /proc/1/ns/pid || readlink /proc/self/ns/pid' 2>/dev/null) && [ -n "$ns" ]; then
  printf '%s\n' "$ns" > ` + pidNamespaceFile + `
else
  na "the pid namespace of the container"
fi
`
	snapshot := func(ctx context.Context) error {
		return e.rig.runIn(ctx, rootSession, "set -eu\n"+kernelSettingsSnapshot+" > "+kernelSettingsBefore+"\nrm -f "+pidNamespaceFile+"\n")
	}
	control := chain(
		func(ctx context.Context) error {
			return e.rig.runIn(ctx, agentContainer, sharedPrivilegedControl(kernelVariants))
		},
		snapshot,
	)
	after := func(ctx context.Context) error {
		return e.rig.runIn(ctx, rootSession, `[ "$(`+kernelSettingsSnapshot+`)" = "$(cat `+kernelSettingsBefore+`)" ] || { echo "a kernel setting changed" >&2; exit 1; }
if [ -s `+pidNamespaceFile+` ]; then
  ns=$(cat `+pidNamespaceFile+`); found=
  for link in /proc/[0-9]*/ns/pid; do [ "$(readlink "$link" 2>/dev/null)" = "$ns" ] && { found=1; break; }; done
  rm -f `+pidNamespaceFile+`
  [ -n "$found" ] || { echo "the pid namespace $ns, which the container reads, is not the pid namespace of any process of the machine" >&2; exit 1; }
fi
`)
	}
	return []attempt{{id: "E18-privileged-kernel", rule: "R10 D12 A01", as: agentContainer, controlFn: control, try: try, after: after}}
}

// privilegedOtherSandboxAttempts is E19. The root control of the target
// follows E11: a target that root cannot reach makes the attempt
// inconclusive.
func (e escapeEnv) privilegedOtherSandboxAttempts() []attempt {
	hosts := e.fill("@TARGET_ADDR@ @TARGET@.orb.local")
	try := e.fill(privilegedHelpers + perVariant(sandboxVariants, func(f string) string {
		return fmt.Sprintf(`for host in `+hosts+` ; do
  blocked_priv "direct to $host (%[1]s)" '%[1]s' sh -c 'ip route add "$1/32" $(ip route show default | head -n1 | cut -d" " -f2-) 2>/dev/null; curl -sS --noproxy "*" -m 5 -o /dev/null "http://$1:@TARGET_PORT@"' sh "$host"
  out=$(P '%[1]s' curl -sS -m 15 -x http://172.31.255.1:3128 -o /dev/null "https://$host:@TARGET_PORT@" 2>&1) && escape "CONNECT through the proxy to $host (%[1]s) succeeded"
  case "$out" in *403*) ;; *) echo "`+unexpectedErrnoMarker+`: CONNECT to $host: $out" >&2; fail "CONNECT to $host failed without a 403: $out";; esac
done
`, f)
	}) + `if P '--network host -v /:/h' sh -c 'grep -q "@TARGET@" /proc/self/mountinfo'; then escape "the mount table of the container lists the target machine"; fi
P '--network host -v /:/h' sh -c 'test -z "$(find /h/mnt -maxdepth 4 -name ` + targetSentinel + ` 2>/dev/null)"' || escape "the sentinel of the target machine is under /h/mnt"
`)
	rootReachesTarget := func(ctx context.Context) error {
		return e.rig.runIn(ctx, rootSession, e.fill("need curl\ncurl -sSf --noproxy '*' -m 5 -o /dev/null http://@TARGET_ADDR@:@TARGET_PORT@ || { echo \""+targetUnreachableMarker+": root does not reach the listener of the target\" >&2; exit 1; }\n"))
	}
	control := chain(
		func(ctx context.Context) error {
			return e.rig.runIn(ctx, agentContainer, sharedPrivilegedControl(sandboxVariants))
		},
		rootReachesTarget,
	)
	return []attempt{{id: "E19-privileged-other-sandbox", rule: "R03 R10", as: agentContainer, controlFn: control, unreachableInconclusive: true, try: try}}
}

// projectShareMounts is the file in which root keeps the file system type
// and the source that its own mount table shows for the mount that holds the
// project directory, which is the share of the project (E20).
const projectShareMounts = "/run/orbit-e20.mounts"

// projectShareMountScript is what root runs to write projectShareMounts: the
// longest mount point above the project directory, never /.
const projectShareMountScript = `awk -v d='@DIR@' '$5 != "/" && (d == $5 || index(d, $5 "/") == 1) && length($5) > best { for (i = 7; i <= NF; i++) if ($i == "-") { best = length($5); line = $(i + 1) " " $(i + 2); break } } END { if (best) print line }' /proc/self/mountinfo > ` + projectShareMounts + `
[ -s ` + projectShareMounts + ` ] || { echo "the mount table of root has no mount that holds @DIR@" >&2; exit 1; }
chmod 644 ` + projectShareMounts + `
`

// privilegedMacAttempts is E20. The file system type and the source of the
// project share come from the mount table of root. A socket that the agent's
// own session cannot open must not open from the container either.
func (e escapeEnv) privilegedMacAttempts() []attempt {
	try := e.fill(privilegedHelpers + `S='@SENTINEL@'
D='@DIR@'
blocked_priv "the sentinel at its host path" '-v /:/h' test -e "/h$S"
P '-v /:/h' sh -c 'find /h/mnt/mac /h/Users /h/Volumes -name "$(basename "$1")" 2>/dev/null | grep -q .' sh "$S" && escape "the sentinel is under /h/mnt/mac, /h/Users, or /h/Volumes"
while read -r fstype source; do
  for mount in $(P '-v /:/h' awk -v t="$fstype" -v s="$source" '{ for (i = 7; i <= NF; i++) if ($i == "-") { if ($(i + 1) == t && $(i + 2) == s) print $5; break } }' /proc/self/mountinfo); do
    [ "$mount" = "$D" ] || [ "$mount" = "/h$D" ] || escape "a host share is mounted at $mount besides the project directory"
  done
  P '-v /:/h' sh -c 'mkdir -p /tmp/m && mount -t "$1" "$2" /tmp/m 2>/dev/null && { [ -e /tmp/m/Users ] || find /tmp/m -maxdepth 4 -name "$(basename "$3")" | grep -q .; }' sh "$fstype" "$source" "$S" && escape "a new $fstype mount of $source exposes more than the project directory"
done < ` + projectShareMounts + `
` + perVariant(macVariants, func(f string) string {
		return fmt.Sprintf(`blocked_priv "a host engine socket (%[1]s)" '%[1]s -v /var/run/docker.sock:/s' test -S /s
blocked_priv "the engine socket of root (%[1]s)" '%[1]s -v /run/docker.sock:/s' test -S /s
for host in @MAC_HOSTS@; do
  blocked_priv "the Mac at $host (%[1]s)" '%[1]s' curl -sSf --noproxy '*' -m 5 -o /dev/null "http://$host:@PORT@"
done
`, f)
	}) + `for socket in $(P '-v /:/h' find /h/run /h/var/run /h/tmp /h/opt -type s 2>/dev/null); do
  case "$socket" in /h/run/user/$(id -u)/docker.sock) continue;; esac
  [ "$(P '-v /:/h' curl -s -m 2 --unix-socket "$socket" http://engine/_ping 2>/dev/null)" != OK ] || escape "a privileged container reached an engine on $socket"
  curl -s -m 2 --unix-socket "${socket#/h}" -o /dev/null http://engine/ >/dev/null 2>&1; own=$?
  P '-v /:/h' curl -s -m 2 --unix-socket "$socket" -o /dev/null http://engine/ >/dev/null 2>&1; contained=$?
  { [ "$own" -ne 7 ] || [ "$contained" -eq 7 ]; } || escape "a privileged container opened $socket, which the session of the agent cannot open"
done
`)
	sentinelReadable := e.fill(privilegedHelpers + "[ \"$(P '-v /:/h' cat \"/h@DIR@/sentinel-copy\")\" = '@CONTENT@' ] || fail \"the sentinel in the project directory is not readable from the privileged container\"\n")
	control := chain(
		func(ctx context.Context) error {
			return e.rig.runIn(ctx, agentContainer, sharedPrivilegedControl(macVariants)+sentinelReadable)
		},
		hostEngineAnswers,
		func(ctx context.Context) error { return e.rig.runIn(ctx, rootSession, e.macReachedByRoot()) },
		func(ctx context.Context) error { return e.rig.runIn(ctx, rootSession, e.fill(projectShareMountScript)) },
	)
	return []attempt{{id: "E20-privileged-mac", rule: "R08 R10 N07", as: agentContainer, controlFn: control, try: try}}
}

// privilegedSSHAttempts is E21, for the sandbox created with
// --no-ssh-forwarding. Like E15 it has no key check: the detectors are the
// agent socket, the key listing of every socket, and the group file. control
// is the control of E15, which E21 shares.
func privilegedSSHAttempts(control func(ctx context.Context) error, rig escapeRig) []attempt {
	try := privilegedHelpers + perVariant(sshVariants, func(f string) string {
		return fmt.Sprintf(`for socket in $(P '%[1]s' find /h/run /h/opt /h/var/run /h/tmp /h/home -type s 2>/dev/null); do
  if P '%[1]s' sh -c 'SSH_AUTH_SOCK="$1" ssh-add -L 2>&1 | grep -qE "^(ssh|ecdsa|sk)-"' sh "$socket"; then escape "a key is listed through $socket (%[1]s)"; fi
done
`, f)
	}) + `[ -z "$(P '-v /:/h' find /h/run /h/opt /h/var/run /h/tmp /h/home -name host-ssh-agent.sock 2>/dev/null)" ] || escape "host-ssh-agent.sock exists under /h"
blocked_priv "write /h/etc/group" '-v /:/h' sh -c ': >> /h/etc/group'
`
	both := chain(
		func(ctx context.Context) error {
			return rig.runIn(ctx, agentContainer, sharedPrivilegedControl(sshVariants))
		},
		control,
	)
	return []attempt{{id: "E21-privileged-ssh-agent", rule: "R08 D25", as: agentContainer, controlFn: both, try: try}}
}

// directProbe is the direct-egress probe of E22 and M02: a direct
// connection to 1.1.1.1.
const directProbe = "curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1"

// egressSnapshot is what root records before E22 and compares after it.
const egressSnapshot = `{ nft list ruleset; sha256sum /etc/nftables.conf ` + proxy.AllowlistPath + `; pgrep -o -x tinyproxy; } 2>&1`

// privilegedEgressAttempts is E22.
func (e escapeEnv) privilegedEgressAttempts() []attempt {
	try := privilegedHelpers + perVariant(egressVariants, func(f string) string {
		return fmt.Sprintf(`for command in "nft flush ruleset" "nft insert rule inet orbit_egress output accept" "iptables -P OUTPUT ACCEPT" "ip link add orbit-e22 type dummy" "ip route add 192.0.2.0/24 dev lo"; do
  P '%[1]s' sh -c "$command" >/dev/null 2>&1 || true
done
if P '%[1]s' sh -c 'test -d /h/etc'; then
  blocked_priv "write /h/etc/nftables.conf (%[1]s)" '%[1]s' sh -c ': >> /h/etc/nftables.conf'
  blocked_priv "write the allowlist (%[1]s)" '%[1]s' sh -c ': >> /h/etc/tinyproxy/allowlist'
fi
case '%[1]s' in *"--pid host"*)
  blocked_priv "nsenter -t 1 -n (%[1]s)" '%[1]s' nsenter -t 1 -n nft flush ruleset
  blocked_priv "kill tinyproxy (%[1]s)" '%[1]s' sh -c 'kill -9 $(pidof tinyproxy)';;
esac
blocked_priv "direct IPv4 (%[1]s)" '%[1]s' `+directProbe+`
blocked_priv "direct IPv6 (%[1]s)" '%[1]s' curl -6 -sS --noproxy '*' -m 5 -k -o /dev/null 'https://[2606:4700:4700::1111]'
blocked_priv "direct by name (%[1]s)" '%[1]s' curl -sS --noproxy '*' -m 5 -o /dev/null https://github.com
blocked_priv "UDP DNS to 1.1.1.1 (%[1]s)" '%[1]s' nslookup -timeout=3 -retry=1 github.com 1.1.1.1
out=$(P '%[1]s' curl -sS -m 15 -x http://172.31.255.1:3128 -o /dev/null https://example.org 2>&1) && escape "CONNECT to example.org (%[1]s) succeeded"
case "$out" in *403*) ;; *) echo "`+unexpectedErrnoMarker+`: CONNECT to example.org: $out" >&2; fail "CONNECT to example.org failed without a 403: $out";; esac
`, f)
	})
	githubThroughTheProxy := privilegedHelpers + perVariant(egressVariants, func(f string) string {
		return fmt.Sprintf("[ \"$(P '%[1]s' curl -sS -m 20 -o /dev/null -w '%%{http_code}' -x http://172.31.255.1:3128 https://github.com 2>/dev/null)\" = 200 ] || fail \"a privileged container ('%[1]s') did not reach github.com through the proxy\"\n", f)
	})
	snapshot := "set -eu\n" + egressSnapshot + " > /run/orbit-e22.before\n"
	control := chain(
		func(ctx context.Context) error {
			return e.rig.runIn(ctx, agentContainer, sharedPrivilegedControl(egressVariants))
		},
		func(ctx context.Context) error { return e.rig.runIn(ctx, agentContainer, githubThroughTheProxy) },
		func(ctx context.Context) error { return e.rig.runIn(ctx, rootSession, snapshot) },
	)
	after := func(ctx context.Context) error {
		return e.rig.runIn(ctx, rootSession, "[ \"$("+egressSnapshot+")\" = \"$(cat /run/orbit-e22.before)\" ] || { echo 'the ruleset, a protected file, or the pid of tinyproxy changed' >&2; exit 1; }\n")
	}
	return []attempt{{id: "E22-privileged-egress", rule: "R03 D14 D22 A05", as: agentContainer, controlFn: control, try: try, after: after}}
}

// checkThePrivilegedLeakIsDetected is M02: with only the weakening of the
// agent uid in place, the direct probe of E22 from a --privileged container
// reports reach.
func checkThePrivilegedLeakIsDetected(ctx context.Context, t *testing.T, rig escapeRig) {
	t.Helper()
	t.Run("M02-privileged-leak-is-detected", func(t *testing.T) {
		t.Log("M02 proves A05: the direct probe of E22 from a privileged container reports reach on a weakened firewall")
		if err := rig.client.RunAsRoot(ctx, rig.name, []byte(weakenForPrivilegedLeak)); err != nil {
			t.Fatalf("CONTROL FAILED: M02: the weakening is not in place in %s: %v", rig.name, err)
		}
		script := "docker run --rm --privileged $IMG " + directProbe + "\n"
		if err := rig.runIn(ctx, agentContainer, script); err != nil {
			t.Errorf("the suite cannot detect a privileged-container leak: the direct probe of E22 reported blocked on a firewall that accepts the agent uid (%v)", err)
			rig.tally.add(outcomeEscape)
			return
		}
		t.Log("detected: the direct probe of E22 reaches 1.1.1.1 from a privileged container")
		rig.tally.add(outcomePass)
	})
}

// TestPrivilegedScriptsParse renders the scripts of E18 to E22 and M02 and
// checks that bash parses them. It starts no machine.
func TestPrivilegedScriptsParse(t *testing.T) {
	e := escapeEnv{dir: "/Users/orbit/project", sentinel: "/Users/orbit/escape-sentinel", sentinelContent: "secret", macPort: 4242, defaultUID: 501, target: "orbit-target", targetAddr: "198.19.249.5"}
	scripts := map[string]string{
		"weaken":      weakenForPrivilegedLeak,
		"E08":         privilegedUIDs,
		"E08-refused": refusedPrivilegedRuns + refusedPrivilegedExec,
	}
	for _, attempts := range [][]attempt{
		e.privilegedKernelAttempts(), e.privilegedOtherSandboxAttempts(), e.privilegedMacAttempts(),
		e.privilegedEgressAttempts(), privilegedSSHAttempts(nil, escapeRig{}),
	} {
		for _, a := range attempts {
			scripts[a.id] = string(escapeScript(a.as, a.try))
		}
	}
	scripts["control"] = string(escapeScript(agentContainer, sharedPrivilegedControl(egressVariants)))
	for name, script := range scripts {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n%s", name, err, out)
		}
	}
}
