// Package orbstacktest holds a deterministic in-memory fake of OrbStack for
// handler tests. It starts no process and needs no OrbStack.
package orbstacktest

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrForeignMachine reports a machine name without the prefix of the fake,
// as the adapter does.
var ErrForeignMachine = errors.New("machine name does not start with the required prefix")

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
// without the prefix of the fake.
func (f *Fake) Create(_ context.Context, name, dir string) error {
	f.Calls = append(f.Calls, Call{Op: OpCreate, Name: name, Dir: dir})
	if f.prefix == "" || !strings.HasPrefix(name, f.prefix) || name == f.prefix {
		return fmt.Errorf("%w %q: %q", ErrForeignMachine, f.prefix, name)
	}
	if f.CreateErr != nil {
		return f.CreateErr
	}
	f.Machines[name] = dir
	return nil
}
