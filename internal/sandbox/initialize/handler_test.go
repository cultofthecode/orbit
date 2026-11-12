package initialize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cultofthecode/orbit/internal/orbstack/orbstacktest"
	"github.com/cultofthecode/orbit/internal/sandbox"
)

// dataVolume is the path of the data volume of macOS. A firmlink joins its
// directories to the root.
const dataVolume = "/System/Volumes/Data"

// relocatingVariables are the environment variables that move a protected
// location. TestMain unsets them, so that the environment of the developer
// does not change what a test protects.
var relocatingVariables = []string{"CLAUDE_CONFIG_DIR", "GH_CONFIG_DIR", "GNUPGHOME", "XDG_CONFIG_HOME", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"}

func TestMain(m *testing.M) {
	for _, variable := range relocatingVariables {
		if err := os.Unsetenv(variable); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

// newHome makes a home directory with a project, .ssh, and .claude in it,
// makes it the home directory of the environment of the test, and returns
// its canonical path.
func newHome(t *testing.T) string {
	t.Helper()
	home := newDir(t)
	makeDirs(t, home, "work/api", ".ssh", ".claude/projects")
	t.Setenv("HOME", home)
	return home
}

// newDir returns the canonical path of a new temporary directory.
func newDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// makeDirs makes the directories dirs in parent.
func makeDirs(t *testing.T, parent string, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(parent, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// symlink makes a symlink to target in a new directory and returns its path.
func symlink(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(newDir(t), "link")
	symlinkAt(t, target, link)
	return link
}

// symlinkAt makes the symlink link to target.
func symlinkAt(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// account returns the entry of the account database for the user of the
// test, with home as the home directory.
func account(home string) *user.User {
	return &user.User{Uid: strconv.Itoa(os.Getuid()), HomeDir: home}
}

// testAgentName is the name of the only agent that lookupAgent knows.
const testAgentName = "test-agent"

// testAgent is the agent that lookupAgent returns for testAgentName.
var testAgent = sandbox.Agent{
	Install:       "curl -fsSL https://agent.example/install.sh | bash",
	EgressDomains: []string{"agent.example", "downloads.agent.example"},
}

// errUnknownAgent is the error of lookupAgent for every other name.
var errUnknownAgent = errors.New("unknown agent")

// lookupAgent is the registry of the tests: it knows testAgent only.
func lookupAgent(name string) (sandbox.Agent, error) {
	if name != testAgentName {
		return sandbox.Agent{}, fmt.Errorf("%w %q", errUnknownAgent, name)
	}
	return testAgent, nil
}

// newHandler returns a handler on fake that finds entry in the account
// database, knows testAgent, and builds the root script with fakeScript.
func newHandler(fake *orbstacktest.Fake, entry *user.User) Handler {
	return Handler{
		Machines:          fake,
		Agent:             lookupAgent,
		ProvisionScript:   fakeScript,
		DockerSetupScript: func() []byte { return []byte(testDockerSetupScript) },
		CurrentUser:       func() (*user.User, error) { return entry, nil },
	}
}

// newHandlerOn is newHandler on any machines.
func newHandlerOn(machines Machines, entry *user.User) Handler {
	handler := newHandler(nil, entry)
	handler.Machines = machines
	return handler
}

// failingScript is a fake whose RunAsAgent fails for one script only.
type failingScript struct {
	*orbstacktest.Fake
	script string
	err    error
}

// RunAsAgent records script and fails when it is the failing script.
func (f failingScript) RunAsAgent(ctx context.Context, name string, script []byte) error {
	if err := f.Fake.RunAsAgent(ctx, name, script); err != nil {
		return err
	}
	if string(script) == f.script {
		return f.err
	}
	return nil
}

// fakeScript returns a root script that names the default uid, the agent
// domains, and the SSH agent forwarding.
func fakeScript(defaultUID uint32, agentDomains []string, forwardSSHAgent bool) ([]byte, error) {
	return []byte(scriptFor(defaultUID, agentDomains, forwardSSHAgent)), nil
}

// scriptFor returns the script that fakeScript builds for uid, agentDomains,
// and forward.
func scriptFor(uid uint32, agentDomains []string, forward bool) string {
	return "provision with the default uid " + strconv.FormatUint(uint64(uid), 10) + " and the agent domains [" + strings.Join(agentDomains, " ") + "] and SSH agent forwarding " + strconv.FormatBool(forward) + "\n"
}

// ownScript returns the script that fakeScript builds for the user who runs
// the test, without an agent.
func ownScript() string {
	return scriptFor(uint32(os.Getuid()), nil, true)
}

// ownAgentScript returns the script that fakeScript builds for the user who
// runs the test, with testAgent.
func ownAgentScript() string {
	return scriptFor(uint32(os.Getuid()), testAgent.EgressDomains, true)
}

// testDockerSetupScript is the client setup script that newHandler passes.
const testDockerSetupScript = "set up the Docker client of the agent user\n"

// testInstallScript is the install script of testAgent that the handler
// runs as the agent user.
const testInstallScript = "{\nset -euo pipefail\nset -a\n. /etc/environment\nset +a\ncurl -fsSL https://agent.example/install.sh | bash\n}\n"

// agentCalls returns the calls that provision the machine name with
// testAgent and install it.
func agentCalls(name string) []orbstacktest.Call {
	return []orbstacktest.Call{
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: ownAgentScript()},
		{Op: orbstacktest.OpRunAsAgent, Name: name, Script: testDockerSetupScript},
		{Op: orbstacktest.OpRunAsAgent, Name: name, Script: testInstallScript},
	}
}

// requireSpelling stops the test when the file system does not accept path
// as a spelling of an existing file. macOS must accept it, so there the test
// fails; on another system it is skipped.
func requireSpelling(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return
	}
	if runtime.GOOS == "darwin" {
		t.Fatalf("macOS does not accept the spelling %q: %v", path, err)
	}
	t.Skipf("the file system does not accept this spelling: %v", err)
}

// refusal is one directory that the handler must refuse.
type refusal struct {
	name string
	dir  string
	want error
}

// checkRefusals runs the handler for each refusal and checks that it fails
// with the wanted error before it asks OrbStack anything.
func checkRefusals(t *testing.T, entry *user.User, refusals []refusal) {
	t.Helper()
	for _, test := range refusals {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err := newHandler(fake, entry).Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, test.want) {
				t.Errorf("Handle(%q) = %v, want %v", test.dir, err, test.want)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

// checkCreates checks that the handler creates the machine of the canonical
// directory project when it gets the path dir.
func checkCreates(t *testing.T, entry *user.User, dir, project string) {
	t.Helper()
	fake := orbstacktest.New(sandbox.NamePrefix)
	name := sandbox.MachineName(project)

	got, err := newHandler(fake, entry).Handle(context.Background(), Input{Dir: dir})

	if err != nil {
		t.Fatalf("Handle(%q) = %v", dir, err)
	}
	if want := (Output{Name: name, Dir: project, Created: true}); got != want {
		t.Errorf("Handle(%q) = %+v, want %+v", dir, got, want)
	}
	if !slices.Equal(fake.Calls, createCalls(name, project)) {
		t.Errorf("calls = %v, want %v", fake.Calls, createCalls(name, project))
	}
}

// checkedCreateCalls returns the calls that create the machine name of the
// directory project and check its mounts.
func checkedCreateCalls(name, project string) []orbstacktest.Call {
	return []orbstacktest.Call{
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpCreate, Name: name, Dir: project, Forward: true},
		{Op: orbstacktest.OpSSHAgentForwarded, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpActiveMounts, Name: name},
	}
}

// checkedKeepCalls returns the calls that find the machine name and check
// its mounts.
func checkedKeepCalls(name string) []orbstacktest.Call {
	return []orbstacktest.Call{
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpState, Name: name},
		{Op: orbstacktest.OpSSHAgentForwarded, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpActiveMounts, Name: name},
	}
}

// provisionCalls are the calls that provision the machine name for the user
// who runs the test and set up its Docker client.
func provisionCalls(name string) []orbstacktest.Call {
	return []orbstacktest.Call{
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: ownScript()},
		{Op: orbstacktest.OpRunAsAgent, Name: name, Script: testDockerSetupScript},
	}
}

// createCalls returns the calls that create, check, and provision the
// machine name of the directory project.
func createCalls(name, project string) []orbstacktest.Call {
	return slices.Concat(checkedCreateCalls(name, project), provisionCalls(name))
}

// keepCalls returns the calls that find, check, and provision the machine
// name.
func keepCalls(name string) []orbstacktest.Call {
	return slices.Concat(checkedKeepCalls(name), provisionCalls(name))
}

func TestHandleCreatesTheMachine(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	tests := []struct {
		name string
		dir  string
	}{
		{name: "canonical path", dir: project},
		{name: "symlink to the project", dir: symlink(t, project)},
		{name: "path that is not clean", dir: home + "/work/../work//api/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkCreates(t, account(home), test.dir, project)
		})
	}
}

func TestHandleKeepsAnExistingMachine(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.Forwarded[name] = true

	got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if want := (Output{Name: name, Dir: project, Created: false}); got != want {
		t.Errorf("Handle = %+v, want %+v", got, want)
	}
	if !slices.Equal(fake.Calls, keepCalls(name)) {
		t.Errorf("calls = %v, want %v", fake.Calls, keepCalls(name))
	}
}

// TestHandleRefusesNestedProjectDirectories covers a project directory that
// another machine already mounts, in any spelling: the agent of that machine
// controls its path and could swap it between the check and the mount.
func TestHandleRefusesNestedProjectDirectories(t *testing.T) {
	home := newHome(t)
	work := filepath.Join(home, "work")
	project := filepath.Join(work, "api")
	makeDirs(t, home, "work/api/sub")
	tests := []struct {
		name  string
		mount string
		dir   string
	}{
		{name: "parent mounted by another sandbox", mount: work, dir: project},
		{name: "grandparent mounted by another sandbox", mount: home + "/work", dir: filepath.Join(project, "sub")},
		{name: "project mounted by another machine", mount: project, dir: project},
		{name: "parent mounted through a symlink", mount: symlink(t, work), dir: project},
		{name: "parent mounted in upper case", mount: strings.ToUpper(work), dir: project},
		{name: "parent mounted on the data volume", mount: dataVolume + work, dir: project},
		{name: "project reached through a symlink", mount: work, dir: symlink(t, project)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireSpelling(t, test.mount)
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines["orbit-work-0123456789abcdef"] = map[string]string{"/srv/work": test.mount}

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, sandbox.ErrNestedProjectDir) || got != (Output{}) {
				t.Errorf("Handle(%q) = %+v, %v, want %v", test.dir, got, err, sandbox.ErrNestedProjectDir)
			}
			if wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpMounts}}; !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
	t.Run("mount of a machine without the prefix", func(t *testing.T) {
		fake := orbstacktest.New(sandbox.NamePrefix)
		fake.Machines["ubuntu"] = map[string]string{work: work}
		if _, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project}); !errors.Is(err, sandbox.ErrNestedProjectDir) {
			t.Errorf("Handle(%q) = %v, want %v", project, err, sandbox.ErrNestedProjectDir)
		}
	})
}

// TestHandleRefusesProjectDirectoriesThatContainAnotherSandbox covers the
// roles of TestHandleRefusesNestedProjectDirectories swapped: another machine
// mounts a directory inside the project directory, in any spelling. The new
// agent would control the path of that mount and could swap it before the
// next start of the other machine.
func TestHandleRefusesProjectDirectoriesThatContainAnotherSandbox(t *testing.T) {
	home := newHome(t)
	work := filepath.Join(home, "work")
	project := filepath.Join(work, "api")
	makeDirs(t, home, "work/api/sub")
	tests := []struct {
		name  string
		mount string
		dir   string
	}{
		{name: "child mounted by another sandbox", mount: project, dir: work},
		{name: "grandchild mounted by another sandbox", mount: filepath.Join(project, "sub"), dir: work},
		{name: "child mounted through a symlink", mount: symlink(t, project), dir: work},
		{name: "child mounted in upper case", mount: strings.ToUpper(project), dir: work},
		{name: "child mounted on the data volume", mount: dataVolume + project, dir: work},
		{name: "parent reached through a symlink", mount: project, dir: symlink(t, work)},
		{name: "parent reached in upper case", mount: project, dir: strings.ToUpper(work)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireSpelling(t, test.mount)
			requireSpelling(t, test.dir)
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines["orbit-api-0123456789abcdef"] = map[string]string{"/srv/api": test.mount}

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, sandbox.ErrContainsProjectDir) || got != (Output{}) {
				t.Errorf("Handle(%q) = %+v, %v, want %v", test.dir, got, err, sandbox.ErrContainsProjectDir)
			}
			if wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpMounts}}; !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
	t.Run("mount of a machine without the prefix", func(t *testing.T) {
		fake := orbstacktest.New(sandbox.NamePrefix)
		fake.Machines["ubuntu"] = map[string]string{project: project}
		if _, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: work}); !errors.Is(err, sandbox.ErrContainsProjectDir) {
			t.Errorf("Handle(%q) = %v, want %v", work, err, sandbox.ErrContainsProjectDir)
		}
	})
}

// TestHandleRefusesProjectDirectoriesThatContainAMissingSandbox covers a
// directory that another machine mounts and that no longer exists. OrbStack
// resolves the stored path again at the next start of that machine, so the
// new agent could put a symlink there. The stored path counts in every
// spelling, also when it does not exist.
func TestHandleRefusesProjectDirectoriesThatContainAMissingSandbox(t *testing.T) {
	home := newHome(t)
	work := filepath.Join(home, "work")
	linkedWork := symlink(t, work)
	leavingLink := filepath.Join(work, "elsewhere")
	symlinkAt(t, newDir(t), leavingLink)
	tests := []struct {
		name   string
		parent string
		mount  string
		dir    string
	}{
		{name: "child that does not exist", parent: work, mount: "gone", dir: work},
		{name: "grandchild below a child that does not exist", parent: work, mount: "gone/deeper", dir: work},
		{name: "grandchild that does not exist", parent: work, mount: "api/gone", dir: work},
		{name: "child that does not exist, in upper case", parent: strings.ToUpper(work), mount: "gone", dir: work},
		{name: "child that does not exist, on the data volume", parent: dataVolume + work, mount: "gone", dir: work},
		{name: "child that does not exist, through a symlink", parent: linkedWork, mount: "gone", dir: work},
		{name: "child that does not exist, parent reached through a symlink", parent: work, mount: "gone", dir: linkedWork},
		{name: "child that does not exist, below a symlink that leaves the directory", parent: leavingLink, mount: "gone", dir: work},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireSpelling(t, test.parent)
			mount := filepath.Join(test.parent, test.mount)
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines["orbit-gone-0123456789abcdef"] = map[string]string{"/srv/gone": mount}

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: test.dir})

			if !errors.Is(err, sandbox.ErrContainsProjectDir) || got != (Output{}) {
				t.Errorf("Handle(%q) = %+v, %v, want %v", test.dir, got, err, sandbox.ErrContainsProjectDir)
			}
			if wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpMounts}}; !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
}

