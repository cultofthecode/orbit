# orbit

Orbit is a Go CLI that runs coding agents in OrbStack sandboxes on macOS.

## Requirements

- A Mac with Apple Silicon (Intel Macs are not supported).
- [OrbStack](https://orbstack.dev).

## Install

Orbit is distributed through a Homebrew tap, from tagged GitHub Releases. The tap is not published yet; until it is, build from source with `go build ./cmd/orbit`. Release archives (`orbit_X.Y.Z_darwin_arm64.tar.gz`) are unsigned and not notarized; each has a checksum in `checksums.txt` and a build provenance attestation (`gh attestation verify <archive> --repo cultofthecode/orbit`). The release process is in [`RELEASING.md`](RELEASING.md).

## Not affiliated with Anthropic

Orbit is not affiliated with Anthropic. It installs the official, unmodified Claude Code client in the sandbox, and each user signs in with their own account.

See `AGENTS.md` for the project overview and `ARCHITECTURE.md` for the system structure.

## SSH agent forwarding

`orbit init` forwards the SSH agent of your Mac into the sandbox by default, so the coding agent can use Git over SSH. The sandbox can then use every key loaded in that agent, not only the one for the project, and it can also add, remove, lock, or unlock keys in it. Pass `--no-ssh-forwarding` to `orbit init` to leave the agent out. The Credentials row of [`ARCHITECTURE.md`](ARCHITECTURE.md) has the details and the recommendations for the host.
