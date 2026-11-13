package main

import (
	"fmt"
	"strings"
	"testing"
)

// outcome is what the suite concludes about one attempt.
type outcome int

const (
	outcomePass outcome = iota
	outcomeEscape
	outcomeControlFailed
	outcomeToolMissing
	outcomeInconclusive
	outcomeNotApplicable
	outcomeStale
)

// privilegedExpectation says whether docker run --privileged is expected to
// start a container on a distribution, on the pinned OrbStack version.
type privilegedExpectation int

const (
	privilegedRefused privilegedExpectation = iota
	privilegedAllowed
)

// privilegedTable holds the expectation per value of ORBIT_IT_DISTRO. A row
// says allowed only for a distribution that the owner allowed (Q1 allows
// Debian 13 trixie and no other). The debian row is absent until the Debian
// run of the distributions spec, step 9, observes it: a run on a
// distribution without a row fails, so no run counts as evidence without it.
var privilegedTable = map[string]privilegedExpectation{
	"ubuntu": privilegedRefused,
}

// privilegedObservation holds the expectation and the observation of
// --privileged on one distribution.
type privilegedObservation struct {
	distro   string
	expected privilegedExpectation
	started  bool
}

// unexpectedErrnoMarker is what a line of E18 to E22 prints when the
// operation failed with an error other than the one that the line lists.
const unexpectedErrnoMarker = "PROBE UNEXPECTED ERRNO"

// escapeMarker is what a line of E18 to E22 prints when the operation that
// it tries succeeded. An attempt whose output has both markers is an escape:
// an unexpected errno on one line never hides a success on another.
const escapeMarker = "PROBE ESCAPE"

// toolMissingMarker is what a probe prints, and exits non-zero, when a tool
// that it needs is missing.
const toolMissingMarker = "PROBE TOOL MISSING"

// targetUnreachableMarker is what a control prints, and exits non-zero, when
// even root in the machine gets no answer from the network of the target
// (E04): OrbStack blocks the path below the Orbit firewall.
const targetUnreachableMarker = "TARGET NETWORK UNREACHABLE"

// attemptResult holds what one run of an attempt observed.
type attemptResult struct {
	id string
	// hasControl is true when the attempt has a positive control.
	hasControl bool
	// informationalControl is true when any failed control of the attempt
	// is inconclusive, not a failure (E11).
	informationalControl bool
	// unreachableControlInconclusive is true when a control that prints
	// targetUnreachableMarker is inconclusive, not a failure (E04 only).
	unreachableControlInconclusive bool
	// controlOutput is empty when the control succeeded, and holds why it
	// failed otherwise.
	controlOutput string
	// controlFailed is true when the control did not succeed.
	controlFailed bool
	// escaped is true when the attempt, which must fail, succeeded.
	escaped bool
	// evidence is what the attempt printed.
	evidence string
	// privileged is set on the gate of E18 to E22 and M02, and is nil on
	// every other attempt.
	privileged *privilegedObservation
}

// inconclusiveControl reports whether the control failed in a way that is
// inconclusive, not a failure: the attempt marks its control informational
// (E11), or the attempt allows an unreachable target (E04) and the control
// could not reach the network of its target. Any other attempt whose control
// prints targetUnreachableMarker fails with CONTROL FAILED.
// The attempt still runs, and it still fails the suite when it succeeds.
func (r attemptResult) inconclusiveControl() bool {
	return r.hasControl && r.controlFailed && (r.informationalControl || (r.unreachableControlInconclusive && strings.Contains(r.controlOutput, targetUnreachableMarker)))
}

