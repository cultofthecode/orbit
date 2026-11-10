# Software delivery process

## Purpose

This file describes how work moves from a ticket to a merged PR in this repository. The agent skills configured in `.ai/agentic.config.json` enforce the process. Humans read it here.

New PRs target `main` by default. An existing PR uses its own current target branch (`baseRefName`) for base updates, reviews, required checks, and merges. Issues and PRs live in GitHub. `.ai/trackers/github.md` defines every tracker operation that the skills run. Edit that file to extend or override tracker behavior.

Work enters through two paths: a free-form task brief that a person gives to an agent, or a filed ticket. Both paths use the same review loop, the same validation gate, and the same merge gates.

Before intake, the team shapes the work. `om-brainstorm` turns one idea or question into a routing decision and a brief. The spec skills (`om-spec-writing`, `om-auto-write-spec`) turn a feature into a design document before anyone builds it. These steps feed the table below. They are not part of the ticket flow.

## Roles

- **Author** — the human or agent who writes the change. The author owns the ticket from the claim to a merge-ready PR.
- **Reviewer** — reads the diff and approves it or requests changes. The reviewer can be a human or the `om-auto-review-pr` skill. The `om-code-review` checklist applies in both cases. The reviewer is also the second person that a `risk-high` change needs. The reviewer signs off the spec when a feature needs one. A team that names a tech lead or an architect puts them in this role.
- **Designer** — owns the flow and its states before the code exists. The designer also owns the design contract that the UI review reads back. The designer can be a human or a skill. Use `om-ux-shape` for shaping, `om-ux-design` for detailed screens from a specification, and `om-ux-review-pr` for the pass over the screens of a PR.
- **QA reviewer** — tests user-facing changes before they merge. The QA reviewer uses `om-prepare-test-env` to start the app once and `om-auto-qa-pr` to test it in a real browser. "Manual" means that a person judges the result and owns `qa-approved`. It does not mean that the person works without help. Always refer to this role by role, never by name or handle, because assignments change.
- **Maintainer** — owns branch protection, the label taxonomy, the config, this document, and the installed skills with their repo-local overrides under `.ai/skills/`. The maintainer decides when gates conflict. The maintainer is also the release manager unless the team names one.

## Ticket lifecycle

