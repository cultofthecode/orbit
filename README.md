# orbit

Orbit is a Go CLI that runs coding agents in OrbStack sandboxes on macOS.

See `AGENTS.md` for the project overview and `ARCHITECTURE.md` for the system structure.

## SSH agent forwarding

`orbit init` forwards the SSH agent of your Mac into the sandbox by default, so the agent can use Git over SSH. The sandbox can then use every key loaded in that agent, not only the one for the project. Pass `--no-ssh-forwarding` to `orbit init` to leave the agent out. The Credentials row of `ARCHITECTURE.md` has the details and the recommendations for the host.
