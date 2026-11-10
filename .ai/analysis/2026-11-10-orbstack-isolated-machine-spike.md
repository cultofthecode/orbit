# Spike: OrbStack isolated machine as the Orbit sandbox (A05, A02, Q09)

Date: 2026-11-10. Time-boxed spike, run by an agent on the owner's Mac. Each result below was observed. Untested items say so.

## Environment

| Item | Value |
|---|---|
| Host | macOS (Darwin 27.2.0), Apple Silicon |
| OrbStack | `orb version`: `Version: 2.2.5 (2020500)`, commit `bf842321` |
| Machine | `orbit-spike`, Ubuntu 26.04.1 LTS "resolute" (current LTS, OrbStack default), arm64 |
| Kernel (shared VM kernel) | `7.2.12-orbstack-00394-gbecdd6130068 #1 SMP PREEMPT Thu Oct 22 22:17:17 UTC 2026 aarch64` |
| Init / cgroups | systemd 259, cgroup v2. Root controllers `cpuset cpu io memory pids`. Delegated to `user@1000.service`: `cpu memory pids` |
| LSMs | `capability,landlock,yama,bpf` (no AppArmor, so Ubuntu's userns restriction does not apply) |
| Docker (in machine) | Docker CE 29.9.3, rootlesskit 3.3.0, slirp4netns 1.3.3, pasta 2026-01-20 |
| Proxy / firewall | tinyproxy 1.11.3, nftables (Ubuntu package) |
| Claude Code | 2.1.322 (native installer) |
| Baseline before the spike | `orb list`: no machines. `docker ps -a`: no containers. `docker volume ls`: none. Networks: only `bridge`, `host`, `none`. |

## Method

1. `orb create --isolated --isolate-network --mount <scratch>/spike-ws ubuntu:26.04 orbit-spike` (11.9 s).
2. Q09 probes as root with curl/getent, using `orb -m orbit-spike -u root bash -s < q09.sh`. Target: `orbit-spike-target` (`nginx:alpine`, IP 192.168.215.2) on the host engine, and `python3 -m http.server 38517 --bind 0.0.0.0` on the Mac. Ran with `isolate_network` on, then `orb config set machine.orbit-spike.isolate_network false` + `orb restart`, then on again.
3. A05: as root, installed nftables, tinyproxy, uidmap, slirp4netns, passt, and Docker CE from download.docker.com. Disabled the rootful `docker`/`containerd` services. Created `agent` (uid 1000, groups only `agent`) and ran `loginctl enable-linger agent`. As agent, ran `dockerd-rootless-setuptool.sh install`. Applied the firewall and proxy below. Ran attack scripts as `agent` (`orb -m orbit-spike -u agent bash -s < agent-tests.sh`, `container-tests.sh`).
4. A02/usability: as `agent`, installed Claude Code with the official `curl -fsSL https://claude.ai/install.sh | bash` through the proxy. Grew the allowlist only from `Proxying refused on filtered domain` lines in `/var/log/tinyproxy/tinyproxy.log`. No sign-in.
5. Footprint: `orb list`/`orb info`, `df`/`du`, cgroup `memory.current`, `ps` RSS, and macOS `ps` RSS of the OrbStack VM process.

Scripts: `/private/tmp/claude-501/-Users-jacob-Project-orbit/d2434c5f-d1b0-4a0e-8f61-d1ff8e51a5dd/scratchpad/spike-scripts/` (`q09.sh`, `egress-setup.sh`, `allow.sh`, `agent-tests.sh`, `container-tests.sh`).

## Findings

### 1. Mechanics: works, with two surprises

- `orb create --isolated --isolate-network --mount SRC` works. **The mount without `:DEST` appeared at `/mnt/mac/<SRC>`, not at the same path.** `mount`: `mac on /mnt/mac/private/tmp/.../spike-ws type virtiofs (rw,relatime)`, and `ls <SRC>` failed with "No such file or directory". The docs example says "same path inside the machine". With an explicit `SRC:SRC` (`orb config set machine.orbit-spike.mounts "$S:$S"` + restart), it mounted at the same absolute path: `mac on /private/tmp/.../spike-ws type virtiofs`. **Orbit must always pass `SRC:SRC`.**
- Running commands from macOS, non-interactively: `orb -m orbit-spike -u root <cmd>` and `orb -m orbit-spike -u agent <cmd>` work for any user, with no password. Scripts can be piped with `bash -s < file` (stdin is forwarded). `orb run` / ssh were not needed and were not tested.
- Writable by a non-root user: yes. A file created as `jacob` showed up on the Mac owned by the Mac user. virtiofs shows ownership as the accessing user: root sees `root root`, jacob sees `jacob jacob`. Writing as `agent` (uid 1000) was not tested separately.
- Mac file system not visible: `/Users` does not exist. `/mnt/mac` holds only the parent directories of the mount (`/mnt/mac/private/...`). `/mnt/linux -> /`.
- `mac`, `pbpaste`, `orbctl`, `osascript`, `open` are present in `/opt/orbstack-guest/bin` but all fail with `dial: no such file or directory`. `find / -xdev -type s` found no socket except systemd's.
- **Default user:** OrbStack creates `jacob` (uid 501) with `jacob ALL=(ALL) NOPASSWD:ALL` and membership in `sudo`, `adm`, `orbstack`. Its password is locked (`!`), so `su jacob` from `agent` failed. Orbit should remove that user or its sudo rule.

### 2. Q09: default reachability. `--isolate-network` blocks less than expected

| Target (from inside machine, as root) | isolate_network = true | isolate_network = false |
|---|---|---|
| Internet IPv4 (`https://example.com`) | 200 | 200 |
| Internet IPv6 (`curl -6`) | timeout | refused. No IPv6 egress in either state |
| Host-engine container by IP `192.168.215.2:80` | **200 (reachable)** | 200 |
| Host-engine container `orbit-spike-target.orb.local` (resolves to 192.168.138.3) | **200 (reachable)** | 200 |
| `host.orb.internal:38517` (resolves to `fd07:b51a:cc66:f0::fe`) | blocked (connect refused) | **200** |
| Gateway `192.168.139.1:38517` | refused | refused |
| Mac bridge IP `192.168.139.3:38517` | **200 (reachable)** | 200 |
| Mac LAN IP `192.168.8.244:38517` | **200 (reachable)** | 200 |
| Host Docker socket (`/var/run/docker.sock`, any unix socket) | absent | absent |
| Docker API TCP `host.orb.internal:2375`, `docker.orb.local:2375` | refused | refused |
| Other machines | not tested (no other machine existed, and creating one was out of scope) | not tested |

The Mac's own server log confirmed the hits: `192.168.139.18 - - "GET / HTTP/1.1" 200` (the machine's IP, via 192.168.139.3) and `192.168.8.244 ...` (via the LAN IP).

