package shell

import (
	"context"
	"fmt"
	"io"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// stateRunning is the only state of a machine that the handler acts on.
const stateRunning = "running"

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// State returns the state of the machine name, or "" when no machine
	// has that name. It never starts a machine.
	State(ctx context.Context, name string) (string, error)
	// Mounts returns the configured mounts of every machine: for each
	// machine name, the host directory of each mount by its mount point.
	Mounts(ctx context.Context) (map[string]map[string]string, error)
	// ActiveMounts returns what the machine name mounts from the host, as
	// its kernel reports it. It starts a stopped machine.
	ActiveMounts(ctx context.Context, name string) (map[string]string, error)
	// RunAsRoot runs script with bash as root in the machine name and
	// fails when the script fails. It starts a stopped machine.
	RunAsRoot(ctx context.Context, name string, script []byte) error
	// Shell runs the login shell of the agent user in the machine name, in
	// the directory dir, with the given streams, and returns its exit
	// status. It starts a stopped machine.
	Shell(ctx context.Context, name, dir string, in io.Reader, out, errOut io.Writer) (int, error)
}

// Handler opens a shell in the sandbox of a project directory. All fields
// are required.
type Handler struct {
	Machines Machines
	// ReadinessScript returns the root script that fails unless the sandbox
	// is provisioned by this orbit and its boundary is up. cmd/orbit passes
	// provision.ReadinessScript; the slice does not import another module.
	ReadinessScript func() []byte
}

// Handle opens a shell as the agent user in the sandbox of the project
// directory in.Dir, in that directory, and returns its exit status. In
// order, it resolves the directory, reads the state of the machine and
// refuses any state but running, checks that the configuration and the
// kernel of the machine mount exactly the directory, and runs the readiness
// check. It runs no command in a machine that it has not seen running, and
// opens no shell when a step fails.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := realpath.Resolve(in.Dir)
	if err != nil {
		return Output{}, fmt.Errorf("project directory: %w", err)
	}
	out := Output{Name: sandbox.MachineName(dir), Dir: dir}
	if err := h.checkRunning(ctx, out.Name, dir); err != nil {
		return Output{}, err
	}
	if err := h.checkMounts(ctx, out.Name, dir); err != nil {
		return Output{}, err
	}
	if err := h.checkReady(ctx, out.Name, dir); err != nil {
		return Output{}, err
	}
	out.Status, err = h.Machines.Shell(ctx, out.Name, dir, in.In, in.Out, in.Err)
	if err != nil {
		return Output{}, fmt.Errorf("shell in sandbox %s: %w", out.Name, err)
	}
	return out, nil
}

// checkRunning fails unless the machine name of the project directory dir
// exists and is running.
func (h Handler) checkRunning(ctx context.Context, name, dir string) error {
	state, err := h.Machines.State(ctx, name)
	if err != nil {
		return fmt.Errorf("state of sandbox %s: %w", name, err)
	}
	switch state {
	case "":
		return fmt.Errorf(`%w for %s: run "orbit init" here, or run "orbit shell" in the directory where you ran "orbit init"`, sandbox.ErrNoSandbox, sandbox.Printable(dir))
	case stateRunning:
		return nil
	}
	return fmt.Errorf("sandbox %s is %s: %w in %s", name, state, ErrNotRunning, dir)
}

// checkMounts fails unless the configuration and the kernel of the machine
// name both mount exactly dir at its own path. The kernel check also proves
// that dir exists in the machine: orb starts the shell in / when it does
// not.
func (h Handler) checkMounts(ctx context.Context, name, dir string) error {
	machines, err := h.Machines.Mounts(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	if err := sandbox.ValidateMounts(dir, machines[name]); err != nil {
		return fmt.Errorf("configuration of sandbox %s: %w", name, err)
	}
	active, err := h.Machines.ActiveMounts(ctx, name)
	if err != nil {
		return fmt.Errorf("read the mounts of sandbox %s: %w", name, err)
	}
	if err := sandbox.ValidateMounts(dir, active); err != nil {
		return fmt.Errorf("mounts of sandbox %s: %w", name, err)
	}
	return nil
}

// checkReady runs the readiness script as root in the machine name and
// fails with ErrNotProvisioned, wrapping the error of the script, when it
// fails.
func (h Handler) checkReady(ctx context.Context, name, dir string) error {
	if err := h.Machines.RunAsRoot(ctx, name, h.ReadinessScript()); err != nil {
		return fmt.Errorf(`sandbox %s %w: %w; run "orbit init" in %s to provision it again`, name, ErrNotProvisioned, err, dir)
	}
	return nil
}
