//go:build !darwin

package destroy

import "io"

// isTerminal is always false off macOS, so that destroy fails closed: it
// refuses unless --yes is passed. Orbit runs only on macOS (OrbStack).
func isTerminal(io.Reader) bool {
	return false
}
