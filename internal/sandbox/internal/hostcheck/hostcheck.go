// Package hostcheck holds the host-side checks that run before Orbit
// creates or starts a machine: it loads the facts of a project directory
// from the system for sandbox.ValidateProjectDir, and it refuses a project
// directory that is nested with the directory of another machine or a
// stored mount path that passes through a symlink. The slices of the module
// share it, so that each start of a machine runs the same checks as init.
package hostcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/internal/realpath"
)

// Unnested refuses dir, the canonical project directory of the machine name,
// when it is, lies inside, or contains a directory that a machine other than
// name mounts, as machines lists the configured mounts of every machine, and
// when a stored path of such a directory passes through a symlink
// (sandbox.ErrLinkedSandboxDir). The nesting comparison never counts the
// machine name. With includeOwn, the symlink walk also covers the stored
// paths of name (sandbox.ErrLinkedProjectDir): the paths that OrbStack
// resolves when it starts that machine.
func Unnested(machines map[string]map[string]string, name, dir string, includeOwn bool) error {
	if err := validateNotNested(dir, mountedDirs(machines, name)); err != nil {
		return err
	}
	return validateUnlinked(machines, name, includeOwn)
}

// maxLinkHops is the most symlinks that the handler follows on the way to
// where the target of a dangling protected path would be created: MAXSYMLINKS
// of macOS.
const maxLinkHops = 32

// ErrUnresolvableLink reports a dangling symlink whose target has ".." after
// another name. When that name is a symlink, the kernel applies ".." to its
// target, so a lexical join would protect the wrong directory.
var ErrUnresolvableLink = errors.New("target of a dangling symlink has .. after a name")

// validateUnlinked refuses the stored mount paths of the machines other than
// name, and with includeOwn also those of name, when a component of one of
// them that exists is a symlink. Orbit stores canonical paths, so such a
// symlink means that the host layout changed after the machine was created;
// OrbStack would follow it at the next start, also when it dangles or when
// one of its hops lies in the new directory.
func validateUnlinked(machines map[string]map[string]string, name string, includeOwn bool) error {
	for machine, mounts := range machines {
		if machine == name && !includeOwn {
			continue
		}
		linked := sandbox.ErrLinkedSandboxDir
		if machine == name {
			linked = sandbox.ErrLinkedProjectDir
		}
		for _, dir := range mounts {
			if err := checkUnlinked(dir, linked); err != nil {
				return fmt.Errorf("project directory %s of sandbox %s: %w", dir, machine, err)
			}
		}
	}
	return nil
}

// checkUnlinked fails with linked when a component of the absolute path that
// exists is a symlink. It stops at the first component that does not exist,
// and fails on every other error.
func checkUnlinked(path string, linked error) error {
	for _, prefix := range pathPrefixes(path) {
		info, err := os.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", linked, prefix)
		}
	}
	return nil
}

// pathPrefixes returns every path from the first component of the absolute
// path to the path itself, in that order.
func pathPrefixes(path string) []string {
	var prefixes []string
	for prefix := path; prefix != filepath.Dir(prefix); prefix = filepath.Dir(prefix) {
		prefixes = append(prefixes, prefix)
	}
	slices.Reverse(prefixes)
	return prefixes
}

// validateNotNested refuses dir when it is, lies inside, or contains one of
// sandboxDirs, the directories that other machines mount, as OrbStack stores
// them. OrbStack resolves the stored path again at every start, so it checks
// each one as it is first, also when it does not exist, and refuses one that
// is not absolute and clean before it reads the file system. Then it checks
// every spelling that each one resolves to.
func validateNotNested(dir string, sandboxDirs []string) error {
	if err := sandbox.ValidateNotNested(dir, sandboxDirs); err != nil {
		return err
	}
	spellings, err := pathSpellings(sandboxDirs)
	if err != nil {
		return fmt.Errorf("project directory of another sandbox: %w", err)
	}
	return sandbox.ValidateNotNested(dir, spellings)
}

