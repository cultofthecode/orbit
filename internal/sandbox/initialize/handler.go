package initialize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// discardTimeout bounds the deletion of a machine whose provisioning or
// install failed.
const discardTimeout = 2 * time.Minute

// maxLinkHops is the most symlinks that the handler follows on the way to
// where the target of a dangling protected path would be created: MAXSYMLINKS
// of macOS.
const maxLinkHops = 32

// errUnresolvableLink reports a dangling symlink whose target has ".." after
// another name. When that name is a symlink, the kernel applies ".." to its
// target, so a lexical join would protect the wrong directory.
var errUnresolvableLink = errors.New("target of a dangling symlink has .. after a name")

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// Mounts returns the configured mounts of every machine: for each
	// machine name, the host directory of each mount by its mount point.
	Mounts(ctx context.Context) (map[string]map[string]string, error)
	// ActiveMounts returns what the machine name mounts from the host, as
	// its kernel reports it: the host path of each mounted directory by its
	// mount point. It names the path, not the identity of the directory.
	ActiveMounts(ctx context.Context, name string) (map[string]string, error)
	// Create creates the isolated machine name with dir as its only mount,
	// at the same absolute path as on the host.
	Create(ctx context.Context, name, dir string) error
	// RunAsRoot runs script with bash as root in the machine name and
	// fails when the script fails.
	RunAsRoot(ctx context.Context, name string, script []byte) error
	// RunAsAgent runs script with bash as the agent user in the machine
	// name and fails when the script fails.
	RunAsAgent(ctx context.Context, name string, script []byte) error
	// Delete deletes the machine name with everything stored in it.
	Delete(ctx context.Context, name string) error
}

// Handler creates and provisions the machine of a project directory and
// installs its agent. All fields are required.
type Handler struct {
	Machines Machines
	// Agent returns the agent named name, or an error when no agent has
	// that name. cmd/orbit passes a lookup in the registry of agent
	// definitions; the slice does not import another module.
	Agent func(name string) (sandbox.Agent, error)
	// ProvisionScript returns the root script that provisions a machine
	// whose default user has the uid defaultUID and whose proxy allows
	// agentDomains in addition to the base list. cmd/orbit passes
	// provision.Script; the slice does not import another module.
	ProvisionScript func(defaultUID uint32, agentDomains []string) ([]byte, error)
	// CurrentUser returns the user who runs Orbit, from the account
	// database of the system. cmd/orbit passes user.Current. The handler
	// trusts its uid and its home directory more than the environment.
	CurrentUser func() (*user.User, error)
}

// installPrelude starts every install script: it stops at the first failed
// command, also in a pipe, so that a failed download fails the install, and
// it exports the proxy settings of /etc/environment, so that the install
// goes through the proxy.
var installPrelude = []string{"set -euo pipefail", "set -a", ". /etc/environment", "set +a"}

// plan is what the handler decided before it asks OrbStack anything.
type plan struct {
	// name is the name of the machine.
	name string
	// dir is the canonical path of the project directory.
	dir string
	// provision is the root script.
	provision []byte
	// install is the install script of the agent, nil without an agent.
	install []byte
}

// Handle creates the machine of the project directory in.Dir, unless it
// already exists, checks that the machine mounts exactly that directory,
// provisions it, also when it already existed, and then installs the agent
// in.Agent as the agent user, when in.Agent is set. The allowlist of the
// proxy is the base list plus the egress domains of in.Agent, so a run
// without an agent removes the domains of an earlier one. It refuses an
// unknown agent, a directory that the mount rules of the sandbox module do
// not accept, and a provisioning script that cannot be built before it asks
// OrbStack anything, and a directory that is, lies inside, or contains the
// project directory of another machine before it creates one. When the
// mount check, provisioning, or the install fails on a machine that this
// call created, it deletes that machine; it never deletes a machine that
// existed before the call.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	p, err := h.prepare(in)
	if err != nil {
		return Output{}, err
	}
	created, err := h.checkedMachine(ctx, p.name, p.dir)
	if err != nil {
		return Output{}, err
	}
	if err := h.setUp(ctx, p, created); err != nil {
		return Output{}, err
	}
	return Output{Name: p.name, Dir: p.dir, Created: created}, nil
}

