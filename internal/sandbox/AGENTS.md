# AGENTS.md — internal/sandbox

The sandbox module: the lifecycle of the sandbox of a project directory. Read the root `AGENTS.md` and `ARCHITECTURE.md` first. Every change here is `risk-high`.

## Layout

- `sandbox.go` — the root package: `NamePrefix`, `MachineName`, `ProjectDir`, `ProtectedDirs`, `ValidateProjectDir`, `ValidateNotNested`, `ValidateMounts`. Pure functions, no I/O.
- `initialize/` — the slice of `orbit init`. The package is not named `init`, because Go refuses to import a package under that name. It creates the machine and provisions it as root with the script that `Handler.ProvisionScript` returns (`provision.Script`, injected by `cmd/orbit`). `NewCommand` is not registered in `cmd/orbit` until the agent is installed as well (story E01-S01).
- `internal/realpath/` — private to the module: `Resolve` returns the canonical path of a directory, `ResolveWithOwner` that path and the owner from one open handle, and `ResolveNoFirmlink` its path on its own volume. Every slice resolves the project directory with `Resolve` or `ResolveWithOwner`.

## Invariants

- A decision about what a sandbox is or what enters it is a pure function in `sandbox.go`, with table tests that include hostile input. A handler loads the facts (`ProjectDir`) from the system, calls the function, and acts on the result. A fact that cannot be read is an error, never a default.
- `ValidateProjectDir` applies two independent rules, and both must accept the directory (R08, R09). Do not add an exception, and do not drop one rule because the other covers a case.
  - Paths: it refuses `/`; a home directory and every directory that contains one; a protected directory (`.claude` and `.ssh` of every home), every directory inside one, and every directory that contains one. It compares path components, not string prefixes.
  - Ownership: it refuses a directory that the user does not own. This covers system directories such as `/System`, `/.vol`, `/.nofollow`, and `/.resolve`.
- The home directories are the one of `$HOME` and the one of the account database (`os/user`), each as its canonical path and as its path on its own volume. Both must exist. `$HOME` alone can name any directory, and on macOS the home directory `/Users/dev` is also `/System/Volumes/Data/Users/dev`, so `/System`, `/System/Volumes`, and `/System/Volumes/Data` contain it.
- A protected directory can be a symlink. The handler resolves every path of `ProtectedDirs` that exists and passes every spelling of its target in `ProjectDir.Protected`. A protected path that exists and cannot be resolved stops `init`.
- Pass only canonical paths to `ValidateProjectDir` and `MachineName`. On macOS `filepath.EvalSymlinks` is not enough: `/users/DEV` and `/System/Volumes/Data/Users/dev` both name the home directory. Resolve every path with `realpath.Resolve` (`F_GETPATH` on macOS), in every slice, so that all slices compute the same machine name for a directory. On macOS the tests of the other spellings fail when the file system does not accept a spelling; they never skip there.
- Read the canonical path and the owner of the project directory from one open handle (`realpath.ResolveWithOwner`: `open(O_SEARCH)`, `fstat`, `F_GETPATH`). Two lookups of the path can name two directories.
- `orb` resolves the mount path again, at every start of the machine, and follows a symlink in it. So the check of the path alone cannot hold, and `init` has two more layers (#29). Keep both.
  - It refuses a project directory that is a directory that another machine mounts, or lies inside one (`ValidateNotNested`, `ErrNestedProjectDir`): the agent of that machine controls the path. It compares every spelling of each mounted directory that exists, and it refuses before it creates a machine.
  - After it creates a machine, and for a machine that already exists, it checks that the configuration and the kernel of the machine (`/proc/self/mountinfo`) both mount exactly the project directory at its own path (`ValidateMounts`, `ErrMountMismatch`). The configuration alone still names a swapped path; only the kernel shows the directory that is mounted. It deletes a machine that it created and that fails the check, and never one that existed before. It never provisions a machine that fails the check.
- The machine of a directory is found by its name: `orbit-<slug>-<hash>`, where the slug has at most 30 characters and the hash is the first 16 hex characters of the SHA-256 of the canonical path. A change to `MachineName` orphans every existing sandbox.
- A slice declares the narrow interface that it needs from OrbStack and is tested against `internal/orbstack/orbstacktest`. No package here imports `internal/orbstack`; `golangci-lint` (depguard) fails on it.
- A slice does not import another slice, and it does not import another module: `cmd/orbit` injects functions such as `provision.Script`.
- `init` provisions every time it runs, also on an existing machine, and only after the mount check passed. When the mount check or provisioning fails on a machine that the same call created, it deletes that machine (with a context that cancellation does not stop). It never deletes a machine that existed before the call.

## Validation

`go test ./internal/sandbox/...` needs no OrbStack.
