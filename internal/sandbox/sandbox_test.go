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
		{home: "/Users/dev", want: []string{"/Users/dev/.claude", "/Users/dev/.ssh"}},
		{home: "/", want: []string{"/.claude", "/.ssh"}},
	}
	for _, test := range tests {
		if got := ProtectedDirs(test.home); !slices.Equal(got, test.want) {
			t.Errorf("ProtectedDirs(%q) = %q, want %q", test.home, got, test.want)
		}
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
	tests := []struct {
		name      string
		path      string
		owner     uint32
		user      uint32
		homes     []string
		protected []string
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := ProjectDir{Path: test.path, OwnerUID: test.owner, UserUID: test.user, Homes: test.homes, Protected: test.protected}
			err := ValidateProjectDir(dir)
			if !errors.Is(err, test.want) {
				t.Errorf("ValidateProjectDir(%+v) = %v, want %v", dir, err, test.want)
			}
		})
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
