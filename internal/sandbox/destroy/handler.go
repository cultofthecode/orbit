package destroy

import (
	"context"
	"fmt"
	"time"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// States of a machine that the handler acts on.
const (
	stateRunning = "running"
	stateStopped = "stopped"
)

// readBackTimeout is how long the read of the state after the delete may
// take.
const readBackTimeout = 30 * time.Second

// Machines is what the handler needs from OrbStack. It has no method that
// starts a machine or runs a command in one.
type Machines interface {
	// State returns the state of the machine name, or "" when no machine
	// has that name. It never starts a machine.
	State(ctx context.Context, name string) (string, error)
	// Delete deletes the machine name with everything stored in it.
	Delete(ctx context.Context, name string) error
}

// Handler destroys the sandbox of a project directory. All fields are
// required.
type Handler struct {
	Machines Machines
	// Confirm returns nil when the owner confirmed the delete of target and
	// ErrNotConfirmed, or the error of reading the answer, otherwise.
	Confirm func(ctx context.Context, target Target) error
}

// Handle deletes the sandbox of the project directory in.Dir. It deletes
// nothing for a directory without a sandbox, for a sandbox in any state but
// running or stopped, and unless Confirm returned nil. It fails when the
// machine is not gone after the delete.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := realpath.Resolve(in.Dir)
	if err != nil {
		return Output{}, fmt.Errorf("project directory: %w", err)
	}
	out := Output{Name: sandbox.MachineName(dir), Dir: dir}
	state, err := h.deletable(ctx, out)
	if err != nil {
		return Output{}, err
	}
	if err := h.Confirm(ctx, Target{Name: out.Name, Dir: dir, Running: state == stateRunning}); err != nil {
		return Output{}, err
	}
	if _, err := h.deletable(ctx, out); err != nil {
		return Output{}, err
	}
	return out, h.delete(ctx, out.Name)
}

// deletable returns the state of the sandbox out and fails unless it is
// running or stopped.
func (h Handler) deletable(ctx context.Context, out Output) (string, error) {
	state, err := h.Machines.State(ctx, out.Name)
	if err != nil {
		return "", fmt.Errorf("state of sandbox %s: %w", out.Name, err)
	}
	switch state {
	case stateRunning, stateStopped:
		return state, nil
	case "":
		return "", noSandbox(out.Dir)
	}
	return "", fmt.Errorf("sandbox %s is %s; %w", out.Name, state, sandbox.ErrUnexpectedState)
}

// delete deletes the machine name and reads its state once afterwards, also
// after a failed delete. It fails unless the machine is gone.
func (h Handler) delete(ctx context.Context, name string) error {
	deleteErr := h.Machines.Delete(ctx, name)
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readBackTimeout)
	defer cancel()
	state, readErr := h.Machines.State(readCtx, name)
	switch {
	case deleteErr == nil && readErr == nil && state == "":
		return nil
	case deleteErr != nil && readErr != nil:
		return partial(name, fmt.Errorf("%w; state of the sandbox after the delete: %w", deleteErr, readErr))
	case readErr != nil:
		return partial(name, fmt.Errorf("state of sandbox %s after the delete: %w", name, readErr))
	case deleteErr != nil:
		return partial(name, fmt.Errorf("%w; the sandbox is %s", deleteErr, describe(state)))
	}
	return partial(name, fmt.Errorf("sandbox %s is %s after orb delete, not gone", name, state))
}

// partial adds to cause that the sandbox may be partly deleted and what to
// do about it.
func partial(name string, cause error) error {
	return fmt.Errorf("destroy sandbox %s: %w; it may be partly deleted; run \"orbit destroy\" again", name, cause)
}

// noSandbox returns sandbox.ErrNoSandbox for the project directory dir.
func noSandbox(dir string) error {
	return fmt.Errorf(`%w for %s: run "orbit init" here, or run this command in the directory where you ran "orbit init"`, sandbox.ErrNoSandbox, sandbox.Printable(dir))
}

// describe returns state, or "gone" for a machine that no longer exists.
func describe(state string) string {
	if state == "" {
		return "gone"
	}
	return state
}
