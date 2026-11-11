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
	// ErrProtectedFile reports a directory that contains a protected file.
	ErrProtectedFile = errors.New("project directory contains a protected file")
	// ErrNotOwner reports a directory that the user does not own.
	ErrNotOwner = errors.New("project directory is not owned by the user")
	// ErrNestedProjectDir reports a directory inside the project directory
	// of another sandbox. The agent of that sandbox controls the path of the
	// directory and can swap it between the check and the mount.
	ErrNestedProjectDir = errors.New("project directory is inside the project directory of another sandbox")
	// ErrContainsProjectDir reports a directory that contains the project
	// directory of another sandbox. The agent of the new sandbox would
	// control the path of that directory and could swap it before the next
	// start of the other sandbox.
	ErrContainsProjectDir = errors.New("project directory contains the project directory of another sandbox")
	// ErrMountMismatch reports a machine that does not mount exactly the
	// project directory, once, at its own path.
	ErrMountMismatch = errors.New("machine does not mount exactly the project directory at its own path")
	// ErrRelocatedPath reports an environment variable that moves a
	// protected location to a value that is empty, relative, or has a ".."
	// segment. Orbit cannot know where the tool puts the location then.
	ErrRelocatedPath = errors.New("environment variable that moves a protected location is not an absolute path without a .. segment")
	// ErrHardLinkedFile reports a protected file that has more hard links
	// than Orbit found in the protected locations. Every link is the same
	// file, and Orbit cannot find the other links without a scan of every
	// volume, so a project directory could hold one.
	ErrHardLinkedFile = errors.New("protected file has more than one hard link")
)

// Errors that the slices of the sandbox lifecycle report alike.
var (
	// ErrNoSandbox reports a project directory without a sandbox: no machine
	// has the name that MachineName computes for it.
	ErrNoSandbox = errors.New("no sandbox")
	// ErrUnexpectedState reports a sandbox in a state other than running or
	// stopped, such as starting or stopping. Orbit acts on no other state.
	ErrUnexpectedState = errors.New("wait and try again")
)

// Errors of the host-side checks that run before Orbit creates or starts a
// machine.
var (
	// ErrNotDirectory reports a project path that is not a directory.
	ErrNotDirectory = errors.New("project path is not a directory")
	// ErrLinkedSandboxDir reports another machine whose stored mount path
	// passes through a symlink. Orbit stores canonical paths, so the host
	// layout changed after that machine was created, and OrbStack follows
	// the symlink at its next start. The slices refuse every project
	// directory until the user fixes the path or deletes that machine.
	ErrLinkedSandboxDir = errors.New("the project directory of another sandbox passes through a symlink")
)

// protectedNames are the directories of a home directory that never enter a
// sandbox, with everything inside them: the credentials and the
// configuration of the agent, SSH, AWS, GnuPG, and the GitHub CLI.
var protectedNames = []string{".aws", ".claude", filepath.Join(".config", "gh"), ".gnupg", ".ssh"}

// protectedFileNames are the files of a home directory that never enter a
// sandbox: the configuration and the state of the agent.
var protectedFileNames = []string{".claude.json"}

// relocation is an environment variable that moves a protected location of
// the home directory, and the name of that location in the directory that
// the variable names; an empty name means the directory itself.
type relocation struct {
	variable string
	name     string
}

// dirRelocations are the variables that move a protected directory: the
// configuration and the state of the agent with its .claude.json, the
// configuration of the GitHub CLI, and the keys of GnuPG.
var dirRelocations = []relocation{
	{variable: "CLAUDE_CONFIG_DIR"},
	{variable: "GH_CONFIG_DIR"},
	{variable: "GNUPGHOME"},
	{variable: "XDG_CONFIG_HOME", name: "gh"},
}

// fileRelocations are the variables that move a protected file: the
// configuration and the credentials of AWS.
var fileRelocations = []relocation{
	{variable: "AWS_CONFIG_FILE"},
	{variable: "AWS_SHARED_CREDENTIALS_FILE"},
}

// singleLinkRelocations are the variables that move a file that must have
// one hard link: the files of fileRelocations and the .claude.json of the
// directory that CLAUDE_CONFIG_DIR moves.
var singleLinkRelocations = slices.Concat(fileRelocations, []relocation{{variable: "CLAUDE_CONFIG_DIR", name: ".claude.json"}})

