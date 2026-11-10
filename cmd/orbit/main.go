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

	"github.com/spf13/cobra"
)

// Exit codes of orbit.
const (
	exitOK      = 0
	exitFailure = 1
)

func main() {
	os.Exit(run(context.Background(), newRoot(), os.Args[1:], os.Stdout, os.Stderr))
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
