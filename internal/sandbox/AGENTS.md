# AGENTS.md — internal/sandbox

The sandbox module: the lifecycle of the sandbox of a project directory. Read the root `AGENTS.md` and `ARCHITECTURE.md` first. Every change here is `risk-high`.

## Layout

- `sandbox.go` — the root package: `NamePrefix`, `MachineName`, `ProjectDir`, `ProtectedDirs`, `ValidateProjectDir`. Pure functions, no I/O.
- `initialize/` — the slice of `orbit init`. The package is not named `init`, because Go refuses to import a package under that name. It creates the machine only; provisioning is not built, so `NewCommand` is not registered in `cmd/orbit`.
- `internal/realpath/` — private to the module: `Resolve` returns the canonical path of a directory, and `ResolveNoFirmlink` its path on its own volume. Every slice resolves the project directory with `Resolve`.

## Invariants

- A decision about what a sandbox is or what enters it is a pure function in `sandbox.go`, with table tests that include hostile input. A handler loads the facts (`ProjectDir`) from the system, calls the function, and acts on the result. A fact that cannot be read is an error, never a default.
- `ValidateProjectDir` applies two independent rules, and both must accept the directory (R08, R09). Do not add an exception, and do not drop one rule because the other covers a case.
  - Paths: it refuses `/`; a home directory and every directory that contains one; a protected directory (`.claude` and `.ssh` of every home), every directory inside one, and every directory that contains one. It compares path components, not string prefixes.
  - Ownership: it refuses a directory that the user does not own. This covers system directories such as `/System`, `/.vol`, `/.nofollow`, and `/.resolve`.
- The home directories are the one of `$HOME` and the one of the account database (`os/user`), each as its canonical path and as its path on its own volume. Both must exist. `$HOME` alone can name any directory, and on macOS the home directory `/Users/dev` is also `/System/Volumes/Data/Users/dev`, so `/System`, `/System/Volumes`, and `/System/Volumes/Data` contain it.
- A protected directory can be a symlink. The handler resolves every path of `ProtectedDirs` that exists and passes every spelling of its target in `ProjectDir.Protected`. A protected path that exists and cannot be resolved stops `init`.
- Pass only canonical paths to `ValidateProjectDir` and `MachineName`. On macOS `filepath.EvalSymlinks` is not enough: `/users/DEV` and `/System/Volumes/Data/Users/dev` both name the home directory. Resolve every path with `realpath.Resolve` (`F_GETPATH` on macOS), in every slice, so that all slices compute the same machine name for a directory. On macOS the tests of the other spellings fail when the file system does not accept a spelling; they never skip there.
- The machine of a directory is found by its name: `orbit-<slug>-<hash>`, where the slug has at most 30 characters and the hash is the first 16 hex characters of the SHA-256 of the canonical path. A change to `MachineName` orphans every existing sandbox.
- A slice declares the narrow interface that it needs from OrbStack and is tested against `internal/orbstack/orbstacktest`. No package here imports `internal/orbstack`; `golangci-lint` (depguard) fails on it.
- A slice does not import another slice.

## Validation

`go test ./internal/sandbox/...` needs no OrbStack.
