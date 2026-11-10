package initialize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/user"
	"slices"
	"strconv"
	"syscall"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// Mounts returns the configured mounts of every machine: for each
	// machine name, the host directory of each mount by its mount point.
	Mounts(ctx context.Context) (map[string]map[string]string, error)
	// ActiveMounts returns what the machine name mounts from the host, as
	// its kernel reports it: the host directory of each mount by its mount
	// point.
	ActiveMounts(ctx context.Context, name string) (map[string]string, error)
	// Create creates the isolated machine name with dir as its only mount,
	// at the same absolute path as on the host.
	Create(ctx context.Context, name, dir string) error
	// Delete deletes the machine name with everything stored in it.
	Delete(ctx context.Context, name string) error
}

// Handler creates the machine of a project directory. Both fields are
// required.
type Handler struct {
	Machines Machines
	// CurrentUser returns the user who runs Orbit, from the account
	// database of the system. cmd/orbit passes user.Current. The handler
	// trusts its uid and its home directory more than the environment.
	CurrentUser func() (*user.User, error)
}

// Handle creates the machine of the project directory in.Dir, unless it
// already exists, and checks that the machine mounts exactly that directory.
// It refuses a directory that the mount rules of the sandbox module do not
// accept before it asks OrbStack anything, and a directory inside the project
// directory of another machine before it creates one. A machine that this
// call created and that fails the check is deleted.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := h.mountableDir(in.Dir)
	if err != nil {
		return Output{}, err
	}
	name := sandbox.MachineName(dir)
	machines, err := h.unnestedMachines(ctx, name, dir)
	if err != nil {
		return Output{}, err
	}
	created, err := h.ensureMachine(ctx, name, dir, machines)
	if err != nil {
		return Output{}, err
	}
	if err := h.verifyMounts(ctx, name, dir, created); err != nil {
		return Output{}, err
	}
	return Output{Name: name, Dir: dir, Created: created}, nil
}

// unnestedMachines returns the configured mounts of every machine, when dir
// lies outside the directories that the machines other than name mount.
func (h Handler) unnestedMachines(ctx context.Context, name, dir string) (map[string]map[string]string, error) {
	machines, err := h.Machines.Mounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	sandboxDirs, err := existingSpellings(mountedDirs(machines, name))
	if err != nil {
		return nil, fmt.Errorf("project directory of another sandbox: %w", err)
	}
	if err := sandbox.ValidateNotNested(dir, sandboxDirs); err != nil {
		return nil, err
	}
	return machines, nil
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
	if deleteErr := h.Machines.Delete(ctx, name); deleteErr != nil {
		return errors.Join(err, fmt.Errorf("delete sandbox %s: %w", name, deleteErr))
	}
	return fmt.Errorf("%w; deleted sandbox %s", err, name)
}

// checkMounts checks that both the configuration and the kernel of the
// machine name report exactly dir, mounted at its own path.
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

// mountableDir returns the canonical path of the project directory path,
// when the mount rules accept it.
func (h Handler) mountableDir(path string) (string, error) {
	facts, err := h.loadFacts(path)
	if err != nil {
		return "", err
	}
	if err := sandbox.ValidateProjectDir(facts); err != nil {
		return "", err
	}
	return facts.Path, nil
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
	return sandbox.ProjectDir{Path: dir, OwnerUID: owner, UserUID: uid, Homes: homes, Protected: protected}, nil
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
