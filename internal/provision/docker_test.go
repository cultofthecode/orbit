package provision

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// provisioningChanges names one change of provisioning per Version. A change
// that bumps Version appends its name; a merge keeps every line.
var provisioningChanges = []string{"base", "ssh-agent-forwarding", "rootless-docker", "firewall-survives-stop"}

func TestVersionCountsTheProvisioningChanges(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		if Version != len(provisioningChanges) {
			t.Fatalf("Version = %d, want %d, one per change in provisioningChanges", Version, len(provisioningChanges))
		}
		body := strings.Join(script(t, distribution), "\n")
		for _, want := range []string{"docker.io", "put /etc/systemd/user/docker.service 0644"} {
			if !strings.Contains(body, want) {
				t.Errorf("the script lacks %q", want)
			}
		}
	})
}

func TestDockerUserUnit(t *testing.T) {
	file := unitFile(t, "/etc/systemd/user/docker.service")
	for key, value := range map[string]string{
		"ConditionUser": "agent", "Delegate": "yes", "Type": "notify", "NotifyAccess": "all",
		"KillMode": "mixed", "Restart": "always",
		"ExecStart":   "/usr/share/docker.io/contrib/dockerd-rootless.sh",
		"Environment": "HTTPS_PROXY=http://172.31.255.1:3128",
	} {
		if !hasDirective(file, key, value) {
			t.Errorf("docker.service lacks %s=%s:\n%s", key, value, file.Content)
		}
	}
	for _, want := range []string{"Environment=HTTP_PROXY=http://172.31.255.1:3128\n", "WantedBy=default.target\n"} {
		if !strings.Contains(file.Content, want) {
			t.Errorf("docker.service lacks %q", want)
		}
	}
	if strings.Contains(file.Content, "127.0.0.1:3128") || strings.Contains(file.Content, "localhost:3128") {
		t.Error("the daemon reaches the proxy over loopback, which slirp4netns hides")
	}
}

func TestBoundaryGateIsOrderedAfterExactlyTheBoundaryUnitsAndRequiresNone(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/orbit-boundary-gate.service")
	after := directiveValues(file, "After")
	want := []string{"nftables.service", "orbit-proxy-address.service", "tinyproxy.service", "orbit-harden.service"}
	if !slices.Equal(slices.Sorted(slices.Values(after)), slices.Sorted(slices.Values(want))) {
		t.Errorf("gate After = %q, want %q", after, want)
	}
	for _, key := range []string{"Requires", "Wants", "BindsTo", "PartOf", "Before"} {
		if v := directiveValues(file, key); len(v) != 0 {
			t.Errorf("gate has %s=%q", key, v)
		}
	}
	for key, value := range map[string]string{"Type": "oneshot", "RemainAfterExit": "yes", "WantedBy": "multi-user.target"} {
		if !hasDirective(file, key, value) {
			t.Errorf("gate lacks %s=%s", key, value)
		}
	}
}

func TestBoundaryGateScriptRunsTheUnitsCheckWithoutWaitingForTheBoot(t *testing.T) {
	file := unitFile(t, "/usr/local/libexec/orbit-boundary-gate")
	if file.Mode != 0o755 {
		t.Errorf("mode = %o, want 755", file.Mode)
	}
	if !strings.Contains(file.Content, strings.Join(boundaryUnitsCheck(), "\n")) {
		t.Errorf("the gate script does not run boundaryUnitsCheck:\n%s", file.Content)
	}
	for _, forbidden := range []string{"systemd-user-sessions", "is-system-running"} {
		if strings.Contains(file.Content, forbidden) {
			t.Errorf("the gate script names %q", forbidden)
		}
	}
	if !strings.HasPrefix(file.Content, "#!/bin/bash\nset -euo pipefail\n") {
		t.Errorf("the gate script does not stop at the first failure:\n%s", file.Content)
	}
}

