// Package orbstacktest holds a deterministic in-memory fake of OrbStack for
// handler tests. It starts no process and needs no OrbStack.
package orbstacktest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// The fake refuses what the adapter refuses, with errors of its own.
var (
	// ErrForeignMachine reports a machine name without the prefix of the
	// fake.
	ErrForeignMachine = errors.New("machine name does not start with the required prefix")
	// ErrMountPath reports a directory that cannot be mounted at its own
	// path: it is not absolute, or it holds a colon or a comma.
	ErrMountPath = errors.New("mount path is not absolute or contains a colon or a comma")
	// ErrNoMachine reports a machine that does not exist, where orb fails
	// with "machine not found".
	ErrNoMachine = errors.New("machine not found")
	// ErrMachineName reports a machine name with white space.
	ErrMachineName = errors.New("machine name holds white space")
	// ErrStopped reports a command in a machine that is not running. orb
	// would start the machine for it, and a handler must never start a
	// machine that way.
	ErrStopped = errors.New("machine is not running; the command would start it")
)

// States of a machine that the fake knows.
const (
	StateRunning = "running"
	StateStopped = "stopped"
)

// Operations that a Fake records.
const (
	OpState             = "state"
	OpStart             = "start"
	OpStop              = "stop"
	OpExists            = "exists"
	OpCreate            = "create"
	OpSSHAgentForwarded = "ssh-agent-forwarded"
	OpMounts            = "mounts"
	OpList              = "list"
	OpActiveMounts      = "active-mounts"
	OpDelete            = "delete"
	OpRunAsRoot         = "run-as-root"
	OpRunAsAgent        = "run-as-agent"
	OpShell             = "shell"
)

// Machine is one machine as List reports it. The fake has its own type
// because it imports nothing from the adapter or from a slice.
type Machine struct {
	Name   string
	State  string
	Mounts []Mount
}

// Mount is one stored mount of a Machine: the host directory and the path in
// the machine.
type Mount struct {
	Source      string
	Destination string
}

// Call is one call that a Fake received.
type Call struct {
	Op     string
	Name   string
	Dir    string
	Script string
	// Forward is the SSH agent forwarding that a Create call asked for.
	Forward bool
}

// Fake keeps machines in memory and records every call.
type Fake struct {
	// Machines maps the name of each machine to its configured mounts: the
	// host directory of each mount by its mount point. Create configures the
	// directory at its own path.
	Machines map[string]map[string]string
	// Active maps the name of a machine to its active mounts, when they
	// differ from the configured ones: what a machine mounts after its path
	// was swapped.
	Active map[string]map[string]string
	// Forwarded maps the name of a machine to its SSH agent forwarding. A
	// machine without an entry does not forward. Create sets the entry.
	Forwarded map[string]bool
	// CreatedForwarded, when set, is the forwarding that Create stores
	// instead of the requested one: a machine that reports another setting
	// than the one it was created with.
	CreatedForwarded *bool
	// States maps the name of a machine to its state. A machine without an
	// entry is running. Create sets StateRunning, Start StateRunning, and
	// Stop StateStopped.
	States map[string]string
	// StateErr, when set, is the result of every State call.
	StateErr error
	// StartErr, when set, is the result of every Start call, which then
	// changes no state.
	StartErr error
	// StopErr, when set, is the result of every Stop call, which then
	// changes no state.
	StopErr error
	// ExistsErr, when set, is the result of every Exists call.
	ExistsErr error
	// SSHAgentForwardedErr, when set, is the result of every
	// SSHAgentForwarded call.
	SSHAgentForwardedErr error
	// CreateErr, when set, is the result of every Create call.
	CreateErr error
	// Listed holds raw entries that List returns after the machines of the
	// fake, as they are: foreign names, no state, odd mounts. The real
	// client drops foreign names; a test of a handler seeds them to check
	// that it drops them again.
	Listed []Machine
	// ListErr, when set, is the result of every List call.
	ListErr error
	// MountsErr, when set, is the result of every Mounts call.
	MountsErr error
	// ActiveMountsErr, when set, is the result of every ActiveMounts call.
	ActiveMountsErr error
	// DeleteErr, when set, is the result of every Delete call.
	DeleteErr error
	// RunAsRootErr, when set, is the result of every RunAsRoot call.
	RunAsRootErr error
	// RunAsAgentErr, when set, is the result of every RunAsAgent call.
	RunAsAgentErr error
	// ShellStatus is the exit status of every Shell call that succeeds.
	ShellStatus int
	// ShellErr, when set, is the result of every Shell call.
	ShellErr error
	// Calls holds the received calls, in order.
	Calls []Call

	prefix string
}

