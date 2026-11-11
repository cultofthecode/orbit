package provision

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func scriptLines(script []byte) []string {
	return strings.Split(string(script), "\n")
}

func checkScripts() map[string][]byte {
	return map[string][]byte{"BootCheckScript": BootCheckScript(), "MarkerCheckScript": MarkerCheckScript(), "ReadinessScript": ReadinessScript()}
}

func TestReadinessScriptChecksTheMarkerTheTableAndEveryBoundaryUnit(t *testing.T) {
	lines := scriptLines(ReadinessScript())
	lineIndex(t, lines, `printf '%s\n' 1 | cmp -s - /etc/orbit/provisioned || fail "/etc/orbit/provisioned does not hold provisioning version 1"`)
	lineIndex(t, lines, `nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`)
	units := lineIndex(t, lines, "for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do")
	lineIndex(t, lines, `  systemctl is-active --quiet "$unit" || fail "$unit is not active"`)
	for _, unit := range append(boundaryUnits(), unitUserSessions) {
		if !strings.Contains(lines[units], " "+unit+" ") && !strings.Contains(lines[units], " "+unit+";") {
			t.Errorf("the readiness script does not check %s", unit)
		}
	}
	if !strings.Contains(string(ReadinessScript()), strings.Join(boundaryCheck(), "\n")) {
		t.Error("the readiness script does not run the boundary check of the self-check of Script")
	}
}

func TestReadinessScriptDoesNotWaitForTheBoot(t *testing.T) {
	if strings.Contains(string(ReadinessScript()), "is-system-running") {
		t.Error("the readiness script waits for the boot, want only the checks")
	}
}

func TestCheckScriptsStopAtTheFirstFailure(t *testing.T) {
	for name, script := range checkScripts() {
		lines := scriptLines(script)
		if !slices.Equal(lines[:3], []string{"#!/bin/bash", "{", "set -euo pipefail"}) {
			t.Errorf("%s starts with %q, want the shebang, a group, and set -euo pipefail", name, lines[:3])
		}
		if lines[len(lines)-2] != "}" || lines[len(lines)-1] != "" {
			t.Errorf("%s ends with %q, want the end of the group and a newline", name, lines[len(lines)-2:])
		}
	}
}

func TestBootCheckScriptWaitsForTheBootWithABound(t *testing.T) {
	lines := scriptLines(BootCheckScript())
	wait := lineIndex(t, lines, "timeout 120 systemctl is-system-running --wait >/dev/null 2>&1 || true")
	table := lineIndex(t, lines, `nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`)
	if wait > table {
		t.Error("the boot check checks the firewall before it waits for the boot")
	}
}

func TestBootCheckScriptChecksTheTableAndEveryBoundaryUnit(t *testing.T) {
	lines := scriptLines(BootCheckScript())
	table := lineIndex(t, lines, `nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`)
	units := lineIndex(t, lines, "for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do")
	lineIndex(t, lines, `  systemctl is-active --quiet "$unit" || fail "$unit is not active"`)
	if table > units {
		t.Error("the boot check checks the units before the table, unlike the self-check of Script")
	}
	for _, unit := range append(boundaryUnits(), unitUserSessions) {
		if !strings.Contains(lines[units], " "+unit+" ") && !strings.Contains(lines[units], " "+unit+";") {
			t.Errorf("the boot check does not check %s", unit)
		}
	}
}

func TestBootCheckScriptMatchesTheSelfCheckOfScript(t *testing.T) {
	provisioning := strings.Join(script(t), "\n")
	if check := strings.Join(boundaryCheck(), "\n"); !strings.Contains(provisioning, check) {
		t.Errorf("Script does not run the boundary check of BootCheckScript:\n%s", check)
	}
}

func TestMarkerCheckScriptComparesTheMarkerWithTheVersion(t *testing.T) {
	lines := scriptLines(MarkerCheckScript())
	lineIndex(t, lines, `printf '%s\n' 1 | cmp -s - /etc/orbit/provisioned || fail "/etc/orbit/provisioned does not hold provisioning version 1"`)
	if strings.Contains(string(MarkerCheckScript()), "systemctl") {
		t.Error("the marker check checks units, want only the marker")
	}
}

func TestCheckScriptsAreDeterministic(t *testing.T) {
	if !bytes.Equal(BootCheckScript(), BootCheckScript()) || !bytes.Equal(MarkerCheckScript(), MarkerCheckScript()) || !bytes.Equal(ReadinessScript(), ReadinessScript()) {
		t.Error("check scripts differ between calls")
	}
}

func TestCheckScriptsParse(t *testing.T) {
	for name, script := range checkScripts() {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = bytes.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("bash -n of %s = %v: %s", name, err, out)
		}
	}
}
