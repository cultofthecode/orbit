package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const testUsage = `Orbit runs coding agents in sandboxes on OrbStack.

Usage:
  orbit <command> [arguments]

Commands:
  echo  Print the arguments
  fail  Return an error
  help  Show this help
`

// failingWriter is a stream that refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("stream closed")
}

func testTable() []command {
	return []command{
		{name: "echo", summary: "Print the arguments", run: echo},
		{name: "fail", summary: "Return an error", run: fail},
	}
}

func echo(_ context.Context, args []string, stdout, _ io.Writer) error {
	_, err := fmt.Fprintln(stdout, strings.Join(args, " "))
	return err
}

func fail(context.Context, []string, io.Writer, io.Writer) error {
	return errors.New("boom")
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no arguments", args: nil, wantCode: 0, wantStdout: testUsage},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: testUsage},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantStdout: testUsage},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantStdout: testUsage},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 2, wantStderr: "orbit: unknown command \"bogus\"\n\n" + testUsage},
		{name: "empty command name", args: []string{""}, wantCode: 2, wantStderr: "orbit: unknown command \"\"\n\n" + testUsage},
		{name: "command gets the remaining arguments", args: []string{"echo", "a", "-h"}, wantCode: 0, wantStdout: "a -h\n"},
		{name: "command returns an error", args: []string{"fail"}, wantCode: 1, wantStderr: "orbit: boom\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), testTable(), tt.args, &stdout, &stderr)
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

func TestRunWithoutCommandsListsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.HasSuffix(stdout.String(), "Commands:\n  help  Show this help\n") {
		t.Errorf("stdout = %q, want the help row as the only command", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want it empty", stderr.String())
	}
}

func TestRunReportsUsageThatCannotBeWritten(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), testTable(), []string{"help"}, failingWriter{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if want := "orbit: print usage: stream closed\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunSurvivesStderrThatCannotBeWritten(t *testing.T) {
	var stdout bytes.Buffer
	code := run(context.Background(), testTable(), []string{"bogus"}, &stdout, failingWriter{})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
