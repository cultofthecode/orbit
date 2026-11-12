package list

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// unknown is shown for a state or a directory that is not known.
const unknown = "?"

// reasonNoState is the reason why a state is not known.
const reasonNoState = "no state"

// NewCommand returns the CLI command "orbit ls", which lists the sandboxes
// with handler and prints a table to the output stream of the command and the
// reason of every unknown value to its error stream. It takes no arguments
// and no flag.
func NewCommand(handler Handler) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List every sandbox with its state and project directory",
		Long: `List every sandbox with its state and project directory.

The directory is the one that OrbStack stores as the mount of the machine. It
is not a check of the directory that the machine mounts now, and the output
is not a proof of it. A "?" marks a value that OrbStack does not give or that
does not match the machine; the reason is printed on the error stream.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, handler)
		},
	}
}

// run calls handler and prints its output.
func run(cmd *cobra.Command, handler Handler) error {
	out, err := handler.Handle(cmd.Context(), Input{})
	if err != nil {
		return err
	}
	if err := printTable(cmd.OutOrStdout(), out.Sandboxes); err != nil {
		return fmt.Errorf("print the list: %w", err)
	}
	return printReasons(cmd.ErrOrStderr(), out.Sandboxes)
}

// printTable prints the header and one line for each sandbox. It prints
// nothing for no sandbox.
func printTable(w io.Writer, sandboxes []Sandbox) error {
	if len(sandboxes) == 0 {
		return nil
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "NAME\tSTATE\tDIRECTORY")
	for _, s := range sandboxes {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", printable(s.Name), orUnknown(s.State), orUnknown(s.Dir))
	}
	return table.Flush()
}

// orUnknown returns the printable form of value, or "?" when it is empty.
func orUnknown(value string) string {
	if value == "" {
		return unknown
	}
	return printable(value)
}

// printReasons prints one line for each reason why a value is unknown.
func printReasons(w io.Writer, sandboxes []Sandbox) error {
	for _, s := range sandboxes {
		for _, reason := range reasons(s) {
			if _, err := fmt.Fprintf(w, "orbit: %s: %s\n", printable(s.Name), printable(reason)); err != nil {
				return fmt.Errorf("print a reason: %w", err)
			}
		}
	}
	return nil
}

// reasons returns why the state or the directory of s is unknown.
func reasons(s Sandbox) []string {
	var found []string
	if s.State == "" {
		found = append(found, reasonNoState)
	}
	if s.Problem != "" {
		found = append(found, s.Problem)
	}
	return found
}

// printable returns s, or its Go-quoted form when it holds a character that
// is not printable or invalid UTF-8, so that a stored value cannot write an escape sequence or
// a line break to the terminal.
func printable(s string) string {
	if !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return strconv.Quote(s)
	}
	return s
}