// prepare resolves the agent, validates the project directory, and builds the
// scripts, without asking OrbStack anything.
func (h Handler) prepare(in Input) (plan, error) {
	agent, err := h.agent(in.Agent)
	if err != nil {
		return plan{}, err
	}
	facts, err := h.mountableDir(in.Dir)
	if err != nil {
		return plan{}, err
	}
	script, err := h.ProvisionScript(facts.UserUID, agent.EgressDomains)
	if err != nil {
		return plan{}, fmt.Errorf("provisioning script: %w", err)
	}
	p := plan{name: sandbox.MachineName(facts.Path), dir: facts.Path, provision: script}
	if in.Agent != "" {
		p.install = installScript(agent.Install)
	}
	return p, nil
}

// agent returns the agent named name, or no agent when name is empty.
func (h Handler) agent(name string) (sandbox.Agent, error) {
	if name == "" {
		return sandbox.Agent{}, nil
	}
	agent, err := h.Agent(name)
	if err != nil {
		return sandbox.Agent{}, fmt.Errorf("agent: %w", err)
	}
	return agent, nil
}

// installScript puts installPrelude and install into one braced group.
// bash reads the script from standard input as it runs; with the group it
// parses the whole script before it runs any of it, so a truncated script
// fails to parse instead of exiting 0 early.
func installScript(install string) []byte {
	lines := slices.Concat([]string{"{"}, installPrelude, []string{install, "}"})
	return []byte(strings.Join(lines, "\n") + "\n")
}

// checkedMachine creates the machine name with the mount dir, unless it
// exists, after it checked that dir is not nested with the directory of
// another machine, and checks the mounts of the machine. It reports whether
// it created the machine.
func (h Handler) checkedMachine(ctx context.Context, name, dir string) (bool, error) {
	machines, err := h.unnestedMachines(ctx, name, dir)
	if err != nil {
		return false, err
	}
	created, err := h.ensureMachine(ctx, name, dir, machines)
	if err != nil {
		return false, err
	}
	return created, h.verifyMounts(ctx, name, dir, created)
}

// setUp provisions the machine of p and installs its agent. When one of
// them fails and created is true, it deletes the machine.
func (h Handler) setUp(ctx context.Context, p plan, created bool) error {
	err := h.runScripts(ctx, p)
	if err == nil || !created {
		return err
	}
	return h.discard(ctx, p.name, err)
}

// runScripts runs the root script of p and then, when p has an agent, its
// install script as the agent user.
func (h Handler) runScripts(ctx context.Context, p plan) error {
	if err := h.Machines.RunAsRoot(ctx, p.name, p.provision); err != nil {
		return fmt.Errorf("provision sandbox %s: %w", p.name, err)
	}
	if p.install == nil {
		return nil
	}
	if err := h.Machines.RunAsAgent(ctx, p.name, p.install); err != nil {
		return fmt.Errorf("install the agent in sandbox %s: %w", p.name, err)
	}
	return nil
}

// discard deletes the machine name that this call created after the failure
// cause, also when ctx is cancelled, within discardTimeout, and reports both.
func (h Handler) discard(ctx context.Context, name string, cause error) error {
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardTimeout)
	defer cancel()
	if err := h.Machines.Delete(deleteCtx, name); err != nil {
		return errors.Join(cause, fmt.Errorf("delete the unprovisioned sandbox %s: %w", name, err))
	}
	return fmt.Errorf("%w (the new sandbox %s was deleted)", cause, name)
}

// unnestedMachines returns the configured mounts of every machine, when dir
// neither is, lies inside, nor contains a directory that a machine other
// than name mounts, and no stored path of such a directory passes through a
// symlink.
func (h Handler) unnestedMachines(ctx context.Context, name, dir string) (map[string]map[string]string, error) {
	machines, err := h.Machines.Mounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	if err := validateNotNested(dir, mountedDirs(machines, name)); err != nil {
		return nil, err
	}
	if err := validateUnlinked(machines, name); err != nil {
		return nil, err
	}
	return machines, nil
}

