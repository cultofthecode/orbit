//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/proxy"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// provisionTimeout bounds the provisioning test: two runs of the root
// script, each with a package install, and the probes.
const provisionTimeout = 10 * time.Minute

// initLockPath is the file that serializes the integration runs on one
// host: TestActiveMountsShowASwappedPath in internal/orbstack holds an
// exclusive lock on the same path while its machine makes init refuse
// every directory.
const initLockPath = "/tmp/orbit-integration-init.lock"

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

// rootOnlyFiles are the files of the boundary, besides the units, that only
// root may write.
var rootOnlyFiles = []string{"/etc/nftables.conf", proxy.ConfigPath, proxy.AllowlistPath, "/etc/environment"}

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
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
blocked() { local what=$1; shift; if "$@" >/dev/null 2>&1; then fail "$what succeeded"; fi; }
allowed() { local what=$1; shift; "$@" >/dev/null 2>&1 || fail "$what failed"; }
as_agent() { runuser -u agent -- "$@"; }
as_uid() { local uid=$1; shift; setpriv --reuid "$uid" --regid "$uid" --clear-groups "$@"; }
direct() { curl -sSf --noproxy '*' -m 5 "$@"; }
refused_by_proxy() { local what=$1; shift; local out; out=$("$@" 2>&1) && fail "$what succeeded"; case "$out" in *403*) ;; *) fail "$what failed without a 403: $out";; esac; }
DEFAULT_UID=%d
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

for socket in /var/run/docker.sock /run/docker.sock; do blocked "agent sees $socket" as_agent test -e "$socket"; done
for socket in $(find / \( -path /proc -o -path /sys \) -prune -o -type s -print 2>/dev/null); do
  [ "$(as_agent curl -s -m 2 --unix-socket "$socket" http://engine/_ping 2>/dev/null)" != OK ] || fail "agent reached an engine on $socket"
done

if sudo -l -U "$DEFAULT_USER" 2>/dev/null | grep -q 'may run'; then fail "default user has a sudo rule"; fi
passwd -S "$DEFAULT_USER" | grep -q ' L ' || fail "default user password not locked"
for group in sudo admin adm; do
  members=$(getent group "$group" | cut -d: -f4)
  [ -z "$members" ] || fail "group $group has members $members"
done

allowed "root direct to 1.1.1.1" direct -k -o /dev/null https://1.1.1.1
blocked "agent direct to 1.1.1.1" as_agent curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1
blocked "agent direct by name" as_agent curl -sSf --noproxy '*' -m 5 https://github.com
blocked "agent DNS to OrbStack DNS" as_agent timeout 5 bash -c 'exec 3<>/dev/udp/0.250.250.200/53 && echo x >&3'
blocked "agent UDP" as_agent timeout 5 bash -c 'echo x > /dev/udp/1.1.1.1/443'
blocked "agent ping" as_agent ping -c1 -W2 1.1.1.1
blocked "agent ping IPv6" as_agent ping -6 -c1 -W2 2606:4700:4700::1111
blocked "agent TCP over IPv6" as_agent timeout 5 bash -c 'exec 3<>/dev/tcp/2606:4700:4700::1111/443'
blocked "agent UDP over IPv6" as_agent timeout 5 bash -c 'echo x > /dev/udp/2606:4700:4700::1111/53'
blocked "agent resolvectl" as_agent resolvectl query github.com
blocked "agent getent hosts" as_agent getent hosts github.com
blocked "default uid direct" as_uid "$DEFAULT_UID" curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1
blocked "container uid direct" as_uid 100000 curl -sSf --noproxy '*' -m 5 -k https://1.1.1.1

allowed "agent proxy to github.com" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://github.com
allowed "agent proxy on the container address" as_agent curl -sSf -m 15 -x http://172.31.255.1:3128 -o /dev/null https://github.com
allowed "agent proxy without case" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://GitHub.com
refused_by_proxy "agent proxy to example.org" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://example.org
refused_by_proxy "agent proxy to github.com:22" as_agent curl -sSf -m 15 -x http://127.0.0.1:3128 -o /dev/null https://github.com:22

