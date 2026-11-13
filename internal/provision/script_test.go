package provision

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/policy"
)

// dockerPackageList is the package list of the rootless engine per
// distribution, written out so that a change to a profile shows in a test.
var dockerPackageList = map[string]string{
	"ubuntu": "docker.io docker-buildx docker-compose-v2 rootlesskit uidmap slirp4netns dbus-user-session",
	"debian": "docker.io docker-cli docker-buildx docker-compose rootlesskit uidmap slirp4netns dbus-user-session",
}

// eachDistribution runs test as a subtest for every distribution that Script
// renders, so that every structural test holds for all of them.
func eachDistribution(t *testing.T, test func(t *testing.T, distribution string)) {
	t.Helper()
	for _, distribution := range Distributions() {
		t.Run(distribution, func(t *testing.T) { test(t, distribution) })
	}
}

func script(t *testing.T, distribution string) []string {
	t.Helper()
	got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: true})
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	return strings.Split(string(got), "\n")
}

func TestScriptStopsAtTheFirstFailureAndRemovesTheMarkerFirst(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		if !slices.Equal(lines[:3], []string{"#!/bin/bash", "{", "set -euo pipefail"}) {
			t.Errorf("script starts with %q, want the shebang, a group, and set -euo pipefail", lines[:3])
		}
		unset := lineIndex(t, lines, "unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy")
		removal := lineIndex(t, lines, "rm -f /etc/orbit/provisioned")
		install := lineIndex(t, lines, "apt-get -o DPkg::Lock::Timeout=120 update -q </dev/null")
		if removal > install {
			t.Error("the marker is removed after the first change")
		}
		if unset > install {
			t.Error("apt-get runs with the proxy settings of /etc/environment")
		}
	})
}

func TestScriptInstallsExactlyTheToolchainAndInfrastructure(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		i := lineIndex(t, lines, "apt-get -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends git openssh-client curl ca-certificates jq ripgrep unzip nftables tinyproxy netcat-openbsd </dev/null")
		j := lineIndex(t, lines, "apt-get -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends "+dockerPackageList[distribution]+" </dev/null")
		for _, line := range lines {
			if strings.HasPrefix(line, "apt-get") && strings.Contains(line, " install") && line != lines[i] && line != lines[j] {
				t.Errorf("unexpected install: %q", line)
			}
		}
	})
}

func TestScriptMasksSystemdResolved(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		lineIndex(t, lines, "systemctl mask systemd-resolved.service systemd-resolved-varlink.socket systemd-resolved-monitor.socket >/dev/null 2>&1")
		start := lineIndex(t, lines, "put /etc/resolv.conf 0644 <<'ORBIT_EOF'")
		if lines[start+1] != "nameserver 0.250.250.200" || lines[start+2] != "ORBIT_EOF" {
			t.Errorf("resolv.conf = %q, want only OrbStack DNS", lines[start+1:start+3])
		}
	})
}

func TestScriptLoadsTheRulesWithoutRestartingNftables(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		lineIndex(t, lines, "nft -f /etc/nftables.conf")
		for _, line := range lines {
			if strings.Contains(line, "nftables") && (strings.Contains(line, "restart") || strings.Contains(line, "stop") || strings.Contains(line, "reload")) {
				t.Errorf("script stops or restarts nftables: %q", line)
			}
			if strings.Contains(line, "flush ruleset") {
				t.Errorf("script flushes the ruleset: %q", line)
			}
		}
	})
}

func TestScriptWritesNothingIntoAHome(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, line := range script(t, distribution) {
			if strings.Contains(line, "/home") && !strings.Contains(line, `/home/*)`) {
				t.Errorf("script touches /home: %q", line)
			}
			if strings.Contains(line, "~") {
				t.Errorf("script touches a home: %q", line)
			}
		}
	})
}

func scriptFor(t *testing.T, distribution string, forward bool) []string {
	t.Helper()
	got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: forward})
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	return strings.Split(string(got), "\n")
}