// TestHandleRefusesStoredMountsThroughASymlink covers a mount of another
// machine whose stored path passes through a symlink. Orbit stores canonical
// paths, so the host layout changed after that machine was created, and
// OrbStack follows the symlink at the next start of that machine. A dangling
// symlink, or one whose hop lies in the new directory, would let the new agent
// choose what the other machine mounts, so the handler refuses every project
// directory while that machine exists.
func TestHandleRefusesStoredMountsThroughASymlink(t *testing.T) {
	tests := []struct {
		name   string
		layout func(t *testing.T, home string) (mount, dir string)
	}{
		{name: "dangling symlink at the stored path", layout: func(t *testing.T, home string) (string, string) {
			makeDirs(t, home, "code")
			symlinkAt(t, filepath.Join(home, "code", "proj"), filepath.Join(home, "proj"))
			return filepath.Join(home, "proj"), filepath.Join(home, "code")
		}},
		{name: "dangling symlink at an ancestor, absolute target", layout: func(t *testing.T, home string) (string, string) {
			makeDirs(t, home, "code")
			symlinkAt(t, filepath.Join(home, "code", "proj"), filepath.Join(home, "proj"))
			return filepath.Join(home, "proj", "api"), filepath.Join(home, "code")
		}},
		{name: "dangling symlink at an ancestor, relative target", layout: func(t *testing.T, home string) (string, string) {
			makeDirs(t, home, "code")
			symlinkAt(t, filepath.Join("code", "proj"), filepath.Join(home, "proj"))
			return filepath.Join(home, "proj", "api"), filepath.Join(home, "code")
		}},
		{name: "hop inside the directory, existing target", layout: func(t *testing.T, home string) (string, string) {
			makeDirs(t, home, "elsewhere/api")
			symlinkAt(t, filepath.Join(home, "work", "q"), filepath.Join(home, "p"))
			symlinkAt(t, filepath.Join(home, "elsewhere"), filepath.Join(home, "work", "q"))
			return filepath.Join(home, "p", "api"), filepath.Join(home, "work")
		}},
		{name: "hop inside the directory, missing target", layout: func(t *testing.T, home string) (string, string) {
			makeDirs(t, home, "elsewhere")
			symlinkAt(t, filepath.Join(home, "work", "q"), filepath.Join(home, "p"))
			symlinkAt(t, filepath.Join(home, "elsewhere"), filepath.Join(home, "work", "q"))
			return filepath.Join(home, "p", "gone"), filepath.Join(home, "work")
		}},
		{name: "symlink that leaves the directory", layout: func(t *testing.T, home string) (string, string) {
			symlinkAt(t, newDir(t), filepath.Join(home, "old"))
			return filepath.Join(home, "old", "api"), filepath.Join(home, "work")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := newHome(t)
			mount, dir := test.layout(t, home)
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines["orbit-linked-0123456789abcdef"] = map[string]string{"/srv/linked": mount}

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: dir})

			if !errors.Is(err, ErrLinkedSandboxDir) || got != (Output{}) {
				t.Errorf("Handle(%q) = %+v, %v, want %v", dir, got, err, ErrLinkedSandboxDir)
			}
			if err != nil && !strings.Contains(err.Error(), "orbit-linked-0123456789abcdef") {
				t.Errorf("Handle(%q) = %v, want the name of the other machine", dir, err)
			}
			if wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpMounts}}; !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
}

// TestHandleRefusesStoredMountsThatAreNotClean covers a mount of another
// machine that is not an absolute and clean path. OrbStack would resolve it
// in a way that the nesting check cannot follow, so the handler refuses
// every project directory while that machine exists.
func TestHandleRefusesStoredMountsThatAreNotClean(t *testing.T) {
	home := newHome(t)
	work := filepath.Join(home, "work")
	makeDirs(t, home, "other")
	t.Chdir(home)
	mounts := []struct {
		name  string
		mount string
	}{
		{name: "relative path of an existing directory", mount: "work/api"},
		{name: "relative path that does not exist", mount: "work/gone"},
		{name: "home shorthand", mount: "~/work/api"},
		{name: "trailing slash", mount: work + "/gone/"},
		{name: "dot dot", mount: work + "/x/../gone"},
		{name: "dot segment", mount: work + "/./gone"},
		{name: "empty", mount: ""},
	}
	for _, mount := range mounts {
		for _, dir := range []string{work, filepath.Join(home, "other")} {
			t.Run(mount.name+" with "+filepath.Base(dir), func(t *testing.T) {
				fake := orbstacktest.New(sandbox.NamePrefix)
				fake.Machines["orbit-odd-0123456789abcdef"] = map[string]string{"/srv/odd": mount.mount}

				got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: dir})

				if !errors.Is(err, sandbox.ErrPathNotClean) || got != (Output{}) {
					t.Errorf("Handle(%q) = %+v, %v, want %v", dir, got, err, sandbox.ErrPathNotClean)
				}
				if wantCalls := []orbstacktest.Call{{Op: orbstacktest.OpMounts}}; !slices.Equal(fake.Calls, wantCalls) {
					t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
				}
			})
		}
	}
}

func TestHandleAcceptsDirectoriesOutsideOtherSandboxes(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	makeDirs(t, home, "work/ap", "work/api-old", "other")
	tests := []struct {
		name     string
		mounts   map[string]string
		spelling bool
	}{
		{name: "sibling", mounts: map[string]string{filepath.Join(home, "other"): filepath.Join(home, "other")}},
		{name: "prefix look-alike", mounts: map[string]string{"/srv/ap": filepath.Join(home, "work", "ap")}},
		{name: "longer sibling", mounts: map[string]string{"/srv/old": filepath.Join(home, "work", "api-old")}},
		{name: "directory that does not exist", mounts: map[string]string{"/srv/gone": filepath.Join(home, "gone")}},
		{name: "sibling that does not exist", mounts: map[string]string{"/srv/gone": filepath.Join(home, "work", "gone")}},
		{name: "prefix look-alike that does not exist", mounts: map[string]string{"/srv/apix": filepath.Join(home, "work", "apix")}},
		{name: "directory below a missing look-alike", mounts: map[string]string{"/srv/apix": filepath.Join(home, "work", "apix", "api")}},
		{name: "machine without mounts", mounts: map[string]string{}},
		{name: "sibling in upper case", mounts: map[string]string{"/srv/other": strings.ToUpper(filepath.Join(home, "other"))}, spelling: true},
		{name: "sibling on the data volume", mounts: map[string]string{"/srv/other": dataVolume + filepath.Join(home, "other")}, spelling: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, mount := range test.mounts {
				if test.spelling {
					requireSpelling(t, mount)
				}
			}
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines["orbit-other-0123456789abcdef"] = test.mounts
			name := sandbox.MachineName(project)

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

			if want := (Output{Name: name, Dir: project, Created: true}); err != nil || got != want {
				t.Errorf("Handle = %+v, %v, want %+v", got, err, want)
			}
		})
	}
}

// TestHandleQuotesAHostileDirectoryOnAMountMismatch pins that the
// mount-mismatch error of a created machine cannot write an escape sequence
// or a line break to the terminal (#111).
func TestHandleQuotesAHostileDirectoryOnAMountMismatch(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "a\x1b[2K\nb")
	makeDirs(t, home, filepath.Join("work", "a\x1b[2K\nb"))
	other := filepath.Join(home, "work", "c\x1b]0;x\x07d")
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Active[sandbox.MachineName(project)] = map[string]string{project: other}

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	for _, want := range []string{"want " + strconv.Quote(project), strconv.Quote(other)} {
		if !errors.Is(err, sandbox.ErrMountMismatch) || !strings.Contains(err.Error(), want) {
			t.Errorf("Handle = %v, want %v with %s", err, sandbox.ErrMountMismatch, want)
		}
	}
	if err != nil && strings.ContainsAny(err.Error(), "\x1b\n\x07") {
		t.Errorf("Handle = %q, want no control character", err)
	}
}

// TestHandleDeletesACreatedMachineThatMountsAnotherDirectory covers a
// project path that is swapped between the check and the mount: the new
// machine mounts another directory, and the handler deletes it.
func TestHandleDeletesACreatedMachineThatMountsAnotherDirectory(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	ssh := filepath.Join(home, ".ssh")
	name := sandbox.MachineName(project)
	tests := []struct {
		name   string
		active map[string]string
	}{
		{name: "swapped for .ssh", active: map[string]string{project: ssh}},
		{name: "swapped for the parent", active: map[string]string{project: filepath.Dir(project)}},
		{name: "second mount", active: map[string]string{project: project, "/mnt/mac": "/"}},
		{name: "no mount", active: map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Active[name] = test.active

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

			if !errors.Is(err, sandbox.ErrMountMismatch) || got != (Output{}) {
				t.Errorf("Handle = %+v, %v, want %v", got, err, sandbox.ErrMountMismatch)
			}
			if len(fake.Machines) != 0 {
				t.Errorf("machines = %v, want the new machine deleted", fake.Machines)
			}
			wantCalls := append(checkedCreateCalls(name, project), orbstacktest.Call{Op: orbstacktest.OpDelete, Name: name})
			if !slices.Equal(fake.Calls, wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
			}
		})
	}
}

// TestHandleRefusesAnExistingMachineThatMountsAnotherDirectory covers a
// machine with the name of the project that mounts something else: the
// handler does not trust a machine by its name, and does not delete it.
func TestHandleRefusesAnExistingMachineThatMountsAnotherDirectory(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	ssh := filepath.Join(home, ".ssh")
	name := sandbox.MachineName(project)
	tests := []struct {
		name       string
		configured map[string]string
		active     map[string]string
		wantCalls  []orbstacktest.Call
	}{
		{
			name:       "configured with .ssh",
			configured: map[string]string{project: ssh},
			wantCalls:  []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}, {Op: orbstacktest.OpMounts}},
		},
		{
			name:       "configured with a second mount",
			configured: map[string]string{project: project, "/home/dev": home},
			wantCalls:  []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}, {Op: orbstacktest.OpMounts}},
		},
		{
			name:       "configured at another path",
			configured: map[string]string{"/mnt/mac" + project: project},
			wantCalls:  []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}, {Op: orbstacktest.OpMounts}},
		},
		{
			name:       "no mount",
			configured: map[string]string{},
			wantCalls:  []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}, {Op: orbstacktest.OpMounts}},
		},
		{
			name:       "mounts .ssh after a swap",
			configured: map[string]string{project: project},
			active:     map[string]string{project: ssh},
			wantCalls:  checkedKeepCalls(name),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines[name] = test.configured
			fake.Forwarded[name] = true
			if test.active != nil {
				fake.Active[name] = test.active
			}

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

			if !errors.Is(err, sandbox.ErrMountMismatch) || got != (Output{}) {
				t.Errorf("Handle = %+v, %v, want %v", got, err, sandbox.ErrMountMismatch)
			}
			if _, kept := fake.Machines[name]; !kept {
				t.Errorf("the existing machine was deleted")
			}
			if !slices.Equal(fake.Calls, test.wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, test.wantCalls)
			}
		})
	}
}

func TestHandleRefusesInvalidDirectories(t *testing.T) {
	home := newHome(t)
	file := filepath.Join(home, "work", "notes.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	checkRefusals(t, account(home), []refusal{
		{name: "home", dir: home, want: sandbox.ErrHomeDir},
		{name: "home with a trailing slash", dir: home + "/", want: sandbox.ErrHomeDir},
		{name: "dot dot to home", dir: home + "/work/..", want: sandbox.ErrHomeDir},
		{name: "symlink to home", dir: symlink(t, home), want: sandbox.ErrHomeDir},
		{name: "root", dir: "/", want: sandbox.ErrRootDir},
		{name: "dot dot to root", dir: home + strings.Repeat("/..", 40), want: sandbox.ErrRootDir},
		{name: "parent of home", dir: filepath.Dir(home), want: sandbox.ErrHomeAncestor},
		{name: ".ssh", dir: filepath.Join(home, ".ssh"), want: sandbox.ErrProtectedDir},
		{name: "child of .claude", dir: filepath.Join(home, ".claude", "projects"), want: sandbox.ErrProtectedDir},
		{name: "symlink to .ssh", dir: symlink(t, filepath.Join(home, ".ssh")), want: sandbox.ErrProtectedDir},
		{name: "missing directory", dir: filepath.Join(home, "work", "missing"), want: fs.ErrNotExist},
		{name: "broken symlink", dir: symlink(t, filepath.Join(home, "work", "missing")), want: fs.ErrNotExist},
		{name: "empty path", dir: "", want: fs.ErrNotExist},
		{name: "file", dir: file, want: ErrNotDirectory},
		{name: "symlink to a file", dir: symlink(t, file), want: ErrNotDirectory},
	})
}

// TestHandleResolvesOtherSpellings covers the spellings that macOS accepts
// for a directory besides its canonical path.
func TestHandleResolvesOtherSpellings(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	spellings := []struct {
		name  string
		spell func(path string) string
	}{
		{name: "upper case", spell: strings.ToUpper},
		{name: "data volume", spell: func(path string) string { return dataVolume + path }},
		{name: "data volume in upper case", spell: func(path string) string { return dataVolume + strings.ToUpper(path) }},
	}
	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			requireSpelling(t, spelling.spell(project))
			checkRefusals(t, account(home), []refusal{
				{name: "home", dir: spelling.spell(home), want: sandbox.ErrHomeDir},
				{name: "parent of home", dir: spelling.spell(filepath.Dir(home)), want: sandbox.ErrHomeAncestor},
				{name: ".ssh", dir: spelling.spell(filepath.Join(home, ".ssh")), want: sandbox.ErrProtectedDir},
				{name: "child of .claude", dir: spelling.spell(filepath.Join(home, ".claude", "projects")), want: sandbox.ErrProtectedDir},
			})
			checkCreates(t, account(home), spelling.spell(project), project)
		})
	}
}

// TestHandleRefusesDirectoriesThatHoldHomeOnTheDataVolume covers the
// directories of macOS that are no ancestors of the canonical path of the
// home directory and still contain it: the home directory is also a
// directory of the data volume. The account of the test has the uid of root,
// which owns these directories, so that only the rule on paths can refuse
// them.
func TestHandleRefusesDirectoriesThatHoldHomeOnTheDataVolume(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS has a data volume")
	}
	home := newHome(t)
	root := &user.User{Uid: "0", HomeDir: home}
	checkRefusals(t, root, []refusal{
		{name: "data volume", dir: dataVolume, want: sandbox.ErrHomeAncestor},
		{name: "parent of the data volume", dir: "/System/Volumes", want: sandbox.ErrHomeAncestor},
		{name: "grandparent of the data volume", dir: "/System", want: sandbox.ErrHomeAncestor},
		{name: "symlink to the data volume", dir: symlink(t, dataVolume), want: sandbox.ErrHomeAncestor},
		{name: "parent of home on the data volume", dir: dataVolume + filepath.Dir(home), want: sandbox.ErrHomeAncestor},
	})
}

// TestHandleRefusesLinkedProtectedDirectories covers a home directory in
// which .ssh is a symlink to a directory outside the home directory and
// .claude is a symlink to a directory inside it.
func TestHandleRefusesLinkedProtectedDirectories(t *testing.T) {
	home := newDir(t)
	vault := newDir(t)
	makeDirs(t, home, "work/api", "dotfiles/claude/projects", "dotfiles/vim")
	makeDirs(t, vault, "keys/ssh/old", "notes")
	symlinkAt(t, filepath.Join(vault, "keys", "ssh"), filepath.Join(home, ".ssh"))
	symlinkAt(t, filepath.Join(home, "dotfiles", "claude"), filepath.Join(home, ".claude"))
	t.Setenv("HOME", home)

	checkRefusals(t, account(home), []refusal{
		{name: ".ssh", dir: filepath.Join(home, ".ssh"), want: sandbox.ErrProtectedDir},
		{name: "child of .ssh", dir: filepath.Join(home, ".ssh", "old"), want: sandbox.ErrProtectedDir},
		{name: "target of .ssh", dir: filepath.Join(vault, "keys", "ssh"), want: sandbox.ErrProtectedDir},
		{name: "child of the target of .ssh", dir: filepath.Join(vault, "keys", "ssh", "old"), want: sandbox.ErrProtectedDir},
		{name: "directory that holds the target of .ssh", dir: filepath.Join(vault, "keys"), want: sandbox.ErrProtectedAncestor},
		{name: "ancestor of the target of .ssh", dir: vault, want: sandbox.ErrProtectedAncestor},
		{name: ".claude", dir: filepath.Join(home, ".claude"), want: sandbox.ErrProtectedDir},
		{name: "child of .claude", dir: filepath.Join(home, ".claude", "projects"), want: sandbox.ErrProtectedDir},
		{name: "target of .claude", dir: filepath.Join(home, "dotfiles", "claude"), want: sandbox.ErrProtectedDir},
		{name: "child of the target of .claude", dir: filepath.Join(home, "dotfiles", "claude", "projects"), want: sandbox.ErrProtectedDir},
		{name: "directory that holds the target of .claude", dir: filepath.Join(home, "dotfiles"), want: sandbox.ErrProtectedAncestor},
		{name: "symlink to the directory that holds the target", dir: symlink(t, filepath.Join(home, "dotfiles")), want: sandbox.ErrProtectedAncestor},
	})
	t.Run("sibling of the target of .claude", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, "dotfiles", "vim"), filepath.Join(home, "dotfiles", "vim"))
	})
	t.Run("sibling of the directory that holds the target of .ssh", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(vault, "notes"), filepath.Join(vault, "notes"))
	})
	t.Run("project", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
}

