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
const Version = 1

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
var infrastructure = []string{"nftables", "tinyproxy"}

// privilegedGroups are the groups that the agent must never be in.
var privilegedGroups = []string{"sudo", "admin", "adm", "docker", "lxd", "disk", "shadow"}

// ErrScriptInput reports input from which Script refuses to render the root
// script.
var ErrScriptInput = errors.New("invalid provisioning input")

// Script returns the root script that provisions a sandbox machine.
// defaultUID is the uid of the default user that OrbStack creates: the uid
// of the Mac user. The script is idempotent, keeps the machine fail-closed
// at each step, writes nothing into a home directory, and writes MarkerPath
// as its last action. The same input yields the same bytes.
func Script(defaultUID uint32) ([]byte, error) {
	if defaultUID == 0 {
		return nil, fmt.Errorf("%w: the default uid is 0", ErrScriptInput)
	}
	files, err := scriptFiles(defaultUID)
	if err != nil {
		return nil, err
	}
	sections := [][]string{
		prelude(),
		installPackages(),
		staticDNS(),
		writeFiles(files),
		startBoundary(),
		createAgent(),
		selfCheck(defaultUID),
		writeMarker(),
	}
	return []byte(lines(slices.Concat(sections...)...)), nil
}

// scriptFiles returns every file that the script writes, after the
// configuration is validated.
func scriptFiles(defaultUID uint32) ([]File, error) {
	firewall, err := Firewall(firewallInput(defaultUID))
	if err != nil {
		return nil, err
	}
	proxyFiles, err := proxy.Config(policy.DefaultAllowlist())
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

// prelude stops at the first failure, drops the proxy settings that a
// login of root gets from /etc/environment (root installs packages
// directly, not through the allowlist), and removes the marker, so that a
// failed run never leaves a machine that looks provisioned.
func prelude() []string {
	return []string{
		"#!/bin/bash",
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

// installPackages installs the toolchain and the infrastructure as root.
func installPackages() []string {
	packages := strings.Join(slices.Concat(toolchain, infrastructure), " ")
	return []string{
		"apt-get update -q",
		"apt-get install -y -q --no-install-recommends " + packages,
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
func startBoundary() []string {
	return []string{
		"systemctl daemon-reload",
		"systemctl enable " + strings.Join(boundaryUnits(), " ") + " >/dev/null",
		"systemctl start " + unitProxyAddress,
		"nft -f " + firewallPath,
		"systemctl start " + unitFirewall,
		"systemctl restart " + unitProxy,
		"systemctl restart " + unitHarden,
	}
}

// createAgent creates the agent user once, without any extra group, and
// refuses a uid below the floor.
func createAgent() []string {
	return []string{
		fmt.Sprintf("id -u %s >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --user-group %s", AgentUser, AgentUser),
		fmt.Sprintf("passwd -l %s >/dev/null", AgentUser),
		fmt.Sprintf(`[ "$(id -u %s)" -ge %d ] || fail "the uid of %s is below %d"`, AgentUser, uidFloor, AgentUser, uidFloor),
	}
}

// selfCheck verifies, as root, what the boundary depends on. Any failure
// stops the script before the marker is written.
func selfCheck(defaultUID uint32) []string {
	script := []string{
		`nft list table inet orbit_egress >/dev/null || fail "the firewall table is not loaded"`,
		"for unit in " + strings.Join(boundaryUnits(), " ") + "; do",
		`  systemctl is-active --quiet "$unit" || fail "$unit is not active"`,
		"done",
		"for unit in " + strings.Join(resolvedUnits, " ") + "; do",
		`  [ "$(systemctl is-enabled "$unit" 2>/dev/null || true)" = masked ] || fail "$unit is not masked"`,
		`  if systemctl is-active --quiet "$unit"; then fail "$unit is active"; fi`,
		"done",
		"getent passwd | while IFS=: read -r name _ uid _ _ home _; do",
		`  [ "$name" = root ] && continue`,
		`  if sudo -n -l -U "$name" 2>/dev/null | grep -q 'may run'; then fail "$name has a sudo rule"; fi`,
		fmt.Sprintf(`  if [ "$uid" -lt %d ] && [ "$uid" -ne %d ]; then`, uidFloor, defaultUID),
		`    case "$home" in /home/*) fail "$name has uid $uid below the floor and a home under /home";; esac`,
		`    if id -nG "$name" | tr ' ' '\n' | grep -qx orbstack; then fail "$name has uid $uid below the floor and the group orbstack"; fi`,
		"  fi",
		"done",
		fmt.Sprintf(`if runuser -u %s -- sudo -n true 2>/dev/null; then fail "%s can use sudo"; fi`, AgentUser, AgentUser),
		fmt.Sprintf("for group in %s; do", strings.Join(privilegedGroups, " ")),
		fmt.Sprintf(`  if id -nG %s | tr ' ' '\n' | grep -qx "$group"; then fail "%s is in the group $group"; fi`, AgentUser, AgentUser),
		"done",
	}
	return script
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
