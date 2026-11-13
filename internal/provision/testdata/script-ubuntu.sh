#!/bin/bash
{
set -euo pipefail
umask 022
cd /
export DEBIAN_FRONTEND=noninteractive
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy
fail() { echo "orbit provisioning: $*" >&2; exit 1; }
put() { cat > "$1.orbit-new"; chown root:root "$1.orbit-new"; chmod "$2" "$1.orbit-new"; mv -f "$1.orbit-new" "$1"; }
grep -qx 'ID=ubuntu' /etc/os-release || fail "this machine is not ubuntu"
grep -qx 'VERSION_CODENAME=resolute' /etc/os-release || fail "this machine is not ubuntu resolute"
rm -f /etc/orbit/provisioned
apt-get -o DPkg::Lock::Timeout=120 update -q </dev/null
apt-get -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends git openssh-client curl ca-certificates jq ripgrep unzip nftables tinyproxy netcat-openbsd </dev/null
trap 'rm -f /usr/sbin/policy-rc.d' EXIT
printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod 755 /usr/sbin/policy-rc.d
apt-get -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends docker.io docker-buildx docker-compose-v2 rootlesskit uidmap slirp4netns dbus-user-session </dev/null
rm -f /usr/sbin/policy-rc.d
trap - EXIT
systemctl disable --now docker.service docker.socket containerd.service 2>/dev/null || true
systemctl mask docker.service docker.socket containerd.service >/dev/null 2>&1
systemctl disable --now systemd-resolved.service systemd-resolved-varlink.socket systemd-resolved-monitor.socket 2>/dev/null || true
systemctl mask systemd-resolved.service systemd-resolved-varlink.socket systemd-resolved-monitor.socket >/dev/null 2>&1
put /etc/resolv.conf 0644 <<'ORBIT_EOF'
nameserver 0.250.250.200
ORBIT_EOF
mkdir -p /etc
put /etc/nftables.conf 0644 <<'ORBIT_EOF'
#!/usr/sbin/nft -f
table inet orbit_egress
delete table inet orbit_egress
table inet orbit_egress {
	set refused_ipv4 {
		type ipv4_addr
		flags interval
		elements = { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/3 }
	}
	set refused_ipv6 {
		type ipv6_addr
		flags interval
		elements = { ::/127, ::ffff:0:0/96, 64:ff9b::/96, fc00::/7, fe80::/10, ff00::/8 }
	}
	chain output {
		type filter hook output priority filter; policy accept;
		meta skuid "tinyproxy" oif "lo" tcp sport 3128 accept
		meta skuid "tinyproxy" meta l4proto { tcp, udp } th dport 53 ip daddr { 0.250.250.200 } accept
		meta skuid "tinyproxy" ip daddr @refused_ipv4 reject
		meta skuid "tinyproxy" ip6 daddr @refused_ipv6 reject
		meta skuid "tinyproxy" tcp dport { 80, 443 } accept
		meta skuid "tinyproxy" reject
		meta skuid < 1000 meta skuid != 501 accept
		ip daddr { 127.0.0.1, 172.31.255.1 } tcp dport 3128 accept
		meta l4proto { tcp, udp } th dport 53 reject
		oif "lo" ip daddr 127.0.0.0/8 accept
		oif "lo" ip6 daddr ::1 accept
		reject
	}
	chain input {
		type filter hook input priority filter; policy accept;
		iif != "lo" tcp dport 3128 drop
	}
}
ORBIT_EOF
mkdir -p /etc/tinyproxy
put /etc/tinyproxy/tinyproxy.conf 0644 <<'ORBIT_EOF'
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
ORBIT_EOF
mkdir -p /etc/tinyproxy
put /etc/tinyproxy/allowlist 0644 <<'ORBIT_EOF'
^api\.anthropic\.com$
^archive\.ubuntu\.com$
^auth\.docker\.io$
^claude\.ai$
^codeload\.github\.com$
^deb\.debian\.org$
^dl-cdn\.alpinelinux\.org$
^files\.pythonhosted\.org$
^ghcr\.io$
^github\.com$
^nodejs\.org$
^pkg-containers\.githubusercontent\.com$
^ports\.ubuntu\.com$
^production\.cloudflare\.docker\.com$
^production\.cloudfront\.docker\.com$
^proxy\.golang\.org$
^pypi\.org$
^raw\.githubusercontent\.com$
^registry-1\.docker\.io$
^registry\.npmjs\.org$
^security\.ubuntu\.com$
^ssh\.github\.com$
^sum\.golang\.org$
ORBIT_EOF
mkdir -p /etc
put /etc/environment 0644 <<'ORBIT_EOF'
PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games:/snap/bin"
HTTP_PROXY=http://127.0.0.1:3128
HTTPS_PROXY=http://127.0.0.1:3128
http_proxy=http://127.0.0.1:3128
https_proxy=http://127.0.0.1:3128
NO_PROXY=localhost,127.0.0.1
no_proxy=localhost,127.0.0.1
ORBIT_EOF
mkdir -p /etc/systemd/system
put /etc/systemd/system/orbit-proxy-address.service 0644 <<'ORBIT_EOF'
[Unit]
Description=Orbit: address on which containers reach the egress proxy
Before=tinyproxy.service systemd-user-sessions.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'ip link show orbit0 >/dev/null 2>&1 || ip link add orbit0 type dummy'
ExecStart=ip addr replace 172.31.255.1/32 dev orbit0
ExecStart=ip link set orbit0 up

[Install]
WantedBy=multi-user.target
ORBIT_EOF
mkdir -p /etc/systemd/system/nftables.service.d
put /etc/systemd/system/nftables.service.d/orbit.conf 0644 <<'ORBIT_EOF'
[Unit]
Before=systemd-user-sessions.service

[Service]
ExecStop=
ORBIT_EOF
mkdir -p /etc/systemd/system/tinyproxy.service.d
put /etc/systemd/system/tinyproxy.service.d/orbit.conf 0644 <<'ORBIT_EOF'
[Unit]
Requires=nftables.service orbit-proxy-address.service
After=nftables.service orbit-proxy-address.service
Before=systemd-user-sessions.service
StartLimitIntervalSec=0

[Service]
Restart=on-failure
RestartSec=1
ORBIT_EOF
mkdir -p /etc/systemd/system
put /etc/systemd/system/orbit-harden.service 0644 <<'ORBIT_EOF'
[Unit]
Description=Orbit: remove sudo from every account except root
Before=systemd-user-sessions.service
StartLimitIntervalSec=0

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/libexec/orbit-harden

[Install]
WantedBy=multi-user.target
ORBIT_EOF
mkdir -p /etc/systemd/system/systemd-user-sessions.service.d
put /etc/systemd/system/systemd-user-sessions.service.d/orbit.conf 0644 <<'ORBIT_EOF'
[Unit]
Requires=nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service
After=nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service
StartLimitIntervalSec=0
ORBIT_EOF
mkdir -p /usr/local/libexec
put /usr/local/libexec/orbit-harden 0755 <<'ORBIT_EOF'
#!/bin/sh
set -eu
find /etc/sudoers.d -mindepth 1 ! -name README -delete
for group in sudo admin adm; do
  getent group "$group" >/dev/null || continue
  for member in $(getent group "$group" | cut -d: -f4 | tr ',' ' '); do
    [ "$member" = root ] || gpasswd -d "$member" "$group" >/dev/null
  done
done
passwd -l root >/dev/null
getent passwd | while IFS=: read -r name _ uid _; do
  if { [ "$uid" -ge 1000 ] && [ "$uid" -ne 65534 ]; } || [ "$uid" -eq 501 ]; then passwd -l "$name" >/dev/null; fi
done
ORBIT_EOF
mkdir -p /etc/systemd/user
put /etc/systemd/user/docker.service 0644 <<'ORBIT_EOF'
[Unit]
Description=Orbit: rootless Docker Engine of the agent
ConditionUser=agent

[Service]
Type=notify
NotifyAccess=all
Environment=PATH=/usr/share/docker.io/contrib:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=HTTP_PROXY=http://172.31.255.1:3128
Environment=HTTPS_PROXY=http://172.31.255.1:3128
Environment=NO_PROXY=localhost,127.0.0.1
ExecStart=/usr/share/docker.io/contrib/dockerd-rootless.sh
ExecReload=/bin/kill -s HUP $MAINPID
TimeoutSec=0
RestartSec=2
Restart=always
LimitNOFILE=infinity
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
Delegate=yes
KillMode=mixed

[Install]
WantedBy=default.target
ORBIT_EOF
mkdir -p /etc/systemd/system
put /etc/systemd/system/orbit-boundary-gate.service 0644 <<'ORBIT_EOF'
[Unit]
Description=Orbit: the boundary is up before the user manager of the agent starts
After=nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service
StartLimitIntervalSec=0

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/libexec/orbit-boundary-gate

[Install]
WantedBy=multi-user.target
ORBIT_EOF
mkdir -p /usr/local/libexec
put /usr/local/libexec/orbit-boundary-gate 0755 <<'ORBIT_EOF'
#!/bin/bash
set -euo pipefail
fail() { echo "$*" >&2; exit 1; }
nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"
[ -z "$(systemctl show --value -p ExecStop nftables.service)" ] || fail "nftables.service has an ExecStop= that flushes the firewall table"
for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service; do
  systemctl is-active --quiet "$unit" || fail "$unit is not active"
done
ORBIT_EOF
mkdir -p /etc/systemd/system/user@.service.d
put /etc/systemd/system/user@.service.d/orbit.conf 0644 <<'ORBIT_EOF'
[Unit]
Requires=orbit-boundary-gate.service
After=orbit-boundary-gate.service
ORBIT_EOF
mkdir -p /etc/ssh/ssh_config.d
put /etc/ssh/ssh_config.d/00-orbit.conf 0644 <<'ORBIT_EOF'
Host github.com ssh.github.com
    HostName ssh.github.com
    Port 443
    HostKeyAlias github.com
    StrictHostKeyChecking yes
    ProxyCommand /usr/bin/nc.openbsd -X connect -x 127.0.0.1:3128 %h %p

Match localuser agent
    IdentityAgent SSH_AUTH_SOCK
ORBIT_EOF
mkdir -p /etc/ssh
put /etc/ssh/ssh_known_hosts 0644 <<'ORBIT_EOF'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
ORBIT_EOF
systemctl daemon-reload
systemctl enable nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service orbit-boundary-gate.service >/dev/null
systemctl start orbit-proxy-address.service
nft -f /etc/nftables.conf
systemctl start nftables.service
systemctl restart tinyproxy.service
systemctl restart orbit-harden.service
systemctl start systemd-user-sessions.service
id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent
passwd -l agent >/dev/null
[ "$(id -u agent)" -ge 1000 ] || fail "the uid of agent is below 1000"
usermod -aG orbstack agent
chmod 4755 /usr/bin/newuidmap /usr/bin/newgidmap
systemctl --global enable docker.service >/dev/null 2>&1
agent_uid=$(id -u agent)
systemctl start orbit-boundary-gate.service
loginctl enable-linger agent
systemctl start "user@$agent_uid.service"
runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user daemon-reload
runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user start docker.service
nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"
[ -z "$(systemctl show --value -p ExecStop nftables.service)" ] || fail "nftables.service has an ExecStop= that flushes the firewall table"
for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do
  systemctl is-active --quiet "$unit" || fail "$unit is not active"
done
[ ! -e /run/nologin ] || fail "logins are disabled"
for unit in systemd-resolved.service systemd-resolved-varlink.socket systemd-resolved-monitor.socket; do
  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || fail "$unit is not masked"
  if systemctl is-active --quiet "$unit"; then fail "$unit is active"; fi
done
getent passwd | while IFS=: read -r name _ uid _ _ home _; do
  [ "$name" = root ] && continue
  rules=$(sudo -n -l -U "$name" 2>/dev/null || true)
  case "$rules" in *"may run"*) fail "$name has a sudo rule";; esac
  if [ "$uid" -lt 1000 ] && [ "$uid" -ne 501 ]; then
    case "$home" in /home/*) fail "$name has uid $uid below the floor and a home under /home";; esac
    case " $(id -nG "$name") " in *" orbstack "*) fail "$name has uid $uid below the floor and the group orbstack";; esac
  fi
done
if runuser -u agent -- sudo -n true 2>/dev/null; then fail "agent can use sudo"; fi
for group in sudo admin adm docker lxd disk shadow; do
  case " $(id -nG agent) " in *" $group "*) fail "agent is in the group $group";; esac
done
[ -f /etc/ssh/ssh_config.d/00-orbit.conf ] || fail "/etc/ssh/ssh_config.d/00-orbit.conf is missing"
[ -f /etc/ssh/ssh_known_hosts ] || fail "/etc/ssh/ssh_known_hosts is missing"
owner=$(dpkg -S /usr/bin/nc.openbsd 2>/dev/null || true)
[ "$owner" = "netcat-openbsd: /usr/bin/nc.openbsd" ] || fail "/usr/bin/nc.openbsd does not belong to netcat-openbsd"
case " $(id -nG agent) " in *" orbstack "*);; *) fail "agent is not in the group orbstack";; esac
for unit in docker.service docker.socket containerd.service; do
  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || fail "$unit is not masked"
  if systemctl is-active --quiet "$unit"; then fail "$unit is active"; fi
done
[ ! -e /var/run/docker.sock ] && [ ! -e /run/docker.sock ] || fail "a rootful Docker socket exists"
[ -e /var/lib/systemd/linger/agent ] || fail "agent has no linger"
[ "$(grep -c . /etc/subuid || true)" = 1 ] || fail "/etc/subuid does not hold exactly one entry"
IFS=: read -r sub_name sub_start _ < /etc/subuid
[ "$sub_name" = agent ] && [ "$sub_start" -ge 1000 ] || fail "/etc/subuid does not hold a range of agent from 1000 up"
[ "$(grep -c . /etc/subgid || true)" = 1 ] || fail "/etc/subgid does not hold exactly one entry"
IFS=: read -r sub_name sub_start _ < /etc/subgid
[ "$sub_name" = agent ] && [ "$sub_start" -ge 1000 ] || fail "/etc/subgid does not hold a range of agent from 1000 up"
[ -n "$(find /usr/bin/newuidmap -maxdepth 0 -user 0 -perm 4755)" ] || fail "/usr/bin/newuidmap is not owned by root with mode 4755"
[ -n "$(find /usr/bin/newgidmap -maxdepth 0 -user 0 -perm 4755)" ] || fail "/usr/bin/newgidmap is not owned by root with mode 4755"
systemctl is-active --quiet orbit-boundary-gate.service || fail "orbit-boundary-gate.service is not active"
runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user is-active --quiet docker.service || fail "docker.service of agent is not active"
docker buildx version >/dev/null || fail "docker buildx is missing"
docker compose version >/dev/null || fail "docker compose is missing"
mkdir -p /etc/orbit
put /etc/orbit/provisioned 0644 <<'ORBIT_EOF'
4
ORBIT_EOF
}
