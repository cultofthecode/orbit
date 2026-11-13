//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
)

// identity is where an attempt or a control runs.
type identity int

const (
	// agentSession is the session of the agent: orb -u agent.
	agentSession identity = iota
	// agentContainer is a rootless container, started from the session of
	// the agent.
	agentContainer
	// rootSession is root in the machine, for setup, controls, and the uids
	// without a session.
	rootSession
)

// attempt is one escape attempt of the suite.
type attempt struct {
	// id is the stable id, such as "E04-mac-direct".
	id string
	// rule names the brief ids that the attempt proves.
	rule string
	// as is where try runs.
	as identity
	// control is the shell that must succeed before try runs, or "".
	control string
	// controlAs is where control runs.
	controlAs identity
	// controlFn is a control that runs in the test process, on the Mac. It
	// runs instead of control.
	controlFn func(ctx context.Context) error
	// informational means that a failed control does not fail the attempt
	// but makes it inconclusive (E11).
	informational bool
	// unreachableInconclusive means that a control that finds the network
	// of its target unreachable makes the attempt inconclusive (E04).
	unreachableInconclusive bool
	// after runs in the test process after try, and fails the attempt when
	// it returns an error: root compares the boundary with what it was
	// before (E22).
	after func(ctx context.Context) error
	// try is the shell that must fail. It exits non-zero and prints the
	// evidence on standard error when the attempt succeeded.
	try string
}

// probeImage is the image of the container attempts, built once in each
// sandbox that runs them.
const probeImage = "orbit-escape-probe"

// needTools defines need: it fails a probe that lacks a tool, so that a
// missing probe tool never reads as a refused attempt.
const needTools = `need() { local tool; for tool in "$@"; do command -v "$tool" >/dev/null 2>&1 || { echo "` + toolMissingMarker + `: $tool" >&2; exit 1; }; done; }
`

const (
	agentPrelude     = agentHelpers + refusedByProxy + "IMG=" + probeImage + "\n" + needTools + "as_agent() { \"$@\"; }\nwait_docker_or_exit() { wait_docker || exit 1; }\n"
	containerPrelude = agentPrelude + "wait_docker_or_exit\nneed docker\n"
	rootPrelude      = failCounter + blockedAllowed + asAgent + asUID + refusedByProxy + needTools + "finish() { [ \"$failures\" -eq 0 ]; }\n"
	finishScript     = "finish\n"
)

// escapeRig runs the attempts in one machine.
type escapeRig struct {
	client *orbstack.Client
	name   string
	tally  *tally
}

// escapeScript returns the full script of body for who.
func escapeScript(who identity, body string) []byte {
	switch who {
	case agentContainer:
		return []byte(containerPrelude + body + finishScript)
	case rootSession:
		return []byte(rootPrelude + body + finishScript)
	default:
		return []byte(agentPrelude + body + finishScript)
	}
}

// runIn runs body as who in the machine of the rig.
func (r escapeRig) runIn(ctx context.Context, who identity, body string) error {
	if who == rootSession {
		return r.client.RunAsRoot(ctx, r.name, escapeScript(who, body))
	}
	return r.client.RunAsAgent(ctx, r.name, escapeScript(who, body))
}

// run runs the attempt as a subtest of t and reports its verdict.
func (r escapeRig) run(ctx context.Context, t *testing.T, a attempt) {
	t.Helper()
	t.Run(a.id, func(t *testing.T) {
		t.Logf("%s proves %s", a.id, a.rule)
		result := attemptResult{id: a.id, hasControl: a.control != "" || a.controlFn != nil, informationalControl: a.informational, unreachableControlInconclusive: a.unreachableInconclusive}
		if err := r.runControl(ctx, a); err != nil {
			result.controlFailed, result.controlOutput = true, err.Error()
		}
		if !result.controlFailed || result.inconclusiveControl() {
			if err := r.runIn(ctx, a.as, a.try); err != nil {
				result.escaped, result.evidence = true, err.Error()
			}
			if a.after != nil {
				if err := a.after(ctx); err != nil {
					result.escaped, result.evidence = true, result.evidence+" after the attempt: "+err.Error()
				}
			}
		}
		r.report(t, result)
	})
}

