package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentProbes holds the shell functions of the probes of the agent, run as
// root in a provisioned machine. Each failed check prints one line to
// standard error.
const agentProbes = `
` + failCounter + asAgent + `connect_code() { as_agent curl -s -m 15 -x http://127.0.0.1:3128 -o /dev/null -w '%{http_connect}' "https://$1/" || true; }
tunnel() { local code; code=$(connect_code "$1"); [ "$code" = 200 ] || fail "CONNECT $1:443 got $code, want a tunnel"; }
refused() { local code; code=$(connect_code "$1"); [ "$code" = 403 ] || fail "CONNECT $1:443 got $code, want 403"; }
claude=/home/agent/.local/bin/claude
`

// agentHelpers starts every script that runs as the agent: the counting
// helpers of the probes, a wait for the daemon, and the probe of a direct
// connection from a container of the image orbit-it-curl.
const agentHelpers = `
` + failCounter + blockedAllowed + `finish() { [ "$failures" -eq 0 ]; }
wait_docker() { for _ in $(seq 90); do docker info >/dev/null 2>&1 && return 0; sleep 1; done; fail "the daemon did not answer within 90 s"; return 1; }
` + containerHelpers + ``

// leakProbe is the probe of a sub-uid process in the network namespace of
// the machine, the leak path of the spike (A05). It starts the process with
// rootlesskit --net=host and setpriv, as any agent can, without the network
// of Docker. It fails unless the process runs as a sub-uid of the agent in
// the namespace of the machine, and then expects that the direct connection
// fails and that the request through the proxy succeeds. The attempt with
// docker run --network host is informational.
const leakProbe = `
sub_uid() { rootlesskit --net=host --disable-host-loopback=false setpriv --reuid=1000 --regid=1000 --clear-groups "$@"; }
leak_probe() {
  local mine out uid ns host before=$failures
  mine=$(readlink /proc/self/ns/net)
  if ! out=$(sub_uid sh -c 'id -u; readlink /proc/self/ns/net; awk "\$1<=1000 && 1000<\$1+\$3 {print \$2+1000-\$1}" /proc/self/uid_map' 2>&1); then
    fail "the sub-uid process did not start: $(printf '%s\n' "$out" | tail -1)"
    return
  fi
  uid=$(printf '%s\n' "$out" | sed -n 1p); ns=$(printf '%s\n' "$out" | sed -n 2p); host=$(printf '%s\n' "$out" | sed -n 3p)
  [ "$uid" = 1000 ] || fail "the process runs as uid $uid, want 1000 in its user namespace"
  [ "${host:-0}" -ge 100000 ] || fail "the process runs as host uid '$host', want a sub-uid of the agent"
  [ "$ns" = "$mine" ] || fail "the process is in $ns, not in the namespace $mine of the machine"
  if sub_uid curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1 >/dev/null 2>&1; then fail "direct connection from the sub-uid process in the namespace of the machine succeeded"; fi
  [ "$(sub_uid curl -sS -m 20 -x http://172.31.255.1:3128 -o /dev/null -w '%{http_code}' https://github.com 2>/dev/null)" = 200 ] || fail "the sub-uid process in the namespace of the machine did not reach an allowed host through the proxy"
  [ "$failures" -eq "$before" ] && echo "leak path exercised: the process runs as host uid $host in $ns, the namespace of the machine, and only the proxy is reachable"
  echo "informational: docker run --network host: $(docker run --rm --network host $IMG true 2>&1 | head -1)"
}
`

