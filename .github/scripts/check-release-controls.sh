#!/usr/bin/env bash
# Fails when a release control disappears from release.yml or .goreleaser.yaml.
set -euo pipefail

cd "$(dirname "$0")/../.."
wf=.github/workflows/release.yml
fail=0

need() {
  if ! grep -Eq -- "$2" "$1"; then
    echo "missing in $1: $3" >&2
    fail=1
  fi
}

job_body() {
  awk -v job="$1" '
    /^[^[:space:]#]/ { top = ($0 ~ /^jobs:/) }
    top && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { cur = $1; sub(/:$/, "", cur); next }
    top && cur == job { print }
  ' "$wf"
}

check_job=$(job_body check)
release_job=$(job_body release)

in_job() {
  if ! printf '%s\n' "$1" | grep -Eq -- "$2"; then
    echo "missing in $wf: $3" >&2
    fail=1
  fi
}

in_job "$release_job" '^    environment: release$' "environment: release on the release job"
in_job "$release_job" '^    needs: check$' "needs: check on the release job"
in_job "$release_job" '^      contents: write$' "contents: write on the release job"
in_job "$check_job" '^      contents: read$' "contents: read on the check job"
if printf '%s\n' "$check_job" | grep -Eq '^      (contents: write|id-token:|attestations:)'; then
  echo "the check job must not hold write or id-token permissions in $wf" >&2
  fail=1
fi
in_job "$check_job" '^[[:space:]]+run: git fetch .*\+refs/heads/main:refs/remotes/origin/main' "fetch of main in the check job"
need "$wf" '^concurrency:' "concurrency block"
need "$wf" 'cancel-in-progress: false' "concurrency without cancellation"
need .goreleaser.yaml '^  draft: true$' "release.draft: true"
need .goreleaser.yaml '^  replace_existing_draft: true$' "release.replace_existing_draft: true"

attest=$(printf '%s\n' "$release_job" | grep -n 'actions/attest-build-provenance@' | head -1 | cut -d: -f1 || true)
publish=$(printf '%s\n' "$release_job" | grep -n -- '--draft=false' | head -1 | cut -d: -f1 || true)
if [ -z "$attest" ] || [ -z "$publish" ] || [ "$attest" -ge "$publish" ]; then
  echo "the publish step must come after the attestation step in $wf" >&2
  fail=1
fi

exit "$fail"
