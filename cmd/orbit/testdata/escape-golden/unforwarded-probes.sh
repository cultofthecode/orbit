
set -u
failures=0
fail() { echo "PROBE FAILED: $*" >&2; failures=$((failures + 1)); }
as_agent() { runuser -u agent -- "$@"; }

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