**Verdict: `--isolate-network` blocks only the `host.orb.internal` alias.** It does not block containers on the host engine, and it does not block the Mac through its other addresses. The OrbStack docs promise "block other machines and host IPs". That promise did not hold for the Mac's non-gateway IPs or for host-engine containers. ARCHITECTURE.md's "blocked: --isolate-network" edge to the host engine is wrong. Isolation from the host engine (PostgreSQL, Redis) must come from the in-machine firewall (D14), which did block these destinations for the agent (see 3).

### 3. A05: egress enforcement. Holds (with the rule set below), not yet hardened

Verdict: **holds** for every bypass tried, as long as the rule matches **every uid >= 1000**, not only the agent uid. A rule for "uid agent" alone **leaks** (see the last bullet).

Rootless Docker in the isolated machine:
- Works. It needs `uidmap` (setuid `newuidmap`/`newgidmap`), `/etc/subuid` entry `agent:100000:65536` (created by `useradd`), `dbus-user-session`, `loginctl enable-linger agent` (root), and `slirp4netns` (or `passt`). `user.max_user_namespaces = 2147483647`. cgroup v2 delegation of `cpu memory pids` works out of the box. `io`/`cpuset` are not delegated (`docker info`: "No cpuset support", "No io.weight support").
- `docker info`: Rootless, `NetworkDriver: slirp4netns`, `PortDriver: builtin`, Storage `overlayfs`, Cgroup driver `systemd`, v2.
- **dockerd runs in the machine's real netns** (RootlessKit 3 detach-netns): `/proc/1/ns/net` and `/proc/<dockerd>/ns/net` are both `net:[4026532403]`. So `--network host` containers share the machine netns.
- **`docker run --privileged` fails for any image:** `runc create failed: ... error reopening /dev/null inside container: open /dev/null: operation not permitted`.

