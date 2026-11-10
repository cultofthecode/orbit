# Orbit — product brief

- Date: 2026-11-10
- Mode: own; Owner: Jakub Tobiasz; Pass: Quick pass
- Evidence basis: one owner request (2026-11-10) and the public Docker Sandboxes documentation, checked the same day. No interviews and no usage data. An earlier ChatGPT design for an OrbStack-native tool (`orb-sbx`), pasted by the owner the same day and cut off at its section 8, is a third source; it is a design proposal, not evidence about users. The most consequential untested belief is A01: container isolation is a sufficient boundary.
- Coverage: 50 claims — 45 sourced (interview 0, data 0, document 34, product 0, benchmark 11), 0 synthetic, 5 assumed; 0 entries on the collection plan
- Synthetic hypotheses outside Coverage: 0
- Definition of Ready signed by: not yet signed. Missing: the spike for A05 and A02. No proposal rows are left.
- Sources: owner request of 2026-11-10 (conversation, not stored in the repository); https://docs.docker.com/ai/sandboxes/ and its Architecture, Security model, Isolation layers, Default security posture, Manage credentials, and Claude Code pages; the `orb-sbx` design dump (pasted in the conversation of 2026-11-10, not stored in the repository); `ARCHITECTURE.md`

## Decision summary

The owner decided to build Orbit: a Go CLI that runs coding agents in sandboxes on OrbStack, as the OrbStack counterpart of Docker Sandboxes (D02, D13). That decision is agreed. The sandbox is one OrbStack isolated machine per agent (D12). Root in that machine owns the firewall and the egress proxy; the agent is an unprivileged user with rootless Docker and no sudo (D14).

The owner uses OrbStack on macOS and reports that Docker Sandboxes do not support it (see Problems). There is no workaround on record.

The first scope is one complete job: start Claude Code in a sandbox that sees one project directory, uses the subscription of the user, and cannot reach the rest of the host (Scope, R01, R03, R06).

Three unknowns can change the design. An isolated machine shares the OrbStack kernel, which is weaker than the microVM of Docker Sandboxes; the owner accepted that limit (A01, D12). The egress design of D14 is decided but not yet proven on a real machine (A05). The subscription login inside the sandbox is untested (A02).

The earlier `orb-sbx` design optimizes for the opposite goal: reuse of the host engine, with an opt-in host Docker socket and SSH agent forwarding. Where it conflicts with today's request, this brief follows the request (D09) and keeps the conflicts as questions (Q05–Q07).

Next action: a spike tests A05 and A02 and answers Q09. No dates are set.

## Vision

- A developer runs an autonomous coding agent on the container engine they already use, and the agent cannot touch anything on the host except the project it was given. This is the intended direction, not a demonstrated benefit. `[DOCUMENT]` owner request, 2026-11-10

## Target group and stakeholders

- Customer (pays): none. No pricing or licensing decision exists. `[ASSUMPTION]` no material
- User (uses): the owner, a developer on macOS with OrbStack who has a Claude subscription. Other users are not evidenced. `[DOCUMENT]` owner request, 2026-11-10
- Stakeholders (decides, blocks, operates): the owner only. `[DOCUMENT]` owner request, 2026-11-10
- Decider for scope decisions: Jakub Tobiasz, owner

## Problems, with evidence

- The owner wants to use Docker Sandboxes but reports that they do not support OrbStack, the engine the owner runs on macOS. One account. The claim is not verified against the Docker documentation (A03). `[DOCUMENT]` owner request, 2026-11-10
- The owner does not want an agent to escape to the host system. `[DOCUMENT]` owner request, 2026-11-10
- The owner wants the agent to run on the existing Claude subscription, not on a separate API key. `[DOCUMENT]` owner request, 2026-11-10

## Product and how it stands out

- What it is: a Go CLI that creates and manages sandboxed coding agents on OrbStack; see Scope. `[DOCUMENT]` owner request, 2026-11-10
- What makes it different: it is built for OrbStack, where `sbx` does not fit; Docker's own setups already have `sbx` (D13). It does not match the microVM isolation of `sbx` (A01).

