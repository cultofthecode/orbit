package provision

import (
	"fmt"
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

func TestOnlyDebianChecksThePrivilegedGuard(t *testing.T) {
	want := map[string]bool{"ubuntu": false, "debian": true}
	eachDistribution(t, func(t *testing.T, distribution string) {
		p, err := profileOf(distribution)
		if err != nil {
			t.Fatal(err)
		}
		check := p.guardCheck()
		if (len(check) > 0) != want[distribution] {
			t.Fatalf("guardCheck has %d lines, want some = %t", len(check), want[distribution])
		}
		text := strings.Join(script(t, distribution), "\n")
		for _, line := range check {
			if !strings.Contains(text, line) {
				t.Errorf("the self-check lacks the guard line %q", line)
			}
		}
		if got := strings.Contains(text, runtimeRealPath); got != want[distribution] {
			t.Errorf("script names %s = %t, want %t", runtimeRealPath, got, want[distribution])
		}
	})
}

func TestTheDiversionComesBeforeTheWrapperIsWritten(t *testing.T) {
	lines := script(t, "debian")
	divert := lineIndexPrefix(t, lines, "if [ \"$(dpkg-divert --truename "+runtimeWrapperPath+")\"")
	put := lineIndexPrefix(t, lines, "put "+runtimeWrapperPath+" ")
	if divert > put {
		t.Errorf("the diversion (line %d) comes after the wrapper (line %d)", divert, put)
	}
	for _, line := range script(t, "ubuntu") {
		if strings.Contains(line, "dpkg-divert") {
			t.Errorf("ubuntu diverts a file: %s", line)
		}
	}
}

func TestTheSelfCheckProvesEveryRuntimeGoesThroughTheWrapper(t *testing.T) {
	p, err := profileOf("debian")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.guardCheck(), "\n")
	for _, want := range []string{
		"docker info --format '{{range .Runtimes}}path={{.Path}}",
		`while IFS= read -r runtime_line; do`,
		`readlink -f "$resolved"`,
		"does not go through the guard",
		"/proc/$daemon_pid/environ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the guard check lacks %q", want)
		}
	}
}

func lineIndexPrefix(t *testing.T, lines []string, prefix string) int {
	t.Helper()
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return i
		}
	}
	t.Fatalf("no line starts with %q", prefix)
	return -1
}

const containerConfig = `{"process":{"capabilities":{"bounding":["CAP_CHOWN","CAP_KILL"],"effective":["CAP_CHOWN"]}},"linux":{"maskedPaths":["/proc/kcore"]}}`

const fullContainerConfig = `{"process":{"capabilities":{"bounding":["CAP_CHOWN","CAP_KILL","CAP_SYS_ADMIN","CAP_SYS_MODULE"]}},"linux":{"maskedPaths":["/proc/kcore"]}}`

// wrapperRig is the wrapper with a fake real runtime that logs its arguments
// and answers the state command with the bundle of one container.
type wrapperRig struct {
	t       *testing.T
	wrapper string
	log     string
	bundle  string
}

func newWrapperRig(t *testing.T, stateWorks bool, config string) wrapperRig {
	t.Helper()
	dir := t.TempDir()
	rig := wrapperRig{t: t, wrapper: filepath.Join(dir, "wrapper"), log: filepath.Join(dir, "log"), bundle: filepath.Join(dir, "bundle")}
	real := filepath.Join(dir, "runc.real")
	state := "exit 1"
	if stateWorks {
		state = fmt.Sprintf(`printf '{\n  "bundle": "%s"\n}\n'`, rig.bundle)
	}
	fake := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\nfor a in \"$@\"; do\n  if [ \"$a\" = state ]; then %s; exit 0; fi\ndone\nexit 0\n", rig.log, state)
	wrapper := strings.Replace(runtimeWrapper(), runtimeRealPath, real, 1)
	rig.write(rig.wrapper, wrapper, 0o755)
	rig.write(real, fake, 0o755)
	if config != "" {
		if err := os.MkdirAll(rig.bundle, 0o755); err != nil {
			t.Fatal(err)
		}
		rig.write(filepath.Join(rig.bundle, "config.json"), config, 0o644)
	}
	return rig
}

