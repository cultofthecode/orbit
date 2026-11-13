package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyDebianCarriesThePrivilegedGuard(t *testing.T) {
	want := map[string]bool{"ubuntu": false, "debian": true}
	eachDistribution(t, func(t *testing.T, distribution string) {
		got := strings.Contains(strings.Join(script(t, distribution), "\n"), "put "+runtimeWrapperPath+" ")
		if got != want[distribution] {
			t.Errorf("guard written = %t, want %t", got, want[distribution])
		}
	})
}

func TestDaemonDropInReadsTheRootOwnedConfiguration(t *testing.T) {
	got := daemonDropIn()
	for _, want := range []string{"ExecStart=\n", "--config-file=" + daemonConfigPath} {
		if !strings.Contains(got, want) {
			t.Errorf("drop-in lacks %q: %s", want, got)
		}
	}
	if !strings.Contains(daemonConfig(), `"default-runtime": "orbit-runc"`) {
		t.Error("the configuration does not make the wrapper the default runtime")
	}
}

func TestRuntimeWrapperRefusesWhatIsNotMasked(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "wrapper")
	real := filepath.Join(dir, "runc")
	script := strings.Replace(runtimeWrapper(), runtimeRealPath, real, 1)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := func(config string) string {
		b := t.TempDir()
		if config != "" {
			if err := os.WriteFile(filepath.Join(b, "config.json"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return b
	}
	tests := []struct {
		name string
		args []string
		ok   bool
	}{
		{"normal", []string{"--root", "/r", "create", "--bundle", bundle(`{"linux":{"maskedPaths":["/proc/kcore"]}}`), "id"}, true},
		{"privileged", []string{"create", "--bundle", bundle(`{"linux":{"namespaces":[]}}`), "id"}, false},
		{"empty masked list", []string{"create", "--bundle=" + bundle(`{"linux":{"maskedPaths":[]}}`), "id"}, false},
		{"no config", []string{"create", "--bundle", bundle(""), "id"}, false},
		{"no bundle", []string{"create", "id"}, false},
		{"run privileged", []string{"run", "-b", bundle(`{}`), "id"}, false},
		{"other command", []string{"kill", "id", "9"}, true},
	}
	for _, tt := range tests {
		err := exec.Command(wrapper, tt.args...).Run()
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok = %t", tt.name, err, tt.ok)
		}
	}
}
