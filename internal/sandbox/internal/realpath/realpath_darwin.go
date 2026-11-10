package realpath

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// maxPathLength is MAXPATHLEN of macOS: the size of the buffer that F_GETPATH
// fills.
const maxPathLength = 1024

// resolve asks the kernel for the path of the open file (F_GETPATH).
// filepath.EvalSymlinks is not enough on macOS. It keeps a spelling in
// another letter case, and a path through /System/Volumes/Data; each of them
// names the same directory as the canonical path, so a comparison of paths
// would not recognize the home directory.
func resolve(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, maxPathLength)
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETPATH, uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", fmt.Errorf("canonical path of %s: %w", path, errno)
	}
	canonical, _, _ := bytes.Cut(buffer, []byte{0})
	return string(canonical), nil
}
