// Package start is the slice of "orbit start": it starts the stopped sandbox
// of a project directory after the host-side checks of init, and it checks
// after the start that the boundary is up. It is the only slice that starts
// a machine.
package start

import "errors"

// Input is the request to start the sandbox of a project directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
}

// Output is the started sandbox of the project directory.
type Output struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory.
	Dir string
	// AlreadyRunning is true when the sandbox ran before the call, which
	// then started nothing and checked only the project directory.
	AlreadyRunning bool
}

// Errors of the handler. Besides these, a directory without a sandbox fails
// with sandbox.ErrNoSandbox and a sandbox that is neither running nor stopped
// with sandbox.ErrUnexpectedState, and then nothing starts. A refused project
// directory fails before the start with sandbox.ErrNotDirectory, an error of
// sandbox.ValidateProjectDir, sandbox.ErrNestedProjectDir,
// sandbox.ErrContainsProjectDir, sandbox.ErrPathNotClean,
// sandbox.ErrLinkedSandboxDir, sandbox.ErrLinkedProjectDir, or
// sandbox.ErrMountMismatch for the configured mounts.
var (
	// ErrBoundaryDown reports a sandbox that failed a check after the start:
	// the boot check, the mounts in the kernel, or the start itself. The
	// handler stopped the sandbox again.
	ErrBoundaryDown = errors.New("did not start safely")
	// ErrNotProvisioned reports a started sandbox whose boundary is up but
	// whose provisioning marker does not hold the version of this orbit. The
	// handler leaves it running, so that init can provision it again.
	ErrNotProvisioned = errors.New("it is not provisioned by this version of orbit")
)
