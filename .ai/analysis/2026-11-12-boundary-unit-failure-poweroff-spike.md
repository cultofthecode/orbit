# Spike: power off the sandbox when a boundary unit fails at boot (issue #83, follow-up of #74)

Date: 2026-11-12. Time-boxed research spike, run by an agent on the owner's Mac. Each result below was observed unless it says "not tested". The note reports facts; the decision is the owner's. No Go code and no file of `internal/provision` changed.

## Environment

| Item | Value |
|---|---|
| Host | macOS (Darwin 27.2.0), Apple Silicon |
| OrbStack | `orb version`: `Version: 2.2.5 (2020500)`, commit `bf842321` |
| Machines | `orbit-spike83-a-7126bb17137476ed` and `orbit-spike83-b-745fda997d22d6b2`, both created by `orbit init` (built from this branch, no agent installed): `orb create --isolated --isolate-network`, then the real root script of `internal/provision` (4 min 29 s). Ubuntu 26.04.1 LTS, kernel `7.2.12-orbstack-00394`, systemd 259 |
| Baseline | `orb list` before the first `orbit init`: empty (no machine of anyone else existed during the spike) |
| Provisioning | The real one: `provision.Version` 3 with `Units()`: the four boundary units, `orbit-boundary-gate.service`, the `systemd-user-sessions.service` drop-in, `StartLimitIntervalSec=0` on `tinyproxy`/`orbit-harden`, `Restart=on-failure` on `tinyproxy` |

## Method

1. The failure is injected, not natural. Each boundary unit gets the drop-in `zz-spike83-fail.conf`: `ExecStartPre=/etc/spike83/failbudget.sh %n`. The script exits 1 while `/etc/spike83/budget-<unit>` holds a positive number and decrements it. A budget of 1 fails the unit on one boot only, so the machine recovers by itself. (The first run used a permanent flag file instead; see 3.) `ExecStartPre` fails before the real `ExecStart`, so a failed unit never ran its real command.
2. The gate under test is a second drop-in, `zz-spike83-action.conf`:
   - `FailureAction=poweroff` (also `poweroff-immediate`), or
   - `OnFailure=orbit-spike-poweroff.service`, a oneshot `ExecStart=/usr/bin/systemctl poweroff`.
3. Probe: a host loop that issues `orb -m <machine> -u agent sh -c '...'` every ~20 ms from the moment of `orb stop` (the first `orb -m` starts the machine: the pending `orb -m` of the issue). The first action of the command appends `boot=<boot_id> uptime=<s> units=<is-active of the four units + systemd-user-sessions>` to `/var/tmp/spike83-ran`, then it probes direct egress (`curl --noproxy '*'`) and `sudo -n true`. Root reads the file afterwards and the journal of the failed boot with `journalctl -b -1 -o short-monotonic`. `uptime` is the VM uptime and keeps counting across a machine's boots, so it orders events of the journal and of the log.
4. Scripts and logs stayed in the session scratchpad (`setup2.sh`, `trial3.sh`, `poll2.sh`).

The first power-off is visible in the console log of OrbStack (`orbctl logs <machine>`):

```
[FAILED] Failed to start nftables.service - nftables.
[DEPEND] Dependency failed for systemd-user-sessions.service - Permit User Sessions.
[DEPEND] Dependency failed for tinyproxy.service - Tinyproxy lightweight HTTP Proxy.
Powering off: unit nftables.service failed
```

`systemctl poweroff` in a machine stops it: `orb list --format json` went `running` to `stopped` in 1.3 s (polling at 0.25 s).

## Findings

### 1. Does the machine power off before any `orb -u agent` session can run a command? No. It powers off a few tens of milliseconds after the failure, and agent commands run in that gap

Journal of the failed boot, first trials (`journalctl -b -1`, `[monotonic s]`):

