package orbstack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
			dir:     "/Users/jacob/Project/api",
			want: []string{
				"create", "--isolated", "--isolate-network",
				"--mount", "/Users/jacob/Project/api:/Users/jacob/Project/api",
				"ubuntu:26.04", "orbit-api-1d9b22c9",
			},
		},
		{
			name:    "directory with spaces and shell characters",
			machine: "orbit-my-project-00db83c9",
			dir:     "/Users/jacob/My Project; $(x)",
			want: []string{
				"create", "--isolated", "--isolate-network",
				"--mount", "/Users/jacob/My Project; $(x):/Users/jacob/My Project; $(x)",
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
		{name: "absolute path", dir: "/Users/jacob/Project/api"},
		{name: "path with spaces", dir: "/Users/jacob/My Project"},
		{name: "empty", dir: "", want: ErrMountPath},
		{name: "relative", dir: "Project/api", want: ErrMountPath},
		{name: "colon in the last name", dir: "/Users/jacob/.ssh:x", want: ErrMountPath},
		{name: "colon in a parent", dir: "/Users/jacob/a:/b", want: ErrMountPath},
		{name: "source and destination", dir: "/Users/jacob/work:/Users/jacob", want: ErrMountPath},
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

	if err := client.Create(ctx, "ubuntu", "/Users/jacob/Project/api"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("Create of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.Delete(ctx, "ubuntu"); !errors.Is(err, ErrForeignMachine) {
		t.Errorf("Delete of a foreign machine = %v, want %v", err, ErrForeignMachine)
	}
	if err := client.Create(ctx, "orbit-api-1d9b22c9", "/Users/jacob/.ssh:x"); !errors.Is(err, ErrMountPath) {
		t.Errorf("Create with a colon in the directory = %v, want %v", err, ErrMountPath)
	}
}

func TestFailedCommandCarriesStderr(t *testing.T) {
	useFakeCLI(t, "echo '[-32098] machine not found' >&2\nexit 3\n")
	client := New(testPrefix)
	ctx := context.Background()
	_, existsErr := client.Exists(ctx, "orbit-api-1d9b22c9")
	failures := map[string]error{
		"Create": client.Create(ctx, "orbit-api-1d9b22c9", "/Users/jacob/Project/api"),
		"Exists": existsErr,
		"Delete": client.Delete(ctx, "orbit-api-1d9b22c9"),
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
		t.Errorf("Exists without orb = %v, want %v", err, exec.ErrNotFound)
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

// useFakeCLI makes a shell script with the given body the only orb on PATH.
func useFakeCLI(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(dir, cli), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}
