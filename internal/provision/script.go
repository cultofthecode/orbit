package provision

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/cultofthecode/orbit/internal/policy"
	"github.com/cultofthecode/orbit/internal/proxy"
)

// Version is the provisioning version that the root script writes into the
// marker file as its last action.
const Version = 3

// MarkerPath is the file that exists only in a machine that provisioning
// completed. A later slice can refuse a machine without it.
const MarkerPath = "/etc/orbit/provisioned"

// AgentUser is the unprivileged user that runs the agent.
const AgentUser = "agent"

// firewallPath is where the distribution's nftables unit reads its rules.
const firewallPath = "/etc/nftables.conf"

// resolvConfPath is the resolver configuration of the machine. On Ubuntu it
// is a symlink into the runtime directory of systemd-resolved.
const resolvConfPath = "/etc/resolv.conf"

// environmentPath holds the environment of every login, read by pam_env.
const environmentPath = "/etc/environment"

// heredocDelimiter ends the content of every file in the root script.
const heredocDelimiter = "ORBIT_EOF"

// resolvedUnits are systemd-resolved and the sockets that would start it or
// answer queries for it.
var resolvedUnits = []string{"systemd-resolved.service", "systemd-resolved-varlink.socket", "systemd-resolved-monitor.socket"}

// orbStackDNS is the DNS server of OrbStack in a machine.
var orbStackDNS = netip.MustParseAddr("0.250.250.200")

// toolchain is the fixed toolchain of D20.
var toolchain = []string{"git", "openssh-client", "curl", "ca-certificates", "jq", "ripgrep", "unzip"}

// infrastructure holds the packages of the firewall and the proxy (D22).
var infrastructure = []string{"nftables", "tinyproxy", netcatPackage}

// forwardedSocketGroup is the group of the socket of the forwarded host SSH
// agent. The agent user is in it exactly when the machine forwards the agent.
const forwardedSocketGroup = "orbstack"

// privilegedGroups are the groups that the agent must never be in.
var privilegedGroups = []string{"sudo", "admin", "adm", "docker", "lxd", "disk", "shadow"}

// ErrScriptInput reports input from which Script refuses to render the root
// script.
var ErrScriptInput = errors.New("invalid provisioning input")

// ScriptInput is what Script needs to know about the machine.
type ScriptInput struct {
	// DefaultUID is the uid of the default user that OrbStack creates: the
	// uid of the Mac user.
	DefaultUID uint32
	// AgentDomains are the egress domains of the agent of the sandbox, nil
	// without an agent; the proxy allows them in addition to the base list
	// (policy.Allowlist).
	AgentDomains []string
	// ForwardSSHAgent is true when the machine forwards the SSH agent of the
	// host. The agent user joins the group of the socket only then.
	ForwardSSHAgent bool
}

// Script returns the root script that provisions a sandbox machine, as in
// describes it. The script is idempotent, keeps the machine
// fail-closed at each step, writes nothing into a home directory, and writes
// MarkerPath as its last action. The same input yields the same bytes.
func Script(in ScriptInput) ([]byte, error) {
	if in.DefaultUID == 0 {
		return nil, fmt.Errorf("%w: the default uid is 0", ErrScriptInput)
	}
	files, err := scriptFiles(in.DefaultUID, in.AgentDomains)
	if err != nil {
		return nil, err
	}
	sections := [][]string{
		prelude(),
		installPackages(),
		installDocker(),
		maskRootfulDocker(),
		staticDNS(),
		writeFiles(files),
		startBoundary(),
		createAgent(in.ForwardSSHAgent),
		helperPermissions(),
		startEngine(),
		selfCheck(in.DefaultUID, in.ForwardSSHAgent),
		writeMarker(),
	}
	return []byte(lines(group(slices.Concat(sections...))...)), nil
}

// scriptFiles returns every file that the script writes, after the
// configuration is validated.
func scriptFiles(defaultUID uint32, agentDomains []string) ([]File, error) {
	firewall, err := Firewall(firewallInput(defaultUID))
	if err != nil {
		return nil, err
	}
	allowlist, err := policy.Allowlist(agentDomains)
	if err != nil {
		return nil, err
	}
	proxyFiles, err := proxy.Config(allowlist)
	if err != nil {
		return nil, err
	}
	files := []File{
		{Path: firewallPath, Mode: 0o644, Content: firewall},
		{Path: proxy.ConfigPath, Mode: 0o644, Content: proxyFiles.Config},
		{Path: proxy.AllowlistPath, Mode: 0o644, Content: proxyFiles.Allowlist},
		{Path: environmentPath, Mode: 0o644, Content: environment()},
	}
	files = append(files, Units(defaultUID)...)
	files = append(files, sshFiles()...)
	return files, checkDelimiter(files)
}

// firewallInput returns the firewall input of a sandbox.
func firewallInput(defaultUID uint32) FirewallInput {
	return FirewallInput{
		DefaultUID: defaultUID,
		ProxyUser:  proxy.User,
		ProxyPort:  proxy.Port,
		ProxyAddrs: []netip.Addr{proxy.LoopbackAddr, proxy.ContainerAddr},
		Resolvers:  []netip.Addr{orbStackDNS},
	}
}