func (r wrapperRig) write(path, content string, mode os.FileMode) {
	r.t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		r.t.Fatal(err)
	}
}

func (r wrapperRig) process(content string) string {
	r.t.Helper()
	path := filepath.Join(r.t.TempDir(), "process.json")
	r.write(path, content, 0o644)
	return path
}

func (r wrapperRig) run(args ...string) error {
	r.t.Helper()
	return exec.Command(r.wrapper, args...).Run()
}

const (
	defaultCaps    = `"CAP_CHOWN","CAP_KILL"`
	allCaps        = `"CAP_CHOWN","CAP_KILL","CAP_SYS_ADMIN","CAP_SYS_MODULE","CAP_SYS_RAWIO","CAP_SYS_BOOT","CAP_MAC_ADMIN"`
	defaultSeccomp = `"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"},`
)

// createConfig renders the parts of a container configuration that the
// wrapper reads: the bounding set, an optional seccomp object and an optional
// AppArmor profile (each ending in a comma), and masked paths.
func createConfig(caps, seccomp, apparmor string) string {
	return `{"process":{` + apparmor + `"capabilities":{"bounding":[` + caps + `]}},"linux":{` + seccomp + `"maskedPaths":["/proc/kcore"]}}`
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
		{"every capability, no seccomp, no AppArmor", []string{"create", "--bundle", bundle(createConfig(allCaps, "", "")), "id"}, false},
		{"every capability, no seccomp, AppArmor unconfined", []string{"create", "--bundle", bundle(createConfig(allCaps, "", `"apparmorProfile":"unconfined",`)), "id"}, false},
		{"every capability, seccomp that allows by default, no AppArmor", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_ALLOW"},`, "")), "id"}, false},
		{"every capability, default seccomp, no AppArmor", []string{"create", "--bundle", bundle(createConfig(allCaps, defaultSeccomp, "")), "id"}, true},
		{"every capability, no seccomp, an AppArmor profile", []string{"create", "--bundle", bundle(createConfig(allCaps, "", `"apparmorProfile":"docker-default",`)), "id"}, true},
		{"default capabilities, no seccomp, no AppArmor", []string{"create", "--bundle", bundle(createConfig(defaultCaps, "", "")), "id"}, true},
		{"one added capability, no seccomp, no AppArmor", []string{"create", "--bundle", bundle(createConfig(defaultCaps+`,"CAP_SYS_ADMIN"`, "", "")), "id"}, true},
		{"every capability, default profiles", []string{"create", "--bundle", bundle(createConfig(allCaps, defaultSeccomp, `"apparmorProfile":"docker-default",`)), "id"}, true},
		{"restore without masked paths", []string{"restore", "--bundle", bundle(`{"linux":{"namespaces":[]}}`), "id"}, false},
		{"restore with short flag, no config", []string{"--root", "/r", "restore", "-b", bundle(""), "id"}, false},
		{"restore with masked paths", []string{"restore", "--bundle", bundle(`{"linux":{"maskedPaths":["/proc/kcore"]}}`), "id"}, true},
		{"restore with every capability and no profiles", []string{"restore", "--bundle", bundle(createConfig(allCaps, "", "")), "id"}, false},
		{"unknown subcommand", []string{"frobnicate", "id"}, false},
		{"unknown subcommand after global flags", []string{"--root", "/r", "--log", "/l", "--systemd-cgroup", "frobnicate"}, false},
		{"unknown global flag with a value", []string{"--mystery", "x", "state", "id"}, false},
		{"global flags with equals signs", []string{"--root=/r", "--log=/l", "state", "id"}, true},
		{"state", []string{"--root", "/r", "state", "id"}, true},
		{"delete", []string{"delete", "--force", "id"}, true},
		{"list", []string{"--root", "/r", "list"}, true},
		{"every capability, seccomp that only logs, AppArmor unconfined", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_LOG"},`, `"apparmorProfile":"unconfined",`)), "id"}, false},
		{"every capability, seccomp with an unknown default action", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_NEW"},`, `"apparmorProfile":"unconfined",`)), "id"}, false},
		{"every capability, seccomp with the trace default action", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_TRACE"},`, `"apparmorProfile":"unconfined",`)), "id"}, false},
		{"every capability, seccomp with an empty object", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{},`, `"apparmorProfile":"unconfined",`)), "id"}, false},
		{"every capability, seccomp that kills by default", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_KILL_PROCESS"},`, `"apparmorProfile":"unconfined",`)), "id"}, true},
		{"every capability, seccomp that traps by default", []string{"create", "--bundle", bundle(createConfig(allCaps, `"seccomp":{"defaultAction":"SCMP_ACT_TRAP"},`, `"apparmorProfile":"unconfined",`)), "id"}, true},
		{"every capability, a user annotation named like the AppArmor field", []string{"create", "--bundle", bundle(annotatedConfig(`"apparmorProfile":"x"`)), "id"}, false},
		{"id that equals create", []string{"kill", "create", "9"}, false},
		{"other command", []string{"kill", "id", "9"}, true},
		{"version", []string{"--version"}, true},
	}
	for _, tt := range tests {
		err := exec.Command(wrapper, tt.args...).Run()
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok = %t", tt.name, err, tt.ok)
		}
	}
}