func usermodLines(lines []string) []string {
	var found []string
	for _, line := range lines {
		if strings.Contains(line, "usermod") || strings.Contains(line, "gpasswd -a") || strings.Contains(line, "adduser") {
			found = append(found, line)
		}
	}
	return found
}

func TestScriptCreatesTheAgentOnceWithOnlyTheSocketGroup(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, forward := range []bool{true, false} {
			lines := scriptFor(t, distribution, forward)
			i := lineIndex(t, lines, "id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent")
			if strings.Contains(lines[i], "-G") || strings.Contains(lines[i], "--groups") {
				t.Errorf("forward %t: agent gets extra groups at creation: %q", forward, lines[i])
			}
			lineIndex(t, lines, `[ "$(id -u agent)" -ge 1000 ] || fail "the uid of agent is below 1000"`)
			want := []string(nil)
			if forward {
				want = []string{"usermod -aG orbstack agent"}
			}
			if got := usermodLines(lines); !slices.Equal(got, want) {
				t.Errorf("forward %t: group changes = %q, want %q", forward, got, want)
			}
		}
	})
}

func TestScriptChecksTheSocketGroupInBothDirections(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		member := `case " $(id -nG agent) " in *" orbstack "*)`
		with := strings.Join(scriptFor(t, distribution, true), "\n")
		without := strings.Join(scriptFor(t, distribution, false), "\n")
		if !strings.Contains(with, member+`;; *) fail "agent is not in the group orbstack";; esac`) {
			t.Error("the script that forwards does not fail when agent is not in orbstack")
		}
		if !strings.Contains(without, member+` fail "agent is in the group orbstack";; esac`) {
			t.Error("the script that does not forward does not fail when agent is in orbstack")
		}
		if strings.Contains(with, "agent is in the group orbstack") || strings.Contains(without, "agent is not in the group orbstack") {
			t.Error("a script checks the membership in the wrong direction")
		}
	})
}

func TestScriptChecksTheSSHRoute(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		for _, want := range []string{
			`[ -f /etc/ssh/ssh_config.d/00-orbit.conf ] || fail "/etc/ssh/ssh_config.d/00-orbit.conf is missing"`,
			`[ -f /etc/ssh/ssh_known_hosts ] || fail "/etc/ssh/ssh_known_hosts is missing"`,
			"owner=$(dpkg -S /usr/bin/nc.openbsd 2>/dev/null || true)",
			`[ "$owner" = "netcat-openbsd: /usr/bin/nc.openbsd" ] || fail "/usr/bin/nc.openbsd does not belong to netcat-openbsd"`,
		} {
			lineIndex(t, lines, want)
		}
	})
}

func TestScriptWritesTheSSHRouteInEverySandbox(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, forward := range []bool{true, false} {
			lines := scriptFor(t, distribution, forward)
			lineIndex(t, lines, "put /etc/ssh/ssh_config.d/00-orbit.conf 0644 <<'ORBIT_EOF'")
			lineIndex(t, lines, "put /etc/ssh/ssh_known_hosts 0644 <<'ORBIT_EOF'")
		}
	})
}

func TestSSHConfigMatchesGolden(t *testing.T) {
	compareGolden(t, "00-orbit.conf", sshConfig())
}

func TestKnownHostsMatchesGolden(t *testing.T) {
	compareGolden(t, "ssh_known_hosts", knownHosts())
}

