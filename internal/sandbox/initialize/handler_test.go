package initialize

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// newHome makes a home directory with a project, .ssh, and .claude in it,
// makes it the home directory of the test, and returns its canonical path.
func newHome(t *testing.T) string {
	t.Helper()
	home := newDir(t)
	for _, dir := range []string{"work/api", ".ssh", ".claude/projects"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	return home
}

// newDir returns the canonical path of a new temporary directory.
func newDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// symlink makes a symlink to target in a new directory and returns its path.
func symlink(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(newDir(t), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestHandleCreatesTheMachine(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	tests := []struct {
		name string
		dir  string
	}{
		{name: "canonical path", dir: project},
		{name: "symlink to the project", dir: symlink(t, project)},
		{name: "path that is not clean", dir: home + "/work/../work//api/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			name := sandbox.MachineName(project)

			got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: test.dir})

			if err != nil {
				t.Fatalf("Handle(%q) = %v", test.dir, err)
			}
			if want := (Output{Name: name, Dir: project, Created: true}); got != want {
				t.Errorf("Handle(%q) = %+v, want %+v", test.dir, got, want)
			}
			wantCalls := []orbstacktest.Call{
				{Op: orbstacktest.OpExists, Name: name},
				{Op: orbstacktest.OpCreate, Name: name, Dir: project},
			}
			if !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
}

func TestHandleKeepsAnExistingMachine(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = project

	got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: project})

	if err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if want := (Output{Name: name, Dir: project, Created: false}); got != want {
		t.Errorf("Handle = %+v, want %+v", got, want)
	}
	wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpExists, Name: name}}
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleRefusesInvalidDirectories(t *testing.T) {
	home := newHome(t)
	file := filepath.Join(home, "work", "notes.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dir  string
		want error
	}{
		{name: "home", dir: home, want: sandbox.ErrHomeDir},
		{name: "home with a trailing slash", dir: home + "/", want: sandbox.ErrHomeDir},
		{name: "dot dot to home", dir: home + "/work/..", want: sandbox.ErrHomeDir},
		{name: "symlink to home", dir: symlink(t, home), want: sandbox.ErrHomeDir},
		{name: "root", dir: "/", want: sandbox.ErrRootDir},
		{name: "dot dot to root", dir: home + strings.Repeat("/..", 40), want: sandbox.ErrRootDir},
		{name: "parent of home", dir: filepath.Dir(home), want: sandbox.ErrHomeAncestor},
		{name: ".ssh", dir: filepath.Join(home, ".ssh"), want: sandbox.ErrProtectedDir},
		{name: "child of .claude", dir: filepath.Join(home, ".claude", "projects"), want: sandbox.ErrProtectedDir},
		{name: "symlink to .ssh", dir: symlink(t, filepath.Join(home, ".ssh")), want: sandbox.ErrProtectedDir},
		{name: "missing directory", dir: filepath.Join(home, "work", "missing"), want: fs.ErrNotExist},
		{name: "broken symlink", dir: symlink(t, filepath.Join(home, "work", "missing")), want: fs.ErrNotExist},
		{name: "empty path", dir: "", want: fs.ErrNotExist},
		{name: "file", dir: file, want: ErrNotDirectory},
		{name: "symlink to a file", dir: symlink(t, file), want: ErrNotDirectory},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, test.want) {
				t.Errorf("Handle(%q) = %v, want %v", test.dir, err, test.want)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

// TestHandleResolvesOtherSpellings covers the spellings that macOS accepts
// for a directory besides its canonical path. A spelling that the file system
// of the test does not accept is skipped.
func TestHandleResolvesOtherSpellings(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	spellings := []struct {
		name  string
		spell func(path string) string
	}{
		{name: "upper case", spell: strings.ToUpper},
		{name: "data volume", spell: func(path string) string { return "/System/Volumes/Data" + path }},
	}
	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			if _, err := os.Stat(spelling.spell(project)); err != nil {
				t.Skipf("the file system does not accept this spelling: %v", err)
			}
			fake := orbstacktest.New(sandbox.NamePrefix)
			handler := Handler{Machines: fake}

			_, err := handler.Handle(context.Background(), Input{Dir: spelling.spell(home)})
			if !errors.Is(err, sandbox.ErrHomeDir) {
				t.Errorf("Handle(%q) = %v, want %v", spelling.spell(home), err, sandbox.ErrHomeDir)
			}
			_, err = handler.Handle(context.Background(), Input{Dir: spelling.spell(filepath.Join(home, ".ssh"))})
			if !errors.Is(err, sandbox.ErrProtectedDir) {
				t.Errorf("Handle(%q) = %v, want %v", spelling.spell(home)+"/.ssh", err, sandbox.ErrProtectedDir)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}

			got, err := handler.Handle(context.Background(), Input{Dir: spelling.spell(project)})
			if err != nil {
				t.Fatalf("Handle(%q) = %v", spelling.spell(project), err)
			}
			if want := (Output{Name: sandbox.MachineName(project), Dir: project, Created: true}); got != want {
				t.Errorf("Handle(%q) = %+v, want %+v", spelling.spell(project), got, want)
			}
		})
	}
}

func TestHandleRefusesWithoutAHomeDirectory(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	tests := []struct {
		name string
		home string
	}{
		{name: "home is not set", home: ""},
		{name: "home does not exist", home: filepath.Join(home, "missing")},
		{name: "home is a relative path that does not exist", home: "missing"},
		{name: "home is a file", home: filepath.Join(home, ".ssh", "config")},
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.home)
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: project})

			if err == nil {
				t.Errorf("Handle with HOME=%q succeeded, want an error", test.home)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

func TestHandleReportsOrbStackFailures(t *testing.T) {
	boom := errors.New("orb: boom")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	tests := []struct {
		name      string
		existsErr error
		createErr error
		wantCalls []orbstacktest.Call
	}{
		{
			name:      "exists fails",
			existsErr: boom,
			wantCalls: []orbstacktest.Call{{Op: orbstacktest.OpExists, Name: name}},
		},
		{
			name:      "create fails",
			createErr: boom,
			wantCalls: []orbstacktest.Call{
				{Op: orbstacktest.OpExists, Name: name},
				{Op: orbstacktest.OpCreate, Name: name, Dir: project},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.ExistsErr = test.existsErr
			fake.CreateErr = test.createErr

			got, err := Handler{Machines: fake}.Handle(context.Background(), Input{Dir: project})

			if !errors.Is(err, boom) {
				t.Errorf("Handle = %v, want %v", err, boom)
			}
			if got != (Output{}) {
				t.Errorf("Handle = %+v, want no output", got)
			}
			if !slices.Equal(fake.Calls, test.wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, test.wantCalls)
			}
		})
	}
}