// TestHandleWithProtectedPathsThatAreNoDirectories covers a protected path
// that leads nowhere, which protects only where its target would be created,
// and one that is a file, which the handler cannot resolve and so refuses to
// go on.
func TestHandleWithProtectedPathsThatAreNoDirectories(t *testing.T) {
	t.Run("broken symlink", func(t *testing.T) {
		home := newDir(t)
		makeDirs(t, home, "work/api")
		symlinkAt(t, filepath.Join(home, "missing"), filepath.Join(home, ".ssh"))
		t.Setenv("HOME", home)
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
		checkRefusals(t, account(home), []refusal{
			{name: ".ssh", dir: filepath.Join(home, ".ssh"), want: fs.ErrNotExist},
		})
	})
	t.Run("file", func(t *testing.T) {
		home := newDir(t)
		makeDirs(t, home, "work/api")
		if err := os.WriteFile(filepath.Join(home, ".claude"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		checkRefusals(t, account(home), []refusal{
			{name: "project", dir: filepath.Join(home, "work", "api"), want: syscall.ENOTDIR},
		})
	})
}

// writeFile makes the empty file path.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHandleRefusesCredentialDirectories covers the protected directories of
// AWS, GnuPG, and the GitHub CLI, and the file .claude.json, in the home
// directory itself.
func TestHandleRefusesCredentialDirectories(t *testing.T) {
	home := newDir(t)
	makeDirs(t, home, "work/api", ".aws/sso", ".gnupg/private-keys-v1.d", ".config/gh", ".config/nvim")
	writeFile(t, filepath.Join(home, ".claude.json"))
	t.Setenv("HOME", home)

	checkRefusals(t, account(home), []refusal{
		{name: ".aws", dir: filepath.Join(home, ".aws"), want: sandbox.ErrProtectedDir},
		{name: "child of .aws", dir: filepath.Join(home, ".aws", "sso"), want: sandbox.ErrProtectedDir},
		{name: "symlink to .aws", dir: symlink(t, filepath.Join(home, ".aws")), want: sandbox.ErrProtectedDir},
		{name: ".gnupg", dir: filepath.Join(home, ".gnupg"), want: sandbox.ErrProtectedDir},
		{name: "child of .gnupg", dir: filepath.Join(home, ".gnupg", "private-keys-v1.d"), want: sandbox.ErrProtectedDir},
		{name: "symlink to the child of .gnupg", dir: symlink(t, filepath.Join(home, ".gnupg", "private-keys-v1.d")), want: sandbox.ErrProtectedDir},
		{name: ".config/gh", dir: filepath.Join(home, ".config", "gh"), want: sandbox.ErrProtectedDir},
		{name: "symlink to .config/gh", dir: symlink(t, filepath.Join(home, ".config", "gh")), want: sandbox.ErrProtectedDir},
		{name: ".config", dir: filepath.Join(home, ".config"), want: sandbox.ErrProtectedAncestor},
		{name: "symlink to .config", dir: symlink(t, filepath.Join(home, ".config")), want: sandbox.ErrProtectedAncestor},
		{name: "home, which holds .claude.json", dir: home, want: sandbox.ErrHomeDir},
	})
	t.Run("sibling of .config/gh", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, ".config", "nvim"), filepath.Join(home, ".config", "nvim"))
	})
	t.Run("project", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
}

// TestHandleRefusesLinkedCredentials covers a home directory in which .aws
// is a symlink to a directory outside the home directory, .gnupg a symlink to
// a directory inside it, .config a symlink that moves .config/gh, and
// .claude.json a symlink to a file in another directory.
func TestHandleRefusesLinkedCredentials(t *testing.T) {
	home := newDir(t)
	vault := newDir(t)
	store := newDir(t)
	makeDirs(t, home, "work/api", "dotfiles/gnupg/private-keys-v1.d", "dotfiles/config/gh", "dotfiles/config/nvim", "dotfiles/vim")
	makeDirs(t, vault, "aws/sso", "notes")
	makeDirs(t, store, "state/projects", "other")
	writeFile(t, filepath.Join(store, "state", "claude.json"))
	symlinkAt(t, filepath.Join(vault, "aws"), filepath.Join(home, ".aws"))
	symlinkAt(t, filepath.Join("dotfiles", "gnupg"), filepath.Join(home, ".gnupg"))
	symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(home, ".config"))
	symlinkAt(t, filepath.Join(store, "state", "claude.json"), filepath.Join(home, ".claude.json"))
	t.Setenv("HOME", home)

	checkRefusals(t, account(home), []refusal{
		{name: ".aws", dir: filepath.Join(home, ".aws"), want: sandbox.ErrProtectedDir},
		{name: "target of .aws", dir: filepath.Join(vault, "aws"), want: sandbox.ErrProtectedDir},
		{name: "child of the target of .aws", dir: filepath.Join(vault, "aws", "sso"), want: sandbox.ErrProtectedDir},
		{name: "directory that holds the target of .aws", dir: vault, want: sandbox.ErrProtectedAncestor},
		{name: "target of .gnupg by a relative symlink", dir: filepath.Join(home, "dotfiles", "gnupg"), want: sandbox.ErrProtectedDir},
		{name: "child of the target of .gnupg", dir: filepath.Join(home, "dotfiles", "gnupg", "private-keys-v1.d"), want: sandbox.ErrProtectedDir},
		{name: "directory that holds the target of .gnupg", dir: filepath.Join(home, "dotfiles"), want: sandbox.ErrProtectedAncestor},
		{name: ".config/gh through the linked .config", dir: filepath.Join(home, ".config", "gh"), want: sandbox.ErrProtectedDir},
		{name: "target of .config/gh", dir: filepath.Join(home, "dotfiles", "config", "gh"), want: sandbox.ErrProtectedDir},
		{name: "target of .config", dir: filepath.Join(home, "dotfiles", "config"), want: sandbox.ErrProtectedAncestor},
		{name: "linked .config", dir: filepath.Join(home, ".config"), want: sandbox.ErrProtectedAncestor},
		{name: "directory that holds the target of .claude.json", dir: filepath.Join(store, "state"), want: sandbox.ErrProtectedFile},
		{name: "symlink to the directory that holds the target of .claude.json", dir: symlink(t, filepath.Join(store, "state")), want: sandbox.ErrProtectedFile},
		{name: "ancestor of the target of .claude.json", dir: store, want: sandbox.ErrProtectedFile},
	})
	accepted := []string{
		filepath.Join(vault, "notes"),
		filepath.Join(home, "dotfiles", "vim"),
		filepath.Join(home, "dotfiles", "config", "nvim"),
		filepath.Join(store, "state", "projects"),
		filepath.Join(store, "other"),
		filepath.Join(home, "work", "api"),
	}
	for _, dir := range accepted {
		t.Run("accepts "+dir, func(t *testing.T) {
			checkCreates(t, account(home), dir, dir)
		})
	}
}

// TestHandleRefusesALinkedParentOfAMissingCredentialDirectory covers a
// .config that is a symlink into a project directory while .config/gh does
// not exist yet: the GitHub CLI creates it there at its next sign-in.
func TestHandleRefusesALinkedParentOfAMissingCredentialDirectory(t *testing.T) {
	home := newDir(t)
	makeDirs(t, home, "work/api", "dotfiles/config/nvim", "dotfiles/vim")
	symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(home, ".config"))
	t.Setenv("HOME", home)
	dotfiles := filepath.Join(home, "dotfiles")

	checkRefusals(t, account(home), []refusal{
		{name: "directory that holds the target of .config", dir: dotfiles, want: sandbox.ErrProtectedAncestor},
		{name: "target of .config", dir: filepath.Join(dotfiles, "config"), want: sandbox.ErrProtectedAncestor},
		{name: "linked .config", dir: filepath.Join(home, ".config"), want: sandbox.ErrProtectedAncestor},
	})
	t.Run("upper case", func(t *testing.T) {
		requireSpelling(t, strings.ToUpper(dotfiles))
		checkRefusals(t, account(home), []refusal{
			{name: "directory that holds the target of .config", dir: strings.ToUpper(dotfiles), want: sandbox.ErrProtectedAncestor},
		})
	})
	accepted := []string{
		filepath.Join(dotfiles, "vim"),
		filepath.Join(dotfiles, "config", "nvim"),
		filepath.Join(home, "work", "api"),
	}
	for _, dir := range accepted {
		t.Run("accepts "+dir, func(t *testing.T) {
			checkCreates(t, account(home), dir, dir)
		})
	}
}

// TestHandleResolvesTheProtectedFile covers a .claude.json that reaches its
// file through more than one symlink, and other spellings of the directory
// that holds that file.
func TestHandleResolvesTheProtectedFile(t *testing.T) {
	home := newDir(t)
	links := newDir(t)
	store := newDir(t)
	makeDirs(t, home, "work/api")
	makeDirs(t, store, "state")
	writeFile(t, filepath.Join(store, "state", "claude.json"))
	symlinkAt(t, filepath.Join("..", filepath.Base(store), "state", "claude.json"), filepath.Join(links, "claude.json"))
	symlinkAt(t, filepath.Join(links, "claude.json"), filepath.Join(home, ".claude.json"))
	t.Setenv("HOME", home)
	state := filepath.Join(store, "state")

	checkRefusals(t, account(home), []refusal{
		{name: "directory that holds the final target", dir: state, want: sandbox.ErrProtectedFile},
	})
	spellings := []struct {
		name  string
		spell func(path string) string
	}{
		{name: "upper case", spell: strings.ToUpper},
		{name: "data volume", spell: func(path string) string { return dataVolume + path }},
	}
	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			requireSpelling(t, spelling.spell(state))
			checkRefusals(t, account(home), []refusal{
				{name: "directory that holds the final target", dir: spelling.spell(state), want: sandbox.ErrProtectedFile},
			})
		})
	}
	t.Run("project", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
}

// hardLink makes the hard link link to the file target.
func hardLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Link(target, link); err != nil {
		t.Fatal(err)
	}
}

// unreadable takes every permission from the file at path, so a test fails
// when the handler opens the file instead of reading its metadata (D08).
func unreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

// checkHardLinkRefusal checks that the handler refuses the project
// directory dir before it asks OrbStack anything, because the protected
// file file has another hard link, and that the error names file.
func checkHardLinkRefusal(t *testing.T, home, dir, file string) {
	t.Helper()
	_ = refuseHardLink(t, home, dir, file)
}

// refuseHardLink runs checkHardLinkRefusal and returns the refusal.
func refuseHardLink(t *testing.T, home, dir, file string) error {
	t.Helper()
	fake := orbstacktest.New(sandbox.NamePrefix)

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: dir})

	if !errors.Is(err, sandbox.ErrHardLinkedFile) {
		t.Fatalf("Handle(%q) = %v, want %v", dir, err, sandbox.ErrHardLinkedFile)
	}
	if !strings.Contains(err.Error(), file) {
		t.Errorf("Handle(%q) = %v, want it to name %s", dir, err, file)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
	return err
}

// TestHandleRefusesAHardLinkedProtectedFile covers a .claude.json with
// another hard link (#37). A hard link is the same file under another name,
// so the agent could read and rewrite it through a link in its project
// directory. Orbit cannot find the other link, so it refuses every project
// directory while one exists, and it reads only the metadata of the file.
func TestHandleRefusesAHardLinkedProtectedFile(t *testing.T) {
	t.Run("other link in the project directory", func(t *testing.T) {
		home := newHome(t)
		file := filepath.Join(home, ".claude.json")
		writeFile(t, file)
		hardLink(t, file, filepath.Join(home, "work", "api", "claude.json"))
		unreadable(t, file)
		checkHardLinkRefusal(t, home, filepath.Join(home, "work", "api"), file)
	})
	t.Run("other link outside every project directory", func(t *testing.T) {
		home := newHome(t)
		file := filepath.Join(home, ".claude.json")
		writeFile(t, file)
		hardLink(t, file, filepath.Join(newDir(t), "claude.json"))
		unreadable(t, file)
		checkHardLinkRefusal(t, home, filepath.Join(home, "work", "api"), file)
	})
	t.Run("target of a symlink with another link", func(t *testing.T) {
		home := newHome(t)
		store := newDir(t)
		target := filepath.Join(store, "claude.json")
		writeFile(t, target)
		hardLink(t, target, filepath.Join(home, "work", "api", "claude.json"))
		symlinkAt(t, target, filepath.Join(home, ".claude.json"))
		checkHardLinkRefusal(t, home, filepath.Join(home, "work", "api"), target)
	})
	t.Run("other link inside a walked protected directory names the one-link file", func(t *testing.T) {
		home := newHome(t)
		file := filepath.Join(home, ".claude.json")
		writeFile(t, file)
		walked := filepath.Join(home, ".claude", "claude.json.bak")
		writeFile(t, filepath.Join(home, ".claude", "settings.json"))
		hardLink(t, file, walked)
		err := refuseHardLink(t, home, filepath.Join(home, "work", "api"), file)
		if strings.Contains(err.Error(), walked) {
			t.Errorf("Handle() = %v, want it not to name %s", err, walked)
		}
	})
	t.Run("single link", func(t *testing.T) {
		home := newHome(t)
		file := filepath.Join(home, ".claude.json")
		writeFile(t, file)
		unreadable(t, file)
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
	t.Run("hard link of another file in the project directory", func(t *testing.T) {
		home := newHome(t)
		writeFile(t, filepath.Join(home, ".claude.json"))
		other := filepath.Join(home, "notes.txt")
		writeFile(t, other)
		hardLink(t, other, filepath.Join(home, "work", "api", "notes.txt"))
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
}

// credentialFile makes the empty file rel, relative to home, with its parent
// directories, and returns its path.
func credentialFile(t *testing.T, home, rel string) string {
	t.Helper()
	makeDirs(t, home, filepath.Dir(rel))
	path := filepath.Join(home, rel)
	writeFile(t, path)
	return path
}

// linkedCredential makes the credential file rel in home with another hard
// link at link, takes every permission from the file, and returns its path.
func linkedCredential(t *testing.T, home, rel, link string) string {
	t.Helper()
	file := credentialFile(t, home, rel)
	hardLink(t, file, link)
	unreadable(t, file)
	return file
}

// linkedIntoProject makes the credential file rel in home with another hard
// link in the project directory work/api, and returns its path.
func linkedIntoProject(t *testing.T, home, rel string) string {
	t.Helper()
	return linkedCredential(t, home, rel, filepath.Join(home, "work", "api", filepath.Base(rel)))
}

// linkedOutside makes the credential file rel in home with another hard link
// outside every project directory, and returns its path.
func linkedOutside(t *testing.T, home, rel string) string {
	t.Helper()
	return linkedCredential(t, home, rel, filepath.Join(newDir(t), filepath.Base(rel)))
}

// hardLinkLayout is a layout of files in a bare home directory and the
// protected file with another hard link that the handler must name when it
// refuses work/api; an empty file means that the handler accepts it.
type hardLinkLayout struct {
	name   string
	layout func(t *testing.T, home string) (file string)
}

// checkHardLinkLayouts builds each layout in a new bare home directory and
// runs the handler for work/api.
func checkHardLinkLayouts(t *testing.T, layouts []hardLinkLayout) {
	t.Helper()
	for _, test := range layouts {
		t.Run(test.name, func(t *testing.T) {
			home := newBareHome(t)
			file := test.layout(t, home)
			project := filepath.Join(home, "work", "api")
			if file == "" {
				checkCreates(t, account(home), project, project)
				return
			}
			checkHardLinkRefusal(t, home, project, file)
		})
	}
}

// TestHandleRefusesHardLinkedFilesInProtectedDirectories covers a regular
// file in a protected directory with a hard link outside every protected
// location (#69): the agent could read and rewrite it through a link in its
// project directory. A link next to the target of a per-file symlink counts
// as outside too, because the walk does not list that directory. Every file
// is unreadable, so the test fails when the handler opens one (D08).
func TestHandleRefusesHardLinkedFilesInProtectedDirectories(t *testing.T) {
	layouts := []hardLinkLayout{
		{name: "settings of Claude Code in the project", layout: func(t *testing.T, home string) string {
			return linkedIntoProject(t, home, ".claude/settings.json")
		}},
		{name: "SSH configuration in the project", layout: func(t *testing.T, home string) string {
			return linkedIntoProject(t, home, ".ssh/config")
		}},
		{name: "AWS credentials in the project", layout: func(t *testing.T, home string) string {
			return linkedIntoProject(t, home, ".aws/credentials")
		}},
		{name: "token of the GitHub CLI in the project", layout: func(t *testing.T, home string) string {
			return linkedIntoProject(t, home, ".config/gh/hosts.yml")
		}},
		{name: "deep file in the project", layout: func(t *testing.T, home string) string {
			return linkedIntoProject(t, home, ".gnupg/private-keys-v1.d/key.key")
		}},
		{name: "other link outside every project", layout: func(t *testing.T, home string) string {
			return linkedOutside(t, home, ".claude/settings.json")
		}},
		{name: "one of two inside links and one outside", layout: func(t *testing.T, home string) string {
			file := linkedOutside(t, home, ".claude/settings.json")
			hardLink(t, file, filepath.Join(home, ".claude", "settings.json.bak"))
			return file
		}},
		{name: "symlinked .ssh", layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, ".ssh", "dotfiles/ssh")
			return linkedIntoProject(t, home, "dotfiles/ssh/config")
		}},
		{name: "per-file symlink in .ssh", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			file := linkedIntoProject(t, home, "dotfiles/ssh/config")
			symlinkAt(t, file, filepath.Join(home, ".ssh", "config"))
			return file
		}},
		{name: "per-file symlink through a symlinked directory", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			file := linkedIntoProject(t, home, "dotfiles/ssh/config")
			symlinkAt(t, filepath.Join(home, "dotfiles"), filepath.Join(home, "links"))
			symlinkAt(t, filepath.Join(home, "links", "ssh", "config"), filepath.Join(home, ".ssh", "config"))
			return file
		}},
		{name: "link next to the target of a per-file symlink", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			file := linkedCredential(t, home, "dotfiles/ssh/config", filepath.Join(home, "dotfiles", "ssh", "config.bak"))
			symlinkAt(t, file, filepath.Join(home, ".ssh", "config"))
			return file
		}},
	}
	for _, moved := range movedLocations {
		if moved.file {
			continue
		}
		layouts = append(layouts, hardLinkLayout{name: "directory of " + moved.variable, layout: func(t *testing.T, home string) string {
			moved.create(t, home)
			moved.set(t, filepath.Join(home, moved.value))
			return linkedIntoProject(t, home, filepath.Join(moved.location, "secret"))
		}})
	}
	checkHardLinkLayouts(t, layouts)
}

