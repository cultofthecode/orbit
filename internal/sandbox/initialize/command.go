package initialize

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Command is the CLI command "orbit init".
type Command struct {
	Handler Handler
}

// Run creates the sandbox of the current working directory and prints the
// result to stdout. It takes no arguments.
func (c Command) Run(ctx context.Context, args []string, stdout, _ io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("%w %q", ErrUnexpectedArgument, args[0])
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	out, err := c.Handler.Handle(ctx, Input{Dir: dir})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, message(out)); err != nil {
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
