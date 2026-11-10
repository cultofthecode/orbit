# Execution plan: sandbox provisioning

Source doc: .ai/specs/2026-11-10-sandbox-provisioning.md
Spec PR: #31
Issue: #16

## Goal

`init` provisions the machine as root: an agent user without sudo, a root-owned nftables firewall for every uid from 1000 up and the default uid, tinyproxy limited to the D21 allowlist, persistent units, and the D20 toolchain.

## Scope

- New modules `internal/policy`, `internal/proxy`, `internal/provision`.
- Adapter operation `RunAsRoot`; fake `RunAsRoot` and `Delete`.
- `init` handler provisions and deletes a machine it created when provisioning fails; `cmd/orbit` injects `provision.Script`.
- Integration test, documentation, archived Delta.

## Non-goals

- Registering `orbit init` (Q3), rootless Docker (E01-S05), SSH forwarding (E01-S04), the escape suite (E01-S06), an adapter restart operation.

## Implementation Plan

The steps are those of the spec's Implementation Plan, in the same order.

## Risks

- Unproven OrbStack facts (DNS at `0.250.250.200`, boot order for `orb -u agent`, sudoers restore) are covered by the integration test and manual restart QA.

## Progress

> Convention: `- [ ]` pending, `- [x]` done. Append ` — <commit sha>` when a step lands. Do not rename step titles.

### Phase 1: Pure configuration

- [x] 1.1 Add internal/policy with the default allowlist and host validation — c62976b
- [x] 1.2 Add internal/proxy with the tinyproxy configuration — 80247ed
- [x] 1.3 Add provision.Firewall — e922da6
- [x] 1.4 Add provision.Units — ec35936
- [x] 1.5 Add provision.Script — c8a9f4b

### Phase 2: Adapter and slice

- [x] 2.1 Add the adapter operation RunAsRoot and extend the fake — 9c14556
- [x] 2.2 Characterize init — 27df8f4 (already pinned on base by checkCreates and TestHandleKeepsAnExistingMachine)
- [x] 2.3 Provision in init — 2eff394

### Phase 3: Proof and records

- [ ] 3.1 Add the integration test of provisioning
- [ ] 3.2 Update the documentation
- [ ] 3.3 Archive the Delta