// runControl runs the positive control of a, if it has one.
func (r escapeRig) runControl(ctx context.Context, a attempt) error {
	switch {
	case a.controlFn != nil:
		return a.controlFn(ctx)
	case a.control != "":
		return r.runIn(ctx, a.controlAs, a.control)
	}
	return nil
}

// report counts the outcome of result and fails t unless the attempt passed.
func (r escapeRig) report(t *testing.T, result attemptResult) {
	t.Helper()
	got, message := attemptVerdict(result)
	r.tally.add(got)
	switch got {
	case outcomePass:
		t.Logf("PASS %s", result.id)
	case outcomeInconclusive:
		t.Logf("INCONCLUSIVE %s", message)
	default:
		t.Error(message)
	}
}

// runAll runs the attempts in order.
func (r escapeRig) runAll(ctx context.Context, t *testing.T, attempts []attempt) {
	t.Helper()
	for _, a := range attempts {
		r.run(ctx, t, a)
	}
}

// header logs the versions once per test binary.
var header struct {
	once sync.Once
	err  error
}

// logOrbStackVersionOnce logs the OrbStack version, the macOS version, and
// the provisioning version in the first TestEscape test that runs, and
// fails every TestEscape test when orb version fails.
func logOrbStackVersionOnce(t *testing.T) {
	t.Helper()
	header.once.Do(func() {
		out, err := exec.Command("orb", "version").CombinedOutput()
		if err != nil {
			header.err = fmt.Errorf("orb version: %w: %s", err, out)
			return
		}
		mac, _ := exec.Command("sw_vers", "-productVersion").Output()
		t.Logf("OrbStack: %s; macOS %s; orbit provision version %d", strings.Join(strings.Fields(string(out)), " "), strings.TrimSpace(string(mac)), provision.Version)
	})
	if header.err != nil {
		t.Fatal(header.err)
	}
}

// escapeEnv holds what the attempts need from the host and the sandbox.
type escapeEnv struct {
	dir, sentinel, sentinelContent string
	macPort                        int
	macLAN                         string
	hostContainer                  string
	hostContainerErr               error
	defaultUID                     int
	target, targetAddr             string
	rig                            escapeRig
	// privileged is true when --privileged is allowed on this distribution and
	// started: E08 then checks the uids of a privileged container.
	privileged bool
	// runtimeGuard is true when the profile of this distribution carries the
	// runc guard: E08 then also tries a privileged exec.
	runtimeGuard bool
}

const targetPort = "9099"

// macHosts returns the names and addresses at which the Mac is tried.
func (e escapeEnv) macHosts() string {
	hosts := []string{macBridgeAddr, "host.orb.internal", "host.docker.internal"}
	if e.macLAN != "" {
		hosts = append(hosts, e.macLAN)
	}
	return strings.Join(hosts, " ")
}

// fill replaces the tokens of body with the values of e.
func (e escapeEnv) fill(body string) string {
	return strings.NewReplacer(
		"@DIR@", e.dir,
		"@SENTINEL@", e.sentinel,
		"@CONTENT@", e.sentinelContent,
		"@BRIDGE@", macBridgeAddr,
		"@PORT@", strconv.Itoa(e.macPort),
		"@MAC_HOSTS@", e.macHosts(),
		"@HOSTCTR@", e.hostContainer,
		"@CTRPORT@", hostContainerPort,
		"@DEFAULT_UID@", strconv.Itoa(e.defaultUID),
		"@ROOT_ONLY_FILES@", strings.Join(rootOnlyPaths(), " "),
		"@TARGET@", e.target,
		"@TARGET_ADDR@", e.targetAddr,
		"@TARGET_PORT@", targetPort,
	).Replace(body)
}

