package realpath

import (
	"bytes"
	"io/fs"
	"os"
	"syscall"
	"unsafe"
)

// Values of macOS that the syscall package does not export.
const (
	// maxPathLength is MAXPATHLEN: the size of the buffer that F_GETPATH
	// fills.
	maxPathLength = 1024
	// openSearch is O_SEARCH (O_EXEC|O_DIRECTORY): open a directory to look
	// up its path. It needs only search permission, and it fails on a file
	// that is not a directory, so a FIFO cannot block it.
	openSearch = 0x40100000
	// getPathNoFirmlink is F_GETPATH_NOFIRMLINK.
	getPathNoFirmlink = 102
)

// resolve asks the kernel for the path of the open directory (F_GETPATH).
// filepath.EvalSymlinks is not enough on macOS. It keeps a spelling in
// another letter case, and a path through /System/Volumes/Data; each of them
// names the same directory as the canonical path, so a comparison of paths
// would not recognize the home directory.
func resolve(path string) (string, error) {
	return kernelPath(path, syscall.F_GETPATH)
}

// resolveNoFirmlink asks the kernel for the path of the open directory on
// its own volume (F_GETPATH_NOFIRMLINK).
func resolveNoFirmlink(path string) (string, error) {
	return kernelPath(path, getPathNoFirmlink)
}

// resolveWithOwner opens the directory path and reads its owner (fstat) and
// its path (F_GETPATH) from the open handle.
func resolveWithOwner(path string) (string, uint32, error) {
	dir, err := os.OpenFile(path, openSearch, 0)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = dir.Close() }()
	return openDirFacts(dir)
}

// openDirFacts returns the path that the kernel reports for the open
// directory dir and the uid of its owner.
func openDirFacts(dir *os.File) (string, uint32, error) {
	owner, err := ownerOf(dir)
	if err != nil {
		return "", 0, err
	}
	canonical, err := pathOf(dir, syscall.F_GETPATH)
	if err != nil {
		return "", 0, err
	}
	return canonical, owner, nil
}

// kernelPath opens the directory path and returns the path that the fcntl
// command reports for it.
func kernelPath(path string, command uintptr) (string, error) {
	dir, err := os.OpenFile(path, openSearch, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	return pathOf(dir, command)
}

// pathOf returns the path that the fcntl command reports for the open file.
func pathOf(file *os.File, command uintptr) (string, error) {
	buffer := make([]byte, maxPathLength)
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), command, uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", &fs.PathError{Op: "fcntl", Path: file.Name(), Err: errno}
	}
	reported, _, _ := bytes.Cut(buffer, []byte{0})
	return string(reported), nil
}

// ownerOf returns the uid of the owner of the open file (fstat).
func ownerOf(file *os.File) (uint32, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &stat); err != nil {
		return 0, &fs.PathError{Op: "fstat", Path: file.Name(), Err: err}
	}
	return stat.Uid, nil
}

func zzUnused() {}