// outsideSlug matches every run of characters that a slug cannot hold.
var outsideSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Agent is what a slice needs to know about the agent of a sandbox. cmd/orbit
// builds it from the definition of the agent; a slice never compares the
// name of an agent.
type Agent struct {
	// Install is the bash script that installs the client as the agent
	// user, through the proxy.
	Install string
	// EgressDomains are the hosts that the proxy allows in addition to the
	// base list.
	EgressDomains []string
}

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
	// ProtectedDirs and RelocatedDirs resolves to, or of the path where its
	// target would be created when it does not exist, and of every
	// directory that a symlink in one of those directories leads to or
	// would create.
	Protected []string
	// ProtectedFileDirs holds every spelling of the directory that holds
	// the file that each path of ProtectedFiles and RelocatedFiles resolves
	// to, or would be created in, of the directory that holds each file
	// that a symlink in a protected directory leads to, and of each
	// directory that holds a symlink on the chain of such a symlink or on
	// the path of RelocatedDirs and RelocatedFiles.
	ProtectedFileDirs []string
	// ProtectedFileLinks holds the link count of each protected file that
	// has more than one hard link: the file that each path of ProtectedFiles
	// and RelocatedSingleLinkFiles resolves to, and each regular file in a
	// directory of Protected or that a symlink in one of them leads to.
	ProtectedFileLinks []FileLinks
}

// FileLinks is the number of hard links of a file and the number of them
// that lie in the protected locations.
type FileLinks struct {
	// Path is the absolute, clean path of the file.
	Path string
	// Count is the link count that the system reports for the file.
	Count uint64
	// Found is the number of distinct paths of the file in the protected
	// locations. A file that must have one hard link has 1.
	Found uint64
}

// Relocated is a protected location that an environment variable moves.
type Relocated struct {
	// Variable is the name of the environment variable.
	Variable string
	// Path is the absolute, clean path of the location.
	Path string
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
	return joinAll(home, protectedNames)
}

// ProtectedFiles returns the paths of the files in the home directory home
// that never enter a sandbox. A handler resolves each of them, because a
// protected file can be a symlink to a file in another directory.
func ProtectedFiles(home string) []string {
	return joinAll(home, protectedFileNames)
}

// RelocatedDirs returns the protected directories that the environment
// moves. lookup returns the value of an environment variable and whether it
// is set; a handler passes os.LookupEnv. An unset variable adds nothing. A
// value that is empty, relative, or has a ".." segment fails with
// ErrRelocatedPath and names the variable. A handler resolves each path, as
// it resolves ProtectedDirs.
func RelocatedDirs(lookup func(string) (string, bool)) ([]Relocated, error) {
	return relocate(dirRelocations, lookup)
}

// RelocatedFiles returns the protected files that the environment moves, by
// the rules of RelocatedDirs. A handler resolves each path, as it resolves
// ProtectedFiles.
func RelocatedFiles(lookup func(string) (string, bool)) ([]Relocated, error) {
	return relocate(fileRelocations, lookup)
}

// RelocatedSingleLinkFiles returns the protected files that the environment
// moves and that must have one hard link, by the rules of RelocatedDirs: the
// files of RelocatedFiles and the .claude.json in the directory of
// CLAUDE_CONFIG_DIR.
func RelocatedSingleLinkFiles(lookup func(string) (string, bool)) ([]Relocated, error) {
	return relocate(singleLinkRelocations, lookup)
}

// ValidateProjectDir reports whether the project directory dir may be
// mounted into a sandbox. First it refuses every project directory while a
// file in dir.ProtectedFileLinks has more hard links than were found in the
// protected locations, with ErrHardLinkedFile naming each such file once,
// because the other link can lie anywhere. Then two independent rules must
// both accept dir. The first refuses the root of the file system; a home
// directory and every directory that contains one; a protected directory,
// every directory inside one, and every directory that contains one; and
// every directory that contains a protected file. The protected directories are ProtectedDirs of
// every home and the paths in dir.Protected. The directories that hold a
// protected file are the homes and the paths in dir.ProtectedFileDirs; a
// directory inside one of them does not contain the file, so it passes. The
// second refuses a directory that the user does not own. Each refusal
// wraps the sentinel error of its reason. Paths are compared by their
// components, so every path must be in a spelling that the system reports.
func ValidateProjectDir(dir ProjectDir) error {
	if err := checkFacts(dir); err != nil {
		return err
	}
	if err := checkHardLinks(dir.ProtectedFileLinks); err != nil {
		return err
	}
	if reason := refusal(dir); reason != nil {
		return fmt.Errorf("%w: %s", reason, dir.Path)
	}
	return nil
}