// newEscapeEnv writes the sentinel files, listens on the Mac, and finds the
// LAN address.
func newEscapeEnv(t *testing.T, dir string, rig escapeRig) escapeEnv {
	t.Helper()
	e := escapeEnv{dir: dir, rig: rig, defaultUID: os.Getuid(), sentinelContent: fmt.Sprintf("secret-%d-%d", os.Getpid(), os.Getuid()), runtimeGuard: runtimeGuardTable[integrationDistro(t)]}
	e.sentinel = filepath.Join(filepath.Dir(dir), "escape-sentinel-"+filepath.Base(dir))
	if err := os.WriteFile(e.sentinel, []byte(e.sentinelContent+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(e.sentinel) })
	if err := os.WriteFile(filepath.Join(dir, "sentinel-copy"), []byte(e.sentinelContent+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.macPort = serveOnTheMac(t)
	e.macLAN = macLANAddress()
	return e
}

// macLANAddress returns the first IPv4 address of an en interface of the Mac
// or "".
func macLANAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if !strings.HasPrefix(iface.Name, "en") || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ip, _, err := net.ParseCIDR(addr.String()); err == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() {
				return ip.String()
			}
		}
	}
	return ""
}

// buildProbeImage builds probeImage as the agent, through the proxy.
func buildProbeImage(ctx context.Context, t *testing.T, rig escapeRig) {
	t.Helper()
	body := `wait_docker_or_exit
dir=$(mktemp -d)
printf 'FROM alpine\nRUN apk add curl bind-tools nftables iproute2 iptables kmod util-linux openssh-client socat\n' > "$dir/Dockerfile"
docker build -q -t ` + probeImage + ` "$dir" >/dev/null 2>"$dir/err" || fail "build ` + probeImage + `: $(tail -3 "$dir/err")"
`
	started := time.Now()
	if err := rig.runIn(ctx, agentSession, body); err != nil {
		t.Fatalf("CONTROL FAILED: image: %v", err)
	}
	t.Logf("probe image built in %s", time.Since(started).Round(time.Second))
}

// macReachedByRoot is the control of the attempts on the Mac: root reaches
// the listener at the bridge address.
func (e escapeEnv) macReachedByRoot() string {
	return e.fill("need curl\nallowed \"root to the Mac\" curl -sSf --noproxy '*' -m 5 -o /dev/null http://@BRIDGE@:@PORT@\n")
}

// hostEngineAnswers is the control of E02: an engine exists on the Mac.
func hostEngineAnswers(ctx context.Context) error {
	if out, err := hostDocker(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("no engine answers on the Mac, so there is no host engine to escape to (%v): %s", err, out)
	}
	return nil
}

// hostPathAttempts is E01.
func (e escapeEnv) hostPathAttempts() []attempt {
	try := e.fill(`need find awk cat
S='@SENTINEL@'
D='@DIR@'
[ ! -e "$S" ] || fail "the sentinel is readable at its host path: $(cat "$S")"
for root in /mnt/mac /Users /Volumes; do
  [ -d "$root" ] || continue
  hit=$(find "$root" -name "$(basename "$S")" 2>/dev/null | head -1)
  [ -z "$hit" ] || fail "the sentinel is at $hit"
done
for mount in $(awk '/ - virtiofs mac /{print $5}' /proc/self/mountinfo); do
  [ "$mount" = "$D" ] || fail "a host share is mounted at $mount besides the project directory"
done
`)
	control := e.fill("[ \"$(cat '@DIR@/sentinel-copy')\" = '@CONTENT@' ] || fail \"the copy in the project directory is not readable\"\n")
	return []attempt{
		{id: "E01-host-path-agent", rule: "R08 D12", as: agentSession, control: control, controlAs: agentSession, try: try},
		{id: "E01-host-path-root", rule: "R08 D12", as: rootSession, control: control, controlAs: rootSession, try: try},
	}
}

