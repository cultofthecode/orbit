// Package orbstack is the adapter to OrbStack. Through the orb CLI it
// creates, finds, and deletes isolated machines and runs scripts in them as
// root or as the agent user. It is the only package with OrbStack-specific
// code, and it depends on no other package of this repository.
package orbstack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// cli is the command of OrbStack.
const cli = "orb"

// rootUser is the user that RunAsRoot runs a script as.
const rootUser = "root"

// AgentUser is the user that RunAsAgent runs a script as: the unprivileged
// user that provisioning creates for the agent.
const AgentUser = "agent"

// image is the distribution of every machine that the adapter creates.
const image = "ubuntu:26.04"

// States that "orb status" prints. It reports the state of OrbStack without
// starting it.
const (
	statusRunning  = "Running"
	statusStarting = "Starting"
	statusStopped  = "Stopped"
)

// stateDir is the directory under $HOME through which orb finds OrbStack.
const stateDir = ".orbstack"

// mountSeparator divides the source and the destination of a mount.
const mountSeparator = ":"

// mountListSeparator divides the mounts of a machine in the configuration of
// OrbStack.
const mountListSeparator = ","

// Values of the mount table of a machine (/proc/self/mountinfo) that the
// adapter reads.
const (
	// mountTable is the mount table of the process that reads it.
	mountTable = "/proc/self/mountinfo"
	// optionalFieldsEnd ends the optional fields of an entry of the table.
	optionalFieldsEnd = "-"
	// hostFileSystem is the type of the file system that shares the host.
	hostFileSystem = "virtiofs"
	// hostSource is the source of that file system. Its root is the root of
	// the host, so the root of a mount of it is a path on the host.
	hostSource = "mac"
)

var (
	// ErrForeignMachine reports a machine name without the prefix of the
	// client. The client creates and deletes only machines with its prefix.
	ErrForeignMachine = errors.New("machine name does not start with the required prefix")
	// ErrMountPath reports a directory that cannot be mounted at its own
	// path: it is not absolute, or it holds a separator of mounts.
	ErrMountPath = errors.New("mount path is not absolute or contains a colon or a comma")
	// ErrMountTable reports a list of machines or a mount table that does not
	// say for certain what is mounted where.
	ErrMountTable = errors.New("mounts cannot be read")
	// ErrNotRunning reports that OrbStack is not running. The client then
	// runs no other command, because orb starts a stopped OrbStack.
	ErrNotRunning = errors.New("OrbStack is not running")
	// ErrHome reports that orb status printed a stopped OrbStack while HOME
	// does not lead orb to the OrbStack of the account: orb finds OrbStack
	// through $HOME/.orbstack, so OrbStack may be running. The client runs no
	// other command, and it never runs orb with another HOME.
	ErrHome = errors.New("orb cannot reach OrbStack through HOME")
)

// currentUser returns the account of the process from the user database of
// the system, which does not read HOME. Tests replace it.
var currentUser = user.Current

// Client drives the orb CLI. It runs a command only after orb status reports
// a running OrbStack, so that no call starts a stopped OrbStack.
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
	if _, err := run(ctx, createArgs(name, dir), nil); err != nil {
		return fmt.Errorf("create machine %s: %w", name, err)
	}
	return nil
}

// Exists reports whether a machine with the given name exists.
func (c *Client) Exists(ctx context.Context, name string) (bool, error) {
	names, err := run(ctx, listArgs(), nil)
	if err != nil {
		return false, fmt.Errorf("list machines: %w", err)
	}
	return listsMachine(names, name), nil
}

// Mounts returns the configured mounts of every machine: for each machine
// name, the host directory of each mount by its mount point. A machine
// without isolation has no configured mounts.
func (c *Client) Mounts(ctx context.Context) (map[string]map[string]string, error) {
	list, err := run(ctx, mountsArgs(), nil)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	return parseMounts([]byte(list))
}

// ActiveMounts returns the host directory of each mount of the host file
// system in the machine name, by its mount point, as the kernel of the
// machine reports it. The kernel reports the path of the mounted directory,
// not its identity: a path swapped for a symlink between two starts of the
// machine still shows as the stored path. It starts the machine when it is
// stopped. It runs no command when name lacks the prefix of the client.
func (c *Client) ActiveMounts(ctx context.Context, name string) (map[string]string, error) {
	if err := c.checkOwned(name); err != nil {
		return nil, err
	}
	table, err := run(ctx, activeMountsArgs(name), nil)
	if err != nil {
		return nil, fmt.Errorf("read the mounts of machine %s: %w", name, err)
	}
	return parseMountTable(table)
}

// Delete deletes the machine name with everything stored in it. It runs no
// command when name lacks the prefix of the client.
func (c *Client) Delete(ctx context.Context, name string) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if _, err := run(ctx, deleteArgs(name), nil); err != nil {
		return fmt.Errorf("delete machine %s: %w", name, err)
	}
	return nil
}