// annotatedConfig is a container with every capability, no seccomp filter, and
// no AppArmor field in its process, and with a user-controlled annotation
// after the root object.
func annotatedConfig(annotation string) string {
	return `{"process":{"capabilities":{"bounding":[` + allCaps + `]}},"root":{"path":"rootfs"},"annotations":{` + annotation + `},"linux":{"maskedPaths":["/proc/kcore"]}}`
}

func TestTheSelfCheckLoopFailsForARuntimeWithAnEmptyPath(t *testing.T) {
	p, err := profileOf("debian")
	if err != nil {
		t.Fatal(err)
	}
	var loop []string
	in := false
	for _, line := range p.guardCheck() {
		if strings.HasPrefix(line, "while ") {
			in = true
		}
		if in {
			loop = append(loop, line)
		}
	}
	wrapper := t.TempDir() + "/runc"
	if err := os.WriteFile(wrapper, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(strings.Join(loop, "\n"), runtimeWrapperPath, wrapper)
	tests := []struct {
		name  string
		paths string
		ok    bool
	}{
		{"one runtime that resolves to the wrapper", "path=" + wrapper, true},
		{"two runtimes that resolve to the wrapper", "path=" + wrapper + "\npath=" + wrapper, true},
		{"empty path", "path=", false},
		{"empty path after a good one", "path=" + wrapper + "\npath=", false},
		{"empty path before a good one", "path=\npath=" + wrapper, false},
		{"path that is not the wrapper", "path=/bin/sh", false},
	}
	for _, tt := range tests {
		script := "fail() { echo \"$*\" >&2; exit 1; }\ndaemon_path=/usr/bin:/bin\nruntime_paths=$1\n" + body + "\n"
		err := exec.Command("bash", "-c", script, "bash", tt.paths).Run()
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok = %t", tt.name, err, tt.ok)
		}
	}
}

