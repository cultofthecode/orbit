package sandbox

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var hostNameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func TestMachineName(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "plain name", dir: "/Users/dev/Project/orbit", want: "orbit-orbit-884265110920ae4f"},
		{name: "upper case and spaces", dir: "/Users/dev/My  Project", want: "orbit-my-project-24c567dad5115e62"},
		{name: "leading and trailing symbols", dir: "/work/__api.v2__", want: "orbit-api-v2-b9006a89ddd90128"},
		{name: "only symbols", dir: "/work/...", want: "orbit-project-c457d0b4a7e21e04"},
		{name: "not ASCII", dir: "/work/żółw", want: "orbit-w-a7042222ff420508"},
		{name: "shell characters", dir: "/work/a;rm -rf $(x)`y`", want: "orbit-a-rm-rf-x-y-ad31f9ce9bb43503"},
		{name: "leading hyphen", dir: "/work/--isolated", want: "orbit-isolated-4f8d48b0be326b07"},
		{
			name: "long name is cut to 30 characters",
			dir:  "/work/" + strings.Repeat("a", 40),
			want: "orbit-" + strings.Repeat("a", 30) + "-d1c2debea6f8ced8",
		},
		{
			name: "cut does not leave a trailing hyphen",
			dir:  "/work/" + strings.Repeat("a", 29) + "-" + strings.Repeat("b", 10),
			want: "orbit-" + strings.Repeat("a", 29) + "-40572da5ff2376ea",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MachineName(test.dir)
			if got != test.want {
				t.Errorf("MachineName(%q) = %q, want %q", test.dir, got, test.want)
			}
			if !strings.HasPrefix(got, NamePrefix) {
				t.Errorf("MachineName(%q) = %q, want the prefix %q", test.dir, got, NamePrefix)
			}
			if !hostNameLabel.MatchString(got) {
				t.Errorf("MachineName(%q) = %q, want a valid host name label", test.dir, got)
			}
		})
	}
}

func TestMachineNameDependsOnTheWholePath(t *testing.T) {
	first := MachineName("/Users/dev/a/api")
	second := MachineName("/Users/dev/b/api")
	if first == second {
		t.Errorf("two directories with the same base name got the same machine name %q", first)
	}
	if again := MachineName("/Users/dev/a/api"); again != first {
		t.Errorf("MachineName is not stable: %q, then %q", first, again)
	}
}

func TestMachineNameIsAHostNameLabelAtItsLongest(t *testing.T) {
	got := MachineName("/work/" + strings.Repeat("a", 300))
	if len(got) != len(NamePrefix)+30+1+16 || !hostNameLabel.MatchString(got) {
		t.Errorf("MachineName of a long name = %q (%d characters), want a host name label of 53 characters", got, len(got))
	}
}

func TestProtectedDirs(t *testing.T) {
	tests := []struct {
		home string
		want []string
	}{
		{home: "/Users/dev", want: []string{"/Users/dev/.aws", "/Users/dev/.claude", "/Users/dev/.config/gh", "/Users/dev/.gnupg", "/Users/dev/.ssh"}},
		{home: "/", want: []string{"/.aws", "/.claude", "/.config/gh", "/.gnupg", "/.ssh"}},
	}
	for _, test := range tests {
		if got := ProtectedDirs(test.home); !slices.Equal(got, test.want) {
			t.Errorf("ProtectedDirs(%q) = %q, want %q", test.home, got, test.want)
		}
	}
}

func TestProtectedFiles(t *testing.T) {
	tests := []struct {
		home string
		want []string
	}{
		{home: "/Users/dev", want: []string{"/Users/dev/.claude.json"}},
		{home: "/", want: []string{"/.claude.json"}},
	}
	for _, test := range tests {
		if got := ProtectedFiles(test.home); !slices.Equal(got, test.want) {
			t.Errorf("ProtectedFiles(%q) = %q, want %q", test.home, got, test.want)
		}
	}
}

// environment returns a lookup over env, as os.LookupEnv would answer it.
func environment(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, set := env[name]
		return value, set
	}
}

