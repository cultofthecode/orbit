package hostcheck

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// printableError is an error whose message holds no character that is not
// printable, so that a directory name in it cannot write an escape sequence
// or a line break to the terminal. It unwraps to the error that it wraps.
type printableError struct{ err error }

// Error returns the message of the wrapped error with each character that is
// not printable, and each byte that is not UTF-8, in its Go escape, and each
// backslash doubled, so that a name that holds a backslash never reads the
// same as one that holds the character that its escape names.
func (e printableError) Error() string {
	var message strings.Builder
	for rest := e.err.Error(); rest != ""; {
		r, size := utf8.DecodeRuneInString(rest)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&message, `\x%02x`, rest[0])
		case r == '\\':
			message.WriteString(`\\`)
		case unicode.IsPrint(r):
			message.WriteString(rest[:size])
		default:
			message.WriteString(strings.Trim(strconv.QuoteToASCII(rest[:size]), `"`))
		}
		rest = rest[size:]
	}
	return message.String()
}

// Unwrap returns the wrapped error.
func (e printableError) Unwrap() error { return e.err }

// printable returns err with every path in its message printable, or nil for
// a nil err. Each path of the package reaches the owner through the exported
// functions, so they wrap their errors once, and no error site can print a
// path raw (#111).
func printable(err error) error {
	if err == nil {
		return nil
	}
	return printableError{err}
}
