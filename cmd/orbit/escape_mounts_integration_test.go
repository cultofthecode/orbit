//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/cultofthecode/orbit/internal/orbstack"
	"github.com/cultofthecode/orbit/internal/sandbox"
	"github.com/cultofthecode/orbit/internal/sandbox/initialize"
)

// swappingMachines swaps the validated project directory for a relative
// symlink to a sibling directory inside Create, after init validated the
// directory and before OrbStack mounts it (#29).
type swappingMachines struct {
	initialize.Machines
	swap func() error
}

// Create swaps the directory and then creates the machine.
func (m swappingMachines) Create(ctx context.Context, name, dir string, forwardSSHAgent bool) error {
	if err := m.swap(); err != nil {
		return err
	}
	return m.Machines.Create(ctx, name, dir, forwardSSHAgent)
}

// TestEscapeSymlinkSwap runs E16 under the exclusive init lock: while the
// swapped machine exists, init refuses every directory, so the test holds
// the lock until the machine is gone and never holds the shared lock.
func TestEscapeSymlinkSwap(t *testing.T) {
	lockInit(t, syscall.LOCK_EX)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	base := projectDir(t)
	checked := filepath.Join(base, "project", "sub")
	secret := filepath.Join(base, "secret")
	for _, dir := range []string{filepath.Dir(checked), checked, secret} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := sandbox.MachineName(checked)
	t.Cleanup(func() {
		if exists, err := client.Exists(context.WithoutCancel(ctx), name); err == nil && exists {
			_ = client.Delete(context.WithoutCancel(ctx), name)
		}
	})
	swapped := false
	machines := swappingMachines{Machines: client, swap: func() error {
		swapped = true
		if err := os.Remove(checked); err != nil {
			return err
		}
		return os.Symlink(filepath.Join("..", "secret"), checked)
	}}

	_, err := initHandler(machines, claudeRegistry(t)).Handle(ctx, initialize.Input{Dir: checked})

	rig := escapeRig{client: client, name: name, tally: &counts}
	t.Run("E16-symlink-swap", func(t *testing.T) {
		t.Log("E16-symlink-swap proves R08 (#29)")
		result := attemptResult{id: "E16-symlink-swap", hasControl: true}
		switch {
		case !swapped:
			result.controlFailed, result.controlOutput = true, "init refused before it created the machine, so the swap was not exercised: "+errString(err)
		case err == nil:
			result.escaped, result.evidence = true, "init accepted a machine that mounts "+secret+" for "+checked
		case !errors.Is(err, sandbox.ErrMountMismatch):
			result.controlFailed, result.controlOutput = true, "init failed with "+err.Error()+", want "+sandbox.ErrMountMismatch.Error()
		case !strings.Contains(err.Error(), secret):
			result.controlFailed, result.controlOutput = true, "the refusal does not name the target of the symlink "+secret+": "+err.Error()
		}
		if exists, existsErr := client.Exists(ctx, name); existsErr != nil || exists {
			result.escaped, result.evidence = true, result.evidence+" a machine for the validated path is left: "+errString(existsErr)
		}
		mounts, mountsErr := client.Mounts(ctx)
		if mountsErr != nil {
			t.Fatalf("Mounts: %v", mountsErr)
		}
		for machine, paths := range mounts {
			for path, host := range paths {
				if strings.Contains(path+host, "secret") && strings.Contains(path+host, base) {
					result.escaped, result.evidence = true, result.evidence+" machine "+machine+" mounts "+path+" "+host
				}
			}
		}
		rig.report(t, result)
	})
}

// credentialForm is one way to point init at a credential location. home,
// when it is set, is the HOME of the run instead of the fake home.
type credentialForm struct {
	name, path, home string
}

// credentialFixtures creates the credential locations in the fake home and
// returns the 28 forms of E17 that use it.
func credentialFixtures(t *testing.T, home string) []credentialForm {
	t.Helper()
	links := projectDir(t)
	var forms []credentialForm
	for _, location := range []string{".ssh", ".claude", ".aws", ".gnupg", ".config/gh"} {
		path := filepath.Join(home, location)
		child := filepath.Join(path, "child")
		if err := os.MkdirAll(child, 0o700); err != nil {
			t.Fatal(err)
		}
		ancestor := filepath.Dir(path)
		toLocation, toChild := filepath.Join(links, strings.ReplaceAll(location, "/", "-")+"-link"), filepath.Join(links, strings.ReplaceAll(location, "/", "-")+"-child-link")
		for link, target := range map[string]string{toLocation: path, toChild: child} {
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
		}
		forms = append(forms,
			credentialForm{name: location, path: path},
			credentialForm{name: location + " child", path: child},
			credentialForm{name: location + " ancestor", path: ancestor},
			credentialForm{name: location + " symlink", path: toLocation},
			credentialForm{name: location + " child symlink", path: toChild},
		)
	}
	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotfiles, "claude.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("dotfiles", "claude.json"), filepath.Join(home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	toDotfiles := filepath.Join(links, "dotfiles-link")
	if err := os.Symlink(dotfiles, toDotfiles); err != nil {
		t.Fatal(err)
	}
	return append(forms,
		credentialForm{name: "claude.json directory", path: dotfiles},
		credentialForm{name: "claude.json ancestor", path: home},
		credentialForm{name: "claude.json directory symlink", path: toDotfiles},
	)
}

