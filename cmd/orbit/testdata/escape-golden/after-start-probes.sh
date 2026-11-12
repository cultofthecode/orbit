
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
as_agent() { runuser -u agent -- "$@"; }
connect_code() { as_agent curl -s -m 15 -x http://127.0.0.1:3128 -o /dev/null -w '%{http_connect}' "https://$1/" || true; }
tunnel() { local code; code=$(connect_code "$1"); [ "$code" = 200 ] || fail "CONNECT $1:443 got $code, want a tunnel"; }
refused() { local code; code=$(connect_code "$1"); [ "$code" = 403 ] || fail "CONNECT $1:443 got $code, want 403"; }
claude=/home/agent/.local/bin/claude

content=$(as_agent cat /home/agent/orbit-it-state) || fail "/home/agent/orbit-it-state is gone"
[ "$content" = 'written before the stop' ] || fail "/home/agent/orbit-it-state holds $content"
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
late=0
: > /run/orbit-it-boot-processes
chmod 0644 /run/orbit-it-boot-processes
for dir in /proc/[0-9]*; do
  uid=$(awk '/^Uid:/ {print $2}' "$dir/status" 2>/dev/null) || continue
  [ -n "$uid" ] && [ "$uid" -ge 1000 ] || continue
  unit=$(awk -F: '$1 == "0" {print $3}' "$dir/cgroup" 2>/dev/null | tr / '\n' | grep -m1 -E '[.](service|scope)$') || unit=""
  agents=$((agents + 1))
  described="process ${dir#/proc/} uid $uid ppid $(awk '/^PPid:/ {print $2}' "$dir/status") ($(tr '\0' ' ' < "$dir/cmdline" | cut -c1-80))"
  if [ -z "$unit" ]; then
    late=$((late + 1))
    echo "$described in no unit" >> /run/orbit-it-boot-processes
    fail "process ${dir#/proc/} with uid $uid is in no unit"
    continue
  fi
  started=$(systemctl show -p InactiveExitTimestampMonotonic --value "$unit")
  echo "$described in $unit, which started at $started us" >> /run/orbit-it-boot-processes
  [ "${started:-0}" -gt "$latest" ] || { late=$((late + 1)); fail "process ${dir#/proc/} with uid $uid runs in $unit, which started at $started us, before the boundary was active at $latest us"; }
done
if [ "$late" -eq 0 ]; then
  echo "boundary active at $latest us; $agents processes with a uid from 1000 up, all started later" >> /run/orbit-it-boot-processes
else
  echo "boundary active at $latest us; $agents processes with a uid from 1000 up, $late not started after the boundary or in no unit" >> /run/orbit-it-boot-processes
fi
{ loginctl list-users --no-legend; systemctl list-units --type=service,scope --state=active --no-legend 'user@*' 'session-*' 2>/dev/null; } >> /run/orbit-it-boot-processes 2>&1 || true
[ "$failures" -eq 0 ]
