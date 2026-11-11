# Capability: agent-sign-in

The owner signs in to the unmodified client with `/login` inside the sandbox, once. The login stays on the disk of the sandbox, and Orbit never touches it.

## Requirement: Sign in with the subscription inside the sandbox
The system SHALL let the owner sign in to the unmodified client with `/login` inside the sandbox, through the egress allowlist, and SHALL NOT need or set an API key.

### Scenario: Sign-in starts (agent-run QA)
- WHEN an agent runs `claude` in `orbit shell` and starts `/login` with the subscription sign-in
- THEN the client prints the sign-in URL and waits for the code, and the proxy log shows no refused host up to that point

### Scenario: First sign-in (owner, not part of the A02 check; Q11)
- WHEN the owner runs `claude` in `orbit shell`, runs `/login`, opens the printed URL in a browser on the Mac, and pastes the code back
- THEN the client reports a successful login on the owner's subscription (`/status`), and the proxy log shows no refused host that the session needed

### Scenario: No key, no login yet
- WHEN `init --agent claude-code` created the sandbox and no one signed in
- THEN `claude -p hi` as `agent` reports that it is not logged in and names `/login`

Frozen by: `cmd/orbit/signin_integration_test.go` (No key, no login yet)

## Requirement: The login stays on the disk of the sandbox
The system SHALL keep the home directory of the agent, and so the login, across `orbit stop`, `orbit start`, and a repeated `orbit init`, and SHALL NOT remove or recreate the agent user or its home.

### Scenario: Stop, start, and init again
- WHEN a file exists in `/home/agent/.claude` and `/home/agent/.claude.json` exists, and the owner runs `orbit init --agent claude-code` again, then `orbit stop` and `orbit start`
- THEN the file exists with the same content, and `~/.claude.json` has the same checksum, after each command

### Scenario: Signed in after a restart (owner, not part of the A02 check; Q11)
- WHEN the owner signed in, and runs `orbit stop`, `orbit start`, and `orbit shell`
- THEN `claude` starts signed in, without `/login`

Frozen by: `cmd/orbit/signin_integration_test.go`

## Requirement: No host credential of the agent enters the sandbox
The system SHALL NOT mount host `~/.claude` or `~/.claude.json`, and SHALL NOT pass a host variable of the agent's provider into the sandbox.

### Scenario: Host variables
- WHEN the environment of `orbit` holds `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, and `CLAUDE_CONFIG_DIR`, and `ORBENV` and `WSLENV` name them
- THEN none of them is set in a script that runs as `agent`, nor in `orbit shell` (QA)

### Scenario: Host directory
- WHEN a sandbox exists
- THEN `<host home>/.claude` and `<host home>/.claude.json` do not exist in the machine, and the machine mounts only the project directory

Frozen by: `cmd/orbit/signin_integration_test.go`

## Requirement: Orbit holds no copy of the credential
The system SHALL NOT read, copy, store, log, or forward the credential of the agent, and the Orbit process SHALL send no request to the provider.

### Scenario: Source guard
- WHEN a non-test Go file of the module names the credential file, the Keychain entry or tool, or a provider credential variable
- THEN `go test ./...` fails

### Scenario: Keychain (agent-run QA)
- WHEN the agent-run QA check of the spec ran
- THEN the item labels of `security dump-keychain` are the same as before the run
- AND the transcript records only the result of the comparison, no account name, service name, or label

Frozen by: `cmd/orbit/credential_guard_test.go`

Source: .ai/specs/2026-11-11-subscription-sign-in.md
