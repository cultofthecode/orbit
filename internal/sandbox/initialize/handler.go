package initialize

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strings"
	"time"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/hostcheck"
)

// discardTimeout bounds the deletion of a machine whose provisioning or
// install failed.
const discardTimeout = 2 * time.Minute

// stateRunning is the only state of an existing machine in which the handler
// runs a command in it.
const stateRunning = "running"

// Machines is what the handler needs from OrbStack.
type Machines interface {
	// State returns the state of the machine name, or "" when no machine
	// has that name. It never starts a machine.
	State(ctx context.Context, name string) (string, error)
	// Mounts returns the configured mounts of every machine: for each
	// machine name, the host directory of each mount by its mount point.
	Mounts(ctx context.Context) (map[string]map[string]string, error)
	// ActiveMounts returns what the machine name mounts from the host, as
	// its kernel reports it: the host path of each mounted directory by its
	// mount point. It names the path, not the identity of the directory.
	// It fails for a machine that is not running.
	ActiveMounts(ctx context.Context, name string) (map[string]string, error)
	// Distribution returns the key of the distribution that the existing
	// machine name was created with. It reads what OrbStack stores, runs no
	// command in the machine, and never starts it. It fails when OrbStack
	// stores an image that no distribution has.
	Distribution(ctx context.Context, name string) (string, error)
	// SSHAgentForwarded reports whether the machine name forwards the SSH
	// agent of the host. It runs nothing in the machine.
	SSHAgentForwarded(ctx context.Context, name string) (bool, error)
	// Create creates the isolated machine name from the pinned image of the
	// distribution key, with dir as its only mount, at the same absolute
	// path as on the host, and forwards the SSH agent of the host only when
	// forwardSSHAgent is true.
	Create(ctx context.Context, name, dir, distribution string, forwardSSHAgent bool) error
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
	// that runs the distribution key distribution, whose default user has
	// the uid defaultUID, whose proxy allows
	// agentDomains in addition to the base list, and which forwards the SSH
	// agent of the host when forwardSSHAgent is true. cmd/orbit passes
	// provision.Script through a function that fills its input; the slice
	// does not import another module.
	ProvisionScript func(distribution string, defaultUID uint32, agentDomains []string, forwardSSHAgent bool) ([]byte, error)
	// DockerSetupScript returns the script that sets up the Docker client of
	// the agent user in a provisioned machine. cmd/orbit passes
	// provision.DockerSetupScript; the slice does not import another module.
	DockerSetupScript func() []byte
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
	// forwardSSHAgent is true unless Input.NoSSHForwarding is set.
	forwardSSHAgent bool
	// requested is Input.Distro: the distribution that the user asked for, or
	// empty.
	requested string
	// distribution is the distribution of the machine, once resolve ran: the
	// requested one for a machine that this call creates, the stored one for
	// a machine that exists.
	distribution sandbox.Distribution
	// scripts holds the root script of every distribution, built before the
	// handler asks OrbStack anything.
	scripts map[sandbox.Distribution][]byte
	// provision is the root script of the distribution, once resolve ran.
	provision []byte
	// dockerSetup is the client setup script of the agent user.
	dockerSetup []byte
	// install is the install script of the agent, nil without an agent.
	install []byte
}

