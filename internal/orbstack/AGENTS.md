# AGENTS.md — internal/orbstack

The adapter to OrbStack, through the `orb` CLI. It is the only package with OrbStack-specific code (R07). Read the root `AGENTS.md` and `ARCHITECTURE.md` (Provisioning requirements) first. Every change here is `risk-high`.

## Layout

- `orbstack.go` — `Client`: `Create`, `Exists`, `Mounts`, `ActiveMounts`, `Delete`, `RunAsRoot`, `RunAsAgent`; the constant `AgentUser`. Each argument list of `orb`, and each parser of its output, is a small pure function with a unit test.
- `orbstacktest/` — the in-memory fake for handler tests. It imports nothing from the adapter.
- `integration_test.go` — tests against a real OrbStack, behind the `integration` build tag.

## Invariants

- `createArgs` always passes `--isolated`, `--isolate-network`, and one mount in the form `DIR:DIR`. A caller cannot change them. A mount without a destination lands under `/mnt/mac`.
- `Create` refuses a directory that is not absolute or that holds a colon or a comma (`ErrMountPath`). `orb` splits a mount at the first colon, so such a path mounts another directory than the one that was validated; OrbStack stores the mounts of a machine as a list with commas.
- `Mounts` reads the configured mounts of every machine from `orb list --format json`. `ActiveMounts` reads `/proc/self/mountinfo` as root in the machine and returns the mounts of the file system `virtiofs` with the source `mac`: its root is the root of the host, so the root of each mount is the host path of the mounted directory as the machine resolved it. It shows a symlink that was swapped in before the mount was made. It does not show a path that was removed and recreated as a symlink between two starts: after a restart it still reports the stored path while the machine serves the target (OrbStack 2.2.5). It is not proof of the identity of the mounted directory. Both fail with `ErrMountTable` on output they cannot read and on a mount point that appears twice; they never guess.
- `Create`, `ActiveMounts`, `Delete`, `RunAsRoot`, and `RunAsAgent` refuse a name without the prefix of the client (`ErrForeignMachine`) before they run a command.
- `RunAsRoot` runs `orb -m NAME -u root bash -s` with the script on standard input. The user is fixed; no argument of the caller reaches the command line. `RunAsAgent` runs `orb -m NAME -u agent bash -s` the same way: its user is the constant `AgentUser`, never an argument, and no argument of the caller reaches the command line. A test in `cmd/orbit` checks that `AgentUser` equals `provision.AgentUser`. A client with an empty prefix refuses every name. `cmd/orbit` passes `sandbox.NamePrefix`.
- The adapter imports no other package of this repository. Only `cmd/orbit` imports the adapter; `golangci-lint` (depguard) fails on any other import. Slices use `orbstacktest` in tests.
- The adapter does not pass `--forward-ssh-agent`. Add an operation only when a slice needs it, and add it to the fake in the same change.
- The client never starts OrbStack. `orb` starts a stopped OrbStack by itself on any command that needs it (OrbStack 2.2.5, #62), so every command of the client first runs `orb status`, which only reports the state. Unless it prints `Running` and exits 0, the client runs no other command and fails: with `ErrNotRunning` and the advice to start OrbStack for `Stopped` or `Starting`, with an error for any other output. Do not add a path to `orb` that skips this check.
- An error of a failed command wraps the error of `os/exec` and carries the standard error of `orb`, when `orb` printed anything.
- The fake applies the same prefix rule and the same mount-path rule as the adapter, with sentinel errors of its own. Keep the two in step.

## Validation

- `go test ./internal/orbstack/...` needs no OrbStack.
- `go test -tags integration -count=1 -v ./internal/orbstack/...` needs a running OrbStack. It creates one machine named `orbit-it-<random>` and deletes it; it registers the deletion only after it saw that no machine has that name. Never touch a machine whose name does not start with `orbit-it-` from a test.
