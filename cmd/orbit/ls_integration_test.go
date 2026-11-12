//go:build integration

package main

import (
	"bytes"
	"context"
	"strings"
	"syscall"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// TestListSandboxes provisions two sandboxes in directories of their own,
// stops one, and runs orbit ls (#13). It lists both with their state and
// their canonical directory, lists only names with the Orbit prefix, and
// leaves the stopped sandbox stopped. It never asserts that no other machine
// exists: the host may have real sandboxes. The check that orbit ls no longer
// lists a sandbox after orbit destroy is in
// TestDestroyDeletesTheSandboxAndNothingElse (#12).
func TestListSandboxes(t *testing.T) {
	lockInit(t, syscall.LOCK_SH)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dirA, nameA := newSandboxDir(ctx, t, client)
	dirB, nameB := newSandboxDir(ctx, t, client)
	setUp := initHandler(client, claudeRegistry(t))
	for dir, name := range map[string]string{dirA: nameA, dirB: nameB} {
		if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir}); err != nil {
			t.Fatalf("init in %s = %v; machines that this test did not create: %s", dir, err, otherMachines(ctx, client, name))
		}
	}
	if _, err := (stop.Handler{Machines: client}).Handle(ctx, stop.Input{Dir: dirB}); err != nil {
		t.Fatalf("stop of %s = %v", nameB, err)
	}

	stdout, stderr := runLs(ctx, t, client)

	t.Logf("orbit ls:\n%s", stdout)
	t.Logf("orbit ls stderr:\n%s", stderr)
	checkRow(t, stdout, nameA, "running", dirA)
	checkRow(t, stdout, nameB, "stopped", dirB)
	checkEveryNameHasThePrefix(t, stdout)
	expectState(ctx, t, client, nameA, "running", "after orbit ls")
	expectState(ctx, t, client, nameB, "stopped", "after orbit ls")
	expectOrbStackRunning(t)
}

// runLs runs orbit ls through the root command on client and returns its
// output streams. It fails t unless the exit code is 0.
func runLs(ctx context.Context, t *testing.T, client *orbstack.Client) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := newRoot(listCommand(listMachines{client}))
	if code := run(ctx, root, []string{"ls"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("orbit ls = %d, %s", code, stderr.String())
	}
	return stdout.String(), stderr.String()
}

// checkRow fails t unless stdout has exactly one row for name with state and
// dir.
func checkRow(t *testing.T, stdout, name, state, dir string) {
	t.Helper()
	var rows []string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, name+" ") {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 || strings.Join(strings.Fields(rows[0]), " ") != name+" "+state+" "+dir {
		t.Errorf("rows of %s = %q, want one row with state %s and directory %s", name, rows, state, dir)
	}
}

// checkEveryNameHasThePrefix fails t unless stdout has the header and only
// names with the Orbit prefix.
func checkEveryNameHasThePrefix(t *testing.T, stdout string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "NAME STATE DIRECTORY" {
		t.Fatalf("first line of orbit ls = %q, want the header", lines)
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, sandbox.NamePrefix) {
			t.Errorf("orbit ls lists %q, want only names with the prefix %q", line, sandbox.NamePrefix)
		}
	}
}