// checkDelimiter refuses a file whose content would end its heredoc early.
func checkDelimiter(files []File) error {
	for _, file := range files {
		if slices.Contains(strings.Split(file.Content, "\n"), heredocDelimiter) {
			return fmt.Errorf("%w: %s holds the line %s", ErrScriptInput, file.Path, heredocDelimiter)
		}
	}
	return nil
}

// environment returns /etc/environment: the default path of Ubuntu and the
// proxy for every login.
func environment() string {
	url := fmt.Sprintf("http://%s:%d", proxy.LoopbackAddr, proxy.Port)
	return lines(
		`PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games:/snap/bin"`,
		"HTTP_PROXY="+url,
		"HTTPS_PROXY="+url,
		"http_proxy="+url,
		"https_proxy="+url,
		"NO_PROXY=localhost,127.0.0.1",
		"no_proxy=localhost,127.0.0.1",
	)
}

// group puts the body of the script into one braced group. bash reads the
// script from standard input as it runs; with the group it parses the whole
// script before it runs any of it, so a child that reads standard input
// cannot consume the rest, and a truncated script fails to parse instead of
// exiting 0 early.
func group(body []string) []string {
	return slices.Concat([]string{"#!/bin/bash", "{"}, body, []string{"}"})
}

// prelude stops at the first failure, drops the proxy settings that a
// login of root gets from /etc/environment (root installs packages
// directly, not through the allowlist), and removes the marker, so that a
// failed run never leaves a machine that looks provisioned.
func prelude() []string {
	return []string{
		"set -euo pipefail",
		"umask 022",
		"cd /",
		"export DEBIAN_FRONTEND=noninteractive",
		"unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy NO_PROXY no_proxy",
		"fail() { echo \"orbit provisioning: $*\" >&2; exit 1; }",
		`put() { cat > "$1.orbit-new"; chown root:root "$1.orbit-new"; chmod "$2" "$1.orbit-new"; mv -f "$1.orbit-new" "$1"; }`,
		"rm -f " + MarkerPath,
	}
}

// aptGet runs apt-get with a wait for the dpkg lock, which apt-daily can
// hold right after the first boot.
const aptGet = "apt-get -o DPkg::Lock::Timeout=120"

// installPackages installs the toolchain and the infrastructure as root. It
// keeps the configuration files that Orbit wrote when a package upgrade
// brings new ones, and gives apt-get no standard input to ask on.
func installPackages() []string {
	packages := strings.Join(slices.Concat(toolchain, infrastructure), " ")
	return []string{
		aptGet + " update -q </dev/null",
		aptGet + " -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y -q --no-install-recommends " + packages + " </dev/null",
	}
}

// staticDNS masks systemd-resolved and its sockets, which would answer any
// query of the agent over D-Bus or varlink, and points the resolver of the
// machine at OrbStack DNS. The new resolv.conf replaces the symlink of the
// distribution.
func staticDNS() []string {
	units := strings.Join(resolvedUnits, " ")
	script := []string{
		"systemctl disable --now " + units + " 2>/dev/null || true",
		"systemctl mask " + units + " >/dev/null 2>&1",
	}
	return append(script, putFile(File{Path: resolvConfPath, Mode: 0o644, Content: "nameserver " + orbStackDNS.String() + "\n"})...)
}

// writeFiles writes every file.
func writeFiles(files []File) []string {
	var script []string
	for _, file := range files {
		script = append(script, "mkdir -p "+parentDir(file.Path))
		script = append(script, putFile(file)...)
	}
	return script
}

// putFile writes file from a quoted heredoc, so that the shell expands
// nothing in its content, through put: a temporary file that is renamed over
// the path. install cannot copy standard input over an existing file.
func putFile(file File) []string {
	return []string{
		fmt.Sprintf("put %s %04o <<'%s'", file.Path, file.Mode, heredocDelimiter),
		strings.TrimSuffix(file.Content, "\n"),
		heredocDelimiter,
	}
}

// startBoundary enables the units and starts them. It loads the rules with
// nft and never restarts the nftables unit, whose stop flushes the ruleset.
// It starts systemd-user-sessions.service last, because a restart that a
// required unit propagates does not start it again after it failed.
func startBoundary() []string {
	return []string{
		"systemctl daemon-reload",
		"systemctl enable " + strings.Join(append(boundaryUnits(), unitGate), " ") + " >/dev/null",
		"systemctl start " + unitProxyAddress,
		"nft -f " + firewallPath,
		"systemctl start " + unitFirewall,
		"systemctl restart " + unitProxy,
		"systemctl restart " + unitHarden,
		"systemctl start " + unitUserSessions,
	}
}