// pathSpellings returns every spelling of each path in paths. For a path
// that does not exist, it returns every spelling of its nearest existing
// parent, joined with the rest of the path.
func pathSpellings(paths []string) ([]string, error) {
	var spellings []string
	for _, path := range paths {
		found, err := nearestSpellings(path)
		if err != nil {
			return nil, err
		}
		spellings = append(spellings, found...)
	}
	return distinct(spellings), nil
}

// nearestSpellings returns every spelling of the absolute path, or, when it
// does not exist, every spelling of its parent joined with its base name.
func nearestSpellings(path string) ([]string, error) {
	spellings, err := dirSpellings(path)
	parent := filepath.Dir(path)
	if !errors.Is(err, fs.ErrNotExist) || parent == path {
		return spellings, err
	}
	parentSpellings, err := nearestSpellings(parent)
	if err != nil {
		return nil, err
	}
	return joinAll(parentSpellings, filepath.Base(path)), nil
}

// joinAll returns each of dirs joined with name.
func joinAll(dirs []string, name string) []string {
	joined := make([]string, len(dirs))
	for i, dir := range dirs {
		joined[i] = filepath.Join(dir, name)
	}
	return joined
}

// mountedDirs returns the host directories that the machines other than name
// mount.
func mountedDirs(machines map[string]map[string]string, name string) []string {
	var dirs []string
	for machine, mounts := range machines {
		if machine != name {
			dirs = slices.AppendSeq(dirs, maps.Values(mounts))
		}
	}
	return dirs
}

// ProjectDir returns the facts of the project directory path, with its
// canonical path, when the mount rules of sandbox.ValidateProjectDir accept
// it. currentUser returns the user who runs Orbit, from the account database
// of the system; its uid and its home directory count more than the
// environment.
func ProjectDir(path string, currentUser func() (*user.User, error)) (sandbox.ProjectDir, error) {
	facts, err := loadFacts(path, currentUser)
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	if err := sandbox.ValidateProjectDir(facts); err != nil {
		return sandbox.ProjectDir{}, err
	}
	return facts, nil
}

// loadFacts reads from the system what the mount rules decide on. It fails
// when it cannot read one of the facts.
func loadFacts(path string, currentUser func() (*user.User, error)) (sandbox.ProjectDir, error) {
	dir, owner, err := resolveDir(path)
	if err != nil {
		return sandbox.ProjectDir{}, fmt.Errorf("project directory: %w", err)
	}
	uid, homes, err := loadUser(currentUser)
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	protected, fileDirs, files, err := loadProtected(homes)
	if err != nil {
		return sandbox.ProjectDir{}, err
	}
	if err := addSingleLinkFiles(files, homes); err != nil {
		return sandbox.ProjectDir{}, err
	}
	return sandbox.ProjectDir{Path: dir, OwnerUID: owner, UserUID: uid, Homes: homes, Protected: protected, ProtectedFileDirs: fileDirs, ProtectedFileLinks: files.links()}, nil
}

// loadProtected returns every spelling of the protected directories of homes
// and of the environment, and of the directories that hold a protected file,
// together with what the symlinks in those protected directories lead to,
// and the files with more than one hard link that it found in them.
func loadProtected(homes []string) ([]string, []string, foundFiles, error) {
	protected, err := allProtectedSpellings(homes)
	if err != nil {
		return nil, nil, nil, err
	}
	fileDirs, err := allFileDirSpellings(homes)
	if err != nil {
		return nil, nil, nil, err
	}
	linkedDirs, linkedFileDirs, files, err := linkedSpellings(protected)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("file in a protected directory: %w", err)
	}
	return distinct(slices.Concat(protected, linkedDirs)), distinct(slices.Concat(fileDirs, linkedFileDirs)), files, nil
}

// allProtectedSpellings returns every spelling of the directories that the
// protected paths of homes and the protected directories that the
// environment moves resolve to, or where their targets would be created.
func allProtectedSpellings(homes []string) ([]string, error) {
	protected, err := protectedSpellings(homes)
	if err != nil {
		return nil, fmt.Errorf("protected directory: %w", err)
	}
	relocated, err := relocatedSpellings(sandbox.RelocatedDirs, targetSpellings)
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(protected, relocated)), nil
}

