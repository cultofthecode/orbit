# AGENTS.md — internal/policy

The network policy of a sandbox: the default allowlist of the first version (D21) and the rule for a host name in an allow rule. Read the root `AGENTS.md` and `ARCHITECTURE.md` first. Every change here is `risk-high`.

## Layout

- `policy.go` — `DefaultAllowlist`, `ValidateHost`, `ErrInvalidHost`. Pure, no I/O.

## Invariants

- `DefaultAllowlist` is exactly D21. A new default host needs the owner's yes and a superseding row in the product brief.
- `ValidateHost` accepts only lowercase DNS names of at least two labels, letters, digits, and inner hyphens, at most 253 characters, with a top-level label that is not all digits. It refuses wildcards, IP literals, ports, a trailing dot, and every other byte, so that a host can be written into the tinyproxy filter by escaping its dots alone. Do not loosen it to accept a pattern.
- Every default host passes `ValidateHost`; a test enforces it.

## Validation

`go test ./internal/policy/...` needs no OrbStack.