// validateUnlinked refuses the stored mount paths of the machines other than
// name when a component of one of them that exists is a symlink. Orbit stores
// canonical paths, so such a symlink means that the host layout changed after
// the machine was created; OrbStack would follow it at the next start, also
// when it dangles or when one of its hops lies in the new directory.
func validateUnlinked(machines map[string]map[string]string, name string) error {
	for machine, mounts := range machines {
		if machine == name {
			continue
		}
		for _, dir := range mounts {
			if err := checkUnlinked(dir); err != nil {
				return fmt.Errorf("project directory %s of sandbox %s: %w", dir, machine, err)
			}
		}
	}
	return nil
}

// checkUnlinked fails with ErrLinkedSandboxDir when a component of the
// absolute path that exists is a symlink. It stops at the first component
// that does not exist, and fails on every other error.
func checkUnlinked(path string) error {
	for _, prefix := range pathPrefixes(path) {
		info, err := os.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrLinkedSandboxDir, prefix)
		}
	}
	return nil
}

// pathPrefixes returns every path from the first component of the absolute
// path to the path itself, in that order.
func pathPrefixes(path string) []string {
	var prefixes []string
	for prefix := path; prefix != filepath.Dir(prefix); prefix = filepath.Dir(prefix) {
		prefixes = append(prefixes, prefix)
	}
	slices.Reverse(prefixes)
	return prefixes
}

// validateNotNested refuses dir when it is, lies inside, or contains one of
// sandboxDirs, the directories that other machines mount, as OrbStack stores
// them. OrbStack resolves the stored path again at every start, so it checks
// each one as it is first, also when it does not exist, and refuses one that
// is not absolute and clean before it reads the file system. Then it checks
// every spelling that each one resolves to.
func validateNotNested(dir string, sandboxDirs []string) error {
	if err := sandbox.ValidateNotNested(dir, sandboxDirs); err != nil {
		return err
	}
	spellings, err := pathSpellings(sandboxDirs)
	if err != nil {
		return fmt.Errorf("project directory of another sandbox: %w", err)
	}
	return sandbox.ValidateNotNested(dir, spellings)
}

// pathSpellings returns every spelling of each path in paths. For a path
// that does not exist, it returns every spelling of its nearest existing
// parent, joined with the rest of the path.
func pathSpellings(paths []string) ([]string, error) {
	var spellings []string
	for _, path := range paths {
		found, err := nearestSpellings(path)
		if err != nil {
			return nil, err
		}
		spellings = append(spellings, found...)
	}
	return distinct(spellings), nil
}

// nearestSpellings returns every spelling of the absolute path, or, when it
// does not exist, every spelling of its parent joined with its base name.
func nearestSpellings(path string) ([]string, error) {
	spellings, err := dirSpellings(path)
	parent := filepath.Dir(path)
	if !errors.Is(err, fs.ErrNotExist) || parent == path {
		return spellings, err
	}
	parentSpellings, err := nearestSpellings(parent)
	if err != nil {
		return nil, err
	}
	return joinAll(parentSpellings, filepath.Base(path)), nil
}

// joinAll returns each of dirs joined with name.
func joinAll(dirs []string, name string) []string {
	joined := make([]string, len(dirs))
	for i, dir := range dirs {
		joined[i] = filepath.Join(dir, name)
	}
	return joined
}

// ensureMachine creates the machine name with the mount dir when it is not
// one of machines, and reports whether it created it.
func (h Handler) ensureMachine(ctx context.Context, name, dir string, machines map[string]map[string]string) (bool, error) {
	if _, exists := machines[name]; exists {
		return false, nil
	}
	if err := h.Machines.Create(ctx, name, dir); err != nil {
		return false, fmt.Errorf("create sandbox %s: %w", name, err)
	}
	return true, nil
}

// verifyMounts checks that the machine name mounts exactly dir at its own
// path. When created is true and the check fails, it deletes the machine.
func (h Handler) verifyMounts(ctx context.Context, name, dir string, created bool) error {
	err := h.checkMounts(ctx, name, dir)
	if err == nil || !created {
		return err
	}
	return h.discard(ctx, name, err)
}