// allFileDirSpellings returns every spelling of the directories that hold
// the protected files of homes and the protected files that the environment
// moves, or would be created in, and of the directories that hold a symlink
// on the path of a location that the environment moves.
func allFileDirSpellings(homes []string) ([]string, error) {
	fileDirs, err := protectedFileDirSpellings(homes)
	if err != nil {
		return nil, fmt.Errorf("protected file: %w", err)
	}
	relocated, err := relocatedSpellings(sandbox.RelocatedFiles, fileDirSpellings)
	if err != nil {
		return nil, err
	}
	hops, err := relocatedHopSpellings()
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(fileDirs, relocated, hops)), nil
}

// relocatedHopSpellings returns every spelling of each directory that holds
// a symlink on the path of a protected location that the environment moves,
// as linkSpellings walks it. A variable can name a path through a project
// directory, where the agent could replace that symlink and redirect the
// tool on the host.
func relocatedHopSpellings() ([]string, error) {
	dirHops, err := relocatedSpellings(sandbox.RelocatedDirs, chainHolders)
	if err != nil {
		return nil, err
	}
	fileHops, err := relocatedSpellings(sandbox.RelocatedFiles, chainHolders)
	if err != nil {
		return nil, err
	}
	return slices.Concat(dirHops, fileHops), nil
}

// chainHolders returns the second result of linkSpellings for path: every
// spelling of each directory that holds a symlink on its chain.
func chainHolders(path string) ([]string, error) {
	_, holders, err := linkSpellings(path)
	return holders, err
}

