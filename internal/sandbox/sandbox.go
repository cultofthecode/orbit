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
	hashLength   = 16
	fallbackSlug = "project"
)

// Reasons why ValidateProjectDir refuses a project directory.
var (
	// ErrPathNotClean reports a path that is not absolute or not clean.
	ErrPathNotClean = errors.New("path is not absolute and clean")
	// ErrNoHome reports that no home directory of the user is known.
	ErrNoHome = errors.New("home directory of the user is not known")
	// ErrRootDir reports the root of the file system.
	ErrRootDir = errors.New("project directory is the root of the file system")
	// ErrHomeDir reports a home directory.
	ErrHomeDir = errors.New("project directory is the home directory")
	// ErrHomeAncestor reports a directory that contains a home directory.
	ErrHomeAncestor = errors.New("project directory contains the home directory")
	// ErrProtectedDir reports a protected directory, or a directory inside
	// one.
	ErrProtectedDir = errors.New("project directory is inside a protected directory")
	// ErrProtectedAncestor reports a directory that contains a protected
	// directory.
	ErrProtectedAncestor = errors.New("project directory contains a protected directory")
	// ErrNotOwner reports a directory that the user does not own.
	ErrNotOwner = errors.New("project directory is not owned by the user")
)

// protectedNames are the directories of a home directory that never enter a
// sandbox, with everything inside them.
var protectedNames = []string{".claude", ".ssh"}

// outsideSlug matches every run of characters that a slug cannot hold.
var outsideSlug = regexp.MustCompile(`[^a-z0-9]+`)

// ProjectDir holds the facts that decide whether a project directory may be
// mounted into a sandbox. A handler loads them from the system; every path
// is absolute and clean.
type ProjectDir struct {
	// Path is the canonical path of the directory: symlinks resolved, in
	// the one spelling that the system reports for the directory.
	Path string
	// OwnerUID is the uid of the owner of the directory.
	OwnerUID uint32
	// UserUID is the uid of the user who runs Orbit.
	UserUID uint32
	// Homes holds every spelling of every home directory of the user: the
	// one of the environment and the one of the account database, each as
	// its canonical path and as its path on its own volume.
	Homes []string
	// Protected holds every spelling of the directory that each path of
	// ProtectedDirs resolves to, for the paths that exist.
	Protected []string
}

// MachineName returns the name of the machine of a project directory:
// "orbit-<slug>-<hash>". The slug comes from the base name of the directory,
// and the hash is the first 16 hex characters of the SHA-256 of the whole
// path, so the name is a valid host name label and two directories with the
// same base name get different machines. resolvedDir is the canonical path of
// the directory.
func MachineName(resolvedDir string) string {
	return NamePrefix + slug(filepath.Base(resolvedDir)) + "-" + shortHash(resolvedDir)
}

// ProtectedDirs returns the paths in the home directory home that never
// enter a sandbox. A handler resolves each of them, because a protected
// directory can be a symlink to a directory somewhere else.
func ProtectedDirs(home string) []string {
	dirs := make([]string, len(protectedNames))
	for i, name := range protectedNames {
		dirs[i] = filepath.Join(home, name)
	}
	return dirs
}

// ValidateProjectDir reports whether the project directory dir may be
// mounted into a sandbox. Two independent rules must both accept it. The
// first refuses the root of the file system; a home directory and every
// directory that contains one; and a protected directory, every directory
// inside one, and every directory that contains one. The protected
// directories are ProtectedDirs of every home and the paths in dir.Protected.
// The second refuses a directory that the user does not own. Each refusal
// wraps the sentinel error of its reason. Paths are compared by their
// components, so every path must be in a spelling that the system reports.
func ValidateProjectDir(dir ProjectDir) error {
	if err := checkFacts(dir); err != nil {
		return err
	}
	if reason := refusal(dir); reason != nil {
		return fmt.Errorf("%w: %s", reason, dir.Path)
	}
	return nil
}

// checkFacts refuses facts that the rules cannot decide on: no home
// directory, or a path that is not absolute and clean.
func checkFacts(dir ProjectDir) error {
	if len(dir.Homes) == 0 {
		return ErrNoHome
	}
	for _, path := range slices.Concat([]string{dir.Path}, dir.Homes, dir.Protected) {
		if !isAbsoluteAndClean(path) {
			return fmt.Errorf("%w: %q", ErrPathNotClean, path)
		}
	}
	return nil
}

// refusal returns the sentinel error of the reason why dir must not be
// mounted, or nil.
func refusal(dir ProjectDir) error {
	protected := allProtected(dir)
	switch {
	case dir.Path == rootDir:
		return ErrRootDir
	case slices.Contains(dir.Homes, dir.Path):
		return ErrHomeDir
	case containsAny(dir.Path, dir.Homes):
		return ErrHomeAncestor
	case isWithinAny(dir.Path, protected):
		return ErrProtectedDir
	case containsAny(dir.Path, protected):
		return ErrProtectedAncestor
	case dir.OwnerUID != dir.UserUID:
		return ErrNotOwner
	}
	return nil
}

// allProtected returns the protected directories of every home of dir,
// followed by the resolved ones that the handler found.
func allProtected(dir ProjectDir) []string {
	protected := slices.Clone(dir.Protected)
	for _, home := range dir.Homes {
		protected = append(protected, ProtectedDirs(home)...)
	}
	return protected
}

// isWithinAny reports whether path is one of parents or lies inside one.
func isWithinAny(path string, parents []string) bool {
	return slices.ContainsFunc(parents, func(parent string) bool {
		return isWithin(path, parent)
	})
}

// containsAny reports whether one of children is path or lies inside it.
func containsAny(path string, children []string) bool {
	return slices.ContainsFunc(children, func(child string) bool {
		return isWithin(child, path)
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