// TestHandleCountsEachPathOfAHardLinkedFileOnce covers protected locations
// that overlap, and symlinks that name a file in another spelling: each path
// of a file counts once, so the link outside every protected location is
// not hidden by a path that was counted twice.
func TestHandleCountsEachPathOfAHardLinkedFileOnce(t *testing.T) {
	layouts := []hardLinkLayout{
		{name: "CLAUDE_CONFIG_DIR is .claude", layout: func(t *testing.T, home string) string {
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			return linkedOutside(t, home, ".claude/settings.json")
		}},
		{name: "XDG_CONFIG_HOME is .config", layout: func(t *testing.T, home string) string {
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			return linkedOutside(t, home, ".config/gh/hosts.yml")
		}},
		{name: "GH_CONFIG_DIR is a symlink to .config/gh", layout: func(t *testing.T, home string) string {
			file := linkedOutside(t, home, ".config/gh/hosts.yml")
			symlinkAt(t, filepath.Join(home, ".config", "gh"), filepath.Join(home, "gh"))
			t.Setenv("GH_CONFIG_DIR", filepath.Join(home, "gh"))
			return file
		}},
		{name: "GNUPGHOME inside .gnupg", layout: func(t *testing.T, home string) string {
			t.Setenv("GNUPGHOME", filepath.Join(home, ".gnupg", "sub"))
			return linkedOutside(t, home, ".gnupg/sub/key.key")
		}},
		{name: "symlink in .claude to a file in .claude", layout: func(t *testing.T, home string) string {
			file := linkedOutside(t, home, ".claude/settings.json")
			symlinkAt(t, file, filepath.Join(home, ".claude", "link.json"))
			return file
		}},
		{name: "two symlinks to one file outside", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh", ".claude")
			file := linkedOutside(t, home, "dotfiles/ssh/config")
			symlinkAt(t, file, filepath.Join(home, ".ssh", "config"))
			symlinkAt(t, file, filepath.Join(home, ".claude", "ssh-config"))
			return file
		}},
		{name: "symlink on the data volume to a file in .claude", layout: func(t *testing.T, home string) string {
			file := linkedOutside(t, home, ".claude/settings.json")
			requireSpelling(t, dataVolume+file)
			symlinkAt(t, dataVolume+file, filepath.Join(home, ".claude", "link.json"))
			return file
		}},
		{name: "symlink in upper case to a file in .claude", layout: func(t *testing.T, home string) string {
			file := linkedOutside(t, home, ".claude/settings.json")
			requireSpelling(t, strings.ToUpper(file))
			symlinkAt(t, strings.ToUpper(file), filepath.Join(home, ".claude", "link.json"))
			return file
		}},
		{name: "symlink in upper case to a file outside", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh", ".claude")
			file := linkedOutside(t, home, "dotfiles/ssh/config")
			requireSpelling(t, strings.ToUpper(file))
			symlinkAt(t, file, filepath.Join(home, ".ssh", "config"))
			symlinkAt(t, strings.ToUpper(file), filepath.Join(home, ".claude", "ssh-config"))
			return file
		}},
		{name: "symlinks to two names of one file outside", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			file := linkedOutside(t, home, "dotfiles/ssh/a")
			hardLink(t, file, filepath.Join(home, "dotfiles", "ssh", "b"))
			symlinkAt(t, file, filepath.Join(home, ".ssh", "a"))
			symlinkAt(t, filepath.Join(home, "dotfiles", "ssh", "b"), filepath.Join(home, ".ssh", "b"))
			return file
		}},
	}
	checkHardLinkLayouts(t, layouts)
}

// TestHandleStopsOnASymlinkToAnUnlistedNameOfSeveralLinks covers a symlink
// that ends at a name that its directory does not list, in another case,
// while that directory holds more than one link of the file: the handler
// cannot tell which path the symlink names, and counting it as a path of
// its own would hide the link in the project directory, so init stops with
// an error that names the directory and makes no OrbStack call.
func TestHandleStopsOnASymlinkToAnUnlistedNameOfSeveralLinks(t *testing.T) {
	home := newBareHome(t)
	claude := filepath.Join(home, ".claude")
	file := linkedIntoProject(t, home, ".claude/a")
	hardLink(t, file, filepath.Join(claude, "b"))
	requireSpelling(t, filepath.Join(claude, "A"))
	symlinkAt(t, filepath.Join(claude, "A"), filepath.Join(claude, "x"))
	fake := orbstacktest.New(sandbox.NamePrefix)

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

	want := "A names one of 2 hard links in " + claude + "; point the symlink at a name that the directory lists"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Handle = %v, want an error that contains %q", err, want)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

// TestHandleAcceptsHardLinksInsideTheProtectedLocations covers files whose
// every hard link lies in the protected locations, as Claude Code and GnuPG
// make them, a subdirectory, which always has more than one link, and a
// directory where a file that must have one link is expected.
func TestHandleAcceptsHardLinksInsideTheProtectedLocations(t *testing.T) {
	checkHardLinkLayouts(t, []hardLinkLayout{
		{name: "snapshots of Claude Code", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".claude/file-history/two")
			linkedCredential(t, home, ".claude/file-history/one/snapshot", filepath.Join(home, ".claude", "file-history", "two", "snapshot"))
			return ""
		}},
		{name: "lock pair of GnuPG", layout: func(t *testing.T, home string) string {
			linkedCredential(t, home, ".gnupg/public-keys.d/pubring.db.lock", filepath.Join(home, ".gnupg", "public-keys.d", ".#lk0x0000.host.123"))
			return ""
		}},
		{name: "link between .claude and .ssh", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			linkedCredential(t, home, ".claude/shared", filepath.Join(home, ".ssh", "shared"))
			return ""
		}},
		{name: "subdirectories", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".claude/projects/one/two", ".ssh/sockets")
			return ""
		}},
		{name: "single link", layout: func(t *testing.T, home string) string {
			unreadable(t, credentialFile(t, home, ".ssh/config"))
			return ""
		}},
		{name: "snapshots with CLAUDE_CONFIG_DIR as .claude", layout: func(t *testing.T, home string) string {
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			makeDirs(t, home, ".claude/file-history/two")
			linkedCredential(t, home, ".claude/file-history/one/snapshot", filepath.Join(home, ".claude", "file-history", "two", "snapshot"))
			return ""
		}},
		{name: "per-file symlink to a file with a link in .ssh", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh")
			file := linkedCredential(t, home, "dotfiles/ssh/config", filepath.Join(home, ".ssh", "config.bak"))
			symlinkAt(t, file, filepath.Join(home, ".ssh", "config"))
			return ""
		}},
		{name: "hard link of a file outside the protected locations", layout: func(t *testing.T, home string) string {
			linkedCredential(t, home, "dotfiles/vim/vimrc", filepath.Join(home, "work", "api", "vimrc"))
			return ""
		}},
		{name: "directory named .claude.json", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".claude.json/one", ".claude.json/two")
			return ""
		}},
		{name: "directory named by AWS_CONFIG_FILE", layout: func(t *testing.T, home string) string {
			makeDirs(t, home, "dotfiles/aws/config/one", "dotfiles/aws/config/two")
			t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "dotfiles", "aws", "config"))
			return ""
		}},
	})
}

// singleLinkFile is a file that the environment moves and that must have
// one hard link: the variable, its value, and the file, relative to home.
type singleLinkFile struct {
	variable string
	value    string
	file     string
}

// singleLinkFiles are the files of every variable that moves one.
var singleLinkFiles = []singleLinkFile{
	{variable: "AWS_CONFIG_FILE", value: "dotfiles/aws/config", file: "dotfiles/aws/config"},
	{variable: "AWS_SHARED_CREDENTIALS_FILE", value: "dotfiles/aws/credentials", file: "dotfiles/aws/credentials"},
	{variable: "CLAUDE_CONFIG_DIR", value: "dotfiles/claude", file: "dotfiles/claude/.claude.json"},
}

// TestHandleRefusesHardLinkedFilesThatTheEnvironmentMoves covers the files
// that the environment moves (#69): like .claude.json, each of them must
// have one hard link, also when the other link lies in a protected location.
func TestHandleRefusesHardLinkedFilesThatTheEnvironmentMoves(t *testing.T) {
	for _, moved := range singleLinkFiles {
		t.Run(moved.variable, func(t *testing.T) {
			checkHardLinkLayouts(t, []hardLinkLayout{
				{name: "other link in the project", layout: func(t *testing.T, home string) string {
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return linkedIntoProject(t, home, moved.file)
				}},
				{name: "other link outside every project", layout: func(t *testing.T, home string) string {
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return linkedOutside(t, home, moved.file)
				}},
				{name: "other link next to it", layout: func(t *testing.T, home string) string {
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return linkedCredential(t, home, moved.file, filepath.Join(home, moved.file+".bak"))
				}},
				{name: "other link of the target of a symlink", layout: func(t *testing.T, home string) string {
					file := linkedIntoProject(t, home, "store/"+filepath.Base(moved.file))
					makeDirs(t, home, filepath.Dir(moved.file))
					symlinkAt(t, file, filepath.Join(home, moved.file))
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return file
				}},
				{name: "single link", layout: func(t *testing.T, home string) string {
					unreadable(t, credentialFile(t, home, moved.file))
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return ""
				}},
				{name: "missing", layout: func(t *testing.T, home string) string {
					makeDirs(t, home, filepath.Dir(moved.file))
					t.Setenv(moved.variable, filepath.Join(home, moved.value))
					return ""
				}},
			})
		})
	}
}

// TestHandleRefusesEmptyVariablesOfFilesThatMustHaveOneLink covers a
// variable of a file that must have one hard link that is set and empty:
// init stops with ErrRelocatedPath before it asks OrbStack anything.
func TestHandleRefusesEmptyVariablesOfFilesThatMustHaveOneLink(t *testing.T) {
	for _, variable := range []string{"AWS_CONFIG_FILE", "CLAUDE_CONFIG_DIR"} {
		t.Run(variable, func(t *testing.T) {
			home := newBareHome(t)
			t.Setenv(variable, "")
			checkRefusals(t, account(home), []refusal{{name: "project", dir: filepath.Join(home, "work", "api"), want: sandbox.ErrRelocatedPath}})
		})
	}
}

// TestHandleStopsOnAProtectedDirectoryThatCannotBeRead covers a subdirectory
// of a protected directory that the walk cannot read: the handler cannot
// count the links of the files in it, so init stops.
func TestHandleStopsOnAProtectedDirectoryThatCannotBeRead(t *testing.T) {
	home := newBareHome(t)
	locked := filepath.Join(home, ".claude", "locked")
	makeDirs(t, home, ".claude/locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	fake := orbstacktest.New(sandbox.NamePrefix)

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "file in a protected directory") {
		t.Errorf("Handle = %v, want %v about a file in a protected directory", err, fs.ErrPermission)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

// TestHandleWithProtectedFilesThatLeadNowhere covers a .claude.json that is
// a broken symlink, which protects only the directory where its target would
// be created, and one that cannot be resolved, which stops init.
func TestHandleWithProtectedFilesThatLeadNowhere(t *testing.T) {
	t.Run("broken symlink", func(t *testing.T) {
		home := newDir(t)
		makeDirs(t, home, "work/api")
		symlinkAt(t, filepath.Join(home, "missing", "claude.json"), filepath.Join(home, ".claude.json"))
		t.Setenv("HOME", home)
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
	t.Run("symlink loop", func(t *testing.T) {
		home := newDir(t)
		makeDirs(t, home, "work/api")
		symlinkAt(t, ".claude.json", filepath.Join(home, ".claude.json"))
		t.Setenv("HOME", home)
		fake := orbstacktest.New(sandbox.NamePrefix)

		_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

		if err == nil || !strings.Contains(err.Error(), "protected file") {
			t.Errorf("Handle = %v, want an error about the protected file", err)
		}
		if len(fake.Calls) != 0 {
			t.Errorf("calls = %v, want none", fake.Calls)
		}
	})
}

// newBareHome makes a home directory with only work/api and dotfiles/vim in
// it, makes it the home directory of the environment of the test, and
// returns its canonical path.
func newBareHome(t *testing.T) string {
	t.Helper()
	home := newDir(t)
	makeDirs(t, home, "work/api", "dotfiles/vim")
	t.Setenv("HOME", home)
	return home
}

// linkLayout is a layout of symlinks in a bare home directory and the
// project directory that the handler must refuse for it.
type linkLayout struct {
	name   string
	layout func(t *testing.T, home string) (dir string)
	want   error
}

// checkLinkLayouts builds each layout in a new bare home directory and
// checks that the handler refuses its project directory and still accepts
// work/api and dotfiles/vim.
func checkLinkLayouts(t *testing.T, layouts []linkLayout) {
	t.Helper()
	for _, test := range layouts {
		t.Run(test.name, func(t *testing.T) {
			home := newBareHome(t)
			dir := test.layout(t, home)
			checkRefusals(t, account(home), []refusal{{name: "refuses", dir: dir, want: test.want}})
			for _, accepted := range []string{filepath.Join(home, "work", "api"), filepath.Join(home, "dotfiles", "vim")} {
				t.Run("accepts "+filepath.Base(accepted), func(t *testing.T) {
					checkCreates(t, account(home), accepted, accepted)
				})
			}
		})
	}
}

// TestHandleRefusesTargetsOfDanglingProtectedPaths covers every protected
// path as a symlink whose target does not exist yet. The tool that owns the
// path creates its credentials at that target, so the directory where the
// target would be created is protected (#27).
func TestHandleRefusesTargetsOfDanglingProtectedPaths(t *testing.T) {
	checkLinkLayouts(t, []linkLayout{
		{name: ".ssh", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, ".ssh"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".claude by a relative symlink", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join("dotfiles", "claude"), filepath.Join(home, ".claude"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".aws", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "aws"), filepath.Join(home, ".aws"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".gnupg", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "gnupg"), filepath.Join(home, ".gnupg"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".config/gh", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".config")
			symlinkAt(t, filepath.Join(home, "dotfiles", "gh"), filepath.Join(home, ".config", "gh"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".config to a missing directory", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(home, ".config"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".config to a missing directory by a relative symlink", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join("dotfiles", "config"), filepath.Join(home, ".config"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".ssh below a missing directory", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "keys", "ssh"), filepath.Join(home, ".ssh"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".ssh through a chain of dangling symlinks", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, "links")
			symlinkAt(t, filepath.Join(home, "links", "ssh"), filepath.Join(home, ".ssh"))
			symlinkAt(t, filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, "links", "ssh"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".ssh below a linked directory", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, "elsewhere")
			symlinkAt(t, filepath.Join(home, "elsewhere"), filepath.Join(home, "dotfiles", "linked"))
			symlinkAt(t, filepath.Join(home, "dotfiles", "linked", "ssh"), filepath.Join(home, ".ssh"))
			return filepath.Join(home, "elsewhere")
		}},
		{name: ".ssh in upper case", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, ".ssh"))
			requireSpelling(t, strings.ToUpper(filepath.Join(home, "dotfiles")))
			return strings.ToUpper(filepath.Join(home, "dotfiles"))
		}},
		{name: ".config on the data volume", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(home, ".config"))
			requireSpelling(t, dataVolume+filepath.Join(home, "dotfiles"))
			return dataVolume + filepath.Join(home, "dotfiles")
		}},
		{name: ".claude.json", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "claude.json"), filepath.Join(home, ".claude.json"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".claude.json below a missing directory", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles", "state", "claude.json"), filepath.Join(home, ".claude.json"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: ".claude.json through a chain of relative symlinks", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, "links")
			symlinkAt(t, filepath.Join("links", "claude.json"), filepath.Join(home, ".claude.json"))
			symlinkAt(t, filepath.Join("..", "dotfiles", "claude.json"), filepath.Join(home, "links", "claude.json"))
			return filepath.Join(home, "dotfiles")
		}},
	})
}