func TestRelocatedDirs(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []Relocated
	}{
		{name: "nothing set", env: map[string]string{}},
		{name: "other variables", env: map[string]string{"HOME": "relative", "SSH_AUTH_SOCK": ""}},
		{
			name: "every variable",
			env: map[string]string{
				"CLAUDE_CONFIG_DIR": "/Users/dev/dotfiles/claude",
				"GH_CONFIG_DIR":     "/Users/dev/dotfiles/gh",
				"GNUPGHOME":         "/Users/dev/dotfiles/gnupg",
				"XDG_CONFIG_HOME":   "/Users/dev/dotfiles/config",
			},
			want: []Relocated{
				{Variable: "CLAUDE_CONFIG_DIR", Path: "/Users/dev/dotfiles/claude"},
				{Variable: "GH_CONFIG_DIR", Path: "/Users/dev/dotfiles/gh"},
				{Variable: "GNUPGHOME", Path: "/Users/dev/dotfiles/gnupg"},
				{Variable: "XDG_CONFIG_HOME", Path: "/Users/dev/dotfiles/config/gh"},
			},
		},
		{
			name: "trailing separator, repeated separator, and dot",
			env:  map[string]string{"GNUPGHOME": "/Users/dev//dotfiles/./gnupg/", "XDG_CONFIG_HOME": "/"},
			want: []Relocated{
				{Variable: "GNUPGHOME", Path: "/Users/dev/dotfiles/gnupg"},
				{Variable: "XDG_CONFIG_HOME", Path: "/gh"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RelocatedDirs(environment(test.env))
			if err != nil || !slices.Equal(got, test.want) {
				t.Errorf("RelocatedDirs = %v, %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestRelocatedFiles(t *testing.T) {
	env := map[string]string{
		"AWS_CONFIG_FILE":             "/Users/dev/dotfiles/aws/config",
		"AWS_SHARED_CREDENTIALS_FILE": "/Users/dev/dotfiles/aws/credentials/",
		"GNUPGHOME":                   "/Users/dev/dotfiles/gnupg",
	}
	want := []Relocated{
		{Variable: "AWS_CONFIG_FILE", Path: "/Users/dev/dotfiles/aws/config"},
		{Variable: "AWS_SHARED_CREDENTIALS_FILE", Path: "/Users/dev/dotfiles/aws/credentials"},
	}

	got, err := RelocatedFiles(environment(env))

	if err != nil || !slices.Equal(got, want) {
		t.Errorf("RelocatedFiles = %v, %v, want %v", got, err, want)
	}
}

func TestRelocatedSingleLinkFiles(t *testing.T) {
	env := map[string]string{
		"AWS_CONFIG_FILE":             "/Users/dev/dotfiles/aws/config",
		"AWS_SHARED_CREDENTIALS_FILE": "/Users/dev/dotfiles/aws/credentials/",
		"CLAUDE_CONFIG_DIR":           "/Users/dev/dotfiles/claude/",
		"GNUPGHOME":                   "/Users/dev/dotfiles/gnupg",
	}
	want := []Relocated{
		{Variable: "AWS_CONFIG_FILE", Path: "/Users/dev/dotfiles/aws/config"},
		{Variable: "AWS_SHARED_CREDENTIALS_FILE", Path: "/Users/dev/dotfiles/aws/credentials"},
		{Variable: "CLAUDE_CONFIG_DIR", Path: "/Users/dev/dotfiles/claude/.claude.json"},
	}

	got, err := RelocatedSingleLinkFiles(environment(env))

	if err != nil || !slices.Equal(got, want) {
		t.Errorf("RelocatedSingleLinkFiles = %v, %v, want %v", got, err, want)
	}
}

// TestRelocatedSingleLinkFilesFailClosed covers a variable of
// RelocatedSingleLinkFiles that is set to a value that names no absolute
// path that Orbit can check.
func TestRelocatedSingleLinkFilesFailClosed(t *testing.T) {
	for _, variable := range []string{"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "CLAUDE_CONFIG_DIR"} {
		for _, value := range []string{"", "dotfiles/claude", "/Users/dev/link/../claude"} {
			t.Run(variable+"="+value, func(t *testing.T) {
				got, err := RelocatedSingleLinkFiles(environment(map[string]string{variable: value}))

				if !errors.Is(err, ErrRelocatedPath) || !strings.Contains(err.Error(), variable+"=") {
					t.Errorf("%s=%q: got %v, %v, want %v naming %s", variable, value, got, err, ErrRelocatedPath, variable)
				}
			})
		}
	}
}

// TestRelocatedLocationsFailClosed covers values that name no absolute path
// that Orbit can protect: it refuses them and names the variable, for every
// variable, also when the other variables are valid.
func TestRelocatedLocationsFailClosed(t *testing.T) {
	values := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "relative", value: "dotfiles/gnupg"},
		{name: "dot", value: "."},
		{name: "dot relative", value: "./gnupg"},
		{name: "parent relative", value: "../gnupg"},
		{name: "unexpanded tilde", value: "~/dotfiles/gnupg"},
		{name: "unexpanded variable", value: "$HOME/dotfiles/gnupg"},
		{name: "space before the separator", value: " /Users/dev/dotfiles"},
		{name: "parent after a name", value: "/Users/dev/link/../gnupg"},
		{name: "trailing parent", value: "/Users/dev/dotfiles/.."},
		{name: "leading parent", value: "/../Users/dev/gnupg"},
	}
	locate := map[string]func(func(string) (string, bool)) ([]Relocated, error){
		"CLAUDE_CONFIG_DIR":           RelocatedDirs,
		"GH_CONFIG_DIR":               RelocatedDirs,
		"GNUPGHOME":                   RelocatedDirs,
		"XDG_CONFIG_HOME":             RelocatedDirs,
		"AWS_CONFIG_FILE":             RelocatedFiles,
		"AWS_SHARED_CREDENTIALS_FILE": RelocatedFiles,
	}
	for variable, relocated := range locate {
		for _, value := range values {
			t.Run(variable+" "+value.name, func(t *testing.T) {
				env := map[string]string{"GH_CONFIG_DIR": "/Users/dev/gh", "AWS_CONFIG_FILE": "/Users/dev/aws", variable: value.value}

				got, err := relocated(environment(env))

				if !errors.Is(err, ErrRelocatedPath) || !strings.Contains(err.Error(), variable+"=") {
					t.Errorf("%s=%q: got %v, %v, want %v naming %s", variable, value.value, got, err, ErrRelocatedPath, variable)
				}
			})
		}
	}
}

// TestRelocatedLocationsTellHowToUseTheDefault covers the message for a
// variable that is set and empty: it tells the user to unset it.
func TestRelocatedLocationsTellHowToUseTheDefault(t *testing.T) {
	_, err := RelocatedDirs(environment(map[string]string{"GNUPGHOME": ""}))

	if !errors.Is(err, ErrRelocatedPath) || !strings.Contains(err.Error(), "unset GNUPGHOME") {
		t.Errorf("RelocatedDirs = %v, want %v that says unset GNUPGHOME", err, ErrRelocatedPath)
	}
}

func TestValidateProjectDir(t *testing.T) {
	const user, root = 501, 0
	homes := []string{"/Users/dev", "/System/Volumes/Data/Users/dev"}
	linked := []string{
		"/Users/dev/dotfiles/ssh",
		"/System/Volumes/Data/Users/dev/dotfiles/ssh",
		"/Users/dev/dotfiles/claude",
		"/System/Volumes/Data/Users/dev/dotfiles/claude",
	}
	outside := []string{"/Volumes/keys/ssh"}
	linkedCredentials := []string{
		"/Users/dev/dotfiles/aws",
		"/System/Volumes/Data/Users/dev/dotfiles/aws",
		"/Users/dev/dotfiles/config/gh",
		"/System/Volumes/Data/Users/dev/dotfiles/config/gh",
		"/Volumes/keys/gnupg",
	}
	fileDirs := []string{"/Users/dev/dotfiles/state", "/System/Volumes/Data/Users/dev/dotfiles/state"}
	fileDirsOutside := []string{"/Volumes/state/claude"}
	tests := []struct {
		name      string
		path      string
		owner     uint32
		user      uint32
		homes     []string
		protected []string
		fileDirs  []string
		want      error
	}{
		{name: "project in home", path: "/Users/dev/Project/orbit", owner: user, user: user, homes: homes},
		{name: "project outside home", path: "/private/tmp/work", owner: user, user: user, homes: homes},
		{name: "prefix look-alike of home", path: "/Users/devx", owner: user, user: user, homes: homes},
		{name: "child of a prefix look-alike of home", path: "/Users/devx/work", owner: user, user: user, homes: homes},
		{name: "sibling of home", path: "/Users/other", owner: user, user: user, homes: homes},
		{name: "prefix look-alike of .ssh", path: "/Users/dev/.sshx", owner: user, user: user, homes: homes},
		{name: "prefix look-alike of .claude", path: "/Users/dev/.claude-work", owner: user, user: user, homes: homes},
		{name: ".ssh outside home", path: "/private/tmp/.ssh", owner: user, user: user, homes: homes},
		{name: ".claude in a project", path: "/Users/dev/Project/orbit/.claude", owner: user, user: user, homes: homes},
		{name: "name with two dots", path: "/Users/dev/..work", owner: user, user: user, homes: homes},
		{name: "sibling of the target of .ssh", path: "/Users/dev/dotfiles/vim", owner: user, user: user, homes: homes, protected: linked},
		{name: "prefix look-alike of the target of .ssh", path: "/Users/dev/dotfiles/sshx", owner: user, user: user, homes: homes, protected: linked},
		{name: "prefix look-alike of the parent of the target", path: "/Users/dev/dotfilesx", owner: user, user: user, homes: homes, protected: linked},
		{name: "project of root, owned by root", path: "/srv/work", owner: root, user: root, homes: []string{"/var/root"}},
		{name: "prefix look-alike of .aws", path: "/Users/dev/.awsx", owner: user, user: user, homes: homes},
		{name: "prefix look-alike of .gnupg", path: "/Users/dev/.gnupg-old", owner: user, user: user, homes: homes},
		{name: "prefix look-alike of .config/gh", path: "/Users/dev/.config/ghx", owner: user, user: user, homes: homes},
		{name: "sibling of .config/gh", path: "/Users/dev/.config/nvim", owner: user, user: user, homes: homes},
		{name: "gh outside .config", path: "/Users/dev/gh", owner: user, user: user, homes: homes},
		{name: ".aws in a project", path: "/Users/dev/Project/orbit/.aws", owner: user, user: user, homes: homes},
		{name: ".gnupg outside home", path: "/private/tmp/.gnupg", owner: user, user: user, homes: homes},
		{name: "sibling of the target of .aws", path: "/Users/dev/dotfiles/vim", owner: user, user: user, homes: homes, protected: linkedCredentials},
		{name: "sibling of the target of .config/gh", path: "/Users/dev/dotfiles/config/nvim", owner: user, user: user, homes: homes, protected: linkedCredentials},
		{name: "child of the directory that holds the target of .claude.json", path: "/Users/dev/dotfiles/state/projects", owner: user, user: user, homes: homes, fileDirs: fileDirs},
		{name: "sibling of the directory that holds the target of .claude.json", path: "/Users/dev/dotfiles/vim", owner: user, user: user, homes: homes, fileDirs: fileDirs},
		{name: "prefix look-alike of the directory that holds the target of .claude.json", path: "/Users/dev/dotfiles/statex", owner: user, user: user, homes: homes, fileDirs: fileDirs},
		{name: "child of the directory outside home that holds the target of .claude.json", path: "/Volumes/state/claude/backup", owner: user, user: user, homes: homes, fileDirs: fileDirsOutside},

		{name: "root", path: "/", owner: user, user: user, homes: homes, want: ErrRootDir},
		{name: "root when a home is root", path: "/", owner: user, user: user, homes: []string{"/"}, want: ErrRootDir},
		{name: "home", path: "/Users/dev", owner: user, user: user, homes: homes, want: ErrHomeDir},
		{name: "home on the data volume", path: "/System/Volumes/Data/Users/dev", owner: user, user: user, homes: homes, want: ErrHomeDir},
		{name: "parent of home", path: "/Users", owner: user, user: user, homes: homes, want: ErrHomeAncestor},
		{name: "grandparent of home", path: "/private", owner: user, user: user, homes: []string{"/private/var/root"}, want: ErrHomeAncestor},
		{name: "data volume", path: "/System/Volumes/Data", owner: user, user: user, homes: homes, want: ErrHomeAncestor},
		{name: "parent of the data volume", path: "/System/Volumes", owner: user, user: user, homes: homes, want: ErrHomeAncestor},
		{name: "grandparent of the data volume", path: "/System", owner: user, user: user, homes: homes, want: ErrHomeAncestor},
		{name: "parent of home on the data volume", path: "/System/Volumes/Data/Users", owner: user, user: user, homes: homes, want: ErrHomeAncestor},
		{name: "grandparent of the data volume as root", path: "/System", owner: root, user: root, homes: homes, want: ErrHomeAncestor},
		{name: "prefix look-alike of the data volume", path: "/System/Volumes/Datax", owner: user, user: user, homes: homes},

		{name: ".claude", path: "/Users/dev/.claude", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: "child of .claude", path: "/Users/dev/.claude/projects/x", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".ssh", path: "/Users/dev/.ssh", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: "child of .ssh", path: "/Users/dev/.ssh/keys", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".ssh on the data volume", path: "/System/Volumes/Data/Users/dev/.ssh", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".ssh when a home is root", path: "/.ssh", owner: user, user: user, homes: []string{"/"}, want: ErrProtectedDir},
		{name: "target of .ssh", path: "/Users/dev/dotfiles/ssh", owner: user, user: user, homes: homes, protected: linked, want: ErrProtectedDir},
		{name: "child of the target of .ssh", path: "/Users/dev/dotfiles/ssh/keys", owner: user, user: user, homes: homes, protected: linked, want: ErrProtectedDir},
		{name: "target of .claude", path: "/Users/dev/dotfiles/claude", owner: user, user: user, homes: homes, protected: linked, want: ErrProtectedDir},
		{name: "child of the target of .claude", path: "/Users/dev/dotfiles/claude/projects", owner: user, user: user, homes: homes, protected: linked, want: ErrProtectedDir},
		{name: "target of .ssh outside home", path: "/Volumes/keys/ssh", owner: user, user: user, homes: homes, protected: outside, want: ErrProtectedDir},
		{name: "directory that holds the targets", path: "/Users/dev/dotfiles", owner: user, user: user, homes: homes, protected: linked, want: ErrProtectedAncestor},
		{name: "parent of a target outside home", path: "/Volumes/keys", owner: user, user: user, homes: homes, protected: outside, want: ErrProtectedAncestor},
		{name: "grandparent of a target outside home", path: "/Volumes", owner: user, user: user, homes: homes, protected: outside, want: ErrProtectedAncestor},
		{name: "without the targets the directory passes", path: "/Users/dev/dotfiles", owner: user, user: user, homes: homes},

		{name: ".aws", path: "/Users/dev/.aws", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: "child of .aws", path: "/Users/dev/.aws/sso/cache", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".aws on the data volume", path: "/System/Volumes/Data/Users/dev/.aws", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".gnupg", path: "/Users/dev/.gnupg", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: "child of .gnupg", path: "/Users/dev/.gnupg/private-keys-v1.d", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".gnupg when a home is root", path: "/.gnupg", owner: user, user: user, homes: []string{"/"}, want: ErrProtectedDir},
		{name: ".config/gh", path: "/Users/dev/.config/gh", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: "child of .config/gh", path: "/Users/dev/.config/gh/extensions", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".config/gh on the data volume", path: "/System/Volumes/Data/Users/dev/.config/gh", owner: user, user: user, homes: homes, want: ErrProtectedDir},
		{name: ".config", path: "/Users/dev/.config", owner: user, user: user, homes: homes, want: ErrProtectedAncestor},
		{name: ".config on the data volume", path: "/System/Volumes/Data/Users/dev/.config", owner: user, user: user, homes: homes, want: ErrProtectedAncestor},
		{name: ".config of the account when the environment is root", path: "/Users/dev/.config", owner: user, user: user, homes: []string{"/", "/Users/dev"}, want: ErrProtectedAncestor},
		{name: "target of .aws", path: "/Users/dev/dotfiles/aws", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "child of the target of .aws", path: "/Users/dev/dotfiles/aws/sso", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "target of .aws on the data volume", path: "/System/Volumes/Data/Users/dev/dotfiles/aws", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "target of .config/gh", path: "/Users/dev/dotfiles/config/gh", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "directory that holds the target of .config/gh", path: "/Users/dev/dotfiles/config", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedAncestor},
		{name: "target of .gnupg outside home", path: "/Volumes/keys/gnupg", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "child of the target of .gnupg outside home", path: "/Volumes/keys/gnupg/private-keys-v1.d", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedDir},
		{name: "directory that holds the target of .gnupg", path: "/Volumes/keys", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedAncestor},
		{name: "directory that holds the targets of the credentials", path: "/Users/dev/dotfiles", owner: user, user: user, homes: homes, protected: linkedCredentials, want: ErrProtectedAncestor},

		{name: "directory that holds the target of .claude.json", path: "/Users/dev/dotfiles/state", owner: user, user: user, homes: homes, fileDirs: fileDirs, want: ErrProtectedFile},
		{name: "directory that holds the target of .claude.json on the data volume", path: "/System/Volumes/Data/Users/dev/dotfiles/state", owner: user, user: user, homes: homes, fileDirs: fileDirs, want: ErrProtectedFile},
		{name: "ancestor of the target of .claude.json", path: "/Users/dev/dotfiles", owner: user, user: user, homes: homes, fileDirs: fileDirs, want: ErrProtectedFile},
		{name: "directory outside home that holds the target of .claude.json", path: "/Volumes/state/claude", owner: user, user: user, homes: homes, fileDirs: fileDirsOutside, want: ErrProtectedFile},
		{name: "ancestor outside home of the target of .claude.json", path: "/Volumes", owner: user, user: user, homes: homes, fileDirs: fileDirsOutside, want: ErrProtectedFile},
		{name: "directory that holds .claude.json when a home is elsewhere", path: "/private/tmp/other", owner: user, user: user, homes: []string{"/Users/dev"}, fileDirs: []string{"/private/tmp/other"}, want: ErrProtectedFile},
		{name: "home holds .claude.json", path: "/Users/dev", owner: user, user: user, homes: homes, fileDirs: []string{"/Users/dev"}, want: ErrHomeDir},
		{name: "without the file targets the directory passes", path: "/Volumes/state/claude", owner: user, user: user, homes: homes},

		{name: "home of the environment when it differs", path: "/private/tmp/other", owner: user, user: user, homes: []string{"/private/tmp/other", "/Users/dev"}, want: ErrHomeDir},
		{name: "home of the account when the environment differs", path: "/Users/dev", owner: user, user: user, homes: []string{"/private/tmp/other", "/Users/dev"}, want: ErrHomeDir},
		{name: ".ssh of the account when the environment differs", path: "/Users/dev/.ssh", owner: user, user: user, homes: []string{"/private/tmp/other", "/Users/dev"}, want: ErrProtectedDir},
		{name: "home of the account when the environment is root", path: "/Users/dev", owner: user, user: user, homes: []string{"/", "/Users/dev"}, want: ErrHomeDir},
		{name: ".claude of the account when the environment is root", path: "/Users/dev/.claude", owner: user, user: user, homes: []string{"/", "/Users/dev"}, want: ErrProtectedDir},
		{name: "parent of the account home when the environment is root", path: "/Users", owner: user, user: user, homes: []string{"/", "/Users/dev"}, want: ErrHomeAncestor},
		{name: "no home", path: "/Users/dev/Project", owner: user, user: user, want: ErrNoHome},
		{name: "empty list of homes", path: "/Users/dev/Project", owner: user, user: user, homes: []string{}, want: ErrNoHome},

		{name: "directory of root", path: "/opt/shared", owner: root, user: user, homes: homes, want: ErrNotOwner},
		{name: "directory of another user", path: "/Users/other/work", owner: 502, user: user, homes: homes, want: ErrNotOwner},
		{name: "directory of another user in home", path: "/Users/dev/Project/orbit", owner: 502, user: user, homes: homes, want: ErrNotOwner},
		{name: "directory of a user, run by root", path: "/srv/work", owner: user, user: root, homes: []string{"/var/root"}, want: ErrNotOwner},
		{name: "volume lookup directory", path: "/.vol", owner: root, user: user, homes: homes, want: ErrNotOwner},
		{name: "no-follow lookup directory", path: "/.nofollow", owner: root, user: user, homes: homes, want: ErrNotOwner},
		{name: "resolve lookup directory", path: "/.resolve", owner: root, user: user, homes: homes, want: ErrNotOwner},
		{name: "system directory", path: "/System/Library", owner: root, user: user, homes: homes, want: ErrNotOwner},

		{name: "empty", path: "", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "relative", path: "Project/orbit", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "dot", path: ".", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "home shorthand", path: "~", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "trailing slash", path: "/Users/dev/Project/", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "home with a trailing slash", path: "/Users/dev/", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "dot dot to home", path: "/Users/dev/Project/..", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "dot dot to .ssh", path: "/Users/dev/Project/../.ssh", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "dot dot to root", path: "/Users/..", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "relative dot dot", path: "../dev", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "dot segment", path: "/Users/./dev", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "double slash", path: "/Users//dev", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "double slash root", path: "//", owner: user, user: user, homes: homes, want: ErrPathNotClean},
		{name: "empty home", path: "/Users/dev/Project", owner: user, user: user, homes: []string{"/Users/dev", ""}, want: ErrPathNotClean},
		{name: "relative home", path: "/Users/dev/Project", owner: user, user: user, homes: []string{"dev"}, want: ErrPathNotClean},
		{name: "home that is not clean", path: "/Users/dev", owner: user, user: user, homes: []string{"/Users/dev/"}, want: ErrPathNotClean},
		{name: "home with dot dot", path: "/Users/dev", owner: user, user: user, homes: []string{"/Users/x/../dev"}, want: ErrPathNotClean},
		{name: "relative protected path", path: "/Users/dev/Project", owner: user, user: user, homes: homes, protected: []string{"dotfiles/ssh"}, want: ErrPathNotClean},
		{name: "protected path with dot dot", path: "/Users/dev/Project", owner: user, user: user, homes: homes, protected: []string{"/Users/dev/Project/../dotfiles/ssh"}, want: ErrPathNotClean},
		{name: "relative directory of a protected file", path: "/Users/dev/Project", owner: user, user: user, homes: homes, fileDirs: []string{"dotfiles/state"}, want: ErrPathNotClean},
		{name: "directory of a protected file with dot dot", path: "/Users/dev/Project", owner: user, user: user, homes: homes, fileDirs: []string{"/Users/dev/Project/../dotfiles/state"}, want: ErrPathNotClean},
		{name: "empty directory of a protected file", path: "/Users/dev/Project", owner: user, user: user, homes: homes, fileDirs: []string{""}, want: ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := ProjectDir{Path: test.path, OwnerUID: test.owner, UserUID: test.user, Homes: test.homes, Protected: test.protected, ProtectedFileDirs: test.fileDirs}
			err := ValidateProjectDir(dir)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateProjectDir(%+v) = %v, want %v", dir, err, test.want)
			}
		})
	}
}

// TestValidateProjectDirRefusesHardLinkedFiles covers a protected file with
// more hard links than were found in the protected locations (#37, #69):
// every project directory is refused, wherever the other link is, and the
// error names the file.
func TestValidateProjectDirRefusesHardLinkedFiles(t *testing.T) {
	homes := []string{"/Users/dev", "/System/Volumes/Data/Users/dev"}
	file := "/Users/dev/.claude.json"
	dataFile := "/System/Volumes/Data/Users/dev/.claude.json"
	settings := "/Users/dev/.claude/settings.json"
	sshConfig := "/Users/dev/.ssh/config"
	tests := []struct {
		name  string
		path  string
		links []FileLinks
		want  error
		names []string
	}{
		{name: "single link", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: file, Count: 1, Found: 1}}},
		{name: "single link in both spellings", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: dataFile, Count: 1, Found: 1}, {Path: file, Count: 1, Found: 1}}},
		{name: "no protected file", path: "/Users/dev/Project/orbit"},
		{name: "every link found", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: settings, Count: 2, Found: 2}}},
		{name: "every one of many links found", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: settings, Count: 65535, Found: 65535}}},
		{name: "more found than counted", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: settings, Count: 2, Found: 3}}},
		{name: "one link more than found", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: settings, Count: 3, Found: 2}}, want: ErrHardLinkedFile, names: []string{settings}},
		{name: "nothing found", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: settings, Count: 1}}, want: ErrHardLinkedFile, names: []string{settings}},
		{name: "other link in the project directory", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: file, Count: 2, Found: 1}}, want: ErrHardLinkedFile, names: []string{file}},
		{name: "other link outside every project directory", path: "/private/tmp/work", links: []FileLinks{{Path: file, Count: 2, Found: 1}}, want: ErrHardLinkedFile, names: []string{file}},
		{name: "many links", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: file, Count: 65535, Found: 1}}, want: ErrHardLinkedFile, names: []string{file}},
		{name: "every spelling is named", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: dataFile, Count: 2, Found: 1}, {Path: file, Count: 2, Found: 1}}, want: ErrHardLinkedFile, names: []string{dataFile, file}},
		{name: "every file is named", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: sshConfig, Count: 2, Found: 1}, {Path: file, Count: 1, Found: 1}, {Path: settings, Count: 4, Found: 3}}, want: ErrHardLinkedFile, names: []string{sshConfig, settings}},
		{name: "target of a symlink", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: "/Users/dev/dotfiles/claude.json", Count: 3, Found: 1}}, want: ErrHardLinkedFile, names: []string{"/Users/dev/dotfiles/claude.json"}},
		{name: "before the path rules", path: "/Users/dev", links: []FileLinks{{Path: file, Count: 2, Found: 1}}, want: ErrHardLinkedFile, names: []string{file}},
		{name: "path rules still apply to a single link", path: "/Users/dev", links: []FileLinks{{Path: file, Count: 1, Found: 1}}, want: ErrHomeDir},
		{name: "path rules still apply when every link is found", path: "/Users/dev/.claude", links: []FileLinks{{Path: settings, Count: 2, Found: 2}}, want: ErrProtectedDir},
		{name: "relative path of a file", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: ".claude.json", Count: 2, Found: 1}}, want: ErrPathNotClean},
		{name: "path of a file with dot dot", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: "/Users/dev/Project/../.claude.json", Count: 1, Found: 1}}, want: ErrPathNotClean},
		{name: "empty path of a file", path: "/Users/dev/Project/orbit", links: []FileLinks{{Path: "", Count: 1, Found: 1}}, want: ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := ProjectDir{Path: test.path, OwnerUID: 501, UserUID: 501, Homes: homes, ProtectedFileLinks: test.links}
			err := ValidateProjectDir(dir)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateProjectDir(%+v) = %v, want %v", dir, err, test.want)
			}
			for _, name := range test.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("ValidateProjectDir(%+v) = %v, want it to name %s", dir, err, name)
				}
			}
		})
	}
}