// engineAttempts is E02.
func (e escapeEnv) engineAttempts() []attempt {
	agentTry := e.fill(`need curl bash timeout
` + engineSockets + `for target in host.docker.internal:2375 host.docker.internal:2376 docker.orb.local:2375; do
  blocked "TCP to $target" timeout 5 bash -c 'exec 3<>"/dev/tcp/${0%:*}/${0#*:}"' "$target"
done
`)
	containerTry := `blocked "a container mounts a host engine socket" docker run --rm -v /var/run/docker.sock:/s $IMG test -S /s
blocked "a container mounts a host engine socket at /run" docker run --rm -v /run/docker.sock:/s $IMG test -S /s
`
	return []attempt{
		{id: "E02-engine-agent", rule: "R10 D12 D15", as: agentSession, controlFn: hostEngineAnswers, try: agentTry},
		{id: "E02-engine-container", rule: "R10 D12 D15", as: agentContainer, controlFn: hostEngineAnswers, try: containerTry},
	}
}

// macAttempts is E03.
func (e escapeEnv) macAttempts() []attempt {
	agentTry := e.fill(`need curl
for host in @MAC_HOSTS@; do
  blocked "direct to $host" curl -sS --noproxy '*' -m 5 -o /dev/null "http://$host:@PORT@"
  refused_by_proxy "CONNECT through the proxy to $host" curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null "https://$host:@PORT@"
done
`)
	attempts := []attempt{{id: "E03-mac-agent", rule: "R03 R10 N07", as: agentSession, control: e.macReachedByRoot(), controlAs: rootSession, try: agentTry}}
	for _, v := range []struct{ id, flags string }{{"E03-mac-container", ""}, {"E03-mac-container-host-network", "--network host"}} {
		try := e.fill(`for host in @MAC_HOSTS@; do
  blocked "container direct to $host" docker run --rm ` + v.flags + ` $IMG curl -sS --noproxy '*' -m 5 -o /dev/null "http://$host:@PORT@"
  blocked "container through the proxy to $host" docker run --rm ` + v.flags + ` $IMG curl -sS -m 15 -o /dev/null "https://$host:@PORT@"
done
`)
		attempts = append(attempts, attempt{id: v.id, rule: "R03 R10 N07", as: agentContainer, control: e.macReachedByRoot(), controlAs: rootSession, try: try})
	}
	return attempts
}

// hostContainerReached is the control of E04: root reaches the container of
// the host engine. When no container started, the control fails. When root
// gets no answer at all, neither a connection nor a refusal, the control
// prints targetUnreachableMarker: OrbStack blocks the network of the host
// engine below the Orbit firewall, and E04 is inconclusive (owner,
// 2026-11-12). A refusal means that the network answered without the
// listener, and the control fails.
func (e escapeEnv) hostContainerReached(ctx context.Context) error {
	if e.hostContainerErr != nil {
		return fmt.Errorf("no container of the host engine started (%v)", e.hostContainerErr)
	}
	body := e.fill(`need bash timeout
for try in 1 2 3 4 5; do
  out=$(timeout 5 bash -c 'exec 3<>/dev/tcp/@HOSTCTR@/@CTRPORT@' 2>&1) && exit 0
  status=$?
  sleep 1
done
case "$status:$out" in
  124:*|*"No route to host"*|*"Network is unreachable"*) echo "` + targetUnreachableMarker + `: root gets no answer from @HOSTCTR@:@CTRPORT@ (exit $status${out:+: $out})" >&2; exit 1;;
esac
fail "root cannot reach the host-engine container at @HOSTCTR@:@CTRPORT@ (exit $status: $out)"
`)
	return e.rig.runIn(ctx, rootSession, body)
}

// hostContainerAttempts is E04.
func (e escapeEnv) hostContainerAttempts() []attempt {
	agentTry := e.fill(`need bash timeout
blocked "agent to the host-engine container" timeout 5 bash -c 'exec 3<>/dev/tcp/@HOSTCTR@/@CTRPORT@'
`)
	containerTry := e.fill(`blocked "container to the host-engine container" docker run --rm $IMG curl -sS --noproxy '*' -m 5 -o /dev/null http://@HOSTCTR@:@CTRPORT@
blocked "container with the network of the machine to the host-engine container" docker run --rm --network host $IMG curl -sS --noproxy '*' -m 5 -o /dev/null http://@HOSTCTR@:@CTRPORT@
`)
	return []attempt{
		{id: "E04-host-container-agent", rule: "N07 D15", as: agentSession, controlFn: e.hostContainerReached, unreachableInconclusive: true, try: agentTry},
		{id: "E04-host-container-container", rule: "N07 D15", as: agentContainer, controlFn: e.hostContainerReached, unreachableInconclusive: true, try: containerTry},
	}
}

