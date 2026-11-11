// Command orbit runs coding agents in sandboxes on OrbStack.
//
// This package is the composition root. It builds the root command from the
// commands of the slices, and it is the only package that prints errors and
// sets the exit code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/agent"
	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
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
	return newRoot(initCommand(orbstack.New(sandbox.NamePrefix), agents)), nil
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
		Machines:        machines,
		Agent:           sandboxAgent(agents),
		ProvisionScript: provision.Script,
		CurrentUser:     user.Current,
	}
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

// run executes root with args and returns the exit code: 0 on success and 1
// for any error. It drops a failed write to stderr, because no other stream
// is left to report it on.
func run(ctx context.Context, root *cobra.Command, args []string, stdout, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "orbit: %v\n", err)
		return exitFailure
	}
	return exitOK
}
