# AGENTS.md — orbit

## Project overview

Orbit is a Go CLI that runs coding agents in sandboxes. It is the OrbStack counterpart of local Docker Sandboxes; other engines are a non-goal. A sandbox is one OrbStack isolated machine in which root owns the firewall and the egress proxy, and the agent is an unprivileged user with rootless Docker. The first version runs Claude Code on OrbStack on macOS, with the official client, unmodified, and the subscription of the user. More agents are added as agent definitions. A sandbox belongs to one project directory and shares only that directory; agents and Git worktrees run inside it. The host SSH agent is forwarded as a socket unless the user passes `--no-ssh-forwarding`. The agent must not reach the rest of the host. The user today is the owner. A sandbox runs Ubuntu 26.04 by default, or Debian 13 trixie with `--distro debian`; Alpine comes in 0.2. The code so far is the CLI in `cmd/orbit`, with the commands `orbit init`, `orbit shell`, `orbit stop`, `orbit start`, `orbit destroy`, and `orbit ls`; the OrbStack adapter in `internal/orbstack`; the sandbox module in `internal/sandbox`, with the `init` slice that creates and provisions the machine and installs the agent of `--agent`, and forwards the host SSH agent unless `--no-ssh-forwarding` is passed, the `shell` slice, which opens a login shell as the agent user in a running, provisioned sandbox, the `stop` slice, the `destroy` slice, which deletes the sandbox of the current directory after a confirmation, the `start` slice, the only path that starts a machine, after the host-side checks of `init`, and the `list` slice (`orbit ls`), which lists every sandbox with the state and the project directory that OrbStack stores for it and starts nothing; the agent module in `internal/agent`, with the Claude Code definition in `internal/agent/claude`; and the pure modules `internal/policy`, `internal/proxy`, and `internal/provision`, which decide what root sets up in it. Distribution is decided (D27): a Homebrew tap, `cultofthecode/homebrew-tap`, from tagged GitHub Releases, for Macs with Apple Silicon, specified in `.ai/specs/2026-11-11-homebrew-distribution.md`; nothing is deployed yet.

## Task routing

Before you work on a task of a given kind, read the files in **Read first**. Locations under `internal/` are planned in `ARCHITECTURE.md`; `internal/sandbox`, `internal/orbstack`, `internal/agent`, `internal/policy`, `internal/proxy`, and `internal/provision` exist. When a module exists, give it its own row and a short `AGENTS.md` with its invariants.

| When the task involves… | Read first | Key rules |
|---|---|---|
| Product scope, rules, non-goals | `.ai/specs/product-brief.md` | Active Business rules, Non-goals, and Decisions are a protected contract. A change needs a superseding row that the owner approves. Rows with status `proposal` are not decided. |
| Delivery process, labels, QA gate, claims | `SDLC.md` | Follow the ticket lifecycle and the label state machine. Never apply `qa-approved` from an authoring task. |
| Code review | `CODE_REVIEW.md` | `om-code-review` and `om-auto-review-pr` apply it automatically. |
| System structure, module boundaries | `ARCHITECTURE.md` | Follow the dependency rules. A new module or a new cross-module dependency needs a row in Key decisions. |
| Sandbox boundary: mounts, machine isolation settings, provisioning (agent user, firewall, proxy), network policy, credentials | `ARCHITECTURE.md` (System context, Provisioning requirements), `.ai/analysis/2026-11-10-orbstack-isolated-machine-spike.md`, brief rules R10, R03, R07, R08, R09, and D14 | Always `risk-high`. Keep the decision in a pure function with hostile-input tests. Run the escape suite (`-run '^TestEscape'`). |
| A new agent, or the agent registry | `internal/agent/AGENTS.md`, `ARCHITECTURE.md` (Agent definitions), brief rules R04 and D26 | Always `risk-high`. One package under `internal/agent/` with one `Definition` and its R04 test, registered in `cmd/orbit`. No slice compares an agent name. A new egress domain needs the owner's yes. Check the terms of the provider before you add a credential path. |
| A new CLI operation | `ARCHITECTURE.md` (Slice anatomy) | One slice is one package: `contract.go`, `handler.go`, `command.go`, `handler_test.go`. Write the contract first. Copy the closest existing slice. |
| The sandbox module: machine name, mount rules, the `init` slice | `internal/sandbox/AGENTS.md` | Always `risk-high`. Pass only canonical paths to the mount rules. The slice package is `initialize`. |
| Provisioning: agent user, firewall rules, proxy units, toolchain, the root script | `internal/provision/AGENTS.md`, `.ai/specs/2026-11-10-sandbox-provisioning.md` | Always `risk-high`. The script is idempotent and fail-closed; root never writes into a home. Run `go test -tags integration -run TestInitProvisionsTheSandbox ./cmd/orbit/`. |
| The default allowlist and host validation | `internal/policy/AGENTS.md`, brief D26 | Always `risk-high`. A new default host needs the owner's yes. |
| tinyproxy configuration | `internal/proxy/AGENTS.md`, brief D22 | Always `risk-high`. Every host passes `policy.ValidateHost`. |
| OrbStack access | `internal/orbstack/AGENTS.md`, https://docs.orbstack.dev/machines/isolated | OrbStack-specific code lives only in `internal/orbstack`. Only `cmd/orbit` imports an adapter; depguard enforces it. Name every machine that Orbit creates with the Orbit prefix. |
| Tests | `ARCHITECTURE.md` (Testing strategy) | Tests sit next to the code. Tests that need a real OrbStack carry the `integration` build tag. Do not weaken a test to make it pass. |
| Pipeline configuration | `.ai/agentic.config.json`, `.ai/trackers/github.md`, `.ai/browsers/agent-browser.md` | Change the config and `SDLC.md` together. Run `om-setup-agent-pipeline` again when the toolchain or the labels change. |
| Specs and design docs | `.ai/specs/` | Filenames: `{YYYY-MM-DD}-{kebab-case-title}.md`. Handoff briefs go under `.ai/specs/briefs/`. |
| CI | `.github/workflows/ci.yml`, `.github/workflows/darwin.yml`, `.ai/specs/2026-11-11-ci-pipeline.md` | `ci.yml` runs the gate on GitHub-hosted `ubuntu-24.04` as the check `gate`; `darwin.yml` runs the unit tests on `macos-26` by hand and weekly. CI runs no integration tests. Pin every action by full commit SHA with the version in a comment; keep `contents: read`; no secrets; no self-hosted runner. |
| Releases, tags, GoReleaser, release notes | `RELEASING.md`, `.ai/specs/2026-11-11-homebrew-distribution.md` | Always `risk-high` (supply chain). Never create a tag or release from an authoring task. Pin every action by SHA. The `tap` job reads the secret `TAP_GITHUB_TOKEN` of the `release` environment: a fine-grained PAT limited to `cultofthecode/homebrew-tap` with contents and pull requests write, created and stored by the owner; never write a token value, and add no GitHub App. Run `.github/scripts/check-release-tag_test.sh` after a change to the tag check, and `.github/scripts/bump-tap-formula_test.sh` after a change to the formula bump. |