func TestBoundaryCheckKeepsItsLinesAndAddsOnlyUserSessionsToTheUnitsCheck(t *testing.T) {
	want := "for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do"
	if !slices.Contains(boundaryCheck(), want) {
		t.Errorf("boundaryCheck lacks %q", want)
	}
	gate := strings.Join(boundaryUnitsCheck(), "\n")
	if strings.Contains(gate, "systemd-user-sessions") || !strings.Contains(gate, "inet orbit_egress") {
		t.Errorf("boundaryUnitsCheck = %q", gate)
	}
}

func TestUserManagerDropInRequiresTheGateAndNothingElse(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/user@.service.d/orbit.conf")
	for _, key := range []string{"Requires", "After"} {
		if got := directiveValues(file, key); !slices.Equal(got, []string{"orbit-boundary-gate.service"}) {
			t.Errorf("user@.service %s = %q, want only the gate", key, got)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(file.Content), "\n") {
		if line != "[Unit]" && !strings.HasPrefix(line, "Requires=") && !strings.HasPrefix(line, "After=") {
			t.Errorf("unexpected line %q", line)
		}
	}
}

func TestScriptGuardsPackageInstallWithPolicyRCAndRemovesItOnEveryPath(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		trap := lineIndex(t, lines, "trap 'rm -f /usr/sbin/policy-rc.d' EXIT")
		guard := lineIndex(t, lines, `printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d`)
		install := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "install -y -q") && strings.Contains(l, "docker.io") })
		removal := lineIndex(t, lines, "rm -f /usr/sbin/policy-rc.d")
		if !slices.IsSorted([]int{trap, guard, install, removal}) {
			t.Errorf("order trap %d, guard %d, install %d, removal %d", trap, guard, install, removal)
		}
	})
}

func TestScriptMasksTheRootfulEngineBeforeAnyStart(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		mask := lineIndex(t, lines, "systemctl mask docker.service docker.socket containerd.service >/dev/null 2>&1")
		lineIndex(t, lines, "systemctl disable --now docker.service docker.socket containerd.service 2>/dev/null || true")
		if start := lineIndex(t, lines, "systemctl start orbit-boundary-gate.service"); mask > start {
			t.Error("the rootful units are masked after the first start")
		}
		for _, line := range lines {
			if strings.Contains(line, "usermod") && strings.Contains(line, "docker") {
				t.Errorf("agent joins the group docker: %q", line)
			}
			if strings.Contains(line, "/home") && !strings.Contains(line, `case "$home"`) {
				t.Errorf("script writes under /home: %q", line)
			}
		}
	})
}

func TestScriptRepairsAndStartsInOrder(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		enable := lineIndex(t, lines, "systemctl --global enable docker.service >/dev/null 2>&1")
		gate := lineIndex(t, lines, "systemctl start orbit-boundary-gate.service")
		linger := lineIndex(t, lines, "loginctl enable-linger agent")
		manager := lineIndex(t, lines, `systemctl start "user@$agent_uid.service"`)
		reload := lineIndex(t, lines, "runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user daemon-reload")
		daemon := lineIndex(t, lines, "runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user start docker.service")
		if !slices.IsSorted([]int{enable, gate, linger, manager, reload, daemon}) {
			t.Errorf("order enable %d, gate %d, linger %d, manager %d, reload %d, daemon %d", enable, gate, linger, manager, reload, daemon)
		}
	})
}

func TestScriptNeverRestartsStopsOrReloadsTheGate(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, line := range script(t, distribution) {
			if !strings.Contains(line, "orbit-boundary-gate") {
				continue
			}
			for _, verb := range []string{"restart", "stop", "try-restart", "reload", "reload-or-restart", "kill"} {
				if strings.Contains(line, "systemctl "+verb+" ") || strings.Contains(line, " "+verb+" ") && strings.Contains(line, "systemctl") {
					t.Errorf("the script touches the gate with %s: %q", verb, line)
				}
			}
		}
	})
}

