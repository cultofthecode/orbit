#!/usr/bin/env bash
# Exercises check-release-tag.sh in a scratch repository: it must refuse a
# lightweight tag, an unsigned tag, a tag signed by a key that is not allowed,
# a signed tag object pushed under another name, v1.2, a tag off main, a tag
# that is not the one the run started for, and a tag whose ci run is not
# green, and accept a signed annotated tag on main. Needs git, ssh-keygen, and bash; no network.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/check-release-tag.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"

ssh-keygen -q -t ed25519 -N '' -C test -f good
ssh-keygen -q -t ed25519 -N '' -C test -f bad
echo "maintainer namespaces=\"git\" $(cat good.pub)" >allowed_signers

mkdir bin
cat >bin/gh <<'GH'
#!/usr/bin/env bash
echo "${FAKE_CI:-success}"
GH
chmod +x bin/gh
export PATH="$work/bin:$PATH" GITHUB_REPOSITORY=o/r ALLOWED_SIGNERS="$work/allowed_signers"

git init -q -b main repo
cd repo
git config user.email t@example.com
git config user.name t
git config commit.gpgsign false
git commit -q --allow-empty -m one
git update-ref refs/remotes/origin/main HEAD
main_commit="$(git rev-parse HEAD)"
export GITHUB_SHA="$main_commit"
git checkout -q -b side
git commit -q --allow-empty -m side

signed_tag() { git -c gpg.format=ssh -c "user.signingkey=$work/$2" tag -s -m release "$1" "$3"; }
failures=0

expect() {
  local want="$1" name="$2" tag="$3"
  if "$script" "$tag" >/dev/null 2>&1; then got=accept; else got=refuse; fi
  if [ "$got" = "$want" ]; then echo "ok   $name"; else echo "FAIL $name: wanted $want, got $got"; failures=$((failures + 1)); fi
}

git tag v0.1.0 "$main_commit"
git tag -a -m unsigned v0.2.0 "$main_commit"
signed_tag v0.3.0 bad "$main_commit"
signed_tag v1.2 good "$main_commit"
signed_tag release-1 good "$main_commit"
signed_tag v0.4.0 good side
signed_tag v0.5.0 good "$main_commit"
signed_tag v0.6.0-rc.1 good "$main_commit"
signed_tag v01.0.0 good "$main_commit"
git update-ref refs/tags/v1.0.0 "$(git rev-parse refs/tags/v0.6.0-rc.1)"
git checkout -q main
git commit -q --allow-empty -m two
git update-ref refs/remotes/origin/main HEAD
signed_tag v0.7.0 good HEAD

expect refuse "lightweight tag" v0.1.0
expect refuse "unsigned annotated tag" v0.2.0
expect refuse "tag signed by a key not in the file" v0.3.0
expect refuse "v1.2" v1.2
expect refuse "release-1" release-1
expect refuse "v01.0.0 (leading zero)" v01.0.0
expect refuse "tag off main" v0.4.0
expect refuse "missing tag" v9.9.9
expect refuse "signed rc tag object pushed as v1.0.0" v1.0.0
expect refuse "tag moved after the run started" v0.7.0
GITHUB_SHA= expect refuse "GITHUB_SHA unset" v0.5.0
GITHUB_SHA="$(git rev-parse refs/tags/v0.5.0)" expect accept "GITHUB_SHA is the tag object" v0.5.0
expect accept "signed tag on main" v0.5.0
expect accept "signed prerelease tag on main" v0.6.0-rc.1
FAKE_CI=failure expect refuse "ci failed" v0.5.0
FAKE_CI=unfinished expect refuse "ci unfinished" v0.5.0
FAKE_CI=none expect refuse "no ci run" v0.5.0
ALLOWED_SIGNERS="$work/none" expect refuse "no allowed signers file" v0.5.0
printf '# only a comment\n' >"$work/empty"
ALLOWED_SIGNERS="$work/empty" expect refuse "allowed signers lists no key" v0.5.0

[ "$failures" -eq 0 ]