| Stage | What happens | Driven by | Done when |
|---|---|---|---|
| Discovery | A person talks through an idea, question, or itch before any artifact exists. They question the problem and compare alternatives, including building nothing. The conversation ends in a routing decision: an answer, a filed ticket, a brief for a spec, or a direct change. | `om-brainstorm` or a human | The conversation has a route. A brief exists when the work continues. |
| Intake | Someone files a ticket or task brief in GitHub. It has enough detail to act on: what is wrong or wanted, for whom, and what done looks like. `om-prepare-issue` files it with SDLC labels. | Anyone, `om-prepare-issue` | The ticket exists |
| Triage | Check that the issue is real and still not fixed on `main`. Check that nobody claimed it and that no open PR covers it. This stage is read-only. It stops the chain cleanly when there is nothing to do. | `om-verify-in-repo` or a human | The issue is actionable, or it is closed as no-action |
| Claim | The author claims the ticket so that concurrent agents do not start work on it. See the claim protocol below. | `om-fix` / `om-auto-create-pr`, or a human | The claim is visible on the ticket |
| Design | For a user-facing change, the team decides the flow and its states before the code exists. This includes what the screen does when it is empty, loading, in error, or without permission. It also includes what the change deliberately does not do. A designer may design detailed screens from the specification with repository components. Only an accepted version becomes its `Prototype:` reference. A ticket that touches no UI skips this stage. | `om-ux-shape`, optionally `om-ux-design` when chosen, or a human designer | The team decided the flow and its states, or the ticket is not user-facing |
| Implement | Find the minimal change surface with `om-root-cause` (read-only). Then implement the change with regression tests and run the validation gate. Task briefs without a ticket go through `om-auto-create-pr`. That skill plans, implements phase by phase in an isolated worktree, and runs the same gate. | `om-root-cause` + `om-fix`, `om-auto-create-pr`, or a human author | The change is complete and the validation gate is green |
| PR | Commit, push, and open a PR against `main` with normalized labels. On a branch that a human worked on, `om-check-and-commit` runs the gate, fixes obvious drift, and pushes when the gate is green. | `om-open-pr`, `om-auto-create-pr`, or `om-check-and-commit` | An open, labeled PR exists |
| Review loop | The reviewer reads the diff against the `om-code-review` checklist. The reviewer approves it or requests changes. The author addresses the requested changes, and the reviewer reviews the PR again until it is approved. `om-auto-continue-pr` resumes agent PRs from the tracking plan. It also adopts a PR that has no plan and reconstructs the plan from the context of the PR. A user-facing change also gets a design pass. `om-ux-review-pr` walks the changed screens and reports findings ranked by user impact. That pass is advisory. It informs the review but does not hold the merge. | `om-auto-review-pr` (single PR), `om-review-prs` (sweep), `om-ux-review-pr` (design pass), or a human | An approving review exists |
| QA | A PR that carries `needs-qa` waits for QA. The reviewer starts the app once with `om-prepare-test-env`. Then the reviewer tests the change in a real browser with `om-auto-qa-pr`. That skill attaches screenshots and a pass/fail report, and by default it touches no labels. The reviewer records the outcome. A flow that is worth keeping becomes `om-integration-tests` coverage. See the QA gate below. | QA reviewer, with `om-prepare-test-env`, `om-auto-qa-pr`, `om-integration-tests` | A QA reviewer or the permitted self-QA exception applies `qa-approved`. `qa-failed` sends the PR back. |
| Merge | `om-merge-buddy` reports, read-only, which PRs can merge now and which are close but blocked. `om-approve-merge-pr` checks every gate again, approves, and squash-merges. `om-auto-merge-triage` proposes candidates for `automerge` and labels the ones that a human confirms. `om-auto-merge-buddy` works unattended on the PRs that a human labeled `automerge`. It continues until the three automerge signals hold. Then it marks the PRs ready or squash-merges them. | `om-merge-buddy` + `om-approve-merge-pr`, `om-auto-merge-triage` + `om-auto-merge-buddy`, or a human | The PR is squash-merged into its current target branch |
| Post-merge housekeeping | Close the issues that the merged PR fixes. Comment on issues whose PRs closed without a merge. Turn leftover requests or review comments into tracked follow-up issues. | `om-close-fixed-issues`, `om-followup-issue-from-pr` | The tracker matches the merged state, and the follow-ups exist |

After the merge, this process stops. Deployment, smoke tests, monitoring, and rollback belong to the release process of the repository, not to this document. The Maintainer (or a release manager, when the team names one) drafts the changelog with `om-auto-update-changelog`. The same person reconciles the tracker with `om-close-fixed-issues`. `om-pipeline-retro` reads finished runs and ranks what second passes cost. This document ends at the merge. Delivery of the change to users is a separate process that the team owns.

## Label state machine

Pipeline labels are mutually exclusive. A PR carries at most one, and that label names the position of the PR in the flow.

- A ready, non-draft PR carries `review`.
- The reviewer moves the PR. A request for changes sets `changes-requested`. After the fixes, the PR returns to `review`. An approval sets `merge-queue`.
- `merge-queue` is a routing state, not proof of QA. A `needs-qa` PR can correctly stay there until QA signs off.
- Only a QA reviewer sets the `qa` pipeline label. The QA reviewer moves a queued `needs-qa` PR from `merge-queue` to `qa` during testing. On a pass, the QA reviewer moves it back to `merge-queue` with `qa-approved`. On a failure, the QA reviewer moves it to `qa-failed`. Automated skills request QA with `needs-qa`. They never set `qa`.
- Humans set and clear `blocked` and `do-not-merge`. These labels stop the flow at any stage.

