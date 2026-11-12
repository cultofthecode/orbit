// Package destroy is the slice of "orbit destroy": it deletes the sandbox of
// a project directory, and no other machine, with everything on its disk,
// after the owner confirmed. It never starts, provisions, or enters the
// machine, and it changes no file on the host.
package destroy

import "errors"

// ErrNotConfirmed reports that the owner did not confirm the delete, and
// that nothing was deleted.
var ErrNotConfirmed = errors.New("not confirmed; nothing was deleted")

// Input is the request to destroy the sandbox of a project directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
}

// Output is the destroyed sandbox of the project directory.
type Output struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory.
	Dir string
}

// Target is what the owner confirms the delete of.
type Target struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory.
	Dir string
	// Running is true when the machine runs, so that open shells end.
	Running bool
}

// The handler fails with sandbox.ErrNoSandbox for a directory without a
// sandbox and with sandbox.ErrUnexpectedState for a sandbox that is neither
// running nor stopped, and then deletes nothing. It fails with
// ErrNotConfirmed, or the error of its Confirm field, when the owner did not
// confirm. A project path that cannot be resolved fails with the error of
// the resolution.