func TestScriptRunsUserSystemctlOnlyThroughRunuserWithoutALoginShell(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		for _, line := range script(t, distribution) {
			if strings.Contains(line, "systemctl --user") && !strings.HasPrefix(line, "runuser -u agent -- env XDG_RUNTIME_DIR=") && !strings.Contains(line, "|| fail") {
				t.Errorf("systemctl --user outside runuser: %q", line)
			}
			if strings.Contains(line, "runuser -l") || strings.Contains(line, "su -") || strings.HasPrefix(line, "su ") {
				t.Errorf("login shell: %q", line)
			}
		}
	})
}

func TestScriptSelfChecksTheEngine(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		for _, want := range []string{
			"for unit in docker.service docker.socket containerd.service; do",
			`[ ! -e /var/run/docker.sock ] && [ ! -e /run/docker.sock ] || fail "a rootful Docker socket exists"`,
			`[ -e /var/lib/systemd/linger/agent ] || fail "agent has no linger"`,
			`[ -n "$(find /usr/bin/newuidmap -maxdepth 0 -user 0 -perm 4755)" ] || fail "/usr/bin/newuidmap is not owned by root with mode 4755"`,
			`[ -n "$(find /usr/bin/newgidmap -maxdepth 0 -user 0 -perm 4755)" ] || fail "/usr/bin/newgidmap is not owned by root with mode 4755"`,
			"chmod 4755 /usr/bin/newuidmap /usr/bin/newgidmap",
			`systemctl is-active --quiet orbit-boundary-gate.service || fail "orbit-boundary-gate.service is not active"`,
			`docker buildx version >/dev/null || fail "docker buildx is missing"`,
			`docker compose version >/dev/null || fail "docker compose is missing"`,
		} {
			lineIndex(t, lines, want)
		}
		lineIndex(t, lines, `runuser -u agent -- env XDG_RUNTIME_DIR=/run/user/$agent_uid systemctl --user is-active --quiet docker.service || fail "docker.service of agent is not active"`)
	})
}

func TestSubIDCheckFailsOnASecondEntryAndOnALowRange(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "the range of agent", content: "agent:100000:65536\n"},
		{name: "a second entry", content: "agent:100000:65536\nother:165536:65536\n", wantErr: true},
		{name: "a range below 1000", content: "agent:100:65536\n", wantErr: true},
		{name: "another user", content: "other:100000:65536\n", wantErr: true},
		{name: "empty", content: "", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subuid")
			writeFixture(t, path, test.content, 0o644)
			err := runChecks(t, subIDCheck(path))
			if (err != nil) != test.wantErr {
				t.Errorf("check error = %v, want error %t", err, test.wantErr)
			}
		})
	}
}

func TestHelperCheckFailsOnAGroupWritableHelperAndAWrongOwner(t *testing.T) {
	uid := os.Getuid()
	tests := []struct {
		name    string
		mode    os.FileMode
		owner   int
		wantErr bool
	}{
		{name: "setuid root mode", mode: 0o755 | os.ModeSetuid, owner: uid},
		{name: "group writable", mode: 0o775 | os.ModeSetuid, owner: uid, wantErr: true},
		{name: "world writable", mode: 0o757 | os.ModeSetuid, owner: uid, wantErr: true},
		{name: "no setuid bit", mode: 0o755, owner: uid, wantErr: true},
		{name: "another owner", mode: 0o755 | os.ModeSetuid, owner: uid + 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "newuidmap")
			writeFixture(t, path, "", 0o644)
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			err := runChecks(t, helperCheck(path, test.owner))
			if (err != nil) != test.wantErr {
				t.Errorf("check error = %v, want error %t", err, test.wantErr)
			}
		})
	}
}