| Group | Labels | Exclusivity | Meaning |
|---|---|---|---|
| Pipeline | `review`, `changes-requested`, `qa`, `qa-failed`, `merge-queue`, `blocked`, `do-not-merge` | one at a time | Workflow state |
| Category | `bug`, `feature`, `refactor`, `security`, `dependencies`, `documentation` | additive | Kind of change |
| Meta | `needs-qa`, `skip-qa`, `qa-approved`, `qa-self-verified`, `in-progress`, `claim-reset`, `ci-monitoring` | additive | Process signals |
| Priority | `priority-low`, `priority-medium`, `priority-high`, `priority-extreme` | one at a time. Unset = medium. | Urgency of the work |
| Risk | `risk-low`, `risk-medium`, `risk-high` | one at a time. Unset = medium. | Blast radius of the change |

Priority is how urgent the work is. Risk is how dangerous the change is to ship. A one-line fix for an outage can be `priority-extreme` and `risk-low`. A large auth refactor that can wait can be `priority-low` and `risk-high`. A PR inherits both labels from its source issue unless the scope clearly changed. When an automated skill adds or changes a pipeline or meta label, it leaves a short comment that explains why.

When an item has no priority label, infer one:

- `priority-extreme` — production outage, data loss, or an active security incident.
- `priority-high` — security hardening or a release-blocking regression.
- `priority-medium` — ordinary bug fixes and net-new features. This is also the default reading when the label is unset.
- `priority-low` — cosmetic changes, docs-only changes, dependency bumps, and follow-up cleanup.

When an item has no risk label, infer one:

- `risk-high` — the sandbox boundary (mounts, machine isolation settings, provisioning of the agent user, firewall, and proxy, network policy, credential handling), authentication and login sessions, data scoping, money, schema migrations, shared contract surfaces, or broad cross-cutting edits.
- `risk-medium` — an ordinary single-area change that ships with tests. This is also the default reading when the label is unset.
- `risk-low` — docs-only, test-only, typo, or isolated cosmetic changes.

When signals conflict, pick the higher label and give the reason in the label comment. A `risk-high` PR does not only get advice to take more care. It triggers gates:

| Area behind `risk-high` | What the PR must carry |
|---|---|
| Sandbox boundary (mounts, machine isolation settings, provisioning of the agent user, firewall, and proxy, network policy, credential handling) | a unit test with hostile input for the changed rule, a passing run of the escape suite against a real OrbStack (`ARCHITECTURE.md`, Testing strategy) with its output in the PR, and a review by a second person. Until the escape suite exists, the PR states that it did not run. |
| Auth, sessions, permissions | an integration test for the denied path and the wrong-scope read, and a review by a second person |
| Data scoping | an isolation test that proves one scope cannot read another |
| Money | tests for the failure, retry, and idempotency paths, and a review by a second person |
| Schema migrations | a migration test up and down, and a rollback plan in the PR body |
| Shared contract surfaces | a test that exercises the consuming side, as `BACKWARD_COMPATIBILITY.md` describes |
| Any `risk-high` | `needs-qa` when the change is user-facing. No self-QA. `om-code-review` blocks the PR without the evidence above, unless a maintainer waives it on the PR. |

Two labels are outside this taxonomy, and only humans decide them. (Only `om-auto-merge-buddy` applies the automerge signal labels below.)

