package start

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"time"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/hostcheck"
)

// States of a machine that the handler acts on.
const (
	stateRunning = "running"
	stateStopped = "stopped"
)

// stopTimeout bounds the stop of a machine that failed a check after the
// start.
const stopTimeout = 2 * time.Minute

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// State returns the state of the machine name, or "" when no machine
	// has that name. It never starts a machine.
	State(ctx context.Context, name string) (string, error)
	// Mounts returns the configured mounts of every machine: for each
	// machine name, the host directory of each mount by its mount point.
	Mounts(ctx context.Context) (map[string]map[string]string, error)
	// Start starts the machine name.
	Start(ctx context.Context, name string) error
	// Stop stops the machine name and keeps its disk.
	Stop(ctx context.Context, name string) error
	// ActiveMounts returns what the machine name mounts from the host, as
	// its kernel reports it. It starts a stopped machine.
	ActiveMounts(ctx context.Context, name string) (map[string]string, error)
	// RunAsRoot runs script with bash as root in the machine name and
	// fails when the script fails. It starts a stopped machine.
	RunAsRoot(ctx context.Context, name string, script []byte) error
}

// Handler starts the sandbox of a project directory. All fields are
// required.
type Handler struct {
	Machines Machines
	// CurrentUser returns the user who runs Orbit, from the account
	// database of the system. cmd/orbit passes user.Current.
	CurrentUser func() (*user.User, error)
	// BootCheckScript returns the root script that waits for the boot and
	// fails unless the boundary is up. cmd/orbit passes
	// provision.BootCheckScript; the slice does not import another module.
	BootCheckScript func() []byte
	// MarkerCheckScript returns the root script that fails unless the
	// provisioning marker holds the version of this orbit. cmd/orbit passes
	// provision.MarkerCheckScript.
	MarkerCheckScript func() []byte
}

// Handle starts the sandbox of the project directory in.Dir. It first runs
// the project directory rules of init, in every state. For a stopped
// sandbox it then checks the configured mounts, the nesting refusal, and the
// stored mount paths of every machine, its own included, before it starts
// the machine; after the start it runs the boot check and checks the mounts
// in the kernel, and stops the machine again when one of them fails
// (ErrBoundaryDown). Last, it checks the provisioning marker and leaves the
// machine running when only that fails (ErrNotProvisioned). It starts
// nothing for a running sandbox, a directory without a sandbox, and a
// sandbox in any other state.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	facts, err := hostcheck.ProjectDir(in.Dir, h.CurrentUser)
	if err != nil {
		return Output{}, err
	}
	out := Output{Name: sandbox.MachineName(facts.Path), Dir: facts.Path}
	state, err := h.Machines.State(ctx, out.Name)
	if err != nil {
		return Output{}, fmt.Errorf("state of sandbox %s: %w", out.Name, err)
	}
	switch state {
	case "":
		return Output{}, noSandbox(out.Dir)
	case stateRunning:
		out.AlreadyRunning = true
		return out, nil
	case stateStopped:
		return out, h.start(ctx, out.Name, out.Dir)
	}
	return Output{}, unexpectedState(out.Name, state)
}

// start starts the stopped machine name of the project directory dir after
// the host-side checks, and checks it after the start.
func (h Handler) start(ctx context.Context, name, dir string) error {
	if err := h.checkHost(ctx, name, dir); err != nil {
		return err
	}
	if err := h.startChecked(ctx, name, dir); err != nil {
		return err
	}
	return h.checkMarker(ctx, name, dir)
}

// checkHost checks that the configured mounts of the machine name are
// exactly dir, that dir is not nested with the directory of another
// machine, and that no stored mount path of any machine, name included,
// passes through a symlink.
func (h Handler) checkHost(ctx context.Context, name, dir string) error {
	machines, err := h.Machines.Mounts(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	if err := sandbox.ValidateMounts(dir, machines[name]); err != nil {
		return fmt.Errorf("configuration of sandbox %s: %w", name, err)
	}
	return hostcheck.Unnested(machines, name, dir, true)
}

// startChecked starts the machine name and checks that its boundary is up
// and that its kernel mounts exactly dir. When the start or a check fails,
// it stops the machine again.
func (h Handler) startChecked(ctx context.Context, name, dir string) error {
	err := h.startAndCheck(ctx, name, dir)
	if err == nil {
		return nil
	}
	return h.stopAgain(ctx, name, dir, err)
}

// startAndCheck starts the machine name, runs the boot check, and checks the
// mounts in its kernel.
func (h Handler) startAndCheck(ctx context.Context, name, dir string) error {
	if err := h.Machines.Start(ctx, name); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	if err := h.Machines.RunAsRoot(ctx, name, h.BootCheckScript()); err != nil {
		return fmt.Errorf("boot check: %w", err)
	}
	active, err := h.Machines.ActiveMounts(ctx, name)
	if err != nil {
		return fmt.Errorf("read the mounts: %w", err)
	}
	if err := sandbox.ValidateMounts(dir, active); err != nil {
		return fmt.Errorf("mounts: %w", err)
	}
	return nil
}

// stopAgain stops the machine name after the failure cause, also when ctx is
// cancelled, within stopTimeout, and reports ErrBoundaryDown with the cause,
// and the failure of the stop when it fails too.
func (h Handler) stopAgain(ctx context.Context, name, dir string, cause error) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	down := fmt.Errorf("sandbox %s %w: %w", name, ErrBoundaryDown, cause)
	if err := h.Machines.Stop(stopCtx, name); err != nil {
		return errors.Join(down, fmt.Errorf(`stop sandbox %s again: %w; the sandbox may still run: run "orbit stop" in %s`, name, err, dir))
	}
	return fmt.Errorf("%w; the sandbox was stopped again", down)
}

// checkMarker fails with ErrNotProvisioned when the provisioning marker of
// the running machine name does not hold the version of this orbit.
func (h Handler) checkMarker(ctx context.Context, name, dir string) error {
	if err := h.Machines.RunAsRoot(ctx, name, h.MarkerCheckScript()); err != nil {
		return fmt.Errorf(`sandbox %s runs, but %w; run "orbit init" in %s to provision it again: %w`, name, ErrNotProvisioned, dir, err)
	}
	return nil
}

// noSandbox returns sandbox.ErrNoSandbox for the project directory dir.
func noSandbox(dir string) error {
	return fmt.Errorf(`%w for %s: run "orbit init" here, or run this command in the directory where you ran "orbit init"`, sandbox.ErrNoSandbox, dir)
}

// unexpectedState returns sandbox.ErrUnexpectedState for the machine name in
// state.
func unexpectedState(name, state string) error {
	return fmt.Errorf("sandbox %s is %s; %w", name, state, sandbox.ErrUnexpectedState)
}
