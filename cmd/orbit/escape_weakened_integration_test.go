//go:build integration

package main

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/proxy"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// proxyLogPath is the log of tinyproxy.
const proxyLogPath = "/var/log/tinyproxy/tinyproxy.log"

// allowTheMac is what root writes in E12: an allow rule for the bridge
// address of the Mac and for host.orb.internal, which bypasses
// policy.ValidateHost on purpose. tinyproxy reloads its filter on SIGUSR1.
const allowTheMac = `set -eu
printf '%s\n' '^192\.168\.139\.3$' '^host\.orb\.internal$' >> ` + proxy.AllowlistPath + `
kill -USR1 "$(pgrep -o -x tinyproxy)"
sleep 2
`

// weakenTheFirewall is what root does in M01: the firewall filters only the
// agent uid, which is the leak of the spike (A05). It never persists.
const weakenTheFirewall = `set -eu
nft insert rule inet orbit_egress output meta skuid != "$(id -u agent)" accept
nft list ruleset | grep -q 'skuid !='
`

// connectCode prints the status of a CONNECT through the proxy as the agent.
const connectCode = `code=$(runuser -u agent -- curl -s -m 15 -x http://127.0.0.1:3128 -o /dev/null -w '%{http_connect}' "https://$HOST:443/" || true)
`

// filterLetTheConnectThrough is the second control of E12: the filter of
// the proxy passes the CONNECT to $HOST, so only the firewall can refuse it.
const filterLetTheConnectThrough = connectCode + `[ "$code" != 403 ] || fail "the proxy refused the CONNECT to $HOST with 403: the allow rule did not take effect"
if grep -F "filtered domain \"$HOST\"" ` + proxyLogPath + ` >/dev/null 2>&1; then fail "the log of the proxy shows a filtered CONNECT to $HOST"; fi
`

// connectEscaped is the attempt of E12: the agent gets a tunnel to $HOST.
const connectEscaped = `code=$(curl -s -m 15 -x http://127.0.0.1:3128 -o /dev/null -w '%{http_connect}' "https://$HOST:443/" || true)
[ "$code" != 200 ] || fail "the CONNECT to $HOST:443 got a tunnel"
`

// macListener counts the connections to a listener of the test on the Mac.
type macListener struct {
	connections atomic.Int64
}

// listenOnThePort443 listens on port 443 of the Mac, which macOS lets an
// unprivileged process bind, and counts every connection.
func listenOnThePort443(t *testing.T) (*macListener, error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:443")
	if err != nil {
		return nil, err
	}
	counted := &macListener{}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			counted.connections.Add(1)
			_ = conn.Close()
		}
	}()
	return counted, nil
}

// TestEscapeWeakenedBoundary runs E12 and M01 in one disposable sandbox
// whose boundary the test weakens at run time: E12 first, while the
// firewall is intact, then M01.
func TestEscapeWeakenedBoundary(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	dir, name := newSandboxDir(ctx, t, client)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	if out, err := initHandler(client, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: dir}); err != nil || !out.Created {
		t.Fatalf("init = %+v, %v; machines that this test did not create: %s", out, err, otherMachines(ctx, client, name))
	}
	rig := escapeRig{client: client, name: name, tally: &counts}
	buildProbeImage(ctx, t, rig)

	checkAnAllowRuleDoesNotOpenTheMac(ctx, t, rig)
	checkTheWeakenedBoundaryIsDetected(ctx, t, rig)
}

// checkAnAllowRuleDoesNotOpenTheMac is E12 (Q13). Only the bridge-address
// CONNECT is D22 evidence: --isolate-network refuses host.orb.internal below
// the Orbit firewall, so that CONNECT is informational.
func checkAnAllowRuleDoesNotOpenTheMac(ctx context.Context, t *testing.T, rig escapeRig) {
	t.Helper()
	t.Run("E12-allow-rule-for-the-mac", func(t *testing.T) {
		t.Log("E12-allow-rule-for-the-mac proves D22; only the bridge-address CONNECT is D22 evidence")
		result := attemptResult{id: "E12-allow-rule-for-the-mac", hasControl: true}
		listener, err := listenOnThePort443(t)
		if err != nil {
			result.controlFailed, result.controlOutput = true, "no listener on port 443 of the Mac: "+err.Error()
			rig.report(t, result)
			return
		}
		direct := "need bash timeout\nallowed \"root to the listener on the Mac\" timeout 5 bash -c 'exec 3<>/dev/tcp/" + macBridgeAddr + "/443'\n"
		if err := rig.runIn(ctx, rootSession, direct); err != nil {
			result.controlFailed, result.controlOutput = true, "root does not reach the listener at "+macBridgeAddr+":443: "+err.Error()
		}
		baseline := listener.connections.Load()
		if err := rig.client.RunAsRoot(ctx, rig.name, []byte(allowTheMac)); err != nil {
			t.Fatalf("write the allow rule in %s: %v", rig.name, err)
		}
		if err := rig.runIn(ctx, rootSession, "HOST="+macBridgeAddr+"\n"+filterLetTheConnectThrough); err != nil {
			result.controlFailed, result.controlOutput = true, result.controlOutput+" "+err.Error()
		}
		if !result.controlFailed {
			if err := rig.runIn(ctx, agentSession, "HOST="+macBridgeAddr+"\n"+connectEscaped); err != nil {
				result.escaped, result.evidence = true, err.Error()
			}
			if got := listener.connections.Load(); got != baseline {
				result.escaped, result.evidence = true, result.evidence+" the listener on the Mac saw a connection"
			}
		}
		rig.report(t, result)
		if err := rig.runIn(ctx, agentSession, "HOST=host.orb.internal\n"+connectEscaped); err != nil {
			t.Errorf("ESCAPE: E12-allow-rule-for-the-mac succeeded at host.orb.internal: %v", err)
			return
		}
		t.Log("informational: the CONNECT to host.orb.internal:443 does not connect; --isolate-network refuses that name below the Orbit firewall, even for root (spike §2)")
	})
}

// checkTheWeakenedBoundaryIsDetected is M01 (Q6): with the firewall
// weakened to the agent uid, the detector of E06 reports reach.
func checkTheWeakenedBoundaryIsDetected(ctx context.Context, t *testing.T, rig escapeRig) {
	t.Helper()
	t.Run("M01-weakened-boundary-is-detected", func(t *testing.T) {
		t.Log("M01 proves A05: the detectors of E06 report reach on a weakened firewall")
		if err := rig.client.RunAsRoot(ctx, rig.name, []byte(weakenTheFirewall)); err != nil {
			t.Fatalf("CONTROL FAILED: M01: the weakening is not in place in %s: %v", rig.name, err)
		}
		detectors := []struct{ what, body string }{
			{"the sub-uid probe of E06", leakProbe + "leak_probe\n"},
			{"the container with the network of the machine and a sub-uid of E06", `no_direct "container with the network of the machine and --user 65534" --network host --user 65534` + "\n"},
		}
		missed := false
		for _, d := range detectors {
			err := rig.runIn(ctx, agentContainer, d.body)
			if err == nil || !strings.Contains(err.Error(), "direct connection from") {
				t.Errorf("the suite cannot detect the A05 leak: %s reported %v on a firewall that filters only the agent uid", d.what, errString(err))
				missed = true
				continue
			}
			t.Logf("detected: %s reports reach", d.what)
		}
		if missed {
			rig.tally.add(outcomeEscape)
			return
		}
		rig.tally.add(outcomePass)
	})
}
