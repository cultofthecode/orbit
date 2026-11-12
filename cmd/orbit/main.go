// Command orbit runs coding agents in sandboxes on OrbStack.
//
// This package is the composition root. It builds the root command from the
// commands of the slices, and it is the only package that prints errors and
// sets the exit code.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/agent"
	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/destroy"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/list"
	"github.com/cultofthecode/orbit/internal/sandbox/shell"
	"github.com/cultofthecode/orbit/internal/sandbox/start"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// Exit codes of orbit.
const (
	exitOK      = 0
	exitFailure = 1
)

func main() {
	root, err := newOrbit()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "orbit: %v\n", err)
		os.Exit(exitFailure)
	}
	os.Exit(run(context.Background(), root, os.Args[1:], os.Stdout, os.Stderr))
}

// newOrbit returns the root command of orbit with the command of every
// slice, on OrbStack, with the registered agents. To add an agent, add its
// definition to the registry.
func newOrbit() (*cobra.Command, error) {
	agents, err := agent.NewRegistry(claude.Definition)
	if err != nil {
		return nil, err
	}
	machines := orbstack.New(sandbox.NamePrefix)
	root := newRoot(initCommand(machines, agents), listCommand(listMachines{machines}), shellCommand(machines), startCommand(machines), stopCommand(machines), destroyCommand(machines))
	return withVersion(root, versionText(version, commit, debug.ReadBuildInfo)), nil
}

// initCommand returns the command "orbit init" on machines, with the agents
// of the registry.
func initCommand(machines initialize.Machines, agents *agent.Registry) *cobra.Command {
	return initialize.NewCommand(initHandler(machines, agents), agents.Names())
}

// initHandler returns the handler of "orbit init" on machines, with the
// agents of the registry.
func initHandler(machines initialize.Machines, agents *agent.Registry) initialize.Handler {
	return initialize.Handler{
		Machines:          machines,
		Agent:             sandboxAgent(agents),
		ProvisionScript:   provisionScript,
		DockerSetupScript: provision.DockerSetupScript,
		CurrentUser:       user.Current,
	}
}

// provisionScript fills the input of provision.Script from the arguments of
// the handler of "orbit init".
func provisionScript(defaultUID uint32, agentDomains []string, forwardSSHAgent bool) ([]byte, error) {
	return provision.Script(provision.ScriptInput{DefaultUID: defaultUID, AgentDomains: agentDomains, ForwardSSHAgent: forwardSSHAgent})
}

// machineLister is the part of the adapter that listMachines converts.
type machineLister interface {
	List(ctx context.Context) ([]orbstack.Machine, error)
}

// listMachines gives the slice of "orbit ls" the machines of the adapter as
// its own type, because no slice imports the adapter.
type listMachines struct {
	lister machineLister
}

// List returns the machines of the adapter as list.Machine, with the source
// and the destination of every mount in their own fields.
func (l listMachines) List(ctx context.Context) ([]list.Machine, error) {
	machines, err := l.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	converted := make([]list.Machine, 0, len(machines))
	for _, machine := range machines {
		item := list.Machine{Name: machine.Name, State: machine.State}
		for _, mount := range machine.Mounts {
			item.Mounts = append(item.Mounts, sandbox.Mount{Source: mount.Source, Destination: mount.Destination})
		}
		converted = append(converted, item)
	}
	return converted, nil
}

// listCommand returns the command "orbit ls" on machines.
func listCommand(machines list.Machines) *cobra.Command {
	return list.NewCommand(list.Handler{Machines: machines})
}

// shellCommand returns the command "orbit shell" on machines, with the
// readiness check of provisioning.
func shellCommand(machines shell.Machines) *cobra.Command {
	return shell.NewCommand(shell.Handler{Machines: machines, ReadinessScript: provision.ReadinessScript})
}

// startCommand returns the command "orbit start" on machines.
func startCommand(machines start.Machines) *cobra.Command {
	return start.NewCommand(startHandler(machines))
}

// startHandler returns the handler of "orbit start" on machines, with the
// check scripts of provisioning.
func startHandler(machines start.Machines) start.Handler {
	return start.Handler{
		Machines:          machines,
		CurrentUser:       user.Current,
		BootCheckScript:   provision.BootCheckScript,
		MarkerCheckScript: provision.MarkerCheckScript,
	}
}

// stopCommand returns the command "orbit stop" on machines.
func stopCommand(machines stop.Machines) *cobra.Command {
	return stop.NewCommand(stop.Handler{Machines: machines})
}

// destroyCommand returns the command "orbit destroy" on machines. The command
// fills the Confirm field of the handler from its flags and streams.
func destroyCommand(machines destroy.Machines) *cobra.Command {
	return destroy.NewCommand(destroy.Handler{Machines: machines})
}

// sandboxAgent returns a lookup of the agents of the registry that gives a
// slice what it needs of a definition.
func sandboxAgent(agents *agent.Registry) func(name string) (sandbox.Agent, error) {
	return func(name string) (sandbox.Agent, error) {
		definition, err := agents.Lookup(name)
		if err != nil {
			return sandbox.Agent{}, err
		}
		return sandbox.Agent{Install: definition.Install, EgressDomains: definition.EgressDomains}, nil
	}
}

// newRoot returns the root command of orbit with the given commands. It
// silences the error and usage output of cobra, so that run alone reports
// errors. To add a command, pass the command of its slice.
func newRoot(commands ...*cobra.Command) *cobra.Command {
	root := &cobra.Command{
		Use:           "orbit",
		Short:         "Orbit runs coding agents in sandboxes on OrbStack.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(commands...)
	return root
}

// run executes root with args and returns the exit code: 0 on success, the
// code of a sandbox.ExitStatus without printing anything, because the
// command in the sandbox already reported to the user, and 1 for any other
// error. It drops a failed write to stderr, because no other stream is left
// to report it on.
func run(ctx context.Context, root *cobra.Command, args []string, stdout, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	var status sandbox.ExitStatus
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &status):
		return status.Code
	}
	_, _ = fmt.Fprintf(stderr, "orbit: %v\n", err)
	return exitFailure
}
