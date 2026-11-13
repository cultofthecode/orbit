# Releasing Orbit

The Maintainer (release manager, `SDLC.md` Roles) cuts every release. A release is a signed, annotated tag `vX.Y.Z` on a merge commit of `main`. The tag starts `.github/workflows/release.yml`, which refuses a wrong tag before it publishes anything. Spec: `.ai/specs/2026-11-11-homebrew-distribution.md` (M3, Q7–Q9, Q13, Q14, Q16).

A release holds one archive, `orbit_X.Y.Z_darwin_arm64.tar.gz` (Apple Silicon only), `checksums.txt`, and a build provenance attestation. The archive is unsigned and not notarized; the documented install is Homebrew.

## Checklist

1. **Changelog PR merged.** Run `om-auto-update-changelog --version X.Y.Z`. The entry's heading is `# X.Y.Z (YYYY-MM-DD)`: the workflow publishes the text under it as the release notes, and fails when it is missing. The same PR adds the transcript of step 2 as `.ai/qa/releases/vX.Y.Z.md`, and the entry links it.
2. **Integration transcript.** On the exact commit to tag, on your Mac: `go test -tags integration -p 1 -timeout 60m ./...` and the escape suite `go test -tags integration -p 1 -timeout 60m -run '^TestEscape' -v ./cmd/orbit/`. Record the output and the OrbStack version (`orb version`). CI runs no integration tests.
3. **Create and check the tag locally.** On the commit of `main` that holds the merged changelog PR: `git fetch origin && git tag -s -m "orbit X.Y.Z" vX.Y.Z`, with `gpg.format=ssh` and `user.signingkey` set to your signing key. Your public key must be listed in `.github/allowed_signers`. Then `git -c gpg.ssh.allowedSignersFile=.github/allowed_signers verify-tag vX.Y.Z` must report a good signature.
4. **Push the tag over SSH:** `git push origin vX.Y.Z`. Never push `--tags`.
5. **Watch the release run** (`gh run watch`). The `check` job refuses a tag that is lightweight, unsigned, signed by another key, not SemVer, a signed tag object pushed under another name, moved after the push, off `main`, or on a commit whose `ci` run is not green. GoReleaser releases the pushed tag even when a pre-release tag points to the same commit. Then `release` waits for the approval of the Maintainer on the `release` environment (deployable only from `v*` tags; the Maintainer can approve a run they started), builds with GoReleaser as a draft release, attests the archive, and only then publishes the release. A re-run replaces a stale draft of the same tag (`replace_existing_draft`), so the publish step always publishes the draft of the attested run. Only repository admins can create `v*` tags (tag ruleset). While the release is a draft an admin could still move or delete the tag; once published, the immutable release locks it.
6. **Verify:** `gh attestation verify orbit_X.Y.Z_darwin_arm64.tar.gz --repo cultofthecode/orbit --signer-workflow cultofthecode/orbit/.github/workflows/release.yml --source-ref refs/tags/vX.Y.Z --format json`. Read the commit from `.[0].verificationResult.signature.certificate.sourceRepositoryDigest` (the source digest of the certificate), check that it is the commit you tagged, and run `git merge-base --is-ancestor <sha> origin/main`. Compare the archive with `checksums.txt` (`shasum -a 256 -c`).
7. **Merge the tap PR.** For a stable tag the `tap` job opens a PR on `cultofthecode/homebrew-tap` that changes `url` and `sha256` of `Formula/orbit.rb` only, to the source tarball of the tag (pre-release tags open none). Merge it with a merge commit after the tap CI is green. When the secret `TAP_GITHUB_TOKEN` is empty, the job logs that it skips the update; then edit the formula by hand.

## Rules

- Never move, delete, or force-push a tag. A bad release is fixed with the next patch version. A refused tag stays; tag the right commit with the next version.
- Raise the `go` line of `go.mod` only to a version that Homebrew's `go` formula ships, or the source-built formula cannot compile Orbit.
- Do not run `goreleaser` with a release token on your Mac. A local dry run is `goreleaser check` and `goreleaser release --snapshot --clean`; it publishes nothing.
- `GORELEASER_VERSION` and every action in `release.yml` are pinned. Change them only in a reviewed PR.

## Owner setup before the first release

- Put the public signing key into `.github/allowed_signers` (until then the check refuses every tag).
- On GitHub Free a private repository has neither the `release` environment nor artifact attestations; the repository is public, so both are available. The `release` environment is limited to `v*` tags with the Maintainer as required reviewer and self-review allowed, so the Maintainer can approve a release run they started; immutable releases are enabled.
- `.github/scripts/check-release-tag_test.sh` runs the tag check against a scratch repository; run it after you change the script.
- Tap updates (E06-S05): create a fine-grained personal access token limited to the repository `cultofthecode/homebrew-tap` with the permissions Contents and Pull requests set to write. Store it as the secret `TAP_GITHUB_TOKEN` of the `release` environment. Never write the token value in the repository, an issue, or a PR. `.github/scripts/bump-tap-formula_test.sh` tests the formula edit; run it after you change `bump-tap-formula.sh`.
