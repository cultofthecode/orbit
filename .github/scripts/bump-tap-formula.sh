#!/usr/bin/env bash
# Sets url and sha256 of a Homebrew formula to the source tarball of a stable
# Orbit tag, and nothing else. Prints the diff. Leaves the file untouched when
# it already names that tag. Usage: bump-tap-formula.sh vX.Y.Z FORMULA_FILE
# ORBIT_TARBALL=<file> uses a local tarball instead of downloading the tag's.
# Spec: .ai/specs/2026-11-11-homebrew-distribution.md (M6).
set -euo pipefail

tag="${1:?usage: bump-tap-formula.sh vX.Y.Z FORMULA_FILE}"
formula="${2:?usage: bump-tap-formula.sh vX.Y.Z FORMULA_FILE}"

if ! [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "refusing '$tag': only a stable vMAJOR.MINOR.PATCH tag updates the tap" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

url="https://github.com/cultofthecode/orbit/archive/refs/tags/${tag}.tar.gz"
tarball="${ORBIT_TARBALL:-}"
if [ -z "$tarball" ]; then
  tarball="$work/source.tar.gz"
  curl --fail --silent --show-error --location --retry 3 --output "$tarball" "$url"
fi
sha="$(shasum -a 256 "$tarball" | cut -d ' ' -f 1)"

awk -v url="$url" -v sha="$sha" '
  /^  url "/    { nurl++;  print "  url \"" url "\""; next }
  /^  sha256 "/ { nsha++;  print "  sha256 \"" sha "\""; next }
  { print }
  END { if (nurl != 1 || nsha != 1) exit 1 }
' "$formula" >"$work/formula.rb" || {
  echo "$formula must hold exactly one url line and one sha256 line" >&2
  exit 1
}

changed="$(diff "$formula" "$work/formula.rb" | grep -c '^>' || true)"
diff -u "$formula" "$work/formula.rb" || true
if [ "$changed" -gt 2 ]; then
  echo "refusing: more than url and sha256 would change" >&2
  exit 1
fi
cp "$work/formula.rb" "$formula"
