package main

// The shell fragments that the probe scripts of the capability tests and the
// escape suite share. TestProbeScriptsMatchGolden fails when a fragment
// changes a rendered script.

// failCounter starts a probe script: it counts the failed checks.
const failCounter = `set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
`

// blockedAllowed holds the helpers that expect a command to fail or to
// succeed.
const blockedAllowed = `blocked() { local what=$1; shift; if "$@" >/dev/null 2>&1; then fail "$what succeeded"; fi; }
allowed() { local what=$1; shift; "$@" >/dev/null 2>&1 || fail "$what failed"; }
`

// asAgent runs a command as the agent from a root script.
const asAgent = `as_agent() { runuser -u agent -- "$@"; }
`

// refusedByProxy expects a request through the proxy to fail with a 403.
const refusedByProxy = `refused_by_proxy() { local what=$1; shift; local out; out=$("$@" 2>&1) && fail "$what succeeded"; case "$out" in *403*) ;; *) fail "$what failed without a 403: $out";; esac; }
`

// containerHelpers holds direct, proxied, and no_direct: the connections of
// a container of the image orbit-it-curl, with and without the proxy.
const containerHelpers = `IMG=orbit-it-curl
direct() { docker run --rm "$@" $IMG curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1; }
proxied() { docker run --rm "$@" $IMG curl -sS -m 20 -o /dev/null -w '%{http_code}' https://github.com; }
no_direct() { local what=$1; shift; if direct "$@" >/dev/null 2>&1; then fail "direct connection from $what succeeded"; fi; }
`

// dnsProbe holds the checks that the agent cannot send DNS, UDP, or ICMP,
// nor use IPv6, nor resolve a name.
const dnsProbe = `blocked "agent DNS to OrbStack DNS" as_agent timeout 5 bash -c 'exec 3<>/dev/udp/0.250.250.200/53 && echo x >&3'
blocked "agent UDP" as_agent timeout 5 bash -c 'echo x > /dev/udp/1.1.1.1/443'
blocked "agent ping" as_agent ping -c1 -W2 1.1.1.1
blocked "agent ping IPv6" as_agent ping -6 -c1 -W2 2606:4700:4700::1111
blocked "agent TCP over IPv6" as_agent timeout 5 bash -c 'exec 3<>/dev/tcp/2606:4700:4700::1111/443'
blocked "agent UDP over IPv6" as_agent timeout 5 bash -c 'echo x > /dev/udp/2606:4700:4700::1111/53'
blocked "agent resolvectl" as_agent resolvectl query github.com
blocked "agent getent hosts" as_agent getent hosts github.com
`

// engineSockets holds the checks that the agent sees no host engine socket
// and reaches no engine over any unix socket but its own.
const engineSockets = `for socket in /var/run/docker.sock /run/docker.sock; do blocked "agent sees $socket" as_agent test -e "$socket"; done
for socket in $(find / \( -path /proc -o -path /sys \) -prune -o -type s -print 2>/dev/null); do
  [ "$socket" != "/run/user/$(id -u agent)/docker.sock" ] || continue
  [ "$(as_agent curl -s -m 2 --unix-socket "$socket" http://engine/_ping 2>/dev/null)" != OK ] || fail "agent reached an engine on $socket"
done
`
