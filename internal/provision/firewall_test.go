package provision

import (
	"errors"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

func validFirewallInput() FirewallInput {
	return FirewallInput{
		DefaultUID: 501,
		ProxyUser:  "tinyproxy",
		ProxyPort:  3128,
		ProxyAddrs: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("172.31.255.1")},
		Resolvers:  []netip.Addr{netip.MustParseAddr("0.250.250.200")},
	}
}

func TestFirewallMatchesGolden(t *testing.T) {
	got, err := Firewall(validFirewallInput())
	if err != nil {
		t.Fatalf("Firewall: %v", err)
	}
	compareGolden(t, "firewall.nft", got)
}

func TestFirewallRefusesHostileInput(t *testing.T) {
	tests := []struct {
		name   string
		change func(*FirewallInput)
	}{
		{name: "default uid 0", change: func(in *FirewallInput) { in.DefaultUID = 0 }},
		{name: "empty proxy user", change: func(in *FirewallInput) { in.ProxyUser = "" }},
		{name: "proxy user root", change: func(in *FirewallInput) { in.ProxyUser = "root" }},
		{name: "proxy user with a quote", change: func(in *FirewallInput) { in.ProxyUser = `tinyproxy" accept; "` }},
		{name: "proxy user with a newline", change: func(in *FirewallInput) { in.ProxyUser = "tinyproxy\nflush ruleset" }},
		{name: "proxy user with a space", change: func(in *FirewallInput) { in.ProxyUser = "tiny proxy" }},
		{name: "proxy user in upper case", change: func(in *FirewallInput) { in.ProxyUser = "TinyProxy" }},
		{name: "proxy user too long", change: func(in *FirewallInput) { in.ProxyUser = strings.Repeat("a", 33) }},
		{name: "proxy port 0", change: func(in *FirewallInput) { in.ProxyPort = 0 }},
		{name: "proxy port 53", change: func(in *FirewallInput) { in.ProxyPort = 53 }},
		{name: "no proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = nil }},
		{name: "public proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = append(in.ProxyAddrs, netip.MustParseAddr("1.1.1.1")) }},
		{name: "host-engine proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = []netip.Addr{netip.MustParseAddr("192.168.215.2")} }},
		{name: "Mac proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = []netip.Addr{netip.MustParseAddr("192.168.139.3")} }},
		{name: "unspecified proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = []netip.Addr{netip.IPv4Unspecified()} }},
		{name: "zero proxy address", change: func(in *FirewallInput) { in.ProxyAddrs = []netip.Addr{{}} }},
		{name: "no resolver", change: func(in *FirewallInput) { in.Resolvers = nil }},
		{name: "unspecified resolver", change: func(in *FirewallInput) { in.Resolvers = []netip.Addr{netip.IPv4Unspecified()} }},
		{name: "IPv6 unspecified resolver", change: func(in *FirewallInput) { in.Resolvers = []netip.Addr{netip.IPv6Unspecified()} }},
		{name: "multicast resolver", change: func(in *FirewallInput) { in.Resolvers = []netip.Addr{netip.MustParseAddr("224.0.0.251")} }},
		{name: "zero resolver", change: func(in *FirewallInput) { in.Resolvers = []netip.Addr{{}} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validFirewallInput()
			test.change(&in)
			got, err := Firewall(in)
			if !errors.Is(err, ErrFirewallInput) {
				t.Errorf("Firewall = %v, want %v", err, ErrFirewallInput)
			}
			if got != "" {
				t.Errorf("Firewall returned rules for refused input:\n%s", got)
			}
		})
	}
}

func TestFirewallRuleOrder(t *testing.T) {
	rules := outputChain(t, validFirewallInput())
	systemAccept := indexOf(t, rules, "meta skuid < 1000 meta skuid != 501 accept")
	for i, rule := range rules {
		if strings.HasPrefix(rule, `meta skuid "tinyproxy"`) && i > systemAccept {
			t.Errorf("proxy-user rule %q comes after the accept for system uids", rule)
		}
	}
	dnsReject := indexOf(t, rules, "meta l4proto { tcp, udp } th dport 53 reject")
	proxyAccept := indexOf(t, rules, "ip daddr { 127.0.0.1, 172.31.255.1 } tcp dport 3128 accept")
	if systemAccept >= proxyAccept || proxyAccept >= dnsReject {
		t.Errorf("want system accept < proxy accept < DNS reject, got %d, %d, %d", systemAccept, proxyAccept, dnsReject)
	}
	if last := rules[len(rules)-1]; last != "reject" {
		t.Errorf("last output rule = %q, want reject", last)
	}
	if last := rulesOfUser(rules, "tinyproxy"); last[len(last)-1] != `meta skuid "tinyproxy" reject` {
		t.Errorf("last proxy-user rule = %q, want a reject", last[len(last)-1])
	}
}

func TestFirewallProxyUserReachesOnlyPublicPort443AndResolvers(t *testing.T) {
	rules := rulesOfUser(outputChain(t, validFirewallInput()), "tinyproxy")
	accepts := 0
	for _, rule := range rules {
		if !strings.HasSuffix(rule, " accept") {
			continue
		}
		accepts++
		allowed := strings.Contains(rule, `oif "lo" tcp sport 3128`) ||
			strings.Contains(rule, "th dport 53 ip daddr { 0.250.250.200 }") ||
			strings.HasSuffix(rule, " tcp dport 443 accept")
		if !allowed {
			t.Errorf("unexpected proxy-user accept %q", rule)
		}
	}
	if accepts != 3 {
		t.Errorf("proxy user has %d accept rules, want 3", accepts)
	}
	refusal := indexOf(t, rules, `meta skuid "tinyproxy" ip daddr @refused_ipv4 reject`)
	port443 := indexOf(t, rules, `meta skuid "tinyproxy" tcp dport 443 accept`)
	if refusal > port443 {
		t.Error("the refusal of non-public addresses comes after the accept of port 443")
	}
}

func TestFirewallRefusesEveryNonPublicRange(t *testing.T) {
	got, err := Firewall(validFirewallInput())
	if err != nil {
		t.Fatalf("Firewall: %v", err)
	}
	for _, prefix := range append(append([]string{}, refusedIPv4...), refusedIPv6...) {
		if !strings.Contains(got, prefix) {
			t.Errorf("rules lack the refused range %s", prefix)
		}
	}
	addresses := []string{
		"192.168.139.3", "192.168.215.2", "198.19.249.1", "169.254.169.254", "127.0.0.1", "10.1.2.3",
		"172.17.0.1", "100.64.0.1", "0.250.250.200", "fd07:b51a:cc66:f0::fe", "fe80::1", "::1", "::ffff:10.0.0.1",
	}
	for _, address := range addresses {
		if !inAny(t, netip.MustParseAddr(address), append(append([]string{}, refusedIPv4...), refusedIPv6...)) {
			t.Errorf("%s is in no refused range", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "140.82.112.3", "2606:4700:4700::1111"} {
		if inAny(t, netip.MustParseAddr(address), append(append([]string{}, refusedIPv4...), refusedIPv6...)) {
			t.Errorf("public address %s is refused", address)
		}
	}
}

func TestFirewallFiltersTheDefaultUIDAndTheFloor(t *testing.T) {
	in := validFirewallInput()
	in.DefaultUID = 502
	rules := outputChain(t, in)
	indexOf(t, rules, "meta skuid < 1000 meta skuid != 502 accept")
	got, _ := Firewall(in)
	if !strings.Contains(got, "table inet orbit_egress {") {
		t.Error("the table does not cover IPv4 and IPv6 (inet)")
	}
}

func TestFirewallDropsProxyConnectionsFromOutside(t *testing.T) {
	got, err := Firewall(validFirewallInput())
	if err != nil {
		t.Fatalf("Firewall: %v", err)
	}
	if !strings.Contains(got, "\t\ttype filter hook input priority filter; policy accept;\n\t\tiif != \"lo\" tcp dport 3128 drop\n") {
		t.Errorf("input chain does not drop the proxy port off loopback:\n%s", got)
	}
}

func TestFirewallSplitsAddressFamilies(t *testing.T) {
	in := validFirewallInput()
	in.ProxyAddrs = append(in.ProxyAddrs, netip.IPv6Loopback())
	in.Resolvers = append(in.Resolvers, netip.MustParseAddr("fd07::53"))
	rules := outputChain(t, in)
	indexOf(t, rules, "ip6 daddr { ::1 } tcp dport 3128 accept")
	indexOf(t, rules, `meta skuid "tinyproxy" meta l4proto { tcp, udp } th dport 53 ip6 daddr { fd07::53 } accept`)
}

func outputChain(t *testing.T, in FirewallInput) []string {
	t.Helper()
	got, err := Firewall(in)
	if err != nil {
		t.Fatalf("Firewall: %v", err)
	}
	start := strings.Index(got, "chain output {")
	end := strings.Index(got, "chain input {")
	var rules []string
	for _, line := range strings.Split(got[start:end], "\n")[2:] {
		rule := strings.TrimSpace(line)
		if rule != "" && rule != "}" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func rulesOfUser(rules []string, user string) []string {
	var own []string
	for _, rule := range rules {
		if strings.HasPrefix(rule, `meta skuid "`+user+`"`) {
			own = append(own, rule)
		}
	}
	return own
}

func indexOf(t *testing.T, rules []string, want string) int {
	t.Helper()
	for i, rule := range rules {
		if rule == want {
			return i
		}
	}
	t.Fatalf("rules lack %q:\n%s", want, strings.Join(rules, "\n"))
	return -1
}

func inAny(t *testing.T, addr netip.Addr, prefixes []string) bool {
	t.Helper()
	for _, prefix := range prefixes {
		if netip.MustParsePrefix(prefix).Contains(addr) {
			return true
		}
	}
	return false
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file; run go test -update after a review of the change\ngot:\n%s", name, got)
	}
}
