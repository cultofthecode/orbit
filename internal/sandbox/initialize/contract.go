// Package initialize is the slice of "orbit init": it creates the sandbox of
// a project directory. So far it creates the machine only; the machine is not
// a safe sandbox before provisioning has set up the agent user, the firewall,
// and the proxy.
package initialize

import "errors"

// Input is the request to create the sandbox of a project directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
}

// Output is the sandbox of the project directory.
type Output struct {
	// Name is the name of the machine.
	Name string
	// Dir is the canonical path of the project directory: the only mount of
	// the machine, at the same path as on the host.
	Dir string
	// Created is true when this call created the machine, and false when
	// the machine already existed.
	Created bool
}

// ErrNotDirectory reports a project path that is not a directory. A refused
// project directory fails with one of the errors of
// sandbox.ValidateProjectDir or sandbox.ErrNestedProjectDir, a missing one
// with fs.ErrNotExist, and a machine that mounts something else than the
// project directory with sandbox.ErrMountMismatch.
var ErrNotDirectory = errors.New("project path is not a directory")
