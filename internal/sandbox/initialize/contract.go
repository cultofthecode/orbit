// Package initialize is the slice of "orbit init": it creates the sandbox of
// a project directory and provisions it as root with the agent user, the
// firewall, the proxy, and the fixed toolchain. The command is not
// registered until the agent is installed as well (story E01-S01).
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
// sandbox.ValidateProjectDir, sandbox.ErrNestedProjectDir,
// sandbox.ErrContainsProjectDir, or ErrLinkedSandboxDir, a missing one
// with fs.ErrNotExist, a variable that moves a protected location to a path
// that Orbit cannot protect with sandbox.ErrRelocatedPath, and a machine
// that mounts something else than the project directory with
// sandbox.ErrMountMismatch.
var ErrNotDirectory = errors.New("project path is not a directory")

// ErrLinkedSandboxDir reports another machine whose stored mount path passes
// through a symlink. Orbit stores canonical paths, so the host layout changed
// after that machine was created, and OrbStack follows the symlink at its
// next start. The handler refuses every project directory until the user
// fixes the path or deletes that machine.
var ErrLinkedSandboxDir = errors.New("the project directory of another sandbox passes through a symlink")
