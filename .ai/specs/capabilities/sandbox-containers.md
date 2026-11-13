# Capability: sandbox-containers

The agent has its own rootless Docker Engine inside the sandbox. Its builds and containers reach the network only through the egress proxy, and nothing of the host engine is reachable.

## Requirement: Rootless engine of the agent
The system SHALL give the user `agent` its own rootless Docker Engine, started at every boot of the machine only after the firewall, the proxy address, the proxy, and the hardening unit are active, and SHALL run no rootful Docker or containerd daemon.

### Scenario: Pull, build, and run
- WHEN `agent` runs `docker pull alpine` and `docker pull ghcr.io/containerd/busybox`, builds three images, `FROM debian:trixie` and `FROM ubuntu:26.04` with `RUN apt-get update && apt-get install -y curl` and `FROM alpine` with `RUN apk add curl`, and runs `docker run --rm alpine true`
- THEN each succeeds on the engine of `agent`, `docker info` reports `rootless`, and the tinyproxy log shows the registry hosts and `dl-cdn.alpinelinux.org` (`ghcr.io` and `pkg-containers.githubusercontent.com` included) as `CONNECT` requests and `deb.debian.org` and `ports.ubuntu.com` as plain HTTP requests

### Scenario: Package source outside the allowlist
- WHEN `agent` builds `FROM debian:trixie` with `RUN echo 'deb http://security.debian.org/debian-security trixie-security main' > /etc/apt/sources.list.d/x.list && apt-get update`
- THEN the build fails, and the proxy answers 403 to the request for `security.debian.org`

### Scenario: Registry outside the allowlist
- WHEN `agent` runs `docker pull quay.io/prometheus/busybox`
- THEN the pull fails, and the proxy answers 403 to `CONNECT quay.io:443`

### Scenario: No rootful engine
- WHEN root checks the machine after provisioning and after `orbit stop` and `orbit start`
- THEN `docker.service`, `docker.socket`, and `containerd.service` are masked and inactive, and neither `/var/run/docker.sock` nor `/run/docker.sock` exists

### Scenario: Boundary unit failed at boot
- WHEN `nftables.service` fails at boot
- THEN the user manager of `agent` does not start, and no process of a uid from 1000 up runs, except a process that `orb -u agent` starts

### Scenario: Repair after a failed boot
- WHEN the gate and the user manager of `agent` failed at boot, the cause is removed, and the owner runs `orbit init`
- THEN the gate is active, the user manager and `docker.service` of `agent` are active, and the client setup succeeds

### Scenario: Docker Compose
- WHEN `agent` runs `docker compose version`, then `docker compose up --abort-on-container-exit --exit-code-from app` in a directory whose `compose.yaml` holds one service `app` with `image: alpine` and `command: ["true"]`, then `docker compose down`
- THEN `docker compose version` reports v2, `up` creates the network and the container of the project on the engine of `agent` and exits 0, and `down` removes both

## Requirement: Container traffic goes only through the proxy
The system SHALL let the engine of `agent`, its builds, and its containers reach the network only through the egress proxy, and SHALL make every direct connection fail, also after a restart, while the proxy is down, and when the agent replaces the engine with one that it starts or configures itself.

### Scenario: Direct connections from containers
- WHEN a container connects to a public address or a host with no allow rule, on the default network, with `--network host`, on a custom network, in a `docker compose` service on the network of its project, with `--user 65534`, with `--dns 1.1.1.1`, or with `--cap-add NET_ADMIN --cap-add NET_RAW`
- THEN each connection fails

### Scenario: Daemon replaced by the agent, in the network namespace of the machine
- WHEN `agent` stops its `docker.service`, starts `dockerd-rootless.sh` itself with `DOCKERD_ROOTLESS_ROOTLESSKIT_NET=host` (rootlesskit `--net=host`) and without any proxy environment, and starts a process with `rootlesskit --net=host` and `setpriv --reuid=1000` that connects to a public address directly and to an allowed host through `172.31.255.1:3128`
- THEN the process runs as a sub-uid of `agent` in the network namespace of PID 1 (the test checks both first, so that it exercises the leak path of the spike, A05), the direct connection fails, and only the connection through the proxy succeeds; a `docker run --network host` attempt is only logged

