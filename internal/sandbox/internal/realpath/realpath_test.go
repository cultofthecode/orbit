package realpath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestResolve(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	file := filepath.Join(root, "Work", "notes.txt")
	t.Chdir(root)
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "canonical path", path: project, want: project},
		{name: "file", path: file, want: file},
		{name: "trailing slash", path: project + "/", want: project},
		{name: "dot dot and double slash", path: root + "/Work/../Work//api", want: project},
		{name: "relative path", path: "Work/api", want: project},
		{name: "dot", path: ".", want: root},
		{name: "symlink to a directory", path: symlink(t, project), want: project},
		{name: "symlink to a file", path: symlink(t, file), want: file},
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
// file besides its canonical path. A spelling that the file system of the
// test does not accept is skipped.
func TestResolveOtherSpellings(t *testing.T) {
	root := newTree(t)
	project := filepath.Join(root, "Work", "api")
	tests := []struct {
		name string
		path string
	}{
		{name: "upper case", path: strings.ToUpper(project)},
		{name: "lower case", path: strings.ToLower(project)},
		{name: "data volume", path: "/System/Volumes/Data" + project},
		{name: "data volume in upper case", path: "/System/Volumes/Data" + strings.ToUpper(project)},
		{name: "symlink to the data volume", path: symlink(t, "/System/Volumes/Data"+project)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := os.Stat(test.path); err != nil {
				t.Skipf("the file system does not accept this spelling: %v", err)
			}
			got, err := Resolve(test.path)
			if err != nil || got != project {
				t.Errorf("Resolve(%q) = %q, %v, want %q", test.path, got, err, project)
			}
		})
	}
}

func TestResolveRefusesMissingFiles(t *testing.T) {
	root := newTree(t)
	tests := []struct {
		name string
		path string
	}{
		{name: "missing file", path: filepath.Join(root, "missing")},
		{name: "missing parent", path: filepath.Join(root, "missing", "api")},
		{name: "broken symlink", path: symlink(t, filepath.Join(root, "missing"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.path)
			if !errors.Is(err, fs.ErrNotExist) || got != "" {
				t.Errorf("Resolve(%q) = %q, %v, want %v", test.path, got, err, fs.ErrNotExist)
			}
		})
	}
}