- `do-not-close` — goes on issues that housekeeping skills must never close automatically. Skills only read it.
- `automerge` — goes on a PR that a human wants merged without more attention. A human applies it directly. Alternatively, `om-auto-merge-triage` applies it to the PRs that a human confirms from its ranked proposal. The eligibility rules are in `automerge.eligibility`. The AI judgment of the triage skill gives priority to important fixes that are close to merge. It inspects older PRs that have screenshots and requested changes. Requested changes must be bounded and must pass that judgment before any tier can propose the PR. Review, QA, and CI stay required. `om-auto-merge-buddy` works on these PRs one at a time within a time window (default 2 hours). It resumes due tracked work from the local file `.ai/meta/merge-buddy.json`. When tracking is not empty, it prefers actionable work assigned to the current user. At the first actionable step, it assigns the current user and verifies the assignment. When scheduling is empty or reset, it keeps the global queue. Live `in-progress` claims stay protected. Readiness is three signals. Each signal is a label that only this skill applies:
  - `automerge-signal-1-cr` — `om-auto-review-pr` approved the current head. An independent native approval is an additional requirement only when the resolved policy requires it.
  - `automerge-signal-2-qa` — QA is not required (`skip-qa`, or no `needs-qa`). Or, `qa-approved` exists and its `QA head:` line names the current head. Only `om-auto-qa-pr` can apply this signal through self-QA. On `risk-high`, it can do so only under the explicit invocation-scoped risk override below.
  - `automerge-signal-3-ci` — every check on the current head finished green, and there are no conflicts.

  On its first visit to a PR with no recorded signal, the buddy derives the initial signals from the real state of the PR. It reads the labels, the reviews and comments, and the CI checks on the current head. Then it starts from the first signal that is still missing. It does not repeat review, QA, or CI work that is already done. To produce a missing signal, the buddy runs `om-auto-review-pr --autofix`, `om-auto-qa-pr`, or `om-auto-fix-pr`. Each runs with `--orchestrated`, so that the buddy alone owns the claim and the labels. When CI still runs, the buddy parks the PR under `ci-monitoring` and checks it again later. When the buddy cannot fix a PR within the attempt limit, it parks the PR under `automerge-blocked`. Remove that label to try again. The buddy carries a fork PR forward to a credited replacement that keeps the label. In `prepare-to-merge` mode, a PR with all three signals gets `merge-queue`. In `prepare-and-merge` mode (the default when the config is missing), the buddy squash-merges the PR. That merge bypasses other labels and host merge rules, because the three signals are the gate. Remove `automerge` to stop the automation.

`.ai/agentic.config.json` sets the mode and the independent-review policy. `automerge.mode` defaults to `prepare-and-merge`. `automerge.requireIndependentReview` defaults to `false`. An explicit `--mode`, `--require-independent-review`, or `--skip-independent-review` flag overrides the current config. Conflicting review flags or invalid values stop the run. Automatic code review stays required. Actionable inherited objections stay required too. The default mode does not wait for a native approval. It also does not wait for a stale requested-changes state after the objections are resolved.

The strict policy needs a fresh native approval on the current head. A human who is not the author and not the automation must give it. Both prepare modes need it before they finish, and no newer requested-changes verdict can stand. When this approval is missing, the run waits. It does not repeat automated stages or use attempts. Check the policy and the evidence again at every entry and at every final guard. A saved status or scheduler policy is never the authority. Standalone skills keep their existing review gates.

`automerge.defaultToHumanQa` defaults to false. In that case, mandatory local QA approval can be missing, or testable gaps can exist. Then the buddy schedules a real `om-auto-qa-pr --orchestrated` run. That run gives coverage or a legitimate self-sign-off, and it reuses current evidence that already covers the head. When the value is true, the buddy prefers a human approval. An explicit `--human-qa` or `--auto-qa` flag overrides the config. Only verified ineligibility, verified blockers, or explicit human routing make the run wait for a human. No preference fabricates approval labels.

### Explicit automerge risk override

`om-auto-merge-buddy --force` records an explicit exception from the operator on the PR and on the run. The exception applies to this invocation only. It waives the risk-high integration-evidence requirement. It also waives the risk-high self-QA exclusion. The QA skill must still complete real verification, attach screenshots, and pin its sign-off to the tested head.

These items stay in force:

- the full review
- actual correctness and security findings
- the configured validation gate
- required QA, CI, and conflicts
- the merge head guard

The override does not change risk labels. It does not authorize a claim takeover. The buddy passes the separate `--skip-risk-checks` flag to orchestrated stages. Without `--force`, strict gates apply, even on resume. Review and QA approvals that came from the override cannot count as ordinary approvals later. Standalone skill defaults do not change.

The independent `--skip-pre-existing-ci-errors` flag permits one narrow waiver. It covers only an individual completed CI failure that is proved identical on the exact current target SHA. The proof needs a matching workflow and job context, unchanged relevant CI definitions, and the same actual diagnostic or root cause. Record both runs, both SHAs, and the proof. The flag does not waive pending, new, unreadable, or unknown failures. The flag applies to one invocation only, and `--force` never turns it on. Later strict runs need a fresh CI evaluation. Target advances and reruns also need a fresh CI evaluation. A waived failure stays visibly failed. It does not show as green. Local validation and all other gates do not change.

