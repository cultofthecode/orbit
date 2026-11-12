package stop

import (
	"context"
	"fmt"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// States of a machine that the handler acts on.
const (
	stateRunning = "running"
	stateStopped = "stopped"
)

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// State returns the state of the machine name, or "" when no machine
	// has that name. It never starts a machine.
	State(ctx context.Context, name string) (string, error)
	// Stop stops the machine name and keeps its disk.
	Stop(ctx context.Context, name string) error
}

// Handler stops the sandbox of a project directory. All fields are
// required.
type Handler struct {
	Machines Machines
}

// Handle stops the sandbox of the project directory in.Dir. It stops
// nothing when the sandbox is already stopped, and refuses a directory
// without a sandbox and a sandbox in any state but running or stopped. It
// fails when the machine is not stopped after the stop.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := realpath.Resolve(in.Dir)
	if err != nil {
		return Output{}, fmt.Errorf("project directory: %w", err)
	}
	out := Output{Name: sandbox.MachineName(dir), Dir: dir}
	state, err := h.state(ctx, out.Name)
	if err != nil {
		return Output{}, err
	}
	switch state {
	case "":
		return Output{}, noSandbox(dir)
	case stateStopped:
		out.AlreadyStopped = true
		return out, nil
	case stateRunning:
		return out, h.stop(ctx, out.Name)
	}
	return Output{}, unexpectedState(out.Name, state)
}

// stop stops the machine name and checks that it is stopped then.
func (h Handler) stop(ctx context.Context, name string) error {
	if err := h.Machines.Stop(ctx, name); err != nil {
		return fmt.Errorf("stop sandbox %s: %w", name, err)
	}
	state, err := h.state(ctx, name)
	if err != nil {
		return err
	}
	if state != stateStopped {
		return fmt.Errorf("sandbox %s is %s after the stop, not %s", name, describe(state), stateStopped)
	}
	return nil
}

// state returns the state of the machine name.
func (h Handler) state(ctx context.Context, name string) (string, error) {
	state, err := h.Machines.State(ctx, name)
	if err != nil {
		return "", fmt.Errorf("state of sandbox %s: %w", name, err)
	}
	return state, nil
}

// noSandbox returns sandbox.ErrNoSandbox for the project directory dir.
func noSandbox(dir string) error {
	return fmt.Errorf(`%w for %s: run "orbit init" here, or run this command in the directory where you ran "orbit init"`, sandbox.ErrNoSandbox, sandbox.Printable(dir))
}

// unexpectedState returns sandbox.ErrUnexpectedState for the machine name in
// state.
func unexpectedState(name, state string) error {
	return fmt.Errorf("sandbox %s is %s; %w", name, state, sandbox.ErrUnexpectedState)
}

// describe returns state, or "gone" for a machine that no longer exists.
func describe(state string) string {
	if state == "" {
		return "gone"
	}
	return state
}