// TestHandleRefusesTargetsOfSymlinksInProtectedDirectories covers a
// protected directory that holds a symlink to a file or a directory
// elsewhere, as a per-file dotfiles layout makes it (#27).
func TestHandleRefusesTargetsOfSymlinksInProtectedDirectories(t *testing.T) {
	checkLinkLayouts(t, []linkLayout{
		{name: "file in .ssh", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".ssh/id_ed25519", "dotfiles/ssh/id_ed25519")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in .ssh, directory that holds the target", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".ssh/id_ed25519", "dotfiles/ssh/id_ed25519")
			return filepath.Join(home, "dotfiles", "ssh")
		}},
		{name: "file in .claude", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".claude/settings.json", "dotfiles/claude/settings.json")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in .aws", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".aws/credentials", "dotfiles/aws/credentials")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in .gnupg by a relative symlink", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".gnupg", "dotfiles/gnupg")
			writeFile(t, filepath.Join(home, "dotfiles", "gnupg", "gpg.conf"))
			symlinkAt(t, filepath.Join("..", "dotfiles", "gnupg", "gpg.conf"), filepath.Join(home, ".gnupg", "gpg.conf"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in .config/gh", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".config/gh/hosts.yml", "dotfiles/gh/hosts.yml")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in a subdirectory of .ssh", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".ssh/config.d/work", "dotfiles/ssh/work")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file through a chain of symlinks", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, "links/id_ed25519", "dotfiles/ssh/id_ed25519")
			makeDirs(t, home, ".ssh")
			symlinkAt(t, filepath.Join(home, "links", "id_ed25519"), filepath.Join(home, ".ssh", "id_ed25519"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file through a chain of symlinks, directory of the first hop", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, "links/id_ed25519", "dotfiles/ssh/id_ed25519")
			makeDirs(t, home, ".ssh")
			symlinkAt(t, filepath.Join(home, "links", "id_ed25519"), filepath.Join(home, ".ssh", "id_ed25519"))
			return filepath.Join(home, "links")
		}},
		{name: "file in .ssh through a symlink in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, "proj/config", "vault/config")
			makeDirs(t, home, ".ssh")
			symlinkAt(t, filepath.Join(home, "proj", "config"), filepath.Join(home, ".ssh", "config"))
			return filepath.Join(home, "proj")
		}},
		{name: "file in .ssh through a symlink in the project by a relative symlink", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, "proj/config", "vault/config")
			makeDirs(t, home, ".ssh")
			symlinkAt(t, filepath.Join("..", "proj", "config"), filepath.Join(home, ".ssh", "config"))
			return filepath.Join(home, "proj")
		}},
		{name: "directory in .claude through a symlink in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, "proj/cmds", "vault/cmds")
			makeDirs(t, home, ".claude")
			symlinkAt(t, filepath.Join(home, "proj", "cmds"), filepath.Join(home, ".claude", "commands"))
			return filepath.Join(home, "proj")
		}},
		{name: "dangling symlink in .ssh through a symlink in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh", "proj", "vault")
			symlinkAt(t, filepath.Join(home, "vault", "id_new"), filepath.Join(home, "proj", "id_new"))
			symlinkAt(t, filepath.Join(home, "proj", "id_new"), filepath.Join(home, ".ssh", "id_new"))
			return filepath.Join(home, "proj")
		}},
		{name: "file in .ssh through a linked directory in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, "proj/link", "vault")
			writeFile(t, filepath.Join(home, "vault", "config"))
			makeDirs(t, home, ".ssh")
			symlinkAt(t, filepath.Join(home, "proj", "link", "config"), filepath.Join(home, ".ssh", "config"))
			return filepath.Join(home, "proj")
		}},
		{name: "directory in .claude", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, ".claude/commands", "dotfiles/commands")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "directory in .claude, the target", want: sandbox.ErrProtectedDir, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, ".claude/commands", "dotfiles/commands")
			return filepath.Join(home, "dotfiles", "commands")
		}},
		{name: "file in a linked directory in .claude", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, ".claude/commands", "dotfiles/commands")
			linkFileInto(t, home, "dotfiles/commands/review.md", "vault/review.md")
			return filepath.Join(home, "vault")
		}},
		{name: "file in the target of a linked .ssh", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkDirInto(t, home, ".ssh", "keys")
			linkFileInto(t, home, "keys/id_ed25519", "vault/id_ed25519")
			return filepath.Join(home, "vault")
		}},
		{name: "directory in .claude that links back to .claude", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".claude")
			symlinkAt(t, filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "loop"))
			linkFileInto(t, home, ".claude/settings.json", "dotfiles/claude/settings.json")
			return filepath.Join(home, "dotfiles")
		}},
		{name: "dangling symlink in .ssh", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".ssh", "dotfiles/ssh")
			symlinkAt(t, filepath.Join(home, "dotfiles", "ssh", "id_new"), filepath.Join(home, ".ssh", "id_new"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "dangling symlink in .gnupg below a missing directory", want: sandbox.ErrProtectedAncestor, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, ".gnupg")
			symlinkAt(t, filepath.Join(home, "dotfiles", "gnupg", "pubring.kbx"), filepath.Join(home, ".gnupg", "pubring.kbx"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "file in .ssh, in upper case", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".ssh/id_ed25519", "dotfiles/ssh/id_ed25519")
			requireSpelling(t, strings.ToUpper(filepath.Join(home, "dotfiles")))
			return strings.ToUpper(filepath.Join(home, "dotfiles"))
		}},
		{name: "file in .aws, on the data volume", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, ".aws/credentials", "dotfiles/aws/credentials")
			requireSpelling(t, dataVolume+filepath.Join(home, "dotfiles"))
			return dataVolume + filepath.Join(home, "dotfiles")
		}},
	})
}

// TestHandleStopsOnSymlinksThatCannotBeFollowed covers a symlink in a
// protected directory that loops, and a dangling protected path whose target
// has ".." after a name, which a lexical join could resolve wrongly. The
// handler cannot tell what they protect, so init stops.
func TestHandleStopsOnSymlinksThatCannotBeFollowed(t *testing.T) {
	tests := []struct {
		name   string
		layout func(t *testing.T, home string)
		want   string
	}{
		{name: "symlink loop in .ssh", want: "file in a protected directory", layout: func(t *testing.T, home string) {
			makeDirs(t, home, ".ssh")
			symlinkAt(t, "loop", filepath.Join(home, ".ssh", "loop"))
		}},
		{name: "dangling .ssh with .. after a name", want: errUnresolvableLink.Error(), layout: func(t *testing.T, home string) {
			symlinkAt(t, "dotfiles/gone/../ssh", filepath.Join(home, ".ssh"))
		}},
		{name: "dangling .claude.json with .. after a name", want: errUnresolvableLink.Error(), layout: func(t *testing.T, home string) {
			symlinkAt(t, "dotfiles/gone/../claude.json", filepath.Join(home, ".claude.json"))
		}},
		{name: "dangling symlink in .ssh with .. after a name", want: errUnresolvableLink.Error(), layout: func(t *testing.T, home string) {
			makeDirs(t, home, ".ssh")
			symlinkAt(t, "../dotfiles/gone/../ssh/config", filepath.Join(home, ".ssh", "config"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := newBareHome(t)
			test.layout(t, home)
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("Handle = %v, want an error that contains %q", err, test.want)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

// TestHandleResolvesParentAfterANameInAProtectedDirectoryLink covers a
// symlink in a protected directory that resolves, with ".." after a name in
// its target (#43). init resolves ".." as the kernel does, as it does for a
// hop of a protected path: it refuses the directories that hold the file, a
// hop, or a name that a ".." cancels, and accepts every other project,
// instead of stopping for a symlink that it calls dangling. The agent could
// replace a cancelled name in a mounted directory with a symlink, and the
// kernel would apply the ".." to its target.
func TestHandleResolvesParentAfterANameInAProtectedDirectoryLink(t *testing.T) {
	tests := []struct {
		name string
		// build makes the link and returns the directories to refuse and
		// the ones to accept.
		build func(t *testing.T, home string) (refused []refusal, accepted []string)
	}{
		{name: "plain name before ..", build: func(t *testing.T, home string) ([]refusal, []string) {
			makeDirs(t, home, "dotfiles/x", "dotfiles/ssh")
			writeFile(t, filepath.Join(home, "dotfiles", "ssh", "config"))
			symlinkAt(t, "../dotfiles/x/../ssh/config", filepath.Join(home, ".ssh", "config"))
			return protectedFileRefusals(filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, "dotfiles")), []string{filepath.Join(home, "dotfiles", "x"), filepath.Join(home, "work", "api")}
		}},
		{name: "plain name in a project before ..", build: func(t *testing.T, home string) ([]refusal, []string) {
			makeDirs(t, home, "work/api/x", "dotfiles/ssh")
			writeFile(t, filepath.Join(home, "dotfiles", "ssh", "config"))
			symlinkAt(t, "../work/api/x/../../../dotfiles/ssh/config", filepath.Join(home, ".ssh", "config"))
			return protectedFileRefusals(filepath.Join(home, "work", "api"), filepath.Join(home, "work"), filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, "dotfiles")), []string{filepath.Join(home, "work", "api", "x"), filepath.Join(home, "dotfiles", "vim")}
		}},
		{name: "symlink before ..", build: func(t *testing.T, home string) ([]refusal, []string) {
			makeDirs(t, home, "dotfiles/ssh", "elsewhere/sub", "elsewhere/ssh")
			writeFile(t, filepath.Join(home, "dotfiles", "ssh", "config"))
			writeFile(t, filepath.Join(home, "elsewhere", "ssh", "config"))
			symlinkAt(t, filepath.Join(home, "elsewhere", "sub"), filepath.Join(home, "dotfiles", "x"))
			symlinkAt(t, "../dotfiles/x/../ssh/config", filepath.Join(home, ".ssh", "config"))
			return protectedFileRefusals(filepath.Join(home, "elsewhere", "ssh"), filepath.Join(home, "elsewhere"), filepath.Join(home, "dotfiles")), []string{filepath.Join(home, "dotfiles", "ssh"), filepath.Join(home, "work", "api")}
		}},
		{name: "directory link", build: func(t *testing.T, home string) ([]refusal, []string) {
			makeDirs(t, home, "dotfiles/x", "dotfiles/ssh/keys")
			writeFile(t, filepath.Join(home, "dotfiles", "ssh", "keys", "id"))
			symlinkAt(t, "../dotfiles/x/../ssh/keys", filepath.Join(home, ".ssh", "keys"))
			return []refusal{
				{name: "the linked directory", dir: filepath.Join(home, "dotfiles", "ssh", "keys"), want: sandbox.ErrProtectedDir},
				{name: "its parent", dir: filepath.Join(home, "dotfiles", "ssh"), want: sandbox.ErrProtectedAncestor},
				{name: "the directory that holds it", dir: filepath.Join(home, "dotfiles"), want: sandbox.ErrProtectedAncestor},
			}, []string{filepath.Join(home, "dotfiles", "x"), filepath.Join(home, "work", "api")}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := newBareHome(t)
			makeDirs(t, home, ".ssh")
			refused, accepted := test.build(t, home)

			checkRefusals(t, account(home), refused)
			for _, dir := range accepted {
				t.Run("accepts "+dir, func(t *testing.T) {
					checkCreates(t, account(home), dir, dir)
				})
			}
		})
	}
}

// protectedFileRefusals returns a refusal with ErrProtectedFile for each of
// dirs.
func protectedFileRefusals(dirs ...string) []refusal {
	var refusals []refusal
	for _, dir := range dirs {
		refusals = append(refusals, refusal{name: "refuses " + dir, dir: dir, want: sandbox.ErrProtectedFile})
	}
	return refusals
}

// TestHandleFollowsALongDanglingChainOnce covers a dangling .ssh that
// reaches its target through a long chain of relative symlinks. Each link has
// two spellings on macOS; the handler must follow it once, not once per
// spelling, or the work doubles with every hop and init hangs.
func TestHandleFollowsALongDanglingChainOnce(t *testing.T) {
	const hops = 24
	home := newBareHome(t)
	makeDirs(t, home, "links")
	symlinkAt(t, filepath.Join("links", "l0"), filepath.Join(home, ".ssh"))
	for i := range hops - 1 {
		symlinkAt(t, "l"+strconv.Itoa(i+1), filepath.Join(home, "links", "l"+strconv.Itoa(i)))
	}
	symlinkAt(t, filepath.Join("..", "dotfiles", "ssh"), filepath.Join(home, "links", "l"+strconv.Itoa(hops-1)))
	dir := filepath.Join(home, "dotfiles")
	result := make(chan error, 1)

	go func() {
		_, err := newHandler(orbstacktest.New(sandbox.NamePrefix), account(home)).Handle(context.Background(), Input{Dir: dir})
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, sandbox.ErrProtectedAncestor) {
			t.Errorf("Handle(%q) = %v, want %v", dir, err, sandbox.ErrProtectedAncestor)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Handle did not finish within 10s")
	}
}

// linkFileInto makes the empty file target and the symlink link to it, both
// relative to home, with their parent directories.
func linkFileInto(t *testing.T, home, link, target string) {
	t.Helper()
	makeDirs(t, home, filepath.Dir(link), filepath.Dir(target))
	writeFile(t, filepath.Join(home, target))
	symlinkAt(t, filepath.Join(home, target), filepath.Join(home, link))
}

// linkDirInto makes the directory target and the symlink link to it, both
// relative to home, with the parent directory of link.
func linkDirInto(t *testing.T, home, link, target string) {
	t.Helper()
	makeDirs(t, home, filepath.Dir(link), target)
	symlinkAt(t, filepath.Join(home, target), filepath.Join(home, link))
}

// TestHandleTrustsNoSingleHome covers an environment and an account database
// that name different home directories: both are home directories.
func TestHandleTrustsNoSingleHome(t *testing.T) {
	home := newHome(t)
	other := newDir(t)
	makeDirs(t, other, ".ssh", "work/api")
	project := filepath.Join(home, "work", "api")
	refusals := []refusal{
		{name: "home", dir: home, want: sandbox.ErrHomeDir},
		{name: ".ssh of home", dir: filepath.Join(home, ".ssh"), want: sandbox.ErrProtectedDir},
		{name: "child of .claude of home", dir: filepath.Join(home, ".claude", "projects"), want: sandbox.ErrProtectedDir},
		{name: "parent of home", dir: filepath.Dir(home), want: sandbox.ErrHomeAncestor},
		{name: "root", dir: "/", want: sandbox.ErrRootDir},
	}
	otherRefusals := []refusal{
		{name: "other home", dir: other, want: sandbox.ErrHomeDir},
		{name: ".ssh of the other home", dir: filepath.Join(other, ".ssh"), want: sandbox.ErrProtectedDir},
		{name: "parent of the other home", dir: filepath.Dir(other), want: sandbox.ErrHomeAncestor},
	}
	tests := []struct {
		name        string
		environment string
		account     string
		refusals    []refusal
	}{
		{name: "environment names another directory", environment: other, account: home, refusals: slices.Concat(refusals, otherRefusals)},
		{name: "environment names the root", environment: "/", account: home, refusals: refusals},
		{name: "environment names the project", environment: project, account: home, refusals: append(slices.Clone(refusals), refusal{name: "project", dir: project, want: sandbox.ErrHomeDir})},
		{name: "account database names another directory", environment: home, account: other, refusals: slices.Concat(refusals, otherRefusals)},
		{name: "account database names the root", environment: home, account: "/", refusals: refusals},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.environment)
			checkRefusals(t, account(test.account), test.refusals)
		})
	}
	t.Run("project is still accepted", func(t *testing.T) {
		t.Setenv("HOME", other)
		checkCreates(t, account(home), project, project)
	})
}

func TestHandleRefusesWithoutBothHomes(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	file := filepath.Join(home, ".ssh", "config")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lookupFailed := errors.New("no entry in the account database")
	found := func(entry *user.User) func() (*user.User, error) {
		return func() (*user.User, error) { return entry, nil }
	}
	tests := []struct {
		name        string
		environment string
		currentUser func() (*user.User, error)
		want        error
	}{
		{name: "environment home is not set", environment: "", currentUser: found(account(home))},
		{name: "environment home does not exist", environment: filepath.Join(home, "missing"), currentUser: found(account(home)), want: fs.ErrNotExist},
		{name: "environment home is a relative path that does not exist", environment: "missing", currentUser: found(account(home)), want: fs.ErrNotExist},
		{name: "environment home is a file", environment: file, currentUser: found(account(home)), want: syscall.ENOTDIR},
		{name: "account lookup fails", environment: home, currentUser: func() (*user.User, error) { return nil, lookupFailed }, want: lookupFailed},
		{name: "account home is empty", environment: home, currentUser: found(account("")), want: fs.ErrNotExist},
		{name: "account home does not exist", environment: home, currentUser: found(account(filepath.Join(home, "missing"))), want: fs.ErrNotExist},
		{name: "account home is a file", environment: home, currentUser: found(account(file)), want: syscall.ENOTDIR},
		{name: "account uid is empty", environment: home, currentUser: found(&user.User{HomeDir: home}), want: strconv.ErrSyntax},
		{name: "account uid is no number", environment: home, currentUser: found(&user.User{Uid: "S-1-5-21", HomeDir: home}), want: strconv.ErrSyntax},
		{name: "account uid is negative", environment: home, currentUser: found(&user.User{Uid: "-1", HomeDir: home}), want: strconv.ErrSyntax},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.environment)
			fake := orbstacktest.New(sandbox.NamePrefix)
			handler := Handler{Machines: fake, CurrentUser: test.currentUser}

			_, err := handler.Handle(context.Background(), Input{Dir: project})

			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Errorf("Handle = %v, want an error that wraps %v", err, test.want)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

func TestHandleRefusesDirectoriesOfOtherUsers(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	t.Run("project of another uid", func(t *testing.T) {
		other := &user.User{Uid: strconv.Itoa(os.Getuid() + 1), HomeDir: home}
		checkRefusals(t, other, []refusal{{name: "project", dir: project, want: sandbox.ErrNotOwner}})
	})
	t.Run("system directories", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root owns the system directories")
		}
		checkRefusals(t, account(home), []refusal{{name: "usr", dir: "/usr", want: sandbox.ErrNotOwner}})
	})
	t.Run("lookup directories of macOS", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("only macOS has these directories")
		}
		checkRefusals(t, account(home), []refusal{
			{name: "volume lookup", dir: "/.vol", want: sandbox.ErrNotOwner},
			{name: "no-follow lookup", dir: "/.nofollow", want: sandbox.ErrNotOwner},
			{name: "resolve lookup", dir: "/.resolve", want: sandbox.ErrNotOwner},
			{name: "system", dir: "/System/Library", want: sandbox.ErrNotOwner},
		})
	})
	t.Run("home through the no-follow lookup of macOS", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("only macOS has this lookup")
		}
		checkRefusals(t, account(home), []refusal{
			{name: "home", dir: "/.nofollow" + home, want: sandbox.ErrHomeDir},
			{name: ".ssh", dir: "/.nofollow" + filepath.Join(home, ".ssh"), want: sandbox.ErrProtectedDir},
		})
	})
}

