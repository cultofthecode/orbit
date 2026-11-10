package realpath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dataVolume is the path of the data volume of macOS. A firmlink joins its
// directories to the root.
const dataVolume = "/System/Volumes/Data"

// newTree makes a directory with the directory "Work/api" and the file
// "Work/notes.txt" in it, and returns its canonical path.
func newTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Work", "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Work", "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// symlink makes a symlink to target in a new directory and returns its path.
func symlink(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// requireSpelling stops the test when the file system does not accept path
// as a spelling of an existing file. macOS must accept it, so there the test
// fails; on another system it is skipped.
func requireSpelling(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return
	}
	if runtime.GOOS == "darwin" {
		t.Fatalf("macOS does not accept the spelling %q: %v", path, err)
	}
	t.Skipf("the file system does not accept this spelling: %v", err)
}

func TestResolve(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	t.Chdir(root)
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "canonical path", path: project, want: project},
		{name: "trailing slash", path: project + "/", want: project},
		{name: "dot dot and double slash", path: root + "/Work/../Work//api", want: project},
		{name: "relative path", path: "Work/api", want: project},
		{name: "dot", path: ".", want: root},
		{name: "symlink to a directory", path: symlink(t, project), want: project},
		{name: "symlink to a symlink", path: symlink(t, symlink(t, project)), want: project},
		{name: "path through a symlink", path: symlink(t, root) + "/Work/api", want: project},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.path)
			if err != nil || got != test.want {
				t.Errorf("Resolve(%q) = %q, %v, want %q", test.path, got, err, test.want)
			}
		})
	}
}

// TestResolveOtherSpellings covers the spellings that macOS accepts for a
// directory besides its canonical path.
func TestResolveOtherSpellings(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	tests := []struct {
		name string
		path string
	}{
		{name: "upper case", path: strings.ToUpper(project)},
		{name: "lower case", path: strings.ToLower(project)},
		{name: "data volume", path: dataVolume + project},
		{name: "data volume in upper case", path: dataVolume + strings.ToUpper(project)},
		{name: "symlink to the data volume", path: symlink(t, dataVolume+project)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireSpelling(t, test.path)
			got, err := Resolve(test.path)
			if err != nil || got != project {
				t.Errorf("Resolve(%q) = %q, %v, want %q", test.path, got, err, project)
			}
		})
	}
}

func TestResolveNoFirmlink(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	want := project
	if runtime.GOOS == "darwin" {
		want = dataVolume + project
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "canonical path", path: project},
		{name: "symlink to a directory", path: symlink(t, project)},
		{name: "dot dot and double slash", path: root + "/Work/../Work//api"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveNoFirmlink(test.path)
			if err != nil || got != want {
				t.Errorf("ResolveNoFirmlink(%q) = %q, %v, want %q", test.path, got, err, want)
			}
		})
	}
}

// TestResolveNoFirmlinkOfOtherSpellings checks that every spelling of a
// directory of macOS has the same path on the data volume.
func TestResolveNoFirmlinkOfOtherSpellings(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	for _, path := range []string{dataVolume + project, strings.ToUpper(project), dataVolume + strings.ToUpper(project)} {
		t.Run(path, func(t *testing.T) {
			requireSpelling(t, path)
			got, err := ResolveNoFirmlink(path)
			if err != nil || got != dataVolume+project {
				t.Errorf("ResolveNoFirmlink(%q) = %q, %v, want %q", path, got, err, dataVolume+project)
			}
		})
	}
}

func TestResolveNeedsOnlySearchPermission(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	if err := os.Chmod(project, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(project, 0o700) })

	got, err := Resolve(project)
	if err != nil || got != project {
		t.Errorf("Resolve(%q) = %q, %v, want %q", project, got, err, project)
	}
	gotNoFirmlink, err := ResolveNoFirmlink(project)
	if err != nil || !strings.HasSuffix(gotNoFirmlink, project) {
		t.Errorf("ResolveNoFirmlink(%q) = %q, %v, want a path that ends in %q", project, gotNoFirmlink, err, project)
	}
}

func TestResolveRefusesMissingDirectories(t *testing.T) {
	root := newTree(t)
	tests := []struct {
		name string
		path string
	}{
		{name: "missing directory", path: filepath.Join(root, "missing")},
		{name: "missing parent", path: filepath.Join(root, "missing", "api")},
		{name: "broken symlink", path: symlink(t, filepath.Join(root, "missing"))},
		{name: "empty path", path: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.path)
			if !errors.Is(err, fs.ErrNotExist) || got != "" {
				t.Errorf("Resolve(%q) = %q, %v, want %v", test.path, got, err, fs.ErrNotExist)
			}
			got, err = ResolveNoFirmlink(test.path)
			if !errors.Is(err, fs.ErrNotExist) || got != "" {
				t.Errorf("ResolveNoFirmlink(%q) = %q, %v, want %v", test.path, got, err, fs.ErrNotExist)
			}
		})
	}
}