// RunAsRoot runs script with bash as root in the machine name, with the
// script on standard input, and fails when the script exits non-zero. It
// runs no command when name lacks the prefix of the client.
func (c *Client) RunAsRoot(ctx context.Context, name string, script []byte) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if _, err := run(ctx, runAsRootArgs(name), script); err != nil {
		return fmt.Errorf("run a script as root in machine %s: %w", name, err)
	}
	return nil
}

// RunAsAgent runs script with bash as AgentUser in the machine name, with
// the script on standard input, and fails when the script exits non-zero.
// The user is fixed; no argument of the caller reaches the command line. It
// runs no command when name lacks the prefix of the client.
func (c *Client) RunAsAgent(ctx context.Context, name string, script []byte) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if _, err := run(ctx, runAsAgentArgs(name), script); err != nil {
		return fmt.Errorf("run a script as %s in machine %s: %w", AgentUser, name, err)
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

// statusArgs returns the arguments of orb that print the state of OrbStack
// without starting it.
func statusArgs() []string {
	return []string{"status"}
}

// listArgs returns the arguments of orb that print the name of every machine,
// one on each line.
func listArgs() []string {
	return []string{"list", "--quiet"}
}

// mountsArgs returns the arguments of orb that print every machine with its
// configuration as JSON.
func mountsArgs() []string {
	return []string{"list", "--format", "json"}
}

// activeMountsArgs returns the arguments of orb that print the mount table of
// the machine name, read as root.
func activeMountsArgs(name string) []string {
	return []string{"-m", name, "-u", "root", "cat", mountTable}
}

// machine is the part of an entry of "orb list --format json" that the
// adapter reads.
type machine struct {
	Name   string `json:"name"`
	Config struct {
		Mounts []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"mounts"`
	} `json:"config"`
}

// parseMounts returns the configured mounts of every machine in the output
// of "orb list --format json". It fails on a machine name or a mount point
// that appears twice.
func parseMounts(list []byte) (map[string]map[string]string, error) {
	var machines []machine
	if err := json.Unmarshal(list, &machines); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMountTable, err)
	}
	mounts := make(map[string]map[string]string, len(machines))
	for _, m := range machines {
		if _, seen := mounts[m.Name]; seen {
			return nil, fmt.Errorf("%w: machine %q is listed twice", ErrMountTable, m.Name)
		}
		mounts[m.Name] = map[string]string{}
		for _, mount := range m.Config.Mounts {
			if err := addMount(mounts[m.Name], mount.Destination, mount.Source); err != nil {
				return nil, fmt.Errorf("machine %q: %w", m.Name, err)
			}
		}
	}
	return mounts, nil
}

// parseMountTable returns the mounts of the host file system in a mount table
// in the format of /proc/self/mountinfo: the root of each mount, which is a
// path on the host, by its mount point. It fails on an entry that it cannot
// read and on a mount point that appears twice.
func parseMountTable(table string) (map[string]string, error) {
	mounts := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		root, mountPoint, isHost, err := parseMountEntry(line)
		if err != nil {
			return nil, err
		}
		if !isHost {
			continue
		}
		if err := addMount(mounts, mountPoint, root); err != nil {
			return nil, err
		}
	}
	return mounts, nil
}

// parseMountEntry returns the root and the mount point of one entry of a
// mount table, and whether it mounts the host file system.
func parseMountEntry(line string) (root, mountPoint string, isHost bool, err error) {
	fields := strings.Split(line, " ")
	end := slices.Index(fields, optionalFieldsEnd)
	if len(fields) < 10 || end < 6 || end+3 >= len(fields) {
		return "", "", false, fmt.Errorf("%w: entry %q", ErrMountTable, line)
	}
	if fields[end+1] != hostFileSystem || fields[end+2] != hostSource {
		return "", "", false, nil
	}
	root, err = unescapeMountPath(fields[3])
	if err != nil {
		return "", "", false, err
	}
	mountPoint, err = unescapeMountPath(fields[4])
	return root, mountPoint, true, err
}

// unescapeMountPath reverses the escapes of a path in a mount table: the
// kernel writes a space, a tab, a newline, and a backslash as a backslash
// with three octal digits.
func unescapeMountPath(path string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != '\\' {
			out.WriteByte(path[i])
			continue
		}
		if i+4 > len(path) {
			return "", fmt.Errorf("%w: escape in %q", ErrMountTable, path)
		}
		code, err := strconv.ParseUint(path[i+1:i+4], 8, 8)
		if err != nil {
			return "", fmt.Errorf("%w: escape in %q", ErrMountTable, path)
		}
		out.WriteByte(byte(code))
		i += 3
	}
	return out.String(), nil
}

// addMount adds the mount of hostDir at mountPoint to mounts. It fails when
// mountPoint is already in mounts, because then the table does not say which
// directory is mounted there.
func addMount(mounts map[string]string, mountPoint, hostDir string) error {
	if _, seen := mounts[mountPoint]; seen {
		return fmt.Errorf("%w: two mounts at %q", ErrMountTable, mountPoint)
	}
	mounts[mountPoint] = hostDir
	return nil
}

// deleteArgs returns the arguments of orb that delete the machine name
// without a confirmation prompt.
func deleteArgs(name string) []string {
	return []string{"delete", "--force", name}
}

// runAsRootArgs returns the arguments of orb that run bash as root in the
// machine name, reading the script from standard input.
func runAsRootArgs(name string) []string {
	return []string{"-m", name, "-u", rootUser, "bash", "-s"}
}

// runAsAgentArgs returns the arguments of orb that run bash as AgentUser in
// the machine name, reading the script from standard input.
func runAsAgentArgs(name string) []string {
	return []string{"-m", name, "-u", AgentUser, "bash", "-s"}
}

// listsMachine reports whether name is one of the machine names that orb
// printed, one on each line.
func listsMachine(names, name string) bool {
	return slices.Contains(strings.Fields(names), name)
}

// run runs orb with args and stdin and returns its standard output, only
// when OrbStack is running: orb starts a stopped OrbStack by itself. The
// error of a failed command carries the standard error of orb.
func run(ctx context.Context, args []string, stdin []byte) (string, error) {
	if err := checkRunning(ctx); err != nil {
		return "", err
	}
	return execute(ctx, args, stdin)
}

// checkRunning fails unless orb status reports a running OrbStack. It reads
// HOME before orb runs, because orb creates $HOME/.orbstack.
func checkRunning(ctx context.Context) error {
	home := readHome()
	status, err := execute(ctx, statusArgs(), nil)
	return statusError(strings.TrimSpace(status), err, homeCause(home))
}

// statusError returns the error for the state that orb status printed, for
// its error, and for homeCause, the reason why HOME may not lead orb to
// OrbStack: nil only for a running OrbStack, ErrHome for a stopped or
// starting one when homeCause is not empty, ErrNotRunning for a stopped or
// starting one otherwise, and an error for anything else.
func statusError(status string, err error, homeCause string) error {
	switch {
	case isNotRunning(status) && homeCause != "":
		return fmt.Errorf("%w: %s, and orb finds OrbStack through $HOME/%s (orb status: %s); OrbStack may still be running: set HOME to the home directory of the account and try again", ErrHome, homeCause, stateDir, status)
	case isNotRunning(status):
		return fmt.Errorf("%w (orb status: %s); start OrbStack, wait until orb status prints Running, and try again", ErrNotRunning, status)
	case err != nil:
		return err
	case status != statusRunning:
		return fmt.Errorf("%s %s printed %q, not %q", cli, statusArgs()[0], status, statusRunning)
	}
	return nil
}

// isNotRunning reports whether orb status printed a stopped or starting
// OrbStack.
func isNotRunning(status string) bool {
	return status == statusStopped || status == statusStarting
}

// home is what decides whether HOME leads orb to the OrbStack of the
// account.
type home struct {
	// env is HOME, canonical when it is absolute.
	env string
	// account is the canonical home directory of the account, or empty when
	// the user database does not give it.
	account string
	// hasStateDir reports whether $HOME/.orbstack is a directory.
	hasStateDir bool
}

// readHome reads HOME, the home directory of the account, and whether
// $HOME/.orbstack is a directory.
func readHome() home {
	h := home{env: canonical(os.Getenv("HOME"))}
	if account, err := currentUser(); err == nil && account.HomeDir != "" {
		h.account = canonical(account.HomeDir)
	}
	if filepath.IsAbs(h.env) {
		info, err := os.Stat(filepath.Join(h.env, stateDir))
		h.hasStateDir = err == nil && info.IsDir()
	}
	return h
}

// canonical returns an absolute path with its symlinks resolved, or cleaned
// when it cannot be resolved. It returns a relative path unchanged.
func canonical(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// homeCause returns why HOME may not lead orb to the OrbStack of the
// account, or an empty string when it leads there.
func homeCause(h home) string {
	switch {
	case !filepath.IsAbs(h.env):
		return fmt.Sprintf("HOME %q is not an absolute path", h.env)
	case h.account != "" && h.env != h.account:
		return fmt.Sprintf("HOME %q is not the home directory of the account, %q", h.env, h.account)
	case !h.hasStateDir:
		return fmt.Sprintf("%q is not a directory", filepath.Join(h.env, stateDir))
	}
	return ""
}

// execute runs orb with args and stdin and returns its standard output, also
// when the command fails. The error of a failed command carries the
// standard error of orb.
func execute(ctx context.Context, args []string, stdin []byte) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), commandError(args[0], err, strings.TrimSpace(stderr.String()))
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