// TestValidateProjectDirNamesEachHardLinkedFileOnce covers a file that the
// facts hold twice: the error names it once.
func TestValidateProjectDirNamesEachHardLinkedFileOnce(t *testing.T) {
	file := "/Users/dev/dotfiles/claude.json"
	dir := ProjectDir{Path: "/Users/dev/Project/orbit", OwnerUID: 501, UserUID: 501, Homes: []string{"/Users/dev"}, ProtectedFileLinks: []FileLinks{{Path: file, Count: 2, Found: 1}, {Path: file, Count: 2, Found: 1}}}

	err := ValidateProjectDir(dir)

	if !errors.Is(err, ErrHardLinkedFile) || strings.Count(err.Error(), file) != 1 {
		t.Errorf("ValidateProjectDir(%+v) = %v, want %v that names %s once", dir, err, ErrHardLinkedFile, file)
	}
}

// TestValidateProjectDirHasTwoIndependentRules checks that each rule refuses
// on its own what both rules refuse together.
func TestValidateProjectDirHasTwoIndependentRules(t *testing.T) {
	homes := []string{"/Users/dev", "/System/Volumes/Data/Users/dev"}
	for _, path := range []string{"/System", "/System/Volumes", "/System/Volumes/Data", "/Users"} {
		pathRule := ValidateProjectDir(ProjectDir{Path: path, OwnerUID: 501, UserUID: 501, Homes: homes})
		if !errors.Is(pathRule, ErrHomeAncestor) {
			t.Errorf("%s owned by the user = %v, want %v", path, pathRule, ErrHomeAncestor)
		}
		ownerRule := ValidateProjectDir(ProjectDir{Path: path, OwnerUID: 0, UserUID: 501, Homes: []string{"/elsewhere"}})
		if !errors.Is(ownerRule, ErrNotOwner) {
			t.Errorf("%s owned by root, with the home elsewhere = %v, want %v", path, ownerRule, ErrNotOwner)
		}
	}
}