func TestPinnedHostKeysMatchTheFingerprintsOfGitHub(t *testing.T) {
	published := map[string]string{
		"ssh-ed25519":         "+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU",
		"ecdsa-sha2-nistp256": "p2QAMXNIC1TJYWeIOttrVc98/R1BUFWu3/LiyKgUfQM",
		"ssh-rsa":             "uNiVztksCsDhcc0u9e8BujQXVUpKZIDTMczCvj3tD2s",
	}
	if len(gitHubHostKeys) != len(published) {
		t.Fatalf("%d pinned keys, want %d", len(gitHubHostKeys), len(published))
	}
	for _, key := range gitHubHostKeys {
		fields := strings.Fields(key)
		if len(fields) != 2 {
			t.Fatalf("pinned key %q is not a type and a key", key)
		}
		blob, err := base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			t.Fatalf("pinned %s key: %v", fields[0], err)
		}
		sum := sha256.Sum256(blob)
		if got := base64.RawStdEncoding.EncodeToString(sum[:]); got != published[fields[0]] {
			t.Errorf("fingerprint of the pinned %s key = %s, want %s from api.github.com/meta", fields[0], got, published[fields[0]])
		}
	}
}

func TestSSHConfigRoutesGitHubThroughTheProxyAndPinsTheKeys(t *testing.T) {
	config := sshConfig()
	for _, want := range []string{
		"Host github.com ssh.github.com\n",
		"    HostName ssh.github.com\n",
		"    Port 443\n",
		"    HostKeyAlias github.com\n",
		"    StrictHostKeyChecking yes\n",
		"    ProxyCommand /usr/bin/nc.openbsd -X connect -x 127.0.0.1:3128 %h %p\n",
		"Match localuser agent\n    IdentityAgent SSH_AUTH_SOCK\n",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("00-orbit.conf lacks %q", want)
		}
	}
	for _, line := range strings.Split(knownHosts(), "\n") {
		if line != "" && !strings.HasPrefix(line, "github.com ssh-") && !strings.HasPrefix(line, "github.com ecdsa-") {
			t.Errorf("ssh_known_hosts holds %q, want only keys of github.com", line)
		}
	}
}

func TestScriptCreatesTheAgentAfterTheBoundaryStarts(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		agent := lineIndex(t, lines, "id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent")
		for _, step := range []string{"nft -f /etc/nftables.conf", "systemctl restart tinyproxy.service", "systemctl restart orbit-harden.service"} {
			if lineIndex(t, lines, step) > agent {
				t.Errorf("%q runs after the agent exists", step)
			}
		}
	})
}

func TestScriptStartsUserSessionsAfterTheRestarts(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		start := lineIndex(t, lines, "systemctl start systemd-user-sessions.service")
		for _, step := range []string{"systemctl restart tinyproxy.service", "systemctl restart orbit-harden.service"} {
			if lineIndex(t, lines, step) > start {
				t.Errorf("%q runs after the start of systemd-user-sessions.service", step)
			}
		}
	})
}

func TestScriptGrantsNoSudo(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501})
		if err != nil {
			t.Fatalf("Script: %v", err)
		}
		for _, forbidden := range []string{"NOPASSWD", "/etc/sudoers", "visudo", "usermod -aG", "gpasswd -a"} {
			for _, line := range strings.Split(string(got), "\n") {
				if strings.Contains(line, forbidden) && !strings.Contains(line, "find /etc/sudoers.d -mindepth 1 ! -name README -delete") {
					t.Errorf("script has %q: %q", forbidden, line)
				}
			}
		}
	})
}

func TestScriptSelfChecks(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		for _, want := range []string{
			`nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`,
			"for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do",
			`[ ! -e /run/nologin ] || fail "logins are disabled"`,
			"for unit in systemd-resolved.service systemd-resolved-varlink.socket systemd-resolved-monitor.socket; do",
			`  if [ "$uid" -lt 1000 ] && [ "$uid" -ne 501 ]; then`,
			`if runuser -u agent -- sudo -n true 2>/dev/null; then fail "agent can use sudo"; fi`,
			"for group in sudo admin adm docker lxd disk shadow; do",
			`  case "$rules" in *"may run"*) fail "$name has a sudo rule";; esac`,
		} {
			lineIndex(t, lines, want)
		}
	})
}

