package provision

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestDistributionsListEveryProfileUbuntuFirst(t *testing.T) {
	if got, want := Distributions(), []string{"ubuntu", "debian"}; !slices.Equal(got, want) {
		t.Errorf("Distributions() = %q, want %q", got, want)
	}
	got := Distributions()
	got[0] = "changed"
	if Distributions()[0] != "ubuntu" {
		t.Error("Distributions returns its own table, not a copy")
	}
}

func TestScriptRefusesAnUnknownDistribution(t *testing.T) {
	for _, name := range []string{"", "Ubuntu", "ubuntu ", " ubuntu", "ubuntu\ndebian", "ubuntu:26.04", "debian:bookworm", "alpine", "../ubuntu", "ubuntu'; rm -rf /; '", "$(id)", "-ubuntu"} {
		got, err := Script(ScriptInput{DefaultUID: 501, Distribution: name})
		if !errors.Is(err, ErrScriptInput) || got != nil {
			t.Errorf("Script(Distribution: %q) = %q, %v, want nil, %v", name, got, err, ErrScriptInput)
		}
	}
}

func TestProfilesHoldOnlyPlainNames(t *testing.T) {
	plain := regexp.MustCompile(`^[a-z0-9]+$`)
	for _, p := range profiles {
		for _, value := range []string{p.name, p.osID, p.codename} {
			if !plain.MatchString(value) {
				t.Errorf("profile %q holds %q, want lowercase letters and digits only: the guard writes it into the script", p.name, value)
			}
		}
	}
}

func TestScriptChecksTheOSReleaseBeforeItChangesAnything(t *testing.T) {
	want := map[string][2]string{
		"ubuntu": {"ID=ubuntu", "VERSION_CODENAME=resolute"},
		"debian": {"ID=debian", "VERSION_CODENAME=trixie"},
	}
	eachDistribution(t, func(t *testing.T, distribution string) {
		lines := script(t, distribution)
		id := lineIndex(t, lines, `grep -qx '`+want[distribution][0]+`' /etc/os-release || fail "this machine is not `+distribution+`"`)
		codename := lineIndex(t, lines, `grep -qx '`+want[distribution][1]+`' /etc/os-release || fail "this machine is not `+distribution+" "+strings.TrimPrefix(want[distribution][1], "VERSION_CODENAME=")+`"`)
		removal := lineIndex(t, lines, "rm -f /etc/orbit/provisioned")
		for _, guard := range []int{id, codename} {
			if guard > removal {
				t.Errorf("the os-release guard on line %d comes after the removal of the marker on line %d", guard, removal)
			}
		}
		for i, line := range lines {
			if i <= max(id, codename) || line == "}" || line == "" {
				continue
			}
			if strings.Contains(line, "os-release") {
				t.Errorf("line %d checks os-release again: %q", i, line)
			}
		}
	})
}

func TestEachDistributionNamesOnlyItsOwnGuard(t *testing.T) {
	ubuntu := strings.Join(script(t, "ubuntu"), "\n")
	debian := strings.Join(script(t, "debian"), "\n")
	if strings.Contains(ubuntu, "ID=debian") || strings.Contains(ubuntu, "trixie") {
		t.Error("the Ubuntu script names Debian")
	}
	if strings.Contains(debian, "ID=ubuntu") || strings.Contains(debian, "resolute") {
		t.Error("the Debian script names Ubuntu")
	}
}

func TestDebianInstallsTheDockerPackagesOfTrixie(t *testing.T) {
	debian := strings.Join(script(t, "debian"), "\n")
	for _, pkg := range []string{" docker-cli ", " docker-compose "} {
		if !strings.Contains(debian, pkg) {
			t.Errorf("the Debian script does not install%s", pkg)
		}
	}
	if strings.Contains(debian, "docker-compose-v2") {
		t.Error("the Debian script installs docker-compose-v2, which trixie does not have")
	}
	if ubuntu := strings.Join(script(t, "ubuntu"), "\n"); strings.Contains(ubuntu, "docker-cli") || !strings.Contains(ubuntu, "docker-compose-v2") {
		t.Error("the Ubuntu script changed its Docker packages")
	}
}

func TestScriptsDifferOnlyWhereTheProfilesDiffer(t *testing.T) {
	ubuntu := script(t, "ubuntu")
	debian := strings.Join(script(t, "debian"), "\n")
	var missing int
	for _, line := range ubuntu {
		if !strings.Contains("\n"+debian+"\n", "\n"+line+"\n") {
			missing++
		}
	}
	if missing != 3 {
		t.Errorf("%d lines of the Ubuntu script are not in the Debian script, want 3: the two guard lines and the Docker package line", missing)
	}
}

func TestScriptMatchesItsGoldenFile(t *testing.T) {
	eachDistribution(t, func(t *testing.T, distribution string) {
		got, err := Script(ScriptInput{Distribution: distribution, DefaultUID: 501, ForwardSSHAgent: true, AgentDomains: []string{"claude.ai", "api.anthropic.com"}})
		if err != nil {
			t.Fatalf("Script: %v", err)
		}
		compareGolden(t, "script-"+distribution+".sh", string(got))
	})
}
