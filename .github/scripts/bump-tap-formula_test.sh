#!/usr/bin/env bash
# Exercises bump-tap-formula.sh on a scratch formula and a local tarball: it
# must change url and sha256 only, leave an up-to-date formula untouched, and
# refuse a pre-release tag, a non-SemVer tag, and a formula without exactly one
# url and one sha256 line. Needs bash and shasum; no network.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/bump-tap-formula.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"

fail() { echo "FAIL: $*" >&2; exit 1; }

printf 'source of v0.2.0\n' >src.tar.gz
want="$(shasum -a 256 src.tar.gz | cut -d ' ' -f 1)"
export ORBIT_TARBALL="$work/src.tar.gz"

cat >orbit.rb <<'RB'
class Orbit < Formula
  desc "Run coding agents in sandboxes on OrbStack"
  url "https://github.com/cultofthecode/orbit/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  head "https://github.com/cultofthecode/orbit.git", branch: "main"
end
RB
cp orbit.rb before.rb

"$script" v0.2.0 orbit.rb >/dev/null
[ "$(diff before.rb orbit.rb | grep -c '^>')" = 2 ] || fail "more or fewer than two lines changed"
grep -q "refs/tags/v0.2.0.tar.gz\"" orbit.rb || fail "url not updated"
grep -q "sha256 \"$want\"" orbit.rb || fail "sha256 not updated"
grep -q 'head "https://github.com/cultofthecode/orbit.git", branch: "main"' orbit.rb || fail "head line touched"

cp orbit.rb again.rb
"$script" v0.2.0 orbit.rb >/dev/null
cmp -s again.rb orbit.rb || fail "an up-to-date formula changed"

for bad in v0.3.0-rc.1 v1.2 release-1 "v1.2.3;x"; do
  if "$script" "$bad" orbit.rb >/dev/null 2>&1; then fail "accepted $bad"; fi
  cmp -s again.rb orbit.rb || fail "$bad changed the formula"
done

grep -v '^  sha256' orbit.rb >nosha.rb
if "$script" v0.2.0 nosha.rb >/dev/null 2>&1; then fail "accepted a formula without sha256"; fi

echo "ok"
