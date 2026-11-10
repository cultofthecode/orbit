//go:build !darwin

package realpath

import "path/filepath"

// resolve makes path absolute and resolves its symlinks.
func resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