// attemptVerdict returns the outcome of an attempt and its message. An
// attempt passes only when its control succeeded and the attempt failed.
func attemptVerdict(r attemptResult) (outcome, string) {
	if r.privileged != nil {
		if o, message, decided := privilegedVerdict(r.id, *r.privileged); decided {
			return o, message
		}
	}
	informational := r.inconclusiveControl()
	if r.hasControl && r.controlFailed && !informational {
		return outcomeControlFailed, fmt.Sprintf("CONTROL FAILED: %s: %s", r.id, r.controlOutput)
	}
	if r.escaped && strings.Contains(r.evidence, toolMissingMarker) {
		return outcomeToolMissing, fmt.Sprintf("%s: %s", r.id, r.evidence)
	}
	if r.escaped && strings.Contains(r.evidence, unexpectedErrnoMarker) && !strings.Contains(r.evidence, escapeMarker) {
		return outcomeControlFailed, fmt.Sprintf("CONTROL FAILED: %s: %s", r.id, r.evidence)
	}
	if r.escaped {
		return outcomeEscape, fmt.Sprintf("ESCAPE: %s succeeded: %s", r.id, r.evidence)
	}
	if informational {
		return outcomeInconclusive, fmt.Sprintf("%s: OrbStack blocked the path below the Orbit firewall, so the firewall was not exercised: %s", r.id, r.controlOutput)
	}
	return outcomePass, ""
}

// privilegedVerdict compares the observation of --privileged with the table.
// It decides when the attempts must not run: refused and refused is not
// applicable, and the other three combinations fail the run. Allowed and
// started is not decided: the attempts run.
func privilegedVerdict(id string, p privilegedObservation) (outcome, string, bool) {
	switch {
	case p.expected == privilegedRefused && !p.started:
		return outcomeNotApplicable, fmt.Sprintf("%s: n/a: --privileged refused on %s", id, p.distro), true
	case p.expected == privilegedRefused:
		return outcomeEscape, fmt.Sprintf("ESCAPE: %s: --privileged starts on %s, where the table says refused; the owner decides", id, p.distro), true
	case !p.started:
		return outcomeStale, fmt.Sprintf("%s: privileged expectation stale for %s", id, p.distro), true
	}
	return outcomePass, "", false
}

// tally counts the outcomes of one TestEscape test.
type tally struct {
	passed, failed, controlsFailed, inconclusive, notApplicable, observationsHeld int
}

// add counts one outcome.
func (c *tally) add(o outcome) {
	switch o {
	case outcomePass:
		c.passed++
	case outcomeControlFailed:
		c.controlsFailed++
	case outcomeInconclusive:
		c.inconclusive++
	case outcomeNotApplicable:
		c.notApplicable++
	default:
		c.failed++
	}
}

// summary returns the line that ends a TestEscape test.
func (c *tally) summary() string {
	return fmt.Sprintf("escape attempts: %d passed, %d failed, %d controls failed, %d inconclusive; observations: %d held; n/a: %d",
		c.passed, c.failed, c.controlsFailed, c.inconclusive, c.observationsHeld, c.notApplicable)
}

