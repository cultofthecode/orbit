# AGENTS.md — internal/orbstack

The adapter to OrbStack, through the `orb` CLI. It is the only package with OrbStack-specific code (R07). Read the root `AGENTS.md` and `ARCHITECTURE.md` (Provisioning requirements) first. Every change here is `risk-high`.

## Layout

- `orbstack.go` — `Client`: `Create`, `Exists`, `Delete`. Each argument list of `orb` comes from a small pure function with a unit test.
- `orbstacktest/` — the in-memory fake for handler tests. It imports nothing from the adapter.
- `integration_test.go` — tests against a real OrbStack, behind the `integration` build tag.

## Invariants

- `createArgs` always passes `--isolated`, `--isolate-network`, and one mount in the form `DIR:DIR`. A caller cannot change them. A mount without a destination lands under `/mnt/mac`.
- `Create` refuses a directory that is not absolute or that holds a colon. `orb` splits a mount at the first colon, so such a path mounts another directory than the one that was validated.
- `Create` and `Delete` refuse a name without the prefix of the client (`ErrForeignMachine`) before they run a command. A client with an empty prefix refuses every name. `cmd/orbit` passes `sandbox.NamePrefix`.
- The adapter imports no other package of this repository. Only `cmd/orbit` imports the adapter; `golangci-lint` (depguard) fails on any other import. Slices use `orbstacktest` in tests.
- The adapter does not pass `--forward-ssh-agent`. Add an operation only when a slice needs it, and add it to the fake in the same change.
- An error of a failed command wraps the error of `os/exec` and carries the standard error of `orb`.
- The fake applies the same prefix rule as the adapter. Keep the two in step.

## Validation

- `go test ./internal/orbstack/...` needs no OrbStack.
- `go test -tags integration -count=1 -v ./internal/orbstack/...` needs a running OrbStack. It creates one machine named `orbit-it-<random>` and deletes it. Never touch a machine whose name does not start with `orbit-it-` from a test.
