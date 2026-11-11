package shell

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// NewCommand returns the CLI command "orbit shell", which opens a shell as
// the agent user in the sandbox of the current working directory with
// handler, on the streams of the command. It takes no arguments and prints
// nothing of its own. A shell that exits non-zero makes it return
// sandbox.ExitStatus with that status.
func NewCommand(handler Handler) *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open a shell as the agent user in the sandbox of the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, handler)
		},
	}
}

// run calls handler for the current working directory with the streams of
// cmd and returns the exit status of the shell as an error when it is not 0.
func run(cmd *cobra.Command, handler Handler) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	out, err := handler.Handle(cmd.Context(), Input{Dir: dir, In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()})
	if err != nil {
		return err
	}
	if out.Status != 0 {
		return sandbox.ExitStatus{Code: out.Status}
	}
	return nil
}
