# AGENTS.md — internal/sandbox

The sandbox module: the lifecycle of the sandbox of a project directory. Read the root `AGENTS.md` and `ARCHITECTURE.md` first. Every change here is `risk-high`.

## Layout

- `sandbox.go` — the root package: `NamePrefix`, `MachineName`, `ValidateProjectDir`. Pure functions, no I/O.
- `initialize/` — the slice of `orbit init`. The package is not named `init`, because Go refuses to import a package under that name. It creates the machine only; provisioning is not built, so `NewCommand` is not registered in `cmd/orbit`.
- `internal/realpath/` — private to the module: `Resolve` returns the canonical path of a file. Every slice resolves the project directory with it.

## Invariants

- A decision about what a sandbox is or what enters it is a pure function in `sandbox.go`, with table tests that include hostile input. Handlers resolve paths, call the function, and act on the result.
- `ValidateProjectDir` refuses `/`, the home directory, every ancestor of the home directory, and `.claude` and `.ssh` in the home directory with everything inside them (R08). It compares path components, not string prefixes. Do not add an exception.
- Pass only canonical paths to `ValidateProjectDir` and `MachineName`. On macOS `filepath.EvalSymlinks` is not enough: `/users/DEV` and `/System/Volumes/Data/Users/dev` both name the home directory. Resolve every path with `realpath.Resolve` (`F_GETPATH` on macOS), in every slice, so that all slices compute the same machine name for a directory. Its tests and the handler tests cover both spellings.
- The machine of a directory is found by its name: `orbit-<slug>-<hash>` of the canonical path. A change to `MachineName` orphans every existing sandbox.
- A slice declares the narrow interface that it needs from OrbStack and is tested against `internal/orbstack/orbstacktest`. No package here imports `internal/orbstack`; `golangci-lint` (depguard) fails on it.
- A slice does not import another slice.

## Validation

`go test ./internal/sandbox/...` needs no OrbStack.