| reference | what it does well | where it falls short for our users | checked on | link |
|---|---|---|---|---|
| Docker Sandboxes (`sbx`) | One microVM with its own kernel per sandbox; the agent has sudo and a private Docker Engine. `[BENCHMARK]` | Not usable with OrbStack, per the owner (A03). | 2026-11-10 | https://docs.docker.com/ai/sandboxes/security/isolation/ |
| Docker Sandboxes — network | All outbound TCP goes through a host proxy with a deny-by-default policy; UDP and ICMP are blocked. `[BENCHMARK]` | — | 2026-11-10 | https://docs.docker.com/ai/sandboxes/security/defaults/ |
| Docker Sandboxes — workspace | Three modes: direct mount at the same absolute path, clone mode (read-only source, private clone), and mountless. `[BENCHMARK]` | — | 2026-11-10 | https://docs.docker.com/ai/sandboxes/architecture/ |
| Docker Sandboxes — credentials | The host proxy replaces a sentinel with the real credential; the value never enters the sandbox. `[BENCHMARK]` | — | 2026-11-10 | https://docs.docker.com/ai/sandboxes/security/credentials/ |
| Docker Sandboxes — Claude Code | Subscription users run `/login` inside the sandbox; the default start command is `claude --dangerously-skip-permissions`; host `~/.claude` is not shared. `[BENCHMARK]` | — | 2026-11-10 | https://docs.docker.com/ai/sandboxes/agents/claude-code/ |

## Goals and success criteria

- Business goal: none set. The product is the owner's own tool (D01).
- User outcome: the owner runs Claude Code in a sandbox on OrbStack with one command, and a documented set of escape attempts fails. `[DOCUMENT]` owner request, 2026-11-10
- Primary metric, baseline today, threshold, date: not set. No baseline exists.
- What must not get worse: the host stays unreachable outside the shared workspace and the credentials the user opted in to (R01, R03, R06).

## Scope

- **Now:** on macOS with OrbStack, start Claude Code in a sandbox for one project directory, authenticate with the subscription of the user (D08), and stop, list, and remove sandboxes. Outbound network is deny-by-default with a manageable allowlist (R01, R03, R06, R07, D12, D14).
- **Later:** one Git worktree per agent (`--branch`), clone mode, and mountless mode; extra mounts; a project configuration file; Codex and other agents (D16); a token-based sign-in (Q12); resource limits; audit logging; an MCP gateway. A GitHub App identity for the agent (D10).
- **Not doing:** see Non-goals

## Domain glossary

| Term | Meaning | Owned by | Visible to |
|---|---|---|---|
| Sandbox | The isolated environment that Orbit creates for one agent. With the first backend it is one OrbStack isolated machine that mounts only the workspace. | `sandbox` module | user |
| Backend | OrbStack, which provides the isolated machines. Orbit drives it through the `orb` CLI. | `orbstack` package | user |
| Agent | The coding agent that runs in a sandbox, for example Claude Code. | `agent` module | user |
| Workspace | The host directory that the user shares with a sandbox. | `sandbox` module | user |
| Network policy | The allow and deny rules that decide which destinations a sandbox can reach. | `policy` module | user |
| Egress proxy | The only network route out of a sandbox. It enforces the network policy. | `proxy` module | internal |

## Key flows

- Current state: the owner cannot run Docker Sandboxes on OrbStack; no other flow is on record. `[DOCUMENT]` owner request, 2026-11-10
- Future state (D08, D12, D14, D16; command names are an agent proposal): `orbit run claude [path]` creates the sandbox if it does not exist, shares only `path` (default: the current directory), and starts Claude Code. On first use the user logs in with `/login`; the login persists in the state volume of the sandbox. `orbit ls`, `orbit shell <name>`, `orbit logs <name>`, `orbit stop <name>`, and `orbit rm <name>` manage the lifecycle. `orbit policy ls|allow|deny` manages the allowlist. `[ASSUMPTION]` agent proposal, modeled on `sbx` and on the command list of the `orb-sbx` design

## Business rules

