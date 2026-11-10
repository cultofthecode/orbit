# Backlogs

## .ai/specs/product-brief.md
Backlog source: .ai/specs/product-brief.md
Last filed: 2026-11-10

Parents are linked as GitHub sub-issues. The epics carry no checklist.

| Id | Issue | Title | Epic | Depends on | Adopted |
|---|---|---|---|---|---|
| E01 | [#2](https://github.com/cultofthecode/orbit/issues/2) | Start a project sandbox and work in it | — | — | no |
| E01-S01 | [#5](https://github.com/cultofthecode/orbit/issues/5) | The owner can run `orbit init --agent claude-code` and gets a sandbox for the current directory with Claude Code installed | #2 | — | no |
| E01-S01-T01 | [#14](https://github.com/cultofthecode/orbit/issues/14) | Add the Go module, the CLI skeleton, and the validation gate | #2 (story #5) | — | no |
| E01-S01-T02 | [#15](https://github.com/cultofthecode/orbit/issues/15) | Add the OrbStack adapter, the sandbox spec, mount validation, and machine creation | #2 (story #5) | E01-S01-T01 (#14) | no |
| E01-S01-T03 | [#16](https://github.com/cultofthecode/orbit/issues/16) | Provision the machine as root: agent user, firewall, egress proxy, toolchain | #2 (story #5) | E01-S01-T02 (#15) | no |
| E01-S01-T04 | [#17](https://github.com/cultofthecode/orbit/issues/17) | Add the Claude Code definition and its install | #2 (story #5) | E01-S01-T03 (#16) | no |
| E01-S02 | [#6](https://github.com/cultofthecode/orbit/issues/6) | The owner can run `orbit shell` and gets a shell as the agent user in the project sandbox | #2 | E01-S01 (#5) | no |
| E01-S03 | [#7](https://github.com/cultofthecode/orbit/issues/7) | The owner can sign in with `/login` once per sandbox and stays signed in | #2 | E01-S02 (#6) | no |
| E01-S04 | [#8](https://github.com/cultofthecode/orbit/issues/8) | The agent can push over SSH with the forwarded host SSH agent, and `--no-ssh` leaves it out | #2 | E01-S02 (#6) | no |
| E01-S05 | [#9](https://github.com/cultofthecode/orbit/issues/9) | The agent can build and run containers inside the sandbox without the host engine | #2 | E01-S01 (#5) | no |
| E01-S06 | [#10](https://github.com/cultofthecode/orbit/issues/10) | The owner can run the escape suite and sees every documented escape attempt fail | #2 | E01-S01 (#5), E01-S05 (#9) | no |
| E02 | [#3](https://github.com/cultofthecode/orbit/issues/3) | Manage the project sandbox | — | — | no |
| E02-S01 | [#11](https://github.com/cultofthecode/orbit/issues/11) | The owner can stop and start the project sandbox with `orbit stop` and `orbit start` | #3 | E01-S01 (#5) | no |
| E02-S02 | [#12](https://github.com/cultofthecode/orbit/issues/12) | The owner can remove the project sandbox with `orbit destroy` | #3 | E01-S01 (#5) | no |
| E02-S03 | [#13](https://github.com/cultofthecode/orbit/issues/13) | The owner can list all sandboxes with `orbit ls` | #3 | E01-S01 (#5) | no |
| E03 | [#4](https://github.com/cultofthecode/orbit/issues/4) | Later | — | — | no |
