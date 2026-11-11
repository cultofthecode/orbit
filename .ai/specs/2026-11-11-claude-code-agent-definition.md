# Claude Code agent definition and its install

Issue: #17 (E01-S01-T04), story #5 (E01-S01). Depends on #16 (merged in #32) and #29 (merged in #33).
Extends: .ai/specs/2026-11-10-sandbox-provisioning.md

## 📝 TLDR

Today the `init` slice creates and provisions a sandbox, but it installs no agent, and `orbit init` is not a registered command. The allowlist of every sandbox is the fixed D21 list. This spec proposes the `internal/agent` module with one definition, `claude-code`, and an `--agent` flag on `orbit init` that installs the official client as the agent user through the proxy and adds the `EgressDomains` of the definition to the allowlist. With it, story E01-S01 is complete and `orbit init` is registered.

## Resolved assumptions

This spec was written unattended. The owner, Jakub Tobiasz, decided Q1–Q3 on 2026-11-11 ([PR #34](https://github.com/cultofthecode/orbit/pull/34), [#17](https://github.com/cultofthecode/orbit/issues/17)). Q4–Q6 are the defaults of the unattended run, and the owner did not change them. No question is open.

| # | Question | Decision | Why |
|---|---|---|---|
| Q1 | Which fields does `Definition` have? Do `Start` and `LoginHint` (and `StatePaths`, `Image`) stay? | **Owner decision, 2026-11-11.** Only `Name`, `Install`, and `EgressDomains`. `Start` and `LoginHint` are dropped: Orbit does not start the agent (D19), and sign-in is `/login` in the client (D08). No `StatePaths` and no `Image` until a slice uses them. `ARCHITECTURE.md` is updated to match. | Least surface. A field without a reader is speculative. The Agent row in Components still says "image, start command"; that is drift. A later slice adds a field when it needs one (rule of Agent definitions). |
| Q2 | Where do the eight Claude Code hosts of D21 live? | **Owner decision, 2026-11-11.** Move them from `policy.defaultAllowlist` into `claude.Definition.EgressDomains`: the same eight hosts, no new host. A sandbox with `--agent claude-code` gets exactly the D21 list. A sandbox without `--agent` no longer reaches the Claude Code hosts. | It is what `ARCHITECTURE.md` (Provisioning requirements) and the done-when of #17 describe: D21 plus the `EgressDomains` of the installed agent. It narrows the no-agent sandbox and widens nothing. The default allowlist is an Ask-first surface, so the owner decided it. |
| Q3 | What does `init` do on an existing sandbox when `--agent` differs from the last run? | **Owner decision, 2026-11-11.** Every `init` rebuilds the allowlist from the `--agent` of that run. `orbit init` without `--agent` on a sandbox with Claude Code removes the Claude Code hosts; the installed client stays in the home and fails closed until `orbit init --agent claude-code` runs again. Orbit stores no record of the installed agent. | No new state (OrbStack is the state store). It fails closed. |
| Q4 | Is the client version pinned? | No pin. `Install` runs the published installer (`curl -fsSL https://claude.ai/install.sh \| bash`), which installs the current release. The client's own updater stays as it is. | `ARCHITECTURE.md` names "the published Claude Code installer". R04: Orbit does not change the client, and a pin needs a version source that the owner did not ask for. The install runs as the agent user, not as root. |
| Q5 | How does the install run as the agent user? | A new adapter method `RunAs(ctx, name, user, script)` that runs `orb -m <name> -u <user> bash -s`, as the spike did. The install script reads `/etc/environment` first, so it uses the proxy. | Root never writes into the home of the agent (provisioning invariant). `runuser` from the root script would mix the two phases in one script. |
| Q6 | What happens when the install fails? | `init` fails. When the same call created the machine, it deletes it, as for a failed provisioning. An existing machine stays. | Same rule as provisioning Q4 of the prior spec: `init` leaves a complete sandbox or none. |

## 📝 Problem Statement

- Story E01-S01 requires `orbit init --agent claude-code` to give a sandbox with Claude Code installed. Tasks #14, #15, and #16 delivered the machine and the boundary; no task installs a client, so the `init` command stays unregistered (`internal/sandbox/AGENTS.md`).
- The spike installed Claude Code 2.1.322 as `agent` with the official installer through the proxy in 19 s. It needed only `claude.ai` and `downloads.claude.ai`; `claude -p` then contacts `api.anthropic.com`. `[DATA]` `.ai/analysis/2026-11-10-orbstack-isolated-machine-spike.md`, finding 4 and 5.
- D16 requires that a new agent is one self-contained definition, and no slice compares an agent name (`ARCHITECTURE.md`, Agent definitions).

## 📝 Proposed Solution

1. `internal/agent/agent.go`: `Definition{Name, Install, EgressDomains}`, `Registry` (built from definitions; refuses an empty or duplicate name), `Lookup(name)` with `ErrUnknownAgent`, and `Names()` for the flag help. `internal/agent/claude/claude.go`: `var Definition`, name `claude-code`. Both depend on nothing in the repository.
2. `policy.Allowlist(agentDomains []string) ([]string, error)`: the base list plus the domains of the agent, without duplicates, in a stable order. It refuses the whole list when one domain fails `ValidateHost`. Pure, with hostile-input tests.
3. `provision.Script(defaultUID uint32, agentDomains []string)` renders the proxy allowlist from `policy.Allowlist`. The rest of the script does not change.
4. The `init` slice: `Input.Agent` (empty means no agent). New handler field `Agent func(name string) (sandbox.Agent, error)`, injected by `cmd/orbit` from the registry, so the slice imports no other module (dependency rule 2). `sandbox.Agent` holds `Install` and `EgressDomains`. After provisioning, when an agent is set, the handler runs `Install` as `provision.AgentUser` through `Machines.RunAs`.
5. `cmd/orbit` builds the registry with `claude.Definition` and registers `initialize.NewCommand` with `--agent`. `--no-ssh` comes with E01-S04 (#8), when SSH forwarding exists.

Alternatives considered:

- npm `@anthropic-ai/claude-code`: needs Node.js, which is not in the toolchain (D20). Rejected.
- A prebuilt image with the client, as `sbx` templates do: Orbit has no image pipeline and the provisioning spec fixed Ubuntu 26.04. Rejected.
- Install from the root script with `runuser`: works, but puts agent-user actions in the root script. Rejected (Q5).

## 📝 Delta

### Capability: agent-install

#### ADDED Requirement: Install the official client as the agent user

The system SHALL install the agent named by `--agent` with the `Install` of its definition, as the agent user, through the proxy, after provisioning passed its self-check.

##### Scenario: Init with Claude Code
- WHEN the owner runs `orbit init --agent claude-code` in an accepted project directory
- THEN the sandbox is provisioned, `runuser -u agent -- /home/agent/.local/bin/claude --version` succeeds, and the client and its files under `/home/agent` are owned by `agent`

##### Scenario: Unknown agent
- WHEN the owner runs `orbit init --agent codex`
- THEN `init` fails with `ErrUnknownAgent`, names the known agents, and asks OrbStack nothing

##### Scenario: The install fails
- WHEN the installer exits non-zero
- THEN `init` fails; a machine that the call created is deleted; an existing machine stays

#### ADDED Requirement: The agent's egress domains are allow rules

The system SHALL allow, through the proxy, exactly the base allowlist plus the `EgressDomains` of the agent of the last `init`. Each `init` rebuilds the allowlist from its own `--agent` (Q3).

##### Scenario: Hostile domain in a definition
- WHEN a definition holds `*.anthropic.com`, `1.2.3.4`, `localhost`, `Claude.AI`, or `example.com:22`
- THEN `policy.Allowlist` refuses the whole list and `init` fails before it runs a script

##### Scenario: No agent (Q2)
- WHEN the owner runs `orbit init` without `--agent`
- THEN the proxy refuses `CONNECT api.anthropic.com:443` and allows `CONNECT github.com:443`

##### Scenario: Init again without the agent (Q3)
- WHEN the owner runs `orbit init --agent claude-code` and then `orbit init` without `--agent` on the same sandbox
- THEN the proxy refuses `CONNECT api.anthropic.com:443`, and Orbit wrote no record of the installed agent

#### ADDED Requirement: Claude Code definition follows R04

The system SHALL define `claude-code` with an install from an official `https` source on `claude.ai`, no change to the client, and no credential.

##### Scenario: R04 test
- WHEN the test of `internal/agent/claude` runs
- THEN it fails if `Install` fetches from another source, writes into `~/.claude`, sets a token or key variable, or passes flags that change the client; and if a domain fails `ValidateHost`

Frozen by: none (all requirements are new).

## 📝 Architecture

`cmd/orbit` → `agent` (registry) → injected into `initialize` as a function. `provision` → `policy.Allowlist`. `initialize` → `Machines.RunAsRoot` (provisioning), then `Machines.RunAs` (install). `orbstack` gains `RunAs`; `orbstacktest` records it. No new module dependency besides `cmd/orbit` → `agent`, which the Components table already lists. A row in Key decisions records the injected `Agent` function.

The handler order after this change: validate dir → nesting → create → verify mounts → provision (root) → install (agent). Each step that fails on a machine that the call created deletes it.

## 📝 Edge Cases & Failure Scenarios

- Network: the installer gets a 403 from the proxy for a host not in the list (the spike saw `api.anthropic.com`, not required). The install still succeeds; if a future installer requires a new host, `init` fails with the installer's output, and the fix is a new host with the owner's yes.
- `~/.local/bin` not in `PATH`: Ubuntu's `/etc/skel/.profile` adds it for login shells; `orbit shell` (E01-S02) must use a login shell. The install itself calls the binary by full path.
- Repeated `init --agent claude-code`: the installer runs again and updates the client. It never touches `~/.claude` (the login of E01-S03 stays).
- `#28` changes `internal/sandbox` protected paths concurrently; the implementation must rebase on it. This spec does not change mount rules.

## 📝 Risks & Impact Review

- `risk-high`: the default allowlist changes shape (Q2, decided by the owner), and a new path runs remote code in the sandbox. The code runs as `agent`, behind the firewall, so it gets no more than the agent itself has.
- `curl | bash` trusts `claude.ai` over TLS. That is the provider's documented install; R04 forbids a modified one.
- The escape suite (E01-S06) does not exist yet. The PR states that it did not run and runs `TestInitProvisionsTheSandbox` plus a new integration test for the install.
- Rollback: revert the PR; the `init` command becomes unregistered again. Existing sandboxes keep the client in the home.

## 📝 Decisions in play

D08, D16, D19, D21 (Q2 is the owner's reading of it: the Claude Code hosts apply to a sandbox of that agent), D22, R03, R04. Q1 changes `ARCHITECTURE.md` (Agent definitions, Components). The owner, Jakub Tobiasz, decided Q1–Q3 on 2026-11-11.

## 📋 Phasing

One phase, one PR: the definition is not useful without the install, and the command is registered only when both exist.

## 📋 Implementation Plan

1. `internal/agent`: `Definition`, `Registry`, `Lookup`, `Names`, `ErrUnknownAgent`, table tests (empty name, duplicate, unknown). Package `AGENTS.md` and a Task routing row in the root `AGENTS.md`.
2. `internal/agent/claude`: `Definition` and its R04 test.
3. `policy.Allowlist` with hostile-input tests; move the eight Claude Code hosts out of `defaultAllowlist` (Q2).
4. `provision.Script(defaultUID, agentDomains)`; update its tests and the call sites.
5. `orbstack.Client.RunAs` with its integration test; `orbstacktest` support.
6. `initialize`: `Input.Agent`, `Handler.Agent`, the install step, handler tests (no agent, unknown agent, install fails on a new and on an existing machine, hostile domain, a second `init` without `--agent` drops the agent domains).
7. `cmd/orbit`: build the registry, register `init` with `--agent`. Integration test: `orbit init --agent claude-code`, then `claude --version` as `agent` and the proxy probes of the Delta.
8. Docs: `ARCHITECTURE.md` (Agent definitions, Components row, Key decisions), `internal/sandbox/AGENTS.md` (`init` registered), root `AGENTS.md` project overview.