func TestRuntimeWrapperRefusesAPrivilegedExec(t *testing.T) {
	globals := []string{"--root", "/r", "--log", "/l", "--log-format", "json", "--systemd-cgroup"}
	const sameCaps = `{"capabilities":{"bounding":["CAP_CHOWN","CAP_KILL"],"effective":["CAP_CHOWN"],"permitted":["CAP_CHOWN","CAP_KILL"]},"args":["true"]}`
	const moreBounding = `{"capabilities":{"bounding":["CAP_CHOWN","CAP_KILL","CAP_SYS_ADMIN"],"effective":["CAP_CHOWN"]}}`
	const moreEffective = `{"capabilities":{"bounding":["CAP_CHOWN"],"effective":["CAP_CHOWN","CAP_SYS_MODULE"]}}`
	const moreAmbient = `{"capabilities":{"bounding":["CAP_CHOWN"],"ambient":["CAP_NET_ADMIN"]}}`
	const noCaps = `{"args":["true"],"cwd":"/"}`
	tests := []struct {
		name       string
		config     string
		stateWorks bool
		process    string
		args       func(process string) []string
		ok         bool
	}{
		{"same capabilities", containerConfig, true, sameCaps, execArgs(globals, "--process"), true},
		{"short flag", containerConfig, true, sameCaps, execArgs(globals, "-p"), true},
		{"flag with equals sign", containerConfig, true, sameCaps, func(p string) []string { return []string{"exec", "--process=" + p, "--detach", "id"} }, true},
		{"no capabilities in the process", containerConfig, true, noCaps, execArgs(globals, "--process"), true},
		{"privileged exec", containerConfig, true, moreBounding, execArgs(globals, "--process"), false},
		{"privileged exec, short flag", containerConfig, true, moreBounding, execArgs(globals, "-p"), false},
		{"privileged exec, equals sign", containerConfig, true, moreBounding, func(p string) []string { return []string{"exec", "--process=" + p, "id"} }, false},
		{"extra effective capability", containerConfig, true, moreEffective, execArgs(globals, "--process"), false},
		{"extra ambient capability", containerConfig, true, moreAmbient, execArgs(globals, "--process"), false},
		{"container with every capability", fullContainerConfig, true, moreBounding, execArgs(globals, "--process"), true},
		{"no process file", containerConfig, true, "", func(string) []string { return []string{"exec", "id", "true"} }, false},
		{"process file that does not exist", containerConfig, true, sameCaps, func(string) []string { return []string{"exec", "--process", "/nonexistent", "id"} }, false},
		{"state fails", containerConfig, false, sameCaps, execArgs(globals, "--process"), false},
		{"container without a configuration", "", true, sameCaps, execArgs(globals, "--process"), false},
		{"container without a bounding set", `{"linux":{"maskedPaths":["/proc/kcore"]}}`, true, sameCaps, execArgs(globals, "--process"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newWrapperRig(t, tt.stateWorks, tt.config)
			process := ""
			if tt.process != "" {
				process = rig.process(tt.process)
			}
			err := rig.run(tt.args(process)...)
			if (err == nil) != tt.ok {
				t.Errorf("err = %v, want ok = %t", err, tt.ok)
			}
		})
	}
}

func execArgs(globals []string, processFlag string) func(string) []string {
	return func(process string) []string {
		return append(append(append([]string{}, globals...), "exec"), processFlag, process, "--detach", "--pid-file", "/p", "id")
	}
}

func TestRuntimeWrapperPassesAnAllowedExecThroughUnchanged(t *testing.T) {
	rig := newWrapperRig(t, true, containerConfig)
	process := rig.process(`{"capabilities":{"bounding":["CAP_CHOWN"]}}`)
	args := []string{"--root", "/r", "exec", "--process", process, "--detach", "id"}
	if err := rig.run(args...); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rig.log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if want := "--root /r state id"; lines[0] != want {
		t.Errorf("state call = %q, want %q", lines[0], want)
	}
	if want := strings.Join(args, " "); lines[len(lines)-1] != want {
		t.Errorf("real call = %q, want %q", lines[len(lines)-1], want)
	}
}

func TestRuntimeWrapperPassesTheAllowedSubcommands(t *testing.T) {
	for _, sub := range []string{"start", "pause", "resume", "ps", "events", "features", "update", "checkpoint", "spec", "help", "version", ""} {
		t.Run("sub="+sub, func(t *testing.T) {
			rig := newWrapperRig(t, true, containerConfig)
			args := []string{"--root", "/r"}
			if sub != "" {
				args = append(args, sub, "id")
			}
			if err := rig.run(args...); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(rig.log)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Join(args, " "); strings.TrimSpace(string(got)) != want {
				t.Errorf("real call = %q, want %q", got, want)
			}
		})
	}
}