// TestResolveRefusesOtherFiles checks that a file that is not a directory
// fails, and that a FIFO without a writer does not block the call.
func TestResolveRefusesOtherFiles(t *testing.T) {
	root := newTree(t)
	file := filepath.Join(root, "Work", "notes.txt")
	fifo := filepath.Join(root, "Work", "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "file", path: file},
		{name: "symlink to a file", path: symlink(t, file)},
		{name: "child of a file", path: filepath.Join(file, "api")},
		{name: "FIFO", path: fifo},
		{name: "symlink to a FIFO", path: symlink(t, fifo)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, err := Resolve(test.path)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, syscall.ENOTDIR) {
					t.Errorf("Resolve(%q) = %v, want %v", test.path, err, syscall.ENOTDIR)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Resolve(%q) blocks", test.path)
			}
		})
	}
}

func TestResolveWithOwner(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	t.Chdir(root)
	tests := []struct {
		name string
		path string
	}{
		{name: "canonical path", path: project},
		{name: "relative path", path: "Work/api"},
		{name: "dot dot and double slash", path: root + "/Work/../Work//api/"},
		{name: "symlink to a symlink", path: symlink(t, symlink(t, project))},
		{name: "path through a symlink", path: symlink(t, root) + "/Work/api"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, owner, err := ResolveWithOwner(test.path)
			if err != nil || got != project || owner != uint32(os.Getuid()) {
				t.Errorf("ResolveWithOwner(%q) = %q, %d, %v, want %q, %d", test.path, got, owner, err, project, os.Getuid())
			}
		})
	}
}

// TestResolveWithOwnerOfOtherSpellings covers the spellings that macOS
// accepts for a directory besides its canonical path.
func TestResolveWithOwnerOfOtherSpellings(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	for _, path := range []string{strings.ToUpper(project), dataVolume + project, dataVolume + strings.ToUpper(project)} {
		t.Run(path, func(t *testing.T) {
			requireSpelling(t, path)
			got, _, err := ResolveWithOwner(path)
			if err != nil || got != project {
				t.Errorf("ResolveWithOwner(%q) = %q, %v, want %q", path, got, err, project)
			}
		})
	}
}

// TestOwnerAndPathDescribeTheOpenDirectory swaps the path of an open
// directory for a symlink to another directory, as an agent that controls
// the parent can do. The path and the owner must still describe the open
// directory, not what its old path names now.
func TestOwnerAndPathDescribeTheOpenDirectory(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	moved := filepath.Join(root, "Work", "moved")
	decoy := filepath.Join(root, "decoy")
	if err := os.Mkdir(decoy, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	if err := os.Rename(project, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, project); err != nil {
		t.Fatal(err)
	}

	got, owner, err := openDirFacts(dir)

	if err != nil || got != moved {
		t.Fatalf("path of the open directory = %q, %v, want %q", got, err, moved)
	}
	opened, err := dir.Stat()
	if err != nil {
		t.Fatal(err)
	}
	named, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(opened, named) {
		t.Errorf("%q names another directory than the open one", got)
	}
	if want := opened.Sys().(*syscall.Stat_t).Uid; owner != want {
		t.Errorf("owner = %d, want %d, the owner of the open directory", owner, want)
	}
}

func TestResolveWithOwnerRefusesWhatIsNoDirectory(t *testing.T) {
	root := newTree(t)
	file := filepath.Join(root, "Work", "notes.txt")
	fifo := filepath.Join(root, "Work", "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "missing directory", path: filepath.Join(root, "missing"), want: fs.ErrNotExist},
		{name: "broken symlink", path: symlink(t, filepath.Join(root, "missing")), want: fs.ErrNotExist},
		{name: "empty path", path: "", want: fs.ErrNotExist},
		{name: "file", path: file, want: syscall.ENOTDIR},
		{name: "symlink to a file", path: symlink(t, file), want: syscall.ENOTDIR},
		{name: "FIFO", path: fifo, want: syscall.ENOTDIR},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, _, err := ResolveWithOwner(test.path)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, test.want) {
					t.Errorf("ResolveWithOwner(%q) = %v, want %v", test.path, err, test.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("ResolveWithOwner(%q) blocks", test.path)
			}
		})
	}
}
