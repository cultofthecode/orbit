// Package policy holds the network policy of a sandbox: the default allowlist
// of the first version, a base list plus the hosts of the agent (D26), and
// the rule that decides whether a host name may stand in an allow rule. It
// is pure and does no I/O.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// maxHostLength is the longest host name that DNS can carry.
const maxHostLength = 253

// minLabels is the least number of labels in a host name of an allow rule.
// A single label such as "localhost" never names a public host.
const minLabels = 2

// ErrInvalidHost reports a host name that cannot stand in an allow rule.
var ErrInvalidHost = errors.New("invalid host name for an allow rule")

// hostLabel matches one label of a host name in lower case: letters, digits,
// and inner hyphens, at most 63 characters.
var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// numericLabel matches a label of digits only.
var numericLabel = regexp.MustCompile(`^[0-9]+$`)

// baseAllowlist holds the base list of D26: the hosts that every sandbox may
// reach through the proxy, whatever its agent.
var baseAllowlist = []string{
	"github.com",
	"codeload.github.com",
	"raw.githubusercontent.com",
	"ssh.github.com",
	"registry.npmjs.org",
	"nodejs.org",
	"pypi.org",
	"files.pythonhosted.org",
	"proxy.golang.org",
	"sum.golang.org",
	"registry-1.docker.io",
	"auth.docker.io",
	"production.cloudflare.docker.com",
	"production.cloudfront.docker.com",
	"ghcr.io",
	"pkg-containers.githubusercontent.com",
	"deb.debian.org",
	"archive.ubuntu.com",
	"security.ubuntu.com",
	"ports.ubuntu.com",
	"dl-cdn.alpinelinux.org",
}

// Allowlist returns the default allowlist of a sandbox whose agent needs
// agentDomains (D26): the base list plus agentDomains, sorted and without
// duplicates. Allowlist(nil) is exactly the base list. It refuses the whole
// list when one of agentDomains fails ValidateHost. The caller owns the
// returned slice.
func Allowlist(agentDomains []string) ([]string, error) {
	for _, domain := range agentDomains {
		if err := ValidateHost(domain); err != nil {
			return nil, fmt.Errorf("agent domain: %w", err)
		}
	}
	hosts := slices.Concat(baseAllowlist, agentDomains)
	slices.Sort(hosts)
	return slices.Compact(hosts), nil
}

// ValidateHost reports whether host may stand in an allow rule. It accepts
// only a lowercase DNS name of at least two labels, each made of letters,
// digits, and inner hyphens, with a top-level label that is not all digits,
// at most 253 characters in total. It refuses wildcards, IP literals, ports,
// a trailing dot, and every other byte, so that the name can be written
// into a configuration file without escaping anything but dots.
func ValidateHost(host string) error {
	if len(host) > maxHostLength {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidHost, maxHostLength)
	}
	labels := strings.Split(host, ".")
	if len(labels) < minLabels {
		return fmt.Errorf("%w: %q has fewer than %d labels", ErrInvalidHost, host, minLabels)
	}
	if err := checkLabels(host, labels); err != nil {
		return err
	}
	if numericLabel.MatchString(labels[len(labels)-1]) {
		return fmt.Errorf("%w: %q ends in a numeric label", ErrInvalidHost, host)
	}
	return nil
}

// checkLabels refuses host when one of its labels is not a valid lowercase
// label.
func checkLabels(host string, labels []string) error {
	for _, label := range labels {
		if !hostLabel.MatchString(label) {
			return fmt.Errorf("%w: %q has the invalid label %q", ErrInvalidHost, host, label)
		}
	}
	return nil
}