func TestValidateNotNested(t *testing.T) {
	sandboxes := []string{"/Users/dev/Project/orbit", "/System/Volumes/Data/Users/dev/Project/orbit", "/private/tmp/work"}
	tests := []struct {
		name      string
		dir       string
		sandboxes []string
		want      error
	}{
		{name: "no sandbox", dir: "/Users/dev/Project/api"},
		{name: "sibling of a sandbox", dir: "/Users/dev/Project/api", sandboxes: sandboxes},
		{name: "prefix look-alike of a sandbox", dir: "/Users/dev/Project/orbitx", sandboxes: sandboxes},
		{name: "child of a prefix look-alike", dir: "/Users/dev/Project/orbit-2/sub", sandboxes: sandboxes},
		{name: "name with two dots in a sibling", dir: "/Users/dev/Project/..orbit", sandboxes: sandboxes},
		{name: "prefix of the name of a sandbox", dir: "/Users/dev/Project/orb", sandboxes: sandboxes},
		{name: "prefix of a sandbox in a parent", dir: "/private/tm", sandboxes: sandboxes},
		{name: "sibling of the parent of a sandbox", dir: "/Users/dev/Projects", sandboxes: sandboxes},

		{name: "child of a sandbox", dir: "/Users/dev/Project/orbit/sub", sandboxes: sandboxes, want: ErrNestedProjectDir},
		{name: "deep child of a sandbox", dir: "/Users/dev/Project/orbit/a/b/c/d", sandboxes: sandboxes, want: ErrNestedProjectDir},
		{name: "project directory of another sandbox", dir: "/private/tmp/work", sandboxes: sandboxes, want: ErrNestedProjectDir},
		{name: "child of a sandbox on the data volume", dir: "/System/Volumes/Data/Users/dev/Project/orbit/sub", sandboxes: sandboxes, want: ErrNestedProjectDir},
		{name: "every directory when a sandbox mounts root", dir: "/Users/dev/Project/api", sandboxes: []string{"/"}, want: ErrNestedProjectDir},
		{name: "child of a sandbox with a dot directory", dir: "/Users/dev/Project/orbit/.git", sandboxes: sandboxes, want: ErrNestedProjectDir},
		{name: "root mounted by another sandbox", dir: "/", sandboxes: []string{"/"}, want: ErrNestedProjectDir},

		{name: "parent of a sandbox", dir: "/Users/dev/Project", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "grandparent of a sandbox", dir: "/Users/dev", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "home-like ancestor of a sandbox", dir: "/Users", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "root above every sandbox", dir: "/", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "parent of a sandbox on the data volume", dir: "/System/Volumes/Data/Users/dev", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "data volume above a sandbox", dir: "/System/Volumes/Data", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "parent of a sandbox in a temporary directory", dir: "/private/tmp", sandboxes: sandboxes, want: ErrContainsProjectDir},
		{name: "parent of a sandbox with a dot directory", dir: "/Users/dev/Project/orbit", sandboxes: []string{"/Users/dev/Project/orbit/.git"}, want: ErrContainsProjectDir},
		{name: "ancestor of a deep sandbox", dir: "/Users/dev/Project", sandboxes: []string{"/Users/dev/Project/a/b/c/d"}, want: ErrContainsProjectDir},
		{name: "parent of one of several sandboxes", dir: "/srv", sandboxes: []string{"/Users/dev/Project/orbit", "/srv/work"}, want: ErrContainsProjectDir},

		{name: "relative directory", dir: "Project/orbit/sub", sandboxes: sandboxes, want: ErrPathNotClean},
		{name: "directory with dot dot", dir: "/Users/dev/Project/orbit/sub/..", sandboxes: sandboxes, want: ErrPathNotClean},
		{name: "directory with a trailing slash", dir: "/Users/dev/Project/orbit/sub/", sandboxes: sandboxes, want: ErrPathNotClean},
		{name: "empty directory", dir: "", sandboxes: sandboxes, want: ErrPathNotClean},
		{name: "sandbox with dot dot", dir: "/Users/dev/Project/api", sandboxes: []string{"/Users/dev/Project/orbit/../api"}, want: ErrPathNotClean},
		{name: "relative sandbox", dir: "/Users/dev/Project/api", sandboxes: []string{"Project"}, want: ErrPathNotClean},
		{name: "empty sandbox", dir: "/Users/dev/Project/api", sandboxes: []string{""}, want: ErrPathNotClean},
		{name: "parent of a sandbox with dot dot", dir: "/Users/dev/Project", sandboxes: []string{"/Users/dev/Project/orbit/.."}, want: ErrPathNotClean},
		{name: "parent of a sandbox with a trailing slash", dir: "/Users/dev/Project", sandboxes: []string{"/Users/dev/Project/orbit/"}, want: ErrPathNotClean},
		{name: "parent with a trailing slash", dir: "/Users/dev/Project/", sandboxes: sandboxes, want: ErrPathNotClean},
		{name: "sandbox in home shorthand", dir: "/Users/dev/Project", sandboxes: []string{"~/Project/orbit"}, want: ErrPathNotClean},
		{name: "sandbox with a dot segment", dir: "/Users/dev/Project", sandboxes: []string{"/Users/dev/./Project/orbit"}, want: ErrPathNotClean},
		{name: "sandbox with a double slash", dir: "/Users/dev/Project", sandboxes: []string{"/Users/dev//Project/orbit"}, want: ErrPathNotClean},
		{name: "unrelated directory beside a sandbox that is not clean", dir: "/srv/api", sandboxes: []string{"/Users/dev/Project/orbit", "Project/orbit"}, want: ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNotNested(test.dir, test.sandboxes)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateNotNested(%q, %q) = %v, want %v", test.dir, test.sandboxes, err, test.want)
			}
		})
	}
}