// relocatedSpellings returns what resolve returns for each protected
// location that locate finds in the environment of the user who runs Orbit.
// It reads only the values of the variables, never a file that they name
// (D08), and names the variable of a location that cannot be resolved.
func relocatedSpellings(locate func(func(string) (string, bool)) ([]sandbox.Relocated, error), resolve func(string) ([]string, error)) ([]string, error) {
	relocated, err := locate(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	var spellings []string
	for _, location := range relocated {
		found, err := resolve(location.Path)
		if err != nil {
			return nil, fmt.Errorf("protected location of %s: %w", location.Variable, err)
		}
		spellings = append(spellings, found...)
	}
	return spellings, nil
}

// loadUser returns the uid of the user who runs Orbit and every spelling of
// the home directories of that user.
func loadUser(currentUser func() (*user.User, error)) (uint32, []string, error) {
	account, err := currentUser()
	if err != nil {
		return 0, nil, fmt.Errorf("current user: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, nil, fmt.Errorf("uid of the current user: %w", err)
	}
	homes, err := homeSpellings(account.HomeDir)
	if err != nil {
		return 0, nil, fmt.Errorf("home directory: %w", err)
	}
	return uint32(uid), homes, nil
}

// resolveDir returns the canonical path of path, which must be an existing
// directory, and the uid of its owner, both read from one open handle.
func resolveDir(path string) (string, uint32, error) {
	dir, owner, err := realpath.ResolveWithOwner(path)
	if errors.Is(err, syscall.ENOTDIR) {
		return "", 0, fmt.Errorf("%w: %w", sandbox.ErrNotDirectory, err)
	}
	return dir, owner, err
}

// homeSpellings returns every spelling of the home directories of the user:
// the one of the environment and accountHome, the one of the account
// database. Both must exist, because the environment alone can name any
// directory.
func homeSpellings(accountHome string) ([]string, error) {
	environmentHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var homes []string
	for _, home := range []string{environmentHome, accountHome} {
		spellings, err := dirSpellings(home)
		if err != nil {
			return nil, err
		}
		homes = append(homes, spellings...)
	}
	return distinct(homes), nil
}

// protectedSpellings returns every spelling of the directories that the
// protected paths of homes resolve to. For a protected path that does not
// exist, it returns every spelling of the path where its target would be
// created (targetSpellings).
func protectedSpellings(homes []string) ([]string, error) {
	var spellings []string
	for _, path := range protectedPaths(homes) {
		found, err := targetSpellings(path)
		if err != nil {
			return nil, err
		}
		spellings = append(spellings, found...)
	}
	return distinct(spellings), nil
}

// protectedFileDirSpellings returns every spelling of the directories that
// hold the files that the protected file paths of homes resolve to, or
// would be created in. It follows the symlinks without opening the files.
func protectedFileDirSpellings(homes []string) ([]string, error) {
	var resolved []string
	for _, path := range protectedFilePaths(homes) {
		spellings, err := fileDirSpellings(path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, spellings...)
	}
	return distinct(resolved), nil
}

// fileDirSpellings returns every spelling of the directory that holds the
// file that path resolves to, without opening the file. For a path that
// leads nowhere, it is the directory in which the final target would be
// created.
func fileDirSpellings(path string) ([]string, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return missingFileDirSpellings(path)
	}
	if err != nil {
		return nil, err
	}
	return dirSpellings(filepath.Dir(target))
}

// addSingleLinkFiles adds to files, as files that must have one hard link,
// the file that each protected file path of homes resolves to, and the file
// that each location of RelocatedSingleLinkFiles resolves to. A path that
// leads nowhere adds nothing. It reads only metadata and never opens a file
// (D08).
func addSingleLinkFiles(files foundFiles, homes []string) error {
	for _, path := range protectedFilePaths(homes) {
		if err := files.addResolved(path); err != nil {
			return fmt.Errorf("protected file: %w", err)
		}
	}
	relocated, err := sandbox.RelocatedSingleLinkFiles(os.LookupEnv)
	if err != nil {
		return err
	}
	for _, location := range relocated {
		if err := files.addResolved(location.Path); err != nil {
			return fmt.Errorf("protected location of %s: %w", location.Variable, err)
		}
	}
	return nil
}

// fileID identifies a file by its device and its inode.
type fileID struct {
	dev uint64
	ino uint64
}

// foundFile is a protected file with more than one hard link.
type foundFile struct {
	// first is the path that the refusal names: the path of a file that
	// must have one hard link, otherwise the first path that the handler
	// found.
	first string
	// paths are the distinct paths of the file that the handler found in
	// the protected locations.
	paths map[string]bool
	// count is the link count of the file.
	count uint64
	// single reports a file that must have one hard link.
	single bool
}

// foundFiles holds the protected files with more than one hard link by their
// identity, so that each path of a file counts once, under every spelling
// and from every protected location that reaches it.
type foundFiles map[fileID]*foundFile

// links returns the link count of each file and the number of its paths
// that the handler found, 1 for a file that must have one hard link, sorted
// by path.
func (f foundFiles) links() []sandbox.FileLinks {
	links := make([]sandbox.FileLinks, 0, len(f))
	for _, file := range f {
		found := uint64(len(file.paths))
		if file.single {
			found = 1
		}
		links = append(links, sandbox.FileLinks{Path: file.first, Count: file.count, Found: found})
	}
	slices.SortFunc(links, func(a, b sandbox.FileLinks) int { return strings.Compare(a.Path, b.Path) })
	return links
}

// add records path as a path of the file id that has count hard links, when
// it has more than one.
func (f foundFiles) add(id fileID, count uint64, path string, single bool) {
	if count < 2 {
		return
	}
	file := f[id]
	if file == nil {
		file = &foundFile{first: path, paths: map[string]bool{}}
		f[id] = file
	}
	file.paths[path] = true
	file.count = max(file.count, count)
	if single && !file.single {
		file.first = path
	}
	file.single = file.single || single
}

// addResolved records the file that path resolves to as a file that must
// have one hard link. A path that leads nowhere adds nothing.
func (f foundFiles) addResolved(path string) error {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.addFile(target, true)
}

// addFile records path, which is no symlink, when it is a regular file. A
// file that no longer exists adds nothing.
func (f foundFiles) addFile(path string, single bool) error {
	id, count, regular, err := fileStat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !regular {
		return err
	}
	f.add(id, count, path, single)
	return nil
}

// addChainEnd records the file at path, where the chain of a symlink in a
// protected directory ends, under its canonical path, when it is a regular
// file with more than one hard link.
func (f foundFiles) addChainEnd(path string) error {
	id, count, regular, err := fileStat(path)
	if err != nil || !regular || count < 2 {
		return err
	}
	canonical, err := canonicalFile(path, id)
	if err != nil {
		return err
	}
	f.add(id, count, canonical, false)
	return nil
}

// canonicalFile returns the canonical path of the file id that path names:
// the canonical path of the directory that holds it, joined with the name
// that the directory lists for it. A symlink can name the file in another
// spelling, and each distinct path counts as one link.
func canonicalFile(path string, id fileID) (string, error) {
	dir, err := realpath.Resolve(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	name, err := listedName(dir, filepath.Base(path), id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// listedName returns the name under which the directory dir lists the file
// id that name names: name itself when dir lists it so, or else the only
// name of the file in dir. It fails when it cannot tell which of several
// names of the file name is.
func listedName(dir, name string, id fileID) (string, error) {
	names, err := namesOf(dir, id)
	if err != nil {
		return "", err
	}
	switch {
	case slices.Contains(names, name):
		return name, nil
	case len(names) == 1:
		return names[0], nil
	}
	return "", fmt.Errorf("%s names one of %d hard links in %s; point the symlink at a name that the directory lists", name, len(names), dir)
}

// namesOf returns the names in the directory dir of the regular file id.
func namesOf(dir string, id fileID) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		entryID, _, _, err := fileStat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if entryID == id {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// fileStat returns the identity and the link count of the file at path, and
// whether it is a regular file, from its metadata, without following a
// symlink.
func fileStat(path string) (fileID, uint64, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileID{}, 0, false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, 0, false, fmt.Errorf("link count of %s is not known", path)
	}
	return fileID{dev: uint64(stat.Dev), ino: stat.Ino}, uint64(stat.Nlink), info.Mode().IsRegular(), nil
}

// missingFileDirSpellings returns every spelling of the directory in which
// the final target of the path that leads nowhere would be created.
func missingFileDirSpellings(path string) ([]string, error) {
	targets, err := targetSpellings(path)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, len(targets))
	for i, target := range targets {
		dirs[i] = filepath.Dir(target)
	}
	return dirs, nil
}

// targetSpellings returns every spelling of the directory that the absolute
// path resolves to. For a path that does not exist, it returns every
// spelling of the path where its target would be created: the nearest
// existing parent joined with the rest of the path, where a dangling symlink
// on the way is followed to its target, as the kernel would follow it.
func targetSpellings(path string) ([]string, error) {
	return followedSpellings(path, 0)
}

// followedSpellings is targetSpellings after hops symlinks.
func followedSpellings(path string, hops int) ([]string, error) {
	spellings, err := dirSpellings(path)
	parent := filepath.Dir(path)
	if !errors.Is(err, fs.ErrNotExist) || parent == path {
		return spellings, err
	}
	parentSpellings, err := followedSpellings(parent, hops)
	if err != nil {
		return nil, err
	}
	return followLinks(joinAll(parentSpellings, filepath.Base(path)), hops)
}

// followLinks returns the paths, with every path that is a symlink replaced
// by the spellings of where its target would be created. The paths are
// spellings of one path, so it follows each distinct target once.
func followLinks(paths []string, hops int) ([]string, error) {
	var kept, targets []string
	for _, path := range paths {
		target, err := danglingTarget(path)
		if err != nil {
			return nil, err
		}
		if target == "" {
			kept = append(kept, path)
		} else {
			targets = append(targets, target)
		}
	}
	followed, err := followTargets(distinct(targets), hops)
	if err != nil {
		return nil, err
	}
	return distinct(slices.Concat(kept, followed)), nil
}

// followTargets returns the spellings of where each of targets, reached
// after hops symlinks, would be created.
func followTargets(targets []string, hops int) ([]string, error) {
	if len(targets) > 0 && hops == maxLinkHops {
		return nil, fmt.Errorf("%w: %s", syscall.ELOOP, targets[0])
	}
	var followed []string
	for _, target := range targets {
		spellings, err := followedSpellings(target, hops+1)
		if err != nil {
			return nil, err
		}
		followed = append(followed, spellings...)
	}
	return followed, nil
}

// danglingTarget returns the target of path when path is a symlink, and ""
// otherwise.
func danglingTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return "", nil
	}
	return linkTarget(path)
}

// linkTarget returns the absolute, clean path that the symlink link points
// to, with a relative target joined to the canonical path of the directory
// that holds the link, so that every spelling of link gives one target. It
// refuses a target that has ".." after another name (errUnresolvableLink).
func linkTarget(link string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if hasParentAfterName(target) {
		return "", fmt.Errorf("%w: %s -> %s", ErrUnresolvableLink, link, target)
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	dir, err := realpath.Resolve(filepath.Dir(link))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, target), nil
}

// hasParentAfterName reports whether target has a ".." segment after a
// segment that is neither "." nor "..". A leading ".." is resolved against
// the directory of the symlink, which is canonical, so a lexical join is
// right for it.
func hasParentAfterName(target string) bool {
	segments := slices.DeleteFunc(strings.Split(target, string(filepath.Separator)), func(segment string) bool {
		return segment == "" || segment == "."
	})
	leading := 0
	for leading < len(segments) && segments[leading] == ".." {
		leading++
	}
	return slices.Contains(segments[leading:], "..")
}

// linkedSpellings returns what the symlinks in the protected directories
// lead to, and in every directory that one of them leads to: every spelling
// of each directory, of the directory that holds each file, and of each
// directory that holds a symlink on the way (linkSpellings). A symlink
// that leads nowhere protects the path where its target would be created,
// as a directory. It also returns every regular file with more than one
// hard link that it finds in those directories or at the end of a chain,
// with each canonical path under which it finds the file. It reads only
// metadata and never opens a file (D08). A protected path that does not
// exist has nothing in it.
func linkedSpellings(protected []string) ([]string, []string, foundFiles, error) {
	var dirs, fileDirs []string
	files := foundFiles{}
	walked := map[string]bool{}
	for queue := slices.Clone(protected); len(queue) > 0; queue = queue[1:] {
		root, err := realpath.Resolve(queue[0])
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		if walked[root] {
			continue
		}
		walked[root] = true
		linkedDirs, linkedFileDirs, err := linksIn(root, files)
		if err != nil {
			return nil, nil, nil, err
		}
		dirs = append(dirs, linkedDirs...)
		fileDirs = append(fileDirs, linkedFileDirs...)
		queue = append(queue, linkedDirs...)
	}
	return dirs, fileDirs, files, nil
}

// linksIn returns what the symlinks in the canonical directory root and
// below it lead to, as linkSpellings returns it, and adds the regular files
// in it and at the end of each chain to files. It does not follow a symlink
// to walk it.
func linksIn(root string, files foundFiles) ([]string, []string, error) {
	var dirs, fileDirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		linkedDirs, linkedFileDirs, err := visitEntry(path, entry.Type(), files)
		dirs = append(dirs, linkedDirs...)
		fileDirs = append(fileDirs, linkedFileDirs...)
		return err
	})
	return dirs, fileDirs, err
}

