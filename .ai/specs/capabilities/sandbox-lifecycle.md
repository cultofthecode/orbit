# Capability: sandbox-lifecycle

The owner can stop the sandbox of a project directory and start it again, with the host-side checks of `orbit init` before every start and the boundary checked after it.

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