// ValidateNotNested reports whether the project directory dir and the
// project directories of the other sandboxes, sandboxDirs, are disjoint. It
// refuses dir when it is one of them or lies inside one, with
// ErrNestedProjectDir, and when it contains one, with ErrContainsProjectDir.
// Every path must be absolute and clean; it refuses one that is not with
// ErrPathNotClean. It compares the paths by their components as they are, so
// the caller passes every spelling that matters, also of a directory that no
// longer exists.
func ValidateNotNested(dir string, sandboxDirs []string) error {
	if err := checkPaths(slices.Concat([]string{dir}, sandboxDirs)); err != nil {
		return err
	}
	switch {
	case isWithinAny(dir, sandboxDirs):
		return fmt.Errorf("%w: %s", ErrNestedProjectDir, dir)
	case containsAny(dir, sandboxDirs):
		return fmt.Errorf("%w: %s", ErrContainsProjectDir, dir)
	}
	return nil
}

// ValidateMounts reports whether mounts, the host directory of each mount of
// a machine by its mount point, are exactly the project directory dir at its
// own path. Paths are compared as they are, so dir must be absolute, clean,
// and canonical. It refuses every other set of mounts with ErrMountMismatch.
func ValidateMounts(dir string, mounts map[string]string) error {
	if err := checkPaths([]string{dir}); err != nil {
		return err
	}
	if len(mounts) != 1 || mounts[dir] != dir {
		return fmt.Errorf("%w: want %s, got %v", ErrMountMismatch, dir, mounts)
	}
	return nil
}

// checkFacts refuses facts that the rules cannot decide on: no home
// directory, or a path that is not absolute and clean.
func checkFacts(dir ProjectDir) error {
	if len(dir.Homes) == 0 {
		return ErrNoHome
	}
	return checkPaths(slices.Concat([]string{dir.Path}, dir.Homes, dir.Protected, dir.ProtectedFileDirs, linkPaths(dir.ProtectedFileLinks)))
}

// linkPaths returns the path of each of files.
func linkPaths(files []FileLinks) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	return paths
}

// checkHardLinks refuses the files that have more hard links than were found
// in the protected locations and names each of them once.
func checkHardLinks(files []FileLinks) error {
	var linked []string
	for _, file := range files {
		if file.Count > file.Found {
			linked = append(linked, file.Path)
		}
	}
	if len(linked) == 0 {
		return nil
	}
	slices.Sort(linked)
	return fmt.Errorf("%w: %s; remove the other hard links to it", ErrHardLinkedFile, strings.Join(slices.Compact(linked), ", "))
}

// checkPaths refuses a path that is not absolute and clean.
func checkPaths(paths []string) error {
	for _, path := range paths {
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
	case containsAny(dir.Path, protectedFileDirs(dir)):
		return ErrProtectedFile
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

// protectedFileDirs returns the directories that hold the protected files of
// every home of dir, followed by the resolved ones that the handler found.
func protectedFileDirs(dir ProjectDir) []string {
	dirs := slices.Clone(dir.ProtectedFileDirs)
	for _, home := range dir.Homes {
		for _, file := range ProtectedFiles(home) {
			dirs = append(dirs, filepath.Dir(file))
		}
	}
	return dirs
}

// relocate returns the location of each of relocations whose variable is
// set.
func relocate(relocations []relocation, lookup func(string) (string, bool)) ([]Relocated, error) {
	var found []Relocated
	for _, moved := range relocations {
		value, set := lookup(moved.variable)
		if !set {
			continue
		}
		path, err := relocatedPath(moved.variable, value)
		if err != nil {
			return nil, err
		}
		found = append(found, Relocated{Variable: moved.variable, Path: filepath.Join(path, moved.name)})
	}
	return found, nil
}

// relocatedPath returns the clean path of the value of variable. It refuses
// a value that is not absolute or has a ".." segment: when the name before
// ".." is a symlink, the kernel applies ".." to its target, so a lexical
// clean would protect the wrong directory.
func relocatedPath(variable, value string) (string, error) {
	switch {
	case value == "":
		return "", fmt.Errorf("%w: %s=\"\" (unset %s to use its default location)", ErrRelocatedPath, variable, variable)
	case !filepath.IsAbs(value) || slices.Contains(strings.Split(value, string(filepath.Separator)), ".."):
		return "", fmt.Errorf("%w: %s=%q", ErrRelocatedPath, variable, value)
	}
	return filepath.Clean(value), nil
}

// joinAll returns dir joined with each of names.
func joinAll(dir string, names []string) []string {
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths
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