// Handle creates the machine of the project directory in.Dir, unless it
// already exists, in which case it refuses a machine that is not running
// with ErrNotRunning, before it runs a command in it, checks that the machine forwards the SSH agent as the
// request says, which on an existing machine runs no command in it, checks
// that the machine mounts exactly that directory, provisions it, also when it already existed, sets up the Docker client of the agent user, and then installs the agent
// in.Agent as the agent user, when in.Agent is set. The allowlist of the
// proxy is the base list plus the egress domains of in.Agent, so a run
// without an agent removes the domains of an earlier one. It refuses an
// unknown distribution, and a distribution other than the one an existing
// machine runs (ErrDistributionMismatch), an unknown agent, a directory that the mount rules of the sandbox module do
// not accept, and a provisioning script that cannot be built before it asks
// OrbStack anything, and a directory that is, lies inside, or contains the
// project directory of another machine before it creates one. When the
// forwarding check, the mount check, provisioning, the client setup, or the install fails on a
// machine that this call created, it deletes that machine; it never deletes a machine that
// existed before the call.
func (h Handler) Handle(ctx context.Context, in Input) (Output, error) {
	p, err := h.prepare(in)
	if err != nil {
		return Output{}, err
	}
	p, created, err := h.checkedMachine(ctx, p)
	if err != nil {
		return Output{}, err
	}
	if err := h.setUp(ctx, p, created); err != nil {
		return Output{}, err
	}
	return Output{Name: p.name, Dir: p.dir, Created: created, Distribution: p.distribution}, nil
}

// prepare resolves the agent, validates the project directory, and builds the
// scripts, without asking OrbStack anything.
func (h Handler) prepare(in Input) (plan, error) {
	if err := checkRequested(in.Distro); err != nil {
		return plan{}, err
	}
	agent, err := h.agent(in.Agent)
	if err != nil {
		return plan{}, err
	}
	facts, err := hostcheck.ProjectDir(in.Dir, h.CurrentUser)
	if err != nil {
		return plan{}, err
	}
	scripts, err := h.provisionScripts(facts.UserUID, agent.EgressDomains, !in.NoSSHForwarding)
	if err != nil {
		return plan{}, err
	}
	p := plan{name: sandbox.MachineName(facts.Path), dir: facts.Path, forwardSSHAgent: !in.NoSSHForwarding, requested: in.Distro, scripts: scripts, dockerSetup: h.DockerSetupScript()}
	if in.Agent != "" {
		p.install = installScript(agent.Install)
	}
	return p, nil
}

// checkRequested refuses a --distro value that is not a distribution. An
// empty value means that the user named none.
func checkRequested(requested string) error {
	if requested == "" {
		return nil
	}
	_, err := sandbox.ParseDistribution(requested)
	return err
}