// agentReachesGitHub is the control of the attempts that the allow rules
// still let an allowed host through.
const agentReachesGitHub = `need curl
[ "$(curl -s -m 20 -x http://127.0.0.1:3128 -o /dev/null -w '%{http_code}' https://github.com)" = 200 ] || fail "the agent did not reach github.com through the proxy"
`

// egressAttempts is E05.
func (e escapeEnv) egressAttempts() []attempt {
	try := `need curl bash ping timeout
blocked "direct to 1.1.1.1" curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
blocked "direct by IPv6" curl -sS --noproxy '*' -m 5 -k -o /dev/null "https://[2606:4700:4700::1111]"
blocked "direct by name" curl -sS --noproxy '*' -m 5 -o /dev/null https://example.org
blocked "UDP" timeout 5 bash -c 'echo x > /dev/udp/1.1.1.1/443'
blocked "ICMP" ping -c1 -W2 1.1.1.1
refused_by_proxy "proxy to example.org" curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://example.org
refused_by_proxy "proxy to github.com:22" curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://github.com:22
`
	return []attempt{{id: "E05-egress-agent", rule: "R03", as: agentSession, control: agentReachesGitHub, controlAs: agentSession, try: try}}
}

// containerReachesGitHub returns the control of a container with flags.
func containerReachesGitHub(flags string) string {
	return `[ "$(proxied ` + flags + ` 2>/dev/null)" = 200 ] || fail "a container with '` + flags + `' did not reach github.com through the proxy"` + "\n"
}

// containerEgressAttempts is E06.
func (e escapeEnv) containerEgressAttempts() []attempt {
	var attempts []attempt
	for _, v := range []struct{ id, flags string }{
		{"E06-container-default", ""},
		{"E06-container-host-network", "--network host"},
		{"E06-container-custom-network", "--network orbit-escape-net"},
		{"E06-container-user", "--user 65534"},
		{"E06-container-net-admin", "--cap-add NET_ADMIN --cap-add NET_RAW"},
	} {
		prefix := ""
		if strings.Contains(v.flags, "orbit-escape-net") {
			prefix = "docker network create orbit-escape-net >/dev/null 2>&1 || true\n"
		}
		attempts = append(attempts, attempt{
			id: v.id, rule: "R03 R10 A05", as: agentContainer,
			control: prefix + containerReachesGitHub(v.flags), controlAs: agentContainer,
			try: prefix + `no_direct "container with '` + v.flags + `'"  ` + v.flags + "\n",
		})
	}
	attempts = append(attempts, attempt{
		id: "E06-container-compose", rule: "R03 R10 A05", as: agentContainer,
		try: `dir=$(mktemp -d); cd "$dir"
printf 'services:\n  app:\n    image: ` + probeImage + `\n' > compose.yaml
blocked "direct connection from a compose service" docker compose -p orbitescape run --rm app curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
docker compose -p orbitescape down >/dev/null 2>&1
`,
		control: `dir=$(mktemp -d); cd "$dir"
printf 'services:\n  app:\n    image: ` + probeImage + `\n' > compose.yaml
code=$(docker compose -p orbitescape run --rm app curl -sS -m 20 -o /dev/null -w '%{http_code}' https://github.com 2>/dev/null)
docker compose -p orbitescape down >/dev/null 2>&1
[ "$code" = 200 ] || fail "a compose service did not reach github.com through the proxy"
`,
		controlAs: agentContainer,
	})
	attempts = append(attempts, attempt{
		id: "E06-sub-uid-in-the-network-of-the-machine", rule: "R03 R10 A05", as: agentContainer,
		try: leakProbe + "leak_probe\n",
	})
	return attempts
}

