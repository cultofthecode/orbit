// Package proxy renders the configuration of tinyproxy, the egress proxy that
// root runs in a sandbox (D22). The proxy enforces the network policy: it
// opens a CONNECT tunnel on port 443 only to a host of the allowlist. The
// package is pure and does no I/O.
package proxy

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/cultofthecode/orbit/internal/policy"
)

// User is the system user that tinyproxy runs as.
const User = "tinyproxy"

// Port is the port on which tinyproxy accepts clients.
const Port uint16 = 3128

// LoopbackAddr is the address on which processes of the machine reach the
// proxy.
var LoopbackAddr = netip.MustParseAddr("127.0.0.1")

// ContainerAddr is the address on which rootless containers reach the proxy.
// Root puts it on the dummy interface orbit0, because slirp4netns does not
// let containers reach the loopback address of the machine.
var ContainerAddr = netip.MustParseAddr("172.31.255.1")

// ConfigPath is where tinyproxy reads its configuration.
const ConfigPath = "/etc/tinyproxy/tinyproxy.conf"

// AllowlistPath is where tinyproxy reads its filter of allowed hosts.
const AllowlistPath = "/etc/tinyproxy/allowlist"

// Files holds the content of the configuration files of tinyproxy.
type Files struct {
	// Config is the content of ConfigPath.
	Config string
	// Allowlist is the content of AllowlistPath: one anchored regular
	// expression for each allowed host.
	Allowlist string
}

// Config returns the configuration of tinyproxy that allows exactly hosts.
// It refuses the whole list when one host fails policy.ValidateHost. The
// output does not depend on the order of hosts or on duplicates.
func Config(hosts []string) (Files, error) {
	for _, host := range hosts {
		if err := policy.ValidateHost(host); err != nil {
			return Files{}, fmt.Errorf("proxy allowlist: %w", err)
		}
	}
	return Files{Config: config(), Allowlist: allowlist(hosts)}, nil
}

// config returns tinyproxy.conf. It has no Listen line, because one Listen
// line cannot name both addresses; the Allow lines limit the clients, and
// the firewall drops connections to the port that do not arrive on loopback.
func config() string {
	lines := []string{
		"User " + User,
		"Group " + User,
		fmt.Sprintf("Port %d", Port),
		"Timeout 600",
		`DefaultErrorFile "/usr/share/tinyproxy/default.html"`,
		`LogFile "/var/log/tinyproxy/tinyproxy.log"`,
		"LogLevel Info",
		`PidFile "/run/tinyproxy/tinyproxy.pid"`,
		"MaxClients 100",
		"Allow " + LoopbackAddr.String(),
		"Allow " + ContainerAddr.String(),
		"ConnectPort 443",
		`Filter "` + AllowlistPath + `"`,
		"FilterType ere",
		"FilterURLs Off",
		"FilterCaseSensitive Off",
		"FilterDefaultDeny Yes",
		"DisableViaHeader Yes",
	}
	return strings.Join(lines, "\n") + "\n"
}

// allowlist returns one anchored, escaped regular expression for each
// distinct host, sorted.
func allowlist(hosts []string) string {
	sorted := slices.Clone(hosts)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	var lines strings.Builder
	for _, host := range sorted {
		lines.WriteString("^" + strings.ReplaceAll(host, ".", `\.`) + "$\n")
	}
	return lines.String()
}