func TestValidateMounts(t *testing.T) {
	const dir = "/Users/dev/Project/orbit"
	tests := []struct {
		name   string
		dir    string
		mounts map[string]string
		want   error
	}{
		{name: "project directory at its own path", dir: dir, mounts: map[string]string{dir: dir}},

		{name: "no mount", dir: dir, mounts: map[string]string{}, want: ErrMountMismatch},
		{name: "no mount table", dir: dir, mounts: nil, want: ErrMountMismatch},
		{name: "swapped for .ssh", dir: dir, mounts: map[string]string{dir: "/Users/dev/.ssh"}, want: ErrMountMismatch},
		{name: "swapped for a sibling", dir: dir, mounts: map[string]string{dir: "/Users/dev/Project/secret"}, want: ErrMountMismatch},
		{name: "swapped for root", dir: dir, mounts: map[string]string{dir: "/"}, want: ErrMountMismatch},
		{name: "parent of the project", dir: dir, mounts: map[string]string{dir: "/Users/dev/Project"}, want: ErrMountMismatch},
		{name: "child of the project", dir: dir, mounts: map[string]string{dir: dir + "/sub"}, want: ErrMountMismatch},
		{name: "other spelling of the project", dir: dir, mounts: map[string]string{dir: "/System/Volumes/Data" + dir}, want: ErrMountMismatch},
		{name: "project in another letter case", dir: dir, mounts: map[string]string{dir: strings.ToLower(dir)}, want: ErrMountMismatch},
		{name: "project with a trailing slash", dir: dir, mounts: map[string]string{dir: dir + "/"}, want: ErrMountMismatch},
		{name: "project at another path", dir: dir, mounts: map[string]string{"/mnt/mac" + dir: dir}, want: ErrMountMismatch},
		{name: "project and a second mount", dir: dir, mounts: map[string]string{dir: dir, "/home/dev/.ssh": "/Users/dev/.ssh"}, want: ErrMountMismatch},
		{name: "project twice", dir: dir, mounts: map[string]string{dir: dir, "/srv/orbit": dir}, want: ErrMountMismatch},
		{name: "another directory at its own path", dir: dir, mounts: map[string]string{"/Users/dev/.ssh": "/Users/dev/.ssh"}, want: ErrMountMismatch},

		{name: "empty project directory", dir: "", mounts: map[string]string{"": ""}, want: ErrPathNotClean},
		{name: "relative project directory", dir: "Project/orbit", mounts: map[string]string{"Project/orbit": "Project/orbit"}, want: ErrPathNotClean},
		{name: "project directory with dot dot", dir: dir + "/..", mounts: map[string]string{dir + "/..": dir + "/.."}, want: ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateMounts(test.dir, test.mounts)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateMounts(%q, %v) = %v, want %v", test.dir, test.mounts, err, test.want)
			}
		})
	}
}
