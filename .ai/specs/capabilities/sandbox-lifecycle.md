# Capability: sandbox-lifecycle

The owner can stop the sandbox of a project directory and start it again, with the host-side checks of `orbit init` before every start and the boundary checked after it, and can destroy it after a confirmation.

## Requirement: Stop the sandbox of the project directory
The system SHALL stop, for `orbit stop` in a project directory with a running sandbox, the machine of that directory and no other machine, and keep the disk of the machine.

### Scenario: Stop a running sandbox
- WHEN the owner runs `orbit stop` in a directory where `orbit init --agent claude-code` ran and the sandbox runs
- THEN the state of the machine is `stopped`, Orbit prints `Stopped sandbox <name> for <dir>`, and exits 0

### Scenario: State survives
- WHEN a file exists in `/home/agent` (for example `~/.claude.json` after `/login`), and the owner runs `orbit stop` and then `orbit start`
- THEN the file exists with the same content (D08)

### Scenario: Already stopped
- WHEN the sandbox of the directory is stopped
- THEN Orbit prints `Sandbox <name> is already stopped`, exits 0, and runs no `orb stop`

Source: .ai/specs/2026-11-11-orbit-stop-start.md

## Requirement: Start the sandbox only after the host-side checks
The system SHALL run, for `orbit start`, the project directory rules, the check of the configured mounts, the nesting refusal, and the symlink check of every stored mount path before it starts the machine, and SHALL start nothing when one of them fails.

### Scenario: Start a stopped sandbox
- WHEN the owner runs `orbit start` in a project directory whose sandbox is stopped and passes every check
- THEN the machine runs, Orbit prints `Started sandbox <name> for <dir>`, and exits 0

### Scenario: Stored path became a symlink
- WHEN, after `orbit stop`, the host directory of another sandbox is replaced by a symlink (`mv ~/b ~/w/b && ln -s w/b ~/b`), and the owner runs `orbit start` in the project directory
- THEN Orbit exits 1 with `ErrLinkedSandboxDir` naming that sandbox, and the machine stays stopped

### Scenario: Protected file appeared
- WHEN, after `orbit stop`, `~/.claude.json` gets a second hard link, and the owner runs `orbit start`
- THEN Orbit exits 1 with `ErrHardLinkedFile` and the machine stays stopped

### Scenario: Already running
- WHEN the sandbox of the directory runs
- THEN Orbit prints `Sandbox <name> is already running`, exits 0, and runs no `orb start`

Source: .ai/specs/2026-11-11-orbit-stop-start.md

## Requirement: The boundary is up after a start
The system SHALL report success for `orbit start` only when the boot finished, the kernel mounts exactly the project directory, the firewall, the proxy address, the proxy, the hardening unit, and user sessions are active, and the firewall table `inet orbit_egress` is loaded; on a failure of one of these the system SHALL stop the machine again.

### Scenario: Egress after a start
- WHEN `orbit start` succeeded, and `agent` connects directly to a public address, through the proxy to a host with no allow rule, and through the proxy to an allowed host
- THEN the first two fail and the third succeeds (R03, D14)

### Scenario: Units before agent processes
- WHEN `orbit start` succeeded
- THEN every boundary unit entered `active` before the start time of every process with a uid from 1000 up

### Scenario: Boundary unit fails at boot
- WHEN `tinyproxy.service` is not active after the boot
- THEN Orbit stops the machine again, exits 1, and the message names the unit

### Scenario: Firewall table not loaded
- WHEN `nftables.service` is active after the boot, but the table `inet orbit_egress` is not loaded
- THEN Orbit stops the machine again, exits 1, and the message says that the firewall table is not loaded

### Scenario: Outdated marker
- WHEN the boundary units are active and `/etc/orbit/provisioned` is missing or holds another version
- THEN the machine stays running, and Orbit exits 1 and names `orbit init`

Source: .ai/specs/2026-11-11-orbit-stop-start.md

## Requirement: No sandbox, no action
The system SHALL exit non-zero and SHALL NOT stop or start any machine when the current directory has no sandbox or its sandbox is in a state other than `running` or `stopped`.

### Scenario: No sandbox
- WHEN the owner runs `orbit stop` or `orbit start` in a directory with no sandbox, or in a subdirectory of a project
- THEN Orbit exits 1, names `orbit init`, and the state of every machine is unchanged

### Scenario: Unexpected state
- WHEN the sandbox of the directory is `starting`
- THEN Orbit exits 1, names the state, and runs no `orb start` or `orb stop`

Source: .ai/specs/2026-11-11-orbit-stop-start.md

