# Capability: sandbox-provisioning

Root provisions a sandbox machine so that the agent runs without privileges behind a root-owned firewall and egress proxy.

## Requirement: Agent user without privileges
The system SHALL create the user `agent` with a uid from 1000 up, no sudo rule, and no privileged group (`sudo`, `admin`, `adm`, `docker`, `lxd`, `disk`, `shadow`). Provisioning adds the agent to no group; E01-S04 adds `orbstack` for the forwarded SSH agent socket (R08).

### Scenario: The agent tries to become root
- WHEN a process of `agent` runs `sudo -n true`, `su root`, or `su <default user>`
- THEN each command fails

## Requirement: Default user without sudo
The system SHALL leave no sudo rule, no membership in `sudo`, `admin`, or `adm`, and no usable password for any account except root, also after a restart of the machine.

### Scenario: OrbStack default user after provisioning
- WHEN root runs `sudo -l -U <default user>` and `passwd -S <default user>` after provisioning and again after `orb restart`
- THEN no rule is listed and the password is locked

## Requirement: Firewall for every uid from 1000 up and the default user
The system SHALL reject all IPv4 and IPv6 traffic, including DNS, UDP, and ICMP, that a socket of a uid from 1000 up or of the default user sends, except TCP to the proxy at `127.0.0.1:3128` or `172.31.255.1:3128` and traffic on the loopback interface to a loopback address. The system SHALL give these uids no name resolution.

### Scenario: Direct traffic from the agent
- WHEN `agent` connects directly to a public address, sends UDP, pings an IPv4 or IPv6 address, queries a DNS server, or runs `resolvectl query example.org`
- THEN each attempt fails

### Scenario: Direct traffic from the default user
- WHEN a process with the uid of the default user connects directly to `192.168.139.3` or to a public address
- THEN the connection fails

## Requirement: Proxy reachable only from inside the machine
The system SHALL drop every connection to port 3128 that does not arrive on the loopback interface.

### Scenario: Connection from outside
- WHEN the Mac or another machine connects to port 3128 on an address of the machine
- THEN the connection fails

### Scenario: Traffic from a container uid
- WHEN a socket of a uid from 100000 up (a rootless container user) connects to a public address
- THEN the connection fails

## Requirement: Proxy allowlist
The system SHALL let the proxy open a `CONNECT` tunnel on port 443 only to a host of the base allowlist (D24) or of the `EgressDomains` of the agent of the last `init`, matched exactly and without case. Each `init` rebuilds the allowlist from its own `--agent`.

### Scenario: Allowed and refused hosts
- WHEN `agent` asks the proxy for `CONNECT github.com:443`, `CONNECT example.org:443`, and `CONNECT github.com:22`
- THEN the first succeeds and the other two get 403

### Scenario: Hostile domain in a definition
- WHEN a definition holds `*.anthropic.com`, `1.2.3.4`, `localhost`, `Claude.AI`, or `example.com:22`
- THEN `policy.Allowlist` refuses the whole list and `init` fails before it asks OrbStack anything

### Scenario: No agent
- WHEN the owner runs `orbit init` without `--agent`
- THEN the proxy refuses `CONNECT api.anthropic.com:443` and allows `CONNECT github.com:443`

### Scenario: Init again without the agent
- WHEN the owner runs `orbit init --agent claude-code` and then `orbit init` without `--agent` on the same sandbox
- THEN the proxy refuses `CONNECT api.anthropic.com:443`, and Orbit wrote no record of the installed agent

Source: .ai/specs/2026-11-11-claude-code-agent-definition.md

## Requirement: Proxy refuses non-public destinations
The system SHALL reject every connection of the proxy user to a loopback, private, link-local, shared, benchmark, multicast, or reserved address, except port 53 on its DNS resolvers, and every connection of the proxy user to a port other than 443 and 53.

### Scenario: Proxy user to a private address
- WHEN a process of the proxy user connects to `192.168.139.3`, `198.19.249.1`, `169.254.169.254`, `127.0.0.1:22`, or `fd07:b51a:cc66:f0::fe`
- THEN the connection fails

## Requirement: Enforcement survives a restart
The system SHALL start the firewall, the proxy address, and the proxy as enabled units before `systemd-user-sessions.service`.

### Scenario: Restart of the machine
- WHEN the machine restarts and `agent` then connects directly to a public address and through the proxy to an allowed host
- THEN the direct connection fails and the proxied one succeeds

## Requirement: Fixed toolchain
The system SHALL install git, openssh-client, curl, ca-certificates, jq, ripgrep, and unzip as root, and the agent SHALL NOT be able to install a system package.

### Scenario: The agent runs apt-get
- WHEN `agent` runs `apt-get install -y htop`
- THEN it fails, and `git`, `ssh`, `curl`, `jq`, `rg`, and `unzip` run

## Requirement: Init of a project directory
The system SHALL create the machine of the project directory when it does not exist, and SHALL then provision it, also when it already existed. When provisioning fails on a machine that the same call created, the system SHALL delete that machine and report the failure.

### Scenario: First init
- WHEN `init` runs in a project directory without a machine
- THEN the machine is created and provisioned, and `Created` is true

### Scenario: Init of an existing machine
- WHEN `init` runs in a project directory whose machine exists
- THEN the machine is provisioned again and is not created or deleted, and `Created` is false

### Scenario: Provisioning fails on a new machine
- WHEN provisioning fails on a machine that this call created
- THEN the machine is deleted and the error names the failed provisioning

Source: .ai/specs/2026-11-10-sandbox-provisioning.md
