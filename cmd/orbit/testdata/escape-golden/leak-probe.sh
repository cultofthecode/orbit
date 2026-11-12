
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
