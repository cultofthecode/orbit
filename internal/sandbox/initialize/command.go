package initialize

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// NewCommand returns the CLI command "orbit init", which creates the sandbox
// of the current working directory with handler and prints the result to the
// output stream of the command. It takes no arguments. Its flag --agent
// names the agent to install; its help lists agents, the names that
// handler knows. Its flag --no-ssh-forwarding leaves the SSH agent of the
// host out of the sandbox. Its flag --distro names the distribution of a new
// sandbox, one of sandbox.Distributions, ubuntu by default.
func NewCommand(handler Handler, agents []string) *cobra.Command {
	var agent, distro string
	var noSSHForwarding bool
	command := &cobra.Command{
		Use:   "init",
		Short: "Create the sandbox of the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, handler, Input{Agent: agent, NoSSHForwarding: noSSHForwarding, Distro: distro})
		},
	}
	command.Flags().StringVar(&agent, "agent", "", "agent to install in the sandbox: "+strings.Join(agents, ", ")+" (default none)")
	command.Flags().StringVar(&distro, "distro", "", "distribution of a new sandbox: "+distributionNames()+" (default "+string(sandbox.DefaultDistribution)+")")
	command.Flags().BoolVar(&noSSHForwarding, "no-ssh-forwarding", false, "do not forward the host SSH agent into the sandbox (by default the sandbox can use every key in it)")
	return command
}

// distributionNames lists the distributions for the help of --distro.
func distributionNames() string {
	var names []string
	for _, d := range sandbox.Distributions() {
		names = append(names, string(d))
	}
	return strings.Join(names, ", ")
}

// run calls handler for the current working directory and in, and prints its
// output.
func run(cmd *cobra.Command, handler Handler, in Input) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	in.Dir = dir
	out, err := handler.Handle(cmd.Context(), in)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), message(out)); err != nil {
		return fmt.Errorf("print the result: %w", err)
	}
	return nil
}

// message returns the line that tells the user what happened.
func message(out Output) string {
	if out.Created {
		return fmt.Sprintf("Created sandbox %s for %s", out.Name, out.Dir)
	}
	return fmt.Sprintf("Sandbox %s already exists for %s", out.Name, out.Dir)
}