// New returns a fake with no machines that creates only machines whose names
// start with prefix.
func New(prefix string) *Fake {
	return &Fake{Machines: map[string]map[string]string{}, Active: map[string]map[string]string{}, Forwarded: map[string]bool{}, States: map[string]string{}, prefix: prefix}
}

// State returns the state of the machine name, or "" when it does not
// exist. It refuses a name without the prefix of the fake.
func (f *Fake) State(_ context.Context, name string) (string, error) {
	f.Calls = append(f.Calls, Call{Op: OpState, Name: name})
	if err := f.checkOwned(name); err != nil {
		return "", err
	}
	if f.StateErr != nil {
		return "", f.StateErr
	}
	return f.state(name), nil
}

// Start sets the machine name running. It refuses a name that the adapter
// refuses and a machine that does not exist.
func (f *Fake) Start(_ context.Context, name string) error {
	f.Calls = append(f.Calls, Call{Op: OpStart, Name: name})
	return f.setState(name, StateRunning, f.StartErr)
}

// Stop sets the machine name stopped. It refuses a name that the adapter
// refuses and a machine that does not exist.
func (f *Fake) Stop(_ context.Context, name string) error {
	f.Calls = append(f.Calls, Call{Op: OpStop, Name: name})
	return f.setState(name, StateStopped, f.StopErr)
}

// setState sets the machine name to state, unless the name is refused, the
// machine does not exist, or failure is set, which it then returns.
func (f *Fake) setState(name, state string, failure error) error {
	if err := f.checkExists(name); err != nil {
		return err
	}
	if strings.ContainsFunc(name, unicode.IsSpace) {
		return fmt.Errorf("%w: %q", ErrMachineName, name)
	}
	if failure != nil {
		return failure
	}
	f.States[name] = state
	return nil
}

// state returns the state of the machine name, or "" when it does not exist.
func (f *Fake) state(name string) string {
	if _, exists := f.Machines[name]; !exists {
		return ""
	}
	if state, set := f.States[name]; set {
		return state
	}
	return StateRunning
}

// checkRunning refuses a name without the prefix of the fake and a machine
// that is not running.
func (f *Fake) checkRunning(name string) error {
	if err := f.checkOwned(name); err != nil {
		return err
	}
	if f.state(name) != StateRunning {
		return fmt.Errorf("%w: %q", ErrStopped, name)
	}
	return nil
}

// Exists reports whether the machine name exists.
func (f *Fake) Exists(_ context.Context, name string) (bool, error) {
	f.Calls = append(f.Calls, Call{Op: OpExists, Name: name})
	if f.ExistsErr != nil {
		return false, f.ExistsErr
	}
	_, exists := f.Machines[name]
	return exists, nil
}

// Create adds the machine name with dir mounted at its own path and the SSH
// agent forwarding forwardSSHAgent. It refuses a name without the prefix of
// the fake, and a directory that the adapter cannot mount at its own path.
func (f *Fake) Create(_ context.Context, name, dir string, forwardSSHAgent bool) error {
	f.Calls = append(f.Calls, Call{Op: OpCreate, Name: name, Dir: dir, Forward: forwardSSHAgent})
	if err := f.checkOwned(name); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, ":,") {
		return fmt.Errorf("%w: %q", ErrMountPath, dir)
	}
	if f.CreateErr != nil {
		return f.CreateErr
	}
	f.Machines[name] = map[string]string{dir: dir}
	f.States[name] = StateRunning
	f.Forwarded[name] = forwardSSHAgent
	if f.CreatedForwarded != nil {
		f.Forwarded[name] = *f.CreatedForwarded
	}
	return nil
}

// SSHAgentForwarded returns the SSH agent forwarding of the machine name. It
// refuses a name without the prefix of the fake and a machine that does not
// exist, and it runs nothing in the machine.
func (f *Fake) SSHAgentForwarded(_ context.Context, name string) (bool, error) {
	f.Calls = append(f.Calls, Call{Op: OpSSHAgentForwarded, Name: name})
	if err := f.checkExists(name); err != nil {
		return false, err
	}
	if f.SSHAgentForwardedErr != nil {
		return false, f.SSHAgentForwardedErr
	}
	return f.Forwarded[name], nil
}

// Mounts returns a copy of the configured mounts of every machine.
func (f *Fake) Mounts(_ context.Context) (map[string]map[string]string, error) {
	f.Calls = append(f.Calls, Call{Op: OpMounts})
	if f.MountsErr != nil {
		return nil, f.MountsErr
	}
	mounts := make(map[string]map[string]string, len(f.Machines))
	for name, machineMounts := range f.Machines {
		mounts[name] = maps.Clone(machineMounts)
	}
	return mounts, nil
}

