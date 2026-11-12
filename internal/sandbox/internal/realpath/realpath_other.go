//go:build !darwin

package realpath

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// resolve refuses a path that is not an existing directory, makes it
// absolute, and resolves its symlinks. It does not clean the path first:
// filepath.EvalSymlinks applies a ".." to the target of the symlink before
// it, as the kernel does, and a lexical clean would drop that symlink.
func resolve(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: syscall.ENOTDIR}
	}
	absolute, err := UncleanAbs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// resolveNoFirmlink equals resolve: only macOS has firmlinks.
func resolveNoFirmlink(path string) (string, error) {
	return resolve(path)
}

// resolveWithOwner opens the directory path and reads its owner (fstat) and
// its path (the link of the handle in /proc/self/fd) from the open handle.
func resolveWithOwner(path string) (string, uint32, error) {
	dir, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = dir.Close() }()
	return openDirFacts(dir)
}

// openDirFacts returns the path of the open directory dir, as /proc/self/fd
// reports it, and the uid of its owner.
func openDirFacts(dir *os.File) (string, uint32, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(dir.Fd()), &stat); err != nil {
		return "", 0, &fs.PathError{Op: "fstat", Path: dir.Name(), Err: err}
	}
	canonical, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(dir.Fd())))
	if err != nil {
		return "", 0, err
	}
	return canonical, stat.Uid, nil
}
