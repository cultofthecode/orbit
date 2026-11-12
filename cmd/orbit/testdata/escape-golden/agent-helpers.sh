
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
