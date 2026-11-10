package provision

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/cultofthecode/orbit/internal/proxy"
)

// uidFloor is the first uid that the firewall filters. Every user account,
// the agent, and every sub-uid of a rootless container is at or above it.
const uidFloor = 1000

// dnsPort is the port of DNS.
const dnsPort = 53

// ErrFirewallInput reports input from which Firewall refuses to render
// rules, because the rules would not hold the boundary.
var ErrFirewallInput = errors.New("invalid firewall input")

// systemUser matches the name of a system user that nft can resolve.
var systemUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// refusedIPv4 holds the IPv4 ranges that the proxy user may not reach:
// unspecified, private, shared, loopback, link-local, IETF protocol
// assignments, benchmark (OrbStack machines), multicast, and reserved.
var refusedIPv4 = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"224.0.0.0/3",
}

// refusedIPv6 holds the IPv6 ranges that the proxy user may not reach:
// unspecified and loopback, IPv4-mapped, NAT64, unique local, link-local,
// and multicast.
var refusedIPv6 = []string{
	"::/127",
	"::ffff:0:0/96",
	"64:ff9b::/96",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
}

// FirewallInput holds what the firewall rules depend on.
type FirewallInput struct {
	// DefaultUID is the uid of the default user that OrbStack creates in
	// the machine: the uid of the Mac user. It is below the uid floor, so
	// the rules name it explicitly.
	DefaultUID uint32
	// ProxyUser is the system user that the egress proxy runs as.
	ProxyUser string
	// ProxyPort is the port on which the proxy accepts clients.
	ProxyPort uint16
	// ProxyAddrs are the addresses of the machine on which clients reach
	// the proxy.
	ProxyAddrs []netip.Addr
	// Resolvers are the DNS servers that the proxy may query.
	Resolvers []netip.Addr
}

// Firewall returns /etc/nftables.conf: one table, inet orbit_egress, that the
// file replaces in one transaction. Its output chain rejects all traffic of
// the default uid and of every uid from 1000 up, except TCP to the proxy and
// loopback traffic, and lets the proxy user reach only port 443 on public
// addresses and port 53 on the resolvers. Its input chain drops connections
// to the proxy port that do not arrive on loopback. Firewall refuses input
// that would weaken these rules.
func Firewall(in FirewallInput) (string, error) {
	if err := validateFirewallInput(in); err != nil {
		return "", err
	}
	return renderFirewall(in), nil
}

// validateFirewallInput refuses input that would filter root, exempt the
// proxy from its own rules, or open a port or an address that the agent
// must not reach.
func validateFirewallInput(in FirewallInput) error {
	if in.DefaultUID == 0 {
		return fmt.Errorf("%w: the default uid is 0", ErrFirewallInput)
	}
	if in.ProxyUser == "root" || !systemUser.MatchString(in.ProxyUser) {
		return fmt.Errorf("%w: proxy user %q", ErrFirewallInput, in.ProxyUser)
	}
	if in.ProxyPort == 0 || in.ProxyPort == dnsPort {
		return fmt.Errorf("%w: proxy port %d", ErrFirewallInput, in.ProxyPort)
	}
	if err := validateProxyAddrs(in.ProxyAddrs); err != nil {
		return err
	}
	return validateResolvers(in.Resolvers)
}

// validateProxyAddrs accepts only loopback addresses and the container
// address of the proxy. Any other address would let the agent reach that
// address directly on the proxy port.
func validateProxyAddrs(addrs []netip.Addr) error {
	if len(addrs) == 0 {
		return fmt.Errorf("%w: no proxy address", ErrFirewallInput)
	}
	for _, addr := range addrs {
		if !isProxyAddr(addr) {
			return fmt.Errorf("%w: proxy address %s is neither loopback nor %s", ErrFirewallInput, addr, proxy.ContainerAddr)
		}
	}
	return nil
}

// isProxyAddr reports whether addr is a loopback address or the container
// address of the proxy.
func isProxyAddr(addr netip.Addr) bool {
	return addr.IsValid() && (addr.IsLoopback() || addr == proxy.ContainerAddr)
}

// validateResolvers refuses an empty list and addresses that cannot be a
// DNS server.
func validateResolvers(addrs []netip.Addr) error {
	if len(addrs) == 0 {
		return fmt.Errorf("%w: no resolver", ErrFirewallInput)
	}
	for _, addr := range addrs {
		if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() {
			return fmt.Errorf("%w: resolver %s", ErrFirewallInput, addr)
		}
	}
	return nil
}