// TestHandleReportsDirectoriesThatCannotBeMounted covers a directory that
// passes the mount rules and that OrbStack cannot mount at its own path.
func TestHandleReportsDirectoriesThatCannotBeMounted(t *testing.T) {
	home := newHome(t)
	for _, name := range []string{"a:b", ".ssh:", "a,b"} {
		t.Run(name, func(t *testing.T) {
			project := filepath.Join(home, "work", name)
			makeDirs(t, home, filepath.Join("work", name))
			fake := orbstacktest.New(sandbox.NamePrefix)

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

			if !errors.Is(err, orbstacktest.ErrMountPath) {
				t.Errorf("Handle(%q) = %v, want %v", project, err, orbstacktest.ErrMountPath)
			}
			if got != (Output{}) || len(fake.Machines) != 0 {
				t.Errorf("Handle(%q) = %+v with the machines %v, want no output and no machine", project, got, fake.Machines)
			}
		})
	}
}

func TestHandleReportsOrbStackFailures(t *testing.T) {
	boom := errors.New("orb: boom")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	tests := []struct {
		name            string
		mountsErr       error
		createErr       error
		activeMountsErr error
		wantCalls       []orbstacktest.Call
	}{
		{
			name:      "listing the machines fails",
			mountsErr: boom,
			wantCalls: []orbstacktest.Call{{Op: orbstacktest.OpMounts}},
		},
		{
			name:      "create fails",
			createErr: boom,
			wantCalls: []orbstacktest.Call{
				{Op: orbstacktest.OpMounts},
				{Op: orbstacktest.OpCreate, Name: name, Dir: project, Forward: true},
			},
		},
		{
			name:            "reading the mounts of the new machine fails",
			activeMountsErr: boom,
			wantCalls:       append(checkedCreateCalls(name, project), orbstacktest.Call{Op: orbstacktest.OpDelete, Name: name}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.MountsErr = test.mountsErr
			fake.CreateErr = test.createErr
			fake.ActiveMountsErr = test.activeMountsErr

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

			if !errors.Is(err, boom) {
				t.Errorf("Handle = %v, want %v", err, boom)
			}
			if got != (Output{}) {
				t.Errorf("Handle = %+v, want no output", got)
			}
			if !slices.Equal(fake.Calls, test.wantCalls) {
				t.Errorf("calls = %v, want %v", fake.Calls, test.wantCalls)
			}
			if len(fake.Machines) != 0 {
				t.Errorf("machines = %v, want none", fake.Machines)
			}
		})
	}
}

