package list

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// Machine is a machine of OrbStack with its stored mounts. The slice has its
// own type because no slice imports the adapter.
type Machine struct {
	Name   string
	State  string
	Mounts []sandbox.Mount
}

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// List returns the machines with the name prefix of Orbit, from one
	// read. It never starts OrbStack or a machine, and fails when the list
	// cannot be read as a whole.
	List(ctx context.Context) ([]Machine, error)
}

// Handler lists the sandboxes. All fields are required.
type Handler struct {
	Machines Machines
}

// Handle returns every machine with the Orbit name prefix, sorted by name,
// with its state and its project directory. It drops every other name again,
// and it calls nothing but List.
func (h Handler) Handle(ctx context.Context, _ Input) (Output, error) {
	machines, err := h.Machines.List(ctx)
	if err != nil {
		return Output{}, fmt.Errorf("list sandboxes: %w", err)
	}
	var out Output
	for _, machine := range machines {
		if !strings.HasPrefix(machine.Name, sandbox.NamePrefix) || machine.Name == sandbox.NamePrefix {
			continue
		}
		out.Sandboxes = append(out.Sandboxes, describe(machine))
	}
	slices.SortStableFunc(out.Sandboxes, func(a, b Sandbox) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// describe returns the sandbox of a machine, with its stored project
// directory or the reason why that is unknown.
func describe(machine Machine) Sandbox {
	s := Sandbox{Name: machine.Name, State: machine.State}
	dir, err := sandbox.StoredProjectDir(machine.Name, machine.Mounts)
	if err != nil {
		s.Problem = err.Error()
		return s
	}
	s.Dir = dir
	return s
}