// renderFirewall writes the ruleset of valid input.
func renderFirewall(in FirewallInput) string {
	lines := []string{
		"#!/usr/sbin/nft -f",
		"table inet orbit_egress",
		"delete table inet orbit_egress",
		"table inet orbit_egress {",
	}
	lines = append(lines, intervalSet("refused_ipv4", "ipv4_addr", refusedIPv4)...)
	lines = append(lines, intervalSet("refused_ipv6", "ipv6_addr", refusedIPv6)...)
	lines = append(lines, chain("output", outputRules(in))...)
	lines = append(lines, chain("input", inputRules(in))...)
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n"
}

// outputRules returns the rules of the output chain, in order: first the
// proxy user, because its uid is below the floor; then the accept for
// system uids; then the filtered uids.
func outputRules(in FirewallInput) []string {
	var rules []string
	rules = append(rules, proxyUserRules(in)...)
	rules = append(rules, fmt.Sprintf("meta skuid < %d meta skuid != %d accept", uidFloor, in.DefaultUID))
	rules = append(rules, filteredUIDRules(in)...)
	return rules
}

// proxyUserRules returns the rules for the proxy user: answers to its
// clients, DNS to the resolvers, a refusal of every non-public address, and
// port 443 only.
func proxyUserRules(in FirewallInput) []string {
	user := fmt.Sprintf("meta skuid %q", in.ProxyUser)
	rules := []string{fmt.Sprintf(`%s oif "lo" tcp sport %d accept`, user, in.ProxyPort)}
	for _, match := range addressMatches(in.Resolvers) {
		rules = append(rules, fmt.Sprintf("%s meta l4proto { tcp, udp } th dport %d %s accept", user, dnsPort, match))
	}
	return append(rules,
		user+" ip daddr @refused_ipv4 reject",
		user+" ip6 daddr @refused_ipv6 reject",
		user+" tcp dport 443 accept",
		user+" reject",
	)
}

// filteredUIDRules returns the rules for every uid that reaches this point:
// the proxy, no DNS, loopback, and a final reject.
func filteredUIDRules(in FirewallInput) []string {
	var rules []string
	for _, match := range addressMatches(in.ProxyAddrs) {
		rules = append(rules, fmt.Sprintf("%s tcp dport %d accept", match, in.ProxyPort))
	}
	return append(rules,
		fmt.Sprintf("meta l4proto { tcp, udp } th dport %d reject", dnsPort),
		`oif "lo" ip daddr 127.0.0.0/8 accept`,
		`oif "lo" ip6 daddr ::1 accept`,
		"reject",
	)
}

// inputRules returns the rules of the input chain: nothing from outside the
// machine reaches the proxy.
func inputRules(in FirewallInput) []string {
	return []string{fmt.Sprintf(`iif != "lo" tcp dport %d drop`, in.ProxyPort)}
}

// addressMatches returns one daddr match for each address family in addrs.
func addressMatches(addrs []netip.Addr) []string {
	var ipv4, ipv6 []string
	for _, addr := range addrs {
		if addr.Is4() {
			ipv4 = append(ipv4, addr.String())
		} else {
			ipv6 = append(ipv6, addr.String())
		}
	}
	var matches []string
	if len(ipv4) > 0 {
		matches = append(matches, "ip daddr { "+strings.Join(ipv4, ", ")+" }")
	}
	if len(ipv6) > 0 {
		matches = append(matches, "ip6 daddr { "+strings.Join(ipv6, ", ")+" }")
	}
	return matches
}

// intervalSet returns a named set of address ranges.
func intervalSet(name, addrType string, ranges []string) []string {
	return []string{
		"\tset " + name + " {",
		"\t\ttype " + addrType,
		"\t\tflags interval",
		"\t\telements = { " + strings.Join(ranges, ", ") + " }",
		"\t}",
	}
}

// chain returns a filter chain on the hook of the same name, with a policy
// of accept: every packet that the chain must stop meets a rule.
func chain(hook string, rules []string) []string {
	lines := []string{
		"\tchain " + hook + " {",
		"\t\ttype filter hook " + hook + " priority filter; policy accept;",
	}
	for _, rule := range rules {
		lines = append(lines, "\t\t"+rule)
	}
	return append(lines, "\t}")
}