For automerge PRs that a human authorized, signal 2 also accepts a trusted completed `om-auto-qa-pr` PASS on the unchanged head. This acceptance does not depend on manual-approval labels or self-QA eligibility. It includes evidence-only reports and risk-high reports that disclose coverage limitations. Trust requires the same author for the starter, the report, and the completion history. That author must have write access. Resolve each legacy short `Verified` SHA uniquely to the exact current head. Keep the limitations visible. Do not add `qa-approved` or `qa-self-verified`. Skip duplicate QA attempts. A newer trusted FAIL, a revocation or deletion, or a head change makes this evidence invalid. This exception for the automerge signal does not change the manual-approval gates of standalone QA and of other merge skills.

## The QA gate

This process has one hard rule. **A PR that carries `needs-qa` must not merge until it also carries `qa-approved`, even when every other check is green.** `om-merge-buddy` classifies such a PR as blocked. `om-approve-merge-pr` refuses to merge it.

- Apply `needs-qa` to UI changes, new features, and other user-facing behavior that needs manual testing.
- For a UI change, QA covers more than "it works". The state matrix is part of the pass/fail result: default, empty, loading, error, no-permission, long content, and narrow viewport. Conformance to the design contract in `.uxproof/` is also part of it, when the repository has that contract. A missing state fails QA. `om-ux-review-pr` stays the advisory design review. QA runs its objective checks.
- Orbit is a CLI and has no web UI. For Orbit, "user-facing" means commands, flags, output, exit codes, and what a sandbox can reach. QA evidence is a terminal transcript of the tested commands on a named OrbStack version, not a browser screenshot. The browser skills and the Design stage apply only if Orbit gets a UI.
- `skip-qa` is the explicit opt-out for docs-only, dependency-only, CI-only, test-only, and similar low-risk changes that are not user-facing. Never combine it with `needs-qa`.
- `qa-failed`, `do-not-merge`, and `blocked` are hard blocks, whatever the other signals say. An active `qa` pipeline label means that a tester works on the PR now. Never merge while a tester is active.
- A QA reviewer satisfies the gate when they test the PR and apply `qa-approved`.
- **`qa-approved` is pinned to a commit.** The comment that grants it carries the line `QA head: <sha>` for the tested head. That comment is the note of the QA reviewer or the self-QA evidence comment. A push after that line leaves the label in place but makes it stale. `om-merge-buddy` then reports "QA evidence older than head". `om-approve-merge-pr` asks for confirmation before it merges. To recover, a QA reviewer tests the PR again. Alternatively, a QA reviewer states on the PR that the new commits do not touch the tested scope, with a fresh `QA head:` line.
- **Self-QA exception**: when no QA reviewer has capacity in time, any engineer may sign off instead. `om-auto-qa-pr --self-qa-signoff` may also sign off. This exception applies only to a `risk-low` or `risk-medium` PR. The evidence on the PR names the tested scenario, the environment, the test data, the observed result, the negative cases tried, and the `QA head:` line. Then apply both `qa-approved` and `qa-self-verified`. The first label passes the gate. The second label makes the exception auditable. No evidence means no `qa-approved`.
- **No self-QA on `risk-high`.** Some PRs need a QA reviewer, or an explicit exception that a maintainer states on the PR. These are PRs labeled `risk-high`, and PRs whose diff touches auth, sessions, data scoping, money, schema migrations, or shared contracts (the `risk-high` inference above). On such a PR, `om-auto-qa-pr` withholds the sign-off and posts only the evidence. A change to auth or money also needs a review by a second person, whatever the QA path.

## The claim protocol

Before you change an issue or PR, read **get-lock**. Also read the assignees, the `in-progress` label, and live claim comments.