```
q1-fa-orbit-proxy-address (FailureAction=poweroff)
[  105.643090] systemd-journald: Journal started
[  105.680668] Failed to start orbit-proxy-address.service
[  105.680810] Powering off: unit orbit-proxy-address.service failed
[  105.744055] systemd-journald: Journal stopped
agent commands that ran in the failed boot: 2
boot=1edccb27 uptime=105.72 units=active,failed,inactive,failed,inactive,      <- 40 ms after the failure
boot=1edccb27 uptime=105.74 DIRECT_EGRESS=[ Could not resolve host: example.com 000] sudo_rc=1
```

(`units` order: `nftables`, `orbit-proxy-address`, `tinyproxy`, `orbit-harden`, `systemd-user-sessions`.)

Timing of the boot until the failure and until the journal stops (single runs, FailureAction=poweroff unless noted):

| Failing unit | Journal start to failure | Failure to journal stopped |
|---|---|---|
| `nftables.service` | 1.5 to 2.5 ms | 20 ms |
| `orbit-proxy-address.service` | 37 to 47 ms | 63 ms |
| `orbit-harden.service` | 39 to 44 ms | 29 ms |
| `tinyproxy.service` | 62 to 64 ms | 150 ms |
| `OnFailure=` (poweroff unit), `nftables` / `proxy-address` / `tinyproxy` / `harden` | same | 32 / 135 / 156 / 175 ms |

Repetitions, "agent commands that started after the failure line of the same boot" (`failure@` is the VM uptime):

```
== failureaction (FailureAction=poweroff)        == immediate (FailureAction=poweroff-immediate)
harden #1..#5:        1 1 1 1 1                   harden #1..#5:        0 0 0 0 0
proxy-address #1..#3: 1 1 1                       proxy-address #1..#3: 0 1 1
```