// containerTraffic is the scenarios "Direct connections from containers",
// "Refused host through the proxy", "Host engine out of reach",
// "Machine namespaces are allowed", and "Host mounts and the machine pid
// namespace". HOST_CONTAINER is the address of a container of the host
// engine, or empty.
const containerTraffic = agentHelpers + `
HOST_CONTAINER='%s'
docker network create orbit-it-net >/dev/null || fail "create a custom network"
for variant in "default network:" "--network host:--network host" "custom network:--network orbit-it-net" "--user 65534:--user 65534" "--dns 1.1.1.1:--dns 1.1.1.1" "capabilities:--cap-add NET_ADMIN --cap-add NET_RAW"; do
  what=${variant%%:*}; flags=${variant#*:}
  no_direct "$what" $flags
  [ "$(proxied $flags 2>/dev/null)" = 200 ] || fail "control: a container with $what did not reach github.com through the proxy"
done
dir=$(mktemp -d); cd "$dir"
printf 'services:\n  app:\n    image: orbit-it-curl\n' > compose.yaml
if docker compose -p orbitcurl run --rm app curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1 >/dev/null 2>&1; then fail "direct connection from a compose service succeeded"; fi
[ "$(docker compose -p orbitcurl run --rm app curl -sS -m 20 -o /dev/null -w '%{http_code}' https://github.com 2>/dev/null)" = 200 ] || fail "control: a compose service did not reach github.com through the proxy"
docker compose -p orbitcurl down >/dev/null 2>&1
if docker run --rm $IMG curl -sS -m 15 -o /dev/null https://example.org 2>&1 | grep -q 403; then :; else fail "a container did not get 403 for https://example.org"; fi
[ "$(docker context show)" = orbit-rootless ] || fail "the current docker context is not orbit-rootless"
[ "$(docker context ls --format '{{.Name}}' | grep -v -x -e default -e orbit-rootless | wc -l)" = 0 ] || fail "a context besides orbit-rootless exists"
[ -z "$(docker ps -a -q)" ] || fail "containers are left over"
if [ -n "$HOST_CONTAINER" ]; then
  if docker run --rm $IMG curl -sS --noproxy '*' -m 5 "http://$HOST_CONTAINER:` + hostContainerPort + `" >/dev/null 2>&1; then fail "a container reached the container of the host engine"; fi
fi
allowed "pid host" docker run --rm --pid host alpine true
allowed "network host" docker run --rm --network host alpine true
before=$(pidof tinyproxy 2>/dev/null || true)
blocked "write /etc/nftables.conf through a mount of /etc" docker run --rm -v /etc:/h alpine sh -c 'echo x >> /h/nftables.conf'
blocked "write the allowlist through a mount of /" docker run --rm -v /:/h alpine sh -c 'echo x >> /h/etc/tinyproxy/allowlist'
blocked "kill tinyproxy from the pid namespace of the machine" docker run --rm --pid host alpine sh -c 'kill -9 $(pidof tinyproxy)'
[ "$(pidof tinyproxy 2>/dev/null || true)" = "$before" ] || fail "tinyproxy changed its pid"
blocked "privileged container" docker run --rm --privileged alpine true
docker network rm orbit-it-net >/dev/null 2>&1
finish
`

// afterStartProbes checks a started sandbox as root: the file of the agent
// survived, the agent reaches no public address directly and no host
// without an allow rule through the proxy, but reaches an allowed host, and
// every unit of the boundary entered active, in this boot, before the unit
// of every process with a uid from 1000 up started. It compares the
// timestamps of systemd in microseconds: the start time of a process in
// /proc counts clock ticks of 10 ms, too coarse to order it.
const afterStartProbes = agentProbes + `
content=$(as_agent cat ` + stateFile + `) || fail "` + stateFile + ` is gone"
[ "$content" = '` + stateFileContent + `' ] || fail "` + stateFile + ` holds $content"
if as_agent curl -sS --noproxy '*' -m 5 -o /dev/null -k https://1.1.1.1 2>/dev/null; then fail "agent reached 1.1.1.1 directly"; fi
refused example.org
tunnel github.com
tunnel api.anthropic.com
latest=0
for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do
  at=$(systemctl show -p ActiveEnterTimestampMonotonic --value "$unit")
  [ "${at:-0}" -gt 0 ] || fail "$unit did not enter active in this boot"
  [ "${at:-0}" -gt "$latest" ] && latest=$at
done
agents=0
: > ` + bootProcessesFile + `
chmod 0644 ` + bootProcessesFile + `
for dir in /proc/[0-9]*; do
  uid=$(awk '/^Uid:/ {print $2}' "$dir/status" 2>/dev/null) || continue
  [ -n "$uid" ] && [ "$uid" -ge 1000 ] || continue
  unit=$(awk -F: '$1 == "0" {print $3}' "$dir/cgroup" 2>/dev/null | tr / '\n' | grep -m1 -E '[.](service|scope)$') || unit=""
  [ -n "$unit" ] || { fail "process ${dir#/proc/} with uid $uid is in no unit"; continue; }
  started=$(systemctl show -p InactiveExitTimestampMonotonic --value "$unit")
  agents=$((agents + 1))
  echo "process ${dir#/proc/} uid $uid ppid $(awk '/^PPid:/ {print $2}' "$dir/status") ($(tr '\0' ' ' < "$dir/cmdline" | cut -c1-80)) in $unit, which started at $started us" >> ` + bootProcessesFile + `
  [ "${started:-0}" -gt "$latest" ] || fail "process ${dir#/proc/} with uid $uid runs in $unit, which started at $started us, before the boundary was active at $latest us"
done
echo "boundary active at $latest us; $agents processes with a uid from 1000 up, all started later" >> ` + bootProcessesFile + `
{ loginctl list-users --no-legend; systemctl list-units --type=service,scope --state=active --no-legend 'user@*' 'session-*' 2>/dev/null; } >> ` + bootProcessesFile + ` 2>&1 || true
[ "$failures" -eq 0 ]
`

