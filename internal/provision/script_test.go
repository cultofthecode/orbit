package provision

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func script(t *testing.T) []string {
	t.Helper()
	got, err := Script(501)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	return strings.Split(string(got), "\n")
}

func TestScriptStopsAtTheFirstFailureAndRemovesTheMarkerFirst(t *testing.T) {
	lines := script(t)
	if lines[0] != "#!/bin/bash" || lines[1] != "set -euo pipefail" {
		t.Errorf("script starts with %q, want the shebang and set -euo pipefail", lines[:2])
	}
	removal := lineIndex(t, lines, "rm -f /etc/orbit/provisioned")
	install := lineIndex(t, lines, "apt-get update -q")
	if removal > install {
		t.Error("the marker is removed after the first change")
	}
}

func TestScriptInstallsExactlyTheToolchainAndInfrastructure(t *testing.T) {
	lines := script(t)
	i := lineIndex(t, lines, "apt-get install -y -q --no-install-recommends git openssh-client curl ca-certificates jq ripgrep unzip nftables tinyproxy")
	for _, line := range lines {
		if strings.Contains(line, "apt-get install") && line != lines[i] {
			t.Errorf("unexpected install: %q", line)
		}
	}
}

func TestScriptMasksSystemdResolved(t *testing.T) {
	lines := script(t)
	lineIndex(t, lines, "systemctl mask systemd-resolved.service >/dev/null")
	start := lineIndex(t, lines, "install -o root -g root -m 0644 /dev/stdin /etc/resolv.conf <<'ORBIT_EOF'")
	if lines[start+1] != "nameserver 0.250.250.200" || lines[start+2] != "ORBIT_EOF" {
		t.Errorf("resolv.conf = %q, want only OrbStack DNS", lines[start+1:start+3])
	}
}

func TestScriptLoadsTheRulesWithoutRestartingNftables(t *testing.T) {
	lines := script(t)
	lineIndex(t, lines, "nft -f /etc/nftables.conf")
	for _, line := range lines {
		if strings.Contains(line, "nftables") && (strings.Contains(line, "restart") || strings.Contains(line, "stop") || strings.Contains(line, "reload")) {
			t.Errorf("script stops or restarts nftables: %q", line)
		}
		if strings.Contains(line, "flush ruleset") {
			t.Errorf("script flushes the ruleset: %q", line)
		}
	}
}

func TestScriptWritesNothingIntoAHome(t *testing.T) {
	for _, line := range script(t) {
		if strings.Contains(line, "/home") && !strings.Contains(line, `/home/*)`) {
			t.Errorf("script touches /home: %q", line)
		}
		if strings.Contains(line, "~") {
			t.Errorf("script touches a home: %q", line)
		}
	}
}

func TestScriptCreatesTheAgentOnceWithoutGroups(t *testing.T) {
	lines := script(t)
	i := lineIndex(t, lines, "id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent")
	if strings.Contains(lines[i], "-G") || strings.Contains(lines[i], "--groups") {
		t.Errorf("agent gets extra groups: %q", lines[i])
	}
	lineIndex(t, lines, `[ "$(id -u agent)" -ge 1000 ] || fail "the uid of agent is below 1000"`)
	for _, line := range lines {
		if strings.Contains(line, "usermod") && strings.Contains(line, "agent") {
			t.Errorf("script changes the groups of agent: %q", line)
		}
	}
}

func TestScriptCreatesTheAgentAfterTheBoundaryStarts(t *testing.T) {
	lines := script(t)
	agent := lineIndex(t, lines, "id -u agent >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group agent")
	for _, step := range []string{"nft -f /etc/nftables.conf", "systemctl restart tinyproxy.service", "systemctl restart orbit-harden.service"} {
		if lineIndex(t, lines, step) > agent {
			t.Errorf("%q runs after the agent exists", step)
		}
	}
}

func TestScriptGrantsNoSudo(t *testing.T) {
	got, err := Script(501)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	for _, forbidden := range []string{"NOPASSWD", "/etc/sudoers", "visudo", "usermod -aG", "gpasswd -a"} {
		for _, line := range strings.Split(string(got), "\n") {
			if strings.Contains(line, forbidden) && !strings.Contains(line, "rm -f /etc/sudoers.d/orbstack") {
				t.Errorf("script has %q: %q", forbidden, line)
			}
		}
	}
}

func TestScriptSelfChecks(t *testing.T) {
	lines := script(t)
	for _, want := range []string{
		`nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`,
		`[ "$(systemctl is-enabled systemd-resolved.service 2>/dev/null || true)" = masked ] || fail "systemd-resolved is not masked"`,
		`  if [ "$uid" -lt 1000 ] && [ "$uid" -ne 501 ]; then`,
		`if runuser -u agent -- sudo -n true 2>/dev/null; then fail "agent can use sudo"; fi`,
		"for group in sudo admin adm docker lxd disk shadow; do",
	} {
		lineIndex(t, lines, want)
	}
}

func TestScriptWritesTheMarkerLast(t *testing.T) {
	lines := script(t)
	end := len(lines) - 1
	if lines[end] != "" {
		t.Fatalf("script does not end with a newline")
	}
	want := []string{
		"mkdir -p /etc/orbit",
		"install -o root -g root -m 0644 /dev/stdin /etc/orbit/provisioned <<'ORBIT_EOF'",
		"1",
		"ORBIT_EOF",
	}
	if got := lines[end-4 : end]; !slices.Equal(got, want) {
		t.Errorf("script ends with %q, want %q", got, want)
	}
}

func TestScriptWritesEveryFileFromAQuotedHeredoc(t *testing.T) {
	lines := script(t)
	files, err := scriptFiles(501)
	if err != nil {
		t.Fatalf("scriptFiles: %v", err)
	}
	for _, file := range files {
		lineIndex(t, lines, "install -o root -g root -m "+modeString(file)+" /dev/stdin "+file.Path+" <<'ORBIT_EOF'")
	}
	for _, line := range lines {
		if strings.Contains(line, "<<") && !strings.HasSuffix(line, "<<'ORBIT_EOF'") {
			t.Errorf("unquoted heredoc: %q", line)
		}
	}
}

func TestScriptCarriesTheDefaultUID(t *testing.T) {
	got, err := Script(502)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	if !strings.Contains(string(got), "meta skuid < 1000 meta skuid != 502 accept") {
		t.Error("the firewall of the script does not filter the default uid")
	}
}

func TestScriptRefusesDefaultUIDZero(t *testing.T) {
	got, err := Script(0)
	if !errors.Is(err, ErrScriptInput) || got != nil {
		t.Errorf("Script(0) = %q, %v, want nil, %v", got, err, ErrScriptInput)
	}
}

func TestScriptIsDeterministic(t *testing.T) {
	first, err := Script(501)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	second, err := Script(501)
	if err != nil {
		t.Fatalf("Script: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two calls gave different scripts")
	}
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
