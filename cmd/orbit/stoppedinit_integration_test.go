//go:build integration

package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/start"
	"github.com/cultofthecode/orbit/internal/sandbox/stop"
)

// TestInitRefusesAStoppedSandboxAndNeverStartsIt provisions a sandbox in a
// directory of its own, stops it, and runs init again (#75). Init refuses
// with ErrNotRunning, names orbit start, and the machine stays stopped. After
// orbit start, the same init succeeds.
func TestInitRefusesAStoppedSandboxAndNeverStartsIt(t *testing.T) {
	lockInit(t, syscall.LOCK_SH)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	setUp := initHandler(client, claudeRegistry(t))
	if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t)}); err != nil {
		t.Fatalf("init in %s = %v; machines that this test did not create: %s", dir, err, otherMachines(ctx, client, name))
	}
	if _, err := (stop.Handler{Machines: client}).Handle(ctx, stop.Input{Dir: dir}); err != nil {
		t.Fatalf("stop of %s = %v", name, err)
	}
	expectState(ctx, t, client, name, "stopped", "after the stop")

	_, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t)})

	if !errors.Is(err, initialize.ErrNotRunning) || !strings.Contains(err.Error(), "orbit start") {
		t.Errorf("init in a stopped sandbox = %v, want %v naming orbit start", err, initialize.ErrNotRunning)
	}
	t.Logf("init in a stopped sandbox: %v", err)
	expectState(ctx, t, client, name, "stopped", "after the refused init")

	if _, err := startHandler(client).Handle(ctx, start.Input{Dir: dir}); err != nil {
		t.Fatalf("start of %s = %v", name, err)
	}
	if _, err := setUp.Handle(ctx, initialize.Input{Dir: dir, Distro: integrationDistro(t)}); err != nil {
		t.Errorf("init after orbit start = %v, want success", err)
	}
	expectOrbStackRunning(t)
}
