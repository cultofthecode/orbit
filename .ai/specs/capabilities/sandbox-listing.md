# Capability: sandbox-listing

`orbit ls` lets the owner see every Orbit sandbox with its state and project directory, without starting anything.

## Requirement: List every sandbox with its project directory and state
The system SHALL print, for `orbit ls`, one line per OrbStack machine whose name starts with the Orbit prefix, with its name, its state as OrbStack reports it, and the project directory that OrbStack stores as its mount, sorted by name, from one read of the machine list.

### Scenario: One running and one stopped sandbox
- WHEN the owner ran `orbit init` in `/Users/dev/api` and in `/Users/dev/web`, ran `orbit stop` in `/Users/dev/web`, and runs `orbit ls` in any directory
- THEN Orbit prints a header `NAME STATE DIRECTORY` and two lines, `orbit-api-<hash> running /Users/dev/api` and `orbit-web-<hash> stopped /Users/dev/web`, and exits 0

### Scenario: No sandbox
- WHEN no OrbStack machine has a name with the Orbit prefix
- THEN Orbit prints nothing on stdout and exits 0

Source: .ai/specs/2026-11-12-orbit-ls.md

## Requirement: Only Orbit machines are listed
The system SHALL never list a machine whose name does not start with the Orbit prefix (R07).

### Scenario: A foreign machine
- WHEN OrbStack has the machines `ubuntu`, `xorbit-a`, and `Orbit-b` and one Orbit sandbox
- THEN Orbit lists only the Orbit sandbox

Source: .ai/specs/2026-11-12-orbit-ls.md

## Requirement: Listing starts nothing
The system SHALL not start OrbStack or a machine, and SHALL run no command inside a machine, for `orbit ls`. As with every command, the window accepted in #62 remains: `orb status` and the next `orb` command are two calls, and OrbStack can stop or start between them.

### Scenario: A stopped sandbox stays stopped
- WHEN a sandbox is stopped and the owner runs `orbit ls`
- THEN it is listed as `stopped` with its project directory, and its state is still `stopped` afterwards

### Scenario: OrbStack is not running
- WHEN OrbStack is stopped and the owner runs `orbit ls`
- THEN Orbit prints nothing on stdout, fails with the cause and the advice to start OrbStack, exits 1, and OrbStack stays stopped (as every command, within the window accepted in #62)

Source: .ai/specs/2026-11-12-orbit-ls.md

## Requirement: An unknown project directory is shown, not guessed
The system SHALL show `?` as the project directory of a prefixed machine whose stored mounts are not exactly one mount of an absolute, clean directory at its own path whose machine name equals the name of the machine, and SHALL print the reason on stderr.

### Scenario: A machine created by hand with the prefix
- WHEN a machine `orbit-manual` mounts `/Users/dev/x` at `/Users/dev/x`
- THEN Orbit lists `orbit-manual` with its state and `?`, prints `orbit: orbit-manual: project directory unknown: the name does not match "/Users/dev/x"` on stderr, and exits 0

### Scenario: One unreadable machine among readable ones
- WHEN one sandbox has a valid stored mount and another prefixed machine has two mounts
- THEN Orbit lists both, the first with its directory and the second with `?`, prints the reason for the second on stderr, and exits 0

Source: .ai/specs/2026-11-12-orbit-ls.md

## Requirement: An unreadable machine list fails closed
The system SHALL fail with exit 1 and print no partial list when the machine list cannot be read as a whole.

### Scenario: Empty output of `orb list`
- WHEN `orb list --format json` exits 0 and prints nothing
- THEN Orbit fails with `ErrMachineList`, prints nothing on stdout, exits 1, and does not run `orb list` again

Source: .ai/specs/2026-11-12-orbit-ls.md

## Requirement: A destroyed sandbox is no longer listed
The system SHALL not list a sandbox after `orbit destroy` removed it. The end-to-end check is added by whichever implementation PR of #12 and #13 merges second (owner decision on #12).

### Scenario: `ls` after `destroy`
- WHEN the owner ran `orbit init` in two projects, then `orbit destroy --yes` in one of them, and runs `orbit ls`
- THEN Orbit lists the other sandbox and not the destroyed one, and exits 0

Source: .ai/specs/2026-11-12-orbit-ls.md
