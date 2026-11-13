# Capability: sandbox-provisioning

Root provisions a sandbox machine so that the agent runs without privileges behind a root-owned firewall and egress proxy.

## Requirement: Agent user without privileges
The system SHALL create the user `agent` with a uid from 1000 up, no sudo rule, and no privileged group (`sudo`, `admin`, `adm`, `docker`, `lxd`, `disk`, `shadow`). The system SHALL add `agent` to the group `orbstack` exactly when the machine forwards the host SSH agent, and to no other group.

### Scenario: The agent tries to become root
- WHEN a process of `agent` runs `sudo -n true`, `su root`, or `su <default user>`
- THEN each command fails

### Scenario: Group of the forwarded socket
- WHEN provisioning runs on a machine that forwards the agent, and again on one that does not
- THEN `id -nG agent` holds `orbstack` on the first and not on the second

Frozen by: `internal/provision/script_test.go` (`TestScriptCreatesTheAgentOnceWithOnlyTheSocketGroup`)

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
The system SHALL let the proxy open a `CONNECT` tunnel on port 443 only, and forward a plain HTTP request, to a host of the base allowlist (D26: the hosts of D24, `dl-cdn.alpinelinux.org` included, plus `ghcr.io` and `pkg-containers.githubusercontent.com`, `deb.debian.org`, `archive.ubuntu.com`, `security.ubuntu.com`, and `ports.ubuntu.com`) or of the `EgressDomains` of the agent of the last `init`, matched exactly and without case. The proxy does not limit the port of a plain HTTP request; the firewall does (Requirement "Proxy refuses non-public destinations"): it reaches only ports 80 and 443. Each `init` rebuilds the allowlist from its own `--agent`.

### Scenario: Allowed and refused hosts
- WHEN `agent` asks the proxy for `CONNECT github.com:443`, `CONNECT example.org:443`, and `CONNECT github.com:22`
- THEN the first succeeds and the other two get 403

### Scenario: GHCR hosts
- WHEN the owner runs `orbit init` without `--agent`, and `agent` asks the proxy for `CONNECT ghcr.io:443`, `CONNECT pkg-containers.githubusercontent.com:443`, and `CONNECT quay.io:443`
- THEN the first two succeed and the third gets 403

### Scenario: Alpine mirror
- WHEN the owner runs `orbit init` without `--agent`, and `agent` asks the proxy for `CONNECT dl-cdn.alpinelinux.org:443`
- THEN it succeeds

### Scenario: Plain HTTP to the package mirrors
- WHEN `agent` asks the proxy for `GET http://deb.debian.org/debian/dists/trixie/InRelease`, `GET http://ports.ubuntu.com/ubuntu-ports/dists/resolute/InRelease`, `GET http://example.org/`, `CONNECT deb.debian.org:80`, and `GET http://deb.debian.org:8080/`, and connects to `deb.debian.org:80` without the proxy
- THEN the first two succeed, the proxy answers 403 to the third and the fourth, the fifth fails because the firewall refuses the connection of the proxy to port 8080, and the direct connection fails

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
The system SHALL reject every connection of the proxy user to a loopback, private, link-local, shared, benchmark, multicast, or reserved address, except port 53 on its DNS resolvers, and every connection of the proxy user to a port other than 80, 443, and 53.

### Scenario: Proxy user to a private address
- WHEN a process of the proxy user connects to `192.168.139.3`, `198.19.249.1`, `169.254.169.254`, `127.0.0.1:22`, or `fd07:b51a:cc66:f0::fe`
- THEN the connection fails

### Scenario: Proxy user to a refused port
- WHEN a process of the proxy user connects to the public address of `deb.debian.org` on port 80, 443, 22, and 8080
- THEN the connections to ports 80 and 443 succeed, and the connections to ports 22 and 8080 fail

Source: .ai/specs/2026-11-11-rootless-docker.md

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
The system SHALL create the machine of the project directory on its resolved distribution when it does not exist, and SHALL then provision it with the root script of that distribution and set up the Docker client of `agent`, also when it already existed. When provisioning or the client setup fails on a machine that the same call created, the system SHALL delete that machine and report the failure.

### Scenario: First init
- WHEN `init` runs in a project directory without a machine and without `--distro`
- THEN the machine is created from the pinned Ubuntu image and provisioned, the client setup runs as `agent`, `Created` is true, and the output names `ubuntu`

