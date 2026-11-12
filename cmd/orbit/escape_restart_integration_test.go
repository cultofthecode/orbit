//go:build integration

package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/provision"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
	"github.com/cultofthecode/orbit/internal/sandbox/shell"
)

// failBoundaryUnit stops the proxy, which also stops
// systemd-user-sessions.service, and leaves /run/nologin as a failed boot
// does.
const failBoundaryUnit = "set -eu\nsystemctl stop tinyproxy.service\ntouch /run/nologin\n"

// restoreBoundaryUnit starts the proxy and the login gate again.
const restoreBoundaryUnit = "set -eu\nrm -f /run/nologin\nsystemctl start tinyproxy.service systemd-user-sessions.service\n"

// boundaryActive is the control of E13: every boundary unit is active.
const boundaryActive = `for unit in nftables.service orbit-proxy-address.service tinyproxy.service orbit-harden.service systemd-user-sessions.service; do
  allowed "$unit is active" systemctl is-active --quiet "$unit"
done
`

// bootOrderScript checks the boot order after the start, as root.
const bootOrderScript = agentProbes + bootOrder + "[ \"$failures\" -eq 0 ]\n"

// TestEscapeRestart runs E13 (the attempts of E03, E05, E06, and E07 after
// orbit stop and orbit start, the boot order, and a boundary unit that
// fails at boot), E14 (orbit shell with a failed boundary unit), and O01
// (the raw orb -u agent) in one disposable sandbox.
func TestEscapeRestart(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	handler := initHandler(client, claudeRegistry(t))
	if out, err := handler.Handle(ctx, initialize.Input{Dir: dir}); err != nil || !out.Created {
		t.Fatalf("init = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	rig := escapeRig{client: client, name: name, tally: &counts}
	buildProbeImage(ctx, t, rig)
	env := newEscapeEnv(t, dir, rig)

	checkAttemptsAfterARestart(ctx, t, rig, env, dir)
	checkABoundaryUnitThatFailsAtBoot(ctx, t, rig, client, handler, dir, name)
	checkShellWithAFailedBoundaryUnit(ctx, t, rig, client, dir, name)
}

// checkAttemptsAfterARestart is E13 after a restart.
func checkAttemptsAfterARestart(ctx context.Context, t *testing.T, rig escapeRig, env escapeEnv, dir string) {
	t.Helper()
	t.Run("E13-restart", func(t *testing.T) {
		t.Log("E13-restart proves A05")
		result := attemptResult{id: "E13-restart", hasControl: true}
		if err := rig.runIn(ctx, rootSession, boundaryActive); err != nil {
			result.controlFailed, result.controlOutput = true, "before the stop: "+err.Error()
		}
		if err := rig.client.RunAsAgent(ctx, rig.name, []byte(writeStateFile)); err != nil {
			t.Fatalf("write %s in %s: %v", stateFile, rig.name, err)
		}
		restart(ctx, t, rig.client, dir)
		if err := rig.client.RunAsRoot(ctx, rig.name, []byte(bootOrderScript)); err != nil {
			result.escaped, result.evidence = true, "boot order: "+err.Error()
		}
		rig.report(t, result)
	})
	var again []attempt
	for _, group := range [][]attempt{env.macAttempts(), env.egressAttempts(), env.containerEgressAttempts(), env.dnsAttempts()} {
		again = append(again, group...)
	}
	for _, a := range again {
		a.id += "-after-restart"
		rig.run(ctx, t, a)
	}
}

// checkABoundaryUnitThatFailsAtBoot is E13 with a boundary unit that fails
// at boot: the user manager of the agent stays down. It repairs the sandbox.
func checkABoundaryUnitThatFailsAtBoot(ctx context.Context, t *testing.T, rig escapeRig, client *orbstack.Client, handler initialize.Handler, dir, name string) {
	t.Helper()
	t.Run("E13-boot-failure", func(t *testing.T) {
		t.Log("E13-boot-failure proves A05")
		result := attemptResult{id: "E13-boot-failure"}
		expectRoot(ctx, t, name, "make nftables.service fail at boot", failNftables)
		orbMachine(ctx, t, "stop", name)
		orbMachine(ctx, t, "start", name)
		if out, err := orbRoot(ctx, name, bootFailure); err != nil {
			result.escaped, result.evidence = true, err.Error()+"\n"+out
		}
		expectRoot(ctx, t, name, "remove the failing drop-in", removeFail)
		if _, err := handler.Handle(ctx, initialize.Input{Dir: dir}); err != nil {
			t.Fatalf("init to repair the failed boot: %v", err)
		}
		rig.report(t, result)
	})
}

// checkShellWithAFailedBoundaryUnit is E14 and O01: it stops a boundary unit
// and expects ErrNotProvisioned naming the unit, without a shell (#74), and
// that the raw orb session as the agent still opens, which is the known
// limit that the documents state. It restores the sandbox and expects the
// shell to open again.
func checkShellWithAFailedBoundaryUnit(ctx context.Context, t *testing.T, rig escapeRig, client *orbstack.Client, dir, name string) {
	t.Helper()
	handler := shell.Handler{Machines: client, ReadinessScript: provision.ReadinessScript}
	if err := client.RunAsRoot(ctx, name, []byte(failBoundaryUnit)); err != nil {
		t.Fatalf("fail a boundary unit of %s: %v", name, err)
	}
	_, streams, shellErr := openShell(ctx, handler, dir, "echo opened\n")
	raw, rawErr := exec.CommandContext(ctx, "orb", "-m", name, "-u", "agent", "id", "-un").CombinedOutput()
	if err := client.RunAsRoot(ctx, name, []byte(restoreBoundaryUnit)); err != nil {
		t.Fatalf("restore the boundary unit of %s: %v", name, err)
	}
	_, restoredStreams, restoredErr := openShell(ctx, handler, dir, "exit 0\n")

	t.Run("E14-shell-refusal", func(t *testing.T) {
		t.Log("E14-shell-refusal proves #74 #115")
		result := attemptResult{id: "E14-shell-refusal", hasControl: true}
		if restoredErr != nil {
			result.controlFailed, result.controlOutput = true, "orbit shell after the restore: "+restoredErr.Error()+restoredStreams
		}
		if !errors.Is(shellErr, shell.ErrNotProvisioned) || !strings.Contains(shellErr.Error(), "tinyproxy.service is not active") || streams != "" {
			result.escaped, result.evidence = true, "orbit shell with tinyproxy.service stopped = "+errString(shellErr)+", output "+streams+"; want ErrNotProvisioned naming the unit and no session"
		}
		rig.report(t, result)
	})
	t.Run("O01-raw-orb-ignores-nologin", func(t *testing.T) {
		t.Log("O01 pins an observation, not an attempt (#74 #115)")
		if got := strings.TrimSpace(string(raw)); rawErr != nil || got != "agent" {
			t.Errorf("raw orb -u agent id -un with /run/nologin and a stopped unit = %q, %v, want %q: OrbStack now honours /run/nologin, so internal/provision/AGENTS.md and .ai/specs/2026-11-10-sandbox-provisioning.md must change", got, rawErr, "agent")
			return
		}
		rig.tally.observationsHeld++
		t.Log("held: raw orb -u agent prints agent while /run/nologin exists and a boundary unit is down; OrbStack ignores /run/nologin")
	})
}

// errString returns the text of err, or "no error".
func errString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}
