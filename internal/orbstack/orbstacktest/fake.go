// Package orbstacktest holds a deterministic in-memory fake of OrbStack for
// handler tests. It starts no process and needs no OrbStack.
package orbstacktest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
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
)

// Operations that a Fake records.
const (
	OpExists       = "exists"
	OpCreate       = "create"
	OpMounts       = "mounts"
	OpActiveMounts = "active-mounts"
	OpDelete       = "delete"
	OpRunAsRoot    = "run-as-root"
)

// Call is one call that a Fake received.
type Call struct {
	Op     string
	Name   string
	Dir    string
	Script string
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
	// ExistsErr, when set, is the result of every Exists call.
	ExistsErr error
	// CreateErr, when set, is the result of every Create call.
	CreateErr error
	// MountsErr, when set, is the result of every Mounts call.
	MountsErr error
	// ActiveMountsErr, when set, is the result of every ActiveMounts call.
	ActiveMountsErr error
	// DeleteErr, when set, is the result of every Delete call.
	DeleteErr error
	// RunAsRootErr, when set, is the result of every RunAsRoot call.
	RunAsRootErr error
	// Calls holds the received calls, in order.
	Calls []Call

	prefix string
}

// New returns a fake with no machines that creates only machines whose names
// start with prefix.
func New(prefix string) *Fake {
	return &Fake{Machines: map[string]map[string]string{}, Active: map[string]map[string]string{}, prefix: prefix}
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

// Create adds the machine name with dir mounted at its own path. It refuses
// a name without the prefix of the fake, and a directory that the adapter
// cannot mount at its own path.
func (f *Fake) Create(_ context.Context, name, dir string) error {
	f.Calls = append(f.Calls, Call{Op: OpCreate, Name: name, Dir: dir})
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
	return nil
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

// ActiveMounts returns a copy of the active mounts of the machine name: those
// in Active, or else the configured ones. It refuses a name without the
// prefix of the fake and a machine that does not exist.
func (f *Fake) ActiveMounts(_ context.Context, name string) (map[string]string, error) {
	f.Calls = append(f.Calls, Call{Op: OpActiveMounts, Name: name})
	if err := f.checkExists(name); err != nil {
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
// the fake and a machine that does not exist.
func (f *Fake) Delete(_ context.Context, name string) error {
	f.Calls = append(f.Calls, Call{Op: OpDelete, Name: name})
	if err := f.checkExists(name); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.Machines, name)
	delete(f.Active, name)
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
// the prefix of the fake.
func (f *Fake) RunAsRoot(_ context.Context, name string, script []byte) error {
	f.Calls = append(f.Calls, Call{Op: OpRunAsRoot, Name: name, Script: string(script)})
	if err := f.checkOwned(name); err != nil {
		return err
	}
	return f.RunAsRootErr
}
