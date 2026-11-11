# Capability: agent-install

`orbit init --agent <name>` installs the official client of a registered agent in the sandbox, as the agent user, through the proxy.

## Requirement: Install the official client as the agent user
The system SHALL install the agent named by `--agent` with the `Install` of its definition, as the agent user, through the proxy, after provisioning passed its self-check.

### Scenario: Init with Claude Code
- WHEN the owner runs `orbit init --agent claude-code` in an accepted project directory
- THEN the sandbox is provisioned, `runuser -u agent -- /home/agent/.local/bin/claude --version` succeeds, and the client and its files under `/home/agent` are owned by `agent`

### Scenario: Unknown agent
- WHEN the owner runs `orbit init --agent codex`
- THEN `init` fails with `ErrUnknownAgent`, names the known agents, and asks OrbStack nothing

### Scenario: The install fails
- WHEN the installer exits non-zero
- THEN `init` fails; a machine that the call created is deleted; an existing machine stays

### Scenario: The download of the installer fails
- WHEN `curl` in `Install` fails, for example because the proxy answers 403 or the host does not resolve
- THEN `init` fails, although `bash` exits 0 on empty input; a machine that the call created is deleted; an existing machine stays

Source: .ai/specs/2026-11-11-claude-code-agent-definition.md

## Requirement: Claude Code definition follows R04
The system SHALL define `claude-code` with an install from an official `https` source on `claude.ai`, no change to the client, and no credential.

### Scenario: R04 test
- WHEN the test of `internal/agent/claude` runs
- THEN it fails if `Install` fetches from another source, writes into `~/.claude`, sets a token or key variable, or passes flags that change the client; and if a domain fails `ValidateHost`

Source: .ai/specs/2026-11-11-claude-code-agent-definition.md
