// Package orbstacktest holds a deterministic in-memory fake of OrbStack for
// handler tests. It starts no process and needs no OrbStack.
package orbstacktest

import (
	"context"
	"errors"
	"fmt"
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
)

// Operations that a Fake records.
const (
	OpExists = "exists"
	OpCreate = "create"
)

// Call is one call that a Fake received.
type Call struct {
	Op   string
	Name string
	Dir  string
}

// Fake keeps machines in memory and records every call.
type Fake struct {
	// Machines maps the name of each machine to its mounted directory.
	Machines map[string]string
	// ExistsErr, when set, is the result of every Exists call.
	ExistsErr error
	// CreateErr, when set, is the result of every Create call.
	CreateErr error
	// Calls holds the received calls, in order.
	Calls []Call

	prefix string
}

// New returns a fake with no machines that creates only machines whose names
// start with prefix.
func New(prefix string) *Fake {
	return &Fake{Machines: map[string]string{}, prefix: prefix}
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

// Create adds the machine name with dir as its mount. It refuses a name
// without the prefix of the fake, and a directory that the adapter cannot
// mount at its own path.
func (f *Fake) Create(_ context.Context, name, dir string) error {
	f.Calls = append(f.Calls, Call{Op: OpCreate, Name: name, Dir: dir})
	if f.prefix == "" || !strings.HasPrefix(name, f.prefix) || name == f.prefix {
		return fmt.Errorf("%w %q: %q", ErrForeignMachine, f.prefix, name)
	}
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, ":,") {
		return fmt.Errorf("%w: %q", ErrMountPath, dir)
	}
	if f.CreateErr != nil {
		return f.CreateErr
	}
	f.Machines[name] = dir
	return nil
}
