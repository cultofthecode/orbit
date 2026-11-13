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

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED; owner decision of 2026-11-13)
Source: .ai/specs/2026-11-12-escape-suite.md (privileged containers, owner decision of 2026-11-13)