## Requirement: Destroy the sandbox of the project directory
The system SHALL delete, for `orbit destroy` in a project directory whose sandbox is running or stopped and after the owner confirmed, the machine of that directory with everything on its disk, and no other machine, and SHALL NOT change any file on the host.

### Scenario: Destroy a stopped sandbox
- WHEN the owner runs `orbit destroy --yes` in a directory where `orbit init --agent claude-code` ran and the sandbox is stopped
- THEN no machine has the name of the sandbox, Orbit prints `Destroyed sandbox <name> for <dir>`, and exits 0

### Scenario: Destroy a running sandbox
- WHEN the sandbox of the directory runs and the owner runs `orbit destroy --yes`
- THEN no machine has the name of the sandbox, and Orbit exits 0

### Scenario: Gone from the machine list
- WHEN `orbit destroy --yes` exits 0
- THEN the adapter's `State` for the name returns `""`, read from the same `orb list --format json` that `orbit ls` (#13) reads (Q3)

### Scenario: Agent state is gone
- WHEN a file exists in `/home/agent` (for example `~/.claude.json` after `/login`) and an image exists in the rootless engine of `agent`, and the owner runs `orbit destroy --yes` and then `orbit init` in the same directory
- THEN the new sandbox has the same name, the file does not exist, and the engine lists no image (D08, D19)

### Scenario: No logout, no host cleanup
- WHEN the owner runs `orbit destroy --yes`
- THEN Orbit runs no command in the machine (no `/logout`, no removal of the login file) and deletes, moves, or writes no file on the host; the delete of the machine is the whole removal (Q2, D08)

### Scenario: Project files untouched
- WHEN the owner runs `orbit destroy --yes`
- THEN every file and directory in the project directory has the same content, mode, and modification time as before

### Scenario: A stopped sandbox is not started
- WHEN the sandbox is stopped and the owner runs `orbit destroy --yes`
- THEN Orbit runs only `orb status`, `orb list`, and `orb delete --force <name>`: no `orb start` and no `orb -m` command (#75)

Source: .ai/specs/2026-11-12-orbit-destroy.md

## Requirement: Destroy only after a confirmation
The system SHALL, in every state of the sandbox, delete nothing unless the owner answered `y` or `yes` (case-insensitive) to a `[y/N]` prompt that names the machine, the project directory, and what is lost, or passed `--yes`, and SHALL refuse without a prompt when standard input is not a terminal and `--yes` is not passed (Q1).

### Scenario: Owner declines
- WHEN the owner runs `orbit destroy` and answers `n`, or presses Enter, or ends the input
- THEN Orbit exits 1, says that nothing was deleted, and the machine is unchanged

### Scenario: No terminal
- WHEN `orbit destroy` runs with standard input from a pipe or `/dev/null` and without `--yes`
- THEN Orbit exits 1, names `--yes`, and the machine is unchanged

### Scenario: Prompt names the target
- WHEN the owner runs `orbit destroy` in a terminal, on a running or a stopped sandbox
- THEN the prompt names the machine and the project directory, says that the login, history, and images of the agent are deleted and the project files stay, and, for a running sandbox, says that open shells end

### Scenario: `--yes` skips the prompt
- WHEN the owner runs `orbit destroy --yes` (or `-y`), with or without a terminal
- THEN Orbit shows no prompt, reads no input, and deletes the sandbox

Source: .ai/specs/2026-11-12-orbit-destroy.md

## Requirement: No sandbox, no delete
The system SHALL exit non-zero and SHALL NOT delete any machine when the current directory has no sandbox, or when its sandbox is in a state other than `running` or `stopped`. The adapter SHALL refuse, before any command, a name that lacks the Orbit prefix or holds white space, so a machine that Orbit did not create is never deleted (R07); `MachineName` always adds the prefix, so this guard is proven at the adapter, not through the CLI.

### Scenario: No sandbox
- WHEN the owner runs `orbit destroy --yes` in a directory with no sandbox, or in a subdirectory of a project
- THEN Orbit exits 1, names `orbit init`, and every machine is unchanged

### Scenario: Unexpected state
- WHEN the sandbox of the directory is `starting`
- THEN Orbit exits 1, names the state, and runs no `orb delete`

### Scenario: Hostile machine name
- WHEN the adapter's `Delete` gets a name that is empty, only the prefix, starts with `-` (`-a`, `--all`), holds white space (`orbit-a orbit-b`, `orbit-x --all`), or lacks the prefix
- THEN it runs no `orb` command and returns `ErrForeignMachine` or `ErrMachineName`

Source: .ai/specs/2026-11-12-orbit-destroy.md