blocked "proxy user to 169.254.169.254" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 http://169.254.169.254
blocked "proxy user to 198.19.249.1" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 http://198.19.249.1
blocked "proxy user to 127.0.0.1:22" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 telnet://127.0.0.1:22
blocked "proxy user to port 80" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 http://1.1.1.1
allowed "proxy user to port 443" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1

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

func TestInitProvisionsTheSandbox(t *testing.T) {
	waitToRunInit(t)
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	t.Cleanup(cancel)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	first, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || !first.Created {
		t.Fatalf("first Handle = %+v, %v, want a created sandbox; machines that this test did not create: %s", first, err, otherMachines(ctx, client, name))
	}
	second, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || second.Created {
		t.Fatalf("second Handle = %+v, %v, want the existing sandbox provisioned again; machines that this test did not create: %s", second, err, otherMachines(ctx, client, name))
	}

	port := macPortReachableByRoot(ctx, t, client, name)
	container := hostContainerReachableByRoot(ctx, t, client, name)
	writeHostFile(t, dir)
	script := fmt.Sprintf(probeScript, os.Getuid(), port, container, dir, strings.Join(rootOnlyPaths(), " "), provision.Version)
	if err := client.RunAsRoot(ctx, name, []byte(script)); err != nil {
		t.Errorf("probes in %s:\n%v", name, err)
	}
	checkAgentWroteTheProject(t, dir)
	checkProxyClosedFromTheMac(t, name)
}

// waitToRunInit holds a shared lock on initLockPath until the end of t. It
// waits while a test of any run on the host holds the exclusive lock, so
// that init does not refuse because of a machine of another run. Call it
// before the deadline of the test starts.
func waitToRunInit(t *testing.T) {
	t.Helper()
	file, err := os.OpenFile(initLockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		return
	}
	t.Logf("waiting for a test of another run that blocks init (%s)", initLockPath)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatalf("lock %s: %v", initLockPath, err)
	}
}