// List returns every machine of the fake, sorted by name, with its state and
// its configured mounts sorted by mount point, followed by the entries of
// Listed. It records only OpList and never changes a state.
func (f *Fake) List(_ context.Context) ([]Machine, error) {
	f.Calls = append(f.Calls, Call{Op: OpList})
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var machines []Machine
	for _, name := range slices.Sorted(maps.Keys(f.Machines)) {
		machine := Machine{Name: name, State: f.state(name)}
		for _, point := range slices.Sorted(maps.Keys(f.Machines[name])) {
			machine.Mounts = append(machine.Mounts, Mount{Source: f.Machines[name][point], Destination: point})
		}
		machines = append(machines, machine)
	}
	return append(machines, f.Listed...), nil
}

// ActiveMounts returns a copy of the active mounts of the machine name: those
// in Active, or else the configured ones. It refuses a name without the
// prefix of the fake, a machine that does not exist, and a machine that is
// not running.
func (f *Fake) ActiveMounts(_ context.Context, name string) (map[string]string, error) {
	f.Calls = append(f.Calls, Call{Op: OpActiveMounts, Name: name})
	if err := f.checkExists(name); err != nil {
		return nil, err
	}
	if err := f.checkRunning(name); err != nil {
		return nil, err
	}
	if f.ActiveMountsErr != nil {
		return nil, f.ActiveMountsErr
	}
	if active, swapped := f.Active[name]; swapped {
		return maps.Clone(active), nil
	}
	return maps.Clone(f.Machines[name]), nil
}

// Delete removes the machine name. It refuses a name without the prefix of
// the fake, a name with white space, and a machine that does not exist.
func (f *Fake) Delete(_ context.Context, name string) error {
	f.Calls = append(f.Calls, Call{Op: OpDelete, Name: name})
	if err := f.checkOwned(name); err != nil {
		return err
	}
	if strings.ContainsFunc(name, unicode.IsSpace) {
		return fmt.Errorf("%w: %q", ErrMachineName, name)
	}
	if err := f.checkExists(name); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.Machines, name)
	delete(f.Active, name)
	delete(f.Forwarded, name)
	delete(f.States, name)
	return nil
}

// checkExists refuses a name without the prefix of the fake and a machine
// that does not exist.
func (f *Fake) checkExists(name string) error {
	if err := f.checkOwned(name); err != nil {
		return err
	}
	if _, exists := f.Machines[name]; !exists {
		return fmt.Errorf("%w: %q", ErrNoMachine, name)
	}
	return nil
}

// checkOwned refuses a name that does not consist of the prefix of the fake
// and at least one more character, as the adapter does.
func (f *Fake) checkOwned(name string) error {
	if f.prefix == "" || !strings.HasPrefix(name, f.prefix) || name == f.prefix {
		return fmt.Errorf("%w %q: %q", ErrForeignMachine, f.prefix, name)
	}
	return nil
}

// RunAsRoot records script for the machine name. It refuses a name without
// the prefix of the fake and a machine that exists and is not running.
func (f *Fake) RunAsRoot(_ context.Context, name string, script []byte) error {
	f.Calls = append(f.Calls, Call{Op: OpRunAsRoot, Name: name, Script: string(script)})
	if err := f.checkNotStopped(name); err != nil {
		return err
	}
	return f.RunAsRootErr
}

// RunAsAgent records script for the machine name. It refuses a name without
// the prefix of the fake and a machine that exists and is not running.
func (f *Fake) RunAsAgent(_ context.Context, name string, script []byte) error {
	f.Calls = append(f.Calls, Call{Op: OpRunAsAgent, Name: name, Script: string(script)})
	if err := f.checkNotStopped(name); err != nil {
		return err
	}
	return f.RunAsAgentErr
}

// Shell records the shell of the agent user in the machine name, in the
// directory dir, and returns ShellStatus. It refuses a name without the
// prefix of the fake, a directory that the adapter cannot pass, and a
// machine that exists and is not running. It does not use the streams.
func (f *Fake) Shell(_ context.Context, name, dir string, _ io.Reader, _, _ io.Writer) (int, error) {
	f.Calls = append(f.Calls, Call{Op: OpShell, Name: name, Dir: dir})
	if err := f.checkNotStopped(name); err != nil {
		return 0, err
	}
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, ":,") {
		return 0, fmt.Errorf("%w: %q", ErrMountPath, dir)
	}
	if f.ShellErr != nil {
		return 0, f.ShellErr
	}
	return f.ShellStatus, nil
}

// checkNotStopped refuses a name without the prefix of the fake and a machine
// that exists and is not running. Handler tests may run a script in a
// machine that they did not add.
func (f *Fake) checkNotStopped(name string) error {
	if err := f.checkOwned(name); err != nil {
		return err
	}
	if state := f.state(name); state != "" && state != StateRunning {
		return fmt.Errorf("%w: %q", ErrStopped, name)
	}
	return nil
}
