# Backlogs

## .ai/specs/product-brief.md
Backlog source: .ai/specs/product-brief.md
Last filed: 2026-11-11

Parents are linked as GitHub sub-issues. The epics carry no checklist. E04 comes from the owner's request of 2026-11-11. The brief does not record it yet: decision 1 of epic #46 (taken by the coordinator under the owner's delegation of 2026-11-11) adds D25 and moves E04 into *Scope → Now*, and E04-T02 (#48) writes that row into the brief.

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
| E04 | [#46](https://github.com/cultofthecode/orbit/issues/46) | Choose the Linux distribution of the sandbox machine: Alpine by default, Debian and Ubuntu as fallbacks | — | E01-S01 (#5) | no |
| E04-T01 | [#47](https://github.com/cultofthecode/orbit/issues/47) | Spike OrbStack's Alpine and Debian images and record what differs from Ubuntu 26.04 | #46 | — | no |
| E04-T02 | [#48](https://github.com/cultofthecode/orbit/issues/48) | Write the sandbox distribution spec and record the owner's answers in the brief | #46 | E04-T01 (#47) | no |
| E04-S01 | [#49](https://github.com/cultofthecode/orbit/issues/49) | The owner can run `orbit init --distro debian` or `--distro ubuntu` and gets the same sandbox boundary on either distribution | #46 | E01-S01 (#5), E04-T02 (#48) | no |
| E04-S01-T01 | [#50](https://github.com/cultofthecode/orbit/issues/50) | Add the distribution to init, the `--distro` flag, and the adapter's pinned image map | #46 (story #49) | E04-T02 (#48) | no |
| E04-S01-T02 | [#51](https://github.com/cultofthecode/orbit/issues/51) | Make the root script distribution-aware for Debian and Ubuntu, and refuse a mismatched `--distro` on an existing sandbox | #46 (story #49) | E04-S01-T01 (#50) | no |
| E04-S02 | [#52](https://github.com/cultofthecode/orbit/issues/52) | The owner can run `orbit init --distro alpine --agent claude-code` and gets an Alpine sandbox with the same boundary and Claude Code installed | #46 | E04-S01 (#49) | no |
| E04-S02-T01 | [#53](https://github.com/cultofthecode/orbit/issues/53) | Run the root script and the agent install on a machine without bash | #46 (story #52) | E04-S01-T01 (#50) | no |
| E04-S02-T02 | [#54](https://github.com/cultofthecode/orbit/issues/54) | Provision Alpine as root: apk, OpenRC services for the firewall and proxy, login gate, agent user, hardening | #46 (story #52) | E04-S01-T02 (#51), E04-S02-T01 (#53) | no |
| E04-S02-T03 | [#55](https://github.com/cultofthecode/orbit/issues/55) | Install Claude Code on Alpine (musl) as the agent user | #46 (story #52) | E04-S02-T02 (#54) | no |
| E04-S03 | [#56](https://github.com/cultofthecode/orbit/issues/56) | The agent can build and run containers with rootless Docker on an Alpine sandbox | #46 | E01-S05 (#9), E04-S02 (#52) | no |
| E04-S04 | [#57](https://github.com/cultofthecode/orbit/issues/57) | `orbit init` without `--distro` creates an Alpine sandbox, after the escape suite passes on all three distributions | #46 | E04-S02 (#52), E04-S03 (#56), E01-S06 (#10) | no |
