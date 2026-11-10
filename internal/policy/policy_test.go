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

func TestDefaultAllowlistMatchesD21(t *testing.T) {
	want := []string{
		"api.anthropic.com", "auth.docker.io", "browser-intake-us5-datadoghq.com",
		"claude.ai", "code.claude.com", "codeload.github.com", "dl-cdn.alpinelinux.org",
		"downloads.claude.ai", "files.pythonhosted.org", "github.com",
		"http-intake.logs.us5.datadoghq.com", "mcp-proxy.anthropic.com", "nodejs.org",
		"platform.claude.com", "production.cloudflare.docker.com",
		"production.cloudfront.docker.com", "proxy.golang.org", "pypi.org",
		"raw.githubusercontent.com", "registry-1.docker.io", "registry.npmjs.org",
		"ssh.github.com", "sum.golang.org",
	}
	got := DefaultAllowlist()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("DefaultAllowlist() = %v, want %v", got, want)
	}
}

func TestDefaultAllowlistHostsAreValid(t *testing.T) {
	for _, host := range DefaultAllowlist() {
		if err := ValidateHost(host); err != nil {
			t.Errorf("default host %q: %v", host, err)
		}
	}
}

func TestDefaultAllowlistReturnsACopy(t *testing.T) {
	first := DefaultAllowlist()
	first[0] = "changed.example"
	if DefaultAllowlist()[0] == "changed.example" {
		t.Error("DefaultAllowlist returned its own slice")
	}
}
