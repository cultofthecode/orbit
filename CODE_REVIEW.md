# Code review rules — orbit

`om-code-review` (and so `om-auto-review-pr`) applies this file automatically, in addition to its built-in checklist. The repository has no code yet, so the repo-specific section is a placeholder. Add rules as conventions appear, and cite the files that set them.

## Review priorities

In order:

1. **Correctness** — the change does what the ticket or spec asks. The code handles edge cases: empty, null, error, concurrent, and large input. Tests cover the new behavior and the regression that the change fixes.
2. **Security** — the code validates input from users, the network, files, and the environment before use. Code, logs, and fixtures contain no secrets. The code checks authorization on every path that reads or changes scoped data.
3. **Contracts** — public APIs, routes, CLI flags, schemas, and config formats change only deliberately. A change that affects consumers includes a migration note.
4. **Maintainability** — the change follows existing patterns and does not add a parallel one. Names explain intent. There is no dead code and no commented-out block.

## Repo-specific checks

TODO: derive these from the codebase when it exists. Examples of the kind of rule that belongs here:

- which validation library every request handler uses
- how the code reports errors (error types, result objects, HTTP status mapping)
- how the code organizes data access (repositories, query builder, ORM conventions)
- naming and file layout conventions for modules and tests

## Validation gate

The gate is `validation.commands` in `.ai/agentic.config.json`, and it is empty now. Until it has commands, reviewers must state in the review that no automated gate ran.

## Severity guidance

- **Blocker** — incorrect behavior, data loss, a security hole, a broken contract without a migration path, or failing validation or required CI. A blocker stops approval.
- **Major** — missing tests for new behavior, unhandled error paths, or a `risk-high` change without the evidence that `SDLC.md` requires. Fix it before the merge.
- **Minor** — readability, naming, or small duplication. Fix it or file a follow-up issue.
- **Nit** — style preference. A nit never blocks. File it as a follow-up only when it is worth tracking.

Report a failing required check or a merge conflict as a blocker finding together with the full review, never instead of it (see `SDLC.md`).
