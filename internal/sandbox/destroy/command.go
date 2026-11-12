package destroy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// NewCommand returns the CLI command "orbit destroy", which deletes the
// sandbox of the current working directory with handler and prints the
// result to the output stream of the command. The handler gets its Confirm
// from the flags and the streams of the command: --yes skips the prompt,
// which goes to the error stream and needs a terminal on standard input.
func NewCommand(handler Handler) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   "destroy",
		Short: "Delete the sandbox of the current directory",
		Long:  "Delete the sandbox of the current directory with everything on its disk: the login, history, and images of the agent. The project files stay. It asks first, unless --yes is passed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			handler.Confirm = confirmer(yes, cmd.InOrStdin(), cmd.ErrOrStderr(), isTerminal)
			return run(cmd, handler)
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")
	return command
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
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Destroyed sandbox %s for %s\n", sandbox.Printable(out.Name), sandbox.Printable(out.Dir)); err != nil {
		return fmt.Errorf("print the result: %w", err)
	}
	return nil
}

// confirmer returns the Confirm function of a handler. With yes it confirms
// without reading input. Otherwise it needs a terminal on in, asks on
// errOut, and confirms only for the answer y or yes in any letter case.
func confirmer(yes bool, in io.Reader, errOut io.Writer, isTerminal func(io.Reader) bool) func(context.Context, Target) error {
	return func(_ context.Context, target Target) error {
		if yes {
			return nil
		}
		if !isTerminal(in) {
			return fmt.Errorf(`%w: standard input is not a terminal; run "orbit destroy --yes" to destroy sandbox %s without a prompt`, ErrNotConfirmed, target.Name)
		}
		if _, err := fmt.Fprint(errOut, prompt(target)); err != nil {
			return fmt.Errorf("print the prompt: %w", err)
		}
		return answer(in)
	}
}

// answer reads one line from in and returns nil for y or yes. A line that
// ends the input without a newline is not an answer.
func answer(in io.Reader) error {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read the answer: %w", err)
	}
	if err == nil {
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return nil
		}
	}
	return ErrNotConfirmed
}

// prompt returns the question for target.
func prompt(target Target) string {
	running := ""
	if target.Running {
		running = " It runs; open shells end."
	}
	return fmt.Sprintf("Destroy sandbox %s for %s?%s The login, history, and images of the agent in it are deleted; the project files stay. [y/N] ", sandbox.Printable(target.Name), sandbox.Printable(target.Dir), running)
}