func TestScriptPutsFilesThroughARename(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		lineIndex(t, lines, `put() { cat > "$1.orbit-new"; chown root:root "$1.orbit-new"; chmod "$2" "$1.orbit-new"; mv -f "$1.orbit-new" "$1"; }`)
		for _, line := range lines {
			if strings.Contains(line, "/dev/stdin") {
				t.Errorf("script copies standard input with install: %q", line)
			}
		}
	})
}

func TestScriptWritesTheMarkerLast(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		end := len(lines) - 1
		if lines[end] != "" {
			t.Fatalf("script does not end with a newline")
		}
		want := []string{
			"mkdir -p /etc/orbit",
			"put /etc/orbit/provisioned 0644 <<'ORBIT_EOF'",
			"3",
			"ORBIT_EOF",
			"}",
		}
		if got := lines[end-5 : end]; !slices.Equal(got, want) {
			t.Errorf("script ends with %q, want %q", got, want)
		}
	})
}

func TestScriptIsOneGroupThatChildrenCannotTruncate(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		opens := 0
		for _, line := range lines {
			if line == "{" {
				opens++
			}
		}
		if opens != 1 || lines[1] != "{" || lines[len(lines)-2] != "}" {
			t.Errorf("script has %d group openings and ends with %q, want one group around the body", opens, lines[len(lines)-2])
		}
		for _, line := range lines {
			if strings.HasPrefix(line, "apt-get") && !strings.HasSuffix(line, "</dev/null") {
				t.Errorf("apt-get can read the script from standard input: %q", line)
			}
		}
	})
}

func TestScriptNegativeChecksDoNotPipeIntoGrep(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, line := range script(t, distribution) {
			if strings.Contains(line, "| grep -q") {
				t.Errorf("a pipe into grep -q passes silently on SIGPIPE under pipefail: %q", line)
			}
		}
	})
}

func TestScriptWritesEveryFileFromAQuotedHeredoc(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		files, err := scriptFiles(501, nil)
		if err != nil {
			t.Fatalf("scriptFiles: %v", err)
		}
		for _, file := range files {
			lineIndex(t, lines, "put "+file.Path+" "+modeString(file)+" <<'ORBIT_EOF'")
		}
		for _, line := range lines {
			if strings.Contains(line, "<<") && !strings.HasSuffix(line, "<<'ORBIT_EOF'") {
				t.Errorf("unquoted heredoc: %q", line)
			}
		}
	})
}

func TestScriptCarriesTheDefaultUID(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 502})
		if err != nil {
			t.Fatalf("Script: %v", err)
		}
		if !strings.Contains(string(got), "meta skuid < 1000 meta skuid != 502 accept") {
			t.Error("the firewall of the script does not filter the default uid")
		}
	})
}

func TestScriptRefusesDefaultUIDZero(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		got, err := Script(ScriptInput{Distribution: distribution})
		if !errors.Is(err, ErrScriptInput) || got != nil {
			t.Errorf("Script(ScriptInput{Distribution: distribution, }) = %q, %v, want nil, %v", got, err, ErrScriptInput)
		}
	})
}

func TestScriptAllowsTheAgentDomainsOnlyWithAnAgent(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		withAgent, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, AgentDomains: []string{"claude.ai", "api.anthropic.com"}})
		if err != nil {
			t.Fatalf("Script with agent domains: %v", err)
		}
		withoutAgent, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: true})
		if err != nil {
			t.Fatalf("Script without agent domains: %v", err)
		}
		for _, rule := range []string{`^claude\.ai$`, `^api\.anthropic\.com$`} {
			if !slices.Contains(strings.Split(string(withAgent), "\n"), rule) {
				t.Errorf("the script with agent domains lacks the allow rule %s", rule)
			}
		}
		for _, host := range []string{"claude", "anthropic"} {
			if strings.Contains(string(withoutAgent), host) {
				t.Errorf("the script without agent domains names %q", host)
			}
		}
		if !slices.Contains(strings.Split(string(withoutAgent), "\n"), `^github\.com$`) {
			t.Error("the script without agent domains lacks the base list")
		}
	})
}