// createAgent creates the agent user once, without any extra group, and
// refuses a uid below the floor. It adds the agent to the group of the
// forwarded socket only when the machine forwards the SSH agent.
func createAgent(forwardSSHAgent bool) []string {
	script := []string{
		fmt.Sprintf("id -u %s >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group %s", AgentUser, AgentUser),
		fmt.Sprintf("passwd -l %s >/dev/null", AgentUser),
		fmt.Sprintf(`[ "$(id -u %s)" -ge %d ] || fail "the uid of %s is below %d"`, AgentUser, uidFloor, AgentUser, uidFloor),
	}
	if forwardSSHAgent {
		script = append(script, fmt.Sprintf("usermod -aG %s %s", forwardedSocketGroup, AgentUser))
	}
	return script
}

// selfCheck verifies, as root, what the boundary depends on, including that
// /run/nologin is absent, so PAM logins stay possible. Any failure stops the
// script before the marker is written.
func selfCheck(defaultUID uint32, forwardSSHAgent bool) []string {
	script := slices.Concat(boundaryCheck(), []string{
		`[ ! -e /run/nologin ] || fail "logins are disabled"`,
		"for unit in " + strings.Join(resolvedUnits, " ") + "; do",
		`  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || fail "$unit is not masked"`,
		`  if systemctl is-active --quiet "$unit"; then fail "$unit is active"; fi`,
		"done",
		"getent passwd | while IFS=: read -r name _ uid _ _ home _; do",
		`  [ "$name" = root ] && continue`,
		`  rules=$(sudo -n -l -U "$name" 2>/dev/null || true)`,
		`  case "$rules" in *"may run"*) fail "$name has a sudo rule";; esac`,
		fmt.Sprintf(`  if [ "$uid" -lt %d ] && [ "$uid" -ne %d ]; then`, uidFloor, defaultUID),
		`    case "$home" in /home/*) fail "$name has uid $uid below the floor and a home under /home";; esac`,
		`    case " $(id -nG "$name") " in *" orbstack "*) fail "$name has uid $uid below the floor and the group orbstack";; esac`,
		"  fi",
		"done",
		fmt.Sprintf(`if runuser -u %s -- sudo -n true 2>/dev/null; then fail "%s can use sudo"; fi`, AgentUser, AgentUser),
		fmt.Sprintf("for group in %s; do", strings.Join(privilegedGroups, " ")),
		fmt.Sprintf(`  case " $(id -nG %s) " in *" $group "*) fail "%s is in the group $group";; esac`, AgentUser, AgentUser),
		"done",
	}, sshCheck(forwardSSHAgent), dockerCheck())
	return script
}

// sshCheck fails unless the SSH files exist, netcatPath belongs to
// netcatPackage, and the agent user is in the group of the forwarded socket
// exactly when the machine forwards the SSH agent: a membership in the wrong
// direction fails too.
func sshCheck(forwardSSHAgent bool) []string {
	script := []string{
		fmt.Sprintf(`[ -f %s ] || fail "%s is missing"`, sshConfigPath, sshConfigPath),
		fmt.Sprintf(`[ -f %s ] || fail "%s is missing"`, knownHostsPath, knownHostsPath),
		fmt.Sprintf(`owner=$(dpkg -S %s 2>/dev/null || true)`, netcatPath),
		fmt.Sprintf(`[ "$owner" = "%s: %s" ] || fail "%s does not belong to %s"`, netcatPackage, netcatPath, netcatPath, netcatPackage),
	}
	member := fmt.Sprintf(`case " $(id -nG %s) " in *" %s "*)`, AgentUser, forwardedSocketGroup)
	if forwardSSHAgent {
		return append(script, member+`;; *) fail "`+AgentUser+` is not in the group `+forwardedSocketGroup+`";; esac`)
	}
	return append(script, member+` fail "`+AgentUser+` is in the group `+forwardedSocketGroup+`";; esac`)
}

// boundaryCheck fails, through the shell function fail, unless the firewall
// table is loaded and every boundary unit and systemd-user-sessions.service
// is active. An active nftables.service alone does not prove the table.
func boundaryCheck() []string {
	return unitsCheck(append(boundaryUnits(), unitUserSessions))
}

// boundaryUnitsCheck is boundaryCheck without
// systemd-user-sessions.service: the gate that user managers wait for cannot
// wait for the unit that ends the login gate.
func boundaryUnitsCheck() []string {
	return unitsCheck(boundaryUnits())
}

// unitsCheck fails unless the firewall table is loaded and every unit of
// units is active.
func unitsCheck(units []string) []string {
	return []string{
		`nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`,
		"for unit in " + strings.Join(units, " ") + "; do",
		`  systemctl is-active --quiet "$unit" || fail "$unit is not active"`,
		"done",
	}
}

// writeMarker writes MarkerPath as the last action of the script.
func writeMarker() []string {
	script := []string{"mkdir -p " + parentDir(MarkerPath)}
	return append(script, putFile(File{Path: MarkerPath, Mode: 0o644, Content: fmt.Sprintln(Version)})...)
}

// parentDir returns the directory of an absolute path.
func parentDir(path string) string {
	return path[:strings.LastIndex(path, "/")]
}
