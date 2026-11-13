# Alpine sandboxes in release 0.2: `--distro alpine`, then Alpine as the default

Issues: epic #46 (E04); E04-S02 #52 with its tasks #53 (E04-S02-T01), #54 (E04-S02-T02), #55 (E04-S02-T03); E04-S03 #56; E04-S04 #57.
Evidence: `.ai/analysis/2026-11-12-orbstack-alpine-debian-spike.md` (#47, cited as "spike 47, topic N"), `.ai/analysis/2026-11-12-boundary-unit-failure-poweroff-spike.md` (#83, cited as "spike 83").
Extends: .ai/specs/2026-11-12-sandbox-distributions.md
Extends: .ai/specs/2026-11-10-sandbox-provisioning.md
Extends: .ai/specs/2026-11-11-rootless-docker.md
Extends: .ai/specs/2026-11-11-claude-code-agent-definition.md
Extends: .ai/specs/2026-11-11-orbit-shell.md
Extends: .ai/specs/2026-11-11-orbit-stop-start.md
Extends: .ai/specs/2026-11-12-escape-suite.md

## 📝 TLDR

The owner wants Alpine sandboxes, with Debian and Ubuntu as fallbacks. On 2026-11-12 the owner deferred Alpine to release 0.2, both as a choice and as the default. Release 0.1 ships `--distro ubuntu|debian` (`.ai/specs/2026-11-12-sandbox-distributions.md`, "the 0.1 spec"). This spec designs 0.2. `orbit init --distro alpine` will create an Alpine 3.23 sandbox with the same boundary, toolchain, Claude Code client, and rootless Docker as on Ubuntu. After that, a separate change makes Alpine the default for new sandboxes, but only when every gate of the 0.1 spec's Q5 has passed. The design adds one more provisioning family, OpenRC, next to apt/systemd. It adds a POSIX bootstrap that installs bash before any bash script runs, and Orbit's own OpenRC readiness and boot checks on every Orbit entry path. The agent's engine runs as a supervised system service, because Alpine has no systemd user manager. OrbStack is blocked on the owner's Mac while this is written. Every claim that needs a running machine and that spike 47 did not observe is marked **[UNVERIFIED]**, with a verification step in the Verification ledger.

## Resolved assumptions (autonomous defaults)

These were the open questions of this spec. Each was answered with the most reversible default (autonomous mode), and the owner can override any of them before merge. Questions that the 0.1 spec already decided (its Q2–Q7) are not asked again. Their answers apply here and are cited where used.

| # | Question | Applied default | Why |
|---|---|---|---|
| Q1 | One spec for S02, S03, and S04, or three? | **One spec, with one phase per story.** Each phase ships as its own PR. | The three stories share the OpenRC family, the readiness checks, and the evidence runs. Three specs would repeat that design three times. The 0.1 spec did the same for S01. Splitting later only means moving sections. |
| Q2 | How does root run scripts on a machine without bash (#53)? | **A POSIX bootstrap.** A new adapter call, `RunAsRootPOSIX` (`orb -m NAME -u root sh -s`), runs `provision.BootstrapScript(distribution)` before the root script, on every distribution. The bootstrap checks `/etc/os-release` and installs `bash` when it is missing. Every other script keeps `bash -s`. | This was the 0.1 spec's direction (topic 3). Epic decisions 4 and 7 already put bash on Alpine. A fully POSIX root script would rewrite all of `internal/provision`. Its only extra gain, bookworm's dash, is not pinned. |
| Q3 | Does the firewall use the stock OpenRC `nftables` service with an Orbit `rules_file`, as the 0.1 spec's design input said? | **No. An Orbit-owned service, `orbit-firewall`, in the `boot` runlevel, whose `stop` leaves the table loaded.** The stock `nftables` service stays in no runlevel, and its `rules_file` points at the Orbit rules file. | The stock `stop()` flushes the ruleset (spike 47, topic 4). The owner decided on #168 that a root stop must keep egress closed (PR #191 does this for systemd). Overriding `stop()` from `/etc/conf.d/nftables` would depend on the order in which `openrc-run` sources the configuration and the script, which nobody has tested; an Orbit-owned script does not depend on it. |
| Q4 | Can `alpine` enter the enum before rootless Docker works on Alpine (S02 before S03)? | **No. The value lands once, in one PR, with the S02 and S03 evidence.** The S02 tasks and S03 build the renderer behind unit tests and golden files. Then a single step adds `alpine` to the three tables and runs the full integration suite and the escape suite on Alpine. There is no hidden or experimental flag. | `init` always runs the Docker client setup, so an `alpine` value without an engine would fail on every `init`. The 0.1 spec put `debian` on `main` only together with its evidence. A hidden flag is an option that nobody asked for. |
| Q5 | What if the boot-window attempt W01 finds direct egress by the agent uid before the firewall loads? | **It blocks step 13 (the `alpine` value), not only the default.** Step 13 first tries the mitigations of topic 4: `orbit-firewall` in `sysinit`, and IPv6 autoconfiguration off on `eth0` until the firewall is loaded. If W01 still fails, step 13 stops until the owner either accepts the measured window in writing, as a known limit next to O01, or rejects Alpine. | Direct egress by the agent uid is an R03 matter, and accepting it is the owner's call, not a default. Only a raw `orb -u agent` can run in the window, so the owner may well accept it, but a spec default must not. Spike 47 measured IPv4 as closed and did not check IPv6. |
| Q6 | `--privileged` starts a container on Alpine (spike 47, topic 9). Q1 of the 0.1 spec allowed this on Debian 13 trixie only. | **This spec does not decide it.** Step 13 adds `alpine` and stops until the owner answers. The recommended answer is to allow it with the same proof as Debian: E18–E22 and M02 on Alpine, and a brief row R11 that names Alpine (extended to Alpine if the 0.1 run proposed R11 for Debian, or proposed for Alpine alone if it did not; see "Proposed brief changes"). Without a yes, Alpine cannot ship, because Orbit cannot refuse the flag (the agent owns its daemon). | The 0.1 spec, which the owner approved, already says that a privileged container that starts on another distribution "is a change that the owner decides". Deciding here would change an active `risk-high` requirement. Leaving it open costs nothing until step 13. |
| Q7 | Alpine's tinyproxy crashes with the Orbit `PidFile` line (no `/run/tinyproxy`). Do we drop the line, or create the directory at boot? | **Drop the line on the OpenRC family.** `proxy.Config` gains a pid-file argument, and `""` leaves the line out. Ubuntu and Debian pass today's path, so their bytes do not change. | Spike 47 observed that the version without the line works. Creating the directory with `checkpath` in an Orbit service is the alternative; it was not tested and it would give OpenRC two pid files. |
| Q8 | Does the in-machine guard require an exact Alpine release (`3.23.8`) or any 3.23 patch release? | **Any 3.23 patch release.** `ID=alpine` and `VERSION_ID` matching `3\.23\.[0-9]+`. | The pin is `alpine:3.23`. OrbStack resolved it to 3.23.8 in the one spike run, and it presumably follows the newest 3.23.x **[UNVERIFIED, V2]**, so an exact guard would break every new sandbox at the next patch release. A minor release (3.24) is a new pin, and a new pin needs its own evidence run. |

**Owner gates at implementation time** (they do not block the merge of this spec; the step that needs them stops until the owner answers):

- **Before step 13:** Q6. Q5, only if W01 fails after the mitigations. The brief rows that step 13 would otherwise contradict: D30 (superseding D20, the Alpine toolchain), the opt-in clause of D29 (superseding D28, the enum with `alpine`), and, if Q6 is yes, a brief row that names Alpine for privileged containers: a new R11 that supersedes R10, or, if an R11 for Debian is already active, a new row that supersedes that R11 (see "Proposed brief changes").
- **Before step 15:** the default clause of D29 and the *Scope → Now* line.

## 📝 Problem Statement

- The owner asked on 2026-11-11 for Alpine by default, with Debian and Ubuntu as fallbacks (#46). On 2026-11-12, after spike 47, the owner deferred Alpine to 0.2 (#48), because Alpine needs a second implementation of the boundary and has open gaps.
- Every provisioning step after the prelude assumes apt and systemd: `installPackages`, `staticDNS` (systemd-resolved), `Units`, `startBoundary`, `selfCheck`, `boundaryCheck` (`systemctl`), `sshCheck` (`dpkg -S`), and the rootless engine as a systemd user unit (`internal/provision/{script,units,docker,check,ssh}.go`). The adapter runs every script with `bash -s` (`internal/orbstack/orbstack.go`, `runAsRootArgs`, `runAsAgentArgs`), and the Alpine image has no bash (spike 47, topic 2).
- `orbit start` and `orbit shell` check the boundary with scripts that take no input: `provision.BootCheckScript` and `ReadinessScript` use `systemctl`, and `MarkerCheckScript` compares the marker with `cmp` (all three injected in `cmd/orbit/main.go`). On Alpine those scripts are the only gate on Orbit's paths: Alpine has no login gate of any kind, and at first contact after a boot every boundary service was still stopped, in 8 of 8 runs (spike 47, topic 1).
- The 0.1 spec, Q5, names the four conditions that gate Alpine and Alpine as the default. This spec turns each condition into a design and a test.

## 📝 Prior spec drift

- `.ai/specs/2026-11-12-sandbox-distributions.md`, topics 1, 2, and 7: **not implemented yet.** PR #189 (E04-S01-T01, #50) adds `sandbox.Distribution` with `ubuntu` only, the pinned image table, and `Create(ctx, name, dir, key, forward)`. #51 (profiles, the os-release guard, `debian`) has no PR yet. This spec builds on the 0.1 design as written. Its Phase 0 waits for #49–#51 to merge.
- The same spec, topic 4, design-input table, row `nftables.service`: "stock `nftables` service in `default`, `rules_file` pointed at an Orbit-owned path". PR #191 (#168, open) shows that the owner wants a root stop of the firewall service to keep the table loaded. The stock OpenRC `stop()` flushes the ruleset. This spec replaces that row (Q3).
- The 0.1 spec's Delta has not been archived into `.ai/specs/capabilities/` yet. The MODIFIED entries below that name 0.1 requirements ("Distribution of a new sandbox", "The same boundary on every distribution", and the 0.1 versions of "Init of a project directory" and "Privileged containers on the tested OrbStack version") apply after Phase 0, once that archive step has run.
- The same spec, topic 10, reserves escape ids E18–E22 and M02 for the privileged attempts. PR #191 (open) names its cases `E23-nftables-stop` and `E23-nftables-restart` in `TestEscapeRestart`, so the ids no longer collide. This spec cites the privileged attempts as "E18–E22 of the 0.1 spec". Its own new case has the id W01, in a `W` (boot window) group that step 16 adds to the catalogue of the escape-suite spec, next to E (attempt), O (observation), and M (meta).
- `internal/provision/script.go:16` has `Version = 3`. PR #191 raises it to 4. Alpine machines are new, so this spec bumps nothing (topic 2).
- The same spec, topic 2, lists `proxy.Config` among the shared pieces whose signatures do not change, and says the bootstrap is empty for the apt family (topic 3). This spec changes both: `proxy.Config` gains a pid-file argument (Q7), `sshFiles` gains the netcat path, the harden script gains a group list (topic 2), and the bootstrap on Ubuntu and Debian holds the guard and the `bash` check, at the cost of one more `orb` call per `init` (topic 3). A non-empty apt bootstrap gives every distribution the same call order, so no slice compares names, and it refuses a wrong machine before the root script runs.

## 📝 Proposed Solution

### Topic 1: the `alpine` value, its pin, and the stored image

- `sandbox.Distributions()` becomes `ubuntu, debian, alpine`. Appending keeps the order stable. The refusal message of the 0.1 spec becomes `unknown distribution "<v>"; use one of: ubuntu, debian, alpine`.
- Adapter pin: key `alpine` → `orb create … alpine:3.23`. Stored `image.distro` / `image.version`: expected `alpine` / `3.23` **[UNVERIFIED, V1]**. The Ubuntu row stores a codename (`resolute`), not the version that was passed, so the Alpine spelling must be captured, not guessed. Step 13 commits the real `orb list --format json` capture as the fixture of `parseDistribution`, and `TestInitPinsTheStoredImage` pins it.
- Provision profile `alpine`: family `openRC`, guard `ID=alpine` and `VERSION_ID=3\.23\.[0-9]+` (Q8; content **[UNVERIFIED, V2]**: spike 47 recorded "Alpine Linux v3.23.8", the pretty name, not the file).
- The composition test of the 0.1 spec (`TestEveryDistributionHasAnImageAndAProfile`) holds the three lists equal. No slice and no agent definition compares a distribution name. The rule of the 0.1 spec, topic 1, applies unchanged.

### Topic 2: the OpenRC family in `internal/provision`

`profile.family` gains a second value, `openRC`. `Script(ScriptInput)` dispatches on the family to one of two private renderers: the existing apt/systemd body (unchanged), and a new `openRCScript` in `internal/provision/openrc.go`. The pieces that the 0.1 spec calls shared stay shared: `Firewall`, `policy.Allowlist`, `putFile`/`writeFiles` (signatures unchanged), and `sshFiles` and the harden script, which each gain one argument from the profile. Today `sshFiles()` takes no argument and uses a constant netcat path (`internal/provision/ssh.go`), and `hardenScript` hard-codes `for group in sudo admin adm` (`internal/provision/units.go`) instead of reading `privilegedGroups`. The apt profiles pass today's values, so their bytes do not change. The `profile` struct gains data fields and no behavior:

| Field | apt/systemd (`ubuntu`, `debian`) | `alpine` | Evidence |
|---|---|---|---|
| `guard` | `ID=…`, `VERSION_CODENAME=…` (`grep -qx`) | `ID=alpine`, `VERSION_ID=3\.23\.[0-9]+` (`grep -Eqx`) | Q8; V2 |
| `packages` | unchanged | toolchain `git openssh-client curl ca-certificates jq ripgrep unzip bash libgcc`; infrastructure `nftables tinyproxy netcat-openbsd`; root helpers `shadow runuser setpriv iproute2-ss` | spike 47, topics 7 and 8; 0.1 Q6 (no `libstdc++`) |
| `netcatPath`, owner check | `/usr/bin/nc.openbsd`, `dpkg -S` | `/usr/bin/nc`, `apk info -W` | spike 47, topic 4 |
| `firewallPath` | `/etc/nftables.conf` | `/etc/orbit/firewall.nft` (Orbit-owned, not a package conffile) | spike 47, topic 4; Q3 |
| `proxyPidFile` | `/run/tinyproxy/tinyproxy.pid` | `""` (no line) | spike 47, topics 4 and 7; Q7 |
| `environmentPath` `PATH` | unchanged | without `/usr/games:/usr/local/games:/snap/bin` | spike 47, topic 6 |
| `privilegedGroups` (self-check) | unchanged | adds `wheel` | spike 47, topic 3 |
| `hardenGroups` (the groups the harden script empties of every account except root) | `sudo admin adm` (today's constant) | `wheel adm` | spike 47, topic 3 |
| `rootMembersAllowed` | none | `wheel`, `adm` (Alpine makes root a member of both) | spike 47, topic 3 |

The order of `openRCScript` mirrors the apt family, and each step stays fail-closed:

1. **Prelude**: `set -euo pipefail` (bash, installed by the bootstrap), the os-release guard of the profile, then `rm -f` of the marker. The guard runs before anything changes, as in 0.1.
2. **Packages**: `apk add --no-cache <packages> </dev/null`. Root installs directly from the Alpine repositories, outside the proxy (D17), as apt does today. No new egress host (spike 47, topic 8; epic decision 8).
3. **DNS**: the static `/etc/resolv.conf` of `staticDNS` (the stock file is a symlink into `/opt/orbstack-guest`, as on Ubuntu), plus one idempotent `nohook resolv.conf` line in `/etc/dhcpcd.conf`. Nothing needs masking: there is no systemd-resolved (spike 47, topic 5).
4. **Files**: the firewall rules at `firewallPath`; `/etc/conf.d/nftables` with `rules_file=<firewallPath>`, so that a stray start of the stock service loads Orbit's table and not the stock `flush ruleset` file; the init scripts of topic 4; `/etc/conf.d/tinyproxy` (`rc_need="orbit-firewall orbit-proxy-address"`); `/etc/conf.d/crond` (`rc_need="orbit-boundary"`); mode `0644` on `/etc/init.d/nftables` (topic 4); the tinyproxy configuration without `PidFile`; the allowlist; `/etc/environment`; `/etc/profile.d/orbit-path.sh` (topic 6); the SSH files; the harden script, which on this family also removes every file in `/etc/doas.d`, as it removes the files of `/etc/sudoers.d` on the apt family; and `/etc/doas.conf`, root-owned, mode 0400, with no `permit` line.
5. **Start the boundary** (topic 4): `rc-update` adds, then `nft -f <firewallPath>`, `rc-service orbit-firewall start` (never `restart`), `rc-service orbit-proxy-address start`, `rc-service tinyproxy restart`, `rc-service orbit-harden restart`.
6. **Agent user**: `useradd --create-home --shell /bin/bash --user-group agent`, `passwd -l`, the uid floor, and `orbstack` only with forwarding. These are the same lines as `createAgent`; `shadow` provides the tools.
7. **Gate and engine**: `rc-service orbit-boundary start` (never `restart`), then the engine of topic 7 (`rc-service orbit-docker start`, never `restart`).
8. **Self-check** (topic 5), then the **marker**.

`Version` is not bumped: an Alpine machine is new, and the apt bytes do not change (golden files). A golden `testdata/script-alpine.sh` pins the OpenRC bytes.

**Alternatives considered.** *Branches inside the apt renderer* (`if family == openRC`): every step differs, so the functions would carry two bodies each and the Ubuntu golden would be at risk with every Alpine change. Rejected. *A `distro` interface with methods per step:* an abstraction with two implementations and one caller. The private family switch is enough (AGENTS.md: no abstraction for single-use code).

### Topic 3: the bootstrap, and scripts without bash (#53)

```go
// BootstrapScript returns the POSIX sh script that root runs before Script on
// a machine of the distribution: it fails unless /etc/os-release matches the
// profile, and installs bash when the profile's family needs it and it is
// missing. The same input yields the same bytes.
func BootstrapScript(distribution string) ([]byte, error)
```

- The body is one braced group, so the whole script parses before any of it runs. A script truncated after the opening `{` fails to parse; a prefix that ends before it holds no command, so nothing runs, and the root script then fails on a machine without bash. A child cannot read the rest of standard input. These are the guarantees of `provision.group`, given here in POSIX syntax: `{`, `set -eu`, `fail()`, `unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy` (on a re-init, OrbStack gives root the proxy variables of `/etc/environment`, and root installs packages directly, as in the prelude of `Script`), the guard lines, `command -v bash >/dev/null 2>&1 || apk add --no-cache bash </dev/null`, `command -v bash >/dev/null 2>&1 || fail "bash is missing"`, `}`. There is no `pipefail`, which the script does not need. For the apt family the install line is left out, and the guard and the `command -v bash` check remain.
- Adapter: `RunAsRootPOSIX(ctx, name, script) error` runs `orb -m NAME -u root sh -s` with fixed arguments. It has the same preconditions as `RunAsRoot` (Orbit prefix, `State` running). `RunAsRoot` and `RunAsAgent` keep `bash -s`. The adapter runs no other new command.
- `init` order: after the mount check (`ActiveMounts` uses `cat`, which BusyBox has), `init` calls `RunAsRootPOSIX(bootstrap)` and then `RunAsRoot(script)`. It does this on every distribution, so no slice compares names. That adds one `orb` call per `init` on Ubuntu and Debian. A failed bootstrap on a machine that the call created deletes the machine, as a failed provisioning does today.
- `start` and `shell` never run the bootstrap. On a machine that never finished `init`, their `bash -s` check fails, and the slice reports `ErrNotProvisioned` or `ErrBoundaryDown` as today. That is fail-closed.
- The agent install keeps `curl -fsSL https://claude.ai/install.sh | bash`, because bash is in the Alpine toolchain (0.1 Q6). `agent.Definition` does not change (0.1 spec, topic 8).
- BusyBox ash parsing a braced group from `orb … sh -s` standard input before it runs it: expected from POSIX, **[UNVERIFIED, V3]**. The unit tests prove it for dash (the CI runner) and for bash, and the adapter integration test proves it on Alpine.

### Topic 4: OpenRC services, boot order, and the firewall stop (#54)

| Service (runlevel) | `depend()` | Start | Stop |
|---|---|---|---|
| `orbit-firewall` (`boot`) | `need localmount; before net; provide firewall` | `nft -f <firewallPath>` | `return 0`: the table stays loaded (Q3, #168 parity). `reload` is the same as `start`. |
| `orbit-proxy-address` (`default`) | `need localmount; before tinyproxy` | the `orbit0` dummy address `172.31.255.1/32` (spike 47, topic 7, verbatim) | `return 0` |
| `tinyproxy` (`default`, stock script) | stock, plus `rc_need="orbit-firewall orbit-proxy-address"` | stock | stock |
| `orbit-harden` (`default`) | `need localmount; after orbit-firewall` | the harden script | `return 0` |
| `orbit-boundary` (`default`) | `after orbit-firewall orbit-proxy-address tinyproxy orbit-harden`, **ordering only, never `need`** | the gate script (topic 5); after its last check passes, it writes the start time of a fresh child process (field 22 of `/proc/self/stat`, read by `cut`; not `/proc/$$/stat`, which is the earlier start of the gate itself) to `/run/orbit/boundary-up` | `return 0` |
| `orbit-docker` (`default`) | `need orbit-boundary; after tinyproxy` | topic 7 | supervise-daemon stop |

- `orbit-firewall` sits in `boot`, not `default`, so that it loads before every service of `default`, among them `networking`, which starts `dhcpcd` for `eth0` (`dhcpcd` is not a runlevel service of its own; spike 47, topic 1). Spike 47 measured the firewall at +0.2 s with the stock service in `default`, before the IPv4 address. OrbStack's `boot` runlevel holds only `bootmisc devfs syslog`, and has no `localmount` (spike 47, topic 1). That `need localmount` pulls `localmount` into `boot`, and that `before net` orders across runlevels, are **[UNVERIFIED, V19]**. Whether any IPv6 address exists before the firewall loads is **[UNVERIFIED, V4]**. V19 checks the order from OpenRC's own state; W01 (topic 8) samples the window.
- **Mitigations, if W01 fails** (Q5; in this order, each with W01 again): (1) `orbit-firewall` in `sysinit`, with `need` only on what `nft` needs; (2) a root-owned `/etc/sysctl.d/orbit-ipv6.conf` with `net.ipv6.conf.eth0.autoconf=0` and `accept_ra=0`, so `eth0` gets no global IPv6 address before `orbit-firewall` loads. OrbStack's `boot` runlevel has no `sysctl` service (spike 47, topic 1), so mitigation 2 also adds the stock `sysctl` service to `boot` with `rc-update`; that it then runs before `orbit-firewall` and before the kernel configures `eth0` is **[UNVERIFIED, V20]**. Neither mitigation has been tried; both are root-only data of the profile. The step that adopts one also updates the topic 4 table, the golden `script-alpine.sh`, and this spec's Delta wording where it names the runlevel. Mitigation 2 would remove IPv6 from the sandbox, which the boundary does not need, because the proxy dials out as the `tinyproxy` uid. It would also break `probeScript`, which fails when `eth0` has no global IPv6 address (`cmd/orbit/probe_scripts_test.go`); with mitigation 2, the per-distribution harness table expects no global IPv6 address on Alpine and asserts that instead.
- **Other boot paths that run agent code** (the boundary must precede them, as the "Enforcement survives a restart" requirement says): the stock `default` runlevel runs `crond` (spike 47, topic 1). `/etc/conf.d/crond` gets `rc_need="orbit-boundary"`, so cron jobs of any user start only after the gate. The self-check also requires that `/etc/crontabs` holds no file except `root`. `/etc/local.d`, `/etc/user/init.d` (the per-user OpenRC instance of `docker-rootless-extras`, not used), and `getty` are inventoried in V18. Whether the agent can install a crontab on Alpine (BusyBox `crontab` needs setuid through `bbsuid`) is **[UNVERIFIED, V18]**.
- The gate uses `after` because, in OpenRC, a restart of a `need` dependency stops and restarts its dependents (spike 47, topic 7, problem 2). `init` restarts `tinyproxy` and `orbit-harden`, so a `need` would cascade into the gate and into the agent's engine. The script starts `orbit-boundary` and never restarts it, so `orbit-docker`'s `need` does not cascade. This is the same reasoning as `gateUnit` (`internal/provision/docker.go`).
- No init script exports `PATH`: doing so removes `/usr/libexec/rc/bin`, and `checkpath` and `einfo` then fail (spike 47, topic 9).
- **The stock `nftables` service is neutralized**, because its `stop()` flushes the ruleset, which is the #168 class under the name an operator would type. It is in no runlevel. Every `init` sets `/etc/init.d/nftables` to mode `0644`, so that `rc-service nftables start|stop` refuses to run it **[UNVERIFIED, V7]**. If V7 shows that OpenRC runs the script anyway, the fallback is that every `init` removes `/etc/init.d/nftables`, and the checks of topic 5 refuse a sandbox where the file exists; a package upgrade that restores it is then caught by the next `orbit shell` or `orbit start`. Its `rules_file` points at Orbit's file, so that even a start by another path loads Orbit's table. The readiness and boot checks of `shell` and `start` (topic 5) require the stock `nftables`, `docker`, and `containerd` services to be stopped and in no runlevel, not only the self-check of `init`. Both `orbit-firewall` and the stock script `provide firewall`. OpenRC picks the provider that is in the runlevel, which is only `orbit-firewall` **[UNVERIFIED, V7]**. Whether `apk upgrade nftables` starts a service or restores the mode of the script is **[UNVERIFIED, V7]**. Every `init` sets the mode again, and the checks refuse a sandbox where the stock service runs.
- **No restart of a crashed proxy on OpenRC.** The stock `tinyproxy` script runs the daemon with `command_background=true` and no supervisor (spike 47, topic 4), whereas the apt family has `Restart=on-failure` (`internal/provision/units.go`). On Alpine, a proxy that crashes at runtime stays down: egress is closed (fail-closed), `orbit shell` and `orbit start` refuse the sandbox, and `orbit init` or `orbit stop` and `orbit start` bring it back. This spec accepts that availability difference. The alternative, `supervisor=supervise-daemon` in `/etc/conf.d/tinyproxy`, changes which pid the pid file holds, and so the pid check of topic 5, and it was not tested. Spike 83 (section 4) shows that the restart on the apt family is itself in tension with a machine-level gate, so parity is not a fixed target. The difference is listed in Risks.

### Topic 5: Orbit's root-side checks on OpenRC (the gate)

The 0.1 spec established that `orb -u` sessions pass every login gate on every distribution, and that Orbit's paths are gated by root-side checks. On Alpine those checks are the only gate. Condition 1 of 0.1 Q5 is met when every Orbit entry path runs the OpenRC check of the machine's distribution:

| Entry path | Today | 0.2 |
|---|---|---|
| `init` | `selfCheck` at the end of `Script` | `selfCheck` of the family |
| `start` | `BootCheckScript()`, then `MarkerCheckScript()` | `Distribution(ctx, name)` (the adapter call of the 0.1 spec; it reads `orb list` and starts nothing), read **before** `Start`; then `BootCheckScript(d)` and `MarkerCheckScript(d)` |
| `shell` | `ReadinessScript()` | `Distribution(ctx, name)` after the mount checks, then `ReadinessScript(d)` |
| the agent's supervised services | `user@.service` requires `orbit-boundary-gate.service` | `orbit-docker` `need orbit-boundary` |

- The check scripts take the distribution: `BootCheckScript(distribution string) ([]byte, error)`, `ReadinessScript(…)`, and `MarkerCheckScript(…)`. Each refuses an unknown name. The slice fields become `func(string) ([]byte, error)`, and `cmd/orbit` injects them. When `Distribution` fails, `shell` and `start` refuse with the adapter's error, wrapped. They do not use `ErrNotProvisioned`, which the handlers wrap with a message that names `orbit init`: a transient `ErrMachineList` is not a provisioning problem, and `init` refuses an `ErrUnknownImage` too. For `ErrUnknownImage` the message says that the stored image of the sandbox matches no pin of this Orbit version. `start` refuses before `Start`, so the machine stays stopped. `DockerSetupScript` stays without input: it ran unmodified on Alpine (spike 47, topic 9).
- **OpenRC boundary check** (`unitsCheck` of the family): the table `inet orbit_egress` is loaded; `rc-service <svc> status` exits 0 for `orbit-firewall`, `orbit-proxy-address`, `tinyproxy`, `orbit-harden`, and `orbit-boundary`; the stock `nftables`, `docker`, and `containerd` services are stopped and in no runlevel (topic 4); and the pid in tinyproxy's pid file is alive and its `/proc/<pid>/comm` is `tinyproxy`, so a reused pid does not pass. That pid file is `/run/tinyproxy.pid`, the stock script's `pidfile=` (spike 47, topic 4, which recorded the variable but did not read the file); it is not the `PidFile` line of the configuration, which Q7 drops. That OpenRC writes the daemon's pid there is **[UNVERIFIED, V6]**. The pid check exists because `rc-service tinyproxy start` printed `[ ok ]` while the status was `crashed` (spike 47, topic 7, problem 1). The exit codes of `status` for `crashed` and `stopped` are **[UNVERIFIED, V6]**. The check therefore also requires the printed status to be `started`. There is no `systemd-user-sessions` and no `/run/nologin` on Alpine (no PAM gate).
- **End of the boot on OpenRC** (`waitForBoot` of the family; 0.1 Q5, condition 1): wait at most `bootWaitSeconds` until `/run/openrc/softlevel` holds `default` and no `openrc` process runs (the `::wait:/sbin/openrc default` entry of `/etc/inittab` has returned). The state is not trusted, and the checks after it decide, as with `systemctl is-system-running --wait`. This assumes that `/run` is a fresh tmpfs at every boot, so that no `softlevel` from the previous boot survives. That assumption, and the exact commands on OpenRC 0.63 / BusyBox 1.37, are **[UNVERIFIED, V5]**. BusyBox `pgrep -x` matches nothing (spike 47, topic 2), so the check uses `pidof openrc`.
- **Self-check additions** of the family: the stock `nftables`, `docker`, and `containerd` services are in no runlevel and are stopped; `rc-service orbit-firewall stop` leaves the table loaded (exercised by the integration test, not by the script); `/etc/doas.conf` holds no `permit` line and `/etc/doas.d` is empty; `runuser -u agent -- doas -n true` fails; `nohook resolv.conf` is present; `apk info -W <netcatPath>` names `netcat-openbsd`; `/etc/init.d/nftables` is not executable (or absent, under the fallback of topic 4); `/etc/conf.d/crond` holds the `rc_need` line; `/etc/crontabs` holds no file except `root`. The `sudo -n -l` loop, the uid floor, and the group checks are unchanged, with `wheel` added, and with root allowed in `wheel` and `adm`.

### Topic 6: the agent's `PATH`, and Claude Code on musl (#55)

- `/etc/profile.d/orbit-path.sh` (root, 0644, rendered for the `openRC` family only) puts `$HOME/.local/bin` in front of `PATH` when it is not already there. Root never writes into a home (provisioning invariant). Spike 47, topic 6, observed that every `orb -u` entry on Alpine applies `/etc/profile.d` (`PROFILED_VAR` set), and `orbit shell` starts the login shell, bash, which reads `/etc/profile`. That `claude` resolves in `orbit shell` and in `bash -lc` on Alpine is **[UNVERIFIED, V8]**. The agent integration test asserts it.
- Toolchain on musl: `bash` and `libgcc`, not `libstdc++`, and no `USE_BUILTIN_RIPGREP` (0.1 Q6). Spike 47, topic 8, observed this for Claude Code 2.1.322: `ldd` lists only `ld-musl`, and `claude doctor` reports `Search: OK (bundled)`. That the version current at implementation needs nothing more is **[UNVERIFIED, V9]**. If a later client needs `libstdc++`, its install fails loudly, and the fix is a profile package line, not a change to the client (R04).
- The installer contacted only `claude.ai`, `downloads.claude.ai`, and `api.anthropic.com` (spike 47, topic 8). No new egress host, so no question for the owner (epic decision 8).

### Topic 7: rootless Docker on Alpine (#56)

- **Packages** (root, `apk add`): `docker docker-cli-buildx docker-cli-compose docker-rootless-extras shadow-uidmap shadow-subids slirp4netns` (spike 47, topic 9, without `fuse-overlayfs`). The spike's storage driver was `overlayfs`, but `fuse-overlayfs` was installed. That rootless `overlayfs` works without it is **[UNVERIFIED, V13]**. If it does not, the package goes back into the list. Docker 29.5.2, compose 2.40.3, and buildx 0.30.1 were observed; the versions at implementation are whatever Alpine 3.23 ships.
- **No rootful engine:** the `docker` and `containerd` services stay in no runlevel and stay stopped, and neither socket exists. OpenRC has no `mask`. The self-check enforces this state, and the agent is not in `docker` (`privilegedGroups`).
- **Sub-ids:** root appends `agent:100000:65536` to `/etc/subuid`, and the same line to `/etc/subgid`, each only when the file has no `agent` line. This uses no `usermod`, so the requirement "Provisioning keeps the agent and its home" (no `usermod` of `agent` in the root script) and its guard `changesTheAgentUser` (`internal/provision/script_test.go`) hold unchanged. Alpine's `useradd` wrote no entry when no sub-id file existed (spike 47, topic 9); whether it adds one once `shadow-subids` has created the empty files is untested, and the append is idempotent either way. `subIDCheck` is unchanged. `helperPermissions` (`chmod 4755` on `newuidmap`/`newgidmap`) is unchanged; that `shadow-uidmap` installs them at the paths that Ubuntu uses is **[UNVERIFIED, V13]**.
- **Supervision:** an Orbit system service, `orbit-docker`, as tested in spike 47, topic 9, with two changes. The uid is resolved at run time (`agent_uid=$(id -u agent)` in the script; `XDG_RUNTIME_DIR=/run/user/$agent_uid`), not hard-coded as `1000`. RootlessKit's network is pinned explicitly: `DOCKERD_ROOTLESS_ROOTLESSKIT_NET=slirp4netns` (the package that the profile installs) and `DOCKERD_ROOTLESS_ROOTLESSKIT_DISABLE_HOST_LOOPBACK=true`. The Ubuntu unit sets neither and relies on the defaults of `dockerd-rootless.sh` (`internal/provision/docker.go`); the Alpine service pins them because RootlessKit 2.x can choose another driver or detach the network namespace. This is a change from the service that spike 47 tested, and that E06, M01, and the container probes still hold with it is **[UNVERIFIED, V13]**. The service keeps `supervisor=supervise-daemon`, `command_user=agent:agent`, `respawn_max=0`, `respawn_delay=2`, `output_logger`/`error_logger` through `logger`, the proxy variables at `172.31.255.1:3128`, and `start_pre` `checkpath` for `/run/user` and `/run/user/$agent_uid`.
- **What the agent can do:** it cannot stop, restart, or reconfigure `orbit-docker` (only root runs `rc-service`). It can kill its own `dockerd`, which supervise-daemon restarts (**[UNVERIFIED, V15]**: the spike saw respawns only after failed starts), and it can run another `dockerd-rootless` by hand as itself. That is the same power it has over its systemd user unit on Ubuntu. The 0.1 spec's reasoning on `--privileged` (the agent owns its daemon) holds.
- **Network namespace of `dockerd`:** M01 does not depend on it; it uses only the sub-uid detector of E06 (escape suite, A3). M02 does: its weakening accepts the agent uid, and it relies on the traffic of a privileged container leaving the machine as the agent uid (0.1 spec, M02; escape suite, A3), which was established for a `dockerd` in RootlessKit's own network namespace. Alpine ships RootlessKit 2.3.5. Whether its `dockerd` runs there, or in the machine's namespace (detach-netns), is **[UNVERIFIED, V12]**. Spike 47, topic 9, points to detach-netns: a `--privileged --network host` container ran in the machine's namespace (`net:[4026532403]`); the table stayed loaded and direct egress stayed blocked. Step 13 compares `readlink /proc/<dockerd>/ns/net` with that of pid 1, and, where Q6 allows privileged containers, M02 must still detect its weakening through E22 on Alpine. If the namespaces are equal, the escape suite's A3 note gains an Alpine paragraph. Either way the attempts of E06 must fail, and M01 must hold.
- **No resource limits** (0.1 Q7, accepted): `--memory`, `--pids-limit`, and `--cpus` are accepted and not enforced. Spike 47, topic 9, tried the workaround: root moved `init` and `getty` into a child cgroup, enabled `cpu memory pids`, and started `dockerd-rootless` in a delegated cgroup owned by the agent. The limits were still not enforced (`Running in rootless-mode without cgroups. Systemd is required to enable cgroups in rootless-mode.`, `Cgroup Driver: none`). The cause is that rootless Docker needs systemd for cgroups, so no OpenRC layout fixes it, and Orbit changes nothing in the cgroups of the machine. The `--distro` help text names the gap.

### Topic 8: tests and evidence

**Unit tests (default gate, no OrbStack):**

- `internal/provision`: every structural test that runs per distribution now also covers `alpine`; the golden `testdata/script-alpine.sh`; `BootstrapScript` for every distribution; and a **truncation test** that cuts each bootstrap at every byte after its opening `{` and before its final `}`, then runs the prefix with `sh -n` under `/bin/sh` and under `bash --posix`, and expects a parse error each time (the CI runner's `/bin/sh` is dash). A unit test also requires that no OpenRC init script exports `PATH`, that `orbit-boundary` has no `need`, that `orbit-firewall`'s `stop()` is `return 0`, and that the guard comes before the marker removal. Hostile input: an unknown or empty distribution is refused by `Script`, `BootstrapScript`, and every check script.
- `internal/proxy`: `Config(hosts, pidFile)` with `""` has no `PidFile` line, and the bytes with today's path are unchanged (a golden test).
- `internal/orbstack`: `RunAsRootPOSIX` arguments are fixed and take no caller input; the prefix and running-state refusals; and the `alpine` pin and stored-image fixture (from V1).
- Slices: `init` calls the bootstrap before the root script and deletes a machine that it created when the bootstrap fails. `start` reads `Distribution` before `Start`, refuses an unknown image with the machine still stopped, and passes the distribution to both check scripts. `shell` reads `Distribution` and refuses an unknown image or an unreadable list with the adapter error, wrapped, never `ErrNotProvisioned`.
- `cmd/orbit`: the composition test with three values; `TestAttemptVerdict` gains the W01 outcomes (`ESCAPE`, `CONTROL FAILED` for each control of topic 8, among them an IPv6 address before the table with the listener not yet confirmed, `no IPv6 address`, `no agent session before the firewall`, and `accepted window` within and beyond `maxSeconds`), and the summary line of the escape suite gains an `accepted` count, so an accepted window is never counted as passed.

**Integration tests and the escape suite (by hand, on a Mac with OrbStack, `ORBIT_IT_DISTRO=alpine`):**

- The probe harness becomes per distribution in test code only, with a table keyed by `ORBIT_IT_DISTRO`: the service verbs (`systemctl …` or `rc-service …`), the rules file path, `pgrep -o tinyproxy` (not `-x`), the listener exclusion of UDP 546 (`dhcpcd`, DHCPv6, root-owned), the IPv6 listener (`nc -6 -k -l 8443` on Alpine, which has no `python3`), and the group check that allows root in `wheel` and `adm` (spike 47, topic 7, probe changes). The agent-side engine probes of `TestInitGivesTheAgentRootlessDocker` get OpenRC counterparts in the same table: where Ubuntu uses `systemctl --user stop docker.service`, `systemd-run --user`, an agent-owned `~/.config/systemd/user/docker.service`, and the `docker.service` active check, Alpine starts the replacement daemon beside `orbit-docker` (the agent cannot stop it), tries the boot hooks of V18, and checks `rc-service orbit-docker status` as root (the requirement "Container traffic goes only through the proxy" in the Delta). Every raw `orb -m <m> -u root` call of the harness passes its command through `sh -c` or standard input, because `orb` runs its arguments without a shell (spike 47, topic 2). Product code compares no name.
- **Alpine harness before the enum value** (Q4 keeps `alpine` out of the enum until step 13): an integration test, `TestOpenRCProvisioning` (`cmd/orbit`, integration tag, test code only), creates a test-local Alpine machine with `orb create --isolated --isolate-network alpine:3.23`, as the adapter test of step 3 does, under a name with the Orbit prefix, and deletes it in `t.Cleanup` (other `cmd/orbit` integration tests already call `orb` directly; this one also creates a machine). It renders the private `alpine` profile, runs the bootstrap and the root script through the adapter, runs the self-check, restarts the machine, and runs the OpenRC boot check and the probe table. Steps 6–10 extend it, so each Phase 2 PR carries Alpine evidence for its own change, and step 13 does not have to resolve every ledger item at once. It is not a user option, so Q4 holds.
- The ordering probe of `afterStartProbes` and E13 ("every boundary unit entered active before any process of a uid from 1000 up") needs one clock on both sides. On OpenRC, after its last check passes, the gate script writes the start time of a fresh child process, field 22 of `/proc/self/stat` (clock ticks since the VM kernel booted, the same clock as every other process), into `/run/orbit/boundary-up` (topic 4). The OpenRC per-service time (`started 00:00:14` in `rc-status`, spike 47, topic 9) has a resolution of one second, which is not enough (spike 47, topic 7: everything started in the same second). The `/proc` clock is coarse too: it counts ticks of 10 ms, which is why `afterStartProbes` uses systemd's microsecond timestamps on the apt family (`cmd/orbit/probe_scripts_test.go`). So on OpenRC the probe uses two checks, and both must hold:
  - **Time:** every process of a uid from 1000 up has a field 22 strictly greater than the value in `boundary-up`. A tie counts as a violation (fail-closed): within one tick, the order is unknown.
  - **Origin:** every such process belongs to the cgroup that OpenRC gives `orbit-docker` (`0::/openrc.orbit-docker`, as spike 47, topic 9, observed for the engine) or `crond`, or descends from the session helper of OrbStack for an `orb -u` call. `orbit-docker` and `crond` start only after `orbit-boundary` (topic 4), so this check does not depend on the clock. It uses the cgroup and not the parent chain, because container shims and daemonized processes are reparented to pid 1 and keep their cgroup. A process of the agent outside these, for example one that a daemonized `orb -u agent` child left behind, is listed and judged by the time check alone.

  The probe excludes, and lists in the log, only the processes under the session helper; V17's runs include a container started with `--restart always` that survives each restart. A negative control runs first: a uid-1000 process that a test-only init script, ordered `before orbit-boundary`, starts at boot must be flagged by both checks. `/etc/local.d` cannot serve as the control, because OpenRC's stock `local` service is ordered `after *` and so runs after the gate. That ordering, the cgroup names, the name of the session helper process, and whether ties occur in practice are **[UNVERIFIED, V17]**.
- **W01, boot window** (new, in `TestEscapeRestart`, on every distribution): `orbit stop`, then the test calls the adapter's `Start` in a goroutine and at once runs two loops, each every 0.1 s, each turn timestamped by the test on the host, so both share one clock. Every `orb` call passes its command through `sh -c` (spike 47, topic 2).
  - **Root loop** (`orb -m <m> -u root sh -c '…'`, setup and controls only, as Q9 of the escape suite allows): in its first turn, before it does anything else, it starts the IPv6 listener of the per-distribution harness table on `[::]:8443` and confirms it with `ss -Hltn 'sport = :8443'`. Each turn then logs the `ip -4` and `ip -6` addresses with global scope on `eth0` and the table state; the first turn is logged in full, since it shows the state at first contact. It keeps sampling after the table loads, until the boot check of the distribution passes (at most `bootWaitSeconds`), so that an address that appears late, such as IPv4 at about +5 s on Alpine (spike 47, topic 1), is seen.
  - **Agent loop**, in the agent's own session (`orb -m <m> -u agent sh -c '…'`), the raw path of O01, as owner decision Q9 of the escape suite requires: its first successful turn checks the try tools with `command -v`; each turn reads the global addresses of `eth0` and makes a direct try with a 1 s timeout per family that has an address: IPv4 to `1.1.1.1:443`; IPv6 to the root listener on the machine's own global IPv6 address, because an isolated machine has no IPv6 route to the internet, even for root, and nothing outside the machine answers over IPv6 (`.ai/analysis/2026-11-10-orbstack-isolated-machine-spike.md`; `probeScript` uses the same target). A connection to that listener shows that the firewall did not filter the agent's IPv6. A try to `[2606:4700:4700::1111]:443` is also made and logged, as an observation only. That Alpine, like Ubuntu, has no IPv6 route to the internet is **[UNVERIFIED, V4]**; if it has one, the internet target becomes the IPv6 target and its control. Each try is logged with its time, target, and result.

  **Verdict.** Any try of the agent loop that connects is `ESCAPE: W01 succeeded: <family> <address> at +<s>`, whenever it happened: with the table loaded, a direct try must fail anyway. Under the owner's decision Q10 of the escape suite, each of these controls fails W01 with `CONTROL FAILED: W01: <what>: <output>`, and the run is not gate evidence: a try tool is missing; after the table loads, the same agent try command does not reach the proxy at `172.31.255.1:3128` (the mechanism can connect); uid 0, which the firewall accepts, does not reach `1.1.1.1:443` or the IPv6 listener directly; IPv4 never gets an address before the sampling ends; an IPv6 address existed while the table was absent before the listener was confirmed. IPv6 alone has nothing to test, and is logged `no IPv6 address`, only when `eth0` held no global IPv6 address at any sample, before or after the table loaded (for example under mitigation 2); then no control is skipped, because no path exists for anyone. If the agent loop's first successful turn comes after the table loaded, W01 logs `no agent session before the firewall`; that is a pass of the question W01 asks, because the O01 path could not run in the window. Otherwise W01 passes when every control passed and no try connected. It logs the window length (first contact to table loaded) as an observation.

  **Accepted window.** If the owner accepts a window in writing at step 13 (Q5), the step-13 PR adds one entry per distribution to a test-data file, `cmd/orbit/testdata/w01-accepted.json`, with `maxSeconds` (the accepted window length) and `source` (the link to the owner's acceptance); no other PR adds or widens an entry. W01 then reports `accepted window`, never passed, only when every control passed, the measured window is not longer than `maxSeconds`, and every connecting try happened within it; anything else stays `ESCAPE`.

  **Limits.** W01 samples, so a pass shows that no try connected, not that no window exists: a window shorter than one `orb` round trip can escape it. The structural evidence of V19 (OpenRC starts `orbit-firewall` before `networking`) stands next to it, and the default (topic 9) needs both. A pass on Ubuntu and Debian is expected, because the boundary was up at first contact in 12 of 12 runs on the pinned images (18 of 18 with bookworm; spike 47, topic 1). Afterwards the test runs the boot check of the distribution (`BootCheckScript`) against the machine, because `orbit start` on a machine that `Start` already started returns `AlreadyRunning` without checks (`internal/sandbox/start/handler.go`). Q5 decides what a failure on Alpine blocks.
- On Alpine, the existing integration tests then show what spike 47 did not run: `init` itself, the project mount and the agent's write access on Alpine **[UNVERIFIED, V11]**, SSH forwarding and `--no-ssh-forwarding` **[UNVERIFIED, V10]**, `claude` on the login `PATH` (V8, V9), `containerTraffic`, `leakProbe`, compose and buildx through the proxy **[UNVERIFIED, V13]**, `init` twice without restarting the engine (V15), and the privileged table row (Q6, V14).
- **Run time:** spike 47 timed `apk add` at 3 s, against 16 s for apt. No integration or escape run on Alpine exists **[UNVERIFIED, V16]**. Under 0.1 Q2 and Q3, which apply to every distribution that the enum holds, a boundary PR runs the escape suite three times (about 41 min on today's Ubuntu figure of 13.5 min each, plus E18–E22 where allowed, plus one W01 restart per distribution). The default gate of topic 9 adds 10 W01 restarts on Alpine, run once by hand, at roughly the cost of 10 `orbit stop` and `orbit start` cycles **[UNVERIFIED, V16]**. A release runs the full suite three times, as three invocations, each under the 60 min timeout of one test binary.

### Topic 9: Alpine becomes the default (#57)

`sandbox.DefaultDistribution` becomes `Alpine` in its own PR, after these gates, each with a pasted transcript:

1. The full integration suite and the escape suite pass on `alpine`, `debian`, and `ubuntu` on the same OrbStack version and the same `main` head (#57, AC 2).
2. W01 passes on Alpine in 10 of 10 restarts, every family with an address passing its control, and V19 shows `orbit-firewall` loaded before `networking` in each of them (Q5). Or, if the owner accepted a window at step 13, the 10 runs report `accepted window` and the longest window measured is not longer than the one the owner accepted; otherwise the default waits for the owner again.
3. The privileged row of `alpine` matches the run, in both directions (0.1 spec, topic 10, the stale-table rule). Q6 itself was answered before step 13.
4. The owner has landed the default clause of D29 and the *Scope → Now* line.

The help text becomes `distribution of a new sandbox: ubuntu, debian, alpine (default alpine; alpine does not enforce container resource limits)`. The integration default of `ORBIT_IT_DISTRO` follows `sandbox.DefaultDistribution`. An existing sandbox keeps its stored distribution, whatever the default (0.1 spec, topic 7). `orbit ls` is unchanged (epic decision 9).

## 📝 Delta

### Capability: sandbox-provisioning

#### MODIFIED Requirement: Distribution of a new sandbox
The system SHALL create a new sandbox on `DefaultDistribution` when `--distro` is not given, or on Ubuntu 26.04 (`ubuntu`), Debian 13 trixie (`debian`), or Alpine 3.23 (`alpine`) when it is given, from an image that only the adapter's pinned table names, and SHALL refuse any other `--distro` value before any OrbStack command. `DefaultDistribution` is `ubuntu` until Phase 4, and `alpine` after it.

##### Scenario: Debian sandbox
- WHEN `orbit init --distro debian` runs in a project directory without a machine
- THEN OrbStack creates the machine from `debian:trixie` with `--isolated --isolate-network`, and the bootstrap and the root script of `debian` provision it

##### Scenario: Alpine sandbox
- WHEN `orbit init --distro alpine --agent claude-code` runs in a project directory without a machine
- THEN OrbStack creates the machine from `alpine:3.23` with `--isolated --isolate-network`, the bootstrap and the root script of `alpine` provision it, and the output names `alpine`

##### Scenario: Value outside the enum
- WHEN `orbit init --distro <v>` runs with `v` in `Ubuntu`, `ubuntu:26.04`, `debian:bookworm`, `bookworm`, `Alpine`, `alpine:3.23`, `alpine:edge`, `3.23`
- THEN init fails with `unknown distribution "<v>"; use one of: ubuntu, debian, alpine`, and runs no OrbStack command

##### Scenario: Default after Phase 4
- WHEN `orbit init` runs without `--distro` in a project directory without a machine, after Phase 4
- THEN the machine is created from `alpine:3.23`; where the machine exists, it keeps its stored distribution

Frozen by: `internal/sandbox/distribution_test.go`, `internal/sandbox/initialize/handler_test.go` (both from E04-S01; characterized again in step 1)

#### ADDED Requirement: Root scripts run on a machine without bash
The system SHALL run, as root and before the root script, a POSIX `sh` bootstrap that parses as a whole before it runs, fails unless `/etc/os-release` matches the distribution, and installs bash when the distribution needs it and lacks it.

##### Scenario: Fresh Alpine machine
- WHEN `init` runs on a new Alpine machine, which has no bash
- THEN the bootstrap installs bash, the root script then runs with `bash -s`, and `init` succeeds

##### Scenario: Truncated bootstrap
- WHEN the bootstrap is cut at any byte after its opening `{` and before its final `}`
- THEN `sh` refuses it with a parse error, nothing in it runs, and the marker is not written; a cut before the `{` leaves no command, so nothing runs either, and the root script then fails on a machine without bash

##### Scenario: Bootstrap on the wrong machine
- WHEN the bootstrap of `alpine` runs on an Ubuntu machine
- THEN it exits non-zero before it installs anything

#### MODIFIED Requirement: Init of a project directory
The system SHALL create the machine of the project directory on its resolved distribution when it does not exist, and SHALL then run the bootstrap of that distribution, provision the machine with its root script, and set up the Docker client of `agent`, also when the machine already existed. When the bootstrap, provisioning, or the client setup fails on a machine that the same call created, the system SHALL delete that machine and report the failure.

##### Scenario: First init
- WHEN `init` runs in a project directory without a machine and without `--distro`
- THEN the machine is created from the pinned image of `DefaultDistribution`, the bootstrap runs, the machine is provisioned, the client setup runs as `agent`, `Created` is true, and the output names that distribution

##### Scenario: Init of an existing machine
- WHEN `init` runs in a project directory whose machine exists, on any distribution
- THEN the bootstrap and the root script of its own distribution run again, the client setup runs again, `~/.docker/config.json` keeps its other keys, the machine is not created or deleted, and `Created` is false

##### Scenario: Bootstrap fails on a new machine
- WHEN the bootstrap fails on a machine that this call created (for example, `apk add bash` fails)
- THEN the machine is deleted, the root script did not run, and the error names the bootstrap

##### Scenario: Provisioning fails on a new machine
- WHEN provisioning fails on a machine that this call created
- THEN the machine is deleted and the error names the failed provisioning

##### Scenario: Client setup fails on a new machine
- WHEN the client setup fails on a machine that this call created
- THEN the machine is deleted, the agent is not installed, and the error names the client setup

Frozen by: `internal/sandbox/initialize/handler_test.go` (`TestHandleCreatesTheMachine`, `TestHandleKeepsAnExistingMachine`, and the call lists that it already pins)

#### MODIFIED Requirement: The same boundary on every distribution
The system SHALL give a Debian or an Alpine sandbox every requirement of this capability and of `sandbox-containers`, `sandbox-lifecycle`, `sandbox-shell`, `ssh-agent-forwarding`, `agent-install`, and `escape-suite` that an Ubuntu sandbox has. On Alpine, OpenRC services take the place of systemd units, and container resource limits are not enforced (0.1 Q7).

##### Scenario: Integration and escape runs on Debian
- WHEN the integration tests and the escape suite run with `ORBIT_IT_DISTRO=debian`
- THEN every test that passes on Ubuntu passes on Debian, except the pinned privileged observation, which follows the requirement "Privileged containers on the tested OrbStack version": if `--privileged` starts on Debian, E18–E22 and M02 run there, every attempt fails, and M02 detects its leak (0.1 Q1)

##### Scenario: Integration and escape runs on Alpine
- WHEN the integration tests and the escape suite run with `ORBIT_IT_DISTRO=alpine`
- THEN every test that passes on Ubuntu passes on Alpine, and the privileged attempts follow the privileged table (Q6). The one exception is W01, and only when the owner has accepted its measured window in writing (Q5); it is then reported as `accepted window`, never as passed.

##### Scenario: The stock firewall service cannot flush the ruleset
- WHEN root runs `rc-service nftables start` and then `rc-service nftables stop` on a running Alpine sandbox
- THEN the table `inet orbit_egress` stays loaded, a direct connection of `agent` still fails, and the next `orbit shell` succeeds only if the stock service is stopped

##### Scenario: No sudo, doas, or wheel
- WHEN root checks an Alpine sandbox after provisioning and after a restart
- THEN no account except root has a sudo or doas rule, `/etc/doas.conf` has no `permit` line, `agent` and the default user are in neither `wheel` nor `adm`, and `doas -n true` as `agent` fails

##### Scenario: Firewall stop keeps egress closed
- WHEN root runs `rc-service orbit-firewall stop` on a running Alpine sandbox
- THEN the table `inet orbit_egress` stays loaded, and a direct connection of `agent` still fails

##### Scenario: A boundary service fails at boot
- WHEN `orbit-firewall`, `orbit-proxy-address`, `tinyproxy`, or `orbit-harden` is not started after the boot of an Alpine sandbox
- THEN `orbit-docker` and `crond` do not start, `orbit start` stops the machine again and names the service, and `orbit shell` refuses and names `orbit init`

Frozen by: `cmd/orbit/provision_integration_test.go` (`TestInitProvisionsTheSandbox`, run per `ORBIT_IT_DISTRO`), `cmd/orbit/escape_shared_integration_test.go` (`TestEscapeShared`), `cmd/orbit/escape_restart_integration_test.go` (`TestEscapeRestart`, case `E13-boot-failure`, for the boot-failure scenario)

#### MODIFIED Requirement: Enforcement survives a restart
The system SHALL start the firewall, the proxy address, and the proxy at every boot, before any agent process that the machine starts by itself. On the apt/systemd family they are enabled units before `systemd-user-sessions.service`. On the OpenRC family they are OpenRC services, with the firewall in the `boot` runlevel (or `sysinit`, if mitigation 1 of Q5 is adopted), started before `networking`, and the agent's engine and `crond` both need `orbit-boundary`.

##### Scenario: Restart of the machine
- WHEN the machine restarts and `agent` then connects directly to a public address and through the proxy to an allowed host
- THEN the direct connection fails and the proxied one succeeds, on every distribution

Frozen by: `cmd/orbit/stopstart_integration_test.go` (`afterStartProbes`), `cmd/orbit/escape_restart_integration_test.go` (`TestEscapeRestart`, E13), `internal/provision/units_test.go`

#### MODIFIED Requirement: Fixed toolchain
The system SHALL install git, openssh-client, curl, ca-certificates, jq, ripgrep, and unzip as root, plus bash and libgcc on Alpine, and the agent SHALL NOT be able to install a system package.

##### Scenario: The agent runs the package manager
- WHEN `agent` runs `apt-get install -y htop` on Ubuntu or Debian, or `apk add htop` on Alpine
- THEN it fails, and `git`, `ssh`, `curl`, `jq`, `rg`, and `unzip` run

Frozen by: `internal/provision/script_test.go`

### Capability: sandbox-lifecycle

#### MODIFIED Requirement: The boundary is up after a start
The system SHALL report success for `orbit start` only when the boot of the machine's distribution finished, the kernel mounts exactly the project directory, the firewall table `inet orbit_egress` is loaded, and the boundary services of that distribution are active: the firewall, the proxy address, the proxy, the hardening service, and `systemd-user-sessions.service` on systemd, or `orbit-boundary` on OpenRC. On a failure of one of these, the system SHALL stop the machine again. The system SHALL read the distribution from OrbStack's stored image before it starts the machine.

##### Scenario: Egress after a start
- WHEN `orbit start` succeeded, and `agent` connects directly to a public address, through the proxy to a host with no allow rule, and through the proxy to an allowed host
- THEN the first two fail and the third succeeds (R03, D14), on every distribution

##### Scenario: Units before agent processes
- WHEN `orbit start` succeeded
- THEN every boundary unit (systemd) or boundary service (OpenRC) entered its active state before the start time of every process with a uid from 1000 up; on OpenRC by the time and origin checks of topic 8

##### Scenario: Boundary unit fails at boot
- WHEN `tinyproxy.service` is not active after the boot
- THEN Orbit stops the machine again, exits 1, and the message names the unit

##### Scenario: Boundary service fails at boot on Alpine
- WHEN `tinyproxy` is `crashed` after the boot of an Alpine sandbox
- THEN Orbit stops the machine again, exits 1, and the message names `tinyproxy`

##### Scenario: Firewall table not loaded
- WHEN the firewall unit or service is active after the boot, but the table `inet orbit_egress` is not loaded
- THEN Orbit stops the machine again, exits 1, and the message says that the firewall table is not loaded

##### Scenario: Outdated marker
- WHEN the boundary units or services are active and `/etc/orbit/provisioned` is missing or holds another version
- THEN the machine stays running, and Orbit exits 1 and names `orbit init`

##### Scenario: Unknown stored image
- WHEN the stored image of the machine matches no pin
- THEN `orbit start` exits 1 before it starts the machine, and the machine stays stopped

Frozen by: `internal/sandbox/start/handler_test.go`, `cmd/orbit/stopstart_integration_test.go`

### Capability: sandbox-shell

#### MODIFIED Requirement: Refuse a sandbox that is missing, stopped, mismounted, or not ready
The system SHALL refuse to open the shell, exit non-zero, and change no machine when the sandbox of the current directory does not exist, is not running, does not mount exactly the project directory, has a stored image that matches no pin, or fails the readiness check of its distribution.

##### Scenario: No sandbox
- WHEN the owner runs `orbit shell` in a directory with no sandbox, or in a subdirectory of a project
- THEN Orbit exits 1, names `orbit init`, and runs no `orb` command against any machine

##### Scenario: Stopped sandbox
- WHEN the sandbox of the directory is stopped
- THEN Orbit exits 1, names `orbit start`, and the machine stays stopped

##### Scenario: Not provisioned
- WHEN `/etc/orbit/provisioned` is missing, holds another version, or a boundary unit is not active
- THEN Orbit exits 1, names `orbit init`, and opens no shell

##### Scenario: Not ready on Alpine
- WHEN a boundary service of an Alpine sandbox is not started, or the marker holds another version
- THEN Orbit exits 1, names `orbit init`, and opens no shell

##### Scenario: Mount mismatch
- WHEN the machine mounts another directory than the project directory
- THEN Orbit exits 1 with `ErrMountMismatch` and opens no shell

##### Scenario: Unknown stored image or unreadable machine list
- WHEN the stored image of the machine matches no pin, or the machine list cannot be read
- THEN Orbit exits 1 with the adapter's error, wrapped, does not name `orbit init`, and opens no shell

Frozen by: `internal/sandbox/shell/handler_test.go`

### Capability: sandbox-containers

#### MODIFIED Requirement: Rootless engine of the agent
The system SHALL give the user `agent` its own rootless Docker Engine, started at every boot of the machine only after the firewall, the proxy address, the proxy, and the hardening unit or service are active: as a systemd user unit behind `orbit-boundary-gate.service` on the apt/systemd family, or as the supervised system service `orbit-docker` that needs `orbit-boundary` on the OpenRC family. It SHALL run no rootful Docker or containerd daemon. On Alpine, container resource limits are accepted and not enforced.

##### Scenario: Pull, build, and run
- WHEN `agent` runs `docker pull alpine` and `docker pull ghcr.io/containerd/busybox`, builds three images, `FROM debian:trixie` and `FROM ubuntu:26.04` with `RUN apt-get update && apt-get install -y curl` and `FROM alpine` with `RUN apk add curl`, and runs `docker run --rm alpine true`
- THEN each succeeds on the engine of `agent`, `docker info` reports `rootless`, and the tinyproxy log shows the registry hosts and `dl-cdn.alpinelinux.org` (`ghcr.io` and `pkg-containers.githubusercontent.com` included) as `CONNECT` requests and `deb.debian.org` and `ports.ubuntu.com` as plain HTTP requests, on every distribution

##### Scenario: Package source outside the allowlist
- WHEN `agent` builds `FROM debian:trixie` with `RUN echo 'deb http://security.debian.org/debian-security trixie-security main' > /etc/apt/sources.list.d/x.list && apt-get update`
- THEN the build fails, and the proxy answers 403 to the request for `security.debian.org`

##### Scenario: Registry outside the allowlist
- WHEN `agent` runs `docker pull quay.io/prometheus/busybox`
- THEN the pull fails, and the proxy answers 403 to `CONNECT quay.io:443`

##### Scenario: No rootful engine
- WHEN root checks an Ubuntu or Debian machine after provisioning and after `orbit stop` and `orbit start`
- THEN `docker.service`, `docker.socket`, and `containerd.service` are masked and inactive, and neither `/var/run/docker.sock` nor `/run/docker.sock` exists

##### Scenario: No rootful engine on Alpine
- WHEN root checks an Alpine sandbox after provisioning and after `orbit stop` and `orbit start`
- THEN the `docker` and `containerd` services are in no runlevel and stopped, and neither `/var/run/docker.sock` nor `/run/docker.sock` exists

##### Scenario: Boundary unit failed at boot
- WHEN `nftables.service` fails at boot of an Ubuntu or Debian machine
- THEN the user manager of `agent` does not start, and no process of a uid from 1000 up runs, except a process that `orb -u agent` starts

##### Scenario: Boundary service failed at boot on Alpine
- WHEN `orbit-firewall` fails at boot of an Alpine sandbox
- THEN `orbit-boundary` does not reach `started`, `orbit-docker` and `crond` do not start, and no process of a uid from 1000 up runs, except a process that `orb -u agent` starts

##### Scenario: Repair after a failed boot
- WHEN the gate and the user manager of `agent` failed at boot of an Ubuntu or Debian machine, the cause is removed, and the owner runs `orbit init`
- THEN the gate is active, the user manager and `docker.service` of `agent` are active, and the client setup succeeds

##### Scenario: Repair after a failed boot on Alpine
- WHEN the gate and `orbit-docker` failed at boot, the cause is removed, and the owner runs `orbit init`
- THEN `orbit-boundary` and `orbit-docker` are started, and the client setup succeeds

##### Scenario: Docker Compose
- WHEN `agent` runs `docker compose version`, then `docker compose up --abort-on-container-exit --exit-code-from app` in a directory whose `compose.yaml` holds one service `app` with `image: alpine` and `command: ["true"]`, then `docker compose down`
- THEN `docker compose version` reports v2, `up` creates the network and the container of the project on the engine of `agent` and exits 0, and `down` removes both, on every distribution

##### Scenario: Init does not restart the engine
- WHEN `init` runs again on an Alpine sandbox whose agent has a running container
- THEN the container keeps running, and `orbit-docker` keeps its pid

##### Scenario: Limits on Alpine
- WHEN `agent` runs `docker run --memory 64m --pids-limit 50 alpine cat /sys/fs/cgroup/memory.max` on Alpine
- THEN the container runs, and the value is not enforced; the `--distro` help text names the gap

Frozen by: `cmd/orbit/docker_integration_test.go` (`TestInitGivesTheAgentRootlessDocker`), `internal/provision/docker_test.go`

#### MODIFIED Requirement: Container traffic goes only through the proxy
The system SHALL let the engine of `agent`, its builds, and its containers reach the network only through the egress proxy, and SHALL make every direct connection fail, also after a restart, while the proxy is down, and when the agent replaces the engine with one that it starts or configures itself, on every distribution.

##### Scenario: Direct connections from containers
- WHEN a container connects to a public address or a host with no allow rule, on the default network, with `--network host`, on a custom network, in a `docker compose` service on the network of its project, with `--user 65534`, with `--dns 1.1.1.1`, or with `--cap-add NET_ADMIN --cap-add NET_RAW`
- THEN each connection fails

##### Scenario: Daemon replaced by the agent, in the network namespace of the machine
- WHEN `agent` stops its `docker.service`, starts `dockerd-rootless.sh` itself with `DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host` (rootlesskit `--net=host`) and without any proxy environment, and starts a process with `rootlesskit --net=host` and `setpriv --reuid=1000` that connects to a public address directly and to an allowed host through `172.31.255.1:3128`
- THEN the process runs as a sub-uid of `agent` in the network namespace of PID 1 (the test checks both first, so that it exercises the leak path of the spike, A05), the direct connection fails, and only the connection through the proxy succeeds; a `docker run --network host` attempt is only logged

##### Scenario: Agent-owned unit at boot
- WHEN `agent` writes `~/.config/systemd/user/docker.service` that starts the daemon of the previous scenario, and the machine restarts
- THEN that unit starts only after `orbit-boundary-gate.service` is active, and the same sub-uid process probe, run after the restart, finds that a direct connection fails

##### Scenario: Daemon replaced by the agent on Alpine
- WHEN `agent`, which cannot stop `orbit-docker`, starts a second `dockerd-rootless.sh` itself beside it, with its own `XDG_RUNTIME_DIR` and data root, `DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host`, and no proxy environment, and starts the sub-uid process of "Daemon replaced by the agent, in the network namespace of the machine"
- THEN the process runs as a sub-uid of `agent` in the network namespace of PID 1 (checked first), the direct connection fails, and only the connection through the proxy succeeds

##### Scenario: Agent-owned boot hook on Alpine
- WHEN `agent` tries to install `@reboot` work that starts the daemon of the Alpine scenario above (a crontab through `crontab -`, or a script under `/etc/user/init.d` or `/etc/local.d`), and the machine restarts
- THEN the install is refused, or the `orbit init` self-check refuses the crontab, or the job runs only after `orbit-boundary` (`crond` needs it), and the same sub-uid process probe, run after the restart, finds that a direct connection fails (V18)

##### Scenario: Refused host through the proxy
- WHEN a container with the proxy settings of `config.json` requests `https://example.org`
- THEN the proxy answers 403

##### Scenario: After a restart
- WHEN the owner runs `orbit stop` and `orbit start`, and `agent` then pulls an image and runs a container that requests an allowed host
- THEN both succeed through the proxy

##### Scenario: Proxy down
- WHEN root stops `tinyproxy` (the systemd unit, or the OpenRC service) and `agent` pulls an image or a container connects to a public address
- THEN both fail, and nothing leaves the machine directly

Frozen by: `cmd/orbit/docker_integration_test.go` (`TestInitGivesTheAgentRootlessDocker`)

#### MODIFIED Requirement: Privileged containers on the tested OrbStack version
This requirement describes observed platform behavior per distribution, not a guarantee of Orbit. Orbit cannot enforce that `docker run --privileged` fails: rootless Docker supports `--privileged` inside the user namespace of `agent`, and the agent owns its daemon and can start it with any flag. The system SHALL carry a table in the integration tests that states, for each distribution of the enum, whether `--privileged` starts a container on the pinned OrbStack version. On a distribution where the table says refused (Ubuntu 26.04), the system SHALL carry an integration test that fails when `--privileged` starts a container, and the owner then decides. On a distribution where the table says allowed (Debian 13 trixie, when the run of the 0.1 spec's step 9 shows that the flag starts, by the owner's decision 0.1 Q1 b; Alpine 3.23, only by the owner's yes to Q6 of this spec), `--privileged` containers are allowed, and the escape attempts E18–E22 SHALL prove that such a container reaches neither the OrbStack VM beyond its machine, nor another machine, nor an integration of the Mac, and cannot bypass the egress rules of the machine, and M02 SHALL detect its leak. Without the owner's yes to Q6, `alpine` gets no row and does not enter the enum. The tests do not run on the machine of a user, so a later OrbStack there can change the behavior without notice.

##### Scenario: Privileged container where the table says refused
- WHEN the integration test runs `docker run --privileged alpine true` as `agent` on the pinned OrbStack version on Ubuntu 26.04
- THEN the container does not start; on an OrbStack version where it starts, the test fails, and the owner decides

##### Scenario: Privileged container where the table says allowed
- WHEN the integration test runs `docker run --privileged alpine true` as `agent` on the pinned OrbStack version on Debian 13 trixie or Alpine 3.23, and the table says allowed
- THEN the container starts, its `uid_map` maps 0 to the uid of `agent`, and the escape suite runs E18–E22 and M02 on that distribution, and they hold

##### Scenario: The table is stale
- WHEN the table says allowed and `--privileged` is refused, or it says refused and `--privileged` starts, or the distribution under test has no row
- THEN the run fails and names the distribution, so the table never claims coverage that the run did not exercise

Frozen by: `cmd/orbit/docker_integration_test.go` (`TestInitGivesTheAgentRootlessDocker`), `cmd/orbit/escape_shared_integration_test.go` (`TestEscapeShared`)

### Capability: agent-install

#### ADDED Requirement: The client is on the login `PATH` on every distribution
The system SHALL make `claude` resolve by name in a login shell of `agent` after `init --agent claude-code`, on every distribution, without writing into the home of `agent` as root.

##### Scenario: Alpine login shell
- WHEN `orbit init --distro alpine --agent claude-code` succeeded and `agent` runs `bash -lc 'claude --version'`
- THEN it prints the version, and the only Orbit file that sets the path is root-owned and outside `/home`

### Capability: escape-suite

#### ADDED Requirement: The boot window is measured on every distribution
The system SHALL run W01 in the restart sandbox of every distribution: from the first contact after a start, no direct connection that the agent makes from its own session over IPv4 or IPv6 succeeds, sampled until the boot check passes. Every positive control of W01 SHALL pass, and a failed control SHALL fail W01 with `CONTROL FAILED` (escape suite, Q10). The system SHALL log the state of the first sample and the length of the window.

##### Scenario: A window opens
- WHEN a direct try of the agent's session connects
- THEN W01 fails with `ESCAPE: W01 succeeded: <family> <address> at +<s>`, and step 13 does not merge unless the owner accepts the window in writing (Q5)

##### Scenario: A control fails
- WHEN a try tool is missing, the agent's try command cannot reach the proxy after the table loads, uid 0 cannot reach a target directly after the table loads, IPv4 never gets an address, or an IPv6 address existed without the table before the listener was confirmed
- THEN W01 fails with `CONTROL FAILED: W01: <what>: <output>`, and the run is not gate evidence

##### Scenario: No IPv6 address at all
- WHEN `eth0` holds no global IPv6 address at any sample, before or after the table loads, until the boot check passes
- THEN W01 logs `no IPv6 address`, tries nothing over IPv6, and decides on IPv4

##### Scenario: No agent session before the firewall
- WHEN the agent's session first runs after the table loaded, and every control passes
- THEN W01 passes and logs `no agent session before the firewall`

##### Scenario: Accepted window
- WHEN `cmd/orbit/testdata/w01-accepted.json` has an entry for the distribution, every control passed, the window is not longer than its `maxSeconds`, and every connecting try happened within it
- THEN W01 reports `accepted window` with the `source` link, and the summary counts it as accepted, never as passed

## 📝 Architecture

```mermaid
flowchart LR
  cmd["cmd/orbit (changed: wiring, composition test, per-distro probe table)"] --> init["sandbox/initialize (changed: bootstrap call)"]
  cmd --> start["sandbox/start (changed: Distribution before Start)"]
  cmd --> shell["sandbox/shell (changed: Distribution, check per distro)"]
  cmd --> orb["orbstack (changed: alpine pin, RunAsRootPOSIX)"]
  cmd --> prov["provision (changed: openRC family, BootstrapScript, checks take distro)"]
  prov --> proxy["proxy (changed: pid-file argument)"]
  init -. "Machines interface" .-> orb
  start -. "Machines interface" .-> orb
  shell -. "Machines interface" .-> orb
```

The diagram shows no new module and no new import edge. The slices already reach the adapter through `Machines` and receive provisioning scripts as injected functions. `provision` already imports `proxy`. The OpenRC renderer is a private file of `internal/provision`. So the design needs no Key decisions row for a module or a dependency. Step 16 adds one dated row for the OpenRC family, the bootstrap, and the gate of topic 5, and changes the Provisioning requirements line that names the image.

| Package | Change |
|---|---|
| `internal/sandbox` | `Alpine` constant; `Distributions()` gains it (step 13); `DefaultDistribution = Alpine` (step 15). |
| `internal/sandbox/initialize` | `BootstrapScript func(string) ([]byte, error)` field; `Machines.RunAsRootPOSIX`; the call order of topic 3; help text. |
| `internal/sandbox/start`, `internal/sandbox/shell` | `Machines.Distribution`; check-script fields take the distribution; the order of topic 5. `internal/sandbox/AGENTS.md` records the new order. |
| `internal/orbstack` | `alpine` pin; `RunAsRootPOSIX`; the fake gains both. |
| `internal/provision` | `family` `openRC`; `openrc.go` (renderer, init scripts, OpenRC checks); `BootstrapScript`; check scripts take the distribution; profile fields of topic 2; golden `script-alpine.sh`. |
| `internal/proxy` | `Config(hosts, pidFile)`. |
| `cmd/orbit` | Wiring; the per-distribution probe table, W01, and the `/run/orbit/boundary-up` ordering probe (test code only). |
| `.ai/specs/product-brief.md` | Unchanged by this PR (section "Proposed brief changes"). |

## 📝 API Contracts

- **CLI:** `orbit init [--agent claude-code] [--no-ssh-forwarding] [--distro ubuntu|debian|alpine]`. Exit codes are unchanged. Only the help text and the refusal message gain `alpine`.
- **Adapter:** `RunAsRootPOSIX(ctx, name string, script []byte) error`. It runs `orb -m name -u root sh -s` with the script on standard input, with fixed arguments, the Orbit prefix check, and the running-state check of `RunAsRoot`.
- **Provision:** `BootstrapScript(distribution string) ([]byte, error)`; `BootCheckScript`, `MarkerCheckScript`, and `ReadinessScript` take `(distribution string)` and return `([]byte, error)`. Each is deterministic per input and refuses an unknown or empty name with `ErrScriptInput`.
- **Proxy:** `Config(hosts []string, pidFile string) (Files, error)`; an empty `pidFile` leaves the `PidFile` line out.

## 📝 Edge Cases & Failure Scenarios

| Case | Behavior |
|---|---|
| `apk add` fails (mirror down, package renamed in a 3.23 patch release) | The script fails, the marker stays absent, and `shell` and `start` refuse the machine. **New machine:** no table exists yet and no agent user exists yet, so nothing of the agent can use the open egress. The machine is deleted. **Re-init:** the table and the boundary of the previous run stay loaded, because the script does not touch them before the packages, and the agent user exists. `orbit init` fails and names the step. |
| The bootstrap cannot install bash | `init` fails, and a new machine is deleted. No bash script ran. |
| OrbStack resolves `alpine:3.23` to a patch release whose `VERSION_ID` the guard accepts but whose packages differ | The self-check decides; any missing piece fails before the marker. |
| OrbStack stores the Alpine image with another spelling after an upgrade | `ErrUnknownImage`. `init`, `start`, and `shell` refuse every existing Alpine sandbox, fail-closed, until the pin row is updated with evidence. |
| `tinyproxy` crashes at boot or later | No egress (fail-closed). The OpenRC check reads the status and the pid, not the start message. `orbit start` stops the machine again, and `orbit shell` refuses. Unlike the apt family, nothing restarts the proxy at runtime (topic 4); `orbit init`, or `orbit stop` and `orbit start`, bring it back. |
| Root restarts `tinyproxy` or `orbit-harden` | `orbit-boundary` (ordering only) and `orbit-docker` keep running. |
| Root runs `rc-service orbit-firewall stop`, or `rc-service nftables start` / `stop` | `orbit-firewall` stop leaves the table loaded. The stock script is not executable (mode 0644), so `rc-service nftables` refuses to run it **[UNVERIFIED, V7]**. If something starts it anyway, it loads Orbit's file, and `shell` and `start` refuse the sandbox while it runs. |
| The agent installs a crontab, or a job of another user runs at boot | `crond` needs `orbit-boundary`, so no job runs before the gate. The self-check refuses any crontab except `root` in `/etc/crontabs` (V18). |
| Root runs `nft flush ruleset` by hand | Out of scope, as on Ubuntu: a root action. The next `orbit shell` or `orbit start` refuses (the table check). |
| `dhcpcd` would rewrite `/etc/resolv.conf` | Prevented by `nohook resolv.conf`; the self-check requires the line. |
| `apk upgrade` replaces `/etc/doas.conf` or `/etc/conf.d/nftables` | Modified conffiles get an `.apk-new` sibling instead of being replaced **[UNVERIFIED, V7]**. Every `init` rewrites them, and the self-check checks them. |
| Raw `orb -m <m> -u agent` during the boot window or with a failed service | Ungated on every distribution: the known limit O01. W01 measures whether the agent uid has direct egress in the window. If it does, step 13 stops (Q5). |
| `orbit start` or `orbit shell` cannot read the machine list | They refuse with the adapter's error, wrapped, and do not name `orbit init`. |
| A container requests memory, pid, or CPU limits on Alpine | Accepted, not enforced (0.1 Q7). Documented in the help text. |
| `--privileged` container on Alpine | Starts (spike 47). The owner decides before step 13 (Q6). Without a yes, step 13 does not merge. |
| `supervise-daemon` gives up | It does not: `respawn_max=0`. |
| `orbit init --distro alpine` on an existing Ubuntu or Debian sandbox | `ErrDistributionMismatch` (0.1 spec, topic 7); the machine is untouched. |

## 📝 Risks & Impact Review

- **A second boundary implementation.** Every boundary change from here on must be made, and proved, twice: in the apt/systemd renderer and in the OpenRC one. Mitigations: shared data (firewall, allowlist, SSH files, harden body), the golden files, and the escape suite on every distribution of the enum (0.1 Q2). The cost is about 13.5 min of escape suite per boundary PR for each distribution added.
- **The gate on Alpine is only Orbit's own check.** Alpine has no login gate and does not hold logins during boot. A raw `orb -u agent`, outside Orbit, can run before the boundary is up. Ubuntu has the same limit only while a unit has failed (O01). W01 measures the window, and Q5 keeps it from becoming the default unless it is closed or accepted.
- **Privileged containers start on Alpine.** This widens the kernel surface reachable by the agent, as on Debian (0.1 spec, Risks). The owner decides (Q6). Orbit cannot prevent it.
- **No container resource limits on Alpine.** Accepted (0.1 Q7). A runaway container can exhaust the machine. The VM limits of OrbStack still apply to the machine.
- **The default is a durable choice.** Switching the default (step 15) affects every new sandbox of every user. It is reversible with a one-line revert, and existing sandboxes keep their distribution. Removing `alpine` from the enum later would strand Alpine sandboxes with `ErrUnknownImage` until `orbit destroy`.
- **Evidence is pending, because OrbStack is blocked on the owner's Mac** (macOS 27.2.1 blocks its binaries). Every **[UNVERIFIED]** item below is a test or a command of a step, not a belief the design depends on silently. Spike 47 ran on OrbStack 2.2.5. The evidence runs must log the OrbStack version that is current when it is unblocked, and they must pass on it.
- **Rollback:**
  - Phase 1 (the bootstrap call in `init`) and step 7 (`start` and `shell` read the distribution) change behavior on Ubuntu and Debian. Reverting either is a boundary change and needs the Ubuntu and Debian escape runs.
  - The rest of Phase 2 is reachable only from tests, so a revert of it is free.
  - After step 13, reverting leaves Alpine sandboxes that the old build refuses with `ErrUnknownImage`. That is fail-closed, and the user runs `orbit destroy`.
- **No proxy restart at runtime on Alpine.** A crash of tinyproxy on Alpine closes egress until the owner runs `orbit init` or restarts the sandbox, where the apt family restarts it after 1 s (topic 4). Fail-closed, at the cost of availability.
- **A possible cron gap on Ubuntu and Debian, outside this spec.** Topic 4 gates `crond` on Alpine. Whether `cron.service` on Ubuntu or Debian can run an agent crontab before `orbit-boundary-gate.service` is not checked here. If it can, that is a gap in the 0.1 boundary. It should be checked separately; this spec does not change the apt family.

### Verification ledger (OrbStack-dependent, run by hand)

| Id | Claim | Verification step | Plan step |
|---|---|---|---|
| V1 | `alpine:3.23` is stored as `alpine` / `3.23` | `orb create --isolated --isolate-network alpine:3.23 orbit-v1-<rand>`; `orb list --format json`; commit the entry as the fixture; delete the machine | 13 |
| V2 | `/etc/os-release` has `ID=alpine` and `VERSION_ID=3.23.<n>`; OrbStack's `alpine:3.23` follows the newest 3.23 patch release | `orb -m <m> -u root sh -c 'cat /etc/os-release'`; compare `VERSION_ID` with the newest 3.23.x listed on the Alpine releases page on the same day, and log both in each evidence run | 6 (`TestOpenRCProvisioning`), 13 (`TestInitPinsTheStoredImage`) |
| V3 | BusyBox ash parses the braced bootstrap from `sh -s` standard input before running it, and refuses a truncated one | Adapter integration test: `RunAsRootPOSIX` with the full bootstrap (bash appears) and with a prefix cut before `}` (non-zero, bash absent) on a fresh Alpine machine | 3 |
| V4 | No sampled direct egress by the agent uid, over IPv4 or IPv6, while the table is absent; whether a global IPv6 address exists before `orbit-firewall` loads; Alpine, like Ubuntu, has no IPv6 route to the internet | W01 (one restart at step 10, 10 restarts at step 15) on Alpine, with the first sample logged; as root after the boot, `curl -6 -m 5 https://example.com` (expect a timeout or a refusal); read together with V19, because sampling cannot prove that no window exists | 10, 13, 15 |
| V5 | `/run/openrc/softlevel` = `default` and `pidof openrc` empty marks the end of the boot on OpenRC 0.63, and `/run` is a fresh tmpfs at every boot (no `softlevel` survives) | `orb restart`; poll both from a raw `orb -u root` every 0.1 s; compare with `rc-status`; `findmnt /run` | 6 |
| V6 | `rc-service <svc> status` exit codes and output for `started`, `stopped`, and `crashed`; OpenRC writes the pid of the tinyproxy daemon to `/run/tinyproxy.pid` | `cat /run/tinyproxy.pid` and `cat /proc/$(cat /run/tinyproxy.pid)/comm` (expect `tinyproxy`); kill tinyproxy with `kill -9`; read `rc-service tinyproxy status; echo $?` | 6 |
| V7 | `rc-service orbit-firewall stop` keeps the table; `rc-service nftables start` and `stop` refuse a mode-0644 script; OpenRC resolves the virtual `firewall` to `orbit-firewall`, the only provider in a runlevel; `apk upgrade`/`apk fix nftables tinyproxy doas` starts no service, keeps modified conffiles, and does or does not restore the mode of `/etc/init.d/nftables` | As root: `rc-service orbit-firewall stop; nft list tables`; `chmod 0644 /etc/init.d/nftables; rc-service nftables start; echo $?; rc-service nftables stop; echo $?; nft list tables`; a throwaway test service `/etc/init.d/orbit-v7` with only `depend() { need firewall; }`: stop `orbit-firewall` (the table stays), run `rc-service orbit-v7 start`, and `rc-status` must show `orbit-firewall` started and `nftables` stopped; delete `orbit-v7`; `apk fix nftables tinyproxy doas; stat -c %a /etc/init.d/nftables; rc-status; ls /etc/*.apk-new /etc/conf.d/*.apk-new`. If the stock script runs at mode 0644, apply the fallback of topic 4 (remove it) and repeat | 6, 13 |
| V8 | `claude` resolves in `bash -lc` and in `orbit shell` through `/etc/profile.d/orbit-path.sh` | Agent integration test on Alpine | 13 |
| V9 | The current Claude Code on musl needs no `libstdc++` and no host beyond its definition | `ldd ~/.local/share/claude/versions/*`; the tinyproxy log during `init --agent claude-code` | 13 |
| V10 | SSH agent forwarding and `--no-ssh-forwarding` behave on Alpine as on Ubuntu (`host-ssh-agent.sock`, group `orbstack`) | `TestInitForwardsTheSSHAgentByDefaultAndRefusesTheOtherSetting`, `TestInitLeavesTheSSHAgentOutOnRequest`, `TestEscapeNoSSHForwarding`, E15, and E21 on Alpine | 13 |
| V11 | The project mount is writable by `agent` on Alpine and shows the host owner | `TestInitProvisionsTheSandbox` mount probes on Alpine | 13 |
| V12 | Alpine's `dockerd` runs in RootlessKit's own network namespace, or, where Q6 allows privileged containers, M02 still detects its weakening through E22 | `readlink /proc/<dockerd pid>/ns/net` against `/proc/1/ns/net`; `TestEscapeWeakenedBoundary` (M01, and M02 where allowed) on Alpine | 13 (after Q6) |
| V13 | Compose, buildx builds, `containerTraffic`, and `leakProbe` pass through the proxy on Alpine; `newuidmap`/`newgidmap` are at the paths of `helperPermissions`; rootless `overlayfs` works without `fuse-overlayfs` | `TestInitGivesTheAgentRootlessDocker` and E06 on Alpine; `command -v newuidmap newgidmap`; `docker info --format '{{.Driver}}'` | 13 |
| V14 | `--privileged` starts on Alpine on the current OrbStack, and E18–E22/M02 hold | The privileged table check and E18–E22/M02 with `ORBIT_IT_DISTRO=alpine` | 13 (after Q6) |
| V15 | A second `init` restarts neither `orbit-boundary` nor `orbit-docker`; supervise-daemon restarts a `dockerd` that the agent kills | `TestInitRunsAgainAfterRepeatedRuns` on Alpine, plus pid comparison; as `agent`, `kill <dockerd pid>`, then wait for a new `dockerd` pid and `docker info` | 13 |
| V16 | Alpine integration and escape run times stay under the 60 min per-binary timeout | The timed runs of step 13 | 13 |
| V17 | The ticks in `/run/orbit/boundary-up` and the cgroup origin check order the boundary before every agent process, a `--restart always` container included; the services' cgroups are `0::/openrc.orbit-docker` and `0::/openrc.crond`; ties are rare enough not to make the probe flaky; the OrbStack session-helper ancestry is identifiable; the stock `local` service is `after *`; the test-only `before orbit-boundary` negative control is flagged | `grep -A3 depend /etc/init.d/local`; `afterStartProbes` and E13 on Alpine, through `TestOpenRCProvisioning`, 10 restarts, with the count of ties logged | 10, 13 |
| V18 | `crond` with `rc_need="orbit-boundary"` runs no job before the gate; the agent cannot install a crontab (or one is refused by the self-check); `/etc/local.d`, `/etc/user/init.d`, and `getty` run no agent code at boot | `rc-update show -v`; `ls -l /usr/bin/crontab /bin/bbsuid`; as `agent`, `crontab -l` and `echo '@reboot id' \| crontab -`; restart and list the processes of uid ≥ 1000 | 6 |
| V19 | `need localmount` from a `boot`-runlevel service pulls in `localmount`, and in each restart `orbit-firewall` actually starts before `networking` (which starts `dhcpcd`) | Computed order: `rc-status -a`, and `rc-depend -s boot default`, which must list `orbit-firewall` before `networking`. Actual order per restart: the test, as root and at run time, sets `rc_logger="YES"` in `/etc/rc.conf` of the test sandbox before the restarts, then reads `/var/log/rc.log` after each boot, which must show `orbit-firewall` started before `networking` (that `rc.log` records the start order is itself checked once by hand against `rc-status`); the W01 log | 6, 13, 15 |
| V20 | Mitigation 2 only: the stock `sysctl` service, added to `boot`, applies `/etc/sysctl.d/orbit-ipv6.conf` before `orbit-firewall` and before `eth0` gets a global IPv6 address | `rc-update add sysctl boot`; restart; `sysctl net.ipv6.conf.eth0.autoconf net.ipv6.conf.eth0.accept_ra`; `ip -6 addr show dev eth0 scope global`; the W01 log | 10, 13 (only if W01 fails) |
| V21 | The Alpine packages add no setuid or setgid binary that Ubuntu lacks, apart from the reviewed ones (`newuidmap`/`newgidmap`, BusyBox `bbsuid`) | `find / -xdev \( -perm -4000 -o -perm -2000 \) -type f` on Alpine and on Ubuntu after `init`; diff the lists; each extra entry is named in the PR | 6 |

## 📝 Decisions in play

Relies on R03, R04, R07, R08, R10, A01, A05, D08, D12, D14, D16, D17, D19, D20, D22, D26, and the proposed D28 of the 0.1 spec. Applies the 0.1 spec's decisions Q2–Q7 (coordinator, under the owner's delegation) and Q1 (owner). Leaves Q6 of this spec to the owner (privileged containers on Alpine), because the owner's Q1 limited privileged containers to Debian 13 trixie. Owner of all: Jakub Tobiasz. This PR changes no brief row.

## 📝 Proposed brief changes (the owner lands them)

The brief is a protected contract. This PR leaves `.ai/specs/product-brief.md` unchanged. The ids below assume that D28 of the 0.1 spec has landed. If it has not, the first free id is used.

- **New row D29** (active, owner; source: owner request of 2026-11-11 on #46 and the deferral of 2026-11-12 on #48). It is in two clauses, because they land at different steps:
  - *Opt-in clause (lands before step 13):* Release 0.2 adds Alpine 3.23 (`orbit init --distro alpine`, image `alpine:3.23`, any 3.23 patch release), with the same boundary as Ubuntu and Debian, implemented with OpenRC services and Orbit's root-side checks on every Orbit entry path. The value ships only after the owner has decided privileged containers on Alpine, and after W01 passes on Alpine or the owner accepts its measured window. Alpine containers get no enforced resource limits. It supersedes D28 for the enum and the command line (`--distro ubuntu|debian|alpine`): D29 carries the full text of D28 with `alpine` added, and D28 becomes `superseded by D29`. If D28 has not landed when D29 is written, D28's text is extended instead and no superseding is needed.
  - *Default clause (lands before step 15):* Alpine becomes the default for new sandboxes once the integration tests and the escape suite pass on all three distributions on one head, and W01 passes on Alpine in 10 of 10 restarts with OpenRC's order of the firewall before the network shown in each, or, if the owner accepted a boot window at step 13, no measured window is longer than the accepted one. An existing sandbox keeps its distribution.
  - *Why:* The owner prefers Alpine (smaller, 35 MB against about 560 MB). Spike 47 showed a working boundary under OpenRC and named the gaps that the gates close.
- **New row D30, superseding D20** (lands before step 13): the full text of D20 (git, openssh-client, curl, ca-certificates, jq, ripgrep, unzip), plus: on Alpine the toolchain also has `bash` and `libgcc` (0.1 Q6), and root installs `shadow`, `runuser`, `setpriv`, and `iproute2-ss` as infrastructure. D20 becomes `superseded by D30`. An active row changes only through a superseding row that the owner approves (brief, "Required path to change").
- **The command line** (D19 names the commands; the `--distro` values are in D28): carried by D29's full text, so no separate row is needed.
- **Scope → Now** (0.2): "The sandbox runs Alpine 3.23 by default, or Ubuntu 26.04 or Debian 13 trixie with `--distro` (D29)." This line replaces the 0.1 line when step 15 merges.
- **Scope → Later:** the item "Alpine sandboxes … in release 0.2 (D28)" that the 0.1 spec adds is removed when step 13 merges, since Alpine is then a choice of `--distro`; its default part is covered by the *Scope → Now* line of step 15.
- **R11** (only if the owner answers Q6 with yes; lands before step 13; it supersedes R10, "never … a privileged mode"). Two branches, because the 0.1 spec proposes R11 only if its Debian run shows that `--privileged` starts (0.1 spec, topic 10):
  - *R11 is proposed for Debian:* if it has not landed yet, its text is extended before it lands, from "on Debian 13 trixie" to "on Debian 13 trixie and Alpine 3.23", with the escape-suite clause naming both. If it is already active, a new row with that text (the next free R id) supersedes it, because an active row changes only through a superseding row.
  - *Debian refused `--privileged`, so no R11 was proposed and R10 is active:* the same text of the 0.1 spec, topic 10, scoped to "Alpine 3.23" alone, with R10 becoming `superseded by R11`. Without the owner's approval of that text, a yes to Q6 cannot land, because it would contradict R10.
- **Order:** D30 (superseding D20), the opt-in clause of D29 (superseding D28), the *Scope → Later* removal, and R11 land before step 13. The default clause of D29 and the *Scope → Now* line land before step 15.

## 📋 Phasing

- **Phase 0, prerequisites.** E04-S01 (#49, #50, #51) merged; PR #191 (#168) merged or closed, with the escape-id collision resolved; E18–E22 and M02 of the 0.1 spec in the escape suite.
- **Phase 1, scripts without bash (E04-S02-T01, #53).** The bootstrap, `RunAsRootPOSIX`, and the `init` call order, on Ubuntu and Debian. The adapter's integration test proves V3 on a test-local Alpine machine.
- **Phase 2, the OpenRC family (E04-S02-T02, #54; E04-S02-T03, #55; E04-S03, #56).** The renderer, the services, the checks, `PATH`, and the engine. Each is covered by unit tests, the Alpine golden file, and the test-only integration harness `TestOpenRCProvisioning` on a test-local Alpine machine. No enum value yet (Q4). Step 7 changes `start` and `shell` on every distribution.
- **Phase 3, `alpine` with its evidence (closes #52 and #56).** The value in all three tables, with the integration suite and the escape suite on Alpine. It comes after the owner's answer to Q6, after the brief rows of the opt-in clause have landed, and after W01 passes (or the owner accepts its window).
- **Phase 4, Alpine by default (E04-S04, #57).** Behind the gates of topic 9.
- **Phase 5, records.** Documents and the archive of the Delta.

## 📋 Implementation Plan

### Phase 1: scripts without bash

1. **Characterize.** The call list of `init` on the fake (`Mounts`, `Create` or `State`, `SSHAgentForwarded`, `Mounts`, `ActiveMounts`, `RunAsRoot`, `RunAsAgent` for the client setup, then `RunAsAgent` for the agent install) is already pinned in `internal/sandbox/initialize/handler_test.go`, and the arguments of `runAsRootArgs` and `runAsAgentArgs` in `internal/orbstack/orbstack_test.go`. Check that these pins cover the cases that step 3 changes (a new machine, an existing machine, each failure that deletes), and add any that are missing. Green on unchanged code; commit alone. → verify: `go test ./internal/sandbox/... ./internal/orbstack/...`
2. **`BootstrapScript`.** Add it for the existing profiles, with the guard and the `command -v bash` check, plus the truncation test under `/bin/sh` and `bash --posix`, and the hostile-input refusals. → verify: `go test ./internal/provision/...`
3. **`RunAsRootPOSIX` and the `init` order.** Add the adapter call and the fake, the `Machines` method, the `BootstrapScript` field, and the deletion of a new machine on a bootstrap failure. The adapter integration test creates an Alpine machine through a test-local `orb create --isolated --isolate-network alpine:3.23` and checks V3. → verify: the full gate; `go test -tags integration -p 1 -run 'TestRunAsRootPOSIX|TestInitProvisionsTheSandbox' ./internal/orbstack/ ./cmd/orbit/` (OrbStack)

### Phase 2: the OpenRC family

4. **`proxy.Config(hosts, pidFile)`.** Add the golden test that keeps today's bytes, and the `""` case. → verify: `go test ./internal/proxy/... ./internal/provision/...`
5. **Profile fields and family switch.** Add the fields of topic 2 to the existing profiles, with the apt values. The Ubuntu and Debian goldens are unchanged. → verify: `go test ./internal/provision/...`
6. **`openRCScript` and the OpenRC checks.** Add the renderer, the init scripts of topic 4 (`orbit-firewall` stop is `return 0`, `orbit-boundary` has no `need`, no `PATH` export), the hardening with `wheel` and doas, `nohook`, `orbit-path.sh`, the OpenRC `unitsCheck` and `waitForBoot`, and the check scripts with a distribution argument. Add the `crond` and stock-`nftables` lines. The private `alpine` profile is reachable only from tests. Add the golden `script-alpine.sh`. Add `TestOpenRCProvisioning` (topic 8): a test-local Alpine machine, the bootstrap, the root script twice, the self-check, a restart, and the OpenRC boot check. It asserts V2, V5, V6, V7, V18, V19, and V21, and the PR pastes its transcript. → verify: `go test ./internal/provision/...`; `go test -tags integration -p 1 -run TestOpenRCProvisioning -v ./cmd/orbit/` (OrbStack)
7. **Slices read the distribution.** `start` reads `Distribution` before `Start`; `shell` reads it after the mount checks; both pass it to the check scripts; wiring in `cmd/orbit`. Characterize the call lists first (as in step 1). → verify: the full gate; on Ubuntu, `go test -tags integration -p 1 -run 'TestStop|TestStart|TestShell' ./cmd/orbit/`
8. **`PATH` and toolchain (#55).** Add the Alpine toolchain line (`bash libgcc`) and `orbit-path.sh` in the golden. Add a unit test that root writes nothing under `/home`. → verify: `go test ./internal/provision/...`
9. **Engine (#56).** Add `orbit-docker`, the Alpine Docker packages, the sub-id lines, the rootful-service check, and `rc-service orbit-docker start` (never restart). → verify: `go test ./internal/provision/...`
10. **Probe harness per distribution.** Add the test-side table of topic 8, the `boundary-up` ordering probe with its negative control, and W01 with its agent-session loop and its controls. Run W01 on Ubuntu (expected to pass). `TestOpenRCProvisioning` runs the probe table, the ordering probe (V17), and W01 on its Alpine machine. If W01 fails there, try the mitigations of topic 4 in this step, so that step 13 does not meet the window for the first time. → verify: `go vet -tags integration ./...`; on Ubuntu, `go test -tags integration -p 1 -timeout 60m -run '^TestEscapeRestart$' -v ./cmd/orbit/`; `go test -tags integration -p 1 -run TestOpenRCProvisioning -v ./cmd/orbit/`
11. **Help text and limits note.** The `--distro` help text gains the limits note for `alpine` (it is not yet a value). → verify: `go test ./internal/sandbox/...`
12. **Ask the owner Q6.** Ask on #52, with the recommended answer and the Alpine results of steps 6–10. No code changes: `alpine` cannot reach the privileged table before step 13, because the enum refuses it, and the stale-table rule fails a run on a distribution without a row. Step 13 starts only after the answer. → verify: the question is posted on #52 and linked from the step's PR or issue comment


### Phase 3: `alpine` with its evidence

13. **Add `alpine`.** Add the enum value, the pin, the stored-image fixture (V1), the profile in `Distributions()`, the privileged row per Q6, and the mismatch handler tests. On a Mac with OrbStack, run `go test -tags integration -p 1 -timeout 60m ./...` with `ORBIT_IT_DISTRO=alpine`, and the escape suite on Alpine, Debian, and Ubuntu. Resolve V1–V4, V7, V8–V17, and V19 (and V20 if mitigation 2 was needed), including the M02/E22 check of V12 where Q6 allows privileged containers, and paste the transcripts with the OrbStack version, the first W01 sample, and the W01 window lengths. Prerequisites: the owner's answer to Q6, and the brief rows listed under "Owner gates" for this step. If W01 fails on Alpine after the mitigations, the step stops and asks the owner on #52 (Q5); it merges only with a written acceptance, and the run then reports W01 as `accepted window`. → verify: the full gate; all three escape runs green (W01 on Alpine passed, or `accepted window` with the acceptance linked); every attempt of E18–E22 passes and M02 holds where the table says allowed

### Phase 4: Alpine by default

14. **Characterize the default.** `TestHandleCreatesTheMachine` asserts the `ubuntu` default. Green; commit alone. → verify: `go test ./internal/sandbox/...`
15. **Switch the default** (#57), after the gates of topic 9: `DefaultDistribution = Alpine`, the help text, `ORBIT_IT_DISTRO` default, the test of step 14 updated. Paste the three-distribution runs on one head. → verify: the full gate; the three runs green; W01 on Alpine 10 of 10 with the V19 order shown in each restart, or, with the owner's written acceptance linked, 10 runs reporting `accepted window` and none longer than the accepted one

### Phase 5: records

16. **Documents.** `ARCHITECTURE.md` (Key decisions row: the OpenRC family, the bootstrap, and the gate of topic 5; the Provisioning requirements line), root `AGENTS.md` (overview, validation per distribution), `internal/provision/AGENTS.md` (families, bootstrap, OpenRC invariants: `after` for the gate, never restart `orbit-boundary` or `orbit-firewall`, no `PATH` export), `internal/orbstack/AGENTS.md` (`RunAsRootPOSIX`, the `alpine` pin), `internal/sandbox/AGENTS.md` (the new orders of `init`, `start`, and `shell`), `internal/proxy/AGENTS.md` (the pid-file argument), `SDLC.md` (escape suite on three distributions), and `.ai/specs/2026-11-12-escape-suite.md` (the `W` group and W01 in the catalogue, the `accepted` category of the summary line, TestEscapeRestart's case list, and the A3 note per V12). → verify: the validation gate
17. **Archive the Delta** into `.ai/specs/capabilities/sandbox-provisioning.md`, `sandbox-lifecycle.md`, `sandbox-shell.md`, `sandbox-containers.md`, `agent-install.md`, and `escape-suite.md` (a new file; no escape-suite capability file exists yet). → verify: the files match the Delta.

`risk-high` evidence for every implementation PR: hostile-input unit tests for every new renderer and argument; the escape suite on every distribution that the enum holds at the PR head (0.1 Q2), pasted; a second reviewer; no self-QA.