// bootProcessesFile is the file in the machine that afterStartProbes fills
// with the processes that have a uid from 1000 up, so that a passing run
// keeps them: RunAsRoot returns the standard error of a script only when it
// fails. It is under /run, which the boot empties.
const bootProcessesFile = "/run/orbit-it-boot-processes"

// The file that the agent writes in its home directory before the stop, and
// its content, which must survive the stop and the start.
const (
	stateFile        = "/home/agent/orbit-it-state"
	stateFileContent = "written before the stop"
)

// unforwardedProbes checks, in a sandbox without the SSH agent, the scenario
// "Init with the flag" and that the push path to GitHub still works.
const unforwardedProbes = `
` + failCounter + asAgent + `
[ "$(id -nG agent)" = agent ] || fail "agent has groups: $(id -nG agent), want only agent"
[ ! -e /opt/orbstack-guest/run/host-ssh-agent.sock ] || fail "the socket of a forwarded agent exists"
[ -z "$(as_agent env | grep '^SSH_AUTH_SOCK=' || true)" ] || fail "SSH_AUTH_SOCK is set for agent"
if as_agent ssh-add -l >/dev/null 2>&1; then fail "ssh-add -l as agent reached an agent"; fi
out=$(as_agent timeout 60 ssh -T git@github.com </dev/null 2>&1 || true)
case "$out" in
  *"Permission denied (publickey)"*) ;;
  *) fail "ssh -T git@github.com did not reach GitHub: $out";;
esac

[ "$failures" -eq 0 ]
`