GitHub, GitLab, and Forgejo use the atomic **acquire-lock** operation. It creates the reserved ref of the descriptor exactly once. The ref points to a unique metadata commit that carries the owner, the run, and the item. Only the creator of the ref continues. Bots with the same login still compete. An existing ref blocks other claimers, even before comments or labels exist. The winner keeps a claim comment. That comment has the legacy prefix plus `Claim-Run`, `Claim-Actor`, `Claim-State`, `Claim-Lock`, and `Claim-Lock-SHA`. Claim and comment IDs are additional audit evidence. Assign the item when the tracker supports it. Then verify `in-progress` as the mandatory second check. Skip only the assignment for bots that are confirmed unassignable.

Only trackers that explicitly lack an atomic primitive use comment-first election. In that election, fresh complete reads select the live candidate that was created first. Losers withdraw their own candidate. They do not add labels or start work. Git-host permissions, API failures, transport failures, and missing operations block acquisition. Never silently downgrade GitHub, GitLab, or Forgejo to comment mode. Linear and Jira issues keep comment election. Their GitHub PRs delegate to the atomic operations. Upgrade the installed claimers and descriptors together. Non-atomic providers keep a safe fallback for older descriptors: a complete list read and an edit.

Chains pass the same run ID, comment ID, and exact lock SHA. They keep ownership through handoffs. Before each phase, mutation, or publication, read the ownership and the reset history again. At completion, clean only the labels and assignments that you own. Then mark the original comment as released. Call **release-lock** last. Git hosts delete the lock through an explicit expected-SHA Git lease. Thus a delayed cleanup cannot delete the lock of a successor. This cleanup is metadata housekeeping. It does not publish staged project work.

**Recovery in the GitHub UI:** stop the stalled runner and the delegated workers. Then add the human-only `claim-reset` label. The next invocation audits the human label event. It then conditionally retires only the exact old lock whose server creation time is earlier than that event. Recovery still competes for a new create-only lock. If the SHA changed, recovery stops. Removal of `in-progress` alone does not clear a lock. The label can stay as a breadcrumb. To reset again later, remove the label and apply it again. Historical resets stay in effect after you remove the label. Reset, stale, and force recovery never waive live local leases, QA gates, or merge gates. They cannot cancel a command that already runs. Issue providers other than GitHub that have no audit report UI recovery as unsupported.

GitHub and Forgejo reserve `refs/om-locks/{kind}-{number}`. GitLab reserves `refs/heads/om-locks/{kind}-{number}`. GitLab and Forgejo have no authenticated ref-creation timestamp (`createdAt: null`). A client `metadataCreatedAt` value never authorizes time-based or UI recovery. Those paths block when no server audit exists. An explicit force still requires stopped workers and an exact-SHA deletion. Lock pushes can trigger CI or hooks. Reserve or filter the lock namespaces through a reviewed repository policy.

Normal acquisition on a Git host is atomic. Recovery relies on correct liveness decisions and on cooperative boundary checks. The comment fallback keeps a race on eventual visibility, which can duplicate work. See the tracker contracts for **get-lock**, **acquire-lock**, **release-lock**, **get-claim-reset**, **claim-winner**, and **withdraw-claim**.

`in-progress` means **actively working**. An agent can finish its work and report it fully: labels applied, review submitted, comments posted. If it still intends to report the CI outcome, it then replaces `in-progress` with `ci-monitoring`. `ci-monitoring` is **not** a claim and blocks nobody. It says only that the agent still owes the CI-result follow-up comment. Another agent or a human can act on the PR freely.

This distinction matters because CI runs for a long time. Suppose that an agent reports its work and then stops while it watches a run. That agent leaves an honest state that describes itself, not a lock that nobody holds. The label comes off when the follow-up arrives. It also comes off when the agent stops waiting at `ci.maxWaitMinutes` and says so.

The agent releases the claim when the work finishes, on success and on failure. The maintainer can clear a stale `in-progress` label that has no recent activity.

### Reporting is decoupled from CI

Agents apply labels, submit reviews, and post comments **as soon as their work is done**. They do not wait for CI to go green. A review that an agent submits while checks still run says so in its body. Branch protection and the QA-approval gate hold the actual merge. The approval covers the code, not a green run. The CI outcome arrives later as a follow-up comment. That comment also corrects the pipeline label if the result changes the verdict.