// checkMounts checks that both the configuration and the kernel of the
// machine name report exactly dir, mounted at its own path. It is defense in
// depth: the kernel does not report a path that was swapped for a symlink
// between two starts. The nesting refusal covers only the folder layout when
// init runs; a slice that starts a machine is required to run it again.
func (h Handler) checkMounts(ctx context.Context, name, dir string) error {
	machines, err := h.Machines.Mounts(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	if err := sandbox.ValidateMounts(dir, machines[name]); err != nil {
		return fmt.Errorf("configuration of sandbox %s: %w", name, err)
	}
	active, err := h.Machines.ActiveMounts(ctx, name)
	if err != nil {
		return fmt.Errorf("read the mounts of sandbox %s: %w", name, err)
	}
	if err := sandbox.ValidateMounts(dir, active); err != nil {
		return fmt.Errorf("mounts of sandbox %s: %w", name, err)
	}
	return nil
}

// mountedDirs returns the host directories that the machines other than name
// mount.
func mountedDirs(machines map[string]map[string]string, name string) []string {
	var dirs []string
	for machine, mounts := range machines {
		if machine != name {
			dirs = slices.AppendSeq(dirs, maps.Values(mounts))
		}
	}
	return dirs
}

// mountableDir returns the facts of the project directory path, with its
// canonical path, when the mount rules accept it.
func (h Handler) mountableDir(path string) (sandbox.ProjectDir, error) {
	facts, err := h.loadFacts(path)
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	if err := sandbox.ValidateProjectDir(facts); err != nil {
		return sandbox.ProjectDir{}, err
	}
	return facts, nil
}

// loadFacts reads from the system what the mount rules decide on. It fails
// when it cannot read one of the facts.
func (h Handler) loadFacts(path string) (sandbox.ProjectDir, error) {
	dir, owner, err := resolveDir(path)
	if err != nil {
		return sandbox.ProjectDir{}, fmt.Errorf("project directory: %w", err)
	}
	uid, homes, err := h.loadUser()
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	protected, fileDirs, err := loadProtected(homes)
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	return sandbox.ProjectDir{Path: dir, OwnerUID: owner, UserUID: uid, Homes: homes, Protected: protected, ProtectedFileDirs: fileDirs}, nil
}

// loadProtected returns every spelling of the protected directories of homes
// and of the environment, and of the directories that hold a protected file,
// together with what the symlinks in those protected directories lead to.
func loadProtected(homes []string) ([]string, []string, error) {
	protected, err := allProtectedSpellings(homes)
	if err != nil {
		return nil, nil, err
	}
	fileDirs, err := allFileDirSpellings(homes)
	if err != nil {
		return nil, nil, err
	}
	linkedDirs, linkedFileDirs, err := linkedSpellings(protected)
	if err != nil {
		return nil, nil, fmt.Errorf("symlink in a protected directory: %w", err)
	}
	return distinct(slices.Concat(protected, linkedDirs)), distinct(slices.Concat(fileDirs, linkedFileDirs)), nil
}

// allProtectedSpellings returns every spelling of the directories that the
// protected paths of homes and the protected directories that the
// environment moves resolve to, or where their targets would be created.
func allProtectedSpellings(homes []string) ([]string, error) {
	protected, err := protectedSpellings(homes)
	if err != nil {
		return nil, fmt.Errorf("protected directory: %w", err)
	}
	relocated, err := relocatedSpellings(sandbox.RelocatedDirs, targetSpellings)
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(protected, relocated)), nil
}

// allFileDirSpellings returns every spelling of the directories that hold
// the protected files of homes and the protected files that the environment
// moves, or would be created in, and of the directories that hold a symlink
// on the path of a location that the environment moves.
func allFileDirSpellings(homes []string) ([]string, error) {
	fileDirs, err := protectedFileDirSpellings(homes)
	if err != nil {
		return nil, fmt.Errorf("protected file: %w", err)
	}
	relocated, err := relocatedSpellings(sandbox.RelocatedFiles, fileDirSpellings)
	if err != nil {
		return nil, err
	}
	hops, err := relocatedHopSpellings()
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(fileDirs, relocated, hops)), nil
}

// relocatedHopSpellings returns every spelling of each directory that holds
// a symlink on the path of a protected location that the environment moves,
// as linkSpellings walks it. A variable can name a path through a project
// directory, where the agent could replace that symlink and redirect the
// tool on the host.
func relocatedHopSpellings() ([]string, error) {
	dirHops, err := relocatedSpellings(sandbox.RelocatedDirs, chainHolders)
	if err != nil {
		return nil, err
	}
	fileHops, err := relocatedSpellings(sandbox.RelocatedFiles, chainHolders)
	if err != nil {
		return nil, err
	}
	return slices.Concat(dirHops, fileHops), nil
}

