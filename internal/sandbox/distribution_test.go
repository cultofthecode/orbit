package sandbox

import (
	"errors"
	"slices"
	"testing"
)

func TestDistributionsListsUbuntuThenDebian(t *testing.T) {
	want := []Distribution{Ubuntu, Debian}
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
	tests := map[string]Distribution{"ubuntu": Ubuntu, "debian": Debian}
	for in, want := range tests {
		got, err := ParseDistribution(in)
		if err != nil || got != want {
			t.Errorf("ParseDistribution(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
}

func TestParseDistributionRefusesEverythingElse(t *testing.T) {
	tests := []string{
		"", " ", "Ubuntu", "DEBIAN", "Debian", " debian", " ubuntu", "ubuntu ", "ubuntu\n", "ubuntu\ndebian", "\tdebian",
		"ubuntu:26.04", "debian:trixie", "debian:bookworm", "bookworm", "trixie", "26.04", "resolute",
		"alpine", "alpine:3.23", "debian ", "debian\n", "debian:13", "ubuntu debian", "ubuntu;debian", "ubuntu,debian", `"ubuntu"`, "'debian'",
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
	want := `unknown distribution "alpine"; use one of: ubuntu, debian`
	if err == nil || err.Error() != want {
		t.Errorf("ParseDistribution(%q) error = %v, want %q", "alpine", err, want)
	}
	_, err = ParseDistribution("a\nb;$(x)\"")
	if err == nil || err.Error() != `unknown distribution "a\nb;$(x)\""; use one of: ubuntu, debian` {
		t.Errorf("a hostile value must be quoted in the error, got %v", err)
	}
}

func TestResolveDistribution(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		existing  Distribution
		exists    bool
		want      Distribution
		wantErr   error
	}{
		{name: "new, not given", want: Ubuntu},
		{name: "new, debian", requested: "debian", want: Debian},
		{name: "new, ubuntu", requested: "ubuntu", want: Ubuntu},
		{name: "new, outside the enum", requested: "alpine", wantErr: ErrUnknownDistribution},
		{name: "existing, outside the enum", requested: "alpine", existing: Ubuntu, exists: true, wantErr: ErrUnknownDistribution},
		{name: "existing debian, not given", existing: Debian, exists: true, want: Debian},
		{name: "existing ubuntu, not given", existing: Ubuntu, exists: true, want: Ubuntu},
		{name: "existing ubuntu, ubuntu", requested: "ubuntu", existing: Ubuntu, exists: true, want: Ubuntu},
		{name: "existing debian, debian", requested: "debian", existing: Debian, exists: true, want: Debian},
		{name: "existing ubuntu, debian", requested: "debian", existing: Ubuntu, exists: true, wantErr: ErrDistributionMismatch},
		{name: "existing debian, ubuntu", requested: "ubuntu", existing: Debian, exists: true, wantErr: ErrDistributionMismatch},
		{name: "existing, stored value outside the enum", existing: "alpine", exists: true, wantErr: ErrUnknownDistribution},
		{name: "existing, empty stored value", exists: true, wantErr: ErrUnknownDistribution},
		{name: "existing, hostile request", requested: "debian\nubuntu", existing: Debian, exists: true, wantErr: ErrUnknownDistribution},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDistribution(tt.requested, tt.existing, tt.exists)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("ResolveDistribution(%q, %q, %v) = %q, %v, want %q, %v", tt.requested, tt.existing, tt.exists, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestResolveDistributionTellsTheUserHowToReplaceAMismatch(t *testing.T) {
	_, err := ResolveDistribution("debian", Ubuntu, true)
	want := `distribution mismatch: it runs ubuntu, not debian; run "orbit destroy" to replace it`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}
