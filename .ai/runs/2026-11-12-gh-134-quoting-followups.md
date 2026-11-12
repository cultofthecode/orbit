# Quoting follow-ups of #128 and #123 (#134, #135, #136, #129)

## Goal

Four priority-low, risk-low follow-ups in one PR: one shared `printable` helper, the directory quoted in the no-sandbox errors, a pinned refusal in a destroy test, and no repeated "list" in the `orbit ls` adapter error.

## Scope

- `internal/sandbox`: `Printable` (pure, no I/O), used by `destroy` and `list`.
- `destroy`, `stop`, `start`, `shell`: quote the directory in `no sandbox for <dir>`.
- `destroy` test: assert `ErrNotConfirmed`.
- `internal/orbstack`: `List` no longer wraps with "list machines".

Non-goals: the hostcheck errors (#111, risk-high); the other adapter methods; no change to mounts, isolation, provisioning, or policy.

## Progress

> Convention: `- [ ]` pending, `- [x]` done. Append ` — <commit sha>` when a step lands. Do not rename step titles.

### Phase 1: Implement

- [x] 1.1 Shared Printable helper with a table test (#134)
- [x] 1.2 Quote the directory in the four no-sandbox errors with hostile-directory tests (#135)
- [x] 1.3 Assert the refusal in TestPromptAndResultQuoteAHostileDirectory (#136)
- [x] 1.4 Drop the repeated "list" in the adapter error (#129)
