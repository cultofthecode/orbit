// Package orbstack is the adapter to OrbStack. It creates, finds, and deletes
// isolated machines through the orb CLI, and it is the only package with
// OrbStack-specific code. It depends on no other package of this repository.
package orbstack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// cli is the command of OrbStack.
const cli = "orb"

// image is the distribution of every machine that the adapter creates.
const image = "ubuntu:26.04"

// mountSeparator divides the source and the destination of a mount.
const mountSeparator = ":"

// mountListSeparator divides the mounts of a machine in the configuration of
// OrbStack.
const mountListSeparator = ","

var (
	// ErrForeignMachine reports a machine name without the prefix of the
	// client. The client creates and deletes only machines with its prefix.
	ErrForeignMachine = errors.New("machine name does not start with the required prefix")
	// ErrMountPath reports a directory that cannot be mounted at its own
	// path: it is not absolute, or it holds a separator of mounts.
	ErrMountPath = errors.New("mount path is not absolute or contains a colon or a comma")
)

// Client drives the orb CLI.
type Client struct {
	prefix string
}

// New returns a client that creates and deletes only machines whose names
// start with prefix. A client with an empty prefix refuses every name.
func New(prefix string) *Client {
	return &Client{prefix: prefix}
}

// Create creates the isolated machine name with dir as its only mount, at the
// same absolute path as on the host. It runs no command when name lacks the
// prefix of the client or dir cannot be mounted.
func (c *Client) Create(ctx context.Context, name, dir string) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if err := checkMountable(dir); err != nil {
		return err
	}
	if _, err := run(ctx, createArgs(name, dir)); err != nil {
		return fmt.Errorf("create machine %s: %w", name, err)
	}
	return nil
}

// Exists reports whether a machine with the given name exists.
func (c *Client) Exists(ctx context.Context, name string) (bool, error) {
	names, err := run(ctx, listArgs())
	if err != nil {
		return false, fmt.Errorf("list machines: %w", err)
	}
	return listsMachine(names, name), nil
}

// Delete deletes the machine name with everything stored in it. It runs no
// command when name lacks the prefix of the client.
func (c *Client) Delete(ctx context.Context, name string) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if _, err := run(ctx, deleteArgs(name)); err != nil {
		return fmt.Errorf("delete machine %s: %w", name, err)
	}
	return nil
}

// checkOwned refuses a name that does not consist of the prefix of the client
// and at least one more character.
func (c *Client) checkOwned(name string) error {
	if c.prefix == "" || !strings.HasPrefix(name, c.prefix) || name == c.prefix {
		return fmt.Errorf("%w %q: %q", ErrForeignMachine, c.prefix, name)
	}
	return nil
}

// checkMountable refuses a directory that the SOURCE:DEST form of a mount
// cannot express at its own path: orb splits a mount at the first colon, and
// OrbStack stores the mounts of a machine as a list with commas.
func checkMountable(dir string) error {
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, mountSeparator+mountListSeparator) {
		return fmt.Errorf("%w: %q", ErrMountPath, dir)
	}
	return nil
}

// createArgs returns the arguments of orb that create the machine name. The
// machine is always isolated, also on the network, and dir is always mounted
// at its own path: a mount without a destination lands under /mnt/mac.
func createArgs(name, dir string) []string {
	return []string{
		"create",
		"--isolated",
		"--isolate-network",
		"--mount", dir + mountSeparator + dir,
		image,
		name,
	}
}

// listArgs returns the arguments of orb that print the name of every machine,
// one on each line.
func listArgs() []string {
	return []string{"list", "--quiet"}
}

// deleteArgs returns the arguments of orb that delete the machine name
// without a confirmation prompt.
func deleteArgs(name string) []string {
	return []string{"delete", "--force", name}
}

// listsMachine reports whether name is one of the machine names that orb
// printed, one on each line.
func listsMachine(names, name string) bool {
	return slices.Contains(strings.Fields(names), name)
}

// run runs orb with args and returns its standard output. The error of a
// failed command carries the standard error of orb.
func run(ctx context.Context, args []string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// commandError wraps the error of the failed orb subcommand and appends what
// orb printed to its standard error, when it printed anything.
func commandError(subcommand string, err error, stderr string) error {
	if stderr == "" {
		return fmt.Errorf("%s %s: %w", cli, subcommand, err)
	}
	return fmt.Errorf("%s %s: %w: %s", cli, subcommand, err, stderr)
}
