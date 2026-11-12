// Package realpath resolves the path of a directory to its canonical path.
// Every slice of the sandbox module resolves the project directory with it,
// so that the mount rules compare canonical paths and every slice computes
// the same machine name for a directory.
package realpath

import (
	"os"
	"path/filepath"
)

// Resolve returns the canonical path of the existing directory path:
// absolute, with symlinks resolved, in the one spelling that the system
// reports for the directory. It needs only search permission on the
// directory. A path that is not a directory fails with syscall.ENOTDIR.
func Resolve(path string) (string, error) {
	return resolve(path)
}

// ResolveWithOwner returns the canonical path of the existing directory path,
// as Resolve does, and the uid of the owner of that directory. It opens the
// directory once and reads both facts from the open handle, so they describe
// the same directory even when path is changed while it runs.
func ResolveWithOwner(path string) (string, uint32, error) {
	return resolveWithOwner(path)
}

// ResolveNoFirmlink returns the path of the existing directory path on its
// own volume. On macOS a firmlink joins the directories of the data volume
// to the root, so the home directory /Users/dev is also the directory
// /System/Volumes/Data/Users/dev; Resolve returns the first path and
// ResolveNoFirmlink the second. On other systems the two are equal.
func ResolveNoFirmlink(path string) (string, error) {
	return resolveNoFirmlink(path)
}

// UncleanAbs returns path, or for a relative path the working directory
// followed by path, without cleaning either. A ".." stays in the result, so
// the caller can apply it as the kernel does: to the target of the symlink
// before it.
func UncleanAbs(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return dir + string(filepath.Separator) + path, nil
}