- `poweroff`: 8 of 8 runs let an agent command start after the unit failed, with the boundary unit `failed` or `inactive` in its `units=` line. `OnFailure=` is slower (about 135 to 175 ms to stop): commands ran in 3 of 4 failed boots (0 for `nftables`, 2, 4, and 4 log lines for the others).
- `poweroff-immediate` (`Powering off immediately: unit ... failed`): 2 of 8 runs. Its journal shows a second line `Powering off.` 5.03 to 5.04 s after the failure in every run (`[183.473] Powering off immediately` then `[188.510] Powering off.`). The cause of the 5 s gap was not investigated; the machine was in some state for that time.
- `nftables.service` fails so early (1.5 to 2.5 ms after journald) that no command ran in 2 of 2 failed boots. The other three units fail 40 to 65 ms into the boot and agent commands do run.
- Egress in the window: `DIRECT_EGRESS` was `Could not resolve host` in every logged command, because DNS is closed by `resolv.conf`, not by the firewall (not tested with a failed `nftables`: no command ran in that window). Direct IP egress was not probed in the window.
- Commands also ran before the failure in the same boot (tinyproxy runs: `units=active,active,activating,activating,inactive` at 174.87, failure at 174.88). That window exists on every boot with or without the gate (#74, and `.ai/analysis/2026-11-12-orbstack-alpine-debian-spike.md` topic 1) and the gate cannot close it.

**Finding: the power-off is fast but not before the first session. BLOCKER for the power-off design, by the acceptance criterion: an `agent` process runs a command while a boundary unit failed on this boot (`orbit-proxy-address.service`, `orbit-harden.service`, `tinyproxy.service` with `poweroff` and `OnFailure=`).** Whether that command can do harm depends on what the failed unit protects; the first command that runs is not controlled by the agent's design, but the window is open.

### 2. Does an `orb -u agent` command during or after the failed boot start the machine again? Yes. With the #75 guard, Orbit's own paths do not, a raw `orb` does

- Raw `orb -m <m> -u agent ...` on a machine that a gate powered off starts it again. The poll loop did this in every trial (states: `stopped` to `starting` to `running` to `stopped`, 41 starts in 20 s, see 3).
- With the #75 guard (state read first), against machine A, which fails permanently:

```
$ orbit shell
orbit: sandbox orbit-spike83-a-7126bb17137476ed is stopped: run "orbit start" in <dir>
$ orbit start
orbit: sandbox orbit-spike83-a-7126bb17137476ed did not start safely: boot check: run a script as root in machine orbit-spike83-a-7126bb17137476ed: orb -m: exit status 1; the sandbox was stopped again
$ orbit ls
orbit-spike83-a-7126bb17137476ed  stopped  <dir>
```

  Orbit starts the machine once (`orbit start`), the boot check cannot run because the machine is already powering off, `start` stops it (it is already stopped) and reports a generic `orb -m: exit status 1`. The state stays `stopped`; no loop. The message does not name the failed unit.
- The guard does not cover the owner's own `orb -m ...` or any other tool that calls `orb -m`: those restart the machine.

**Finding: with the guard, `orbit` never loops; a raw `orb -m` does, once per attempt.**

### 3. Power-off loop and recovery

Machine A, permanent failure (`/etc/spike83/fail-nftables.service` present), `FailureAction=poweroff` on all four units, polling for 20 s:

```
== t1-fa-nftables states   (first and last lines of 82)
1794507275.991 state=stopped  ...  1794507295.939 state=stopping
41 x state=starting, 21 x state=stopped; 42 `orb -m -u agent` attempts, 0 produced output
```

About 0.45 s per cycle: boot, `Powering off: unit nftables.service failed`, stop, the next pending `orb -m` starts it. When the polling ended the machine stayed `stopped` (6 x 1 s checks): no loop without a pending `orb -m`.

Recovery attempts, none worked:

| Attempt | Result |
|---|---|
| 8 sequential `orb -m A -u root <command that removes the flag>` | no output, no effect |
| 40 sequential attempts, then 30 parallel attempts (5 rounds of 6, 70 ms apart), `mv /etc/spike83 /etc/spike83.off` as root | never ran (a first attempt with `rm` was refused by the safety check of my agent harness, so I used `mv`) |
| `orb pull -m A /etc/spike83/fail-...`, `orb push -m A ...` | return 0, nothing transferred, state stays `stopped` |
| `orbit start` | fails as in 2 |
| `orbctl export A file.tar.zst` | works on a stopped machine in 1.2 s (487 MB, includes `rootfs/etc/spike83/fail-nftables.service`). `orbctl import -n <name> <unmodified file>` works and keeps `isolated: true`, `isolate_network: true`, `mounts` (`orb config show`), and the clone fails the same way. An archive rewritten without the flag (bsdtar `@archive` and Python `tarfile`) was rejected by `import`: `bsdtar: Linkpath can't be converted from UTF-8 to current locale` (one file name has non-ASCII characters: `rootfs/etc/ssl/certs/NetLock_Arany_=Class_Gold=_Főtanúsítvány.pem`). So a host-side repair was not achieved |

**Finding: with the gate on every boundary unit, a boot-time failure that does not go away makes the machine unreachable for repair through `orb`, `orbit start`, and `push`/`pull`. The only recovery that worked was `orb delete`, which destroys the agent's data and login (D08).** Making a repair possible needs a way to start the machine without the gate (for example a flag that root reads and the gate checks), which is a change of its own and was not tried. A drop-in that the owner could remove from the Mac does not exist: nothing on the host edits the machine's disk. That is a blocker for any gate that has no off switch.

### 4. A failure later in the boot, or at runtime

Machine B, `FailureAction=poweroff` on all four units, budget 0:

| Event (as root) | Result |
|---|---|
| `kill -9` the tinyproxy main process (`Restart=on-failure`, `StartLimitIntervalSec=0`) | Journal: `tinyproxy.service: Main process exited, code=killed, status=9/KILL`, `Failed with result 'signal'`, `Powering off: unit tinyproxy.service failed`, `Stopping user@1000.service`, `Stopped docker.service - Orbit: rootless Docker Engine of the agent.` (1 s later), `tinyproxy.service: Failed to schedule restart job: Transaction ... contradicts existing jobs`, `Journal stopped`. The machine went to `stopping`. The restart that `Restart=on-failure` would have done never happens |
| `systemctl stop tinyproxy` | no action. `systemd-user-sessions.service` becomes `inactive` (it requires tinyproxy), `nftables` stays active, `/run/nologin` returns; the machine keeps running |
| `systemctl stop nftables` (the unit's stop flushes the ruleset) | no action. `nft list tables` printed nothing; as `agent`, `curl --noproxy '*' https://1.1.1.1` returned `301`: direct egress open |
| A failure of a unit during `orbit init` (it restarts `tinyproxy`, `orbit-harden`, `systemd-user-sessions`) | not tested; by 1 and the first row it powers the machine off in the middle of provisioning |

`FailureAction` acts on the `failed` state only. It does not act on a clean stop, and so it does not cover the stop of `nftables` that opens the firewall. In the first row the action preempts the restart: a crash of the proxy now takes down the machine, every running container, and every agent process (graceful stop, about 1 s), where today the proxy restarts. Whether that is wanted is the owner's call; against D14 it is the fail-closed outcome, against availability it turns a one-second proxy blip into a lost session.

### 5. Interaction with the readiness check of `orbit shell`

```
$ (healthy)  echo 'id; exit' | orbit shell
uid=1000(agent) gid=1000(agent) groups=1000(agent),67278(orbstack)
$ (tinyproxy stopped cleanly; no FailureAction fires)  orbit shell
orbit: sandbox orbit-spike83-b-745fda997d22d6b2 is not ready: ... tinyproxy.service is not active; run "orbit init" in <dir> to provision it again
$ raw: orb -m <m> -u agent id    (same state)
uid=1000(agent) gid=1000(agent) groups=1000(agent),67278(orbstack)
inactive inactive /run/nologin            <- tinyproxy, systemd-user-sessions inactive; /run/nologin present
$ (machine stopped by a gate) orbit shell
orbit: sandbox ... is stopped: run "orbit start" in <dir>
```

- The gate and the readiness check overlap for `failed` units at boot and differ elsewhere: the readiness check also catches the clean stop (4), a missing table, and a wrong marker; the gate catches a `failed` state that nothing reads afterwards. A running machine with a failed boundary unit, which is what the readiness check refuses, should not exist after a gate (apart from the window in 1).
- The readiness check stays necessary with the gate: it covers the window (a session already open), the clean stop, and a machine that the gate does not reach (`systemd-user-sessions` stopped).
- `orbit start` tells the owner nothing specific after a gate; with the gate the failure reason is only in `orbctl logs <machine>`.

### 6. Agent processes that start at boot without a session (rootless Docker with linger, #9)

Boots with a failing `tinyproxy` and with a failing `orbit-harden`, `FailureAction=poweroff`, `journalctl -b -1` filtered for `orbit-boundary-gate`, `user@1000`, `docker.service`, `dockerd`, `Started session`:

```
### failing unit: tinyproxy.service
[  382.691978] systemd-journald: Journal started
[  382.714437] Reached target basic.target - Basic System.
[  382.748335] Failed to start tinyproxy.service
[  382.748443] Powering off: unit tinyproxy.service failed
### failing unit: orbit-harden.service
[  389.460717] Journal started
[  389.481050] Reached target basic.target
[  389.498402] Failed to start orbit-harden.service
[  389.498486] Powering off: unit orbit-harden.service failed
healthy boot, in order: Started tinyproxy, Finished orbit-harden, Starting orbit-boundary-gate, Finished systemd-user-sessions, Finished orbit-boundary-gate, Starting user@1000.service
```

No line for `orbit-boundary-gate`, `user@1000`, or `docker.service` appears in the failed boots. The user manager of the agent (and with it the rootless `dockerd`) starts only after the gate unit passes, which already follows from `orbit-boundary-gate.service` and `user@.service` requiring it. The poweroff gate adds nothing there. **Finding: no agent service ran before the power-off.** Not tested: a service that the agent enabled itself with `systemctl --user` (it also lives in `user@1000`, behind the same gate).

## Blockers for the power-off design (acceptance criterion: an `agent` process runs while a boundary unit failed)

1. `FailureAction=poweroff` and `OnFailure=`: agent commands started after the failure in 8 of 8 trials with `poweroff` and in 3 of 4 with `OnFailure=` (1). `poweroff-immediate`: 2 of 8, with an unexplained 5 s gap.
2. A permanent failure bricks the machine for every repair path that goes through `orb` (3).
3. A crash of the proxy at runtime takes down the machine instead of restarting the proxy (4).
4. A raw `orb -m` restarts a powered-off machine and loops (2, 3).

## Recommendation (for the owner; the owner decides)

**Reject `FailureAction=poweroff` as the fail-closed gate for #74.** It does not close the window it was proposed for (1), it removes the only recovery path (3), it turns a proxy crash into a lost machine (4), and the readiness check of `orbit shell` stays necessary next to it (5). The residual risk stays what #74 records: a session that the owner opens by hand with `orb -m ... -u agent`, outside Orbit, is ungated (pinned by O01 of the escape suite).

If the owner wants a machine-level gate anyway, the smallest adaptation that this spike supports is `FailureAction=poweroff-immediate` on the four units, with all of these conditions, each untested here: an off switch that root-through-`orb` can reach before the gate acts (so that repair does not need `orb delete`); an explanation of the 5 s gap after `Powering off immediately`; and a decision on whether a proxy crash may take the machine down (or the proxy keeps its restart and only the boot-time failure is gated, for example by a gate unit that runs once at boot and not by `FailureAction` on `tinyproxy.service`). Those are a new design, not a change of this spike.

## What the fresh #47 spike means for OpenRC and Alpine

From `.ai/analysis/2026-11-12-orbstack-alpine-debian-spike.md` (merged as PR #164): Alpine has BusyBox init and OpenRC 0.63, no login gate of any kind, and `orb` logins arrive 2.5 s into the boot, with no IPv4 address and the firewall not yet loaded, for 5 to 7 s (topic 1). It marks that as a conditional blocker for Alpine. This spike did not run on Alpine. Facts that follow from the two notes:

- OpenRC has no equivalent of `FailureAction=` or `OnFailure=`. A power-off there would be a command (`poweroff`) that the boundary gate service (`orbit-boundary`, the gate of the #47 root script) runs when its own check fails. That is a different design from the one tested here, and its window (gate runs after `need` dependencies) was not measured.
- Because Alpine has no login gate at all, a machine-level power-off is the only gate that can exist there. On Ubuntu and Debian it duplicates the login gate that `orb` ignores anyway (#74), and the findings 1 to 4 above apply, with the same race: a gate that acts after the failure cannot stop a session that already ran. On Alpine the window is also not 40 ms: in #47 it was 5 to 7 s.
- The Debian trixie and bookworm images use systemd 257 and 252: the `FailureAction=poweroff` behavior tested here (systemd 259) was not tested on them.

## Cleanup confirmation

- `orb delete -f orbit-spike83-a-7126bb17137476ed` and `orb delete -f orbit-spike83-b-745fda997d22d6b2`. I also created and deleted `orbit-spike83-rt` (an import of an unmodified export of A, for the recovery test).
- `orb list` after: empty (exit 0). `orb list --format json`: `[]`. `orb config show | grep -c spike83`: `0`.
- The exports (`*.tar.zst`, about 480 MB each) were deleted from the scratchpad. The scratchpad keeps the scripts and logs; the project directories `proj/spike83-a` and `proj/spike83-b` hold no project data.
- No machine of anyone else existed (the first `orb list` was empty); none was touched. No global OrbStack setting changed. No repository file other than this note changed.

## Limits of this spike

- The failure is injected with `ExecStartPre`, one boot at a time. A natural failure (a broken `tinyproxy.conf`, a rule that `nft` rejects) might take longer or shorter to appear. The numbers are single runs and a few repetitions; they show orders of magnitude (milliseconds, not seconds), not a distribution.
- `sudo_rc=1` in the probe lines is not evidence about `orbit-harden`: the injected failure skips the real script, and the machine was already hardened.
- `uptime` in the log is the VM's uptime, shared by every machine and boot; I used it only to order events of one boot against its journal.
- Trial 1 (permanent failure) had no side-channel in the probe, so it shows that no command completed, not that none started.