| Id | Rule | Applies to | Source | Status | Review by | Required path to change | Owner | Supersedes |
|---|---|---|---|---|---|---|---|---|
| R01 | A sandbox never gets the engine socket of the host, a privileged mode, or a host namespace. | every sandbox | `[DOCUMENT]` owner request ("should not let the agent escape"); `[BENCHMARK]` Isolation layers | active | unknown | owner approves a superseding row | Jakub Tobiasz | none |
| R02 | Only the workspace paths that the user passes are shared from the host. The home directory, `~/.claude`, SSH keys, and cloud credentials are never mounted. | every sandbox | `[DOCUMENT]` owner request ("that directory only"); `[BENCHMARK]` Security model | superseded by R06 | unknown | owner approves a superseding row | Jakub Tobiasz | none |
| R03 | All outbound traffic of a sandbox passes through the egress proxy. A destination with no allow rule is blocked. Sandboxes cannot reach each other. | every sandbox | `[DOCUMENT]` owner request ("take the architecture of Docker sandboxes"); `[BENCHMARK]` Default security posture | active | unknown | owner approves a superseding row | Jakub Tobiasz | none |
| R04 | Orbit runs the official agent client unmodified. It does not remove or restrict an authentication method of the client. It does not collect, store, or intermediate the credentials of an agent, and it does not send requests to the provider with them. Usage is billed to the user's own plan. | credentials, every agent | `[DOCUMENT]` owner decision, 2026-11-10 (unmodified client); `[BENCHMARK]` https://code.claude.com/docs/en/legal-and-compliance (the other clauses, added by the agent from the provider terms) | active | when the provider terms change | owner approves a superseding row | Jakub Tobiasz | none |
| R05 | The mandatory path uses only the Docker Engine API. An engine-specific feature may only add hardening. | engine support | `[DOCUMENT]` owner request ("reimplement them agnostically") | superseded by R07 | unknown | owner approves a superseding row | Jakub Tobiasz | none |
| R06 | Only the workspace paths that the user passes are shared from the host. A host credential enters a sandbox only when the user opts in to that one credential, and in the narrowest form available: a forwarded agent socket or a short-lived token, not a key file. The set is configurable per project. The home directory and `~/.claude` are never mounted. | every sandbox | `[DOCUMENT]` owner decision, 2026-11-10 (answer to Q06) | active | unknown | owner approves a superseding row | Jakub Tobiasz | R02 |
| R07 | Orbit supports OrbStack only. OrbStack-specific code lives in one package; slices use it through narrow interfaces, so that handler tests run without OrbStack. | backend support | `[DOCUMENT]` owner decision, 2026-11-10 (D13) | active | unknown | owner approves a superseding row | Jakub Tobiasz | R05 |

## Non-goals

| Id | We are not building | Why | Owner | Status | Review by | Required path to change | Source | Supersedes |
|---|---|---|---|---|---|---|---|---|
| N01 | Cloud sandboxes and a sandboxes API or SDK | The request is a local CLI. | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[DOCUMENT]` owner request, 2026-11-10 | none |
| N02 | Organization-wide governance of policies | Single-developer tool. | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[DOCUMENT]` owner request, 2026-11-10 | none |
| N03 | A private Docker Engine and sudo inside the sandbox, in the first scope | Both need privileges that weaken a container boundary (D06). | pending | superseded by D12 | — | owner approves a superseding row | `[BENCHMARK]` Isolation layers | none |
| N04 | Kits, shared skills store, and MCP gateway, in the first scope | Not needed for the first job. | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[BENCHMARK]` Security model | none |
| N05 | Windows hosts | The owner works on macOS. | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[DOCUMENT]` owner request, 2026-11-10 | none |
| N06 | Support for other engines or backends (Docker Desktop, Colima, Linux Docker Engine, Apple `container`) | Docker's own setups already have `sbx`. | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[DOCUMENT]` owner decision, 2026-11-10 | none |
| N07 | Access from a sandbox to services on the host engine or on the macOS host | Not needed (D15). | Jakub Tobiasz | active | unknown | owner approves a superseding row | `[DOCUMENT]` owner decision, 2026-11-10 | none |

