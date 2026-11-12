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
)

// toolMissingMarker is what a probe prints, and exits non-zero, when a tool
// that it needs is missing.
const toolMissingMarker = "PROBE TOOL MISSING"

// attemptResult holds what one run of an attempt observed.
type attemptResult struct {
	id string
	// hasControl is true when the attempt has a positive control.
	hasControl bool
	// informationalControl is true when a failed control does not fail the
	// attempt (E11).
	informationalControl bool
	// controlOutput is empty when the control succeeded, and holds why it
	// failed otherwise.
	controlOutput string
	// controlFailed is true when the control did not succeed.
	controlFailed bool
	// escaped is true when the attempt, which must fail, succeeded.
	escaped bool
	// evidence is what the attempt printed.
	evidence string
}

// attemptVerdict returns the outcome of an attempt and its message. An
// attempt passes only when its control succeeded and the attempt failed.
func attemptVerdict(r attemptResult) (outcome, string) {
	informational := r.hasControl && r.controlFailed && r.informationalControl
	if r.hasControl && r.controlFailed && !informational {
		return outcomeControlFailed, fmt.Sprintf("CONTROL FAILED: %s: %s", r.id, r.controlOutput)
	}
	if r.escaped && strings.Contains(r.evidence, toolMissingMarker) {
		return outcomeToolMissing, fmt.Sprintf("%s: %s", r.id, r.evidence)
	}
	if r.escaped {
		return outcomeEscape, fmt.Sprintf("ESCAPE: %s succeeded: %s", r.id, r.evidence)
	}
	if informational {
		return outcomeInconclusive, fmt.Sprintf("%s: OrbStack blocked the path below the Orbit firewall, so the firewall was not exercised: %s", r.id, r.controlOutput)
	}
	return outcomePass, ""
}

// tally counts the outcomes of one TestEscape test.
type tally struct {
	passed, failed, controlsFailed, inconclusive, observationsHeld int
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
	default:
		c.failed++
	}
}

// summary returns the line that ends a TestEscape test.
func (c *tally) summary() string {
	return fmt.Sprintf("escape attempts: %d passed, %d failed, %d controls failed, %d inconclusive; observations: %d held",
		c.passed, c.failed, c.controlsFailed, c.inconclusive, c.observationsHeld)
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
	for _, o := range []outcome{outcomePass, outcomePass, outcomeEscape, outcomeControlFailed, outcomeInconclusive, outcomeToolMissing} {
		c.add(o)
	}
	c.observationsHeld = 1
	want := "escape attempts: 2 passed, 2 failed, 1 controls failed, 1 inconclusive; observations: 1 held"
	if got := c.summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
