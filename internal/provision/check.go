package provision

import (
	"fmt"
	"slices"
)

// bootWaitSeconds bounds the wait of BootCheckScript for the end of the boot.
const bootWaitSeconds = 120

// BootCheckScript returns the root script that orbit start runs after it
// started a machine. It waits at most bootWaitSeconds for the boot to end,
// and then fails unless the firewall table is loaded and every boundary unit
// and systemd-user-sessions.service is active, as the self-check of Script
// requires. It prints the first failure to standard error. The bytes never
// change between calls.
func BootCheckScript() []byte {
	body := slices.Concat(checkPrelude(), []string{waitForBoot()}, boundaryCheck())
	return []byte(lines(group(body)...))
}

// MarkerCheckScript returns the root script that fails unless MarkerPath
// holds exactly Version, as Script writes it. It prints the failure to
// standard error. The bytes never change between calls.
func MarkerCheckScript() []byte {
	body := slices.Concat(checkPrelude(), []string{markerCheck()})
	return []byte(lines(group(body)...))
}

// checkPrelude stops at the first failure and defines fail, which prints its
// arguments to standard error and exits 1.
func checkPrelude() []string {
	return []string{
		"set -euo pipefail",
		`fail() { echo "$*" >&2; exit 1; }`,
	}
}

// waitForBoot waits until systemd reports the end of the boot, at most
// bootWaitSeconds. "running" and "degraded" both end the wait; the state is
// not trusted, because the checks after it decide.
func waitForBoot() string {
	return fmt.Sprintf("timeout %d systemctl is-system-running --wait >/dev/null 2>&1 || true", bootWaitSeconds)
}

// markerCheck compares MarkerPath byte for byte with the content that Script
// writes.
func markerCheck() string {
	return fmt.Sprintf(`printf '%%s\n' %d | cmp -s - %s || fail "%s does not hold provisioning version %d"`, Version, MarkerPath, MarkerPath, Version)
}