// dnsAttempts is E07.
func (e escapeEnv) dnsAttempts() []attempt {
	rootResolves := "need getent\nallowed \"root resolves github.com\" getent hosts github.com\n"
	agentTry := `need bash timeout getent
` + dnsProbe + `blocked "DNS over UDP to 0.250.250.200" timeout 5 bash -c 'exec 3<>/dev/udp/0.250.250.200/53 && echo x >&3'
blocked "DNS over TCP to 0.250.250.200" timeout 5 bash -c 'exec 3<>/dev/tcp/0.250.250.200/53'
blocked "DNS over UDP to 1.1.1.1" timeout 5 bash -c 'exec 3<>/dev/udp/1.1.1.1/53 && echo x >&3'
blocked "DNS over TCP to 1.1.1.1" timeout 5 bash -c 'exec 3<>/dev/tcp/1.1.1.1/53'
`
	containerTry := `for flags in "" "--dns 1.1.1.1" "--network host"; do
  blocked "container resolves with '$flags'" docker run --rm $flags $IMG nslookup -timeout=3 -retry=1 github.com
done
`
	return []attempt{
		{id: "E07-dns-agent", rule: "R03", as: agentSession, control: rootResolves, controlAs: rootSession, try: agentTry},
		{id: "E07-dns-container", rule: "R03", as: agentContainer, control: rootResolves, controlAs: rootSession, try: containerTry},
	}
}

// rootAttempts is E08.
func (e escapeEnv) rootAttempts() []attempt {
	agentTry := e.fill(`DEFAULT_USER=$(getent passwd @DEFAULT_UID@ | cut -d: -f1)
blocked "sudo" sudo -n true </dev/null
blocked "sudo.ws" /usr/bin/sudo.ws -n true </dev/null
blocked "the Rust sudo" /usr/lib/cargo/bin/sudo -n true </dev/null
blocked "su to root" su -c true root </dev/null
blocked "su to the default user" su -c true "$DEFAULT_USER" </dev/null
blocked "pkexec" pkexec true </dev/null
cap=$(awk '/^CapEff:/ {print $2}' /proc/self/status)
[ "$cap" = 0000000000000000 ] || fail "CapEff is $cap, want 0"
for group in sudo admin adm docker lxd disk shadow; do
  if id -nG | tr ' ' '\n' | grep -qx "$group"; then fail "the agent is in the group $group"; fi
done
`)
	containerTry := refusedPrivilegedRuns
	if e.runtimeGuard {
		containerTry += refusedPrivilegedExec
	}
	if e.privileged {
		containerTry = privilegedUIDs
	}
	return []attempt{
		{id: "E08-root-agent", rule: "R10 D14 D17", as: agentSession, try: agentTry},
		{id: "E08-root-container", rule: "R10 D14 D17", as: agentContainer, try: containerTry},
	}
}

