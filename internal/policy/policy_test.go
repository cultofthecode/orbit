package policy

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestValidateHostAccepts(t *testing.T) {
	hosts := []string{
		"github.com",
		"registry-1.docker.io",
		"http-intake.logs.us5.datadoghq.com",
		"a.b",
		strings.Repeat("a", 63) + ".com",
	}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			if err := ValidateHost(host); err != nil {
				t.Errorf("ValidateHost(%q) = %v, want nil", host, err)
			}
		})
	}
}

func TestValidateHostRefusesHostileInput(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{name: "wildcard", host: "*.github.com"},
		{name: "upper case", host: "GitHub.com"},
		{name: "trailing dot", host: "github.com."},
		{name: "port", host: "github.com:443"},
		{name: "IPv4 literal", host: "1.1.1.1"},
		{name: "IPv6 literal", host: "[::1]"},
		{name: "IPv6 literal without brackets", host: "::1"},
		{name: "empty label", host: "a..b"},
		{name: "leading hyphen", host: "-a.com"},
		{name: "trailing hyphen", host: "a-.com"},
		{name: "empty", host: ""},
		{name: "too long", host: strings.Repeat("a.", 126) + "ab"},
		{name: "label too long", host: strings.Repeat("a", 64) + ".com"},
		{name: "not ASCII", host: "githüb.com"},
		{name: "newline", host: "github.com\nexample.org"},
		{name: "space", host: "github.com example.org"},
		{name: "single label", host: "localhost"},
		{name: "regular expression", host: "git.ub.com|.*"},
		{name: "anchor", host: "^github.com$"},
		{name: "underscore", host: "git_hub.com"},
		{name: "leading dot", host: ".github.com"},
		{name: "scheme", host: "https://github.com"},
		{name: "path", host: "github.com/x"},
		{name: "numeric top-level label", host: "a.123"},
		{name: "NUL byte", host: "github.com\x00.evil.org"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateHost(test.host)
			if !errors.Is(err, ErrInvalidHost) {
				t.Errorf("ValidateHost(%q) = %v, want %v", test.host, err, ErrInvalidHost)
			}
		})
	}
}

// baseHostsOfD24 is the base list of D24, sorted.
var baseHostsOfD24 = []string{
	"auth.docker.io", "codeload.github.com", "dl-cdn.alpinelinux.org",
	"files.pythonhosted.org", "github.com", "nodejs.org",
	"production.cloudflare.docker.com", "production.cloudfront.docker.com",
	"proxy.golang.org", "pypi.org", "raw.githubusercontent.com",
	"registry-1.docker.io", "registry.npmjs.org", "ssh.github.com", "sum.golang.org",
}

func TestAllowlistWithoutAgentIsTheBaseListOfD24(t *testing.T) {
	for _, domains := range [][]string{nil, {}} {
		got, err := Allowlist(domains)
		if err != nil || !slices.Equal(got, baseHostsOfD24) {
			t.Errorf("Allowlist(%q) = %q, %v, want %q", domains, got, err, baseHostsOfD24)
		}
	}
}

func TestBaseListHoldsNoAgentHost(t *testing.T) {
	got, err := Allowlist(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range got {
		if strings.Contains(host, "claude") || strings.Contains(host, "anthropic") || strings.Contains(host, "datadoghq") {
			t.Errorf("base list holds the agent host %q", host)
		}
	}
}

func TestAllowlistAddsTheAgentDomains(t *testing.T) {
	got, err := Allowlist([]string{"downloads.claude.ai", "claude.ai", "github.com", "claude.ai"})
	if err != nil {
		t.Fatalf("Allowlist = %v", err)
	}
	want := slices.Sorted(slices.Values(append(slices.Clone(baseHostsOfD24), "claude.ai", "downloads.claude.ai")))
	if !slices.Equal(got, want) {
		t.Errorf("Allowlist = %q, want %q", got, want)
	}
}

func TestAllowlistRefusesTheWholeListForOneHostileDomain(t *testing.T) {
	tests := []struct {
		name   string
		domain string
	}{
		{name: "wildcard", domain: "*.anthropic.com"},
		{name: "IPv4 literal", domain: "1.2.3.4"},
		{name: "IPv6 literal", domain: "::1"},
		{name: "single label", domain: "localhost"},
		{name: "upper case", domain: "Claude.AI"},
		{name: "port", domain: "example.com:22"},
		{name: "empty", domain: ""},
		{name: "newline", domain: "claude.ai\n.*"},
		{name: "regular expression", domain: "claude.ai|.*"},
		{name: "trailing dot", domain: "claude.ai."},
		{name: "scheme", domain: "https://claude.ai"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Allowlist([]string{"claude.ai", test.domain, "downloads.claude.ai"})
			if !errors.Is(err, ErrInvalidHost) || got != nil {
				t.Errorf("Allowlist with %q = %q, %v, want nil, %v", test.domain, got, err, ErrInvalidHost)
			}
		})
	}
}

func TestBaseListHostsAreValid(t *testing.T) {
	for _, host := range baseAllowlist {
		if err := ValidateHost(host); err != nil {
			t.Errorf("base host %q: %v", host, err)
		}
	}
}

func TestAllowlistReturnsACopy(t *testing.T) {
	first, err := Allowlist(nil)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "changed.example"
	second, err := Allowlist(nil)
	if err != nil || second[0] == "changed.example" {
		t.Error("Allowlist returned the base list itself")
	}
}
