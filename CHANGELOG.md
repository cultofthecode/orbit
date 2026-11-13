# Changelog

Release entries are written by `om-auto-update-changelog` on a docs PR before each tag, newest first, under a heading `# X.Y.Z (YYYY-MM-DD)`. The release workflow publishes the entry of the tag as the release notes. See [`RELEASING.md`](RELEASING.md).

# 0.1.0 (2026-11-14)

## Added

- Orbit CLI with the commands `orbit init`, `orbit shell`, `orbit stop`, `orbit start`, `orbit destroy`, and `orbit ls`.
- One sandbox per project directory. The sandbox shares only that directory.
- Sandboxes run Ubuntu 26.04 by default. Use `--distro debian` for Debian 13 trixie.
- Claude Code runs in the sandbox as the agent. It uses your own subscription.
- The agent runs as an unprivileged user with rootless Docker.
- Root sets up the firewall and the egress proxy inside the sandbox.
- The host SSH agent is forwarded to the sandbox. Use `--no-ssh-forwarding` to turn it off.
- Git over SSH works from the sandbox through the proxy.
- `orbit shell` opens a login shell as the agent user.
- `orbit stop` and `orbit start` pause and resume a sandbox.
- `orbit ls` lists every sandbox with its state and project directory.
- `orbit destroy` deletes the sandbox of the current directory after a confirmation.
- `orbit --version` prints the version and the commit.
- Orbit names OrbStack and shows how to install it when `orb` is missing.
- Release archive for Apple Silicon Macs, installed with Homebrew.
- Apache License 2.0.

## Security

- `orbit init` refuses a project directory that would expose host credential files to the sandbox.
- `orbit init` refuses a project directory with links that reach protected host paths.
- Orbit gives OrbStack only an allowlist of host environment variables.
- The agent user has no sudo. Only root can change the firewall and the proxy.
- Egress stays closed when the firewall service stops.
- A release starts only from a signed tag on `main`.

## Changed

- `orbit init` refuses a stopped sandbox. Only `orbit start` starts a machine.
- Error messages name the path and the cause.
- Orbit refuses every OrbStack command while OrbStack is not running.