### Scenario: Init of an existing machine
- WHEN `init` runs in a project directory whose machine exists
- THEN the machine is provisioned again with the root script of its own distribution, the client setup runs again, `~/.docker/config.json` keeps its other keys, the machine is not created or deleted, and `Created` is false

### Scenario: Provisioning fails on a new machine
- WHEN provisioning fails on a machine that this call created
- THEN the machine is deleted and the error names the failed provisioning

### Scenario: Client setup fails on a new machine
- WHEN the client setup fails on a machine that this call created
- THEN the machine is deleted, the agent is not installed, and the error names the client setup

Frozen by: `internal/sandbox/initialize/handler_test.go` (`TestHandleCreatesTheMachine`, `TestHandleKeepsAnExistingMachine`)

Source: .ai/specs/2026-11-11-rootless-docker.md
Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, MODIFIED)

Source: .ai/specs/2026-11-10-sandbox-provisioning.md

## Requirement: Provisioning keeps the agent and its home
The system SHALL create the user `agent` only when it does not exist, and the root script SHALL NOT remove, move, or recreate the user or any directory under `/home`.

### Scenario: Script content
- WHEN the root script is rendered
- THEN it holds no `userdel`, no `deluser`, no `usermod` of `agent`, and no line that names `/home` (besides the `/home/*)` pattern of the self-check)

Frozen by: `internal/provision/script_test.go` (`TestScriptWritesNothingIntoAHome`, `TestScriptCreatesTheAgentOnceWithoutGroups`)

Source: .ai/specs/2026-11-11-subscription-sign-in.md

## Requirement: Distribution of a new sandbox
The system SHALL create a new sandbox on Ubuntu 26.04 by default, or on Debian 13 trixie with `--distro debian`, from an image that only the adapter's pinned table names, and SHALL refuse any other `--distro` value before any OrbStack command.

### Scenario: Debian sandbox
- WHEN `orbit init --distro debian` runs in a project directory without a machine
- THEN OrbStack creates the machine from `debian:trixie` with `--isolated --isolate-network`, and the root script of `debian` provisions it

### Scenario: Value outside the enum
- WHEN `orbit init --distro <v>` runs with `v` in `alpine`, `Ubuntu`, `ubuntu:26.04`, `debian:bookworm`, `bookworm`
- THEN init fails with `unknown distribution "<v>"; use one of: ubuntu, debian`, and runs no OrbStack command

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED)

## Requirement: An existing sandbox keeps its distribution
The system SHALL read the distribution of an existing sandbox from the image that OrbStack stores for it, without a command in the machine. It SHALL keep that distribution when `--distro` is not given, and SHALL refuse a different `--distro`, or an image that no pin matches, before any command in the machine.

### Scenario: No flag on an existing Debian sandbox
- WHEN `orbit init` runs without `--distro` where the sandbox runs Debian
- THEN the Debian root script provisions it again

### Scenario: Another distribution requested
- WHEN `orbit init --distro debian` runs where the sandbox runs Ubuntu
- THEN init fails with a message that names both distributions and `orbit destroy`, and the machine is neither changed nor deleted

### Scenario: Unknown stored image
- WHEN the stored image of the existing machine matches no pin
- THEN init fails and runs no command in the machine

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED)

## Requirement: The root script runs only on its distribution
The system SHALL make the root script fail, before it removes the marker or changes anything, unless `/etc/os-release` names the distribution and release that the script was rendered for.

### Scenario: Debian script on an Ubuntu machine
- WHEN the root script rendered for `debian` runs on an Ubuntu 26.04 machine
- THEN it exits non-zero, and `/etc/orbit/provisioned` and every boundary file are as they were

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED)

## Requirement: The same boundary on every distribution
The system SHALL give a Debian sandbox every requirement of this capability and of `sandbox-containers`, `sandbox-lifecycle`, `sandbox-shell`, `ssh-agent-forwarding`, and `agent-install` that an Ubuntu sandbox has.

### Scenario: Integration and escape runs on Debian
- WHEN the integration tests and the escape suite run with `ORBIT_IT_DISTRO=debian`
- THEN every test that passes on Ubuntu passes on Debian, and the privileged container refusal of `sandbox-containers` holds on Debian as on Ubuntu

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, ADDED; scenario amended by the owner's decision of 2026-11-13, see `sandbox-containers`)