DNS decision: block port 53 (TCP and UDP) for all uids >= 1000. Names are resolved only by the proxy (tinyproxy runs as uid `tinyproxy` < 1000). The agent has no working resolver. `curl` without a proxy fails with "Could not resolve host". This is intended, and tools must use `HTTPS_PROXY`.

Results as `agent` (uid 1000):

| Attempt | Result |
|---|---|
| `curl https://example.org` / `example.com`, no proxy env | blocked: `Could not resolve host` (DNS blocked) |
| `curl -x 127.0.0.1:3128 https://example.org` (not allowlisted) | blocked: `CONNECT tunnel failed, response 403` |
| `curl -x 127.0.0.1:3128 https://example.com` (allowlisted) | **200** |
| Proxy `CONNECT 1.1.1.1:443`, `CONNECT example.com:22` | 403 |
| Proxy `CONNECT example.com:443` with SNI/Host `example.org` | 200: the tunnel goes to example.com's IP (domain-fronting class, see risks) |
| `curl --connect-to ...:1.1.1.1:443`, `nc -zv 1.1.1.1 443` | blocked (timeout) |
| `nc` to gateway `192.168.139.1:53`, Mac `192.168.139.3:38517`, host container `192.168.215.2:80`, IPv6 `docker.orb.local`, IPv6 `host.orb.internal` | blocked |
| `dig @1.1.1.1`, `dig @0.250.250.200` (OrbStack DNS), `dig +tcp @1.1.1.1` | blocked (timed out) |
| UDP `echo > /dev/udp/1.1.1.1/443` | `Operation not permitted` |
| `ping 1.1.1.1`, `ping -6 2606:4700:4700::1111` | 100 % loss |
| `nft list ruleset` / `nft flush ruleset` | `Operation not permitted (you must be root)` |
| `sudo -n true` | `I'm sorry agent. I'm afraid I can't do that` |
| `su -c id root`, `su -c id jacob` (stdin closed) | `Authentication failure` |
| Write `/etc/tinyproxy/allowlist`, `/etc/tinyproxy/tinyproxy.conf` | `Permission denied` |
| `pkill -x tinyproxy`, `systemctl stop tinyproxy`/`nftables` | `Operation not permitted` / `Access denied` |
| `unshare -rn` | only `lo` (DOWN), no route. `curl https://1.1.1.1` failed. `nft flush ruleset` inside succeeded but only in the new netns, and host egress afterwards was still blocked |