// boundaryAttempts is E09.
func (e escapeEnv) boundaryAttempts() []attempt {
	agentTry := e.fill(`need systemctl pgrep
units="nftables.service tinyproxy.service orbit-harden.service orbit-proxy-address.service"
for unit in $units; do
  for action in stop restart; do blocked "systemctl $action $unit" systemctl --no-ask-password "$action" "$unit" </dev/null; done
done
proxy_pid=$(pgrep -o -x tinyproxy) || fail "tinyproxy is not running"
blocked "kill tinyproxy" kill -TERM "$proxy_pid"
blocked "nft add rule" nft add rule inet orbit_egress output accept
blocked "nft insert rule" nft insert rule inet orbit_egress output accept
blocked "nft flush ruleset" nft flush ruleset
for file in @ROOT_ONLY_FILES@; do
  blocked "write $file" sh -c ': >> "$1"' sh "$file"
  blocked "create a file next to $file" sh -c ': > "$1"' sh "$(dirname "$file")/orbit-probe"
done
for unit in $units; do allowed "$unit active after the attempts" systemctl is-active --quiet "$unit"; done
[ "$(pgrep -o -x tinyproxy)" = "$proxy_pid" ] || fail "tinyproxy restarted after the attempts"
blocked "direct to 1.1.1.1 after the attempts" curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
`)
	containerTry := `before=$(pidof tinyproxy 2>/dev/null || true)
blocked "write /etc/nftables.conf through a mount of /etc" docker run --rm -v /etc:/h $IMG sh -c 'echo x >> /h/nftables.conf'
blocked "write the allowlist through a mount of /" docker run --rm -v /:/h $IMG sh -c 'echo x >> /h/etc/tinyproxy/allowlist'
blocked "kill tinyproxy from the pid namespace of the machine" docker run --rm --pid host $IMG sh -c 'kill -9 $(pidof tinyproxy)'
[ "$(pidof tinyproxy 2>/dev/null || true)" = "$before" ] || fail "tinyproxy changed its pid"
`
	return []attempt{
		{id: "E09-boundary-agent", rule: "D14 A05", as: agentSession, try: agentTry},
		{id: "E09-boundary-container", rule: "D14 A05", as: agentContainer, try: containerTry},
	}
}

// noSessionAttempts is E10.
func (e escapeEnv) noSessionAttempts() []attempt {
	try := e.fill(`need curl setpriv
for uid in @DEFAULT_UID@ 100000 tinyproxy; do
  blocked "uid $uid to the Mac" as_uid "$uid" curl -sS --noproxy '*' -m 5 -o /dev/null http://@BRIDGE@:@PORT@
  blocked "uid $uid to 169.254.169.254" as_uid "$uid" curl -sS --noproxy '*' -m 5 -o /dev/null http://169.254.169.254
  blocked "uid $uid to the OrbStack gateway" as_uid "$uid" curl -sS --noproxy '*' -m 5 -o /dev/null http://198.19.249.1
  blocked "uid $uid to port 22" as_uid "$uid" curl -sS --noproxy '*' -m 5 -o /dev/null telnet://1.1.1.1:22
  blocked "uid $uid to port 8080" as_uid "$uid" curl -sS --noproxy '*' -m 5 -o /dev/null http://1.1.1.1:8080
done
for uid in @DEFAULT_UID@ 100000; do
  blocked "uid $uid direct to 1.1.1.1:443" as_uid "$uid" curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
done
`)
	control := "need curl\nallowed \"root direct to 1.1.1.1:443\" curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1\n"
	return []attempt{{id: "E10-uids-without-a-session", rule: "R03 D22", as: rootSession, control: control, controlAs: rootSession, try: try}}
}

// otherSandboxAttempts is E11.
func (e escapeEnv) otherSandboxAttempts() []attempt {
	control := e.fill("need curl\nallowed \"root to the other sandbox\" curl -sSf --noproxy '*' -m 5 -o /dev/null http://@TARGET_ADDR@:@TARGET_PORT@\n")
	hosts := e.fill("@TARGET_ADDR@ @TARGET@.orb.local")
	agentTry := e.fill(`need curl
for host in ` + hosts + `; do
  blocked "direct to $host" curl -sS --noproxy '*' -m 5 -o /dev/null "http://$host:@TARGET_PORT@"
  refused_by_proxy "CONNECT through the proxy to $host" curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null "https://$host:@TARGET_PORT@"
done
`)
	attempts := []attempt{{id: "E11-other-sandbox-agent", rule: "R03", as: agentSession, control: control, controlAs: rootSession, informational: true, try: agentTry}}
	for _, v := range []struct{ id, flags string }{{"E11-other-sandbox-container", ""}, {"E11-other-sandbox-container-host-network", "--network host"}} {
		try := e.fill(`for host in ` + hosts + `; do
  blocked "container direct to $host" docker run --rm ` + v.flags + ` $IMG curl -sS --noproxy '*' -m 5 -o /dev/null "http://$host:@TARGET_PORT@"
done
`)
		attempts = append(attempts, attempt{id: v.id, rule: "R03", as: agentContainer, control: control, controlAs: rootSession, informational: true, try: try})
	}
	return attempts
}

