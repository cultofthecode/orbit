#!/usr/bin/env bash
# Refuses a release tag before anything is published.
#
# Usage: check-release-tag.sh <tag>
#
# Checks, in order: SemVer name, annotated tag whose own name is the ref name
# (a signed tag object pushed under another name is a replay), signature
# accepted against the allowed signers file, tagged commit is an ancestor of
# origin/main, tag is the one the run started for, and the ci workflow
# concluded success for that commit. Any failure exits non-zero.
#
# Environment:
#   GITHUB_REPOSITORY  owner/name for the CI lookup (required)
#   GITHUB_SHA         commit (or tag object) the run started for (required);
#                      the release job builds this commit
#   ALLOWED_SIGNERS    path of the allowed signers file (default: .github/allowed_signers)
#   MAIN_REF           ref that releases must come from (default: origin/main)
#   GH_TOKEN           token for `gh api` (needs actions: read)
set -euo pipefail

fail() {
  echo "check-release-tag: refused: $*" >&2
  exit 1
}

tag="${1:-}"
[ -n "$tag" ] || fail "usage: check-release-tag.sh <tag>"
allowed_signers="${ALLOWED_SIGNERS:-.github/allowed_signers}"
main_ref="${MAIN_REF:-origin/main}"
repo="${GITHUB_REPOSITORY:-}"
run_sha="${GITHUB_SHA:-}"

num='(0|[1-9][0-9]*)'
pre='([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*|0|[1-9][0-9]*)'
semver="^v${num}\\.${num}\\.${num}(-${pre}(\\.${pre})*)?\$"

check_semver() {
  [[ "$tag" =~ $semver ]] || fail "tag '$tag' is not vMAJOR.MINOR.PATCH[-PRERELEASE]"
}

check_annotated() {
  local kind
  kind="$(git cat-file -t "refs/tags/$tag" 2>/dev/null)" || fail "tag '$tag' does not exist"
  [ "$kind" = "tag" ] || fail "tag '$tag' is lightweight; it must be an annotated, signed tag"
}

check_name() {
  local name
  name="$(git for-each-ref --format='%(tag)' "refs/tags/$tag")"
  [ "$name" = "$tag" ] || fail "tag object is named '$name', not '$tag'; a signed tag must not be pushed under another name"
}

check_signature() {
  [ -f "$allowed_signers" ] || fail "allowed signers file '$allowed_signers' is missing"
  grep -Eqv '^[[:space:]]*(#|$)' "$allowed_signers" || fail "allowed signers file lists no key"
  git -c gpg.format=ssh -c "gpg.ssh.allowedSignersFile=$allowed_signers" \
    verify-tag "refs/tags/$tag" >/dev/null 2>&1 \
    || fail "signature of tag '$tag' is missing, invalid, or not by an allowed signer"
}

tagged_commit() {
  git rev-parse --verify "refs/tags/$tag^{commit}" 2>/dev/null || fail "tag '$tag' does not point to a commit"
}

check_on_main() {
  git rev-parse --verify --quiet "$main_ref^{commit}" >/dev/null || fail "ref '$main_ref' not found"
  git merge-base --is-ancestor "$1" "$main_ref" || fail "commit $1 is not an ancestor of $main_ref"
}

check_run_sha() {
  [ -n "$run_sha" ] || fail "GITHUB_SHA is not set"
  [ "$1" = "$run_sha" ] || [ "$(git rev-parse "refs/tags/$tag")" = "$run_sha" ] \
    || fail "tag '$tag' points to $1, but the run started for $run_sha; the tag moved"
}

check_ci() {
  [ -n "$repo" ] || fail "GITHUB_REPOSITORY is not set"
  local conclusion
  conclusion="$(gh api "repos/$repo/actions/workflows/ci.yml/runs?head_sha=$1&event=push&branch=main&per_page=100" \
    --jq '[.workflow_runs[]] | sort_by(.created_at) | last | if . == null then "none" elif .status != "completed" then "unfinished" else .conclusion end')" \
    || fail "could not read the ci runs of commit $1"
  [ "$conclusion" = "success" ] || fail "ci for commit $1 is '$conclusion', not 'success'"
}

main() {
  check_semver
  check_annotated
  check_name
  check_signature
  local commit
  commit="$(tagged_commit)"
  check_on_main "$commit"
  check_run_sha "$commit"
  check_ci "$commit"
  echo "check-release-tag: tag $tag on $commit accepted"
}

main