// visitEntry adds the regular file path to files, or returns what the chain
// of the symlink path leads to and adds the regular file where it ends.
func visitEntry(path string, mode fs.FileMode, files foundFiles) ([]string, []string, error) {
	switch {
	case mode.IsRegular():
		return nil, nil, files.addFile(path, false)
	case mode&fs.ModeSymlink != 0:
		return followLink(path, files)
	}
	return nil, nil, nil
}

// followLink returns what linkSpellings returns for the symlink link, and
// adds the regular file where its chain ends to files.
func followLink(link string, files foundFiles) ([]string, []string, error) {
	walk, missing, err := walkChain(link)
	if err != nil {
		return nil, nil, err
	}
	if missing {
		return walk.missingSpellings()
	}
	if err := files.addChainEnd(walk.resolved); err != nil {
		return nil, nil, err
	}
	return walk.endSpellings()
}

// linkSpellings returns what the chain of the symlink link leads to, as the
// kernel resolves it one name at a time. The first list has every spelling of
// the directory that the chain ends at, or of where its target would be
// created, as a directory, when it leads nowhere. The second has every
// spelling of each directory that holds a symlink on the chain, every hop
// included, and of the directory that holds the file that the chain ends at.
// The agent could replace a hop in a mounted directory, so each of them is
// protected as the holder of a protected file.
func linkSpellings(link string) ([]string, []string, error) {
	walk, missing, err := walkChain(link)
	if err != nil {
		return nil, nil, err
	}
	if missing {
		return walk.missingSpellings()
	}
	return walk.endSpellings()
}

