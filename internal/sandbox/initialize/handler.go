package initialize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	// Exists reports whether the machine name exists.
	Exists(ctx context.Context, name string) (bool, error)
	// Create creates the isolated machine name with dir as its only mount,
	// at the same absolute path as on the host.
	Create(ctx context.Context, name, dir string) error
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
// already exists. It refuses a directory that the mount rules of the sandbox
// module do not accept, before it asks OrbStack anything.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := h.mountableDir(in.Dir)
	if err != nil {
		return Output{}, err
	}
	name := sandbox.MachineName(dir)
	created, err := h.ensureMachine(ctx, name, dir)
	if err != nil {
		return Output{}, err
	}
	return Output{Name: name, Dir: dir, Created: created}, nil
}

// ensureMachine creates the machine name with the mount dir when it does not
// exist, and reports whether it created it.
func (h Handler) ensureMachine(ctx context.Context, name, dir string) (bool, error) {
	exists, err := h.Machines.Exists(ctx, name)
	if err != nil {
		return false, fmt.Errorf("look up sandbox %s: %w", name, err)
	}
	if exists {
		return false, nil
	}
	if err := h.Machines.Create(ctx, name, dir); err != nil {
		return false, fmt.Errorf("create sandbox %s: %w", name, err)
	}
	return true, nil
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
// directory, and the uid of its owner.
func resolveDir(path string) (string, uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.IsDir() {
		return "", 0, fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, fmt.Errorf("owner of %s: %w", path, errors.ErrUnsupported)
	}
	dir, err := realpath.Resolve(path)
	return dir, stat.Uid, err
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
	var protected []string
	for _, path := range protectedPaths(homes) {
		spellings, err := dirSpellings(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		protected = append(protected, spellings...)
	}
	return distinct(protected), nil
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