// chainHolders returns the second result of linkSpellings for path: every
// spelling of each directory that holds a symlink on its chain.
func chainHolders(path string) ([]string, error) {
	_, holders, err := linkSpellings(path)
	return holders, err
}

// relocatedSpellings returns what resolve returns for each protected
// location that locate finds in the environment of the user who runs Orbit.
// It reads only the values of the variables, never a file that they name
// (D08), and names the variable of a location that cannot be resolved.
func relocatedSpellings(locate func(func(string) (string, bool)) ([]sandbox.Relocated, error), resolve func(string) ([]string, error)) ([]string, error) {
	relocated, err := locate(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	var spellings []string
	for _, location := range relocated {
		found, err := resolve(location.Path)
		if err != nil {
			return nil, fmt.Errorf("protected location of %s: %w", location.Variable, err)
		}
		spellings = append(spellings, found...)
	}
	return spellings, nil
}

// loadUser returns the uid of the user who runs Orbit and every spelling of
// the home directories of that user.
func (h Handler) loadUser() (uint32, []string, error) {
	account, err := h.CurrentUser()
	if err != nil {
		return 0, nil, fmt.Errorf("current user: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, nil, fmt.Errorf("uid of the current user: %w", err)
	}
	homes, err := homeSpellings(account.HomeDir)
	if err != nil {
		return 0, nil, fmt.Errorf("home directory: %w", err)
	}
	return uint32(uid), homes, nil
}

// resolveDir returns the canonical path of path, which must be an existing
// directory, and the uid of its owner, both read from one open handle.
func resolveDir(path string) (string, uint32, error) {
	dir, owner, err := realpath.ResolveWithOwner(path)
	if errors.Is(err, syscall.ENOTDIR) {
		return "", 0, fmt.Errorf("%w: %w", ErrNotDirectory, err)
	}
	return dir, owner, err
}

// homeSpellings returns every spelling of the home directories of the user:
// the one of the environment and accountHome, the one of the account
// database. Both must exist, because the environment alone can name any
// directory.
func homeSpellings(accountHome string) ([]string, error) {
	environmentHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var homes []string
	for _, home := range []string{environmentHome, accountHome} {
		spellings, err := dirSpellings(home)
		if err != nil {
			return nil, err
		}
		homes = append(homes, spellings...)
	}
	return distinct(homes), nil
}

// protectedSpellings returns every spelling of the directories that the
// protected paths of homes resolve to. For a protected path that does not
// exist, it returns every spelling of the path where its target would be
// created (targetSpellings).
func protectedSpellings(homes []string) ([]string, error) {
	var spellings []string
	for _, path := range protectedPaths(homes) {
		found, err := targetSpellings(path)
		if err != nil {
			return nil, err
		}
		spellings = append(spellings, found...)
	}
	return distinct(spellings), nil
}

// protectedFileDirSpellings returns every spelling of the directories that
// hold the files that the protected file paths of homes resolve to, or
// would be created in. It follows the symlinks without opening the files.
func protectedFileDirSpellings(homes []string) ([]string, error) {
	var resolved []string
	for _, path := range protectedFilePaths(homes) {
		spellings, err := fileDirSpellings(path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, spellings...)
	}
	return distinct(resolved), nil
}

// fileDirSpellings returns every spelling of the directory that holds the
// file that path resolves to, without opening the file. For a path that
// leads nowhere, it is the directory in which the final target would be
// created.
func fileDirSpellings(path string) ([]string, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return missingFileDirSpellings(path)
	}
	if err != nil {
		return nil, err
	}
	return dirSpellings(filepath.Dir(target))
}

// missingFileDirSpellings returns every spelling of the directory in which
// the final target of the path that leads nowhere would be created.
func missingFileDirSpellings(path string) ([]string, error) {
	targets, err := targetSpellings(path)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, len(targets))
	for i, target := range targets {
		dirs[i] = filepath.Dir(target)
	}
	return dirs, nil
}

// targetSpellings returns every spelling of the directory that the absolute
// path resolves to. For a path that does not exist, it returns every
// spelling of the path where its target would be created: the nearest
// existing parent joined with the rest of the path, where a dangling symlink
// on the way is followed to its target, as the kernel would follow it.
func targetSpellings(path string) ([]string, error) {
	return followedSpellings(path, 0)
}

