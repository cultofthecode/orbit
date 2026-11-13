package sandbox

import (
	"errors"
	"slices"
	"testing"
)

func TestDistributionsListsUbuntu(t *testing.T) {
	want := []Distribution{Ubuntu}
	if got := Distributions(); !slices.Equal(got, want) {
		t.Errorf("Distributions() = %q, want %q", got, want)
	}
	if DefaultDistribution != Ubuntu {
		t.Errorf("DefaultDistribution = %q, want %q", DefaultDistribution, Ubuntu)
	}
}

func TestDistributionsReturnsACopy(t *testing.T) {
	got := Distributions()
	got[0] = "alpine"
	if again := Distributions(); again[0] != Ubuntu {
		t.Errorf("Distributions() changed through a returned slice: %q", again)
	}
}

func TestParseDistributionAcceptsTheEnum(t *testing.T) {
	tests := map[string]Distribution{"ubuntu": Ubuntu}
	for in, want := range tests {
		got, err := ParseDistribution(in)
		if err != nil || got != want {
			t.Errorf("ParseDistribution(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
}

func TestParseDistributionRefusesEverythingElse(t *testing.T) {
	tests := []string{
		"", " ", "Ubuntu", "DEBIAN", "debian", " ubuntu", "ubuntu ", "ubuntu\n", "ubuntu\ndebian", "\tdebian",
		"ubuntu:26.04", "debian:trixie", "debian:bookworm", "bookworm", "trixie", "26.04", "resolute",
		"alpine", "alpine:3.23", "ubuntu debian", "ubuntu;debian", "ubuntu,debian", `"ubuntu"`, "'debian'",
		"ubuntu;reboot", "$(ubuntu)", "`debian`", "-ubuntu", "--debian", "-", "--", "--mount", "../ubuntu",
		"/ubuntu", "ubuntu/", "ubuntu\x00", "ubu\x00ntu", "ubuntu\u200b", "ubuntu\u00a0", "ｕｂｕｎｔｕ", "ubuntu=debian",
		"docker.io/library/ubuntu", "ghcr.io/evil/image",
	}
	for _, in := range tests {
		got, err := ParseDistribution(in)
		if !errors.Is(err, ErrUnknownDistribution) {
			t.Errorf("ParseDistribution(%q) = %q, %v, want ErrUnknownDistribution", in, got, err)
		}
		if got != "" {
			t.Errorf("ParseDistribution(%q) returned %q with an error, want none", in, got)
		}
	}
}

func TestParseDistributionNamesTheChoices(t *testing.T) {
	_, err := ParseDistribution("alpine")
	want := `unknown distribution "alpine"; use one of: ubuntu`
	if err == nil || err.Error() != want {
		t.Errorf("ParseDistribution(%q) error = %v, want %q", "alpine", err, want)
	}
	_, err = ParseDistribution("a\nb;$(x)\"")
	if err == nil || err.Error() != `unknown distribution "a\nb;$(x)\""; use one of: ubuntu` {
		t.Errorf("a hostile value must be quoted in the error, got %v", err)
	}
}
