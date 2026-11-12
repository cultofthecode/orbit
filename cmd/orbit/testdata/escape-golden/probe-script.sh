
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
blocked() { local what=$1; shift; if "$@" >/dev/null 2>&1; then fail "$what succeeded"; fi; }
allowed() { local what=$1; shift; "$@" >/dev/null 2>&1 || fail "$what failed"; }
as_agent() { runuser -u agent -- "$@"; }
as_uid() { local uid=$1; shift; setpriv --reuid "$uid" --regid "$uid" --clear-groups "$@"; }
direct() { curl -sSf --noproxy '*' -m 5 "$@"; }
refused_by_proxy() { local what=$1; shift; local out; out=$("$@" 2>&1) && fail "$what succeeded"; case "$out" in *403*) ;; *) fail "$what failed without a 403: $out";; esac; }
DEFAULT_UID=501
MAC_PORT=4242
HOST_CONTAINER=172.17.0.2
PROJECT_DIR='/Users/orbit/project'
ROOT_ONLY_FILES="/etc/nftables.conf /etc/tinyproxy/tinyproxy.conf"
PROVISION_VERSION=7
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

[ "$(as_agent cat "$PROJECT_DIR/written-on-the-host")" = "from the host" ] || fail "agent read in the project directory"
allowed "agent delete in the project directory" as_agent rm "$PROJECT_DIR/written-on-the-host"
allowed "agent write in the project directory" as_agent sh -c 'echo "$2" > "$1"' sh "$PROJECT_DIR/written-by-the-agent" "from the agent"
[ "$(as_agent cat "$PROJECT_DIR/written-by-the-agent")" = "from the agent" ] || fail "agent read its file in the project directory"

for socket in /var/run/docker.sock /run/docker.sock; do blocked "agent sees $socket" as_agent test -e "$socket"; done
for socket in $(find / \( -path /proc -o -path /sys \) -prune -o -type s -print 2>/dev/null); do
  [ "$socket" != "/run/user/$(id -u agent)/docker.sock" ] || continue
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
allowed "proxy user to port 80" as_uid tinyproxy curl -sS --noproxy '*' -m 5 -o /dev/null http://deb.debian.org
allowed "proxy user to port 443" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
blocked "proxy user to port 22" as_uid tinyproxy curl -sS --noproxy '*' -m 5 telnet://1.1.1.1:22
blocked "proxy user to port 8080" as_uid tinyproxy curl -sS --noproxy '*' -m 5 http://deb.debian.org:8080

if [ "$MAC_PORT" -gt 0 ]; then
  MAC="http://192.168.139.3:$MAC_PORT"
  blocked "agent to the Mac" as_agent curl -sSf --noproxy '*' -m 5 "$MAC"
  blocked "default uid to the Mac" as_uid "$DEFAULT_UID" curl -sSf --noproxy '*' -m 5 "$MAC"
  blocked "proxy user to the Mac" as_uid tinyproxy curl -sSf --noproxy '*' -m 5 "$MAC"
fi

V6=$(ip -6 -o addr show dev eth0 scope global | awk 'NR == 1 {split($4, a, "/"); print a[1]}')
if [ -n "$V6" ]; then
  python3 -c 'import socket, sys, time; s = socket.socket(socket.AF_INET6); s.bind((sys.argv[1], 8443)); s.listen(8); time.sleep(60)' "$V6" </dev/null >/dev/null 2>&1 &
  listener=$!
  for try in 1 2 3 4 5 6 7 8 9 10; do ss -Hltn 'sport = :8443' | grep -q . && break; sleep 0.5; done
  allowed "root TCP over IPv6 to the machine" timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/8443"' sh "$V6"
  blocked "agent TCP over IPv6 to the machine" as_agent timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/8443"' sh "$V6"
  blocked "agent UDP over IPv6 to the machine" as_agent timeout 5 bash -c 'echo x > "/dev/udp/$1/8443"' sh "$V6"
  kill "$listener"; wait "$listener" 2>/dev/null
else
  fail "the machine has no global IPv6 address"
fi

if [ -n "$HOST_CONTAINER" ]; then
  blocked "agent to the host-engine container" as_agent timeout 5 bash -c 'exec 3<>"/dev/tcp/$1/8080"' sh "$HOST_CONTAINER"
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
