// Package realpath resolves a path to the one canonical path of its file.
// Every slice of the sandbox module resolves the project directory with it,
// so that the mount rules compare canonical paths and every slice computes
// the same machine name for a directory.
package realpath

// Resolve returns the canonical path of the existing file path: absolute,
// with symlinks resolved, in the one spelling that the system reports for
// the file.
func Resolve(path string) (string, error) {
	return resolve(path)
}
