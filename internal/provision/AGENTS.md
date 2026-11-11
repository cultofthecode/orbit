# AGENTS.md — internal/provision

What root sets up in a sandbox machine before any agent runs: the firewall, the proxy units, the hardened users, the agent user, and the fixed toolchain (D14, D17, D20, D22). Spec: `.ai/specs/2026-11-10-sandbox-provisioning.md`. Read the root `AGENTS.md` and `ARCHITECTURE.md` (Provisioning requirements) first. Every change here is `risk-high` and needs the integration test.

## Layout

- `firewall.go` — `Firewall(FirewallInput)`: `/etc/nftables.conf`, one table `inet orbit_egress`. Golden file: `testdata/firewall.nft` (`go test -update` after a review of the change).
- `units.go` — `Units(defaultUID)`: the systemd units and drop-ins, and the harden script.
- `script.go` — `Script(ScriptInput{DefaultUID, AgentDomains, ForwardSSHAgent})`: the root script that `init` runs through `RunAsRoot`. The proxy allowlist is `policy.Allowlist(agentDomains)`; `nil` means no agent.
- `ssh.go` — `00-orbit.conf` and `ssh_known_hosts` (golden files `testdata/00-orbit.conf` and `testdata/ssh_known_hosts`).
- `check.go` — `BootCheckScript()` and `MarkerCheckScript()`: the root scripts that `start` runs after `orb start`; `ReadinessScript()` for `shell`. The boot check waits at most 120 s for the boot and then runs `boundaryCheck`, the same table and unit checks as the self-check of `Script`; the marker check fails unless `MarkerPath` holds exactly `Version`. They are two scripts because `start` stops the machine again when the boot check fails and leaves it running when only the marker check fails. `ReadinessScript()` is the root script that `shell` runs on a running machine before it opens the shell: the marker check and `boundaryCheck`, without the boot wait. `orb -u agent` sessions do not pass the login gate of `systemd-user-sessions.service` (#74), so `shell` checks the boundary itself.

## Invariants

- The firewall filters every uid from 1000 up and the default uid (the Mac uid, below 1000). The rules for the proxy user come before the accept for uids below 1000. The proxy user reaches only port 443 on public addresses and port 53 on OrbStack DNS.
- `Firewall` refuses input that would weaken the rules: a default uid of 0, a proxy user that is `root` or not a plain name, a proxy port of 0 or 53, a proxy address that is neither loopback nor `proxy.ContainerAddr`, and an unspecified or multicast resolver. Keep these refusals and their hostile-input tests.
- `systemd-user-sessions.service` requires and follows every unit of the boundary, so no login starts while one of them failed.
- The script is idempotent and fail-closed: it removes the marker first and writes it last, loads rules with `nft -f` and never restarts `nftables.service` (its stop flushes the ruleset), writes every file through `put` (a temporary file and a rename), and creates the agent only after the boundary runs.
- Every unit that the script restarts, directly or through `Requires=` (`tinyproxy.service`, `orbit-harden.service`, `systemd-user-sessions.service`), sets `StartLimitIntervalSec=0`, so that repeated `init` runs never hit the start limit of systemd (#66). The script starts `systemd-user-sessions.service` after the restarts, and the self-check requires it to be active.
- The script never removes, moves, or recreates the agent user or anything under `/home`: the login of the agent (D08) lives in its home. It creates `agent` only when missing and refuses `userdel`, `deluser`, and every `usermod` of `agent` except `usermod -aG orbstack agent` (`changesTheAgentUser`). Removing the machine is the only thing that removes the login.
- Root never writes into a home directory. The script adds the agent to exactly one group, `orbstack`, and only when `ScriptInput.ForwardSSHAgent` is true; the self-check fails in both directions (a missing membership on a forwarding machine, a membership on one that does not forward). `Version` is 2: provisioning writes `netcat-openbsd`, `/etc/ssh/ssh_config.d/00-orbit.conf`, `/etc/ssh/ssh_known_hosts`, and the group, and `shell` and `start` read "marker == `Version`" as "provisioned by this build".
- `00-orbit.conf` sorts before `10-orbstack.conf`: `ssh` takes the first value it reads, and OrbStack points every user at a socket that only the default user can open. `ProxyCommand` calls `/usr/bin/nc.openbsd` by its absolute path, with the address of `proxy.LoopbackAddr` and `proxy.Port`. The pinned GitHub host keys in `ssh.go` are checked against the fingerprints of `api.github.com/meta` by a test.
- The script unsets the proxy variables: a login of root gets them from `/etc/environment`, and root installs packages directly.

## Validation

- `go test ./internal/provision/...` needs no OrbStack.
- `go test -tags integration -count=1 -run TestInitProvisionsTheSandbox -v ./cmd/orbit/` provisions a real machine twice and probes every scenario except the restart. `TestInitRunsAgainAfterRepeatedRuns` re-runs `init` on one sandbox after a burst of restarts of the harden unit, and checks that user sessions stay active and `/run/nologin` is gone. Check the restart by hand: `orb restart <machine>`, then the probes of the restart scenario.
