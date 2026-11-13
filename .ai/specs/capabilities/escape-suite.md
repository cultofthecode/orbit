# Capability: escape-suite

The escape suite (`.ai/specs/2026-11-12-escape-suite.md`) runs the same attempts on every supported distribution, and it reports the privileged-container attempts as not applicable where `--privileged` is refused.

## Requirement: Privileged-container attempts follow the privileged table
The system SHALL report the privileged-container attempts E18–E22 and M02 on each distribution as `n/a: --privileged refused on <distro>` while `--privileged` is refused there, and SHALL count them apart from the attempts, never as passed. The attempts and their controls are defined in `.ai/specs/2026-11-12-sandbox-distributions.md` (Delta, Capability: escape-suite), not in this file.

### Scenario: Refused on every distribution
- WHEN the escape suite runs with `ORBIT_IT_DISTRO=debian`, or on the default Ubuntu distribution
- THEN E18–E22 and M02 are reported as `n/a: --privileged refused on <distro>`, and the summary counts them apart from the passed attempts

### Scenario: The flag starts on a distribution
- WHEN `--privileged` starts a container on a distribution where the privileged table says refused
- THEN the integration test of `sandbox-containers` fails and names the distribution, and the owner decides before any attempt is counted

## Requirement: Every documented escape attempt fails, against a control that passes
The system SHALL run each attempt E01–E22 of the catalogue and SHALL pass an attempt only when its positive control, if it has one, succeeded and the attempt failed. E18–E22 apply only on a distribution where `--privileged` is allowed; on a distribution where it is refused, they are `n/a` and the requirement above applies. The full text of this requirement for E01–E17 and E23 stays in `.ai/specs/2026-11-12-escape-suite.md`, which this file does not copy.

### Scenario: An attempt of E18–E22 runs
- WHEN `--privileged` is allowed on the distribution under test
- THEN each of E18–E22 passes only when the shared privileged control and its own control succeeded and the attempt failed

## Requirement: Cases that change a machine run in their own sandbox
The system SHALL run E18, E19, E20, and E22 in the shared sandbox in the order E01–E08, E10, E11, E18, E19, E20, E22, E09, with a re-check of the boundary after E22 and after E09. The system SHALL run E21 in the disposable `--no-ssh-forwarding` sandbox, after E15, and M02 in the weakened sandbox, after M01. These placements apply only where `--privileged` is allowed. The other placements stay in `.ai/specs/2026-11-12-escape-suite.md`.

### Scenario: Placement where the flag is refused
- WHEN `--privileged` is refused on the distribution under test
- THEN E18–E22 and M02 do not run, and the order of the shared sandbox is E01–E08, E10, E11, E09

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED; owner decision of 2026-11-13)
Source: .ai/specs/2026-11-12-escape-suite.md (privileged containers, owner decision of 2026-11-13)