// followedSpellings is targetSpellings after hops symlinks.
func followedSpellings(path string, hops int) ([]string, error) {
	spellings, err := dirSpellings(path)
	parent := filepath.Dir(path)
	if !errors.Is(err, fs.ErrNotExist) || parent == path {
		return spellings, err
	}
	parentSpellings, err := followedSpellings(parent, hops)
	if err != nil {
		return nil, err
	}
	return followLinks(joinAll(parentSpellings, filepath.Base(path)), hops)
}

// followLinks returns the paths, with every path that is a symlink replaced
// by the spellings of where its target would be created. The paths are
// spellings of one path, so it follows each distinct target once.
func followLinks(paths []string, hops int) ([]string, error) {
	var kept, targets []string
	for _, path := range paths {
		target, err := danglingTarget(path)
		if err != nil {
			return nil, err
		}
		if target == "" {
			kept = append(kept, path)
		} else {
			targets = append(targets, target)
		}
	}
	followed, err := followTargets(distinct(targets), hops)
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(kept, followed)), nil
}

// followTargets returns the spellings of where each of targets, reached
// after hops symlinks, would be created.
func followTargets(targets []string, hops int) ([]string, error) {
	if len(targets) > 0 && hops == maxLinkHops {
		return nil, fmt.Errorf("%w: %s", syscall.ELOOP, targets[0])
	}
	var followed []string
	for _, target := range targets {
		spellings, err := followedSpellings(target, hops+1)
		if err != nil {
			return nil, err
		}
		followed = append(followed, spellings...)
	}
	return followed, nil
}

// danglingTarget returns the target of path when path is a symlink, and ""
// otherwise.
func danglingTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return "", nil
	}
	return linkTarget(path)
}

// linkTarget returns the absolute, clean path that the symlink link points
// to, with a relative target joined to the canonical path of the directory
// that holds the link, so that every spelling of link gives one target. It
// refuses a target that has ".." after another name (errUnresolvableLink).
func linkTarget(link string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if hasParentAfterName(target) {
		return "", fmt.Errorf("%w: %s -> %s", errUnresolvableLink, link, target)
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	dir, err := realpath.Resolve(filepath.Dir(link))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, target), nil
}

// hasParentAfterName reports whether target has a ".." segment after a
// segment that is neither "." nor "..". A leading ".." is resolved against
// the directory of the symlink, which is canonical, so a lexical join is
// right for it.
func hasParentAfterName(target string) bool {
	segments := slices.DeleteFunc(strings.Split(target, string(filepath.Separator)), func(segment string) bool {
		return segment == "" || segment == "."
	})
	leading := 0
	for leading < len(segments) && segments[leading] == ".." {
		leading++
	}
	return slices.Contains(segments[leading:], "..")
}

// linkedSpellings returns what the symlinks in the protected directories
// lead to, and in every directory that one of them leads to: every spelling
// of each directory, of the directory that holds each file, and of each
// directory that holds a symlink on the way (linkSpellings). A symlink
// that leads nowhere protects the path where its target would be created,
// as a directory. It reads only metadata and never opens a file (D08). A
// protected path that does not exist has nothing in it.
func linkedSpellings(protected []string) ([]string, []string, error) {
	var dirs, fileDirs []string
	walked := map[string]bool{}
	for queue := slices.Clone(protected); len(queue) > 0; queue = queue[1:] {
		root, err := realpath.Resolve(queue[0])
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if walked[root] {
			continue
		}
		walked[root] = true
		linkedDirs, linkedFileDirs, err := linksIn(root)
		if err != nil {
			return nil, nil, err
		}
		dirs = append(dirs, linkedDirs...)
		fileDirs = append(fileDirs, linkedFileDirs...)
		queue = append(queue, linkedDirs...)
	}
	return dirs, fileDirs, nil
}

// linksIn returns what the symlinks in the directory root and below it lead
// to, as linkSpellings returns it. It does not follow a symlink to walk it.
func linksIn(root string) ([]string, []string, error) {
	var dirs, fileDirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSymlink == 0 {
			return err
		}
		linkedDirs, linkedFileDirs, err := linkSpellings(path)
		dirs = append(dirs, linkedDirs...)
		fileDirs = append(fileDirs, linkedFileDirs...)
		return err
	})
	return dirs, fileDirs, err
}

