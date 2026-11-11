// Package initialize is the slice of "orbit init": it creates the sandbox of
// a project directory, provisions it as root with the agent user, the
// firewall, the proxy, and the fixed toolchain, and installs the agent named
// by --agent as the agent user.
package initialize

import "github.com/cultofthecode/orbit/internal/sandbox"

// Input is the request to create the sandbox of a project directory.
type Input struct {
	// Dir is the path of the project directory.
	Dir string
	// Agent is the name of the agent to install, or empty for none. The
	// allowlist of the proxy holds the egress domains of this agent only.
	Agent string
	// NoSSHForwarding leaves the SSH agent of the host out of the sandbox.
	// The zero value forwards it (R08, D25). OrbStack fixes the setting when
	// it creates the machine, so a machine whose setting differs fails with
	// sandbox.ErrSSHForwardingMismatch.
	NoSSHForwarding bool
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

// ErrNotDirectory reports a project path that is not a directory. It is
// sandbox.ErrNotDirectory. An unknown agent fails with the error of
// Handler.Agent. A refused project directory fails with one of the errors of
// sandbox.ValidateProjectDir, sandbox.ErrNestedProjectDir,
// sandbox.ErrContainsProjectDir, or ErrLinkedSandboxDir, a missing one with
// fs.ErrNotExist, a variable that moves a protected location to a path that
// Orbit cannot protect with sandbox.ErrRelocatedPath, and a machine that
// mounts something else than the project directory with
// sandbox.ErrMountMismatch, and a machine whose SSH agent forwarding differs
// from the request with sandbox.ErrSSHForwardingMismatch.
var ErrNotDirectory = sandbox.ErrNotDirectory

// ErrLinkedSandboxDir reports another machine whose stored mount path passes
// through a symlink. It is sandbox.ErrLinkedSandboxDir. The handler refuses
// every project directory until the user fixes the path or deletes that
// machine.
var ErrLinkedSandboxDir = sandbox.ErrLinkedSandboxDir

// ErrSSHForwardingMismatch reports an existing machine whose SSH agent
// forwarding differs from the request, which init refuses before it runs a
// command in the machine. It is sandbox.ErrSSHForwardingMismatch.
var ErrSSHForwardingMismatch = sandbox.ErrSSHForwardingMismatch