// provisionScripts builds the root script of every distribution. Building all
// of them before the first OrbStack call makes every one of them fail on the
// same bad input, whichever distribution the machine turns out to run.
func (h Handler) provisionScripts(uid uint32, agentDomains []string, forwardSSHAgent bool) (map[sandbox.Distribution][]byte, error) {
	scripts := map[sandbox.Distribution][]byte{}
	for _, d := range sandbox.Distributions() {
		script, err := h.ProvisionScript(string(d), uid, agentDomains, forwardSSHAgent)
		if err != nil {
			return nil, fmt.Errorf("provisioning script for %s: %w", d, err)
		}
		scripts[d] = script
	}
	return scripts, nil
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

// checkedMachine creates the machine of p, unless it exists, after it
// checked that its directory is not nested with the directory of another
// machine, refuses an existing machine that is not running, resolves its
// distribution, checks its SSH agent forwarding, which
// reads only the list of OrbStack, and then its mounts, which is the first
// command that runs in the machine. It returns p with its distribution and
// its root script, and reports whether it created the machine.
func (h Handler) checkedMachine(ctx context.Context, p plan) (plan, bool, error) {
	machines, err := h.unnestedMachines(ctx, p.name, p.dir)
	if err != nil {
		return p, false, err
	}
	_, exists := machines[p.name]
	if exists {
		if err := h.checkRunning(ctx, p.name); err != nil {
			return p, false, err
		}
	}
	p, err = h.resolve(ctx, p, exists)
	if err != nil {
		return p, false, err
	}
	created, err := h.ensureMachine(ctx, p, exists)
	if err != nil {
		return p, false, err
	}
	if err := h.verifyForwarding(ctx, p, created); err != nil {
		return p, false, err
	}
	return p, created, h.verifyMounts(ctx, p.name, p.dir, created)
}

// resolve sets the distribution and the root script of p. For a machine that
// exists it reads the stored distribution, which runs no command in the
// machine, and refuses a request for another one with
// sandbox.ErrDistributionMismatch, before anything runs in the machine. An
// existing machine that is not running is refused before this.
func (h Handler) resolve(ctx context.Context, p plan, exists bool) (plan, error) {
	var stored sandbox.Distribution
	if exists {
		key, err := h.Machines.Distribution(ctx, p.name)
		if err != nil {
			return p, fmt.Errorf("read the distribution of sandbox %s: %w", p.name, err)
		}
		stored = sandbox.Distribution(key)
	}
	dist, err := sandbox.ResolveDistribution(p.requested, stored, exists)
	if err != nil {
		return p, fmt.Errorf("sandbox %s: %w", p.name, err)
	}
	p.distribution = dist
	p.provision = p.scripts[dist]
	return p, nil
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

// runScripts runs the root script of p, then the setup of the Docker client
// as the agent user, and then, when p has an agent, its install script as the
// agent user.
func (h Handler) runScripts(ctx context.Context, p plan) error {
	if err := h.Machines.RunAsRoot(ctx, p.name, p.provision); err != nil {
		return fmt.Errorf("provision sandbox %s: %w", p.name, err)
	}
	if err := h.Machines.RunAsAgent(ctx, p.name, p.dockerSetup); err != nil {
		return fmt.Errorf("set up the Docker client in sandbox %s: %w", p.name, err)
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
	if err := hostcheck.Unnested(machines, name, dir, false); err != nil {
		return nil, err
	}
	return machines, nil
}

// ensureMachine creates the machine of p with its mount and its SSH agent
// forwarding unless it exists, and reports whether it created it.
func (h Handler) ensureMachine(ctx context.Context, p plan, exists bool) (bool, error) {
	if exists {
		return false, nil
	}
	if err := h.Machines.Create(ctx, p.name, p.dir, string(p.distribution), p.forwardSSHAgent); err != nil {
		return false, fmt.Errorf("create sandbox %s: %w", p.name, err)
	}
	return true, nil
}

// checkRunning fails with ErrSandboxGone when the machine name no longer
// exists, and with ErrNotRunning unless the existing machine name is
// running. It reads the state with State, which never starts a machine, and
// runs before any command in the machine.
func (h Handler) checkRunning(ctx context.Context, name string) error {
	state, err := h.Machines.State(ctx, name)
	if err != nil {
		return fmt.Errorf("state of sandbox %s: %w", name, err)
	}
	if state == "" {
		return fmt.Errorf("sandbox %s: %w", name, ErrSandboxGone)
	}
	if state != stateRunning {
		return fmt.Errorf("sandbox %s is %q: %w, then run \"orbit init\" again; init never starts a sandbox", name, state, ErrNotRunning)
	}
	return nil
}

// verifyForwarding checks that the machine of p forwards the SSH agent as p
// says. When created is true and the check fails, it deletes the machine.
// On an existing machine it changes nothing.
func (h Handler) verifyForwarding(ctx context.Context, p plan, created bool) error {
	err := h.checkForwarding(ctx, p)
	if err == nil || !created {
		return err
	}
	return h.discard(ctx, p.name, err)
}

// checkForwarding reads the SSH agent forwarding of the machine of p and
// compares it with the request.
func (h Handler) checkForwarding(ctx context.Context, p plan) error {
	have, err := h.Machines.SSHAgentForwarded(ctx, p.name)
	if err != nil {
		return fmt.Errorf("read the SSH agent forwarding of sandbox %s: %w", p.name, err)
	}
	if err := sandbox.CheckSSHForwarding(p.forwardSSHAgent, have); err != nil {
		return fmt.Errorf("sandbox %s: %w", p.name, err)
	}
	return nil
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