## Decisions

| Id | Date | Decision | Why | Owner | Status | Review by | Required path to change | Source | Supersedes |
|---|---|---|---|---|---|---|---|---|---|
| D01 | 2026-11-10 | Build Orbit as an engine-agnostic replacement for local Docker Sandboxes, and reuse their architecture. | Docker Sandboxes do not fit the owner's OrbStack setup. | Jakub Tobiasz | superseded by D13 | unknown | owner | `[DOCUMENT]` owner request | none |
| D02 | 2026-11-10 | Orbit is a CLI written in Go. | Owner's choice. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner request | none |
| D03 | 2026-11-10 | The code uses a vertical slice architecture adjusted to Go conventions; see `ARCHITECTURE.md`. | Owner's choice, from the AI-friendly architecture guide. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner request | none |
| D04 | 2026-11-10 | The agent authenticates with the user's subscription. An API key is not required. | Owner's choice. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner request | none |
| D05 | 2026-11-10 | The first version supports Claude Code and Codex, on OrbStack on macOS. | Owner: both agents are a must for the first version. | Jakub Tobiasz | superseded by D16 | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | none |
| D06 | 2026-11-10 | The first isolation backend is a hardened container on the engine, not one microVM per sandbox. | It is the only boundary every Docker-compatible engine offers (R05). Alternative considered: a microVM per sandbox as in `sbx`, which is stronger but engine-specific. The `orb-sbx` design made the same choice and names the same limit: containers share a kernel. Agent recommendation; see A01, Q02. | pending | superseded by D12 | — | owner | `[BENCHMARK]` Architecture, comparison table | none |
| D07 | 2026-11-10 | The egress proxy runs as an Orbit container beside the sandbox, on an internal network. | A host process is not reachable from an internal network in an engine-neutral way. `sbx` runs its proxy on the host. Agent recommendation. | pending | superseded by D12; see Q08 | — | owner | `[BENCHMARK]` Architecture, networking | none |
| D08 | 2026-11-10 | Authentication. The user signs in with `/login` inside the sandbox, once per sandbox; the login stays on the disk of that machine. A token path (`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`) is not in the first version; see Q12. Orbit never asks for, stores, or changes a credential of the agent, and never calls the provider itself. | Checked against the Claude Code legal page on 2026-11-10. It allows a user to sign in to the unmodified binary with their own subscription, also where a platform hosts it. It forbids developers to collect, store, or intermediate Claude.ai credentials, and to restrict a built-in authentication method. So Orbit does not keep the token in a Keychain entry of its own, which the earlier candidate did. A read-only mount of host `~/.claude` cannot authenticate on macOS (the login is in the Keychain) and stays forbidden by R06. `sbx` uses the same sign-in: `/login` inside the sandbox with the unmodified client, and it does not use `setup-token`. `sbx` goes further and keeps the session token on the host behind its proxy; the Docker documentation does not say on what basis, so that part is not a precedent for Orbit (https://docs.docker.com/ai/sandboxes/get-started/, https://docs.docker.com/ai/sandboxes/configuration/credentials/). Agent reading of the terms, not legal advice; see Q11. Confirmed by the owner. | Jakub Tobiasz | active | when A02 is tested or Q12 is answered | owner | `[BENCHMARK]` https://code.claude.com/docs/en/legal-and-compliance; https://code.claude.com/docs/en/authentication | none |

| D09 | 2026-11-10 | Where the `orb-sbx` design conflicts with the request of 2026-11-10, the request wins: the name is Orbit, the tool is engine-agnostic, and isolation comes before reuse of the host engine. The first scope has no engine socket mode and no SSH agent forwarding. | The request is later and explicit ("should not let the agent escape", "agnostically"). Alternative considered: the trusted `--docker host` mode of the design. Agent reading of precedence; see Q05–Q07. | pending | superseded by D10, D12, D13 | when Q05–Q07 are answered | owner | `[DOCUMENT]` owner request; `orb-sbx` design, sections 4–6 | none |
| D10 | 2026-11-10 | The agent can commit and push. Git identity and a Git credential (SSH agent or HTTPS token) are opt-in per R06. A GitHub App that gives the agent its own identity is a later feature. | Owner's choice: host credentials are acceptable when minimized and easy to customize. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | none |
| D11 | 2026-11-10 | A sandbox may reach services on the host engine, such as PostgreSQL and Redis. | Owner's choice. The mechanism is open: an explicit opt-in per network or per service is the agent proposal, so that R03 stays deny-by-default. | Jakub Tobiasz | superseded by D15 | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | none |
| D12 | 2026-11-10 | The first sandbox backend is one OrbStack isolated machine per sandbox (`orb create --isolated`), with only the workspace mounted. The agent gets sudo and its own Docker Engine inside the machine. Orbit offers no host engine socket. No microVM. | Owner's choice among five options. It gives the agent Docker without the host socket. Alternatives considered: no engine, gVisor, a filtered socket proxy, Apple `container`. Accepted limit: all OrbStack machines share one kernel, and OrbStack does not recommend them against code that actively tries to exploit the kernel. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10; `[BENCHMARK]` https://docs.orbstack.dev/machines/isolated | D06, D07, N03 |
| D13 | 2026-11-10 | Orbit targets OrbStack only. It is the OrbStack counterpart of `sbx`, not an engine-agnostic tool. | Owner: Docker already has `sbx`. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | D01 |
| D14 | 2026-11-10 | Egress enforcement: root in the isolated machine owns the firewall and the egress proxy. The agent is an unprivileged user with rootless Docker and no sudo. The machine is created with `--isolate-network`. | Deny-by-default egress (R03) must survive an agent that tries to remove it. This amends D12: the agent does not get sudo. Agent proposal, accepted by the owner. Not yet proven (A05). | Jakub Tobiasz | active | when A05 is tested | owner | `[DOCUMENT]` owner decision, 2026-11-10 | none |
| D15 | 2026-11-10 | A sandbox does not need to reach services on the host engine. Orbit builds no feature for it. What an isolated machine can reach by default is verified and documented. | Owner's choice. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | D11 |
| D16 | 2026-11-10 | The first version supports Claude Code only. Codex comes later. The agent setup is designed so that a new agent is one self-contained definition; see `ARCHITECTURE.md`, Agent definitions. | Owner's choice. | Jakub Tobiasz | active | unknown | owner | `[DOCUMENT]` owner decision, 2026-11-10 | D05 |

## Riskiest assumptions

| Id | Assumption | Importance | Evidence today | If false | Smallest test | Owner | By when | Result |
|---|---|---|---|---|---|---|---|---|
| A01 | An OrbStack isolated machine, which shares the kernel of the OrbStack VM, is a sufficient boundary for the owner. `[ASSUMPTION]` owner decision D12 | high | OrbStack documents isolated machines as fit for AI agents that do not try to break out, and as no substitute for a full VM against malicious code. | D12 changes to a microVM backend. | An escape test suite runs in an isolated machine. | pending | unknown | accepted untested (D12) |
| A02 | Claude Code authenticates with `/login` in an isolated machine behind the egress allowlist. `[ASSUMPTION]` agent proposal, D08 | high | `/login` in a container is documented by Anthropic and used by `sbx`. Not tried in an isolated machine. | D08 changes. | Spike: run `/login` in an isolated machine; record the domains that the login and the client need. | pending | unknown | untested |
| A03 | Docker Sandboxes cannot be used with OrbStack. | medium | The owner's account only. The `orb-sbx` design says otherwise: the two run side by side as independent engines, and the limit is that a sandbox cannot see OrbStack containers, images, volumes, or networks. Not checked against the Docker documentation. | The reason for D01 weakens; the owner decides whether to continue. | Install `sbx` beside OrbStack and record what fails. | pending | unknown | untested |
| A04 | Subscription credentials can later be injected outside the sandbox, as `sbx` does for OAuth. | medium | The Claude Code legal page forbids developers to intermediate Claude.ai credentials. | — | None. | pending | — | refuted by the provider terms (R04); not pursued |
| A05 | In an OrbStack isolated machine, an unprivileged agent user with rootless Docker cannot bypass a root-owned firewall and proxy, and the agent stays usable without sudo. `[ASSUMPTION]` agent proposal, D14 | high | None. Not tried. | D14 changes; R03 may need a weaker form. | Spike: build the machine, then try to reach a blocked destination from the agent user, from a rootless container, and over DNS. Install the tools Claude Code needs without sudo. | pending | unknown | untested |

## Kill criteria

Not decided. A natural criterion is A03: if Docker Sandboxes work beside OrbStack, the owner decides whether Orbit continues.

## Hypotheses to test

None. No synthetic material exists.

## Open questions

| Id | Question | Blocking | Who can answer | Status |
|---|---|---|---|---|
| Q01 | What did the ChatGPT conversation decide? | — | owner | closed: the owner pasted it on 2026-11-10 (cut off at section 8). Conflicts are recorded in D09 and Q05–Q07. |
| Q02 | Container or microVM as the boundary? Checked 2026-11-10: OrbStack runs everything in one VM with one kernel and has no nested KVM on Apple Silicon, so Kata and Firecracker cannot run inside it (https://docs.orbstack.dev/machines/, https://docs.orbstack.dev/architecture). A microVM per sandbox on macOS needs a backend outside OrbStack, for example Apple `container` (macOS 26, Apple silicon). `[BENCHMARK]` | — | owner | closed by D12: no microVM. The boundary is an isolated machine with a shared kernel, and the owner accepted that limit. |
| Q03 | How is Orbit distributed (GitHub Releases, Homebrew tap)? | no | owner | open |
| Q04 | Which agents follow Claude Code? Codex is the first candidate (D16). | no | owner | open |
| Q05 | Does Orbit get an opt-in trusted mode that gives the agent the host engine socket (`--docker host` in the `orb-sbx` design)? It contradicts R01 and needs a superseding row. | — | owner | closed by D12: no host socket; the agent gets its own engine inside its machine. |
| Q06 | How does the agent push to Git remotes? The `orb-sbx` design forwards the SSH agent, which contradicts R02. | — | owner | closed: D10, R06 |
| Q07 | May a sandbox reach services on the host engine (PostgreSQL, Redis), which the `orb-sbx` design calls its biggest advantage? If yes, through which policy rule? | — | owner | closed: yes, D11. The mechanism is still open. |
| Q08 | How does Orbit enforce deny-by-default egress (R03) in an isolated machine? | — | owner | closed by D14. Feasibility is tracked as A05. |
| Q09 | What can an isolated machine reach by default: containers on the OrbStack host engine, other machines, the macOS host? Verify with and without `--isolate-network` and document the result in `ARCHITECTURE.md`. | no | spike | open |
| Q10 | How does Codex authenticate with a ChatGPT subscription in a sandbox, and what do OpenAI's terms allow? | no — Codex is later scope (D16) | research | open |
| Q11 | If Orbit is distributed to other people, does it count as a product that runs Claude Code? The Claude Code legal page then requires the Commercial Terms, and forbids the Claude Code name or logo in the product name. For the owner's own use this does not apply. Anthropic names its sales contact for questions on permitted authentication. | no for the owner's own use; yes before a public release | owner, Anthropic | open |
| Q12 | May Orbit pass a user-set `CLAUDE_CODE_OAUTH_TOKEN` through to the client, or keep a subscription token on the host as `sbx` does? Optional: ask Anthropic through the contact on its legal page. | no — later scope | owner, Anthropic | open |

## Definition of Ready addendum (own)

Problems and Target group rest on one owner account, which is enough for the owner's own tool but not for a wider audience. The active rows (D02–D04, D08, D10, D12–D16, R01, R03, R04, R06, R07, N01, N02, N04–N07) are ready for implementation planning. This brief is ready for: the spike for A05, A02, and Q09, and architecture review. It is not ready for a backlog until A05 holds.

## Collection plan

None. The open questions need owner decisions, not new material.