// linkSpellings returns what the chain of the symlink link leads to, as the
// kernel resolves it one name at a time. The first list has every spelling of
// the directory that the chain ends at, or of where its target would be
// created, as a directory, when it leads nowhere. The second has every
// spelling of each directory that holds a symlink on the chain, every hop
// included, and of the directory that holds the file that the chain ends at.
// The agent could replace a hop in a mounted directory, so each of them is
// protected as the holder of a protected file.
func linkSpellings(link string) ([]string, []string, error) {
	walk := chainWalk{resolved: string(filepath.Separator), rest: pathNames(link)}
	for len(walk.rest) > 0 {
		missing, err := walk.step()
		if err != nil {
			return nil, nil, err
		}
		if missing {
			return walk.missingSpellings()
		}
	}
	return walk.endSpellings()
}

// chainWalk resolves a path one name at a time, as the kernel does, and keeps
// every spelling of each directory that holds a symlink on the way.
type chainWalk struct {
	resolved string
	rest     []string
	hops     int
	holders  []string
}

// step resolves the next name of the path. It reports whether that name does
// not exist.
func (w *chainWalk) step() (bool, error) {
	next := filepath.Join(w.resolved, w.rest[0])
	info, err := os.Lstat(next)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, w.follow(next)
	}
	w.resolved, w.rest = next, w.rest[1:]
	return false, nil
}

// follow replaces the symlink link, which is the next name of the path, with
// its target, and keeps the directory that holds link. It follows at most
// maxLinkHops symlinks.
func (w *chainWalk) follow(link string) error {
	if w.hops == maxLinkHops {
		return fmt.Errorf("%w: %s", syscall.ELOOP, link)
	}
	target, err := linkTarget(link)
	if err != nil {
		return err
	}
	holders, err := dirSpellings(w.resolved)
	if err != nil {
		return err
	}
	w.holders = append(w.holders, holders...)
	w.resolved, w.rest, w.hops = string(filepath.Separator), slices.Concat(pathNames(target), w.rest[1:]), w.hops+1
	return nil
}

// missingSpellings returns every spelling of where the target of a path that
// leads nowhere would be created, as a directory, and the holders.
func (w *chainWalk) missingSpellings() ([]string, []string, error) {
	spellings, err := dirSpellings(w.resolved)
	if err != nil {
		return nil, nil, err
	}
	return joinAll(spellings, filepath.Join(w.rest...)), w.holders, nil
}

// endSpellings returns every spelling of the directory that the path ends at,
// and the holders, with the directory that holds the file that the path ends
// at.
func (w *chainWalk) endSpellings() ([]string, []string, error) {
	info, err := os.Lstat(w.resolved)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		dirs, err := dirSpellings(w.resolved)
		return dirs, w.holders, err
	}
	fileDirs, err := dirSpellings(filepath.Dir(w.resolved))
	return nil, slices.Concat(w.holders, fileDirs), err
}

// pathNames returns the names in the absolute, clean path.
func pathNames(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == filepath.Separator })
}

// protectedPaths returns the protected paths of every home in homes.
func protectedPaths(homes []string) []string {
	var paths []string
	for _, home := range homes {
		paths = append(paths, sandbox.ProtectedDirs(home)...)
	}
	return paths
}

// protectedFilePaths returns the protected file paths of every home in homes.
func protectedFilePaths(homes []string) []string {
	var paths []string
	for _, home := range homes {
		paths = append(paths, sandbox.ProtectedFiles(home)...)
	}
	return paths
}

// dirSpellings returns the canonical path of the existing directory path and
// its path on its own volume.
func dirSpellings(path string) ([]string, error) {
	canonical, err := realpath.Resolve(path)
	if err != nil {
		return nil, err
	}
	noFirmlink, err := realpath.ResolveNoFirmlink(path)
	if err != nil {
		return nil, err
	}
	return []string{canonical, noFirmlink}, nil
}

// distinct returns the paths without duplicates, sorted.
func distinct(paths []string) []string {
	slices.Sort(paths)
	return slices.Compact(paths)
}
