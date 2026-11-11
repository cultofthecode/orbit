# Claude Code agent definition and its install

Issue: #17 (E01-S01-T04), story #5 (E01-S01). Depends on #16 (merged in #32) and #29 (merged in #33).
Extends: .ai/specs/2026-11-10-sandbox-provisioning.md

## 📝 TLDR

Today the `init` slice creates and provisions a sandbox, but it installs no agent, and `orbit init` is not a registered command. The allowlist of every sandbox is the fixed D21 list; D24 replaces it with a base list plus the `EgressDomains` of the agent (Q2). This spec proposes the `internal/agent` module with one definition, `claude-code`, and an `--agent` flag on `orbit init` that installs the official client as the agent user through the proxy and adds the `EgressDomains` of the definition to the allowlist. With it, story E01-S01 is complete and `orbit init` is registered.

## Resolved assumptions

This spec was written unattended. The owner, Jakub Tobiasz, decided Q1–Q3 on 2026-11-11 ([PR #34](https://github.com/cultofthecode/orbit/pull/34), [#17](https://github.com/cultofthecode/orbit/issues/17)). Q4–Q6 are the defaults of the unattended run, and the owner did not change them. No question is open.

| # | Question | Decision | Why |
|---|---|---|---|
| Q1 | Which fields does `Definition` have? Do `Start` and `LoginHint` (and `StatePaths`, `Image`) stay? | **Owner decision, 2026-11-11.** Only `Name`, `Install`, and `EgressDomains`. `Start` and `LoginHint` are dropped: Orbit does not start the agent (D19), and sign-in is `/login` in the client (D08). No `StatePaths` and no `Image` until a slice uses them. `ARCHITECTURE.md` is updated to match. | Least surface. A field without a reader is speculative. The Agent row in Components still says "image, start command"; that is drift. A later slice adds a field when it needs one (rule of Agent definitions). |
| Q2 | Where do the eight Claude Code hosts of D21 live? | **Owner decision, 2026-11-11.** Move them from `policy.defaultAllowlist` into `claude.Definition.EgressDomains`: the same eight hosts, no new host. A sandbox with `--agent claude-code` gets exactly the D21 list. A sandbox without `--agent` no longer reaches the Claude Code hosts. | It is what `ARCHITECTURE.md` (Provisioning requirements) and the done-when of #17 describe: D21 plus the `EgressDomains` of the installed agent. It narrows the no-agent sandbox and widens nothing. The default allowlist is an Ask-first surface, so the owner decided it. |
| Q3 | What does `init` do on an existing sandbox when `--agent` differs from the last run? | **Owner decision, 2026-11-11.** Every `init` rebuilds the allowlist from the `--agent` of that run. `orbit init` without `--agent` on a sandbox with Claude Code removes the Claude Code hosts; the installed client stays in the home and fails closed until `orbit init --agent claude-code` runs again. Orbit stores no record of the installed agent. | No new state (OrbStack is the state store). It fails closed. |
| Q4 | Is the client version pinned? | No pin. `Install` runs the published installer (`curl -fsSL https://claude.ai/install.sh \| bash`), which installs the current release. The client's own updater stays as it is. | `ARCHITECTURE.md` names "the published Claude Code installer". R04: Orbit does not change the client, and a pin needs a version source that the owner did not ask for. The install runs as the agent user, not as root. |
| Q5 | How does the install run as the agent user? | A new adapter method `RunAsAgent(ctx, name, script)` that runs `orb -m <name> -u agent bash -s`, as the spike did. The user is the adapter constant `orbstack.AgentUser`; no argument of the caller reaches the command line, as for `RunAsRoot`. The handler, not the definition, wraps every `Install` in one prelude: `set -euo pipefail`, then `set -a; . /etc/environment; set +a`, so a failed download fails the install and the install uses the proxy. Changed after review [5853788265](https://github.com/cultofthecode/orbit/pull/34#pullrequestreview-5853788265) (M2, M3); the first default was `RunAs(ctx, name, user, script)`. | Root never writes into the home of the agent (provisioning invariant). `runuser` from the root script would mix the two phases in one script. A user parameter would reopen the fixed-user invariant of `internal/orbstack` and give a second path to root. Without `pipefail`, `curl -fsSL … \| bash` exits 0 when `curl` fails. |
| Q6 | What happens when the install fails? | `init` fails. When the same call created the machine, it deletes it, as for a failed provisioning. An existing machine stays. | Same rule as provisioning Q4 of the prior spec: `init` leaves a complete sandbox or none. |

## 📝 Problem Statement

- Story E01-S01 requires `orbit init --agent claude-code` to give a sandbox with Claude Code installed. Tasks #14, #15, and #16 delivered the machine and the boundary; no task installs a client, so the `init` command stays unregistered (`internal/sandbox/AGENTS.md`).
- The spike installed Claude Code 2.1.322 as `agent` with the official installer through the proxy in 19 s. It needed only `claude.ai` and `downloads.claude.ai`; `claude -p` then contacts `api.anthropic.com`. `[DATA]` `.ai/analysis/2026-11-10-orbstack-isolated-machine-spike.md`, finding 4 and 5.
- D16 requires that a new agent is one self-contained definition, and no slice compares an agent name (`ARCHITECTURE.md`, Agent definitions).

## 📝 Proposed Solution

1. `internal/agent/agent.go`: `Definition{Name, Install, EgressDomains}`, `Registry` (built from definitions; refuses an empty or duplicate name), `Lookup(name)` with `ErrUnknownAgent`, and `Names()` for the flag help. `internal/agent/claude/claude.go`: `var Definition`, name `claude-code`. Neither imports a package of the repository; only the R04 test `claude_test.go` imports `internal/policy` for `ValidateHost`.
2. `policy.Allowlist(agentDomains []string) ([]string, error)`: the base list of D24 plus the domains of the agent, without duplicates, in a stable order. It refuses the whole list when one domain fails `ValidateHost`. Pure, with hostile-input tests.
3. `provision.Script(defaultUID uint32, agentDomains []string)` renders the proxy allowlist from `policy.Allowlist`. The rest of the script does not change.
4. The `init` slice: `Input.Agent` (empty means no agent). New handler field `Agent func(name string) (sandbox.Agent, error)`, injected by `cmd/orbit` from the registry, so the slice imports no other module (dependency rule 2). `sandbox.Agent` holds `Install` and `EgressDomains`. The field `ProvisionScript` becomes `func(defaultUID uint32, agentDomains []string) ([]byte, error)`. After provisioning, when an agent is set, the handler wraps `Install` in the prelude of Q5 and runs it through `Machines.RunAsAgent`. The prelude is agent-agnostic and lives in the handler; a definition never repeats it.
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

##### Scenario: The download of the installer fails
- WHEN `curl` in `Install` fails, for example because the proxy answers 403 or the host does not resolve
- THEN `init` fails, although `bash` exits 0 on empty input; a machine that the call created is deleted; an existing machine stays

#### MODIFIED Requirement: Proxy allowlist (capability `sandbox-provisioning`)

The system SHALL let the proxy open a `CONNECT` tunnel on port 443 only to a host of the base allowlist (D24) or of the `EgressDomains` of the agent of the last `init`, matched exactly and without case. Each `init` rebuilds the allowlist from its own `--agent` (Q3).

##### Scenario: Allowed and refused hosts
- WHEN `agent` asks the proxy for `CONNECT github.com:443`, `CONNECT example.org:443`, and `CONNECT github.com:22`
- THEN the first succeeds and the other two get 403

##### Scenario: Hostile domain in a definition
- WHEN a definition holds `*.anthropic.com`, `1.2.3.4`, `localhost`, `Claude.AI`, or `example.com:22`
- THEN `policy.Allowlist` refuses the whole list and `init` fails before it asks OrbStack anything

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

Frozen by: none. `Proxy allowlist` replaces the requirement of the same name in `.ai/specs/capabilities/sandbox-provisioning.md` (D21 → D24); the other requirements are new.

## 📝 Architecture

`cmd/orbit` → `agent` (registry) → injected into `initialize` as a function. `provision` → `policy.Allowlist`. `initialize` → `Machines.RunAsRoot` (provisioning), then `Machines.RunAsAgent` (install). `orbstack` gains `RunAsAgent` with the fixed user `AgentUser`; `orbstacktest` records it as `OpRunAsAgent`. The adapter imports no other package, so a test in `cmd/orbit` checks that `orbstack.AgentUser` equals `provision.AgentUser`. No new module dependency besides `cmd/orbit` → `agent`, which the Components table already lists. A row in Key decisions records the injected `Agent` function.

The handler order after this change: resolve agent (`Agent`) → validate dir → build the provisioning script (`ProvisionScript`, which runs `policy.Allowlist`) → nesting → create → verify mounts → provision (root) → install (agent). The script is built after the directory is validated because it needs the uid of the user; neither step asks OrbStack anything. So an unknown agent, a refused directory, and a hostile domain all fail before the first `orb` command, and no machine is created for them. Each later step that fails on a machine that the call created deletes it.

## 📝 Edge Cases & Failure Scenarios

- Network: the installer gets a 403 from the proxy for a host not in the list (the spike saw `api.anthropic.com`, not required). The install still succeeds; if a future installer requires a new host, `init` fails with the installer's output, and the fix is a new host with the owner's yes.
- `~/.local/bin` not in `PATH`: Ubuntu's `/etc/skel/.profile` adds it for login shells; `orbit shell` (E01-S02) must use a login shell. The install itself calls the binary by full path.
- Repeated `init --agent claude-code`: the installer runs again and updates the client. Orbit's `Install` command writes nothing into `~/.claude`; the files that the official installer writes are the provider's (R04). The spike observed only `~/.local/share/claude` and `~/.local/bin/claude`; it did not check `~/.claude` or `~/.claude.json`.
- Mount rules: this spec does not change them.

## 📝 Risks & Impact Review

- `risk-high`: the default allowlist changes shape (Q2, decided by the owner), and a new path runs remote code in the sandbox. The code runs as `agent`, behind the firewall, so it gets no more than the agent itself has.
- `curl | bash` trusts `claude.ai` over TLS. That is the provider's documented install; R04 forbids a modified one.
- The escape suite (E01-S06) does not exist yet. The PR states that it did not run and runs `TestInitProvisionsTheSandbox` plus a new integration test for the install.
- The implementation PR is `risk-high` and inherits the gate of #17: a unit test with hostile input for the changed rule (`policy.Allowlist`), the escape suite or a statement that it did not run, a review by a second person, and QA evidence as a terminal transcript of `orbit init --agent claude-code` on a named OrbStack version.
- Rollback: revert the PR; the `init` command becomes unregistered again. Existing sandboxes keep the client in the home.

## 📝 Decisions in play

D08, D16, D19, D21 superseded by D24 (recorded in the product brief from the owner's Q2), D22, R03, R04. Q1 changes `ARCHITECTURE.md` (Agent definitions, Components). The owner, Jakub Tobiasz, decided Q1–Q3 on 2026-11-11.

## 📋 Phasing

One phase, one PR: the definition is not useful without the install, and the command is registered only when both exist.

## 📋 Implementation Plan

1. `internal/agent`: `Definition`, `Registry`, `Lookup`, `Names`, `ErrUnknownAgent`, table tests (empty name, duplicate, unknown). Package `AGENTS.md` and a Task routing row in the root `AGENTS.md`.
2. `internal/agent/claude`: `Definition` and its R04 test.
3. `policy.Allowlist` with hostile-input tests; move the eight Claude Code hosts out of `defaultAllowlist` (Q2, D24).
4. `provision.Script(defaultUID, agentDomains)`; update its tests and the call sites.
5. `orbstack.Client.RunAsAgent` with the constant `AgentUser`, a unit test of its argument list (`orb -m NAME -u agent bash -s`, no caller value), the prefix rule (`ErrForeignMachine`), and an integration test that `id -un` prints `agent`; `orbstacktest` records `OpRunAsAgent` with the same prefix rule.
6. `initialize`: `Input.Agent`, `Handler.Agent`, `ProvisionScript(defaultUID, agentDomains)`, the order of Architecture, the install prelude, and the install step. Handler tests: no agent; unknown agent and hostile domain record no OrbStack call; install fails on a new and on an existing machine; the script passed to `RunAsAgent` starts with the prelude and then holds `Install` unchanged; a second `init` without `--agent` drops the agent domains.
7. `cmd/orbit`: build the registry, register `init` with `--agent`, and a test that `orbstack.AgentUser == provision.AgentUser`. Integration tests: `orbit init --agent claude-code`, then `claude --version` as `agent` and the proxy probes of the Delta; and a test definition whose `Install` is `curl -fsSL https://example.org/install.sh | bash` (a host the proxy refuses), for which `init` fails and deletes the new machine.
8. Docs:
   - `ARCHITECTURE.md`: Agent definitions, Components row, Key decisions (the injected `Agent` function; D24 in the egress row), and the Provisioning requirements bullet: *the default allowlist is the base list of D24, plus the `EgressDomains` of the agent of the last `init`*.
   - `internal/policy/AGENTS.md`: *`Allowlist(nil)` is exactly the base list of D24; `Allowlist` refuses the whole list when one agent domain fails `ValidateHost`.* The doc comments of `policy.go` and the test that checks the list name D24.
   - `internal/provision/AGENTS.md`: `Script(defaultUID, agentDomains)` in Layout.
   - `internal/orbstack/AGENTS.md`: `RunAsAgent` in Layout; the invariant that `RunAsAgent` runs `orb -m NAME -u agent bash -s`, its user is fixed, and no argument of the caller reaches the command line; the prefix rule names `RunAsAgent`.
   - `.ai/specs/capabilities/sandbox-provisioning.md`: the `Proxy allowlist` requirement of the Delta.
   - `internal/sandbox/AGENTS.md` (`init` registered), root `AGENTS.md` (project overview; brief D24 in the allowlist routing row).
   - The product brief already has D24 (this spec PR).