Results from rootless containers (agent's dockerd):

| Attempt | Result |
|---|---|
| Default network: direct `https://example.org` | blocked (`Could not resolve host`). `--resolve` to an IP: timeout |
| Default network: `nslookup` via slirp DNS, `--dns 1.1.1.1` | blocked (no servers reached) |
| Default network: `-e https_proxy=http://172.31.255.1:3128` example.com / example.org | 200 / 403 |
| Default network: proxy at `127.0.0.1` or `10.0.2.2` | unreachable (slirp4netns `disable-host-loopback`). Containers must use a non-loopback proxy IP, hence the `orbit0` dummy address `172.31.255.1` |
| `--network host`: direct (DNS or IP) | blocked |
| `--network host`: proxy at 127.0.0.1 or 172.31.255.1, example.com | 200 |
| `--network host --user 65534`: direct to IP | blocked; proxy works |
| Custom bridge network, direct | blocked |
| `ping` with `--cap-add NET_ADMIN --cap-add NET_RAW` | 100 % loss |
| `--cap-add NET_ADMIN ip link add d0 type dummy` | succeeds, but only in the container's own netns |
| `--pid host`: sees tinyproxy (`ps`), `pkill`/`kill -9 <pid>` | `can't kill pid 4422: Operation not permitted`. Proxy kept pid 4422 |
| `-v /etc/tinyproxy:/h` write allowlist, `-v /etc:/h` write `nftables.conf`, `-v /:/host` read `/etc/shadow` | `Permission denied` (files show as `nobody`) |
| `--privileged` | cannot start (see above) |
| Rootless dockerd pulls (`docker pull`) | go through the proxy only after `HTTPS_PROXY=http://172.31.255.1:3128` in `~/.config/systemd/user/docker.service.d/proxy.conf`. Without it, blocked |

Which component's packets are matched (per-uid counters, because `log` lines never appeared in `journalctl -k`/`dmesg` and `nf_log_all_netns` does not exist in the machine):
- Default-network container traffic leaves through **slirp4netns, a process of uid 1000** (`ps`: `agent 3789 slirp4netns`). The drop counted on `meta skuid 1000` (4 packets).
- `--network host --user 65534` traffic is sent by the container process itself in the machine netns, as **kuid 165533** (`uid_map`: `0 1000 1; 1 100000 65536`). The drop counted on `meta skuid 100000-165535` (3 packets).
- **Leak shown:** with a temporary rule `meta skuid 100000-165535 accept` (what a "match uid agent only" rule set amounts to), `docker run --network host --user 65534 curlimages/curl --resolve example.org:443:23.192.228.80 https://example.org` completed a TCP + TLS handshake to the remote server. curl reached certificate verification (`certificate has expired`, exit 60), so the connection went out. The temporary rule was then removed. **Rules must cover the agent's whole subuid range, or simply all uids >= 1000.**

IPv6: the machine has no IPv6 internet egress (even root: `curl -6` timeout/refused). It does have ULA IPv6 to OrbStack (`host.orb.internal`, `docker.orb.local` resolve to `fd07:...`). The `inet` table covers IPv6, and agent IPv6 to those ULAs was blocked.

Fail-closed: when tinyproxy was down (systemd start limit tripped by repeated restarts), dockerd's pull failed with `proxyconnect tcp: dial tcp 172.31.255.1:3128: i/o timeout`. Nothing went out directly.

Persistence after `orb stop`/`orb start`: `nftables` and `tinyproxy` came back `active` with the table loaded. **The `orbit0` dummy interface did not** (`Device "orbit0" does not exist`), because the spike created it with `ip link` only. Containers then lose the proxy (fail closed). A real setup needs a systemd-networkd `.netdev`/`.network` or a unit. Boot-order race (agent user services before nftables) was not tested.

### 4. Usability without sudo: works, after root prepares the machine

| Task as `agent` | Result |
|---|---|
| Claude Code native installer through proxy | works in 19 s. Needs only `claude.ai` (install.sh, redirects) and `downloads.claude.ai`. Installs to `~/.local/share/claude/versions/2.1.322`, link `~/.local/bin/claude` (warns PATH lacks `~/.local/bin`). The installer also tried `api.anthropic.com` (blocked, not required) |
| `claude --version` | `2.1.322 (Claude Code)`, no network needed |
| `claude -p hi` | `Not logged in · Please run /login` (5 CONNECTs to `api.anthropic.com`) |
| `claude doctor` | `No installation issues found` |
| `git clone https://github.com/...` with `HTTPS_PROXY` | works (`github.com`) |
| `git ls-remote git@github.com:...` (SSH) | fails (port 22 blocked). Git over SSH needs another path |
| Node 22 user-space (tarball from `nodejs.org` into `~/.local`) | works: node v22.23.4, npm 10.9.9 |
| `npm install` (local) and `npm install -g` (prefix `~/.local`) | works (`registry.npmjs.org`) |
| `docker build` with `RUN apk add` | **fails** unless `~/.docker/config.json` has `proxies.default.httpProxy/httpsProxy = http://172.31.255.1:3128`. With it, works (`jq-1.8.2`). Needs `dl-cdn.alpinelinux.org` |
| `docker pull` | needs the rootless dockerd systemd drop-in (see 3) and the Docker Hub hosts |
| **First thing that breaks without sudo** | `apt-get install` (`Could not open lock file /var/lib/dpkg/lock-frontend ... are you root?`). Also `python3 -m pip` is missing (`No module named pip`) and cannot be added. Root must preinstall system packages, or Orbit needs a root-side "install package" path |

### 5. A02: login preconditions. Reachable, login itself untested

All documented hosts completed TLS through the proxy once allowlisted (`ssl_verify_result=0`). Any HTTP status means the host was reached:

| Host | HTTP status via proxy | Role (Anthropic network-config doc) |
|---|---|---|
| `api.anthropic.com` | 404 | API, feature flags, telemetry events. **Contacted by `claude -p` before login** |
| `claude.ai` | 403 | claude.ai auth, install script |
| `claude.com` | 200 | browser sign-in page (opened on the Mac) |
| `platform.claude.com` | 200 | OAuth token exchange/refresh for claude.ai and Console |
| `sso.claude.com` | 200 | SSO orgs only |
| `downloads.claude.ai` | 403 | installer, auto-updater |
| `mcp-proxy.anthropic.com` | 404 | claude.ai MCP connectors (optional) |
| `code.claude.com` | 302 | docs lookups (optional) |
| `http-intake.logs.us5.datadoghq.com`, `browser-intake-us5-datadoghq.com` | 403 / 403 | telemetry (optional, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`) |
| `raw.githubusercontent.com` | 301 | `/release-notes` changelog (optional) |
| `statsig.anthropic.com` | refused by proxy (not allowlisted); not observed from the client | — |

Login flow (from Anthropic's authentication doc, not exercised): `claude` opens a browser. "If the browser doesn't open automatically, press `c` to copy the login URL ... If your browser shows a login code instead of redirecting back after you sign in, paste it into the terminal at the `Paste code here if prompted` prompt." So a paste-code path exists and no localhost callback inside the machine is required. In the isolated machine `open`/`xdg-open` and `pbcopy` fail (`dial: no such file or directory`), so the URL will not open by itself, and copying it to the Mac's clipboard via `c` probably fails (not tested). The user copies the URL from the terminal. Credentials land in `~/.claude/.credentials.json` (mode 0600) in the agent's home, inside the machine. **Untested:** the actual `/login`, token refresh, and which hosts a logged-in session contacts.

### 6. Footprint

| Measure | Value |
|---|---|
| Disk right after `orb create` (Ubuntu 26.04) | `orb list`: 620.5 MB |
| After provisioning (packages, Docker CE, agent, proxy, rootless Docker, no images) | `orb list`: 1.3 GB. `du -sxh /`: 1.3 G (`/usr` 909 M, `/var/lib/apt` 149 M) |
| After Claude Code + images (alpine, curlimages/curl, node:22-slim, one built image) + Node tarball | `orb list`/`orb info`: 2.3 GB. `du -sxh /`: 2.2 G. Claude Code: 326 M. Rootless Docker store: 395 M (`docker system df`: images 401.7 MB, node:22-slim 347 MB). Node in `~/.local/lib`: 19 M |
| `df /` inside machine | `/dev/vdb1 726G 2.3G` (shared OrbStack data disk. Size is the VM disk, not a per-machine quota) |
| macOS data dir | `~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data/`: `Operation not permitted` (TCC). Not measured |
| Machine memory, idle (after `drop_caches`) | cgroup `memory.current` 406 MB (anon 314 MB, file 82 MB). Sum of process RSS 487 MB (shared pages double-counted) |
| Top idle RSS | rootless `dockerd` 141 MB (after builds; 63 MB fresh), `containerd` 81 MB, rootlesskit child `exe` 40 MB, rootlesskit 34 MB, tinyproxy 34 MB, systemd (root) 16 MB, systemd (agent) 15 MB, journald 15 MB, systemd (jacob) 13 MB |
| macOS VM process (`/Applications/OrbStack.app/Contents/Frameworks/OrbStack`) RSS | running: 2501 MB → stopped (+20 s): 2208 MB → running again (+15 s): 2455 MB. Earlier, right after image builds: 3969 MB. The VM balloons memory, so these are noisy. Before-creation was not recorded |
| Time: create | 11.9 s (`orb create`, includes image download) |
| Time: stop / start | 1.28 s / 0.06 s, first `orb -m ... true` succeeded immediately |
| Time: delete | 0.16 s |
| Image sharing with host engine | **Not shared, duplicated.** Rootless images live in `/home/agent/.local/share/docker` (395 M). The host engine's image list held only `nginx:alpine` during the spike. Each sandbox pulls and stores its own images. |

## Final firewall and proxy configuration (verbatim)

`/etc/nftables.conf` (as applied). During the spike the last rule also had `flags skuid` and two counter-only rules (`meta skuid 1000 counter`, `meta skuid 100000-165535 counter`) for attribution:

```
#!/usr/sbin/nft -f
table inet orbit_egress
delete table inet orbit_egress
table inet orbit_egress {
  chain output {
    type filter hook output priority filter; policy accept;
    meta skuid < 1000 accept
    ip daddr { 127.0.0.1, 172.31.255.1 } tcp dport 3128 counter accept
    meta l4proto { tcp, udp } th dport 53 counter reject
    oif lo ip daddr 127.0.0.0/8 counter accept
    oif lo ip6 daddr ::1 counter accept
    log prefix "orbit-egress-drop " counter reject
  }
}
```

Proxy address for containers (not persistent in the spike; must be made persistent):

```
ip link add orbit0 type dummy
ip addr replace 172.31.255.1/32 dev orbit0
ip link set orbit0 up
```

`/etc/tinyproxy/tinyproxy.conf` (root:root 0644):

```
User tinyproxy
Group tinyproxy
Port 3128
Timeout 600
DefaultErrorFile "/usr/share/tinyproxy/default.html"
LogFile "/var/log/tinyproxy/tinyproxy.log"
LogLevel Info
PidFile "/run/tinyproxy/tinyproxy.pid"
MaxClients 100
Allow 127.0.0.1
Allow 172.31.255.1
ConnectPort 443
Filter "/etc/tinyproxy/allowlist"
FilterType ere
FilterURLs Off
FilterCaseSensitive Off
FilterDefaultDeny Yes
DisableViaHeader Yes
```

`/etc/tinyproxy/allowlist` at the end of the spike (one exact host per line):

```
^example\.com$
^registry-1\.docker\.io$
^auth\.docker\.io$
^production\.cloudflare\.docker\.com$
^production\.cloudfront\.docker\.com$
^dl-cdn\.alpinelinux\.org$
^claude\.ai$
^downloads\.claude\.ai$
^api\.anthropic\.com$
^claude\.com$
^platform\.claude\.com$
^sso\.claude\.com$
^mcp-proxy\.anthropic\.com$
^code\.claude\.com$
^http-intake\.logs\.us5\.datadoghq\.com$
^browser-intake-us5-datadoghq\.com$
^raw\.githubusercontent\.com$
^github\.com$
^codeload\.github\.com$
^registry\.npmjs\.org$
^nodejs\.org$
```

Agent-side proxy settings that were needed:
- Shell: `HTTPS_PROXY=http://127.0.0.1:3128 HTTP_PROXY=... NO_PROXY=localhost,127.0.0.1`.
- Rootless dockerd: `~/.config/systemd/user/docker.service.d/proxy.conf` with `Environment=HTTP_PROXY=http://172.31.255.1:3128 HTTPS_PROXY=http://172.31.255.1:3128 NO_PROXY=localhost,127.0.0.1`.
- Builds and containers: `~/.docker/config.json` `"proxies": {"default": {"httpProxy": "http://172.31.255.1:3128", "httpsProxy": "http://172.31.255.1:3128", "noProxy": "localhost,127.0.0.1"}}`.

These are agent-owned files. Changing them cannot widen egress, because nftables still applies.

## Required host names

- **Claude Code install:** `claude.ai`, `downloads.claude.ai` (observed as sufficient).
- **Claude Code start and operation (observed before login):** `api.anthropic.com`.
- **Login (documented, reachable; login not exercised):** `claude.ai`, `platform.claude.com`. `claude.com` and `sso.claude.com` are opened in the Mac browser, so the machine probably does not need them (not verified).
- **Optional:** `mcp-proxy.anthropic.com`, `code.claude.com`, `raw.githubusercontent.com`, `http-intake.logs.us5.datadoghq.com`, `browser-intake-us5-datadoghq.com`.
- **Docker Hub pulls (observed):** `registry-1.docker.io`, `auth.docker.io`, `production.cloudflare.docker.com`, `production.cloudfront.docker.com`. Alpine packages: `dl-cdn.alpinelinux.org`.
- **Dev tooling (observed):** `github.com` (git HTTPS clone), `registry.npmjs.org`, `nodejs.org`.

## Open problems and risks

1. **`--isolate-network` does not isolate from the host engine or the Mac** (Q09 table). Only the in-machine firewall protects these, and only for non-root uids. Root in the machine, and the default `jacob` user with NOPASSWD sudo, can reach host-engine containers and Mac services on 192.168.139.3 / LAN IP. Report to OrbStack or re-check on a newer version. Also update ARCHITECTURE.md.
2. **The design depends on the agent never becoming root** in its machine, on one shared kernel. A kernel or setuid escalation defeats all of D14. The machine also has setuid `newuidmap`, `su`, `sudo`, and `ping` with `cap_net_raw`.
3. **Default user `jacob` has NOPASSWD sudo.** Orbit must delete it or remove the sudoers line. `orb -m <machine>` uses it by default.
4. **Rules must match uid >= 1000 (or the subuid range), not only the agent uid.** Proven leak otherwise (`--network host --user N`).
5. **Host-name filtering is not data-loss prevention.** CONNECT to an allowlisted host with a different SNI/Host worked (domain fronting on shared CDNs). Allowlisting `github.com` or `registry.npmjs.org` permits uploads there. tinyproxy sees only the CONNECT host, without TLS inspection.
6. **No DNS for the agent.** Tools that ignore `HTTPS_PROXY` (raw sockets, some language runtimes, `git` over SSH, UDP/QUIC) do not work. This is safe but rough.
7. **Persistence:** the `orbit0` address is lost on restart. tinyproxy can hit systemd's start limit on rapid restarts (`start of the service was attempted too often`). Use `reload`/SIGUSR1 for allowlist changes, not restart. Boot-order race not tested.
8. **Rootless Docker limits seen:** `--privileged` cannot start any container here, there are no `io`/`cpuset` cgroup controls, and images are duplicated per sandbox (~400 MB for a node image plus a few small ones).
9. **No sudo:** `apt-get` and `pip` are unavailable to the agent. Root must preinstall a toolchain, or Orbit needs a controlled root-side install path.
10. **Netfilter `log` produced no visible kernel log in the machine.** Audit of blocked attempts needs counters, NFLOG + ulogd, or the proxy log.
11. **Untested:** actual `/login` and a logged-in session's hosts; reachability of other OrbStack machines; `orb run`/ssh access paths; whether `c` (copy URL) works without the Mac clipboard; agent-owned writes on the virtiofs mount; pasta as rootless network driver; boot-time race.

## Cleanup confirmation

- `orb delete -f orbit-spike`: completed in 0.16 s. `orb list`: empty. `orb config show | grep -c orbit-spike`: `0`.
- `docker rm -f orbit-spike-target`: removed. `docker ps -a`: empty. `docker rmi nginx:alpine` (pulled for this spike): deleted. `docker images`: empty.
- `docker volume ls`: empty. `docker network ls`: `bridge`, `host`, `none`. Same as baseline.
- The Mac `python3 -m http.server 38517` was stopped. `lsof -iTCP:38517 -sTCP:LISTEN`: nothing.
- Per-machine settings changed during the spike (`isolate_network`, `mounts`) applied only to `orbit-spike` and were removed with it. No global OrbStack setting was changed.
- The scratch directories (`spike-ws`, `spike-scripts`, `httproot`) remain in the session scratchpad. No repo file other than this report was changed.
