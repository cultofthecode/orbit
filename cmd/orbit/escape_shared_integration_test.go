//go:build integration

package main

import (
	"testing"

	"github.com/cultofthecode/orbit/internal/agent/claude"
	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// boundaryIntact is the re-check of the boundary after E09: the units are
// active, the proxy runs, and a direct connection is still refused.
const boundaryIntact = `need curl pgrep
for unit in nftables.service tinyproxy.service orbit-harden.service orbit-proxy-address.service; do
  allowed "$unit is active" systemctl is-active --quiet "$unit"
done
allowed "tinyproxy runs" pgrep -x tinyproxy
blocked "the agent reaches 1.1.1.1 directly" setpriv --reuid agent --regid agent --clear-groups curl -sS --noproxy '*' -m 5 -k -o /dev/null https://1.1.1.1
`

// TestEscapeShared runs the read-only attempts E01 to E11, and E09 last, in
// one sandbox that init --agent claude-code provisioned (Q3, Q11). E11
// creates one disposable target.
func TestEscapeShared(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	handler := initHandler(client, claudeRegistry(t))
	if out, err := handler.Handle(ctx, initialize.Input{Dir: dir, Agent: claude.Definition.Name}); err != nil || !out.Created {
		t.Fatalf("init --agent claude-code = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	rig := escapeRig{client: client, name: name, tally: &counts}
	buildProbeImage(ctx, t, rig)
	env := newEscapeEnv(ctx, t, dir, rig)
	env.target, env.targetAddr = startTarget(ctx, t, client)
	t.Logf("sandbox %s, target of E11 %s at %s", name, env.target, env.targetAddr)

	for _, group := range [][]attempt{
		env.hostPathAttempts(), env.engineAttempts(), env.macAttempts(), env.hostContainerAttempts(),
		env.egressAttempts(), env.containerEgressAttempts(), env.dnsAttempts(), env.rootAttempts(),
		env.noSessionAttempts(), env.otherSandboxAttempts(),
	} {
		rig.runAll(ctx, t, group)
	}
	rig.runAll(ctx, t, env.boundaryAttempts())
	if err := rig.runIn(ctx, rootSession, boundaryIntact); err != nil {
		t.Fatalf("the boundary of %s is damaged after E09, so no later result may come from it: %v", name, err)
	}
}