// probeScript checks, as root in a provisioned machine, every scenario of
// the sandbox-provisioning capability except the restart. Root is the
// positive control for every destination that a filtered uid must not
// reach; a Mac port of 0 means that root could not reach the Mac, so the
// probes to the Mac do not run. An empty host container means that root
// could not reach a container of the host engine, so its probe does not run
// either. Neither the Mac nor a container of the host engine answers over
// IPv6 in a machine, so root listens on the IPv6 address of the machine as
// the destination of the IPv6 probes. The project directory holds
// hostFile, which the agent reads and deletes, and gets agentFile from the
// agent. The probes that try to stop the boundary run last, and the
// boundary is checked again after them. Each failed check prints one line
// to standard error, and the script exits non-zero when any check failed.
const probeScript = `
` + failCounter + blockedAllowed + asAgent + asUID + `direct() { curl -sSf --noproxy '*' -m 5 "$@"; }
` + refusedByProxy + `DEFAULT_UID=%d
MAC_PORT=%d
HOST_CONTAINER=%s
PROJECT_DIR='%s'
ROOT_ONLY_FILES="%s"
PROVISION_VERSION=%d
DEFAULT_USER=$(getent passwd "$DEFAULT_UID" | cut -d: -f1)

[ "$(cat /etc/orbit/provisioned)" = "$PROVISION_VERSION" ] || fail "marker"
[ "$(id -u agent)" -ge 1000 ] || fail "agent uid below 1000"
[ "$(id -nG agent)" = "agent orbstack" ] || fail "agent has groups: $(id -nG agent), want agent orbstack: the default init forwards the SSH agent"
blocked "agent sudo" as_agent sudo -n true
blocked "agent su to root" as_agent su -c true root </dev/null
blocked "agent su to the default user" as_agent su -c true "$DEFAULT_USER" </dev/null
blocked "agent nft list" as_agent nft list ruleset
blocked "agent apt-get" as_agent apt-get install -y htop
for tool in git ssh curl jq rg unzip; do allowed "toolchain $tool" command -v "$tool"; done

[ "$(as_agent cat "$PROJECT_DIR/` + hostFile + `")" = "` + hostFileContent + `" ] || fail "agent read in the project directory"
allowed "agent delete in the project directory" as_agent rm "$PROJECT_DIR/` + hostFile + `"
allowed "agent write in the project directory" as_agent sh -c 'echo "$2" > "$1"' sh "$PROJECT_DIR/` + agentFile + `" "` + agentFileContent + `"
[ "$(as_agent cat "$PROJECT_DIR/` + agentFile + `")" = "` + agentFileContent + `" ] || fail "agent read its file in the project directory"

` + engineSockets + `
if sudo -l -U "$DEFAULT_USER" 2>/dev/null | grep -q 'may run'; then fail "default user has a sudo rule"; fi
passwd -S "$DEFAULT_USER" | grep -q ' L ' || fail "default user password not locked"
for group in sudo admin adm; do
  members=$(getent group "$group" | cut -d: -f4)
  [ -z "$members" ] || fail "group $group has members $members"
done

allowed "root direct to 1.1.1.1" direct -k -o /dev/null https://1.1.1.1
blocked "agent direct to 1.1.1.1" as_agent curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1
blocked "agent direct by name" as_agent curl -sSf --noproxy '*' -m 5 https://github.com
` + dnsProbe + `blocked "default uid direct" as_uid "$DEFAULT_UID" curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1
blocked "container uid direct" as_uid 100000 curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1

allowed "agent proxy to github.com" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://github.com
allowed "agent proxy on the container address" as_agent curl -sSf -m 15 -x http://172.31.255.1:3128 -o /dev/null https://github.com
allowed "agent proxy without case" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://GitHub.com
refused_by_proxy "agent proxy to example.org" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://example.org
refused_by_proxy "agent proxy to github.com:22" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://github.com:22

blocked "proxy user to 169.254.169.254" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 http://169.254.169.254
blocked "proxy user to 198.19.249.1" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 http://198.19.249.1
blocked "proxy user to 127.0.0.1:22" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 telnet://127.0.0.1:22
allowed "proxy user to port 80" as_uid tinyproxy curl -sS --noproxy '*' -m 5 -o /dev/null http://deb.debian.org
allowed "proxy user to port 443" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
blocked "proxy user to port 22" as_uid tinyproxy curl -sS --noproxy '*' -m 5 telnet://1.1.1.1:22
blocked "proxy user to port 8080" as_uid tinyproxy curl -sS --noproxy '*' -m 5 http://deb.debian.org:8080

if [ "$MAC_PORT" -gt 0 ]; then
  MAC="http://` + macBridgeAddr + `:$MAC_PORT"
  blocked "agent to the Mac" as_agent curl -sSf --noproxy '*' -m 5 "$MAC"
  blocked "default uid to the Mac" as_uid "$DEFAULT_UID" curl -sSf --noproxy '*' -m 5 "$MAC"
  blocked "proxy user to the Mac" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 "$MAC"
fi

V6=$(ip -6 -o addr show dev eth0 scope global | awk 'NR == 1 {split($4, a, "/"); print a[1]}')
if [ -n "$V6" ]; then
  python3 -c 'import socket, sys, time; s = socket.socket(socket.AF_INET6); s.bind((sys.argv[1], ` + ipv6ProbePort + `)); s.listen(8); time.sleep(60)' "$V6" </dev/null >/dev/null 2>&1 &
  listener=$!
  for try in 1 2 3 4 5 6 7 8 9 10; do ss -Hltn 'sport = :` + ipv6ProbePort + `' | grep -q . && break; sleep 0.5; done
  allowed "root TCP over IPv6 to the machine" timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/` + ipv6ProbePort + `"' sh "$V6"
  blocked "agent TCP over IPv6 to the machine" as_agent timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/` + ipv6ProbePort + `"' sh "$V6"
  blocked "agent UDP over IPv6 to the machine" as_agent timeout 5 bash -c 'echo x > "/dev/udp/$1/` + ipv6ProbePort + `"' sh "$V6"
  kill "$listener"; wait "$listener" 2>/dev/null
else
  fail "the machine has no global IPv6 address"
fi

if [ -n "$HOST_CONTAINER" ]; then
  blocked "agent to the host-engine container" as_agent timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/` + hostContainerPort + `"' sh "$HOST_CONTAINER"
fi

others=$(ss -Hlntu | awk '{print $5}' | grep -v -E ':(3128|68)$' || true)
[ -z "$others" ] || fail "unexpected listeners: $others"

for unit in nftables.service tinyproxy.service orbit-harden.service orbit-proxy-address.service; do
  for action in stop restart; do blocked "agent systemctl $action $unit" as_agent systemctl --no-ask-password "$action" "$unit" </dev/null; done
done
proxy_pid=$(pgrep -o -x tinyproxy) || fail "tinyproxy is not running"
blocked "agent kill tinyproxy" as_agent kill -TERM "$proxy_pid"
blocked "agent nft add rule" as_agent nft add rule inet orbit_egress output accept
blocked "agent nft insert rule" as_agent nft insert rule inet orbit_egress output accept
blocked "agent nft flush ruleset" as_agent nft flush ruleset
for file in $ROOT_ONLY_FILES; do
  blocked "agent write $file" as_agent sh -c ': >> "$1"' sh "$file"
  blocked "agent create a file next to $file" as_agent sh -c ': > "$1"' sh "$(dirname "$file")/orbit-probe"
done
for unit in nftables.service tinyproxy.service orbit-harden.service orbit-proxy-address.service; do
  allowed "$unit active after the attempts of the agent" systemctl is-active --quiet "$unit"
done
[ "$(pgrep -o -x tinyproxy)" = "$proxy_pid" ] || fail "tinyproxy restarted after the attempts of the agent"
blocked "agent direct to 1.1.1.1 after the attempts of the agent" as_agent curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1

[ "$failures" -eq 0 ]
`

