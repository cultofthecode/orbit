//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// provisionTimeout bounds the provisioning test: two runs of the root
// script, each with a package install, and the probes.
const provisionTimeout = 10 * time.Minute

// macBridgeAddr is the address of the Mac on the bridge of OrbStack, as the
// spike of 2026-11-10 found it reachable from a machine.
const macBridgeAddr = "192.168.139.3"

// probeScript checks, as root in a provisioned machine, every scenario of
// the sandbox-provisioning capability except the restart. Root is the
// positive control for every destination that a filtered uid must not
// reach; a Mac port of 0 means that root could not reach the Mac, so the
// probes to the Mac do not run. Each failed check prints one line to standard error, and the
// script exits non-zero when any check failed.
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
DEFAULT_USER=$(getent passwd "$DEFAULT_UID" | cut -d: -f1)

[ "$(cat /etc/orbit/provisioned)" = 1 ] || fail "marker"
[ "$(id -u agent)" -ge 1000 ] || fail "agent uid below 1000"
[ "$(id -nG agent)" = agent ] || fail "agent has groups: $(id -nG agent)"
blocked "agent sudo" as_agent sudo -n true
blocked "agent su to root" as_agent su -c true root </dev/null
blocked "agent su to the default user" as_agent su -c true "$DEFAULT_USER" </dev/null
blocked "agent nft list" as_agent nft list ruleset
blocked "agent apt-get" as_agent apt-get install -y htop
for tool in git ssh curl jq rg unzip; do allowed "toolchain $tool" command -v "$tool"; done

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

others=$(ss -Hlntu | awk '{print $5}' | grep -v -E ':(3128|68)$' || true)
[ -z "$others" ] || fail "unexpected listeners: $others"

[ "$failures" -eq 0 ]
`

func TestInitProvisionsTheSandbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	t.Cleanup(cancel)
	client := orbstack.New(sandbox.NamePrefix)
	dir := projectDir(t)
	name := sandbox.MachineName(dir)
	if exists, err := client.Exists(ctx, name); err != nil || exists {
		t.Fatalf("Exists(%q) before init = %v, %v, want false", name, exists, err)
	}
	t.Cleanup(func() {
		if err := client.Delete(context.WithoutCancel(ctx), name); err != nil {
			t.Errorf("Delete(%q) = %v", name, err)
		}
	})
	handler := initHandler(client, claudeRegistry(t))

	first, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || !first.Created {
		t.Fatalf("first Handle = %+v, %v, want a created sandbox", first, err)
	}
	second, err := handler.Handle(ctx, initialize.Input{Dir: dir})
	if err != nil || second.Created {
		t.Fatalf("second Handle = %+v, %v, want the existing sandbox provisioned again", second, err)
	}

	port := macPortReachableByRoot(ctx, t, client, name)
	script := fmt.Sprintf(probeScript, os.Getuid(), port)
	if err := client.RunAsRoot(ctx, name, []byte(script)); err != nil {
		t.Errorf("probes in %s:\n%v", name, err)
	}
	checkProxyClosedFromTheMac(t, name)
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
