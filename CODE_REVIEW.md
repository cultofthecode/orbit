# Code review rules — orbit

`om-code-review` (and so `om-auto-review-pr`) applies this file automatically, in addition to its built-in checklist. The code so far is the CLI skeleton in `cmd/orbit`, so the repo-specific section is still short. Add rules as conventions appear, and cite the files that set them.

## Review priorities

In order:

1. **Correctness** — the change does what the ticket or spec asks. The code handles edge cases: empty, null, error, concurrent, and large input. Tests cover the new behavior and the regression that the change fixes.
2. **Security** — the code validates input from users, the network, files, and the environment before use. Code, logs, and fixtures contain no secrets. The code checks authorization on every path that reads or changes scoped data.
3. **Contracts** — public APIs, routes, CLI flags, schemas, and config formats change only deliberately. A change that affects consumers includes a migration note.
4. **Maintainability** — the change follows existing patterns and does not add a parallel one. Names explain intent. There is no dead code and no commented-out block.

## Repo-specific checks

These rules come from `ARCHITECTURE.md` and `.ai/specs/product-brief.md`. Cite real files when they exist.

- **Sandbox boundary.** A change to mounts, machine isolation settings, provisioning (agent user, firewall, proxy), network policy, or credential handling is `risk-high` and carries the evidence that `SDLC.md` requires. A sandbox that gets the host engine socket, a machine without `--isolated` and `--isolate-network`, an agent user with sudo or the rootful Docker socket, the home directory, or a host credential without the user's opt-in is a blocker (R01, R06). A new default allow rule needs a stated reason.
- **Protected contract.** A diff that contradicts an active Business rule, Non-goal, or Decision in the product brief is a blocker unless the same PR carries a superseding row that the owner approved.
- **Slices.** One operation is one package with `contract.go`, `handler.go`, `command.go`, and `handler_test.go`. Commands hold no decisions. A slice does not import another slice. A module uses another module only through its root package. Only `cmd/orbit` imports an adapter.
- **Security decisions are pure functions** with table tests that include hostile input: symlinks and `..` paths out of the workspace, wildcard and look-alike domains.
- **Agents.** An agent is one `Definition` package under `internal/agent/`. A comparison of an agent name outside that package is a major finding. Code that stores, rewrites, or forwards an agent credential, modifies a client, or restricts its sign-in methods is a blocker (R04).
- **Backend access.** OrbStack-specific code lives only in `internal/orbstack` (R07). Every machine that Orbit creates carries the Orbit name prefix.
- **Errors** are wrapped with `%w` and matched with `errors.Is`. Only `cmd/orbit` prints errors and sets exit codes. No `panic` for expected failures.
- **Secrets** never appear in logs, machine configuration, image layers, or test fixtures.
- **Style.** `gofmt`. Doc comments on packages and exported identifiers; no comments inside function bodies. No abstraction for single-use code. A new Go module dependency needs a stated reason.

## Validation gate

The gate is `validation.commands` in `.ai/agentic.config.json`. Reviewers run it and state the result in the review.

## Severity guidance

- **Blocker** — incorrect behavior, data loss, a security hole, a broken contract without a migration path, or failing validation or required CI. A blocker stops approval.
- **Major** — missing tests for new behavior, unhandled error paths, or a `risk-high` change without the evidence that `SDLC.md` requires. Fix it before the merge.
- **Minor** — readability, naming, or small duplication. Fix it or file a follow-up issue.
- **Nit** — style preference. A nit never blocks. File it as a follow-up only when it is worth tracking.

Report a failing required check or a merge conflict as a blocker finding together with the full review, never instead of it (see `SDLC.md`).
