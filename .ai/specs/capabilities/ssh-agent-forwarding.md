# Capability: ssh-agent-forwarding

The agent can use the host SSH agent as a socket, by default, and `--no-ssh-forwarding` leaves it out. Git over SSH reaches GitHub through the egress proxy and nowhere else.

## Requirement: Forward the host SSH agent by default
The system SHALL create the machine of `orbit init` with OrbStack SSH agent forwarding when `--no-ssh-forwarding` is not passed, and SHALL let the user `agent` use the forwarded socket.

### Scenario: Init without the flag
- WHEN `orbit init` creates a sandbox without `--no-ssh-forwarding`, and `SSH_AUTH_SOCK` on the Mac names a running agent (the integration test skips this check, and says so, when it does not)
- THEN `orb list` reports `forward_ssh_agent: true` for the machine, and `ssh-add -l` run as `agent` connects to the socket (it lists identities or reports that the agent has none)

## Requirement: Leave the SSH agent out on request
The system SHALL create the machine without SSH agent forwarding when `--no-ssh-forwarding` is passed, and the user `agent` SHALL NOT be in the group `orbstack`.

### Scenario: Init with the flag
- WHEN `orbit init --no-ssh-forwarding` creates a sandbox
- THEN no file exists at `/opt/orbstack-guest/run/host-ssh-agent.sock`, `SSH_AUTH_SOCK` is unset for `agent`, and `ssh-add -l` as `agent` fails

## Requirement: Forwarding is fixed at creation
The system SHALL refuse `init` on an existing machine whose forwarding setting differs from the request, before it provisions the machine, and SHALL NOT change or delete that machine.

### Scenario: Flag differs from the machine
- WHEN `init` runs with `--no-ssh-forwarding` in a project directory whose machine forwards the agent, or without it where the machine does not
- THEN `init` fails with `ErrSSHForwardingMismatch` before it runs a command inside the machine, the message names `--no-ssh-forwarding` and how to recreate the sandbox, and the machine is neither started, provisioned, nor deleted

### Scenario: Created machine reports another setting
- WHEN the machine that this call created reports a forwarding setting other than the requested one
- THEN `init` deletes that machine and fails with `ErrSSHForwardingMismatch`

## Requirement: Git over SSH to GitHub through the proxy
The system SHALL route SSH from `agent` to `github.com` over `ssh.github.com:443` through the egress proxy, with GitHub's pinned host keys, and SHALL leave port 22 closed.

### Scenario: Push path
- WHEN `agent` runs `ssh -T git@github.com` in a sandbox that forwards the agent
- THEN the connection reaches GitHub without a host-key prompt (`Permission denied (publickey)` with no key, `successfully authenticated` with one)

### Scenario: Authenticated push (manual, QA)
- WHEN the Mac's SSH agent holds a key with push access and `agent` runs `git push` to a `git@github.com:` remote
- THEN the push succeeds

## Requirement: No other SSH destination
The system SHALL block SSH from `agent` to any host and port without an allow rule, also with the forwarded agent.

### Scenario: Host without an allow rule
- WHEN `agent` runs `ssh -o ConnectTimeout=5 git@gitlab.com`, `ssh -p 22 git@ssh.github.com`, or `ssh` with `ProxyCommand` through the proxy to `altssh.gitlab.com:443`
- THEN each connection fails

## Requirement: No host key material in the sandbox
The system SHALL NOT place a private key file or any content of the host `~/.ssh` in a sandbox.

### Scenario: Search for keys
- WHEN the sandbox forwards the agent
- THEN `/home/agent/.ssh` is absent or empty right after `init`, the only host mount is the project directory, no file under `/home` (outside the project directory) or `/root` holds private-key content (`PRIVATE KEY-----`), and `/etc/ssh` holds none besides the machine's own `ssh_host_*_key` files, if the image has them

Frozen by: `cmd/orbit/sshforwarding_integration_test.go` (`TestInitForwardsTheSSHAgentByDefaultAndRefusesTheOtherSetting`, `TestInitLeavesTheSSHAgentOutOnRequest`), `internal/sandbox/initialize/handler_test.go`, `internal/provision/script_test.go`