func TestScriptRefusesAHostileAgentDomain(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, domain := range []string{"*.anthropic.com", "1.2.3.4", "localhost", "Claude.AI", "example.com:22", "claude.ai\n^.*$"} {
			got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, AgentDomains: []string{"claude.ai", domain}})
			if !errors.Is(err, policy.ErrInvalidHost) || got != nil {
				t.Errorf("Script with %q = %q, %v, want nil, %v", domain, got, err, policy.ErrInvalidHost)
			}
		}
	})
}

func TestScriptIsDeterministic(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		first, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: true})
		if err != nil {
			t.Fatalf("Script: %v", err)
		}
		second, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: true})
		if err != nil {
			t.Fatalf("Script: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Error("two calls gave different scripts")
		}
	})
}

func TestCheckDelimiterRefusesAnEarlyEnd(t *testing.T) {
	hostile := []File{{Path: "/etc/x", Mode: 0o644, Content: "a\nORBIT_EOF\nrm -rf /\n"}}
	if err := checkDelimiter(hostile); !errors.Is(err, ErrScriptInput) {
		t.Errorf("checkDelimiter = %v, want %v", err, ErrScriptInput)
	}
	inline := []File{{Path: "/etc/x", Mode: 0o644, Content: "a ORBIT_EOF b\n"}}
	if err := checkDelimiter(inline); err != nil {
		t.Errorf("checkDelimiter refused a delimiter inside a line: %v", err)
	}
}

func TestEnvironmentPointsAtTheProxy(t *testing.T) {
	content := environment()
	for _, want := range []string{"HTTPS_PROXY=http://127.0.0.1:3128\n", "https_proxy=http://127.0.0.1:3128\n", "NO_PROXY=localhost,127.0.0.1\n", "PATH="} {
		if !strings.Contains(content, want) {
			t.Errorf("environment lacks %q", want)
		}
	}
}

func lineIndex(t *testing.T, lines []string, want string) int {
	t.Helper()
	i := slices.Index(lines, want)
	if i < 0 {
		t.Fatalf("script lacks the line %q", want)
	}
	return i
}

func modeString(file File) string {
	return map[bool]string{true: "0755", false: "0644"}[file.Mode == 0o755]
}

// changesTheAgentUser reports whether line removes the agent user or alters
// it with usermod, except the one change that adds the agent to the group
// orbstack: the login of the agent lives in its home, which provisioning never
// removes or recreates.
func changesTheAgentUser(line string) bool {
	if strings.Contains(line, "userdel") || strings.Contains(line, "deluser") {
		return true
	}
	if strings.TrimSpace(line) == "usermod -aG orbstack agent" {
		return false
	}
	return strings.Contains(line, "usermod") && strings.Contains(line, "agent")
}

func TestChangesTheAgentUserRefusesHostileLines(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"userdel agent", true},
		{"userdel --remove agent", true},
		{"deluser agent", true},
		{"deluser --remove-home agent", true},
		{"usermod -d /x agent", true},
		{"usermod -aG sudo agent", true},
		{"usermod -aG orbstack,sudo agent", true},
		{"usermod -aG orbstack agent", false},
		{"id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent", false},
		{`[ "$(id -u agent)" -ge 1000 ] || fail "the uid of agent is below 1000"`, false},
	}
	for _, c := range cases {
		if got := changesTheAgentUser(c.line); got != c.want {
			t.Errorf("changesTheAgentUser(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestScriptNeverRemovesOrAltersTheAgentUser(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, forward := range []bool{true, false} {
			for _, line := range scriptFor(t, distribution, forward) {
				if changesTheAgentUser(line) {
					t.Errorf("forward %t: script changes or removes the agent user: %q", forward, line)
				}
			}
		}
	})
}
