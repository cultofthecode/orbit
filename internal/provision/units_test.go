package provision

import (
	"slices"
	"strings"
	"testing"
)

func TestUnitsStartBeforeUserSessions(t *testing.T) {
	for _, path := range []string{
		"/etc/systemd/system/orbit-proxy-address.service",
		"/etc/systemd/system/nftables.service.d/orbit.conf",
		"/etc/systemd/system/tinyproxy.service.d/orbit.conf",
		"/etc/systemd/system/orbit-harden.service",
	} {
		file := unitFile(t, path)
		if !hasDirective(file, "Before", "systemd-user-sessions.service") {
			t.Errorf("%s does not start before systemd-user-sessions.service:\n%s", path, file.Content)
		}
	}
}

func TestUserSessionsRequireTheBoundary(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/systemd-user-sessions.service.d/orbit.conf")
	for _, unit := range []string{"nftables.service", "orbit-proxy-address.service", "tinyproxy.service", "orbit-harden.service"} {
		for _, key := range []string{"Requires", "After"} {
			if !hasDirective(file, key, unit) {
				t.Errorf("user sessions lack %s=%s:\n%s", key, unit, file.Content)
			}
		}
	}
}

func TestProxyRequiresFirewallAndAddress(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/tinyproxy.service.d/orbit.conf")
	for _, unit := range []string{"nftables.service", "orbit-proxy-address.service"} {
		for _, key := range []string{"Requires", "After"} {
			if !hasDirective(file, key, unit) {
				t.Errorf("tinyproxy lacks %s=%s", key, unit)
			}
		}
	}
	if !hasDirective(file, "StartLimitIntervalSec", "0") {
		t.Error("tinyproxy keeps the start limit")
	}
}

func TestHardenUnitHasNoStartLimit(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/orbit-harden.service")
	if !hasDirective(file, "StartLimitIntervalSec", "0") {
		t.Errorf("orbit-harden.service keeps the start limit:\n%s", file.Content)
	}
}

func TestUserSessionsHaveNoStartLimit(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/systemd-user-sessions.service.d/orbit.conf")
	if !hasDirective(file, "StartLimitIntervalSec", "0") {
		t.Errorf("systemd-user-sessions.service keeps the start limit:\n%s", file.Content)
	}
}

func TestOwnUnitsAreEnabledAtBoot(t *testing.T) {
	for _, path := range []string{"/etc/systemd/system/orbit-proxy-address.service", "/etc/systemd/system/orbit-harden.service"} {
		if !hasDirective(unitFile(t, path), "WantedBy", "multi-user.target") {
			t.Errorf("%s is not wanted by multi-user.target", path)
		}
	}
}

func TestProxyAddressUnitSetsTheContainerAddress(t *testing.T) {
	file := unitFile(t, "/etc/systemd/system/orbit-proxy-address.service")
	if !strings.Contains(file.Content, "ExecStart=ip addr replace 172.31.255.1/32 dev orbit0\n") {
		t.Errorf("unit does not set the container address:\n%s", file.Content)
	}
}

func TestHardenScript(t *testing.T) {
	file := unitFile(t, "/usr/local/libexec/orbit-harden")
	if file.Mode != 0o755 {
		t.Errorf("mode = %o, want 755", file.Mode)
	}
	for _, want := range []string{
		"set -eu",
		"find /etc/sudoers.d -mindepth 1 ! -name README -delete",
		"for group in sudo admin adm; do",
		"passwd -l root >/dev/null",
		`[ "$uid" -eq 501 ]`,
		`[ "$uid" -ge 1000 ]`,
	} {
		if !strings.Contains(file.Content, want) {
			t.Errorf("harden script lacks %q:\n%s", want, file.Content)
		}
	}
	if strings.Contains(file.Content, "NOPASSWD") {
		t.Error("harden script grants sudo")
	}
}

func TestUnitsAreOwnedByRootPaths(t *testing.T) {
	for _, file := range Units(501) {
		if !strings.HasPrefix(file.Path, "/etc/") && !strings.HasPrefix(file.Path, "/usr/local/") {
			t.Errorf("unit file %s is outside /etc and /usr/local", file.Path)
		}
		if file.Mode&0o022 != 0 {
			t.Errorf("%s is writable by group or others: %o", file.Path, file.Mode)
		}
	}
}

func unitFile(t *testing.T, path string) File {
	t.Helper()
	files := Units(501)
	i := slices.IndexFunc(files, func(file File) bool { return file.Path == path })
	if i < 0 {
		t.Fatalf("Units has no file %s", path)
	}
	return files[i]
}

func hasDirective(file File, key, value string) bool {
	for _, line := range strings.Split(file.Content, "\n") {
		name, values, ok := strings.Cut(line, "=")
		if ok && name == key && slices.Contains(strings.Fields(values), value) {
			return true
		}
	}
	return false
}
