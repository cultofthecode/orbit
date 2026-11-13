package provision

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/cultofthecode/orbit/internal/proxy"
)

// File is one file that the root script writes, owned by root.
type File struct {
	// Path is the absolute path of the file in the machine.
	Path string
	// Mode is the permission of the file.
	Mode fs.FileMode
	// Content is the whole content of the file.
	Content string
}

// Unit names that the root script enables and that user sessions require.
const (
	unitFirewall     = "nftables.service"
	unitProxyAddress = "orbit-proxy-address.service"
	unitProxy        = "tinyproxy.service"
	unitHarden       = "orbit-harden.service"
	unitUserSessions = "systemd-user-sessions.service"
)

// systemdDir is where the units of root live.
const systemdDir = "/etc/systemd/system/"

// hardenPath is the path of the script that removes sudo from every account
// except root.
const hardenPath = "/usr/local/libexec/orbit-harden"

// nobodyUID is the uid of the account nobody. It has no password to lock.
const nobodyUID = 65534

// proxyInterface is the dummy interface that carries proxy.ContainerAddr.
const proxyInterface = "orbit0"

// Units returns the unit files, the drop-ins, and the harden script. Every
// unit of the boundary starts before systemd-user-sessions.service, and
// that service requires all of them, so that no user session starts while
// one of them has failed. defaultUID is the uid of the default user, whose
// password the harden script locks.
func Units(defaultUID uint32) []File {
	return append([]File{
		{Path: systemdDir + unitProxyAddress, Mode: 0o644, Content: proxyAddressUnit()},
		{Path: systemdDir + unitFirewall + ".d/orbit.conf", Mode: 0o644, Content: firewallDropIn()},
		{Path: systemdDir + unitProxy + ".d/orbit.conf", Mode: 0o644, Content: proxyDropIn()},
		{Path: systemdDir + unitHarden, Mode: 0o644, Content: hardenUnit()},
		{Path: systemdDir + unitUserSessions + ".d/orbit.conf", Mode: 0o644, Content: userSessionsDropIn()},
		{Path: hardenPath, Mode: 0o755, Content: hardenScript(defaultUID)},
	}, dockerUnits()...)
}

// boundaryUnits returns the units that user sessions require.
func boundaryUnits() []string {
	return []string{unitFirewall, unitProxyAddress, unitProxy, unitHarden}
}

// proxyAddressUnit puts proxy.ContainerAddr on the dummy interface after
// every boot; an address set with ip alone does not survive a restart.
func proxyAddressUnit() string {
	return lines(
		"[Unit]",
		"Description=Orbit: address on which containers reach the egress proxy",
		"Before="+unitProxy+" "+unitUserSessions,
		"",
		"[Service]",
		"Type=oneshot",
		"RemainAfterExit=yes",
		fmt.Sprintf("ExecStart=/bin/sh -c 'ip link show %s >/dev/null 2>&1 || ip link add %s type dummy'", proxyInterface, proxyInterface),
		fmt.Sprintf("ExecStart=ip addr replace %s/32 dev %s", proxy.ContainerAddr, proxyInterface),
		fmt.Sprintf("ExecStart=ip link set %s up", proxyInterface),
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	)
}

// firewallDropIn orders the firewall of the distribution before user
// sessions and removes its ExecStop=, which flushes the whole ruleset: a stop
// or restart of the unit by root then leaves the table loaded, and egress
// stays closed (#168). The empty assignment clears the list of the unit.
func firewallDropIn() string {
	return lines(
		"[Unit]",
		"Before="+unitUserSessions,
		"",
		"[Service]",
		"ExecStop=",
	)
}

// proxyDropIn ties the proxy to the firewall and its address, and keeps it
// from staying down after repeated restarts.
func proxyDropIn() string {
	return lines(
		"[Unit]",
		"Requires="+unitFirewall+" "+unitProxyAddress,
		"After="+unitFirewall+" "+unitProxyAddress,
		"Before="+unitUserSessions,
		"StartLimitIntervalSec=0",
		"",
		"[Service]",
		"Restart=on-failure",
		"RestartSec=1",
	)
}

// hardenUnit runs the harden script at every boot, in case OrbStack
// restores the sudo rule of its default user. It has no start limit,
// because every run of the root script restarts it.
func hardenUnit() string {
	return lines(
		"[Unit]",
		"Description=Orbit: remove sudo from every account except root",
		"Before="+unitUserSessions,
		"StartLimitIntervalSec=0",
		"",
		"[Service]",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"ExecStart="+hardenPath,
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	)
}

// userSessionsDropIn keeps logins disabled while a unit of the boundary has
// not started. It has no start limit, because every restart of a unit that
// it requires restarts it too.
func userSessionsDropIn() string {
	units := strings.Join(boundaryUnits(), " ")
	return lines(
		"[Unit]",
		"Requires="+units,
		"After="+units,
		"StartLimitIntervalSec=0",
	)
}

// hardenScript removes every sudoers drop-in (OrbStack's rule for its
// default user and the image's rule for "ubuntu"), removes every account
// except root from the administrative groups, and locks the passwords of
// root, of the default user, and of every account from the uid floor up
// except nobody.
func hardenScript(defaultUID uint32) string {
	return lines(
		"#!/bin/sh",
		"set -eu",
		"find /etc/sudoers.d -mindepth 1 ! -name README -delete",
		"for group in sudo admin adm; do",
		`  getent group "$group" >/dev/null || continue`,
		`  for member in $(getent group "$group" | cut -d: -f4 | tr ',' ' '); do`,
		`    [ "$member" = root ] || gpasswd -d "$member" "$group" >/dev/null`,
		"  done",
		"done",
		"passwd -l root >/dev/null",
		"getent passwd | while IFS=: read -r name _ uid _; do",
		fmt.Sprintf(`  if { [ "$uid" -ge %d ] && [ "$uid" -ne %d ]; } || [ "$uid" -eq %d ]; then passwd -l "$name" >/dev/null; fi`, uidFloor, nobodyUID, defaultUID),
		"done",
	)
}

// lines joins its arguments into the content of a text file.
func lines(content ...string) string {
	return strings.Join(content, "\n") + "\n"
}
