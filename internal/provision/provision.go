// Package provision decides what root sets up in a sandbox machine before
// any agent runs in it: the firewall, the proxy and its units, the hardened
// users, the agent user without sudo, and the fixed toolchain (D14, D17,
// D20, D22). It renders all of it as one root script. The package is pure
// and does no I/O; the init slice runs the script through the adapter.
package provision
