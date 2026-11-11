// Package stop is the slice of "orbit stop": it stops the sandbox of a
// project directory, and no other machine, and keeps its disk.
package stop

// Input is the request to stop the sandbox of a project directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
}

// Output is the stopped sandbox of the project directory.
type Output struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory.
	Dir string
	// AlreadyStopped is true when the sandbox was stopped before the call,
	// which then stopped nothing.
	AlreadyStopped bool
}

// The handler fails with sandbox.ErrNoSandbox for a directory without a
// sandbox and with sandbox.ErrUnexpectedState for a sandbox that is neither
// running nor stopped, and then stops nothing. A project path that cannot
// be resolved fails with the error of the resolution.
