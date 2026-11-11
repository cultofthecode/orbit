package initialize

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// NewCommand returns the CLI command "orbit init", which creates the sandbox
// of the current working directory with handler and prints the result to the
// output stream of the command. It takes no arguments. Its flag --agent
// names the agent to install; its help lists agents, the names that
// handler knows.
func NewCommand(handler Handler, agents []string) *cobra.Command {
	var agent string
	command := &cobra.Command{
		Use:   "init",
		Short: "Create the sandbox of the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, handler, agent)
		},
	}
	command.Flags().StringVar(&agent, "agent", "", "agent to install in the sandbox: "+strings.Join(agents, ", ")+" (default none)")
	return command
}

// run calls handler for the current working directory and the agent, and
// prints its output.
func run(cmd *cobra.Command, handler Handler, agent string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	out, err := handler.Handle(cmd.Context(), Input{Dir: dir, Agent: agent})
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
