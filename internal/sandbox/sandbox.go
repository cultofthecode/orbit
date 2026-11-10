// Package sandbox is the root of the sandbox module. It holds the pure
// functions that decide what a sandbox is: the name of the machine of a
// project directory, and which directory may be mounted into it. It does no
// I/O.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// NamePrefix starts the name of every machine that Orbit creates.
const NamePrefix = "orbit-"

// rootDir is the root of the file system.
const rootDir = string(filepath.Separator)

// Limits and fallbacks of a machine name.
const (
	slugLimit    = 30
	hashLength   = 8
	fallbackSlug = "project"
)

// Reasons why ValidateProjectDir refuses a project directory.
var (
	// ErrPathNotClean reports a path that is not absolute or not clean.
	ErrPathNotClean = errors.New("path is not absolute and clean")
	// ErrRootDir reports the root of the file system.
	ErrRootDir = errors.New("project directory is the root of the file system")
	// ErrHomeDir reports the home directory.
	ErrHomeDir = errors.New("project directory is the home directory")
	// ErrHomeAncestor reports a directory that contains the home directory.
	ErrHomeAncestor = errors.New("project directory contains the home directory")
	// ErrProtectedDir reports a protected directory of the home directory,
	// or a directory inside one.
	ErrProtectedDir = errors.New("project directory is inside a protected directory")
)

// protectedDirs are the directories of the home directory that never enter a
// sandbox, with everything inside them.
var protectedDirs = []string{".claude", ".ssh"}

// outsideSlug matches every run of characters that a slug cannot hold.
var outsideSlug = regexp.MustCompile(`[^a-z0-9]+`)

// MachineName returns the name of the machine of a project directory:
// "orbit-<slug>-<hash>". The slug comes from the base name of the directory,
// and the hash from the whole path, so the name is a valid host name label
// and two directories with the same base name get different machines.
// resolvedDir is the absolute, symlink-resolved path of the directory.
func MachineName(resolvedDir string) string {
	return NamePrefix + slug(filepath.Base(resolvedDir)) + "-" + shortHash(resolvedDir)
}

// ValidateProjectDir reports whether dir may be mounted into a sandbox of the
// user whose home directory is home. Both arguments must be absolute, clean
// paths in the one canonical spelling of the directory: symlinks resolved,
// and no other spelling that the file system accepts for the same directory.
// It refuses the root of the file system, the home directory, every ancestor
// of the home directory, and the protected directories of the home directory
// with everything inside them. Each refusal wraps the sentinel error of its
// reason.
func ValidateProjectDir(dir, home string) error {
	if !isAbsoluteAndClean(dir) {
		return fmt.Errorf("%w: project directory %q", ErrPathNotClean, dir)
	}
	if !isAbsoluteAndClean(home) {
		return fmt.Errorf("%w: home directory %q", ErrPathNotClean, home)
	}
	if reason := refusal(dir, home); reason != nil {
		return fmt.Errorf("%w: %s", reason, dir)
	}
	return nil
}

// refusal returns the sentinel error of the reason why dir must not be
// mounted, or nil.
func refusal(dir, home string) error {
	switch {
	case dir == rootDir:
		return ErrRootDir
	case dir == home:
		return ErrHomeDir
	case isWithin(home, dir):
		return ErrHomeAncestor
	case isProtected(dir, home):
		return ErrProtectedDir
	}
	return nil
}

// isProtected reports whether dir is a protected directory of home, or lies
// inside one.
func isProtected(dir, home string) bool {
	return slices.ContainsFunc(protectedDirs, func(name string) bool {
		return isWithin(dir, filepath.Join(home, name))
	})
}

// isAbsoluteAndClean reports whether path is absolute and has no "." or ".."
// segment, no repeated separator, and no trailing separator.
func isAbsoluteAndClean(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

// isWithin reports whether path is parent or lies inside it. It compares path
// components, so "/a/bc" is not within "/a/b".
func isWithin(path, parent string) bool {
	parts, parentParts := components(path), components(parent)
	return len(parts) >= len(parentParts) && slices.Equal(parts[:len(parentParts)], parentParts)
}

// components returns the names between the separators of path.
func components(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == filepath.Separator })
}

// slug returns the part of a machine name that a person can read: name in
// lower case, with every run of other characters than a-z and 0-9 replaced by
// one hyphen.
func slug(name string) string {
	hyphenated := outsideSlug.ReplaceAllString(strings.ToLower(name), "-")
	trimmed := strings.Trim(hyphenated, "-")
	short := strings.TrimRight(trimmed[:min(len(trimmed), slugLimit)], "-")
	if short == "" {
		return fallbackSlug
	}
	return short
}

// shortHash returns the first hex characters of the SHA-256 of path.
func shortHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:hashLength]
}
