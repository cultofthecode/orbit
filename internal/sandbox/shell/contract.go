// Package shell is the slice of "orbit shell": it opens an interactive login
// shell as the agent user in the running, provisioned sandbox of a project
// directory, in that directory. It never starts, provisions, repairs, or
// deletes a machine, and it runs nothing in the shell.
package shell

import (
	"errors"
	"io"
)

// Input is the request to open a shell in the sandbox of a project
// directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
	// In, Out, and Err are the streams of the shell. orb gives the shell a
	// terminal only when In is one, so the command passes its own streams
	// unwrapped.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Output is the shell that ran.
type Output struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory.
	Dir string
	// Status is the exit status of the shell.
	Status int
}

// Errors of the handler. Besides these, a directory without a sandbox fails
// with sandbox.ErrNoSandbox, and a sandbox whose configuration or kernel
// does not mount exactly the project directory with sandbox.ErrMountMismatch.
// Every refusal opens no shell.
var (
	// ErrNotRunning reports a sandbox in any state but running. The handler
	// runs no command in it, because every command in a machine starts it.
	ErrNotRunning = errors.New(`run "orbit start"`)
	// ErrNotProvisioned reports a running sandbox that failed the readiness
	// check, or whose readiness check could not run. It wraps the error of
	// the check.
	ErrNotProvisioned = errors.New("is not ready")
)