func TestAttemptVerdict(t *testing.T) {
	tests := []struct {
		name string
		in   attemptResult
		want outcome
		msg  string
	}{
		{"attempt failed, control passed", attemptResult{id: "E03", hasControl: true}, outcomePass, ""},
		{"attempt failed, no control", attemptResult{id: "E08"}, outcomePass, ""},
		{"attempt succeeded", attemptResult{id: "E03", hasControl: true, escaped: true, evidence: "reached 192.168.139.3"}, outcomeEscape, "ESCAPE: E03 succeeded: reached 192.168.139.3"},
		{"control failed", attemptResult{id: "E04", hasControl: true, controlFailed: true, controlOutput: "no engine"}, outcomeControlFailed, "CONTROL FAILED: E04: no engine"},
		{"control failed beats an escape", attemptResult{id: "E04", hasControl: true, controlFailed: true, controlOutput: "x", escaped: true, evidence: "y"}, outcomeControlFailed, "CONTROL FAILED: E04: x"},
		{"informational control failed", attemptResult{id: "E11", hasControl: true, informationalControl: true, controlFailed: true, controlOutput: "timeout"}, outcomeInconclusive, "below the Orbit firewall"},
		{"informational control failed, attempt succeeded", attemptResult{id: "E11", hasControl: true, informationalControl: true, controlFailed: true, escaped: true, evidence: "reached"}, outcomeEscape, "ESCAPE: E11 succeeded: reached"},
		{"informational control passed", attemptResult{id: "E11", hasControl: true, informationalControl: true}, outcomePass, ""},
		{"target network unreachable", attemptResult{id: "E04", hasControl: true, unreachableControlInconclusive: true, controlFailed: true, controlOutput: targetUnreachableMarker + ": no answer from 192.168.215.2"}, outcomeInconclusive, "below the Orbit firewall"},
		{"target network unreachable, attempt succeeded", attemptResult{id: "E04", hasControl: true, unreachableControlInconclusive: true, controlFailed: true, controlOutput: targetUnreachableMarker, escaped: true, evidence: "reached"}, outcomeEscape, "ESCAPE: E04 succeeded: reached"},
		{"target network unreachable, check other than E04", attemptResult{id: "E05", hasControl: true, controlFailed: true, controlOutput: targetUnreachableMarker + ": no answer"}, outcomeControlFailed, "CONTROL FAILED: E05: TARGET NETWORK UNREACHABLE"},
		{"target network unreachable, attempt succeeded, other check", attemptResult{id: "E05", hasControl: true, controlFailed: true, controlOutput: targetUnreachableMarker, escaped: true, evidence: "reached"}, outcomeControlFailed, "CONTROL FAILED: E05"},
		{"target reached, listener missing", attemptResult{id: "E04", hasControl: true, unreachableControlInconclusive: true, controlFailed: true, controlOutput: "connection refused"}, outcomeControlFailed, "CONTROL FAILED: E04: connection refused"},
		{"privileged refused as expected", attemptResult{id: "E18", privileged: &privilegedObservation{distro: "ubuntu", expected: privilegedRefused}}, outcomeNotApplicable, "E18: n/a: --privileged refused on ubuntu"},
		{"privileged starts where the table says refused", attemptResult{id: "E18", privileged: &privilegedObservation{distro: "ubuntu", expected: privilegedRefused, started: true}}, outcomeEscape, "the owner decides"},
		{"privileged expectation stale", attemptResult{id: "E22", privileged: &privilegedObservation{distro: "debian", expected: privilegedAllowed}}, outcomeStale, "privileged expectation stale for debian"},
		{"privileged allowed and started, attempt failed", attemptResult{id: "E22", hasControl: true, privileged: &privilegedObservation{distro: "debian", expected: privilegedAllowed, started: true}}, outcomePass, ""},
		{"privileged allowed and started, attempt succeeded", attemptResult{id: "E22", hasControl: true, escaped: true, evidence: "nft flush ruleset succeeded", privileged: &privilegedObservation{distro: "debian", expected: privilegedAllowed, started: true}}, outcomeEscape, "ESCAPE: E22 succeeded"},
		{"line failed with another errno", attemptResult{id: "E18", hasControl: true, escaped: true, evidence: unexpectedErrnoMarker + ": insmod: No such file"}, outcomeControlFailed, "CONTROL FAILED: E18"},
		{"line failed with another errno beside an escape", attemptResult{id: "E18", hasControl: true, escaped: true, evidence: unexpectedErrnoMarker + ": insmod: No such file\n" + escapeMarker + ": /proc/sys/kernel/core_pattern changed"}, outcomeEscape, "ESCAPE: E18 succeeded"},
		{"probe tool missing", attemptResult{id: "E05", hasControl: true, escaped: true, evidence: toolMissingMarker + ": curl"}, outcomeToolMissing, "PROBE TOOL MISSING: curl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := attemptVerdict(tt.in)
			if got != tt.want || !strings.Contains(msg, tt.msg) {
				t.Errorf("attemptVerdict(%+v) = %v, %q, want %v containing %q", tt.in, got, msg, tt.want, tt.msg)
			}
		})
	}
}

func TestTallySummary(t *testing.T) {
	var c tally
	for _, o := range []outcome{outcomePass, outcomePass, outcomeEscape, outcomeControlFailed, outcomeInconclusive, outcomeToolMissing, outcomeNotApplicable, outcomeStale} {
		c.add(o)
	}
	c.observationsHeld = 1
	want := "escape attempts: 2 passed, 3 failed, 1 controls failed, 1 inconclusive; observations: 1 held; n/a: 1"
	if got := c.summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestPrivilegedTableKeepsUbuntuRefused(t *testing.T) {
	if got := privilegedTable["ubuntu"]; got != privilegedRefused {
		t.Errorf("ubuntu row = %v, want refused (Q1 decided Debian only)", got)
	}
}
