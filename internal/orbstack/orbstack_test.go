package orbstack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const testPrefix = "orbit-"

func TestCreateArgs(t *testing.T) {
	tests := []struct {
		name    string
		machine string
		dir     string
		want    []string
	}{
		{
			name:    "project directory",
			machine: "orbit-api-1d9b22c9",
			dir:     "/Users/dev/Project/api",
			want: []string{
				"create", "--isolated", "--isolate-network",
				"--mount", "/Users/dev/Project/api:/Users/dev/Project/api",
				"ubuntu:26.04", "orbit-api-1d9b22c9",
			},
		},
		{
			name:    "directory with spaces and shell characters",
			machine: "orbit-my-project-00db83c9",
			dir:     "/Users/dev/My Project; $(x)",
			want: []string{
				"create", "--isolated", "--isolate-network",
				"--mount", "/Users/dev/My Project; $(x):/Users/dev/My Project; $(x)",
				"ubuntu:26.04", "orbit-my-project-00db83c9",
			},
		},
		{
			name:    "directory that looks like a flag",
			machine: "orbit-x-00000000",
			dir:     "/--forward-ssh-agent",
			want: []string{
				"create", "--isolated", "--isolate-network",
				"--mount", "/--forward-ssh-agent:/--forward-ssh-agent",
				"ubuntu:26.04", "orbit-x-00000000",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := createArgs(test.machine, test.dir)
			if !slices.Equal(got, test.want) {
				t.Errorf("createArgs(%q, %q) =\n%q, want\n%q", test.machine, test.dir, got, test.want)
			}
			if slices.Contains(got, "--forward-ssh-agent") {
				t.Errorf("createArgs(%q, %q) = %q, want no SSH agent forwarding", test.machine, test.dir, got)
			}
		})
	}
}

func TestListArgs(t *testing.T) {
	want := []string{"list", "--quiet"}
	if got := listArgs(); !slices.Equal(got, want) {
		t.Errorf("listArgs() = %q, want %q", got, want)
	}
}

func TestDeleteArgs(t *testing.T) {
	want := []string{"delete", "--force", "orbit-api-1d9b22c9"}
	if got := deleteArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("deleteArgs() = %q, want %q", got, want)
	}
}

func TestMountsArgs(t *testing.T) {
	want := []string{"list", "--format", "json"}
	if got := mountsArgs(); !slices.Equal(got, want) {
		t.Errorf("mountsArgs() = %q, want %q", got, want)
	}
}

func TestActiveMountsArgs(t *testing.T) {
	want := []string{"-m", "orbit-api-1d9b22c9", "-u", "root", "cat", "/proc/self/mountinfo"}
	if got := activeMountsArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("activeMountsArgs() = %q, want %q", got, want)
	}
}

// machineList is the output of "orb list --format json" of OrbStack 2.2.5
// with an isolated machine and a machine without isolation.
const machineList = `[
  {"id": "01M73SPM5Q7ZR17EAG6KHQVAWR", "name": "orbit-api-1d9b22c9",
   "config": {"isolated": true, "isolate_network": true,
     "mounts": [{"source": "/Users/dev/Project/api", "destination": "/Users/dev/Project/api"}]},
   "state": "running"},
  {"id": "01M73SPM5Q7ZR17EAG6KHQVAWS", "name": "ubuntu", "config": {"isolated": false}, "state": "stopped"}
]`

