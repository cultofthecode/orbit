# Releasing Orbit

The Maintainer (release manager, `SDLC.md` Roles) cuts every release. A release is a signed, annotated tag `vX.Y.Z` on a merge commit of `main`. The tag starts `.github/workflows/release.yml`, which refuses a wrong tag before it publishes anything. Spec: `.ai/specs/2026-11-11-homebrew-distribution.md` (M3, Q7–Q9, Q13, Q14, Q16).

A release holds one archive, `orbit_X.Y.Z_darwin_arm64.tar.gz` (Apple Silicon only), `checksums.txt`, and a build provenance attestation. The archive is unsigned and not notarized; the documented install is Homebrew.

## Checklist

1. **Changelog PR merged.** Run `om-auto-update-changelog --version X.Y.Z`. The entry's heading is `# X.Y.Z (YYYY-MM-DD)`: the workflow publishes the text under it as the release notes, and fails when it is missing. The same PR adds the transcript of step 2 as `.ai/qa/releases/vX.Y.Z.md`, and the entry links it.
2. **Integration transcript.** On the exact commit to tag, on your Mac: `go test -tags integration -p 1 -timeout 60m ./...` and the escape suite `go test -tags integration -p 1 -timeout 60m -run '^TestEscape' -v ./cmd/orbit/`. Record the output and the OrbStack version (`orb version`). CI runs no integration tests.
3. **Check the tag locally.** On the commit of `main` that holds the merged changelog PR: `git fetch origin && git tag -s -m "orbit X.Y.Z" vX.Y.Z`, with `gpg.format=ssh` and `user.signingkey` set to your signing key. Your public key must be listed in `.github/allowed_signers`.
4. **Push the tag over SSH:** `git push origin vX.Y.Z`. Never push `--tags`.
5. **Watch the release run** (`gh run watch`). The `check` job refuses a tag that is lightweight, unsigned, signed by another key, not SemVer, off `main`, or on a commit whose `ci` run is not green. Then `release` builds with GoReleaser and attests the archive.
6. **Verify:** `gh attestation verify orbit_X.Y.Z_darwin_arm64.tar.gz --repo cultofthecode/orbit`, and compare the archive with `checksums.txt` (`shasum -a 256 -c`).
7. **Merge the tap PR** once the tap exists (E06-S04 and E06-S05).

## Rules

- Never move, delete, or force-push a tag. A bad release is fixed with the next patch version. A refused tag stays; tag the right commit with the next version.
- Raise the `go` line of `go.mod` only to a version that Homebrew's `go` formula ships, or the source-built formula cannot compile Orbit.
- Do not run `goreleaser` with a release token on your Mac. A local dry run is `goreleaser check` and `goreleaser release --snapshot --clean`; it publishes nothing.
- `GORELEASER_VERSION` and every action in `release.yml` are pinned. Change them only in a reviewed PR.

## Owner setup before the first release

- Put the public signing key into `.github/allowed_signers` (until then the check refuses every tag).
- Make the repository public (E06-T03). On GitHub Free a private repository has neither the `release` environment nor artifact attestations, so the workflow first runs for real after that. Create the `release` environment limited to `v*` tags.
- `.github/scripts/check-release-tag_test.sh` runs the tag check against a scratch repository; run it after you change the script.
