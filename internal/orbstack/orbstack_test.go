package orbstack

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	_, stateErr := client.State(ctx, "orbit-api-1d9b22c9")
	failures := map[string]error{
		"Create":       client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api"),
		"Exists":       existsErr,
		"Mounts":       mountsErr,
		"ActiveMounts": activeMountsErr,
		"State":        stateErr,
		"Start":        client.Start(ctx, "orbit-api-1d9b22c9"),
		"Stop":         client.Stop(ctx, "orbit-api-1d9b22c9"),
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
			err := statusError(test.status, test.err, "")
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
			useAccountHome(t)
			_, existsErr := client.Exists(ctx, "orbit-api-1d9b22c9")
			_, mountsErr := client.Mounts(ctx)
			_, activeMountsErr := client.ActiveMounts(ctx, "orbit-api-1d9b22c9")
			_, stateErr := client.State(ctx, "orbit-api-1d9b22c9")
			_, shellErr := client.Shell(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api", strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			results := map[string]error{
				"Create":       client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api"),
				"Exists":       existsErr,
				"Mounts":       mountsErr,
				"ActiveMounts": activeMountsErr,
				"State":        stateErr,
				"Start":        client.Start(ctx, "orbit-api-1d9b22c9"),
				"Stop":         client.Stop(ctx, "orbit-api-1d9b22c9"),
				"Delete":       client.Delete(ctx, "orbit-api-1d9b22c9"),
				"RunAsRoot":    client.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
				"RunAsAgent":   client.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
				"Shell":        shellErr,
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

// useAccountHome makes HOME a new directory with .orbstack in it, and the
// home directory of the account the same directory. It returns that
// directory.
func useAccountHome(t *testing.T) string {
	t.Helper()
	dir := hostDir(t)
	if err := os.Mkdir(filepath.Join(dir, stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	useAccount(t, dir)
	return dir
}

// useAccount makes accountHome the home directory of the account for the
// test.
func useAccount(t *testing.T, accountHome string) {
	t.Helper()
	previous := currentUser
	currentUser = func() (*user.User, error) { return &user.User{HomeDir: accountHome}, nil }
	t.Cleanup(func() { currentUser = previous })
}

// hostDir returns the canonical path of a new directory on the host.
func hostDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatusErrorNamesHome(t *testing.T) {
	cause := `HOME "/tmp/x" is not the home directory of the account, "/Users/dev"`
	failed := errors.New("orb status: exit status 1")
	tests := []struct {
		name     string
		status   string
		err      error
		wantHome bool
	}{
		{name: "stopped", status: "Stopped", err: failed, wantHome: true},
		{name: "starting", status: "Starting", err: failed, wantHome: true},
		{name: "running", status: "Running"},
		{name: "unknown state", status: "Paused"},
		{name: "nothing printed and a failed command", status: "", err: failed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := statusError(test.status, test.err, cause)
			if errors.Is(err, ErrHome) != test.wantHome {
				t.Errorf("statusError(%q, %v, cause) = %v, want %v: %v", test.status, test.err, err, ErrHome, test.wantHome)
			}
			if errors.Is(err, ErrNotRunning) {
				t.Errorf("statusError(%q, %v, cause) = %v, want no %v while HOME is the cause", test.status, test.err, err, ErrNotRunning)
			}
			if test.wantHome && !strings.Contains(err.Error(), cause) {
				t.Errorf("statusError(%q, %v, cause) = %v, want the cause in the text", test.status, test.err, err)
			}
		})
	}
}

func TestHomeCause(t *testing.T) {
	tests := []struct {
		name string
		home home
		want string
	}{
		{name: "account home with its state", home: home{env: "/Users/dev", account: "/Users/dev", hasStateDir: true}},
		{name: "unknown account with a state", home: home{env: "/Users/dev", hasStateDir: true}},
		{name: "another HOME", home: home{env: "/private/tmp/x", account: "/Users/dev"}, want: `HOME "/private/tmp/x" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
		{name: "another HOME with a state", home: home{env: "/private/tmp/x", account: "/Users/dev", hasStateDir: true}, want: `HOME "/private/tmp/x" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
		{name: "account home without its state", home: home{env: "/Users/dev", account: "/Users/dev"}, want: `"/Users/dev/.orbstack" is not a directory: start OrbStack once as this account, or set HOME to the directory that holds .orbstack, and try again`},
		{name: "unknown account without a state", home: home{env: "/private/tmp/x"}, want: `"/private/tmp/x/.orbstack" is not a directory: start OrbStack once as this account, or set HOME to the directory that holds .orbstack, and try again`},
		{name: "empty HOME", home: home{account: "/Users/dev"}, want: `HOME "" is not an absolute path: set HOME to the home directory of the account and try again`},
		{name: "relative HOME", home: home{env: "Users/dev", account: "/Users/dev", hasStateDir: true}, want: `HOME "Users/dev" is not an absolute path: set HOME to the home directory of the account and try again`},
		{name: "HOME of a parent of the account home", home: home{env: "/Users", account: "/Users/dev", hasStateDir: true}, want: `HOME "/Users" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
		{name: "HOME with a prefix of the account home", home: home{env: "/Users/de", account: "/Users/dev", hasStateDir: true}, want: `HOME "/Users/de" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
		{name: "HOME with a newline", home: home{env: "/Users/dev\n/x", account: "/Users/dev"}, want: `HOME "/Users/dev\n/x" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
		{name: "HOME with a control character", home: home{env: "/Users/dev\x1b[2J", account: "/Users/dev"}, want: `HOME "/Users/dev\x1b[2J" is not the home directory of the account, "/Users/dev": set HOME to that directory and try again`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := homeCause(test.home); got != test.want {
				t.Errorf("homeCause(%+v) = %q, want %q", test.home, got, test.want)
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	dir := hostDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want string
	}{
		{path: dir, want: dir},
		{path: dir + "/", want: dir},
		{path: dir + "/./x/..", want: dir},
		{path: link, want: dir},
		{path: "/nonexistent/a/../b/", want: "/nonexistent/b"},
		{path: "", want: ""},
		{path: "relative/dir", want: "relative/dir"},
		{path: "./", want: "./"},
	}
	for _, test := range tests {
		if got := canonical(test.path); got != test.want {
			t.Errorf("canonical(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestReadHome(t *testing.T) {
	account := useAccountHome(t)
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(account, link); err != nil {
		t.Fatal(err)
	}
	other := hostDir(t)
	stateFile := hostDir(t)
	if err := os.WriteFile(filepath.Join(stateFile, stateDir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		env  string
		want home
	}{
		{name: "account home", env: account, want: home{env: account, account: account, hasStateDir: true}},
		{name: "symlink to the account home", env: link, want: home{env: account, account: account, hasStateDir: true}},
		{name: "account home with a trailing slash", env: account + "/", want: home{env: account, account: account, hasStateDir: true}},
		{name: "another directory", env: other, want: home{env: other, account: account}},
		{name: ".orbstack is a file", env: stateFile, want: home{env: stateFile, account: account}},
		{name: "empty HOME", env: "", want: home{account: account}},
		{name: "relative HOME", env: ".", want: home{env: ".", account: account}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.env)
			if got := readHome(); got != test.want {
				t.Errorf("readHome() with HOME %q = %+v, want %+v", test.env, got, test.want)
			}
		})
	}
}

func TestSameDirOrSelf(t *testing.T) {
	dir := hostDir(t)
	other := hostDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		path  string
		other string
		want  string
	}{
		{name: "same spelling", path: dir, other: dir, want: dir},
		{name: "another spelling of the same directory", path: dir, other: dir + "/.", want: dir + "/."},
		{name: "symlink to the same directory", path: dir, other: link, want: link},
		{name: "another directory", path: dir, other: other, want: dir},
		{name: "missing other", path: dir, other: "/nonexistent", want: dir},
		{name: "missing path", path: "/nonexistent", other: dir, want: "/nonexistent"},
		{name: "empty other", path: dir, other: "", want: dir},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameDirOrSelf(test.path, test.other); got != test.want {
				t.Errorf("sameDirOrSelf(%q, %q) = %q, want %q", test.path, test.other, got, test.want)
			}
		})
	}
}

func TestReadHomeWithoutAnAccount(t *testing.T) {
	dir := hostDir(t)
	t.Setenv("HOME", dir)
	previous := currentUser
	currentUser = func() (*user.User, error) { return nil, errors.New("no such user") }
	t.Cleanup(func() { currentUser = previous })

	if got, want := readHome(), (home{env: dir}); got != want {
		t.Errorf("readHome() without an account = %+v, want %+v", got, want)
	}
}

// TestStoppedUnderAnotherHomeNamesHome checks that every operation fails
// with ErrHome, not ErrNotRunning, when orb status prints Stopped while HOME
// does not lead orb to OrbStack, and that it runs no orb command other than
// orb status.
func TestStoppedUnderAnotherHomeNamesHome(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T)
		wantCause string
	}{
		{
			name: "HOME is an empty directory",
			setup: func(t *testing.T) {
				t.Setenv("HOME", hostDir(t))
				useAccount(t, hostDir(t))
			},
			wantCause: "is not the home directory of the account",
		},
		{
			name: "HOME is another directory with .orbstack",
			setup: func(t *testing.T) {
				useAccountHome(t)
				useAccount(t, hostDir(t))
			},
			wantCause: "is not the home directory of the account",
		},
		{
			name: "account home without .orbstack",
			setup: func(t *testing.T) {
				dir := hostDir(t)
				t.Setenv("HOME", dir)
				useAccount(t, dir)
			},
			wantCause: `.orbstack" is not a directory`,
		},
		{
			name: "HOME is unset",
			setup: func(t *testing.T) {
				t.Setenv("HOME", "")
				if err := os.Unsetenv("HOME"); err != nil {
					t.Fatal(err)
				}
			},
			wantCause: `HOME "" is not an absolute path`,
		},
		{
			name: "HOME is relative",
			setup: func(t *testing.T) {
				t.Setenv("HOME", ".")
			},
			wantCause: `HOME "." is not an absolute path`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			useBareFakeCLI(t, "printf '%s\\n' \"$1\" >> '"+calls+"'\n"+
				"if [ \"$1\" = status ]; then echo Stopped; exit 1; fi\n")
			test.setup(t)
			client := New(testPrefix)
			ctx := context.Background()
			_, existsErr := client.Exists(ctx, "orbit-api-1d9b22c9")
			_, mountsErr := client.Mounts(ctx)
			_, activeMountsErr := client.ActiveMounts(ctx, "orbit-api-1d9b22c9")
			_, shellErr := client.Shell(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api", strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			results := map[string]error{
				"Create":       client.Create(ctx, "orbit-api-1d9b22c9", "/Users/dev/Project/api"),
				"Exists":       existsErr,
				"Mounts":       mountsErr,
				"ActiveMounts": activeMountsErr,
				"Delete":       client.Delete(ctx, "orbit-api-1d9b22c9"),
				"RunAsRoot":    client.RunAsRoot(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
				"RunAsAgent":   client.RunAsAgent(ctx, "orbit-api-1d9b22c9", []byte("true\n")),
				"Shell":        shellErr,
			}

			for operation, err := range results {
				if !errors.Is(err, ErrHome) || errors.Is(err, ErrNotRunning) {
					t.Errorf("%s = %v, want %v and not %v", operation, err, ErrHome, ErrNotRunning)
					continue
				}
				if strings.Contains(err.Error(), ErrNotRunning.Error()) || !strings.Contains(err.Error(), test.wantCause) {
					t.Errorf("%s = %q, want %q in the text and not %q", operation, err, test.wantCause, ErrNotRunning)
				}
			}
			received, err := os.ReadFile(calls)
			if want := strings.Repeat("status\n", len(results)); err != nil || string(received) != want {
				t.Errorf("orb ran %q, %v, want only orb status, once for each operation", received, err)
			}
		})
	}
}

// TestRunningUnderAnotherHomeRuns checks that the client does not refuse a
// HOME other than the account home when orb status reports a running
// OrbStack through it.
func TestRunningUnderAnotherHomeRuns(t *testing.T) {
	useFakeCLI(t, "echo orbit-api-1d9b22c9\n")
	t.Setenv("HOME", hostDir(t))
	useAccount(t, hostDir(t))

	if exists, err := New(testPrefix).Exists(context.Background(), "orbit-api-1d9b22c9"); err != nil || !exists {
		t.Errorf("Exists under another HOME = %v, %v, want true, nil", exists, err)
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

// TestAllowedVariablesAreTheDecidedList pins the allowlist of #76 to its 13
// names, so that a name added to or removed from allowedVariables fails a
// test and a reviewer sees the change of the boundary (R08).
func TestAllowedVariablesAreTheDecidedList(t *testing.T) {
	want := []string{
		"HOME", "PATH", "TMPDIR",
		"TERM", "COLORTERM",
		"LANG", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
	}
	if !slices.Equal(allowedVariables, want) {
		t.Errorf("allowedVariables = %q, want exactly %q", allowedVariables, want)
	}
}

func TestAllowedEnvironment(t *testing.T) {
	allowed := []string{
		"HOME=/Users/dev", "PATH=/usr/bin:/bin", "TMPDIR=/var/folders/x/T/", "TERM=xterm-256color", "COLORTERM=truecolor",
		"LANG=en_US.UTF-8", "LC_ALL=C.UTF-8", "LC_COLLATE=C", "LC_CTYPE=UTF-8", "LC_MESSAGES=C", "LC_MONETARY=C", "LC_NUMERIC=C", "LC_TIME=C",
	}
	tests := []struct {
		name    string
		environ []string
		want    []string
	}{
		{
			name:    "every allowed variable",
			environ: allowed,
			want:    allowed,
		},
		{
			name:    "ORBENV and WSLENV naming a secret",
			environ: []string{"HOME=/Users/dev", "ORBENV=TOKEN:HOME", "WSLENV=TOKEN/u", "TOKEN=secret"},
			want:    []string{"HOME=/Users/dev"},
		},
		{
			name: "credentials and endpoints of the host",
			environ: []string{
				"HTTP_PROXY=http://u:pw@p:1", "https_proxy=http://u:pw@p:1", "ALL_PROXY=socks5://u:pw@p:1", "NO_PROXY=localhost",
				"DOCKER_HOST=ssh://u@host", "DOCKER_AUTH_CONFIG={\"auths\":{\"r\":{\"auth\":\"dTpwdw==\"}}}", "docker_host=x",
				"ELECTRON_RUN_AS_NODE=1", "SSH_AUTH_SOCK=/private/tmp/agent", "GITHUB_TOKEN=ghp_x", "AWS_SECRET_ACCESS_KEY=x",
				"ANTHROPIC_API_KEY=x", "CLAUDE_CODE_OAUTH_TOKEN=x", "HOME=/Users/dev",
			},
			want: []string{"HOME=/Users/dev"},
		},
		{
			name: "values that orb forwards but that are not allowed",
			environ: []string{
				"TERM_PROGRAM=iTerm.app", "TERM_PROGRAM_VERSION=3", "ITERM_SESSION_ID=x", "WEZTERM_PANE=1",
				"__CFBundleIdentifier=x", "__CF_USER_TEXT_ENCODING=0x1F5", "COMMAND_MODE=unix2003", "TERM=dumb",
			},
			want: []string{"TERM=dumb"},
		},
		{
			name: "names near an allowed name",
			environ: []string{
				"home=/x", "Home=/x", "path=/x", "Term=x", "HOME_X=/x", "XHOME=/x", " HOME=/x", "HOME =/x",
				"LC_X=x", "LC_=x", "LC_all=x", "LANGUAGE=x", "LANG_X=x", "TMP=/x", "TEMP=/x", "USER=dev", "LOGNAME=dev", "SHELL=/bin/zsh",
			},
			want: []string{},
		},
		{
			name:    "allowed name in a value or without a name",
			environ: []string{"NOTE=HOME=/x", "=HOME", "=PATH=/x", "ORBENV", "TOKEN"},
			want:    []string{},
		},
		{
			name:    "value that holds an equals sign and duplicates in order",
			environ: []string{"TERM=a=b", "GITHUB_TOKEN=x", "TERM=", "HOME=/a", "HOME=/b"},
			want:    []string{"TERM=a=b", "TERM=", "HOME=/a", "HOME=/b"},
		},
		{
			name:    "empty environment",
			environ: []string{},
			want:    []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environ := slices.Clone(test.environ)
			got := allowedEnvironment(environ)
			if !slices.Equal(got, test.want) {
				t.Errorf("allowedEnvironment(%q) = %q, want %q", test.environ, got, test.want)
			}
			if !slices.Equal(environ, test.environ) {
				t.Errorf("allowedEnvironment changed its argument to %q", environ)
			}
		})
	}
}

func TestStartArgs(t *testing.T) {
	want := []string{"start", "orbit-api-1d9b22c9"}
	if got := startArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("startArgs() = %q, want %q", got, want)
	}
}

func TestStopArgs(t *testing.T) {
	want := []string{"stop", "orbit-api-1d9b22c9"}
	if got := stopArgs("orbit-api-1d9b22c9"); !slices.Equal(got, want) {
		t.Errorf("stopArgs() = %q, want %q", got, want)
	}
}

// TestStartAndStopRefuseHostileNamesBeforeAnyCommand checks that Start and
// Stop never reach orb with a name that is not one machine of the client:
// orb stop without a machine stops OrbStack with every machine, and orb
// start without one starts every machine that ran when OrbStack last
// stopped.
func TestStartAndStopRefuseHostileNamesBeforeAnyCommand(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		machine string
		want    error
	}{
		{name: "empty name", prefix: testPrefix, machine: "", want: ErrForeignMachine},
		{name: "only the prefix", prefix: testPrefix, machine: "orbit-", want: ErrForeignMachine},
		{name: "short flag", prefix: testPrefix, machine: "-a", want: ErrForeignMachine},
		{name: "long flag", prefix: testPrefix, machine: "--all", want: ErrForeignMachine},
		{name: "force flag", prefix: testPrefix, machine: "-f", want: ErrForeignMachine},
		{name: "name and a flag", prefix: testPrefix, machine: "orbit-x --all", want: ErrMachineName},
		{name: "name with a tab", prefix: testPrefix, machine: "orbit-x\t-a", want: ErrMachineName},
		{name: "name with a newline", prefix: testPrefix, machine: "orbit-x\n", want: ErrMachineName},
		{name: "foreign name", prefix: testPrefix, machine: "ubuntu", want: ErrForeignMachine},
		{name: "empty client prefix", prefix: "", machine: "orbit-api-1d9b22c9", want: ErrForeignMachine},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			useFakeCLI(t, "printf '%s\\n' \"$*\" >> '"+calls+"'\n")
			client := New(test.prefix)
			ctx := context.Background()
			if err := client.Start(ctx, test.machine); !errors.Is(err, test.want) {
				t.Errorf("Start(%q) = %v, want %v", test.machine, err, test.want)
			}
			if err := client.Stop(ctx, test.machine); !errors.Is(err, test.want) {
				t.Errorf("Stop(%q) = %v, want %v", test.machine, err, test.want)
			}
			if received, err := os.ReadFile(calls); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("orb ran %q, %v, want no command", received, err)
			}
		})
	}
}

// shellVariables are the variables that the shell of the fake orb sets by
// itself, so they are in the environment that it records.
var shellVariables = []string{"PWD", "OLDPWD", "SHLVL", "_"}

// TestEveryCommandGetsOnlyAllowedVariables sets every allowed variable and
// forbidden ones: credentials, endpoints of the host, values that orb
// forwards, names near an allowed name, and ORBENV and WSLENV naming a
// variable of the test process. It runs every operation of the client
// against a fake orb that records its environment, and checks that every
// command of orb, orb status included, gets every allowed variable and no
// other one.
func TestEveryCommandGetsOnlyAllowedVariables(t *testing.T) {
	const machineName = "orbit-api-1d9b22c9"
	dir := t.TempDir()
	useBareFakeCLI(t, "/usr/bin/env > \"$(/usr/bin/mktemp '"+dir+"/env.XXXXXX')\"\n"+
		reportsRunning+
		"if [ \"$1\" = list ] && [ \"$2\" = --format ]; then echo '[]'; exit 0; fi\n"+
		"if [ \"$5\" = cat ]; then echo '195 125 0:35 /p /p rw - virtiofs mac rw'; fi\n")
	for _, name := range allowedVariables {
		if name != "HOME" && name != "PATH" {
			t.Setenv(name, "allowed-"+name)
		}
	}
	forbidden := []string{
		"ORBIT_TEST_SECRET", "HTTPS_PROXY", "http_proxy", "DOCKER_HOST", "DOCKER_AUTH_CONFIG", "ELECTRON_RUN_AS_NODE",
		"SSH_AUTH_SOCK", "GITHUB_TOKEN", "TERM_PROGRAM", "__CFBundleIdentifier", "home", "HOME_X", "LC_X", "USER",
	}
	for _, name := range forbidden {
		t.Setenv(name, "http://orbit:forwarding-marker@proxy.invalid:3128")
	}
	t.Setenv("ORBENV", "ORBIT_TEST_SECRET:HOME")
	t.Setenv("WSLENV", "ORBIT_TEST_SECRET/u")
	client := New(testPrefix)
	ctx := context.Background()

	_, existsErr := client.Exists(ctx, machineName)
	_, mountsErr := client.Mounts(ctx)
	_, activeMountsErr := client.ActiveMounts(ctx, machineName)
	_, stateErr := client.State(ctx, machineName)
	operations := map[string]error{
		"Create":       client.Create(ctx, machineName, "/Users/dev/Project/api"),
		"Exists":       existsErr,
		"Mounts":       mountsErr,
		"ActiveMounts": activeMountsErr,
		"State":        stateErr,
		"Start":        client.Start(ctx, machineName),
		"Stop":         client.Stop(ctx, machineName),
		"Delete":       client.Delete(ctx, machineName),
		"RunAsRoot":    client.RunAsRoot(ctx, machineName, []byte("true\n")),
		"RunAsAgent":   client.RunAsAgent(ctx, machineName, []byte("true\n")),
	}
	for operation, err := range operations {
		if err != nil {
			t.Errorf("%s = %v", operation, err)
		}
	}

	records, err := filepath.Glob(filepath.Join(dir, "env.*"))
	if err != nil || len(records) != 2*len(operations) {
		t.Fatalf("recorded environments = %d, %v, want %d", len(records), err, 2*len(operations))
	}
	for _, record := range records {
		content, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		environ := strings.Split(strings.TrimSpace(string(content)), "\n")
		for _, entry := range environ {
			if name, _, _ := strings.Cut(entry, "="); !slices.Contains(allowedVariables, name) && !slices.Contains(shellVariables, name) {
				t.Errorf("orb got %q", entry)
			}
		}
		for _, name := range allowedVariables {
			if kept := name + "=" + os.Getenv(name); !slices.Contains(environ, kept) {
				t.Errorf("orb did not get %q", kept)
			}
		}
	}
}

func TestStartAndStopRunOrbWithTheName(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	useFakeCLI(t, "printf '%s\\n' \"$*\" >> '"+calls+"'\n")
	client := New(testPrefix)
	ctx := context.Background()
	if err := client.Stop(ctx, "orbit-api-1d9b22c9"); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if err := client.Start(ctx, "orbit-api-1d9b22c9"); err != nil {
		t.Fatalf("Start = %v", err)
	}
	received, err := os.ReadFile(calls)
	if want := "stop orbit-api-1d9b22c9\nstart orbit-api-1d9b22c9\n"; err != nil || string(received) != want {
		t.Errorf("orb ran %q, %v, want %q", received, err, want)
	}
}

func TestParseState(t *testing.T) {
	list := `[
		{"name":"ubuntu","state":"stopped","config":{}},
		{"name":"orbit-api-1d9b22c9","state":"running","config":{}},
		{"name":"orbit-web-00db83c9","state":"stopped","config":{}},
		{"name":"orbit-new-11111111","state":"starting","config":{}}
	]`
	tests := []struct {
		machine string
		want    string
	}{
		{machine: "orbit-api-1d9b22c9", want: "running"},
		{machine: "orbit-web-00db83c9", want: "stopped"},
		{machine: "orbit-new-11111111", want: "starting"},
		{machine: "orbit-gone-22222222", want: ""},
	}
	for _, test := range tests {
		got, err := parseState([]byte(list), test.machine)
		if err != nil || got != test.want {
			t.Errorf("parseState(%q) = %q, %v, want %q", test.machine, got, err, test.want)
		}
	}
}

func TestParseStateRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		list string
	}{
		{name: "not JSON", list: "orbit-api-1d9b22c9 running"},
		{name: "not a list", list: `{"name":"orbit-api-1d9b22c9","state":"running"}`},
		{name: "name twice", list: `[{"name":"orbit-api-1d9b22c9","state":"stopped"},{"name":"orbit-api-1d9b22c9","state":"running"}]`},
		{name: "no state", list: `[{"name":"orbit-api-1d9b22c9"}]`},
		{name: "state not a string", list: `[{"name":"orbit-api-1d9b22c9","state":1}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseState([]byte(test.list), "orbit-api-1d9b22c9")
			if !errors.Is(err, ErrMachineList) {
				t.Errorf("parseState(%s) = %q, %v, want %v", test.list, got, err, ErrMachineList)
			}
		})
	}
}

func TestStateReadsTheListAndStartsNothing(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	useFakeCLI(t, "printf '%s\\n' \"$*\" >> '"+calls+"'\n"+`echo '[{"name":"orbit-api-1d9b22c9","state":"stopped"}]'`+"\n")
	got, err := New(testPrefix).State(context.Background(), "orbit-api-1d9b22c9")
	if err != nil || got != "stopped" {
		t.Errorf("State = %q, %v, want %q", got, err, "stopped")
	}
	received, err := os.ReadFile(calls)
	if want := "list --format json\n"; err != nil || string(received) != want {
		t.Errorf("orb ran %q, %v, want only %q", received, err, want)
	}
}

func TestStateRefusesAForeignMachine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := New(testPrefix).State(context.Background(), "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("State of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
}

func TestShellArgs(t *testing.T) {
	want := []string{"-m", "orbit-api-1d9b22c9", "-u", "agent", "-w", "/Users/dev/Project/api"}
	if got := shellArgs("orbit-api-1d9b22c9", "/Users/dev/Project/api"); !slices.Equal(got, want) {
		t.Errorf("shellArgs() = %q, want %q", got, want)
	}
}

// TestShellRefusesHostileInputBeforeAnyCommand checks that Shell never
// reaches orb with a machine that is not one of the client or a directory
// that could change the user, add a flag, or name another mount.
func TestShellRefusesHostileInputBeforeAnyCommand(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		machine string
		dir     string
		want    error
	}{
		{name: "foreign name", prefix: testPrefix, machine: "ubuntu", dir: "/Users/dev/api", want: ErrForeignMachine},
		{name: "empty name", prefix: testPrefix, machine: "", dir: "/Users/dev/api", want: ErrForeignMachine},
		{name: "only the prefix", prefix: testPrefix, machine: "orbit-", dir: "/Users/dev/api", want: ErrForeignMachine},
		{name: "flag as the name", prefix: testPrefix, machine: "-u", dir: "/Users/dev/api", want: ErrForeignMachine},
		{name: "empty client prefix", prefix: "", machine: "orbit-api-1d9b22c9", dir: "/Users/dev/api", want: ErrForeignMachine},
		{name: "empty directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "", want: ErrMountPath},
		{name: "relative directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "api", want: ErrMountPath},
		{name: "user flag as the directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "-uroot", want: ErrMountPath},
		{name: "long flag as the directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "--user=root", want: ErrMountPath},
		{name: "colon in the directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "/Users/dev/.ssh:x", want: ErrMountPath},
		{name: "comma in the directory", prefix: testPrefix, machine: "orbit-api-1d9b22c9", dir: "/Users/dev/a,b", want: ErrMountPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			useFakeCLI(t, "printf '%s\\n' \"$*\" >> '"+calls+"'\n")

			status, err := New(test.prefix).Shell(context.Background(), test.machine, test.dir, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

			if !errors.Is(err, test.want) || status != 0 {
				t.Errorf("Shell(%q, %q) = %d, %v, want 0, %v", test.machine, test.dir, status, err, test.want)
			}
			if received, err := os.ReadFile(calls); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("orb ran %q, %v, want no command", received, err)
			}
		})
	}
}

// TestShellAttachesTheStreamsAndReturnsTheStatus checks that orb gets the
// fixed arguments, reads the input of the caller, writes to the output and
// the error stream of the caller, and that Shell returns its exit status
// without an error.
func TestShellAttachesTheStreamsAndReturnsTheStatus(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	useFakeCLI(t, "for arg in \"$@\"; do printf '%s\\n' \"$arg\"; done > '"+args+"'\n/bin/cat\necho diagnostics >&2\nexit 3\n")
	var out, errOut bytes.Buffer

	status, err := New(testPrefix).Shell(context.Background(), "orbit-api-1d9b22c9", "/Users/dev/Project/api", strings.NewReader("id -un\nexit 3\n"), &out, &errOut)

	if err != nil || status != 3 {
		t.Errorf("Shell = %d, %v, want 3, nil", status, err)
	}
	if out.String() != "id -un\nexit 3\n" || errOut.String() != "diagnostics\n" {
		t.Errorf("output = %q, error stream = %q, want the input and %q", out.String(), errOut.String(), "diagnostics\n")
	}
	received, err := os.ReadFile(args)
	if want := "-m\norbit-api-1d9b22c9\n-u\nagent\n-w\n/Users/dev/Project/api\n"; err != nil || string(received) != want {
		t.Errorf("arguments of orb = %q, %v, want %q", received, err, want)
	}
}

// TestShellGivesOrbTheDefaultActionOfSIGINT checks that orb can be ended by
// SIGINT while Shell holds the signal, and that the status reports the
// signal as a shell does.
func TestShellGivesOrbTheDefaultActionOfSIGINT(t *testing.T) {
	useFakeCLI(t, "kill -INT $$\nexit 0\n")

	status, err := New(testPrefix).Shell(context.Background(), "orbit-api-1d9b22c9", "/Users/dev/Project/api", strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	if err != nil || status != 128+int(syscall.SIGINT) {
		t.Errorf("Shell with orb ended by SIGINT = %d, %v, want %d, nil", status, err, 128+int(syscall.SIGINT))
	}
}

// TestHoldingSignalsKeepsTheProcessAlive sends SIGINT and SIGQUIT to the
// test process while holdingSignals runs, and waits until a second channel
// notified of both signals receives them, so that both arrive before the
// hold ends. Without the hold, either signal ends the test binary.
func TestHoldingSignalsKeepsTheProcessAlive(t *testing.T) {
	sent := []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT}
	err := holdingSignals(func() error {
		received := make(chan os.Signal, len(sent))
		signal.Notify(received, syscall.SIGINT, syscall.SIGQUIT)
		defer signal.Stop(received)
		for _, sig := range sent {
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				return err
			}
		}
		return awaitSignals(received, len(sent))
	})
	if err != nil {
		t.Fatalf("holdingSignals = %v", err)
	}
}

// awaitSignals waits until received delivers count signals, or fails after
// a deadline that only a lost signal reaches.
func awaitSignals(received <-chan os.Signal, count int) error {
	deadline := time.After(10 * time.Second)
	for range count {
		select {
		case <-received:
		case <-deadline:
			return errors.New("signals not delivered within 10s")
		}
	}
	return nil
}

func TestExitStatus(t *testing.T) {
	exited := exec.Command("sh", "-c", "exit 5").Run()
	signaled := exec.Command("sh", "-c", "kill -TERM $$").Run()
	notRun := errors.New("exec: not started")
	tests := []struct {
		name    string
		err     error
		want    int
		wantErr error
	}{
		{name: "success", err: nil, want: 0},
		{name: "exit code", err: exited, want: 5},
		{name: "signal", err: signaled, want: 128 + int(syscall.SIGTERM)},
		{name: "not run", err: notRun, want: 0, wantErr: notRun},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := exitStatus(test.err)
			if got != test.want || !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
				t.Errorf("exitStatus(%v) = %d, %v, want %d, %v", test.err, got, err, test.want, test.wantErr)
			}
		})
	}
}

// TestShellFailsWhenOrbCannotRun removes orb after orb status, so that the
// shell command cannot start, and expects an error instead of a status.
func TestShellFailsWhenOrbCannotRun(t *testing.T) {
	useBareFakeCLI(t, "if [ \"$1\" = status ]; then echo Running; /bin/rm \"$0\"; exit 0; fi\nexit 9\n")

	status, err := New(testPrefix).Shell(context.Background(), "orbit-api-1d9b22c9", "/Users/dev/Project/api", strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	if !errors.Is(err, exec.ErrNotFound) || status != 0 || !strings.Contains(err.Error(), "run a shell as agent in machine orbit-api-1d9b22c9") {
		t.Errorf("Shell without orb = %d, %v, want 0 and %v", status, err, exec.ErrNotFound)
	}
}

// TestEveryCommandRunsOrbWithTheSameEnvironment runs every operation of the
// client, Shell included, against a fake orb that records its environment,
// with ORBENV naming a secret of the test process. It checks that every
// command of orb, orb status included, gets the same environment, so that a
// filter of the environment covers Shell only when it sits where every
// command is built: in executeStreams.
func TestEveryCommandRunsOrbWithTheSameEnvironment(t *testing.T) {
	const machineName = "orbit-api-1d9b22c9"
	const projectDir = "/Users/dev/Project/api"
	dir := t.TempDir()
	useBareFakeCLI(t, "/usr/bin/env > \"$(/usr/bin/mktemp '"+dir+"/env.XXXXXX')\"\n"+
		reportsRunning+
		"if [ \"$1\" = list ] && [ \"$2\" = --format ]; then echo '[]'; exit 0; fi\n"+
		"if [ \"$5\" = cat ]; then echo '195 125 0:35 /p /p rw - virtiofs mac rw'; fi\n")
	t.Setenv("ORBIT_TEST_SECRET", "forwarding-marker")
	t.Setenv("ORBENV", "ORBIT_TEST_SECRET")
	client := New(testPrefix)
	ctx := context.Background()

	_, existsErr := client.Exists(ctx, machineName)
	_, mountsErr := client.Mounts(ctx)
	_, activeMountsErr := client.ActiveMounts(ctx, machineName)
	_, stateErr := client.State(ctx, machineName)
	_, shellErr := client.Shell(ctx, machineName, projectDir, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	operations := map[string]error{
		"Create":       client.Create(ctx, machineName, projectDir),
		"Exists":       existsErr,
		"Mounts":       mountsErr,
		"ActiveMounts": activeMountsErr,
		"State":        stateErr,
		"Start":        client.Start(ctx, machineName),
		"Stop":         client.Stop(ctx, machineName),
		"Delete":       client.Delete(ctx, machineName),
		"RunAsRoot":    client.RunAsRoot(ctx, machineName, []byte("true\n")),
		"RunAsAgent":   client.RunAsAgent(ctx, machineName, []byte("true\n")),
		"Shell":        shellErr,
	}
	for operation, err := range operations {
		if err != nil {
			t.Errorf("%s = %v", operation, err)
		}
	}

	records, err := filepath.Glob(filepath.Join(dir, "env.*"))
	if err != nil || len(records) != 2*len(operations) {
		t.Fatalf("recorded environments = %d, %v, want %d", len(records), err, 2*len(operations))
	}
	first, err := os.ReadFile(records[0])
	if err != nil || len(first) == 0 {
		t.Fatalf("first recorded environment = %q, %v", first, err)
	}
	for _, record := range records[1:] {
		content, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, first) {
			t.Errorf("environment of orb differs between commands:\n%s\n---\n%s", first, content)
		}
	}
}