func TestParseMounts(t *testing.T) {
	tests := []struct {
		name string
		list string
		want map[string]map[string]string
	}{
		{
			name: "isolated machine and machine without isolation",
			list: machineList,
			want: map[string]map[string]string{
				"orbit-api-1d9b22c9": {"/Users/dev/Project/api": "/Users/dev/Project/api"},
				"ubuntu":             {},
			},
		},
		{name: "no machines", list: "[]", want: map[string]map[string]string{}},
		{
			name: "mount at another path and a second mount",
			list: `[{"name": "orbit-x", "config": {"mounts": [
				{"source": "/Users/dev/.ssh", "destination": "/Users/dev/Project/api"},
				{"source": "/Users/dev", "destination": "/home/dev"}]}}]`,
			want: map[string]map[string]string{
				"orbit-x": {"/Users/dev/Project/api": "/Users/dev/.ssh", "/home/dev": "/Users/dev"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMounts([]byte(test.list))
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseMounts = %v, %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestParseMountsRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		list string
	}{
		{name: "empty output", list: ""},
		{name: "text output", list: "orbit-api-1d9b22c9  running  ubuntu"},
		{name: "object instead of a list", list: `{"name": "orbit-x"}`},
		{name: "mounts of the wrong type", list: `[{"name": "orbit-x", "config": {"mounts": "/a:/a"}}]`},
		{name: "machine listed twice", list: `[{"name": "orbit-x"}, {"name": "orbit-x"}]`},
		{
			name: "two mounts at one mount point",
			list: `[{"name": "orbit-x", "config": {"mounts": [
				{"source": "/Users/dev/Project/api", "destination": "/Users/dev/Project/api"},
				{"source": "/Users/dev/.ssh", "destination": "/Users/dev/Project/api"}]}}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMounts([]byte(test.list))
			if !errors.Is(err, ErrMountTable) || got != nil {
				t.Errorf("parseMounts(%q) = %v, %v, want %v", test.list, got, err, ErrMountTable)
			}
		})
	}
}

// mountTableEntries are entries of /proc/self/mountinfo in an isolated
// machine of OrbStack 2.2.5, besides the mount of the project.
var mountTableEntries = []string{
	"125 1 0:31 / / rw,relatime shared:1 - btrfs /dev/vdb1 rw,space_cache=v2",
	"130 125 0:5 /null /dev/null rw,nosuid master:2 - devtmpfs devtmpfs rw,size=4k",
	"140 125 0:40 /opt/orbstack-guest /opt/orbstack-guest ro,relatime - overlay orbstack ro,lowerdir=/x",
	"150 125 0:22 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw",
	"160 125 0:44 / /tmp rw,nosuid,nodev shared:9 - tmpfs tmpfs rw",
}

// table returns a mount table with the entries of the machine and entries.
func table(entries ...string) string {
	return strings.Join(slices.Concat(mountTableEntries, entries), "\n") + "\n"
}

func TestParseMountTable(t *testing.T) {
	const project = "/Users/dev/Project/api"
	tests := []struct {
		name  string
		table string
		want  map[string]string
	}{
		{
			name:  "project at its own path",
			table: table("195 125 0:35 " + project + " " + project + " rw,relatime shared:107 - virtiofs mac rw"),
			want:  map[string]string{project: project},
		},
		{
			name:  "symlink swapped for another directory",
			table: table("195 125 0:35 /Users/dev/.ssh " + project + " rw,relatime shared:107 - virtiofs mac rw"),
			want:  map[string]string{project: "/Users/dev/.ssh"},
		},
		{
			name:  "no optional fields",
			table: table("195 125 0:35 " + project + " " + project + " rw,relatime - virtiofs mac rw"),
			want:  map[string]string{project: project},
		},
		{
			name:  "escaped space, tab, newline, and backslash",
			table: table(`195 125 0:35 /Users/dev/My\040Project\011a\012b\134c /Users/dev/My\040Project\011a\012b\134c rw - virtiofs mac rw`),
			want:  map[string]string{"/Users/dev/My Project\ta\nb\\c": "/Users/dev/My Project\ta\nb\\c"},
		},
		{
			name:  "virtiofs of another source",
			table: table("195 125 0:35 / /mnt/other rw - virtiofs other rw"),
			want:  map[string]string{},
		},
		{
			name:  "host shared at a second mount point",
			table: table("195 125 0:35 "+project+" "+project+" rw - virtiofs mac rw", "196 125 0:35 / /mnt/mac rw - virtiofs mac rw"),
			want:  map[string]string{project: project, "/mnt/mac": "/"},
		},
		{name: "no mount of the host", table: table(), want: map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMountTable(test.table)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseMountTable = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestParseMountTableRefusesWhatItCannotRead(t *testing.T) {
	const project = "/Users/dev/Project/api"
	tests := []struct {
		name  string
		table string
	}{
		{name: "empty table", table: ""},
		{name: "error text", table: "cat: /proc/self/mountinfo: No such file or directory\n"},
		{name: "entry without the end of the optional fields", table: table("195 125 0:35 / / rw shared:1 virtiofs mac rw x")},
		{name: "entry without a source", table: table("195 125 0:35 / / rw - virtiofs")},
		{name: "end of the optional fields too early", table: table("195 125 0:35 / - rw x y virtiofs mac rw")},
		{name: "blank line in the table", table: table("", "195 125 0:35 / / rw - virtiofs mac rw")},
		{name: "cut escape", table: table(`195 125 0:35 /Users/dev/a\04 /Users/dev/a\04 rw - virtiofs mac rw`)},
		{name: "escape that is not octal", table: table(`195 125 0:35 /Users/dev/a\089 /Users/dev/a\089 rw - virtiofs mac rw`)},
		{name: "escape above one byte", table: table(`195 125 0:35 /Users/dev/a\777 /Users/dev/a\777 rw - virtiofs mac rw`)},
		{
			name: "second mount over the project",
			table: table(
				"195 125 0:35 "+project+" "+project+" rw - virtiofs mac rw",
				"196 195 0:35 /Users/dev/.ssh "+project+" rw - virtiofs mac rw",
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMountTable(test.table)
			if !errors.Is(err, ErrMountTable) || got != nil {
				t.Errorf("parseMountTable(%q) = %q, %v, want %v", test.table, got, err, ErrMountTable)
			}
		})
	}
}

func TestMountsAndActiveMountsReadOrb(t *testing.T) {
	const project = "/Users/dev/Project/api"
	useFakeCLI(t, "if [ \"$1\" = list ]; then printf '%s\\n' '"+machineList+"'\n"+
		"else echo '195 125 0:35 "+project+" "+project+" rw - virtiofs mac rw'; fi\n")
	client := New(testPrefix)
	ctx := context.Background()
	want := map[string]string{project: project}

	mounts, err := client.Mounts(ctx)
	if err != nil || !reflect.DeepEqual(mounts["orbit-api-1d9b22c9"], want) {
		t.Errorf("Mounts = %v, %v, want %v for orbit-api-1d9b22c9", mounts, err, want)
	}
	active, err := client.ActiveMounts(ctx, "orbit-api-1d9b22c9")
	if err != nil || !reflect.DeepEqual(active, want) {
		t.Errorf("ActiveMounts = %v, %v, want %v", active, err, want)
	}
}

func TestListsMachine(t *testing.T) {
	const names = "orbit-api-1d9b22c9\nubuntu\norbit-web-00db83c9\n"
	tests := []struct {
		name    string
		names   string
		machine string
		want    bool
	}{
		{name: "first line", names: names, machine: "orbit-api-1d9b22c9", want: true},
		{name: "last line", names: names, machine: "orbit-web-00db83c9", want: true},
		{name: "no machines", names: "", machine: "orbit-api-1d9b22c9", want: false},
		{name: "prefix of a name", names: names, machine: "orbit-api", want: false},
		{name: "longer than a name", names: names, machine: "orbit-api-1d9b22c9x", want: false},
		{name: "empty name", names: names, machine: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := listsMachine(test.names, test.machine); got != test.want {
				t.Errorf("listsMachine(%q, %q) = %v, want %v", test.names, test.machine, got, test.want)
			}
		})
	}
}

func TestCheckOwned(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		machine string
		want    error
	}{
		{name: "name with the prefix", prefix: testPrefix, machine: "orbit-api-1d9b22c9"},
		{name: "name of another machine", prefix: testPrefix, machine: "ubuntu", want: ErrForeignMachine},
		{name: "prefix in the middle", prefix: testPrefix, machine: "my-orbit-api", want: ErrForeignMachine},
		{name: "prefix without the hyphen", prefix: testPrefix, machine: "orbital", want: ErrForeignMachine},
		{name: "upper case prefix", prefix: testPrefix, machine: "Orbit-api", want: ErrForeignMachine},
		{name: "leading space", prefix: testPrefix, machine: " orbit-api", want: ErrForeignMachine},
		{name: "flag", prefix: testPrefix, machine: "--all", want: ErrForeignMachine},
		{name: "only the prefix", prefix: testPrefix, machine: "orbit-", want: ErrForeignMachine},
		{name: "empty name", prefix: testPrefix, machine: "", want: ErrForeignMachine},
		{name: "empty prefix", prefix: "", machine: "orbit-api-1d9b22c9", want: ErrForeignMachine},
		{name: "empty prefix and empty name", prefix: "", machine: "", want: ErrForeignMachine},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := New(test.prefix).checkOwned(test.machine)
			if !errors.Is(err, test.want) {
				t.Errorf("checkOwned(%q) with prefix %q = %v, want %v", test.machine, test.prefix, err, test.want)
			}
		})
	}
}

func TestCheckMountable(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want error
	}{
		{name: "absolute path", dir: "/Users/dev/Project/api"},
		{name: "path with spaces", dir: "/Users/dev/My Project"},
		{name: "empty", dir: "", want: ErrMountPath},
		{name: "relative", dir: "Project/api", want: ErrMountPath},
		{name: "colon in the last name", dir: "/Users/dev/.ssh:x", want: ErrMountPath},
		{name: "colon in a parent", dir: "/Users/dev/a:/b", want: ErrMountPath},
		{name: "source and destination", dir: "/Users/dev/work:/Users/dev", want: ErrMountPath},
		{name: "comma in the last name", dir: "/Users/dev/a,b", want: ErrMountPath},
		{name: "comma in a parent", dir: "/Users/dev/a,/b", want: ErrMountPath},
		{name: "second mount after a comma", dir: "/Users/dev/work,/Users/dev:/Users/dev", want: ErrMountPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := checkMountable(test.dir); !errors.Is(err, test.want) {
				t.Errorf("checkMountable(%q) = %v, want %v", test.dir, err, test.want)
			}
		})
	}
}

// TestGuardsRunNoCommand checks that a refused call fails with its sentinel
// error. PATH holds no orb, so a call that reached the CLI would fail with
// another error.
func TestGuardsRunNoCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	client := New(testPrefix)
	ctx := context.Background()

	if err := client.Create(ctx, "ubuntu", "/Users/dev/Project/api"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("Create of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.Delete(ctx, "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("Delete of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if _, err := client.ActiveMounts(ctx, "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("ActiveMounts of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.RunAsRoot(ctx, "ubuntu", []byte("true\n")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsRoot in a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.RunAsRoot(ctx, "orbit-", []byte("true\n")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsRoot in a machine named only by the prefix = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.RunAsAgent(ctx, "ubuntu", []byte("true\n")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsAgent in a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.RunAsAgent(ctx, "orbit-", []byte("true\n")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsAgent in a machine named only by the prefix = %v, want %v", err, ErrForeignMachine)
	}
	if err := New("").RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true\n")); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("RunAsAgent of a client without a prefix = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/.ssh:x"); !errors.Is(err, ErrMountPath) {
		t.Errorf("Create with a colon in the directory = %v, want %v", err, ErrMountPath)
	}
	if err := client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/a,b"); !errors.Is(err, ErrMountPath) {
		t.Errorf("Create with a comma in the directory = %v, want %v", err, ErrMountPath)
	}
}

func TestFailedCommandCarriesStderr(t *testing.T) {
	useFakeCLI(t, "echo '[-32098] machine not found' >&2\nexit 3\n")
	client := New(testPrefix)
	ctx := context.Background()
	_, existsErr := client.Exists(ctx, "orbit-api-1d9b22c9")
	_, mountsErr := client.Mounts(ctx)
	_, activeMountsErr := client.ActiveMounts(ctx, "orbit-api-1d9b22c9")
	failures := map[string]error{
		"Create":       client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api"),
		"Exists":       existsErr,
		"Mounts":       mountsErr,
		"ActiveMounts": activeMountsErr,
		"Delete":       client.Delete(ctx, "orbit-api-1d9b22c9"),
		"RunAsRoot":    client.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
		"RunAsAgent":   client.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
	}

	for operation, err := range failures {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Errorf("%s = %v, want it to wrap the exit status 3", operation, err)
		}
		if err == nil || !strings.Contains(err.Error(), "[-32098] machine not found") {
			t.Errorf("%s = %v, want the standard error of orb in the text", operation, err)
		}
	}
}

func TestMissingCLIIsWrapped(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := New(testPrefix).Exists(context.Background(), "orbit-api-1d9b22c9")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Exists without orb = %v, want %v", err, exec.ErrNotFound)
	}
	if want := `list machines: orb status: exec: "orb": executable file not found in $PATH`; err.Error() != want {
		t.Errorf("Exists without orb = %q, want %q", err.Error(), want)
	}
}

func TestErrorTextOfAFailedCommand(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "orb prints an error",
			script: "echo ' [-32098] machine not found ' >&2\nexit 3\n",
			want:   "delete machine orbit-api-1d9b22c9: orb delete: exit status 3: [-32098] machine not found",
		},
		{
			name:   "orb prints nothing",
			script: "exit 3\n",
			want:   "delete machine orbit-api-1d9b22c9: orb delete: exit status 3",
		},
		{
			name:   "orb prints only white space",
			script: "echo '  ' >&2\nexit 3\n",
			want:   "delete machine orbit-api-1d9b22c9: orb delete: exit status 3",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useFakeCLI(t, test.script)
			err := New(testPrefix).Delete(context.Background(), "orbit-api-1d9b22c9")
			if err == nil || err.Error() != test.want {
				t.Errorf("Delete = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExistsReadsTheList(t *testing.T) {
	useFakeCLI(t, "echo orbit-api-1d9b22c9\necho ubuntu\n")
	client := New(testPrefix)
	tests := []struct {
		machine string
		want    bool
	}{
		{machine: "orbit-api-1d9b22c9", want: true},
		{machine: "orbit-web-00db83c9", want: false},
	}
	for _, test := range tests {
		got, err := client.Exists(context.Background(), test.machine)
		if err != nil || got != test.want {
			t.Errorf("Exists(%q) = %v, %v, want %v", test.machine, got, err, test.want)
		}
	}
}

// reportsRunning makes a fake orb report a running OrbStack to orb status.
const reportsRunning = "if [ \"$1\" = status ]; then echo Running; exit 0; fi\n"

// useFakeCLI makes a shell script with the given body the only orb on PATH.
// The script reports a running OrbStack to orb status.
func useFakeCLI(t *testing.T, body string) {
	t.Helper()
	useBareFakeCLI(t, reportsRunning+body)
}

// useBareFakeCLI makes a shell script with the given body, and nothing else,
// the only orb on PATH.
func useBareFakeCLI(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(dir, cli), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestStatusArgs(t *testing.T) {
	if got, want := statusArgs(), []string{"status"}; !slices.Equal(got, want) {
		t.Errorf("statusArgs() = %q, want %q", got, want)
	}
}

func TestStatusError(t *testing.T) {
	failed := errors.New("orb status: exit status 1")
	tests := []struct {
		name        string
		status      string
		err         error
		wantRunning bool
		wantStopped bool
	}{
		{name: "running", status: "Running", wantRunning: true},
		{name: "stopped", status: "Stopped", err: failed, wantStopped: true},
		{name: "starting", status: "Starting", err: errors.New("orb status: exit status 2"), wantStopped: true},
		{name: "stopped with exit status 0", status: "Stopped", wantStopped: true},
		{name: "running with a failed command", status: "Running", err: failed},
		{name: "nothing printed", status: ""},
		{name: "nothing printed and a failed command", status: "", err: failed},
		{name: "lower case", status: "running"},
		{name: "running with more text", status: "Running\nStopped"},
		{name: "unknown state", status: "Paused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := statusError(test.status, test.err)
			if (err == nil) != test.wantRunning {
				t.Errorf("statusError(%q, %v) = %v, want running %v", test.status, test.err, err, test.wantRunning)
			}
			if errors.Is(err, ErrNotRunning) != test.wantStopped {
				t.Errorf("statusError(%q, %v) = %v, want %v: %v", test.status, test.err, err, ErrNotRunning, test.wantStopped)
			}
		})
	}
}

// TestNoCallStartsAStoppedOrbStack checks that every operation fails with
// ErrNotRunning while orb status reports a stopped or starting OrbStack, and
// that it runs no orb command other than orb status: orb starts a stopped
// OrbStack by itself.
func TestNoCallStartsAStoppedOrbStack(t *testing.T) {
	tests := []struct {
		name   string
		status string
		exit   int
	}{
		{name: "stopped", status: "Stopped", exit: 1},
		{name: "starting", status: "Starting", exit: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			useBareFakeCLI(t, "printf '%s\\n' \"$1\" >> '"+calls+"'\n"+
				"if [ \"$1\" = status ]; then echo "+test.status+"; exit "+strconv.Itoa(test.exit)+"; fi\n")
			client := New(testPrefix)
			ctx := context.Background()
			_, existsErr := client.Exists(ctx, "orbit-api-1d9b22c9")
			_, mountsErr := client.Mounts(ctx)
			_, activeMountsErr := client.ActiveMounts(ctx, "orbit-api-1d9b22c9")
			results := map[string]error{
				"Create":       client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api"),
				"Exists":       existsErr,
				"Mounts":       mountsErr,
				"ActiveMounts": activeMountsErr,
				"Delete":       client.Delete(ctx, "orbit-api-1d9b22c9"),
				"RunAsRoot":    client.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
				"RunAsAgent":   client.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
			}

			for operation, err := range results {
				if !errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "start OrbStack") {
					t.Errorf("%s = %v, want %v with the advice to start OrbStack", operation, err, ErrNotRunning)
				}
			}
			received, err := os.ReadFile(calls)
			if want := strings.Repeat("status\n", len(results)); err != nil || string(received) != want {
				t.Errorf("orb ran %q, %v, want only orb status, once for each operation", received, err)
			}
		})
	}
}

func TestRunAsRootArgs(t *testing.T) {
	want := []string{"-m", "orbit-api-1d9b22c9", "-u", "root", "bash", "-s"}
	if got := runAsRootArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("runAsRootArgs() = %q, want %q", got, want)
	}
}

func TestRunAsRootPassesTheScriptOnStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "received")
	useFakeCLI(t, "printf '%s\\n' \"$*\" > '"+out+".args'\n/bin/cat > '"+out+"'\n")
	script := []byte("#!/bin/bash\necho 'it is $HOME' ; exit 0\n")
	if err := New(testPrefix).RunAsRoot(context.Background(), "orbit-api-1d9b22c9", script); err != nil {
		t.Fatalf("RunAsRoot = %v", err)
	}
	received, err := os.ReadFile(out)
	if err != nil || string(received) != string(script) {
		t.Errorf("stdin of orb = %q, %v, want %q", received, err, script)
	}
	args, err := os.ReadFile(out + ".args")
	if err != nil || string(args) != "-m orbit-api-1d9b22c9 -u root bash -s\n" {
		t.Errorf("arguments of orb = %q, %v", args, err)
	}
}

func TestRunAsAgentArgs(t *testing.T) {
	want := []string{"-m", "orbit-api-1d9b22c9", "-u", "agent", "bash", "-s"}
	if got := runAsAgentArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("runAsAgentArgs() = %q, want %q", got, want)
	}
}

// TestRunAsAgentPutsNoCallerValueOnTheCommandLine runs RunAsAgent with
// machine names and scripts that try to change the user or the command, and
// checks that orb gets the fixed arguments and the script only on standard
// input.
func TestRunAsAgentPutsNoCallerValueOnTheCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		machine string
		script  string
	}{
		{name: "plain", machine: "orbit-api-1d9b22c9", script: "id -un\n"},
		{name: "user in the script", machine: "orbit-api-1d9b22c9", script: "-u root\n"},
		{name: "flag in the name", machine: "orbit-x -u root", script: "true\n"},
		{name: "second command in the name", machine: "orbit-x; bash", script: "true\n"},
		{name: "quotes in the script", machine: "orbit-api-1d9b22c9", script: "echo 'it is $HOME' \"$(id -u)\"\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "received")
			useFakeCLI(t, "for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done > '"+out+".args'\n/bin/cat > '"+out+"'\n")

			if err := New(testPrefix).RunAsAgent(context.Background(), test.machine, []byte(test.script)); err != nil {
				t.Fatalf("RunAsAgent = %v", err)
			}

			received, err := os.ReadFile(out)
			if err != nil || string(received) != test.script {
				t.Errorf("stdin of orb = %q, %v, want %q", received, err, test.script)
			}
			args, err := os.ReadFile(out + ".args")
			want := strings.Join([]string{"-m", test.machine, "-u", "agent", "bash", "-s"}, "\n") + "\n"
			if err != nil || string(args) != want {
				t.Errorf("arguments of orb = %q, %v, want %q", args, err, want)
			}
		})
	}
}
