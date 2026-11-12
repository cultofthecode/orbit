package destroy

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether in is a terminal: an *os.File whose descriptor
// answers the terminal-attributes ioctl. os.ModeCharDevice is not enough,
// because /dev/null is a character device too.
func isTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&attributes)))
	return errno == 0
}