// walkChain resolves the chain of the symlink link one name at a time, as
// the kernel does, and reports whether it leads nowhere.
func walkChain(link string) (*chainWalk, bool, error) {
	walk := &chainWalk{resolved: string(filepath.Separator), rest: pathNames(link)}
	for len(walk.rest) > 0 {
		missing, err := walk.step()
		if err != nil || missing {
			return walk, missing, err
		}
	}
	return walk, false, nil
}

// chainWalk resolves a path one name at a time, as the kernel does, and keeps
// every spelling of each directory that holds a symlink on the way.
type chainWalk struct {
	resolved string
	rest     []string
	hops     int
	holders  []string
}

// step resolves the next name of the path. It reports whether that name does
// not exist.
func (w *chainWalk) step() (bool, error) {
	next := filepath.Join(w.resolved, w.rest[0])
	info, err := os.Lstat(next)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, w.follow(next)
	}
	w.resolved, w.rest = next, w.rest[1:]
	return false, nil
}

// follow replaces the symlink link, which is the next name of the path, with
// its target, and keeps the directory that holds link. It follows at most
// maxLinkHops symlinks.
func (w *chainWalk) follow(link string) error {
	if w.hops == maxLinkHops {
		return fmt.Errorf("%w: %s", syscall.ELOOP, link)
	}
	target, err := linkTarget(link)
	if err != nil {
		return err
	}
	holders, err := dirSpellings(w.resolved)
	if err != nil {
		return err
	}
	w.holders = append(w.holders, holders...)
	w.resolved, w.rest, w.hops = string(filepath.Separator), slices.Concat(pathNames(target), w.rest[1:]), w.hops+1
	return nil
}

