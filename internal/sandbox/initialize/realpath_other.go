//go:build !darwin

package initialize

import "path/filepath"

// realPath returns the canonical path of the existing file path: absolute,
// with symlinks resolved.
func realPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