## Code conventions

- Standard Go style: `gofmt`, short lowercase package names, errors wrapped with `%w`.
- One level of abstraction per function. Split a complicated function into well-named functions.
- Doc comments on packages and exported identifiers. No comments inside function bodies.
- No abstraction for single-use code, and no option that nobody asked for.

## Validation commands

The gate is active. The same commands are in `validation.commands` in `.ai/agentic.config.json` and in `SDLC.md`. Run them from the repository root, in this order:

```sh
test -z "$(gofmt -l .)"
go vet ./...
golangci-lint run
go test ./...
go build ./...
```

Integration tests and the escape suite need a running OrbStack: `go test -tags integration -p 1 -timeout 60m ./...`. They are not part of the default gate, and CI does not run them: they run by hand on a Mac with OrbStack (`SDLC.md`, Validation gate). CI (`.github/workflows/ci.yml`) runs the five commands above on every PR and on every push to `main`, then the darwin lint, vet, and build pass. Run the packages one at a time (`-p 1`): `init` refuses every directory while another machine's stored mount passes through a symlink, and `internal/orbstack` creates such a machine on purpose. A `risk-high` change to the sandbox boundary runs the escape suite on both distributions and pastes both outputs in the PR. Ubuntu (default): `go test -tags integration -p 1 -timeout 60m -run '^TestEscape' -v ./cmd/orbit/`. Debian: `ORBIT_IT_DISTRO=debian go test -tags integration -p 1 -timeout 60m -run '^TestEscape' -v ./cmd/orbit/`. The sandbox distribution is set with `orbit init --distro ubuntu|debian` (default `ubuntu`).

## Always / Ask first / Never

- **Always:** work on a branch, never directly on `main`. Run the validation gate before you open a PR. Label a change to the sandbox boundary `risk-high`.
- **Ask first:** before you add a Go module dependency; before you change the default network allowlist, the isolation settings of a sandbox, or what is mounted into it; before you add OrbStack-specific code outside `internal/orbstack`; before you change an active row in the product brief.
- **Never:** commit secrets, tokens, or `.env` content. Never delete, rename, or recolor existing tracker labels. Never share the host engine socket or the home directory with a sandbox, and never create a machine without `--isolated` and `--isolate-network`. Never give the agent user sudo, the rootful Docker socket, or write access to the firewall or proxy configuration. Never pass a key file into a sandbox, and never pass a host credential that the user did not opt in to, except the SSH agent socket that is forwarded by default and that `--no-ssh-forwarding` removes (R08). Never read, copy, store, log, or forward the subscription credentials of the user (D08). Never modify an agent client or restrict its sign-in methods.

## Related documents

- `.ai/specs/product-brief.md` — problem, scope, rules, decisions, open questions
- `SDLC.md` — ticket flow and merge gates
- `CODE_REVIEW.md` — review rules
- `ARCHITECTURE.md` — system structure
- `.ai/agentic.config.json` — pipeline configuration
