package sandbox

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Printable returns s, or its Go-quoted form when it holds a character that
// is not printable or invalid UTF-8, so that a stored value or a directory
// name cannot write an escape sequence or a line break to the terminal and
// change what the owner reads. It does no I/O.
func Printable(s string) string {
	if !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return strconv.Quote(s)
	}
	return s
}