func TestHandleDeletesANewMachineWhenProvisioningFails(t *testing.T) {
	boom := errors.New("orb: script failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.RunAsRootErr = boom

	got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "provision sandbox "+name) {
		t.Errorf("Handle = %v, want the failed provisioning of %s", err, name)
	}
	if got != (Output{}) {
		t.Errorf("Handle = %+v, want no output", got)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("machines = %v, want the new machine deleted", fake.Machines)
	}
	wantCalls := slices.Concat(checkedCreateCalls(name, project), provisionCalls(name)[:1], []orbstacktest.Call{{Op: orbstacktest.OpDelete, Name: name}})
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleKeepsAnExistingMachineWhenProvisioningFails(t *testing.T) {
	boom := errors.New("orb: script failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.Forwarded[name] = true
	fake.RunAsRootErr = boom

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	if _, kept := fake.Machines[name]; !kept {
		t.Errorf("machines = %v, want the existing machine kept", fake.Machines)
	}
	if want := slices.Concat(checkedKeepCalls(name), provisionCalls(name)[:1]); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

func TestHandleRefusesAScriptThatCannotBeBuiltBeforeItAsksOrbStack(t *testing.T) {
	boom := errors.New("invalid provisioning input")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	fake := orbstacktest.New(sandbox.NamePrefix)
	handler := newHandler(fake, account(home))
	handler.ProvisionScript = func(uint32, []string, bool) ([]byte, error) { return nil, boom }

	_, err := handler.Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestHandleReportsBothErrorsWhenTheDeleteFails(t *testing.T) {
	scriptErr := errors.New("orb: script failed")
	deleteErr := errors.New("orb: delete failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.RunAsRootErr = scriptErr
	fake.DeleteErr = deleteErr

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, scriptErr) || !errors.Is(err, deleteErr) {
		t.Errorf("Handle = %v, want both %v and %v", err, scriptErr, deleteErr)
	}
	if err == nil || !strings.Contains(err.Error(), name) {
		t.Errorf("Handle = %v, want the machine name in the error", err)
	}
}

func TestHandleDeletesANewMachineAfterCancellation(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	fake := orbstacktest.New(sandbox.NamePrefix)
	ctx, cancel := context.WithCancel(context.Background())
	fake.RunAsRootErr = context.Canceled
	handler := newHandler(fake, account(home))
	machines := deleteRecorder{Fake: fake, cancel: cancel}
	handler.Machines = &machines

	_, err := handler.Handle(ctx, Input{Dir: project})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Handle = %v, want %v", err, context.Canceled)
	}
	if machines.deleteCtxErr != nil {
		t.Errorf("Delete got a cancelled context: %v", machines.deleteCtxErr)
	}
	if !machines.deleteHadDeadline {
		t.Error("Delete got a context without a deadline")
	}
}

// deleteRecorder cancels the context of the run when the script runs, and
// records whether Delete got a context that was still usable.
type deleteRecorder struct {
	*orbstacktest.Fake
	cancel            context.CancelFunc
	deleteCtxErr      error
	deleteHadDeadline bool
}

// RunAsRoot cancels the context of the run and then fails like the fake.
func (r *deleteRecorder) RunAsRoot(ctx context.Context, name string, script []byte) error {
	r.cancel()
	return r.Fake.RunAsRoot(ctx, name, script)
}

// Delete records the error of ctx and deletes like the fake.
func (r *deleteRecorder) Delete(ctx context.Context, name string) error {
	r.deleteCtxErr = ctx.Err()
	_, r.deleteHadDeadline = ctx.Deadline()
	return r.Fake.Delete(ctx, name)
}

func TestHandleReportsAFailedDeletion(t *testing.T) {
	boom := errors.New("orb: boom")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Active[name] = map[string]string{project: filepath.Join(home, ".ssh")}
	fake.DeleteErr = boom

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, sandbox.ErrMountMismatch) || !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want an error that wraps %v and %v", err, sandbox.ErrMountMismatch, boom)
	}
}

func TestHandleInstallsTheAgentAfterProvisioning(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)

	got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if want := (Output{Name: name, Dir: project, Created: true}); got != want {
		t.Errorf("Handle = %+v, want %+v", got, want)
	}
	wantCalls := append(checkedCreateCalls(name, project), agentCalls(name)...)
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleWithoutAgentAsksForNoAgent(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	handler := newHandler(fake, account(home))
	handler.Agent = func(name string) (sandbox.Agent, error) {
		t.Errorf("Agent(%q) called without an agent", name)
		return testAgent, nil
	}

	if _, err := handler.Handle(context.Background(), Input{Dir: project}); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if !slices.Equal(fake.Calls, createCalls(name, project)) {
		t.Errorf("calls = %v, want %v", fake.Calls, createCalls(name, project))
	}
}

func TestHandleRefusesAnUnknownAgentBeforeItAsksOrbStack(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	for _, agent := range []string{"codex", "Test-Agent", " test-agent", "test-agent\n", "../test-agent"} {
		t.Run(agent, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines[sandbox.MachineName(project)] = map[string]string{project: project}
			fake.Calls = nil

			_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, Agent: agent})

			if !errors.Is(err, errUnknownAgent) {
				t.Errorf("Handle(%q) = %v, want %v", agent, err, errUnknownAgent)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

// TestHandleRefusesAHostileAgentDomainBeforeItAsksOrbStack gives the
// provisioning script the domains of the agent unchanged, and stops at its
// refusal before the first call to OrbStack. provision.Script refuses such
// a domain with policy.ErrInvalidHost; cmd/orbit tests that composition.
func TestHandleRefusesAHostileAgentDomainBeforeItAsksOrbStack(t *testing.T) {
	refused := errors.New("invalid host name for an allow rule")
	hostile := sandbox.Agent{Install: testAgent.Install, EgressDomains: []string{"agent.example", "*.anthropic.com"}}
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	fake := orbstacktest.New(sandbox.NamePrefix)
	handler := newHandler(fake, account(home))
	handler.Agent = func(string) (sandbox.Agent, error) { return hostile, nil }
	var gotDomains []string
	handler.ProvisionScript = func(_ uint32, agentDomains []string, _ bool) ([]byte, error) {
		gotDomains = agentDomains
		return nil, refused
	}

	_, err := handler.Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, refused) {
		t.Errorf("Handle = %v, want %v", err, refused)
	}
	if !slices.Equal(gotDomains, hostile.EgressDomains) {
		t.Errorf("agent domains of the script = %q, want %q", gotDomains, hostile.EgressDomains)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("calls = %v, want none", fake.Calls)
	}
}

func TestHandleDeletesANewMachineWhenTheInstallFails(t *testing.T) {
	boom := errors.New("orb: install failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	failing := failingScript{Fake: fake, script: testInstallScript, err: boom}

	_, err := newHandlerOn(failing, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "was deleted") {
		t.Errorf("Handle = %v, want %v and the deletion in the text", err, boom)
	}
	if _, kept := fake.Machines[name]; kept {
		t.Errorf("machines = %v, want the new machine deleted", fake.Machines)
	}
	wantCalls := slices.Concat(checkedCreateCalls(name, project), agentCalls(name), []orbstacktest.Call{{Op: orbstacktest.OpDelete, Name: name}})
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleKeepsAnExistingMachineWhenTheInstallFails(t *testing.T) {
	boom := errors.New("orb: install failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.Forwarded[name] = true
	failing := failingScript{Fake: fake, script: testInstallScript, err: boom}

	_, err := newHandlerOn(failing, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	if _, kept := fake.Machines[name]; !kept {
		t.Errorf("machines = %v, want the existing machine kept", fake.Machines)
	}
	wantCalls := append(checkedKeepCalls(name), agentCalls(name)...)
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleInstallsNothingWhenProvisioningFails(t *testing.T) {
	boom := errors.New("orb: script failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.RunAsRootErr = boom

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	wantCalls := slices.Concat(checkedCreateCalls(name, project), agentCalls(name)[:1], []orbstacktest.Call{{Op: orbstacktest.OpDelete, Name: name}})
	if !slices.Equal(fake.Calls, wantCalls) {
		t.Errorf("calls = %v, want %v", fake.Calls, wantCalls)
	}
}

func TestHandleWithoutAgentDropsTheDomainsOfAnEarlierAgent(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	handler := newHandler(fake, account(home))
	if _, err := handler.Handle(context.Background(), Input{Dir: project, Agent: testAgentName}); err != nil {
		t.Fatalf("Handle with the agent = %v", err)
	}
	fake.Calls = nil

	got, err := handler.Handle(context.Background(), Input{Dir: project})

	if err != nil || got.Created {
		t.Fatalf("Handle without the agent = %+v, %v, want the existing machine", got, err)
	}
	if !slices.Equal(fake.Calls, keepCalls(name)) {
		t.Errorf("calls = %v, want %v: the base list and no install", fake.Calls, keepCalls(name))
	}
}

func TestInstallScriptIsOneGroupWithThePrelude(t *testing.T) {
	install := "curl -fsSL https://agent.example/install.sh | bash\necho 'it is $HOME' \"$(id -u)\""
	lines := strings.Split(string(installScript(install)), "\n")
	want := slices.Concat([]string{"{", "set -euo pipefail", "set -a", ". /etc/environment", "set +a"}, strings.Split(install, "\n"), []string{"}", ""})
	if !slices.Equal(lines, want) {
		t.Errorf("installScript = %q, want %q", lines, want)
	}
}

// TestTruncatedInstallScriptFailsToParse feeds bash every non-empty prefix
// of an install script that lacks at least its last line. Without the group,
// bash runs a prefix up to the cut and exits 0 when the cut falls between
// two commands, or inside the URL of the installer.
func TestTruncatedInstallScriptFailsToParse(t *testing.T) {
	script := installScript("echo installed")
	lastLine := strings.LastIndex(strings.TrimSuffix(string(script), "\n"), "\n") + 1
	for cut := 1; cut <= lastLine; cut++ {
		command := exec.Command("bash", "-s")
		command.Stdin = strings.NewReader(string(script[:cut]))
		output, err := command.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("bash with the script cut to %q = %v (%s), want the syntax error exit status 2", script[:cut], err, output)
		}
	}
}

// movedLocation is a protected location that an environment variable moves,
// in a layout of the tests: the value of the variable and the location that
// it names, both relative to the home directory and inside dotfiles, and
// whether the location is a file.
type movedLocation struct {
	variable string
	value    string
	location string
	file     bool
}

// movedLocations are the locations of every variable that moves one.
var movedLocations = []movedLocation{
	{variable: "CLAUDE_CONFIG_DIR", value: "dotfiles/claude", location: "dotfiles/claude"},
	{variable: "GH_CONFIG_DIR", value: "dotfiles/gh", location: "dotfiles/gh"},
	{variable: "GNUPGHOME", value: "dotfiles/gnupg", location: "dotfiles/gnupg"},
	{variable: "XDG_CONFIG_HOME", value: "dotfiles/config", location: "dotfiles/config/gh"},
	{variable: "AWS_CONFIG_FILE", value: "dotfiles/aws/config", location: "dotfiles/aws/config", file: true},
	{variable: "AWS_SHARED_CREDENTIALS_FILE", value: "dotfiles/aws/credentials", location: "dotfiles/aws/credentials", file: true},
}

// create makes the location in home.
func (m movedLocation) create(t *testing.T, home string) {
	t.Helper()
	m.createIn(t, home, "dotfiles")
}

// createIn makes the location in the directory dir of home instead of in
// dotfiles.
func (m movedLocation) createIn(t *testing.T, home, dir string) {
	t.Helper()
	location := filepath.Join(dir, inDotfiles(m.location))
	if !m.file {
		makeDirs(t, home, location)
		return
	}
	makeDirs(t, home, filepath.Dir(location))
	writeFile(t, filepath.Join(home, location))
}

// set sets the variable to path.
func (m movedLocation) set(t *testing.T, path string) {
	t.Setenv(m.variable, path)
}

// inDotfiles returns path, which is relative to home and inside dotfiles,
// relative to dotfiles.
func inDotfiles(path string) string {
	return strings.TrimPrefix(path, "dotfiles/")
}

// containerErr is the error for a project directory that contains the
// location.
func (m movedLocation) containerErr() error {
	if m.file {
		return sandbox.ErrProtectedFile
	}
	return sandbox.ErrProtectedAncestor
}

// layouts returns the layouts in which the handler must refuse a project
// directory because of the location.
func (m movedLocation) layouts() []linkLayout {
	layouts := []linkLayout{
		{name: "location inside the project", want: m.containerErr(), layout: func(t *testing.T, home string) string {
			m.create(t, home)
			m.set(t, filepath.Join(home, m.value))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "symlink to a location inside the project", want: m.containerErr(), layout: func(t *testing.T, home string) string {
			m.create(t, home)
			symlinkAt(t, filepath.Join(home, m.value), filepath.Join(home, "linked"))
			m.set(t, filepath.Join(home, "linked"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "dangling symlink to a location inside the project", want: m.containerErr(), layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, m.value), filepath.Join(home, "linked"))
			m.set(t, filepath.Join(home, "linked"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "location through a symlink in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			m.createIn(t, home, "store")
			symlinkAt(t, filepath.Join(home, "store", inDotfiles(m.value)), filepath.Join(home, "dotfiles", "cred"))
			m.set(t, filepath.Join(home, "dotfiles", "cred"))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "location through a symlinked directory in the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			m.createIn(t, home, "store")
			symlinkAt(t, filepath.Join(home, "store"), filepath.Join(home, "dotfiles", "sub"))
			m.set(t, filepath.Join(home, "dotfiles", "sub", inDotfiles(m.value)))
			return filepath.Join(home, "dotfiles")
		}},
		{name: "missing location under a symlinked parent", want: m.containerErr(), layout: func(t *testing.T, home string) string {
			symlinkAt(t, filepath.Join(home, "dotfiles"), filepath.Join(home, "links"))
			m.set(t, filepath.Join(home, "links", inDotfiles(m.value)))
			return filepath.Join(home, "dotfiles")
		}},
	}
	if m.file {
		return append(layouts, linkLayout{name: "project that holds the location", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			m.create(t, home)
			m.set(t, filepath.Join(home, m.value))
			return filepath.Dir(filepath.Join(home, m.location))
		}})
	}
	return append(layouts,
		linkLayout{name: "location equal to the project", want: sandbox.ErrProtectedDir, layout: func(t *testing.T, home string) string {
			m.create(t, home)
			m.set(t, filepath.Join(home, m.value))
			return filepath.Join(home, m.location)
		}},
		linkLayout{name: "project inside the location", want: sandbox.ErrProtectedDir, layout: func(t *testing.T, home string) string {
			makeDirs(t, home, filepath.Join(m.location, "projects"))
			m.set(t, filepath.Join(home, m.value))
			return filepath.Join(home, m.location, "projects")
		}},
		linkLayout{name: "symlink in a location outside the project", want: sandbox.ErrProtectedFile, layout: func(t *testing.T, home string) string {
			linkFileInto(t, home, filepath.Join("store", inDotfiles(m.location), "secret"), "dotfiles/secret")
			m.set(t, filepath.Join(home, "store", inDotfiles(m.value)))
			return filepath.Join(home, "dotfiles")
		}},
	)
}

// TestHandleRefusesLocationsThatTheEnvironmentMoves covers every variable
// that moves a protected location into a project directory (#40). The
// project directories outside the location stay accepted.
func TestHandleRefusesLocationsThatTheEnvironmentMoves(t *testing.T) {
	for _, moved := range movedLocations {
		t.Run(moved.variable, func(t *testing.T) {
			checkLinkLayouts(t, moved.layouts())
		})
	}
}

// TestHandleKeepsTheDefaultLocationsWhenTheEnvironmentMovesThem covers a
// home directory whose default locations still exist while every variable
// names a location elsewhere: both stay protected.
func TestHandleKeepsTheDefaultLocationsWhenTheEnvironmentMovesThem(t *testing.T) {
	home := newBareHome(t)
	makeDirs(t, home, ".claude", ".config/gh", ".gnupg", ".aws")
	for _, moved := range movedLocations {
		moved.create(t, home)
		moved.set(t, filepath.Join(home, moved.value))
	}

	checkRefusals(t, account(home), []refusal{
		{name: ".claude", dir: filepath.Join(home, ".claude"), want: sandbox.ErrProtectedDir},
		{name: ".config/gh", dir: filepath.Join(home, ".config", "gh"), want: sandbox.ErrProtectedDir},
		{name: ".gnupg", dir: filepath.Join(home, ".gnupg"), want: sandbox.ErrProtectedDir},
		{name: ".aws", dir: filepath.Join(home, ".aws"), want: sandbox.ErrProtectedDir},
		{name: "dotfiles", dir: filepath.Join(home, "dotfiles"), want: sandbox.ErrProtectedAncestor},
	})
	t.Run("project", func(t *testing.T) {
		checkCreates(t, account(home), filepath.Join(home, "work", "api"), filepath.Join(home, "work", "api"))
	})
}

// TestHandleFailsClosedOnLocationsThatCannotBeProtected covers a variable
// whose value names no absolute path, or a location that cannot be
// resolved: init stops before it asks OrbStack anything and names the
// variable.
func TestHandleFailsClosedOnLocationsThatCannotBeProtected(t *testing.T) {
	tests := []struct {
		name  string
		value func(t *testing.T, home string) string
		want  string
	}{
		{name: "empty", want: sandbox.ErrRelocatedPath.Error(), value: func(*testing.T, string) string { return "" }},
		{name: "relative", want: sandbox.ErrRelocatedPath.Error(), value: func(*testing.T, string) string { return "dotfiles/vim" }},
		{name: "parent after a name", want: sandbox.ErrRelocatedPath.Error(), value: func(t *testing.T, home string) string {
			return filepath.Join(home, "dotfiles") + "/../work"
		}},
		{name: "symlink loop", want: "protected location of", value: func(t *testing.T, home string) string {
			symlinkAt(t, "loop", filepath.Join(home, "loop"))
			return filepath.Join(home, "loop")
		}},
		{name: "dangling symlink with .. after a name", want: errUnresolvableLink.Error(), value: func(t *testing.T, home string) string {
			symlinkAt(t, "dotfiles/gone/../store", filepath.Join(home, "linked"))
			return filepath.Join(home, "linked")
		}},
	}
	for _, moved := range movedLocations {
		for _, test := range tests {
			t.Run(moved.variable+" "+test.name, func(t *testing.T) {
				home := newBareHome(t)
				moved.set(t, test.value(t, home))
				fake := orbstacktest.New(sandbox.NamePrefix)

				_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

				if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), moved.variable) {
					t.Errorf("Handle = %v, want an error that contains %q and %s", err, test.want, moved.variable)
				}
				if len(fake.Calls) != 0 {
					t.Errorf("calls = %v, want none", fake.Calls)
				}
			})
		}
	}
}

// TestHandleFailsClosedOnADirectoryVariableThatNamesAFile covers a variable
// that moves a protected directory to a path that is a file: the handler
// cannot protect what the tool would put there, so init stops and names the
// variable.
func TestHandleFailsClosedOnADirectoryVariableThatNamesAFile(t *testing.T) {
	for _, moved := range movedLocations {
		if moved.file {
			continue
		}
		t.Run(moved.variable, func(t *testing.T) {
			home := newBareHome(t)
			writeFile(t, filepath.Join(home, "dotfiles", "plain"))
			moved.set(t, filepath.Join(home, "dotfiles", "plain"))
			fake := orbstacktest.New(sandbox.NamePrefix)

			_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: filepath.Join(home, "work", "api")})

			if err == nil || !strings.Contains(err.Error(), "protected location of "+moved.variable) {
				t.Errorf("Handle = %v, want an error that names %s", err, moved.variable)
			}
			if len(fake.Calls) != 0 {
				t.Errorf("calls = %v, want none", fake.Calls)
			}
		})
	}
}

func TestHandleCreatesWithoutForwardingWhenAsked(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)

	got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, NoSSHForwarding: true})

	if err != nil || got != (Output{Name: name, Dir: project, Created: true}) {
		t.Fatalf("Handle = %+v, %v, want the created sandbox", got, err)
	}
	want := []orbstacktest.Call{
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpCreate, Name: name, Dir: project, Forward: false},
		{Op: orbstacktest.OpSSHAgentForwarded, Name: name},
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpActiveMounts, Name: name},
		{Op: orbstacktest.OpRunAsRoot, Name: name, Script: scriptFor(uint32(os.Getuid()), nil, false)},
		{Op: orbstacktest.OpRunAsAgent, Name: name, Script: testDockerSetupScript},
	}
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

func TestHandleProvisionsAnExistingMachineThatMatchesTheFlag(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, NoSSHForwarding: true})

	if err != nil {
		t.Fatalf("Handle = %v", err)
	}
	want := append(checkedKeepCalls(name),
		orbstacktest.Call{Op: orbstacktest.OpRunAsRoot, Name: name, Script: scriptFor(uint32(os.Getuid()), nil, false)},
		orbstacktest.Call{Op: orbstacktest.OpRunAsAgent, Name: name, Script: testDockerSetupScript})
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
}

func TestHandleRefusesAnExistingMachineWhoseForwardingDiffers(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	tests := []struct {
		name      string
		forwarded bool
		input     Input
	}{
		{name: "flag against a machine that forwards", forwarded: true, input: Input{Dir: project, NoSSHForwarding: true}},
		{name: "no flag against a machine that does not forward", forwarded: false, input: Input{Dir: project}},
		{name: "no flag with an agent against a machine that does not forward", forwarded: false, input: Input{Dir: project, Agent: testAgentName}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines[name] = map[string]string{project: project}
			fake.Forwarded[name] = test.forwarded

			got, err := newHandler(fake, account(home)).Handle(context.Background(), test.input)

			if !errors.Is(err, ErrSSHForwardingMismatch) || got != (Output{}) {
				t.Errorf("Handle = %+v, %v, want %v", got, err, ErrSSHForwardingMismatch)
			}
			if err != nil && !strings.Contains(err.Error(), "--no-ssh-forwarding") {
				t.Errorf("error %q does not name --no-ssh-forwarding", err)
			}
			want := []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}}
			if !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want only %v: no command in the machine, no start, no delete", fake.Calls, want)
			}
			if _, kept := fake.Machines[name]; !kept {
				t.Error("the existing machine was deleted")
			}
		})
	}
}

func TestHandleDeletesACreatedMachineThatReportsAnotherForwarding(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	other := false
	fake.CreatedForwarded = &other

	got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, ErrSSHForwardingMismatch) || got != (Output{}) {
		t.Errorf("Handle = %+v, %v, want %v", got, err, ErrSSHForwardingMismatch)
	}
	want := []orbstacktest.Call{
		{Op: orbstacktest.OpMounts},
		{Op: orbstacktest.OpCreate, Name: name, Dir: project, Forward: true},
		{Op: orbstacktest.OpSSHAgentForwarded, Name: name},
		{Op: orbstacktest.OpDelete, Name: name},
	}
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
	if len(fake.Machines) != 0 {
		t.Errorf("machines = %v, want the created machine deleted", fake.Machines)
	}
}

func TestHandleStopsWhenTheForwardingCannotBeRead(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	boom := errors.New("orb list has no forward_ssh_agent")
	t.Run("existing machine is kept and untouched", func(t *testing.T) {
		fake := orbstacktest.New(sandbox.NamePrefix)
		fake.Machines[name] = map[string]string{project: project}
		fake.SSHAgentForwardedErr = boom

		_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

		if !errors.Is(err, boom) {
			t.Errorf("Handle = %v, want %v", err, boom)
		}
		want := []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}, {Op: orbstacktest.OpSSHAgentForwarded, Name: name}}
		if !slices.Equal(fake.Calls, want) {
			t.Errorf("calls = %v, want %v", fake.Calls, want)
		}
		if _, kept := fake.Machines[name]; !kept {
			t.Error("the existing machine was deleted")
		}
	})
	t.Run("created machine is deleted", func(t *testing.T) {
		fake := orbstacktest.New(sandbox.NamePrefix)
		fake.SSHAgentForwardedErr = boom

		_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

		if !errors.Is(err, boom) {
			t.Errorf("Handle = %v, want %v", err, boom)
		}
		if len(fake.Machines) != 0 {
			t.Errorf("machines = %v, want the created machine deleted", fake.Machines)
		}
		for _, call := range fake.Calls {
			if call.Op == orbstacktest.OpRunAsRoot || call.Op == orbstacktest.OpActiveMounts {
				t.Errorf("call %v ran in the machine before the check passed", call)
			}
		}
	})
}

func TestHandleSetsUpTheDockerClientAfterProvisioningAndBeforeTheInstall(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)

	if _, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName}); err != nil {
		t.Fatalf("Handle = %v", err)
	}

	want := slices.Concat(checkedCreateCalls(name, project), agentCalls(name))
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v", fake.Calls, want)
	}
	if fake.Calls[len(fake.Calls)-2].Script != testDockerSetupScript {
		t.Error("the client setup does not come right before the install")
	}
}

