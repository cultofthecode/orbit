# AGENTS.md — internal/agent

The agent definitions (D16): what Orbit needs to install an agent in a sandbox. Spec: `.ai/specs/2026-11-11-claude-code-agent-definition.md`. Read the root `AGENTS.md` and `ARCHITECTURE.md` (Agent definitions) first. Every change here is `risk-high`: `Install` runs in the sandbox, and `EgressDomains` widen the allowlist of the proxy.

## Layout

- `agent.go` — `Definition{Name, Install, EgressDomains}`, `Registry` (`NewRegistry`, `Lookup`, `Names`), `ErrUnknownAgent`, `ErrInvalidDefinition`. Pure, no I/O.
- `claude/` — `claude.Definition`, named `claude-code`, and its R04 test.

## Invariants

- One package per agent, with one exported `Definition` and a test that checks it against R04: an official `https` install source, no change to the client, no credential, and every domain passes `policy.ValidateHost`. `cmd/orbit` registers it; no slice changes.
- No code outside the package of an agent compares its name. A slice gets `sandbox.Agent` through the function that `cmd/orbit` injects.
- `NewRegistry` refuses an empty name and a duplicate name. `Lookup` fails with `ErrUnknownAgent` and names the known agents; it returns a copy of `EgressDomains`.
- `Install` is a bash script that runs as the agent user. It holds no secret, no value of the user, and no prelude: the `init` handler wraps it in one braced group with `set -euo pipefail` and the proxy settings of `/etc/environment`.
- `EgressDomains` of `claude-code` are exactly the eight Claude Code hosts of D24. A new domain needs the owner's yes and a superseding row in the product brief.
- Orbit never reads, copies, stores, or forwards the credentials of an agent (D08, R04). A definition writes nothing into `~/.claude`.

## Validation

`go test ./internal/agent/...` needs no OrbStack. `go test -tags integration -p 1 -run 'TestInitInstallsClaudeCode|TestInitDeletesTheNewMachine' -v ./cmd/orbit/` installs Claude Code in a real sandbox.
