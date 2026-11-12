
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
blocked() { local what=$1; shift; if "$@" >/dev/null 2>&1; then fail "$what succeeded"; fi; }
allowed() { local what=$1; shift; "$@" >/dev/null 2>&1 || fail "$what failed"; }
finish() { [ "$failures" -eq 0 ]; }
wait_docker() { for _ in $(seq 90); do docker info >/dev/null 2>&1 && return 0; sleep 1; done; fail "the daemon did not answer within 90 s"; return 1; }
IMG=orbit-it-curl
direct() { docker run --rm "$@" $IMG curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1; }
proxied() { docker run --rm "$@" $IMG curl -sS -m 20 -o /dev/null -w '%{http_code}' https://github.com; }
no_direct() { local what=$1; shift; if direct "$@" >/dev/null 2>&1; then fail "direct connection from $what succeeded"; fi; }

HOST_CONTAINER='172.17.0.2'
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
  if docker run --rm $IMG curl -sS --noproxy '*' -m 5 "http://$HOST_CONTAINER:8080" >/dev/null 2>&1; then fail "a container reached the container of the host engine"; fi
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