func TestHandleDeletesANewMachineWhenTheClientSetupFails(t *testing.T) {
	boom := errors.New("orb: setup failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	failing := failingScript{Fake: fake, script: testDockerSetupScript, err: boom}

	_, err := newHandlerOn(failing, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "set up the Docker client in sandbox "+name) || !strings.Contains(err.Error(), "was deleted") {
		t.Errorf("Handle = %v, want the failed client setup and the deletion", err)
	}
	want := slices.Concat(checkedCreateCalls(name, project), agentCalls(name)[:2], []orbstacktest.Call{{Op: orbstacktest.OpDelete, Name: name}})
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v (no install)", fake.Calls, want)
	}
}

func TestHandleKeepsAnExistingMachineWhenTheClientSetupFails(t *testing.T) {
	boom := errors.New("orb: setup failed")
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.Forwarded[name] = true
	failing := failingScript{Fake: fake, script: testDockerSetupScript, err: boom}

	_, err := newHandlerOn(failing, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	if _, kept := fake.Machines[name]; !kept {
		t.Errorf("machines = %v, want the existing machine kept", fake.Machines)
	}
	if want := slices.Concat(checkedKeepCalls(name), agentCalls(name)[:2]); !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want %v (no install)", fake.Calls, want)
	}
}

func TestHandleRefusesAnExistingMachineThatIsNotRunning(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	tests := []struct {
		name  string
		state string
	}{
		{name: "stopped", state: orbstacktest.StateStopped},
		{name: "starting", state: "starting"},
		{name: "unknown state", state: "paused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := orbstacktest.New(sandbox.NamePrefix)
			fake.Machines[name] = map[string]string{project: project}
			fake.Forwarded[name] = true
			fake.States[name] = test.state

			got, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

			if !errors.Is(err, ErrNotRunning) || got != (Output{}) {
				t.Fatalf("Handle = %+v, %v, want %v", got, err, ErrNotRunning)
			}
			if !strings.Contains(err.Error(), "orbit start") {
				t.Errorf("error %q does not name orbit start", err)
			}
			want := []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}}
			if !slices.Equal(fake.Calls, want) {
				t.Errorf("calls = %v, want only %v: no command in the machine, no start, no delete", fake.Calls, want)
			}
			if state := fake.States[name]; state != test.state {
				t.Errorf("state = %q, want %q unchanged", state, test.state)
			}
			if _, kept := fake.Machines[name]; !kept {
				t.Error("the existing machine was deleted")
			}
		})
	}
}

func TestHandleStopsWhenTheStateCannotBeRead(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	boom := errors.New("orb list failed")
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.StateErr = boom

	_, err := newHandler(fake, account(home)).Handle(context.Background(), Input{Dir: project})

	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want %v", err, boom)
	}
	want := []orbstacktest.Call{{Op: orbstacktest.OpMounts}, {Op: orbstacktest.OpState, Name: name}}
	if !slices.Equal(fake.Calls, want) {
		t.Errorf("calls = %v, want only %v", fake.Calls, want)
	}
}

// vanishingMachine is a fake that deletes a machine when init lists the
// mounts, so that the machine is gone when init reads its state.
type vanishingMachine struct {
	*orbstacktest.Fake
	name string
}

func (v vanishingMachine) Mounts(ctx context.Context) (map[string]map[string]string, error) {
	mounts, err := v.Fake.Mounts(ctx)
	delete(v.Machines, v.name)
	return mounts, err
}

func TestHandleNamesAMachineThatVanishesBeforeItsStateIsRead(t *testing.T) {
	home := newHome(t)
	project := filepath.Join(home, "work", "api")
	name := sandbox.MachineName(project)
	fake := orbstacktest.New(sandbox.NamePrefix)
	fake.Machines[name] = map[string]string{project: project}
	fake.Forwarded[name] = true

	got, err := newHandlerOn(vanishingMachine{Fake: fake, name: name}, account(home)).Handle(context.Background(), Input{Dir: project, Agent: testAgentName})

	if !errors.Is(err, ErrSandboxGone) || errors.Is(err, ErrNotRunning) || got != (Output{}) {
		t.Fatalf("Handle = %+v, %v, want %v and not %v", got, err, ErrSandboxGone, ErrNotRunning)
	}
	if strings.Contains(err.Error(), `""`) || strings.Contains(err.Error(), "orbit start") || !strings.Contains(err.Error(), "orbit init") {
		t.Errorf("error %q must not say is \"\" or point to orbit start, and must point to orbit init", err)
	}
}

// hopLayout builds one layout in which a protected path reaches its target
// through a symlink that lies in the project directory proj.
type hopLayout struct {
	name  string
	build func(t *testing.T, home, proj, outside string)
}

// TestHandleRefusesAProjectThatHoldsAHopOfAProtectedPath covers a protected
// path whose chain passes a symlink inside a project directory (#36): the
// agent could repoint that hop at a directory that it controls.
func TestHandleRefusesAProjectThatHoldsAHopOfAProtectedPath(t *testing.T) {
	layouts := []hopLayout{
		{name: ".aws through proj/link", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, outside, "vault/aws")
			symlinkAt(t, filepath.Join(outside, "vault", "aws"), filepath.Join(proj, "link"))
			symlinkAt(t, filepath.Join(proj, "link"), filepath.Join(home, ".aws"))
		}},
		{name: ".ssh through a relative hop", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, outside, "vault/ssh")
			rel, err := filepath.Rel(proj, filepath.Join(outside, "vault", "ssh"))
			if err != nil {
				t.Fatal(err)
			}
			symlinkAt(t, rel, filepath.Join(proj, "link"))
			symlinkAt(t, filepath.Join("proj", "link"), filepath.Join(home, ".ssh"))
		}},
		{name: ".claude through proj/link", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, outside, "vault/claude")
			symlinkAt(t, filepath.Join(outside, "vault", "claude"), filepath.Join(proj, "link"))
			symlinkAt(t, filepath.Join(proj, "link"), filepath.Join(home, ".claude"))
		}},
		{name: ".config through proj/cfg", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, home, "dotfiles/config/gh")
			symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(proj, "cfg"))
			symlinkAt(t, filepath.Join(proj, "cfg"), filepath.Join(home, ".config"))
		}},
		{name: ".config through proj/cfg, gh missing", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, home, "dotfiles/config")
			symlinkAt(t, filepath.Join(home, "dotfiles", "config"), filepath.Join(proj, "cfg"))
			symlinkAt(t, filepath.Join(proj, "cfg"), filepath.Join(home, ".config"))
		}},
		{name: ".claude.json through proj/c.json", build: func(t *testing.T, home, proj, outside string) {
			makeDirs(t, outside, "store/state")
			writeFile(t, filepath.Join(outside, "store", "state", "claude.json"))
			symlinkAt(t, filepath.Join(outside, "store", "state", "claude.json"), filepath.Join(proj, "c.json"))
			symlinkAt(t, filepath.Join(proj, "c.json"), filepath.Join(home, ".claude.json"))
		}},
		{name: ".gnupg through a hop that dangles", build: func(t *testing.T, home, proj, outside string) {
			symlinkAt(t, filepath.Join(outside, "missing"), filepath.Join(proj, "link"))
			symlinkAt(t, filepath.Join(proj, "link"), filepath.Join(home, ".gnupg"))
		}},
	}
	spellings := []struct {
		name  string
		spell func(path string) string
	}{
		{name: "canonical", spell: func(path string) string { return path }},
		{name: "upper case", spell: strings.ToUpper},
		{name: "data volume", spell: func(path string) string { return dataVolume + path }},
	}
	for _, layout := range layouts {
		for _, spelling := range spellings {
			t.Run(layout.name+", "+spelling.name, func(t *testing.T) {
				home := newDir(t)
				outside := newDir(t)
				makeDirs(t, home, "proj/sub", "work/api")
				t.Setenv("HOME", home)
				proj := filepath.Join(home, "proj")
				layout.build(t, home, proj, outside)
				requireSpelling(t, spelling.spell(proj))

				checkRefusals(t, account(home), []refusal{
					{name: "directory that holds the hop", dir: spelling.spell(proj), want: sandbox.ErrProtectedFile},
				})
			})
		}
	}
}

// TestHandleRefusesTheAncestorsOfAHopOfAProtectedPath covers the directories
// that contain the directory that holds a hop, and accepts the directories
// that do not hold it.
func TestHandleRefusesTheAncestorsOfAHopOfAProtectedPath(t *testing.T) {
	home := newDir(t)
	outside := newDir(t)
	makeDirs(t, home, "work/proj/sub", "work/other", "vault")
	makeDirs(t, outside, "vault/aws")
	symlinkAt(t, filepath.Join(outside, "vault", "aws"), filepath.Join(home, "work", "proj", "link"))
	symlinkAt(t, filepath.Join(home, "work", "proj", "link"), filepath.Join(home, ".aws"))
	t.Setenv("HOME", home)

	checkRefusals(t, account(home), []refusal{
		{name: "directory that holds the hop", dir: filepath.Join(home, "work", "proj"), want: sandbox.ErrProtectedFile},
		{name: "ancestor of the directory that holds the hop", dir: filepath.Join(home, "work"), want: sandbox.ErrProtectedFile},
	})
	for _, dir := range []string{
		filepath.Join(home, "work", "proj", "sub"),
		filepath.Join(home, "work", "other"),
		filepath.Join(home, "vault"),
	} {
		t.Run("accepts "+dir, func(t *testing.T) {
			checkCreates(t, account(home), dir, dir)
		})
	}
}

// TestHandleRefusesAProjectThatHoldsAHopOfTheHome covers a home directory
// that the environment or the account database names through a symlink in a
// project directory (#36): every protected path lies on its chain, so the
// agent could repoint the home of the host tools at a directory that it
// controls.
func TestHandleRefusesAProjectThatHoldsAHopOfTheHome(t *testing.T) {
	layouts := []struct {
		name string
		// build makes the hop in proj to home and returns the home of the
		// environment and the home of the account database.
		build func(t *testing.T, home, proj string) (string, string)
	}{
		{name: "HOME through proj/h", build: func(t *testing.T, home, proj string) (string, string) {
			symlinkAt(t, home, filepath.Join(proj, "h"))
			return filepath.Join(proj, "h"), home
		}},
		{name: "account home through proj/h", build: func(t *testing.T, home, proj string) (string, string) {
			symlinkAt(t, home, filepath.Join(proj, "h"))
			return home, filepath.Join(proj, "h")
		}},
		{name: "HOME through a relative proj/h", build: func(t *testing.T, home, proj string) (string, string) {
			rel, err := filepath.Rel(proj, home)
			if err != nil {
				t.Fatal(err)
			}
			symlinkAt(t, rel, filepath.Join(proj, "h"))
			return filepath.Join(proj, "h"), home
		}},
		{name: "HOME with .. after proj/h", build: func(t *testing.T, home, proj string) (string, string) {
			makeDirs(t, home, "sub")
			symlinkAt(t, filepath.Join(home, "sub"), filepath.Join(proj, "h"))
			return proj + "/h/..", home
		}},
	}
	places := []struct {
		name string
		proj func(home, outside string) string
	}{
		{name: "project outside the home", proj: func(_, outside string) string { return filepath.Join(outside, "proj") }},
		{name: "project in the home", proj: func(home, _ string) string { return filepath.Join(home, "proj") }},
	}
	spellings := []struct {
		name  string
		spell func(path string) string
	}{
		{name: "canonical", spell: func(path string) string { return path }},
		{name: "upper case", spell: strings.ToUpper},
		{name: "data volume", spell: func(path string) string { return dataVolume + path }},
	}
	for _, layout := range layouts {
		for _, place := range places {
			for _, spelling := range spellings {
				t.Run(layout.name+", "+place.name+", "+spelling.name, func(t *testing.T) {
					home := newDir(t)
					outside := newDir(t)
					makeDirs(t, home, ".aws", "work/api")
					proj := place.proj(home, outside)
					makeDirs(t, proj, "sub")
					environmentHome, accountHome := layout.build(t, home, proj)
					t.Setenv("HOME", environmentHome)
					requireSpelling(t, spelling.spell(proj))

					checkRefusals(t, account(accountHome), []refusal{
						{name: "directory that holds the hop", dir: spelling.spell(proj), want: sandbox.ErrProtectedFile},
					})
					t.Run("accepts a directory inside it", func(t *testing.T) {
						checkCreates(t, account(accountHome), filepath.Join(proj, "sub"), filepath.Join(proj, "sub"))
					})
				})
			}
		}
	}
}

// TestHandleRefusesAProjectThatHoldsAHopOfARelativeHome covers a relative
// HOME, which the kernel resolves from the working directory, through a
// symlink in a project directory.
func TestHandleRefusesAProjectThatHoldsAHopOfARelativeHome(t *testing.T) {
	home := newDir(t)
	outside := newDir(t)
	makeDirs(t, home, ".aws")
	makeDirs(t, outside, "proj")
	symlinkAt(t, home, filepath.Join(outside, "proj", "h"))
	t.Chdir(outside)
	t.Setenv("HOME", filepath.Join("proj", "h"))

	checkRefusals(t, account(home), []refusal{
		{name: "directory that holds the hop", dir: filepath.Join(outside, "proj"), want: sandbox.ErrProtectedFile},
	})
}

// TestHandleFollowsAHopOfAProtectedPathWithParentAfterAName covers a hop of
// a protected path whose target exists and has ".." after a name: the walk
// resolves ".." as the kernel does, so init refuses the directories that hold
// a hop or a name that a ".." cancels, and accepts every other project.
func TestHandleFollowsAHopOfAProtectedPathWithParentAfterAName(t *testing.T) {
	tests := []struct {
		name string
		// build makes the hops and returns the directories that hold one.
		build func(t *testing.T, home, outside string) []string
	}{
		{name: "plain name before ..", build: func(t *testing.T, home, outside string) []string {
			makeDirs(t, outside, "x", "vault/aws")
			symlinkAt(t, outside+"/x/../vault/aws", filepath.Join(home, "proj", "link"))
			symlinkAt(t, filepath.Join(home, "proj", "link"), filepath.Join(home, ".aws"))
			return []string{filepath.Join(home, "proj")}
		}},
		{name: "symlink before ..", build: func(t *testing.T, home, outside string) []string {
			makeDirs(t, outside, "links", "elsewhere/sub", "elsewhere/vault/aws")
			symlinkAt(t, filepath.Join(outside, "elsewhere", "sub"), filepath.Join(outside, "links", "x"))
			symlinkAt(t, outside+"/links/x/../vault/aws", filepath.Join(home, "proj", "link"))
			symlinkAt(t, filepath.Join(home, "proj", "link"), filepath.Join(home, ".aws"))
			return []string{filepath.Join(home, "proj"), filepath.Join(outside, "links")}
		}},
		{name: "plain name in a project before ..", build: func(t *testing.T, home, outside string) []string {
			makeDirs(t, home, "proj/x", "vault/aws")
			symlinkAt(t, "proj/x/../../vault/aws", filepath.Join(home, ".aws"))
			return []string{filepath.Join(home, "proj")}
		}},
		{name: "relative target with .. after a name", build: func(t *testing.T, home, outside string) []string {
			makeDirs(t, home, "x", "vault/aws")
			symlinkAt(t, "../x/../vault/aws", filepath.Join(home, "proj", "link"))
			symlinkAt(t, filepath.Join(home, "proj", "link"), filepath.Join(home, ".aws"))
			return []string{filepath.Join(home, "proj")}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := newDir(t)
			outside := newDir(t)
			makeDirs(t, home, "proj", "work/api")
			t.Setenv("HOME", home)
			holders := test.build(t, home, outside)

			var refusals []refusal
			for _, holder := range holders {
				refusals = append(refusals, refusal{name: "holder " + holder, dir: holder, want: sandbox.ErrProtectedFile})
			}
			checkRefusals(t, account(home), refusals)
			t.Run("accepts another project", func(t *testing.T) {
				dir := filepath.Join(home, "work", "api")
				checkCreates(t, account(home), dir, dir)
			})
		})
	}
}