// otherMachines returns the names of the machines on the host besides
// name, for the failure message of an init test: init checks the mounts of
// every machine, so a machine that the test did not create can make it
// refuse.
func otherMachines(ctx context.Context, client *orbstack.Client, name string) string {
	mounts, err := client.Mounts(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	names := slices.DeleteFunc(slices.Sorted(maps.Keys(mounts)), func(machine string) bool { return machine == name })
	return strings.Join(names, ", ")
}

// hardenRestarts restarts the harden unit as every run of init does, one
// time more than the default start limit of systemd allows (5 starts in
// 10 s). Each restart also restarts systemd-user-sessions.service, which
// requires the harden unit. The burst does not depend on how fast apt-get
// runs in init. Both units stay active, and logins stay enabled. The
// restart of the harden unit returns before the restart of user sessions
// ends, so the script waits until user sessions leave a transient state.
const hardenRestarts = `
set -eu
for run in 1 2 3 4 5 6; do systemctl restart orbit-harden.service; done
for wait in $(seq 300); do
	case "$(systemctl is-active systemd-user-sessions.service)" in activating|deactivating|reloading) sleep 0.1;; *) break;; esac
done
systemctl is-active --quiet orbit-harden.service
systemctl is-active --quiet systemd-user-sessions.service
! test -e /run/nologin
`

// TestInitRunsAgainAfterRepeatedRuns re-runs init on one sandbox right
// after the harden unit restarted more often than the default start limit
// of systemd allows (#66).
func TestInitRunsAgainAfterRepeatedRuns(t *testing.T) {
	waitToRunInit(t)
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	t.Cleanup(cancel)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	handler := initHandler(client, claudeRegistry(t))

	if _, err := handler.Handle(ctx, initialize.Input{Dir: dir}); err != nil {
		t.Fatalf("first Handle = %v; machines that this test did not create: %s", err, otherMachines(ctx, client, name))
	}
	for run := 2; run <= 3; run++ {
		if err := client.RunAsRoot(ctx, name, []byte(hardenRestarts)); err != nil {
			t.Fatalf("restarts of orbit-harden.service before run %d in %s:\n%v", run, name, err)
		}
		if out, err := handler.Handle(ctx, initialize.Input{Dir: dir}); err != nil || out.Created {
			t.Fatalf("Handle run %d = %+v, %v, want the existing sandbox provisioned again; machines that this test did not create: %s", run, out, err, otherMachines(ctx, client, name))
		}
	}
}

// rootOnlyPaths returns rootOnlyFiles and the units and the harden script of
// the boundary.
func rootOnlyPaths() []string {
	paths := slices.Clone(rootOnlyFiles)
	for _, unit := range provision.Units(uint32(os.Getuid())) {
		paths = append(paths, unit.Path)
	}
	return paths
}

// writeHostFile writes hostFile into the project directory dir on the host.
func writeHostFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, hostFile), []byte(hostFileContent+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkAgentWroteTheProject expects that the agent deleted hostFile and
// wrote agentFile in the project directory dir, as the host sees it.
func checkAgentWroteTheProject(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, hostFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s on the host after the agent deleted it: %v", hostFile, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, agentFile))
	if err != nil || string(content) != agentFileContent+"\n" {
		t.Errorf("%s on the host = %q, %v, want %q", agentFile, content, err, agentFileContent+"\n")
	}
}

// projectDir returns the canonical path of a new project directory that
// the mount rules accept.
func projectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// serveOnTheMac starts an HTTP server on every address of the Mac and
// returns its port. Root in the machine reaching it is the positive control
// of the probes to the Mac.
func serveOnTheMac(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// macPortReachableByRoot serves on the Mac and returns the port when root in
// the machine name reaches it, or 0 after it logs that the probes to the Mac
// cannot run.
func macPortReachableByRoot(ctx context.Context, t *testing.T, client *orbstack.Client, name string) int {
	t.Helper()
	port := serveOnTheMac(t)
	control := fmt.Sprintf("curl -sSf --noproxy '*' -m 5 -o /dev/null http://%s:%d\n", macBridgeAddr, port)
	if err := client.RunAsRoot(ctx, name, []byte(control)); err != nil {
		t.Logf("root in %s cannot reach the Mac at %s:%d (%v); the probes to the Mac did not run", name, macBridgeAddr, port, err)
		return 0
	}
	return port
}

// hostContainerReachableByRoot starts a container on the host engine that
// listens on hostContainerPort and returns its address when root in the
// machine name reaches it, or "" after it logs that the probe of the
// container cannot run.
func hostContainerReachableByRoot(ctx context.Context, t *testing.T, client *orbstack.Client, name string) string {
	t.Helper()
	addr, err := startHostContainer(ctx, t)
	if err != nil {
		t.Logf("no container on the host engine (%v); the probe of the host-engine container did not run", err)
		return ""
	}
	control := fmt.Sprintf("for try in 1 2 3 4 5; do timeout 5 bash -c 'exec 3<>/dev/tcp/%s/%s' && exit 0; sleep 1; done; exit 1\n", addr, hostContainerPort)
	if err := client.RunAsRoot(ctx, name, []byte(control)); err != nil {
		t.Logf("root in %s cannot reach the host-engine container at %s:%s (%v); the probe of the host-engine container did not run", name, addr, hostContainerPort, err)
		return ""
	}
	return addr
}

// startHostContainer runs a busybox HTTP server on the host engine, removes
// it at the end of the test, and returns its IPv4 address.
func startHostContainer(ctx context.Context, t *testing.T) (string, error) {
	t.Helper()
	id, err := hostDocker(ctx, "run", "-d", "--rm", "busybox:1.37", "httpd", "-f", "-p", hostContainerPort)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _, _ = hostDocker(context.WithoutCancel(ctx), "rm", "-f", id) })
	addr, err := hostDocker(ctx, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	if err != nil {
		return "", err
	}
	if _, err := netip.ParseAddr(addr); err != nil {
		return "", fmt.Errorf("container %s has no address: %w", id, err)
	}
	return addr, nil
}

// hostDocker runs the docker client of the Mac and returns its trimmed
// standard output.
func hostDocker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// checkProxyClosedFromTheMac dials the proxy port of the machine from the
// Mac and expects no connection.
func checkProxyClosedFromTheMac(t *testing.T, name string) {
	t.Helper()
	host := name + ".orb.local"
	if _, err := net.LookupHost(host); err != nil {
		t.Logf("%s does not resolve on the Mac (%v); the check of the proxy port from outside did not run", host, err)
		return
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "3128"), 5*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Errorf("the Mac connected to %s:3128", host)
	} else if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "refused") {
		t.Logf("dial %s:3128 failed with %v", host, err)
	}
}
