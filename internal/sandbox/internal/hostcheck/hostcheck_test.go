package hostcheck

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

const (
	own   = "orbit-own-0123456789abcdef"
	other = "orbit-other-0123456789abcdef"
)

// layout is a canonical temporary directory with the project directory proj
// and the directory of another sandbox, b.
type layout struct {
	root, proj, b string
}

// newLayout creates proj and b in a new canonical directory.
func newLayout(t *testing.T) layout {
	t.Helper()
	root, err := realpath.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := layout{root: root, proj: filepath.Join(root, "proj"), b: filepath.Join(root, "b")}
	for _, dir := range []string{l.proj, l.b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// swapForSymlink moves dir into root/w and leaves a symlink at dir, as a
// host-side "mv dir w/dir && ln -s w/dir dir" does.
func (l layout) swapForSymlink(t *testing.T, dir string) {
	t.Helper()
	moved := filepath.Join(l.root, "w", filepath.Base(dir))
	if err := os.MkdirAll(filepath.Dir(moved), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Fatal(err)
	}
}

// machines returns the stored mounts of the own sandbox and of another one.
func (l layout) machines() map[string]map[string]string {
	return map[string]map[string]string{
		own:   {l.proj: l.proj},
		other: {l.b: l.b},
	}
}

func TestUnnestedChecksTheOwnStoredPathOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name       string
		swap       func(layout) string
		includeOwn bool
		want       error
	}{
		{name: "nothing swapped, init", swap: func(layout) string { return "" }},
		{name: "nothing swapped, start", swap: func(layout) string { return "" }, includeOwn: true},
		{name: "own path swapped, init", swap: func(l layout) string { return l.proj }},
		{name: "own path swapped, start", swap: func(l layout) string { return l.proj }, includeOwn: true, want: sandbox.ErrLinkedProjectDir},
		{name: "other path swapped, init", swap: func(l layout) string { return l.b }, want: sandbox.ErrLinkedSandboxDir},
		{name: "other path swapped, start", swap: func(l layout) string { return l.b }, includeOwn: true, want: sandbox.ErrLinkedSandboxDir},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			l := newLayout(t)
			if dir := test.swap(l); dir != "" {
				l.swapForSymlink(t, dir)
			}

			err := Unnested(l.machines(), own, l.proj, test.includeOwn)

			if test.want == nil && err != nil {
				t.Fatalf("Unnested = %v, want nil", err)
			}
			if !errors.Is(err, test.want) {
				t.Errorf("Unnested = %v, want %v", err, test.want)
			}
			if errors.Is(err, sandbox.ErrLinkedProjectDir) && errors.Is(err, sandbox.ErrLinkedSandboxDir) {
				t.Errorf("Unnested = %v, want only one of the two errors", err)
			}
		})
	}
}

func TestUnnestedNamesTheLinkedComponentOfTheOwnPath(t *testing.T) {
	l := newLayout(t)
	inner := filepath.Join(l.proj, "inner")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	machines := map[string]map[string]string{own: {inner: inner}}
	l.swapForSymlink(t, l.proj)

	err := Unnested(machines, own, inner, true)

	if !errors.Is(err, sandbox.ErrLinkedProjectDir) {
		t.Fatalf("Unnested = %v, want %v", err, sandbox.ErrLinkedProjectDir)
	}
	if want := "project directory " + inner + " of sandbox " + own + ": " + sandbox.ErrLinkedProjectDir.Error() + ": " + l.proj; err.Error() != want {
		t.Errorf("Unnested = %q, want %q", err, want)
	}
}

func TestUnnestedNeverCountsTheOwnMachineAsNested(t *testing.T) {
	l := newLayout(t)
	machines := map[string]map[string]string{own: {l.proj: l.proj, l.root: l.root}}

	for _, includeOwn := range []bool{false, true} {
		if err := Unnested(machines, own, l.proj, includeOwn); err != nil {
			t.Errorf("Unnested(includeOwn %v) = %v, want nil", includeOwn, err)
		}
	}
}

func TestUnnestedRefusesNestingWithAnotherMachine(t *testing.T) {
	l := newLayout(t)
	tests := []struct {
		name  string
		other string
		want  error
	}{
		{name: "same directory", other: l.proj, want: sandbox.ErrNestedProjectDir},
		{name: "other contains the project", other: l.root, want: sandbox.ErrNestedProjectDir},
		{name: "project contains the other", other: filepath.Join(l.proj, "sub"), want: sandbox.ErrContainsProjectDir},
		{name: "stored path not clean", other: l.root + "/b/../proj", want: sandbox.ErrPathNotClean},
		{name: "stored path relative", other: "proj", want: sandbox.ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			machines := map[string]map[string]string{own: {l.proj: l.proj}, other: {"/mnt": test.other}}
			for _, includeOwn := range []bool{false, true} {
				if err := Unnested(machines, own, l.proj, includeOwn); !errors.Is(err, test.want) {
					t.Errorf("Unnested(includeOwn %v) = %v, want %v", includeOwn, err, test.want)
				}
			}
		})
	}
}

func TestLoadUserQuotesAMissingHome(t *testing.T) {
	tests := []struct {
		name          string
		missing       string
		inEnvironment bool
	}{
		{name: "environment home newline and escape", missing: "/missing\n\x1b[2J", inEnvironment: true},
		{name: "environment home title sequence", missing: "/missing\x1b]0;title\x07", inEnvironment: true},
		{name: "environment home carriage return and tab", missing: "/missing\r\t", inEnvironment: true},
		{name: "account home newline and escape", missing: "/missing\n\x1b[2J"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environmentHome, accountHome := t.TempDir(), test.missing
			if test.inEnvironment {
				environmentHome, accountHome = test.missing, t.TempDir()
			}
			t.Setenv("HOME", environmentHome)
			account := func() (*user.User, error) {
				return &user.User{Uid: "501", HomeDir: accountHome}, nil
			}
			_, _, _, err := loadUser(account)
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("loadUser = %v, want %v", err, fs.ErrNotExist)
			}
			if !strings.Contains(err.Error(), strconv.Quote(test.missing)) {
				t.Errorf("loadUser = %q, want the quoted home", err)
			}
			for _, raw := range []string{"\n", "\x1b", "\x07", "\r", "\t"} {
				if strings.Contains(err.Error(), raw) {
					t.Errorf("loadUser = %q, contains the raw character %q", err, raw)
				}
			}
		})
	}
}