// missingSpellings returns every spelling of where the target of a path that
// leads nowhere would be created, as a directory, and the holders.
func (w *chainWalk) missingSpellings() ([]string, []string, error) {
	spellings, err := dirSpellings(w.resolved)
	if err != nil {
		return nil, nil, err
	}
	return joinAll(spellings, filepath.Join(w.rest...)), w.holders, nil
}

// endSpellings returns every spelling of the directory that the path ends at,
// and the holders, with the directory that holds the file that the path ends
// at.
func (w *chainWalk) endSpellings() ([]string, []string, error) {
	info, err := os.Lstat(w.resolved)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		dirs, err := dirSpellings(w.resolved)
		return dirs, w.holders, err
	}
	fileDirs, err := dirSpellings(filepath.Dir(w.resolved))
	return nil, slices.Concat(w.holders, fileDirs), err
}

// pathNames returns the names in the absolute, clean path.
func pathNames(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == filepath.Separator })
}

// protectedPaths returns the protected paths of every home in homes.
func protectedPaths(homes []string) []string {
	var paths []string
	for _, home := range homes {
		paths = append(paths, sandbox.ProtectedDirs(home)...)
	}
	return paths
}

// protectedFilePaths returns the protected file paths of every home in homes.
func protectedFilePaths(homes []string) []string {
	var paths []string
	for _, home := range homes {
		paths = append(paths, sandbox.ProtectedFiles(home)...)
	}
	return paths
}

// dirSpellings returns the canonical path of the existing directory path and
// its path on its own volume.
func dirSpellings(path string) ([]string, error) {
	canonical, err := realpath.Resolve(path)
	if err != nil {
		return nil, err
	}
	noFirmlink, err := realpath.ResolveNoFirmlink(path)
	if err != nil {
		return nil, err
	}
	return []string{canonical, noFirmlink}, nil
}

// distinct returns the paths without duplicates, sorted.
func distinct(paths []string) []string {
	slices.Sort(paths)
	return slices.Compact(paths)
}
