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
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
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

// installURL is where a user finds OrbStack.
const installURL = "https://orbstack.dev"

// stateDir is the directory under $HOME through which orb finds OrbStack.
const stateDir = ".orbstack"

// allowedVariables are the only host variables that executeStreams passes to
// orb, matched by exact name; it removes every other one. orb copies some host
// variables into the command that it runs in a machine, as root and as the
// agent, and ORBENV and WSLENV make it copy every host variable that they
// name (OrbStack 2.2.5), so a host credential could otherwise enter a sandbox
// without an opt-in (R08). The owner chose this allowlist over a list of
// forbidden names (#76): a variable that a new version of OrbStack starts to
// forward stays on the host.
//
// OrbStack 2.2.5, probed with only these variables set, runs every command of
// the adapter with HOME alone and panics without it ("$HOME is not
// defined"): orb finds OrbStack through $HOME/.orbstack (#70). PATH and
// TMPDIR are what orb, a Go program, reads to find a program it starts and a
// directory for temporary files; orb does not copy them into a machine. orb
// copies TERM, COLORTERM, and the locale variables into the machine, where
// a terminal needs them: without TERM a command in the machine gets
// TERM=dumb.
var allowedVariables = []string{
	"HOME", "PATH", "TMPDIR",
	"TERM", "COLORTERM",
	"LANG", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
}

// stateRunning is the only state of a machine in which the client runs a
// command in it.
const stateRunning = "running"

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
	// ErrMachineList reports a list of machines that does not say for
	// certain in which state a machine is.
	ErrMachineList = errors.New("machine list cannot be read")
	// ErrMachineNotRunning reports a machine that is not in the state
	// running, or does not exist. Every orb -m command starts a stopped
	// machine, so the client runs none of them then.
	ErrMachineNotRunning = errors.New("machine is not running")
	// ErrMachineName reports a machine name with white space. A name that
	// sandbox.MachineName computes has none, and Start, Stop, and Delete pass
	// only such a name to orb.
	ErrMachineName = errors.New("machine name holds white space")
	// ErrNotInstalled reports that orb is not on PATH, so OrbStack is not
	// installed or orb cannot be found. The client then runs no other command.
	// The error wraps exec.ErrNotFound as well.
	ErrNotInstalled = errors.New("OrbStack is not installed or orb is not on PATH")
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
// the system, which does not read HOME on macOS. A build with the osusergo
// tag falls back to HOME, and then only the .orbstack check can name HOME.
// Tests replace it.
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
// same absolute path as on the host, and forwards the SSH agent of the host
// into it only when forwardSSHAgent is true. OrbStack fixes that setting when
// it creates the machine. It runs no command when name lacks the prefix of
// the client or dir cannot be mounted.
func (c *Client) Create(ctx context.Context, name, dir string, forwardSSHAgent bool) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if err := checkMountable(dir); err != nil {
		return err
	}
	if _, err := run(ctx, createArgs(name, dir, forwardSSHAgent), nil); err != nil {
		return fmt.Errorf("create machine %s: %w", name, err)
	}
	return nil
}

