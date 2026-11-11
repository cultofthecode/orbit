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
	"syscall"
	"time"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// discardTimeout bounds the deletion of a machine whose provisioning failed.
const discardTimeout = 2 * time.Minute

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
	// Delete deletes the machine name with everything stored in it.
	Delete(ctx context.Context, name string) error
}

// Handler creates and provisions the machine of a project directory. All
// fields are required.
type Handler struct {
	Machines Machines
	// ProvisionScript returns the root script that provisions a machine
	// whose default user has the uid defaultUID. cmd/orbit passes
	// provision.Script; the slice does not import another module.
	ProvisionScript func(defaultUID uint32) ([]byte, error)
	// CurrentUser returns the user who runs Orbit, from the account
	// database of the system. cmd/orbit passes user.Current. The handler
	// trusts its uid and its home directory more than the environment.
	CurrentUser func() (*user.User, error)
}

// Handle creates the machine of the project directory in.Dir, unless it
// already exists, checks that the machine mounts exactly that directory,
// and then provisions it, also when it already existed. It refuses a
// directory that the mount rules of the sandbox module do not accept before
// it asks OrbStack anything, and a directory that is, lies inside, or
// contains the project directory of another machine before it creates one.
// When the mount check or provisioning fails on a machine that this call
// created, it deletes that machine; it never deletes a machine that existed
// before the call.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	facts, err := h.mountableDir(in.Dir)
	if err != nil {
		return Output{}, err
	}
	name := sandbox.MachineName(facts.Path)
	machines, err := h.unnestedMachines(ctx, name, facts.Path)
	if err != nil {
		return Output{}, err
	}
	created, err := h.ensureMachine(ctx, name, facts.Path, machines)
	if err != nil {
		return Output{}, err
	}
	if err := h.verifyMounts(ctx, name, facts.Path, created); err != nil {
		return Output{}, err
	}
	if err := h.provision(ctx, name, facts.UserUID, created); err != nil {
		return Output{}, err
	}
	return Output{Name: name, Dir: facts.Path, Created: created}, nil
}

// provision runs the root script in the machine name. The default user of
// the machine has the uid of the user who runs Orbit. When the script fails
// and created is true, it deletes the machine.
func (h Handler) provision(ctx context.Context, name string, uid uint32, created bool) error {
	err := h.runScript(ctx, name, uid)
	if err == nil || !created {
		return err
	}
	return h.discard(ctx, name, err)
}

// runScript builds the root script and runs it in the machine name.
func (h Handler) runScript(ctx context.Context, name string, uid uint32) error {
	script, err := h.ProvisionScript(uid)
	if err != nil {
		return fmt.Errorf("provision sandbox %s: %w", name, err)
	}
	if err := h.Machines.RunAsRoot(ctx, name, script); err != nil {
		return fmt.Errorf("provision sandbox %s: %w", name, err)
	}
	return nil
}

// discard deletes the unprovisioned machine name after the failure cause,
// also when ctx is cancelled, within discardTimeout, and reports both.
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
	protected, err := protectedSpellings(homes)
	if err != nil {
		return sandbox.ProjectDir{}, fmt.Errorf("protected directory: %w", err)
	}
	fileDirs, err := protectedFileDirSpellings(homes)
	if err != nil {
		return sandbox.ProjectDir{}, fmt.Errorf("protected file: %w", err)
	}
	return sandbox.ProjectDir{Path: dir, OwnerUID: owner, UserUID: uid, Homes: homes, Protected: protected, ProtectedFileDirs: fileDirs}, nil
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
// protected paths of homes resolve to. It leaves out a protected path that
// does not exist.
func protectedSpellings(homes []string) ([]string, error) {
	return existingSpellings(protectedPaths(homes))
}

// protectedFileDirSpellings returns every spelling of the directories that
// hold the files that the protected file paths of homes resolve to. It
// follows the symlinks without opening the files, and it leaves out a path
// that does not exist.
func protectedFileDirSpellings(homes []string) ([]string, error) {
	var resolved []string
	for _, path := range protectedFilePaths(homes) {
		target, err := filepath.EvalSymlinks(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		spellings, err := dirSpellings(filepath.Dir(target))
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, spellings...)
	}
	return distinct(resolved), nil
}

// existingSpellings returns every spelling of the directories that paths
// resolve to. It leaves out a path that does not exist.
func existingSpellings(paths []string) ([]string, error) {
	var resolved []string
	for _, path := range paths {
		spellings, err := dirSpellings(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, spellings...)
	}
	return distinct(resolved), nil
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