### Scenario: Agent-owned unit at boot
- WHEN `agent` writes `~/.config/systemd/user/docker.service` that starts the daemon of the previous scenario, and the machine restarts
- THEN that unit starts only after `orbit-boundary-gate.service` is active, and the same sub-uid process probe, run after the restart, finds that a direct connection fails

### Scenario: Refused host through the proxy
- WHEN a container with the proxy settings of `config.json` requests `https://example.org`
- THEN the proxy answers 403

### Scenario: After a restart
- WHEN the owner runs `orbit stop` and `orbit start`, and `agent` then pulls an image and runs a container that requests an allowed host
- THEN both succeed through the proxy

### Scenario: Proxy down
- WHEN root stops `tinyproxy.service` and `agent` pulls an image or a container connects to a public address
- THEN both fail, and nothing leaves the machine directly

## Requirement: No host engine
The system SHALL give the sandbox no socket, endpoint, variable, image, or container of the host engine.

### Scenario: Host engine out of reach
- WHEN `agent` lists contexts, images, and containers, and a container connects to the address of a container on the host engine
- THEN only the context `orbit-rootless` exists, nothing of the host engine is listed, and the connection fails

### Scenario: Host Docker variables
- WHEN `orbit init` runs on a host with `DOCKER_HOST`, `DOCKER_CONFIG`, and `DOCKER_AUTH_CONFIG` set
- THEN none of them is set in a shell or script of `agent`

## Requirement: Privileged containers on the tested OrbStack version
This requirement describes observed platform behavior, not a guarantee of Orbit. Orbit cannot enforce that `docker run --privileged` fails: rootless Docker supports `--privileged` inside the user namespace of `agent`, and the agent owns its daemon and can start it with any flag (Q5). On Ubuntu 26.04 on OrbStack 2.2.5 the flag fails for a reason of the platform. On Debian 13 trixie the provisioning profile refuses it: a root-owned runtime guard replaces `runc` (`internal/provision/privileged.go`), and the escape suite checks the guard (`runtimeGuardTable` in `cmd/orbit/escape_verdict_test.go`). The system SHALL carry an integration test that pins this behavior on the OrbStack version that the test runs on: the test fails when `--privileged` starts a container, and the owner then decides (Q5 b). The test does not run on the machine of a user, so a later OrbStack there can let the flag start a container without notice.

### Scenario: Privileged container
- WHEN the integration test runs `docker run --privileged alpine true` as `agent` on OrbStack 2.2.5 on Ubuntu 26.04 or on Debian 13 trixie
- THEN the container does not start on either distribution; on an OrbStack version where it starts, the test fails

### Scenario: Equivalent capability combination
- WHEN the integration test runs a container of `agent` with the capability set of `--privileged` on Debian 13 trixie
- THEN the container does not start, as on Ubuntu 26.04

Source: .ai/specs/2026-11-12-sandbox-distributions.md (Delta, MODIFIED; owner decision of 2026-11-13: `--privileged` and its equivalent are refused on every distribution, so the allowed scenario of the Delta is not archived)

## Requirement: Containers cannot change the boundary
The system SHALL keep the firewall and proxy configuration and the proxy process out of reach of every container of `agent`.

### Scenario: Machine namespaces are allowed
- WHEN `agent` runs `docker run --rm --pid host alpine true` and `docker run --rm --network host alpine true`
- THEN both containers start: each shares a namespace of the daemon of `agent` or of the machine, both inside the sandbox, not of the macOS host or the OrbStack VM, which R01 allows (Q13)

### Scenario: Host mounts and the machine pid namespace
- WHEN a container mounts `/etc` or `/` and writes `/etc/nftables.conf` or `/etc/tinyproxy/allowlist`, or runs with `--pid host` and kills the tinyproxy process
- THEN the containers start, each write and the kill fail, and the proxy keeps its pid

Frozen by: `cmd/orbit/docker_integration_test.go` (`TestInitGivesTheAgentRootlessDocker`), `internal/provision/docker_test.go`, `internal/provision/script_test.go`, `internal/sandbox/initialize/handler_test.go`

Source: .ai/specs/2026-11-11-rootless-docker.md
