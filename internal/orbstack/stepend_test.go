package orbstack

import (
	"testing"
	"time"
)

// cleanupGrace is the time that createContext leaves before the deadline
// of the test binary, so that t.Cleanup can delete the machine.
const cleanupGrace = 3 * time.Minute

// integrationTimeout bounds the steps of one integration test after its
// machine exists.
const integrationTimeout = 3 * time.Minute

// stepEnd returns when a step that starts at start must end: after
// integrationTimeout, or cleanupGrace before the test deadline when that is
// earlier. Without a deadline (hasDeadline false) only integrationTimeout
// applies.
func stepEnd(start, deadline time.Time, hasDeadline bool) time.Time {
	end := start.Add(integrationTimeout)
	if hasDeadline && deadline.Add(-cleanupGrace).Before(end) {
		return deadline.Add(-cleanupGrace)
	}
	return end
}

func TestStepEndIsBoundedByTheTestDeadline(t *testing.T) {
	start := time.Now()
	for name, tc := range map[string]struct {
		deadline    time.Time
		hasDeadline bool
		want        time.Time
	}{
		"no deadline":       {want: start.Add(integrationTimeout)},
		"far deadline":      {start.Add(time.Hour), true, start.Add(integrationTimeout)},
		"near deadline":     {start.Add(integrationTimeout), true, start.Add(integrationTimeout - cleanupGrace)},
		"deadline in grace": {start.Add(cleanupGrace / 2), true, start.Add(-cleanupGrace / 2)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := stepEnd(start, tc.deadline, tc.hasDeadline); !got.Equal(tc.want) {
				t.Errorf("stepEnd = %v, want %v", got, tc.want)
			}
		})
	}
}