// startTarget creates the disposable target of E11, a plain isolated
// machine with a listener of root, and returns its address.
func startTarget(ctx context.Context, t *testing.T, client *orbstack.Client) (name, addr string) {
	t.Helper()
	dir, name := newSandboxDir(ctx, t, client)
	if err := client.Create(ctx, name, dir, "ubuntu", false); err != nil {
		t.Fatalf("create the target %s of E11: %v; machines that this test did not create: %s", name, err, otherMachines(ctx, client, name))
	}
	listen := "set -eu\nsystemd-run --quiet --unit=orbit-escape-listener bash -c \"while true; do printf 'HTTP/1.0 200 OK\\\\r\\\\nContent-Length: 0\\\\r\\\\n\\\\r\\\\n' | nc -l " + targetPort + "; done\"\nfor try in $(seq 20); do ss -Hltn 'sport = :" + targetPort + "' | grep -q . && exit 0; sleep 0.5; done\nexit 1\n"
	if out, err := orbRoot(ctx, name, listen); err != nil {
		t.Fatalf("start the listener in the target %s of E11: %v\n%s", name, err, out)
	}
	if out, err := orbRoot(ctx, name, "echo sentinel > /root/"+targetSentinel+"\n"); err != nil {
		t.Fatalf("write the sentinel of E19 in the target %s: %v\n%s", name, err, out)
	}
	out, err := orbRoot(ctx, name, "ip -4 -o addr show eth0 | awk '{split($4, a, \"/\"); print a[1]}'\n")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("address of the target %s of E11: %q, %v", name, out, err)
	}
	return name, strings.TrimSpace(out)
}

// refusedPrivilegedRuns is the privileged line of E08 on a distribution where
// --privileged is refused: on the default runtime and on each runtime that
// the daemon registers by itself, so that no runtime name skips the refusal.
// Each runtime has a control, an unprivileged container of the same image on
// the same runtime, so that a refusal never comes from a wrong runtime name or
// a missing image.
const refusedPrivilegedRuns = `blocked "a privileged container" docker run --rm --privileged $IMG true
for runtime in runc io.containerd.runc.v2; do
  allowed "an unprivileged container on the runtime $runtime" docker run --rm --runtime="$runtime" $IMG true
  blocked "a privileged container on the runtime $runtime" docker run --rm --runtime="$runtime" --privileged $IMG true
done
`

// refusedPrivilegedExec is the privileged exec line of E08, for a distribution
// whose profile carries the runc guard. An unprivileged exec in the same
// container is its control.
const refusedPrivilegedExec = `id=$(docker run -d $IMG sleep 60) || { fail "the container of the exec probe did not start"; id=; }
if [ -n "$id" ]; then
  allowed "an exec in a running container" docker exec "$id" true
  blocked "a privileged exec in a running container" docker exec --privileged "$id" true
  docker rm -f "$id" >/dev/null 2>&1
fi
`

// privilegedUIDs is the privileged line of E08 on a distribution where
// --privileged is allowed: every process of a privileged container runs, as
// the machine sees it, under the uid of the agent or one of its sub-uids,
// never uid 0.
const privilegedUIDs = `id=$(docker run -d --privileged $IMG sleep 30) || { fail "the privileged container did not start"; id=; }
if [ -n "$id" ]; then
  pids=$(docker top "$id" -eo pid | tail -n +2)
  [ -n "$pids" ] || fail "docker top lists no process of the privileged container"
  for pid in $pids; do
    uid=$(awk '/^Uid:/ {print $2}' "/proc/$pid/status" 2>/dev/null) || continue
    { [ "$uid" -ne 0 ] && [ "$uid" -ge "$(id -u)" ]; } || fail "process $pid of the privileged container runs as uid $uid"
  done
  docker rm -f "$id" >/dev/null 2>&1
fi
`
