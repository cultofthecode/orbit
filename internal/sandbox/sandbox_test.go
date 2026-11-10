package sandbox

import (
	"errors"
	"regexp"
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
		{name: "plain name", dir: "/Users/dev/Project/orbit", want: "orbit-orbit-88426511"},
		{name: "upper case and spaces", dir: "/Users/dev/My  Project", want: "orbit-my-project-24c567da"},
		{name: "leading and trailing symbols", dir: "/work/__api.v2__", want: "orbit-api-v2-b9006a89"},
		{name: "only symbols", dir: "/work/...", want: "orbit-project-c457d0b4"},
		{name: "not ASCII", dir: "/work/żółw", want: "orbit-w-a7042222"},
		{name: "shell characters", dir: "/work/a;rm -rf $(x)`y`", want: "orbit-a-rm-rf-x-y-ad31f9ce"},
		{name: "leading hyphen", dir: "/work/--isolated", want: "orbit-isolated-4f8d48b0"},
		{
			name: "long name is cut to 30 characters",
			dir:  "/work/" + strings.Repeat("a", 40),
			want: "orbit-" + strings.Repeat("a", 30) + "-d1c2debe",
		},
		{
			name: "cut does not leave a trailing hyphen",
			dir:  "/work/" + strings.Repeat("a", 29) + "-" + strings.Repeat("b", 10),
			want: "orbit-" + strings.Repeat("a", 29) + "-40572da5",
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

func TestValidateProjectDir(t *testing.T) {
	const home = "/Users/dev"
	tests := []struct {
		name string
		dir  string
		home string
		want error
	}{
		{name: "project in home", dir: "/Users/dev/Project/orbit", home: home},
		{name: "project outside home", dir: "/private/tmp/work", home: home},
		{name: "prefix look-alike of home", dir: "/Users/devx", home: home},
		{name: "child of a prefix look-alike of home", dir: "/Users/devx/work", home: home},
		{name: "sibling of home", dir: "/Users/other", home: home},
		{name: "prefix look-alike of .ssh", dir: "/Users/dev/.sshx", home: home},
		{name: "prefix look-alike of .claude", dir: "/Users/dev/.claude-work", home: home},
		{name: ".ssh outside home", dir: "/private/tmp/.ssh", home: home},
		{name: ".claude in a project", dir: "/Users/dev/Project/orbit/.claude", home: home},
		{name: "name with two dots", dir: "/Users/dev/..work", home: home},

		{name: "root", dir: "/", home: home, want: ErrRootDir},
		{name: "home", dir: "/Users/dev", home: home, want: ErrHomeDir},
		{name: "parent of home", dir: "/Users", home: home, want: ErrHomeAncestor},
		{name: "grandparent of home", dir: "/private", home: "/private/var/root", want: ErrHomeAncestor},
		{name: ".claude", dir: "/Users/dev/.claude", home: home, want: ErrProtectedDir},
		{name: "child of .claude", dir: "/Users/dev/.claude/projects/x", home: home, want: ErrProtectedDir},
		{name: ".ssh", dir: "/Users/dev/.ssh", home: home, want: ErrProtectedDir},
		{name: "child of .ssh", dir: "/Users/dev/.ssh/keys", home: home, want: ErrProtectedDir},
		{name: "root when home is root", dir: "/", home: "/", want: ErrRootDir},
		{name: ".ssh when home is root", dir: "/.ssh", home: "/", want: ErrProtectedDir},

		{name: "empty", dir: "", home: home, want: ErrPathNotClean},
		{name: "relative", dir: "Project/orbit", home: home, want: ErrPathNotClean},
		{name: "dot", dir: ".", home: home, want: ErrPathNotClean},
		{name: "home shorthand", dir: "~", home: home, want: ErrPathNotClean},
		{name: "trailing slash", dir: "/Users/dev/Project/", home: home, want: ErrPathNotClean},
		{name: "home with a trailing slash", dir: "/Users/dev/", home: home, want: ErrPathNotClean},
		{name: "dot dot to home", dir: "/Users/dev/Project/..", home: home, want: ErrPathNotClean},
		{name: "dot dot to .ssh", dir: "/Users/dev/Project/../.ssh", home: home, want: ErrPathNotClean},
		{name: "dot dot to root", dir: "/Users/..", home: home, want: ErrPathNotClean},
		{name: "relative dot dot", dir: "../dev", home: home, want: ErrPathNotClean},
		{name: "dot segment", dir: "/Users/./dev", home: home, want: ErrPathNotClean},
		{name: "double slash", dir: "/Users//dev", home: home, want: ErrPathNotClean},
		{name: "double slash root", dir: "//", home: home, want: ErrPathNotClean},
		{name: "empty home", dir: "/Users/dev/Project", home: "", want: ErrPathNotClean},
		{name: "relative home", dir: "/Users/dev/Project", home: "dev", want: ErrPathNotClean},
		{name: "home that is not clean", dir: "/Users/dev", home: "/Users/dev/", want: ErrPathNotClean},
		{name: "home with dot dot", dir: "/Users/dev", home: "/Users/x/../dev", want: ErrPathNotClean},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProjectDir(test.dir, test.home)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateProjectDir(%q, %q) = %v, want %v", test.dir, test.home, err, test.want)
			}
		})
	}
}
