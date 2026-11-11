# Capability: sandbox-shell

The owner can open an interactive login shell as the unprivileged agent user in the running, provisioned sandbox of a project directory, and start the agent there.

## Requirement: Shell as the agent user in the project directory
The system SHALL open, for `orbit shell` in a project directory with a running, provisioned sandbox, an interactive login shell as the user `agent` in that sandbox, with the project directory as its working directory and `~/.local/bin` on `PATH` when it exists.

### Scenario: Shell in an initialized project
- WHEN the owner runs `orbit shell` in a directory where `orbit init --agent claude-code` ran
- THEN a login shell runs as `agent`, `pwd` prints the project directory, and `command -v claude` prints `/home/agent/.local/bin/claude`

### Scenario: Worktree at the same path
- WHEN the agent runs `git worktree add wt-a` in the shell
- THEN `<project>/wt-a` exists on the host, and `git worktree list` prints the same absolute path on the host and in the sandbox (D18)

### Scenario: Exit status
- WHEN the shell exits with status 3
- THEN `orbit shell` exits with status 3 and prints nothing more

Source: .ai/specs/2026-11-11-orbit-shell.md

## Requirement: The shell is not root
The system SHALL run the shell as `agent` only, with no sudo rule, no working `su`, no capability, and no privileged group.

### Scenario: Escalation attempts
- WHEN the shell runs `sudo -n true`, `/usr/bin/sudo.ws -n true`, `/usr/lib/cargo/bin/sudo -n true`, and `su -c id root </dev/null`
- THEN each fails, `id -u` is at least 1000, and `CapEff` is 0

Source: .ai/specs/2026-11-11-orbit-shell.md

## Requirement: Refuse a sandbox that is missing, stopped, mismounted, or not ready
The system SHALL refuse to open the shell, exit non-zero, and change no machine when the sandbox of the current directory does not exist, is not running, does not mount exactly the project directory, or fails the readiness check.

### Scenario: No sandbox
- WHEN the owner runs `orbit shell` in a directory with no sandbox, or in a subdirectory of a project
- THEN Orbit exits 1, names `orbit init`, and runs no `orb` command against any machine

### Scenario: Stopped sandbox
- WHEN the sandbox of the directory is stopped
- THEN Orbit exits 1, names `orbit start`, and the machine stays stopped

### Scenario: Not provisioned
- WHEN `/etc/orbit/provisioned` is missing, holds another version, or a boundary unit is not active
- THEN Orbit exits 1, names `orbit init`, and opens no shell

### Scenario: Mount mismatch
- WHEN the machine mounts another directory than the project directory
- THEN Orbit exits 1 with `ErrMountMismatch` and opens no shell

Source: .ai/specs/2026-11-11-orbit-shell.md
