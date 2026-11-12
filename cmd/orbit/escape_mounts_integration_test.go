//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// credentialForm is one way to point init at a credential location.
type credentialForm struct {
	name, path string
}

// credentialFixtures creates the credential locations in the fake home and
// returns the 28 forms of E17.
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
			credentialForm{location, path},
			credentialForm{location + " child", child},
			credentialForm{location + " ancestor", ancestor},
			credentialForm{location + " symlink", toLocation},
			credentialForm{location + " child symlink", toChild},
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
		credentialForm{"claude.json directory", dotfiles},
		credentialForm{"claude.json ancestor", home},
		credentialForm{"claude.json directory symlink", toDotfiles},
	)
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
	forms := credentialFixtures(t, home)
	if len(forms) != 28 {
		t.Fatalf("%d forms, want 28", len(forms))
	}
	var counts tally
	t.Cleanup(func() { t.Log(counts.summary()) })
	rig := escapeRig{client: client, tally: &counts}
	for _, form := range forms {
		t.Run("E17-"+strings.NewReplacer("/", "-", " ", "-").Replace(form.name), func(t *testing.T) {
			t.Logf("E17 proves R08 D08: init on %s", form.path)
			result := attemptResult{id: "E17 " + form.name}
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
