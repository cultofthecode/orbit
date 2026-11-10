// Command orbit runs coding agents in sandboxes on OrbStack.
//
// This package is the composition root. It holds the table of commands, and
// it is the only package that prints errors and sets the exit code.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// Exit codes of orbit.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// usageIntro is the part of the usage text that comes before the command rows.
const usageIntro = `Orbit runs coding agents in sandboxes on OrbStack.

Usage:
  orbit <command> [arguments]

Commands:
`

// errUnknownCommand reports a first argument that names no command.
var errUnknownCommand = errors.New("unknown command")

// command is one entry of the command table.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, stdout, stderr io.Writer) error
}

// commands is the command table of orbit. To add a command, add one entry.
var commands = []command{}

func main() {
	os.Exit(run(context.Background(), commands, os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command that args names from table and returns the exit
// code: 0 on success, 2 for an unknown command, and 1 for any other error.
func run(ctx context.Context, table []command, args []string, stdout, stderr io.Writer) int {
	err := dispatch(ctx, table, args, stdout, stderr)
	if err == nil {
		return exitOK
	}
	return report(stderr, table, err)
}

// dispatch prints the usage for a help request, and otherwise runs the command
// that the first argument names with the remaining arguments.
func dispatch(ctx context.Context, table []command, args []string, stdout, stderr io.Writer) error {
	if wantsHelp(args) {
		return printUsage(stdout, table)
	}
	cmd, found := lookup(table, args[0])
	if !found {
		return fmt.Errorf("%w %q", errUnknownCommand, args[0])
	}
	return cmd.run(ctx, args[1:], stdout, stderr)
}

// wantsHelp reports whether args are empty or start with a help form.
func wantsHelp(args []string) bool {
	return len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help"
}

// lookup returns the command of table with the given name.
func lookup(table []command, name string) (command, bool) {
	for _, cmd := range table {
		if cmd.name == name {
			return cmd, true
		}
	}
	return command{}, false
}

// report prints err to stderr, followed by the usage when the command is
// unknown, and returns the exit code for err. It drops a failed write to
// stderr, because no other stream is left to report it on.
func report(stderr io.Writer, table []command, err error) int {
	if errors.Is(err, errUnknownCommand) {
		_, _ = fmt.Fprintf(stderr, "orbit: %v\n\n%s", err, usage(table))
		return exitUsage
	}
	_, _ = fmt.Fprintf(stderr, "orbit: %v\n", err)
	return exitFailure
}

// printUsage writes the usage text to w.
func printUsage(w io.Writer, table []command) error {
	if _, err := io.WriteString(w, usage(table)); err != nil {
		return fmt.Errorf("print usage: %w", err)
	}
	return nil
}

// usage returns the usage text: the intro, one row for each command of table,
// and the row of help.
func usage(table []command) string {
	rows := append(slices.Clone(table), command{name: "help", summary: "Show this help"})
	width := longestName(rows)
	var text strings.Builder
	text.WriteString(usageIntro)
	for _, row := range rows {
		fmt.Fprintf(&text, "  %-*s  %s\n", width, row.name, row.summary)
	}
	return text.String()
}

// longestName returns the length of the longest command name in rows.
func longestName(rows []command) int {
	width := 0
	for _, row := range rows {
		width = max(width, len(row.name))
	}
	return width
}