`ci.maxWaitMinutes` limits the wait for that outcome (default 40). The limit can expire while checks still run. Then the agent stops waiting and runs the local validation gate as its own evidence. It posts that evidence with the names of the pending checks. It also states explicitly that no further follow-up will come. Then it drops `ci-monitoring` and finishes.

A red signal does not cut the review short either. A failing required check or a conflicted head becomes a **blocker finding**. The reviewer reports it together with the full code review, never instead of it. Thus one review cycle gives the author the failing check, the conflict, and every code finding. Without this rule, the author gets the cheapest red flag first and needs another cycle to find the rest. Such a verdict is still `changes-requested`. The review is more complete, but the gate is the same.

None of this changes the merge gates. Early reporting is safe. Early merging is not. Required checks still gate every merge, and the merge tooling refuses until the checks are really green.

## The automation contract

The `om-auto-*` skills run this process unattended, and you can chain them. Each skill accepts the artifact that the previous skill produced: an issue ID, a spec path, or a PR number. The PR number comes from the `PR: #<number> (link: <url>)` reference line that every PR-producing skill emits. Each skill also detects work that already started, such as an open PR that references the issue or plan. It continues that work and does not open a duplicate.

A completed autonomous run leaves a **ready** (non-draft) PR. The PR has a full label set: one pipeline label, a category, a QA meta label, one priority, and one risk. It also leaves a run-summary comment. For user-facing changes, it attaches screenshots from the working app as PR evidence. Draft PRs are only for states that are explicitly incomplete. These are spec-only design PRs, interrupted handoffs, and autonomous defaults that a human must confirm.

Automation applies `qa-approved` only through the self-QA exception: `om-auto-qa-pr --self-qa-signoff`, always paired with `qa-self-verified`. On `risk-high`, automation applies it only under the explicit invocation-scoped risk override above. No authoring, review, or merge skill ever applies `qa-approved`.

## Validation gate

Every PR passes the full validation gate before review sign-off, in this order:

1. `test -z "$(gofmt -l .)"` — formatting
2. `go vet ./...` — static checks
3. `golangci-lint run` — lint with the `standard` linters, plus a `depguard` rule that lets only `cmd/orbit` import `internal/orbstack` (the adapter part of dependency rule 1 in `ARCHITECTURE.md`)
4. `go test ./...` — unit and handler tests
5. `go build ./...` — build

Integration tests and the escape suite need a running OrbStack (`go test -tags integration ./...`). They are not in the default gate. A `risk-high` change to the sandbox boundary must run them (see the risk table).

Any non-zero exit fails the gate and blocks the PR. The implementing skills run the gate before they open a PR. `om-check-and-commit` runs it before it pushes a branch that a human worked on. The command list lives in `.ai/agentic.config.json`. When the list changes, update it there and in this section together.

## Amending this process

This document and `.ai/agentic.config.json` describe the same process. Change them together. Run the `om-setup-agent-pipeline` skill again when the toolchain or the label taxonomy changes.

The Design and Review stages read a design contract. `om-ux-setup` creates it once from the repository. Run `om-ux-setup` again when the design system changes.

Per-skill deviations belong in a repo-local skill with the same name at `.ai/skills/<skill-name>/SKILL.md`. Examples are extra review rules, a different PR body template, or an added gate step. The repo-local skill takes precedence over the installed skill. It can `@`-import or reference the installed skill to extend it, not replace it. Local rules win. But a repo-local skill cannot grant what the safety rules of the installed skill forbid.

## Human-confirmed issue autofix queue

`om-auto-issue-triage` proposes useful open issues that have a decided scope and bounded work. It applies `autofix.label` (default `cez-autofix`) only to issues that a human confirms after seeing the evidence. It skips active or uncertain claims and keeps the assignees. It checks the current state again and verifies the label by reading it back. The label may trigger the existing worker of the repository. This skill does not install or invoke a worker. Implementation stays with `om-auto-fix-issue` or the existing worker. All normal specification, code-review, QA, and CI gates stay the same.