// SSHAgentForwarded reports whether the machine name forwards the SSH agent
// of the host, as orb list prints it. It runs only orb status and orb list,
// so it never starts a machine. It fails when no machine has that name, when
// the entry has no boolean setting, and when the name lacks the prefix of the
// client, and then it runs no command.
func (c *Client) SSHAgentForwarded(ctx context.Context, name string) (bool, error) {
	if err := c.checkOwned(name); err != nil {
		return false, err
	}
	list, err := run(ctx, mountsArgs(), nil)
	if err != nil {
		return false, fmt.Errorf("list machines: %w", err)
	}
	return parseSSHAgentForwarded([]byte(list), name)
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
// machine still shows as the stored path. It never starts the machine: it
// fails with ErrMachineNotRunning, and runs no command in the machine,
// unless State reports it running. It runs no command when name lacks the
// prefix of the client.
func (c *Client) ActiveMounts(ctx context.Context, name string) (map[string]string, error) {
	if err := c.checkRunningMachine(ctx, name); err != nil {
		return nil, err
	}
	table, err := run(ctx, activeMountsArgs(name), nil)
	if err != nil {
		return nil, fmt.Errorf("read the mounts of machine %s: %w", name, err)
	}
	return parseMountTable(table)
}

// State returns the state of the machine name as orb list prints it, such
// as "running" or "stopped", or "" when no machine has that name. It runs
// only orb status and orb list, so it never starts a machine. It runs no
// command when name lacks the prefix of the client.
func (c *Client) State(ctx context.Context, name string) (string, error) {
	if err := c.checkOwned(name); err != nil {
		return "", err
	}
	list, err := run(ctx, mountsArgs(), nil)
	if err != nil {
		return "", fmt.Errorf("list machines: %w", err)
	}
	return parseState([]byte(list), name)
}

// Machine is a machine of OrbStack with the name prefix of the client, as
// orb list prints it.
type Machine struct {
	Name   string
	State  string
	Mounts []Mount
}

// Mount is one stored mount of a machine: the host directory and the path in
// the machine.
type Mount struct {
	Source      string
	Destination string
}

// List returns every machine whose name starts with the prefix of the client,
// with its state and its stored mounts, from one read of orb list. It runs
// only orb status and orb list, so it never starts OrbStack or a machine, and
// it runs no command in a machine.
func (c *Client) List(ctx context.Context) ([]Machine, error) {
	list, err := run(ctx, mountsArgs(), nil)
	if err != nil {
		return nil, err
	}
	return parseList([]byte(list), c.prefix)
}

// Start starts the machine name, and only that machine: orb start without a
// name starts every machine that ran when OrbStack last stopped. It runs no
// command when name lacks the prefix of the client or holds white space.
func (c *Client) Start(ctx context.Context, name string) error {
	if err := c.checkNamed(name); err != nil {
		return err
	}
	if _, err := run(ctx, startArgs(name), nil); err != nil {
		return fmt.Errorf("start machine %s: %w", name, err)
	}
	return nil
}

// Stop stops the machine name, and only that machine: orb stop without a
// name stops OrbStack with every machine. It keeps the disk of the machine.
// It runs no command when name lacks the prefix of the client or holds
// white space.
func (c *Client) Stop(ctx context.Context, name string) error {
	if err := c.checkNamed(name); err != nil {
		return err
	}
	if _, err := run(ctx, stopArgs(name), nil); err != nil {
		return fmt.Errorf("stop machine %s: %w", name, err)
	}
	return nil
}

// Delete deletes the machine name with everything stored in it. It runs no
// command when name lacks the prefix of the client or holds white space,
// because orb delete takes several names and -a deletes every machine.
func (c *Client) Delete(ctx context.Context, name string) error {
	if err := c.checkNamed(name); err != nil {
		return err
	}
	if _, err := run(ctx, deleteArgs(name), nil); err != nil {
		return fmt.Errorf("delete machine %s: %w", name, err)
	}
	return nil
}

// RunAsRoot runs script with bash as root in the machine name, with the
// script on standard input, and fails when the script exits non-zero. It
// fails with ErrMachineNotRunning, and runs no command in the machine,
// unless State reports it running. It runs no command when name lacks the
// prefix of the client.
func (c *Client) RunAsRoot(ctx context.Context, name string, script []byte) error {
	if err := c.checkRunningMachine(ctx, name); err != nil {
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
// fails with ErrMachineNotRunning, and runs no command in the machine,
// unless State reports it running. It runs no command when name lacks the
// prefix of the client.
func (c *Client) RunAsAgent(ctx context.Context, name string, script []byte) error {
	if err := c.checkRunningMachine(ctx, name); err != nil {
		return err
	}
	if _, err := run(ctx, runAsAgentArgs(name), script); err != nil {
		return fmt.Errorf("run a script as %s in machine %s: %w", AgentUser, name, err)
	}
	return nil
}

// Shell runs the login shell of AgentUser in the machine name, in the
// directory dir, with in, out, and errOut attached directly to orb, so that
// orb gives the shell a terminal when in is one. It returns the exit status
// of orb, which is that of the shell, and an error only when orb could not
// run. While orb runs, the process catches SIGINT and SIGQUIT, so that the
// signal ends orb and not the caller. The user is fixed, and dir is the
// only value of the caller besides name on the command line. It fails with
// ErrMachineNotRunning, and runs no command in the machine, unless State
// reports it running. It runs no command when name lacks the prefix of the
// client or dir cannot be mounted.
func (c *Client) Shell(ctx context.Context, name, dir string, in io.Reader, out, errOut io.Writer) (int, error) {
	if err := c.checkOwned(name); err != nil {
		return 0, err
	}
	if err := checkMountable(dir); err != nil {
		return 0, err
	}
	if err := c.checkRunningMachine(ctx, name); err != nil {
		return 0, err
	}
	var status int
	err := holdingSignals(func() error {
		var err error
		status, err = exitStatus(executeStreams(ctx, shellArgs(name, dir), in, out, errOut))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("run a shell as %s in machine %s: %w", AgentUser, name, err)
	}
	return status, nil
}

// checkRunningMachine refuses a name that checkOwned refuses and a machine
// whose state is not running, read with State, which never starts a machine.
// It guards every orb -m command, because orb starts a stopped machine by
// itself. The machine can stop between this check and the command that
// follows; that window is accepted, as for OrbStack (#62, #75).
func (c *Client) checkRunningMachine(ctx context.Context, name string) error {
	state, err := c.State(ctx, name)
	if err != nil {
		return err
	}
	switch state {
	case stateRunning:
		return nil
	case "":
		return fmt.Errorf("%w: machine %q does not exist", ErrMachineNotRunning, name)
	}
	return fmt.Errorf("%w: machine %q is %s", ErrMachineNotRunning, name, state)
}

// checkOwned refuses a name that does not consist of the prefix of the client
// and at least one more character.
func (c *Client) checkOwned(name string) error {
	if c.prefix == "" || !strings.HasPrefix(name, c.prefix) || name == c.prefix {
		return fmt.Errorf("%w %q: %q", ErrForeignMachine, c.prefix, name)
	}
	return nil
}

// checkNamed refuses a name that checkOwned refuses and a name with white
// space.
func (c *Client) checkNamed(name string) error {
	if err := c.checkOwned(name); err != nil {
		return err
	}
	if strings.ContainsFunc(name, unicode.IsSpace) {
		return fmt.Errorf("%w: %q", ErrMachineName, name)
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

// forwardFlag is the flag of orb create that forwards the SSH agent of the
// host into the machine.
const forwardFlag = "--forward-ssh-agent"

// createArgs returns the arguments of orb that create the machine name. The
// machine is always isolated, also on the network, and dir is always mounted
// at its own path: a mount without a destination lands under /mnt/mac. The
// flag that forwards the SSH agent is there only when forward is true.
func createArgs(name, dir string, forward bool) []string {
	args := []string{"create", "--isolated", "--isolate-network"}
	if forward {
		args = append(args, forwardFlag)
	}
	return append(args, "--mount", dir+mountSeparator+dir, image, name)
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

// startArgs returns the arguments of orb that start the machine name.
func startArgs(name string) []string {
	return []string{"start", name}
}

// stopArgs returns the arguments of orb that stop the machine name.
func stopArgs(name string) []string {
	return []string{"stop", name}
}

// machine is the part of an entry of "orb list --format json" that the
// adapter reads.
type machine struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Config struct {
		ForwardSSHAgent *bool `json:"forward_ssh_agent"`
		Mounts          []struct {
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

// parseList returns the machines of the output of "orb list --format json"
// whose names start with prefix, in the order of the output, with their state
// as it is (also "") and their mounts in the order of config.mounts. OrbStack
// prints [] when it has no machine (OrbStack 2.2.5); null is read as no
// machines too, as encoding/json reads it. Empty output is never read as no
// machines: it fails with ErrMachineList, like output that is not a JSON array
// and a prefixed name that appears twice. It drops a name without the prefix
// and the name equal to the prefix, as checkOwned does; an empty prefix lists
// nothing.
func parseList(list []byte, prefix string) ([]Machine, error) {
	var entries []machine
	if err := json.Unmarshal(list, &entries); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMachineList, err)
	}
	var machines []Machine
	seen := map[string]bool{}
	for _, e := range entries {
		if prefix == "" || !strings.HasPrefix(e.Name, prefix) || e.Name == prefix {
			continue
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("%w: machine %q is listed twice", ErrMachineList, e.Name)
		}
		seen[e.Name] = true
		machines = append(machines, toMachine(e))
	}
	return machines, nil
}

// toMachine converts an entry of orb list to a Machine.
func toMachine(e machine) Machine {
	m := Machine{Name: e.Name, State: e.State}
	for _, mount := range e.Config.Mounts {
		m.Mounts = append(m.Mounts, Mount{Source: mount.Source, Destination: mount.Destination})
	}
	return m
}

// parseSSHAgentForwarded returns the SSH agent setting of the machine name in
// the output of "orb list --format json". It fails on output that it cannot
// read, on a machine that is not listed or is listed twice, and on an entry
// whose setting is missing or not a boolean: it never assumes a default.
func parseSSHAgentForwarded(list []byte, name string) (bool, error) {
	var machines []machine
	if err := json.Unmarshal(list, &machines); err != nil {
		return false, fmt.Errorf("%w: %w", ErrMachineList, err)
	}
	var settings []*bool
	for _, m := range machines {
		if m.Name == name {
			settings = append(settings, m.Config.ForwardSSHAgent)
		}
	}
	switch {
	case len(settings) == 0:
		return false, fmt.Errorf("%w: machine %q is not listed", ErrMachineList, name)
	case len(settings) > 1:
		return false, fmt.Errorf("%w: machine %q is listed twice", ErrMachineList, name)
	case settings[0] == nil:
		return false, fmt.Errorf("%w: machine %q has no config.forward_ssh_agent", ErrMachineList, name)
	}
	return *settings[0], nil
}

// parseState returns the state of the machine name in the output of "orb
// list --format json", or "" when no entry has that name. It fails on output
// that it cannot read, on a name that appears twice, and on an entry of name
// without a state.
func parseState(list []byte, name string) (string, error) {
	var machines []machine
	if err := json.Unmarshal(list, &machines); err != nil {
		return "", fmt.Errorf("%w: %w", ErrMachineList, err)
	}
	var states []string
	for _, m := range machines {
		if m.Name == name {
			states = append(states, m.State)
		}
	}
	switch {
	case len(states) > 1:
		return "", fmt.Errorf("%w: machine %q is listed twice", ErrMachineList, name)
	case len(states) == 1 && states[0] == "":
		return "", fmt.Errorf("%w: machine %q has no state", ErrMachineList, name)
	case len(states) == 1:
		return states[0], nil
	}
	return "", nil
}

// parseMountTable returns the mounts of the host file system in a mount table
// in the format of /proc/self/mountinfo: the root of each mount, which is a
// path on the host, by its mount point. It fails on an entry that it cannot
// read, on a mount point that appears twice, and on a table with no content:
// a real table always lists the root file system, so empty output means that
// the read failed (orb exited 0 and printed nothing), not that nothing is
// mounted.
func parseMountTable(table string) (map[string]string, error) {
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("%w: the mount table is empty", ErrMountTable)
	}
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

// shellArgs returns the arguments of orb that run the login shell of
// AgentUser in the machine name with dir as its working directory. Without
// a command, orb starts the shell of the user from /etc/passwd as a login
// shell; without -w, it starts in the home directory of the user.
func shellArgs(name, dir string) []string {
	return []string{"-m", name, "-u", AgentUser, "-w", dir}
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
// starting one otherwise, ErrNotInstalled for an orb that cannot be found,
// and an error for anything else.
func statusError(status string, err error, homeCause string) error {
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%w (%w); install OrbStack with `brew install --cask orbstack` or from %s, make sure orb is on PATH, and try again", ErrNotInstalled, err, installURL)
	case isNotRunning(status) && homeCause != "":
		return fmt.Errorf("%w (orb status: %s): orb finds OrbStack through $HOME/%s, so OrbStack may still be running; %s", ErrHome, status, stateDir, homeCause)
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
// $HOME/.orbstack is a directory. An account home that is the same directory
// as HOME counts as HOME.
func readHome() home {
	h := home{env: canonical(os.Getenv("HOME"))}
	if account, err := currentUser(); err == nil && account.HomeDir != "" {
		h.account = sameDirOrSelf(canonical(account.HomeDir), h.env)
	}
	if filepath.IsAbs(h.env) {
		info, err := os.Stat(filepath.Join(h.env, stateDir))
		h.hasStateDir = err == nil && info.IsDir()
	}
	return h
}

// sameDirOrSelf returns other when path and other name the same directory,
// also through a firmlink or another letter case, and path otherwise.
func sameDirOrSelf(path, other string) string {
	pathInfo, pathErr := os.Stat(path)
	otherInfo, otherErr := os.Stat(other)
	if pathErr == nil && otherErr == nil && os.SameFile(pathInfo, otherInfo) {
		return other
	}
	return path
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
		return fmt.Sprintf("HOME %q is not an absolute path: set HOME to the home directory of the account and try again", h.env)
	case h.account != "" && h.env != h.account:
		return fmt.Sprintf("HOME %q is not the home directory of the account, %q: set HOME to that directory and try again", h.env, h.account)
	case !h.hasStateDir:
		return fmt.Sprintf("%q is not a directory: start OrbStack once as this account, or set HOME to the directory that holds .orbstack, and try again", filepath.Join(h.env, stateDir))
	}
	return ""
}

// execute runs orb with args and stdin and returns its standard output, also
// when the command fails. The error of a failed command carries the
// standard error of orb.
func execute(ctx context.Context, args []string, stdin []byte) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := executeStreams(ctx, args, bytes.NewReader(stdin), &stdout, &stderr); err != nil {
		return stdout.String(), commandError(args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// executeStreams runs orb with args and the three streams attached as they
// are. Every orb command of the client runs through it. orb gets only the
// variables of allowedVariables (HOME, PATH, TMPDIR, TERM, COLORTERM, LANG,
// and the LC_ variables of POSIX) from the environment of the process.
func executeStreams(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Env = allowedEnvironment(os.Environ())
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}

// allowedEnvironment returns a copy of environ, a list of NAME=value entries,
// with only the entries of allowedVariables, in their order. It removes a name
// that differs from an allowed one only in letter case or by a prefix or a
// suffix, because the names of the environment are case-sensitive.
func allowedEnvironment(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), isForbidden)
}

// isForbidden reports whether entry sets a variable that is not in
// allowedVariables.
func isForbidden(entry string) bool {
	name, _, _ := strings.Cut(entry, "=")
	return !slices.Contains(allowedVariables, name)
}

// exitStatus returns the exit status of a command that ended with err: 0
// for nil, the exit code for an exit, and 128 plus the number of the signal
// for a command that a signal ended, as a shell reports it. Any other error
// means that the command did not run, and exitStatus returns it.
func exitStatus(err error) (int, error) {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 0, err
	}
	if wait, ok := exit.Sys().(syscall.WaitStatus); ok && wait.Signaled() {
		return 128 + int(wait.Signal()), nil
	}
	return exit.ExitCode(), nil
}

// heldSignals are the signals of the terminal that holdingSignals catches.
var heldSignals = []os.Signal{os.Interrupt, syscall.SIGQUIT}

// holdingSignals runs fn while the process catches heldSignals, so that
// they do not end it, and restores their handling afterwards. A child that
// fn starts gets them with their default action: a caught signal, unlike an
// ignored one, does not pass to the child as ignored.
func holdingSignals(fn func() error) error {
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, heldSignals...)
	defer signal.Stop(caught)
	return fn()
}

// commandError wraps the error of the failed orb subcommand and appends what
// orb printed to its standard error, when it printed anything.
func commandError(subcommand string, err error, stderr string) error {
	if stderr == "" {
		return fmt.Errorf("%s %s: %w", cli, subcommand, err)
	}
	return fmt.Errorf("%s %s: %w: %s", cli, subcommand, err, stderr)
}
