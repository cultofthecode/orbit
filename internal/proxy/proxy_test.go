package proxy

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/policy"
)

func TestConfigAllowlistIsAnchoredAndEscaped(t *testing.T) {
	files, err := Config([]string{"github.com", "registry-1.docker.io"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	want := "^github\\.com$\n^registry-1\\.docker\\.io$\n"
	if files.Allowlist != want {
		t.Errorf("Allowlist = %q, want %q", files.Allowlist, want)
	}
}

func TestConfigAllowlistMatchesOnlyTheExactHost(t *testing.T) {
	files, err := Config([]string{"github.com"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	pattern := regexp.MustCompile(strings.TrimSpace(files.Allowlist))
	for _, host := range []string{"github.com"} {
		if !pattern.MatchString(host) {
			t.Errorf("%q does not match %q", pattern, host)
		}
	}
	for _, host := range []string{"githubXcom", "evilgithub.com", "github.com.evil.org", "api.github.com", "github.co"} {
		if pattern.MatchString(host) {
			t.Errorf("%q matches %q", pattern, host)
		}
	}
}

func TestConfigIgnoresOrderAndDuplicates(t *testing.T) {
	first, err := Config([]string{"b.example", "a.example"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	second, err := Config([]string{"a.example", "b.example", "a.example"})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if first != second {
		t.Errorf("Config depends on order or duplicates: %q, then %q", first, second)
	}
}

func TestConfigRefusesTheWholeListForOneInvalidHost(t *testing.T) {
	hostile := []string{
		"*.github.com",
		"github.com$\n^.*",
		"github.com:22",
		"10.0.0.1",
		"",
	}
	for _, host := range hostile {
		t.Run(host, func(t *testing.T) {
			files, err := Config([]string{"github.com", host})
			if !errors.Is(err, policy.ErrInvalidHost) {
				t.Errorf("Config with %q = %v, want %v", host, err, policy.ErrInvalidHost)
			}
			if files != (Files{}) {
				t.Errorf("Config with %q returned files %+v, want none", host, files)
			}
		})
	}
}

func TestConfigSettings(t *testing.T) {
	hosts, err := policy.Allowlist(nil)
	if err != nil {
		t.Fatalf("Allowlist: %v", err)
	}
	files, err := Config(hosts)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	lines := strings.Split(files.Config, "\n")
	for _, want := range []string{
		"User tinyproxy",
		"Group tinyproxy",
		"Port 3128",
		"Allow 127.0.0.1",
		"Allow 172.31.255.1",
		"ConnectPort 443",
		`Filter "/etc/tinyproxy/allowlist"`,
		"FilterType ere",
		"FilterURLs Off",
		"FilterCaseSensitive Off",
		"FilterDefaultDeny Yes",
		"DisableViaHeader Yes",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("configuration lacks the line %q", want)
		}
	}
	for _, line := range lines {
		for _, forbidden := range []string{"Listen", "Upstream", "ConnectPort 22", "ConnectPort 80", "Allow 0.0.0.0", "BasicAuth"} {
			if strings.HasPrefix(line, forbidden) {
				t.Errorf("configuration has the line %q", line)
			}
		}
	}
	if count := countPrefix(lines, "ConnectPort"); count != 1 {
		t.Errorf("configuration has %d ConnectPort lines, want 1", count)
	}
}

func TestConfigAllowsTheNewBaseHostsOfD26AndNoOtherHostOfTheirDomains(t *testing.T) {
	hosts, err := policy.Allowlist(nil)
	if err != nil {
		t.Fatalf("Allowlist: %v", err)
	}
	files, err := Config(hosts)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	entries := strings.Split(strings.TrimSpace(files.Allowlist), "\n")
	for _, host := range []string{
		"ghcr.io", "pkg-containers.githubusercontent.com", "deb.debian.org",
		"archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com", "dl-cdn.alpinelinux.org",
	} {
		want := "^" + strings.ReplaceAll(host, ".", `\.`) + "$"
		if !slices.Contains(entries, want) {
			t.Errorf("allowlist lacks %q", want)
		}
	}
	for _, host := range []string{"evil.ghcr.io", "ghcr.io.example.org", "de.archive.ubuntu.com", "security.debian.org", "quay.io"} {
		for _, entry := range entries {
			if regexp.MustCompile(entry).MatchString(host) {
				t.Errorf("allowlist entry %q matches %q", entry, host)
			}
		}
	}
}

func countPrefix(lines []string, prefix string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}
