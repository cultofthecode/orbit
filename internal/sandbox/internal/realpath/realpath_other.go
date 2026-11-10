//go:build !darwin

package realpath

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// resolve refuses a path that is not an existing directory, makes it
// absolute, and resolves its symlinks.
func resolve(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: syscall.ENOTDIR}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// resolveNoFirmlink equals resolve: only macOS has firmlinks.
func resolveNoFirmlink(path string) (string, error) {
	return resolve(path)
}
