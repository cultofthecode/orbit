# Escape suite (#10)

## Goal

Deliver the escape suite of `.ai/specs/2026-11-12-escape-suite.md`: `TestEscape*` integration tests in `cmd/orbit` that run every documented escape attempt in real OrbStack sandboxes, one subtest per attempt with a positive control, and name the suite as the evidence of every `risk-high` boundary PR.

## Scope

- `cmd/orbit`: the probe fragments and their golden test, the pure verdict and tally, the catalogue and runner, `TestEscapeShared` (E01–E11), `TestEscapeRestart` (E13, E14, O01), `TestEscapeNoSSHForwarding` (E15), `TestEscapeWeakenedBoundary` (E12, M01), `TestEscapeSymlinkSwap` (E16), `TestEscapeCredentialLocations` (E17).
- Documentation: `SDLC.md`, `AGENTS.md`, `ARCHITECTURE.md`, `internal/provision/AGENTS.md`, the provisioning spec, and the escape-suite spec (owner decisions A1–A3 of 2026-11-12 14:14).

Non-goals: production code, kernel exploits (A01), item 1 of #115.

## Progress

PR: #157

> Convention: `- [ ]` pending, `- [x]` done. Append ` — <commit sha>` when a step lands. Do not rename step titles.

### Phase 0: Fragments

- [x] 0.1 Move the probe scripts into an untagged file with a golden test — 8101482
- [x] 0.2 Extract the shared shell fragments — 722937f

### Phase 1: Catalogue and the shared sandbox

- [x] 1.1 Pure attempt verdict and tally — fb9072a
- [x] 1.2 Catalogue, runner, and the shared cases E01–E11 — 4c439bf

### Phase 2: Disposable cases

- [x] 2.1 E12–E17, O01, and M01 — 5d40a4d
- [x] 2.2 Apply the owner decisions A1 (E04 inconclusive), A2 (E15 throwaway key), and A3 (M01 sub-uid detector) to the spec and the tests

### Phase 3: Documentation and evidence

- [x] 3.1 Name the suite command in the gate documents — 0f91f55
- [x] 3.2 Run the default gate, the escape suite, and the full integration run, and record the run times

## Risks

- E15 cannot fail through a forwarding of the throwaway key on OrbStack 2.2.5: OrbStack forwards the agent that it connects to itself, not the `SSH_AUTH_SOCK` of the caller (spec, A2). The other checks of E15 show that no forwarding exists.
- E04 is inconclusive on OrbStack 2.2.5: `--isolate-network` blocks the network of the host engine even for root (spec, A1).
