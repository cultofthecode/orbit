package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// failingWriter is a stream that refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("stream closed")
}

func testRoot() *cobra.Command {
	return newRoot(echoCommand(), failCommand(), statusCommand())
}

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Return the exit status of a command in a sandbox",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("shell: %w", sandbox.ExitStatus{Code: 3})
		},
	}
}

func echoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "echo",
		Short: "Print the arguments",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(args, " "))
			return err
		},
	}
}

func failCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "fail",
		Short: "Return an error",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("boom")
		},
	}
}

func TestRunPrintsHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), testRoot(), args, &stdout, &stderr)
			if code != exitOK {
				t.Errorf("exit code = %d, want %d", code, exitOK)
			}
			for _, want := range []string{"Orbit runs coding agents", "orbit [command]", "echo", "fail"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want it empty", stderr.String())
			}
		})
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "command gets its arguments", args: []string{"echo", "a", "b"}, wantCode: exitOK, wantStdout: "a b\n"},
		{name: "command returns an error", args: []string{"fail"}, wantCode: exitFailure, wantStderr: "orbit: boom\n"},
		{name: "command returns the exit status of a sandbox command", args: []string{"status"}, wantCode: 3},
		{name: "unknown command", args: []string{"bogus"}, wantCode: exitFailure, wantStderr: "orbit: unknown command \"bogus\" for \"orbit\"\n"},
		{name: "misspelled command gets a suggestion", args: []string{"ech"}, wantCode: exitFailure, wantStderr: "orbit: unknown command \"ech\" for \"orbit\"\n\nDid you mean this?\n\techo\n\n"},
		{name: "unknown flag", args: []string{"echo", "--nope"}, wantCode: exitFailure, wantStderr: "orbit: unknown flag: --nope\n"},
		{name: "unexpected argument", args: []string{"fail", "extra"}, wantCode: exitFailure, wantStderr: "orbit: unknown command \"extra\" for \"orbit fail\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), testRoot(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunWithoutCommandsPrintsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), newRoot(), nil, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "Orbit runs coding agents") {
		t.Errorf("stdout = %q, want the description of orbit", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want it empty", stderr.String())
	}
}

func TestRunSurvivesStderrThatCannotBeWritten(t *testing.T) {
	var stdout bytes.Buffer
	code := run(context.Background(), testRoot(), []string{"bogus"}, &stdout, failingWriter{})
	if code != exitFailure {
		t.Errorf("exit code = %d, want %d", code, exitFailure)
	}
}

func buildInfoOf(moduleVersion string) buildInfoReader {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: moduleVersion}}, true
	}
}

func noBuildInfo() (*debug.BuildInfo, bool) {
	return nil, false
}

func TestVersionFlag(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		info    buildInfoReader
		want    string
	}{
		{name: "release build", version: "v1.2.3", commit: "abc1234", info: buildInfoOf("(devel)"), want: "orbit version 1.2.3 (commit abc1234)\n"},
		{name: "linker version without a commit", version: "1.2.3", info: noBuildInfo, want: "orbit version 1.2.3\n"},
		{name: "go install", info: buildInfoOf("v1.2.3"), want: "orbit version 1.2.3\n"},
		{name: "go build in a checkout", info: buildInfoOf("v0.0.0-20261111152512-0402908abcde+dirty"), want: "orbit version 0.0.0-20261111152512-0402908abcde+dirty\n"},
		{name: "build info without a version", info: buildInfoOf(""), want: "orbit version dev\n"},
		{name: "devel build info", info: buildInfoOf("(devel)"), want: "orbit version dev\n"},
		{name: "no build info", info: noBuildInfo, want: "orbit version dev\n"},
		{name: "linker value wins over build info", version: "2.0.0", info: buildInfoOf("v1.2.3"), want: "orbit version 2.0.0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := withVersion(testRoot(), versionText(tt.version, tt.commit, tt.info))
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), root, []string{"--version"}, &stdout, &stderr)
			if code != exitOK {
				t.Errorf("exit code = %d, want %d", code, exitOK)
			}
			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want it empty", stderr.String())
			}
		})
	}
}

func TestVersionAddsNoCommand(t *testing.T) {
	root := withVersion(testRoot(), "1.0.0")
	for _, command := range root.Commands() {
		if command.Name() == "version" {
			t.Error("orbit has a version command, want only the flag")
		}
	}
}
