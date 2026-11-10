package initialize

import (
	"context"
	"fmt"
	"os"

	"github.com/cultofthecode/orbit/internal/sandbox"
)

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// Exists reports whether the machine name exists.
	Exists(ctx context.Context, name string) (bool, error)
	// Create creates the isolated machine name with dir as its only mount,
	// at the same absolute path as on the host.
	Create(ctx context.Context, name, dir string) error
}

// Handler creates the machine of a project directory.
type Handler struct {
	Machines Machines
}

// Handle creates the machine of the project directory in.Dir, unless it
// already exists. It refuses a directory that the mount rules of the sandbox
// module do not accept, before it asks OrbStack anything.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	dir, err := mountableDir(in.Dir)
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
func mountableDir(path string) (string, error) {
	dir, err := resolveDir(path)
	if err != nil {
		return "", fmt.Errorf("project directory: %w", err)
	}
	home, err := resolveHome()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	if err := sandbox.ValidateProjectDir(dir, home); err != nil {
		return "", err
	}
	return dir, nil
}

// resolveHome returns the canonical path of the home directory of the user.
func resolveHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return resolveDir(home)
}

// resolveDir returns the canonical path of path, which must be an existing
// directory.
func resolveDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	return realPath(path)
}