func TestDockerSetupScriptIsDeterministicAndPullsNothing(t *testing.T) {
	first, second := string(DockerSetupScript()), string(DockerSetupScript())
	if first != second {
		t.Error("DockerSetupScript changes between calls")
	}
	lines := strings.Split(first, "\n")
	if !slices.Equal(lines[:3], []string{"#!/bin/bash", "{", "set -euo pipefail"}) {
		t.Errorf("script starts with %q", lines[:3])
	}
	for _, forbidden := range []string{"docker pull", "docker run", "docker build"} {
		if strings.Contains(first, forbidden) {
			t.Errorf("setup script runs %q", forbidden)
		}
	}
	for _, want := range []string{
		`docker info --format '{{json .SecurityOptions}}' | grep -q rootless || { echo "the Docker engine of the agent is not rootless" >&2; exit 1; }`,
		`docker compose version --short | grep -Eq '^v?2\.' || { echo "docker compose is not v2" >&2; exit 1; }`,
		"docker context use orbit-rootless >/dev/null",
		"unset DOCKER_HOST DOCKER_CONFIG DOCKER_CONTEXT",
	} {
		lineIndex(t, lines, want)
	}
	if !strings.Contains(first, "http://172.31.255.1:3128") || strings.Contains(first, "127.0.0.1:3128") {
		t.Error("the setup script does not use the container address of the proxy")
	}
}

func TestMergeProxiesKeepsEveryOtherKey(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	tests := []struct {
		name    string
		before  string
		symlink bool
	}{
		{name: "missing file"},
		{name: "auths present", before: `{"auths":{"ghcr.io":{"auth":"abc"}},"credsStore":"x"}`},
		{name: "another proxy address", before: `{"proxies":{"default":{"httpProxy":"http://10.0.0.1:1","ftpProxy":"ftp://f:1"}},"currentContext":"c"}`},
		{name: "symlink", before: `{"auths":{"a":{}}}`, symlink: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, ".docker", "config.json")
			real := config
			if test.symlink {
				real = filepath.Join(dir, "elsewhere.json")
				if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, config); err != nil {
					t.Fatal(err)
				}
			}
			if test.before != "" {
				if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, real, test.before, 0o600)
			}
			if err := runChecks(t, mergeProxies("'"+config+"'")); err != nil {
				t.Fatal(err)
			}
			assertMerged(t, test.before, real)
			if test.symlink {
				if info, err := os.Lstat(config); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("config.json is no longer a symlink: %v", err)
				}
			}
		})
	}
}

func TestMergedConfigIsMode0600WhenCreated(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	config := filepath.Join(t.TempDir(), ".docker", "config.json")
	if err := runChecks(t, mergeProxies("'"+config+"'")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(config)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config.json mode = %v, %v, want 0600", info, err)
	}
}

func assertMerged(t *testing.T, before, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("config.json is not JSON: %v\n%s", err, raw)
	}
	def := got["proxies"].(map[string]any)["default"].(map[string]any)
	for key, want := range map[string]string{"httpProxy": "http://172.31.255.1:3128", "httpsProxy": "http://172.31.255.1:3128", "noProxy": "localhost,127.0.0.1"} {
		if def[key] != want {
			t.Errorf("proxies.default.%s = %v, want %s", key, def[key], want)
		}
	}
	if before == "" {
		return
	}
	var old map[string]any
	if err := json.Unmarshal([]byte(before), &old); err != nil {
		t.Fatal(err)
	}
	for key, value := range old {
		if key == "proxies" {
			continue
		}
		if gotValue, _ := json.Marshal(got[key]); string(gotValue) != mustJSON(t, value) {
			t.Errorf("key %q changed: %s, want %s", key, gotValue, mustJSON(t, value))
		}
	}
	oldProxies, _ := old["proxies"].(map[string]any)
	oldDefault, _ := oldProxies["default"].(map[string]any)
	if oldDefault["ftpProxy"] != nil && def["ftpProxy"] != oldDefault["ftpProxy"] {
		t.Error("an unrelated proxies.default key was dropped")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func runChecks(t *testing.T, checks []string) error {
	t.Helper()
	body := "set -euo pipefail\nfail() { echo \"$*\" >&2; exit 1; }\n" + strings.Join(checks, "\n") + "\n"
	cmd := exec.Command("bash", "-c", body)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("check output: %s", out)
		return err
	}
	return nil
}

func directiveValues(file File, key string) []string {
	var values []string
	for _, line := range strings.Split(file.Content, "\n") {
		if name, v, ok := strings.Cut(line, "="); ok && name == key {
			values = append(values, strings.Fields(v)...)
		}
	}
	return values
}
