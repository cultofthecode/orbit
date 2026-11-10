package initialize

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewCommand returns the CLI command "orbit init", which creates the sandbox
// of the current working directory with handler and prints the result to the
// output stream of the command. It takes no arguments.
func NewCommand(handler Handler) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the sandbox of the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, handler)
		},
	}
}

// run calls handler for the current working directory and prints its output.
func run(cmd *cobra.Command, handler Handler) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	out, err := handler.Handle(cmd.Context(), Input{Dir: dir})
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
