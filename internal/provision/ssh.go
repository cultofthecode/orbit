package provision

import (
	"fmt"

	"github.com/cultofthecode/orbit/internal/proxy"
)

// sshConfigPath is the SSH client configuration of Orbit. It sorts before
// 10-orbstack.conf, and ssh takes the first value that it reads for a
// keyword, so these settings win over those of OrbStack.
const sshConfigPath = "/etc/ssh/ssh_config.d/00-orbit.conf"

// knownHostsPath holds the pinned host keys of GitHub, owned by root.
const knownHostsPath = "/etc/ssh/ssh_known_hosts"

// netcatPackage owns netcatPath, the variant of nc with -X connect.
const netcatPackage = "netcat-openbsd"

// netcatPath is the program that ProxyCommand runs, by its absolute path:
// /usr/bin/nc is an alternatives link that no package owns.
const netcatPath = "/usr/bin/nc.openbsd"

// gitHubSSHHost is the name under which the host keys are pinned. The
// configuration maps github.com and ssh.github.com to it with
// HostKeyAlias.
const gitHubSSHHost = "github.com"

// gitHubHostKeys are the SSH host keys that GitHub publishes (the ssh_keys
// of https://api.github.com/meta), Ed25519, ECDSA, then RSA. A test checks
// their fingerprints against the ones that GitHub publishes.
var gitHubHostKeys = []string{
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
	"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
	"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=",
}

// sshFiles returns the SSH client configuration and the pinned host keys,
// which every sandbox gets, with or without the forwarded agent.
func sshFiles() []File {
	return []File{
		{Path: sshConfigPath, Mode: 0o644, Content: sshConfig()},
		{Path: knownHostsPath, Mode: 0o644, Content: knownHosts()},
	}
}

// sshConfig returns 00-orbit.conf. Git over SSH to GitHub goes to
// ssh.github.com:443 through the egress proxy, the host key is checked
// against the pinned keys and never asked for, and the agent user finds the
// forwarded socket through SSH_AUTH_SOCK where OrbStack points every other
// user at a socket that only the default user can open.
func sshConfig() string {
	proxyCommand := fmt.Sprintf("%s -X connect -x %s:%d %%h %%p", netcatPath, proxy.LoopbackAddr, proxy.Port)
	return lines(
		"Host github.com ssh.github.com",
		"    HostName ssh.github.com",
		"    Port 443",
		"    HostKeyAlias "+gitHubSSHHost,
		"    StrictHostKeyChecking yes",
		"    ProxyCommand "+proxyCommand,
		"",
		"Match localuser "+AgentUser,
		"    IdentityAgent SSH_AUTH_SOCK",
	)
}

// knownHosts returns ssh_known_hosts with the host keys of GitHub.
func knownHosts() string {
	entries := make([]string, 0, len(gitHubHostKeys))
	for _, key := range gitHubHostKeys {
		entries = append(entries, gitHubSSHHost+" "+key)
	}
	return lines(entries...)
}
