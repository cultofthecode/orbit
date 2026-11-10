# AGENTS.md — orbit

## Project overview

TODO: write one paragraph about orbit. Say what it is, who uses it, and how the team deploys it. The repository has no code or README yet, so nothing could be inferred.

## Task routing

Before you work on a task of a given kind, read the files in **Read first**. This table came from an empty repository. As the codebase grows, replace the `TODO` rows with real areas. Use one row for each top-level package, app, or module group. Also add rows for the API layer, the UI layer, tests, and CI. Alternatively, generate the table again with `om-create-agents-md`.

| When the task involves… | Read first | Key rules |
|---|---|---|
| Delivery process, labels, QA gate, claims | `SDLC.md` | Follow the ticket lifecycle and the label state machine. Never apply `qa-approved` from an authoring task. |
| Code review | `CODE_REVIEW.md` | `om-code-review` and `om-auto-review-pr` apply it automatically. |
| System structure, module boundaries | `ARCHITECTURE.md` | TODO: fill in when the team decides the architecture. |
| Pipeline configuration | `.ai/agentic.config.json`, `.ai/trackers/github.md`, `.ai/browsers/agent-browser.md` | Change the config and `SDLC.md` together. Run `om-setup-agent-pipeline` again when the toolchain or the labels change. |
| Specs and design docs | `.ai/specs/` | Filenames: `{YYYY-MM-DD}-{kebab-case-title}.md`. Handoff briefs go under `.ai/specs/briefs/`. |
| Application code | TODO | TODO |
| Tests | TODO | TODO |
| CI | TODO (`.github/workflows/`) | TODO |

## Validation commands

None yet. `validation.commands` is empty in `.ai/agentic.config.json`. TODO: when the stack exists, add the typecheck, lint, test, and build commands here and in the config.

## Always / Ask first / Never

- **Always:** work on a branch, never directly on `main`. Run the validation gate before you open a PR.
- **Ask first:** TODO
- **Never:** commit secrets, tokens, or `.env` content. Never delete, rename, or recolor existing tracker labels.

## Related documents

- `SDLC.md` — ticket flow and merge gates
- `CODE_REVIEW.md` — review rules
- `ARCHITECTURE.md` — system structure
- `.ai/agentic.config.json` — pipeline configuration
