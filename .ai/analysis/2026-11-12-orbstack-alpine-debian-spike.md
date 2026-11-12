# Spike: OrbStack Alpine and Debian images against Ubuntu 26.04 (E04-T01, issue #47)

Date: 2026-11-12. Time-boxed spike, run by an agent on the owner's Mac. Each result below was observed. Untested items say so. The note reports facts. The epic #46 already records decisions 1 to 9 (Q1–Q9, 2026-11-11, made by the coordinator under the owner's delegation). The last section sets the facts against each of them as input for the spec of E04-T02 (#48); it changes no decision.

## Environment

| Item | Value |
|---|---|
| Host | macOS (Darwin 27.2.0), Apple Silicon, host name JARVIS |
| OrbStack | `orb version`: `Version: 2.2.5 (2020500)`, commit `bf842321f95fc3ca7cda3227bf0cd41cb188c471` |
| Kernel (shared VM kernel, same in every machine) | `7.2.12-orbstack-00394-gbecdd6130068` |
| Baseline | `orb list` before the spike: no output (no machines) |
| `orbit-spike47-alpine` | `alpine:3.23`, Alpine Linux v3.23.8, BusyBox 1.37.0, OpenRC 0.63 `[LXC]`, arm64. Created in 12.7 s, 35.4 MB |
| `orbit-spike47-trixie` | `debian:trixie`, Debian 13, systemd 257.13, dash 0.5.12-12, arm64. Created in 10.5 s, 561.9 MB |
| `orbit-spike47-bookworm` | `debian:bookworm`, Debian 12, systemd 252.39, dash 0.5.12-2, arm64. Created in 10.5 s, 561.0 MB |
| `orbit-spike47-ubuntu` | `ubuntu:26.04`, 26.04.1 LTS "resolute", dash 0.5.12-12ubuntu3, arm64. Created in 13.4 s, 622.2 MB. The control for every comparison |

Every machine was created with `orb create --isolated --isolate-network <distro:version> orbit-spike47-<name>` (no `--mount`, no `--forward-ssh-agent`). Only these four machines were touched. No other machine existed.

## Method

1. Stock machines: read init, shell, users, sudo or doas files, PAM, resolver, and environment (topics 1 to 3, 5, 6).
2. Login gate and boot race: closed `/run/nologin` (and `/etc/nologin`), then a deliberately slow boundary stand-in (`sleep 25`) that must finish before logins, then `orb restart` and an immediate `orb -m <m> -u <user>` command.
3. Packages: installed the D20 toolchain and the D22 infrastructure by hand and read their layouts (topic 4).
4. Provisioning (topic 7): rendered the real root script from `internal/provision` (`Script(ScriptInput{DefaultUID: 501, ForwardSSHAgent: true, AgentDomains: <the Claude Code domains>})`, throwaway test file, deleted, no repository change), and rendered the `probeScript`, `afterStartProbes` and `DockerSetupScript` of `cmd/orbit`. Ran the unmodified script as the Ubuntu control, then on Debian, then hand-adapted it where it failed. For Alpine wrote the script by hand in POSIX `sh` with OpenRC units, and ran it three times to prove idempotence.
5. Probes: the `probeScript` of `cmd/orbit/probe_scripts_test.go` as root with `orb -m <m> -u root bash -s`, with a port served on the Mac (`0.0.0.0:38471`, stopped afterwards) for the probes to the Mac. Then `orb stop`/`orb start` and `afterStartProbes`.
6. Alpine only: Claude Code native installer as `agent` (topic 8), rootless Docker under OpenRC with container probes (topic 9).

The probe set ran with this deviation from the repository text: no project mount. The project directory was `/srv/proj`, owned by `agent`, holding the host file that the probe creates. The probe of the host-engine container did not run (no container was created on the host engine, to leave the host engine alone). The harness needed small per-distro patches, listed in topic 7.

Scripts and raw outputs: `/private/tmp/claude-501/-Users-jarvis-Projects-orbit-wt-gh-47/ade8feaa-4679-4e95-8dfa-9202e4cdc66e/scratchpad/` (`art/*-root-script.sh`, `art/alpine-probe.sh`, `art/alpine-docker-root.sh`, `out/*.txt`). They are not in the repository, as with the Ubuntu spike.

## Summary

| Topic | Alpine 3.23 | Debian trixie | Debian bookworm |
|---|---|---|---|
| Init | BusyBox init + OpenRC. No login gate of any kind. `orb` logins arrive during boot | systemd 257, same units as Ubuntu | systemd 252, same units as Ubuntu |
| Boundary before login | **Not possible.** 8 of 8 first contacts found every boundary service stopped. Network was down and the firewall loaded first, so no egress window was seen | Same as Ubuntu: `orb` ignores the gate. 6 of 6 first contacts found the boundary up | Same. 6 of 6 |
| Shell | no bash by default. `/bin/sh` is BusyBox ash | bash. `/bin/sh` is dash | bash. `sh` is dash 0.5.12-2 **without `pipefail`** |
| Root script | needs a different script (apk, OpenRC, `/etc/nftables.nft`). 242 lines written, passes the probes | 2 package changes, then passes | 4 changes (topic 7), then passes. Docker design not satisfiable from the archive |
| Default user | uid 501, groups include `wheel`, NOPASSWD file, `sudo` and `doas` installed | uid 501, group `sudo`, NOPASSWD file | same as trixie |
| DNS | symlink to OrbStack file. **`dhcpcd` rewrites a static file** unless told not to | symlink. `systemd-resolved` already masked | same |
| `/etc/environment` | applied to every `orb -u` command (OrbStack reads it), no PAM | applied | applied |
| Claude Code | works (musl build), needs `bash` | not run | not run |
| Rootless Docker | works under OpenRC. **No cgroup limits.** `--privileged` runs, boundary holds | unit active. Engine not exercised | unit active. Engine not exercised. docker.io 20.10, no buildx, no compose v2 |

Boundary findings, marked as the issue asks:

- **BLOCKER (conditional) for Alpine: logins are not held until the boundary is up.** Nothing in Alpine can gate an `orb` login (topic 1). The window was 5 to 7 s after `orb start`. In every run the machine had no IPv4 address during it, and the firewall table appeared 0.2 s after first contact, before the address, so no direct IPv4 egress was observed. IPv6 was not checked in this window (topic 1). The blocker lifts when the spec makes the root-side readiness check mandatory on every Orbit entry path (it already exists for systemd, see below), and the supervised services of the agent need the gate. Anyone who calls `orb -m <m> -u agent` directly bypasses Orbit's check on any distribution.
- **Finding for every distribution, Ubuntu included: `orb -m <m> -u <user>` passes `/run/nologin` and `systemd-user-sessions.service`.** It ran 0.02 s after a boundary stand-in started and while `systemd-user-sessions.service` was `inactive`. `internal/provision/check.go` already says "orb sessions do not pass the login gate", and `orbit start` and `orbit shell` check the boundary as root. This spike confirms the cause. Not new, and not distribution-specific.
- No sudo or doas rule survived hardening on any machine (topic 3). Not a blocker.
- Not boundary blockers, but design blockers: Debian bookworm cannot give the Docker design (compose v2, buildx) from its archive, and Alpine rootless Docker cannot enforce resource limits.

## Findings

### 1. Init system and boot order

Stock process trees (`ps` as root right after create):

- Alpine: PID 1 `/sbin/init` is BusyBox (`readlink -f /proc/1/exe` is `/bin/busybox`). `/etc/inittab`:

```
::sysinit:/sbin/openrc sysinit
::sysinit:/sbin/openrc boot
::wait:/sbin/openrc default
::respawn:/sbin/getty 38400 console
```

  Stock runlevels: `boot` = `bootmisc devfs syslog`, `default` = `crond networking`. `dhcpcd` runs for `eth0`. There is no `sshd`, no `systemd`, no PAM login stack.
- trixie, bookworm, Ubuntu: PID 1 is systemd, with `systemd-networkd`, `systemd-logind`, `dbus`, `agetty` and `polkitd`. Ubuntu adds `cron` and `rsyslog`.

How to run a boundary service before a login:

| Distribution | Mechanism | Holds an `orb` login? |
|---|---|---|
| Ubuntu, Debian | `Before=systemd-user-sessions.service` (the Ubuntu design) | **No** (below) |
| Alpine | An OpenRC service in `default` with `before net`/`after nftables` (the stock `nftables` unit already has `before net` and `provide firewall`) | **No** (below) |

Does `orb -m <m> -u <user>` pass a login gate? Test on systemd: created user `probe`, `systemctl stop systemd-user-sessions.service` (this writes `/run/nologin`: "System is going down. Unprivileged users are not permitted to log in anymore."), then:

```
orb -m orbit-spike47-ubuntu -u probe sh -c 'id; whoami'   -> uid=1000(probe) ... whoami=probe   rc=0
orb -m orbit-spike47-ubuntu -u probe bash -lc 'loginctl list-sessions'   -> session c2 1000 probe ... class manager
```

Same result on trixie and bookworm (rc 0 on all three, `/run/nologin` present, `/etc/pam.d/login` has `pam_nologin`, but `orb` does not use `login`). On Alpine, with both `/etc/nologin` and `/run/nologin` present: `orb -m orbit-spike47-alpine -u probe id` gave `uid=1000(probe)` rc=0 (Alpine has no `pam_nologin` at all: `/etc/pam.d` holds only the shadow-utils files).

Boot race. A `slowgate` unit (`Before=systemd-user-sessions.service`, `sleep 25`, enabled), then `orb restart` and an immediate command as `probe`:

```
ubuntu:  probe ran at 1794500816.586 uid=1000 | start=1794500816.565 done=(missing) | slowgate=activating user-sessions=inactive system=starting /run/nologin present
trixie:  probe ran at 1794500873.558 uid=1000 | start=1794500873.536 done=(missing) | slowgate=activating user-sessions=inactive system=starting /run/nologin present
```

On Alpine (`slowgate` as an OpenRC service in `boot`, `sleep 25`): the first `orb` command ran at +2.5 s with `bootmisc [ starting ]`, `slowgate [ stopped ]`, and no `default` service started (`crond`, `networking` absent from the list). So OrbStack does not wait for the boot on either init system.

First-contact state with the real provisioned boundary (`orb stop`, `orb start`, immediate root command, 6 runs per systemd distribution):

```
ubuntu/trixie/bookworm: system=starting nft=[table inet orbit_egress] nftables=active proxy-addr=active tinyproxy=active harden=active user-sessions=active sudoers.d=[README] jarvis-pw=L   (18 of 18)
```

(The gate unit `orbit-boundary-gate.service` was `activating` in 14 of 18 runs and `active` in 4.) Alpine, 8 runs:

```
first contact: nft=[] nftables=stopped orbit-proxy-address=stopped tinyproxy=stopped orbit-harden=stopped orbit-boundary=stopped networking=stopped sudoers.d=[] jarvis-pw=L eth0=   (8 of 8)
```

Window measurement on Alpine (poll every 0.2 s from first contact; an agent-uid `curl --noproxy` to 1.1.1.1 whenever `eth0` has an address):

```
+0.0s addr=none firewall=none proxy=stopped agent-direct=no-address
+0.2s addr=none firewall=table inet orbit_egress  proxy=stopped agent-direct=no-address
+5.4s addr=192.168.139.48/24 firewall=table inet orbit_egress proxy=started agent-direct=blocked     (runs: 5.4 s, 6.7 s, 6.1 s)
```

The poll covered IPv4 only: `addr` is `ip -4 -o addr show eth0`, and the direct attempt is IPv4 (`https://1.1.1.1`). The first-contact line above also reads `eth0` with `ip -4`. IPv6 addresses on `eth0` (`dhcpcd` runs DHCPv6, topic 5) and direct IPv6 egress were **not checked** in this window, so the observation "no address before the firewall" holds for IPv4 only. The firewall table is `inet`, so it covers IPv6 once loaded at +0.2 s; whether an IPv6 address exists in the first 0.2 s is unknown. No machine was created again to check it.

The firewall loads before the IPv4 network is configured (`nftables` is `before net`), so the unit order fails closed on Alpine for IPv4. The window with no proxy lasts about 5 to 7 s. Persistent hardening (empty `/etc/sudoers.d`, locked passwords) was already on disk at first contact. On Alpine, where the harden service had not run yet at first contact, `/etc/sudoers.d` was still empty in all 8 runs: OrbStack did not restore its sudoers file at start.

**Finding.** No gate holds an `orb` login on any distribution. Alpine's boundary start (about +0.2 s for the firewall, +5 to 7 s for the proxy) trails first contact in every run. Marked as a blocker for Alpine under the issue's negative criterion, see the summary.

### 2. Shell

| | root's shell (`getent passwd root`) | `orb -u root` bare (stdin) | `sh` | bash |
|---|---|---|---|---|
| Alpine | `/bin/sh` | `-sh`, BusyBox ash | `/bin/busybox` | **absent** (`-sh: bash: not found`) |
| trixie | `/bin/bash` | `-bash` | dash | `/usr/bin/bash` |
| bookworm | `/bin/bash` | `-bash` | dash 0.5.12-2 | `/usr/bin/bash` |
| Ubuntu | `/bin/bash` | `-bash` | dash | `/usr/bin/bash` |

- `orb -m <m> -u root <cmd...>` executes the arguments directly: a single string with a `;` fails with `No such file or directory` (`-sh: ... not found`). Use `sh -c '...'` or pipe a script to stdin. The shell of a bare `orb -u root` is the login shell of the user (`$0` is `-bash` or `-sh`).
- `internal/orbstack/orbstack.go:688,694` runs every script with `bash -s` (`RunAsRoot`, `RunAsAgent`). On Alpine it fails before the first line: `-sh: bash: not found`.
- Can the root script be POSIX `sh`? The rendered Ubuntu root script has no bashism (`grep -E '\[\[|<<<|<\(|local |declare |select '` finds nothing) and `sh -n` accepts it on all four machines. Run for real with `sh -s`: **Ubuntu rc=0, trixie rc=0, bookworm rc=2** (`sh: 3: set: Illegal option -o pipefail`, because dash 0.5.12-2 lacks `pipefail`; the unmodified script has `set -euo pipefail` on line 3). BusyBox ash 1.37 accepts `set -o pipefail`. Three helpers stay bash: the gate script (`#!/bin/bash`), the probes (`local`, `/dev/tcp`), and `DockerSetupScript` (run with `bash -s`). The hand-written Alpine script is POSIX `sh` and ran under BusyBox ash.
- `time` does not exist in dash (`sh: 1: time: not found`). A BusyBox applet list is smaller too: BusyBox `setpriv` has no `--reuid`, BusyBox `pgrep -x` finds nothing (`pgrep -o -x tinyproxy` rc=1, `pgrep -o tinyproxy` finds it), no `runuser`, no `ss`, no `python3` on Alpine or Debian images. See topic 7.

**Finding.** bash is absent on Alpine, so the adapter's `bash -s` fails there before line 1. The rendered root script has no bashism, but it uses `pipefail`, which bookworm's dash lacks; three helpers stay bash.

### 3. The default user

Same in all four machines: uid 501 (the uid of the Mac user), name `jarvis`, password locked (`!` in `/etc/shadow`), and `/etc/sudoers.d/orbstack` (mode 0440, 30 bytes) with `jarvis ALL=(ALL) NOPASSWD:ALL`.

| | groups | sudo files besides `orbstack` | doas |
|---|---|---|---|
| Alpine | `jarvis adm wheel uucp audio dialout input video orbstack` (shell `/bin/sh`) | `/etc/sudoers` has only `root ALL=(ALL:ALL) ALL` and `@includedir /etc/sudoers.d` | `doas` 6.8.2 setuid root installed. `/etc/doas.conf` holds only a commented `# permit persist :wheel`. `/etc/doas.d` is empty. `sudo` 1.9.17 is also installed and setuid |
| trixie | `jarvis adm uucp dialout sudo audio video plugdev staff input orbstack` | `%sudo ALL=(ALL:ALL) ALL` | none installed |
| bookworm | same as trixie | same | none installed |
| Ubuntu | same as Debian | `%sudo`, `%admin`, and `/etc/sudoers.d/90-incus` (`ubuntu ALL=(ALL) NOPASSWD:ALL`, for a user that does not exist) | none installed |

`sudo -n -l -U jarvis` on Debian lists two entries (`(ALL : ALL) ALL` from `%sudo`, `(ALL) NOPASSWD: ALL`). On Alpine, `wheel:x:10:root,jarvis` (root is a member of `wheel` and `adm` by default).

After the hardening of the adapted script (the harden script of `internal/provision` plus `wheel` in its group list, one run per boot):

```
Alpine:  ls -la /etc/sudoers.d -> total 0 | wheel:x:10:root | adm:x:4:root | sudo -n -l -U jarvis: "User jarvis is not allowed to run sudo"
         passwd -S jarvis/root/agent -> L / L / L | doas.conf active rules: none | /etc/doas.d: 0 files
         agent: sudo -n true -> "sudo: a password is required" | doas -n true -> "doas: Operation not permitted" | su -c true root -> "Password:" (root locked)
```

The Debian probes (`agent sudo`, `agent su to root`, `default user has a sudo rule`, `default user password not locked`, `group sudo has members`) all passed on trixie and bookworm.

**Finding.** The default user and its NOPASSWD rule are the same on every distribution. Alpine adds `wheel` (root plus the default user) and the `doas` binary. No rule survives the hardening: the harden script must list `wheel` (Alpine) in addition to `sudo admin adm`. The probe that requires empty `adm`/`wheel` groups must ignore `root`, who is a member on Alpine.

### 4. Packages, `tinyproxy`, `nftables`

D20 + D22 package names and versions (`apk add` took 3.0 s and left 76 packages, 46.0 MiB; `apt-get install` took 16 s on trixie and 15 s on bookworm):

| Package | Alpine 3.23.8 | trixie | bookworm |
|---|---|---|---|
| git | `git` 2.52.1-r0 | `git` 1:2.47.3-0+deb13u1 | `git` 1:2.39.5-0+deb12u3 |
| openssh client | `openssh-client` (meta, installs `openssh-client-default` 10.2_p1-r1) | `openssh-client` 1:10.0p1 (preinstalled) | `openssh-client` 1:9.2p1 (preinstalled) |
| curl | `curl` 8.23.0-r0 | `curl` 8.14.1 (preinstalled) | `curl` 7.88.1 (preinstalled) |
| ca-certificates, unzip | `ca-certificates` 20261020-r0, `unzip` 6.0-r16 | same names, 6.0-29 | same names, 6.0-28 |
| jq | `jq` 1.8.2-r0 | `jq` 1.7.1-6 | `jq` 1.6-2.1 |
| ripgrep | `ripgrep` 15.1.0-r0 (binary `rg`) | `ripgrep` 14.1.1-1 | `ripgrep` 13.0.0-4 |
| nftables | `nftables` 1.1.5-r2 (+ `nftables-openrc`) | `nftables` 1.1.3-1 | `nftables` 1.0.6-2 |
| tinyproxy | `tinyproxy` 1.11.3-r0 (+ `tinyproxy-openrc`) | `tinyproxy` 1.11.2-1 | `tinyproxy` 1.11.1-2.1 |
| nc with `-X connect` | `netcat-openbsd` 1.234.1-r0, binary **`/usr/bin/nc`** (OpenBSD netcat) | `netcat-openbsd` 1.229-1, `/usr/bin/nc.openbsd` | `netcat-openbsd` 1.219-1, **`/bin/nc.openbsd`** |

Differences that break the Ubuntu script:

- `dpkg -S /usr/bin/nc.openbsd` (the `sshCheck` of `internal/provision/script.go`) **fails on bookworm** (`dpkg-query: no path found matching pattern /usr/bin/nc.openbsd`): the package ships `/bin/nc.openbsd` and `/bin` is a symlink to `usr/bin`. `dpkg -S /bin/nc.openbsd` answers `netcat-openbsd: /bin/nc.openbsd`. Alpine has no `dpkg`: `apk info -W /usr/bin/nc` answers `owned by netcat-openbsd-1.234.1-r0`, and there is no `nc.openbsd`.
- `tinyproxy`:

| | Alpine | trixie | bookworm |
|---|---|---|---|
| user | `tinyproxy` uid 101, gid 102, home `/dev/null`, shell `/sbin/nologin` | uid 100, gid 102, home `/run/tinyproxy`, `/bin/false` | uid 101, gid 107 |
| config | `/etc/tinyproxy/tinyproxy.conf` (default `Port 8888`, `User nobody`, no `PidFile`, no `LogFile`) | same path, `User tinyproxy`, `PidFile "/run/tinyproxy/tinyproxy.pid"`, `LogFile "/var/log/tinyproxy/tinyproxy.log"` | same |
| unit | OpenRC `/etc/init.d/tinyproxy`: `command_background=true`, `command_user=tinyproxy:tinyproxy`, `pidfile=/run/tinyproxy.pid`, `depend() { config; use dns; need net; after firewall }`, `reload` sends SIGUSR1 | `tinyproxy.service` (`ExecStart=/usr/bin/tinyproxy -d`, `ExecReload=/bin/kill -USR1 $MAINPID`), **enabled and started by the package on port 8888** | same |
| pid dir | none. `/run/tinyproxy/` does not exist. With the Orbit config `PidFile "/run/tinyproxy/tinyproxy.pid"` the daemon exits: `tinyproxy: Could not create file /run/tinyproxy/tinyproxy.pid: No such file or directory` (OpenRC marks it `crashed`) | `/run/tinyproxy` created by `tmpfiles.d` (`D /run/tinyproxy 0750 tinyproxy tinyproxy -`) | same |
| log dir | `/var/log/tinyproxy` `tinyproxy:tinyproxy`. The log file is `-rw-r----- tinyproxy tinyproxy` | `/var/log/tinyproxy` `tinyproxy:adm` 0750 | `/var/log/tinyproxy` `root:root` 0755 (log contents were not checked on Debian; the probes passed) |
| error page | `/usr/share/tinyproxy/default.html` (also `debug.html`, `stats.html`) | `/usr/share/tinyproxy/default.html` | `/usr/share/tinyproxy/default.html` |

  Every `tinyproxy` uid is below 1000, so the rule `meta skuid "tinyproxy"` and `meta skuid < 1000` of the firewall work unchanged. The Orbit config line `PidFile` must be dropped on Alpine (the Alpine adaptation deleted it) or its directory created at every boot.
- `nftables`:

| | Alpine | Debian (both) |
|---|---|---|
| service | OpenRC `nftables` (`depend: need localmount; after sysctl; before net; provide firewall`), **not** in a runlevel until added | `nftables.service`, `WantedBy=sysinit.target`, **disabled** by default (`Before=network-pre.target`) |
| rules file | `/etc/nftables.nft` (also `/etc/nftables.d`, `/var/lib/nftables/*.nft` included). Changeable with `rules_file=` in `/etc/conf.d/nftables` | `/etc/nftables.conf` |
| stock rules | `flush ruleset` plus `table inet filter` with **input `policy drop`** (loopback, established, ICMP, SLAAC allowed) and output `policy accept` | `flush ruleset` plus empty `inet filter` chains |
| start | `start_pre` runs `checkconfig` (`nft -c -f`), then `nft -f "$rules_file"` | `ExecStart=/usr/sbin/nft -f /etc/nftables.conf` |
| **stop** | `nft flush ruleset` (`stop()` in the init script) | `ExecStop=/usr/sbin/nft flush ruleset` |
| reload | `reload` = `start`, loads the file in place | `ExecReload=/usr/sbin/nft -f ...` |

  Stop observed to remove the Orbit firewall on both: Alpine `rc-service nftables stop` then `nft list tables` printed nothing, `rc-service nftables start` restored `table inet orbit_egress`. trixie `systemctl stop nftables` then `nft list tables` printed nothing. Same rule as Ubuntu: **never stop or restart the nftables service** (reload loads in place). The Orbit table file does not use `flush ruleset`, only `delete table inet orbit_egress`, so loading it does not remove other tables, and the Alpine stock file must be replaced, because its `flush ruleset` would remove the Orbit table.

D20 on musl and on Debian: every D20 tool resolved (probe `allowed "toolchain $tool"` for `git ssh curl jq rg unzip` passed on all four).

**Finding.** Package names match except `netcat-openbsd`'s path and the Debian Docker set (topic 7). The firewall file, the service stop semantics and the missing `/run/tinyproxy` directory are the differences that need distribution-specific code.

### 5. DNS

Stock, all four machines: `/etc/resolv.conf` is a symlink to the read-only `/opt/orbstack-guest/etc/resolv.conf` (`nameserver 0.250.250.200`, `options edns0`, `search .`). `/etc/nsswitch.conf`: `hosts: files dns`.

| | local resolver or stub | listeners (`ss`/`netstat`) |
|---|---|---|
| Alpine | none. No `systemd-resolved`, no `dnsmasq`, `unbound`, `named`. `dhcpcd` only | only `dhcpcd`: UDP 68 and UDP 546 (DHCPv6, on the link-local and ULA addresses) |
| trixie, bookworm | `systemd-resolved` package is installed, its unit is already **masked** by OrbStack (`systemd-resolved.service masked enabled`) | UDP 68 of `systemd-networkd` only |
| Ubuntu | `systemd-resolved.service masked`, `-monitor.socket` and `-varlink.socket` `disabled` | UDP 68 only |

So no stub answers on `127.0.0.53` on any distribution, and the DNS server of the machine is the OrbStack address, which the firewall already blocks for the agent (probe `agent DNS to OrbStack DNS` passed on all).

Alpine pitfall: `dhcpcd` rewrites `/etc/resolv.conf`. After `put /etc/resolv.conf` and a stop/start the file was `# Generated by dhcpcd from eth0.dhcp ... nameserver 0.250.250.200` (and for a moment held no `nameserver` line). The content is equal today, but it then follows the DHCP server of OrbStack. Fix tested: `echo 'nohook resolv.conf' >> /etc/dhcpcd.conf`. After a stop/start the file stayed `-rw-r--r-- root root 25 bytes`, `nameserver 0.250.250.200`. Agent behaviour after that, as in the Ubuntu spike: `getent hosts github.com` as agent finds no answer, `curl --noproxy '*' https://github.com` gives `Could not resolve host`, and `curl` through the environment proxy gives 200.

**Finding.** No local resolver or stub runs on any distribution, and the firewall already blocks the agent from OrbStack's DNS. Alpine needs `nohook resolv.conf` in `/etc/dhcpcd.conf` to keep a static resolver file.

### 6. Login environment

Where are proxy variables and `PATH` set for every login? Test: `ENVFILE_VAR` in `/etc/environment`, `PROFILED_VAR` in `/etc/profile.d/zz-probe.sh`, then:

| Entry path as `probe` | Alpine | trixie | bookworm | Ubuntu |
|---|---|---|---|---|
| `orb -m <m> -u probe env` (no shell) | both set | both set | both set | both set |
| `orb ... sh -c`, `sh -lc`, `bash -lc`, bare `orb -u probe` (stdin) | both set | both set | both set | both set |
| `su - probe -c` | `/etc/environment` **unset**, profile.d set | both set (`pam_env` in `/etc/pam.d/su`) | both set | both set |
| `runuser -l probe -c` | no `runuser` | `/etc/environment` **unset**, profile.d set | same | same |

A second edit of `/etc/environment` and `/etc/profile.d` showed up in the next `orb -u probe env` without a restart on Alpine and Ubuntu. So OrbStack builds the session environment itself on every `orb` call: it reads `/etc/environment` (no PAM needed, Alpine included) and the login profile. A process that `orb -u root` starts with `env -i` gets neither. `pgrep` of the `orbstack-agent` showed no such variable in its own environment.

PATH: the environment of an `orb` command is `PATH=/opt/orbstack-guest/bin-hiprio:<PATH of /etc/environment or the default>:/opt/orbstack-guest/bin:/opt/orbstack-guest/data/bin/cmdlinks`, and `BROWSER=/opt/orbstack-guest/bin/open`. On the provisioned Alpine machine the agent got `HTTPS_PROXY=http://127.0.0.1:3128` and the other six proxy variables from `/etc/environment`, `SHELL=/bin/bash` and `groups=agent orbstack`.

**Finding.** `/etc/environment` is the right place for the proxy and `PATH` on all four distributions, for every `orb` entry. Services that OpenRC or systemd starts do not read it: they need their own environment (`HTTP_PROXY=http://172.31.255.1:3128` in the Alpine `orbit-docker` unit, as in the systemd user unit). The Ubuntu `PATH` of `environment()` includes `/usr/games`, `/usr/local/games` and `/snap/bin`, which do not exist on Alpine (the adaptation removed them).

### 7. The provisioning script adapted by hand, and the probes

**Ubuntu control** (unmodified rendered script, `orb -m orbit-spike47-ubuntu -u root bash -s`): rc=0 in 18 s, second run rc=0. `probeScript` with the Mac port: **rc=0**. `afterStartProbes` after `orb stop`/`orb start`: **rc=0** (`boundary active at 71199858253 us; 7 processes with a uid from 1000 up, all started later`).

**trixie.** Unmodified script: `E: Unable to locate package docker-compose-v2`, rc=100. Changes:

1. Docker package list: `docker-compose-v2` is `docker-compose` (2.26.1-4; v2 plugin). `docker-buildx` 0.13.1 exists.
2. Add `docker-cli`: `docker.io` 26.1.5 only **recommends** it, and the script installs with `--no-install-recommends` (`bash: line 328: docker: command not found`, `orbit provisioning: docker buildx is missing`).

Then rc=0, `afterStartProbes` rc=0. `probeScript` rc=0 after one harness change: install `python3-minimal` (Debian images have no `python3`; the probe uses it for the IPv6 listener).

**bookworm.** Unmodified: `E: Unable to locate package docker-buildx` and `docker-compose-v2`, rc=100. Archive: `docker.io` 20.10.24, `docker-compose` 1.29.2 (Python v1), no `docker-buildx` and no `docker-cli` candidate, `rootlesskit` 1.1.0, `slirp4netns` 1.2.0. Changes to get further:

1. Docker package list: drop `docker-buildx` and `docker-compose-v2`.
2. Drop the two self-checks `docker buildx version` and `docker compose version`.
3. `ProxyCommand` in `00-orbit.conf`: `/usr/bin/nc.openbsd` becomes `/bin/nc.openbsd`.
4. The ownership check: `dpkg -S /bin/nc.openbsd` instead of `/usr/bin/nc.openbsd` (topic 4).

Then rc=0, `probeScript` rc=0 (with `python3-minimal`), `afterStartProbes` rc=0. The rootless `docker.service` user unit was `active`. **Not shown:** that the 20.10 engine pulls or runs a container (no `docker` command was run on Debian), and compose v2 or buildx at all.

**Alpine.** A 242-line POSIX `sh` script (`/private/tmp/.../scratchpad/art/alpine-root-script.sh`). It writes the same bytes as the Ubuntu render for the firewall (to `/etc/nftables.nft`), `/etc/tinyproxy/tinyproxy.conf` (without the `PidFile` line), `/etc/tinyproxy/allowlist`, `ssh_known_hosts` and `00-orbit.conf` (ProxyCommand path `/usr/bin/nc`), and `/etc/environment` (without `/usr/games:/usr/local/games:/snap/bin`). What is different, verbatim:

```
apk add --no-cache git openssh-client curl ca-certificates jq ripgrep unzip nftables tinyproxy netcat-openbsd bash shadow runuser setpriv iproute2-ss
```

(`bash` for the `bash -s` of the adapter and the installer, `shadow` for `usermod`/`gpasswd`, `runuser` and `setpriv` because the BusyBox versions lack `-u` and `--reuid`, `iproute2-ss` for `ss`.)

```
# /etc/init.d/orbit-proxy-address   (the orbit0 dummy address, 0755)
#!/sbin/openrc-run
description="Orbit: address on which containers reach the egress proxy"
depend() { need localmount; before tinyproxy; }
start() {
	ebegin "Putting 172.31.255.1 on orbit0"
	{ ip link show orbit0 >/dev/null 2>&1 || ip link add orbit0 type dummy; } && ip addr replace 172.31.255.1/32 dev orbit0 && ip link set orbit0 up
	eend $?
}
stop() { return 0; }

# /etc/init.d/orbit-harden  (runs /usr/local/libexec/orbit-harden, same script as Ubuntu plus "wheel")
depend() { need localmount; after nftables; }

# /etc/init.d/orbit-boundary  (the gate: runs /usr/local/libexec/orbit-boundary-gate, POSIX sh,
#                              nft list table + rc-service <unit> status for the four units)
depend() { after nftables orbit-proxy-address tinyproxy orbit-harden; }

# /etc/conf.d/tinyproxy
rc_need="nftables orbit-proxy-address"
```

Plus `rc-update add nftables orbit-proxy-address tinyproxy orbit-harden orbit-boundary default`, `nohook resolv.conf` in `/etc/dhcpcd.conf`, `nft -f /etc/nftables.nft`, `rc-service nftables start` (never `restart`), `rc-service tinyproxy restart`, `rc-service orbit-harden restart`, and the sudo, group and `apk info -W /usr/bin/nc` self-checks.

Problems met on the way (each fixed in the final script):

1. `tinyproxy` crashed because of the missing `/run/tinyproxy` (topic 4). `rc-service tinyproxy status` said `crashed` while `rc-service tinyproxy start` printed `[ ok ]`.
2. The first gate used `need nftables orbit-proxy-address tinyproxy orbit-harden`. In OpenRC a restart of a `need` dependency stops and restarts its dependents: the third run printed `Checking the boundary ... orbit-harden is not started` and `ERROR: orbit-boundary failed to start`. The same cascade would restart the rootless Docker engine on every `orbit init`. Fix: `after` (ordering only), the OpenRC counterpart of the systemd gate that deliberately avoids `Requires=` (`gateUnit` in `internal/provision/docker.go`). With it runs 4 and 5 are quiet (`WARNING: ... has already been started`).
3. `dhcpcd` rewrote `/etc/resolv.conf` (topic 5).

Runs: run 2 rc=0, runs 3 to 6 rc=0, marker `3` written. After `orb stop`/`orb start` the services came up in order (`nftables`, `networking`, `orbit-proxy-address`, `tinyproxy`, `orbit-harden`, `orbit-boundary`, `orbit-docker`).

Probes on Alpine (`probeScript` as root with `bash -s` after installing bash). It needed these changes, none of which touches the boundary:

- `systemctl ...` replaced by `rc-service <name> stop|restart|status` (service names without `.service`); `/etc/nftables.conf` replaced by `/etc/nftables.nft`; `apt-get` replaced by `apk add`.
- `pgrep -o -x tinyproxy` replaced by `pgrep -o tinyproxy` (BusyBox `-x` matches nothing).
- The IPv6 listener: `nc -6 -k -l 8443` instead of `python3` (no python on Alpine).
- The group check ignores `root` (member of `wheel` and `adm` by default); `wheel` added to the group list.
- The listener check excludes UDP 546 (`dhcpcd`, DHCPv6, Alpine only; root-owned).

Result: `probeScript` **rc=0** (first run after hardening, and again after a stop/start with Docker running), and the Alpine version of `afterStartProbes` **rc=0** (`orbit-boundary` at epoch 1794503317, 7 processes of uid >= 1000, all at epoch ~1794503317; one-second resolution, the services and the daemon started in the same second).

Not run: the probe of the host-engine container, the project mount, the `unforwardedProbes` (SSH forwarding), the integration test through `orbit init` itself (this spike runs no Go code).

**Finding.** Debian trixie needs two package changes. bookworm needs four (numbered above), and cannot satisfy the Docker design. Alpine needs its own script: package manager, OpenRC units, the `nftables` file path, no `PidFile`, `bash` installed, `wheel` hardened, resolver pinned.

### 8. Claude Code native installer on Alpine

As `agent` through the proxy, `orb -m orbit-spike47-alpine -u agent bash -c 'curl -fsSL https://claude.ai/install.sh | bash'`: **rc=0 in 26 s**.

```
Installing Claude Code native build latest...
✔ Claude Code successfully installed!   Version: 2.1.322   Location: ~/.local/bin/claude
```

- Needed packages: the toolchain plus `bash` (the pipe `| bash` has no other shell to run). `libgcc` was present (installed with `ripgrep`). `libstdc++` was **not** installed and was not needed in this version. No `gcompat`.
- The binary: `~/.local/share/claude/versions/2.1.322`, 325,808,392 bytes (ELF arm64, `ldd`: only `/lib/ld-musl-aarch64.so.1`, so it is the musl build). 311 MB on disk with the launcher.
- `claude --version` -> `2.1.322 (Claude Code)`. `claude doctor`: `Running: native (2.1.322)`, `Platform: linux-arm64`, `Search: OK (bundled)`. `claude -p hi` -> `Not logged in · Please run /login`.
- Hosts contacted (from `/var/log/tinyproxy/tinyproxy.log`, log truncated before the install): installer `claude.ai` 1, `downloads.claude.ai` 9, `api.anthropic.com` 1. After `claude doctor` and `claude -p hi`: `api.anthropic.com` 6. No refused request, so the Claude definition of `internal/agent/claude` is enough, as on Ubuntu (2.1.322 there too).
- `PATH` lacks `~/.local/bin` (the installer prints the same warning as on Ubuntu).
- Login itself: not exercised (as in the Ubuntu spike).

**Finding.** No new host and no musl-specific package, only `bash`. Claude Code runs on Alpine.

### 9. Rootless Docker on Alpine under OpenRC

Packages (`apk add --no-cache docker docker-cli-buildx docker-cli-compose docker-rootless-extras shadow-uidmap shadow-subids slirp4netns fuse-overlayfs`): 36 packages, the machine went from 49.9 MiB to 401.4 MiB. Versions: docker 29.5.2-r0, buildx 0.30.1, compose 2.40.3, `rootlesskit` 2.3.5-r10, `slirp4netns` 1.3.3-r0, `fuse-overlayfs` 1.16-r0. (`docker-rootless-extras` gives `dockerd-rootless`; `shadow-subids` gives `newuidmap`, `newgidmap`, `getsubids` and **empty** `/etc/subuid` and `/etc/subgid`.)

- **Rootful engine:** the packages install the OpenRC services `docker` and `containerd` and a `docker` group (gid 103). Neither is in a runlevel and neither runs: `rc-service docker status` is `stopped`, no `/var/run/docker.sock` or `/run/docker.sock`. OpenRC has no equivalent of `systemctl mask`. The script must check these states (the Alpine self-check of the adaptation does `rc-service <svc> status` for both). Making the init scripts non-executable was not tried.
- **subuid/subgid:** `useradd` created no sub-id entry (no file existed). `usermod --add-subuids 100000-165535 --add-subgids 100000-165535 agent` created the range; inside a privileged container `cat /proc/self/uid_map` showed `0 1000 1` and `1 100000 65536`.
- **`newuidmap`/`newgidmap`:** installed `-rwxr-xr-x` (not setuid). Without `chmod 4755` rootlesskit cannot map ids. The existing `helperPermissions` step already does this (the `helperMode` of `docker.go`).
- **Supervision and proxy settings:** the package ships a *user* OpenRC service (`/etc/user/init.d/docker`, `supervisor=supervise-daemon`, `command="dockerd-rootless"`), which needs a per-user OpenRC instance. There is no user manager on Alpine, so this spike did not use it (`openrc --user` untested). Instead a system service `orbit-docker`, `command_user=agent:agent`, run with `supervise-daemon`:

```
#!/sbin/openrc-run
description="Orbit: rootless Docker engine of the agent"
supervisor=supervise-daemon
command=/usr/bin/dockerd-rootless
command_user="agent:agent"
directory=/home/agent
output_logger="logger -t orbit-docker -p daemon.info"
error_logger="logger -t orbit-docker -p daemon.err"
respawn_delay=2
respawn_max=0
export HOME=/home/agent
export XDG_RUNTIME_DIR=/run/user/1000
export HTTP_PROXY=http://172.31.255.1:3128
export HTTPS_PROXY=http://172.31.255.1:3128
export NO_PROXY=localhost,127.0.0.1
depend() { need orbit-boundary; after tinyproxy; }
start_pre() {
	checkpath -d -m 0755 /run/user
	checkpath -d -m 0700 -o agent:agent /run/user/1000
}
```

  The uid `1000` is hard-coded in this test unit. Mistakes made on the way, for the implementer: `export PATH=...` in an OpenRC script removes `/usr/libexec/rc/bin` and every helper (`checkpath`, `mountinfo`, `einfo`) fails with `not found`; `output_log` in `/var/log` made `supervise-daemon` exit 1 at once (the agent cannot create the file; use `output_logger`); `checkpath -o agent:agent` needs the parent `/run/user` to exist. `respawn_max` defaults to 5: the supervisor gave up after 5 failures (`respawned ... too many times, exiting`), which is why `respawn_max=0` is there (systemd's `Restart=always`).
- **Boot order:** `need orbit-boundary`, so no process of the agent starts before the gate (the boundary at epoch 1794503317, the engine in the same second, see topic 7). After `orb stop`/`orb start` the engine was `started 00:00:14`. `orbit-boundary` is only started (never restarted) by the script, so `need` here does not cascade.
- **Client:** the repository's `DockerSetupScript` ran **unmodified** as the agent (`bash -s`): `Successfully created context "orbit-rootless"`, `~/.docker/config.json` with `proxies.default` at `172.31.255.1:3128`. `docker info`: `Server Version: 29.5.2`, `Storage Driver: overlayfs`, `Cgroup Version: 2`, `Security Options: rootless`, `docker compose version --short` `2.40.3`. Pulls through the proxy (the base list covers Docker Hub): `alpine:3.23` 14.6 MB, `curlimages/curl` 39.2 MB.
- **cgroup v2 delegation: not available.**

```
mount: none on /sys/fs/cgroup type cgroup2 (rw,nosuid,nodev,noexec,relatime,nsdelegate)   cgroup.controllers: cpuset cpu io memory pids
root cgroup: cgroup.subtree_control empty; processes in it: init, getty
echo "+memory" > /sys/fs/cgroup/cgroup.subtree_control  ->  sh: write error: Resource busy
```

  Root could enable `cpu memory pids` only after it moved `init` and `getty` into a child cgroup, and then create `orbit-agent` owned by the agent and start `dockerd-rootless` in it (`/proc/<dockerd>/cgroup` = `0::/orbit-agent`, `--exec-opt native.cgroupdriver=cgroupfs`). Even so: `WARNING: Running in rootless-mode without cgroups. Systemd is required to enable cgroups in rootless-mode.`, `Cgroup Driver: none`, and `docker run --memory 64m --pids-limit 50 --cpus 0.5` reported `memory.max`, `pids.max` `max` and `cpu.max` `max 100000` (limits accepted, not enforced; `--memory 64m` alone prints `Your kernel does not support swap limit capabilities or the cgroup is not mounted`). Under the real service, OpenRC places the engine in `0::/openrc.orbit-docker`, with the same result. This differs from Ubuntu, where systemd delegates `cpu memory pids` to `user@1000.service`.
- **Container probes** (agent, rootless engine, default login env; excerpts of `out/alpine-containers*.txt`, `out/alpine-privileged.txt`):

| Attempt | Result |
|---|---|
| Default network, direct `https://1.1.1.1` with `--noproxy` (curl image) | blocked (timeout 6 s) |
| Default network through the proxy: github.com / example.org | 200 / `CONNECT tunnel failed, response 403` |
| `--network host`, direct to 1.1.1.1 | blocked. Through `127.0.0.1:3128` and `172.31.255.1:3128`: 200 |
| `--network host --user 65534`, direct, and with `--resolve example.org:443:23.192.228.80` | blocked (timeout). The leak shape of the Ubuntu spike did not appear (the rule covers all uids from 1000 up) |
| `--dns 1.1.1.1 --cap-add NET_ADMIN --cap-add NET_RAW`: `nslookup`, `ping` | `connection timed out`, 100 % loss |
| `-v /etc:/h` write `nftables.nft`; `-v /:/h` write the allowlist and read `/etc/shadow` | `Permission denied` for all three |
| `--pid host`, `kill -9` of tinyproxy | `can't kill pid 514: Operation not permitted`. The pid stayed 514 |
| **`--privileged`** | **starts** (on Ubuntu it failed with `error reopening /dev/null`). Inside: `uid=0`, `CapEff 000001ffffffffff`, `uid_map` `0 1000 1` / `1 100000 65536` |
| `--privileged --network host`: `nft list ruleset`, `nft flush ruleset`, `ip link add d0 type dummy` | `Operation not permitted (you must be root)`, `Could not process rule: Operation not permitted`, `RTNETLINK answers: Operation not permitted`. The container's `net:[4026532403]` is the machine's namespace; the table stayed loaded and direct egress stayed blocked afterwards |
| `--privileged --pid host`, `--privileged -v /:/h` | `can't kill`, `Permission denied` on `/proc/1/root/etc/shadow`, on `/h/etc/nftables.nft` and on `/h/etc/shadow` |

  After all of them, as root: `nft list tables` printed `table inet orbit_egress`, `rc-service tinyproxy status` `started`, tinyproxy kept its pid. Not run: the repository's `containerTraffic` and `leakProbe` scripts (they need an `orbit-it-curl` image and a host-engine container), compose, buildx builds, and `fuse-overlayfs` as the storage driver (`overlayfs` was used).

**Finding.** Rootless Docker works on Alpine under OpenRC, behind the same boundary, with one extra service of our own. It cannot enforce container resource limits under any configuration tried, and `--privileged` starts (with no effect on the boundary in the tests above).

## Open problems and risks

1. **Alpine has no login gate** (topic 1). Conditional blocker, see the summary. Any `orb -m <m> -u agent` outside Orbit sees a machine whose boundary is still starting for 5 to 7 s. IPv4 egress stayed closed in all runs because the firewall precedes the IPv4 network; IPv6 was not checked in this window (topic 1). The root-side readiness and boot checks (`ReadinessScript`, `BootCheckScript`) use `systemctl` and `systemd-user-sessions.service` and need an OpenRC counterpart, and `waitForBoot` needs an Alpine definition of "the boot ended".
2. **`orb` ignores the login gate on every distribution** (topic 1). The Ubuntu design is the same. Already documented in `check.go`.
3. **Debian bookworm cannot provide the Docker design from its archive** (docker.io 20.10.24, no buildx, no compose v2, `docker-compose` 1.29.2). Docker's own apt repository would add a new trust root for root-side installs.
4. **bookworm's `dash` has no `pipefail`**, so a POSIX `sh` root script that keeps `set -o pipefail` fails there (`Illegal option -o pipefail`).
5. **Alpine rootless containers get no resource limits** (topic 9).
6. **Alpine `--privileged` containers start.** The probes show no boundary effect. The Ubuntu spike's note that `--privileged` cannot start no longer holds across distributions.
7. **`dhcpcd` on Alpine** rewrites the static resolver file and listens on UDP 546. The first needs `nohook resolv.conf`. The second is a root-owned DHCPv6 listener that the probe must allow.
8. **Alpine ships `sudo` and `doas`** (setuid, both installed by default). Hardening removes every rule, but the binaries stay. The harden script and the probes must name `wheel`.
9. **OpenRC propagation:** `need` restarts dependents (topic 7, item 2). The rootless engine must `need` the gate, and nothing may restart the gate.
10. **Debian `tinyproxy` starts after install on port 8888 with the stock config** until the script replaces it. Same as Ubuntu.
11. **Untested:** the project mount and write access of the agent on virtiofs on each distribution, the forwarded SSH agent socket per distribution (the group `orbstack`, gid 67278, exists on all four), Docker pulls and containers on Debian, `openrc --user`, `amd64` machines, an apk upgrade of `/etc/nftables.nft` (a modified conffile), the `bookworm` slow-gate race (only the closed gate was tried there), and the host-engine container probe.

## Facts against the epic's decisions (Q1–Q9)

Epic #46 records decisions 1 to 9 ("Decisions (formerly open questions)"), made on 2026-11-11 by the coordinator under the owner's delegation. E04-T02 (#48) records decision 1 as brief row D25 and carries the others into the spec. This section does not reopen them. For each decision it says whether the facts above **support**, **strain** or **contradict** it, as input for E04-T02. Where a fact contradicts a premise, the spec and the owner decide what follows. These are recommendations, not decisions.

1. **Q1, Alpine is the default (D25, the owner's own request). Strains.** Alpine works: the hand-written boundary passes `probeScript` and `afterStartProbes` (topic 7), Claude Code runs (topic 8), rootless Docker runs behind the boundary (topic 9), and the base image is 35 MB against 560 MB for Debian. But Alpine is a second implementation of the boundary (OpenRC units, another firewall file, no `/run/tinyproxy`, an OpenRC gate), it cannot hold an `orb` login (topic 1, the conditional blocker), its rootless containers get no resource limits (topic 9), and `--privileged` containers start (topic 9). Decision 6 already defers the switch, so these facts weigh on what decision 6 requires, not on whether Alpine is the target. For the spec: a distribution-specific root script in `internal/provision` (not a `sed` over the Ubuntu one).
2. **Q2, `--distro alpine|debian|ubuntu` as a fixed enum. Supports**, without new evidence: the three images were created from fixed `distro:version` strings with `--isolated --isolate-network`, and nothing in the spike needed a free-form image.
3. **Q3, pins Alpine 3.23, Debian 13 trixie, Ubuntu 26.04. Supports.** trixie needs two package changes and then passes every probe, including the restart (topic 7). It keeps systemd, the user-unit design of the Docker spec, the systemd readiness checks, and cgroup delegation. bookworm, which OrbStack 2.2.5 picks for a bare `debian`, needs four changes, cannot provide the Docker design from its archive (docker.io 20.10, no buildx, no compose v2), and its `dash` has no `pipefail`. So the pin must name `debian:trixie`, never `debian`. Alpine 3.23 resolved to 3.23.8.
4. **Q4, Alpine toolchain adds `bash`, `libgcc`, `libstdc++` (amends D20). Partly contradicts its premise.** `bash` is needed, as decided: the adapter's `bash -s` and the installer's `| bash` have no other shell (topics 2, 8). `libgcc` was present (pulled in by `ripgrep`). **`libstdc++` was not installed and Claude Code 2.1.322 did not need it** (`ldd` shows only `ld-musl`, topic 8). Root needed more than `shadow` or `util-linux`: the adaptation installed `shadow`, `runuser`, `setpriv`, `iproute2-ss` and `netcat-openbsd` (topic 7), which decision 4 classes as infrastructure or busybox replacements.
5. **Q5, `init --distro X` on a sandbox of another distribution fails and names `orbit destroy`. Supports.** The root scripts are not interchangeable: the Ubuntu script fails on Alpine before its first line (`bash: not found`), and the Alpine script uses `apk` and OpenRC. How `init` reads the distribution of an existing machine was not checked.
6. **Q6, Alpine becomes the default only after #52, #56 and the escape suite on Alpine. Supports, and adds conditions.** The facts name what that gate depends on: an OpenRC counterpart of `ReadinessScript`, `BootCheckScript` and `waitForBoot` on every Orbit entry path (open problem 1), the IPv6 state of the start window (topic 1, not checked), escape tests for `--privileged` containers (the Ubuntu spike found that they cannot start, Alpine starts them), and a position on container resource limits (the brief lists resource limits under *Later*, so this is a parity gap with Ubuntu, not a broken rule).
7. **Q7, the agent's login shell is `/bin/bash` on Alpine. Supports.** On the provisioned Alpine machine the agent got `SHELL=/bin/bash` (topic 6), and `bash` is already in the toolchain by decision 4. For the spec: with `bash` decided, the root script can keep the adapter's `bash -s`, provided that the adapter first installs `bash` on Alpine with `sh` (for example `sh -c 'apk add bash'`) before the first script. A POSIX `sh` root script without `pipefail` (explicit `|| fail`) would also run on dash and BusyBox ash, but it goes beyond decisions 4 and 7, and its distinct gain, bookworm's `dash`, does not matter once Q3 pins trixie.
8. **Q8, this epic adds no egress host. Supports; its condition is satisfied.** The native installer on musl contacted only `claude.ai`, `downloads.claude.ai` and `api.anthropic.com`, all in the Claude Code definition, with no refused request (topic 8). Root installs `apk` and `apt` packages directly, outside the proxy, and rootless Docker pulls went through the base list (topic 9). No stop for the owner's yes is needed. Docker's own apt repository, which only bookworm would need, is a new root-side trust root, not an egress host, and Q3 does not pin bookworm.
9. **Q9, `orbit ls` does not show the distribution in this epic. Supports**, without new evidence.

### Facts that contradict premises of the epic (for E04-T02)

The table "What is Debian/Ubuntu-specific today" on #46 states these premises. The spike observed otherwise:

- **`libstdc++`** (decision 4, row "Agent install"): not needed by Claude Code 2.1.322 on musl. Whether to keep it as a margin for later client versions is the spec's choice.
- **`USE_BUILTIN_RIPGREP=0`** (row "Agent install"): not set in any spike script, and `claude doctor` reported `Search: OK (bundled)`. The bundled ripgrep worked on musl. The system `ripgrep` of D20 is installed anyway.
- **`/etc/environment` is not read without PAM** (row "Login environment"): OrbStack reads it for every `orb -u` command on Alpine too (topic 6). Only `su -` ignores it on Alpine.
- **Whether `orb -u` logins pass a gate** (row "Login gate"): they pass every gate on every distribution, Ubuntu included (topic 1).
- **nftables stop behavior** (row "Firewall unit"): the OpenRC service flushes the ruleset on stop, the same as the Debian unit. Only the rules file differs (`/etc/nftables.nft`).

### Points the decisions do not cover

- **Where Orbit sets the proxy and `PATH`:** `/etc/environment`, on every distribution (topic 6). Services started by OpenRC or systemd need their own environment.
- **Per-distribution data, not conditionals in slices:** the package list, the `netcat` path, the Docker package list, the firewall file path, the `tinyproxy` pid setting, and the group list of the harden script could live in one `distro` description. This is an analogy with the rule for agents (`ARCHITECTURE.md:113`: no code outside `internal/agent/<name>` compares an agent name; D16, `ARCHITECTURE.md:182`). No such rule exists for distributions yet; the spec would have to add it.

## Cleanup confirmation

- `orb delete -f` for `orbit-spike47-alpine`, `orbit-spike47-trixie`, `orbit-spike47-bookworm` and `orbit-spike47-ubuntu`, all completed. `orb list` afterwards printed nothing (0 lines). `orb config show | grep -c orbit-spike47`: `0`. `orb list` before the delete, for the record: the four machines at 804.0 MB (Alpine with Claude Code, Docker and two images), 1.0 GB (bookworm), 1.1 GB (trixie) and 1.3 GB (Ubuntu), not comparable with each other.
- The Mac HTTP server (`python3 -m http.server 38471`) was stopped. `lsof -iTCP:38471 -sTCP:LISTEN`: no listening socket.
- The host engine was not touched (no host container, image or network was created). No global OrbStack setting was changed.
- Repository: only this note was added. The two throwaway Go test files used to render the scripts were deleted before the commit (`git status` clean apart from this file).
