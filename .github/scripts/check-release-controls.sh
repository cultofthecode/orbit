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

need "$wf" '^[[:space:]]+environment: release$' "environment: release"
need "$wf" '^concurrency:' "concurrency block"
need "$wf" 'cancel-in-progress: false' "concurrency without cancellation"
need "$wf" 'refs/remotes/origin/main' "fetch of main in the check job"
need .goreleaser.yaml '^  draft: true$' "release.draft: true"
need .goreleaser.yaml '^  replace_existing_draft: true$' "release.replace_existing_draft: true"

attest=$(grep -n 'actions/attest-build-provenance@' "$wf" | head -1 | cut -d: -f1 || true)
publish=$(grep -n -- '--draft=false' "$wf" | head -1 | cut -d: -f1 || true)
if [ -z "$attest" ] || [ -z "$publish" ] || [ "$attest" -ge "$publish" ]; then
  echo "the publish step must come after the attestation step in $wf" >&2
  fail=1
fi

exit "$fail"
