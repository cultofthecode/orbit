package sandbox

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Distribution is the Linux distribution of a sandbox: a value of a closed
// set. A caller never passes an image or a version, only one of these values;
// the adapter maps each value to its pinned image (D28).
type Distribution string

// The distributions of release 0.1.
const (
	// Ubuntu is Ubuntu 26.04.
	Ubuntu Distribution = "ubuntu"
	// Debian is Debian 13 trixie.
	Debian Distribution = "debian"
)

// DefaultDistribution is the distribution of a new sandbox when the user
// names none.
const DefaultDistribution = Ubuntu

// ErrUnknownDistribution reports a value that is not a Distribution.
var ErrUnknownDistribution = errors.New("unknown distribution")

// distributions is the closed set, in the order that the help shows.
var distributions = []Distribution{Ubuntu, Debian}

// Distributions returns every distribution, Ubuntu first, in a new slice.
func Distributions() []Distribution {
	return slices.Clone(distributions)
}

// ParseDistribution returns the distribution that s names, matched exactly
// and case-sensitively. It refuses every other value, the empty string, an
// image, a version, and a value with white space, quotes, or a leading dash
// included, with an error that wraps ErrUnknownDistribution and quotes s.
func ParseDistribution(s string) (Distribution, error) {
	for _, d := range distributions {
		if string(d) == s {
			return d, nil
		}
	}
	names := make([]string, len(distributions))
	for i, d := range distributions {
		names[i] = string(d)
	}
	return "", fmt.Errorf("%w %q; use one of: %s", ErrUnknownDistribution, s, strings.Join(names, ", "))
}