// hopFixtures creates a second fake home whose protected paths reach their
// targets through a symlink in a project directory, the layouts of #36, and
// returns the 6 forms of E17 that init each such project directory: .aws,
// .config, and .claude.json through a hop in their own project, HOME itself
// through a hop, and .gnupg and .ssh/config through a target whose ".."
// cancels a plain name in their own project. The agent could repoint the hop,
// or replace the cancelled name with a symlink, and reach a directory that it
// controls.
func hopFixtures(t *testing.T) []credentialForm {
	t.Helper()
	home := projectDir(t)
	work := filepath.Join(home, "work")
	for _, dir := range []string{"vault/aws", "vault/gnupg", "dotfiles/config/gh", "dotfiles/ssh", "store/state", "work/aws", "work/config", "work/claude", "work/gnupg/x", "work/ssh/x", ".ssh"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join("store", "state", "claude.json"), filepath.Join("dotfiles", "ssh", "config")} {
		if err := os.WriteFile(filepath.Join(home, file), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := projectDir(t)
	homeProject := filepath.Join(outside, "proj")
	if err := os.MkdirAll(homeProject, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, hop := range []struct{ target, link string }{
		{filepath.Join(home, "vault", "aws"), filepath.Join(work, "aws", "link")},
		{filepath.Join(work, "aws", "link"), filepath.Join(home, ".aws")},
		{filepath.Join(home, "dotfiles", "config"), filepath.Join(work, "config", "cfg")},
		{filepath.Join(work, "config", "cfg"), filepath.Join(home, ".config")},
		{filepath.Join(home, "store", "state", "claude.json"), filepath.Join(work, "claude", "c.json")},
		{filepath.Join(work, "claude", "c.json"), filepath.Join(home, ".claude.json")},
		{home, filepath.Join(homeProject, "h")},
		{"work/gnupg/x/../../../vault/gnupg", filepath.Join(home, ".gnupg")},
		{"../work/ssh/x/../../../dotfiles/ssh/config", filepath.Join(home, ".ssh", "config")},
	} {
		if err := os.Symlink(hop.target, hop.link); err != nil {
			t.Fatal(err)
		}
	}
	return []credentialForm{
		{name: ".aws hop", path: filepath.Join(work, "aws"), home: home},
		{name: ".config hop", path: filepath.Join(work, "config"), home: home},
		{name: "claude.json hop", path: filepath.Join(work, "claude"), home: home},
		{name: "HOME hop", path: homeProject, home: filepath.Join(homeProject, "h")},
		{name: ".gnupg cancelled name", path: filepath.Join(work, "gnupg"), home: home},
		{name: ".ssh config cancelled name", path: filepath.Join(work, "ssh"), home: home},
	}
}

// TestEscapeCredentialLocations runs E17: init refuses every form of every
// credential location in a fake HOME, and creates no machine. The test never
// touches the real credential locations of the owner (Q5, D08).
func TestEscapeCredentialLocations(t *testing.T) {
	waitToRunInit(t)
	logOrbStackVersionOnce(t)
	ctx := provisionContext(t)
	client := orbstack.New(sandbox.NamePrefix)
	home, accountHome := temporaryHome(t)
	machines := accountHomeMachines{machines: client, home: accountHome}
	handler := initHandler(machines, claudeRegistry(t))
	forms := slices.Concat(credentialFixtures(t, home), hopFixtures(t))
	if len(forms) != 34 {
		t.Fatalf("%d forms, want 34", len(forms))
	}
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	rig := escapeRig{client: client, tally: &counts}
	for _, form := range forms {
		t.Run("E17-"+strings.NewReplacer("/", "-", " ", "-").Replace(form.name), func(t *testing.T) {
			if form.home != "" {
				t.Setenv("HOME", form.home)
			}
			t.Logf("E17 proves R08 D08: init on %s", form.path)
			result := attemptResult{id: "E17 " + form.name}
			deleteMachinesOfForm(ctx, t, client, form.path)
			out, err := handler.Handle(ctx, initialize.Input{Dir: form.path})
			if !refusedAsACredential(err) {
				result.escaped, result.evidence = true, "init on "+form.path+" = "+errString(err)+" "+out.Name
			}
			resolved, _ := filepath.EvalSymlinks(form.path)
			for _, dir := range []string{form.path, resolved} {
				if exists, err := machines.Exists(ctx, sandbox.MachineName(dir)); err != nil || exists {
					result.escaped, result.evidence = true, result.evidence+" a machine exists for "+dir+" "+errString(err)
				}
			}
			rig.report(t, result)
		})
	}
}

// deleteMachinesOfForm registers the deletion, when the test ends, of every
// machine that init may create for path or its resolved path. It runs even
// when the test reports an escape, so that a failed E17 leaves no machine.
func deleteMachinesOfForm(ctx context.Context, t *testing.T, client *orbstack.Client, path string) {
	t.Helper()
	resolved, _ := filepath.EvalSymlinks(path)
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		for _, dir := range []string{path, resolved} {
			name := sandbox.MachineName(dir)
			if exists, err := client.Exists(cleanupCtx, name); err == nil && exists {
				if err := client.Delete(cleanupCtx, name); err != nil {
					t.Errorf("Delete(%q) = %v", name, err)
				}
			}
		}
	})
}

// refusedAsACredential reports whether err is one of the refusals of a
// credential location.
func refusedAsACredential(err error) bool {
	for _, refusal := range []error{sandbox.ErrProtectedDir, sandbox.ErrProtectedAncestor, sandbox.ErrProtectedFile, sandbox.ErrHomeDir, sandbox.ErrHomeAncestor} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}