// macBridgeAddr is the address of the Mac on the bridge of OrbStack, as the
// spike of 2026-11-10 found it reachable from a machine.
const macBridgeAddr = "192.168.139.3"

// ipv6ProbePort is the port on which root listens on the IPv6 address of
// the machine.
const ipv6ProbePort = "8443"

// hostContainerPort is the port on which the probe container of the host
// engine listens.
const hostContainerPort = "8080"

// The files through which the probes check that the agent writes the
// project directory: the host writes hostFile, the agent reads and deletes
// it, and the agent writes agentFile, which the host reads.
const (
	hostFile         = "written-on-the-host"
	hostFileContent  = "from the host"
	agentFile        = "written-by-the-agent"
	agentFileContent = "from the agent"
)

// goldenScripts renders the six probe scripts with fixed arguments. The
// files under testdata/escape-golden/ hold the expected output.
func goldenScripts() map[string]string {
	return map[string]string{
		"probe-script":       fmt.Sprintf(probeScript, 501, 4242, "172.17.0.2", "/Users/orbit/project", "/etc/nftables.conf /etc/tinyproxy/tinyproxy.conf", 7),
		"agent-helpers":      agentHelpers,
		"leak-probe":         leakProbe,
		"container-traffic":  strings.Replace(containerTraffic, "%s", "172.17.0.2", 1),
		"after-start-probes": afterStartProbes,
		"unforwarded-probes": unforwardedProbes,
	}
}

// TestProbeScriptsMatchGolden fails when a rendered probe script differs
// from its golden file by a single byte. It starts no machine.
func TestProbeScriptsMatchGolden(t *testing.T) {
	for name, got := range goldenScripts() {
		want, err := os.ReadFile(filepath.Join("testdata", "escape-golden", name+".sh"))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs from testdata/escape-golden/%s.sh", name, name)
		}
	}
}
